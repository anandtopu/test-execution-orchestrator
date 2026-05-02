# ADR-0006: Karpenter + spot-aware scheduling for v1.0 worker pool

**Status:** Accepted — revised after timeline change from 1 → 3 months
**Date:** 2026-04-30 (revised)
**Supersedes:** earlier draft that deferred Karpenter to v1.5

## Context
PRD §6 (#5) called out spot-aware checkpointed scheduling via Karpenter as a key cost-reduction lever. The earlier 1-month-scope draft of this ADR deferred Karpenter to v1.5, which dropped PRD goal #3 (≥30% cost reduction) from v1.0. With the timeline now at 3 months and 3-4 engineers, we have the room to do this properly.

## Decision
For the v1.0 worker pool, use **Karpenter** with a `NodePool` that mixes **on-demand and spot** AWS EC2 instances. Workers handle Spot interruption signals from EC2 Instance Metadata Service (IMDS); in-flight tests checkpoint progress and the run manager reschedules them on a different node. See ADR-0020 for the interruption-handling design.

cluster-autoscaler is dropped from the chart defaults; operators who prefer it can disable Karpenter via `values.yaml` and run their own.

## Consequences

**+** PRD goal #3 (≥30% cost reduction) is back in scope.
**+** Karpenter's consolidation logic packs workers densely; we get better instance-type fit than cluster-autoscaler's static node groups.
**+** Karpenter is AWS-native, aligning with our v1.0 AWS-only posture.

**−** Karpenter adds a CRD set and an operator pod to the chart. We mitigate by bundling it as an optional subchart with sensible defaults.
**−** Spot-interruption handling needs explicit worker-side logic (IMDS poller, graceful drain, in-flight test checkpoint). This is real engineering, ~2-3 weeks one engineer. ADR-0020 covers it.
**−** Operators outside AWS (or on EKS without Karpenter prerequisites) need to disable the subchart. Documented.

## Alternatives considered
- **cluster-autoscaler**, deferring Karpenter to v1.5. Rejected: drops cost-reduction goal from v1.0; we have the time now.
- **Karpenter without spot interruption handling.** Rejected: all the cost gain comes from spot; without proper handling we lose tests on preemption.
- **Custom scheduler.** Rejected: out of scope; Karpenter is the standard.
