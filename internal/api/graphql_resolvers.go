package api

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/teo-dev/teo/internal/cost"
)

// errInvalidRunID is returned by mutations when the runId arg is empty.
var errInvalidRunID = errors.New("runId is required")

// errNoFailures is returned by rerunFailed when the parent run has nothing to retry.
var errNoFailures = errors.New("parent run has no failed tests to rerun")

func queryRuns(ctx context.Context, pool *pgxpool.Pool, first int) ([]map[string]any, error) {
	rows, err := pool.Query(ctx, `
        SELECT r.id::text, repos.full_name, r.commit_sha, r.branch, r.status,
               COALESCE(r.total_duration_ms,0), r.started_at, r.finished_at
        FROM teo.runs r
        JOIN teo.repos ON repos.id = r.repo_id
        ORDER BY r.created_at DESC
        LIMIT $1
    `, first)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanToMaps(rows, []string{"id", "repo_full_name", "commit_sha", "branch", "status", "total_duration_ms", "started_at", "finished_at"})
}

func queryFailureClusters(ctx context.Context, pool *pgxpool.Pool) ([]map[string]any, error) {
	rows, err := pool.Query(ctx, `
        SELECT id::text, representative_message, representative_stack, occurrences,
               first_seen, last_seen
        FROM teo.failure_clusters
        ORDER BY last_seen DESC
        LIMIT 100
    `)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanToMaps(rows, []string{"id", "representative_message", "representative_stack", "occurrences", "first_seen", "last_seen"})
}

func queryFlakes(ctx context.Context, pool *pgxpool.Pool) ([]map[string]any, error) {
	rows, err := pool.Query(ctx, `
        SELECT t.id::text AS test_id, t.path, t.name,
               fr.flake_rate::float, fr.wilson_lower::float, fr.sample_size, fr.category
        FROM teo.flake_records fr
        JOIN teo.tests t ON t.id = fr.test_id
        WHERE fr.wilson_lower > 0.05
        ORDER BY fr.wilson_lower DESC
        LIMIT 200
    `)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanToMaps(rows, []string{"test_id", "path", "name", "flake_rate", "wilson_lower", "sample_size", "category"})
}

func queryRunByID(ctx context.Context, pool *pgxpool.Pool, id string) (map[string]any, error) {
	row := pool.QueryRow(ctx, `
        SELECT r.id::text, repos.full_name, r.commit_sha, r.branch, r.status,
               COALESCE(r.total_duration_ms,0), COALESCE(r.preemption_count,0),
               r.started_at, r.finished_at
        FROM teo.runs r
        JOIN teo.repos ON repos.id = r.repo_id
        WHERE r.id = $1
    `, id)
	vals := make([]any, 9)
	ptrs := make([]any, len(vals))
	for i := range vals {
		ptrs[i] = &vals[i]
	}
	if err := row.Scan(ptrs...); err != nil {
		return nil, err
	}
	keys := []string{"id", "repo_full_name", "commit_sha", "branch", "status", "total_duration_ms", "preemption_count", "started_at", "finished_at"}
	out := make(map[string]any, len(keys))
	for i, k := range keys {
		out[k] = vals[i]
	}
	return out, nil
}

