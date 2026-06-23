package api

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

// newTestHub builds a Postgres-free hub with an injected snapshot reader and a
// long safety interval so reads are driven only by explicit pokes (deterministic
// — no ticker races).
func newTestHub(snap runSnapshotFn) *Hub {
	return &Hub{
		runs:     make(map[string]*runFanout),
		snapshot: snap,
		baseCtx:  context.Background(),
		safety:   time.Hour,
	}
}

func recvWithin(t *testing.T, ch chan any, d time.Duration) (any, bool) {
	t.Helper()
	select {
	case v, ok := <-ch:
		return v, ok
	case <-time.After(d):
		t.Fatalf("timed out waiting for value after %s", d)
		return nil, false
	}
}

func runningSnap(id string) map[string]any {
	return map[string]any{"id": id, "status": "running"}
}

func TestHubSubscribeEmitsInitialSnapshot(t *testing.T) {
	hub := newTestHub(func(_ context.Context, id string) (map[string]any, error) {
		return runningSnap(id), nil
	})
	ch, err := hub.Subscribe(t.Context(), "run-1")
	if err != nil {
		t.Fatal(err)
	}
	v, ok := recvWithin(t, ch, time.Second)
	if !ok {
		t.Fatal("channel closed before initial snapshot")
	}
	m := v.(map[string]any)
	if m["id"] != "run-1" {
		t.Fatalf("got id %v, want run-1", m["id"])
	}
}

func TestHubFanoutSharesOneReadAcrossSubscribers(t *testing.T) {
	var reads int64
	hub := newTestHub(func(_ context.Context, id string) (map[string]any, error) {
		atomic.AddInt64(&reads, 1)
		return runningSnap(id), nil
	})
	ctx := t.Context()

	const n = 3
	chans := make([]chan any, n)
	for i := range chans {
		ch, err := hub.Subscribe(ctx, "run-1")
		if err != nil {
			t.Fatal(err)
		}
		chans[i] = ch
		recvWithin(t, ch, time.Second) // drain the per-subscriber initial snapshot
	}
	if got := atomic.LoadInt64(&reads); got != n {
		t.Fatalf("after %d subscribes: %d reads, want %d (one initial each)", n, got, n)
	}

	hub.poke("run-1")
	for i, ch := range chans {
		v, ok := recvWithin(t, ch, time.Second)
		if !ok {
			t.Fatalf("subscriber %d channel closed", i)
		}
		if v.(map[string]any)["id"] != "run-1" {
			t.Fatalf("subscriber %d got wrong snapshot", i)
		}
	}
	// The single poke must collapse to ONE shared read fanned to all n.
	if got := atomic.LoadInt64(&reads); got != n+1 {
		t.Fatalf("after poke: %d reads, want %d (one shared fanout read)", got, n+1)
	}
}

func TestHubTerminalStatusClosesChannel(t *testing.T) {
	var status atomic.Value
	status.Store("running")
	hub := newTestHub(func(_ context.Context, id string) (map[string]any, error) {
		return map[string]any{"id": id, "status": status.Load().(string)}, nil
	})
	ch, err := hub.Subscribe(t.Context(), "run-1")
	if err != nil {
		t.Fatal(err)
	}
	recvWithin(t, ch, time.Second) // initial (running)

	status.Store("succeeded")
	hub.poke("run-1")

	// Drain until the channel is closed (the terminal snapshot may arrive first).
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		v, ok := recvWithin(t, ch, time.Second)
		if !ok {
			// closed → subscription completed on terminal status
			hub.mu.Lock()
			_, present := hub.runs["run-1"]
			hub.mu.Unlock()
			if present {
				t.Fatal("run still registered after terminal close")
			}
			return
		}
		if v.(map[string]any)["status"] == "succeeded" {
			continue // terminal snapshot delivered; next recv should be the close
		}
	}
	t.Fatal("channel never closed after terminal status")
}

func TestHubUnsubscribeOnContextCancel(t *testing.T) {
	hub := newTestHub(func(_ context.Context, id string) (map[string]any, error) {
		return runningSnap(id), nil
	})
	ctx, cancel := context.WithCancel(context.Background())
	ch, err := hub.Subscribe(ctx, "run-1")
	if err != nil {
		t.Fatal(err)
	}
	recvWithin(t, ch, time.Second)

	cancel() // simulate client disconnect

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		hub.mu.Lock()
		_, present := hub.runs["run-1"]
		hub.mu.Unlock()
		if !present {
			return // loop stopped + run deregistered after last subscriber left
		}
	}
	t.Fatal("run not deregistered after context cancel")
}

func TestTrySendDropsOldest(t *testing.T) {
	ch := make(chan any, 1)
	trySend(ch, "first")
	trySend(ch, "second") // buffer full → drop "first", enqueue "second"
	v := <-ch
	if v != "second" {
		t.Fatalf("got %v, want second (drop-oldest)", v)
	}
	// A trySend into an unread full channel must never block.
	trySend(ch, "a")
	trySend(ch, "b")
	if v := <-ch; v != "b" {
		t.Fatalf("got %v, want b", v)
	}
}

func TestIsTerminalRunStatus(t *testing.T) {
	terminal := []string{"succeeded", "failed", "canceled"}
	live := []string{"pending", "planning", "dispatching", "running", "finalizing", "", "weird"}
	for _, s := range terminal {
		if !isTerminalRunStatus(s) {
			t.Errorf("%q should be terminal", s)
		}
	}
	for _, s := range live {
		if isTerminalRunStatus(s) {
			t.Errorf("%q should not be terminal", s)
		}
	}
	if isTerminalRunStatus(123) {
		t.Error("non-string should not be terminal")
	}
}
