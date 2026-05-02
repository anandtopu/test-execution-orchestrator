// Package nats holds the JetStream subjects and small helpers used by the
// run manager (publisher) and worker (subscriber). Per ADR-0007.
package nats

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

// Subjects used by TEO.
const (
	StreamShards = "TEO_SHARDS"
	SubjShardsDispatch = "teo.shards.dispatch"

	StreamResults = "TEO_RESULTS"
	SubjTestStarted  = "teo.results.test_started"
	SubjTestFinished = "teo.results.test_finished"
)

// ShardDispatch is the message body for SubjShardsDispatch.
type ShardDispatch struct {
	RunID         string         `json:"run_id"`
	ShardID       string         `json:"shard_id"`
	RepoFullName  string         `json:"repo_full_name"`
	Runner        string         `json:"runner"`
	Tests         []DispatchTest `json:"tests"`
	PredictedMS   int            `json:"predicted_ms"`
	DispatchedAt  time.Time      `json:"dispatched_at"`
}

// DispatchTest is a slim TestEntry shape inside a dispatch message.
type DispatchTest struct {
	Path       string   `json:"path"`
	Name       string   `json:"name"`
	ParamsHash string   `json:"params_hash,omitempty"`
	Tags       []string `json:"tags,omitempty"`
}

// Connect opens a NATS connection and a JetStream context.
// Returns ErrUnavailable if the cluster is unreachable; callers should fall back
// to direct Postgres claim (the worker's existing pull path).
func Connect(url string) (*nats.Conn, jetstream.JetStream, error) {
	if url == "" {
		return nil, nil, ErrUnavailable
	}
	nc, err := nats.Connect(url, nats.Timeout(5*time.Second), nats.MaxReconnects(-1))
	if err != nil {
		return nil, nil, err
	}
	js, err := jetstream.New(nc)
	if err != nil {
		nc.Close()
		return nil, nil, err
	}
	return nc, js, nil
}

// EnsureStreams declares the streams TEO needs. Idempotent.
func EnsureStreams(ctx context.Context, js jetstream.JetStream) error {
	_, err := js.CreateOrUpdateStream(ctx, jetstream.StreamConfig{
		Name:      StreamShards,
		Subjects:  []string{SubjShardsDispatch},
		Retention: jetstream.WorkQueuePolicy,
		MaxAge:    24 * time.Hour,
		Replicas:  1, // chart sets to 3 in HA prod
	})
	if err != nil {
		return err
	}
	_, err = js.CreateOrUpdateStream(ctx, jetstream.StreamConfig{
		Name:      StreamResults,
		Subjects:  []string{"teo.results.>"},
		Retention: jetstream.LimitsPolicy,
		MaxAge:    7 * 24 * time.Hour,
		Replicas:  1,
	})
	return err
}

// Publish sends a JSON-encoded message on subj.
func Publish(ctx context.Context, js jetstream.JetStream, subj string, body any) error {
	b, err := json.Marshal(body)
	if err != nil {
		return err
	}
	_, err = js.Publish(ctx, subj, b)
	return err
}

// ErrUnavailable is returned when no NATS URL is configured.
var ErrUnavailable = errors.New("nats unavailable")
