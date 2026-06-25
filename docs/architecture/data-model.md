# TEO — Data Model

> ⚠️ **This is the original design draft (2026-04-30) and has drifted from the applied migrations.** For the **as-built** schema — including migrations 002–006 and the corrected `user_roles` keys — read [`schema.md`](schema.md) and [`er-diagram.md`](er-diagram.md). Kept for design rationale and sizing notes (§5); the migration SQL is the final word on structure.

**Status:** Superseded design draft (see banner above)
**Date:** 2026-04-30

Two stores: **Postgres** for transactional state (runs, shards, tests, flakes, ownership), **ClickHouse** for high-volume analytical data (span events, durations, flake-stats time series). S3 for cold archive.

This doc is the source of truth for the schema. Migrations live in `migrations/postgres/` and `migrations/clickhouse/`.

---

## 1. Postgres schema

All tables in schema `teo`. UUIDs are v7 (time-ordered). All tables have `created_at`, `updated_at` (triggered) — omitted from listings for brevity.

### `repos`
```sql
CREATE TABLE teo.repos (
  id UUID PRIMARY KEY,
  vcs TEXT NOT NULL CHECK (vcs IN ('github')),  -- v1: GitHub only
  full_name TEXT NOT NULL,        -- "owner/name"
  default_branch TEXT NOT NULL DEFAULT 'main',
  enabled BOOLEAN NOT NULL DEFAULT TRUE,
  UNIQUE(vcs, full_name)
);
```

### `runs`
```sql
CREATE TABLE teo.runs (
  id UUID PRIMARY KEY,
  repo_id UUID NOT NULL REFERENCES teo.repos(id),
  commit_sha TEXT NOT NULL,
  branch TEXT NOT NULL,
  triggered_by TEXT NOT NULL,                      -- "github-app", "cli", "rerun"
  trigger_actor TEXT,                              -- gh user login when known
  trigger_pr_number INT,
  status TEXT NOT NULL CHECK (status IN (
    'pending','planning','dispatching','running',
    'finalizing','succeeded','failed','cancelled')),
  budget_seconds INT,                              -- optional cap
  started_at TIMESTAMPTZ,
  finished_at TIMESTAMPTZ,
  total_duration_ms INT,
  worker_minutes_used NUMERIC(10,2),
  meta JSONB NOT NULL DEFAULT '{}'
);
CREATE INDEX runs_repo_started_idx ON teo.runs(repo_id, started_at DESC);
CREATE INDEX runs_commit_idx ON teo.runs(commit_sha);
```

### `shards`
```sql
CREATE TABLE teo.shards (
  id UUID PRIMARY KEY,
  run_id UUID NOT NULL REFERENCES teo.runs(id) ON DELETE CASCADE,
  index INT NOT NULL,
  worker_id TEXT,                                  -- pod name
  predicted_duration_ms INT NOT NULL,
  actual_duration_ms INT,
  status TEXT NOT NULL CHECK (status IN (
    'pending','running','succeeded','failed','lost')),
  test_count INT NOT NULL,
  started_at TIMESTAMPTZ,
  finished_at TIMESTAMPTZ,
  UNIQUE(run_id, index)
);
```

### `tests` (logical test identity)
```sql
CREATE TABLE teo.tests (
  id UUID PRIMARY KEY,
  repo_id UUID NOT NULL REFERENCES teo.repos(id),
  fingerprint TEXT NOT NULL,         -- see ADR-0010
  path TEXT NOT NULL,
  name TEXT NOT NULL,                -- fully-qualified
  params_hash TEXT NOT NULL DEFAULT '',
  owner_team TEXT,                   -- from CODEOWNERS resolution
  tags TEXT[] NOT NULL DEFAULT '{}',
  status TEXT NOT NULL CHECK (status IN ('active','quarantined','deleted')),
  first_seen TIMESTAMPTZ NOT NULL DEFAULT now(),
  last_seen TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE(repo_id, fingerprint)
);
CREATE INDEX tests_repo_path_idx ON teo.tests(repo_id, path);
CREATE INDEX tests_quarantined_idx ON teo.tests(repo_id) WHERE status = 'quarantined';
```

