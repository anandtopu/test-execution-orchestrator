# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Canonical truth

`progress.md` at the repo root is the single source of truth for implementation status — every epic, every FR, every named follow-up. **Read it before planning work.** Every behavior-changing PR is expected to update the corresponding row in the same commit; `docs/architecture/overview.md` is the spec, `progress.md` is what's actually wired up. When the two disagree, treat `progress.md` as ground truth for code.

`PRD.md` (product context) and `docs/adr/` (binding architectural decisions, numbered ADRs) are the next reading layer. ADR-0012 (revised) defines v1.0 scope; anything labeled 📦 in `progress.md` is deferred there.

## Common commands

The repo is driven by `Makefile`. All commands assume the working dir is the repo root.

```bash
make build              # compile all 7 services into bin/
make test               # full test suite with -race -count=1 (unit only by default)
make test-short         # -short flag, faster local loop
make test-integration   # -tags=integration, requires Docker (testcontainers spins up Postgres + MinIO)
make lint               # golangci-lint v2.x; v1.x configs are rejected
make fmt                # gofmt + goimports (-local github.com/teo-dev/teo)
make licenses           # blocks AGPL/GPL transitively (go-licenses)
make proto              # regenerate internal/proto/teov1/*.pb.go via buf (requires buf + protoc-gen-go + protoc-gen-go-grpc)
make all                # lint + test + build
```

Single-package or single-test runs:

```bash
go test ./internal/scheduler/...
go test -run TestWilsonInterval ./internal/flake
go test -tags=integration -run TestGraphQL ./internal/api
```

Web UI (Next.js 15 + React 19 + urql + GraphQL):

```bash
cd web && npm test            # vitest run
cd web && npm run test:coverage # vitest run --coverage
cd web && npm run typecheck   # tsc --noEmit
cd web && npm run lint        # next lint
cd web && npm run dev         # next dev
```

Python ML predictor (FastAPI + LightGBM, ADR-0019):

```bash
cd services/predictor-ml
pip install -e .[dev]
pytest
```

`teo` CLI subcommands (built into `bin/teo`): `migrate up|status`, `digest dry-run --user=<email>`, `doctor [--json]`, `version`, `help`. `teo doctor` exercises connectivity to every TEO dependency in parallel; useful for diagnosing local/staging brokenness.

The `make migrate` target is a leftover no-op stub from before E-02 landed — **don't trust it**. Migrations are run via the CLI: `bin/teo migrate up` / `bin/teo migrate status` (driven by `internal/migrate`).

**Windows:** the Makefile pins `SHELL := /usr/bin/env bash`, so `make` requires bash (Git Bash or WSL). From raw PowerShell, fall back to the underlying tooling directly: `go build ./cmd/<svc>`, `go test -race -count=1 ./...`, `golangci-lint run ./...`.

## Toolchain pins (these matter)

