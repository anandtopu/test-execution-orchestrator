# ADR-0003: Postgres + ClickHouse + S3 storage tiering

**Status:** Accepted
**Date:** 2026-04-30

## Context
TEO has two distinct workloads: transactional (runs, shards, tests, flake records — must be consistent, queried by ID, joined) and analytical (millions of span events and durations per day, scanned for dashboards and flake stats). Plus large blobs (logs, screenshots).

## Decision
Three tiers:
- **Postgres 16** (CloudNativePG) — OLTP. Transactional source of truth.
- **ClickHouse 24.x OSS** — OLAP. Span events, durations, flake observations time series.
- **S3-compatible** (MinIO bundled, or external S3) — blobs and cold archive.

ClickHouse is **rebuildable from Postgres** — losing it degrades analytics but does not lose runs.

## Consequences
**+** Each store is used in its sweet spot; no over-engineering of one to do the other.
**+** ClickHouse OSS is Apache 2.0, dependency policy compatible (per ADR-0018).
**+** ClickHouse aggregations for the run timeline and flake dashboard are sub-second at our target scale.
**−** Two database technologies to operate. We mitigate via bundled operators (CloudNativePG, ClickHouse Operator) so day-2 ops are k8s-native.
**−** Dual-write consistency: we accept eventual consistency between Postgres and ClickHouse via the result pipeline, with idempotent upserts on both sides.

## Alternatives considered
- **Postgres only**, using TimescaleDB or partitioned hypertables for span events. Rejected: ClickHouse is materially faster on the column-scan workloads we need.
- **TimescaleDB** specifically. Less performant than ClickHouse for our access patterns; license (TSL) is also incompatible with our Apache 2.0 OSS posture for some features.
- **DuckDB embedded** for analytics. No clustering/replication story.
