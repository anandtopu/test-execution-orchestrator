package runmanager

import (
	"context"
	"log/slog"

	"github.com/nats-io/nats.go"

	"github.com/teo-dev/teo/internal/model"
	teonats "github.com/teo-dev/teo/internal/nats"
)

// UINotifyObserver publishes a best-effort core-NATS hint on every committed
// run transition so API gateways serving GraphQL WebSocket subscriptions can
// re-read the run and push it to clients (FR-706, S-09-02). It is a pure
// notification: the payload carries only the run id, and the API re-reads the
// authoritative row from Postgres. Failures are logged and swallowed — they must
// never block or roll back the state machine (see RunObserver's contract).
type UINotifyObserver struct {
	Conn   *nats.Conn
	Logger *slog.Logger
}

// OnRunStateChanged implements RunObserver.
func (o *UINotifyObserver) OnRunStateChanged(_ context.Context, snap RunSnapshot, _ model.RunStatus) error {
	if o.Conn == nil || snap.ID == "" {
		return nil
	}
	if err := teonats.PublishCore(o.Conn, teonats.SubjUIRunChanged, teonats.UIRunChanged{RunID: snap.ID}); err != nil && o.Logger != nil {
		o.Logger.Warn("ui run-changed publish failed", "run_id", snap.ID, "err", err)
	}
	return nil
}
