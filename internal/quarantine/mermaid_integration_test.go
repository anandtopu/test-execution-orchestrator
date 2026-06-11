//go:build integration

package quarantine

import (
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/teo-dev/teo/internal/testpg"
)

// captureOpener is a fake IssueOpener that records the (title, body) it was
// handed and returns a fixed issue number/url, so the e2e sweep can assert on
// the rendered issue body and on issue-number persistence without GitHub.
type captureOpener struct {
	title, body, repo string
	labels            []string
	number            int
	url               string
}

func (o *captureOpener) Open(_ context.Context, repoFullName, title, body string, _, labels []string) (int, string, error) {
	o.repo = repoFullName
	o.title = title
	o.body = body
	o.labels = labels
	return o.number, o.url, nil
}

// NOTE: This test requires Docker (testcontainers spins up a real Postgres via
// internal/testpg). It was NOT executed in the authoring environment because
// Docker is unavailable there; it is written to compile and run under
// `go test -tags=integration ./internal/quarantine/...` where Docker exists.

func execIT(t *testing.T, pool *pgxpool.Pool, sql string, args ...any) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), sql, args...); err != nil {
		t.Fatalf("seed: %v\nSQL: %s", err, sql)
	}
}

// TestRecentOutcomesAndIssueBodyE2E seeds a repo/run/shards/test with a realistic
// execution history — including a retried attempt within one shard and a skipped
// run — then asserts that recentOutcomes collapses retries to one bar per run,
// preserves oldest->newest order, and that buildIssueBody embeds a renderable
// Mermaid block reflecting that history.
func TestRecentOutcomesAndIssueBodyE2E(t *testing.T) {
	pool, cleanup := testpg.Start(t)
	t.Cleanup(cleanup)
	ctx := context.Background()

	repoID := uuid.New().String()
	runID := uuid.New().String()
	testID := uuid.New().String()

	execIT(t, pool, `INSERT INTO teo.repos (id, vcs, full_name) VALUES ($1, 'github', 'owner/sample')`, repoID)
	execIT(t, pool, `
        INSERT INTO teo.runs (id, repo_id, commit_sha, branch, triggered_by, status, started_at)
        VALUES ($1, $2, 'cafef00d', 'main', 'test', 'running', now())
    `, runID, repoID)
	execIT(t, pool, `
        INSERT INTO teo.tests (id, repo_id, fingerprint, path, name, runner, status)
        VALUES ($1, $2, 'fp-x', 'tests/x.py', 'test_flaky', 'pytest', 'active')
    `, testID, repoID)

	// Four shards (= four logical runs), oldest -> newest by started_at.
	// Shard 1 (oldest): passed
	// Shard 2: failed on attempt 1, passed on attempt 2 (retry) -> final = passed
	// Shard 3: skipped (neutral -> filtered, but counted in caption)
	// Shard 4 (newest): failed
	base := time.Now().Add(-1 * time.Hour)
	shards := []struct {
		offset  time.Duration
		entries []struct {
			attempt int
			outcome string
		}
	}{
		{0 * time.Minute, []struct {
			attempt int
			outcome string
		}{{1, "passed"}}},
		{10 * time.Minute, []struct {
			attempt int
			outcome string
		}{{1, "failed"}, {2, "passed"}}},
		{20 * time.Minute, []struct {
			attempt int
			outcome string
		}{{1, "skipped"}}},
		{30 * time.Minute, []struct {
			attempt int
			outcome string
		}{{1, "failed"}}},
	}

	for i, sh := range shards {
		shardID := uuid.New().String()
		execIT(t, pool, `
            INSERT INTO teo.shards (id, run_id, index, status, predicted_duration_ms, test_count, started_at, finished_at)
            VALUES ($1, $2, $3, 'succeeded', 1000, 1, $4, $4)
        `, shardID, runID, i, base.Add(sh.offset))
		for _, e := range sh.entries {
			execIT(t, pool, `
                INSERT INTO teo.test_executions (shard_id, test_id, attempt, outcome, duration_ms, started_at, finished_at)
                VALUES ($1, $2, $3, $4, 1000, $5, $5)
            `, shardID, testID, e.attempt, e.outcome, base.Add(sh.offset).Add(time.Duration(e.attempt)*time.Second))
		}
	}

	d := &Daemon{Pool: pool, Logger: slog.Default()}
	outcomes, err := d.recentOutcomes(ctx, testID, maxHistoryPoints*3)
	require.NoError(t, err)

	// One row per shard (retry collapsed), oldest -> newest.
	require.Equal(t, []string{"passed", "passed", "skipped", "failed"}, outcomes)

	body := buildIssueBody("tests/x.py", "test_flaky", 0.25, 100, outcomes)
	require.Contains(t, body, "## Recent run history")
	require.Contains(t, body, "```mermaid")
	require.Contains(t, body, "xychart-beta")
	// pass, pass, (skipped filtered), fail -> bars 1,1,0.
	require.Contains(t, body, "bar [1, 1, 0]")
	require.Contains(t, body, "_1 skipped/interrupted run(s) omitted from the chart below._")
	// Sanity: the fence opens and closes.
	require.Equal(t, strings.Count(body, "```mermaid"), 1)
}

