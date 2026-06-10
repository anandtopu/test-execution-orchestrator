package quarantine

import (
	"strings"
	"testing"
)

func TestBuildIssueBodyContainsKeyFacts(t *testing.T) {
	got := buildIssueBody("path/to/test.py", "test_does_a_thing", 0.073, 200, []string{"passed", "failed", "passed"})

	for _, want := range []string{
		"path/to/test.py",
		"test_does_a_thing",
		"7.3%",   // 0.073 → 7.3%
		"200",    // sample size
		"Wilson", // explanation references the detector
	} {
		if !strings.Contains(got, want) {
			t.Errorf("body missing %q", want)
		}
	}
}

func TestBuildIssueBodyMarkdownStructure(t *testing.T) {
	got := buildIssueBody("p", "n", 0.10, 50, []string{"passed", "failed", "passed"})
	for _, want := range []string{
		"## Flaky test detected",
		"## What happened",
		"## Next steps",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("body missing markdown section %q", want)
		}
	}
}

func TestBuildIssueBodyZeroSamples(t *testing.T) {
	// Defensive: should not divide-by-zero or produce "NaN%" if called with 0.
	got := buildIssueBody("p", "n", 0, 0, nil)
	if strings.Contains(got, "NaN") {
		t.Errorf("body contains NaN: %s", got)
	}
}

func TestBuildIssueBodyRunHistorySection(t *testing.T) {
	// With real pass/fail outcomes the body must carry the run-history section
	// and a renderable Mermaid fence with the bar series.
	got := buildIssueBody("p", "n", 0.10, 50, []string{"passed", "failed", "passed"})
	for _, want := range []string{
		"## Recent run history",
		"```mermaid",
		"xychart-beta",
		"bar [1, 0, 1]",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("body missing %q\n---\n%s", want, got)
		}
	}
}

func TestBuildIssueBodyRunHistoryEmptyDegrades(t *testing.T) {
	// Zero history: the section header is still present but it degrades to the
	// plain italic line, NOT a Mermaid fence (Mermaid errors on an empty axis).
	got := buildIssueBody("p", "n", 0.10, 50, nil)
	if !strings.Contains(got, "## Recent run history") {
		t.Errorf("body missing run-history section header")
	}
	if !strings.Contains(got, "_No recent run history available._") {
		t.Errorf("empty history should degrade to the plain italic line")
	}
	if strings.Contains(got, "```mermaid") {
		t.Errorf("empty history must not emit a mermaid fence: %s", got)
	}
}
