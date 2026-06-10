package api

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/teo-dev/teo/internal/audit"
	"github.com/teo-dev/teo/internal/auth"
	"github.com/teo-dev/teo/internal/model"
	"github.com/teo-dev/teo/internal/runsvc"
)

// RunsHandler exposes run-intake REST endpoints. All run-intake business logic
// lives in the shared runsvc.Service so HTTP/GraphQL and gRPC share one code
// path; this handler only does HTTP transport concerns (auth gating, JSON
// decode, chi routing) and maps runsvc sentinel errors to RFC 7807 problems.
type RunsHandler struct {
	Pool  *pgxpool.Pool
	Audit *audit.Logger
	Svc   *runsvc.Service
}

// service returns the shared run-intake service, constructing one from the
// handler's Pool/Audit when not explicitly injected (so existing call sites
// that only set Pool+Audit keep working).
func (h *RunsHandler) service() *runsvc.Service {
	if h.Svc != nil {
		return h.Svc
	}
	return &runsvc.Service{Pool: h.Pool, Audit: h.Audit}
}

// Routes mounts the runs subroutes onto r.
func (h *RunsHandler) Routes(r chi.Router) {
	r.Post("/", h.Create)
	r.Get("/{id}", h.Get)
	r.Post("/{id}/cancel", h.Cancel)
}

// writeRunProblem maps a runsvc sentinel error to an RFC 7807 problem response.
func writeRunProblem(w http.ResponseWriter, err error) {
	var verr *runsvc.ValidationError
	switch {
	case errors.As(err, &verr):
		writeProblem(w, http.StatusBadRequest, "Validation failed", "see errors", verr.Fields...)
	case errors.Is(err, runsvc.ErrRepoNotFound):
		writeProblem(w, http.StatusNotFound, "Repo not found", err.Error())
	case errors.Is(err, runsvc.ErrIdempotencyConflict):
		writeProblem(w, http.StatusConflict, "Idempotency-Key conflict",
			"Idempotency-Key was previously used for a different commit on this repo")
	case errors.Is(err, runsvc.ErrRunNotFound):
		writeProblem(w, http.StatusNotFound, "Not found", err.Error())
	default:
		writeProblem(w, http.StatusInternalServerError, "Internal error", err.Error())
	}
}

// Create implements POST /api/v1/runs.
func (h *RunsHandler) Create(w http.ResponseWriter, r *http.Request) {
	if auth.PrincipalFrom(r.Context()) == nil {
		writeProblem(w, http.StatusUnauthorized, "Unauthorized", "authentication required")
		return
	}
	var req model.CreateRunRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeProblem(w, http.StatusBadRequest, "Bad Request", "invalid JSON body")
		return
	}
	req.IdempotencyKey = r.Header.Get("Idempotency-Key")

	run, created, err := h.service().Create(r.Context(), req)
	if err != nil {
		writeRunProblem(w, err)
		return
	}
	if created {
		writeJSON(w, http.StatusCreated, run)
		return
	}
	writeJSON(w, http.StatusOK, run) // idempotent replay
}

// Get implements GET /api/v1/runs/{id}.
func (h *RunsHandler) Get(w http.ResponseWriter, r *http.Request) {
	if auth.PrincipalFrom(r.Context()) == nil {
		writeProblem(w, http.StatusUnauthorized, "Unauthorized", "authentication required")
		return
	}
	id := chi.URLParam(r, "id")
	run, err := h.service().Get(r.Context(), id)
	if err != nil {
		writeRunProblem(w, err)
		return
	}
	writeJSON(w, http.StatusOK, run)
}

// Cancel implements POST /api/v1/runs/{id}/cancel.
func (h *RunsHandler) Cancel(w http.ResponseWriter, r *http.Request) {
	if auth.PrincipalFrom(r.Context()) == nil {
		writeProblem(w, http.StatusUnauthorized, "Unauthorized", "authentication required")
		return
	}
	id := chi.URLParam(r, "id")
	run, err := h.service().Cancel(r.Context(), id)
	if err != nil {
		writeRunProblem(w, err)
		return
	}
	writeJSON(w, http.StatusOK, run)
}
