package resultpipeline

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"time"

	chdriver "github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Backfiller re-clusters historical failures that predate (or were missed by)
// the live OTLP clustering path.
//
// The live path (otlp.go Export -> Cluster.UpsertCluster) upserts a
// teo.failure_clusters row but never back-links the originating
// teo.test_executions row; only the worker gRPC path (internal/grpcsvc) sets
// failure_cluster_id inline. As a result, every failure that arrived via OTLP
// has failure_cluster_id = NULL. This job closes that gap: it scans failed
// executions with a trace id but no cluster assignment, fetches the stack trace
// for that trace from ClickHouse span_events, computes the fingerprint via the
// SAME FingerprintStack path used on live ingest, and writes the cluster id back
// onto the execution row.
//
// IMPORTANT: backfill is a back-link-ONLY operation. The failures it targets were
// already counted once at live ingest (otlp.go Export -> UpsertCluster), so it
// resolves the cluster id via the count-free ClusterResolver.ClusterFor (NOT the
// occurrence-incrementing UpsertCluster) to avoid double-counting occurrences,
// which drive cluster ranking (GraphQL occurrences field, spatial-map y-axis,
// GitHub check-run top-3).
//
// Seams (StackSource, ClusterResolver, ExecSource) keep the core Run() logic
// unit-testable without Postgres or ClickHouse.
type Backfiller struct {
	Execs   ExecSource
	Stacks  StackSource
	Cluster ClusterResolver
	Logger  *slog.Logger

	// DryRun computes fingerprints (proving the scan + stack fetch work) but
	// performs no upserts or assignments.
	DryRun bool

	// Since bounds the scan to executions started within this window. Zero means
	// scan all historical failures.
	Since time.Duration
}

// ExecRow is one candidate failed execution to (re)cluster.
type ExecRow struct {
	ExecID  string
	RepoID  string
	TraceID string
}

// ExecSource reads candidate failures and writes back cluster assignments.
type ExecSource interface {
	// PendingFailures returns failed/errored/timed_out executions that have a
	// trace id but no failure_cluster_id, started within `since` (zero = all).
	PendingFailures(ctx context.Context, since time.Duration) ([]ExecRow, error)
	// AssignCluster back-links an execution to a cluster. Implementations MUST
	// guard on failure_cluster_id IS NULL so a re-run is idempotent.
	AssignCluster(ctx context.Context, execID, clusterID string) error
}

// StackSource fetches the stack trace + representative message for a trace id.
// An empty stack (no error span / span_events TTL expired) is not an error.
type StackSource interface {
	StackFor(ctx context.Context, traceID string) (stack, message string, err error)
}

// ClusterResolver fingerprints a stack and resolves its failure cluster id
// WITHOUT incrementing occurrences (insert-or-lookup, refreshing last_seen only).
// *Cluster (cluster.go) satisfies this via ClusterFor. Backfill must NOT use the
// occurrence-incrementing UpsertCluster: its target failures were already counted
// at live ingest, so re-counting here would inflate cluster ranking.
type ClusterResolver interface {
	ClusterFor(ctx context.Context, repoID, stack, message string) (string, error)
}

// Stats summarizes a backfill pass.
type Stats struct {
	Scanned              int
	Assigned             int
	SkippedNoStack       int // trace had no recoverable stack (e.g. TTL expired)
	SkippedNoFingerprint int // stack present but FingerprintStack reduced it to ""
	Errors               int // per-row errors that did not abort the run
	DryRun               bool

	// PreviewClusters is the number of DISTINCT clusters (by stack fingerprint)
	// a dry-run would have touched. It is only populated when DryRun is true and
	// lets an operator gauge the blast radius before committing the back-link
	// pass. Outside dry-run it stays zero (the real path tracks clusters via the
	// per-trace cache, not a distinct count).
	PreviewClusters int
}

