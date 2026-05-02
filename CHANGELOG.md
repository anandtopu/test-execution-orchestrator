# Changelog

All notable changes to TEO are documented here. The format is based on
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the project adheres to
[Semantic Versioning](https://semver.org/spec/v2.0.0.html) once 1.0 ships.

For a finer-grained per-FR / per-epic implementation status, see [`progress.md`](progress.md).

## [Unreleased]

### Fixed — CI run-3 failures (release-blocking)
- **Vitest — "ReferenceError: React is not defined" in 18 component tests.** `web/tsconfig.json` sets `"jsx": "preserve"` (lets Next.js handle the transform in dev/build), so Vitest's esbuild loader fell back to the *classic* JSX transform — which expects `React` to be in scope on every `.tsx` file. The component tests omit `import React from 'react'` because Next.js handles that with the automatic runtime. Fix: add `esbuild: { jsx: 'automatic' }` to `web/vitest.config.ts` so Vitest uses the same automatic runtime in tests. Verified locally: 42 / 42 tests now pass.
- **Vitest — DOM cleanup not running between tests.** With the JSX fix landed, three `StatusBadge` tests then failed with "Found multiple elements by: [data-testid=status-badge]" because `@testing-library/react`'s automatic after-each cleanup only registers when Vitest globals are enabled (we run with `globals: false`). Wired it manually in `web/src/test-setup.ts`: `afterEach(cleanup)`. Without this fix the JSX repair would have surfaced a fresh failure on the next CI run.
- **Go — `TestAPIKeyHashAndVerify` flaked 1-in-64 on CI.** The test tampered with the API key by replacing the last character: `bad := display[:len(display)-1] + "X"`. When the random base64 secret already ended with `X` (1/64 ≈ 1.5% per run), the replacement yielded the *same* string — argon2id verified successfully and the test reported "tampered key verified". CI rolled an X. Fix: append the sentinel instead of replacing it (`bad := display + "X"`) — the modified string is now guaranteed to differ from the original regardless of which random byte the generator produced. Stress-tested 200 iterations locally: green.

### Fixed — CI run-2 failures (release-blocking)
The first CI fix (simple-protocol on the migration driver) didn't help — pgx still rewrote the SQL and Postgres kept rejecting `001_initial.up.sql` at the first `(`. Two fixes here:

- **Migration runner — Postgres-aware SQL splitter.** Replaced the naive line-based `splitStatements` with `splitSQL` in `internal/migrate/migrate.go`: tracks `$$ ... $$` and `$tag$ ... $tag$` dollar-quoted strings (so the plpgsql trigger-function body in migration 001 stays intact), `'...'` literals with the SQL-standard `''` escape, `"..."` quoted identifiers, `--` line comments, and `/* */` block comments. Splits on top-level `;` and Execs each statement individually — sidesteps the entire pgx parameter-rewrite path that broke us in CI even with `QueryExecModeSimpleProtocol`. Per-statement error messages now point at the offending statement number + first line. Eight unit tests cover the regression (dollar-quoted function), tagged dollar quotes (`$func$ ... $func$`), escaped single quotes with embedded `;`, line/block comments containing `;`, and quoted identifiers — all locking in the corner cases.
- **`web/src/app/page.tsx` line 11**: `{' — see what's running and why builds are red.'}` — the apostrophe in `what's` closed the JS string literal mid-expression, so `tsc --noEmit` failed with TS1005 + TS1381. Pre-existing bug; never surfaced because Vitest doesn't typecheck. Switched the outer string from single to double quotes.

### Fixed — CI run-1 failures (release-blocking)
First CI run on the published GitHub repo surfaced three failures, all now resolved:

- **Migration runner — `syntax error at or near "("` on every integration test.** Root cause: `internal/migrate/migrate.go` opened pgx via `sql.Open("pgx", dsn)`, which defaults to `QueryExecModeCacheStatement`. In that mode pgx scans the SQL string for `$N` parameter placeholders and corrupts dollar-quoted plpgsql function bodies (`$$ ... $$`) — Postgres then sees the rewritten text and fails on the first `(` of the next CREATE TABLE. Fix: parse the DSN with `pgx.ParseConfig`, set `DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol`, and open via `stdlib.OpenDB(*cfg)`. Simple protocol forwards the SQL byte-for-byte to Postgres. Reason captured as a comment in `openDriver` so this doesn't regress on the next refactor. All ~30 integration tests across `internal/api` and `internal/runmanager` were blocked on this one bug.

- **Helm `chart-testing` — `clickhouse-operator chart not found`.** The Altinity chart is published as `altinity-clickhouse-operator` (not `clickhouse-operator`) in the repo at `https://docs.altinity.com/clickhouse-operator/` (the trailing slash matters for Helm's `index.yaml` resolution). Updated `deploy/helm/teo/Chart.yaml`'s dependency entry and the `helm repo add` lines in both `.github/workflows/ci.yml` and `release.yml`. The `clickhouse` alias is preserved so `values.yaml` toggles (`clickhouse.enabled=...`) continue to work without consumer-side churn.

- **Web `setup-node` — `Some specified paths were not resolved, unable to cache dependencies`.** `web/package-lock.json` had never been generated, so the `cache-dependency-path: web/package-lock.json` hint couldn't compute a cache key. Generated the lockfile via `npm install --package-lock-only --legacy-peer-deps` and committed it (349 KB, 587 packages resolved). Added `web/.npmrc` with `legacy-peer-deps=true` so `npm ci` in CI doesn't fail the React 19 / Radix UI / `@visx/visx` peer-dep matrix that hasn't fully aligned yet. Verified locally: `npm ci --dry-run` reports "added 587 packages in 623ms".

### Changed — gRPC: protoc-gen-go wiring (JSON codec retired)
- **`make proto`** now actually generates code: new [`proto/buf.yaml`](proto/buf.yaml) + [`proto/buf.gen.yaml`](proto/buf.gen.yaml) drive `buf generate` against the locally-installed `protoc-gen-go` and `protoc-gen-go-grpc`. Lint runs as STANDARD with four targeted exceptions documented in `buf.yaml` (PACKAGE_DIRECTORY_MATCH, SERVICE_SUFFIX, RPC_REQUEST_RESPONSE_UNIQUE, RPC_RESPONSE_STANDARD_NAME, RPC_REQUEST_STANDARD_NAME) — keeping the existing service / message names rather than wire-breaking renames for cosmetic gain.
- **`proto/teo/v1/*.proto` → `proto/teov1/*.proto`** flat layout. The Protobuf package stays `teo.v1` (wire-compatible) but the file path now matches the `option go_package = ".../internal/proto/teov1;teov1"`, so `paths=source_relative` produces the right output dir without acrobatics.
- **`internal/proto/teov1/`** is the new home for the generated `runs.pb.go`, `runs_grpc.pb.go`, `workers.pb.go`, `workers_grpc.pb.go`. Generation is idempotent under `make proto`.
- **`internal/grpcsvc/workers.go` rewritten** to use the generated bindings: embeds `teov1.UnimplementedWorkersServer` (required under `require_unimplemented_servers=true`), accepts `*teov1.PullAssignmentRequest` / `*teov1.TestFinished`, returns `*teov1.Assignment` / `*teov1.Ack`. The Register helper now calls `teov1.RegisterWorkersServer(srv, ws)`. All hand-written JSON-tagged structs (`PullAssignmentRequest`, `Assignment`, `TestEntryRef`, `TestFinished`, `Ack`) and the hand-rolled `grpc.ServiceDesc` are gone.
- **`internal/grpcsvc/jsoncodec/` package deleted** along with its codec roundtrip tests. The `cmd/api/main.go` `init()` that registered the JSON codec is removed; `grpc.NewServer()` no longer passes `grpc.ForceServerCodec(...)`. The wire format on `:9090` is now binary protobuf — what every gRPC client expects out of the box.
- **`progress.md`**: gap-closeout PR row 3 flips 🟡 → ✅; the "▶ Resume here" callout drops Path A entirely (Path B / Cut v1.0.0 is the only remaining recommendation); unit-package count adjusted from 21 → 20 to reflect the `jsoncodec` deletion; "What's next" stops listing protoc wiring as a polish item.

### Fixed — Release pipeline drift (latent v1.0.0 blockers)
- **Go version pin drift**: `.github/workflows/ci.yml` (`env GO_VERSION="1.23"`) and `.github/workflows/release.yml` (`go-version: '1.23'`) both pre-dated the bump to `go 1.25.0` in `go.mod`. Either workflow would have failed at `setup-go` against the current module — the release pipeline silently broken from the day the toolchain bumped. Both bumped to 1.25.
- **Deprecated `.goreleaser.yml` syntax**: `archives.builds: [teo]` is deprecated as of goreleaser v2.15. Replaced with `archives.ids: [teo]` so `goreleaser check` passes cleanly.
- **Verified end-to-end** via `goreleaser release --snapshot --clean --skip=sign,sbom,announce` against goreleaser v2.15.4. All seven services build across linux/darwin amd64/arm64 (plus windows for the `teo` CLI), six archives produced (`teo_*.tar.gz` + `teo_*.zip`), `checksums.txt` written. The cosign and syft steps skipped (binaries not installed locally; CI installs them via `cosign-installer@v3` and `sbom-action/download-syft@v0`). Build pass time ~90s on the local Windows machine — confirms no surprise compilation or template-rendering issues block tagging v1.0.0.

### Added — Cost dashboard (FR-709 completed)
- **`internal/cost`** new pure-function package: `Pricer{SpotPerMin, OnDemandPerMin}` plus `NewFromEnv()` reading `TEO_COST_SPOT_PER_MIN` / `TEO_COST_ONDEMAND_PER_MIN` (defaults $0.012 / $0.040 per worker-minute — m5.xlarge ballpark; operators with cost-explorer data should override). `RunCost(spotMin, ondemandMin)` clamps negatives to zero rather than producing wrong-signed numbers from schema drift. Six tests cover positive / zero / negative / mixed inputs and the env override + fallback contract.
- **`costSummary(weeks: Int = 8)` GraphQL query** in `internal/api/graphql.go`. Resolver `queryCostSummary` aggregates `teo.runs` by `date_trunc('week', started_at)` over the requested window, applies the Pricer in Go (keeps rate config in one place + SQL portable), returns `[CostWeek!]!` with `weekStart, runs, spotMinutes, onDemandMinutes, totalCost, costPerBuild, spotShare`. Runs without `started_at` are excluded — pending/cancelled-before-start rows shouldn't show in spend reporting. Window arg clamps to `[1, 52]` weeks.
- **`web/src/app/cost/page.tsx`** server-renders the dashboard via `gqlFetch` + the new `CostSummaryQuery` in `web/src/lib/queries.ts`. Table columns: Week of, Runs, $/build, Spot %, Total, plus a CSS-only horizontal bar of the per-build trend normalized against the largest week — no charting library added. New `formatDollars()` helper in `format.ts` handles null/NaN gracefully. Nav link added in `layout.tsx`.
- **Tests**: Go unit tests for `Pricer.RunCost` and `NewFromEnv`; two new integration cases (`TestQueryCostSummary_AggregatesByWeek`, `TestQueryCostSummary_ExcludesRunsWithoutStartedAt`) seed `spot_minutes`/`on_demand_minutes` on the existing fixture and assert the cost arithmetic + the started_at-required filter. Frontend Vitest covers `formatDollars` (two-decimal USD + null/NaN em-dash) and structural tests for the new `CostSummaryQuery` (every field the page consumes, named operation, `$weeks: Int` arg).
- **`progress.md`**: FR-709 flips ⏳ → ✅; Go unit packages 20 → 21; integration tests 36 → 38.

### Added — MinIO testcontainer harness + logstore S3 integration test
- **`internal/testminio`** package mirrors the `internal/testpg` pattern: `Start(t)` launches `minio/minio` via testcontainers with default root creds (`minioadmin`), waits on `/minio/health/ready`, and returns `(endpoint, accessKey, secretKey, region, cleanup)`. Build-tagged `integration` so unit-test runs don't require Docker.
- **`internal/logstore/s3_integration_test.go`** exercises the production S3 path end-to-end against MinIO: small body via single PUT (round-trip equality), 17MB body that crosses the transfermanager's 16MB multipart threshold (sha256 + length verification — comparing 17MB byte slices in failed-test output is useless), same-key overwrite. Uses `t.Setenv` to inject `AWS_ACCESS_KEY_ID/SECRET/REGION` so `config.LoadDefaultConfig` picks them up without leaking process-wide state into sibling tests.
- **`progress.md`**: integration count rises from 33 → 36; "Still uncovered" loses the logstore-MinIO bullet. The build-status line now mentions both harnesses (testpg + testminio).

### Added — REST run-intake integration tests
- **`internal/api/runs_integration_test.go`** closes the "Still uncovered" callout for `internal/api/runs.go`. Ten cases against a real Postgres via `testpg.Start`: POST happy-path + 401 + validation (multiple field errors in one envelope) + 404-on-unknown-repo + Idempotency-Key replay (asserts same run id returned, only one row in DB); GET by id happy-path + 404 + 401; POST cancel running→cancelled + idempotent on terminal run + 404. Uses `auth.JWTIssuer.Issue` to obtain a real Bearer token so requests pass the auth middleware.
- **`progress.md`**: drops `internal/api/runs.go` from the "Still uncovered" list; integration-test count rises from 23 → 33. Notes that `internal/logstore` still needs a MinIO-testcontainer harness for an end-to-end S3 roundtrip — interface contract and worker wiring are unit-covered.

### Added — S3 log uploader (FR-404 completed)
- **`internal/logstore`** package introduces an `Uploader` interface (`Upload(ctx, key, body, size) error`) with two impls: `S3` wraps `aws-sdk-go-v2/feature/s3/transfermanager` (auto-promotes single PUT → multipart above 16MB, parallel parts) and `Noop` for dev/CI/where S3 isn't configured. AWS credentials come from the standard chain (env, shared config, IRSA/IMDS); MinIO works through the `endpoint` arg + path-style addressing.
- **Worker `recordResult` integration**: when the adapter Result carries non-empty Stdout/Stderr/Failure fields, the agent redacts them through the existing `redact.New()` chain, concatenates `=== stdout ===`/`=== stderr ===`/`=== failure ===` sections into a scratch buffer, uploads at `runs/{runID}/shards/{shardID}/tests/{testID}/{attempt}.log`, and persists the resulting key into `test_executions.log_object_key` (the column that has been provisioned since migration 001 but not populated until now). Empty captures skip the upload.
- **`Agent.Uploader` field** + `cmd/worker/main.go` wiring: when `TEO_S3_BUCKET` is set the worker constructs `logstore.NewS3(...)`, otherwise `Noop` (so dev / kind clusters / `TEO_LOGSTORE=noop` don't need creds). Init failure is non-fatal — the worker logs a warning and falls back to Noop.
- **Tests**: `internal/logstore/logstore_test.go` covers the Noop body-drain contract + S3-as-Uploader compile-time assertion. `internal/worker/upload_test.go` exercises `uploadLog` with a recording stub: verifies the key shape, the section markers in the body, redaction of an `AKIA…` AWS access key before upload, the empty-streams skip, and that an upload error returns `""` (so `log_object_key` stays NULL) without crashing the result-record path.
- **Why the transfermanager and not the deprecated `feature/s3/manager`**: AWS deprecated the older Uploader in favor of `feature/s3/transfermanager` in late 2024; we adopt the new package directly so this code doesn't carry a same-day deprecation warning. `transfermanager.New(s3Client).UploadObject(ctx, *UploadObjectInput)` is the entry point.
- **`progress.md`**: FR-404 flips from 🟡 to ✅; the E-06 row gains a per-test log capture note. While in the file the stale FR-505 ⏳ entry is corrected to ✅ (the rerun-failed mutation has been wired and tested for several sessions; the FR-700 line already noted this).

### Added — Run-export REST endpoints (FR-503 + FR-504 completed)
- **`GET /api/v1/runs/{id}/export?format={junit|otlp}`** in `internal/api/export.go`. JUnit returns `application/xml` with a `<testsuites><testsuite>` document — one testsuite per shard (`shard-N`), one testcase per `(test, attempt)` joined with `failure_clusters` so failed cases carry both the representative message (as the `message` attr) and the stack (as testcase body). Outcome mapping: `passed`→empty, `failed`→`<failure>`, `errored|timed_out|interrupted`→`<error>`, `skipped`→`<skipped/>`. Suite + total roll-ups for tests/failures/errors/skipped/time emit alongside.
- **OTLP path** queries `teo.span_events` for the run, rebuilds a `collectorpb.ExportTraceServiceRequest` with one `ResourceSpans` (resource carries `teo.run_id` + `service.name=teo`) and one `ScopeSpans` containing every span. Default content type is `application/x-protobuf` (binary `proto.Marshal`); `?as=json` returns `protojson` for human inspection. Span attrs round-trip from the ClickHouse Map column; `teo.run_id`/`teo.test_id` are always present even when the source row's attributes map omitted them.
- **`SpanQuerier` interface** keeps the OTLP path unit-testable: production wiring (`chSpanQuerier`) uses `chdriver.Conn`; the test stub returns canned rows. New `WithClickHouseConn(conn)` and `WithSpanQuerier(q)` options on `api.New(...)`. When ClickHouse isn't configured, `?format=otlp` returns 501 with a clear problem document; JUnit still works because it reads only Postgres.
- **`cmd/api/main.go`** opens a native ClickHouse conn when `TEO_CLICKHOUSE_DSN` is set and threads it through `api.WithClickHouseConn(...)`. The existing API path is unchanged when the env var is unset.
- **Tests**: `internal/api/export_test.go` exercises `buildOTLP` against a stub querier (round-trip of trace_id hex, status code, span attrs, the inserted `teo.run_id`/`teo.test_id`), plus pure-function tests for `hexToBytes` and `formatSeconds`. `internal/api/export_integration_test.go` (under `-tags=integration`) covers the full HTTP path: JUnit happy path with the seed fixture's failed cluster-attached execution; mixed-outcome JUnit (passed + skipped + failed) verifies the per-outcome XML element shape; 400/404/501 negative paths; and an OTLP+JSON happy path with a stubbed `SpanQuerier` against the real Postgres `runs` row.
- **Why the integration test stops at JUnit + stubbed-OTLP**: ClickHouse testcontainers aren't yet wired into `internal/testpg`, so the OTLP→ClickHouse path is exercised by stubbing the querier inside an integration test that still walks through real Postgres + the chi router + auth middleware. A future ClickHouse harness would replace the stub.

### Added — `teo doctor` CLI (FR-1005 completed)
- **`internal/doctor`** — reusable diagnostic backbone with a `Check` interface (`Name() / Run(ctx) Result`), 4-state `Status` enum (ok/warn/fail/skipped), parallel `Run(ctx, checks, deadline)` fanout, and an `ExitCode` mapper that treats Skipped as 0.
- **Concrete checks**: `PostgresCheck` (ping + reports `teo.schema_migrations` version; warn if connected but migrations table missing), `ClickHouseCheck` (same), `NATSCheck` (single-attempt connect, RTT in the message), `HTTPCheck` (any 2xx is OK; injectable client for tests), plus `PoolCheck` and `SQLPingCheck` for callers that already hold a connection.
- **`teo doctor` CLI subcommand** (`cmd/teo/doctor.go`) — flags: `--postgres-dsn`, `--clickhouse-dsn`, `--nats-url`, `--api-url`, `--predictor-url`, `--deadline`, `--json`. Each respects the matching `TEO_*` env var as default. Output: tabwriter-aligned table by default with a `N ok · N warn · N fail · N skipped` summary line, or pretty-printed JSON for scripting. Exit code 1 only if any check failed.
- **Tests** (`internal/doctor/doctor_test.go`): 14 cases covering parallel fanout (asserts ~50ms not ~150ms for 3 × 50ms checks), deadline enforcement, exit-code mapping table, status string, CheckFunc adapter, HTTPCheck 2xx/5xx/empty/unreachable branches, and the skip-when-not-configured contract for every dep check.
- **Compile-time interface assertions** in the test file catch a refactor that breaks the `Check` contract.

### Changed — OTLP write path uses native ClickHouse PrepareBatch
- **`internal/db/db.go`** gains `OpenClickHouseConn(ctx, dsn)` returning the native `clickhouse-go/v2` `driver.Conn`. The existing `OpenClickHouse` (returning `*sql.DB`) stays for the migration runner and any future query path; performance-sensitive writes use the native conn.
- **`internal/resultpipeline/otlp.go`** swaps `BeginTx` + `PrepareContext` + per-row `ExecContext` loop for `conn.PrepareBatch(ctx, sql)` + `batch.Append(...)` + `batch.Send()`. Column-major, one network round-trip per Export call. The `OTLPReceiver.ClickHouse *sql.DB` field is replaced by `OTLPReceiver.CH driver.Conn`.
- **`cmd/result-pipeline/main.go`** opens the native conn instead of `*sql.DB`; the existing metrics + cluster wiring is unchanged. `defer chConn.Close()` replaces `defer chDB.Close()`.
- **Tests** (`internal/resultpipeline/otlp_export_test.go`): 4 new cases covering the Export entry-point's not-configured contract, the `flatten` row-shape (verifies repo_id/run_id resource-attr extraction, ERROR status mapping, hex traceID), failure-cluster routing of `exception.message` / `exception.stacktrace` attrs, and `parseUUIDOrZero` invalid-input handling. The existing helper tests still cover `statusToCode`, `attrsToMap`, `hexBytes`.
- **DSN parsing**: new `parseClickHouseDSN` helper in `internal/db` translates the `clickhouse://user:pass@host:port/db` form into a `*clickhouse.Options` for the native open path; reasonable defaults for dial timeout (5s) and pool sizes match the previous `*sql.DB` config.

### Added — Prometheus collector wiring (FR-1003 completed)
- **`internal/metrics`** package — single source of truth for every `teo_*` metric the bundled dashboards and PrometheusRules reference. Per-process `prometheus.NewRegistry()` (no global state); typed accessors keep registration in one place. Standard process + Go runtime collectors included.
- **API HTTP middleware** (`internal/api/metrics_middleware.go`): records `http_server_requests_seconds` per `(handler, method, status)` using `chi.RouteContext().RoutePattern()` for the handler label so URL params don't blow up cardinality. The histogram's auto-emitted `_count` series doubles as the request-rate counter.
- **Run Manager instrumentation**: `teo_run_transitions_total{to_status}` ticked on every successful state-machine commit; `teo_runs_active{status}` refreshed each reconciliation tick (with stale-status reset to 0); `teo_runs_stuck_total` recomputed on every budget check. `teo_scheduler_plan_seconds` + `_total` wrap the `scheduler.PlanFunc` call.
- **Result pipeline instrumentation**: `teo_clickhouse_inserts_total` (rows), `teo_clickhouse_insert_seconds` (latency histogram), `teo_clickhouse_insert_failures_total` (error counter) recorded around the OTLP write path.
- **Headless services**: new `Registry.ServeHTTP(addr)` helper exposes `/metrics` + `/healthz` on a tiny dedicated listener (`:9100` default; override via `TEO_METRICS_LISTEN`). Wired into `cmd/run-manager` and `cmd/result-pipeline`. The API server already exposes `/metrics` via its chi router.
- **API `Server` constructor** now accepts `Option`s; added `WithMetrics(*Registry)` so callers can share a registry across the API and any auxiliary listeners. Defaults to `metrics.New()` if not provided.
- **Tests** (`internal/metrics/metrics_test.go` + `internal/api/metrics_middleware_test.go`): the registry-completeness test is the single guardrail for "did someone silently rename a metric the dashboards depend on?" — it touches every collector and asserts each HELP line appears in the exposition. Middleware tests verify `RoutePattern`-based labels and 5xx classification.

### Added — E-13 Karpenter + spot-aware scheduling (completed)
- **Worker draining state machine** (`internal/worker/worker.go`):
  - New `SpotInterruptionSource` interface (satisfied by `*spot.Watcher`); injection-friendly so unit tests don't need IMDS.
  - Atomic `draining` flag + `currentShardID` tracking; `IsDraining()` accessor.
  - `watchSpot()` goroutine forwards the first interruption to `beginDrain()`; ignores empty-action signals.
  - `beginDrain()` is idempotent (atomic CompareAndSwap), updates the in-flight shard to `preempted`, bumps `runs.preemption_count` for visibility (FR-308 telemetry).
  - Both the Postgres-claim path and the NATS handler early-return when draining; NATS naks back so a non-draining worker picks the message up (no message loss).
  - Main loop exits cleanly once draining and idle (no in-flight shard).
- **Run Manager reschedule sweep** (`internal/runmanager/manager.go`, new `RescheduleInterval` ticker, default 5s): finds shards in `preempted`/`lost` whose dedupe marker isn't set, computes the unfinished tests by subtracting `test_executions` from the original round-robin assignment, creates a fresh `pending` shard with that residue. Per-run advisory lock keeps it concurrent-safe with state-transition handling.
- **Reshard manifest convention**: residual tests for a re-spawned shard are stored at `runs.meta.reshards[<new_shard_id>]`. The worker's `loadTestsForShard` consults this map first, falling back to round-robin — no schema change to `run_plans` required.
- **Migration 004_shard_meta** adds `shards.meta JSONB NOT NULL DEFAULT '{}'`; the `meta->>'rescheduled_at'` marker dedupes the sweep.
- **`cmd/worker/main.go`** wires `spot.NewWatcher()` by default; opt out with `TEO_SPOT_WATCH=disabled` for dev / non-EC2 environments.
- **Tests**: 6 unit tests in `drain_test.go` (default-false, idempotent, no-SQL when no shard, signal forwarding, empty-action filter, race-free atomic). 3 integration tests in `reschedule_integration_test.go` (only-uncompleted, all-completed no-op, idempotent-second-sweep).

### Added — E-11 Helm chart + observability + release pipeline (completed)
- **Subchart vendoring** (`Chart.yaml dependencies`): CloudNativePG (alias `postgres`, v0.22.1), Altinity ClickHouse Operator (alias `clickhouse`, v0.24.5), NATS (v1.2.8), MinIO (v5.4.0), Dex (v0.19.1). Each gated by a `<dep>.enabled` value and grouped under `tags: [stateful|auth]` so an operator can disable the whole stateful tier in one flag for BYO-managed-services deployments.
- **Per-subchart values overrides** in `values.yaml` matching each upstream chart's value document — including monitoring toggles (`PodMonitor`, `ServiceMonitor`).
- **5 bundled Grafana dashboards** (`templates/grafana-dashboards.yaml`) shipped as ConfigMaps with the standard `grafana_dashboard: "1"` label so the Grafana sidecar discovers them: API latency, scheduler decision, run state machine, ClickHouse insert lag, NATS consumer lag.
- **6 PrometheusRule alerts** (`templates/prometheus-alerts.yaml`) covering NFR-OBS-705: `TeoApiHighLatency`, `TeoApiErrorRateHigh`, `TeoRunStuck`, `TeoClickHouseInsertLag`, `TeoNatsConsumerLag`, `TeoControlPlaneReplicaDown`. Every alert carries a `runbook_url` pointing at the new docs.
- **6 runbook docs** under `docs/operations/runbooks/`: index plus one page per alert with diagnose / fix / verify sections.
- **`docs/operations/restore-drill.md`** — quarterly DR runbook with 8 sections (pre-flight, cluster provisioning, Postgres PITR via CloudNativePG `bootstrap.recovery`, ClickHouse restore, install, smoke, UI sanity, results recording).
- **`.goreleaser.yml`** — multi-OS / multi-arch binary releases for all 7 services (linux/darwin amd64/arm64; teo CLI also windows), CycloneDX SBOMs per archive via syft, cosign keyless signing of checksums, GitHub-style changelog grouping.
- **`.github/workflows/release.yml`** — runs goreleaser on tag push, then publishes the packaged Helm chart (with subchart deps built) to gh-pages via `helm/chart-releaser-action`.
- **`.github/cr.yaml`** for chart-releaser; **`.github/ct.yaml`** for chart-testing.
- **CI `helm` job upgraded** from a no-op to a real `helm dep build` + `helm lint` + a 4-cell on/off matrix `helm template` smoke + `chart-testing lint` against changed charts.

### Added — E-09 Web UI: integration tests (epic completed)
- **`internal/testpg/`** — build-tagged (`integration`) helper that spins up Postgres 16 via `testcontainers-go`, applies the project's migrations, returns a configured `*pgxpool.Pool`. One `Start(t) → (pool, cleanup)` call per test gives full isolation.
- **`internal/api/graphql_integration_test.go`** — 8 resolver tests against a real Postgres with seeded fixtures: `queryRuns` ordering, `queryRunByID` includes repo and duration, missing-id error, `queryShards` ordered by index, `queryFailedTestCount` for runs with and without failures, `queryFlakes`, `queryFailureClusters`, `rerunFailed` happy path (creates child + parent_run_id linkage + run_plans entry), `rerunFailed` refusal when there are no failures.
- **`internal/api/graphql_http_integration_test.go`** — 5 full HTTP roundtrip cases: `runs` query response shape, `run(id)` with shards + failedTestCount + preemptionCount, `flakes` + `failureClusters` combined query, `rerunFailed` mutation, malformed query returns the GraphQL `errors` envelope (no 5xx).
- **Make**: new `make test-integration` target (requires Docker).
- **CI**: `integration-test` job upgraded from a no-op to a real `go test -tags=integration -race -timeout 15m` run, gated behind `unit-test` success; uploads logs on failure.

### Added — E-10 GitHub App + Checks API (completed)
- **`runmanager.RunObserver` interface** (`internal/runmanager/observer.go`) plus `RunObserverFunc` adapter. The Manager invokes registered observers AFTER committing each state transition, with a fresh `RunSnapshot` and the `prev` status. Observer errors are logged but cannot affect the transition (S-10-02 design — third-party API latency must not hold the Postgres advisory lock).
- **`github.CheckObserver`** (`internal/github/check_observer.go`) implements RunObserver:
  - Creates a Check Run (status=in_progress, deep-link to TEO) on the first transition out of `pending`.
  - Updates the Check Run on every in-flight transition with shard counts (done / running / pending / failed).
  - Finalizes on terminal status with conclusion (`success` / `failure` / `cancelled`), final summary line, and a Markdown body listing the **top-3 failure clusters** for the run with stack snippets (S-10-03).
  - Cluster query joins `test_executions` → `failure_clusters`, ranks by occurrences-in-run desc, last_seen desc.
- **Migration `003_run_check_run`** adds `runs.github_check_run_id` and `runs.github_installation_id` with a partial index for the active rows.
- **cmd/run-manager** registers the CheckObserver when `TEO_GITHUB_TOKEN` is set; `TEO_BASE_URL` and `TEO_GITHUB_CHECK_NAME` are configurable; warns and skips otherwise.
- **Tests** (`observer_test.go` + `check_observer_test.go`): RunObserverFunc round-trip, error propagation contract, conclusion-label table, terminal detection, cluster Markdown empty case + 2-cluster case + long-stack truncation + missing-message fallback.

### Added — E-09 Web UI Phase C+D (page migration + rerun flow)
- **All four pages migrated to GraphQL.** `web/src/app/runs/page.tsx`, `clusters/page.tsx`, `flakes/page.tsx`, and `runs/[id]/page.tsx` now call `gqlFetch()` with named operations from `web/src/lib/queries.ts`. Zero `/api/v1/*` REST calls left in `web/src/app/`.
- **`<LiveRunShards />` Client Component** (`web/src/components/LiveRunShards.tsx`): server-rendered initial state, then polls every 2 seconds while `isLive(status)` is true, auto-stops on terminal status. Honors `pollMs` and a `fetcher` injection prop for tests.
- **Two Next API proxy routes**:
  - `GET /api/graphql/run?id=...` — server-side proxy for the polling component, keeps `TEO_API_URL` and the API key off the browser.
  - `POST /api/graphql/rerun` — proxy for the rerun-failed mutation.
- **`<RerunFailedButton />`** (`web/src/components/RerunFailedButton.tsx`): rendered only when status is terminal AND `failedTestCount > 0`. Pluralization-aware label, busy state, inline error on failure, navigates to the new run on success via `next/navigation`.
- **Run-detail page polish**: title row with status badge, structured `<dl>` of repo/branch/commit/duration/preemptions, `RerunFailedButton` next to the Shards heading.
- **Two new Vitest files** (`LiveRunShards.test.tsx`, `RerunFailedButton.test.tsx`): 10 cases covering initial render, fake-timer-driven polling start/stop, terminal-status no-poll, empty state, button visibility, pluralization, success navigation, and failure error display.

### Added — E-09 Web UI (strategy + foundations)
- **Strategy doc** [`docs/backlog/E-09-strategy.md`](docs/backlog/E-09-strategy.md) — phased plan (Phase A backend, B urql, C page swaps, D rerun, E test framework, F resolver tests), 5-day estimate, explicit deferral of WebSocket subscriptions in favour of 2s client polling.
- **GraphQL backend extended** (`internal/api/graphql.go`, `graphql_resolvers.go`):
  - `Query.run(id: ID!)` returns the single run with shards.
  - `Shard` type with `index`, `status`, `workerId`, `predictedDurationMs`, `actualDurationMs`, `testCount`, `startedAt`, `finishedAt`.
  - `Run.shards` lazy-resolves via `queryShards`; `Run.failedTestCount` via `queryFailedTestCount`; `Run.preemptionCount` exposed.
  - `Mutation.rerunFailed(runId: ID!)` creates a child run scoped to the parent's failed/quarantined tests, sets `parent_run_id`, returns the new Run.
  - `clampFirst` extracted and tested for the `runs(first:)` argument.
- **Go GraphQL tests** (`internal/api/graphql_schema_test.go`): schema completeness, Run/Shard field presence, `clampFirst` boundary cases, resolver execution against a stub `map[string]any` source, mutation arg-validation negative test.
- **urql client + queries module** (`web/src/lib/graphql.ts`, `queries.ts`): single source of truth for every operation; `gqlFetch<T>()` helper for Server Components.
- **Reusable components** (`web/src/components/StatusBadge.tsx`, `GanttBar.tsx`) extracted from the page files.
- **Pure helpers** (`web/src/lib/format.ts`): `statusColorClass`, `ganttWidthPct`, `formatDurationSec`, `formatPercent`, `shortSha`, `isLive` — all unit-tested.
- **Vitest + Testing Library + jsdom** wired in `web/`: `vitest.config.ts` with `@/` alias, `src/test-setup.ts` for jest-dom matchers, `npm test` / `test:watch` / `test:coverage` scripts, new `web` CI job (`typecheck` + `test`).
- **Frontend test files** (4): `format.test.ts`, `queries.test.ts`, `StatusBadge.test.tsx`, `GanttBar.test.tsx`.

### Added — E-15 Auto-quarantine (completed)
- **GitHub Issues REST client** (`internal/github/issues.go`): `Create`, `Patch`, `Comment`, `Get` operations against the v2022-11-28 API. Tested with `httptest.Server` — 4 cases including 4xx error propagation.
- **`GitHubOpener`** (`internal/quarantine/github_opener.go`) bridges the `IssueOpener` and `IssueCommenter` interfaces to the Issues client.
- **Dedupe via comment**: when a flake re-quarantines, the daemon comments on the existing issue rather than opening a duplicate (`flake_records.github_issue_number` is the dedupe key, S-15-02 AC4).
- **SLA nudge sweeper** (`internal/quarantine/sla.go`): daily cron that posts a "this has been open N days" comment on every quarantine issue past `flake.sla_days`. Idempotent on `flake_records.last_nudged_at` so consecutive daily runs don't spam (S-15-03).
- **Un-quarantine proposer** (`internal/quarantine/unquarantine.go`): tracks K consecutive passes per quarantined test, posts a Markdown comment with a single-use magic-link token. Token TTL 14 days (configurable).
- **`GET /api/v1/quarantine/restore?token=<…>`** endpoint consumes the magic link, transitions the test back to `active`, marks the token consumed. No auth header required — the token is the auth (per ADR-0014). Returns 410 on consumed/expired.
- **Postgres migration 002_quarantine_tracking**: adds `flake_records.{last_nudged_at, unquarantine_proposed_at, consecutive_passes}` and a `unquarantine_tokens` table.
- **Subcommands**: `result-pipeline quarantine-sla-sweep` and `result-pipeline unquarantine-proposals`.
- **Helm chart**: three quarantine cron jobs (existing `quarantine-sweeper` + new `quarantine-sla-sweeper` and `unquarantine-proposals`); `quarantine.githubTokenSecretName` value plumbs the GH token into all three.

### Added — E-16 Owner digest (completed)
- **SMTP sender** (`internal/digest/sender.go`) with multipart/alternative MIME, optional STARTTLS auth via standard `net/smtp`. Tested with a mock dialer.
- **Slack sender** with incoming-webhook POST (Markdown payload via `mrkdwn` text blocks). Tested with `httptest.Server`.
- **Multiplex sender**: fans out to every configured channel; one channel's failure doesn't block the others.
- **Digest Runner** (`internal/digest/runner.go`): walks enabled repos, builds per-owner stats, enforces per-user opt-out (`users.digest_opt_out`) and per-repo opt-out (`repos.meta.digest_opt_out`), dispatches via the configured Sender. Returns per-recipient `Result` for telemetry and dry-run logging.
- **Subcommand dispatcher** in `cmd/result-pipeline/main.go`: default = OTLP receiver, `owner-digest` / `flake-recompute` / `quarantine-sweep` route to the corresponding job code (CronJob entry points the chart already references).
- **`teo digest dry-run --user=<email> [--format=html|text]`** CLI subcommand prints the rendered digest a user would receive without sending — useful for QA before enabling SMTP/Slack in prod.
- **Helm wiring** (`values.yaml` + `flake-job-cron.yaml`): `ownerDigest.delivery.smtp.{enabled,host,port,from,credentialsSecretName}` and `ownerDigest.delivery.slack.{enabled,webhookSecretName,webhookSecretKey}` propagate as env vars from k8s Secrets per ADR-0015.

### Added — Gap closeout (post E-16)
- **OTLP gRPC ingest receiver** in `internal/resultpipeline/otlp.go`: implements
  `collectorpb.TraceServiceServer`, writes spans into ClickHouse `teo.span_events`,
  routes ERROR-status spans into `teo.failure_clusters`. Hosted by `cmd/result-pipeline`
  on `:4317` with a gRPC health service.
- **NATS JetStream dispatch + worker subscriber** (`internal/nats/`,
  `internal/worker/nats.go`): Run Manager publishes `ShardDispatch` messages on
  `teo.shards.dispatch` during the `dispatching` transition; workers subscribe via a
  durable consumer with `MaxAckPending=1`. Falls back gracefully to Postgres
  SKIP-LOCKED claim when NATS is unavailable.
- **gRPC server in `cmd/api`** with `WorkersService.PullAssignment` and
  `ReportTestFinished` (`internal/grpcsvc/`). Uses a JSON codec
  (`internal/grpcsvc/jsoncodec/`) until `protoc-gen-go` is wired into Make. .proto
  files in `proto/teo/v1/{runs,workers}.proto`.
- **Real ClickHouse query** in the Python predictor trainer
  (`services/predictor-ml/src/teo_predictor_ml/train.py`): replaces synthetic data
  with a per-(repo, test) aggregation over `teo.test_runs`; `_synthetic()` retained
  as a dry-run fallback.
- **GraphQL read API** at `/graphql` (`internal/api/graphql.go` +
  `graphql_resolvers.go`): runs / failureClusters / flakes resolvers backed by the
  same Postgres pool; SDL served at `/graphql/schema`.
- **Lint sweep**: `slices.Contains` for role/scope/transition checks,
  `strings.CutPrefix`/`CutSuffix` in CODEOWNERS matcher, `fmt.Fprintf` over
  `WriteString(fmt.Sprintf)` in quarantine templater, builtin `max`/`min` in
  scheduler bounds, `any` over `interface{}` in JWT parser callback.

### Added — E-12 ML predictor (Python)
- `services/predictor-ml/`: FastAPI app, model registry with S3-backed per-repo
  TTL cache, training script with champion/challenger gating against the heuristic
  baseline (rejects if MAE × 1.5 > heuristic MAE), Dockerfile.

### Added — E-13 Karpenter + spot
- IMDSv2 token + interruption poller in `internal/spot/`.
- Helm chart `karpenter-nodepool.yaml` with `teo-workers-spot` (preferred, weight
  100) and `teo-workers-on-demand` (fallback, weight 50) NodePools, plus a shared
  `EC2NodeClass`.

### Added — E-14 Additional adapters
- `pkg/adapter/gotest`: discovers via `go test -list`, executes `go test -json`,
  parses streaming events into Results. Subtests via `t.Run` are surfaced as
  distinct entries.
- `pkg/adapter/jest`: discovers via `jest --listTests --json`, executes with
  `--json --outputFile`, walks `assertionResults` per test file.

### Added — E-15 Auto-quarantine
- `internal/quarantine/`: daemon transitions Wilson-confirmed flakes to
  `tests.status='quarantined'`; constructs CODEOWNERS-resolved Issue body. The
  GitHub Issues API client is the named next step (interface exists).
- `internal/codeowners/`: parser + matcher with later-rule-wins semantics;
  supports `*`, `*.ext`, `/dir/`, `/path/**` patterns.

### Added — E-16 Owner digest
- `internal/digest/`: per-author aggregation (owned tests, flake count, CI minutes,
  WoW delta, slowest tests), HTML + plain-text templates.

### Added — E-11 Helm chart
- Umbrella chart at `deploy/helm/teo/`: API + Run Manager Deployments,
  Karpenter NodePools, migration pre-upgrade Job, Cron Jobs (flake-job,
  quarantine-sweeper, owner-digest, predictor-train), NetworkPolicy, NOTES.txt.

### Added — E-10 GitHub App
- `internal/github/`: webhook receiver with HMAC SHA-256 verification,
  `installation` event handler upserting `github_installations` + repos table.
  Check Runs REST client (create/update). Push-event → Check Run wiring is the
  named next step.

### Added — E-09 Web UI scaffold
- `web/` Next.js 15 app router: `/runs` list, `/runs/[id]` Gantt timeline,
  `/flakes`, `/clusters`. Distroless multi-stage Dockerfile.

### Added — E-08 Wilson flake detection
- `internal/flake/`: `WilsonInterval` formula, `Decision` (flaky/broken/insufficient/stable),
  nightly job that reads ClickHouse and writes `teo.flake_records`.

### Added — E-07 Result pipeline
- `internal/resultpipeline/cluster.go`: stack-trace fingerprinting (Python-aware
  normalizer + generic fallback that strips hex addresses and numerics), upsert
  path into `teo.failure_clusters`.

### Added — E-06 Worker + pytest + redactor
- `internal/worker/`: claim shard via SKIP-LOCKED, run adapter, report results.
- `pkg/adapter/pytest/`: `--collect-only` discovery, `--json-report` execution.
- `internal/redact/`: AWS keys, JWTs, GitHub PATs, generic high-entropy patterns;
  `[REDACTED:<rule_id>]` replacement.

### Added — E-05 Scheduler + heuristic predictor
- `internal/scheduler/`: pure-function `PlanFunc` with LPT bin-packing,
  exclusivity-tag handling, quarantine lane, deterministic tie-break by stable hash.
  Property test verifies makespan ≤ 4/3 × OPT against brute-force enumeration on 30
  random instances.
- `internal/predictor/`: rolling-mean per (repo, file) with cold-start defaults
  and runner-specific p50 fallbacks (pytest=1.2s, go=0.5s, jest=1.5s).

### Added — E-04 Run Manager
- `internal/runmanager/`: 8-state machine with explicit transition table,
  reconciliation loop, per-run `pg_try_advisory_xact_lock` leader election
  (ADR-0013), budget-exceeded auto-fail.

### Added — E-03 API gateway
- `internal/api/`: chi router, `POST/GET /api/v1/runs/...` with idempotency-key
  middleware, JSON-schema-equivalent validation, RFC 7807 error responses.
- `internal/auth/`: JWT (HS256) issuer, argon2id-hashed API keys with prefix
  format, 30-second revocation cache.
- `internal/audit/`: append-only audit logger with INSERT-only DB role contract.

### Added — E-02 Migrations
- `migrations/postgres/001_initial.up.sql` + down: full schema from
  `docs/architecture/data-model.md` §1, 13 tables, advisory triggers.
- `migrations/clickhouse/001_initial.up.sql` + down: `test_runs`, `span_events`,
  `flake_observations`, `mv_run_summary` materialized view.
- `internal/migrate/`: forward-only runner with backend-specific
  `schema_migrations` table; supports both pgx and clickhouse-go drivers.
- `cmd/teo migrate up|status` subcommands.

### Added — E-01 Foundation
- Go 1.23 monorepo, service stubs (`teo`, `api`, `run-manager`, `scheduler`,
  `result-pipeline`, `predictor`, `worker`), `internal/version` package with
  build-identity injection.
- `Makefile` with `build`, `test`, `lint`, `fmt`, `licenses`, `docker-build`,
  `helm-lint` targets.
- `.golangci.yml` with `errcheck`, `gosec`, `revive`, `staticcheck`, `unused`,
  plus supporting linters.
- Multi-stage `Dockerfile` building any service from `cmd/<svc>` onto distroless.
- GitHub Actions CI (`ci.yml`): lint, unit tests, integration test scaffolding
  (testcontainers), multi-arch image build, Helm lint, Trivy + Syft, `go-licenses`.
- Project hygiene: `LICENSE` (Apache 2.0), `NOTICE`, `README.md`, `CONTRIBUTING.md`,
  `CODE_OF_CONDUCT.md`, `SECURITY.md`, this `CHANGELOG.md`, `progress.md`.
- `docs/`: architecture (overview, tech-stack, data-model, api-design, deployment),
  requirements (functional + non-functional), 20 ADRs, backlog (epics, stories,
  tasks), process (definition-of-done).

### Notes
- Module path is `github.com/teo-dev/teo` (placeholder).
- Build status: 14 test packages green; 110 source files.
- See [`progress.md`](progress.md) for per-epic and per-FR implementation status.
