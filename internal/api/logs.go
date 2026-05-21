package api

import (
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/teo-dev/teo/internal/auth"
	"github.com/teo-dev/teo/internal/logstore"
)

// logURLTTL is how long a presigned per-test log URL stays valid. Short enough
// that a leaked URL expires quickly, long enough for the viewer to page through
// the tail a few times.
const logURLTTL = 5 * time.Minute

// logURLHandler serves GET /api/v1/runs/{id}/tests/{execId}/log. It returns a
// short-lived presigned S3 URL for the test execution's captured log
// (S-09-03 / FR-703-704). The browser (or the Next.js BFF proxy) then fetches
// the object directly — bytes never stream through the API.
//
// When no presigner is wired (S3 not configured) the route returns 501 so the
// UI can show "log storage not configured" rather than a generic error.
func logURLHandler(pool *pgxpool.Pool, presigner logstore.Presigner) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if auth.PrincipalFrom(r.Context()) == nil {
			writeProblem(w, http.StatusUnauthorized, "Unauthorized", "authentication required")
			return
		}
		if presigner == nil {
			writeProblem(w, http.StatusNotImplemented, "Log storage not configured",
				"set TEO_S3_BUCKET to enable per-test log retrieval")
			return
		}
		runID := chi.URLParam(r, "id")
		execID := chi.URLParam(r, "execId")

		// Validate the execution belongs to the run. Without the run_id join an
		// attacker who guesses any execId could mint a log URL for a run they
		// can't otherwise see (IDOR).
		var key *string
		err := pool.QueryRow(r.Context(), `
            SELECT te.log_object_key
            FROM teo.test_executions te
            JOIN teo.shards s ON s.id = te.shard_id
            WHERE te.id = $1 AND s.run_id = $2
        `, execID, runID).Scan(&key)
		if errors.Is(err, pgx.ErrNoRows) {
			writeProblem(w, http.StatusNotFound, "Not found",
				"no test execution "+execID+" in run "+runID)
			return
		}
		if err != nil {
			writeProblem(w, http.StatusInternalServerError, "Database error", err.Error())
			return
		}
		if key == nil || *key == "" {
			writeProblem(w, http.StatusNotFound, "No log",
				"this test execution has no captured log (empty output, or capture disabled)")
			return
		}

		url, err := presigner.Presign(r.Context(), *key, logURLTTL)
		if errors.Is(err, logstore.ErrPresignUnavailable) {
			writeProblem(w, http.StatusNotImplemented, "Log storage not configured", err.Error())
			return
		}
		if err != nil {
			writeProblem(w, http.StatusInternalServerError, "Presign failed", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"url":              url,
			"key":              *key,
			"expiresInSeconds": int(logURLTTL.Seconds()),
		})
	}
}
