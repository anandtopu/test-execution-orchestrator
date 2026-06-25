# TEO — User Stories with Acceptance Criteria

**Status:** v1.0.0 shipped (tag `v1.0.0`, 2026-06-11); status column refreshed 2026-06-20 against code + the 2026-06-10 reconciliation.
**Implementation progress:** [`progress.md`](../../progress.md) is the canonical dashboard.

Format:
```
S-<epic>-<n>: <persona> <action>
  AC1: <observable, testable criterion>
  AC2: ...
  Links: FR-xxx, ADR-yyyy
  Status: ✅ done · 🟡 scaffolded · ⏳ pending · 📦 deferred
```

Definition of Done (applies to every story): see `docs/process/definition-of-done.md`.

## Per-epic story status (high-level)

| Epic | Stories | Status |
|---|---|---|
| E-01 Foundation | S-01-01..03 | All ✅ |
| E-02 Storage & migrations | S-02-01..03 | S-02-01..02 ✅; S-02-03 load-test ✅ (executed green 2026-06-10: 1M rows, p99 78.8 ms — `internal/testch` + `otlp_loadtest_integration_test.go`) |
| E-03 API gateway | S-03-01..04 | S-03-01 ✅; S-03-02 OIDC roundtrip ✅ (backend `/auth/*` + `teo_session` cookie; frontend `middleware.ts` + `SessionNav` wired 2026-06-10; end-to-end test vs a real Dex still pending a live deploy); S-03-03 ✅; S-03-04 ✅ |
| E-04 Run Manager | S-04-01..04 | All ✅; S-04-02 leader-election integration/chaos test ✅ (executed green against real Postgres via testcontainers 2026-06-10) |
| E-05 Scheduler | S-05-01..04 | S-05-01 ✅ (with property test); S-05-02 ✅; S-05-03 ✅; S-05-04 ✅ (plan persisted to Postgres + S3; `teo replay` CLI landed) |
| E-06 Worker agent | S-06-01..04 | S-06-01 ✅; S-06-02 ✅; S-06-03 ✅ (2026-06-22) — SIGTERM/graceful-cancel handler (`cmd/worker/main.go`) + kill-mid-test integration test (`internal/worker/kill_mid_integration_test.go`: graceful shutdown + completed-work durability + idempotent report retry); S-06-04 ✅ |
| E-07 Result pipeline | S-07-01..03 | S-07-01 ✅ (OTLP receiver live); S-07-02 ✅ (+ failure-cluster backfill job); S-07-03 ✅ (JUnit + OTLP export REST endpoints wired + integration-tested) |
| E-08 Flake detection | S-08-01..03 | S-08-01..02 ✅; **S-08-03 manual operator quarantine ✅** (2026-06-22) — `quarantineTest`/`unquarantineTest` GraphQL mutations (engineer/admin-gated, audit-logged) + `QuarantineControl` UI button/confirm in the Flakes detail sheet; scheduler non-blocking lane (AC2) already done. |
| E-09 Web UI | S-09-01..04 | S-09-01 ✅ (GraphQL); S-09-02 ✅ (Gantt + live WebSocket subscriptions `/graphql/subscriptions`, v1.1 2026-06-23, with 2s polling as the NATS-optional fallback; both auto-stop on terminal); S-09-03 ✅ (failure clusters + flakes); S-09-04 ✅ (rerun-failed button + GraphQL mutation + Next API proxy) |
| E-10 GitHub | S-10-01..03 | All ✅: install + webhook + HMAC verify, Check Run create on first transition, in-flight updates with shard counts, terminal finalize with conclusion + top-3 failure-cluster Markdown summary |
| E-11 Helm + ops | S-11-01..06 | All ✅: chart + subchart vendoring + chart-testing CI matrix; pre-upgrade migration hook; 5 Grafana dashboards as ConfigMaps; 6 PrometheusRule alerts with runbook URLs + 6 runbook docs; goreleaser config + cosign signing + SBOM + chart-releaser publish; restore drill runbook |
| E-12 ML predictor | S-12-01..04 | S-12-01..03 ✅; S-12-04 calibration UI overlay ✅ (predicted-vs-observed band on the run-detail Gantt, landed 2026-06-10) |
| E-13 Karpenter + spot | S-13-01..05 | All ✅: NodePools + IMDS poller + Agent draining state machine + Run Manager reschedule sweep (with reshard manifest) + on-demand affinity for retries + preemption_count telemetry |
| E-14 Adapters | S-14-01..03 | S-14-01..02 ✅; S-14-03 SPI doc ✅ (`docs/adapters/spi.md` + copy-and-fill skeleton at `pkg/adapter/template/`) |
| E-15 Auto-quarantine | S-15-01..04 | All ✅: auto-transition (with broken-vs-flaky), GitHub Issues API client + dedupe-by-comment, SLA nudge sweeper (idempotent on `last_nudged_at`), magic-link un-quarantine restore endpoint |
| E-16 Owner digest | S-16-01..03 | All ✅: aggregation + templates, SMTP + Slack + Multiplex senders, opt-out enforcement, `teo digest dry-run --user=<email>` CLI |

