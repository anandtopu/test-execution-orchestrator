//go:build integration

package predictor

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/teo-dev/teo/internal/model"
	"github.com/teo-dev/teo/internal/testpg"
)

// TestHeuristicPredictAgainstRealPostgres seeds teo.repos + teo.tests +
// teo.test_executions (via shards/runs) and asserts the always-on Go heuristic —
// the Fallback's safety net — returns NON-cold-start predictions for tests with
// >= 3 recorded attempts, and cold-start for tests below the threshold / absent.
//
// This is the Go-side integration counterpart to the Python TestClient tests:
// it proves loadStats() actually reads the same teo.test_executions history the
// Python serve path reads, so a fallback to the heuristic produces real numbers.
//
// Gated by //go:build integration; requires Docker (testcontainers Postgres).
func TestHeuristicPredictAgainstRealPostgres(t *testing.T) {
	pool, cleanup := testpg.Start(t)
	t.Cleanup(cleanup)
	ctx := context.Background()

	repoID := uuid.New().String()
	runID := uuid.New().String()
	shardID := uuid.New().String()
	hotTestID := uuid.New().String()  // >= 3 attempts → warm
	coldTestID := uuid.New().String() // 1 attempt → still cold

	mustExec(t, pool, `INSERT INTO teo.repos (id, vcs, full_name) VALUES ($1, 'github', 'owner/sample')`, repoID)
	mustExec(t, pool, `
        INSERT INTO teo.runs (id, repo_id, commit_sha, branch, triggered_by, status, started_at)
        VALUES ($1, $2, 'cafef00d', 'main', 'test', 'succeeded', now())
    `, runID, repoID)
	mustExec(t, pool, `
        INSERT INTO teo.shards (id, run_id, index, status, predicted_duration_ms, test_count, started_at, finished_at)
        VALUES ($1, $2, 0, 'succeeded', 5000, 2, now() - interval '1 minute', now())
    `, shardID, runID)

	mustExec(t, pool, `
        INSERT INTO teo.tests (id, repo_id, fingerprint, path, name, runner, status)
        VALUES ($1, $2, 'fp-hot', 'tests/hot.py', 'test_hot', 'pytest', 'active')
    `, hotTestID, repoID)
	mustExec(t, pool, `
        INSERT INTO teo.tests (id, repo_id, fingerprint, path, name, runner, status)
        VALUES ($1, $2, 'fp-cold', 'tests/cold.py', 'test_cold', 'pytest', 'active')
    `, coldTestID, repoID)

	// 4 attempts for the hot test (>= 3 → non-cold-start). Distinct durations so
	// percentile_disc has something to bucket; one failure to exercise fail_rate.
	durations := []int{1000, 1200, 1400, 1600}
	outcomes := []string{"passed", "passed", "failed", "passed"}
	for i := range durations {
		mustExec(t, pool, `
            INSERT INTO teo.test_executions (shard_id, test_id, attempt, outcome, duration_ms, started_at, finished_at)
            VALUES ($1, $2, $3, $4, $5, now() - interval '1 hour', now() - interval '1 hour')
        `, shardID, hotTestID, i+1, outcomes[i], durations[i])
	}

	// 1 attempt for the cold test (< 3 → cold-start).
	mustExec(t, pool, `
        INSERT INTO teo.test_executions (shard_id, test_id, attempt, outcome, duration_ms, started_at, finished_at)
        VALUES ($1, $2, 1, 'passed', 999, now() - interval '1 hour', now() - interval '1 hour')
    `, shardID, coldTestID)

	h := NewHeuristic(pool)
	tests := []model.TestEntry{
		{Path: "tests/hot.py", Name: "test_hot"},
		{Path: "tests/cold.py", Name: "test_cold"},
		{Path: "tests/absent.py", Name: "test_never_seen"}, // no row at all
	}

	preds, err := h.Predict(ctx, "owner/sample", tests)
	require.NoError(t, err)
	require.Len(t, preds, 3, "one prediction per input test, in order")

	// Hot test: >= 3 attempts → NOT cold-start; p50/p95 drawn from history (so
	// they fall within the seeded duration range and are not the cold default).
	hot := preds[0]
	require.Equal(t, "tests/hot.py::test_hot", hot.Fingerprint)
	require.False(t, hot.IsColdStart, "test with >=3 attempts must not be cold-start")
	require.GreaterOrEqual(t, hot.P50DurationMS, 1000)
	require.LessOrEqual(t, hot.P50DurationMS, 1600)
	require.GreaterOrEqual(t, hot.P95DurationMS, hot.P50DurationMS)
	require.InDelta(t, 0.25, hot.FlakeProbability, 0.0001, "1 of 4 attempts failed → fail_rate 0.25")

	// Cold test: < 3 attempts → cold-start defaults.
	cold := preds[1]
	require.Equal(t, "tests/cold.py::test_cold", cold.Fingerprint)
	require.True(t, cold.IsColdStart, "test with <3 attempts must be cold-start")
	require.Equal(t, 1200, cold.P50DurationMS, "cold-start default p50")

	// Absent test: never recorded → cold-start.
	absent := preds[2]
	require.True(t, absent.IsColdStart, "unseen test must be cold-start")
	require.Equal(t, 1200, absent.P50DurationMS)
}

// TestHeuristicUnknownRepoIsColdStart proves an unknown repo degrades to
// cold-start (the pre-onboarding path) rather than erroring.
func TestHeuristicUnknownRepoIsColdStart(t *testing.T) {
	pool, cleanup := testpg.Start(t)
	t.Cleanup(cleanup)

	h := NewHeuristic(pool)
	preds, err := h.Predict(context.Background(), "nobody/unknown", []model.TestEntry{
		{Path: "x.py", Name: "test_x"},
	})
	require.NoError(t, err)
	require.Len(t, preds, 1)
	require.True(t, preds[0].IsColdStart, "unknown repo → cold-start, not an error")
}

func mustExec(t *testing.T, pool *pgxpool.Pool, sql string, args ...any) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, err := pool.Exec(ctx, sql, args...)
	require.NoError(t, err, "seed exec failed:\n%s", sql)
}
