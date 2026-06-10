package quarantine

import (
	"fmt"
	"strings"
)

// maxHistoryPoints caps the number of run-history bars rendered in the Mermaid
// diagram. GitHub issue bodies are bounded (65536 chars) and a denser chart adds
// no diagnostic value; if the input is longer we keep the MOST RECENT N points.
const maxHistoryPoints = 30

// outcomeClass is the pass/fail/neutral bucket a raw test_executions.outcome
// maps to for charting purposes.
type outcomeClass int

const (
	// outcomeFail covers 'failed','errored','timed_out' and any unknown value.
	outcomeFail outcomeClass = iota
	// outcomePass is exclusively 'passed'.
	outcomePass
	// outcomeNeutral covers 'skipped'/'interrupted' (no pass/fail verdict).
	outcomeNeutral
)

// classifyOutcome classifies a raw test_executions.outcome into a
// pass/fail/neutral bucket. The test_executions CHECK constraint allows:
// 'passed','failed','skipped','errored','timed_out','interrupted'.
//
// Product decision (documented for reviewers): only 'passed' counts as a pass.
// 'failed','errored','timed_out' are real failures and map to fail. 'skipped'
// and 'interrupted' are non-results (the test never produced a verdict) and are
// classified as neutral so they can be filtered out of the strictly pass/fail
// bar series rather than being charted as a misleading fail.
func classifyOutcome(o string) outcomeClass {
	switch o {
	case "passed":
		return outcomePass
	case "skipped", "interrupted":
		return outcomeNeutral
	default: // failed, errored, timed_out, and any unknown value -> fail
		return outcomeFail
	}
}

// renderRunHistoryMermaid produces a GitHub-renderable Mermaid block visualizing
// the test's recent pass/fail run history as an xychart-beta bar chart.
//
// Input contract: outcomes are ordered OLDEST -> NEWEST. (The quarantine daemon
// fetches executions started_at DESC and reverses before calling.) The function
// is pure and side-effect free: deterministic output for a given input, no DB,
// no context, no logger.
//
// Behavior:
//   - Zero history (after filtering): returns a plain italic Markdown line, NOT a
//     fenced block, because Mermaid errors on an empty x-axis / empty bar series.
//   - 'skipped'/'interrupted' runs are filtered out of the bar series (they have
//     no pass/fail verdict); the count of filtered runs is noted in a caption.
//   - If more than maxHistoryPoints pass/fail runs remain after filtering, the
//     most recent maxHistoryPoints are charted (tail of the oldest->newest
//     slice). The caller (Daemon.recentOutcomes) over-fetches raw rows so this
//     pass/fail cap — not the DB row count — is what bounds the chart.
//
// xychart-beta is chosen over gitGraph because gitGraph commit IDs/labels have
// escaping pitfalls, whereas a numeric bar series is escaping-free and stable.
func renderRunHistoryMermaid(outcomes []string) string {
	// Classify and filter, preserving oldest->newest order.
	var bars []int // 1 = pass, 0 = fail
	filtered := 0
	for _, o := range outcomes {
		switch classifyOutcome(o) {
		case outcomeNeutral:
			filtered++
		case outcomePass:
			bars = append(bars, 1)
		default: // outcomeFail
			bars = append(bars, 0)
		}
	}

	if len(bars) == 0 {
		return "_No recent run history available._\n"
	}

	// Keep the most recent maxHistoryPoints (tail of the oldest->newest slice).
	if len(bars) > maxHistoryPoints {
		bars = bars[len(bars)-maxHistoryPoints:]
	}

	var b strings.Builder
	if filtered > 0 {
		fmt.Fprintf(&b, "_%d skipped/interrupted run(s) omitted from the chart below._\n\n", filtered)
	}
	b.WriteString("```mermaid\n")
	b.WriteString("xychart-beta\n")
	b.WriteString("    title \"Recent run outcomes (oldest -> newest)\"\n")

	// x-axis: 1..N indices.
	b.WriteString("    x-axis [")
	for i := range bars {
		if i > 0 {
			b.WriteString(", ")
		}
		fmt.Fprintf(&b, "%d", i+1)
	}
	b.WriteString("]\n")

	b.WriteString("    y-axis \"Pass=1 / Fail=0\" 0 --> 1\n")

	// bar series.
	b.WriteString("    bar [")
	for i, v := range bars {
		if i > 0 {
			b.WriteString(", ")
		}
		fmt.Fprintf(&b, "%d", v)
	}
	b.WriteString("]\n")

	b.WriteString("```\n")
	return b.String()
}
