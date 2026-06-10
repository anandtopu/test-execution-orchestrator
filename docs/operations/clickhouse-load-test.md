# ClickHouse span_events load test (S-02-03 / T-02-03-02)

This document covers the load-test harness for the OTLP span-ingest write path
(`internal/resultpipeline/otlp.go` → `OTLPReceiver.writeSpans`, the native
`conn.PrepareBatch` insert into `teo.span_events`) and records the
throughput / latency numbers it produces.

## What it measures

`TestClickHouseSpanInsertLoad` (in
`internal/resultpipeline/otlp_loadtest_integration_test.go`, build tag
`integration`) inserts ~1M synthetic `span_events` rows in batches via the
**exact production write path** (`writeSpans`) against a real ClickHouse
spun up by `internal/testch` (testcontainers, `clickhouse-server:24.8-alpine`).

For each batch it:

1. builds the synthetic rows **before** the timer starts (`synthRows`), so row
   generation never pollutes the measurement;
2. times **only** `writeSpans(ctx, batch)`;
3. records the per-batch wall-clock duration.

After all batches it computes:

- **throughput** — total rows ÷ total insert wall-clock (rows/sec);
- **p50 / p95 / p99** per-batch insert latency (`percentile`, nearest-rank);
- and asserts `SELECT count() FROM teo.span_events` equals the total inserted
  (the table is a plain `MergeTree`, so the count is exact).

## How to run it

Docker is required (testcontainers pulls and runs ClickHouse). From the repo
root:

```bash
# Full ~1M-row run (give it room — defaults to a 20m test timeout):
go test -tags=integration -run TestClickHouseSpanInsertLoad \
    -timeout 20m ./internal/resultpipeline/

# Fast CI smoke (10k rows under -short):
go test -tags=integration -short -run TestClickHouseSpanInsertLoad \
    ./internal/resultpipeline/
```

### Knobs

| Setting             | Env / flag             | Default (full) | Default (`-short`) |
| ------------------- | ---------------------- | -------------- | ------------------ |
| Total rows          | `TEO_LOADTEST_ROWS`    | `1000000`      | `10000`            |
| Batch size          | `TEO_LOADTEST_BATCH`   | `5000`         | `5000`             |

A malformed or non-positive env value falls back to the size-appropriate
default. Example — 5M rows in 10k batches:

```bash
TEO_LOADTEST_ROWS=5000000 TEO_LOADTEST_BATCH=10000 \
  go test -tags=integration -run TestClickHouseSpanInsertLoad \
    -timeout 30m ./internal/resultpipeline/
```

The test logs a line of the form:

```
load: rows=1000000 batch=5000 elapsed=… throughput=… rows/s p50=… p95=… p99=…
```

## Recorded results

Record the numbers from a representative run here so regressions are visible in
review. Capture the machine class (this matters — throughput is hardware- and
disk-bound).

| Date       | Machine / runner        | Rows    | Batch | Throughput (rows/s) | p50 batch | p95 batch | p99 batch |
| ---------- | ----------------------- | ------- | ----- | ------------------- | --------- | --------- | --------- |
| _not yet run_ | _CI testcontainers (TODO)_ | 1000000 | 5000  | _TODO_              | _TODO_    | _TODO_    | _TODO_    |

> **Status:** the harness compiles and is correct (verified via
> `go vet -tags=integration ./internal/testch/... ./internal/resultpipeline/...`)
> but has **not been executed** — Docker is unavailable on the authoring
> machine. The table above is intentionally left as `TODO` rather than
> populated with fabricated numbers. Fill it in from the first CI
> testcontainers run.

## Related metrics

The production path already emits `teo_clickhouse_insert_seconds` (histogram)
and `teo_clickhouse_inserts_total` / `teo_clickhouse_insert_failures_total`
(counters) from `internal/metrics`. The load test measures `writeSpans`
directly rather than scraping these, but the histogram buckets in
`metrics.go` should be kept consistent with the latencies this test reveals.
