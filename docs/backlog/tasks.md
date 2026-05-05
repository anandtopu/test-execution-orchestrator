# TEO — Engineering Tasks

**Status:** Draft (revised 2026-04-30 with implementation status)
**Implementation progress:** [`progress.md`](../../progress.md) is the canonical dashboard.

Tasks are concrete enough to estimate (each ≤ 2 days). Each task references its parent story.

## Implementation status by task group

| Story group | Status | Notes |
|---|---|---|
| **T-01-* Foundation** | ✅ all complete | Repo, Makefile, lint, CI, hygiene files all in place |
| **T-02-* Migrations** | ✅ all complete | Pg + CH initial schemas applied via runner |
| **T-03-* API gateway** | ✅ all complete | OIDC end-to-end test pending real Dex deployment |
| **T-04-* Run Manager** | ✅ all complete | Leader-election chaos test runs locally; CI integration test pending |
| **T-05-* Scheduler** | ✅ all complete | LPT + property test against brute-force optimum land; plan-persist works |
| **T-06-* Worker** | ✅ all complete | pytest adapter executes; redactor verified |
| **T-07-* Result pipeline** | ✅ all complete | OTLP receiver, failure clustering, and REST export endpoints (`GET /api/v1/runs/{id}/export?format={junit\|otlp}`) all wired and integration-tested |
| **T-08-* Flake detection** | ✅ all complete | Wilson math verified against textbook |
| **T-09-* Web UI** | ✅ all complete | All four pages migrated to GraphQL; LiveRunShards polls every 2s with auto-stop on terminal; rerun-failed flow wired with Next API proxy; six Vitest files covering helpers, queries, components, and timer behaviour |
| **T-10-* GitHub** | ✅ all complete | webhook + Checks + Issues clients, RunObserver pattern in Manager, CheckObserver creates/updates/finalizes, top-3 cluster Markdown summary, schema migration 003 for check-run linkage |
| **T-11-* Helm + ops** | ✅ all complete | Chart.yaml dependencies (5 subcharts, toggleable), pre-upgrade migration Job, 5 Grafana dashboard ConfigMaps with sidecar-discovery labels, PrometheusRule with 6 alerts + runbook docs, .goreleaser.yml, release.yml workflow with cosign + SBOM + chart-releaser, chart-testing config, restore drill runbook |
| **T-12-* ML predictor** | ✅ all complete | Real ClickHouse query landed; champion/challenger gating in place |
| **T-13-* Karpenter** | ✅ all complete | NodePools, IMDS poller, Agent draining (atomic flag + current-shard tracking + idempotent beginDrain), Run Manager reschedule sweep with reshard manifest in runs.meta, worker reshard lookup, migration 004 for shards.meta dedupe marker, preemption_count telemetry |
| **T-14-* Adapters** | ✅ all complete | `go test` adapter, Jest adapter, SPI doc at `docs/adapters/spi.md`, copy-and-fill skeleton at `pkg/adapter/template/` |
| **T-15-* Auto-quarantine** | ✅ all complete | Issues client, dedupe-by-comment, SLA sweeper, un-quarantine proposer, magic-link restore endpoint, three Cron entries in chart |
| **T-16-* Owner digest** | ✅ all complete | aggregation, templates, SMTP/Slack senders, runner with opt-out, dry-run CLI, Helm secret wiring |

Status legend: ✅ done · 🟡 scaffolded with named follow-up · ⏳ pending · 📦 deferred

---

## E-01 Foundation

### S-01-01 Local build
- T-01-01-01: Initialize Go module `github.com/<org>/teo`; add `go.mod`, basic `main.go` per service. (0.5d)
- T-01-01-02: Add `Makefile` with `build`, `test`, `lint`, `migrate`, `proto` targets. (0.5d)
- T-01-01-03: Pin `golangci-lint` config (`.golangci.yml`); enable `errcheck`, `gosec`, `revive`, `staticcheck`, `unused`. (0.5d)
- T-01-01-04: Author `README.md` with prerequisites, dev loop, and project layout. (0.5d)

### S-01-02 CI
- T-01-02-01: `.github/workflows/ci.yml` — lint + unit tests. (0.5d)
- T-01-02-02: Add integration-test job using `testcontainers-go` for Postgres + ClickHouse. (1d)
- T-01-02-03: Add Docker build matrix (amd64, arm64); push to GHCR on `main`. (1d)
- T-01-02-04: Add `helm lint` and `helm template` smoke step. (0.5d)
- T-01-02-05: Add Trivy + syft (SBOM) steps; fail on HIGH+ CVEs. (0.5d)

