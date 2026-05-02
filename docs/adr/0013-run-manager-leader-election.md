# ADR-0013: Run Manager HA via leader election per Run

**Status:** Accepted
**Date:** 2026-04-30

## Context
The Run Manager drives a Run through its state machine. If two Run Manager replicas process the same Run concurrently, we get duplicate shard dispatch, conflicting state writes, and split-brain.

## Decision
Run Manager runs with N=2 replicas. Each Run has a `lease_holder_id` advisory lock in Postgres (via `pg_try_advisory_xact_lock(hash(run_id))`). A replica picks up a Run only if it can acquire the lock; the lock is held for the duration of the Run lifecycle and renewed on every state transition. Heartbeat: 5s; staleness threshold: 30s. If the lease holder dies, the second replica steals the lease and resumes from the persisted state.

## Consequences
**+** No split-brain; deterministic recovery.
**+** No external coordination service needed (Postgres already there).
**+** Failover < 10s in the typical case.
**−** All Runs converge to one replica if the other is briefly unavailable; we accept the unbalanced load given the workload.
**−** Replica autoscaling is constrained; we don't HPA Run Manager in v1.

## Alternatives considered
- **k8s Lease object.** Works but adds a dependency on the kube-apiserver for state we already keep in Postgres. Both work; we picked Postgres for atomicity with Run state writes.
- **Active/passive single replica.** Rejected: 5-minute MTTR on pod crash is unacceptable.