### `test_executions`
The transactional record of a single test run. (Span-level detail lives in ClickHouse.)

```sql
CREATE TABLE teo.test_executions (
  id UUID PRIMARY KEY,
  shard_id UUID NOT NULL REFERENCES teo.shards(id) ON DELETE CASCADE,
  test_id UUID NOT NULL REFERENCES teo.tests(id),
  attempt INT NOT NULL DEFAULT 1,
  outcome TEXT NOT NULL CHECK (outcome IN ('passed','failed','skipped','errored','timed_out')),
  duration_ms INT NOT NULL,
  otel_trace_id TEXT,
  failure_cluster_id UUID,
  started_at TIMESTAMPTZ NOT NULL,
  finished_at TIMESTAMPTZ NOT NULL,
  UNIQUE(shard_id, test_id, attempt)
);
CREATE INDEX te_test_started_idx ON teo.test_executions(test_id, started_at DESC);
CREATE INDEX te_outcome_idx ON teo.test_executions(outcome) WHERE outcome IN ('failed','errored');
```

### `failure_clusters`
```sql
CREATE TABLE teo.failure_clusters (
  id UUID PRIMARY KEY,
  repo_id UUID NOT NULL REFERENCES teo.repos(id),
  stack_fingerprint TEXT NOT NULL,
  representative_message TEXT NOT NULL,
  representative_stack TEXT NOT NULL,
  first_seen TIMESTAMPTZ NOT NULL DEFAULT now(),
  last_seen TIMESTAMPTZ NOT NULL DEFAULT now(),
  occurrences BIGINT NOT NULL DEFAULT 1,
  UNIQUE(repo_id, stack_fingerprint)
);
```

### `flake_records`
```sql
CREATE TABLE teo.flake_records (
  test_id UUID PRIMARY KEY REFERENCES teo.tests(id),
  flake_rate NUMERIC(5,4) NOT NULL,
  wilson_lower NUMERIC(5,4) NOT NULL,
  sample_size INT NOT NULL,
  category TEXT,                  -- 'order_dependent','timing','network','env','unknown'
  quarantined_at TIMESTAMPTZ,
  unquarantined_at TIMESTAMPTZ,
  evidence JSONB NOT NULL DEFAULT '{}'
);
```

### `users`, `api_keys`, `roles`
```sql
CREATE TABLE teo.users (
  id UUID PRIMARY KEY,
  email TEXT NOT NULL UNIQUE,
  display_name TEXT NOT NULL,
  oidc_subject TEXT UNIQUE,
  active BOOLEAN NOT NULL DEFAULT TRUE
);

CREATE TABLE teo.api_keys (
  id UUID PRIMARY KEY,
  prefix TEXT NOT NULL UNIQUE,           -- e.g., "teo_ci_AbCd"
  hash TEXT NOT NULL,                    -- argon2id of the secret
  name TEXT NOT NULL,
  created_by UUID REFERENCES teo.users(id),
  expires_at TIMESTAMPTZ,
  revoked_at TIMESTAMPTZ,
  scopes TEXT[] NOT NULL                 -- e.g., {'runs.write','results.write'}
);

CREATE TABLE teo.user_roles (
  user_id UUID NOT NULL REFERENCES teo.users(id),
  role TEXT NOT NULL CHECK (role IN ('admin','engineer','read_only')),
  repo_id UUID REFERENCES teo.repos(id), -- NULL = global
  PRIMARY KEY(user_id, role, COALESCE(repo_id, '00000000-0000-0000-0000-000000000000'))
);
```

### `audit_log`
Append-only.
```sql
CREATE TABLE teo.audit_log (
  id BIGSERIAL PRIMARY KEY,
  at TIMESTAMPTZ NOT NULL DEFAULT now(),
  actor_user_id UUID,
  actor_api_key_id UUID,
  action TEXT NOT NULL,
  target_type TEXT,
  target_id TEXT,
  meta JSONB NOT NULL DEFAULT '{}'
);
CREATE INDEX audit_at_idx ON teo.audit_log(at);
```

