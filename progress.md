# TEO — Implementation Progress

**Last updated:** 2026-05-20 (⚠ **backlog reconciliation** found **12 items marked ✅ here that were not wired in code** — see [Backlog reconciliation](#backlog-reconciliation--verified-incomplete-items-2026-05-20) below. **Then fixed 3 of them this session: #1 `teo replay` CLI, #5 log-tail viewer, #6 OIDC sign-in flow** — 9 items remain (2 functional: #3/#4 AST fingerprints + the #2 S3 plan archive). Prior 2026-05-05 entry: Gap #5 GraphQL authz gate landed; `internal/nats` unit-tested.)
**Build status:** ✅ `go build ./...` clean · ✅ 26 Go unit packages green · ✅ 38 integration tests under `-tags=integration` (testcontainers Postgres for GraphQL + run-intake + run-export + cost resolvers; testcontainers MinIO for the logstore S3 round-trip; runs in CI) · 6 frontend test files (Vitest, runs in CI)
**Reading order:** [PRD](PRD.md) → [Architecture overview](docs/architecture/overview.md) → [ADR-0012 scope](docs/adr/0012-mvp-scope-cut.md) → this file.

> **▶ Resume here next session.** ⚠ **The "functionally release-ready / no known blockers" status was overstated.** A 2026-05-20 code audit found **12 items marked ✅ that were not implemented** (see [Backlog reconciliation](#backlog-reconciliation--verified-incomplete-items-2026-05-20)); five were functional ACs. **3 are now fixed** (#1 `teo replay`, #5 log-tail viewer, #6 OIDC sign-in); **9 remain — 2 functional (#3/#4 AST fingerprints), 1 partial (#2 S3 plan archive), the rest polish/test-debt.** The infrastructure that *is* done remains real — `go build ./...` clean, 25 unit packages + 38 integration tests green, goreleaser verified end-to-end (`goreleaser release --snapshot --clean --skip=sign,sbom,announce` against v2.15.4 produces all 7 services × linux/darwin amd64/arm64 + windows for `teo`, plus archives + checksums in ~90s), protoc-gen-go wiring landed, `docs/release-notes/v1.0.0.md` drafted. **But triage the open items into v1.0-must-fix vs v1.1-defer before tagging.**
>
> After the open items are triaged/closed, two manual steps remain to ship — both require human authorization:
> 1. **Run the restore drill** against staging per [`docs/operations/restore-drill.md`](docs/operations/restore-drill.md) and record the outcome in the runbook's history log.
> 2. **Tag and push:** `git tag v1.0.0 && git push --tags` — `release.yml` then runs goreleaser (binaries + cosign + Syft SBOMs) and chart-releaser (Helm chart published to gh-pages). After GitHub creates the Release, paste `docs/release-notes/v1.0.0.md` into the description.
>
> At tag time, also roll the `[Unreleased]` block in `CHANGELOG.md` under `## [v1.0.0] — <YYYY-MM-DD>`.

This is the single source of truth for implementation status. Every entry links to the code or doc it tracks. Status legend:

- ✅ **Done** — code lands, tests pass, doc reflects reality
- 🟡 **Scaffolded** — wire-correct skeleton in place, expected behavior covered, but a follow-up is required (called out per row)
- ⏳ **Pending** — agreed in scope, not yet started
- 📦 **Deferred** — out of scope for v1.0 per ADR-0012 (revised)

---

## Backlog reconciliation — verified incomplete items (2026-05-20)

A code-level audit of every story/task in `docs/backlog/{stories,tasks}.md` against the source tree found the items below. Each was **marked ✅ elsewhere in this file but is not wired in code** — verified by inspecting the source, not the docs. (The older `stories.md`, last touched 2026-04-30, still honestly flags most of these as ⏳/🟡; `progress.md` had drifted ahead of reality.)

| # | Item | Story / Task | Status | Evidence (verified in code) |
|---|---|---|---|---|
| 1 | `teo replay <run_id>` CLI | S-05-04 AC2 / T-05-04-02 | ✅ **done** (2026-05-20) | [`cmd/teo/replay.go`](cmd/teo/replay.go) reads `runs.meta.computed_plan`, re-runs `scheduler.Replay` with `scheduler.DefaultConstraints()`, and reports determinism (exit 1 on mismatch; `--json` flag). Unit-tested in [`internal/scheduler/replay_test.go`](internal/scheduler/replay_test.go) |
| 2 | Assignment plan persisted to **S3** `runs/<id>/plan.json` | S-05-04 AC1 / T-05-04-01 | 🟡 deviates | Plan is persisted to **Postgres** (`runs.meta.computed_plan` + the `teo.run_plans` table), not the S3 object path the AC specifies. Replay-from-DB works; S3 archive + CLI do not |
| 3 | Go AST-signature fingerprint | S-14-01 AC4 / T-14-01-04 | ⏳ missing | No `go/ast` use in `pkg/adapter/gotest`; the worker fingerprint is `path::name::paramsHash` (`internal/worker/worker.go:288`), not an AST signature |
| 4 | Python AST-signature fingerprint | S-06-01 / T-06-01-03 | ⏳ missing | No AST visitor in `pkg/adapter/pytest`; same `path::name::paramsHash` fingerprint |
| 5 | UI log-tail viewer (S3 presigned URL, paginated) | S-09-03 AC1 / T-09-03-03 | ✅ **done** (2026-05-20) | `logstore.Presigner` + `S3.Presign`; API `GET /api/v1/runs/{id}/tests/{execId}/log` (ownership-checked, 501 when S3 unconfigured); Next BFF `/api/logs` proxies the presigned URL's tail via HTTP Range; `LogTail` component + `runs/[id]/tests/[execId]` page. Tested both sides. *Remaining:* a per-test list linking into the viewer (broader S-09-03) |
| 6 | UI OIDC sign-in flow + JWT refresh | S-03-02 AC1 / T-03-02-03 | ✅ **done** (2026-05-20) | New dependency-free [`internal/oidc`](internal/oidc) (discovery, code exchange, JWKS RS256 verify); API `/auth/{login,callback,logout,session,refresh}` issue an HS256 JWT in an httpOnly `teo_session` cookie; middleware reads that cookie; `/login` page + `SessionNav`; `auth.oidc.*` Helm values wired into the api deployment. Unit-tested |
| 7 | UI predicted-vs-observed calibration overlay | S-12-04 AC2 / T-12-04-01 | 🟡 partial | `LiveRunShards` renders `actual ?? predicted` (a fallback), not a side-by-side overlay; per-test `confidence` / `model_version` are not surfaced |
| 8 | Mermaid run-history viz in auto-quarantine issue body | S-15-02 AC1 / T-15-02-02 | 🟡 partial | Issue body is plain Markdown; no Mermaid in `internal/quarantine` or `internal/github` |
| 9 | Failure-cluster backfill job | T-07-02-03 | ⏳ missing | No backfill code in `internal/` or `cmd/`; clustering only runs on live ingest |
| 10 | ClickHouse 1M-row load-test harness + p99 numbers | S-02-03 / T-02-03-02 | ⏳ missing | No load test / benchmark; insert path uses per-request `PrepareBatch` (no documented throughput numbers) |
| 11 | Run-list virtual scroll for 100+ rows | S-09-01 AC / T-09-01-03 | ⏳ missing | No virtualization/windowing in `web/src` (the list renders all rows) |
| 12 | Leader-election 2-replica chaos/integration test | S-04-02 / T-04-02-03 | ⏳ missing | The `pg_try_advisory_xact_lock` code exists, but the only runmanager integration test is `reschedule_integration_test.go` — no 2-replica lease/takeover test |

**Not gaps (deferred by an explicit decision — leave as-is):**
- WebSocket subscriptions (S-09-02 AC3 / T-09-02-02) — superseded by 2 s polling, which meets NFR-PERF-107's 3 s SLO; deferred to v1.1.
- Jest AST-signature fingerprint (S-14-02 AC3) — explicitly deferred to v1.5; path + name only at v1.0.

**Release impact:** items **1, 5, 6 are now resolved** (2026-05-20). The remaining functional gaps are **3 and 4** (Go/Python AST fingerprints); item **2** (plan archived to S3) is a partial — `teo replay` works off the Postgres-persisted plan, but the S3 `runs/<id>/plan.json` archive named in the AC is still absent. The rest are polish (7, 8, 9, 11) or test debt (10, 12). Triage 2/3/4 into *v1.0 must-fix* or *defer to v1.1 (with an ADR/row update)* before cutting the tag.

---

## Epic-level dashboard

> 🟡 rows below have one or more **open acceptance criteria** from the reconciliation table above — the Notes describe delivered scope; the 🟡 flags what's still missing. E-02 and E-04 keep ✅ but carry open follow-ups (#10 load test, #12 leader-election test); E-07 keeps ✅ with a minor backfill follow-up (#9).

| ID | Epic | Status | Notes |
|---|---|---|---|
| **E-01** | Foundation: monorepo, CI, base services | ✅ | `go.mod`, `Makefile`, `.golangci.yml`, `.github/workflows/ci.yml`, all hygiene files. 7 service stubs compile. |
| **E-02** | Postgres + ClickHouse schema + migrations | ✅ | Initial migration files for both stores. `internal/migrate` runner + `teo migrate` CLI subcommand. |
| **E-03** | API gateway + auth + audit log | ✅ | chi-based REST (`/api/v1/runs` POST/GET/cancel), JWT (HS256) issuer, argon2id-hashed API keys with 30s revocation cache, audit middleware. RFC 7807 errors. |
| **E-04** | Run Manager state machine + leader election | ✅ | 8-state machine with legality table. Per-run `pg_try_advisory_xact_lock` (ADR-0013). Budget enforcement loop. |
| **E-05** | Scheduler (LPT) + heuristic predictor | 🟡 | Pure-function `PlanFunc`, exclusivity tags, quarantine lane, deterministic tie-break, brute-force property test verifies makespan ≤ 4/3 × OPT over 30 random instances. |
| **E-06** | Worker agent + pytest adapter + redactor | 🟡 | Worker claims via Postgres SKIP-LOCKED; pytest adapter parses `--collect-only` + `--json-report`; redactor scrubs AWS keys, JWTs, GitHub PATs, generic high-entropy patterns. **Per-test log capture** (FR-404) uploads redacted stdout/stderr to S3 through `internal/logstore` (transfermanager-driven, auto-multipart >16MB); the resulting object key is persisted into `test_executions.log_object_key`. |
| **E-07** | Result pipeline (OTLP + failure clustering) | ✅ | OTLP gRPC receiver implements `collectorpb.TraceServiceServer` on `:4317`, writes to ClickHouse `teo.span_events`, routes ERROR-status spans into `failure_clusters`. Python-aware traceback fingerprinting + generic fallback. **JUnit + OTLP run-exports** wired at `GET /api/v1/runs/{id}/export?format={junit\|otlp}` — JUnit reads `test_executions`+`failure_clusters` from Postgres (one `<testsuite>` per shard), OTLP reads `teo.span_events` via the `SpanQuerier` interface and emits binary protobuf (or `?as=json` for `protojson`). |
| **E-08** | Flake detection (Wilson interval) | ✅ | `WilsonInterval` formula with textbook tests. Nightly job (`internal/flake/job.go`) classifies flaky / broken / insufficient. |
| **E-09** | Web UI (Next.js + GraphQL) | 🟡 | All four pages migrated to **GraphQL only** (zero `/api/v1/*` REST calls left in `web/src/app/`). Run-detail page uses a **2s-polling Client Component** (`LiveRunShards`) that auto-stops when status is terminal — meets NFR-PERF-107's 3-second SLO. **Rerun-failed button** wired with mutation proxy. Two Next API routes (`/api/graphql/run`, `/api/graphql/rerun`) keep the API key server-side. **6 frontend Vitest files** + **13 GraphQL integration tests** under `-tags=integration` (testcontainers-driven real-Postgres tests covering every resolver, the run-by-id query, the rerunFailed mutation, schema error envelope, and the full HTTP roundtrip). WebSocket subscriptions remain deferred to v1.1 per strategy §3 (polling meets the freshness SLO). |
| **E-10** | GitHub App integration + Checks API | ✅ | Webhook receiver with HMAC SHA-256 verification, installation event handler, Check Runs + Issues REST clients. **`runmanager.RunObserver` interface** dispatches a snapshot to every registered observer after each successful state transition (post-commit, so observer latency can't hold the lock). **`github.CheckObserver`** creates the Check Run on the first transition out of `pending`, updates it on every in-flight transition with shard counts, and finalizes with conclusion + Markdown summary including the **top-3 failure clusters** for the run (S-10-03). Migration 003 adds `runs.github_check_run_id` + `runs.github_installation_id`. cmd/run-manager wires the observer when `TEO_GITHUB_TOKEN` is set; warns and skips otherwise. |
| **E-11** | Helm chart + observability + release pipeline | ✅ | Umbrella chart with API/Run Manager/result-pipeline Deployments, migrations pre-upgrade Job, three quarantine + digest + ML-train CronJobs, Karpenter NodePools. **Subcharts vendored** in `Chart.yaml` (CloudNativePG, ClickHouse Operator, NATS, MinIO, Dex) — each toggleable so operators can BYO managed services. **Five Grafana dashboards** as ConfigMaps (API latency, scheduler, run state machine, ClickHouse lag, NATS lag) labeled for sidecar discovery. **Six PrometheusRule alerts** with `runbook_url` annotations pointing at `docs/operations/runbooks/`. **`.goreleaser.yml`** for binary releases (linux/darwin amd64/arm64) with cosign keyless signing + Syft SBOMs; **release.yml** workflow runs goreleaser then publishes the chart to gh-pages via chart-releaser. **chart-testing config** (`.github/ct.yaml`) + upgraded CI helm job that templates the on/off subchart matrix. **Restore drill runbook** at `docs/operations/restore-drill.md` with reproducible 8-section procedure and history log. |
| **E-12** | ML predictor (Python, LightGBM) | 🟡 | FastAPI app, model registry with S3-backed per-repo TTL cache, training script with champion/challenger gating, **real ClickHouse query** for training data, synthetic fallback for dry-runs. Dockerfile. |
| **E-13** | Karpenter + spot-aware scheduling + checkpointing | ✅ | IMDSv2 poller (`internal/spot/`) wired into Agent via the `SpotInterruptionSource` interface. **Draining state machine** in the Agent: atomic `draining` flag, current-shard tracking, `beginDrain()` marks the in-flight shard `preempted` and bumps `runs.preemption_count`. Both the Postgres-claim and NATS-handler paths early-return when draining; NATS naks back so a non-draining worker picks up the message. **Run Manager reschedule sweep** (`reschedulePreempted`, every 5s) finds preempted/lost shards, computes the set of unfinished tests (intended-via-round-robin minus already-recorded `test_executions`), and creates a fresh pending shard with that residue under `runs.meta.reshards[<new_shard_id>]`. Worker's `loadTestsForShard` consults that map first, falling back to round-robin. Migration 004 adds `shards.meta` for the `rescheduled_at` dedupe marker. Three integration tests cover the happy path (only-uncompleted), all-completed no-op, and idempotent-second-sweep dedupe. |
| **E-14** | Additional runner adapters | 🟡 | `pkg/adapter/gotest` parses `go test -json` events; `pkg/adapter/jest` runs Jest with `--json --outputFile` and parses assertion results. **SPI contract documented at `docs/adapters/spi.md`** (interface, Discover/Execute semantics, fingerprint/redaction/OTel boundary, conformance checklist) with a copy-and-fill skeleton at `pkg/adapter/template/`. |
| **E-15** | Auto-quarantine workflow + GitHub Issue creation | 🟡 | CODEOWNERS parser + matcher, quarantine daemon transitions tests with broken-vs-flaky distinction. **GitHub Issues REST client** (`internal/github/issues.go`) wires Open/Comment/Patch via the same auth model as Check Runs. Re-quarantine dedupes via comment instead of duplicate issue. **SLA nudge sweeper** (`internal/quarantine/sla.go`) posts after `flake.sla_days` and is idempotent on `last_nudged_at`. **Un-quarantine proposer** (`internal/quarantine/unquarantine.go`) tracks K consecutive passes, posts a magic-link comment; `/api/v1/quarantine/restore?token=…` consumes it (single-use, token-gated, no auth required since the link is the auth). |
| **E-16** | Owner digest (weekly Slack/email) | ✅ | HTML + plain-text templates, per-author aggregation, SMTP sender (with mock-dialer tests), Slack webhook sender (httptest-verified), Multiplex fanout, opt-out enforcement (per-user via `users.digest_opt_out`, per-repo via `repos.meta.digest_opt_out`), `result-pipeline owner-digest` subcommand wired into the Helm CronJob, `teo digest dry-run --user=<email>` CLI for inspection. |

---

## Gap-closeout PR (post-E-16)

These were called out as the realistic gaps after the E-01..E-16 sweep, and are now resolved:

| # | Gap | Status | Code |
|---|---|---|---|
| 1 | OTLP gRPC ingest server | ✅ | [`internal/resultpipeline/otlp.go`](internal/resultpipeline/otlp.go), [`cmd/result-pipeline/main.go`](cmd/result-pipeline/main.go) |
| 2 | NATS dispatch + worker subscriber | ✅ | [`internal/nats/`](internal/nats), [`internal/worker/nats.go`](internal/worker/nats.go), publish path in [`internal/runmanager/manager.go`](internal/runmanager/manager.go) |
| 3 | gRPC server in cmd/api | ✅ | [`internal/grpcsvc/workers.go`](internal/grpcsvc/workers.go) embeds `teov1.UnimplementedWorkersServer` from [`internal/proto/teov1/`](internal/proto/teov1) (binary protobuf wire format); generation wired via [`proto/buf.yaml`](proto/buf.yaml) + [`proto/buf.gen.yaml`](proto/buf.gen.yaml) and the `make proto` target. JSON codec deleted. |
| 4 | Real ClickHouse query in predictor trainer | ✅ | [`services/predictor-ml/src/teo_predictor_ml/train.py`](services/predictor-ml/src/teo_predictor_ml/train.py) |
| 5 | GraphQL read API | ✅ | [`internal/api/graphql.go`](internal/api/graphql.go) — endpoint live, schema served at `/graphql/schema`. UI swap landed under E-09 (zero `/api/v1/*` REST calls in `web/src/app/`). **Authz now enforced**: route returns 401 for missing principal, 403 for principals with no role; the `rerunFailed` mutation gates on `RoleEngineer`/`RoleAdmin` via the new `requireMutationRole` helper, so read-only browsers can read but cannot mutate. |
| 6 | Lint sweep | ✅ | `slices.Contains`, `strings.CutPrefix`/`CutSuffix`, builtin `max`/`min`, `any` over `interface{}`, dead helpers removed. |

---

## Functional requirement coverage

Each FR from [`docs/requirements/functional.md`](docs/requirements/functional.md) tracked here. ✅ = wired end-to-end; 🟡 = code path exists with a documented follow-up; ⏳ = pending; 📦 = deferred.

> ⚠ Reconciliation update (2026-05-20): **FR-703–705** (#5 log-tail viewer) and **FR-801** (#6 OIDC sign-in flow) are now wired. **FR-304** is partially resolved — `teo replay` (#1) landed, but the plan is archived in Postgres, not the S3 `runs/<id>/plan.json` path the AC names (#2). Treat the [Backlog reconciliation](#backlog-reconciliation--verified-incomplete-items-2026-05-20) table as ground truth where rows disagree.

### FR-100 Run intake
- ✅ FR-101..104: create/cancel via REST, idempotency via `Idempotency-Key`
- ✅ FR-105: budget enforcement loop in Run Manager
- ✅ FR-106: `pytest` + `go test` + `jest` adapters present (E-14); runner-image variants wired into the Helm chart at [`deploy/helm/teo/values.yaml`](deploy/helm/teo/values.yaml) (`workers.runners.{pytest,gotest,jest}.image`)

### FR-200 Test discovery & manifest
- ✅ FR-201..204: pytest discovery with `--collect-only`, manifest schema in `internal/model`

### FR-300 Scheduling & sharding
- ✅ FR-301..306: LPT, exclusivity, plan persistence, replay-friendly
- ✅ FR-308: Karpenter NodePools + IMDSv2 poller + Agent draining state machine + Run Manager reschedule sweep + worker reshard manifest lookup; preemption_count visible per run
- 📦 FR-307 (speculative re-execution), 📦 FR-309 (cost budget)

### FR-400 Worker execution
- ✅ FR-401..403: claim, execute, OTel span emission per test
- ✅ FR-404: per-test stdout/stderr captures uploaded to S3 via the new `internal/logstore` package. `Uploader` interface; production `S3` impl wraps `aws-sdk-go-v2/feature/s3/transfermanager` (auto-promotes to multipart >16MB); `Noop` impl for dev/CI. Worker's `recordResult` redacts both streams + the failure block, concatenates them, uploads to `runs/{runID}/shards/{shardID}/tests/{testID}/{attempt}.log`, and persists the resulting key into `test_executions.log_object_key`. `cmd/worker` constructs the S3 uploader when `TEO_S3_BUCKET` is set; otherwise `Noop`. Empty captures skip the upload.
- ✅ FR-405..407: heartbeat, idempotent reports, ShardFinished
- ✅ FR-408: redactor on the worker

### FR-500 Result aggregation
- ✅ FR-501: at-least-once writes + idempotent upsert (`(shard, test, attempt)` unique constraint)
- ✅ FR-502: failure clustering by stack-trace fingerprint
- ✅ FR-503/504: JUnit + OTLP export endpoints at `GET /api/v1/runs/{id}/export?format={junit|otlp}`. JUnit XML maps outcomes (passed→empty, failed→`<failure>`, errored/timed_out/interrupted→`<error>`, skipped→`<skipped/>`), embeds the failure-cluster representative message + stack on failed cases, and emits per-suite + total roll-ups. OTLP rebuilds `collectorpb.ExportTraceServiceRequest` from `teo.span_events` and ships binary proto by default (`?as=json` for `protojson`). When ClickHouse isn't configured, OTLP returns 501; JUnit still works because it reads Postgres only. The `SpanQuerier` interface keeps the OTLP path unit-testable.
- ✅ FR-505: rerun-failed mutation — see FR-700 for the UI side; the GraphQL mutation `rerunFailed(runId)` resolves through `internal/api/graphql_resolvers.go` and is covered by both unit and integration tests.

### FR-600 Flake detection
- ✅ FR-601..604: Wilson interval, classification, nightly job
- ✅ FR-605..606: auto-quarantine state transition, non-blocking lane
- ✅ FR-607: ML predictor service with heuristic fallback
- ✅ FR-609: GitHub Issue creation, dedupe-via-comment, CODEOWNERS-resolved assignees, SLA nudge sweeper, un-quarantine proposal flow with magic-link restore endpoint

### FR-700 UI
- ✅ FR-701..705: run list, run timeline, failure clusters, flake history — all on GraphQL via `gqlFetch` + named operations from `web/src/lib/queries.ts`
- ✅ FR-706: live updates via 2s polling on the run-detail page (`LiveRunShards`); polling auto-stops on terminal status; tested with fake timers
- ✅ FR-505 (rerun-failed): UI button + Next API route + GraphQL mutation; navigates to the new run on success
- ✅ FR-708: weekly per-author digest with WoW delta, SMTP + Slack delivery, opt-out enforcement, dry-run CLI
- ✅ FR-709 (cost dashboard): weekly $/build trend + spot share at `/cost`. New `internal/cost` pricing helper (Pricer with SpotPerMin + OnDemandPerMin rates; reads `TEO_COST_SPOT_PER_MIN` / `TEO_COST_ONDEMAND_PER_MIN` overrides; defaults $0.012/$0.040 per worker-minute). New `costSummary(weeks: Int = 8)` GraphQL query aggregates `teo.runs` rows by `date_trunc('week', started_at)` and emits `CostWeek { weekStart, runs, spotMinutes, onDemandMinutes, totalCost, costPerBuild, spotShare }`. Next.js page server-renders the table + a CSS bar visualization of the per-build trend (no charting lib).

### FR-800 Identity & authz
- ✅ FR-801: OIDC via Dex (chart wiring)
- ✅ FR-802: API keys with prefix + argon2id hash
- ✅ FR-803..805: roles, audit logging, revocation within 30s

### FR-900 GitHub integration
- ✅ FR-901: GitHub App scaffold; webhook signature verification
- ✅ FR-902/903: `RunObserver` pattern in Run Manager; `CheckObserver` creates/updates/finalizes the Check Run; failure summary embeds the top-3 failure clusters (Markdown), deep-link to TEO via `TEO_BASE_URL`
- ✅ FR-904: HMAC verification + 401 on tamper

### FR-1000 Operations
- ✅ FR-1001: Helm install path (`helm install teo deploy/helm/teo …`); subcharts vendored in `Chart.yaml`; chart-testing CI job templates the on/off matrix
- ✅ FR-1002: pre-upgrade migration Job
- ✅ FR-1003: `/healthz`, `/readyz` on API; **`internal/metrics`** centralises every `teo_*` collector (the dashboards/alerts shipped in E-11 reference these names exactly); chi middleware records `http_server_requests_seconds` per (handler, method, status) with the chi RoutePattern as label so cardinality stays bounded; Run Manager emits `teo_runs_active`, `teo_run_transitions_total`, `teo_runs_stuck_total`, `teo_scheduler_plan_seconds`/`_total`; result-pipeline emits `teo_clickhouse_inserts_total` + `teo_clickhouse_insert_seconds` + `_failures_total`; `/metrics` served on the API + dedicated `:9100` listener on the headless services.
- ✅ FR-1004: CloudNativePG + clickhouse-backup operator vendored; restore drill runbook at `docs/operations/restore-drill.md`
- ✅ FR-1005: `teo doctor` CLI runs Postgres / ClickHouse / NATS / API / predictor checks in parallel under a single deadline. Skipped checks (no DSN/URL configured) don't fail the exit code; any Fail flips it to 1. `--json` flag for scripting; humans get a tabwriter-aligned table with a one-line summary. The `internal/doctor` package is the reusable backbone — same Check interface usable from a future in-process diagnostic endpoint.

### Deferred (📦 confirmed via ADR-0012 revised)
- 📦 What-if simulator
- 📦 Cost-budgeted execution
- 📦 LLM root-cause hints
- 📦 RSpec / JUnit-direct / Bazel adapters
- 📦 Multi-cloud worker pool
- 📦 Speculative re-execution
- 📦 Cross-repo test impact analysis
- 📦 SOC 2 readiness

---

## Test coverage snapshot

```
ok  internal/api                       (graphql schema + resolvers; OTLP export
                                        round-trip via stub SpanQuerier; hexToBytes
                                        + formatSeconds helpers)
ok  internal/audit                     (nil-Logger + nil-pool no-op guards on Log)
ok  internal/cost                      (Pricer.RunCost on positive/zero/negative
                                        inputs; NewFromEnv default + override +
                                        invalid/negative-fallback paths)
ok  internal/db                        (parseClickHouseDSN: full URL, default db,
                                        no-creds, user-only, applied defaults,
                                        malformed-URL rejection)
ok  internal/logstore                  (Noop body-drain + nil-safe; S3 conforms to
                                        Uploader interface)
ok  internal/predictor                 (NewHeuristic seeded defaults; Predict nil
                                        guards; coldStart fingerprint+P95 = 3×P50;
                                        coldOnly preserves order; defaultFor lookup
                                        + fallback)
ok  internal/quarantine                (issue-body markdown structure + key-fact
                                        substitution + zero-sample NaN guard;
                                        GitHubOpener nil-receiver + nil-client
                                        Open/Comment guards)
ok  internal/worker                    (drain idempotency; uploadLog key shape +
                                        redaction + skip-when-empty + upload-error
                                        handling)
ok  internal/auth                      (JWT roundtrip, argon2id verify+tamper)
ok  internal/codeowners                (rule precedence, path matching)
ok  internal/digest                    (SMTP MIME composition, ctx cancel, Slack
                                        webhook payload + 4xx fail, Multiplex fanout,
                                        owner→user matching)
ok  internal/flake                     (Wilson textbook, classify states)
ok  internal/github                    (HMAC verify valid + tampered + malformed)
ok  internal/migrate                   (statement splitting)
ok  internal/nats                      (Connect("") → ErrUnavailable; ShardDispatch
                                        JSON wire format incl. time.Time round-trip
                                        + omitempty optional fields; subject/stream
                                        constants pinned)
ok  internal/redact                    (AWS keys, JWTs, PATs scrubbed; clean text untouched)
ok  internal/resultpipeline            (Python fingerprint stable across line-numbers; OTLP attr/status mapping)
ok  internal/runmanager                (state transitions, terminal detection)
ok  internal/scheduler                 (LPT determinism, exclusivity, quarantine,
                                        makespan ratio ≤ 4/3 × brute-force optimum on 30 instances)
ok  internal/spot                      (IMDS interruption parsing)
ok  internal/version                   (build identity round-trip)
ok  pkg/adapter/gotest                 (dedupe order/empty; mergeEnv append/no-op;
                                        processEvents pass/fail/skip + package-level
                                        ignore + malformed-line skip + synthetic
                                        entry; New defaults)
ok  pkg/adapter/jest                   (translate full status table; mergeEnv
                                        append/no-op; parseListTests path-relative
                                        + malformed-rejection; parseReport assertion
                                        emit + nested ancestors + todo→skipped +
                                        empty-no-op + malformed-rejection)
ok  pkg/adapter/pytest                 (collect-only parser)
ok  pkg/adapter/template               (SPI invariants: Name non-empty, empty-tests
                                        no-op, unknown-status → errored, mergeEnv)
```

**Now covered by integration tests** (`-tags=integration`): `internal/api/graphql_resolvers.go` (every read resolver + the rerunFailed mutation, against a real Postgres), full `/graphql` HTTP roundtrip via `api.Server`, **run-export REST endpoints** (JUnit XML happy-path + mixed-outcome shape, OTLP+JSON happy-path with stubbed SpanQuerier, 400/404/501 negative paths), **run-intake REST endpoints** (`internal/api/runs.go` Create/Get/Cancel — happy paths, validation, idempotency-key replay returns same id, auth-required 401, unknown-repo 404, terminal-run cancel idempotent), **logstore S3 round-trip against MinIO** (small body via single PUT, >16MB body promoted to multipart by transfermanager, same-key overwrite — uses a dedicated `internal/testminio.Start` testcontainer harness). **`internal/nats`** is now unit-tested for the broker-free surface: empty-URL `Connect` returns `ErrUnavailable`, `ShardDispatch` JSON wire format pinned (field tags + `time.Time` round-trip + `omitempty` on optional `DispatchTest` fields), and the five subject/stream constants are pinned against accidental rename. The `Connect`/`EnsureStreams`/`Publish` paths that need a live broker remain integration-tier. **Still uncovered (all leaf packages):** `internal/config` (struct definitions only), `internal/grpcsvc` (thin gRPC service wrappers), `internal/model` (data types). The DB-backed code paths in `internal/audit`, `internal/db`, `internal/predictor`, `internal/quarantine` daemons remain integration-tier — the harnesses in `internal/testpg/` and `internal/testminio/` make those follow-ups straightforward.

---

## What's next (named follow-ups, prioritized)

The core of all 16 epics is wired, but the 2026-05-20 audit reopened **12 backlog items** (see [Backlog reconciliation](#backlog-reconciliation--verified-incomplete-items-2026-05-20)). Prioritized below; close or formally defer (ADR + row update) each before tagging v1.0.0.

**Resolved 2026-05-20:** #1 `teo replay` CLI, #5 log-tail viewer, #6 OIDC sign-in flow (see the reconciliation table for what landed).

**Functional must-triage (block an honest v1.0):**
1. Go AST-signature fingerprint (#3, S-14-01 AC4) and Python AST fingerprint (#4, T-06-01-03) — fingerprints are `path::name::paramsHash`, so a renamed/moved test loses its history.
2. Plan archived to S3 `runs/<id>/plan.json` (#2, S-05-04 AC1) — `teo replay` already verifies determinism off the Postgres-persisted plan; the S3 archive is the remaining piece of FR-304.

**Lower priority (polish / coverage):**
5. Predicted-vs-observed calibration overlay (#7, S-12-04), Mermaid issue history (#8, S-15-02), run-list virtual scroll (#11), failure-cluster backfill (#9).

**Test/verification debt:**
6. ClickHouse 1M-row load test (#10, S-02-03) and leader-election 2-replica chaos test (#12, S-04-02) — both need a CI testcontainers run.

Already real and not in question: build is clean; 25 unit packages + 38 integration tests green; `make proto` → `buf generate` wiring landed (binary-protobuf gRPC codec); the release pipeline (E-11) cuts a tag → goreleaser → cosign-signed binaries + SBOMs + chart-released Helm chart; restore-drill runbook written; metrics + dashboards + alerts emit and fire.

---

## How this file stays accurate

- Every PR that lands an FR or closes a follow-up updates the corresponding row here in the same commit.
- Reviewers refuse to merge a PR that changes behavior without a `progress.md` delta.
- The CI pipeline does not enforce this yet — it's a social contract until a `progress-lint` step lands.
