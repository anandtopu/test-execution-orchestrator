package worker

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/teo-dev/teo/internal/model"
	teonats "github.com/teo-dev/teo/internal/nats"
)

// SubscribeNATS pulls shard dispatch messages from JetStream and processes
// them with the same path the Postgres claim uses. If JetStream returns an
// error, the caller's polling loop continues to claim from Postgres as a
// fallback (NATS is not a hard dependency).
func (a *Agent) SubscribeNATS(ctx context.Context, js jetstream.JetStream) error {
	if js == nil {
		return errors.New("nats not configured")
	}
	consumer, err := js.CreateOrUpdateConsumer(ctx, teonats.StreamShards, jetstream.ConsumerConfig{
		Name:          "teo-worker-" + a.WorkerID,
		Durable:       "teo-worker-" + a.WorkerID,
		AckPolicy:     jetstream.AckExplicitPolicy,
		AckWait:       30 * time.Minute, // long enough for a shard to run
		MaxAckPending: 1,                // one shard at a time per worker
		FilterSubject: teonats.SubjShardsDispatch,
	})
	if err != nil {
		return err
	}
	cc, err := consumer.Consume(func(msg jetstream.Msg) {
		a.handleMessage(ctx, msg)
	})
	if err != nil {
		return err
	}
	go func() {
		<-ctx.Done()
		cc.Stop()
	}()
	a.Logger.Info("nats consumer started", "worker", a.WorkerID)
	return nil
}

func (a *Agent) handleMessage(ctx context.Context, msg jetstream.Msg) {
	if a.draining.Load() {
		// Don't take new work; nak so another (non-draining) worker picks it up.
		_ = msg.NakWithDelay(5 * time.Second)
		return
	}
	var d teonats.ShardDispatch
	if err := json.Unmarshal(msg.Data(), &d); err != nil {
		a.Logger.Error("decode dispatch", "err", err)
		_ = msg.Ack() // drop malformed; nothing we can do
		return
	}

	// Atomically claim the shard in Postgres. If another worker beat us to it
	// (the JS `MaxAckPending=1` doesn't preclude a parallel pg-claim path),
	// just ack and move on.
	tag, err := a.Pool.Exec(ctx, `
        UPDATE teo.shards
        SET status = 'running', worker_id = $1, started_at = COALESCE(started_at, now())
        WHERE id = $2 AND status = 'pending'
    `, a.WorkerID, d.ShardID)
	if err != nil {
		a.Logger.Error("claim shard via nats", "err", err, "shard", d.ShardID)
		// nak with a backoff so JS retries on another worker
		_ = msg.NakWithDelay(15 * time.Second)
		return
	}
	if tag.RowsAffected() == 0 {
		_ = msg.Ack()
		return
	}

	tests := make([]model.TestEntry, 0, len(d.Tests))
	for _, t := range d.Tests {
		tests = append(tests, model.TestEntry{
			Path:         t.Path,
			Name:         t.Name,
			ParamsHash:   t.ParamsHash,
			ASTSignature: t.ASTSignature,
			Tags:         t.Tags,
		})
	}
	a.Logger.Info("nats dispatch claimed", "shard", d.ShardID, "tests", len(tests))
	a.currentShardID.Store(d.ShardID)
	defer a.currentShardID.Store("")
	a.executeShard(ctx, d.RunID, d.ShardID, tests)
	_ = msg.Ack()
}
