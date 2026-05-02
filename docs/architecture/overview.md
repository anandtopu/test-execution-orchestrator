# TEO — Architecture Overview

**Version:** 0.1
**Date:** 2026-04-30
**Status:** Draft, gates implementation
**Audience:** Engineers, reviewers, future contributors
**Implementation status:** see [`progress.md`](../../progress.md) for the live, per-epic and per-FR dashboard. This document describes what the system **should** look like; `progress.md` describes what's currently **wired up**. When the two diverge, treat this doc as the spec and `progress.md` as ground truth for code.

---

## 1. Context

The Test Execution Orchestrator (TEO) is a self-hosted, OSS, single-tenant control plane that schedules test executions across a worker pool, ingests results as OpenTelemetry traces, and statistically detects flaky tests. See `PRD.md` for product context and `docs/adr/` for material decisions.

This document is the canonical "how it fits together" reference. For the **why** behind each choice, see the linked ADR. For the **how much is built so far**, see [`progress.md`](../../progress.md).

---

## 2. Constraints driving this architecture

| Constraint | Source | Impact |
|---|---|---|
| 3-month delivery, 3-4 engineers | User directive (2026-04-30, revised) | See ADR-0012 (revised) for what's in scope vs deferred. ML predictor, Karpenter+spot, `go test`+Jest adapters, auto-quarantine, owner digest are all in v1.0 |
| Self-hosted, single-tenant | User directive | No tenant_id columns, no per-tenant isolation. Helm chart day 1. |
| AWS-only | User directive | Karpenter + S3 acceptable; deferring multi-cloud abstractions |
| Go backend | User directive | gRPC + `gqlgen`; pgx; Bun ORM; `clickhouse-go/v2` |
| Fully OSS (Apache 2.0) | User directive | No AGPL deps; ClickHouse OSS is Apache 2.0; LightGBM Apache 2.0; OK |
| Greenfield | User directive | We build identity, secrets, observability ourselves (lightweight) |

---

## 3. System diagram (MVP, 1-month scope)

```
                          ┌─────────────────────────────────────────────┐
                          │            TEO Control Plane                │
                          │                                             │
   ┌──────────────┐   gRPC│  ┌────────────┐    ┌───────────────┐        │
   │ teo CLI       │─────▶│  │ API gateway│───▶│ Run Manager   │        │
   │ (in CI step)  │      │  │ (gRPC+REST)│    │ (orchestration│        │
   └──────────────┘       │  └─────┬──────┘    │  state machine)│       │
                          │        │           └──────┬─────────┘        │
                          │        │                  │                  │
                          │        ▼                  ▼                  │
                          │  ┌──────────────┐  ┌────────────┐            │
                          │  │ GraphQL/REST │  │ Scheduler  │            │
                          │  │  read API    │  │ (pure func)│            │
                          │  └─────┬────────┘  └────┬───────┘            │
                          │        │                │                    │
                          │        │           dispatch via NATS         │
                          │        │                │                    │
                          │  ┌─────▼─────┐    ┌─────▼──────┐             │
                          │  │ Result    │◀───│ Worker pool│             │
                          │  │ Pipeline  │    │ (k8s pods) │             │
                          │  │ (OTLP)    │    └─────┬──────┘             │
                          │  └──┬──┬─────┘          │                    │
                          │     │  │                ▼                    │
                          │     │  │          ┌──────────┐               │
                          │     │  │          │ Test      │              │
                          │     │  │          │ runners   │              │
                          │     │  │          │ (pytest…) │              │
                          │     │  │          └──────────┘               │
                          │     │  └────▶ Postgres (OLTP: runs, shards,  │
                          │     │           tests, flakes)               │
                          │     └───────▶ ClickHouse (OLAP: spans,       │
                          │               durations, flake stats)        │
                          │                                              │
                          │     S3-compatible (logs, screenshots, cold)  │
                          └─────────────────────────────────────────────┘

                          ┌──────────────┐
                          │ Web UI       │  Next.js, GraphQL client
                          │ (Next.js)    │
                          └──────────────┘
```

