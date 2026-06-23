package api

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nats-io/nats.go"

	"github.com/teo-dev/teo/internal/metrics"
	"github.com/teo-dev/teo/internal/model"
	teonats "github.com/teo-dev/teo/internal/nats"
)

// Hub fans run-state changes out to GraphQL WebSocket subscribers (FR-706,
// S-09-02). Architecture:
//
//   - Postgres is authoritative. The hub never trusts an event's payload; a
//     "run X changed" hint only triggers a re-read of the full run row.
//   - Triggers are (a) a core-NATS hint on teonats.SubjUIRunChanged published
//     best-effort by the run-manager on every committed transition, and (b) a
//     per-run safety timer that re-reads every `safety` interval while the run
//     is live. (b) bounds freshness for intra-running shard progress, which the
//     run-manager state machine does not itself transition on; (a) accelerates
//     run-level changes to sub-second.
//   - Each subscriber gets a buffered, drop-oldest channel of full snapshots.
//     Because every push is the complete current run, coalescing/dropping is
//     safe (last-write-wins → the client always converges).
//   - The subscription completes when the run reaches a terminal status,
//     mirroring the poller's isLive auto-stop.
//
// One core-NATS subscription per process means any API replica can serve any
// run's subscription with no sticky sessions: every replica sees every hint and
// pushes only to its own local subscribers.
type Hub struct {
	mu       sync.Mutex
	runs     map[string]*runFanout
	snapshot runSnapshotFn
	metrics  *metrics.Registry
	nc       *nats.Conn
	baseCtx  context.Context
	safety   time.Duration
}

// runSnapshotFn reads the authoritative run snapshot (run row + shards). It is a
// field so the hub is unit-testable without Postgres.
type runSnapshotFn func(ctx context.Context, runID string) (map[string]any, error)

type subscriber struct {
	ch chan any
}

type runFanout struct {
	subs   map[*subscriber]struct{}
	notify chan struct{}
	ctx    context.Context
	cancel context.CancelFunc
}

// NewHub builds a hub backed by Postgres for snapshots and, when nc is
// non-nil, a core-NATS subscription for low-latency hints. reg may be nil.
func NewHub(pool *pgxpool.Pool, nc *nats.Conn, reg *metrics.Registry) *Hub {
	h := &Hub{
		runs:    make(map[string]*runFanout),
		metrics: reg,
		nc:      nc,
		baseCtx: context.Background(),
		safety:  2 * time.Second,
		snapshot: func(ctx context.Context, runID string) (map[string]any, error) {
			return runSnapshotWithShards(ctx, pool, runID)
		},
	}
	if nc != nil {
		// Best-effort: a failed subscribe just means hints don't arrive; the
		// safety timer still drives freshness and the frontend still has its
		// polling fallback.
		_, _ = nc.Subscribe(teonats.SubjUIRunChanged, h.onHint)
	}
	return h
}

// runSnapshotWithShards reads the run row and pre-populates its shards so the
// fanout does ONE shards read shared across all subscribers, instead of each
// subscriber's GraphQL execution re-querying. The Run.shards resolver prefers
// this pre-set slice when present (see graphql.go).
func runSnapshotWithShards(ctx context.Context, pool *pgxpool.Pool, runID string) (map[string]any, error) {
	run, err := queryRunByID(ctx, pool, runID)
	if err != nil {
		return nil, err
	}
	if shards, err := queryShards(ctx, pool, runID); err == nil {
		run["shards"] = shards
	}
	return run, nil
}

func (h *Hub) onHint(msg *nats.Msg) {
	var ev teonats.UIRunChanged
	if err := json.Unmarshal(msg.Data, &ev); err != nil || ev.RunID == "" {
		return
	}
	h.poke(ev.RunID)
}

// poke nudges a run's loop to re-read. Non-blocking: the buffered notify
// channel coalesces a burst of hints into a single pending read.
func (h *Hub) poke(runID string) {
	h.mu.Lock()
	rf := h.runs[runID]
	h.mu.Unlock()
	if rf == nil {
		return
	}
	select {
	case rf.notify <- struct{}{}:
	default:
	}
}

