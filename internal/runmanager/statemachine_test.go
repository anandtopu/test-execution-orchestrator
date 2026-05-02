package runmanager

import (
	"testing"

	"github.com/teo-dev/teo/internal/model"
)

func TestHappyPath(t *testing.T) {
	path := []model.RunStatus{
		model.RunPending,
		model.RunPlanning,
		model.RunDispatching,
		model.RunRunning,
		model.RunFinalizing,
		model.RunSucceeded,
	}
	for i := 0; i < len(path)-1; i++ {
		if !CanTransition(path[i], path[i+1]) {
			t.Errorf("step %d: %s → %s should be legal", i, path[i], path[i+1])
		}
	}
}

func TestBadTransition(t *testing.T) {
	if CanTransition(model.RunSucceeded, model.RunRunning) {
		t.Error("succeeded→running must not be legal")
	}
	if CanTransition(model.RunPending, model.RunSucceeded) {
		t.Error("pending→succeeded must not be legal (skips planning)")
	}
}

func TestTerminalDetection(t *testing.T) {
	for _, s := range []model.RunStatus{model.RunSucceeded, model.RunFailed, model.RunCancelled} {
		if !IsTerminal(s) {
			t.Errorf("%s should be terminal", s)
		}
	}
	for _, s := range []model.RunStatus{model.RunPending, model.RunRunning, model.RunDispatching} {
		if IsTerminal(s) {
			t.Errorf("%s should not be terminal", s)
		}
	}
}
