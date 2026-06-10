package quarantine

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestClassifyOutcome(t *testing.T) {
	cases := []struct {
		in   string
		want outcomeClass
	}{
		{"passed", outcomePass},
		{"failed", outcomeFail},
		{"errored", outcomeFail},
		{"timed_out", outcomeFail},
		{"skipped", outcomeNeutral},
		{"interrupted", outcomeNeutral},
		{"totally-unknown-value", outcomeFail}, // default case -> fail
		{"", outcomeFail},
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			require.Equal(t, c.want, classifyOutcome(c.in))
		})
	}
}

func TestRenderRunHistoryMermaid_EmptyAndAllNeutral(t *testing.T) {
	const emptyLine = "_No recent run history available._\n"

	t.Run("nil input", func(t *testing.T) {
		got := renderRunHistoryMermaid(nil)
		require.Equal(t, emptyLine, got)
		require.NotContains(t, got, "```mermaid")
	})

	t.Run("empty slice", func(t *testing.T) {
		got := renderRunHistoryMermaid([]string{})
		require.Equal(t, emptyLine, got)
		require.NotContains(t, got, "```mermaid")
	})

	t.Run("all neutral filters to empty", func(t *testing.T) {
		// Even though there are inputs, after neutral filtering nothing remains,
		// so we must return the plain italic line and NOT a fenced block.
		got := renderRunHistoryMermaid([]string{"skipped", "interrupted", "skipped"})
		require.Equal(t, emptyLine, got)
		require.NotContains(t, got, "```mermaid")
		// No misleading caption either, since there are zero charted bars.
		require.NotContains(t, got, "omitted")
	})
}

func TestRenderRunHistoryMermaid_Mixed(t *testing.T) {
	// oldest -> newest: pass, fail, skipped, pass
	got := renderRunHistoryMermaid([]string{"passed", "failed", "skipped", "passed"})

	require.Contains(t, got, "```mermaid")
	require.Contains(t, got, "xychart-beta")
	// Three charted runs (the skipped one is filtered): bars 1,0,1.
	require.Contains(t, got, "bar [1, 0, 1]")
	// x-axis is 1-based, oldest->newest, one entry per charted bar.
	require.Contains(t, got, "x-axis [1, 2, 3]")
	// Exactly one skipped/interrupted omitted, with correct pluralized caption.
	require.Contains(t, got, "_1 skipped/interrupted run(s) omitted from the chart below._")
}

func TestRenderRunHistoryMermaid_AllPassAllFail(t *testing.T) {
	t.Run("all pass", func(t *testing.T) {
		got := renderRunHistoryMermaid([]string{"passed", "passed", "passed"})
		require.Contains(t, got, "bar [1, 1, 1]")
		require.Contains(t, got, "x-axis [1, 2, 3]")
		// No neutrals, so no omission caption.
		require.NotContains(t, got, "omitted")
	})
	t.Run("all fail (mixed failure verdicts)", func(t *testing.T) {
		got := renderRunHistoryMermaid([]string{"failed", "errored", "timed_out"})
		require.Contains(t, got, "bar [0, 0, 0]")
		require.NotContains(t, got, "omitted")
	})
}

func TestRenderRunHistoryMermaid_CaptionCount(t *testing.T) {
	// Two neutrals among the inputs -> caption count must read 2.
	got := renderRunHistoryMermaid([]string{"skipped", "passed", "interrupted", "failed"})
	require.Contains(t, got, "_2 skipped/interrupted run(s) omitted from the chart below._")
	require.Contains(t, got, "bar [1, 0]")
}

func TestRenderRunHistoryMermaid_TruncationKeepsTail(t *testing.T) {
	// Build 35 pass/fail runs where the OLDEST 5 are passes and the rest fails,
	// so we can detect whether the tail (most recent) was kept.
	in := make([]string, 0, 35)
	for i := 0; i < 5; i++ {
		in = append(in, "passed") // oldest 5
	}
	for i := 0; i < 30; i++ {
		in = append(in, "failed") // newest 30
	}
	got := renderRunHistoryMermaid(in)

	// Extract the bar series and assert exactly maxHistoryPoints bars, all the
	// most-recent (failed) ones — none of the oldest passes survive.
	require.Contains(t, got, "```mermaid")
	bars := extractBarSeries(t, got)
	require.Len(t, bars, maxHistoryPoints, "must cap to maxHistoryPoints bars")
	for i, v := range bars {
		require.Equalf(t, 0, v, "bar %d should be a recent fail, oldest passes must be dropped", i)
	}
	// x-axis must also be capped to maxHistoryPoints entries, 1-based.
	require.Contains(t, got, "x-axis [1, 2,")
	require.NotContains(t, got, "x-axis [1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20, 21, 22, 23, 24, 25, 26, 27, 28, 29, 30, 31")
}

