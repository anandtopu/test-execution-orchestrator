// Package worker contains the worker-agent loop. The agent registers with the
// API, claims a shard, runs the bundled adapter, streams results back.
// In MVP, the dispatch path is a simple Postgres-claim instead of a full NATS
// + gRPC dance — sufficient to demonstrate end-to-end. The NATS path lands
// when E-07/E-13 wire it up.
package worker

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/teo-dev/teo/internal/logstore"
	"github.com/teo-dev/teo/internal/model"
	"github.com/teo-dev/teo/internal/redact"
	"github.com/teo-dev/teo/internal/spot"
	"github.com/teo-dev/teo/pkg/adapter"
)

// Agent is the worker-side loop.
type Agent struct {
	Pool     *pgxpool.Pool
	Adapter  adapter.Adapter
	Logger   *slog.Logger
	Workdir  string
	WorkerID string

	// SpotWatcher is the optional EC2 spot-interruption detector. If nil, the
	// worker runs as if it were on-demand. Per ADR-0020, on signal the agent
	// drains: stops claiming, marks the in-flight shard preempted, exits clean.
	SpotWatcher SpotInterruptionSource

	// Uploader stores per-test stdout/stderr captures. When nil the worker
	// uses logstore.Noop (logs are still redacted in-memory but never persisted).
	// FR-404.
	Uploader logstore.Uploader

	PollInterval      time.Duration
	HeartbeatInterval time.Duration

	redactor *redact.Redactor

	// draining flips to true when an interruption is detected. Subsequent
	// claim attempts (Postgres or NATS) early-return.
	draining atomic.Bool
	// currentShardID is the id of the shard the worker is executing right now,
	// or "" if idle. Used by the drain path to mark the right shard preempted.
	currentShardID atomic.Value
}

// SpotInterruptionSource is the contract the Agent expects from a spot watcher.
// Implementations: *spot.Watcher (production) and stubs in tests.
type SpotInterruptionSource interface {
	Watch(ctx context.Context) <-chan spot.Interruption
}

// IsDraining reports whether the agent has received an interruption notice.
func (a *Agent) IsDraining() bool { return a.draining.Load() }

// Run claims and executes assignments until ctx is canceled.
func (a *Agent) Run(ctx context.Context) error {
	if a.PollInterval == 0 {
		a.PollInterval = 2 * time.Second
	}
	if a.HeartbeatInterval == 0 {
		a.HeartbeatInterval = 5 * time.Second
	}
	if a.WorkerID == "" {
		a.WorkerID, _ = os.Hostname()
	}
	a.redactor = redact.New()
	if a.Uploader == nil {
		a.Uploader = logstore.Noop()
	}
	a.Logger.Info("worker started", "id", a.WorkerID, "runner", a.Adapter.Name())

	if a.SpotWatcher != nil {
		go a.watchSpot(ctx)
	}

	tick := time.NewTicker(a.PollInterval)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-tick.C:
			if a.draining.Load() {
				// Stop pulling new work; let the in-flight shard finish or be
				// timed out by the run manager. Exit if no shard is in flight.
				if cur, _ := a.currentShardID.Load().(string); cur == "" {
					a.Logger.Info("draining and idle; worker exiting cleanly")
					return nil
				}
				continue
			}
			if err := a.tryClaimAndRun(ctx); err != nil && !errors.Is(err, pgx.ErrNoRows) {
				a.Logger.Error("claim/run failed", "err", err)
			}
		}
	}
}

// watchSpot listens for an interruption notice and triggers the drain path.
// Per ADR-0020 the goroutine exits after the first signal — there is no
// "un-interruption" event from EC2.
func (a *Agent) watchSpot(ctx context.Context) {
	ch := a.SpotWatcher.Watch(ctx)
	select {
	case <-ctx.Done():
		return
	case it, ok := <-ch:
		if !ok || it.Action == "" {
			return
		}
		a.beginDrain(ctx, it)
	}
}

// beginDrain marks the agent as draining and updates the in-flight shard to
// 'preempted' so the Run Manager can reschedule its remaining tests.
// Idempotent: a second invocation is a no-op.
func (a *Agent) beginDrain(ctx context.Context, it spot.Interruption) {
	if !a.draining.CompareAndSwap(false, true) {
		return
	}
	a.Logger.Warn("spot interruption received; draining",
		"action", it.Action, "reclaim_at", it.Time)
	cur, _ := a.currentShardID.Load().(string)
	if cur == "" {
		return
	}
	// Mark shard preempted (only if still 'running' — race-safe).
	tag, err := a.Pool.Exec(ctx, `
        UPDATE teo.shards
        SET status = 'preempted', finished_at = now()
        WHERE id = $1 AND status = 'running'
    `, cur)
	if err != nil {
		a.Logger.Error("mark shard preempted failed", "shard", cur, "err", err)
		return
	}
	if tag.RowsAffected() == 0 {
		return // shard already terminal
	}
	// Bump runs.preemption_count for visibility (FR-308 telemetry).
	if _, err := a.Pool.Exec(ctx, `
        UPDATE teo.runs
        SET preemption_count = preemption_count + 1
        WHERE id = (SELECT run_id FROM teo.shards WHERE id = $1)
    `, cur); err != nil {
		a.Logger.Warn("bump preemption_count failed", "shard", cur, "err", err)
	}
}

