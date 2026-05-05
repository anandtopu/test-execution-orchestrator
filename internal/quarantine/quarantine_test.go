package quarantine

import (
	"strings"
	"testing"
)

func TestBuildIssueBodyContainsKeyFacts(t *testing.T) {
	got := buildIssueBody("path/to/test.py", "test_does_a_thing", 0.073, 200)

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
	got := buildIssueBody("p", "n", 0.10, 50)
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
	got := buildIssueBody("p", "n", 0, 0)
	if strings.Contains(got, "NaN") {
		t.Errorf("body contains NaN: %s", got)
	}
}
