package flake

import (
	"context"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Job is the nightly flake-recompute Cron Job. It scans tests with at least
// MinSamples attempts in the rolling window, computes the Wilson interval,
// and updates teo.flake_records.
type Job struct {
	Pool   *pgxpool.Pool
	Logger *slog.Logger
	Threshold Threshold
}

// Run scans all eligible tests once and writes flake_records.
func (j *Job) Run(ctx context.Context) error {
	t := j.Threshold
	if t.MinSamples == 0 {
		t = Default()
	}
	rows, err := j.Pool.Query(ctx, `
        WITH recent AS (
            SELECT te.test_id,
                   sum(CASE WHEN te.outcome IN ('failed','errored','timed_out') THEN 1 ELSE 0 END) AS failures,
                   count(*) AS attempts
            FROM teo.test_executions te
            JOIN teo.shards s ON s.id = te.shard_id
            JOIN teo.runs r ON r.id = s.run_id
            WHERE te.started_at > now() - INTERVAL '30 days'
            GROUP BY te.test_id
        )
        SELECT test_id, failures, attempts FROM recent WHERE attempts >= $1
    `, t.MinSamples)
	if err != nil {
		return err
	}
	defer rows.Close()

	type rec struct {
		testID string
		failures int
		attempts int
	}
	var batch []rec
	for rows.Next() {
		var r rec
		if err := rows.Scan(&r.testID, &r.failures, &r.attempts); err != nil {
			return err
		}
		batch = append(batch, r)
	}
	if err := rows.Err(); err != nil {
		return err
	}

	flaky, broken := 0, 0
	for _, r := range batch {
		d := Classify(r.failures, r.attempts, t)
		var category *string
		if d.IsBroken {
			c := "broken"
			category = &c
			broken++
		} else if d.IsFlaky {
			c := "unknown"
			category = &c
			flaky++
		}
		if _, err := j.Pool.Exec(ctx, `
            INSERT INTO teo.flake_records
                (test_id, flake_rate, wilson_lower, wilson_upper, sample_size, category, last_recomputed_at)
            VALUES ($1, $2, $3, $4, $5, $6, now())
            ON CONFLICT (test_id) DO UPDATE SET
                flake_rate = EXCLUDED.flake_rate,
                wilson_lower = EXCLUDED.wilson_lower,
                wilson_upper = EXCLUDED.wilson_upper,
                sample_size = EXCLUDED.sample_size,
                category = COALESCE(EXCLUDED.category, teo.flake_records.category),
                last_recomputed_at = now()
        `, r.testID, d.FailureRate, d.WilsonLower, d.WilsonUpper, r.attempts, category); err != nil {
			j.Logger.Error("flake upsert failed", "test", r.testID, "err", err)
		}
		// Auto-quarantine flaky tests when repo allows; broken tests get status='broken'.
		if d.IsBroken {
			_, _ = j.Pool.Exec(ctx, `UPDATE teo.tests SET status = 'broken' WHERE id = $1 AND status = 'active'`, r.testID)
		}
	}
	j.Logger.Info("flake job done", "scanned", len(batch), "flaky", flaky, "broken", broken)
	return nil
}
