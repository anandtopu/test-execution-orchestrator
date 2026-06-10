// Package resultpipeline does failure clustering and (later) OTLP ingest +
// ClickHouse fanout. The clustering algorithm fingerprints stack traces by
// language: Python tracebacks are normalized; everything else falls back to a
// hash of the last 5 stripped lines (per docs/README.md open question).
package resultpipeline

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Cluster computes a stack fingerprint and inserts/updates a failure_cluster row.
type Cluster struct {
	Pool *pgxpool.Pool
}

// pythonFrame matches a Python traceback frame line:
//
//	File "/path/to/file.py", line 42, in func_name
var pythonFrame = regexp.MustCompile(`File "([^"]+)", line (\d+), in (\S+)`)

// numericLiteral strips numeric literals (line numbers in non-frame text) so
// noise like "took 3.4 seconds" doesn't fragment clusters.
var numericLiteral = regexp.MustCompile(`\b\d+(?:\.\d+)?\b`)

// hexLike strips hex addresses (object pointers, hashes) for the same reason.
var hexLike = regexp.MustCompile(`0x[0-9a-fA-F]+|[0-9a-f]{16,}`)

// FingerprintStack returns a stable fingerprint and a normalized representative.
// The fingerprint is hex(sha256(normalized)).
func FingerprintStack(stack string) (fingerprint, normalized string) {
	if stack == "" {
		return "", ""
	}
	if isPythonTraceback(stack) {
		normalized = normalizePython(stack)
	} else {
		normalized = normalizeGeneric(stack)
	}
	sum := sha256.Sum256([]byte(normalized))
	fingerprint = hex.EncodeToString(sum[:])
	return
}

func isPythonTraceback(s string) bool {
	return strings.Contains(s, "Traceback (most recent call last)") || pythonFrame.MatchString(s)
}

func normalizePython(s string) string {
	var lines []string
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if m := pythonFrame.FindStringSubmatch(line); m != nil {
			// Drop the line number; keep file + function for stability across edits.
			lines = append(lines, "File "+m[1]+" in "+m[3])
			continue
		}
		// Final exception line (e.g., "AssertionError: foo")
		if i := strings.Index(line, ":"); i > 0 && !strings.HasPrefix(line, "  ") {
			head := line[:i]
			if isException(head) {
				lines = append(lines, head)
				continue
			}
		}
	}
	return strings.Join(lines, "\n")
}

func isException(name string) bool {
	// Heuristic: starts with uppercase letter and ends with "Error" / "Exception" / etc.
	if name == "" || name[0] < 'A' || name[0] > 'Z' {
		return false
	}
	return strings.HasSuffix(name, "Error") ||
		strings.HasSuffix(name, "Exception") ||
		strings.HasSuffix(name, "Failure") ||
		strings.HasSuffix(name, "Warning")
}

// normalizeGeneric strips numerics + hex from the last 5 non-empty lines.
func normalizeGeneric(s string) string {
	parts := strings.Split(s, "\n")
	lines := make([]string, 0, len(parts))
	for _, line := range parts {
		t := strings.TrimSpace(line)
		if t == "" {
			continue
		}
		t = hexLike.ReplaceAllString(t, "<HEX>")
		t = numericLiteral.ReplaceAllString(t, "<N>")
		lines = append(lines, t)
	}
	if len(lines) > 5 {
		lines = lines[len(lines)-5:]
	}
	return strings.Join(lines, "\n")
}

// UpsertCluster inserts or updates the failure_clusters row for this fingerprint
// and returns its ID.
func (c *Cluster) UpsertCluster(ctx context.Context, repoID, stack, message string) (string, error) {
	if c == nil || c.Pool == nil {
		return "", nil
	}
	fingerprint, normalized := FingerprintStack(stack)
	if fingerprint == "" {
		return "", nil
	}
	id := uuid.New().String()
	var got string
	err := c.Pool.QueryRow(ctx, `
        INSERT INTO teo.failure_clusters
            (id, repo_id, stack_fingerprint, representative_message, representative_stack)
        VALUES ($1, $2, $3, $4, $5)
        ON CONFLICT (repo_id, stack_fingerprint) DO UPDATE
        SET occurrences = teo.failure_clusters.occurrences + 1,
            last_seen = now()
        RETURNING id
    `, id, repoID, fingerprint, message, normalized).Scan(&got)
	return got, err
}

// ClusterFor resolves the failure cluster id for a stack WITHOUT incrementing
// occurrences. It is the count-free counterpart to UpsertCluster, used by the
// backfill job: those failures were already counted once at live OTLP ingest
// (otlp.go Export -> UpsertCluster), so re-counting them during a back-link-only
// pass would double-count and corrupt cluster ranking. If no row exists yet it
// inserts one (occurrences defaults to 1 via the column default, representing
// the single failure being linked); if a row already exists it returns the
// existing id and only refreshes last_seen, leaving occurrences untouched.
func (c *Cluster) ClusterFor(ctx context.Context, repoID, stack, message string) (string, error) {
	if c == nil || c.Pool == nil {
		return "", nil
	}
	fingerprint, normalized := FingerprintStack(stack)
	if fingerprint == "" {
		return "", nil
	}
	id := uuid.New().String()
	var got string
	err := c.Pool.QueryRow(ctx, `
        INSERT INTO teo.failure_clusters
            (id, repo_id, stack_fingerprint, representative_message, representative_stack)
        VALUES ($1, $2, $3, $4, $5)
        ON CONFLICT (repo_id, stack_fingerprint) DO UPDATE
        SET last_seen = now()
        RETURNING id
    `, id, repoID, fingerprint, message, normalized).Scan(&got)
	return got, err
}
