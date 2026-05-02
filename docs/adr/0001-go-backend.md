# ADR-0001: Go for backend services and CLI

**Status:** Accepted
**Date:** 2026-04-30

## Context
We need a single language for the API gateway, run manager, scheduler, result pipeline, worker agent, and CLI. The team chose Go (per intake question #3).

## Decision
Use **Go 1.23** for all backend services and the CLI.

## Consequences
**+** Single static binary per service simplifies the Helm chart and the CLI distribution.
**+** Native concurrency model fits the worker/orchestration domain.
**+** Strong gRPC ecosystem; the Kubernetes/CNCF stack is Go-native, easing integration with Karpenter, NATS, ClickHouse driver.
**+** Low memory footprint suits self-hosted operators with smaller clusters.
**−** No first-class ML libraries — the Predictor service stays heuristic in MVP and switches to Python at v1.5 when GBT is introduced.
**−** Generics are still relatively new; we accept some boilerplate over deep generic abstractions.

## Alternatives considered
- **Java/Kotlin** — heavier runtime, slower iteration on small services. Rejected.
- **Rust** — would slow delivery materially. Rejected.
- **TypeScript/Node** — fine for the UI, but the worker concurrency story is weaker. Rejected for backend.