func queryShards(ctx context.Context, pool *pgxpool.Pool, runID string) ([]map[string]any, error) {
	rows, err := pool.Query(ctx, `
        SELECT id::text, index, status, worker_id, predicted_duration_ms,
               actual_duration_ms, test_count, started_at, finished_at
        FROM teo.shards
        WHERE run_id = $1
        ORDER BY index ASC
    `, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanToMaps(rows, []string{"id", "index", "status", "worker_id", "predicted_duration_ms", "actual_duration_ms", "test_count", "started_at", "finished_at"})
}

func queryFailedTestCount(ctx context.Context, pool *pgxpool.Pool, runID string) (int, error) {
	var n int
	err := pool.QueryRow(ctx, `
        SELECT count(*) FROM teo.test_executions te
        JOIN teo.shards s ON s.id = te.shard_id
        WHERE s.run_id = $1 AND te.outcome IN ('failed','errored','timed_out')
    `, runID).Scan(&n)
	return n, err
}

// rerunFailed creates a child run scoped to the failed/quarantined tests of
// parent runID. Returns the newly-created run as a map[string]any so it slots
// into the existing GraphQL Run resolver.
func rerunFailed(ctx context.Context, pool *pgxpool.Pool, parentRunID string) (map[string]any, error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	// Load parent run
	var repoID, commit, branch string
	if err := tx.QueryRow(ctx, `
        SELECT repo_id::text, commit_sha, branch FROM teo.runs WHERE id = $1
    `, parentRunID).Scan(&repoID, &commit, &branch); err != nil {
		return nil, err
	}

	// Collect failed tests from the parent
	rows, err := tx.Query(ctx, `
        SELECT DISTINCT t.path, t.name, t.params_hash, t.runner
        FROM teo.test_executions te
        JOIN teo.shards s ON s.id = te.shard_id
        JOIN teo.tests t ON t.id = te.test_id
        WHERE s.run_id = $1
          AND te.outcome IN ('failed','errored','timed_out')
    `, parentRunID)
	if err != nil {
		return nil, err
	}
	type failedTest struct {
		Path       string `json:"path"`
		Name       string `json:"name"`
		ParamsHash string `json:"params_hash,omitempty"`
		Runner     string `json:"-"`
	}
	var tests []failedTest
	runner := ""
	for rows.Next() {
		var t failedTest
		if err := rows.Scan(&t.Path, &t.Name, &t.ParamsHash, &t.Runner); err != nil {
			rows.Close()
			return nil, err
		}
		if runner == "" {
			runner = t.Runner
		}
		tests = append(tests, t)
	}
	rows.Close()
	if len(tests) == 0 {
		return nil, errNoFailures
	}
	if runner == "" {
		runner = "pytest"
	}

	// Build manifest JSON identical to the run-intake shape
	manifest := map[string]any{"runner": runner, "tests": tests}
	manifestJSON, _ := json.Marshal(manifest)

	newID := uuid.New().String()
	// Postgres can't infer parameter types inside jsonb_build_object (declared
	// as `(VARIADIC any)`), so explicit casts are required on $6/$7 — without
	// them the prepare step fails with "could not determine data type of
	// parameter $6" (SQLSTATE 42P18).
	if _, err := tx.Exec(ctx, `
        INSERT INTO teo.runs
            (id, repo_id, commit_sha, branch, triggered_by, status, parent_run_id, meta)
        VALUES ($1, $2, $3, $4, 'rerun', 'pending', $5,
                jsonb_build_object('test_count', $6::int, 'runner', $7::text))
    `, newID, repoID, commit, branch, parentRunID, len(tests), runner); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `
        INSERT INTO teo.run_plans (run_id, plan, plan_version) VALUES ($1, $2::jsonb, 'manifest-v1')
    `, newID, manifestJSON); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return queryRunByID(ctx, pool, newID)
}

// queryCostSummary returns weekly aggregates of run cost from teo.runs.
// One row per ISO week within the last `weeks` weeks of the runs table,
// sorted oldest → newest so the UI can render a left-to-right trend.
//
// Cost is computed in Go (not SQL) so the rate config stays in one place
// (internal/cost) and the SQL stays portable. The query bucket is bounded
// to runs whose started_at falls in the last N weeks; canceled runs with
// no started_at are excluded.
func queryCostSummary(ctx context.Context, pool *pgxpool.Pool, p cost.Pricer, weeks int) ([]map[string]any, error) {
	if weeks <= 0 || weeks > 52 {
		weeks = 8
	}
	rows, err := pool.Query(ctx, `
        SELECT date_trunc('week', started_at) AS week_start,
               count(*)                       AS runs,
               COALESCE(sum(spot_minutes), 0)::float      AS spot_minutes,
               COALESCE(sum(on_demand_minutes), 0)::float AS ondemand_minutes
        FROM teo.runs
        WHERE started_at IS NOT NULL
          AND started_at >= now() - ($1::int || ' weeks')::interval
        GROUP BY week_start
        ORDER BY week_start ASC
    `, weeks)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []map[string]any
	for rows.Next() {
		var wk time.Time
		var runsCount int
		var spotMin, ondemandMin float64
		if err := rows.Scan(&wk, &runsCount, &spotMin, &ondemandMin); err != nil {
			return nil, err
		}
		total := p.RunCost(spotMin, ondemandMin)
		var perBuild, spotShare float64
		if runsCount > 0 {
			perBuild = total / float64(runsCount)
		}
		if denom := spotMin + ondemandMin; denom > 0 {
			spotShare = spotMin / denom
		}
		out = append(out, map[string]any{
			"week_start":       wk.UTC().Format("2006-01-02"),
			"runs":             runsCount,
			"spot_minutes":     spotMin,
			"ondemand_minutes": ondemandMin,
			"total_cost":       total,
			"cost_per_build":   perBuild,
			"spot_share":       spotShare,
		})
	}
	return out, rows.Err()
}

func scanToMaps(rows pgx.Rows, columns []string) ([]map[string]any, error) {
	var out []map[string]any
	for rows.Next() {
		vals, err := rows.Values()
		if err != nil {
			return nil, err
		}
		m := make(map[string]any, len(columns))
		for i, c := range columns {
			if i < len(vals) {
				m[c] = vals[i]
			}
		}
		out = append(out, m)
	}
	return out, rows.Err()
}
