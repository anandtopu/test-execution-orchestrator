// Package runsvc holds the transport-agnostic run-intake business logic shared
// by the HTTP/GraphQL gateway (internal/api) and the gRPC surface
// (internal/grpcsvc). Both transports call into Service so there is exactly one
// code path for Create/Get/Cancel; each transport is responsible for its own
// authentication and for translating the sentinel errors below into its native
// error representation (RFC 7807 problem+json for HTTP, gRPC status codes for
// gRPC).
package runsvc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/teo-dev/teo/internal/audit"
	"github.com/teo-dev/teo/internal/model"
)

// Sentinel errors returned by Service. Transports inspect these with errors.Is
// (and errors.As for ValidationError) to choose an appropriate status code.
var (
	// ErrValidation signals one or more invalid request fields. The concrete
	// error is always a *ValidationError so callers can recover the per-field
	// messages via errors.As.
	ErrValidation = errors.New("validation failed")
	// ErrRepoNotFound means the target repository is not registered or disabled.
	ErrRepoNotFound = errors.New("repo not found")
	// ErrRunNotFound means no run exists with the requested id.
	ErrRunNotFound = errors.New("run not found")
	// ErrIdempotencyConflict means the Idempotency-Key was previously used for a
	// different commit on the same repo.
	ErrIdempotencyConflict = errors.New("idempotency-key conflict")
)

// ValidationError carries the per-field validation failures. It wraps
// ErrValidation so errors.Is(err, ErrValidation) is true while errors.As lets a
// transport recover the field list.
type ValidationError struct {
	Fields []model.FieldError
}

func (e *ValidationError) Error() string {
	if len(e.Fields) == 0 {
		return "validation failed"
	}
	return fmt.Sprintf("validation failed: %d field error(s)", len(e.Fields))
}

func (e *ValidationError) Unwrap() error { return ErrValidation }

// Service is the run-intake business logic, parameterised only by its data
// dependencies. It performs no authentication; transports gate that themselves.
type Service struct {
	Pool  *pgxpool.Pool
	Audit *audit.Logger
}

