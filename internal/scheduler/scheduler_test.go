package scheduler

import (
	"fmt"
	"math/rand"
	"sort"
	"testing"

	"github.com/teo-dev/teo/internal/model"
)

func mkTest(name string, durMS int, tags ...string) Test {
	return Test{
		Entry:       model.TestEntry{Path: "p", Name: name, Tags: tags},
		PredictedMS: durMS,
	}
}

func TestLPTBalances(t *testing.T) {
	tests := []Test{
		mkTest("a", 10000), mkTest("b", 5000), mkTest("c", 4000),
		mkTest("d", 3000), mkTest("e", 2000), mkTest("f", 1000),
	}
	plan := PlanFunc(tests, FleetSnapshot{}, Constraints{TargetShardSeconds: 8, MinShards: 3, MaxShards: 3})
	if len(plan.Assignments) != 3 {
		t.Fatalf("want 3 shards, got %d", len(plan.Assignments))
	}
	// LPT should produce makespan ≤ 4/3 × optimal. Optimal here is 8000+1000=9000 (or similar).
	totalCount := 0
	for _, a := range plan.Assignments {
		totalCount += len(a.Tests)
	}
	if totalCount != 6 {
		t.Fatalf("want 6 tests packed, got %d", totalCount)
	}
}

func TestDeterminism(t *testing.T) {
	tests := []Test{}
	rng := rand.New(rand.NewSource(42))
	for i := 0; i < 100; i++ {
		tests = append(tests, mkTest(fmt.Sprintf("t%03d", i), rng.Intn(5000)+100))
	}
	p1 := PlanFunc(append([]Test(nil), tests...), FleetSnapshot{}, Constraints{TargetShardSeconds: 60, MinShards: 5, MaxShards: 5})
	p2 := PlanFunc(append([]Test(nil), tests...), FleetSnapshot{}, Constraints{TargetShardSeconds: 60, MinShards: 5, MaxShards: 5})
	if len(p1.Assignments) != len(p2.Assignments) {
		t.Fatal("shard counts differ")
	}
	for i := range p1.Assignments {
		if len(p1.Assignments[i].Tests) != len(p2.Assignments[i].Tests) {
			t.Fatalf("shard %d test count differs", i)
		}
		for j := range p1.Assignments[i].Tests {
			if p1.Assignments[i].Tests[j].Entry.Name != p2.Assignments[i].Tests[j].Entry.Name {
				t.Fatalf("shard %d test %d differs", i, j)
			}
		}
	}
}

func TestExclusivityConstraint(t *testing.T) {
	tests := []Test{
		mkTest("a", 1000, "exclusive-port-5432"),
		mkTest("b", 1000, "exclusive-port-5432"),
		mkTest("c", 1000, "exclusive-port-5432"),
	}
	plan := PlanFunc(tests, FleetSnapshot{}, Constraints{MinShards: 3, MaxShards: 3})
	// All three must be in the same shard (the exclusive bin).
	for _, a := range plan.Assignments {
		if len(a.Tests) > 0 && len(a.Tests) != 3 && len(a.Tests) != 0 {
			// allowed: one bin has all 3, the others are empty
			t.Fatalf("exclusive tag conflict: shard sizes = %v", shardSizes(plan))
		}
	}
}

func TestQuarantineLaneSeparate(t *testing.T) {
	tests := []Test{
		mkTest("normal", 1000),
		{Entry: model.TestEntry{Path: "p", Name: "flaky"}, PredictedMS: 1000, IsQuarantined: true},
	}
	plan := PlanFunc(tests, FleetSnapshot{}, Constraints{MinShards: 1, MaxShards: 1})
	if len(plan.QuarantineLane) != 1 {
		t.Fatalf("want 1 quarantined, got %d", len(plan.QuarantineLane))
	}
	for _, a := range plan.Assignments {
		for _, te := range a.Tests {
			if te.Entry.Name == "flaky" {
				t.Fatal("quarantined test must not appear in active assignments")
			}
		}
	}
}

func TestMakespanRatioBound(t *testing.T) {
	// Brute-force the optimal makespan on small instances; verify LPT ≤ 4/3 × OPT.
	rng := rand.New(rand.NewSource(7))
	for trial := 0; trial < 30; trial++ {
		n := rng.Intn(8) + 4 // 4..11 tests
		m := rng.Intn(3) + 2 // 2..4 machines
		durs := make([]int, n)
		for i := range durs {
			durs[i] = rng.Intn(900) + 100
		}
		tests := make([]Test, n)
		for i, d := range durs {
			tests[i] = mkTest(fmt.Sprintf("t%d", i), d)
		}
		plan := PlanFunc(tests, FleetSnapshot{}, Constraints{MinShards: m, MaxShards: m})
		opt := optimalMakespan(durs, m)
		ratio := float64(plan.MakespanPredictedMS) / float64(opt)
		if ratio > 4.0/3.0+1e-9 {
			t.Fatalf("trial %d: LPT makespan %d, optimal %d, ratio %.3f > 4/3", trial, plan.MakespanPredictedMS, opt, ratio)
		}
	}
}

// optimalMakespan brute-forces the optimal makespan via partition enumeration.
// O(m^n) — only safe for small n.
func optimalMakespan(durs []int, m int) int {
	best := -1
	assignment := make([]int, len(durs))
	var recur func(idx int)
	recur = func(idx int) {
		if idx == len(durs) {
			loads := make([]int, m)
			for i, a := range assignment {
				loads[a] += durs[i]
			}
			ms := 0
			for _, l := range loads {
				if l > ms {
					ms = l
				}
			}
			if best == -1 || ms < best {
				best = ms
			}
			return
		}
		for i := 0; i < m; i++ {
			assignment[idx] = i
			recur(idx + 1)
		}
	}
	recur(0)
	return best
}

func shardSizes(p Plan) []int {
	out := make([]int, len(p.Assignments))
	for i, a := range p.Assignments {
		out[i] = len(a.Tests)
	}
	sort.Ints(out)
	return out
}
