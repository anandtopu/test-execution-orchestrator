package llmhints

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Compile-time assertion that PGClusterSource satisfies the ClusterSource seam.
var _ ClusterSource = PGClusterSource{}

// PGClusterSource is the production ClusterSource backed by Postgres.
type PGClusterSource struct {
	Pool *pgxpool.Pool
}

// PendingClusters returns the highest-impact clusters that still need a hint
// (or all clusters when restale is true), bounded to limit. Ordering by
// occurrences DESC means that under a per-pass cap the most-seen failures get
// hints first.
func (s PGClusterSource) PendingClusters(ctx context.Context, restale bool, limit int) ([]Cluster, error) {
	if limit <= 0 {
		limit = defaultMaxClusters
	}
	rows, err := s.Pool.Query(ctx, `
        SELECT id::text, repo_id::text,
               COALESCE(representative_message, ''),
               COALESCE(representative_stack, ''),
               occurrences
        FROM teo.failure_clusters
        WHERE ($1 OR root_cause_hint IS NULL)
        ORDER BY occurrences DESC, last_seen DESC
        LIMIT $2
    `, restale, limit)
	if err != nil {
		return nil, fmt.Errorf("query pending clusters: %w", err)
	}
	defer rows.Close()

	var out []Cluster
	for rows.Next() {
		var c Cluster
		if err := rows.Scan(&c.ID, &c.RepoID, &c.Message, &c.Stack, &c.Occurrences); err != nil {
			return nil, fmt.Errorf("scan cluster: %w", err)
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate clusters: %w", err)
	}
	return out, nil
}

// SaveHint persists the hint. The NULL guard (unless restale) makes a re-run
// idempotent: a cluster already hinted by an earlier pass is never overwritten
// or re-billed.
func (s PGClusterSource) SaveHint(ctx context.Context, h Hint, restale bool) error {
	_, err := s.Pool.Exec(ctx, `
        UPDATE teo.failure_clusters
        SET root_cause_hint   = $2,
            hint_category     = $3,
            hint_confidence   = $4,
            hint_generated_at = now()
        WHERE id = $1 AND ($5 OR root_cause_hint IS NULL)
    `, h.ClusterID, h.Hint, h.Category, h.Confidence, restale)
	if err != nil {
		return fmt.Errorf("save hint for cluster %s: %w", h.ClusterID, err)
	}
	return nil
}
