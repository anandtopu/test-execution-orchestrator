package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/teo-dev/teo/internal/audit"
	"github.com/teo-dev/teo/internal/auth"
	"github.com/teo-dev/teo/internal/model"
)

// RunsHandler exposes run-intake REST endpoints.
type RunsHandler struct {
	Pool  *pgxpool.Pool
	Audit *audit.Logger
}

// Routes mounts the runs subroutes onto r.
func (h *RunsHandler) Routes(r chi.Router) {
	r.Post("/", h.Create)
	r.Get("/{id}", h.Get)
	r.Post("/{id}/cancel", h.Cancel)
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

	if errs := validateCreateRun(&req); len(errs) > 0 {
		writeProblem(w, http.StatusBadRequest, "Validation failed", "see errors", errs...)
		return
	}

	ctx := r.Context()

	// Look up repo
	var repoID string
	err := h.Pool.QueryRow(ctx,
		`SELECT id FROM teo.repos WHERE full_name = $1 AND vcs = 'github' AND enabled = true`,
		req.RepoFullName).Scan(&repoID)
	if errors.Is(err, pgx.ErrNoRows) {
		writeProblem(w, http.StatusNotFound, "Repo not found",
			"repository "+req.RepoFullName+" is not registered or is disabled")
		return
	}
	if err != nil {
		writeProblem(w, http.StatusInternalServerError, "Database error", err.Error())
		return
	}

	// Idempotency: if a run already exists with the same key for this repo/commit, return it.
	if req.IdempotencyKey != "" {
		var existing string
		err := h.Pool.QueryRow(ctx,
			`SELECT id FROM teo.runs
             WHERE repo_id = $1 AND meta->>'idempotency_key' = $2
             ORDER BY created_at DESC LIMIT 1`,
			repoID, req.IdempotencyKey).Scan(&existing)
		if err == nil {
			run, err := h.loadRun(ctx, existing)
			if err == nil {
				writeJSON(w, http.StatusOK, run)
				return
			}
		}
	}

	runID := uuid.New().String()
	meta := map[string]any{}
	if req.IdempotencyKey != "" {
		meta["idempotency_key"] = req.IdempotencyKey
	}
	meta["test_count"] = len(req.Manifest.Tests)
	meta["runner"] = req.Manifest.Runner
	metaJSON, _ := json.Marshal(meta)

	var budgetSec *int
	if req.Budget != nil && req.Budget.MaxSeconds > 0 {
		budgetSec = &req.Budget.MaxSeconds
	}

	_, err = h.Pool.Exec(ctx, `
        INSERT INTO teo.runs
            (id, repo_id, commit_sha, branch, triggered_by, trigger_actor,
             trigger_pr_number, status, budget_seconds, meta)
        VALUES ($1, $2, $3, $4, $5, $6, NULLIF($7,0)::int, 'pending', $8, $9::jsonb)
    `, runID, repoID, req.CommitSHA, req.Branch, "api",
		nullableString(req.TriggerActor), req.TriggerPRNumber, budgetSec, metaJSON)
	if err != nil {
		writeProblem(w, http.StatusInternalServerError, "Insert failed", err.Error())
		return
	}

	// Persist the manifest as a run-plan precursor so the Run Manager can pick it up.
	planJSON, _ := json.Marshal(req.Manifest)
	_, _ = h.Pool.Exec(ctx, `
        INSERT INTO teo.run_plans (run_id, plan, plan_version) VALUES ($1, $2::jsonb, 'manifest-v1')
    `, runID, planJSON)

	_ = h.Audit.Log(ctx, audit.Entry{
		Action:     "run.create",
		TargetType: "run",
		TargetID:   runID,
		Meta:       map[string]any{"repo": req.RepoFullName, "commit": req.CommitSHA},
	})

	run, err := h.loadRun(ctx, runID)
	if err != nil {
		writeProblem(w, http.StatusInternalServerError, "Reload failed", err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, run)
}

// Get implements GET /api/v1/runs/{id}.
func (h *RunsHandler) Get(w http.ResponseWriter, r *http.Request) {
	if auth.PrincipalFrom(r.Context()) == nil {
		writeProblem(w, http.StatusUnauthorized, "Unauthorized", "authentication required")
		return
	}
	id := chi.URLParam(r, "id")
	run, err := h.loadRun(r.Context(), id)
	if errors.Is(err, pgx.ErrNoRows) {
		writeProblem(w, http.StatusNotFound, "Not found", "run "+id+" does not exist")
		return
	}
	if err != nil {
		writeProblem(w, http.StatusInternalServerError, "Database error", err.Error())
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
	tag, err := h.Pool.Exec(r.Context(), `
        UPDATE teo.runs
        SET status = 'cancelled', finished_at = COALESCE(finished_at, now())
        WHERE id = $1
          AND status IN ('pending','planning','dispatching','running')
    `, id)
	if err != nil {
		writeProblem(w, http.StatusInternalServerError, "Database error", err.Error())
		return
	}
	if tag.RowsAffected() == 0 {
		// Either does not exist or already terminal — re-load to disambiguate.
		run, err := h.loadRun(r.Context(), id)
		if errors.Is(err, pgx.ErrNoRows) {
			writeProblem(w, http.StatusNotFound, "Not found", "run "+id+" does not exist")
			return
		}
		writeJSON(w, http.StatusOK, run) // already in a terminal state — idempotent
		return
	}
	_ = h.Audit.Log(r.Context(), audit.Entry{Action: "run.cancel", TargetType: "run", TargetID: id})
	run, _ := h.loadRun(r.Context(), id)
	writeJSON(w, http.StatusOK, run)
}

func (h *RunsHandler) loadRun(ctx context.Context, id string) (*model.Run, error) {
	var r model.Run
	var repoFull string
	err := h.Pool.QueryRow(ctx, `
        SELECT r.id, r.repo_id, repos.full_name, r.commit_sha, r.branch,
               r.status, r.started_at, r.finished_at, COALESCE(r.total_duration_ms,0),
               COALESCE(r.budget_seconds,0), COALESCE(r.preemption_count,0),
               r.created_at, r.updated_at
        FROM teo.runs r
        JOIN teo.repos ON repos.id = r.repo_id
        WHERE r.id = $1
    `, id).Scan(&r.ID, &r.RepoID, &repoFull, &r.CommitSHA, &r.Branch,
		&r.Status, &r.StartedAt, &r.FinishedAt, &r.TotalDurationMS,
		&r.BudgetSeconds, &r.PreemptionCount, &r.CreatedAt, &r.UpdatedAt)
	if err != nil {
		return nil, err
	}
	r.RepoFullName = repoFull
	return &r, nil
}

func validateCreateRun(req *model.CreateRunRequest) []FieldError {
	var errs []FieldError
	if req.RepoFullName == "" {
		errs = append(errs, FieldError{Field: "repo_full_name", Message: "required"})
	}
	if req.CommitSHA == "" {
		errs = append(errs, FieldError{Field: "commit_sha", Message: "required"})
	}
	if req.Branch == "" {
		errs = append(errs, FieldError{Field: "branch", Message: "required"})
	}
	if req.Manifest.Runner == "" {
		errs = append(errs, FieldError{Field: "manifest.runner", Message: "required"})
	}
	if len(req.Manifest.Tests) == 0 {
		errs = append(errs, FieldError{Field: "manifest.tests", Message: "must contain at least one test"})
	}
	return errs
}

func nullableString(s string) any {
	if s == "" {
		return nil
	}
	return s
}
