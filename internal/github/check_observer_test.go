package github

import (
	"strings"
	"testing"

	"github.com/teo-dev/teo/internal/model"
)

func TestConclusionLabel(t *testing.T) {
	cases := map[model.RunStatus]string{
		model.RunSucceeded: "passed",
		model.RunFailed:    "failed",
		model.RunCancelled: "canceled",
		model.RunRunning:   "running", // passthrough for unknown
	}
	for in, want := range cases {
		if got := conclusionLabel(in); got != want {
			t.Errorf("conclusionLabel(%s) = %q, want %q", in, got, want)
		}
	}
}

func TestIsTerminal(t *testing.T) {
	for _, s := range []model.RunStatus{model.RunSucceeded, model.RunFailed, model.RunCancelled} {
		if !isTerminal(s) {
			t.Errorf("%s should be terminal", s)
		}
	}
	for _, s := range []model.RunStatus{model.RunPending, model.RunRunning, model.RunDispatching, model.RunFinalizing} {
		if isTerminal(s) {
			t.Errorf("%s should not be terminal", s)
		}
	}
}

func TestBuildClusterMarkdownEmpty(t *testing.T) {
	if got := buildClusterMarkdown(nil); got != "" {
		t.Errorf("empty clusters should produce empty markdown; got %q", got)
	}
}

func TestBuildClusterMarkdownIncludesAllClusters(t *testing.T) {
	clusters := []ClusterSummary{
		{Message: "AssertionError: boom", Stack: "File a.py line 1\nAssertionError: boom", Occurrences: 12},
		{Message: "ValueError: nope", Stack: "File b.py line 2\nValueError: nope", Occurrences: 7},
	}
	got := buildClusterMarkdown(clusters)
	for _, want := range []string{
		"### Top failure clusters",
		"#1",
		"#2",
		"12 occurrences",
		"7 occurrences",
		"AssertionError: boom",
		"ValueError: nope",
		"```",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("markdown missing %q\n%s", want, got)
		}
	}
}

func TestBuildClusterMarkdownTruncatesLongStacks(t *testing.T) {
	long := strings.Repeat("x", 2000)
	got := buildClusterMarkdown([]ClusterSummary{{Message: "boom", Stack: long, Occurrences: 1}})
	if !strings.Contains(got, "(truncated)") {
		t.Error("expected truncation marker")
	}
	if strings.Count(got, "x") > 1024 {
		t.Errorf("stack not truncated; %d x's present", strings.Count(got, "x"))
	}
}

func TestBuildClusterMarkdownHandlesEmptyMessage(t *testing.T) {
	got := buildClusterMarkdown([]ClusterSummary{{Message: "", Stack: "x", Occurrences: 1}})
	if !strings.Contains(got, "(no message)") {
		t.Errorf("expected fallback message; got %s", got)
	}
}
