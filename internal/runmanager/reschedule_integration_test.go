//go:build integration

package runmanager

import (
	"context"
	"encoding/json"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/teo-dev/teo/internal/testpg"
)

// seed inserts a repo + run + run_plans + a single shard with 4 tests,
// 2 of which already have completed test_executions (i.e., were "done"
// before preemption). The shard itself starts in 'preempted' status.
type seedIDs struct {
	repoID, runID, shardID, doneTestA, doneTestB string
}

func seed(t *testing.T, pool *pgxpool.Pool) seedIDs {
	t.Helper()
	ids := seedIDs{
		repoID:    uuid.New().String(),
		runID:     uuid.New().String(),
		shardID:   uuid.New().String(),
		doneTestA: uuid.New().String(),
		doneTestB: uuid.New().String(),
	}
	exec(t, pool, `INSERT INTO teo.repos (id, vcs, full_name) VALUES ($1, 'github', 'owner/sample')`, ids.repoID)
	exec(t, pool, `
        INSERT INTO teo.runs (id, repo_id, commit_sha, branch, triggered_by, status, started_at)
        VALUES ($1, $2, 'cafef00d', 'main', 'test', 'running', now())
    `, ids.runID, ids.repoID)

	manifest := map[string]any{
		"runner": "pytest",
		"tests": []map[string]string{
			{"path": "tests/a.py", "name": "test_done_a", "params_hash": ""},
			{"path": "tests/b.py", "name": "test_done_b", "params_hash": ""},
			{"path": "tests/c.py", "name": "test_pending_c", "params_hash": ""},
			{"path": "tests/d.py", "name": "test_pending_d", "params_hash": ""},
		},
	}
	mfBytes, _ := json.Marshal(manifest)
	exec(t, pool, `INSERT INTO teo.run_plans (run_id, plan, plan_version) VALUES ($1, $2, 'manifest-v1')`, ids.runID, mfBytes)

	// Single shard at index 0 of total_shards=1 → claims all 4 tests via round-robin.
	exec(t, pool, `
        INSERT INTO teo.shards (id, run_id, index, status, predicted_duration_ms, test_count, started_at, finished_at, worker_id)
        VALUES ($1, $2, 0, 'preempted', 30000, 4, now() - interval '1 minute', now(), 'spot-worker-1')
    `, ids.shardID, ids.runID)

	// 2 of the 4 tests have completed executions tied to this shard.
	exec(t, pool, `
        INSERT INTO teo.tests (id, repo_id, fingerprint, path, name, runner, status)
        VALUES ($1, $2, 'fp-a', 'tests/a.py', 'test_done_a', 'pytest', 'active')
    `, ids.doneTestA, ids.repoID)
	exec(t, pool, `
        INSERT INTO teo.tests (id, repo_id, fingerprint, path, name, runner, status)
        VALUES ($1, $2, 'fp-b', 'tests/b.py', 'test_done_b', 'pytest', 'active')
    `, ids.doneTestB, ids.repoID)
	exec(t, pool, `
        INSERT INTO teo.test_executions (shard_id, test_id, attempt, outcome, duration_ms, started_at, finished_at)
        VALUES ($1, $2, 1, 'passed', 1000, now(), now())
    `, ids.shardID, ids.doneTestA)
	exec(t, pool, `
        INSERT INTO teo.test_executions (shard_id, test_id, attempt, outcome, duration_ms, started_at, finished_at)
        VALUES ($1, $2, 1, 'passed', 1100, now(), now())
    `, ids.shardID, ids.doneTestB)

	return ids
}

func exec(t *testing.T, pool *pgxpool.Pool, sql string, args ...any) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), sql, args...); err != nil {
		t.Fatalf("seed: %v\nSQL: %s", err, sql)
	}
}

