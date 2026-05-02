# ADR-0020: Spot interruption handling and worker checkpointing

**Status:** Accepted
**Date:** 2026-04-30

## Context
ADR-0006 puts Karpenter + spot in scope for v1.0. The cost-reduction goal (PRD #3, ≥30%) only materializes if we **don't lose work on preemption**. AWS gives a 2-minute interruption notice via IMDS for Spot instances; we must use it.

## Decision

### Detection
The worker agent runs an IMDS poller (`http://169.254.169.254/latest/meta-data/spot/instance-action`, 5s interval). On a `200 OK` response (interruption scheduled), the worker enters **draining** mode:

1. Stops pulling new assignments.
2. For the in-flight assignment:
   - For each test that has **already started**: emits a `TestInterrupted` event with the current attempt's progress fingerprint (test name, started_at, partial OTel span, last log offset). The control plane marks it as `lost` for this attempt and **reschedules on a different node** with `attempt + 1`.
   - For tests in the same assignment **not yet started**: the entire remainder is rescheduled in a new shard.
3. Sends `ShardFinished` with `status=preempted` and exits cleanly.

### Reschedule policy
- Preempted tests are rescheduled on **on-demand** nodes by default (Karpenter `NodePool` configuration: a fallback pool with `karpenter.sh/capacity-type: on-demand`). Rationale: a test that was preempted once is more expensive to lose twice; on-demand bounds the failure cost.
- This is configurable; operators preferring max-savings can route preempted retries back to spot.

### No mid-test checkpointing in v1.0
We do **not** attempt to resume a test mid-flight after preemption. Test runners (pytest, go test, Jest) have no first-class resume primitive, and a per-runner mid-flight checkpoint would be brittle. Instead, a preempted test is **fully re-run** with `attempt + 1`. We accept the wasted partial work as the cost of doing business.

The PRD §5.1 talks about "checkpointing" for Spot resilience; we interpret this as **assignment-level checkpointing** (which tests have completed-and-reported survive), not test-level. This is sufficient for the cost-reduction target and is what teams actually need in practice.

### NodePool configuration
The Helm chart ships two Karpenter `NodePool` manifests:
- `teo-workers-spot` — `karpenter.sh/capacity-type: spot`, instance families `c`, `m`, `r`, sizes 2xlarge–8xlarge. Higher weight (preferred).
- `teo-workers-on-demand` — `karpenter.sh/capacity-type: on-demand`, same families/sizes. Lower weight (fallback).

Pod-level node-affinity routes preempted retries to the on-demand pool.

## Consequences

**+** Preemption causes at most one extra test run per affected test, not loss of the entire run.
**+** Operators get most of the spot cost savings (typically 60-90% off list) with bounded reliability impact.
**+** Logic is testable: we can simulate preemption in CI by killing a worker pod with `SIGTERM` after a short delay.

**−** Preempted-and-retried tests inflate the apparent test count and wall-clock for the run. We expose `preemption_count` per run in the UI so operators can see the true picture.
**−** Worker draining adds ~30s latency on preemption (IMDS detection + final reports). Acceptable within the 2-minute notice window.

## Alternatives considered
- **Mid-test checkpoint and resume.** Rejected: per-runner brittleness; not worth the engineering for v1.0.
- **No spot at all.** Rejected: defeats the cost-reduction goal.
- **Spot only, no on-demand fallback for retries.** Rejected: a test can be preempted twice in a row in worst-case Spot conditions, leading to runaway retries.