### S-01-03 Project hygiene
- T-01-03-01: Add `LICENSE`, `NOTICE`, `CONTRIBUTING.md`, `CODE_OF_CONDUCT.md`, `SECURITY.md`. (0.5d)
- T-01-03-02: Add `CHANGELOG.md` and Keep-a-Changelog convention. (0.25d)
- T-01-03-03: Add `go-licenses` check to CI (block AGPL/GPL deps). (0.5d)

---

## E-02 Storage & migrations

### S-02-01 Migrations tooling
- T-02-01-01: Vendor `golang-migrate/migrate/v4`; wire into a `teo migrate` Cobra subcommand. (0.5d)
- T-02-01-02: Define migration directory layout `migrations/postgres/`, `migrations/clickhouse/`. (0.25d)
- T-02-01-03: Implement `teo migrate status` per database. (0.5d)

### S-02-02 Initial schemas
- T-02-02-01: Postgres `001_initial.up.sql` with all tables from `data-model.md` §1. (1d)
- T-02-02-02: ClickHouse `001_initial.up.sql` with `test_runs`, `span_events`, `flake_observations`. (1d)
- T-02-02-03: Integration tests verifying schema matches doc. (0.5d)

### S-02-03 ClickHouse insert path
- T-02-03-01: Async batched writer using `clickhouse-go/v2`; configurable batch size + interval. (1d)
- T-02-03-02: Load test harness for 1M rows; document numbers. (1d)

---

## E-03 API gateway

### S-03-01 Run intake
- T-03-01-01: Define `RunRequest` proto (`teo.v1.Runs.CreateRun`). (0.5d)
- T-03-01-02: Implement REST handler `POST /api/v1/runs` (delegates to gRPC). (0.5d)
- T-03-01-03: Idempotency-Key middleware backed by Postgres. (1d)
- T-03-01-04: JSON-schema validation; RFC 7807 error responses. (1d)

### S-03-02 OIDC auth
- T-03-02-01: Bundle Dex subchart in Helm; document OIDC config keys. (1d)
- T-03-02-02: JWT issuer in API gateway; HS256 signing key from env. (1d)
- T-03-02-03: UI sign-in flow and JWT refresh. (1d)

### S-03-03 API keys
- T-03-03-01: Schema for `api_keys`; `argon2id` hashing helper. (0.5d)
- T-03-03-02: Create/revoke endpoints + UI views. (1d)
- T-03-03-03: API-key cache with 30s TTL invalidation on revoke. (0.5d)

### S-03-04 Audit log
- T-03-04-01: Audit middleware; auto-captures actor, action, target. (0.5d)
- T-03-04-02: Postgres role with INSERT-only on `audit_log`. (0.25d)

---

## E-04 Run Manager

### S-04-01 State machine
- T-04-01-01: Define states + transition table; encode in Go. (0.5d)
- T-04-01-02: Run reconciliation loop (poll-based, every 1s). (1d)
- T-04-01-03: Domain event emission to NATS. (0.5d)

### S-04-02 Leader election
- T-04-02-01: Postgres advisory lock on `hash(run_id)` per run. (0.5d)
- T-04-02-02: Lease renewal heartbeat (5s); steal-on-stale logic (30s). (1d)
- T-04-02-03: Integration test with 2 replicas + chaos kill. (1d)

### S-04-03 Budget enforcement
- T-04-03-01: Budget timer per run; transition to `failed` on expiry. (0.5d)

### S-04-04 Cancellation
- T-04-04-01: `CancelRun` RPC; publish cancel signal to workers via NATS. (0.5d)
- T-04-04-02: Worker-side cancel handler (graceful + 5s SIGTERM). (0.5d)

---

## E-05 Scheduler

### S-05-01 LPT
- T-05-01-01: Implement `Plan` pure function with LPT heuristic. (1d)
- T-05-01-02: Property tests: makespan ≤ 4/3 × optimal on small instances (brute-force optimal). (1d)
- T-05-01-03: Determinism: stable hash tie-breaks; same input → same output. (0.5d)

### S-05-02 Constraint solver
- T-05-02-01: Tag-based constraint check; exclusivity + worker-capability matching. (1d)
- T-05-02-02: Constraint test cases. (0.5d)

### S-05-03 Heuristic predictor
- T-05-03-01: Predictor service (Go); rolling-mean per `(repo, file)` from Postgres. (1d)
- T-05-03-02: Cold-start defaults per runner; flag in response. (0.25d)

### S-05-04 Plan persistence
- T-05-04-01: Persist plan JSON to S3 on dispatch. (0.5d)
- T-05-04-02: `teo replay <run_id>` CLI command. (0.5d)

