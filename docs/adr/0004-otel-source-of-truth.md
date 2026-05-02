# ADR-0004: OpenTelemetry as the result source of truth

**Status:** Accepted
**Date:** 2026-04-30

## Context
The PRD §5.2 calls for "trace-native result aggregation": each test execution is a span; the suite is a trace. JUnit XML is the dominant industry format but is end-of-run, lossy, and per-shard. OTel is streaming, structured, and unifies test data with the same observability stack used for production.

## Decision
The canonical wire format from worker → control plane is **OTLP (OpenTelemetry over gRPC)**. Each test execution produces one span; logs and screenshots are span events or links. JUnit XML is supported as an **input adapter** (we ingest it from runners that don't natively emit OTLP) and as an **export format**. It is not the source of truth.

## Consequences
**+** Test result data lives next to (and can correlate with) prod observability data when operators wire it up.
**+** Span attributes give us cheap structured data for flake categorization heuristics.
**+** Streaming model: a worker dying does not lose its prior reported tests.
**−** OTel SDKs in test runners (especially pytest) are still maturing; we ship our own thin adapter to bridge.
**−** Trace volume is real; we mitigate with tail-sampling — 100% of failures, 1% of successes (NFR-PRIV-601 informs retention).

## Alternatives considered
- **JUnit XML as canonical**, OTel as a side channel. Rejected: locks us out of streaming, structured attributes, and production-style trace tooling.
- **Custom binary format**. Rejected: reinvents the wheel; loses ecosystem.