func TestReschedulePreempted_OnlyUncompletedTests(t *testing.T) {
	pool, cleanup := testpg.Start(t)
	t.Cleanup(cleanup)
	ids := seed(t, pool)

	mgr := &Manager{Pool: pool, Logger: slog.Default()}
	mgr.reschedulePreempted(context.Background())

	// A new pending shard should now exist.
	var newShardID string
	var newCount int
	err := pool.QueryRow(context.Background(), `
        SELECT id::text, test_count
        FROM teo.shards
        WHERE run_id = $1 AND status = 'pending'
    `, ids.runID).Scan(&newShardID, &newCount)
	if err != nil {
		t.Fatalf("expected new pending shard: %v", err)
	}
	if newCount != 2 {
		t.Errorf("new shard test_count = %d, want 2 (only the unfinished tests)", newCount)
	}

	// The reshard manifest in runs.meta.reshards must list the 2 unfinished tests.
	var reshardJSON []byte
	if err := pool.QueryRow(context.Background(), `
        SELECT r.meta->'reshards'->$1
        FROM teo.runs r WHERE r.id = $2
    `, newShardID, ids.runID).Scan(&reshardJSON); err != nil {
		t.Fatalf("read reshards: %v", err)
	}
	var mf struct {
		Tests []struct {
			Path string `json:"path"`
			Name string `json:"name"`
		} `json:"tests"`
	}
	if err := json.Unmarshal(reshardJSON, &mf); err != nil {
		t.Fatalf("decode reshard: %v", err)
	}
	if len(mf.Tests) != 2 {
		t.Fatalf("reshard tests = %d, want 2", len(mf.Tests))
	}
	gotNames := map[string]bool{mf.Tests[0].Name: true, mf.Tests[1].Name: true}
	for _, want := range []string{"test_pending_c", "test_pending_d"} {
		if !gotNames[want] {
			t.Errorf("reshard missing %s; got %v", want, mf.Tests)
		}
	}

	// Original shard now has the dedupe marker.
	var marker *time.Time
	if err := pool.QueryRow(context.Background(), `
        SELECT (meta->>'rescheduled_at')::timestamptz FROM teo.shards WHERE id = $1
    `, ids.shardID).Scan(&marker); err != nil {
		t.Fatalf("read marker: %v", err)
	}
	if marker == nil {
		t.Error("rescheduled_at marker not set on original shard")
	}
}

func TestReschedulePreempted_NoOpWhenAllTestsCompleted(t *testing.T) {
	pool, cleanup := testpg.Start(t)
	t.Cleanup(cleanup)

	repoID := uuid.New().String()
	runID := uuid.New().String()
	shardID := uuid.New().String()
	exec(t, pool, `INSERT INTO teo.repos (id, vcs, full_name) VALUES ($1, 'github', 'owner/done')`, repoID)
	exec(t, pool, `
        INSERT INTO teo.runs (id, repo_id, commit_sha, branch, triggered_by, status, started_at)
        VALUES ($1, $2, 'a', 'main', 'test', 'running', now())
    `, runID, repoID)
	mfBytes, _ := json.Marshal(map[string]any{
		"runner": "pytest",
		"tests":  []map[string]string{{"path": "p", "name": "all_done"}},
	})
	exec(t, pool, `INSERT INTO teo.run_plans (run_id, plan, plan_version) VALUES ($1, $2, 'manifest-v1')`, runID, mfBytes)
	exec(t, pool, `
        INSERT INTO teo.shards (id, run_id, index, status, predicted_duration_ms, test_count, started_at, finished_at)
        VALUES ($1, $2, 0, 'preempted', 1000, 1, now(), now())
    `, shardID, runID)
	testID := uuid.New().String()
	exec(t, pool, `
        INSERT INTO teo.tests (id, repo_id, fingerprint, path, name, runner, status)
        VALUES ($1, $2, 'fp', 'p', 'all_done', 'pytest', 'active')
    `, testID, repoID)
	exec(t, pool, `
        INSERT INTO teo.test_executions (shard_id, test_id, attempt, outcome, duration_ms, started_at, finished_at)
        VALUES ($1, $2, 1, 'passed', 100, now(), now())
    `, shardID, testID)

	mgr := &Manager{Pool: pool, Logger: slog.Default()}
	mgr.reschedulePreempted(context.Background())

	// No new pending shard.
	var n int
	if err := pool.QueryRow(context.Background(), `
        SELECT count(*) FROM teo.shards WHERE run_id = $1 AND status = 'pending'
    `, runID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("pending shards = %d, want 0", n)
	}
	// Original shard rescheduled_at should be set; status flipped to succeeded.
	var status string
	var marker *time.Time
	if err := pool.QueryRow(context.Background(), `
        SELECT status, (meta->>'rescheduled_at')::timestamptz FROM teo.shards WHERE id = $1
    `, shardID).Scan(&status, &marker); err != nil {
		t.Fatal(err)
	}
	if status != "succeeded" {
		t.Errorf("status = %s, want succeeded", status)
	}
	if marker == nil {
		t.Error("rescheduled_at marker not set")
	}
}

func TestReschedulePreempted_DedupeOnSecondSweep(t *testing.T) {
	pool, cleanup := testpg.Start(t)
	t.Cleanup(cleanup)
	ids := seed(t, pool)

	mgr := &Manager{Pool: pool, Logger: slog.Default()}
	mgr.reschedulePreempted(context.Background())
	mgr.reschedulePreempted(context.Background()) // second sweep

	// Exactly one new pending shard, not two.
	var n int
	if err := pool.QueryRow(context.Background(), `
        SELECT count(*) FROM teo.shards WHERE run_id = $1 AND status = 'pending'
    `, ids.runID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("pending shards = %d, want 1 (sweep should be idempotent)", n)
	}
}
