// Package spot detects EC2 Spot interruption notices via IMDSv2 and signals
// the worker to drain (ADR-0020).
package spot

import (
	"context"
	"io"
	"net/http"
	"time"
)

// IMDSEndpoint is the standard IMDS host on AWS EC2.
const IMDSEndpoint = "http://169.254.169.254"

// Interruption represents a detected interruption notice.
type Interruption struct {
	Action string    // "terminate" | "stop" | "hibernate"
	Time   time.Time // when AWS will reclaim the instance
}

// Watcher polls IMDS at a fixed interval. The first signal is sent on the
// returned channel; subsequent signals are dropped (the consumer should drain).
//
// Watcher satisfies the worker.SpotInterruptionSource contract.
type Watcher struct {
	HTTP     *http.Client
	Endpoint string
	Period   time.Duration
}

// NewWatcher returns a Watcher with sensible defaults.
func NewWatcher() *Watcher {
	return &Watcher{
		HTTP:     &http.Client{Timeout: 2 * time.Second},
		Endpoint: IMDSEndpoint,
		Period:   5 * time.Second,
	}
}

// Watch polls IMDS until ctx is canceled or an interruption is detected.
// The channel receives at most one Interruption.
func (w *Watcher) Watch(ctx context.Context) <-chan Interruption {
	out := make(chan Interruption, 1)
	go func() {
		defer close(out)
		token := w.refreshToken(ctx)
		ticker := time.NewTicker(w.Period)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if it, ok := w.poll(ctx, token); ok {
					select {
					case out <- it:
					default:
					}
					return
				}
			}
		}
	}()
	return out
}

func (w *Watcher) refreshToken(ctx context.Context) string {
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, w.Endpoint+"/latest/api/token", nil)
	if err != nil {
		return ""
	}
	req.Header.Set("X-aws-ec2-metadata-token-ttl-seconds", "21600")
	resp, err := w.HTTP.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return ""
	}
	b, _ := io.ReadAll(resp.Body)
	return string(b)
}

func (w *Watcher) poll(ctx context.Context, token string) (Interruption, bool) {
	url := w.Endpoint + "/latest/meta-data/spot/instance-action"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return Interruption{}, false
	}
	if token != "" {
		req.Header.Set("X-aws-ec2-metadata-token", token)
	}
	resp, err := w.HTTP.Do(req)
	if err != nil {
		return Interruption{}, false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Interruption{}, false
	}
	body, _ := io.ReadAll(resp.Body)
	// Body is JSON like: {"action":"terminate","time":"2026-04-30T08:00:00Z"}
	return parseAction(body)
}

func parseAction(b []byte) (Interruption, bool) {
	type doc struct {
		Action string `json:"action"`
		Time   string `json:"time"`
	}
	var d doc
	if err := jsonUnmarshal(b, &d); err != nil {
		return Interruption{}, false
	}
	if d.Action == "" {
		return Interruption{}, false
	}
	t, _ := time.Parse(time.RFC3339, d.Time)
	return Interruption{Action: d.Action, Time: t}, true
}
