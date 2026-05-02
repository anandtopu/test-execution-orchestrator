# ADR-0005: Scheduler implemented as a pure function

**Status:** Accepted
**Date:** 2026-04-30

## Context
Scheduling is the product's core wedge. We want it deterministic (replayable for the future "what-if" simulator), unit-testable without a worker fleet, and explainable to operators ("why did this test land in shard 7?").

## Decision
The Scheduler is a **pure function**:
```go
func Plan(tests []Test, predictions Predictions, fleet FleetSnapshot, constraints Constraints) AssignmentPlan
```
- No side effects, no I/O, no time/random unless injected.
- All state (fleet snapshot, predictions, constraints) is passed in explicitly.
- Output is a serializable `AssignmentPlan` (JSON), persisted with every run.

The Run Manager is the orchestrator that calls the scheduler and applies its output.

## Consequences
**+** Trivial to unit-test with table-driven cases over LPT correctness, exclusivity tags, and edge cases.
**+** Replayable: future "what-if" simulator is one CLI command (`teo replay <run_id> --workers=10 --type=spot`).
**+** Explainable: the saved plan is exactly the decision; we never lose "why."
**−** All fleet info must be snapshotted into the call. We accept the modest copying cost.
**−** Tie-breaking must be deterministic (sort by stable hash, not map iteration order).

## Alternatives considered
- **Stateful scheduler service** with internal queues and event loops. Rejected: harder to test, harder to explain, and we don't need its added flexibility for v1.
- **Embedded in the API gateway**. Rejected: scheduler benchmarks need to run as a library; embedding in HTTP makes that awkward.
