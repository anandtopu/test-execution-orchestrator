//go:build integration

package api

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/teo-dev/teo/internal/cost"
	"github.com/teo-dev/teo/internal/testpg"
)

// seed inserts a small fixture: one repo with two runs (one finished+failed,
// one running), plus shards, tests, executions, a flake record, and a failure
// cluster. Returns the repo and run IDs that the tests then assert against.
type seedIDs struct {
	repoID, run1ID, run2ID, shard1ID, shard2ID, testID, clusterID, cluster2ID string
}

func seed(t *testing.T, pool *pgxpool.Pool) seedIDs {
	t.Helper()
	ids := seedIDs{
		repoID:     uuid.New().String(),
		run1ID:     uuid.New().String(),
		run2ID:     uuid.New().String(),
		shard1ID:   uuid.New().String(),
		shard2ID:   uuid.New().String(),
		testID:     uuid.New().String(),
		clusterID:  uuid.New().String(),
		cluster2ID: uuid.New().String(),
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
        INSERT INTO teo.tests (id, repo_id, fingerprint, path, name, runner, status, owner_team)
        VALUES ($1, $2, 'fp-1', 'tests/test_x.py', 'test_x', 'pytest', 'active', '@teo-dev/platform')
    `, ids.testID, ids.repoID)
	mustExec(t, pool, `
        INSERT INTO teo.failure_clusters (id, repo_id, stack_fingerprint, representative_message, representative_stack, occurrences, first_seen, last_seen)
        VALUES ($1, $2, 'sf-1', 'AssertionError: boom', 'File a.py in test_x\nAssertionError', 3, now() - interval '2 hours', now() - interval '5 minutes')
    `, ids.clusterID, ids.repoID)
	// Second cluster — higher occurrences, more recent, so computeClusterLayout
	// has spread to place rows distinctly (x from last_seen, y/r from occurrences).
	mustExec(t, pool, `
        INSERT INTO teo.failure_clusters (id, repo_id, stack_fingerprint, representative_message, representative_stack, occurrences, first_seen, last_seen)
        VALUES ($1, $2, 'sf-2', 'panic: runtime error: nil map', 'File b.go in TestY\npanic', 40, now() - interval '1 hour', now() - interval '1 minute')
    `, ids.cluster2ID, ids.repoID)
	// Failed execution on run1 attached to cluster-1 → affected_runs >= 1 for sf-1.
	mustExec(t, pool, `
        INSERT INTO teo.test_executions (shard_id, test_id, attempt, outcome, duration_ms,
                                         failure_cluster_id, started_at, finished_at)
        VALUES ($1, $2, 1, 'failed', 1500, $3,
                now() - interval '5 minutes', now() - interval '5 minutes' + interval '1.5 seconds')
    `, ids.shard1ID, ids.testID, ids.clusterID)
	// Dedicated run/shard carrying ~25 executions for the seeded test so the
	// sparkline + duration_mean have history. Kept on its own run so the
	// run1/run2 failed-count assertions stay exact. One of its failed executions
	// is attached to cluster-2 so sf-2 has affected_runs == 1 (a distinct run).
	run3ID := uuid.New().String()
	shard3ID := uuid.New().String()
	// created_at is pinned to the past too (not just started_at): queryRuns
	// orders by created_at DESC, so a default now() here would sort this
	// history run ahead of run2 and break the "first run is the running one"
	// ordering assertions. It stays a real 3rd run in the list/cost counts.
	mustExec(t, pool, `
        INSERT INTO teo.runs (id, repo_id, commit_sha, branch, triggered_by, status, started_at, finished_at, created_at)
        VALUES ($1, $2, 'feedface', 'main', 'test', 'succeeded', now() - interval '2 hours', now() - interval '2 hours' + interval '1 minute', now() - interval '2 hours')
    `, run3ID, ids.repoID)
	mustExec(t, pool, `
        INSERT INTO teo.shards (id, run_id, index, status, predicted_duration_ms, actual_duration_ms, test_count, worker_id)
        VALUES ($1, $2, 0, 'succeeded', 25000, 25000, 25, 'worker-C')
    `, shard3ID, run3ID)
	for i := 0; i < 25; i++ {
		outcome := "passed"
		var clusterArg any
		if i%5 == 0 {
			outcome = "failed"
		}
		// Attach the first failed execution to cluster-2.
		if i == 0 {
			clusterArg = ids.cluster2ID
		}
		mustExec(t, pool, `
            INSERT INTO teo.test_executions (shard_id, test_id, attempt, outcome, duration_ms, failure_cluster_id, started_at, finished_at)
            VALUES ($1, $2, $7, $3, $4, $6,
                    now() - interval '2 hours' + ($5 * interval '1 second'),
                    now() - interval '2 hours' + ($5 * interval '1 second') + interval '1 second')
        `, shard3ID, ids.testID, outcome, 1000+i*10, i, clusterArg, i+1)
	}

	// flake_record so queryFlakes returns something. quarantined_at is set so the
	// quarantinedAt/status round-trip can be asserted (ui-clusters-flakes).
	mustExec(t, pool, `
        INSERT INTO teo.flake_records (test_id, flake_rate, wilson_lower, wilson_upper, sample_size, quarantined_at)
        VALUES ($1, 0.20, 0.10, 0.32, 25, now() - interval '3 days')
    `, ids.testID)
	// Reflect the quarantine on the test row too, so the badge resolves to
	// 'quarantined'.
	mustExec(t, pool, `UPDATE teo.tests SET status = 'quarantined', quarantined_at = now() - interval '3 days' WHERE id = $1`, ids.testID)

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
	// seed inserts run1, run2, and a third sparkline-carrier run (run3), all in
	// the same repo. Ordered by created_at DESC, run3 (inserted last) sorts
	// first; run1 and run2 must both still be present in creation order.
	if len(got) != 3 {
		t.Fatalf("got %d runs, want 3", len(got))
	}
	idx := map[string]int{}
	for i, r := range got {
		idx[r["id"].(string)] = i
	}
	if _, ok := idx[ids.run1ID]; !ok {
		t.Errorf("run1 missing from results")
	}
	if _, ok := idx[ids.run2ID]; !ok {
		t.Errorf("run2 missing from results")
	}
	// run2 was created after run1 → sorts ahead of it.
	if idx[ids.run2ID] >= idx[ids.run1ID] {
		t.Errorf("run2 (idx %d) should sort before run1 (idx %d)", idx[ids.run2ID], idx[ids.run1ID])
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
	// run1, run2 and the run3 sparkline-history run all land in the current
	// week with started_at set; run3 carries zero spot/on-demand minutes so it
	// adds a build to the count without changing total_cost or spot_share.
	if got, want := row["runs"].(int), 3; got != want {
		t.Errorf("runs = %v, want %v", got, want)
	}
	// 30+60 = 90 spot · 10+0 = 10 on-demand · cost = 90*0.01 + 10*0.05 = 1.40
	if got := row["total_cost"].(float64); got < 1.39 || got > 1.41 {
		t.Errorf("total_cost = %v, want ~1.40", got)
	}
	// cost_per_build = 1.40 / 3 builds ≈ 0.467
	if got := row["cost_per_build"].(float64); got < 0.46 || got > 0.47 {
		t.Errorf("cost_per_build = %v, want ~0.467", got)
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
	if len(got) != 2 {
		t.Fatalf("got %d clusters, want 2", len(got))
	}
	// Ordered by last_seen DESC: cluster-2 (1 minute ago) before cluster-1.
	if got[0]["id"] != ids.cluster2ID {
		t.Errorf("first id = %v, want cluster-2 %v", got[0]["id"], ids.cluster2ID)
	}
	if got[1]["id"] != ids.clusterID {
		t.Errorf("second id = %v, want cluster-1 %v", got[1]["id"], ids.clusterID)
	}
	if got[1]["representative_message"] != "AssertionError: boom" {
		t.Errorf("message = %v", got[1]["representative_message"])
	}
}

// TestQueryFailureClustersComputesLayout asserts the spatial-map fields are
// populated server-side: each row carries finite x/y in [0,1] and r in [9,40],
// a non-empty category, the seeded stack_fingerprint, and a correct affected_runs
// derived from the attached test_executions.
func TestQueryFailureClustersComputesLayout(t *testing.T) {
	pool, cleanup := testpg.Start(t)
	t.Cleanup(cleanup)
	seed(t, pool)

	got, err := queryFailureClusters(context.Background(), pool)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d clusters, want 2", len(got))
	}
	byFP := map[string]map[string]any{}
	for _, m := range got {
		x, ok := m["x"].(float64)
		if !ok {
			t.Fatalf("x is not float64: %T", m["x"])
		}
		y, ok := m["y"].(float64)
		if !ok {
			t.Fatalf("y is not float64: %T", m["y"])
		}
		r, ok := m["r"].(float64)
		if !ok {
			t.Fatalf("r is not float64: %T", m["r"])
		}
		if x < 0 || x > 1 {
			t.Errorf("x out of bounds: %v", x)
		}
		if y < 0 || y > 1 {
			t.Errorf("y out of bounds: %v", y)
		}
		if r < 9 || r > 40 {
			t.Errorf("r out of bounds: %v", r)
		}
		if cat, _ := m["category"].(string); cat == "" {
			t.Errorf("category empty for %v", m["stack_fingerprint"])
		}
		fp, _ := m["stack_fingerprint"].(string)
		byFP[fp] = m
	}

	c1 := byFP["sf-1"]
	if c1 == nil {
		t.Fatal("cluster sf-1 missing")
	}
	if c1["stack_fingerprint"] != "sf-1" {
		t.Errorf("stack_fingerprint = %v, want sf-1", c1["stack_fingerprint"])
	}
	// sf-1 has exactly one attached execution on run1.
	if n, _ := toInt64(c1["affected_runs"]); n != 1 {
		t.Errorf("sf-1 affected_runs = %v, want 1", c1["affected_runs"])
	}
	if cat, _ := c1["category"].(string); cat != "assertion" {
		t.Errorf("sf-1 category = %v, want assertion", cat)
	}
	// sf-2 ("panic: ...") classifies as panic and has one attached execution.
	c2 := byFP["sf-2"]
	if c2 == nil {
		t.Fatal("cluster sf-2 missing")
	}
	if n, _ := toInt64(c2["affected_runs"]); n != 1 {
		t.Errorf("sf-2 affected_runs = %v, want 1", c2["affected_runs"])
	}
	if cat, _ := c2["category"].(string); cat != "panic" {
		t.Errorf("sf-2 category = %v, want panic", cat)
	}
}

// TestQueryFlakesIncludesSparklineAndWilsonUpper asserts the Flakes resolver
// hydrates the sparkline + duration mean from the seeded executions and carries
// through the Wilson upper bound from the flake_record.
func TestQueryFlakesIncludesSparklineAndWilsonUpper(t *testing.T) {
	pool, cleanup := testpg.Start(t)
	t.Cleanup(cleanup)
	seed(t, pool)

	got, err := queryFlakes(context.Background(), pool)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d flakes, want 1", len(got))
	}
	row := got[0]

	spark, _ := row["spark"].(string)
	if spark == "" {
		t.Fatal("spark is empty")
	}
	if len(spark) > 20 {
		t.Errorf("spark len = %d, want <= 20", len(spark))
	}
	for _, c := range spark {
		if c != 'P' && c != 'F' && c != 'S' {
			t.Errorf("spark contains unexpected char %q in %q", c, spark)
		}
	}

	wu, _ := row["wilson_upper"].(float64)
	if wu < 0.319 || wu > 0.321 {
		t.Errorf("wilson_upper = %v, want 0.32", row["wilson_upper"])
	}

	dur, _ := toInt64(row["duration_mean_ms"])
	if dur <= 0 {
		t.Errorf("duration_mean_ms = %v, want > 0", row["duration_mean_ms"])
	}
}

// TestQueryFlakesRoundTripsQuarantineAndOwner asserts the ui-clusters-flakes
// additive columns round-trip end-to-end: wilson_upper, the 'quarantined' status
// badge (from the seeded quarantined test), a non-null RFC3339 quarantined_at,
// and the CODEOWNERS owner_team. The wilson_lower>0.05 gate is also re-checked.
func TestQueryFlakesRoundTripsQuarantineAndOwner(t *testing.T) {
	pool, cleanup := testpg.Start(t)
	t.Cleanup(cleanup)
	ids := seed(t, pool)

	// Add a second flake_record below the 0.05 gate; it must be filtered out so
	// the result still contains exactly the one seeded, above-threshold flake.
	belowID := uuid.New().String()
	mustExec(t, pool, `
        INSERT INTO teo.tests (id, repo_id, fingerprint, path, name, runner, status)
        VALUES ($1, $2, 'fp-below', 'tests/test_below.py', 'test_below', 'pytest', 'active')
    `, belowID, ids.repoID)
	mustExec(t, pool, `
        INSERT INTO teo.flake_records (test_id, flake_rate, wilson_lower, wilson_upper, sample_size)
        VALUES ($1, 0.02, 0.01, 0.05, 50)
    `, belowID)

	got, err := queryFlakes(context.Background(), pool)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d flakes, want 1 (wilson_lower>0.05 gate)", len(got))
	}
	row := got[0]

	if row["test_id"] != ids.testID {
		t.Errorf("test_id = %v, want the above-threshold flake %v", row["test_id"], ids.testID)
	}
	if wu, _ := row["wilson_upper"].(float64); wu < 0.319 || wu > 0.321 {
		t.Errorf("wilson_upper = %v, want ~0.32", row["wilson_upper"])
	}
	if row["status"] != "quarantined" {
		t.Errorf("status = %v, want quarantined", row["status"])
	}
	qAt, ok := row["quarantined_at"].(string)
	if !ok || qAt == "" {
		t.Fatalf("quarantined_at not a non-empty string: %#v", row["quarantined_at"])
	}
	if _, err := time.Parse(time.RFC3339, qAt); err != nil {
		t.Errorf("quarantined_at %q not RFC3339: %v", qAt, err)
	}
	if row["owner_team"] != "@teo-dev/platform" {
		t.Errorf("owner_team = %v, want @teo-dev/platform", row["owner_team"])
	}
}

// TestQueryFailureClustersStackIsSingleString locks in that representativeStack
// is resolved as one string (the adapter splits it client-side), not an array.
func TestQueryFailureClustersStackIsSingleString(t *testing.T) {
	pool, cleanup := testpg.Start(t)
	t.Cleanup(cleanup)
	seed(t, pool)

	got, err := queryFailureClusters(context.Background(), pool)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) == 0 {
		t.Fatal("no clusters returned")
	}
	for _, m := range got {
		if _, ok := m["representative_stack"].(string); !ok {
			t.Errorf("representative_stack is %T, want string", m["representative_stack"])
		}
	}
}

// TestQueryRunPredictor covers the run-level calibration aggregate: mae equals
// the mean absolute delta of finished shards, p95 is present, modelVersion falls
// back to 'heuristic' when meta carries none, and a run with <2 finished shards
// returns a nil predictor without error.
func TestQueryRunPredictor(t *testing.T) {
	pool, cleanup := testpg.Start(t)
	t.Cleanup(cleanup)
	ids := seed(t, pool)

	// Give run1 a second finished shard so it has >=2 actuals. shard1 has
	// predicted=30000 actual=31000 (delta 1000). Add predicted=20000
	// actual=23000 (delta 3000). mae = (1000+3000)/2 = 2000.
	mustExec(t, pool, `
        INSERT INTO teo.shards (id, run_id, index, status, predicted_duration_ms, actual_duration_ms, test_count, worker_id)
        VALUES ($1, $2, 1, 'succeeded', 20000, 23000, 3, 'worker-D')
    `, uuid.New().String(), ids.run1ID)

	got, err := queryRunPredictor(context.Background(), pool, ids.run1ID)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil {
		t.Fatal("expected predictor, got nil")
	}
	mae, _ := got["mae"].(float64)
	if mae < 1999 || mae > 2001 {
		t.Errorf("mae = %v, want ~2000", got["mae"])
	}
	if _, ok := got["p95_delta_ms"]; !ok {
		t.Error("p95_delta_ms missing")
	}
	if got["model_version"] != "heuristic" {
		t.Errorf("model_version = %v, want heuristic (fallback)", got["model_version"])
	}

	// run2 has a single 'running' shard with NULL actual_duration_ms → <2
	// finished shards → nil predictor, no error.
	got2, err := queryRunPredictor(context.Background(), pool, ids.run2ID)
	if err != nil {
		t.Fatalf("run2 predictor errored: %v", err)
	}
	if got2 != nil {
		t.Errorf("run2 predictor = %v, want nil (<2 finished shards)", got2)
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
