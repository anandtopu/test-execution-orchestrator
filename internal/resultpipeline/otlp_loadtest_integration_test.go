//go:build integration

package resultpipeline

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/teo-dev/teo/internal/testch"
)

// TestClickHouseSpanInsertLoad drives the production OTLP write path
// (OTLPReceiver.writeSpans → native conn.PrepareBatch) against a real
// ClickHouse and reports insert throughput plus p50/p95/p99 per-batch latency.
//
// Row count defaults to ~1M (10k under -short) and is overridable via
// TEO_LOADTEST_ROWS / TEO_LOADTEST_BATCH. See docs/operations/clickhouse-load-test.md.
//
// NOT EXECUTED in the authoring environment (Docker unavailable). It is
// build-tag-gated and compiles under `go vet -tags=integration`.
func TestClickHouseSpanInsertLoad(t *testing.T) {
	conn, _, cleanup := testch.Start(t)
	t.Cleanup(cleanup)

	r := &OTLPReceiver{CH: conn}

	total := loadTestRowCount(testing.Short())
	batchSize := loadTestBatchSize()
	if batchSize > total {
		batchSize = total
	}

	ctx := context.Background()
	var insertWall time.Duration
	durs := make([]time.Duration, 0, (total/batchSize)+1)

	inserted := 0
	for inserted < total {
		n := batchSize
		if remaining := total - inserted; remaining < n {
			n = remaining
		}
		// Build rows BEFORE timing so generation cost never pollutes the
		// throughput / latency numbers.
		rows := synthRows(n)

		start := time.Now()
		err := r.writeSpans(ctx, rows)
		d := time.Since(start)
		require.NoErrorf(t, err, "writeSpans batch at offset %d", inserted)

		insertWall += d
		durs = append(durs, d)
		inserted += n
	}

	// Exact count: span_events is a plain MergeTree, no row collapsing.
	var count uint64
	require.NoError(t, conn.QueryRow(ctx,
		`SELECT count() FROM teo.span_events`).Scan(&count))
	require.Equal(t, uint64(total), count, "row count must equal total inserted")

	throughput := float64(total) / insertWall.Seconds()
	p50 := percentile(durs, 0.50)
	p95 := percentile(durs, 0.95)
	p99 := percentile(durs, 0.99)

	require.Greater(t, throughput, 0.0, "throughput must be positive")
	require.GreaterOrEqual(t, p99, p50, "p99 must be >= p50")

	t.Logf("load: rows=%d batch=%d elapsed=%s throughput=%.0f rows/s p50=%s p95=%s p99=%s",
		total, batchSize, insertWall, throughput, p50, p95, p99)
}

// TestClickHouseHarnessSmoke is a fast round-trip sanity check on the testch
// harness: start ClickHouse, apply migrations, insert one row via the
// production write path, read it back. NOT EXECUTED here (Docker unavailable).
func TestClickHouseHarnessSmoke(t *testing.T) {
	conn, _, cleanup := testch.Start(t)
	t.Cleanup(cleanup)

	r := &OTLPReceiver{CH: conn}
	ctx := context.Background()

	require.NoError(t, r.writeSpans(ctx, synthRows(1)))

	var count uint64
	require.NoError(t, conn.QueryRow(ctx,
		`SELECT count() FROM teo.span_events`).Scan(&count))
	require.Equal(t, uint64(1), count)
}