The detailed acceptance criteria per story appear below; statuses above match the named follow-ups in [`progress.md`](../../progress.md).

---

## E-01 Foundation

### S-01-01: Engineer can build the project locally
- **AC1** `make build` produces all service binaries on macOS, Linux, and Windows (WSL).
- **AC2** `make test` runs unit tests in <2 minutes on a developer laptop.
- **AC3** `make lint` runs `golangci-lint` with the repo's pinned config.
- **AC4** README explains the toolchain (Go 1.23, Node 22 LTS, Helm 3.14+).
- **Links:** NFR-MAINT-802

### S-01-02: CI runs lint, tests, and image build on every PR
- **AC1** `.github/workflows/ci.yml` triggers on push and PR.
- **AC2** Workflow steps: `lint`, `unit-test`, `integration-test` (with Postgres + ClickHouse via testcontainers), `build-images`, `helm-lint`.
- **AC3** Workflow blocks merge if any step fails.
- **AC4** Average green CI time ≤ 10 minutes on a fresh PR.
- **Links:** NFR-MAINT-802

### S-01-03: Repo has standard project hygiene
- **AC1** `LICENSE` (Apache 2.0), `NOTICE`, `CONTRIBUTING.md`, `CODE_OF_CONDUCT.md`, `SECURITY.md` present.
- **AC2** `CHANGELOG.md` initialized.
- **AC3** `go.mod` pins minimum Go 1.23.
- **Links:** ADR-0018, NFR-COMP-1001

---

## E-02 Storage & migrations

### S-02-01: Engineer can apply and roll back migrations
- **AC1** `teo migrate up` applies forward migrations.
- **AC2** Migrations are SQL files in `migrations/postgres/` and `migrations/clickhouse/` with `NNN_name.up.sql` naming.
- **AC3** Each migration is reviewed against the schema in `docs/architecture/data-model.md`.
- **AC4** `teo migrate status` reports current version per database.
- **Links:** FR-1002

### S-02-02: Initial schema is applied successfully
- **AC1** Postgres `001_initial.up.sql` creates all tables in the data-model doc.
- **AC2** ClickHouse `001_initial.up.sql` creates `test_runs`, `span_events`, `flake_observations`.
- **AC3** Integration test verifies all tables exist with the documented columns.

### S-02-03: ClickHouse insert path is verified at scale
- **AC1** Load test inserts 1M rows into `test_runs` in <60 seconds on the dev cluster.
- **AC2** Insert latency p99 < 5s under sustained 5K rows/s.
- **Links:** NFR-PERF-105, NFR-SCALE-203

---

## E-03 API gateway

### S-03-01: CI step can submit a run
- **AC1** `POST /api/v1/runs` accepts a `RunRequest` JSON and returns the new run with status `pending`.
- **AC2** Idempotency-Key header dedupes within 24 hours.
- **AC3** Rejects unknown repos with 404 and an RFC 7807 error body.
- **AC4** Validates manifest against a JSON schema and returns 400 with field-level errors on invalid input.
- **Links:** FR-101..103

