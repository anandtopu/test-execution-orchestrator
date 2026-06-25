# TEO — Developer Handoff

**Audience:** an engineer picking up this codebase cold.
**Goal:** get you productive — build, test, navigate, and know where the bodies are buried — in one read.
**Last updated:** 2026-06-24 (v1.0.0 shipped; first v1.1 item — WebSocket subscriptions — shipped; v1.5 Jest AST signature in review on PR #4).

> **Read these first, in order:** [`progress.md`](../../progress.md) (ground truth for what's wired up) → [`CLAUDE.md`](../../CLAUDE.md) (working agreements) → [`PRD.md`](../../PRD.md) (product context) → [`docs/adr/`](../adr/) (binding decisions). This handoff is the connective tissue between them.

---

## 1. What TEO is

A self-hosted, **single-tenant, AWS-only** test execution orchestrator. It schedules test runs across a worker pool, ingests results as OpenTelemetry traces, statistically detects flaky tests, and surfaces it all through a GraphQL UI and GitHub Checks. Think "distributed test runner + flake intelligence" as a control plane you run yourself.

There is **no multi-tenancy** — assume single tenant throughout (no `tenant_id` columns, no per-tenant isolation).

For the system shape, read [`architecture/diagrams.md`](../architecture/diagrams.md). For the data, read [`architecture/schema.md`](../architecture/schema.md) and [`architecture/er-diagram.md`](../architecture/er-diagram.md).

---

## 2. Toolchain (these pins matter)

| Tool | Version | Notes |
|---|---|---|
| Go | **1.25** | `go.mod` is `go 1.25.0`. The README's "1.23+" line is stale. |
| golangci-lint | **v2.5.0+** | v1.x cannot read Go 1.25 export data → phantom "undefined" errors. Config is v2 schema. |
| Node | **22 LTS** | for `web/` (Next.js 15 + React 19) |
| Python | 3.x | only for `services/predictor-ml` (FastAPI + LightGBM) |
| buf + protoc-gen-go(-grpc) | — | only needed for `make proto` |

Module path: `github.com/teo-dev/teo`. goimports runs with `-local github.com/teo-dev/teo`.

---

## 3. Build & test

The repo is driven by a `Makefile`, but **the Makefile needs bash** (`SHELL := /usr/bin/env bash`). On Windows use Git Bash/WSL for `make`, or fall back to raw Go tooling from PowerShell.

```bash
make build              # compile all 7 services into bin/
make test               # unit suite: -race -count=1 -timeout 120s
make test-short         # -short, faster local loop
make test-integration   # -tags=integration; needs Docker (testcontainers: Postgres + MinIO)
make lint               # golangci-lint v2
make fmt                # gofmt + goimports
make all                # lint + test + build
make help               # list every target
```

Raw equivalents (Windows/PowerShell, no bash):
```powershell
go build ./cmd/<svc>
go test -count=1 ./...
golangci-lint run ./...
```

Single-package / single-test:
```bash
go test ./internal/scheduler/...
go test -run TestWilsonInterval ./internal/flake
go test -tags=integration -run TestGraphQL ./internal/api
```

Web UI:
```bash
cd web && npm test          # vitest
cd web && npm run typecheck  # tsc --noEmit
cd web && npm run dev
```

**Gotchas that will bite you:**
- `-race` needs cgo. On a stock Windows box without a C toolchain, `go test -race` fails with *"-race requires cgo"* — run without `-race` locally; CI runs it with `CGO_ENABLED=1`.
- `make migrate` is a **no-op stub** — don't trust it. Migrations run via the CLI: `bin/teo migrate up` / `migrate status` (Windows: `bin/teo.exe`).
- Built binaries land in `bin/` with a `.exe` suffix on Windows.
- Service binaries print build identity when run with no args (a smoke test) — don't add a log line above that path.

---

## 4. The seven services (`cmd/`)

| Binary | Role | Long-running? |
|---|---|---|
| `teo` | CLI: `migrate`, `discover`, `replay`, `digest`, `doctor`, `version`. The CI entry point and ops Swiss-army knife. | no |
| `api` | gRPC + REST + GraphQL gateway. Auth, the read API, WebSocket subscriptions. Stateless. | yes |
| `run-manager` | The orchestration state machine. Leader-elected **per run** via `pg_try_advisory_xact_lock` (ADR-0013). Calls predictor + scheduler, creates shards, dispatches to NATS, runs finalize. | yes |
| `scheduler` | The pure `Plan(...)` function as a binary (for replay/testing). In the hot path it's invoked **in-process** by the run-manager. LPT bin-packing, ≤4/3×OPT (ADR-0005). | (function) |
| `predictor` | Per-test duration/flake predictions. Go heuristic always-on; proxies to the optional Python LightGBM service with auto-fallback. | yes |
| `result-pipeline` | OTLP gRPC receiver (:4317). Writes Postgres + ClickHouse + S3; failure clustering; flake detection. | yes |
| `worker` | Pulls assignments, runs tests via adapters, streams OTLP + `ReportTestFinished`. Karpenter-managed (spot + on-demand). | yes (pod) |

gRPC contracts live in `proto/teov1/` (`Runs`, `Workers` services) → generated into `internal/proto/teov1`. **Proto changes must be additive** (no removals/renumbers).

---

## 5. Where things live (`internal/`)

Production Go is all under `internal/` (private). Map by concern:

| Concern | Package(s) |
|---|---|
| Run orchestration / state machine | `runmanager`, `runsvc`, `grpcsvc` |
| Scheduling | `scheduler` |
| Prediction | `predictor` |
| Result ingestion | `resultpipeline`, `logstore` (S3) |
| Flake math & quarantine | `flake` (Wilson interval), `quarantine`, `codeowners` |
| Data access | `db`, `migrate`, `model` |
| Messaging | `nats` |
| Auth / identity | `auth`, `oidc`, `audit` |
| GitHub integration | `github` (App, webhooks, Check Runs, Issues) |
| Cost / spot | `cost`, `spot` |
| Read/UI API | `api` (GraphQL + REST) |
| Cross-cutting | `metrics` (single source for metric names), `config`, `redact`, `version`, `doctor` |
| Notifications | `digest` (owner digest) |
| Test harnesses | `testpg` (real Postgres), `testminio` (real S3), `testch` (ClickHouse) — testcontainers, `//go:build integration` |

Runner adapters are the one public package: `pkg/adapter/` — `adapter.go` is the SPI interface, `template/` is the copy-me starter, and `pytest/`, `gotest/`, `jest/` are the three shipped adapters. See [`docs/adapters/spi.md`](../adapters/spi.md).

---

## 6. Request flow in one paragraph

A CI step runs `teo run` → CLI discovers tests (adapter) → `CreateRun` (gRPC) hits **api** → row inserted (`runs.status=pending`). **run-manager** claims it under an advisory lock, asks **predictor** for per-test `{p50,p95,flake_prob}`, calls the **scheduler** to get a replayable `AssignmentPlan`, writes `shards` + `run_plans`, and publishes to NATS `teo.shards.dispatch`. **worker** pods pull assignments, run tests via the adapter, and stream OTLP spans + `ReportTestFinished` to **result-pipeline**, which writes `test_executions` (Postgres) + `test_runs`/`span_events` (ClickHouse) + logs (S3) and clusters failures. When the last shard finishes, run-manager finalizes (clustering, flake detection, GitHub Check update) and marks the run terminal. The UI follows live via a 2s GraphQL poll, upgraded to a WebSocket subscription when NATS is present. See the sequence diagram in [`diagrams.md` §3](../architecture/diagrams.md).

---

## 7. Data stores

- **Postgres** (`teo` schema) — transactional: runs, shards, tests, executions, flakes, identity, audit. 13 tables, 6 migrations. See [`schema.md`](../architecture/schema.md).
- **ClickHouse** (`teo` db) — analytical: `test_runs`, `span_events`, `flake_observations`, `mv_run_summary`. MergeTree + TTL.
- **NATS JetStream** — shard dispatch (`teo.shards.dispatch`) + UI hint bus (`teo.ui.run_changed`).
- **S3 / MinIO** — logs, screenshots, cold archive, backups.

`teo doctor [--json]` checks connectivity to every dependency in parallel — run it first when something's broken locally or in staging.

---

## 8. Conventions (the reviewer will hold you to these)

- **Every behavior-changing PR updates the matching `progress.md` row in the same commit.** `progress.md` is canonical truth for code; `overview.md` is the spec.
- PR references a **story ID** (`S-<epic>-<n>` from `docs/backlog/stories.md`) plus any FR/ADR IDs. Conventional Commit titles (`feat(scheduler): …`).
- Definition of Done: [`docs/process/definition-of-done.md`](../process/definition-of-done.md). Reviewers refuse PRs that don't satisfy it.
- Migrations: forward-only, numbered, paired up/down; test against empty **and** populated DB.
- Errors: wrap with `fmt.Errorf("%w: …", err, …)`. Intentional ignore is bare `_ = foo()` — don't wrap it.
- Logs: structured JSON via `log/slog`; standard fields `service`, `level`, `time`, `trace_id`, `span_id`.
- Tests: stdlib `testing` + `testify/require`. **No `time.Sleep` to wait for events** — use an inline bounded-deadline poll (see `internal/worker/drain_test.go`). Property tests where invariants exist (scheduler brute-forces 30 random instances asserting ≤4/3×OPT).
- Metrics: defined only in `internal/metrics`; the Helm dashboards/alerts reference those exact names.
- The `pkg/adapter/{pytest,gotest,jest}` subprocess code legitimately carries gosec **G204/G304** exemptions (caller-supplied test paths; reading own JSON). Don't generalize the exemption.

---

## 9. Gotchas & traps

- **golangci-lint v1 will lie to you** about Go 1.25 code — install v2.5.0+.
- **gofmt CRLF drift on Windows:** files committed with LF can show whole-file `1:1` gofmt findings locally because of CRLF in the working copy. The git index is LF → CI is clean. Don't "fix" these with `gofmt -w`; you'll fight the editor/git autocrlf instead.
- **`build_failures.txt`** at the root is a transient artifact from a past CI sweep — don't rely on it.
- **Don't cut a release tag** without running the restore drill ([`docs/operations/restore-drill.md`](../operations/restore-drill.md)) first. The release pipeline is `release.yml` + `.goreleaser.yml` (cosign-signed binaries + Syft SBOMs + chart-released Helm chart).
- The full restore drill §1–§7 needs **AWS staging credentials** and is currently the one standing ops item (the offline §0b chart pre-check is green).

---

## 10. Common tasks → where to start

| You want to… | Start here |
|---|---|
| Add a runner adapter | copy `pkg/adapter/template/`, implement `adapter.go` SPI, read [`docs/adapters/spi.md`](../adapters/spi.md) |
| Change run states | `internal/runmanager` + `runs.status` CHECK in a new migration + update [`diagrams.md` §4](../architecture/diagrams.md) |
| Touch the schema | new `migrations/postgres/NNN_*.{up,down}.sql` + update [`schema.md`](../architecture/schema.md) + [`er-diagram.md`](../architecture/er-diagram.md) |
| Add a GraphQL field | `internal/api` (resolvers + schema tests) + `web/src/lib/queries.ts` |
| Add a metric | `internal/metrics` only, then wire the dashboard in `deploy/helm/teo/` |
| Diagnose local/staging breakage | `bin/teo doctor --json` |
| Verify scheduler determinism | `bin/teo replay <run_id> [--from-s3] [--json]` |

---

## 11. Current status snapshot (2026-06-24)

- **v1.0.0 shipped** (tag, 2026-06-11); v1.0 backlog fully closed.
- **v1.1:** WebSocket subscriptions shipped (E-09, 2026-06-23).
- **v1.5:** Jest AST-signature fingerprint in review (PR #4) — once merged, all three adapters populate `ast_signature`.
- **Only standing item:** full cloud restore drill §1–§7 (AWS-blocked).

Always re-confirm against [`progress.md`](../../progress.md) — it's updated in the same commit as the code.
