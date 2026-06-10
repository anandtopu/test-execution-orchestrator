//go:build integration

package resultpipeline

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/teo-dev/teo/internal/testpg"
)

// fixedStackSource is a Postgres-only stub StackSource: it returns a fixed Python
// traceback for a known trace id, standing in for CHStackSource so the backfill
// end-to-end path can be exercised without ClickHouse. (The production
// CHStackSource is unit-covered by the fingerprint reuse assertions; here we
// prove the real PGExecSource + *Cluster wiring.)
type fixedStackSource struct {
	byTrace map[string]string // trace_id -> stack
	message string
}

func (s fixedStackSource) StackFor(_ context.Context, traceID string) (string, string, error) {
	return s.byTrace[traceID], s.message, nil
}

// seedFailedExec inserts repo → run → shard → test → a failed test_execution with
// the given outcome, otel_trace_id, and failure_cluster_id, returning the exec id.
// A nil clusterID leaves failure_cluster_id NULL (the backfill's target state).
func seedFailedExec(t *testing.T, pool *pgxpool.Pool, repoID, traceID, outcome string, clusterID *string) string {
	t.Helper()
	ctx := context.Background()

	runID := uuid.New().String()
	_, err := pool.Exec(ctx, `
        INSERT INTO teo.runs (id, repo_id, commit_sha, branch, triggered_by, status, started_at)
        VALUES ($1, $2, 'cafe', 'main', 'api', 'failed', now())
    `, runID, repoID)
	require.NoError(t, err)

	shardID := uuid.New().String()
	_, err = pool.Exec(ctx, `
        INSERT INTO teo.shards (id, run_id, index, predicted_duration_ms, status, test_count)
        VALUES ($1, $2, 0, 1000, 'failed', 1)
    `, shardID, runID)
	require.NoError(t, err)

	testID := uuid.New().String()
	_, err = pool.Exec(ctx, `
        INSERT INTO teo.tests (id, repo_id, fingerprint, path, name)
        VALUES ($1, $2, $3, 'tests/test_a.py', 'test_one')
    `, testID, repoID, uuid.New().String())
	require.NoError(t, err)

	execID := uuid.New().String()
	_, err = pool.Exec(ctx, `
        INSERT INTO teo.test_executions
            (id, shard_id, test_id, outcome, duration_ms, otel_trace_id, failure_cluster_id,
             started_at, finished_at)
        VALUES ($1, $2, $3, $4, 500, $5, $6, now(), now())
    `, execID, shardID, testID, outcome, traceID, clusterID)
	require.NoError(t, err)
	return execID
}

func seedRepo(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	id := uuid.New().String()
	_, err := pool.Exec(context.Background(),
		`INSERT INTO teo.repos (id, vcs, full_name) VALUES ($1, 'github', $2)`,
		id, "owner/backfill-"+id[:8])
	require.NoError(t, err)
	return id
}

const itPythonTraceback = `Traceback (most recent call last):
  File "/app/svc.py", line 42, in handle_request
    self.do_work()
  File "/app/svc.py", line 91, in do_work
    raise AssertionError("boom")
AssertionError: boom`