### S-03-02: User can sign in via OIDC
- **AC1** UI redirect to OIDC provider; on callback, the API issues a JWT.
- **AC2** JWT contains `sub`, `email`, `roles`; signed with HS256, 1h expiry.
- **AC3** Configurable OIDC issuer + client ID via Helm values.
- **Links:** FR-801, ADR-0014

### S-03-03: Admin can create and revoke API keys
- **AC1** UI flow lists API keys, shows prefix + name + scope + creation date.
- **AC2** Creating a key shows the plaintext exactly once with a copy button.
- **AC3** Revoking a key takes effect within 30 seconds across all replicas.
- **AC4** All key operations are audit-logged.
- **Links:** FR-802, FR-805, NFR-SEC-504

### S-03-04: All mutations are audit-logged
- **AC1** Every state-changing endpoint writes a row to `teo.audit_log`.
- **AC2** Audit row includes actor, action, target type/ID, timestamp, and request metadata.
- **AC3** Audit log is append-only; no DELETE or UPDATE permitted by the application role.
- **Links:** FR-804, NFR-SEC-505

---

## E-04 Run Manager

### S-04-01: A submitted run progresses through its state machine
- **AC1** A run created at `pending` transitions to `planning → dispatching → running → finalizing → succeeded` on the happy path.
- **AC2** Each transition writes to `runs.status` and emits a domain event.
- **AC3** Invalid transitions (e.g., `succeeded → running`) are rejected.
- **Links:** FR-101..104

### S-04-02: Two Run Manager replicas don't double-process a run
- **AC1** Integration test: 2 RM replicas; submit 100 runs concurrently. Every run has exactly one lease holder.
- **AC2** Killing the lease holder mid-run causes the second replica to take over within 10s.
- **Links:** ADR-0013, NFR-AVAIL-302

### S-04-03: A run that exceeds its budget is auto-failed
- **AC1** Run with `budget.max_seconds=60` and tests exceeding 60s wall-clock is marked `failed` with reason `budget_exceeded`.
- **Links:** FR-105

### S-04-04: Cancelling a running run stops dispatch and reaps workers
- **AC1** `POST /runs/{id}/cancel` transitions the run to `cancelled`.
- **AC2** In-flight workers receive a cancel signal and stop within 5s.
- **AC3** Cancellation is idempotent.
- **Links:** FR-104

---

## E-05 Scheduler

### S-05-01: LPT scheduler produces near-optimal makespan
- **AC1** Unit test: random duration vectors of size 100–1000; LPT makespan / optimal makespan ≤ 4/3.
- **AC2** Property test: scheduler is deterministic for the same input (stable hash tie-breaks).
- **Links:** FR-301, FR-306, ADR-0005

### S-05-02: Tests with conflicting tags don't share a shard
- **AC1** Two tests both tagged `exclusive-port-5432` are placed in different shards.
- **AC2** A test with `needs-postgres` runs only on a worker advertising `provides-postgres`.
- **Links:** FR-303

### S-05-03: Cold-start tests use the heuristic predictor
- **AC1** A test with no history gets `is_cold_start=true` and the runner-default p50.
- **AC2** UI labels the run "learning mode" when >50% of tests are cold-start.
- **Links:** FR-305

### S-05-04: Assignment plan is persisted and replayable
- **AC1** Every run has its plan stored as JSON in S3 (`runs/<id>/plan.json`).
- **AC2** `teo replay <run_id>` reads the plan and verifies determinism.
- **Links:** FR-304

---

## E-06 Worker agent

### S-06-01: CLI discovers pytest tests
- **AC1** `teo discover --runner pytest .` outputs a manifest with path, name, params hash.
- **AC2** Discovery completes in <30s for a 5,000-test suite.
- **AC3** Discovery failure exits non-zero with a parseable error.
- **Links:** FR-201, FR-204

