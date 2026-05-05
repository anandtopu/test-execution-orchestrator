package audit

import (
	"context"
	"testing"
)

// The happy-path INSERT against teo.audit_log is exercised by the API
// integration tests under -tags=integration; here we cover the nil-safety
// guards that protect dry-run / test-harness call sites from panicking.

func TestLogNilLoggerIsNoOp(t *testing.T) {
	var l *Logger
	if err := l.Log(context.Background(), Entry{Action: "noop"}); err != nil {
		t.Fatalf("nil Logger should be no-op, got %v", err)
	}
}

func TestLogNilPoolIsNoOp(t *testing.T) {
	l := &Logger{Pool: nil}
	if err := l.Log(context.Background(), Entry{Action: "noop"}); err != nil {
		t.Fatalf("nil pool should be no-op, got %v", err)
	}
}

func TestLogNilPoolAcceptsRichEntryWithoutPanic(t *testing.T) {
	// Defensive: even with a populated Entry (including non-trivial Meta), the
	// nil-pool early return must fire before any marshaling or DB work.
	l := &Logger{Pool: nil}
	err := l.Log(context.Background(), Entry{
		Action:     "run.create",
		TargetType: "run",
		TargetID:   "r-123",
		Meta:       map[string]any{"reason": "manual", "actor": "ci"},
	})
	if err != nil {
		t.Fatalf("nil pool with rich entry should still no-op, got %v", err)
	}
}
