// Package predictor implements the Go heuristic predictor: rolling-mean per
// (repo, file) over the last 30 runs, with cold-start fallback. The Python
// LightGBM service implements the same gRPC contract; the Go heuristic is
// always present as the fallback (ADR-0019).
package predictor

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/teo-dev/teo/internal/model"
)

// Prediction is one test's predicted duration + flake probability.
type Prediction struct {
	Fingerprint      string
	P50DurationMS    int
	P95DurationMS    int
	FlakeProbability float32
	IsColdStart      bool
}

// Predictor produces Predictions from history.
type Predictor interface {
	Predict(ctx context.Context, repoFullName string, tests []model.TestEntry) ([]Prediction, error)
}

// Heuristic is the always-present rolling-mean predictor.
type Heuristic struct {
	Pool       *pgxpool.Pool
	WindowDays int
	Defaults   map[string]int // runner → default p50 (ms) for cold-start
}

// NewHeuristic returns a Heuristic with sensible defaults.
func NewHeuristic(pool *pgxpool.Pool) *Heuristic {
	return &Heuristic{
		Pool:       pool,
		WindowDays: 30,
		Defaults: map[string]int{
			"pytest": 1200,
			"go":     500,
			"jest":   1500,
		},
	}
}

// Predict returns one Prediction per input test, in the same order.
func (h *Heuristic) Predict(ctx context.Context, repoFullName string, tests []model.TestEntry) ([]Prediction, error) {
	if h == nil || h.Pool == nil {
		return nil, errors.New("heuristic not configured")
	}
	if len(tests) == 0 {
		return nil, nil
	}

	// Resolve repo
	var repoID string
	err := h.Pool.QueryRow(ctx,
		`SELECT id FROM teo.repos WHERE full_name = $1 AND vcs = 'github'`,
		repoFullName).Scan(&repoID)
	if err != nil {
		// On unknown repo we still return cold-start predictions — calling code may
		// be in a pre-onboarding flow.
		return h.coldOnly(tests), nil
	}

	// Build a per-(path,name) lookup of recent rolling stats.
	stats, err := h.loadStats(ctx, repoID)
	if err != nil {
		return h.coldOnly(tests), nil
	}

	out := make([]Prediction, len(tests))
	for i, t := range tests {
		key := t.Path + "::" + t.Name
		if s, ok := stats[key]; ok && s.attempts >= 3 {
			out[i] = Prediction{
				Fingerprint:      key,
				P50DurationMS:    s.p50,
				P95DurationMS:    s.p95,
				FlakeProbability: s.flakeRate,
				IsColdStart:      false,
			}
			continue
		}
		out[i] = h.coldStart(t)
	}
	return out, nil
}

type stat struct {
	attempts  int
	p50       int
	p95       int
	flakeRate float32
}

func (h *Heuristic) loadStats(ctx context.Context, repoID string) (map[string]stat, error) {
	q := `
        WITH recent AS (
            SELECT te.test_id, te.duration_ms, te.outcome
            FROM teo.test_executions te
            JOIN teo.shards s ON s.id = te.shard_id
            JOIN teo.runs r ON r.id = s.run_id
            WHERE r.repo_id = $1 AND te.started_at > now() - INTERVAL '30 days'
        )
        SELECT t.path, t.name,
               count(r.duration_ms),
               percentile_disc(0.5)  WITHIN GROUP (ORDER BY r.duration_ms) AS p50,
               percentile_disc(0.95) WITHIN GROUP (ORDER BY r.duration_ms) AS p95,
               sum(CASE WHEN r.outcome IN ('failed','errored','timed_out') THEN 1 ELSE 0 END)::float
                 / GREATEST(count(*), 1) AS flake_rate
        FROM teo.tests t
        LEFT JOIN recent r ON r.test_id = t.id
        WHERE t.repo_id = $1 AND t.status != 'deleted'
        GROUP BY t.path, t.name
    `
	rows, err := h.Pool.Query(ctx, q, repoID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string]stat)
	for rows.Next() {
		var path, name string
		var attempts int
		var p50, p95 int
		var flakeRate float64
		if err := rows.Scan(&path, &name, &attempts, &p50, &p95, &flakeRate); err != nil {
			return nil, err
		}
		out[path+"::"+name] = stat{
			attempts:  attempts,
			p50:       p50,
			p95:       p95,
			flakeRate: float32(flakeRate),
		}
	}
	return out, rows.Err()
}

func (h *Heuristic) coldStart(t model.TestEntry) Prediction {
	return Prediction{
		Fingerprint:   t.Path + "::" + t.Name,
		P50DurationMS: h.defaultFor(""),
		P95DurationMS: h.defaultFor("") * 3,
		IsColdStart:   true,
	}
}

func (h *Heuristic) coldOnly(tests []model.TestEntry) []Prediction {
	out := make([]Prediction, len(tests))
	for i, t := range tests {
		out[i] = h.coldStart(t)
	}
	return out
}

func (h *Heuristic) defaultFor(runner string) int {
	if v, ok := h.Defaults[runner]; ok {
		return v
	}
	return 1200
}
