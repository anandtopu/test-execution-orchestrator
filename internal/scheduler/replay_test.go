package scheduler

import (
	"testing"

	"github.com/teo-dev/teo/internal/model"
)

// A plan produced by PlanFunc must replay to an identical plan — the whole point
// of FR-304: the scheduler is a deterministic pure function.
func TestReplayIsDeterministic(t *testing.T) {
	tests := []Test{
		mkTest("t1", 1200),
		mkTest("t2", 800),
		mkTest("t3", 600),
		mkTest("t4", 600), // tie → exercises the stable-hash tie-break
		mkTest("t5", 50),
		{Entry: model.TestEntry{Path: "p", Name: "flaky"}, PredictedMS: 300, IsQuarantined: true},
	}
	stored := PlanFunc(tests, FleetSnapshot{}, DefaultConstraints())

	recomputed, ok := Replay(stored, DefaultConstraints())
	if !ok {
		t.Fatal("replay of a freshly computed plan must be deterministic")
	}
	if len(stored.Assignments) != len(recomputed.Assignments) {
		t.Fatalf("shard count: stored %d, recomputed %d", len(stored.Assignments), len(recomputed.Assignments))
	}
	if stored.MakespanPredictedMS != recomputed.MakespanPredictedMS {
		t.Fatalf("makespan: stored %d, recomputed %d", stored.MakespanPredictedMS, recomputed.MakespanPredictedMS)
	}
	if len(stored.QuarantineLane) != len(recomputed.QuarantineLane) {
		t.Fatalf("quarantine lane: stored %d, recomputed %d", len(stored.QuarantineLane), len(recomputed.QuarantineLane))
	}
}

// Exclusivity tags must survive the round-trip — they live on Entry.Tags and
// steer bin placement, so a replay that dropped them would diverge.
func TestReplayPreservesExclusivity(t *testing.T) {
	tests := []Test{
		mkTest("t1", 500, "exclusive-port-5432"),
		mkTest("t2", 500, "exclusive-port-5432"),
		mkTest("t3", 500),
		mkTest("t4", 500),
	}
	stored := PlanFunc(tests, FleetSnapshot{}, DefaultConstraints())
	if _, ok := Replay(stored, DefaultConstraints()); !ok {
		t.Fatal("exclusivity-tagged plan must replay deterministically")
	}
}

// Replaying with different constraints than produced the plan can legitimately
// diverge — Replay must report that rather than silently passing.
func TestReplayDetectsConstraintDrift(t *testing.T) {
	// ~1,000,000ms of work → DefaultConstraints (300s target) picks ~4 shards.
	tests := make([]Test, 0, 10)
	for i := range 10 {
		tests = append(tests, mkTest("t"+string(rune('a'+i)), 100_000))
	}
	stored := PlanFunc(tests, FleetSnapshot{}, DefaultConstraints())
	if len(stored.Assignments) < 2 {
		t.Fatalf("precondition: expected the default plan to use >1 shard, got %d", len(stored.Assignments))
	}
	// Force a single shard: fewer than the default plan chose.
	if _, ok := Replay(stored, Constraints{TargetShardSeconds: 1_000_000, MinShards: 1, MaxShards: 1}); ok {
		t.Fatal("a different shard count must be reported as non-deterministic")
	}
}

// An empty plan (no tests) replays cleanly.
func TestReplayEmptyPlan(t *testing.T) {
	stored := PlanFunc(nil, FleetSnapshot{}, DefaultConstraints())
	if _, ok := Replay(stored, DefaultConstraints()); !ok {
		t.Fatal("empty plan must replay deterministically")
	}
}
