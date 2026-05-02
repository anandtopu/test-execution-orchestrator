//go:build integration

package api

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/teo-dev/teo/internal/cost"
	"github.com/teo-dev/teo/internal/testpg"
)

// seed inserts a small fixture: one repo with two runs (one finished+failed,
// one running), plus shards, tests, executions, a flake record, and a failure
// cluster. Returns the repo and run IDs that the tests then assert against.
type seedIDs struct {
	repoID, run1ID, run2ID, shard1ID, shard2ID, testID, clusterID string
}

func seed(t *testing.T, pool *pgxpool.Pool) seedIDs {
	t.Helper()
	ids := seedIDs{
		repoID:    uuid.New().String(),
		run1ID:    uuid.New().String(),
		run2ID:    uuid.New().String(),
		shard1ID:  uuid.New().String(),
		shard2ID:  uuid.New().String(),
		testID:    uuid.New().String(),
		clusterID: uuid.New().String(),
	}
	mustExec(t, pool, `INSERT INTO teo.repos (id, vcs, full_name) VALUES ($1, 'github', 'owner/sample')`, ids.repoID)

	// run1 — completed and failed
	mustExec(t, pool, `
        INSERT INTO teo.runs (id, repo_id, commit_sha, branch, triggered_by, status,
                              started_at, finished_at, total_duration_ms, preemption_count)
        VALUES ($1, $2, 'deadbeef', 'main', 'test', 'failed',
                now() - interval '5 minutes', now() - interval '4 minutes', 60000, 1)
    `, ids.run1ID, ids.repoID)

	// run2 — currently running
	mustExec(t, pool, `
        INSERT INTO teo.runs (id, repo_id, commit_sha, branch, triggered_by, status, started_at)
        VALUES ($1, $2, 'cafebabe', 'main', 'test', 'running', now() - interval '1 minute')
    `, ids.run2ID, ids.repoID)

	// One shard per run
	mustExec(t, pool, `
        INSERT INTO teo.shards (id, run_id, index, status, predicted_duration_ms, actual_duration_ms,
                                test_count, started_at, finished_at, worker_id)
        VALUES ($1, $2, 0, 'failed', 30000, 31000, 5, now() - interval '5 minutes', now() - interval '4 minutes', 'worker-A')
    `, ids.shard1ID, ids.run1ID)
	mustExec(t, pool, `
        INSERT INTO teo.shards (id, run_id, index, status, predicted_duration_ms,
                                test_count, started_at, worker_id)
        VALUES ($1, $2, 0, 'running', 20000, 4, now() - interval '1 minute', 'worker-B')
    `, ids.shard2ID, ids.run2ID)

	// One test row + one failure cluster + one failed test_execution attached
	mustExec(t, pool, `
        INSERT INTO teo.tests (id, repo_id, fingerprint, path, name, runner, status)
        VALUES ($1, $2, 'fp-1', 'tests/test_x.py', 'test_x', 'pytest', 'active')
    `, ids.testID, ids.repoID)
	mustExec(t, pool, `
        INSERT INTO teo.failure_clusters (id, repo_id, stack_fingerprint, representative_message, representative_stack)
        VALUES ($1, $2, 'sf-1', 'AssertionError: boom', 'File a.py in test_x\nAssertionError')
    `, ids.clusterID, ids.repoID)
	mustExec(t, pool, `
        INSERT INTO teo.test_executions (shard_id, test_id, attempt, outcome, duration_ms,
                                         failure_cluster_id, started_at, finished_at)
        VALUES ($1, $2, 1, 'failed', 1500, $3,
                now() - interval '5 minutes', now() - interval '5 minutes' + interval '1.5 seconds')
    `, ids.shard1ID, ids.testID, ids.clusterID)

	// flake_record so queryFlakes returns something
	mustExec(t, pool, `
        INSERT INTO teo.flake_records (test_id, flake_rate, wilson_lower, wilson_upper, sample_size)
        VALUES ($1, 0.20, 0.10, 0.32, 25)
    `, ids.testID)

	return ids
}

func mustExec(t *testing.T, pool *pgxpool.Pool, sql string, args ...any) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), sql, args...); err != nil {
		t.Fatalf("seed: %v\nSQL: %s", err, sql)
	}
}