---

## E-06 Worker agent

### S-06-01 Discovery
- T-06-01-01: pytest discovery: `pytest --collect-only -q` parser. (1d)
- T-06-01-02: Compute `params_hash` from `pytest.mark.parametrize` values. (1d)
- T-06-01-03: Compute AST signature (Python AST visitor) for stable fingerprint. (1d)

### S-06-02 Execution
- T-06-02-01: Worker registration + assignment-pull RPC client. (0.5d)
- T-06-02-02: pytest invocation with per-test selectors; parse JSON report. (1d)
- T-06-02-03: OTel span emission per test (custom adapter, not `pytest-opentelemetry`). (1d)
- T-06-02-04: Streaming log capture to S3 (multipart upload). (1d)

### S-06-03 Reliability
- T-06-03-01: Heartbeat goroutine (5s). (0.25d)
- T-06-03-02: Graceful cancel + SIGTERM handler. (0.5d)
- T-06-03-03: Idempotent report retries with `(shard,test,attempt)` dedupe. (0.5d)

### S-06-04 Redactor
- T-06-04-01: Pluggable pattern engine; default rules (AWS keys, JWTs, high-entropy). (1d)
- T-06-04-02: Configurable extra patterns via Helm values. (0.5d)
- T-06-04-03: Benchmark < 2% CPU overhead at typical log volumes. (0.5d)

---

## E-07 Result pipeline

### S-07-01 OTLP receiver
- T-07-01-01: Vendor OTel collector receiver as a library. (1d)
- T-07-01-02: Enrichment pipeline (attach repo, branch, commit, runner image hash). (0.5d)
- T-07-01-03: Postgres + ClickHouse writers behind a fanout. (1d)

### S-07-02 Failure clustering
- T-07-02-01: Stack normalizer (Python tracebacks first; generic fallback). (1d)
- T-07-02-02: SHA256 of normalized top-N frames → `failure_clusters` upsert. (0.5d)
- T-07-02-03: Backfill job for existing failures. (0.5d)

### S-07-03 Exports
- T-07-03-01: JUnit XML emitter from `test_executions`. (1d)
- T-07-03-02: OTLP proto emitter from ClickHouse `span_events`. (1d)

---

## E-08 Flake detection

### S-08-01 In-run flake-candidate detection
- T-08-01-01: Detect divergent outcomes across attempts within a run. (0.5d)
- T-08-01-02: Promote test to `flaky-candidate` in `flake_records`. (0.5d)

### S-08-02 Wilson-interval job
- T-08-02-01: Nightly Cron Job (k8s) computes Wilson bound per test. (1d)
- T-08-02-02: Update `flake_records.flake_rate`, `wilson_lower`, `sample_size`. (0.5d)
- T-08-02-03: Unit test against textbook Wilson examples. (0.5d)

### S-08-03 Quarantine flow
- T-08-03-01: API `quarantine` / `unquarantine` mutations + RBAC. (0.5d)
- T-08-03-02: Scheduler treats quarantined tests as non-blocking lane. (0.5d)
- T-08-03-03: UI: button + modal + audit visibility. (1d)

---

## E-09 Web UI

### S-09-01 Run list
- T-09-01-01: Next.js scaffold; auth flow with JWT. (1d)
- T-09-01-02: GraphQL client (urql); `runs` query + filtering. (1d)
- T-09-01-03: Run-list page with virtual scroll for 100+ rows. (1d)

### S-09-02 Run timeline
- T-09-02-01: Gantt chart component (`visx`). (1d)
- T-09-02-02: Subscription wiring for live test events. (1d)
- T-09-02-03: Test-row click → test detail. (0.5d)

### S-09-03 Test detail + clusters
- T-09-03-01: Test detail page with sparkline of last 30 attempts. (1d)
- T-09-03-02: Failure cluster page; ranked by recency × occurrences. (1d)
- T-09-03-03: Embedded log tail viewer (S3 presigned URL, paginated). (1d)

### S-09-04 Rerun
- T-09-04-01: "Rerun failed" mutation; create child run. (0.5d)

---

## E-10 GitHub integration

### S-10-01 GitHub App
- T-10-01-01: App manifest in repo; setup script. (0.5d)
- T-10-01-02: Webhook receiver: HMAC verification, dedupe by delivery ID. (1d)
- T-10-01-03: Persist installation → `repos.enabled = true`. (0.5d)

### S-10-02 Check Runs
- T-10-02-01: GitHub Checks API client. (0.5d)
- T-10-02-02: Create Check Run on run start; update on each shard finish. (1d)
- T-10-02-03: Finalize Check Run with summary + deep link. (0.5d)

