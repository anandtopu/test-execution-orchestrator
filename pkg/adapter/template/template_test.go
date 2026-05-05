package template

import (
	"context"
	"testing"

	"github.com/teo-dev/teo/internal/model"
	"github.com/teo-dev/teo/pkg/adapter"
)

// These tests cover the SPI invariants every adapter must satisfy regardless
// of runner. Conformance items from docs/adapters/spi.md that don't depend on
// a real runner subprocess belong here; runner-specific parsing tests go in
// sibling _test.go files.

func TestNameNonEmpty(t *testing.T) {
	if got := New().Name(); got == "" {
		t.Fatal("Name() returned empty string")
	}
}

func TestExecuteEmptyReturnsNil(t *testing.T) {
	a := New()
	called := false
	err := a.Execute(context.Background(), t.TempDir(), nil, adapter.ExecOptions{},
		func(adapter.Result) { called = true })
	if err != nil {
		t.Fatalf("Execute(empty) returned error: %v", err)
	}
	if called {
		t.Fatal("onResult should not fire for empty test slice")
	}
}

func TestTranslateOutcomeUnknownIsErrored(t *testing.T) {
	if got := translateOutcome("not-a-real-status"); got != model.OutcomeErrored {
		t.Fatalf("unknown status mapped to %q, want %q", got, model.OutcomeErrored)
	}
}

func TestMergeEnvAppends(t *testing.T) {
	got := mergeEnv([]string{"A=1"}, map[string]string{"B": "2"})
	if len(got) != 2 || got[0] != "A=1" || got[1] != "B=2" {
		t.Fatalf("mergeEnv produced %v", got)
	}
}

func TestMergeEnvNoExtraReturnsBase(t *testing.T) {
	base := []string{"A=1"}
	got := mergeEnv(base, nil)
	if &got[0] != &base[0] {
		t.Fatal("mergeEnv with nil extras should return base unchanged")
	}
}
