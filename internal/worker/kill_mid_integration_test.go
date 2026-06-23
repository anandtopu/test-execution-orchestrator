//go:build integration

package worker

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/teo-dev/teo/internal/logstore"
	"github.com/teo-dev/teo/internal/model"
	"github.com/teo-dev/teo/internal/redact"
	"github.com/teo-dev/teo/internal/testpg"
	"github.com/teo-dev/teo/pkg/adapter"
)

// blockingAdapter emits a fixed set of results, signals that it has done so,
// then (optionally) blocks until ctx is cancelled — modelling a worker that is
// killed (SIGTERM → ctx cancel) while a later test is still running.
type blockingAdapter struct {
	name     string
	results  []adapter.Result
	recorded chan struct{} // receives once all `results` have been emitted
	block    bool          // block on ctx.Done() after emitting, like a long test
}

func (b *blockingAdapter) Name() string { return b.name }

func (b *blockingAdapter) Discover(context.Context, string) ([]model.TestEntry, error) {
	return nil, nil
}

func (b *blockingAdapter) Execute(ctx context.Context, _ string, _ []model.TestEntry, _ adapter.ExecOptions, onResult adapter.ResultHandler) error {
	for _, r := range b.results {
		onResult(r) // synchronous: recordResult has persisted the row when this returns
	}
	if b.recorded != nil {
		select {
		case b.recorded <- struct{}{}:
		default:
		}
	}
	if b.block {
		<-ctx.Done() // the "current test" is killed mid-flight
		return ctx.Err()
	}
	return nil
}

func seedWorkerShard(t *testing.T, pool *pgxpool.Pool, tests []model.TestEntry) (repoID, runID, shardID string) {
	t.Helper()
	repoID = uuid.New().String()
	runID = uuid.New().String()
	shardID = uuid.New().String()
	mustExecW(t, pool, `INSERT INTO teo.repos (id, vcs, full_name) VALUES ($1, 'github', 'owner/worker')`, repoID)
	mustExecW(t, pool, `
        INSERT INTO teo.runs (id, repo_id, commit_sha, branch, triggered_by, status, started_at)
        VALUES ($1, $2, 'deadbeef', 'main', 'test', 'running', now())
    `, runID, repoID)
	planJSON, err := json.Marshal(model.TestManifest{Runner: "pytest", Tests: tests})
	if err != nil {
		t.Fatal(err)
	}
	mustExecW(t, pool, `INSERT INTO teo.run_plans (run_id, plan, plan_version) VALUES ($1, $2::jsonb, 'manifest-v1')`, runID, planJSON)
	mustExecW(t, pool, `
        INSERT INTO teo.shards (id, run_id, index, status, predicted_duration_ms, test_count)
        VALUES ($1, $2, 0, 'pending', 1000, $3)
    `, shardID, runID, len(tests))
	return repoID, runID, shardID
}

func mustExecW(t *testing.T, pool *pgxpool.Pool, sql string, args ...any) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), sql, args...); err != nil {
		t.Fatalf("seed: %v\nSQL: %s", err, sql)
	}
}

func countExecutions(t *testing.T, pool *pgxpool.Pool, shardID string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM teo.test_executions WHERE shard_id = $1`, shardID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// TestWorkerGracefulCancelMidTest (S-06-03 / T-06-03-02): a worker is killed
// (ctx cancelled, as cmd/worker does on SIGTERM) while a test is mid-flight.
// The worker must shut down promptly rather than hang, and work that already
// completed before the kill must survive in Postgres.
func TestWorkerGracefulCancelMidTest(t *testing.T) {
	pool, cleanup := testpg.Start(t)
	t.Cleanup(cleanup)

	_, _, shardID := seedWorkerShard(t, pool, []model.TestEntry{
		{Path: "tests/test_a.py", Name: "test_a"},
		{Path: "tests/test_b.py", Name: "test_b"},
	})

	now := time.Now()
	ad := &blockingAdapter{
		name:     "pytest",
		recorded: make(chan struct{}, 1),
		block:    true,
		results: []adapter.Result{{
			Test:       model.TestEntry{Path: "tests/test_a.py", Name: "test_a"},
			Outcome:    model.OutcomePassed,
			Started:    now.Add(-time.Second),
			Finished:   now,
			DurationMS: 1000,
		}},
	}

	a := &Agent{
		Pool:         pool,
		Adapter:      ad,
		Logger:       slog.Default(),
		WorkerID:     "worker-kill",
		PollInterval: 20 * time.Millisecond,
	}

	ctx, cancel := context.WithCancel(context.Background())
	runErr := make(chan error, 1)
	go func() { runErr <- a.Run(ctx) }()

	// Wait until test_a is recorded — the worker is now blocked "mid-test" on test_b.
	select {
	case <-ad.recorded:
	case <-time.After(10 * time.Second):
		cancel()
		t.Fatal("worker never recorded the first test result")
	}
	if n := countExecutions(t, pool, shardID); n != 1 {
		t.Fatalf("executions before kill = %d, want 1", n)
	}

	// Kill the worker mid-test.
	cancel()
	select {
	case err := <-runErr:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Run returned %v, want context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("worker did not shut down within 5s of cancel — hung mid-test")
	}

	// Completed work survived the kill (at-least-once durability).
	if n := countExecutions(t, pool, shardID); n != 1 {
		t.Fatalf("executions after kill = %d, want 1 (completed work must survive)", n)
	}
}

// TestRecordResultIdempotentOnRetry (S-06-03 / T-06-03-03): a result report
// that is retried — e.g. a killed worker is restarted and re-runs the shard, or
// the ack to a previous report was lost — must not create a duplicate
// test_execution. The (shard, test, attempt) unique constraint + ON CONFLICT
// DO NOTHING guarantees this.
func TestRecordResultIdempotentOnRetry(t *testing.T) {
	pool, cleanup := testpg.Start(t)
	t.Cleanup(cleanup)

	_, runID, shardID := seedWorkerShard(t, pool, []model.TestEntry{{Path: "p", Name: "n"}})

	a := &Agent{
		Pool:     pool,
		Adapter:  &blockingAdapter{name: "pytest"},
		Logger:   slog.Default(),
		WorkerID: "worker-retry",
	}
	a.redactor = redact.New()
	a.Uploader = logstore.Noop()

	now := time.Now()
	res := adapter.Result{
		Test:       model.TestEntry{Path: "tests/test_x.py", Name: "test_x"},
		Outcome:    model.OutcomeFailed,
		Started:    now.Add(-time.Second),
		Finished:   now,
		DurationMS: 1234,
	}

	ctx := context.Background()
	a.recordResult(ctx, runID, shardID, res)
	a.recordResult(ctx, runID, shardID, res) // retry — must be a no-op

	if n := countExecutions(t, pool, shardID); n != 1 {
		t.Fatalf("executions after duplicate report = %d, want 1 (idempotent retry)", n)
	}
}