- **Go 1.25** (`go.mod` is `go 1.25.0`; CI uses 1.25; the Dockerfile's default `ARG GO_VERSION` is 1.25). The README's "Go 1.23+" line is stale — do not believe it.
- **golangci-lint v2.5.0+**. v1.x cannot read Go 1.25's stdlib export-data and will report phantom "undefined" errors for code that compiles fine. Config schema is v2 (`linters.default: none`, `linters.settings`, `formatters.enable`).
- **Node 22 LTS** for the web UI.
- Module path is `github.com/teo-dev/teo`; goimports is invoked with `-local github.com/teo-dev/teo` so internal imports group correctly.

## Repository shape

```
cmd/{teo,api,run-manager,scheduler,result-pipeline,predictor,worker}   # one binary per dir
internal/                                                               # all production Go code (private)
pkg/adapter/{pytest,gotest,jest}                                        # runner-adapter SPI (E-14)
proto/teov1/*.proto                                                     # gRPC contract; generated → internal/proto/teov1
migrations/{postgres,clickhouse}/NNN_*.{up,down}.sql                    # forward-only, numbered
deploy/helm/teo/                                                        # umbrella chart, vendored subcharts
services/predictor-ml/                                                  # Python LightGBM service (separate from Go)
web/                                                                    # Next.js UI (GraphQL only; zero REST calls)
docs/{adr,architecture,backlog,operations,process,requirements}         # specs, ADRs, runbooks
```

## Architecture in one screen

TEO is a self-hosted, **single-tenant**, **AWS-only** test execution orchestrator. There are **no `tenant_id` columns and no per-tenant isolation** — assume single tenancy throughout. The control plane is a fan-out of cooperating Go services, all sharing Postgres (OLTP), ClickHouse (OLAP), NATS JetStream (dispatch), and S3 (logs/cold).

```
teo CLI ──gRPC──▶ API gateway ──▶ Run Manager (state machine + leader-elected via pg_try_advisory_xact_lock per run, ADR-0013)
                       │                │
                       │                ├──▶ Scheduler (pure function: Plan(tests, predictions, fleet, constraints) → AssignmentPlan; LPT bin-packing, ≤4/3 × OPT, replayable JSON plan)
                       │                ├──▶ Predictor (Go heuristic always-on; Python LightGBM optional; auto-fallback on outage/MAE drift)
                       │                └──▶ NATS subject teo.shards.dispatch
                       │                         │
                       │                         ▼
                       │                  Worker pool (Karpenter-managed; spot + on-demand NodePools; pytest/go test/jest adapters; per-test OTLP spans)
                       │                         │
                       └──▶ Result Pipeline ◀────┘  (OTLP gRPC :4317 → ClickHouse span_events; failure clustering by stack-trace fingerprint → Postgres failure_clusters; per-test logs → S3 via internal/logstore)
                       │
                       └──▶ GraphQL/REST read API (web UI consumes GraphQL only, polls every 2s on run-detail; auto-stops on terminal status)
```

Cross-cutting:
- **Auth:** JWT HS256 (v1) for humans + argon2id-hashed API keys for CI; 30s revocation cache; Dex preconfigured for OIDC in the chart.
- **GitHub:** App scaffold; HMAC SHA-256 webhook verification; `runmanager.RunObserver` pattern dispatches snapshots post-commit; `github.CheckObserver` lifecycle-manages a Check Run with top-3 failure clusters in the finalize summary.
- **Spot interruption:** IMDSv2 poller (`internal/spot/`) → Agent draining state machine (atomic `draining` flag, NATS naks back) → Run Manager `reschedulePreempted` sweep recomputes residue and creates a fresh shard with the unfinished tests recorded under `runs.meta.reshards[<new_shard_id>]`.
- **Observability:** All metrics live in `internal/metrics` (one place, the dashboards/alerts in the Helm chart reference these names exactly). Chi middleware records `http_server_requests_seconds` with the chi RoutePattern as label so cardinality stays bounded.

## Conventions worth knowing

- **PRs reference a story ID** from `docs/backlog/stories.md` (`S-<epic>-<n>`) plus any FR/ADR IDs. Conventional Commits in titles (`feat(scheduler): ...`).
- **Definition of Done** is `docs/process/definition-of-done.md`. Reviewers refuse to merge a PR that doesn't satisfy it.
- **Migrations are forward-only**, numbered (`001_*`, `002_*`, ...) with paired `.up.sql`/`.down.sql`. Test against both empty DB and a populated fixture.
- **gRPC proto changes must be additive** — no field removals or renumbers.
- **Errors:** wrap with `fmt.Errorf("%w: ...", err, ...)`; include enough context to diagnose without a debugger.
- **Logs:** structured JSON via `log/slog`; standard fields are `service`, `level`, `time`, `trace_id`, `span_id`.
- **Tests:** stdlib `testing` + `testify/require`. **No `time.Sleep` to wait for events** — the canonical idiom is an inline bounded-deadline poll (`for deadline := time.Now().Add(...); time.Now().Before(deadline);`); see `internal/worker/drain_test.go` for the shape. There is no shared `Eventually` helper. Property tests where mathematical invariants exist (the scheduler test brute-forces 30 random instances and asserts ≤4/3 × OPT).
- **Integration tests** live alongside unit tests but are gated by `//go:build integration`. They use the harnesses in `internal/testpg/` (real Postgres) and `internal/testminio/` (real S3-compatible MinIO) via testcontainers — Docker is required and tests will fail loudly without it.
- **Subprocess-spawning code in `pkg/adapter/{pytest,gotest,jest}` legitimately needs gosec G204/G304 exemptions** (caller-supplied test paths; reading own JSON output). Don't generalize this exemption elsewhere.
- **errcheck:** `_ = foo()` is the canonical idiom for "I know this returns an error and I'm intentionally ignoring it." Don't wrap it.

## Things to be careful about

- The repo went through a string of CI build-failure recoveries (see `CHANGELOG.md` `[Unreleased]`). `build_failures.txt` is a transient artifact from one of those sweeps; don't rely on it.
- `progress.md` advertises v1.0.0 as functionally release-ready. The release pipeline is `release.yml` + `.goreleaser.yml` (cosign-signed binaries + Syft SBOMs + chart-released Helm chart on tag push). Don't cut a tag without running the restore drill in `docs/operations/restore-drill.md` first.
- Service binaries print build identity when invoked without args (`internal/version` injects via `-ldflags -X`). Don't add an unconditional log line above that — the no-args path is also used as a smoke test.