// TestBackfillClustersEndToEnd exercises the full back-link path against real
// Postgres: a failed test_execution with a trace id and NULL failure_cluster_id
// is fingerprinted (via the stub StackSource's fixed traceback), resolved through
// the real *Cluster.ClusterFor, and back-linked. Asserts exactly one
// failure_clusters row for (repo_id, fingerprint), the exec's failure_cluster_id
// is now non-NULL and equal to that cluster, and a re-run is idempotent (still one
// cluster row, occurrences NOT double-incremented because the IS NULL guard means
// the second pass scans zero pending rows). Run is synchronous — no time.Sleep.
//
// NOT RUN HERE: requires Docker (testpg spins up Postgres via testcontainers),
// which is unavailable on this machine. Written + vetted behind //go:build
// integration.
func TestBackfillClustersEndToEnd(t *testing.T) {
	pool, cleanup := testpg.Start(t)
	t.Cleanup(cleanup)
	ctx := context.Background()

	repoID := seedRepo(t, pool)
	traceID := "trace-e2e-0001"
	execID := seedFailedExec(t, pool, repoID, traceID, "failed", nil)

	b := &Backfiller{
		Execs:   PGExecSource{Pool: pool},
		Stacks:  fixedStackSource{byTrace: map[string]string{traceID: itPythonTraceback}, message: "boom"},
		Cluster: &Cluster{Pool: pool},
	}

	stats, err := b.Run(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, stats.Scanned)
	require.Equal(t, 1, stats.Assigned)
	require.Equal(t, 0, stats.Errors)

	// Exactly one cluster row for (repo_id, fingerprint).
	fp, _ := FingerprintStack(itPythonTraceback)
	var clusterCount int
	var clusterID string
	var occurrences int64
	require.NoError(t, pool.QueryRow(ctx, `
        SELECT count(*) FROM teo.failure_clusters
        WHERE repo_id = $1 AND stack_fingerprint = $2
    `, repoID, fp).Scan(&clusterCount))
	require.Equal(t, 1, clusterCount)
	require.NoError(t, pool.QueryRow(ctx, `
        SELECT id, occurrences FROM teo.failure_clusters
        WHERE repo_id = $1 AND stack_fingerprint = $2
    `, repoID, fp).Scan(&clusterID, &occurrences))

	// The exec is now back-linked to that exact cluster.
	var linked *string
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT failure_cluster_id::text FROM teo.test_executions WHERE id = $1`, execID).Scan(&linked))
	require.NotNil(t, linked, "failure_cluster_id must be non-NULL after backfill")
	require.Equal(t, clusterID, *linked)

	// Re-run: idempotent. The exec now has a non-NULL cluster id, so
	// PendingFailures returns zero rows and nothing is upserted again.
	stats2, err := b.Run(ctx)
	require.NoError(t, err)
	require.Equal(t, 0, stats2.Scanned, "second pass must find zero pending rows (IS NULL guard)")
	require.Equal(t, 0, stats2.Assigned)

	var clusterCount2 int
	var occurrences2 int64
	require.NoError(t, pool.QueryRow(ctx, `
        SELECT count(*), max(occurrences) FROM teo.failure_clusters
        WHERE repo_id = $1 AND stack_fingerprint = $2
    `, repoID, fp).Scan(&clusterCount2, &occurrences2))
	require.Equal(t, 1, clusterCount2, "re-run must not create a second cluster row")
	require.Equal(t, occurrences, occurrences2, "re-run must NOT double-increment occurrences")
}

// TestBackfillIgnoresPassedAndAlreadyAssigned: a 'passed' exec and a 'failed' exec
// that already has a failure_cluster_id must both be invisible to PendingFailures,
// so Run leaves them untouched (neither scanned nor reassigned).
//
// NOT RUN HERE: requires Docker (unavailable). Written + vetted behind
// //go:build integration.
func TestBackfillIgnoresPassedAndAlreadyAssigned(t *testing.T) {
	pool, cleanup := testpg.Start(t)
	t.Cleanup(cleanup)
	ctx := context.Background()

	repoID := seedRepo(t, pool)

	// A 'passed' exec with a trace id but no cluster — must be ignored (outcome).
	passedID := seedFailedExec(t, pool, repoID, "trace-passed", "passed", nil)

	// A 'failed' exec already linked to a pre-existing cluster — must be ignored
	// (failure_cluster_id already set).
	preID := uuid.New().String()
	_, err := pool.Exec(ctx, `
        INSERT INTO teo.failure_clusters
            (id, repo_id, stack_fingerprint, representative_message, representative_stack)
        VALUES ($1, $2, 'preexisting-fp', 'msg', 'stack')
    `, preID, repoID)
	require.NoError(t, err)
	assignedID := seedFailedExec(t, pool, repoID, "trace-assigned", "failed", &preID)

	b := &Backfiller{
		Execs:   PGExecSource{Pool: pool},
		Stacks:  fixedStackSource{byTrace: map[string]string{"trace-passed": itPythonTraceback, "trace-assigned": itPythonTraceback}, message: "boom"},
		Cluster: &Cluster{Pool: pool},
	}

	stats, err := b.Run(ctx)
	require.NoError(t, err)
	require.Equal(t, 0, stats.Scanned, "neither the passed nor the already-assigned exec should be pending")
	require.Equal(t, 0, stats.Assigned)

	// The passed exec is still unlinked.
	var passedLink *string
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT failure_cluster_id::text FROM teo.test_executions WHERE id = $1`, passedID).Scan(&passedLink))
	require.Nil(t, passedLink, "passed exec must remain unlinked")

	// The already-assigned exec still points at its original cluster.
	var assignedLink *string
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT failure_cluster_id::text FROM teo.test_executions WHERE id = $1`, assignedID).Scan(&assignedLink))
	require.NotNil(t, assignedLink)
	require.Equal(t, preID, *assignedLink, "already-assigned exec must keep its original cluster")

	// And only the pre-existing cluster exists (no new one created).
	var totalClusters int
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT count(*) FROM teo.failure_clusters WHERE repo_id = $1`, repoID).Scan(&totalClusters))
	require.Equal(t, 1, totalClusters)
}