// Run scans pending failures and assigns each to a cluster. Per-row errors are
// logged and counted but do not abort the pass (matching the digest/flake job
// idioms). Returns the aggregated stats and a non-nil error only on a fatal
// failure (the initial scan).
func (b *Backfiller) Run(ctx context.Context) (Stats, error) {
	stats := Stats{DryRun: b.DryRun}
	logger := b.Logger
	if logger == nil {
		logger = slog.Default()
	}

	rows, err := b.Execs.PendingFailures(ctx, b.Since)
	if err != nil {
		return stats, fmt.Errorf("backfill scan pending failures: %w", err)
	}

	// Per-trace caches. Many executions (subtests, retries) share an
	// otel_trace_id; without deduping we would issue one ClickHouse QueryRow and
	// one cluster resolve per execution for an identical fingerprint. Cache the
	// fetched stack/message and the resolved cluster id keyed by trace id so N
	// executions on one trace cost one CH query and one cluster resolve.
	type cachedStack struct {
		stack, message string
		err            error
	}
	stackCache := make(map[string]cachedStack)
	clusterCache := make(map[string]string) // trace_id -> resolved cluster id

	// previewFingerprints accumulates the distinct stack fingerprints a dry-run
	// would have resolved, so PreviewClusters reports the blast radius (how many
	// failure_clusters rows the real pass would touch) without any mutation.
	previewFingerprints := make(map[string]struct{})

	for _, row := range rows {
		stats.Scanned++

		cs, ok := stackCache[row.TraceID]
		if !ok {
			cs.stack, cs.message, cs.err = b.Stacks.StackFor(ctx, row.TraceID)
			stackCache[row.TraceID] = cs
		}
		if cs.err != nil {
			stats.Errors++
			logger.Warn("backfill stack fetch failed", "exec", row.ExecID, "trace", row.TraceID, "err", cs.err)
			continue
		}
		stack, message := cs.stack, cs.message
		if stack == "" {
			stats.SkippedNoStack++
			continue
		}

		if b.DryRun {
			// Prove the fingerprint path works without mutating anything. Count
			// distinct non-empty fingerprints so PreviewClusters reflects how
			// many failure_clusters rows the real pass would touch.
			if fp, _ := FingerprintStack(stack); fp == "" {
				stats.SkippedNoFingerprint++
			} else {
				previewFingerprints[fp] = struct{}{}
			}
			continue
		}

		clusterID, cached := clusterCache[row.TraceID]
		if !cached {
			clusterID, err = b.Cluster.ClusterFor(ctx, row.RepoID, stack, message)
			if err != nil {
				stats.Errors++
				logger.Warn("backfill resolve cluster failed", "exec", row.ExecID, "err", err)
				continue
			}
			clusterCache[row.TraceID] = clusterID
		}
		if clusterID == "" {
			// Fingerprint reduced to empty; nothing to link.
			stats.SkippedNoFingerprint++
			continue
		}

		if err := b.Execs.AssignCluster(ctx, row.ExecID, clusterID); err != nil {
			stats.Errors++
			logger.Warn("backfill assign cluster failed", "exec", row.ExecID, "cluster", clusterID, "err", err)
			continue
		}
		stats.Assigned++
	}

	stats.PreviewClusters = len(previewFingerprints)

	logger.Info("backfill-clusters done",
		"scanned", stats.Scanned,
		"assigned", stats.Assigned,
		"skipped_no_stack", stats.SkippedNoStack,
		"skipped_no_fingerprint", stats.SkippedNoFingerprint,
		"errors", stats.Errors,
		"preview_clusters", stats.PreviewClusters,
		"dry_run", stats.DryRun,
	)
	return stats, nil
}

// --- production seams -------------------------------------------------------

// PGExecSource is the production ExecSource backed by Postgres.
type PGExecSource struct {
	Pool *pgxpool.Pool
}

// pendingFailuresPageSize bounds each keyset page so a --since=0 (all-history)
// scan over a large fleet never materializes an unbounded result set in one
// round-trip. The default --since is bounded (720h in Helm); this guards the
// all-history case.
const pendingFailuresPageSize = 5000

