package api

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/teo-dev/teo/internal/audit"
	"github.com/teo-dev/teo/internal/auth"
	"github.com/teo-dev/teo/internal/cost"
)

// errInvalidRunID is returned by mutations when the runId arg is empty.
var errInvalidRunID = errors.New("runId is required")

// errNoFailures is returned by rerunFailed when the parent run has nothing to retry.
var errNoFailures = errors.New("parent run has no failed tests to rerun")

// errForbiddenMutation is returned when an authenticated principal lacks the
// engineer/admin role required to invoke a state-changing mutation.
var errForbiddenMutation = errors.New("forbidden: requires engineer or admin role")

// requireMutationRole gates state-changing GraphQL fields. The /graphql route
// handler is responsible for returning 401 on missing principals (this helper
// returns the same forbidden envelope as a defense-in-depth fallback).
func requireMutationRole(ctx context.Context) error {
	p := auth.PrincipalFrom(ctx)
	if p == nil || !p.HasRole(auth.RoleEngineer, auth.RoleAdmin) {
		return errForbiddenMutation
	}
	return nil
}

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
        SELECT fc.id::text, fc.representative_message, fc.representative_stack,
               fc.occurrences, fc.first_seen, fc.last_seen, fc.stack_fingerprint,
               (SELECT count(DISTINCT s.run_id)
                  FROM teo.test_executions te
                  JOIN teo.shards s ON s.id = te.shard_id
                 WHERE te.failure_cluster_id = fc.id) AS affected_runs
        FROM teo.failure_clusters fc
        ORDER BY fc.last_seen DESC
        LIMIT 100
    `)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out, err := scanToMaps(rows, []string{
		"id", "representative_message", "representative_stack", "occurrences",
		"first_seen", "last_seen", "stack_fingerprint", "affected_runs",
	})
	if err != nil {
		return nil, err
	}
	computeClusterLayout(out)
	return out, nil
}

// computeClusterLayout derives presentation-only spatial-map coordinates for the
// Clusters screen, in place. x maps last_seen onto [0,1] (newest → left, x=0),
// y is log-scaled occurrences (more occurrences → smaller y, toward the top),
// r is a blast-radius pixel size in [9,40], and category is a heuristic from the
// representative message. Coordinates are RELATIVE to the returned window
// (min/max within the page), so they are presentation-only and shift as data
// changes — not stable IDs.
func computeClusterLayout(rows []map[string]any) {
	if len(rows) == 0 {
		return
	}
	var minSeen, maxSeen time.Time
	haveSeen := false
	maxOcc := int64(0)
	occOf := func(m map[string]any) int64 {
		switch v := m["occurrences"].(type) {
		case int64:
			return v
		case int32:
			return int64(v)
		case int:
			return int64(v)
		}
		return 0
	}
	seenOf := func(m map[string]any) (time.Time, bool) {
		if t, ok := m["last_seen"].(time.Time); ok {
			return t, true
		}
		return time.Time{}, false
	}
	for _, m := range rows {
		if occ := occOf(m); occ > maxOcc {
			maxOcc = occ
		}
		if t, ok := seenOf(m); ok {
			if !haveSeen || t.Before(minSeen) {
				minSeen = t
			}
			if !haveSeen || t.After(maxSeen) {
				maxSeen = t
			}
			haveSeen = true
		}
	}
	spanNs := maxSeen.Sub(minSeen).Seconds()
	logMaxOcc := math.Log10(float64(maxOcc) + 1)
	for _, m := range rows {
		occ := occOf(m)
		// x: newest → left (x=0). If all-equal or single row → 0.5.
		x := 0.5
		if t, ok := seenOf(m); ok && haveSeen && spanNs > 0 {
			x = 1 - (t.Sub(minSeen).Seconds() / spanNs)
		}
		// y: higher occurrences sit toward the top (smaller y).
		y := 0.5
		if logMaxOcc > 0 {
			y = clampFloat(1-math.Log10(float64(occ)+1)/logMaxOcc, 0, 1)
		}
		// r: blast-radius pixel size.
		r := 9.0
		if maxOcc > 0 {
			r = clampFloat(9+30*math.Sqrt(float64(occ)/float64(maxOcc)), 9, 40)
		}
		m["x"] = clampFloat(x, 0, 1)
		m["y"] = y
		m["r"] = r
		msg, _ := m["representative_message"].(string)
		m["category"] = classifyClusterCategory(msg)
	}
}

func clampFloat(v, lo, hi float64) float64 {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return lo
	}
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// classifyClusterCategory derives a heuristic ClusterCategory from a
// representative failure message. Pure and unit-testable in isolation.
func classifyClusterCategory(msg string) string {
	m := strings.ToLower(msg)
	switch {
	case strings.Contains(m, "panic"):
		return "panic"
	case strings.Contains(m, "timeout") || strings.Contains(m, "deadline exceeded"):
		return "timeout"
	case strings.Contains(m, "connection refused") || strings.Contains(m, "dial tcp") || strings.Contains(m, "nosuchkey"):
		return "network"
	case strings.Contains(m, "data race") || strings.Contains(m, "-race"):
		return "race"
	default:
		return "assertion"
	}
}

func queryFlakes(ctx context.Context, pool *pgxpool.Pool) ([]map[string]any, error) {
	rows, err := pool.Query(ctx, `
        SELECT t.id::text AS test_id, t.path, t.name,
               fr.flake_rate::float, fr.wilson_lower::float, fr.wilson_upper::float,
               fr.sample_size, fr.category, COALESCE(t.status, 'active') AS test_status,
               COALESCE(fr.quarantined_at, t.quarantined_at) AS quarantined_at,
               t.owner_team
        FROM teo.flake_records fr
        JOIN teo.tests t ON t.id = fr.test_id
        WHERE fr.wilson_lower > 0.05
        ORDER BY fr.wilson_lower DESC
        LIMIT 200
    `)
	if err != nil {
		return nil, err
	}
	out, err := scanToMaps(rows, []string{
		"test_id", "path", "name", "flake_rate", "wilson_lower", "wilson_upper",
		"sample_size", "category", "test_status", "quarantined_at", "owner_team",
	})
	rows.Close()
	if err != nil {
		return nil, err
	}
	for _, m := range out {
		m["status"] = flakeStatusBadge(m["test_status"])
		// Normalize the quarantined_at timestamp to RFC3339 so the String field
		// (and the web adapter's Date.parse) gets an unambiguous ISO value
		// rather than pgx's time.Time default %v rendering. NULL stays nil →
		// GraphQL null.
		if t, ok := m["quarantined_at"].(time.Time); ok {
			m["quarantined_at"] = t.UTC().Format(time.RFC3339)
		}
	}
	if err := attachFlakeSparklines(ctx, pool, out); err != nil {
		return nil, err
	}
	return out, nil
}

// attachFlakeSparklines populates "spark" (last-20 P/F/S outcomes, chronological
// left→right) and "duration_mean_ms" for each flake row, in a single batched
// query keyed by test_id = ANY($ids) to avoid N+1 round-trips.
func attachFlakeSparklines(ctx context.Context, pool *pgxpool.Pool, rows []map[string]any) error {
	if len(rows) == 0 {
		return nil
	}
	ids := make([]string, 0, len(rows))
	for _, m := range rows {
		if id, ok := m["test_id"].(string); ok && id != "" {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	q, err := pool.Query(ctx, `
        WITH ranked AS (
            SELECT te.test_id,
                   te.outcome,
                   te.duration_ms,
                   row_number() OVER (PARTITION BY te.test_id
                                      ORDER BY te.started_at DESC) AS rn
            FROM teo.test_executions te
            -- test_executions.test_id is uuid; the $1 param is a Go []string,
            -- which pgx encodes as text[] under the pool's extended protocol.
            -- The explicit ::uuid[] cast prevents "operator does not exist:
            -- uuid = text" at runtime (there is no uuid = text operator).
            WHERE te.test_id = ANY($1::uuid[])
        )
        SELECT test_id::text,
               string_agg(CASE outcome
                              WHEN 'passed'  THEN 'P'
                              WHEN 'skipped' THEN 'S'
                              ELSE 'F' END,
                          '' ORDER BY rn DESC) AS spark,
               COALESCE(avg(duration_ms) FILTER (WHERE duration_ms IS NOT NULL), 0)::float AS dur_mean
        FROM ranked
        WHERE rn <= 20
        GROUP BY test_id
    `, ids)
	if err != nil {
		return err
	}
	defer q.Close()
	type sparkRow struct {
		spark   string
		durMean float64
	}
	byID := make(map[string]sparkRow, len(ids))
	for q.Next() {
		var id, spark string
		var durMean float64
		if err := q.Scan(&id, &spark, &durMean); err != nil {
			return err
		}
		byID[id] = sparkRow{spark: spark, durMean: durMean}
	}
	if err := q.Err(); err != nil {
		return err
	}
	// string_agg ORDER BY rn DESC yields oldest→newest (rn=1 is newest), so the
	// resulting string already reads left→right chronological for the Sparkline
	// atom — no reversal needed.
	for _, m := range rows {
		id, _ := m["test_id"].(string)
		if sr, ok := byID[id]; ok {
			m["spark"] = sr.spark
			m["duration_mean_ms"] = int(math.Round(sr.durMean))
		} else {
			m["spark"] = ""
			m["duration_mean_ms"] = 0
		}
	}
	return nil
}

// sparklineMaxLen caps the sparkline at the most-recent N outcomes so the Flakes
// atom renders a fixed-width strip regardless of execution history depth.
const sparklineMaxLen = 20

// encodeSparkline maps an ordered slice of test-execution outcomes (oldest→newest,
// left→right) to a compact P/F/S string for the Flakes Sparkline atom: 'passed'→P,
// 'skipped'→S, everything else (failed/errored/timed_out)→F. The result is capped
// at sparklineMaxLen, keeping the most-recent (trailing) outcomes when longer.
//
// This mirrors the CASE in attachFlakeSparklines' SQL; it exists as a pure Go
// helper so the encoding is unit-testable without a database round-trip.
func encodeSparkline(outcomes []string) string {
	if len(outcomes) > sparklineMaxLen {
		outcomes = outcomes[len(outcomes)-sparklineMaxLen:]
	}
	var b strings.Builder
	b.Grow(len(outcomes))
	for _, o := range outcomes {
		switch strings.ToLower(o) {
		case "passed":
			b.WriteByte('P')
		case "skipped":
			b.WriteByte('S')
		default:
			b.WriteByte('F')
		}
	}
	return b.String()
}

// flakeStatusBadge maps teo.tests.status to the UI flake status badge.
func flakeStatusBadge(raw any) string {
	s, _ := raw.(string)
	switch strings.ToLower(s) {
	case "quarantined":
		return "quarantined"
	case "broken":
		return "broken"
	default:
		return "flagged"
	}
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
	// NOTE (ui-home-calibration): the Shard GraphQL type exposes
	// predictionConfidence + modelVersion, but this SELECT does NOT yet read
	// teo.shards.prediction_confidence / model_version (those columns don't
	// exist until the graphql-schema-fields migration lands). Until then the
	// resolvers return null for the absent map keys. When the migration adds the
	// columns, add them to BOTH the SELECT below and the scanToMaps column list —
	// they do not flow through automatically.
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

// queryRunPredictor computes run-level predictor calibration from the run's
// finished shards (those with a non-null actual_duration_ms). Per-shard delta is
// actual-predicted; mae is the mean absolute delta; modelVersion is read from
// teo.runs.meta->>'predictor_model' (fallback 'heuristic'); rho is the Pearson
// correlation of predicted vs actual across the run's shards; p50/p95 are
// percentiles of the absolute deltas. Returns nil (no error) when the run has
// fewer than 2 finished shards — the calibration overlay degrades gracefully.
//
// This reuses only data the resolvers already read (shards + runs.meta); it does
// NOT import internal/predictor (Phase A scope).
func queryRunPredictor(ctx context.Context, pool *pgxpool.Pool, runID string) (map[string]any, error) {
	var modelVersion string
	if err := pool.QueryRow(ctx, `
        SELECT COALESCE(meta->>'predictor_model', 'heuristic')
        FROM teo.runs WHERE id = $1
    `, runID).Scan(&modelVersion); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	if modelVersion == "" {
		modelVersion = "heuristic"
	}

	rows, err := pool.Query(ctx, `
        SELECT COALESCE(predicted_duration_ms, 0), actual_duration_ms
        FROM teo.shards
        WHERE run_id = $1 AND actual_duration_ms IS NOT NULL
    `, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var predicted, actual []float64
	for rows.Next() {
		var p, a int64
		if err := rows.Scan(&p, &a); err != nil {
			return nil, err
		}
		predicted = append(predicted, float64(p))
		actual = append(actual, float64(a))
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(actual) < 2 {
		return nil, nil
	}

	deltas := make([]float64, len(actual))
	absDeltas := make([]float64, len(actual))
	var sumAbs float64
	for i := range actual {
		d := actual[i] - predicted[i]
		deltas[i] = d
		absDeltas[i] = math.Abs(d)
		sumAbs += math.Abs(d)
	}
	mae := sumAbs / float64(len(actual))
	rho := pearson(predicted, actual)
	sort.Float64s(absDeltas)
	p50 := percentile(absDeltas, 0.50)
	p95 := percentile(absDeltas, 0.95)

	// confidence: 1/(1+normalizedMAE); normalized by mean actual to stay scale-free.
	var meanActual float64
	for _, a := range actual {
		meanActual += a
	}
	meanActual /= float64(len(actual))
	confidence := 1.0
	if meanActual > 0 {
		confidence = 1 / (1 + mae/meanActual)
	}

	return map[string]any{
		"mae":           mae,
		"rho":           rho,
		"model_version": modelVersion,
		"p50_delta_ms":  int(math.Round(p50)),
		"p95_delta_ms":  int(math.Round(p95)),
		"sample_count":  len(actual),
		"confidence":    confidence,
	}, nil
}

// pearson returns the Pearson correlation coefficient of x and y. Returns 0 when
// lengths differ, are <2, or either series has zero variance.
func pearson(x, y []float64) float64 {
	n := len(x)
	if n != len(y) || n < 2 {
		return 0
	}
	var sx, sy float64
	for i := 0; i < n; i++ {
		sx += x[i]
		sy += y[i]
	}
	mx, my := sx/float64(n), sy/float64(n)
	var cov, vx, vy float64
	for i := 0; i < n; i++ {
		dx, dy := x[i]-mx, y[i]-my
		cov += dx * dy
		vx += dx * dx
		vy += dy * dy
	}
	if vx == 0 || vy == 0 {
		return 0
	}
	return cov / math.Sqrt(vx*vy)
}

// percentile returns the p-quantile (0..1) of an already-sorted ascending slice
// using nearest-rank. Empty slice → 0.
func percentile(sorted []float64, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	if p <= 0 {
		return sorted[0]
	}
	if p >= 1 {
		return sorted[len(sorted)-1]
	}
	idx := int(math.Ceil(p*float64(len(sorted)))) - 1
	if idx < 0 {
		idx = 0
	}
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx]
}

// errInvalidTestID is returned by the quarantine mutations when testId is empty.
var errInvalidTestID = errors.New("testId is required")

// errTestNotFound is returned when no teo.tests row matches the supplied testId.
var errTestNotFound = errors.New("test not found")

// errCannotQuarantineDeleted guards against quarantining a soft-deleted test.
var errCannotQuarantineDeleted = errors.New("cannot quarantine a deleted test")

// rowQuerier is satisfied by both *pgxpool.Pool and pgx.Tx, so scanTestRow can
// read inside or outside a transaction.
type rowQuerier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// quarantineTest is the operator-initiated quarantine transition (S-08-03,
// T-08-03-01). It mirrors the auto-quarantine daemon's state changes so manual
// and automatic paths converge.
func quarantineTest(ctx context.Context, pool *pgxpool.Pool, aud *audit.Logger, testID, reason string) (map[string]any, error) {
	return setQuarantine(ctx, pool, aud, testID, true, reason)
}

// unquarantineTest restores a quarantined test to the active lane (S-08-03,
// T-08-03-01). It mirrors the magic-link restore endpoint's bookkeeping.
func unquarantineTest(ctx context.Context, pool *pgxpool.Pool, aud *audit.Logger, testID string) (map[string]any, error) {
	return setQuarantine(ctx, pool, aud, testID, false, "")
}

// setQuarantine flips teo.tests.status between active and quarantined under a
// row lock, mirrors the flake_records bookkeeping the auto-quarantine daemon
// (quarantine.go) and magic-link restore (unquarantine.go) use, and appends an
// audit row attributed to the caller's principal. It returns the updated test
// as a map[string]any for the GraphQL Test resolver. Idempotent: re-running in
// the same direction is a no-op that still returns the current row.
func setQuarantine(ctx context.Context, pool *pgxpool.Pool, aud *audit.Logger, testID string, on bool, reason string) (map[string]any, error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	// Lock the row so a concurrent quarantine sweep can't race the transition.
	var status string
	switch err := tx.QueryRow(ctx, `SELECT status FROM teo.tests WHERE id = $1 FOR UPDATE`, testID).Scan(&status); {
	case errors.Is(err, pgx.ErrNoRows):
		return nil, errTestNotFound
	case err != nil:
		return nil, err
	}

	action := "test.unquarantine"
	if on {
		action = "test.quarantine"
		if status == "deleted" {
			return nil, errCannotQuarantineDeleted
		}
		reason = strings.TrimSpace(reason)
		if reason == "" {
			reason = "manual: operator quarantine"
		}
		if _, err := tx.Exec(ctx, `
            UPDATE teo.tests
            SET status = 'quarantined', quarantined_at = now(), quarantine_reason = $2
            WHERE id = $1 AND status <> 'deleted'
        `, testID, reason); err != nil {
			return nil, err
		}
		if _, err := tx.Exec(ctx,
			`UPDATE teo.flake_records SET quarantined_at = now() WHERE test_id = $1`, testID); err != nil {
			return nil, err
		}
	} else {
		if _, err := tx.Exec(ctx, `
            UPDATE teo.tests
            SET status = 'active', quarantined_at = NULL, quarantine_reason = NULL
            WHERE id = $1 AND status = 'quarantined'
        `, testID); err != nil {
			return nil, err
		}
		if _, err := tx.Exec(ctx,
			`UPDATE teo.flake_records SET quarantined_at = NULL, unquarantined_at = now() WHERE test_id = $1 AND quarantined_at IS NOT NULL`,
			testID); err != nil {
			return nil, err
		}
	}

	m, err := scanTestRow(ctx, tx, testID)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}

	// Audit after commit so a logging failure can't roll back an applied
	// transition. Log is nil-safe when the pool is absent (unit tests).
	meta := map[string]any{"status": m["status"]}
	if on {
		meta["reason"] = reason
	}
	if err := aud.Log(ctx, audit.Entry{Action: action, TargetType: "test", TargetID: testID, Meta: meta}); err != nil {
		slog.WarnContext(ctx, "quarantine audit log failed", "test_id", testID, "action", action, "error", err)
	}
	return m, nil
}

// scanTestRow loads the columns the GraphQL Test type resolves, formatting
// quarantined_at to RFC3339 (nil when NULL) to match this package's other
// timestamp fields.
func scanTestRow(ctx context.Context, q rowQuerier, testID string) (map[string]any, error) {
	var (
		id, path, name, st string
		qAt                *time.Time
		reason, ownerTeam  *string
	)
	err := q.QueryRow(ctx, `
        SELECT id::text, path, name, status, quarantined_at, quarantine_reason, owner_team
        FROM teo.tests WHERE id = $1
    `, testID).Scan(&id, &path, &name, &st, &qAt, &reason, &ownerTeam)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, errTestNotFound
	}
	if err != nil {
		return nil, err
	}
	m := map[string]any{
		"id": id, "path": path, "name": name, "status": st,
		"quarantine_reason": reason, "owner_team": ownerTeam,
		"quarantined_at": nil,
	}
	if qAt != nil {
		m["quarantined_at"] = qAt.UTC().Format(time.RFC3339)
	}
	return m, nil
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