// Create inserts a new run (status 'pending') and its manifest run-plan
// precursor, honoring the Idempotency-Key contract: same key + same commit
// returns the existing run with created=false; same key + different commit
// returns ErrIdempotencyConflict. A fresh insert returns created=true.
func (s *Service) Create(ctx context.Context, req model.CreateRunRequest) (run *model.Run, created bool, err error) {
	if verrs := validateCreateRun(&req); len(verrs) > 0 {
		return nil, false, &ValidationError{Fields: verrs}
	}

	// Look up repo.
	var repoID string
	err = s.Pool.QueryRow(ctx,
		`SELECT id FROM teo.repos WHERE full_name = $1 AND vcs = 'github' AND enabled = true`,
		req.RepoFullName).Scan(&repoID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, fmt.Errorf("%w: %s", ErrRepoNotFound, req.RepoFullName)
	}
	if err != nil {
		return nil, false, fmt.Errorf("repo lookup: %w", err)
	}

	// Idempotency contract: same key + same commit → return existing run.
	// Same key + different commit → conflict (the client is reusing a key for a
	// semantically different request, which the spec forbids).
	if req.IdempotencyKey != "" {
		existing, conflict, lerr := s.lookupIdempotent(ctx, repoID, req.IdempotencyKey, req.CommitSHA)
		if conflict {
			return nil, false, ErrIdempotencyConflict
		}
		if lerr == nil && existing != nil {
			return existing, false, nil
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

	_, err = s.Pool.Exec(ctx, `
        INSERT INTO teo.runs
            (id, repo_id, commit_sha, branch, triggered_by, trigger_actor,
             trigger_pr_number, status, budget_seconds, meta)
        VALUES ($1, $2, $3, $4, $5, $6, NULLIF($7,0)::int, 'pending', $8, $9::jsonb)
    `, runID, repoID, req.CommitSHA, req.Branch, "api",
		nullableString(req.TriggerActor), req.TriggerPRNumber, budgetSec, metaJSON)
	if err != nil {
		// 23505 = unique_violation. The partial unique index on
		// (repo_id, meta->>'idempotency_key') closes the SELECT/INSERT race: a
		// concurrent request that won the insert is now visible, so re-do the
		// lookup and return the existing row (created=false) instead of erroring.
		if isUniqueViolation(err) && req.IdempotencyKey != "" {
			existing, conflict, lerr := s.lookupIdempotent(ctx, repoID, req.IdempotencyKey, req.CommitSHA)
			if conflict {
				return nil, false, ErrIdempotencyConflict
			}
			if lerr == nil && existing != nil {
				return existing, false, nil
			}
		}
		return nil, false, fmt.Errorf("insert run: %w", err)
	}

	// Persist the manifest as a run-plan precursor so the Run Manager can pick it up.
	planJSON, _ := json.Marshal(req.Manifest)
	_, _ = s.Pool.Exec(ctx, `
        INSERT INTO teo.run_plans (run_id, plan, plan_version) VALUES ($1, $2::jsonb, 'manifest-v1')
    `, runID, planJSON)

	if s.Audit != nil {
		_ = s.Audit.Log(ctx, audit.Entry{
			Action:     "run.create",
			TargetType: "run",
			TargetID:   runID,
			Meta:       map[string]any{"repo": req.RepoFullName, "commit": req.CommitSHA},
		})
	}

	run, err = s.loadRun(ctx, runID)
	if err != nil {
		return nil, false, fmt.Errorf("reload run: %w", err)
	}
	return run, true, nil
}

// Get returns the run with the given id, or ErrRunNotFound if it does not exist.
func (s *Service) Get(ctx context.Context, id string) (*model.Run, error) {
	run, err := s.loadRun(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("%w: %s", ErrRunNotFound, id)
	}
	if err != nil {
		return nil, fmt.Errorf("load run: %w", err)
	}
	return run, nil
}

// Cancel transitions a non-terminal run to 'cancelled'. It is idempotent:
// canceling an already-terminal run returns that run with no error.
// A missing run yields ErrRunNotFound.
//
// NOTE: the literal status written is "cancelled" (two l's) to match the
// teo.runs CHECK constraint in migrations/postgres/001_initial.up.sql. This
// deliberately differs from model.RunCancelled ("canceled", one l); do not
// "fix" it to use the model constant or the UPDATE silently affects 0 rows.
func (s *Service) Cancel(ctx context.Context, id string) (*model.Run, error) {
	tag, err := s.Pool.Exec(ctx, `
        UPDATE teo.runs
        SET status = 'cancelled', finished_at = COALESCE(finished_at, now())
        WHERE id = $1
          AND status IN ('pending','planning','dispatching','running')
    `, id)
	if err != nil {
		return nil, fmt.Errorf("cancel run: %w", err)
	}
	if tag.RowsAffected() == 0 {
		// Either does not exist or already terminal — re-load to disambiguate.
		run, lerr := s.loadRun(ctx, id)
		if errors.Is(lerr, pgx.ErrNoRows) {
			return nil, fmt.Errorf("%w: %s", ErrRunNotFound, id)
		}
		if lerr != nil {
			return nil, fmt.Errorf("load run: %w", lerr)
		}
		return run, nil // already in a terminal state — idempotent
	}
	if s.Audit != nil {
		_ = s.Audit.Log(ctx, audit.Entry{Action: "run.cancel", TargetType: "run", TargetID: id})
	}
	run, err := s.loadRun(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("reload run: %w", err)
	}
	return run, nil
}

// lookupIdempotent returns the existing run for (repoID, key) when its commit
// matches commitSHA. It reports conflict=true when a row exists but its commit
// differs. A miss returns (nil, false, pgx.ErrNoRows).
func (s *Service) lookupIdempotent(ctx context.Context, repoID, key, commitSHA string) (*model.Run, bool, error) {
	var existing, existingCommit string
	err := s.Pool.QueryRow(ctx,
		`SELECT id, commit_sha FROM teo.runs
         WHERE repo_id = $1 AND meta->>'idempotency_key' = $2
         ORDER BY created_at DESC LIMIT 1`,
		repoID, key).Scan(&existing, &existingCommit)
	if err != nil {
		return nil, false, err
	}
	if existingCommit != commitSHA {
		return nil, true, nil
	}
	run, lerr := s.loadRun(ctx, existing)
	if lerr != nil {
		return nil, false, lerr
	}
	return run, false, nil
}

func (s *Service) loadRun(ctx context.Context, id string) (*model.Run, error) {
	var r model.Run
	var repoFull string
	err := s.Pool.QueryRow(ctx, `
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

func validateCreateRun(req *model.CreateRunRequest) []model.FieldError {
	var errs []model.FieldError
	if req.RepoFullName == "" {
		errs = append(errs, model.FieldError{Field: "repo_full_name", Message: "required"})
	}
	if req.CommitSHA == "" {
		errs = append(errs, model.FieldError{Field: "commit_sha", Message: "required"})
	}
	if req.Branch == "" {
		errs = append(errs, model.FieldError{Field: "branch", Message: "required"})
	}
	if req.Manifest.Runner == "" {
		errs = append(errs, model.FieldError{Field: "manifest.runner", Message: "required"})
	}
	if len(req.Manifest.Tests) == 0 {
		errs = append(errs, model.FieldError{Field: "manifest.tests", Message: "must contain at least one test"})
	}
	return errs
}

// isUniqueViolation reports whether err is a Postgres 23505 unique_violation,
// produced by the partial unique index on (repo_id, meta->>'idempotency_key').
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

func nullableString(s string) any {
	if s == "" {
		return nil
	}
	return s
}
