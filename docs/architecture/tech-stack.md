# TEO — Tech Stack

**Status:** Draft, gates implementation
**Date:** 2026-04-30

Each row links to the ADR with the rationale and rejected alternatives. If you want to swap any of these, change the ADR first.

---

## Languages & Runtimes

| Layer | Choice | Version pin | ADR |
|---|---|---|---|
| Backend services (API, Run Manager, Scheduler, Result Pipeline, Worker Agent) | **Go** | 1.23 | ADR-0001 |
| Predictor service | **Go heuristic** (always present) + **Python LightGBM** (default-on, ML) | Go 1.23 / Python 3.12 | ADR-0001, ADR-0019 |
| Web UI | **TypeScript / Next.js** | Next.js 15, Node 22 LTS | ADR-0008 |
| CLI (`teo`) | **Go** | 1.23 | ADR-0001 |

## Frameworks & Libraries (Go)

| Concern | Library | Notes |
|---|---|---|
| HTTP router | `chi` | Idiomatic; small surface |
| gRPC | `google.golang.org/grpc` + `protoc-gen-go-grpc` | Standard |
| GraphQL | `github.com/99designs/gqlgen` | Schema-first, codegen, plays well with Go |
| Postgres driver | `jackc/pgx/v5` | Best-of-class; native driver, not `database/sql` |
| ORM / query builder | `uptrace/bun` | Light ORM; we lean on raw SQL where it pays |
| Migrations | `golang-migrate/migrate/v4` | Plain SQL files, easy to review |
| ClickHouse | `ClickHouse/clickhouse-go/v2` | Official driver |
| Messaging | `nats-io/nats.go` (JetStream) | ADR-0007 |
| Logging | `log/slog` (stdlib) | Structured JSON |
| Metrics | `prometheus/client_golang` | Standard |
| Tracing | `go.opentelemetry.io/otel` | We dogfood OTel |
| Config | `spf13/viper` + env overrides | Helm-friendly |
| CLI framework | `spf13/cobra` | For `teo` CLI |
| Validation | `go-playground/validator/v10` | Tag-based |
| Mocking | `vektra/mockery` | Codegen mocks |
| Testing | stdlib `testing` + `testify/require` | No exotic frameworks |
| Linting | `golangci-lint` (config in repo) | Required in CI |

## Frameworks & Libraries (UI)

| Concern | Library |
|---|---|
| Framework | Next.js 15 (App Router) |
| UI primitives | `shadcn/ui` (Radix + Tailwind) |
| Styling | Tailwind CSS 4 |
| Data fetching | `urql` (GraphQL) |
| Charts | `visx` for run-timeline Gantt; `recharts` for dashboards |
| State | React Server Components for read-heavy paths; `zustand` only where needed |

## Storage

| Store | Use | Version | ADR |
|---|---|---|---|
| Postgres | OLTP: runs, shards, tests, ownership, flake records | 16 (CloudNativePG-managed) | ADR-0003 |
| ClickHouse | OLAP: span events, durations, flake stats | 24.x OSS | ADR-0003 |
| S3 / MinIO | Cold archive: logs, screenshots, raw OTLP after 30 days | — | ADR-0003 |
| NATS JetStream | Streaming: shard dispatch, OTLP fan-out, worker heartbeats | 2.10 | ADR-0007 |

## Infrastructure

| Concern | Choice | ADR |
|---|---|---|
| Container orchestration | Kubernetes (EKS) | ADR-0009 |
| Worker autoscaling | **Karpenter** with mixed spot + on-demand NodePools | ADR-0006 (revised), ADR-0020 |
| Packaging | Helm chart (per-component subcharts; one umbrella) | ADR-0009 |
| CI for TEO itself | GitHub Actions (with TEO running its own tests once Phase 1 is done) | — |
| Image registry | GHCR (public, OSS distribution) | — |
| IaC | Terraform module (reference) for EKS + RDS + MSK alternatives where possible | — |

## Observability of TEO itself

| Concern | Choice |
|---|---|
| Metrics | Prometheus + Grafana (preinstalled in chart) |
| Logs | Loki |
| Traces | TEO emits OTel into its own pipeline (dogfood); a Grafana Tempo deployment is the **out-of-band** sink for cases where TEO is broken |

## Security

| Concern | Choice |
|---|---|
| Auth | OIDC via Dex (preconfigured) for humans; API keys for CI |
| Authz | RBAC: `admin`, `engineer`, `read-only`; per-repo allowlists |
| Secret management | Kubernetes Secrets for v1; SealedSecrets recommended |
| Image signing | cosign (sigstore) for release artifacts |
| Vulnerability scanning | Trivy in CI; fail on HIGH+ |
| SBOM | syft generates CycloneDX SBOMs per release |

## Build & release

| Concern | Choice |
|---|---|
| Build | `go build` + Docker multi-stage; `goreleaser` for tagged binaries |
| Versioning | SemVer; CalVer optional for chart |
| Release cadence | Weekly minor during pre-1.0; SemVer-strict at 1.0 |

---

## What we explicitly rejected

| Rejected | Reason |
|---|---|
| Java/Kotlin backend | User chose Go |
| Kafka | NATS JetStream is materially lighter ops for our throughput envelope (see ADR-0007) |
| Bazel for our own build | Go toolchain is sufficient; Bazel adds a week of setup we don't have |
| MongoDB / DynamoDB | Need relational integrity for runs/shards/tests; Postgres wins |
| Elasticsearch for logs | Loki is simpler and OSS-aligned; we'd revisit only at multi-PB scale |
| In-house feature store | Predictor in MVP is a heuristic; not needed |
| Rust for worker agent | Go is sufficient; Rust would slow us by weeks |
