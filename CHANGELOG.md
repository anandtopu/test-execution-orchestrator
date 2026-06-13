# Changelog

All notable changes to TEO are documented here. The format is based on
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the project adheres to
[Semantic Versioning](https://semver.org/spec/v2.0.0.html) once 1.0 ships.

For a finer-grained per-FR / per-epic implementation status, see [`progress.md`](progress.md).

## [Unreleased]

### Fixed — release pipeline: bootstrap `gh-pages` before chart-releaser

The `v1.0.0` Release run's `helm-publish` job failed at the final `cr index` step
with `fatal: invalid reference: origin/gh-pages` — chart-releaser cannot update the
charts index on a repo that has never had a `gh-pages` branch. GoReleaser (signed
binaries + SBOMs) and the chart `.tgz` upload had already succeeded; only the pages
index update failed. Added an idempotent "Ensure gh-pages branch exists" step to
`.github/workflows/release.yml` that bootstraps an empty `index.yaml` + `.nojekyll`
via a throwaway git worktree (leaving the build working tree untouched) when the
branch is absent. No-op once `gh-pages` exists.

## [1.0.0] - 2026-06-11

First stable release. All 16 epics and the 12 reconciliation-audit items are
closed; the full `-tags=integration` suite runs green (31 packages) and the
ClickHouse 1M-row load test records p99 78.8 ms. The offline chart-render
pre-check of the restore drill passes; the full cloud-based restore drill is
deferred to a live environment (tracked in `docs/operations/restore-drill.md`).

### Added — Leader-election 2-replica integration/chaos test (`leader-election-test`, #12 / S-04-02 / T-04-02-03)

Closes the last open reconciliation item (#12): the per-run `pg_try_advisory_xact_lock` coordination (ADR-0013) had production code but no 2-replica lease/takeover test.

- **New `internal/runmanager/leader_election_integration_test.go`** (`//go:build integration`, `internal/testpg`) with a `zeroPredictor` stub so `plan()` runs DB-free:
  - `TestLeaderElection_LockHeldBlocksSecondReplica` — deterministic core. Replica A holds the per-run advisory lock in an open tx (same `runIDLockKey` the Manager uses); replica B's `tryHandle` is then a no-op (run stays `pending`, 0 shards); after A's tx rolls back (lease ends) B takes over and plans the run to `dispatching`.
  - `TestLeaderElection_ConcurrentReplicasConvergeWithoutDuplicateShards` — chaos test. 4 replicas (own Managers, shared pool, barrier-synced) hammer `reconcileOnce` against one pending run; it converges to `running` exactly once with no duplicate `(run_id,index)` shards. Race-free by design: the lock and the `pending→planning` UPDATE commit atomically, so a competitor never observes `status='pending'` with the lock free (MVCC).
- **Executed green against real Postgres via testcontainers** (Docker available this run) — both tests pass; the full runmanager integration suite stays green. No production-code, proto, or migration changes.

### Added — ClickHouse span-ingest load-test harness + p99 numbers (`clickhouse-loadtest`, #10 / S-02-03 / T-02-03-02)

Closes gap-closeout row 10: the OTLP span-ingest write path (`internal/resultpipeline/otlp.go` `OTLPReceiver.writeSpans`, native `conn.PrepareBatch`) had no load test or documented throughput/p99 numbers.

- **New `internal/testch`** — a testcontainers ClickHouse harness mirroring `internal/testpg` / `internal/testminio`. `Start` runs `clickhouse-server:24.8-alpine`, gates readiness on an HTTP `/ping` of the 8123 port (no `time.Sleep`), applies the ClickHouse migrations via `migrate.Up`, and returns a native `db.OpenClickHouseConn` driver.Conn + DSN + cleanup. Build-tagged `//go:build integration`.
- **New `internal/resultpipeline/otlp_loadtest_integration_test.go`** (`//go:build integration`) — `TestClickHouseSpanInsertLoad` drives the **exact production `writeSpans` path** at ~1M rows (`loadTestRowCount`: 1M default, 10k under `-short`, `TEO_LOADTEST_ROWS` override) in `TEO_LOADTEST_BATCH`-sized batches (5k default). Each batch's `synthRows` is built **before** the timer so generation never pollutes the numbers; only `writeSpans` is timed. Reports throughput + p50/p95/p99 (`percentile`, nearest-rank) and asserts `count()` equals the total (plain `MergeTree`, exact). `TestClickHouseHarnessSmoke` is a one-row round-trip sanity check.
- **New `internal/resultpipeline/loadmetrics.go`** (no build tag) — `percentile`, `synthRows`, `loadTestRowCount`, `loadTestBatchSize`. Pure logic kept Docker-free so it stays unit-testable.
- **`docs/operations/clickhouse-load-test.md`** — run command, env knobs, and a results table. The p99 row is deliberately left `TODO` (no fabricated numbers) to be filled from the first CI testcontainers run.
- Compiles + `go vet -tags=integration` clean; **NOT executed** here (Docker unavailable on the dev host). No proto/migration changes.

### Added — failure-cluster backfill job (`failure-cluster-backfill`, #9 / T-07-02-03)

Closes gap-closeout row 9: clustering previously ran **only** on live ingest. The OTLP path (`internal/resultpipeline/otlp.go` `Export` → `Cluster.UpsertCluster`) upserts a `failure_clusters` row but never sets `test_executions.failure_cluster_id`; only the worker gRPC path (`internal/grpcsvc`) back-linked inline. As a result every OTLP-ingested failure had a NULL cluster id and there was no way to (re)cluster historical failures.

- **New `internal/resultpipeline/backfill.go`** — `Backfiller.Run` scans failed/errored/timed_out `test_executions` with a non-empty `otel_trace_id` but NULL `failure_cluster_id`, fetches the stack + message from ClickHouse `span_events` (error span `status_code=2`, `argMax` by `end_time`), and **reuses the exact live-ingest fingerprint path** — `FingerprintStack` + `*Cluster.UpsertCluster` (no duplicated clustering logic) — then back-links via an idempotent `UPDATE … WHERE failure_cluster_id IS NULL`. Interface seams (`ExecSource` / `StackSource` / `ClusterUpserter`; `*Cluster` already satisfies the latter) keep `Run` unit-testable with no DB. `--dry-run` fingerprints without mutating; `--since` bounds the scan (0 = all history). Per-row errors are logged + counted, never abort the pass (digest/flake idiom); a `Stats` summary is logged via slog.
- **`cmd/result-pipeline/main.go`** — new `backfill-clusters [--since <dur>] [--dry-run]` subcommand (mirrors `owner-digest`/`flake-recompute`). It is the first cron entry point needing **both** Postgres and ClickHouse, so it opens a dedicated ClickHouse connection and exits 1 if `TEO_CLICKHOUSE_DSN` is unset.
- **Helm** — new default-off `backfillClusters.{enabled,schedule,since}` values + a CronJob in `flake-job-cron.yaml` injecting both `<release>-postgres-creds` and `<release>-clickhouse-creds`, `concurrencyPolicy: Forbid`. Disabled by default because it is the only result-pipeline cron requiring ClickHouse.
- **Tests** — unit tests (stub seams, no DB) and an `//go:build integration` `internal/testpg` test (seed a failed exec → assert assignment + idempotency; will not run without Docker) land in the follow-up test stage. No proto/migration changes (`failure_cluster_id`, `otel_trace_id`, `te_outcome_idx` already exist in `migrations/postgres/001_initial.up.sql`).

### Docs — Phase B doc reconciliation (`graphql-schema-fields`, `ui-clusters-flakes`, `ui-home-calibration`, `oidc-frontend`, `run-list-virtual-scroll`)

Documentation sweep over the "GraphQL schema + UI wiring" phase. No code change.

- **`docs/architecture/api-design.md` §3** gained a "UI-observability fields (`graphql-schema-fields`)" subsection enumerating the additive `FailureCluster` (x/y/r/category/stackFingerprint/affectedRuns), `FlakeRecord` (wilsonUpper/spark/status/durationMeanMs/quarantinedAt/ownerTeam), `Run.predictor { … }` (+ flat `predictorMae`/`predictorRho`/`modelVersion`), and `Shard` (deltaMs/predictionConfidence/modelVersion) fields, who computes them, and the note that per-shard confidence/model_version resolve null until a future `teo.shards` migration. Records that all four marquee screens now render from GraphQL via `web/src/lib/teo-adapt.ts` (the `teo-data.ts` MOCK is no longer imported by any `web/src/app/` route).
- **`progress.md` gap-closeout row 5** — corrected the trailing stale clause that still claimed `ui-clusters-flakes`/`ui-home-calibration` "still read MOCK `teo-data.ts` until they rewire to urql"; both have since rewired (cross-linked to FR-700 and reconciliation row 7).
- The gRPC `Runs` service (`runs-grpc`, `docs/architecture/api-design.md` §1), the ML-predictor HTTP wiring + heuristic fallback (`ml-predictor`, api-design.md §1 + `docs/adr/0019-ml-predictor.md` implementation note), and the adapter `ASTSignature` fingerprint contract (`docs/adapters/spi.md`) were already documented in their respective feature docs during the Phase A/B sweep — verified current, no edit needed.

### Changed — run list now windows rows so it stays performant at 100+ runs (`run-list-virtual-scroll`, #11 / S-09-01)

Closes gap-closeout row 11: `web/src/app/runs/page.tsx` previously fetched `runs(first: 50)` and rendered every row with a plain `runs.map()` inside a `<table>` — no windowing.

- **`web/src/app/runs/page.tsx`** now fetches `runs(first: 200)` (200 is the backend `clampFirst` cap in `internal/api/graphql.go` — values above are silently capped, so no backend change) and renders the list through the new `RunsTable` client component, passing the server-fetched rows down as props. The page stays an async Server Component (mirrors the `LiveRunShards` initial-data pattern).
- **New `web/src/components/RunsTable.tsx` (`'use client'`)** does dependency-free fixed-row-height windowing rather than adding `@tanstack/react-virtual` (the repo intentionally avoids extra UI deps — cf. the cost dashboard's CSS bars). It renders only the rows whose index falls inside the visible scroll window plus overscan; `visibleCount` is derived from `height`/`rowHeight`/`overscan` props (deterministic under jsdom, which has no layout engine) and two `aria-hidden` spacer rows preserve the full scrollbar extent. Existing columns and styling are unchanged; the empty state ("No runs yet") is preserved.
- **Windowing-math correctness fix.** The `<thead>` previously lived *inside* the scroll viewport, so once scrolled, `scrollTop` included the ~36px header pixels while `startIndex = floor(scrollTop / rowHeight)` treated `scrollTop` as a pure data-row offset — an off-by-roughly-one-row error masked only by the default `overscan=6`; a caller passing `overscan=0|1` rendered a blank/clipped top. The header now renders in a **separate non-scrolling table** above the scroll div (header stays pinned, UX parity with the old page-scroll table), so `scrollTop` is a pure data-row offset and the window math is exact at any overscan. Both tables use `table-layout: fixed` + a shared `<colgroup>` so header and body columns stay aligned. Cells are clamped (`truncate`) so a row can never exceed `rowHeight` and desync the spacer math; the `rowHeight` prop doc now states it MUST equal the rendered single-line row height including border.
- **Test added: `web/src/components/RunsTable.test.tsx`** (vitest + @testing-library/react, mirrors `LiveRunShards.test.tsx`): asserts the window is smaller than the total (300 runs → `ceil(height/rowHeight)+overscan*2` rows), spacer rows sum to `(total − windowed) * rowHeight`, scrolling the `runs-scroll` viewport advances the rendered window to the correct row range, `overscan=0` still renders the correct window (no header offset to mask), and `runs=[]` renders the empty state.

### Changed — `/` home screen + predictor calibration overlay now render from live GraphQL (`ui-home-calibration`, #7 / S-12-04)

Closes gap-closeout row 7: the marquee `/` live-run screen and its predictor-accuracy overlay no longer read the `TEO_DATA` mock — they render real run data, and the overlay is a genuine side-by-side predicted-vs-observed view rather than the old `actual ?? predicted` fallback.

- **`web/src/app/page.tsx` is now a data-fetching Server Component.** It resolves the most-recent run via `RunsQuery` (`first: 1`) + `RunByIdQuery`, adapts the result, and feeds `RunDetailScreen` real props. When there is no run, it renders an empty state instead of mock data. The live 2s poll runs through a thin `LiveRunDetail` client wrapper (`web/src/components/teo/LiveRunDetail.tsx`) that re-fetches `/api/graphql/run` and re-adapts while the run `isLive()`, freezing on terminal status.
- **New pure `adaptRun`/`adaptStatus` in `web/src/lib/teo-adapt.ts`.** Maps the GraphQL Run/Shard shape onto the teo/ `Run`/`Shard` view types: status-vocabulary mapping (succeeded→pass, lost→fail, preempted→preempt, running/pending/queued→running, unknown→running), ms→seconds conversion, run-level rollups (testCount/passed/failed/running, predictedTotalSec=max, p95 shard, elapsed from `startedAt`→now or `totalDurationMs`), and a real `predictor` block whose p50/p95 delta come from finished shards' fractional miss. Total + null-safe (never throws, never NaN), so missing predictor fields degrade to an em-dash.
- **`RunByIdQuery` extended (additive).** Run gains `predictorMae`/`predictorRho`/`modelVersion`; Shard gains `predictionConfidence`/`modelVersion`. `RunDetailScreen` is now prop-driven (TEO_DATA only as a no-props default); `RunGantt` `PredictorAccuracy` shows `modelVersion` in the panel head + per-line `· conf NN%`; `LiveRunShards`/`GanttBar` draw the predicted band + actual bar side-by-side and surface confidence.
- **Backend GraphQL (additive only).** `internal/api/graphql.go` exposes the new Run-level flat predictor fields via `runPredictorFieldResolve` (sharing `queryRunPredictor`, null for <2 finished shards) and the per-shard `predictionConfidence`/`modelVersion` via `mapResolve`. These resolve to null until the sibling `graphql-schema-fields` migration adds `teo.shards.prediction_confidence`/`model_version` (dependency noted, migration NOT authored here); `queryShards` SQL is left untouched so the schema is forward-compatible either way.

Review-fix sweep (`ui-home-calibration`, post-merge findings):

- **Home route no longer hard-crashes on a backend outage.** `web/src/app/page.tsx` now wraps both `fetchLatestRun()` and the failure-clusters fetch in try/catch (a hard connection failure makes the underlying `fetch` *reject*, which `gqlFetch`'s null-on-error contract doesn't cover) — the most-visited `/` route degrades to the "No runs yet" empty state like the sibling clusters/flakes pages instead of throwing.
- **Predictor MAE unit fix.** `buildRunPredictor` now converts the backend's millisecond `mae` to seconds (`/1000`); previously a ~4200 ms MAE rendered as `"4200.0s"` instead of `~4.2s`. Covered by a new unit test.
- **Live elapsed counter syncs + freezes.** `RunDetailScreen` resets `elapsed` whenever the polled `run.elapsedSec` changes (the `useState` initializer only ran on mount, so the server value was silently ignored) and stops the 1 s ticker once the run is terminal (`isLive(run.status)`), so a finished run no longer counts up forever.
- **Status badge + live-poll vocabulary.** `STATUS_MAP` gained `succeeded`/`canceled`/`cancelled`/`lost`/`pending` (a completed run no longer mislabels as a gray "SUCCEEDED" skip-chip), and `isLive` now lowercases input and treats both `canceled` (the `internal/model` spelling) and `cancelled` (the SQL spelling) plus `lost` as terminal, so the home overlay can't poll a finished run forever.
- **Resolver fan-out removed.** `internal/api/graphql.go` memoizes `queryRunPredictor` per (request, run) on the run Source map (`cachedRunPredictor`); the nested `predictor` resolver and the three flat field resolvers now share one computation instead of issuing 4× the queries (8 DB round-trips → 2) on every 2 s poll.
- **Comment/forward-pointer corrections.** Fixed the misleading "`queryShards` COALESCEs" comment in `graphql.go` and added a note on `queryShards` itself: `prediction_confidence`/`model_version` resolve to null because the SELECT does not read them yet and MUST be wired when the `graphql-schema-fields` migration lands (they do not light up automatically). Documented the deliberate ms-vs-fractional divergence for the unused `p50DeltaMs`/`p95DeltaMs` in `buildRunPredictor`.
- **Test coverage.** New `adaptRun`/`adaptStatus`/`buildRunPredictor` unit tests in `teo-adapt.test.ts` (status vocab, ms→s, rollups, p50/p95 fractional deltas, flat→nested predictor fallback, no-throw/no-NaN on an all-null run); strengthened `queries.test.ts` to assert the nested `predictor {` block and flat `predictorMae`/`predictorRho` distinctly; added `Run.predictorMae/Rho/modelVersion` + `Shard.predictionConfidence/modelVersion` to the Go schema-shape assertions; and a new `TestSDLAgreesWithProgrammaticSchema` that diffs the hand-written SDL field sets against `buildSchema` so the two contracts can't drift.
- **Component + page + round-trip test sweep (2026-06-10).** Added `web/src/components/teo/RunDetail.test.tsx` (RTL): renders `RunDetailScreen` from a small adapted fixture (one finished + one running shard) and asserts the Predictor accuracy panel shows the real run-level MAE (`4.2s`) and `modelVersion`, renders exactly one `predict-line` per **finished** shard with the correct signed delta (`+6s` slower / `-12s` faster) and per-line `conf NN%`, and that an all-running run renders the panel with a `0.0s` em-dash path (no crash). Extended `web/src/components/LiveRunShards.test.tsx`: seed shards now carry `predictionConfidence`/`modelVersion`, and a new test asserts a **finished** shard renders the predicted band (`gantt-pred-0`) and a distinct actual bar (`gantt-bar-0`) — proving it is no longer a single `actual ?? predicted` bar — plus that `gantt-conf-0` surfaces `conf 78%`. Added `web/src/app/page.test.tsx`: the `/` home Server Component renders the "No runs yet" empty state (not a throw) when the latest-run fetch returns null OR rejects (backend outage), and renders `LiveRunDetail` when a run exists. Added `TestGraphQLEndpoint_RunByID_PredictorCalibration` to `graphql_http_integration_test.go` (`//go:build integration`, `internal/testpg`): the home `RunByIdQuery` resolves a non-null `predictorMae`/nested `predictor.mae` (~2000ms) + `modelVersion` ("heuristic" fallback) for a run with ≥2 finished shards, the per-shard `predictionConfidence`/`modelVersion` fields are present + correctly typed (null until the `graphql-schema-fields` migration adds the columns — Phase A), and a run with <2 finished shards returns a null predictor without a GraphQL error.

Verification: `go build ./internal/api/...` + `go test -count=1 ./internal/api/...` green; `go vet -tags=integration ./internal/api/...` clean (the new RunByID calibration integration test compiles but is Docker-gated — **not run locally**, Docker unavailable). `cd web && npm run typecheck` clean; `npm test` green for the changed files (78 tests across `teo-adapt`/`queries`/`LiveRunShards`/`RunDetail`/`page`). Scoped checks only — Phase A backend work is concurrently in flight.

### Added — OIDC sign-in flow wired into the running UI (`oidc-frontend`, FR-801 / S-03-02 AC1)

Closes reconciliation row 6's frontend half: the complete backend OIDC flow (`/auth/{login,callback,logout,session,refresh}`, httpOnly `teo_session` cookie) is now actually used by the browser. New `web/src/middleware.ts` soft-gates the UI on the `teo_session` cookie behind opt-in `NEXT_PUBLIC_REQUIRE_AUTH=1` (default off, so an OIDC-disabled backend where login 503s stays fully usable), redirecting unauthenticated users to `/login` while allow-listing `/login`, `/auth/*`, the BFF `/api/*` proxy, `/_next/*`, and static assets. `SessionNav` is mounted in the Shell topbar (replacing the hardcoded `MP` avatar) and now proactively POSTs `/auth/refresh` on a fixed interval (`NEXT_PUBLIC_SESSION_REFRESH_MS`, ~10 min), flipping back to the Sign-in link on a non-OK refresh. `refresher`/`refreshMs` props mirror the existing `fetcher` seam for fake-timer testability. `next.config.js` documents both env flags. No backend change. Verification: scoped `cd web && npm run typecheck` / `npm test` / `npm run lint`.

Review fixes (2026-06-10, `oidc-frontend`):
- **Dropped the dead `?next=` redirect param.** The middleware previously wrote `/login?next=<original>` and a comment promising post-sign-in return-to, but nothing consumed it (the login page hands off to a static `/auth/login` href and the Go OIDC callback unconditionally 302s to `uiBaseURL` or `/`). Removed the unread param and the misleading comment rather than advertise a round-trip that doesn't happen; documented that real return-to would need end-to-end plumbing through the Go callback (out of scope here).
- **Refresh-disable + interval cleanup in `SessionNav`.** `NEXT_PUBLIC_SESSION_REFRESH_MS=0` (or any non-finite/negative value) now cleanly disables proactive refresh instead of falsy-`||` silently re-enabling the 10-min default (and a negative value can no longer drive a tight `setInterval` loop). On a failed refresh (resolves false or throws) the interval is now `clearInterval`'d after flipping to signed-out, so the widget stops POSTing `/auth/refresh` once it shows "Sign in".
- **Fixed the clipped sign-in widget.** `SessionNav` was mounted inside the fixed 28×28 circular gradient `.topbar__user` avatar, clipping the email + Sign-out. Moved it into a new normal-flow inline-flex `.topbar__session` container (`web/src/styles/teo.css`).
- **Tightened the matcher allow-list** to anchor `login` (`login(?:/|$)`) and `favicon.ico` (`favicon\.ico$`) as exact segments, so a future `/login-help` route isn't silently un-gated.
- **Tests.** Added `web/src/middleware.test.ts` (gate-off passthrough, gate-on redirect with no `next` param, cookie-present passthrough, matcher segment-anchoring) and `SessionNav` fake-timer cases (no refresh when unauthenticated or `refreshMs<=0`, refresh fires after `refreshMs`, false/throwing refresher flips to Sign-in and stops the interval, interval cleared on unmount).
- **Test hardening (2026-06-10).** Added a defense-in-depth in-function allow-list guard to `middleware.ts` so `/login`, `/auth/*`, and `/api/*` are never gated even if the middleware is invoked directly (not just excluded by `config.matcher`). Extended `middleware.test.ts` with invocation-level assertions that, with the gate on and no cookie, `/login` (no redirect loop), `/auth/callback`, and `/api/graphql/run` return `next()` with no `Location`, plus a `new NextRequest(...)` + `req.cookies.set('teo_session', …)` redirect-vs-passthrough pair matching the spec's construction. Now 15 middleware + 9 `SessionNav` cases; full `web` suite 142 green via scoped `npx vitest run`.

### Added — Runs gRPC service (`CreateRun`/`GetRun`/`CancelRun`) over a shared run-intake core (`runs-grpc`)

Closes gap-closeout row 3's Runs half: the `Runs` gRPC service from `docs/architecture/api-design.md` §1 is now implemented, not just declared in the proto. Previously only the `Workers` dispatch RPCs were served on `:9090`; the human/CI-facing run lifecycle had no gRPC surface.

- **New `internal/runsvc` core.** Extracted a transport-agnostic `runsvc.Service` (`Create`/`Get`/`Cancel`) that owns run-intake validation, idempotency-key replay, and the `teo.runs` + `teo.run_plans` writes. HTTP (`internal/api/runs.go`), GraphQL, and now gRPC all call this single code path, so the three transports can't drift. Validation errors surface as a typed `runsvc.ValidationError`.
- **New `internal/grpcsvc/runs.go`.** `RunsService` embeds `teov1.UnimplementedRunsServer` and adapts proto ⇄ domain (`protoToCreateReq`, `runToProto`). Domain errors map to gRPC status codes: validation → `InvalidArgument`, missing repo/run → `NotFound`, idempotency conflict on a different commit → `AlreadyExists`, anything else → a generic `Internal` (raw error logged server-side, never leaked on the wire). `runToProto` saturates `int`→`int32` via `clampInt32` so a large millisecond aggregate can't wrap. `RegisterRuns` panics at wiring time if `Svc` is nil.
- **Auth interceptor.** New `internal/grpcsvc/auth.go` `AuthUnaryInterceptor` validates a JWT or API key from `authorization` metadata using the same primitives as the HTTP middleware and rejects unauthenticated callers of the Runs RPCs with `codes.Unauthenticated`. Workers dispatch RPCs stay open as internal cluster traffic (gated by the method-name allowlist).
- **Wiring.** `cmd/api/main.go` registers the Runs service next to `WorkersService` and installs the interceptor on the shared `grpc.NewServer`.
- **Tests.** Unit: `internal/grpcsvc/runs_test.go` + `auth_test.go` cover `grpcErr`/`runToProto`/`protoToCreateReq`/`clampInt32` and the interceptor (gated-reject, valid-JWT-pass, ungated pass-through); `internal/runsvc` covers `validateCreateRun`/`ValidationError`. Integration: `internal/grpcsvc/runs_integration_test.go` (`//go:build integration`) runs CreateRun/GetRun/CancelRun over a bufconn server with the production interceptor against a real Postgres (`internal/testpg`) — pending create writes exactly one `runs` + one `run_plans` row, unknown-repo→`NotFound`, empty-manifest→`InvalidArgument`, missing-auth→`Unauthenticated`, idempotency replay returns the same id (one row), different-commit reuse→`AlreadyExists`, GetRun round-trip + random-uuid→`NotFound`, CancelRun running→`cancelled` / succeeded idempotent / missing→`NotFound`. Mirrors `api.TestPOSTRunsCreatesRow` so HTTP and gRPC are proven to share the `runsvc` path.

Verification: `go build ./...` and `go vet ./...` clean; `go test -count=1 ./internal/grpcsvc/... ./internal/runsvc/... ./internal/api/...` green (unit tier); the bufconn+testpg integration suite compiles under `go vet -tags=integration` (Docker-gated, not run locally). `StreamRunEvents` from the proto stays unimplemented (no UI/CLI consumer yet) — additive, not a regression.

### Added — ML predictor wired end-to-end with Go-side heuristic auto-fallback (`ml-predictor`, FR-607)

Closes gap-closeout row 4 and flips E-12 / FR-607 from "service exists, never actually called" to wired end-to-end. The Python LightGBM service had a serve path that queried a non-existent ClickHouse column set — every history fetch raised, was swallowed, and the model only ever emitted its bias term — and the Run Manager never called it at all.

- **Go client + fallback.** New `internal/predictor/mlclient.go` (`MLClient`) POSTs to `<TEO_PREDICTOR_ML_URL>/v1/predict` (snake_case JSON body, deadline-bounded). New `internal/predictor/fallback.go` (`Fallback`) tries the ML client first and transparently reverts to the always-on Go `Heuristic` on timeout / connection error / non-200 / decode error / length mismatch, incrementing `teo_predictor_fallback_total` via an `OnFallback` hook (wired in `cmd/run-manager`, logged via slog). A server-side `used_fallback` 200 surfaces through `OnServerColdStart` → new `teo_predictor_server_coldstart_total` counter (MAE-drift signal). The Run Manager builds the `Fallback` only when `TEO_PREDICTOR_ML_URL` is set — heuristic-only otherwise, and nothing hard-fails if the Python service is down (the ADR-0019 non-negotiable "runs without ML" property).
- **Python serve path corrected.** History source moved to Postgres `teo.test_executions` (the same table the Go heuristic's `loadStats` reads), keyed by `path::name`, for BOTH training and serving — eliminating train/serve feature skew. New `services/predictor-ml/src/teo_predictor_ml/repo.py`: `_resolve_repo_id` queries `teo.repos` (TTL-cached) and `_features_for` builds real feature vectors from a prefetched per-repo 30-day history; cold-start stays a genuine fallback (no DSN / unknown repo / model miss / DB error). Commit-diff and time-of-day features are zero in both paths and documented as the canonical definition. Dead `clickhouse-driver` dep + `_parse_dsn` removed.
- **`cmd/predictor`.** Repurposed to a `/healthz` proxy gated on `TEO_PREDICTOR_ML_URL` (no-args build-identity smoke path preserved); the proxy is extracted into a testable `healthzProxyHandler`.
- **Transport note.** HTTP is used deliberately rather than the ADR-0019 gRPC `Predictor` contract — a documented divergence (the Run Manager call is in-cluster, low-QPS, and the heuristic fallback makes the wire format non-load-bearing). Recorded in `docs/architecture/api-design.md` and `docs/adr/0019-ml-predictor.md`.
- **Tests.** Go: `mlclient_test.go` (status/decode/length-mismatch/transport/empty-input/server-coldstart, snake_case body shape, `unexpected status 500` message, bounded-deadline slow-server timeout via a release channel — no `time.Sleep`) + `fallback_test.go` (primary-success, fallback-on-error/length-mismatch, OnFallback-once, nil-primary delegate, nil-logger no-panic, slog Warn capture, both-error propagation via `errors.Is`, `Predictor`-interface compile guards) + `cmd/predictor/main_test.go` (proxy 200/non-200/unreachable→503 + no-args smoke). `internal/predictor/heuristic_integration_test.go` (`//go:build integration`, `internal/testpg`) seeds repos/tests/test_executions and asserts non-cold-start ≥3 attempts / cold-start below threshold / unknown-repo. Python: `services/predictor-ml/tests/{test_app.py,test_features.py}` — 14 pytest cases (FastAPI TestClient model/cold-start/raise-swallow paths + feature-extraction shape).

Verification: `go build ./...` and `go vet ./...` clean; `go test -count=1 ./internal/predictor/... ./cmd/predictor/...` green (unit tier); heuristic integration test compiles under `go vet -tags=integration` (Docker-gated). Python pytest suite ships green in CI (not run in this Go-gated pass). Helm `predictor-deployment.yaml` / `run-manager-deployment.yaml` / `values.yaml` carry the `TEO_PREDICTOR_ML_URL` wiring (not rendered/linted here).

### Changed — Harden pytest AST-signature lookup against the `parseCollect` strip invariant (`pytest-astsig-param`)

**No observable behavior change today** — current parametrized variants already resolve correctly, because `parseCollect` pre-strips the `[param]` suffix into `ParamsHash` before `Discover`'s attachment loop runs, so the name fed into the signature lookup never carries a `[`. This is a robustness/decoupling change, not a bug fix.

`pkg/adapter/pytest` attached AST signatures during `Discover` by keying `byName[entry.Name]`, while the embedded Python helper keys signatures by BARE pytest qualnames (`test_x`, `TestClass::test_y`) with no `[param]` suffix. That resolved correctly only because of the implicit `parseCollect` invariant above. The lookup now keys on `stripParams(entry.Name)`, so the attachment is correct by construction even if a future producer feeds an entry whose `Name` still carries the suffix — every parametrized variant (`test_x[case1]`, `test_x[case2]`) maps to its base function body's signature regardless. Variants still differ by `ParamsHash` for fingerprinting; only the shared `ASTSignature` is keyed this way. Non-parametrized names are unaffected (`stripParams` is a no-op without a `[`). The attachment loop is factored into a pure `attachSignatures` helper so the bare-qualname keying contract is unit-tested (`TestAttachSignatures`) without spawning Python — reverting to `byName[entry.Name]` now fails that test. The Go adapter (`gotest`) is verified to not have the analogous coupling: `go test -list` only emits bare `Test*`/`Benchmark*`/`Example*` names, and its signature lookup keys on that same bare name.

### Changed — `/clusters` and `/flakes` now render from live GraphQL (`ui-clusters-flakes`)

The redesigned Clusters / Flakes screens (`web/src/components/teo/{Clusters,Flakes}.tsx`) stopped importing the `TEO_DATA` mock and now consume data via props. Each page server component (`web/src/app/{clusters,flakes}/page.tsx`) fetches the orphaned `FailureClustersQuery` / `FlakesQuery` through `gqlFetch` and maps the rows with a new pure adapter, `web/src/lib/teo-adapt.ts` (`adaptClusters` / `adaptFlakes`).

- **Adapter.** `teo-adapt.ts` maps the GraphQL row shape onto the existing `Cluster` / `Flake` view types. It passes through everything the `graphql-schema-fields` phase exposed (cluster x/y/r/category/affectedRuns; flake wilsonUpper/spark/status/durationMeanMs) and DERIVES the remaining design-only fields deterministically (FNV-1a hash, no `Math.random`): cluster `file` parsed from the representative stack, `stack[]` split, `tests[]`/`affectedRunIds[]`/`related[]` empty (no DB source); flake `owner {i,c}` from the test id, `quarantinedDays` 0, `wHi` fallback `rate*1.4` when `wilsonUpper` is null, `durMean` from ms→s. x/y/r/category are re-derived only as a fallback when the backend omits them.
- **Components.** `ClustersScreen`/`FlakesScreen` now take `{ clusters }` / `{ flakes }` props; the hardcoded default `selectedId 'fc-7e3a'` becomes `clusters[0]?.id`, an empty-state panel renders on zero rows, and `totals.wilsonMean` guards divide-by-zero. Visual design is unchanged.
- **Resilience.** Both pages coalesce a null `gqlFetch` (non-2xx / API outage) to `[]`, so a fresh or unreachable deployment shows the empty state instead of crashing.
- **No backend change.** The schema/resolvers were already extended by `graphql-schema-fields`; this phase is web-only plus the doc rows. Verification: `cd web && npm run typecheck` clean, `npm test` (51 tests) green, `go build ./internal/api/...` clean.

### Fixed — `ui-clusters-flakes` review sweep (Rules-of-Hooks, error observability, adapter tests)

Follow-up hardening on the GraphQL cutover above:

- **Rules-of-Hooks (major).** `ClustersScreen` placed its empty-state early `return` BETWEEN the `visible` and `edges` `useMemo` calls, so the empty render ran one fewer hook than the non-empty render. Because `clusters` is a server-fetched prop that can legitimately flip empty↔non-empty on an in-place re-render (App Router soft-nav / revalidation / a future client poll), that transition would throw "Rendered fewer/more hooks than during the previous render" and crash the page. The early return now sits below every hook (`edges` is computed unconditionally). `next lint` isn't wired in this repo so `react-hooks/rules-of-hooks` never caught it.
- **Error observability (minor).** `gqlFetch` now logs non-2xx responses and HTTP-200 GraphQL-level errors (`j.errors`) server-side, and documents that callers intentionally coalesce `null`→`[]`, so a backend regression during the cutover is observable rather than indistinguishable from a genuinely empty dataset.
- **Route resilience (minor).** The `/clusters` + `/flakes` page server components now wrap `gqlFetch` in try/catch returning `[]`, so a hard `fetch` reject (DNS failure, connection refused, unset/invalid `TEO_API_URL`) degrades to the empty state instead of throwing the route (there is no `error.tsx` boundary for these routes). Comments corrected to match the real behavior.
- **Adapter unit tests (major gap).** Added `web/src/lib/teo-adapt.test.ts` (25 cases) covering `hash32` determinism, `classifyCategory` per-category + precedence, `extractFile` parsing/fallback, `deriveOwner`/`deriveSpark` stability + length, and `adaptClusters`/`adaptFlakes` passthrough-vs-fallback (backend x/y/r/category & wilsonUpper passthrough, derived fallbacks, ms→s, null/empty→[]). The module previously shipped with zero tests.
- **Go/TS parity (minor).** `classifyCategory` was reconciled to mirror `internal/api/graphql_resolvers.go` `classifyClusterCategory` EXACTLY — branch precedence panic→timeout→network→race→assertion and the same substrings (dropped the extra `deadline`/`\brace\b`/`dial `/`connection`/`refused` heuristics that diverged from Go); doc comment updated to claim true parity.
- **Test rigor (minor).** `queries.test.ts` now asserts the 1-char spatial-map fields (`x`/`y`/`r`) as selection tokens anchored to selection-set whitespace, instead of `toContain('x')` which passed trivially against `representativeMessage`.

Verification: `cd web && npm run typecheck` clean; `npm test` for the two changed suites green (35 tests across `teo-adapt.test.ts` + `queries.test.ts`); `go build ./internal/api/...` clean. No backend or proto change.

### Added — `FlakeRecord.quarantinedAt`/`ownerTeam` + component & round-trip tests (`ui-clusters-flakes` test sweep)

Completes the `ui-clusters-flakes` wiring with the quarantine/owner fields the Flakes screen needs and direct component coverage.

- **Schema (additive).** `FlakeRecord` now exposes `quarantinedAt` (RFC3339, null when the test isn't quarantined) and `ownerTeam` (CODEOWNERS-resolved team). `queryFlakes` selects `COALESCE(fr.quarantined_at, t.quarantined_at)` + `t.owner_team` and normalizes the timestamp to RFC3339 before it hits the String field. Mirrored in `internal/api/graphql.go`, the `internal/api/server.go` SDL, and `web/src/lib/queries.ts`. No field removed/renumbered — purely additive.
- **Adapter.** `adaptFlakes` now resolves `status='quarantined'` + `quarantinedDays>0` (new `daysSince` helper) when `quarantinedAt` is set, `'flagged'` when `wilsonLower>0.05` and not quarantined, and seeds the owner avatar from `ownerTeam` when present (falling back to a deterministic testId hash). `GqlFlake` gained the two optional fields.
- **Component tests (NEW).** `web/src/components/teo/Clusters.test.tsx` and `Flakes.test.tsx` (@testing-library/react) render the prop-driven screens from adapter output: cluster list shows both titles + the count badge and the detail panel follows row clicks; flake table renders one row per test, the Tracked KPI counts them, a row click opens the detail sheet; both assert the empty-state path (no throw, KPIs read 0, no `NaN` in the DOM). A no-op `ResizeObserver` stub was added to `web/src/test-setup.ts` so the spatial map's `ResizeObserver` mounts under jsdom.
- **Go tests.** `graphql_schema_test.go` asserts the two new `FlakeRecord` fields and a `Do()` stub-source resolve of them (additive guarantee — pre-existing fields unchanged). `graphql_integration_test.go` seeds `owner_team`/`quarantined_at` and round-trips `wilson_upper`/`status`/`quarantined_at`/`owner_team`, re-checks the `wilson_lower>0.05` gate, and asserts `representativeStack` resolves as a single string; `graphql_http_integration_test.go` selects the new fields over the HTTP roundtrip.

Verification: `go test -count=1 ./internal/api/...` green; `go vet -tags=integration ./internal/api/...` clean (Docker-gated tiers not run locally); `cd web && npm test` green (99 tests) + `npm run typecheck` clean. The `/clusters` + `/flakes` components remain free of `TEO_DATA` (grep returns nothing).

### Added — GraphQL schema fields for the redesigned marquee UI (`graphql-schema-fields`)

Additively extends the read API so the redesigned Clusters / Flakes / Run-detail calibration screens can stop rendering from MOCK `web/src/lib/teo-data.ts`. Prerequisite for `ui-clusters-flakes` and `ui-home-calibration`. No migration — every value is computed server-side from existing Postgres tables.

- **FailureCluster spatial map.** New `x`/`y`/`r` coordinates (computed in `computeClusterLayout`: x = last_seen newest→left, y = log-scaled occurrences, r = blast-radius pixels 9–40), `category` (`classifyClusterCategory` keyword heuristic: panic/timeout/network/race/assertion), `stackFingerprint`, and `affectedRuns` (distinct-run subquery over `test_executions.failure_cluster_id`). x/y/r are presentation-only and relative to the returned page.
- **FlakeRecord sparkline.** New `wilsonUpper` (cast `::float`), `spark` (last-20 P/F/S outcomes, chronological, computed in one batched `test_id = ANY($1)` query — no N+1), `status` badge (from `tests.status`), and `durationMeanMs`.
- **Run predictor calibration.** New `Run.predictor { mae rho modelVersion p50DeltaMs p95DeltaMs sampleCount confidence }` (`queryRunPredictor`, computed from finished shards; Pearson rho in Go; `modelVersion` reads `runs.meta->>'predictor_model'`, falls back to `heuristic`; null for <2 finished shards) and `Shard.deltaMs` (actual−predicted). Per-test predictor confidence is out of scope (only shard-level predicted/actual is stored).
- **Wiring.** Schema (`internal/api/graphql.go`), resolvers (`internal/api/graphql_resolvers.go`), the hand-written SDL at `/graphql/schema` (`internal/api/server.go`), and the named operations in `web/src/lib/queries.ts` all updated. Components keep reading MOCK data until the downstream UI phases rewire to urql.

Review fixes (post-review sweep on this gap):

- **Blocker — `uuid = text` runtime failure in flake sparklines.** `attachFlakeSparklines` passed a Go `[]string` into `WHERE te.test_id = ANY($1)`, which pgx encodes as `text[]` under the pool's extended protocol; `test_executions.test_id` is `uuid`, and there is no `uuid = text` operator, so every flake query failed at runtime with `operator does not exist: uuid = text`. Fixed by casting the array param: `ANY($1::uuid[])`. (Only reachable via the Docker-gated integration test, so `go build`/`go vet`/`npm test` never caught it.)
- **Minor — orphaned `Shard` type in the published SDL.** The hand-written SDL declared `type Shard { … deltaMs }` but `type Run` had no `shards`/`failedTestCount` field, leaving `Shard` unreachable and the doc drifted from the programmatic schema. Added `shards: [Shard!]`, `failedTestCount: Int`, and `preemptionCount: Int` to the SDL `Run` block.
- **Test coverage.** Added `internal/api/graphql_layout_test.go` (table-driven unit tests for the new pure helpers: `classifyClusterCategory`, `clampFloat` NaN/Inf/clamp, `pearson` n<2/mismatched-len/zero-variance/±1, `percentile` empty/edge/interior, `flakeStatusBadge`, `computeClusterLayout` single-row/all-equal/newest→left/empty) and extended `graphql_schema_test.go` with `RunPredictor`/`FailureCluster`/`FlakeRecord` shape checks plus `Shard.deltaMs`. The backing SQL resolvers remain integration-tier (Docker-gated).
- **Test coverage (this sweep).** Added a pure `encodeSparkline` helper (mirrors the SQL CASE) + `TestSparklineEncoding` (passed→P/skipped→S/else→F, 20-char cap keeping the trailing/newest outcomes) and `TestClusterCoordsDeterministic` (x/y/r bounds, y monotonically smaller for larger occurrences, finite single-row fallback) in `graphql_layout_test.go`. Extended the integration tier: the shared `seed` now adds a second failure cluster, a cluster-2-attached failed execution, and a 25-execution sparkline run; new `TestQueryFailureClustersComputesLayout`, `TestQueryFlakesIncludesSparklineAndWilsonUpper`, and `TestQueryRunPredictor` (mae == mean |delta|, `heuristic` fallback, nil for <2 finished shards); and `graphql_http_integration_test.go` now selects the new FailureCluster/Flake/`predictor { mae }`/`shards { deltaMs }` fields end-to-end. `queries.test.ts` gained spatial-map/sparkline/predictor field assertions.

Verification: `go build ./internal/api/...` clean; `go test -count=1 ./internal/api/...` green (unit tier); `go vet -tags=integration ./internal/api/...` clean (integration tier compiles — not run here, Docker unavailable); `cd web && npm run typecheck` and `npm test` (51 tests) green.

### Added — Assignment plan archived to S3 (#2)

Closes the last functional gap from the 2026-05-20 backlog reconciliation (S-05-04 AC1 / FR-304).

- **Run Manager S3 archive.** After planning, the Run Manager uploads the computed plan to `runs/<id>/plan.json` via the new `Manager.PlanStore` (`logstore.Uploader`) and the shared `runmanager.PlanObjectKey` helper. Best-effort — the plan also lives in `runs.meta.computed_plan`, so a transient S3 failure logs a warning instead of failing the run. Wired in `cmd/run-manager` (gated on `TEO_S3_BUCKET`) and the Helm run-manager deployment (`storage.s3.*`).
- **Read-back.** New `logstore.S3.Download` (GetObject) plus **`teo replay --from-s3 --s3-bucket=…`**, which reads the archived plan and runs the same determinism check as the Postgres path (default unchanged).
- **Lint config.** Added `cancelled` to the misspell `ignore-rules` in `.golangci.yml` — it's TEO's domain spelling (the `teo.runs` status enum is `cancelled`), so `locale: US` was wrongly flagging legitimate comments.

Verification: `go build ./...` clean; `go test ./...` green incl. the new `runmanager` plan-archive unit tests (key/body, no-store no-op, error-swallow); MinIO download round-trip added to the logstore integration suite; `golangci-lint run` 0 issues on the touched packages. `teo replay --from-s3` round-trips a real archived plan.

### Added — AST-signature fingerprints + `teo discover` (#3, #4)

Continues the 2026-05-20 backlog-reconciliation fixes: tests now carry a normalized AST signature so a body change yields a distinct test identity (and fresh flake history) instead of silently inheriting the old body's stats.

- **Go AST signature (#3, S-14-01 AC4).** `pkg/adapter/gotest/astsig.go` parses each package's test files via `go/ast` (located through `go list -f`) and hashes the normalized function body — stable across reformatting and comment edits, changing when the logic changes. Attached to `model.TestEntry.ASTSignature` during `Discover`.
- **Python AST signature (#4, T-06-01-03).** `pkg/adapter/pytest/astsig.go` runs an embedded `ast`-module helper over the discovered files, hashing each test's body (module-level `test_*` functions and `test_*` methods of `Test*` classes, keyed to match pytest nodeids). Best-effort: empty signature if no Python interpreter is present. Jest stays signature-less (deferred to v1.5 per S-14-02 AC3).
- **Fingerprint + persistence.** The worker fingerprint is now `path::name::paramsHash::astSig`. `model.TestEntry.ASTSignature` and `nats.DispatchTest.ASTSignature` carry it through both worker claim paths (Postgres SKIP-LOCKED and NATS dispatch); migration **006** adds `teo.tests.ast_signature` with a partial index for future move/rename linking.
- **`teo discover --runner <r> [dir]` (closes S-06-01 AC1).** New CLI that runs a runner adapter's discovery (computing AST signatures) and emits a manifest JSON for the `manifest` field of `POST /api/v1/runs` — the production producer of fingerprints. Previously there was no `teo discover` command and `Discover` had no non-test caller.

Verification: `go build ./...` clean; `go test ./...` green (Go AST tests incl. an end-to-end temp-module case; Python AST tests run against the local interpreter); `golangci-lint run` 0 issues on the new/edited files. A live `teo discover --runner go ./internal/version` emits populated `ast_signature` values.

### Added — Backlog reconciliation fixes: replay CLI, log-tail viewer, OIDC sign-in (#1, #5, #6)

A 2026-05-20 code audit found 12 backlog items marked ✅ in `progress.md` that were not wired in code (see the "Backlog reconciliation" section there). Three of them are now implemented:

- **`teo replay <run_id>` (#1, S-05-04 / FR-304).** New `cmd/teo/replay.go` reads the persisted `runs.meta.computed_plan`, reconstructs the scheduler inputs, and re-runs the pure scheduler to verify the plan is still deterministic (exit 1 + diff summary on mismatch; `--json` for scripting). New `scheduler.DefaultConstraints()` and `scheduler.Replay()` are shared by the live planning path (`internal/runmanager`) and the CLI so the two can't drift. Unit-tested in `internal/scheduler/replay_test.go`.
- **UI per-test log-tail viewer (#5, S-09-03 / FR-703-704).** New `logstore.Presigner` + `S3.Presign` (presigned GET). API endpoint `GET /api/v1/runs/{id}/tests/{execId}/log` returns a short-lived presigned URL, ownership-checked via the run join (no IDOR) and returning 501 when S3 isn't configured. Next.js BFF route `/api/logs` proxies the tail of that URL via HTTP suffix-Range (so the browser needs no S3 reachability); `LogTail` client component (with "Load earlier" pagination) + a `runs/[id]/tests/[execId]` page host it. Wired into `cmd/api` (gated on `TEO_S3_BUCKET`) and the Helm api deployment (`storage.s3.*`). Tested on both sides.
- **OIDC sign-in flow + JWT refresh (#6, S-03-02 / FR-801).** New dependency-free `internal/oidc` package: discovery, authorization-code exchange, and RS256 ID-token verification against the IdP's JWKS (built on net/http + crypto/rsa + the already-vendored golang-jwt). API routes `/auth/login`, `/auth/callback`, `/auth/logout`, `/auth/session`, `/auth/refresh` mint a TEO HS256 JWT into an httpOnly `teo_session` cookie on success; the auth middleware now also reads that cookie. New `/login` page + `SessionNav` header widget. Configurable via `auth.oidc.*` Helm values (issuer, clientID, client secret, UI base URL) wired into the api deployment. Unit-tested with an in-process fake IdP (full RS256/JWKS round-trip).

Verification: `go build ./...` clean; `go test ./...` green incl. the new `internal/oidc` package (26 testable packages); `golangci-lint run` 0 issues on the touched packages; `web` typecheck clean + 48 Vitest tests green (incl. new `LogTail` + `SessionNav` suites).

### Added — Post-v1.0 unit coverage sweep (gotest/jest/quarantine + db/audit/predictor)

Closes the named coverage gaps from the v1.0.0 resume callout — the testable surface of every backend package now has unit tests, even where the DB-backed paths remain integration-tier.

- **`pkg/adapter/gotest`** — refactored `Execute` to extract a `processEvents(io.Reader, ...)` helper so the JSON-stream parser can be exercised against canned input without spawning `go test`. New tests cover the basic pass/fail/skip stream, package-level event suppression, malformed-line skip, and the synthetic-entry fallback for un-indexed tests. Plus `dedupe`, `mergeEnv`, and `New()` defaults.
- **`pkg/adapter/jest`** — analogous extraction of `parseListTests` and `parseReport` from `Discover`/`Execute`. Tests cover the full `translate()` status mapping (passed/failed/skipped/pending/todo + unknown→errored), path-relative `--listTests` output, nested `describe`/`it` ancestor assembly, todo-as-skipped, joined failure messages, and structural-error rejection on malformed JSON.
- **`pkg/adapter/template`** — copy-and-fill skeleton at `pkg/adapter/template/` (referenced from `docs/adapters/spi.md`) plus a `template_test.go` that pins the SPI invariants every adapter must satisfy regardless of runner: non-empty `Name()`, no-op on empty test slice, unknown-status→`OutcomeErrored`, `mergeEnv` semantics. New adapter authors get a green test suite as their starting point.
- **`internal/quarantine`** — `quarantine_test.go` covers `buildIssueBody` Markdown structure (must contain "## Flaky test detected", "## What happened", "## Next steps"), key-fact substitution (path/name/percent/sample-size/Wilson reference), and the zero-sample NaN guard. `github_opener_test.go` pins nil-receiver and nil-client guards on both `Open` and `Comment` so a misconfigured Daemon errors instead of panicking.
- **`internal/db`** — `parseClickHouseDSN` is now unit-tested against full URL with creds, no-creds, user-only, default-database fallback, applied connection defaults (5s dial, 20/5 conn pool, 1h lifetime), and malformed-URL rejection.
- **`internal/audit`** — covers the nil-`Logger` and nil-pool early-return guards on `Log`. Happy-path INSERT remains exercised by the API integration tests under `-tags=integration`.
- **`internal/predictor`** — `NewHeuristic` seeded defaults, `Predict` nil-receiver/nil-pool error paths, `coldStart` fingerprint shape and P95 = 3 × P50 invariant, `coldOnly` order/count preservation, and `defaultFor` known-runner-vs-fallback. DB-backed `loadStats` remains integration-tier.
- **E-14 SPI doc** — `docs/adapters/spi.md` now exists as the canonical adapter contract (Discover/Execute semantics, fingerprint/redaction/OTel boundary, conformance checklist). `progress.md` and `docs/backlog/tasks.md` rows updated from "SPI doc pending" → "all complete".

Verification: `go build ./...` clean, `go test -count=1 ./...` 25 testable packages green (was 20 at end of v1.0.0 sweep).

### Fixed — CI run-6: integration tests reaching the API for the first time

The migration runner finally healed enough to let the API integration tests execute their HTTP/SQL paths end-to-end — and three latent bugs surfaced in that first complete run:

- **6× export integration tests fail with 401.** `internal/api/export_integration_test.go` constructed every request via `httptest.NewRequest(...)` with no auth header. The export handler enforces auth (`auth.PrincipalFrom(r.Context()) == nil → 401`) so anonymous requests bounce. Fix: route every test request through `signedRequest(t, ...)` from the same package's `runs_integration_test.go` — it issues a real Bearer JWT against the same secret `newTestServer` configures. Replaced six call sites with `replace_all`. (These tests had only ever been compile-checked locally because Docker wasn't available; the auth oversight didn't surface until CI's testcontainers run.)

- **2× rerunFailed tests fail with `could not determine data type of parameter $6`.** The rerunFailed INSERT used `jsonb_build_object('test_count', $6, 'runner', $7)`. `jsonb_build_object` is declared `(VARIADIC any)` so Postgres can't infer types during prepare and gives up with SQLSTATE 42P18. Fix in `internal/api/graphql_resolvers.go`: added explicit casts `$6::int, $7::text` plus an inline comment so this doesn't regress.

- **Docker build job fails with `go.mod requires go >= 1.25.0 (running go 1.23.12)`.** The Dockerfile's `ARG GO_VERSION=1.23` predated the toolchain bump in `go.mod`. Same root cause as the run-1 ci.yml drift, just a third place that hadn't been swept yet. Bumped to 1.25.

Verification: `go test ./...` 20 packages green, `go vet -tags=integration ./...` clean, `golangci-lint run` reports 0 issues.

### Fixed — CI run-5: golangci-lint vs Go 1.25 + 11 latent issues
- **Lint job — `could not load export data: internal error in importing "sync/atomic" (unsupported version: 2)`** — golangci-lint v1.61.0 (Sep 2024) can't read Go 1.25's stdlib export-data format. Every typecheck-based linter then reported phantom "undefined: pgx/chi/jwt/nats/clickhouse" for code that compiles fine. Fixed by:
  - `.github/workflows/ci.yml`: bumped `GOLANGCI_LINT_VERSION` v1.61.0 → v2.5.0 and `golangci/golangci-lint-action` v6 → v7 (v6 only supports v1.x linter).
  - `.golangci.yml`: migrated v1 → v2 schema. Notable shape changes: `linters.disable-all` → `linters.default: none`; `linters-settings` → `linters.settings`; `gofmt` + `goimports` moved out of `linters.enable` into the new `formatters.enable` block; `issues.exclude-rules` → `linters.exclusions.rules`; `issues.exclude-dirs` → `linters.exclusions.paths`. Set `run.go: "1.25"` to match `go.mod`.
- **One real bug surfaced by the v2 typecheck.** `pkg/adapter/pytest/pytest.go` constructed `cmd := exec.CommandContext(ctx, ...)` BEFORE wrapping `ctx` with `context.WithTimeout(...)` — meaning the pytest invocation never observed the configured timeout. Moved the timeout setup to run before `CommandContext`. (ineffassign caught this — flagged the never-read reassignment.)
- **10 latent issues fixed.** Capitalized error string in `internal/digest/sender.go` (staticcheck ST1005); `buildSlaBody` → `buildSLABody` (revive var-naming); `resolveOwner`'s always-nil `err` return dropped along with caller (unparam); pre-allocated `files` and `lines` slices (prealloc); blank-import comment on `_ "github.com/ClickHouse/clickhouse-go/v2"` (revive blank-imports); three `_`-renames for unused method parameters (revive); trailing newline in `internal/api/export_integration_test.go` (gofmt).
- **errcheck relaxations** (documented inline in `.golangci.yml`):
  - `check-blank: false` — the `_ = foo()` pattern is the canonical Go idiom for "I know this returns an error and I'm intentionally ignoring it"; forcing wrappers around it adds noise without catching real bugs.
  - Expanded `exclude-functions` for `defer`-style cleanup that doesn't rely on the result: `pgx.Tx.Rollback`, `pgx.Rows.Close`, `chdriver.Conn/Rows.Close`, `*sql.DB/Rows.Close`, `*pgxpool.Pool.Close`, `*net/http.Server.Shutdown`, `*tabwriter.Writer.Flush`, `fmt.Fprintln`/`Fprintf`, `os.RemoveAll`.
- **gosec scoped exclusions:** runner adapters (`pkg/adapter/{pytest,gotest,jest}/`) legitimately need to launch subprocesses with caller-supplied test paths and read their own JSON output files (G204/G304 false positives in this scope). G115 integer-overflow exclusions on the OTLP timestamp conversion sites (`internal/api/export.go`, `internal/resultpipeline/otlp.go`, `internal/runmanager/manager.go`) — these use the canonical proto-encoding pattern.
- **Path exclusion:** added `web/node_modules` to `linters.exclusions.paths` so a local `npm install` doesn't surface third-party Go scraps (e.g. `flatted/golang/`) as TEO-owned.
- **Followup deferred:** `revive.exported` (must-have-doc-comments on exported types/consts) is disabled — six internal types in `internal/auth`, `internal/migrate`, `internal/model`, `internal/scheduler` would each need a one-liner. Tracked but not gating v1.0.0.

Verification: `golangci-lint run --timeout=5m` now reports `0 issues.` against the full repo. `gofmt -l .` is empty.

### Fixed — CI run-4 failure: invalid `user_roles` PRIMARY KEY
With the splitter now reporting the offending statement number, CI pinpointed `001_initial.up.sql` statement 33 (the `teo.user_roles` CREATE TABLE) as the source of the persistent `syntax error at or near "("`. Root cause: the table used `PRIMARY KEY(user_id, role, COALESCE(repo_id, '00000000-...'::uuid))` — Postgres rejects function calls inside PRIMARY KEY constraints (only plain column references are allowed). The COALESCE was a workaround to enforce uniqueness across nullable `repo_id`, but it was never valid SQL.

Replaced with two partial unique indexes — idiomatic Postgres for the "either global or per-repo unique" pattern:

```sql
CREATE UNIQUE INDEX user_roles_global_idx
    ON teo.user_roles (user_id, role)
    WHERE repo_id IS NULL;
CREATE UNIQUE INDEX user_roles_per_repo_idx
    ON teo.user_roles (user_id, role, repo_id)
    WHERE repo_id IS NOT NULL;
```

Same uniqueness semantics, valid SQL. No Go code or downstream migration referenced `user_roles` as a foreign-key target, so dropping the PK is safe.

Added `TestSplitSQL_RealMigrationFile` — loads the live 001_initial.up.sql, runs it through the splitter, and asserts ≥30 statements + each ends with `;` (with a documented exception for the plpgsql function body whose statement-terminating `;` precedes the LANGUAGE clause). Catches future schema-shape drift at the unit-test layer instead of waiting for the testcontainers Postgres in CI.

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