---

## 4. Components

### 4.1 `teo` CLI
- Single Go binary, distributed via GitHub Releases.
- Discovers tests via runner adapters (pytest in MVP).
- Sends a `RunRequest` to the API; streams shard outcomes back to the user terminal.
- Used both in CI steps and locally for `teo run --suite pytest`.

### 4.2 API Gateway
- gRPC for high-throughput worker traffic, REST + GraphQL for human/UI traffic.
- Auth: JWT bearer (HS256 v1, asymmetric in v1.5) + API keys for CI.
- Stateless; horizontal scaling behind a Service in k8s.

### 4.3 Run Manager
- Drives a `Run` through its state machine: `pending → planning → dispatching → running → finalizing → succeeded|failed|cancelled`.
- Persists state transitions to Postgres; emits domain events to NATS for the result pipeline.
- Leader-elected via k8s Lease (only one active Run Manager per Run); failover under 10s — see ADR-0013.

### 4.4 Scheduler (pure function)
- Signature: `Plan(tests []Test, predictions Predictions, fleet FleetSnapshot, constraints Constraints) → AssignmentPlan`.
- LPT bin-packing over predicted durations + critical-path-first ordering within shard.
- Replayable: every plan is persisted as JSON, enabling a future "what-if" simulator without rework.
- ADR-0005.

### 4.5 Predictor (Go heuristic + Python LightGBM)
- gRPC service with `Predict(test_fingerprint) → {p50, p95, flake_prob}`.
- Two implementations behind the same contract:
  - **Go heuristic** (always present): per-`(repo, file)` rolling mean over the last 30 runs; flake_prob = 0 unless the statistical detector marks it.
  - **Python ML** (optional, default-on): LightGBM GBT models, trained nightly on ClickHouse history; per-repo regressor, global flake classifier. See ADR-0019.
- Run Manager calls gRPC; on Python service outage or MAE drift, automatically falls back to the heuristic. The system never depends on the ML predictor for correctness.

### 4.6 Worker Agent
- Go binary, packaged in a runtime container (one variant per runner — MVP ships `pytest` only).
- Pulls assignments via gRPC, executes the runner, streams OTLP spans + JUnit-XML adapter output to the result pipeline.
- Heartbeats every 5s; if a heartbeat is missed for 30s, the Run Manager marks the worker LOST and reschedules its in-flight tests.

### 4.7 Result Pipeline
- OTLP gRPC receiver (vendored from the OpenTelemetry Collector).
- Enrichment: attach `repo`, `branch`, `commit`, `runner_image_hash`, `worker_id`.
- Writers: Postgres (test outcomes — small, indexed), ClickHouse (span events, durations — high-volume), S3 (logs, screenshots).
- Failure clustering: stack-trace fingerprint (top-N normalized frames, hashed) → `failure_clusters` table.
- Flake detector: nightly job + on-demand on rerun; Wilson lower bound > threshold → `flake_records` row.

### 4.8 Worker Pool
- EKS cluster with **Karpenter** managing the worker NodePools (`teo-workers-spot` and `teo-workers-on-demand`).
- Workers run primarily on Spot; preemption is detected via IMDS and triggers a graceful drain. Preempted tests reschedule with `attempt+1` and route to on-demand by default. See ADR-0006 (revised) and ADR-0020.
- Tests run in pods with the runner-specific image; pod request = predicted duration + 20% slack.

### 4.9 Web UI
- Next.js 15 (App Router), React Server Components for the run timeline.
- GraphQL via `urql` against the read API.
- Runs in a sidecar container in the same Helm release; same auth as the API.

---

## 5. Data flow: a single run

