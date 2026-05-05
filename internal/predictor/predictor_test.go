package predictor

import (
	"context"
	"testing"

	"github.com/teo-dev/teo/internal/model"
)

func TestNewHeuristicSeedsRunnerDefaults(t *testing.T) {
	h := NewHeuristic(nil)
	if h.WindowDays != 30 {
		t.Errorf("WindowDays = %d, want 30", h.WindowDays)
	}
	for runner, want := range map[string]int{
		"pytest": 1200,
		"go":     500,
		"jest":   1500,
	} {
		if got := h.Defaults[runner]; got != want {
			t.Errorf("default[%s] = %d, want %d", runner, got, want)
		}
	}
}

func TestPredictNilReceiverErrors(t *testing.T) {
	var h *Heuristic
	if _, err := h.Predict(context.Background(), "owner/repo", []model.TestEntry{{Path: "p", Name: "n"}}); err == nil {
		t.Fatal("expected error from nil Heuristic")
	}
}

func TestPredictNilPoolErrors(t *testing.T) {
	h := &Heuristic{}
	_, err := h.Predict(context.Background(), "owner/repo", []model.TestEntry{{Path: "p", Name: "n"}})
	if err == nil {
		t.Fatal("expected error from nil pool")
	}
}

func TestColdStartUsesDefaultsAndMarksColdStart(t *testing.T) {
	h := NewHeuristic(nil)
	p := h.coldStart(model.TestEntry{Path: "pkg/a", Name: "TestX"})

	if p.Fingerprint != "pkg/a::TestX" {
		t.Errorf("Fingerprint = %q", p.Fingerprint)
	}
	if !p.IsColdStart {
		t.Error("IsColdStart should be true")
	}
	if p.P50DurationMS != 1200 {
		t.Errorf("P50 = %d, want 1200 (default)", p.P50DurationMS)
	}
	if p.P95DurationMS != 3600 {
		t.Errorf("P95 = %d, want 3 × 1200", p.P95DurationMS)
	}
	if p.FlakeProbability != 0 {
		t.Errorf("FlakeProbability = %v, want 0 for cold-start", p.FlakeProbability)
	}
}

func TestColdOnlyPreservesOrderAndCount(t *testing.T) {
	h := NewHeuristic(nil)
	tests := []model.TestEntry{
		{Path: "a", Name: "T1"},
		{Path: "b", Name: "T2"},
		{Path: "c", Name: "T3"},
	}
	got := h.coldOnly(tests)
	if len(got) != len(tests) {
		t.Fatalf("got %d predictions, want %d", len(got), len(tests))
	}
	for i, p := range got {
		want := tests[i].Path + "::" + tests[i].Name
		if p.Fingerprint != want {
			t.Errorf("predictions[%d] fingerprint = %q, want %q", i, p.Fingerprint, want)
		}
		if !p.IsColdStart {
			t.Errorf("predictions[%d] IsColdStart should be true", i)
		}
	}
}

func TestDefaultForKnownRunner(t *testing.T) {
	h := NewHeuristic(nil)
	if got := h.defaultFor("pytest"); got != 1200 {
		t.Errorf("defaultFor(pytest) = %d, want 1200", got)
	}
}

func TestDefaultForUnknownRunnerFallsBack(t *testing.T) {
	h := NewHeuristic(nil)
	if got := h.defaultFor("rspec"); got != 1200 {
		t.Errorf("defaultFor(rspec) fallback = %d, want 1200", got)
	}
}

func TestDefaultForUnknownRunnerWithEmptyMap(t *testing.T) {
	h := &Heuristic{}
	if got := h.defaultFor("anything"); got != 1200 {
		t.Errorf("defaultFor with no Defaults map = %d, want 1200", got)
	}
}