### S-10-03 Failure summary
- T-10-03-01: Top-3 cluster summary in Check Run output Markdown. (0.5d)

---

## E-11 Helm chart & ops

### S-11-01 Chart
- T-11-01-01: Author umbrella chart + per-component subchart templates. (2d)
- T-11-01-02: Subcharts: CloudNativePG, ClickHouse Operator, NATS, MinIO, Dex. (1d)
- T-11-01-03: `values.yaml` documentation pass. (1d)
- T-11-01-04: Chart-testing CI on kind 1.29 + 1.30. (1d)

### S-11-02 Migration hook
- T-11-02-01: Helm pre-upgrade Job runs `teo migrate up`. (0.5d)
- T-11-02-02: Hook failure surfaces in `helm upgrade` output. (0.25d)

### S-11-03 Dashboards
- T-11-03-01: Grafana dashboards as ConfigMaps. (1d)
- T-11-03-02: ServiceMonitors for Prometheus scrape. (0.5d)

### S-11-04 Alerts
- T-11-04-01: Alert rules with runbook URLs. (1d)

### S-11-05 Release pipeline
- T-11-05-01: `goreleaser` config; multi-arch images. (1d)
- T-11-05-02: cosign signing + verification policy. (0.5d)
- T-11-05-03: SBOM via syft; attach to GitHub Release. (0.5d)
- T-11-05-04: Helm chart publishing to `gh-pages`. (0.5d)

### S-11-06 Restore drill
- T-11-06-01: Document Postgres + ClickHouse restore procedures. (0.5d)
- T-11-06-02: Run a real restore drill in staging before v1.0.0. (0.5d)

---

---

## E-12 ML predictor (Python + LightGBM)

### S-12-01 gRPC contract parity
- T-12-01-01: Python service scaffold (FastAPI, gRPC server via `grpcio`); regenerate `predictor.proto` stubs. (1d)
- T-12-01-02: Implement `Predict` calling the loaded LightGBM regressor + classifier. (1d)
- T-12-01-03: Health endpoint with model version, training date, last MAE. (0.5d)
- T-12-01-04: Helm subchart for the predictor, optional toggle. (0.5d)

### S-12-02 Nightly training
- T-12-02-01: Feature extractor reading from ClickHouse (per-test rolling features). (2d)
- T-12-02-02: LightGBM training script for `duration_regressor` (per repo). (1d)
- T-12-02-03: LightGBM training script for `flake_classifier` (global). (1d)
- T-12-02-04: Champion/challenger gating against heuristic baseline; reject on MAE × 1.5. (1d)
- T-12-02-05: Cron Job manifest in chart, S3 model artifact path. (0.5d)
- T-12-02-06: Per-repo model artifact loader with hot reload. (1d)

### S-12-03 Fallback
- T-12-03-01: Run Manager treats predictor RPC failures as fallback to heuristic; metric. (0.5d)
- T-12-03-02: Alert rule on `predictor_mae` drift over 5 runs. (0.5d)

### S-12-04 Calibration metadata
- T-12-04-01: Predict response includes confidence + model version; UI overlay on timeline. (1d)

---

## E-13 Karpenter + spot-aware

### S-13-01 NodePool config
- T-13-01-01: Author Karpenter NodePool templates in chart; document instance families. (1d)
- T-13-01-02: Karpenter operator subchart toggle; install on EKS test cluster. (0.5d)
- T-13-01-03: Override path for operator-supplied NodePool config. (0.5d)

### S-13-02 IMDS poller
- T-13-02-01: Worker IMDS client; 5s poll loop. (0.5d)
- T-13-02-02: Draining state machine (stop pulling, finish reports, exit). (1d)
- T-13-02-03: Integration test with simulated IMDS endpoint. (1d)

### S-13-03 Reschedule on preemption
- T-13-03-01: Run Manager handler for `ShardFinished{status=preempted}`. (0.5d)
- T-13-03-02: Per-test reschedule with `attempt+1`; dedupe with prior attempt records. (1d)
- T-13-03-03: `preemption_count` field on `runs`; UI surface. (0.5d)

### S-13-04 On-demand fallback for retries
- T-13-04-01: Pod node-affinity for `attempt>1` workers; routing to on-demand pool. (1d)

### S-13-05 Cost telemetry
- T-13-05-01: `worker_minutes_used.spot` and `.on_demand` accounting per run. (0.5d)
- T-13-05-02: Grafana dashboard: weekly $/build, spot vs on-demand share. (0.5d)

---