func (a *Agent) tryClaimAndRun(ctx context.Context) error {
	if a.draining.Load() {
		return nil
	}
	tx, err := a.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var shardID, runID string
	err = tx.QueryRow(ctx, `
        UPDATE teo.shards
        SET status = 'running', worker_id = $1, started_at = now()
        WHERE id = (
            SELECT id FROM teo.shards
            WHERE status = 'pending'
              AND run_id IN (SELECT id FROM teo.runs WHERE status IN ('dispatching','running'))
            ORDER BY created_at ASC
            LIMIT 1 FOR UPDATE SKIP LOCKED
        )
        RETURNING id, run_id
    `, a.WorkerID).Scan(&shardID, &runID)
	if errors.Is(err, pgx.ErrNoRows) {
		return tx.Commit(ctx)
	}
	if err != nil {
		return err
	}

	tests, err := a.loadTestsForShard(ctx, tx, shardID)
	if err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}

	a.Logger.Info("shard claimed", "shard", shardID, "tests", len(tests))
	a.currentShardID.Store(shardID)
	defer a.currentShardID.Store("")
	a.executeShard(ctx, runID, shardID, tests)
	return nil
}

// loadTestsForShard returns the tests that the planner (or rescheduler)
// intended for this shard.
//
// Lookup order:
//  1. Per-shard reshard manifest at runs.meta.reshards[shardID] (E-13 reschedule)
//  2. Round-robin over the run-level manifest (E-02 default)
func (a *Agent) loadTestsForShard(ctx context.Context, tx pgx.Tx, shardID string) ([]model.TestEntry, error) {
	// (1) Per-shard reshard manifest, if present.
	//
	// NOTE: $1 and $2 are both the shard id but MUST be separate placeholders.
	// $1 is a JSONB text key (`meta->'reshards'->$1`, which forces $1 :: text)
	// while $2 is compared to the uuid `shards.id` column. Reusing a single $1
	// gives Postgres conflicting type deductions for one parameter, so the query
	// fails to plan — which previously aborted the surrounding tx and made the
	// next query return a confusing 25P02 ("transaction is aborted") instead.
	var reshardJSON []byte
	err := tx.QueryRow(ctx, `
        SELECT r.meta->'reshards'->$1
        FROM teo.runs r
        WHERE r.id = (SELECT run_id FROM teo.shards WHERE id = $2)
    `, shardID, shardID).Scan(&reshardJSON)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		// A real error here has aborted the tx; surface it rather than falling
		// through to query (2), which would then fail with 25P02 and mask the
		// root cause.
		return nil, err
	}
	if len(reshardJSON) > 0 {
		if tests, err := decodeManifestTests(reshardJSON); err == nil && len(tests) > 0 {
			return tests, nil
		}
	}

	// (2) Run-level manifest with round-robin assignment.
	var manifestJSON []byte
	var index int
	var totalShards int
	err = tx.QueryRow(ctx, `
        SELECT rp.plan, s.index,
               (SELECT count(*) FROM teo.shards WHERE run_id = s.run_id)
        FROM teo.shards s
        JOIN teo.run_plans rp ON rp.run_id = s.run_id
        WHERE s.id = $1
    `, shardID).Scan(&manifestJSON, &index, &totalShards)
	if err != nil {
		return nil, err
	}
	all, err := decodeManifestTests(manifestJSON)
	if err != nil || len(all) == 0 {
		return nil, err
	}
	mine := make([]model.TestEntry, 0)
	for i, t := range all {
		if i%totalShards == index {
			mine = append(mine, t)
		}
	}
	return mine, nil
}

func (a *Agent) executeShard(ctx context.Context, runID, shardID string, tests []model.TestEntry) {
	if len(tests) == 0 {
		_, _ = a.Pool.Exec(ctx, `
            UPDATE teo.shards SET status = 'succeeded', finished_at = now() WHERE id = $1
        `, shardID)
		return
	}
	timeout := 30 * time.Minute
	err := a.Adapter.Execute(ctx, a.Workdir, tests, adapter.ExecOptions{Timeout: timeout},
		func(r adapter.Result) {
			a.recordResult(ctx, runID, shardID, r)
		})
	// If draining, beginDrain already marked the shard as 'preempted'. Don't
	// overwrite that with 'failed' or 'succeeded'.
	if a.draining.Load() {
		a.Logger.Info("execute returned during drain; leaving shard preempted", "shard", shardID)
		return
	}
	if err != nil {
		a.Logger.Error("adapter execute failed", "err", err)
		_, _ = a.Pool.Exec(ctx, `UPDATE teo.shards SET status = 'failed', finished_at = now() WHERE id = $1`, shardID)
		return
	}
	_, _ = a.Pool.Exec(ctx, `UPDATE teo.shards SET status = 'succeeded', finished_at = now() WHERE id = $1`, shardID)
}

