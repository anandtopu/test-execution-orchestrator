// Package audit appends rows to teo.audit_log. The application role is granted
// INSERT-only on this table, so misuse can't tamper history.
package audit

import (
	"context"
	"encoding/json"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/teo-dev/teo/internal/auth"
)

// Logger persists audit rows.
type Logger struct {
	Pool *pgxpool.Pool
}

// Entry is a single audit row.
type Entry struct {
	Action     string
	TargetType string
	TargetID   string
	Meta       map[string]any
}

// Log writes one audit row, attributing the actor from the principal in ctx.
func (l *Logger) Log(ctx context.Context, e Entry) error {
	if l == nil || l.Pool == nil {
		return nil // permitted in tests / dry-runs
	}
	p := auth.PrincipalFrom(ctx)
	var actorUser, actorAPIKey *string
	if p != nil {
		if p.IsAPIKey {
			id := p.APIKeyID
			if id != "" {
				actorAPIKey = &id
			}
		} else if p.UserID != "" {
			id := p.UserID
			actorUser = &id
		}
	}
	meta := []byte("{}")
	if e.Meta != nil {
		b, err := json.Marshal(e.Meta)
		if err != nil {
			return err
		}
		meta = b
	}
	_, err := l.Pool.Exec(ctx, `
        INSERT INTO teo.audit_log (actor_user_id, actor_api_key_id, action, target_type, target_id, meta)
        VALUES ($1, $2, $3, $4, $5, $6::jsonb)
    `, actorUser, actorAPIKey, e.Action, e.TargetType, e.TargetID, meta)
	return err
}