func TestRenderRunHistoryMermaid_MixedNoNeutral(t *testing.T) {
	// Spec fixture: oldest -> newest pass,fail,pass with no neutrals proves the
	// ordering is preserved (not reversed) and no omission caption is emitted.
	got := renderRunHistoryMermaid([]string{"passed", "failed", "passed"})
	require.Contains(t, got, "```mermaid")
	require.Contains(t, got, "xychart-beta")
	require.Contains(t, got, "bar [1, 0, 1]")
	require.NotContains(t, got, "omitted")
	require.NotContains(t, got, "NaN")
}

func TestRenderRunHistoryMermaid_NoNaNNoEmptyBars(t *testing.T) {
	// A skipped/interrupted mix must never produce NaN or an empty `bar []`.
	got := renderRunHistoryMermaid([]string{"passed", "skipped", "interrupted", "failed"})
	require.NotContains(t, got, "NaN")
	require.NotContains(t, got, "bar []")
	require.Contains(t, got, "bar [1, 0]")
}

func TestRenderRunHistoryMermaid_Truncation50KeepsRecent30(t *testing.T) {
	// 50 pass/fail outcomes: oldest 20 pass, newest 30 fail. After capping to
	// maxHistoryPoints (30) only the most-recent 30 (all fails) survive.
	in := make([]string, 0, 50)
	for i := 0; i < 20; i++ {
		in = append(in, "passed")
	}
	for i := 0; i < 30; i++ {
		in = append(in, "failed")
	}
	got := renderRunHistoryMermaid(in)
	bars := extractBarSeries(t, got)
	require.Len(t, bars, maxHistoryPoints)
	for i, v := range bars {
		require.Equalf(t, 0, v, "bar %d must be a recent fail; oldest passes dropped", i)
	}
}

func TestRenderRunHistoryMermaid_Deterministic(t *testing.T) {
	in := []string{"passed", "failed", "skipped", "passed", "errored"}
	first := renderRunHistoryMermaid(in)
	for i := 0; i < 5; i++ {
		require.Equal(t, first, renderRunHistoryMermaid(in), "output must be deterministic")
	}
}

func TestRenderRunHistoryMermaid_Golden(t *testing.T) {
	// Byte-exact golden on a small fixed input (pass,fail,pass) — locks the full
	// rendered block so any accidental format drift is caught.
	got := renderRunHistoryMermaid([]string{"passed", "failed", "passed"})
	const want = "```mermaid\n" +
		"xychart-beta\n" +
		"    title \"Recent run outcomes (oldest -> newest)\"\n" +
		"    x-axis [1, 2, 3]\n" +
		"    y-axis \"Pass=1 / Fail=0\" 0 --> 1\n" +
		"    bar [1, 0, 1]\n" +
		"```\n"
	require.Equal(t, want, got)
}

// extractBarSeries parses the integer values out of the `bar [..]` line of a
// rendered Mermaid block.
func extractBarSeries(t *testing.T, body string) []int {
	t.Helper()
	const marker = "bar ["
	i := strings.Index(body, marker)
	require.GreaterOrEqual(t, i, 0, "no bar series found in body")
	rest := body[i+len(marker):]
	end := strings.Index(rest, "]")
	require.GreaterOrEqual(t, end, 0)
	inner := strings.TrimSpace(rest[:end])
	if inner == "" {
		return nil
	}
	var out []int
	for _, part := range strings.Split(inner, ",") {
		switch strings.TrimSpace(part) {
		case "1":
			out = append(out, 1)
		case "0":
			out = append(out, 0)
		default:
			t.Fatalf("unexpected bar value %q", part)
		}
	}
	return out
}
