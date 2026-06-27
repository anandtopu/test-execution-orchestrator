// Package llmhints generates a short, human-readable root-cause hint for each
// failure cluster using an LLM (ADR-0021). It mirrors the predictor integration
// pattern: an external service behind a seam, opt-in and default-off, with
// graceful no-hint degradation — the system never blocks on it and renders an
// em-dash wherever a hint is absent.
//
// The Runner here holds no Anthropic/HTTP knowledge: it depends only on the
// Summarizer and ClusterSource seams, so the orchestration logic is unit-tested
// with stubs and no network. The production Summarizer (Client, client.go)
// redacts secret material from each cluster BEFORE any external call, extending
// the ADR-0016 boundary to the hint path.
package llmhints

import (
	"context"
	"fmt"
	"log/slog"
)

const (
	defaultBatchSize   = 20
	defaultMaxClusters = 500
)

// Cluster is one failure cluster to summarize. Message/Stack are the cluster's
// representative_message / representative_stack — raw, unredacted; the
// Summarizer is responsible for redacting them before egress.
type Cluster struct {
	ID          string
	RepoID      string
	Message     string
	Stack       string
	Occurrences int64
}

// Hint is the LLM-generated root-cause summary for a cluster.
type Hint struct {
	ClusterID  string
	Category   string  // short bucket label, e.g. "assertion", "timeout", "network"
	Hint       string  // one to three sentences; the root-cause explanation
	Confidence float64 // 0..1, clamped by the Summarizer
}

// Summarizer turns clusters into hints. The production implementation (Client)
// calls Claude; tests use a stub. Implementations MUST redact secret material
// from Cluster.Message/Stack before any external call, and SHOULD be
// best-effort: a per-cluster failure yields no Hint for that cluster (omitted
// from the result) rather than failing the whole batch.
type Summarizer interface {
	Summarize(ctx context.Context, clusters []Cluster) ([]Hint, error)
}

// ClusterSource reads clusters that still need a hint and persists results.
type ClusterSource interface {
	// PendingClusters returns clusters with no root_cause_hint yet (or all
	// clusters when restale is true), highest-impact first, bounded to limit.
	PendingClusters(ctx context.Context, restale bool, limit int) ([]Cluster, error)
	// SaveHint writes the hint back. Implementations MUST guard on
	// root_cause_hint IS NULL (unless restale) so a re-run is idempotent and
	// never re-bills an already-hinted cluster.
	SaveHint(ctx context.Context, h Hint, restale bool) error
}

// Runner orchestrates one llm-hints pass: load pending clusters, summarize in
// batches, persist. Best-effort: a per-batch summarize error or per-cluster save
// error is logged and counted but never aborts the pass (matching the
// backfill/digest idioms). Returns a non-nil error only on a fatal failure (the
// initial cluster load).
type Runner struct {
	Clusters   ClusterSource
	Summarizer Summarizer
	Logger     *slog.Logger

	// Restale re-summarizes clusters that already have a hint (use after a
	// prompt or model change). Default false: only NULL-hint clusters are
	// loaded and written.
	Restale bool

	// BatchSize bounds how many clusters go to the Summarizer per call.
	// <= 0 defaults to defaultBatchSize.
	BatchSize int

	// MaxClusters caps total clusters processed in one pass.
	// <= 0 defaults to defaultMaxClusters.
	MaxClusters int

	// DryRun loads + summarizes but never persists.
	DryRun bool
}

// Stats summarizes one llm-hints pass.
type Stats struct {
	Scanned int // clusters handed to the summarizer
	Hinted  int // clusters that got a hint (persisted, or would be under dry-run)
	Skipped int // clusters the summarizer returned no usable hint for
	Errors  int // per-batch / per-cluster failures that did not abort the pass
	DryRun  bool
}

// Run executes one pass and returns its aggregated stats.
func (r *Runner) Run(ctx context.Context) (Stats, error) {
	logger := r.Logger
	if logger == nil {
		logger = slog.Default()
	}
	batchSize := r.BatchSize
	if batchSize <= 0 {
		batchSize = defaultBatchSize
	}
	maxClusters := r.MaxClusters
	if maxClusters <= 0 {
		maxClusters = defaultMaxClusters
	}

	stats := Stats{DryRun: r.DryRun}

	clusters, err := r.Clusters.PendingClusters(ctx, r.Restale, maxClusters)
	if err != nil {
		return stats, fmt.Errorf("llm-hints load pending clusters: %w", err)
	}

	for start := 0; start < len(clusters); start += batchSize {
		end := min(start+batchSize, len(clusters))
		batch := clusters[start:end]
		stats.Scanned += len(batch)

		hints, err := r.Summarizer.Summarize(ctx, batch)
		if err != nil {
			// A whole-batch failure (config/transport class). Count every
			// cluster in the batch as an error and move on.
			stats.Errors += len(batch)
			logger.Warn("llm-hints summarize batch failed", "size", len(batch), "err", err)
			continue
		}

		byID := make(map[string]Hint, len(hints))
		for _, h := range hints {
			byID[h.ClusterID] = h
		}

		for _, c := range batch {
			h, ok := byID[c.ID]
			if !ok || h.Hint == "" {
				stats.Skipped++
				continue
			}
			if r.DryRun {
				stats.Hinted++
				continue
			}
			if err := r.Clusters.SaveHint(ctx, h, r.Restale); err != nil {
				stats.Errors++
				logger.Warn("llm-hints save failed", "cluster", c.ID, "err", err)
				continue
			}
			stats.Hinted++
		}
	}

	logger.Info("llm-hints done",
		"scanned", stats.Scanned,
		"hinted", stats.Hinted,
		"skipped", stats.Skipped,
		"errors", stats.Errors,
		"dry_run", stats.DryRun,
	)
	return stats, nil
}
