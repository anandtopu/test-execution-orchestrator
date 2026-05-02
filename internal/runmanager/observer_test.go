package runmanager

import (
	"context"
	"errors"
	"testing"

	"github.com/teo-dev/teo/internal/model"
)

func TestRunObserverFuncImplementsInterface(t *testing.T) {
	called := false
	var obs RunObserver = RunObserverFunc(func(_ context.Context, snap RunSnapshot, prev model.RunStatus) error {
		called = true
		if snap.ID != "r-1" {
			t.Errorf("snap.ID = %q, want r-1", snap.ID)
		}
		if prev != model.RunPending {
			t.Errorf("prev = %s, want pending", prev)
		}
		return nil
	})
	err := obs.OnRunStateChanged(context.Background(),
		RunSnapshot{ID: "r-1", Status: model.RunPlanning}, model.RunPending)
	if err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("observer never invoked")
	}
}

// recordingObserver captures every invocation for assertion.
type recordingObserver struct {
	calls []recordedCall
	err   error
}
type recordedCall struct {
	snap RunSnapshot
	prev model.RunStatus
}

func (r *recordingObserver) OnRunStateChanged(_ context.Context, snap RunSnapshot, prev model.RunStatus) error {
	r.calls = append(r.calls, recordedCall{snap: snap, prev: prev})
	return r.err
}

func TestObserverErrorDoesNotPanic(t *testing.T) {
	r := &recordingObserver{err: errors.New("boom")}
	// Calling OnRunStateChanged directly should propagate the error to the
	// caller; the Manager itself absorbs and logs it (manager_test exercises
	// that path with a real DB; we only verify the contract here).
	if err := r.OnRunStateChanged(context.Background(), RunSnapshot{ID: "x"}, model.RunPending); err == nil {
		t.Fatal("expected boom")
	}
	if len(r.calls) != 1 {
		t.Errorf("recorded %d calls, want 1", len(r.calls))
	}
}