// PendingFailures selects failed executions with a trace id but no cluster.
// The te_outcome_idx partial index covers the outcome filter. The scan is
// keyset-paginated on te.id (the cursor is the last id seen) so the whole
// candidate set is fetched in bounded pages rather than one unbounded query.
func (s PGExecSource) PendingFailures(ctx context.Context, since time.Duration) ([]ExecRow, error) {
	secs := int64(since.Seconds())
	var out []ExecRow
	cursor := "00000000-0000-0000-0000-000000000000"
	for {
		page, last, err := s.pendingFailuresPage(ctx, secs, cursor)
		if err != nil {
			return nil, err
		}
		out = append(out, page...)
		if len(page) < pendingFailuresPageSize {
			break
		}
		cursor = last
	}
	return out, nil
}

// pendingFailuresPage fetches one keyset page of candidates with te.id > after,
// ordered by te.id, returning the rows and the last id seen (the next cursor).
func (s PGExecSource) pendingFailuresPage(ctx context.Context, secs int64, after string) ([]ExecRow, string, error) {
	rows, err := s.Pool.Query(ctx, `
        SELECT te.id::text, r.repo_id::text, COALESCE(te.otel_trace_id, '')
        FROM teo.test_executions te
        JOIN teo.shards sh ON sh.id = te.shard_id
        JOIN teo.runs r ON r.id = sh.run_id
        WHERE te.failure_cluster_id IS NULL
          AND te.outcome IN ('failed','errored','timed_out')
          AND te.otel_trace_id IS NOT NULL
          AND te.otel_trace_id <> ''
          AND ($1 = 0 OR te.started_at > now() - make_interval(secs => $1))
          AND te.id > $2::uuid
        ORDER BY te.id
        LIMIT $3
    `, secs, after, pendingFailuresPageSize)
	if err != nil {
		return nil, "", fmt.Errorf("query pending failures: %w", err)
	}
	defer rows.Close()

	var out []ExecRow
	var last string
	for rows.Next() {
		var e ExecRow
		if err := rows.Scan(&e.ExecID, &e.RepoID, &e.TraceID); err != nil {
			return nil, "", fmt.Errorf("scan pending failure: %w", err)
		}
		out = append(out, e)
		last = e.ExecID
	}
	if err := rows.Err(); err != nil {
		return nil, "", fmt.Errorf("iterate pending failures: %w", err)
	}
	return out, last, nil
}

// AssignCluster back-links the execution, guarding on NULL for idempotency so a
// re-run never double-counts or overwrites an existing assignment.
func (s PGExecSource) AssignCluster(ctx context.Context, execID, clusterID string) error {
	_, err := s.Pool.Exec(ctx, `
        UPDATE teo.test_executions
        SET failure_cluster_id = $2
        WHERE id = $1 AND failure_cluster_id IS NULL
    `, execID, clusterID)
	if err != nil {
		return fmt.Errorf("assign cluster %s to exec %s: %w", clusterID, execID, err)
	}
	return nil
}

// CHStackSource is the production StackSource backed by ClickHouse span_events.
type CHStackSource struct {
	CH chdriver.Conn
}

// StackFor returns the exception stack + message for the error span of a trace.
// status_code 2 == ERROR (see otlp.go statusToCode). A trace with no error span
// (or whose span_events have aged out of the 30d TTL) yields ("", "", nil).
func (s CHStackSource) StackFor(ctx context.Context, traceID string) (string, string, error) {
	var stack, message string
	// Map(String,String) access returns '' (never NULL) for a missing key, so a
	// plain coalesce(attributes[...], status_message) would never fall back. Use
	// an explicit emptiness check to mirror the live path's
	// firstNonEmpty(exception.message, status_message) semantics (otlp.go).
	row := s.CH.QueryRow(ctx, `
        SELECT argMax(attributes['exception.stacktrace'], end_time) AS stack,
               argMax(if(empty(attributes['exception.message']), status_message, attributes['exception.message']), end_time) AS message
        FROM teo.span_events
        WHERE trace_id = ? AND status_code = 2
        GROUP BY trace_id
    `, traceID)
	if err := row.Scan(&stack, &message); err != nil {
		// No matching error span -> treat as "no stack", not a hard error. The
		// ClickHouse driver returns sql.ErrNoRows for an empty GROUP BY result.
		if errors.Is(err, sql.ErrNoRows) {
			return "", "", nil
		}
		return "", "", fmt.Errorf("clickhouse stack for trace %s: %w", traceID, err)
	}
	return stack, message, nil
}