func TestQueryRunsReturnsRecentInOrder(t *testing.T) {
	pool, cleanup := testpg.Start(t)
	t.Cleanup(cleanup)
	ids := seed(t, pool)

	got, err := queryRuns(context.Background(), pool, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d runs, want 2", len(got))
	}
	// Most recent (run2, running) is created last and should sort first.
	if got[0]["id"] != ids.run2ID {
		t.Errorf("expected run2 first; got %v", got[0]["id"])
	}
	if got[0]["status"] != "running" {
		t.Errorf("status = %v", got[0]["status"])
	}
	if got[1]["id"] != ids.run1ID {
		t.Errorf("expected run1 second; got %v", got[1]["id"])
	}
}

func TestQueryRunByIDIncludesRepoAndDuration(t *testing.T) {
	pool, cleanup := testpg.Start(t)
	t.Cleanup(cleanup)
	ids := seed(t, pool)

	got, err := queryRunByID(context.Background(), pool, ids.run1ID)
	if err != nil {
		t.Fatal(err)
	}
	if got["repo_full_name"] != "owner/sample" {
		t.Errorf("repo = %v", got["repo_full_name"])
	}
	if got["status"] != "failed" {
		t.Errorf("status = %v", got["status"])
	}
	if got["total_duration_ms"].(int32) != 60000 {
		t.Errorf("duration = %v", got["total_duration_ms"])
	}
	if got["preemption_count"].(int32) != 1 {
		t.Errorf("preemption_count = %v", got["preemption_count"])
	}
}

func TestQueryRunByIDNotFoundReturnsErr(t *testing.T) {
	pool, cleanup := testpg.Start(t)
	t.Cleanup(cleanup)
	_, err := queryRunByID(context.Background(), pool, uuid.New().String())
	if err == nil {
		t.Fatal("expected error for missing run")
	}
}

func TestQueryShardsOrderedByIndex(t *testing.T) {
	pool, cleanup := testpg.Start(t)
	t.Cleanup(cleanup)
	ids := seed(t, pool)
	// Add a second shard to run1 so we can assert ordering.
	mustExec(t, pool, `
        INSERT INTO teo.shards (id, run_id, index, status, predicted_duration_ms, test_count, worker_id)
        VALUES ($1, $2, 1, 'pending', 12000, 3, '')
    `, uuid.New().String(), ids.run1ID)

	got, err := queryShards(context.Background(), pool, ids.run1ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d shards, want 2", len(got))
	}
	if got[0]["index"].(int32) != 0 || got[1]["index"].(int32) != 1 {
		t.Errorf("shards out of order: %v", got)
	}
	if got[0]["worker_id"] != "worker-A" {
		t.Errorf("worker_id = %v", got[0]["worker_id"])
	}
}

func TestQueryFailedTestCount(t *testing.T) {
	pool, cleanup := testpg.Start(t)
	t.Cleanup(cleanup)
	ids := seed(t, pool)

	n, err := queryFailedTestCount(context.Background(), pool, ids.run1ID)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("failedTestCount = %d, want 1", n)
	}
	n2, _ := queryFailedTestCount(context.Background(), pool, ids.run2ID)
	if n2 != 0 {
		t.Errorf("failedTestCount(run2) = %d, want 0", n2)
	}
}

func TestQueryFlakesReturnsActiveFlakes(t *testing.T) {
	pool, cleanup := testpg.Start(t)
	t.Cleanup(cleanup)
	ids := seed(t, pool)

	got, err := queryFlakes(context.Background(), pool)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d flakes, want 1", len(got))
	}
	if got[0]["test_id"] != ids.testID {
		t.Errorf("test_id = %v", got[0]["test_id"])
	}
	if got[0]["path"] != "tests/test_x.py" {
		t.Errorf("path = %v", got[0]["path"])
	}
}