### S-06-02: Worker runs an assignment end-to-end
- **AC1** Worker pulls assignment, executes pytest with the assigned tests, reports each result.
- **AC2** stdout/stderr per test stream to S3; sealed on test finish.
- **AC3** OTel span emitted per test with duration, outcome, and attributes.
- **Links:** FR-401..404, FR-403

### S-06-03: Worker survives a heartbeat failure
- **AC1** Killing a worker mid-test causes the run to reschedule that test within 30s.
- **AC2** No duplicate completion records appear in `test_executions`.
- **Links:** FR-405, FR-406, NFR-REL-404

### S-06-04: Redactor scrubs known secrets before transmission
- **AC1** A test that prints an AWS access key has the key replaced with `[REDACTED:aws_access_key]` in S3 logs.
- **AC2** A configurable regex appended via Helm values fires correctly.
- **AC3** Redactor adds <2% CPU overhead in benchmarks.
- **Links:** FR-408, ADR-0016, NFR-SEC-506

---

## E-07 Result pipeline

### S-07-01: OTLP gRPC ingest accepts spans at scale
- **AC1** The OTLP receiver accepts standard OTel span batches via gRPC.
- **AC2** Sustained 5K spans/s/replica without backpressure.
- **AC3** Failed inserts retry with exponential backoff; messages survive a writer crash.
- **Links:** FR-501, NFR-PERF-105

### S-07-02: Failures cluster by stack-trace fingerprint
- **AC1** Two test executions with the same normalized stack are linked to the same `failure_cluster`.
- **AC2** A new failure pattern creates a new cluster with `occurrences=1` and `first_seen=now()`.
- **AC3** UI shows clusters ranked by recency × occurrences.
- **Links:** FR-502

### S-07-03: A run can be exported as JUnit XML and OTLP proto
- **AC1** `GET /api/v1/runs/{id}/junit.xml` returns a valid JUnit document, validated against the JUnit XSD.
- **AC2** `GET /api/v1/runs/{id}/otlp` returns a `traces.proto` representation.
- **Links:** FR-503, FR-504

---

## E-08 Flake detection

### S-08-01: A test with a failure followed by a pass is marked flaky-candidate
- **AC1** Within a single run, `attempts > 1` with divergent outcomes flips the test to `flaky-candidate` in `flake_records`.
- **Links:** FR-601, FR-602

### S-08-02: Wilson interval confirms flakiness with bounded false positives
- **AC1** Nightly job computes Wilson lower bound for every test with `n ≥ 20` in the 30-day window.
- **AC2** Tests crossing the threshold are `flaky` in `flake_records`.
- **AC3** Manual labeling of 200 quarantined tests yields ≤ 1% false positive rate.
- **Links:** FR-603, FR-604, ADR-0011

### S-08-03: Operator can quarantine a flaky test
- **AC1** UI button "Quarantine" on a test detail page; confirms reason; writes to audit log.
- **AC2** A quarantined test runs but its outcome does not fail the run.
- **AC3** UI clearly shows quarantined tests in a separate lane on the run timeline.
- **Links:** FR-605, FR-606
- **Status:** ✅ **done (2026-06-22).** AC1 — `QuarantineControl` (Flakes detail sheet) exposes a Quarantine button with an inline reason confirm; the `quarantineTest(testId, reason)` GraphQL mutation writes the transition and an audit row (`action='test.quarantine'`). AC2 — scheduler non-blocking quarantine lane (`internal/scheduler`), unchanged. AC3 — quarantined tests already render in their own status lane on the Flakes screen; the control flips to Unquarantine (`unquarantineTest`) for restore. Both mutations are gated to engineer/admin via `requireMutationRole` and mirror the auto-quarantine daemon's `teo.tests`/`flake_records` bookkeeping. Code: `internal/api/graphql.go` (Test type + mutations), `internal/api/graphql_resolvers.go` (`setQuarantine`/`quarantineTest`/`unquarantineTest`), `web/src/components/teo/QuarantineControl.tsx`, `web/src/app/api/graphql/quarantine/route.ts`. Tests: `graphql_quarantine_test.go` (schema/RBAC/arg-validation, unit) + `graphql_quarantine_integration_test.go` (real-Postgres transition + audit, `//go:build integration`) + `QuarantineControl.test.tsx` (7 vitest cases).

