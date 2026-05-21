// Package scheduler implements TEO's LPT (Longest-Processing-Time-first)
// bin-packing scheduler as a pure function. Per ADR-0005:
//   - no I/O, no time, no random unless injected
//   - inputs/outputs are serializable; the plan is replayable
//   - tie-breaking is by stable hash so determinism holds across runs
package scheduler

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"sort"

	"github.com/teo-dev/teo/internal/model"
)

// Test bundles a manifest entry with its prediction.
type Test struct {
	Entry         model.TestEntry
	PredictedMS   int
	IsColdStart   bool
	FlakeProb     float32
	IsQuarantined bool
}

// FleetSnapshot describes available capacity at planning time.
type FleetSnapshot struct {
	MaxShards  int      // hard cap
	WorkerTags []string // capability tags advertised by workers
}

// Constraints is config knobs the operator can turn.
type Constraints struct {
	TargetShardSeconds int // ideal per-shard wall-clock; if 0, defaults to 5 minutes
	MinShards          int
	MaxShards          int
}

// Assignment is one shard's payload.
type Assignment struct {
	ShardIndex          int
	PredictedDurationMS int
	Tests               []Test
}

// Plan is the result of scheduling.
type Plan struct {
	Assignments         []Assignment
	QuarantineLane      []Test // run non-blocking
	TotalPredictedMS    int
	MakespanPredictedMS int    // longest assignment's duration
	Version             string // plan format version, useful for replay
}

const planVersion = "lpt-v1"

// DefaultConstraints returns the planning knobs the Run Manager uses for every
// run. Both the live planning path (internal/runmanager) and the offline
// `teo replay` determinism check call this, so the two can never drift: a replay
// is only meaningful if it re-runs the scheduler with the exact constraints that
// produced the stored plan.
func DefaultConstraints() Constraints {
	return Constraints{
		TargetShardSeconds: 300,
		MinShards:          1,
		MaxShards:          50,
	}
}

// Replay reconstructs the scheduler inputs from a previously computed plan and
// re-runs PlanFunc with the given constraints, returning the recomputed plan and
// whether it is byte-for-byte identical to stored (i.e. the scheduler is still
// deterministic for this input). It backs `teo replay <run_id>` (FR-304, S-05-04).
//
// The reconstruction is faithful because PlanFunc's output depends only on each
// test's Entry (path/name/params/tags), PredictedMS, and IsQuarantined — all of
// which survive a round-trip through the stored Plan. Input ordering does not
// matter: PlanFunc sorts internally with a stable hash tie-break. The 50ms floor
// is idempotent because the stored durations were already floored when first
// planned.
func Replay(stored Plan, c Constraints) (recomputed Plan, deterministic bool) {
	tests := make([]Test, 0, len(stored.QuarantineLane))
	for _, a := range stored.Assignments {
		for _, t := range a.Tests {
			t.IsQuarantined = false
			tests = append(tests, t)
		}
	}
	for _, t := range stored.QuarantineLane {
		t.IsQuarantined = true
		tests = append(tests, t)
	}
	recomputed = PlanFunc(tests, FleetSnapshot{}, c)
	return recomputed, plansEqual(stored, recomputed)
}

// plansEqual compares two plans by their canonical JSON encoding. The scheduler
// guarantees a deterministic shape (sorted assignments, stable tie-breaks), so
// equal inputs must round-trip to equal JSON.
func plansEqual(a, b Plan) bool {
	aj, err1 := json.Marshal(a)
	bj, err2 := json.Marshal(b)
	if err1 != nil || err2 != nil {
		return false
	}
	return bytes.Equal(aj, bj)
}

