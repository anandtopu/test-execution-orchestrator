package api

import (
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestClassifyClusterCategory(t *testing.T) {
	cases := map[string]struct {
		msg  string
		want string
	}{
		"panic":            {"runtime panic: nil map", "panic"},
		"timeout":          {"context deadline exceeded after 30s", "timeout"},
		"timeout_keyword":  {"operation timeout", "timeout"},
		"network_refused":  {"dial tcp 127.0.0.1:5432: connection refused", "network"},
		"network_nosuch":   {"S3 NoSuchKey: object missing", "network"},
		"race":             {"WARNING: DATA RACE detected", "race"},
		"race_flag":        {"built with -race", "race"},
		"assertion":        {"expected 3 got 4", "assertion"},
		"empty_to_default": {"", "assertion"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			require.Equal(t, c.want, classifyClusterCategory(c.msg))
		})
	}
}

func TestClampFloat(t *testing.T) {
	require.Equal(t, 0.0, clampFloat(math.NaN(), 0, 1))
	require.Equal(t, 0.0, clampFloat(math.Inf(1), 0, 1))
	require.Equal(t, 0.0, clampFloat(math.Inf(-1), 0, 1))
	require.Equal(t, 0.0, clampFloat(-5, 0, 1))
	require.Equal(t, 1.0, clampFloat(5, 0, 1))
	require.Equal(t, 0.5, clampFloat(0.5, 0, 1))
	require.Equal(t, 9.0, clampFloat(math.NaN(), 9, 40))
}

func TestPearson(t *testing.T) {
	require.Equal(t, 0.0, pearson(nil, nil), "n<2 → 0")
	require.Equal(t, 0.0, pearson([]float64{1}, []float64{1}), "n<2 → 0")
	require.Equal(t, 0.0, pearson([]float64{1, 2}, []float64{1}), "mismatched len → 0")
	require.Equal(t, 0.0, pearson([]float64{2, 2, 2}, []float64{1, 2, 3}), "zero variance in x → 0")
	require.Equal(t, 0.0, pearson([]float64{1, 2, 3}, []float64{5, 5, 5}), "zero variance in y → 0")

	// Perfect positive correlation.
	require.InDelta(t, 1.0, pearson([]float64{1, 2, 3, 4}, []float64{2, 4, 6, 8}), 1e-9)
	// Perfect negative correlation.
	require.InDelta(t, -1.0, pearson([]float64{1, 2, 3, 4}, []float64{8, 6, 4, 2}), 1e-9)
}

func TestPercentile(t *testing.T) {
	require.Equal(t, 0.0, percentile(nil, 0.5), "empty → 0")
	s := []float64{1, 2, 3, 4, 5}
	require.Equal(t, 1.0, percentile(s, 0), "p<=0 → first")
	require.Equal(t, 1.0, percentile(s, -1), "p<=0 → first")
	require.Equal(t, 5.0, percentile(s, 1), "p>=1 → last")
	require.Equal(t, 5.0, percentile(s, 2), "p>=1 → last")
	// nearest-rank interior: ceil(0.5*5)-1 = 2 → s[2] = 3.
	require.Equal(t, 3.0, percentile(s, 0.5))
	// ceil(0.95*5)-1 = 4 → s[4] = 5.
	require.Equal(t, 5.0, percentile(s, 0.95))
}

func TestFlakeStatusBadge(t *testing.T) {
	require.Equal(t, "quarantined", flakeStatusBadge("quarantined"))
	require.Equal(t, "quarantined", flakeStatusBadge("QUARANTINED"))
	require.Equal(t, "broken", flakeStatusBadge("broken"))
	require.Equal(t, "flagged", flakeStatusBadge("active"))
	require.Equal(t, "flagged", flakeStatusBadge(""))
	require.Equal(t, "flagged", flakeStatusBadge(nil), "non-string → flagged")
}

func TestComputeClusterLayoutSingleRow(t *testing.T) {
	rows := []map[string]any{
		{"occurrences": int64(5), "last_seen": time.Now(), "representative_message": "panic"},
	}
	computeClusterLayout(rows)
	// Single row → span is 0, so x falls back to 0.5.
	require.Equal(t, 0.5, rows[0]["x"])
	require.Equal(t, "panic", rows[0]["category"])
	// y/r must be finite and in range.
	require.GreaterOrEqual(t, rows[0]["y"].(float64), 0.0)
	require.LessOrEqual(t, rows[0]["y"].(float64), 1.0)
	require.GreaterOrEqual(t, rows[0]["r"].(float64), 9.0)
	require.LessOrEqual(t, rows[0]["r"].(float64), 40.0)
}