func TestQueryCostSummary_AggregatesByWeek(t *testing.T) {
	pool, cleanup := testpg.Start(t)
	t.Cleanup(cleanup)
	ids := seed(t, pool)

	// Two runs in the seed are stale ('5 minutes ago' / '1 minute ago') and
	// land in the current week. The seed also inserts on_demand_minutes via
	// schema default 0; we patch in concrete usage so the aggregate has
	// numbers to report.
	mustExec(t, pool, `UPDATE teo.runs SET spot_minutes = 30, on_demand_minutes = 10 WHERE id = $1`, ids.run1ID)
	mustExec(t, pool, `UPDATE teo.runs SET spot_minutes = 60, on_demand_minutes = 0  WHERE id = $1`, ids.run2ID)

	got, err := queryCostSummary(context.Background(), pool, cost.Pricer{
		SpotPerMin: 0.01, OnDemandPerMin: 0.05,
	}, 8)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d weeks, want 1", len(got))
	}
	row := got[0]
	if got, want := row["runs"].(int), 2; got != want {
		t.Errorf("runs = %v, want %v", got, want)
	}
	// 30+60 = 90 spot · 10+0 = 10 on-demand · cost = 90*0.01 + 10*0.05 = 1.40
	if got := row["total_cost"].(float64); got < 1.39 || got > 1.41 {
		t.Errorf("total_cost = %v, want ~1.40", got)
	}
	if got := row["cost_per_build"].(float64); got < 0.69 || got > 0.71 {
		t.Errorf("cost_per_build = %v, want ~0.70", got)
	}
	// Spot share = 90 / (90+10) = 0.90
	if got := row["spot_share"].(float64); got < 0.89 || got > 0.91 {
		t.Errorf("spot_share = %v, want ~0.90", got)
	}
}

func TestQueryCostSummary_ExcludesRunsWithoutStartedAt(t *testing.T) {
	pool, cleanup := testpg.Start(t)
	t.Cleanup(cleanup)
	repoID := uuid.New().String()
	mustExec(t, pool, `INSERT INTO teo.repos (id, vcs, full_name) VALUES ($1, 'github', 'owner/cost')`, repoID)

	// One pending run with NULL started_at — must be ignored even if it has
	// minutes already attributed.
	mustExec(t, pool, `
        INSERT INTO teo.runs (id, repo_id, commit_sha, branch, triggered_by, status,
                              spot_minutes, on_demand_minutes)
        VALUES (gen_random_uuid(), $1, 'abc', 'main', 'test', 'pending', 5, 5)
    `, repoID)

	got, err := queryCostSummary(context.Background(), pool, cost.Pricer{
		SpotPerMin: 0.01, OnDemandPerMin: 0.05,
	}, 8)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("expected 0 weeks for pending-only data; got %d", len(got))
	}
}

func TestQueryFailureClustersReturnsRecent(t *testing.T) {
	pool, cleanup := testpg.Start(t)
	t.Cleanup(cleanup)
	ids := seed(t, pool)

	got, err := queryFailureClusters(context.Background(), pool)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d clusters, want 1", len(got))
	}
	if got[0]["id"] != ids.clusterID {
		t.Errorf("id = %v", got[0]["id"])
	}
	if got[0]["representative_message"] != "AssertionError: boom" {
		t.Errorf("message = %v", got[0]["representative_message"])
	}
}

func TestRerunFailedCreatesChildRun(t *testing.T) {
	pool, cleanup := testpg.Start(t)
	t.Cleanup(cleanup)
	ids := seed(t, pool)

	got, err := rerunFailed(context.Background(), pool, ids.run1ID)
	if err != nil {
		t.Fatal(err)
	}
	if got["id"] == ids.run1ID {
		t.Fatal("rerun should create a NEW run, not reuse the parent's id")
	}
	if got["status"] != "pending" {
		t.Errorf("new run status = %v, want pending", got["status"])
	}
	if got["commit_sha"] != "deadbeef" {
		t.Errorf("commit not propagated; got %v", got["commit_sha"])
	}

	// Parent_run_id should reference the original
	var parent *string
	if err := pool.QueryRow(context.Background(),
		`SELECT parent_run_id::text FROM teo.runs WHERE id = $1`, got["id"]).Scan(&parent); err != nil {
		t.Fatal(err)
	}
	if parent == nil || *parent != ids.run1ID {
		t.Errorf("parent_run_id = %v, want %v", parent, ids.run1ID)
	}

	// run_plans entry should contain the failed test
	var planRaw []byte
	if err := pool.QueryRow(context.Background(),
		`SELECT plan FROM teo.run_plans WHERE run_id = $1`, got["id"]).Scan(&planRaw); err != nil {
		t.Fatal(err)
	}
	if len(planRaw) == 0 {
		t.Error("run_plans row missing for child run")
	}
}

func TestRerunFailedRefusesWhenNoFailures(t *testing.T) {
	pool, cleanup := testpg.Start(t)
	t.Cleanup(cleanup)
	ids := seed(t, pool)

	// run2 is "running" and has no failed executions.
	_, err := rerunFailed(context.Background(), pool, ids.run2ID)
	if err == nil {
		t.Fatal("expected error when parent has no failures")
	}
}
