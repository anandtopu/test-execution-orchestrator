package worker

import (
	"context"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"github.com/teo-dev/teo/internal/spot"
)

// stubSpotSource sends a single Interruption when StartedFiring is closed.
type stubSpotSource struct {
	send chan spot.Interruption
}

func (s *stubSpotSource) Watch(_ context.Context) <-chan spot.Interruption {
	return s.send
}

func TestIsDrainingDefaultFalse(t *testing.T) {
	a := &Agent{Logger: slog.Default()}
	if a.IsDraining() {
		t.Fatal("fresh agent should not be draining")
	}
}

func TestBeginDrainIsIdempotent(t *testing.T) {
	a := &Agent{Logger: slog.Default()}
	// nil Pool is OK: beginDrain returns early when there's no current shard,
	// so the SQL paths that need Pool are not exercised.
	a.beginDrain(context.Background(), spot.Interruption{Action: "terminate", Time: time.Now()})
	if !a.IsDraining() {
		t.Fatal("after first drain signal, IsDraining should be true")
	}
	// Second call must be a no-op (and must not panic on nil Pool).
	a.beginDrain(context.Background(), spot.Interruption{Action: "terminate"})
	if !a.IsDraining() {
		t.Fatal("draining flag should remain true after second signal")
	}
}

func TestBeginDrainSkipsSQLWhenNoCurrentShard(t *testing.T) {
	a := &Agent{Logger: slog.Default()} // Pool is nil; would panic if SQL ran
	// currentShardID unset → empty string → drain should skip the SQL branch.
	a.beginDrain(context.Background(), spot.Interruption{Action: "terminate"})
	// No panic, draining true.
	if !a.IsDraining() {
		t.Fatal("draining flag should be set")
	}
}

func TestWatchSpotForwardsFirstSignal(t *testing.T) {
	src := &stubSpotSource{send: make(chan spot.Interruption, 1)}
	a := &Agent{Logger: slog.Default(), SpotWatcher: src}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go a.watchSpot(ctx)

	src.send <- spot.Interruption{Action: "terminate", Time: time.Now()}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if a.IsDraining() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("watchSpot did not flip draining flag within 2s")
}

func TestWatchSpotIgnoresEmptyAction(t *testing.T) {
	src := &stubSpotSource{send: make(chan spot.Interruption, 1)}
	a := &Agent{Logger: slog.Default(), SpotWatcher: src}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go a.watchSpot(ctx)

	src.send <- spot.Interruption{Action: ""} // shouldn't trigger drain

	time.Sleep(100 * time.Millisecond)
	if a.IsDraining() {
		t.Fatal("empty action should not trigger drain")
	}
}

// TestDrainingCounterRaceFree verifies the atomic flip used by IsDraining.
func TestDrainingCounterRaceFree(t *testing.T) {
	a := &Agent{Logger: slog.Default()}
	var hits int64
	const N = 50
	done := make(chan struct{}, N)
	for i := 0; i < N; i++ {
		go func() {
			a.beginDrain(context.Background(), spot.Interruption{Action: "terminate"})
			atomic.AddInt64(&hits, 1)
			done <- struct{}{}
		}()
	}
	for i := 0; i < N; i++ {
		<-done
	}
	if !a.IsDraining() {
		t.Fatal("draining flag should be set after concurrent calls")
	}
	if atomic.LoadInt64(&hits) != N {
		t.Fatalf("hits = %d, want %d", hits, N)
	}
}