func TestComputeClusterLayoutAllEqualLastSeen(t *testing.T) {
	now := time.Now()
	rows := []map[string]any{
		{"occurrences": int64(3), "last_seen": now, "representative_message": "a"},
		{"occurrences": int64(7), "last_seen": now, "representative_message": "b"},
	}
	computeClusterLayout(rows)
	// All-equal last_seen → span 0 → both x fall back to 0.5.
	require.Equal(t, 0.5, rows[0]["x"])
	require.Equal(t, 0.5, rows[1]["x"])
}

func TestComputeClusterLayoutNewestToLeft(t *testing.T) {
	old := time.Now().Add(-24 * time.Hour)
	recent := time.Now()
	rows := []map[string]any{
		{"occurrences": int64(1), "last_seen": recent, "representative_message": "newest"},
		{"occurrences": int64(1), "last_seen": old, "representative_message": "oldest"},
	}
	computeClusterLayout(rows)
	// Newest → left (x≈0), oldest → right (x≈1).
	require.InDelta(t, 0.0, rows[0]["x"].(float64), 1e-9)
	require.InDelta(t, 1.0, rows[1]["x"].(float64), 1e-9)
}

func TestComputeClusterLayoutEmpty(t *testing.T) {
	require.NotPanics(t, func() { computeClusterLayout(nil) })
}

// TestClusterCoordsDeterministic exercises the full layout contract on a known
// multi-row fixture: x/y in [0,1], r in [9,40], y monotonically smaller for
// larger occurrences (more occurrences sit higher up), and a stable, finite
// fallback for the single-row / all-equal case.
func TestClusterCoordsDeterministic(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	rows := []map[string]any{
		{"occurrences": int64(2), "last_seen": t0.Add(1 * time.Hour), "representative_message": "AssertionError"},
		{"occurrences": int64(50), "last_seen": t0.Add(2 * time.Hour), "representative_message": "panic: boom"},
		{"occurrences": int64(10), "last_seen": t0.Add(3 * time.Hour), "representative_message": "dial tcp: connection refused"},
	}
	computeClusterLayout(rows)

	for i, m := range rows {
		x := m["x"].(float64)
		y := m["y"].(float64)
		r := m["r"].(float64)
		require.False(t, math.IsNaN(x) || math.IsInf(x, 0), "row %d x not finite", i)
		require.False(t, math.IsNaN(y) || math.IsInf(y, 0), "row %d y not finite", i)
		require.False(t, math.IsNaN(r) || math.IsInf(r, 0), "row %d r not finite", i)
		require.GreaterOrEqual(t, x, 0.0)
		require.LessOrEqual(t, x, 1.0)
		require.GreaterOrEqual(t, y, 0.0)
		require.LessOrEqual(t, y, 1.0)
		require.GreaterOrEqual(t, r, 9.0)
		require.LessOrEqual(t, r, 40.0)
		require.NotEmpty(t, m["category"])
	}

	// y is monotonically smaller (higher up) as occurrences grow: row[1] (50) is
	// the highest, row[0] (2) is the lowest.
	yByOcc := map[int64]float64{}
	for _, m := range rows {
		yByOcc[m["occurrences"].(int64)] = m["y"].(float64)
	}
	require.Less(t, yByOcc[50], yByOcc[10], "more occurrences must sit higher (smaller y)")
	require.Less(t, yByOcc[10], yByOcc[2], "more occurrences must sit higher (smaller y)")

	// Single row / all-equal case: x falls back to 0.5 with no NaN/Inf.
	single := []map[string]any{
		{"occurrences": int64(4), "last_seen": t0, "representative_message": "x"},
	}
	computeClusterLayout(single)
	require.Equal(t, 0.5, single[0]["x"])
	require.False(t, math.IsNaN(single[0]["y"].(float64)) || math.IsInf(single[0]["y"].(float64), 0))
	require.False(t, math.IsNaN(single[0]["r"].(float64)) || math.IsInf(single[0]["r"].(float64), 0))
}

// TestSparklineEncoding pins the pure outcome→P/F/S mapping and the 20-char cap.
func TestSparklineEncoding(t *testing.T) {
	// passed→P, failed→F, skipped→S, errored→F (default branch).
	require.Equal(t, "PFSF", encodeSparkline([]string{"passed", "failed", "skipped", "errored"}))
	// Case-insensitive and timed_out → F.
	require.Equal(t, "PF", encodeSparkline([]string{"PASSED", "timed_out"}))
	require.Equal(t, "", encodeSparkline(nil))

	// Cap at 20, keeping the most-recent (trailing) outcomes.
	in := make([]string, 25)
	for i := range in {
		in[i] = "passed"
	}
	in[24] = "failed" // newest is a failure
	got := encodeSparkline(in)
	require.Len(t, got, 20)
	require.Equal(t, byte('F'), got[len(got)-1], "newest (trailing) outcome must be preserved")
	require.Equal(t, "PPPPPPPPPPPPPPPPPPPF", got)
}
