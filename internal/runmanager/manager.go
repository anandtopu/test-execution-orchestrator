package runmanager

import (
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nats-io/nats.go/jetstream"

	teometrics "github.com/teo-dev/teo/internal/metrics"
	teonats "github.com/teo-dev/teo/internal/nats"
	"github.com/teo-dev/teo/internal/model"
	"github.com/teo-dev/teo/internal/predictor"
	"github.com/teo-dev/teo/internal/scheduler"
)

// Manager reconciles runs through their state machine.
type Manager struct {
	Pool      *pgxpool.Pool
	Predictor predictor.Predictor
	JS        jetstream.JetStream // optional: if nil, dispatch is Postgres-only
	Logger    *slog.Logger
	Metrics   *teometrics.Registry // optional; nil = no-op
	Observers []RunObserver // notified after every successful state transition

	PollInterval        time.Duration
	BudgetCheckInterval time.Duration
	RescheduleInterval  time.Duration // sweeps preempted/lost shards; defaults to 5s
}

// notifyObservers invokes every registered observer with the post-transition
// snapshot. Observer errors are logged but do not affect the Manager.
func (m *Manager) notifyObservers(ctx context.Context, runID string, prev model.RunStatus) {
	if len(m.Observers) == 0 {
		return
	}
	snap, err := m.snapshot(ctx, runID)
	if err != nil {
		m.Logger.Warn("observer snapshot failed", "run", runID, "err", err)
		return
	}
	for i, obs := range m.Observers {
		if err := obs.OnRunStateChanged(ctx, snap, prev); err != nil {
			m.Logger.Warn("observer error", "observer_index", i, "run", runID, "err", err)
		}
	}
}

// snapshot reads the public-facing fields of a run for observer dispatch.
func (m *Manager) snapshot(ctx context.Context, runID string) (RunSnapshot, error) {
	var s RunSnapshot
	err := m.Pool.QueryRow(ctx, `
        SELECT r.id::text, repos.full_name, r.commit_sha, r.branch, r.status,
               r.started_at, r.finished_at, COALESCE(r.total_duration_ms, 0),
               COALESCE(r.preemption_count, 0),
               r.github_check_run_id, r.github_installation_id
        FROM teo.runs r
        JOIN teo.repos ON repos.id = r.repo_id
        WHERE r.id = $1
    `, runID).Scan(&s.ID, &s.RepoFullName, &s.CommitSHA, &s.Branch, &s.Status,
		&s.StartedAt, &s.FinishedAt, &s.TotalDurationMS, &s.PreemptionCount,
		&s.GitHubCheckRunID, &s.GitHubInstallationID)
	return s, err
}

// Run starts the reconciliation loop until ctx is cancelled.
func (m *Manager) Run(ctx context.Context) error {
	if m.PollInterval == 0 {
		m.PollInterval = time.Second
	}
	if m.BudgetCheckInterval == 0 {
		m.BudgetCheckInterval = 5 * time.Second
	}
	if m.RescheduleInterval == 0 {
		m.RescheduleInterval = 5 * time.Second
	}
	tick := time.NewTicker(m.PollInterval)
	defer tick.Stop()
	budget := time.NewTicker(m.BudgetCheckInterval)
	defer budget.Stop()
	resched := time.NewTicker(m.RescheduleInterval)
	defer resched.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-tick.C:
			m.reconcileOnce(ctx)
		case <-budget.C:
			m.checkBudgets(ctx)
		case <-resched.C:
			m.reschedulePreempted(ctx)
		}
	}
}

func (m *Manager) reconcileOnce(ctx context.Context) {
	rows, err := m.Pool.Query(ctx, `
        SELECT id, status FROM teo.runs
        WHERE status IN ('pending','dispatching','finalizing')
        ORDER BY created_at ASC
        LIMIT 50
    `)
	if err != nil {
		m.Logger.Error("scan runs failed", "err", err)
		return
	}
	type todo struct {
		ID     string
		Status model.RunStatus
	}
	var work []todo
	for rows.Next() {
		var t todo
		if err := rows.Scan(&t.ID, &t.Status); err != nil {
			m.Logger.Error("scan row failed", "err", err)
			continue
		}
		work = append(work, t)
	}
	rows.Close()

	for _, t := range work {
		m.tryHandle(ctx, t.ID, t.Status)
	}

	// Refresh the runs_active gauge — single GROUP BY scan, cheap.
	if m.Metrics != nil {
		m.refreshRunsActive(ctx)
	}
}

