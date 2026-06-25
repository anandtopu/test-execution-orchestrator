# TEO — Schema Design (current)

**Status:** Current — reflects `migrations/postgres/001..006` and `migrations/clickhouse/001` as applied on `main`.
**Supersedes for code:** [`data-model.md`](data-model.md) is the original 2026-04-30 design draft and has drifted (it still shows the invalid `COALESCE` primary key and omits migrations 002–006). **This file is the as-built reference.** When in doubt, the migration SQL is the final word.

Two stores: **Postgres** (`teo` schema) for transactional state, **ClickHouse** (`teo` database) for high-volume analytics. **S3** for logs and cold archive. Single-tenant: no `tenant_id` anywhere.

Conventions: all Postgres ids are `UUID DEFAULT gen_random_uuid()` (pgcrypto). Every mutable table carries `created_at`/`updated_at`, the latter maintained by the `teo.set_updated_at()` `BEFORE UPDATE` trigger. Migrations are **forward-only**, numbered, paired `.up.sql`/`.down.sql`.

---

## 1. Postgres (`teo` schema)

### 1.1 Core run model

| Table | Purpose | Key columns | Notable constraints / indexes |
|---|---|---|---|
| `repos` | Tracked repositories | `id`, `vcs` (`github` only), `full_name`, `default_branch`, `enabled`, `auto_quarantine_enabled` | `UNIQUE(vcs, full_name)` |
| `runs` | One test run (the aggregate root) | `id`, `repo_id→repos`, `commit_sha`, `branch`, `triggered_by`, `status`, `budget_seconds`, cost columns, `parent_run_id→runs` (rerun lineage), `meta` JSONB | `status` CHECK (8 states); partial index on non-terminal `status`; `runs_repo_started_idx`, `runs_commit_idx`; **`runs_idempotency_key_uniq`** partial unique on `(repo_id, meta->>'idempotency_key')` (migr. 005); GitHub Check columns + index (migr. 003) |
| `shards` | A unit of dispatched work within a run | `id`, `run_id→runs`, `index`, `worker_id`, `predicted_duration_ms`, `actual_duration_ms`, `status`, `test_count`, `meta` JSONB (migr. 004) | `UNIQUE(run_id, index)`; `status` CHECK incl. `lost`, `preempted` |
| `run_plans` | Archived `AssignmentPlan` (replayable) | `run_id→runs` PK, `plan` JSONB, `plan_version` | one row per run; drives `teo replay` |

`runs` cost/preemption columns: `worker_minutes_used`, `spot_minutes`, `on_demand_minutes`, `preemption_count`, `total_duration_ms`.

### 1.2 Test identity & results

| Table | Purpose | Key columns | Notable constraints / indexes |
|---|---|---|---|
| `tests` | Logical test identity (stable across moves) | `id`, `repo_id→repos`, `fingerprint`, `path`, `name`, `params_hash`, `runner`, `owner_team`, `tags[]`, `status`, `ast_signature` (migr. 006) | `UNIQUE(repo_id, fingerprint)`; `status` CHECK (`active/quarantined/broken/deleted`); partial quarantine + owner indexes; **`tests_ast_signature_idx`** partial on `(repo_id, ast_signature)` for move/rename linking |
| `test_executions` | One transactional record per test attempt | `id`, `shard_id→shards`, `test_id→tests`, `attempt`, `outcome`, `duration_ms`, `otel_trace_id`, `failure_cluster_id→failure_clusters`, `log_object_key` | `UNIQUE(shard_id, test_id, attempt)`; outcome CHECK (6 outcomes incl. `interrupted`); per-test + failed-outcome partial indexes |
| `failure_clusters` | De-duplicated failures by stack fingerprint | `id`, `repo_id→repos`, `stack_fingerprint`, `representative_message`, `representative_stack`, `occurrences` | `UNIQUE(repo_id, stack_fingerprint)`; recency index |
| `flake_records` | Per-test flake statistics & quarantine state | `test_id→tests` PK, `flake_rate`, `wilson_lower`, `wilson_upper`, `sample_size`, `category`, `github_issue_number/url`, `evidence` JSONB | sweep-state columns `last_nudged_at`, `unquarantine_proposed_at`, `consecutive_passes` (migr. 002) |
| `unquarantine_tokens` | Single-use magic links for one-click un-quarantine (migr. 002) | `token` PK, `test_id→tests`, `expires_at`, `consumed_at`, `consumed_by→users` | per-test index |

The **fingerprint** (`path::name::params_hash::ast_signature`) is what links a test across renames/reformatting; `ast_signature` is now populated by all three adapters (pytest/gotest/jest). See ADR-0010.

