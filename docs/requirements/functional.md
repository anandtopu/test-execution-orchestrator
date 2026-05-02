# TEO — Functional Requirements (FR)

**Status:** Draft
**Date:** 2026-04-30 (revised after timeline → 3 months, scope expansion per ADR-0012)
**Scope:** 3-month v1.0. Items marked `[deferred]` are post-v1.0 (v1.1+).
**Implementation status:** see [`progress.md`](../../progress.md) for the per-FR ✅/🟡/⏳ dashboard. This document is the authoritative requirements list; `progress.md` is the authoritative status.

Each FR has a stable ID. Tests, ACs, and PR descriptions reference these IDs.

---

## FR-100: Run intake

| ID | Requirement |
|---|---|
| FR-101 | A user (CLI or CI step) can submit a run with a test manifest, commit SHA, branch, and optional budget. |
| FR-102 | The system rejects a run for a repo that is not registered. |
| FR-103 | The system accepts duplicate run requests with the same `Idempotency-Key` and returns the existing run. |
| FR-104 | A run can be cancelled while in `pending`, `planning`, `dispatching`, or `running` states. |
| FR-105 | A run automatically transitions to `failed` if it exceeds `budget.max_seconds`. |
| FR-106 | The system supports `pytest`, `go test`, and Jest runners. `[deferred: RSpec, JUnit-direct, Bazel]` |

## FR-200: Test discovery & manifest

| ID | Requirement |
|---|---|
| FR-201 | The CLI can discover pytest tests by invoking `pytest --collect-only -q`. |
| FR-202 | The manifest captures: path, fully-qualified name, parameter set hash, declared tags. |
| FR-203 | Tests can declare resource tags (e.g., `needs-postgres`, `exclusive-port-5432`); the scheduler treats these as constraints. |
| FR-204 | The CLI exits non-zero with a clear error if discovery fails. |

## FR-300: Scheduling & sharding

| ID | Requirement |
|---|---|
| FR-301 | The scheduler partitions tests across N shards using LPT bin-packing over predicted durations. |
| FR-302 | Within each shard, tests are ordered longest-predicted-duration first. |
| FR-303 | Tests with conflicting exclusivity tags are not placed in the same shard. |
| FR-304 | The scheduler emits a JSON `assignment_plan` artifact with every run, sufficient to replay the decision. |
| FR-305 | When no history exists for a test, the scheduler uses a per-runner default duration (pytest = 1.2s) and marks the prediction `is_cold_start = true`. |
| FR-306 | The scheduler runs as a pure function: same inputs → same outputs (modulo tie-breaking on stable hash). |
| FR-307 | `[deferred]` Speculative re-execution of straggler tests. |
| FR-308 | The scheduler is spot-aware: workers can run on AWS Spot instances; preemption causes the in-flight test to be rescheduled with `attempt+1` and routes by default to an on-demand fallback pool. See ADR-0020. |
| FR-309 | `[deferred]` Cost-budgeted execution. |

## FR-400: Worker execution

| ID | Requirement |
|---|---|
| FR-401 | A worker pulls one assignment at a time. |
| FR-402 | A worker streams `TestStarted` and `TestFinished` events to the control plane within 2s of the test's start/end. |
| FR-403 | A worker emits one OTel span per test, tagged with run_id, shard_id, test_id, attempt. |
| FR-404 | A worker streams stdout/stderr to S3-compatible storage in append mode; objects are sealed on test finish. |
| FR-405 | A worker heartbeats every 5 seconds. |
| FR-406 | If a worker misses 6 consecutive heartbeats, the control plane marks its shard `lost` and reschedules its in-flight tests. |
| FR-407 | A worker that finishes its assignment sends `ShardFinished` with totals; the control plane idempotently accepts. |
| FR-408 | Test logs are redacted on the worker before transmission, using configurable patterns. |

## FR-500: Result aggregation