// Plan partitions tests across shards using the LPT heuristic, ordered longest-first
// within each shard. Quarantined tests are routed to a separate non-blocking lane.
//
// LPT correctness: for the makespan objective, LPT yields a (4/3 - 1/(3m))-approximation
// of the optimal — see "An Application of Bin-Packing to Multiprocessor Scheduling",
// Graham 1969. We assume non-zero positive durations (predictor enforces minimum 50ms).
func PlanFunc(tests []Test, _ FleetSnapshot, c Constraints) Plan {
	if c.TargetShardSeconds <= 0 {
		c.TargetShardSeconds = 300
	}
	if c.MinShards < 1 {
		c.MinShards = 1
	}
	if c.MaxShards < c.MinShards {
		c.MaxShards = max(c.MinShards, 50)
	}

	// 1. Partition active vs quarantined.
	active := make([]Test, 0, len(tests))
	quar := make([]Test, 0)
	for _, t := range tests {
		if t.IsQuarantined {
			quar = append(quar, t)
		} else {
			active = append(active, t)
		}
	}

	// 2. Compute total work, then choose shard count to hit the target wall-clock.
	// The 50ms floor protects bin-packing from a predictor that returns 0 or
	// negative durations; mutate the slice (not a value copy) so step 4 sees it.
	totalMS := 0
	for i := range active {
		if active[i].PredictedMS < 50 {
			active[i].PredictedMS = 50
		}
		totalMS += active[i].PredictedMS
	}
	// avoid divide-by-zero
	target := c.TargetShardSeconds * 1000
	desired := 1
	if totalMS > 0 && target > 0 {
		desired = (totalMS + target - 1) / target
	}
	shardCount := desired
	shardCount = max(shardCount, c.MinShards)
	shardCount = min(shardCount, c.MaxShards)
	if shardCount > len(active) && len(active) > 0 {
		shardCount = len(active)
	}
	if shardCount == 0 {
		shardCount = 1
	}

	// 3. LPT: sort tests descending by predicted duration. Tie-break with stable hash
	//    so equal-duration tests sort deterministically across runs.
	sort.SliceStable(active, func(i, j int) bool {
		if active[i].PredictedMS != active[j].PredictedMS {
			return active[i].PredictedMS > active[j].PredictedMS
		}
		return stableHash(active[i].Entry) < stableHash(active[j].Entry)
	})

	// Initialize empty bins.
	bins := make([]Assignment, shardCount)
	for i := range bins {
		bins[i].ShardIndex = i
		bins[i].Tests = make([]Test, 0)
	}

	// 4. Assign each test to the bin with the smallest current predicted duration,
	//    respecting exclusivity tags.
	exclusiveBin := map[string]int{} // exclusive tag → bin index (sticky)

	for _, t := range active {
		best := -1
		bestDur := -1
		exclusiveTag := pickExclusiveTag(t.Entry.Tags)

		for i := range bins {
			// Exclusive tag conflict check
			if exclusiveTag != "" {
				if owner, ok := exclusiveBin[exclusiveTag]; ok && owner != i {
					continue
				}
			}
			d := bins[i].PredictedDurationMS
			if best == -1 || d < bestDur {
				best = i
				bestDur = d
			}
		}
		if best == -1 {
			best = 0 // safety fallback (shouldn't happen)
		}
		bins[best].Tests = append(bins[best].Tests, t)
		bins[best].PredictedDurationMS += t.PredictedMS
		if exclusiveTag != "" {
			exclusiveBin[exclusiveTag] = best
		}
	}

	// 5. Within each bin, order by descending predicted duration (critical-path-first).
	for i := range bins {
		sort.SliceStable(bins[i].Tests, func(a, b int) bool {
			return bins[i].Tests[a].PredictedMS > bins[i].Tests[b].PredictedMS
		})
	}

	// 6. Compute makespan.
	makespan := 0
	for _, b := range bins {
		if b.PredictedDurationMS > makespan {
			makespan = b.PredictedDurationMS
		}
	}

	// Drop empty bins from the tail.
	for len(bins) > 1 && len(bins[len(bins)-1].Tests) == 0 {
		bins = bins[:len(bins)-1]
	}

	return Plan{
		Assignments:         bins,
		QuarantineLane:      quar,
		TotalPredictedMS:    totalMS,
		MakespanPredictedMS: makespan,
		Version:             planVersion,
	}
}

// pickExclusiveTag returns the first tag prefixed with "exclusive-", or "".
func pickExclusiveTag(tags []string) string {
	for _, t := range tags {
		if len(t) >= 10 && t[:10] == "exclusive-" {
			return t
		}
	}
	return ""
}

func stableHash(e model.TestEntry) uint64 {
	h := sha256.New()
	h.Write([]byte(e.Path))
	h.Write([]byte{0})
	h.Write([]byte(e.Name))
	h.Write([]byte{0})
	h.Write([]byte(e.ParamsHash))
	sum := h.Sum(nil)
	return binary.BigEndian.Uint64(sum[:8])
}