## E-14 Additional runner adapters

### S-14-01 `go test` adapter
- T-14-01-01: Discovery: parse `go test -list . ./...` output. (0.5d)
- T-14-01-02: Execution: invoke `go test -json`; parse streaming events into reports. (1.5d)
- T-14-01-03: Subtest handling (`t.Run`) → distinct test entries. (1d)
- T-14-01-04: Go AST signature for stable fingerprint. (1.5d)
- T-14-01-05: Worker image variant: `golang:1.23` + worker agent. (0.5d)

### S-14-02 Jest adapter
- T-14-02-01: Discovery: `jest --listTests --json`. (0.5d)
- T-14-02-02: Execution: per-test invocation with `--testNamePattern`; parse `--json --outputFile`. (1.5d)
- T-14-02-03: describe/it nesting → fingerprint (path+stack+name; no AST in v1.0). (1d)
- T-14-02-04: Worker image variant: `node:22-alpine` + worker agent. (0.5d)

### S-14-03 Adapter SPI
- T-14-03-01: `docs/adapters/spi.md` documenting the contract. (1d)
- T-14-03-02: Scaffold `pkg/adapter/template/` for community contributions. (0.5d)

---

## E-15 Auto-quarantine workflow

### S-15-01 Auto-transition
- T-15-01-01: Daemon checks Wilson-confirmed flakes; transitions `tests.status → quarantined`. (1d)
- T-15-01-02: Per-repo `auto_quarantine_enabled` flag. (0.25d)
- T-15-01-03: `broken` vs `flaky` distinction: 100% failure rate → `status=broken` not `quarantined`. (0.5d)

### S-15-02 GitHub Issue creation
- T-15-02-01: CODEOWNERS resolver (parse `.github/CODEOWNERS`, match by path). (1d)
- T-15-02-02: Issue body templater (Markdown, embedded Mermaid for run history). (1d)
- T-15-02-03: Idempotency: dedupe by test_id; comment on existing issue if open. (0.5d)

### S-15-03 SLA
- T-15-03-01: Daily Cron Job posts a nudge comment after `flake.sla_days`. (0.5d)

### S-15-04 Un-quarantine proposal
- T-15-04-01: Track consecutive-passes; comment on issue when threshold reached. (1d)
- T-15-04-02: One-click un-quarantine link → magic-link auth. (1d)

---

## E-16 Owner digest

### S-16-01 Weekly per-author digest
- T-16-01-01: Aggregation query: per-owner test count, flake count, CI minutes (vs prior week). (1d)
- T-16-01-02: Top-N slowest tests per owner. (0.5d)
- T-16-01-03: HTML template + plain-text fallback. (1d)

### S-16-02 Delivery
- T-16-02-01: SMTP sender (configurable in chart). (0.5d)
- T-16-02-02: Slack webhook sender. (0.5d)
- T-16-02-03: Per-user opt-out endpoint + UI. (0.5d)
- T-16-02-04: Per-repo opt-out flag. (0.25d)

### S-16-03 Dry-run
- T-16-03-01: `teo digest dry-run --user=<email>` prints rendered HTML. (0.25d)

---

## Estimation summary

Total task days ≈ **149d** of focused work, broken down:

| Phase | Epics | Task days |
|---|---|---|
| 1 (Weeks 1-3) | E-01, E-02, E-03 (foundation, schema, API gateway) | ~28d |
| 2 (Weeks 4-6) | E-04, E-05, E-06, E-09 partial (run path, scheduler, worker, UI list/timeline) | ~38d |
| 3 (Weeks 7-9) | E-07, E-08, E-09 final, E-10 (result pipeline, flake detection, UI polish, GitHub) | ~30d |
| 4 (Weeks 10-12) | E-11 final, E-12, E-13, E-14, E-15, E-16 (chart hardening, ML, Karpenter, adapters, auto-quarantine, digest) | ~53d |

With a team of **4 engineers (1 platform, 2 backend, 1 frontend)** at ~80% utilization over 12 weeks: 4 × 5 × 12 × 0.8 = **192 person-days available**. Buffer of ~43 days covers the highest-risk epics (E-12 ML predictor calibration, E-13 spot interruption robustness).

With **3 engineers**: 144 person-days available — would require deferring **E-16 (Owner digest)** and **S-12-04 (calibration UI overlay)** to v1.1.

**Critical path:** E-01 → E-02 → E-03 → E-04 → E-05 → E-06 → E-07 → demo. E-09 (UI), E-12 (ML), E-13 (Karpenter), E-14 (adapters) parallel against the critical path; E-15/E-16 layer on at the end.