---

## E-09 Web UI

### S-09-01: User can browse recent runs
- **AC1** `/runs` lists the last 100 runs with repo, branch, status, duration, started time.
- **AC2** Filter by repo, branch, status; URL is bookmarkable.
- **AC3** Page renders < 2s for 100 rows.
- **Links:** FR-701, NFR-PERF-106

### S-09-02: User can view a run's timeline
- **AC1** `/runs/<id>` shows shards as horizontal bars (Gantt) with workers on the y-axis.
- **AC2** Tests within a shard are clickable; click → test detail.
- **AC3** Live updates: new test results appear within 3s of finish.
- **Links:** FR-702, FR-706, NFR-PERF-107

### S-09-03: User can drill into a failure
- **AC1** Test detail page shows: outcome, duration, last 30 attempts as a sparkline, log tail, OTel trace embed.
- **AC2** Failure clusters are linked from each failed execution.
- **Links:** FR-703, FR-704, FR-705

### S-09-04: User can rerun failed tests
- **AC1** "Rerun failed" button on a failed run creates a new run scoped to failed/quarantined tests.
- **AC2** New run links back to the parent.
- **Links:** FR-505

---

## E-10 GitHub integration

### S-10-01: GitHub App is installable on a repo
- **AC1** App manifest in repo is publishable via `https://github.com/apps/teo-bot/installations/new`.
- **AC2** Installation enables that repo in TEO automatically.
- **AC3** Webhook receiver verifies HMAC; rejects unsigned payloads with 401.
- **Links:** FR-901, FR-904

### S-10-02: A push triggers a Check Run
- **AC1** A push to a configured branch creates a Check Run with status `in_progress`.
- **AC2** As shards complete, the Check Run summary updates with shard count and pass/fail tallies.
- **AC3** On run finalization, Check Run is `success` or `failure` with a deep link to TEO.
- **Links:** FR-902

### S-10-03: A failed Check Run shows the top failure cluster
- **AC1** Check Run output includes top 3 failure clusters with stack snippets.
- **AC2** Each cluster links to its TEO page.
- **Links:** FR-903

---

## E-11 Helm chart + ops

### S-11-01: Operator can install TEO with one command
- **AC1** `helm install teo deploy/helm/teo -n teo --create-namespace -f values.yaml` succeeds on a fresh kind cluster.
- **AC2** All Pods reach Ready within 5 minutes.
- **AC3** `helm upgrade` is non-disruptive (rolling restart, zero failed runs).
- **Links:** FR-1001, NFR-AVAIL-301

### S-11-02: Migrations run automatically on upgrade
- **AC1** Helm pre-upgrade hook runs `teo migrate up`.
- **AC2** Hook failure aborts the upgrade and reports a clear error.
- **Links:** FR-1002

### S-11-03: Bundled Grafana dashboards expose key signals
- **AC1** Dashboards: API latency, scheduler decision time, run state machine, ClickHouse insert lag, NATS consumer lag.
- **AC2** All dashboards load within 3s on a fresh deployment.
- **Links:** NFR-OBS-704

### S-11-04: Bundled alerts fire on degradation
- **AC1** Alert rules: API p95 > 500ms 5m, run stuck > budget×2, ClickHouse lag > 30s, NATS consumer lag > 1m.
- **AC2** Alerts include runbook URLs.
- **Links:** NFR-OBS-705

### S-11-05: Release artifacts are signed and SBOM-tracked
- **AC1** `release.yml` runs `goreleaser`, produces multi-arch images, signs with cosign, generates CycloneDX SBOM.
- **AC2** `cosign verify` succeeds against the GHCR image with the public key shipped in repo.
- **Links:** NFR-SEC-507

### S-11-06: Operator has a documented restore drill
- **AC1** README has a step-by-step restore procedure.
- **AC2** A staging restore drill is run by the team before tagging v1.0.0.
- **Links:** NFR-AVAIL-305