// TestQuarantineSweepE2E exercises the full Daemon.Run sweep end-to-end against a
// real Postgres: a Wilson-confirmed flake on an auto-quarantine-enabled repo,
// with a realistic mixed execution history, must (1) flip tests.status to
// 'quarantined', (2) open an issue whose body carries a Mermaid run-history
// chart matching the seeded oldest->newest outcomes, and (3) persist the
// returned github_issue_number on the flake record.
//
// Run is synchronous, so a direct assert after Run returns suffices (no poll).
//
// NOTE: requires Docker (testcontainers Postgres via internal/testpg). NOT
// executed in the authoring environment (Docker unavailable); written to compile
// and run under `go test -tags=integration ./internal/quarantine/...`.
func TestQuarantineSweepE2E(t *testing.T) {
	pool, cleanup := testpg.Start(t)
	t.Cleanup(cleanup)
	ctx := context.Background()

	repoID := uuid.New().String()
	runID := uuid.New().String()
	testID := uuid.New().String()

	execIT(t, pool, `INSERT INTO teo.repos (id, vcs, full_name, auto_quarantine_enabled) VALUES ($1, 'github', 'owner/sweep', TRUE)`, repoID)
	execIT(t, pool, `
        INSERT INTO teo.runs (id, repo_id, commit_sha, branch, triggered_by, status, started_at)
        VALUES ($1, $2, 'deadbeef', 'main', 'test', 'running', now())
    `, runID, repoID)
	execIT(t, pool, `
        INSERT INTO teo.tests (id, repo_id, fingerprint, path, name, runner, status)
        VALUES ($1, $2, 'fp-sweep', 'tests/sweep.py', 'test_sweepy', 'pytest', 'active')
    `, testID, repoID)
	// Wilson lower 0.10 > 0.05 and 25 >= 20 samples, not yet quarantined -> eligible.
	execIT(t, pool, `
        INSERT INTO teo.flake_records (test_id, flake_rate, wilson_lower, wilson_upper, sample_size, quarantined_at)
        VALUES ($1, 0.1500, 0.1000, 0.3000, 25, NULL)
    `, testID)

	// Seed three shards (= three runs) with distinct started_at, oldest -> newest:
	// passed, failed, passed.
	base := time.Now().Add(-3 * time.Hour)
	seeded := []struct {
		offset  time.Duration
		outcome string
	}{
		{0 * time.Minute, "passed"},
		{30 * time.Minute, "failed"},
		{60 * time.Minute, "passed"},
	}
	for i, s := range seeded {
		shardID := uuid.New().String()
		execIT(t, pool, `
            INSERT INTO teo.shards (id, run_id, index, status, predicted_duration_ms, test_count, started_at, finished_at)
            VALUES ($1, $2, $3, 'succeeded', 1000, 1, $4, $4)
        `, shardID, runID, i, base.Add(s.offset))
		execIT(t, pool, `
            INSERT INTO teo.test_executions (shard_id, test_id, attempt, outcome, duration_ms, started_at, finished_at)
            VALUES ($1, $2, 1, $3, 1000, $4, $4)
        `, shardID, testID, s.outcome, base.Add(s.offset))
	}

	opener := &captureOpener{number: 4321, url: "https://example.test/issues/4321"}
	d := &Daemon{Pool: pool, Logger: slog.Default(), IssueOpener: opener}

	require.NoError(t, d.Run(ctx))

	// (1) test row transitioned to quarantined.
	var status string
	require.NoError(t, pool.QueryRow(ctx, `SELECT status FROM teo.tests WHERE id = $1`, testID).Scan(&status))
	require.Equal(t, "quarantined", status)

	// (2) issue body carries the Mermaid chart matching the seeded order.
	require.NotEmpty(t, opener.body, "IssueOpener should have been called")
	require.Contains(t, opener.body, "```mermaid")
	require.Contains(t, opener.body, "xychart-beta")
	// passed, failed, passed -> bars 1,0,1 in oldest->newest order.
	require.Contains(t, opener.body, "bar [1, 0, 1]")
	require.Contains(t, opener.title, "test_sweepy")

	// (3) github_issue_number persisted on the flake record.
	var issueNum int
	require.NoError(t, pool.QueryRow(ctx, `SELECT github_issue_number FROM teo.flake_records WHERE test_id = $1`, testID).Scan(&issueNum))
	require.Equal(t, 4321, issueNum)
}