| ID | Requirement |
|---|---|
| FR-501 | Test results are written to Postgres (`test_executions`) and ClickHouse (`test_runs`, `span_events`) with at-least-once delivery and idempotent upsert. |
| FR-502 | Failed test executions cluster by stack-trace fingerprint into `failure_clusters`. |
| FR-503 | The system exports a JUnit XML representation of any run on demand. |
| FR-504 | The system exports an OTLP proto representation of any run on demand. |
| FR-505 | The system supports a "rerun failed only" mode that creates a new run scoped to the failed/quarantined tests of a prior run. |
| FR-506 | `[deferred]` GraphQL query API beyond the read fields needed for the UI. |
| FR-507 | `[deferred]` Slack and Linear native integrations. |

## FR-600: Flake detection (statistical only in MVP)

| ID | Requirement |
|---|---|
| FR-601 | On a failed test, the worker may rerun it up to K=2 times in-shard with a different RNG seed (configurable). |
| FR-602 | A test is marked `flaky-candidate` if attempts within a single run produce divergent outcomes. |
| FR-603 | A nightly job recomputes Wilson lower-bound flake rate per test over a rolling 30-day window. |
| FR-604 | A test is marked `flaky` (and quarantine-eligible) when Wilson lower bound > 0.05 over ≥ 20 runs. |
| FR-605 | A confirmed flake is auto-quarantined; the operator can disable per-repo. A quarantined test that starts failing 100% of the time is escalated to `broken`, not silenced. |
| FR-606 | A quarantined test runs in a non-blocking lane: failures do not fail the run. |
| FR-607 | An ML predictor (LightGBM GBT) produces per-test duration p50/p95 and flake probability. The system falls back to the heuristic predictor on outage or MAE drift. See ADR-0019. |
| FR-608 | `[deferred]` Causal flake categorization beyond a coarse heuristic. |
| FR-609 | On auto-quarantine, a GitHub Issue is opened, assigned to CODEOWNERS, with stale-after-N-days SLA. Un-quarantine is operator-confirmed (not automatic). |

## FR-700: UI

| ID | Requirement |
|---|---|
| FR-701 | A user can view a list of recent runs filtered by repo, branch, status. |
| FR-702 | A user can view a single run's timeline as a Gantt over workers. |
| FR-703 | A user can view a single failed test's stack trace, log tail, and OTel trace. |
| FR-704 | A user can view all `failure_clusters` and drill into affected tests. |
| FR-705 | A user can view per-test history (last 30 days) and the current flake status. |
| FR-706 | The UI updates live (subscription) for any run still in `running` or earlier state. |
| FR-707 | `[deferred]` "What-if" simulator. |
| FR-708 | A weekly per-author digest (Slack/email) shows tests owned, flake count, CI minutes consumed, and slowest tests. Per-user and per-repo opt-out. |
| FR-709 | The UI shows a cost dashboard with weekly $/build trend and spot-vs-on-demand share. |

## FR-800: Identity & authz

| ID | Requirement |
|---|---|
| FR-801 | Humans authenticate via OIDC (Dex preconfigured). |
| FR-802 | Machines authenticate via API keys, scoped to a repo and a set of permissions. |
| FR-803 | Roles: `admin`, `engineer`, `read_only`. Admin can manage users, repos, and API keys. |
| FR-804 | All mutations are audit-logged with actor, action, target, timestamp. |
| FR-805 | API keys can be revoked; revocation takes effect within 30 seconds. |

## FR-900: Integration with GitHub

| ID | Requirement |
|---|---|
| FR-901 | The TEO instance can be installed as a GitHub App with `checks:write`, `contents:read`, `metadata:read`, `pull_requests:write` scopes. |
| FR-902 | On a `push` event, TEO posts a Check Run that updates as the run progresses (in_progress → success/failure). |
| FR-903 | A failed Check Run includes a deep link to the run page and the top failure cluster summary. |
| FR-904 | The webhook receiver verifies HMAC signatures and rejects unsigned payloads. |

## FR-1000: Operations

| ID | Requirement |
|---|---|
| FR-1001 | The operator can deploy TEO via `helm install teo deploy/helm/teo -f values.yaml`. |
| FR-1002 | The operator can apply schema migrations via a Helm pre-upgrade hook. |
| FR-1003 | The system exposes `/healthz`, `/readyz`, `/metrics`. |
| FR-1004 | Backups of Postgres and ClickHouse run automatically when enabled. |
| FR-1005 | A `teo doctor` CLI command checks connectivity to all dependencies and reports status. |