---

## E-12 ML predictor (Python + LightGBM)

### S-12-01: Predictor service exposes the same gRPC contract as the heuristic
- **AC1** Python service implements `Predictor.Predict` from `proto/teo/v1/predictor.proto`.
- **AC2** Run Manager has a feature flag `predictor.backend = heuristic|ml`; switches at startup, no other code change.
- **AC3** Health endpoint reports model version, training date, last MAE on holdout.
- **Links:** ADR-0019

### S-12-02: Nightly training job produces a model artifact per repo
- **AC1** Cron Job in the Helm chart runs at 02:00 UTC; reads from ClickHouse, writes to S3 `teo-artifacts/models/<repo_id>/<yyyy-mm-dd>/`.
- **AC2** Training failure for one repo does not block training for others.
- **AC3** Job emits `train_job_failures_total`, `train_duration_seconds` metrics.
- **AC4** A model is rejected if MAE × 1.5 > heuristic-baseline MAE on the holdout.
- **Links:** ADR-0019

### S-12-03: Predictor falls back to heuristic on outage or drift
- **AC1** If the Python service is unreachable for 30s, Run Manager calls the heuristic predictor instead; metric `predictor_fallback_total` increments.
- **AC2** If `predictor_mae` exceeds threshold for 5 nightly runs in a row, an alert fires.
- **Links:** ADR-0019, NFR-OBS-705

### S-12-04: Predictor outputs include calibration metadata
- **AC1** `Predict` response includes `is_cold_start`, `model_version`, `confidence` per test.
- **AC2** UI surfaces "predicted" vs "observed" duration on the run timeline so operators can sanity-check.

---

## E-13 Karpenter + spot-aware scheduling

### S-13-01: Helm chart provisions Karpenter NodePools
- **AC1** Chart renders two NodePool manifests: `teo-workers-spot` and `teo-workers-on-demand`, with documented instance families and weights.
- **AC2** A fresh EKS cluster gets the Karpenter operator installed via subchart toggle.
- **AC3** Operators can override NodePool config via `values.yaml` (instance types, sizes, weights).
- **Links:** ADR-0006, ADR-0020

### S-13-02: Worker detects EC2 Spot interruption notice
- **AC1** Worker polls IMDS `/latest/meta-data/spot/instance-action` every 5s.
- **AC2** On a 200 OK, worker enters draining mode within 10s.
- **AC3** Integration test simulates IMDS response; worker enters draining mode correctly.
- **Links:** ADR-0020

### S-13-03: Preempted tests are rescheduled without loss
- **AC1** A worker killed during a 30-test assignment results in: (a) completed tests stay completed, (b) in-flight test is rescheduled with `attempt+1`, (c) not-yet-started tests are rescheduled in a new shard.
- **AC2** Run still finishes successfully if no other failures occur.
- **AC3** UI shows a `preemption_count` per run.
- **Links:** ADR-0020, NFR-REL-404

### S-13-04: Preempted retries route to on-demand by default
- **AC1** A test rescheduled with `attempt > 1` due to preemption schedules to a node with `karpenter.sh/capacity-type: on-demand`.
- **AC2** Operators can override via `values.yaml`.
- **Links:** ADR-0020

### S-13-05: Cost reduction is measurable
- **AC1** A `worker_minutes_used` field per run distinguishes spot from on-demand minutes.
- **AC2** Grafana dashboard shows weekly $/build trend and spot-vs-on-demand share.
- **Links:** PRD goal #3

---

## E-14 Additional runner adapters

### S-14-01: `go test` adapter discovers and runs tests
- **AC1** `teo discover --runner go .` parses `go test -list . ./...` output into a manifest.
- **AC2** Worker invokes `go test -run '^(TestX|TestY)$' -json` and parses streaming JSON events into `TestStarted`/`TestFinished` reports.
- **AC3** Subtests (`t.Run`) are surfaced as distinct test entries with their parent's path.
- **AC4** Stable fingerprint for Go uses package path + test name + (subtest path) + AST sig of the test function (via `go/ast`).
- **Links:** FR-106 (partial), ADR-0010

