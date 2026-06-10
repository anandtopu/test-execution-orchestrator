//go:build integration

package runmanager

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/teo-dev/teo/internal/model"
	"github.com/teo-dev/teo/internal/predictor"
	"github.com/teo-dev/teo/internal/testpg"
)

// Leader-election integration tests (S-04-02 / T-04-02-03). Two (or more)
// run-manager replicas share a Postgres and must NOT double-process a run.
// Coordination is per-run via pg_try_advisory_xact_lock acquired inside the
// same transaction that advances the run's status (ADR-0013). The decisive
// property is that the lock and the pending->planning UPDATE commit atomically,
// so a competing replica can never observe status='pending' while the lock is
// free: it either sees 'pending' with the lock held (and backs off) or sees the
// advanced status after commit (and routes to dispatch, never re-planning).

// zeroPredictor is a deterministic, DB-free predictor stub so plan() can run
// without seeding test history. One non-zero prediction per test keeps the
// scheduler from special-casing empty input.
type zeroPredictor struct{}

func (zeroPredictor) Predict(_ context.Context, _ string, tests []model.TestEntry) ([]predictor.Prediction, error) {
	out := make([]predictor.Prediction, len(tests))
	for i := range out {
		out[i] = predictor.Prediction{P50DurationMS: 1000}
	}
	return out, nil
}

// seedPendingRun inserts a repo + a pending run + its input manifest (3 tests),
// i.e. exactly what plan() consumes. Returns the run id.
func seedPendingRun(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	repoID := uuid.New().String()
	runID := uuid.New().String()
	exec(t, pool, `INSERT INTO teo.repos (id, vcs, full_name) VALUES ($1, 'github', 'owner/leader')`, repoID)
	exec(t, pool, `
        INSERT INTO teo.runs (id, repo_id, commit_sha, branch, triggered_by, status)
        VALUES ($1, $2, 'deadbeef', 'main', 'test', 'pending')
    `, runID, repoID)
	manifest := map[string]any{
		"runner": "pytest",
		"tests": []map[string]string{
			{"path": "tests/a.py", "name": "test_a", "params_hash": ""},
			{"path": "tests/b.py", "name": "test_b", "params_hash": ""},
			{"path": "tests/c.py", "name": "test_c", "params_hash": ""},
		},
	}
	mf, _ := json.Marshal(manifest)
	exec(t, pool, `INSERT INTO teo.run_plans (run_id, plan, plan_version) VALUES ($1, $2, 'manifest-v1')`, runID, mf)
	return runID
}

func runStatus(t *testing.T, pool *pgxpool.Pool, runID string) string {
	t.Helper()
	var st string
	if err := pool.QueryRow(context.Background(), `SELECT status FROM teo.runs WHERE id = $1`, runID).Scan(&st); err != nil {
		t.Fatalf("read run status: %v", err)
	}
	return st
}

func shardCounts(t *testing.T, pool *pgxpool.Pool, runID string) (total, distinctIndex int) {
	t.Helper()
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*), count(DISTINCT index) FROM teo.shards WHERE run_id = $1`, runID).
		Scan(&total, &distinctIndex); err != nil {
		t.Fatalf("count shards: %v", err)
	}
	return total, distinctIndex
}

// TestLeaderElection_LockHeldBlocksSecondReplica is the deterministic core of
// S-04-02: while replica A holds the per-run advisory lock, replica B must back
// off (process nothing); once A's lease ends, B takes over and plans the run.
func TestLeaderElection_LockHeldBlocksSecondReplica(t *testing.T) {
	pool, cleanup := testpg.Start(t)
	t.Cleanup(cleanup)
	ctx := context.Background()
	runID := seedPendingRun(t, pool)

	// Replica A: hold the per-run advisory lock in an open transaction. This
	// uses the SAME key derivation the Manager uses, so it genuinely contends
	// with tryHandle. We acquire only the lock (no status change) to isolate
	// the lock's effect from the idempotent status guards.
	holdTx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin hold tx: %v", err)
	}
	var got bool
	if err := holdTx.QueryRow(ctx, `SELECT pg_try_advisory_xact_lock($1)`, runIDLockKey(runID)).Scan(&got); err != nil {
		_ = holdTx.Rollback(ctx)
		t.Fatalf("acquire lock: %v", err)
	}
	if !got {
		_ = holdTx.Rollback(ctx)
		t.Fatal("replica A failed to acquire a fresh advisory lock")
	}

	// Replica B (a distinct Manager, drawing its own pooled connection) attempts
	// to handle the same run while A holds the lock. It must be a no-op.
	mgrB := &Manager{Pool: pool, Predictor: zeroPredictor{}, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	mgrB.tryHandle(ctx, runID, model.RunPending)

	if st := runStatus(t, pool, runID); st != "pending" {
		t.Errorf("while lock held, run advanced to %q; want still 'pending' (B should have backed off)", st)
	}
	if total, _ := shardCounts(t, pool, runID); total != 0 {
		t.Errorf("while lock held, %d shards created; want 0 (B should have backed off)", total)
	}

	// Replica A crashes / its lease ends: rolling back releases the xact lock.
	if err := holdTx.Rollback(ctx); err != nil {
		t.Fatalf("release lock: %v", err)
	}

	// Replica B takes over and now plans the run.
	mgrB.tryHandle(ctx, runID, model.RunPending)

	if st := runStatus(t, pool, runID); st != "dispatching" {
		t.Errorf("after takeover, run status = %q; want 'dispatching'", st)
	}
	total, distinctIndex := shardCounts(t, pool, runID)
	if total == 0 {
		t.Error("after takeover, no shards created; want >0")
	}
	if total != distinctIndex {
		t.Errorf("shard index collision: total=%d distinct=%d (a single plan must not duplicate indexes)", total, distinctIndex)
	}
}

// TestLeaderElection_ConcurrentReplicasConvergeWithoutDuplicateShards is the
// chaos test: 4 replicas hammer reconcileOnce against one pending run. The run
// must converge to 'running' exactly once, with a single plan's worth of shards
// and no duplicate (run_id,index) — i.e. no replica double-planned.
func TestLeaderElection_ConcurrentReplicasConvergeWithoutDuplicateShards(t *testing.T) {
	pool, cleanup := testpg.Start(t)
	t.Cleanup(cleanup)
	runID := seedPendingRun(t, pool)

	const replicas = 4
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < replicas; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			mgr := &Manager{Pool: pool, Predictor: zeroPredictor{}, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
			<-start // barrier: maximize contention on the first pass
			// Bounded-deadline poll (repo idiom — no time.Sleep). Each pass
			// re-reads run status from Postgres, exactly like the production
			// reconciliation loop, so a replica that lost the lock routes to the
			// dispatch path on a later pass instead of re-planning.
			for deadline := time.Now().Add(20 * time.Second); time.Now().Before(deadline); {
				mgr.reconcileOnce(context.Background())
				if runStatus(t, pool, runID) == "running" {
					return
				}
			}
		}()
	}
	close(start)
	wg.Wait()

	if st := runStatus(t, pool, runID); st != "running" {
		t.Fatalf("after concurrent reconcile, run status = %q; want 'running'", st)
	}
	total, distinctIndex := shardCounts(t, pool, runID)
	if total == 0 {
		t.Fatal("no shards created under concurrent reconcile")
	}
	if total != distinctIndex {
		t.Errorf("duplicate shards under contention: total=%d distinct=%d (leader election should plan the run exactly once)", total, distinctIndex)
	}
}
