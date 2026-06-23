package runmanager

import (
	"testing"

	"github.com/teo-dev/teo/internal/model"
)

// The publish path over a live NATS connection is covered by the integration
// test; here we pin the best-effort short-circuits so a missing/empty input can
// never panic or block the state machine.

func TestUINotifyObserverNilConnIsNoOp(t *testing.T) {
	o := &UINotifyObserver{Conn: nil}
	if err := o.OnRunStateChanged(t.Context(), RunSnapshot{ID: "r1", Status: model.RunRunning}, model.RunPending); err != nil {
		t.Fatalf("nil conn should be a no-op, got %v", err)
	}
}

func TestUINotifyObserverEmptyIDIsNoOp(t *testing.T) {
	o := &UINotifyObserver{}
	if err := o.OnRunStateChanged(t.Context(), RunSnapshot{}, model.RunPending); err != nil {
		t.Fatalf("empty id should be a no-op, got %v", err)
	}
}

func TestUINotifyObserverImplementsRunObserver(_ *testing.T) {
	var _ RunObserver = (*UINotifyObserver)(nil)
}
