package api

import (
	"errors"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/teo-dev/teo/internal/quarantine"
)

// quarantineRestoreHandler consumes a magic-link token (S-15-04) and transitions
// the test back to active. Single-use; idempotent on second call (returns 410).
//
// Per ADR-0014 this endpoint requires no auth: the magic-link token IS the
// authorization. Tokens expire and are single-use; presence implies the
// CODEOWNERS-resolved assignee chose to restore.
func quarantineRestoreHandler(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := r.URL.Query().Get("token")
		if token == "" {
			writeProblem(w, http.StatusBadRequest, "Missing token", "?token=... required")
			return
		}
		testID, err := quarantine.Restore(r.Context(), pool, token)
		switch {
		case errors.Is(err, quarantine.ErrTokenConsumed):
			writeProblem(w, http.StatusGone, "Token already used",
				"this magic link has been consumed; the test was restored on a previous click")
			return
		case errors.Is(err, quarantine.ErrTokenExpired):
			writeProblem(w, http.StatusGone, "Token expired",
				"this magic link is past its expiry; ask TEO to re-propose un-quarantine")
			return
		case err != nil:
			writeProblem(w, http.StatusInternalServerError, "Restore failed", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{
			"status":  "ok",
			"test_id": testID,
		})
	}
}