1. CI step calls `teo run --suite pytest`. CLI discovers tests, posts `RunRequest` (manifest + `commit`, `branch`, `budget_hint`).
2. API Gateway authenticates, persists the Run (`status=pending`), emits `RunCreated`.
3. Run Manager picks it up, calls Predictor for per-test estimates, calls Scheduler, gets an `AssignmentPlan`.
4. Run Manager creates Shards in Postgres, dispatches to NATS subject `teo.shards.dispatch`.
5. Worker Agents (already pod-scaled by HPA based on queue depth) pull assignments, execute tests, stream OTLP per test.
6. Result Pipeline writes to ClickHouse + Postgres in real time. UI tails the run via GraphQL subscription.
7. On the last shard finalizing, Run Manager runs the `finalize` job: failure clustering, flake detection, exports, GitHub Checks update.
8. Run is marked terminal; cold-archive job moves logs to S3 after 30 days (rule, not on completion).

---

## 6. Cross-cutting concerns

| Concern | Approach | ADR |
|---|---|---|
| Identity | OIDC for human users (Dex preconfigured in Helm); API keys for CI | ADR-0014 |
| Authz | Role-based: `admin`, `engineer`, `read-only` | ADR-0014 |
| Secrets | Kubernetes Secrets for v1; SealedSecrets recommended in Helm | ADR-0015 |
| Observability | TEO emits OTel spans for its own operations to its own pipeline (dogfooded) | ADR-0004 |
| PII / log redaction | Pluggable redactor; ships with regexes for AWS keys, JWTs, common secret formats | ADR-0016 |
| Backups | Postgres: daily `pg_basebackup` to S3; ClickHouse: daily `BACKUP` to S3 | ADR-0017 |
| HA | Control plane: 2 replicas, Postgres in HA via CloudNativePG, ClickHouse single-shard for v1 | ADR-0017 |

---

## 7. Out of scope for v1.0 (3-month)

See ADR-0012 (revised) for the formal scope. Summary of what's deferred to v1.1+:

- "What-if" simulator
- Cost-budgeted execution mode
- LLM-assisted root-cause hints
- Multi-cloud worker fleet (GCP, Azure)
- Worker adapters beyond pytest, `go test`, Jest (RSpec, JUnit-direct, Bazel deferred)
- Speculative re-execution of stragglers
- Causal flake categorization v2 (beyond coarse heuristic)
- Cross-repo test impact analysis
- SOC 2 Type 1 readiness
- Asymmetric (RS256) JWT signing

## 8. Implementation status

This section is a pointer; do not maintain the truth here. The live dashboard lives in [`progress.md`](../../progress.md) and is updated in the same commit that lands the corresponding code.

At a glance (as of 2026-04-30):

- **9 of 16 epics ✅ done** end-to-end at the code level: E-01..E-08, E-12, E-14.
- **6 epics 🟡 scaffolded** with named follow-ups documented per row in `progress.md`: E-09 (UI swap to GraphQL), E-10 (Run Manager → Check Run callback), E-11 (subchart vendoring + dashboards + goreleaser), E-13 (worker draining wiring), E-15 (GitHub Issues API client), E-16 (SMTP/Slack senders).
- **Build status:** 14 test packages green, 110 source files, `go build ./...` clean.

Goal alignment relative to PRD §3:
- ✅ #1 wall-clock reduction — LPT scheduler with property-test-verified ≤4/3 × OPT bound.
- ✅ #2 flake quarantine FP rate — Wilson interval implementation with textbook-verified math.
- 🟡 #3 cost reduction — Karpenter NodePools land; spot interruption handling has IMDS detection but the worker draining state machine is the named next step.
- 🟡 #5 runner adapters — pytest, `go test`, Jest done (3 of 6); RSpec/JUnit-direct/Bazel deferred.

---

## 8. Open questions (still)

1. **Stack-trace fingerprinting across languages.** Python tracebacks, Go panics, Java exceptions all look different. v1 ships with Python normalization; everything else falls back to "raw hash of last 5 lines." Acceptable?
2. **Predictor cold-start beyond per-file mean.** Should we ship a tiny baked-in default (e.g., `pytest` median = 1.2s) for repos with zero history? Lean: yes, just hardcode it.
3. **Where does the redactor run?** On the worker before stream, or in the pipeline? Lean: worker (so secrets never hit the wire), but it costs CPU on the worker.
