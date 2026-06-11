package resultpipeline

import (
	"fmt"
	"os"
	"sort"
	"strconv"
	"time"

	"github.com/google/uuid"
)

// loadTestRowCount is the total number of span_events rows the ClickHouse
// load test inserts. The default targets the S-02-03 / T-02-03-02 acceptance
// criterion of ~1M rows; `-short` drops it to a CI-friendly 10k, and the
// TEO_LOADTEST_ROWS env var overrides both (a malformed/non-positive value
// falls back to the size-appropriate default).
//
// It lives in this no-build-tag file (alongside percentile and synthRows) so
// the pure logic stays unit-testable without Docker — the integration load
// test in otlp_loadtest_integration_test.go is the only Docker-gated consumer.
func loadTestRowCount(short bool) int {
	def := 1_000_000
	if short {
		def = 10_000
	}
	if v := os.Getenv("TEO_LOADTEST_ROWS"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			return def
		}
		return n
	}
	return def
}

// loadTestBatchSize is the per-batch row count for the load test. ClickHouse
// favors large batched inserts; 5k is a sane default. TEO_LOADTEST_BATCH
// overrides it (a malformed/non-positive value falls back to the default).
func loadTestBatchSize() int {
	const def = 5_000
	if v := os.Getenv("TEO_LOADTEST_BATCH"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			return def
		}
		return n
	}
	return def
}

// percentile returns the q-th percentile (q in [0,1]) of durs using the
// nearest-rank method on a copy of the input (the caller's slice is not
// mutated). It returns 0 for an empty slice, the sole element for a
// single-element slice, and clamps q into [0,1] so callers can't index out of
// range. Monotone in q for a fixed input.
func percentile(durs []time.Duration, q float64) time.Duration {
	n := len(durs)
	if n == 0 {
		return 0
	}
	if n == 1 {
		return durs[0]
	}
	if q < 0 {
		q = 0
	}
	if q > 1 {
		q = 1
	}
	sorted := make([]time.Duration, n)
	copy(sorted, durs)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })

	// Nearest-rank: rank = ceil(q * n), 1-based, clamped to [1, n].
	rank := int(q*float64(n) + 0.999999999)
	if rank < 1 {
		rank = 1
	}
	if rank > n {
		rank = n
	}
	return sorted[rank-1]
}

// synthRows builds n deterministic-shaped synthetic span_events rows for the
// load test, numbered globally from `offset` (so successive batches produce
// DISTINCT rows — trace_id/span_id/start_time are derived from the global
// index, not a per-batch 0..n counter). Each row carries a valid UUID
// test_id/run_id (so parseUUIDOrZero round-trips a real value), a small
// attribute map, and a realistic ~5ms span duration. Rows are built outside
// the timed region of the load test so generation cost never pollutes the
// throughput/latency numbers.
//
// Global uniqueness matters: span_events' ORDER BY (run_id, trace_id,
// start_time) means batches must not repeat the same index sequence.
//
// start_time MUST be recent. teo.span_events carries
// `TTL toDate(start_time) + INTERVAL 30 DAY DELETE`, so any row older than 30
// days is dropped the next time a merge touches its part — and every insert
// triggers merges. A fixed past date (e.g. 2026-01-01) makes each new batch's
// merge silently delete the previous batch's now-expired rows, so the table
// only ever holds the latest batch. Anchoring at now()-24h keeps rows safely
// inside the retention window (and not in the future) for the run's duration.
func synthRows(offset, n int) []rowSpan {
	if n <= 0 {
		return nil
	}
	base := time.Now().Add(-24 * time.Hour)
	rows := make([]rowSpan, 0, n)
	for j := 0; j < n; j++ {
		i := offset + j
		testID := uuid.New()
		runID := uuid.New()
		start := base.Add(time.Duration(i) * time.Millisecond)
		rows = append(rows, rowSpan{
			traceID:       fmt.Sprintf("%032x", i),
			spanID:        fmt.Sprintf("%016x", i),
			parentSpanID:  "",
			testID:        testID.String(),
			runID:         runID.String(),
			name:          fmt.Sprintf("test_synth_%d", i),
			kind:          1,
			startTime:     start,
			endTime:       start.Add(5 * time.Millisecond),
			statusCode:    1,
			statusMessage: "",
			attrKeys:      []string{"teo.test_id", "teo.run_id", "teo.synthetic"},
			attrVals:      []string{testID.String(), runID.String(), "true"},
		})
	}
	return rows
}
