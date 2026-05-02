-- TEO initial ClickHouse schema. Source of truth: docs/architecture/data-model.md §2.

CREATE DATABASE IF NOT EXISTS teo;

-- ---------------------------------------------------------------- test_runs
CREATE TABLE IF NOT EXISTS teo.test_runs (
    run_id UUID,
    shard_id UUID,
    test_id UUID,
    test_path String,
    test_name String,
    repo_id UUID,
    commit_sha String,
    branch String,
    runner LowCardinality(String) DEFAULT 'pytest',
    attempt UInt8,
    outcome Enum8('passed'=1,'failed'=2,'skipped'=3,'errored'=4,'timed_out'=5,'interrupted'=6),
    duration_ms UInt32,
    worker_id String,
    capacity_type LowCardinality(String) DEFAULT 'unknown',  -- spot | on_demand | unknown
    started_at DateTime64(3),
    finished_at DateTime64(3)
) ENGINE = MergeTree
PARTITION BY toYYYYMM(started_at)
ORDER BY (repo_id, test_id, started_at)
TTL toDate(started_at) + INTERVAL 365 DAY DELETE;

-- ---------------------------------------------------------------- span_events
CREATE TABLE IF NOT EXISTS teo.span_events (
    trace_id String,
    span_id String,
    parent_span_id String,
    test_id UUID,
    run_id UUID,
    name String,
    kind Enum8('internal'=0,'server'=1,'client'=2,'producer'=3,'consumer'=4),
    start_time DateTime64(9),
    end_time DateTime64(9),
    status_code Enum8('unset'=0,'ok'=1,'error'=2),
    status_message String,
    attributes Map(String, String),
    event_times Array(DateTime64(9)),
    event_names Array(String),
    event_attributes Array(Map(String, String))
) ENGINE = MergeTree
PARTITION BY toYYYYMM(start_time)
ORDER BY (run_id, trace_id, start_time)
TTL toDate(start_time) + INTERVAL 30 DAY DELETE;

-- ---------------------------------------------------------------- flake_observations (rolled-up by day)
CREATE TABLE IF NOT EXISTS teo.flake_observations (
    day Date,
    test_id UUID,
    repo_id UUID,
    attempts UInt32,
    failures UInt32,
    pass_rate Float32,
    wilson_lower Float32,
    wilson_upper Float32
) ENGINE = SummingMergeTree
PARTITION BY toYYYYMM(day)
ORDER BY (repo_id, test_id, day);

-- ---------------------------------------------------------------- run_summary materialized view
CREATE MATERIALIZED VIEW IF NOT EXISTS teo.mv_run_summary
ENGINE = SummingMergeTree
ORDER BY (repo_id, run_id)
POPULATE
AS SELECT
    run_id,
    repo_id,
    countIf(outcome = 'passed')   AS passed,
    countIf(outcome = 'failed')   AS failed,
    countIf(outcome = 'errored')  AS errored,
    countIf(outcome = 'skipped')  AS skipped,
    countIf(outcome = 'timed_out') AS timed_out,
    countIf(outcome = 'interrupted') AS interrupted,
    sum(duration_ms)              AS total_duration_ms,
    quantile(0.5)(duration_ms)    AS p50_duration_ms,
    quantile(0.95)(duration_ms)   AS p95_duration_ms
FROM teo.test_runs
GROUP BY run_id, repo_id;
