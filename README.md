# Test Execution Orchestrator (TEO)

A self-hosted, OSS, AWS-targeted control plane that schedules tests across an elastic
worker pool, ingests results as OpenTelemetry traces, and statistically detects flaky
tests.

> **Status:** Pre-alpha. **9 of 16 epics done**, 6 scaffolded with named follow-ups.
> Build clean, 14 test packages green. See [`progress.md`](progress.md) for the live dashboard.
> See [`docs/`](docs/) for the architecture, roadmap, ADRs, and backlog.
> See [`PRD.md`](PRD.md) for product context.

## Quick links

- [**Implementation progress**](progress.md) — live status of every epic, story, and FR
- [Architecture overview](docs/architecture/overview.md)
- [Tech stack](docs/architecture/tech-stack.md)
- [Functional requirements](docs/requirements/functional.md)
- [Architecture Decision Records](docs/adr/)
- [Epics & backlog](docs/backlog/epics.md)
- [Definition of Done](docs/process/definition-of-done.md)
- [Changelog](CHANGELOG.md)

## Repository layout

```
.
├── cmd/                    Service entry points (one binary per directory)
│   ├── teo/                CLI (E-06)
│   ├── api/                API gateway (E-03)
│   ├── run-manager/        Run state machine (E-04)
│   ├── scheduler/          LPT scheduler (E-05)
│   ├── result-pipeline/    OTLP ingest + writers (E-07)
│   ├── predictor/          Go heuristic predictor (E-05)
│   └── worker/             Worker agent (E-06)
├── internal/               Private Go packages
│   └── version/            Build identity for every binary
├── pkg/                    Public-ish packages (e.g., adapter SPI)
├── proto/                  gRPC protobuf definitions (E-03)
├── migrations/             Postgres + ClickHouse migrations (E-02)
├── deploy/
│   └── helm/teo/           Reference Helm chart (E-11)
├── services/
│   └── predictor-ml/       Python LightGBM predictor (E-12)
├── docs/                   Architecture, requirements, ADRs, backlog, process
├── .github/workflows/      CI pipelines
├── Dockerfile              Multi-stage build for any Go service
├── Makefile                Build / test / lint / docker entry points
├── go.mod
└── README.md
```

## Prerequisites

| Tool | Version | Purpose |
|---|---|---|
| Go | 1.23+ | Backend services & CLI |
| Node.js | 22 LTS | Web UI (E-09) |
| Docker | 24+ | Image builds, integration tests via testcontainers |
| Helm | 3.14+ | Chart development (E-11) |
| `golangci-lint` | 1.60+ | Linting (`make lint`) |
| `go-licenses` | latest | License compliance (`make licenses`) |
| `goimports` | latest | Formatting (`make fmt`) |

`golangci-lint`, `goimports`, and `go-licenses` are not vendored. Install with:

```bash
brew install golangci-lint                     # macOS
# or: see https://golangci-lint.run/usage/install/

go install golang.org/x/tools/cmd/goimports@latest
go install github.com/google/go-licenses@latest
```

## Dev loop

```bash
# Compile every service into bin/
make build

# Run unit + short integration tests
make test

# Run linter
make lint

# Format
make fmt

# License compliance check (no AGPL / GPL transitively)
make licenses

# All-in-one
make all
```

Each binary prints its build identity when invoked without arguments:

```bash
$ ./bin/api
api dev (commit=unknown date=unknown go1.23.0 darwin/arm64)
```

## Module path note

The Go module path is `github.com/teo-dev/teo`. Replace it with your fork's path before
the first contribution; a single `find . -type f \( -name '*.go' -o -name 'go.mod' -o -name 'Makefile' -o -name 'Dockerfile' \) -exec sed -i ...` invocation handles it.

## Contributing

Read [`CONTRIBUTING.md`](CONTRIBUTING.md) and the [Definition of Done](docs/process/definition-of-done.md). PRs reference a story ID from [`docs/backlog/stories.md`](docs/backlog/stories.md).

## License

Apache License 2.0. See [`LICENSE`](LICENSE) and [`NOTICE`](NOTICE).