### Views

`teo.v_test_health` — joins `tests`, latest `flake_records`, last 30-day pass rate from a materialized view fed by ClickHouse.

---

## 2. ClickHouse schema

Database `teo`. All tables use `MergeTree` family with TTL for hot/cold tiering.

### `teo.test_runs` (one row per execution, denormalized for analytics)
```sql
CREATE TABLE teo.test_runs (
  run_id UUID,
  shard_id UUID,
  test_id UUID,
  test_path String,
  test_name String,
  repo_id UUID,
  commit_sha String,
  branch String,
  attempt UInt8,
  outcome Enum8('passed'=1,'failed'=2,'skipped'=3,'errored'=4,'timed_out'=5),
  duration_ms UInt32,
  worker_id String,
  started_at DateTime64(3),
  finished_at DateTime64(3)
) ENGINE = MergeTree
PARTITION BY toYYYYMM(started_at)
ORDER BY (repo_id, test_id, started_at)
TTL toDate(started_at) + INTERVAL 30 DAY TO VOLUME 'cold',
    toDate(started_at) + INTERVAL 365 DAY DELETE;
```

### `teo.span_events` (raw OTel spans for tests)
```sql
CREATE TABLE teo.span_events (
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
  events Nested(time DateTime64(9), name String, attributes Map(String, String))
) ENGINE = MergeTree
PARTITION BY toYYYYMM(start_time)
ORDER BY (run_id, trace_id, start_time)
TTL toDate(start_time) + INTERVAL 30 DAY DELETE;  -- failed runs sampled 100%, green sampled 1%
```

### `teo.flake_observations` (one row per (test, day))
```sql
CREATE TABLE teo.flake_observations (
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
```

### Materialized views

- `mv_run_summary` (run_id → totals, p50/p95 durations, pass rate) — drives the run-list page.
- `mv_test_recent_30d` — feeds `teo.v_test_health` in Postgres via a periodic sync.

---

## 3. Object store layout (S3 / MinIO)

```
s3://teo-artifacts/
  runs/<run_id>/
    logs/<shard_id>/<test_id>.log.gz
    screenshots/<test_id>/<n>.png
    raw_otlp/<batch_id>.proto.gz       # 30-day retention only on failures
  cold/
    span_events/<yyyy-mm>/...           # post-30d migration target
```

Server-side encryption (SSE-S3 or KMS), versioning on, lifecycle policy: `cold/` to Glacier Deep Archive after 90d, delete after 365d.

---

## 4. Identity & relationships

```
repos 1─* runs 1─* shards 1─* test_executions *─1 tests
                                       │
                                       └─ optional ─* failure_clusters
tests 1─0..1 flake_records
```

A `test_execution` is the connector row. `test_id` (logical) is stable across renames via the AST-based fingerprint (ADR-0010); a rename + content-change appears as a new `test` row, but the fingerprint logic keeps history when only the location moves.

---

## 5. Sizing assumptions (back-of-envelope)

| Volume | Estimate | Notes |
|---|---|---|
| Runs/day per medium customer | 500 | 5 PRs × 100 commits |
| Tests/run avg | 2,000 | medium suite |
| `test_executions` rows/day | 1M | 500 × 2,000 |
| `span_events` rows/day | 5M | ~5 spans per test on failed; sampled on green |
| Postgres growth | ~2 GB/month at this scale | Indexes dominate |
| ClickHouse growth | ~30 GB/month uncompressed; ~3 GB compressed | LZ4 default |

These are well within single-node ClickHouse and CloudNativePG single-primary capacity for v1.

---

## 6. Migration policy

- Forward-only migrations; no destructive `DROP COLUMN` in patch releases.
- Online schema changes only (`ALTER TABLE … ADD COLUMN`, never `… DROP NOT NULL` on a hot table without a 2-phase deploy).
- ClickHouse `ALTER`s are async; tooling waits for mutation completion before declaring the migration done.