// refreshRunsActive samples the per-status run count for the gauge. Called
// from reconcileOnce so the freshness matches the reconciliation cadence.
func (m *Manager) refreshRunsActive(ctx context.Context) {
	rows, err := m.Pool.Query(ctx, `SELECT status, count(*) FROM teo.runs GROUP BY status`)
	if err != nil {
		return
	}
	defer rows.Close()
	// Reset every status we know about to 0 first so a status that vanished
	// (e.g., last 'pending' just transitioned) reports 0 rather than stale.
	for _, st := range []string{"pending", "planning", "dispatching", "running", "finalizing", "succeeded", "failed", "cancelled"} {
		m.Metrics.RunsActive.WithLabelValues(st).Set(0)
	}
	for rows.Next() {
		var st string
		var n float64
		if err := rows.Scan(&st, &n); err != nil {
			continue
		}
		m.Metrics.RunsActive.WithLabelValues(st).Set(n)
	}
}

// tryHandle acquires the per-run advisory lock and processes one transition.
func (m *Manager) tryHandle(ctx context.Context, runID string, status model.RunStatus) {
	tx, err := m.Pool.Begin(ctx)
	if err != nil {
		return
	}
	defer tx.Rollback(ctx)

	got := false
	if err := tx.QueryRow(ctx,
		`SELECT pg_try_advisory_xact_lock($1)`,
		runIDLockKey(runID)).Scan(&got); err != nil {
		return
	}
	if !got {
		return // another replica owns this run
	}

	prev := status
	switch status {
	case model.RunPending:
		if err := m.plan(ctx, tx, runID); err != nil {
			m.Logger.Error("plan failed", "run", runID, "err", err)
			_, _ = tx.Exec(ctx, `UPDATE teo.runs SET status = 'failed', finished_at = now() WHERE id = $1`, runID)
		}
	case model.RunDispatching:
		if err := m.dispatch(ctx, tx, runID); err != nil {
			m.Logger.Error("dispatch failed", "run", runID, "err", err)
		}
	case model.RunFinalizing:
		if err := m.finalize(ctx, tx, runID); err != nil {
			m.Logger.Error("finalize failed", "run", runID, "err", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		m.Logger.Error("tx commit failed", "run", runID, "err", err)
		return
	}
	// Observer dispatch happens AFTER commit so observers see the
	// post-transition state and so a slow third-party API can't hold the lock.
	m.notifyObservers(ctx, runID, prev)
	// Metric: count state transitions by destination status. Read the new
	// status directly to label correctly (the snapshot fn used by observers
	// already touched the row, so cache locality is good).
	if m.Metrics != nil {
		var to string
		if err := m.Pool.QueryRow(ctx, `SELECT status FROM teo.runs WHERE id = $1`, runID).Scan(&to); err == nil {
			m.Metrics.RunTransitions.WithLabelValues(to).Inc()
		}
	}
}

func (m *Manager) plan(ctx context.Context, tx pgx.Tx, runID string) error {
	// Move to planning
	if _, err := tx.Exec(ctx,
		`UPDATE teo.runs SET status = 'planning' WHERE id = $1 AND status = 'pending'`,
		runID); err != nil {
		return err
	}

	// Load run + repo + manifest
	var repoID, repoFull string
	if err := tx.QueryRow(ctx, `
        SELECT r.repo_id, repos.full_name FROM teo.runs r
        JOIN teo.repos ON repos.id = r.repo_id WHERE r.id = $1
    `, runID).Scan(&repoID, &repoFull); err != nil {
		return err
	}

	var manifestRaw []byte
	if err := tx.QueryRow(ctx,
		`SELECT plan FROM teo.run_plans WHERE run_id = $1`, runID).
		Scan(&manifestRaw); err != nil {
		return fmt.Errorf("load manifest: %w", err)
	}
	var manifest model.TestManifest
	if err := json.Unmarshal(manifestRaw, &manifest); err != nil {
		return err
	}

	// Predict
	preds, err := m.Predictor.Predict(ctx, repoFull, manifest.Tests)
	if err != nil {
		return fmt.Errorf("predict: %w", err)
	}
	tests := make([]scheduler.Test, len(manifest.Tests))
	for i, e := range manifest.Tests {
		var p predictor.Prediction
		if i < len(preds) {
			p = preds[i]
		}
		tests[i] = scheduler.Test{
			Entry:       e,
			PredictedMS: p.P50DurationMS,
			IsColdStart: p.IsColdStart,
			FlakeProb:   p.FlakeProbability,
		}
	}

	// Run the pure scheduler (timed for the teo_scheduler_plan_seconds histogram).
	planStart := time.Now()
	plan := scheduler.PlanFunc(tests, scheduler.FleetSnapshot{}, scheduler.Constraints{
		TargetShardSeconds: 300,
		MinShards:          1,
		MaxShards:          50,
	})
	if m.Metrics != nil {
		m.Metrics.SchedulerPlans.Inc()
		m.Metrics.SchedulerPlanSec.Observe(time.Since(planStart).Seconds())
	}

	// Persist plan and shards
	planJSON, _ := json.Marshal(plan)
	if _, err := tx.Exec(ctx, `
        INSERT INTO teo.run_plans (run_id, plan, plan_version) VALUES ($1, $2::jsonb, $3)
        ON CONFLICT (run_id) DO UPDATE SET plan = EXCLUDED.plan, plan_version = EXCLUDED.plan_version
    `, runID, planJSON, plan.Version); err != nil {
		return err
	}

	for _, a := range plan.Assignments {
		shardID := uuid.New().String()
		if _, err := tx.Exec(ctx, `
            INSERT INTO teo.shards (id, run_id, index, predicted_duration_ms, status, test_count)
            VALUES ($1, $2, $3, $4, 'pending', $5)
        `, shardID, runID, a.ShardIndex, a.PredictedDurationMS, len(a.Tests)); err != nil {
			return err
		}
	}

	if _, err := tx.Exec(ctx, `
        UPDATE teo.runs SET status = 'dispatching', started_at = COALESCE(started_at, now())
        WHERE id = $1
    `, runID); err != nil {
		return err
	}
	m.Logger.Info("run planned", "run", runID, "shards", len(plan.Assignments),
		"makespan_ms", plan.MakespanPredictedMS)
	return nil
}

func (m *Manager) dispatch(ctx context.Context, tx pgx.Tx, runID string) error {
	// Publish each pending shard to NATS so subscribed workers can claim it.
	// If NATS isn't configured (m.JS == nil), workers fall back to the
	// Postgres SKIP-LOCKED claim path.
	if m.JS != nil {
		var planRaw []byte
		if err := tx.QueryRow(ctx,
			`SELECT plan FROM teo.run_plans WHERE run_id = $1`, runID).Scan(&planRaw); err == nil {
			var plan scheduler.Plan
			if err := json.Unmarshal(planRaw, &plan); err == nil {
				m.publishShards(ctx, tx, runID, plan)
			}
		}
	}
	if _, err := tx.Exec(ctx,
		`UPDATE teo.runs SET status = 'running' WHERE id = $1 AND status = 'dispatching'`,
		runID); err != nil {
		return err
	}
	return nil
}

func (m *Manager) publishShards(ctx context.Context, tx pgx.Tx, runID string, plan scheduler.Plan) {
	rows, err := tx.Query(ctx, `
        SELECT s.id, s.index, s.predicted_duration_ms, repos.full_name
        FROM teo.shards s
        JOIN teo.runs r ON r.id = s.run_id
        JOIN teo.repos ON repos.id = r.repo_id
        WHERE s.run_id = $1 AND s.status = 'pending'
    `, runID)
	if err != nil {
		m.Logger.Warn("query shards for dispatch", "err", err)
		return
	}
	defer rows.Close()
	type meta struct {
		id, repoFull string
		index, ms    int
	}
	var shards []meta
	for rows.Next() {
		var sm meta
		if err := rows.Scan(&sm.id, &sm.index, &sm.ms, &sm.repoFull); err != nil {
			continue
		}
		shards = append(shards, sm)
	}
	for _, sm := range shards {
		if sm.index >= len(plan.Assignments) {
			continue
		}
		a := plan.Assignments[sm.index]
		dispatch := teonats.ShardDispatch{
			RunID:        runID,
			ShardID:      sm.id,
			RepoFullName: sm.repoFull,
			Runner:       "pytest", // resolved at worker side from manifest in MVP
			PredictedMS:  a.PredictedDurationMS,
			DispatchedAt: time.Now().UTC(),
		}
		for _, t := range a.Tests {
			dispatch.Tests = append(dispatch.Tests, teonats.DispatchTest{
				Path:       t.Entry.Path,
				Name:       t.Entry.Name,
				ParamsHash: t.Entry.ParamsHash,
				Tags:       t.Entry.Tags,
			})
		}
		if err := teonats.Publish(ctx, m.JS, teonats.SubjShardsDispatch, dispatch); err != nil {
			m.Logger.Warn("nats publish dispatch failed", "shard", sm.id, "err", err)
		}
	}
}

func (m *Manager) finalize(ctx context.Context, tx pgx.Tx, runID string) error {
	// Compute aggregate outcome from shards.
	var anyFailed bool
	if err := tx.QueryRow(ctx, `
        SELECT EXISTS (SELECT 1 FROM teo.shards WHERE run_id = $1 AND status IN ('failed','lost'))
    `, runID).Scan(&anyFailed); err != nil {
		return err
	}
	final := model.RunSucceeded
	if anyFailed {
		final = model.RunFailed
	}
	if _, err := tx.Exec(ctx, `
        UPDATE teo.runs
        SET status = $2, finished_at = now(),
            total_duration_ms = COALESCE(
                (SELECT EXTRACT(EPOCH FROM (now() - started_at))::int * 1000 FROM teo.runs WHERE id = $1),
                0)
        WHERE id = $1
    `, runID, final); err != nil {
		return err
	}
	m.Logger.Info("run finalized", "run", runID, "status", final)
	return nil
}

// reschedulePreempted scans for shards in 'preempted' or 'lost' state that
// haven't yet been replayed (`meta.rescheduled_at IS NULL`) and creates a
// fresh pending shard for the tests that didn't finish in the original shard.
// Per ADR-0020:
//   - Tests already in test_executions for that shard are NOT re-run.
//   - Tests in the original assignment that have no execution row become the
//     new shard's payload.
//   - The original shard is marked as `meta.rescheduled_at = now()` so we
//     don't reschedule it twice.
func (m *Manager) reschedulePreempted(ctx context.Context) {
	rows, err := m.Pool.Query(ctx, `
        SELECT s.id::text, s.run_id::text, s.index, rp.plan
        FROM teo.shards s
        JOIN teo.run_plans rp ON rp.run_id = s.run_id
        JOIN teo.runs r ON r.id = s.run_id
        WHERE s.status IN ('preempted','lost')
          AND r.status IN ('dispatching','running')
          AND (s.meta->>'rescheduled_at') IS NULL
        LIMIT 25
    `)
	if err != nil {
		m.Logger.Warn("reschedule scan failed", "err", err)
		return
	}
	type todo struct {
		shardID, runID string
		index          int
		planRaw        []byte
	}
	var work []todo
	for rows.Next() {
		var t todo
		if err := rows.Scan(&t.shardID, &t.runID, &t.index, &t.planRaw); err != nil {
			continue
		}
		work = append(work, t)
	}
	rows.Close()

	for _, t := range work {
		if err := m.rescheduleOne(ctx, t.shardID, t.runID, t.index, t.planRaw); err != nil {
			m.Logger.Warn("reschedule shard failed", "shard", t.shardID, "err", err)
		}
	}
}

// rescheduleOne re-shards the not-yet-completed tests of one preempted shard.
func (m *Manager) rescheduleOne(ctx context.Context, shardID, runID string, index int, planRaw []byte) error {
	tx, err := m.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	// Per-run advisory lock — same scheme as state-transition handling.
	got := false
	if err := tx.QueryRow(ctx,
		`SELECT pg_try_advisory_xact_lock($1)`, runIDLockKey(runID)).Scan(&got); err != nil {
		return err
	}
	if !got {
		return nil
	}

	// Decode the original shard's intended tests via the plan's round-robin
	// (matches loadTestsForShard in the worker).
	type planTest struct {
		Path       string `json:"path"`
		Name       string `json:"name"`
		ParamsHash string `json:"params_hash"`
	}
	type manifest struct {
		Runner string     `json:"runner"`
		Tests  []planTest `json:"tests"`
	}
	var mf manifest
	if err := json.Unmarshal(planRaw, &mf); err != nil {
		return fmt.Errorf("decode manifest: %w", err)
	}
	if len(mf.Tests) == 0 {
		return nil
	}
	var totalShards int
	if err := tx.QueryRow(ctx,
		`SELECT count(*) FROM teo.shards WHERE run_id = $1`, runID).Scan(&totalShards); err != nil {
		return err
	}
	if totalShards == 0 {
		return nil
	}
	intended := make([]planTest, 0)
	for i, t := range mf.Tests {
		if i%totalShards == index {
			intended = append(intended, t)
		}
	}

	// Subtract tests that already have an execution row for this shard.
	doneRows, err := tx.Query(ctx, `
        SELECT t.path, t.name, t.params_hash
        FROM teo.test_executions te
        JOIN teo.tests t ON t.id = te.test_id
        WHERE te.shard_id = $1
    `, shardID)
	if err != nil {
		return err
	}
	done := make(map[string]bool, 32)
	for doneRows.Next() {
		var p, n, h string
		if err := doneRows.Scan(&p, &n, &h); err != nil {
			doneRows.Close()
			return err
		}
		done[p+"|"+n+"|"+h] = true
	}
	doneRows.Close()

	remaining := make([]planTest, 0, len(intended))
	for _, t := range intended {
		if !done[t.Path+"|"+t.Name+"|"+t.ParamsHash] {
			remaining = append(remaining, t)
		}
	}
	if len(remaining) == 0 {
		// Every test in the original shard had finished before preemption.
		// Mark the shard succeeded and record the dedupe marker.
		if _, err := tx.Exec(ctx, `
            UPDATE teo.shards
            SET status = 'succeeded',
                meta = COALESCE(meta,'{}'::jsonb) || jsonb_build_object('rescheduled_at', now())
            WHERE id = $1
        `, shardID); err != nil {
			return err
		}
		return tx.Commit(ctx)
	}

	// Append a new pending shard at the end. The worker's loadTestsForShard
	// uses round-robin keyed on (i % totalShards == index); the new shard's
	// round-robin assignment won't match the original tests. To work around
	// that without a schema change, we encode the intended tests as a
	// `plan-v1` record instead of relying on round-robin.
	newPlan := manifest{Runner: mf.Runner, Tests: remaining}
	newPlanJSON, _ := json.Marshal(newPlan)
	newShardID := uuid.New().String()
	if _, err := tx.Exec(ctx, `
        INSERT INTO teo.shards
            (id, run_id, index, predicted_duration_ms, status, test_count)
        VALUES ($1, $2, $3, $4, 'pending', $5)
    `, newShardID, runID, totalShards /* append at end */, 0, len(remaining)); err != nil {
		return err
	}
	// Persist the per-shard remaining manifest to teo.run_plans by upserting
	// a sub-key. Run-plans is per-run today; we use a simple convention:
	// store remaining tests under runs.meta.reshards[<new_shard_id>] = manifest.
	if _, err := tx.Exec(ctx, `
        UPDATE teo.runs
        SET meta = COALESCE(meta,'{}'::jsonb) || jsonb_build_object('reshards',
            COALESCE(meta->'reshards','{}'::jsonb) || jsonb_build_object($1::text, $2::jsonb))
        WHERE id = $3
    `, newShardID, newPlanJSON, runID); err != nil {
		return err
	}

	// Mark the original shard with a dedupe marker so the sweep doesn't loop.
	if _, err := tx.Exec(ctx, `
        UPDATE teo.shards
        SET meta = COALESCE(meta,'{}'::jsonb) || jsonb_build_object('rescheduled_at', now())
        WHERE id = $1
    `, shardID); err != nil {
		return err
	}

	if err := tx.Commit(ctx); err != nil {
		return err
	}
	m.Logger.Info("rescheduled preempted shard",
		"original", shardID, "new", newShardID, "remaining", len(remaining))
	return nil
}

func (m *Manager) checkBudgets(ctx context.Context) {
	_, err := m.Pool.Exec(ctx, `
        UPDATE teo.runs
        SET status = 'failed', finished_at = COALESCE(finished_at, now()),
            meta = meta || jsonb_build_object('failure_reason', 'budget_exceeded')
        WHERE status IN ('planning','dispatching','running')
          AND budget_seconds IS NOT NULL
          AND started_at IS NOT NULL
          AND now() - started_at > make_interval(secs => budget_seconds)
    `)
	if err != nil {
		m.Logger.Error("budget check failed", "err", err)
	}
	// Update the runs_stuck gauge: count runs past 2x their budget but not
	// yet timed out by the UPDATE above (rare race window) plus runs whose
	// budget the operator hasn't set but that are visibly stuck. The
	// alert threshold (NFR-OBS-705) is "any value > 0".
	if m.Metrics != nil {
		var n float64
		if err := m.Pool.QueryRow(ctx, `
            SELECT count(*) FROM teo.runs
            WHERE status IN ('planning','dispatching','running','finalizing')
              AND started_at IS NOT NULL
              AND budget_seconds IS NOT NULL
              AND now() - started_at > make_interval(secs => budget_seconds * 2)
        `).Scan(&n); err == nil {
			m.Metrics.RunsStuck.Set(n)
		}
	}
}

// runIDLockKey deterministically maps a UUID to a 64-bit advisory lock key.
func runIDLockKey(runID string) int64 {
	h := fnv.New64a()
	h.Write([]byte(runID))
	return int64(h.Sum64())
}