### 1.3 Identity, authz, audit, GitHub

| Table | Purpose | Key columns | Notable constraints |
|---|---|---|---|
| `users` | Human identities | `id`, `email` UNIQUE, `display_name`, `oidc_subject` UNIQUE, `digest_opt_out` | — |
| `api_keys` | CI credentials (argon2id) | `id`, `prefix` UNIQUE, `hash`, `created_by→users`, `expires_at`, `revoked_at`, `scopes[]`, `last_used_at` | active-key partial index on `prefix WHERE revoked_at IS NULL` |
| `user_roles` | RBAC (global or per-repo) | `user_id→users`, `role` (`admin/engineer/read_only`), `repo_id→repos` (NULL = global) | **two partial unique indexes** enforce uniqueness (the original `COALESCE` PK was invalid SQL — see migration comment) |
| `audit_log` | Append-only action log | `id` BIGSERIAL, `at`, `actor_user_id→users`, `actor_api_key_id→api_keys`, `action`, `target_*`, `meta` | indexed on `at` and `(action, at)` |
| `github_installations` | GitHub App installs | `id` BIGINT PK, `account_login`, `account_type`, `suspended` | `runs.github_installation_id` references this by value (soft link, no FK) |

---

## 2. ClickHouse (`teo` database)

All `MergeTree`-family with monthly partitions and TTL. No foreign keys — joins are by id at query time.

| Table / view | Engine | Order key | Retention | Purpose |
|---|---|---|---|---|
| `test_runs` | `MergeTree` | `(repo_id, test_id, started_at)` | TTL 365d DELETE | One denormalized row per execution (analytics: durations, history, flake source). Includes `runner`, `capacity_type` (`spot/on_demand/unknown`), 6-value `outcome` enum |
| `span_events` | `MergeTree` | `(run_id, trace_id, start_time)` | TTL 30d DELETE | Raw OTel spans per test; `attributes Map`, parallel `event_*` arrays |
| `flake_observations` | `SummingMergeTree` | `(repo_id, test_id, day)` | — | Daily rollup of attempts/failures/pass_rate + Wilson bounds |
| `mv_run_summary` | `SummingMergeTree` MV | `(repo_id, run_id)` | — | Per-run pass/fail/skip/error counts + p50/p95 durations; drives the run-list page |

> Note: the design draft (`data-model.md`) describes a hot/cold `TO VOLUME` tier and a `Nested(events …)` column; the **applied** migration uses a single-tier 365d TTL on `test_runs` and flat `event_times/event_names/event_attributes` arrays on `span_events`. The migration is authoritative.

---

## 3. S3 / object store layout

```
s3://teo-artifacts/
  runs/<run_id>/
    logs/<shard_id>/<test_id>.log.gz     # referenced by test_executions.log_object_key
    screenshots/<test_id>/<n>.png
    raw_otlp/<batch_id>.proto.gz
  cold/
    span_events/<yyyy-mm>/...
backups/                                  # pg_basebackup + ClickHouse BACKUP (daily)
```

SSE (SSE-S3 or KMS), versioning on; lifecycle: `cold/` → Glacier Deep Archive after 90d, delete after 365d. See ADR-0017.

---

## 4. Relationship summary

```
repos 1─* runs 1─* shards 1─* test_executions *─1 tests
  │                                  │                │
  │                                  └──*─1 failure_clusters
  └─* tests 1─0..1 flake_records 1─* unquarantine_tokens
runs 0..1─* runs        (parent_run_id: rerun lineage)
runs 1─0..1 run_plans   (archived AssignmentPlan)
users 1─* api_keys / user_roles / audit_log
```

`test_executions` is the connector row joining a physical run (shard) to a logical test. See [`er-diagram.md`](er-diagram.md) for the full entity-relationship diagram.

---

## 5. Migration index

| # | File | What it adds |
|---|---|---|
| 001 | `001_initial` | Full base schema (13 tables, trigger, pgcrypto) |
| 002 | `002_quarantine_tracking` | `flake_records` sweep columns + `unquarantine_tokens` |
| 003 | `003_run_check_run` | `runs.github_check_run_id` / `github_installation_id` + index |
| 004 | `004_shard_meta` | `shards.meta` JSONB (reschedule dedupe) |
| 005 | `005_idempotency_key_unique` | partial unique index on `runs(repo_id, meta->>'idempotency_key')` |
| 006 | `006_test_ast_signature` | `tests.ast_signature` + lookup index |

Run via the CLI: `bin/teo migrate up` / `bin/teo migrate status` (driven by `internal/migrate`). The `make migrate` target is a no-op stub — don't use it.
