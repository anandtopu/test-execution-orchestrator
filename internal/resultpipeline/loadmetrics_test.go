package resultpipeline

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// These unit tests exercise the pure load-test helpers without Docker (the
// integration load test in otlp_loadtest_integration_test.go is the only
// build-tagged consumer). They also keep the helpers reachable in the default
// build so the `unused` linter doesn't flag them.

func TestPercentile(t *testing.T) {
	require.Equal(t, time.Duration(0), percentile(nil, 0.5), "empty → 0")
	require.Equal(t, 7*time.Millisecond, percentile([]time.Duration{7 * time.Millisecond}, 0.99), "single element")

	// Nearest-rank on 1..10 ms: rank = ceil(q*n), 1-based.
	durs := []time.Duration{
		10 * time.Millisecond, 3 * time.Millisecond, 7 * time.Millisecond,
		1 * time.Millisecond, 9 * time.Millisecond, 2 * time.Millisecond,
		8 * time.Millisecond, 4 * time.Millisecond, 6 * time.Millisecond, 5 * time.Millisecond,
	}
	require.Equal(t, 5*time.Millisecond, percentile(durs, 0.50), "p50 → 5th of 10")
	require.Equal(t, 10*time.Millisecond, percentile(durs, 0.95), "p95 → 10th")
	require.Equal(t, 10*time.Millisecond, percentile(durs, 0.99), "p99 → 10th")
	require.Equal(t, 1*time.Millisecond, percentile(durs, 0.0), "q=0 → min")
	require.Equal(t, 10*time.Millisecond, percentile(durs, 1.0), "q=1 → max")
	// q is clamped into [0,1].
	require.Equal(t, 1*time.Millisecond, percentile(durs, -0.5), "q<0 clamps to min")
	require.Equal(t, 10*time.Millisecond, percentile(durs, 1.5), "q>1 clamps to max")

	// The caller's slice is not mutated (percentile sorts a copy).
	orig := []time.Duration{3, 1, 2}
	_ = percentile(orig, 0.5)
	require.Equal(t, []time.Duration{3, 1, 2}, orig, "input slice must be untouched")
}

func TestLoadTestRowCount(t *testing.T) {
	t.Setenv("TEO_LOADTEST_ROWS", "")
	require.Equal(t, 1_000_000, loadTestRowCount(false), "full default")
	require.Equal(t, 10_000, loadTestRowCount(true), "-short default")

	t.Setenv("TEO_LOADTEST_ROWS", "250000")
	require.Equal(t, 250_000, loadTestRowCount(false), "env override wins over both defaults")
	require.Equal(t, 250_000, loadTestRowCount(true), "env override wins under -short too")

	t.Setenv("TEO_LOADTEST_ROWS", "notanumber")
	require.Equal(t, 1_000_000, loadTestRowCount(false), "malformed → size-appropriate default")
	t.Setenv("TEO_LOADTEST_ROWS", "-5")
	require.Equal(t, 10_000, loadTestRowCount(true), "non-positive → default")
}

func TestLoadTestBatchSize(t *testing.T) {
	t.Setenv("TEO_LOADTEST_BATCH", "")
	require.Equal(t, 5_000, loadTestBatchSize(), "default")
	t.Setenv("TEO_LOADTEST_BATCH", "1000")
	require.Equal(t, 1_000, loadTestBatchSize(), "env override")
	t.Setenv("TEO_LOADTEST_BATCH", "0")
	require.Equal(t, 5_000, loadTestBatchSize(), "non-positive → default")
	t.Setenv("TEO_LOADTEST_BATCH", "junk")
	require.Equal(t, 5_000, loadTestBatchSize(), "malformed → default")
}

func TestSynthRows(t *testing.T) {
	require.Nil(t, synthRows(0, 0), "n<=0 → nil")
	require.Nil(t, synthRows(5, -1), "negative n → nil")

	rows := synthRows(0, 100)
	require.Len(t, rows, 100)

	// Global offset makes successive batches DISTINCT — the bug that let the
	// span_events TTL collapse repeated batches to one. trace_id/start_time are
	// keyed off the global index, so batch [0,100) and [100,200) must not overlap.
	next := synthRows(100, 100)
	seen := make(map[string]bool, 200)
	for _, r := range append(append([]rowSpan{}, rows...), next...) {
		require.False(t, seen[r.traceID], "duplicate trace_id %s across batches", r.traceID)
		seen[r.traceID] = true
	}
	require.Len(t, seen, 200, "all 200 rows globally unique")

	// start_time must sit inside the 30-day retention TTL (and not in the
	// future), or a merge would reap the rows mid-test.
	now := time.Now()
	for _, r := range rows {
		require.True(t, r.startTime.After(now.Add(-30*24*time.Hour)), "row within TTL window")
		require.True(t, r.startTime.Before(now.Add(time.Hour)), "row not in the future")
	}
}
