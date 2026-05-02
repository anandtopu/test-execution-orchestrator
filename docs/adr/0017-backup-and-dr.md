# ADR-0017: Backup and DR posture for v1

**Status:** Accepted
**Date:** 2026-04-30

## Context
We are self-hosted; operators own their backup story. But we ship sensible defaults and clear runbooks.

## Decision
Defaults bundled in the Helm chart:
- **Postgres**: CloudNativePG with continuous WAL archiving to S3. RPO ≤ 5 minutes. PITR enabled.
- **ClickHouse**: `clickhouse-backup` operator job, daily full backup to S3. RPO = 1 day. ClickHouse loss is recoverable from Postgres `test_executions` (rebuild MV) — degraded analytics during rebuild is acceptable.
- **S3 artifacts**: Versioning + lifecycle policy. Optional cross-region replication via operator config.
- **NATS JetStream**: 3-replica streams; loss requires 2+ pod failures simultaneously. No external backup (state is recoverable from Postgres + workers).

RTO target: ≤ 1 hour.

Operators are required to test restore procedures quarterly. The chart README has a "Restore drill" section with exact commands.

## Consequences
**+** Sensible defaults; nothing exotic.
**+** Postgres is the canonical source; ClickHouse is rebuildable. This is a deliberate design property.
**−** Restoring ClickHouse from Postgres is slow on large datasets. For operators with high analytical volume, we recommend ClickHouse replication (post-MVP).

## Alternatives considered
- **Velero for full-cluster backup.** Rejected: heavyweight, and Velero's behavior with stateful workloads is operator-specific. We give operators per-component tooling instead.
- **No defaults; operators figure it out.** Rejected: poor first-run experience.