### S-14-02: Jest adapter discovers and runs tests
- **AC1** Discovery via `jest --listTests --json`.
- **AC2** Worker runs Jest with `--testNamePattern` and `--json --outputFile=` to capture results.
- **AC3** Stable fingerprint uses file path + describe-stack + test name + AST signature. ✅ **AST signature shipped 2026-06-24 (v1.5):** `pkg/adapter/jest/astsig.go` runs an embedded `@babel/parser` Node helper (resolved from the project under test) that hashes each `it()`/`test()` callback body — stable across reformatting/comment edits, sensitive to logic changes — attached at `Execute` (Jest can't enumerate blocks without running) keyed by the `describe > … > title` report name. Dynamic titles (`it.each`/interpolated) and a missing parser/node degrade to empty signatures (the prior path+name-only behavior). Code: `pkg/adapter/jest/{astsig.go,jest.go}`; tests: `pkg/adapter/jest/astsig_test.go` (parser-backed key-match/stability/dynamic-skip, gated on node + `@babel/parser` via `TEO_JS_PARSER_PATHS`, plus pure plumbing tests).
- **Links:** FR-106 (partial)

### S-14-03: Adapter SPI is documented for community contributions
- **AC1** `docs/adapters/spi.md` documents the adapter contract: discovery command, execution command, result-event format, fingerprint hook.
- **AC2** A scaffold `pkg/adapter/template/` exists for new adapters.

---

## E-15 Auto-quarantine workflow

### S-15-01: Wilson-confirmed flake auto-transitions to quarantined
- **AC1** When `flake_records.wilson_lower > threshold` and the test is not already quarantined, the system transitions it to `quarantined` and writes an audit row.
- **AC2** Auto-quarantine can be disabled per-repo via `repos.auto_quarantine_enabled = false`.
- **AC3** A test that starts failing 100% of the time is escalated to status `broken`, NOT auto-quarantined as flaky (the "broken vs flaky" distinction).
- **Links:** FR-605, FR-609, PRD §11

### S-15-02: GitHub Issue is opened on auto-quarantine
- **AC1** Issue body includes: failure cluster snapshot, suspected category, last 20 runs visualization (rendered as Mermaid), CODEOWNERS-resolved assignees.
- **AC2** Issue title format: `[TEO] Flaky test quarantined: <test_name>`.
- **AC3** Issue is labeled `teo`, `flaky`, `auto-generated`.
- **AC4** A second auto-quarantine on the same test does not open a duplicate issue; it adds a comment to the existing one if open.
- **Links:** FR-609

### S-15-03: Stale-after-N-days SLA on quarantine issues
- **AC1** Configurable SLA (`flake.sla_days`, default 14). After expiry, a comment is posted nudging the assignees.
- **AC2** No automatic close — the assignee fixes and unquarantines.

### S-15-04: Un-quarantine proposal when test stabilizes
- **AC1** If a quarantined test passes K consecutive runs (default 30), the system posts a comment on the issue proposing un-quarantine, with a one-click link.
- **AC2** Un-quarantine is operator-action-only — never automatic.

---

## E-16 Owner digest

### S-16-01: Weekly digest is generated per author
- **AC1** Cron Job runs Mondays at 09:00 UTC per repo timezone (configurable).
- **AC2** Digest includes: tests owned (count), flake count and trend, CI minutes consumed (and WoW delta), top 3 slowest tests.
- **AC3** "Owner" is resolved from CODEOWNERS; tests without a code owner are aggregated into a "no owner" digest sent to admins.
- **Links:** FR-708 (partial)

### S-16-02: Digest is delivered via Slack and email
- **AC1** Helm values configure SMTP and/or Slack webhook.
- **AC2** Per-user opt-out via UI.
- **AC3** Per-repo opt-out via repo settings.

### S-16-03: Digest is testable in dry-run mode
- **AC1** `teo digest dry-run --user=<email>` writes the rendered HTML to stdout without sending.