// Subscribe registers a subscriber for runID and returns its snapshot channel.
// The channel is closed (ending the GraphQL subscription) when the run reaches a
// terminal status or the request ctx is cancelled. The returned channel type is
// `chan any` because graphql-go's ExecuteSubscription type-switches on
// exactly that type to drive a streaming (vs single-shot) subscription.
func (h *Hub) Subscribe(ctx context.Context, runID string) (chan any, error) {
	sub := &subscriber{ch: make(chan any, 1)}

	h.mu.Lock()
	rf := h.runs[runID]
	if rf == nil {
		rctx, cancel := context.WithCancel(h.baseCtx)
		rf = &runFanout{
			subs:   make(map[*subscriber]struct{}),
			notify: make(chan struct{}, 1),
			ctx:    rctx,
			cancel: cancel,
		}
		h.runs[runID] = rf
		go h.loop(runID, rf)
	}
	rf.subs[sub] = struct{}{}
	h.mu.Unlock()

	if h.metrics != nil {
		h.metrics.GraphQLSubscriptionsActive.Inc()
	}

	// Emit an initial snapshot so a new subscriber renders immediately rather
	// than waiting for the first hint/tick.
	if snap, err := h.snapshot(ctx, runID); err == nil {
		trySend(sub.ch, snap)
	}

	// Deregister on client disconnect (the WS handler cancels ctx on close).
	go func() {
		<-ctx.Done()
		h.unsubscribe(runID, sub)
	}()

	return sub.ch, nil
}

// loop owns a run's re-read cadence: it reads on every hint or safety tick and
// fans the snapshot out, exiting when the run is terminal or all subscribers
// have left (rf.ctx cancelled).
func (h *Hub) loop(runID string, rf *runFanout) {
	ticker := time.NewTicker(h.safety)
	defer ticker.Stop()
	for {
		select {
		case <-rf.ctx.Done():
			return
		case <-rf.notify:
		case <-ticker.C:
		}
		snap, err := h.snapshot(rf.ctx, runID)
		if err != nil {
			continue // transient; retry on the next hint/tick
		}
		h.fanout(runID, snap)
		if isTerminalRunStatus(snap["status"]) {
			h.closeRun(runID)
			return
		}
	}
}

// fanout sends snap to every live subscriber under the lock. Sends are
// non-blocking (buffered, drop-oldest), so holding the lock cannot stall, and
// doing so guarantees no concurrent closeRun/unsubscribe closes a channel
// mid-send.
func (h *Hub) fanout(runID string, snap map[string]any) {
	h.mu.Lock()
	defer h.mu.Unlock()
	rf := h.runs[runID]
	if rf == nil {
		return
	}
	for s := range rf.subs {
		trySend(s.ch, snap)
		if h.metrics != nil {
			h.metrics.GraphQLSubscriptionPushes.Inc()
		}
	}
}

// closeRun ends all subscriptions for a terminal run.
func (h *Hub) closeRun(runID string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	rf := h.runs[runID]
	if rf == nil {
		return
	}
	for s := range rf.subs {
		close(s.ch)
		if h.metrics != nil {
			h.metrics.GraphQLSubscriptionsActive.Dec()
		}
	}
	rf.cancel()
	delete(h.runs, runID)
}

// unsubscribe removes one subscriber; if it was the last, the run's loop is
// stopped. Idempotent: a subscriber already removed by closeRun is a no-op.
func (h *Hub) unsubscribe(runID string, sub *subscriber) {
	h.mu.Lock()
	defer h.mu.Unlock()
	rf := h.runs[runID]
	if rf == nil {
		return
	}
	if _, ok := rf.subs[sub]; !ok {
		return
	}
	delete(rf.subs, sub)
	close(sub.ch)
	if h.metrics != nil {
		h.metrics.GraphQLSubscriptionsActive.Dec()
	}
	if len(rf.subs) == 0 {
		rf.cancel()
		delete(h.runs, runID)
	}
}

// trySend does a non-blocking, drop-oldest send: if the buffer is full it drops
// the stale snapshot and enqueues the fresh one. Safe because snapshots are
// last-write-wins.
func trySend(ch chan any, v any) {
	select {
	case ch <- v:
	default:
		select {
		case <-ch:
		default:
		}
		select {
		case ch <- v:
		default:
		}
	}
}

func isTerminalRunStatus(v any) bool {
	s, _ := v.(string)
	return s == string(model.RunSucceeded) ||
		s == string(model.RunFailed) ||
		s == string(model.RunCancelled)
}