func (a *Agent) recordResult(ctx context.Context, runID, shardID string, r adapter.Result) {
	// Find or create the test row, then insert a test_execution.
	repoID := ""
	_ = a.Pool.QueryRow(ctx, `
        SELECT repo_id FROM teo.runs WHERE id = $1
    `, runID).Scan(&repoID)

	// Fingerprint folds in the AST signature (S-14-01 / S-06-01): a test whose
	// body changes gets a distinct identity (and fresh flake history) rather than
	// silently inheriting the old body's stats. Always 4 parts so the format is
	// stable even when astSig is empty (jest, or an adapter that couldn't parse).
	fingerprint := r.Test.Path + "::" + r.Test.Name + "::" + r.Test.ParamsHash + "::" + r.Test.ASTSignature
	var testID string
	err := a.Pool.QueryRow(ctx, `
        INSERT INTO teo.tests (repo_id, fingerprint, path, name, params_hash, ast_signature, runner, status)
        VALUES ($1, $2, $3, $4, $5, $6, $7, 'active')
        ON CONFLICT (repo_id, fingerprint) DO UPDATE SET last_seen = now()
        RETURNING id
    `, repoID, fingerprint, r.Test.Path, r.Test.Name, r.Test.ParamsHash, r.Test.ASTSignature, a.Adapter.Name()).Scan(&testID)
	if err != nil {
		a.Logger.Error("upsert test failed", "err", err)
		return
	}

	// Upload redacted stdout/stderr to S3 (FR-404). Empty captures skip the
	// upload but still leave log_object_key NULL — the schema allows that.
	logKey := a.uploadLog(ctx, runID, shardID, testID, r)

	_, err = a.Pool.Exec(ctx, `
        INSERT INTO teo.test_executions
            (shard_id, test_id, attempt, outcome, duration_ms, started_at, finished_at, log_object_key)
        VALUES ($1, $2, 1, $3, $4, $5, $6, NULLIF($7,''))
        ON CONFLICT (shard_id, test_id, attempt) DO NOTHING
    `, shardID, testID, r.Outcome, r.DurationMS, r.Started, r.Finished, logKey)
	if err != nil {
		a.Logger.Error("insert test_execution failed", "err", err)
	}
}

// uploadLog redacts stdout+stderr and ships them to the configured Uploader.
// Returns the object key on success, "" on no-content or upload failure (the
// failure is logged; we never block recording the test result on log delivery).
//
// Key layout: runs/{runID}/shards/{shardID}/tests/{testID}/{attempt}.log
// Stable + content-addressed by primary key columns so a future re-record
// overwrites in place.
func (a *Agent) uploadLog(ctx context.Context, runID, shardID, testID string, r adapter.Result) string {
	if len(r.Stdout) == 0 && len(r.Stderr) == 0 {
		return ""
	}
	key := "runs/" + runID + "/shards/" + shardID + "/tests/" + testID + "/1.log"

	var buf bytes.Buffer
	if len(r.Stdout) > 0 {
		buf.WriteString("=== stdout ===\n")
		buf.WriteString(a.redactor.String(string(r.Stdout)))
		if r.Stdout[len(r.Stdout)-1] != '\n' {
			buf.WriteByte('\n')
		}
	}
	if len(r.Stderr) > 0 {
		buf.WriteString("=== stderr ===\n")
		buf.WriteString(a.redactor.String(string(r.Stderr)))
		if r.Stderr[len(r.Stderr)-1] != '\n' {
			buf.WriteByte('\n')
		}
	}
	if r.FailureMessage != "" || r.FailureStack != "" {
		buf.WriteString("=== failure ===\n")
		if msg := a.redactor.String(r.FailureMessage); msg != "" {
			buf.WriteString(msg)
			buf.WriteByte('\n')
		}
		if stk := a.redactor.String(r.FailureStack); stk != "" {
			buf.WriteString(stk)
			if stk[len(stk)-1] != '\n' {
				buf.WriteByte('\n')
			}
		}
	}
	size := int64(buf.Len())
	if err := a.Uploader.Upload(ctx, key, &buf, size); err != nil {
		a.Logger.Warn("log upload failed", "key", key, "err", err)
		return ""
	}
	return key
}

func decodeManifestTests(b []byte) ([]model.TestEntry, error) {
	var m model.TestManifest
	if err := jsonUnmarshal(b, &m); err != nil {
		return nil, err
	}
	return m.Tests, nil
}

// jsonUnmarshal is a thin wrapper to avoid importing encoding/json at the top
// where no other usage exists.
func jsonUnmarshal(b []byte, v any) error {
	return jsonNew(b, v)
}
