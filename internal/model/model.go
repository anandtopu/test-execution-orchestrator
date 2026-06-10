// Package model defines core domain types shared by services.
package model

import "time"

// RunStatus enumerates run lifecycle states.
type RunStatus string

const (
	RunPending     RunStatus = "pending"
	RunPlanning    RunStatus = "planning"
	RunDispatching RunStatus = "dispatching"
	RunRunning     RunStatus = "running"
	RunFinalizing  RunStatus = "finalizing"
	RunSucceeded   RunStatus = "succeeded"
	RunFailed      RunStatus = "failed"
	RunCancelled   RunStatus = "canceled"
)

// TestOutcome enumerates per-test outcomes.
type TestOutcome string

const (
	OutcomePassed      TestOutcome = "passed"
	OutcomeFailed      TestOutcome = "failed"
	OutcomeSkipped     TestOutcome = "skipped"
	OutcomeErrored     TestOutcome = "errored"
	OutcomeTimedOut    TestOutcome = "timed_out"
	OutcomeInterrupted TestOutcome = "interrupted"
)

// TestEntry is one test in a manifest.
type TestEntry struct {
	Path       string `json:"path"`
	Name       string `json:"name"`
	ParamsHash string `json:"params_hash,omitempty"`
	// ASTSignature is a normalized hash of the test function's body, computed at
	// discovery (go/ast for Go, the ast module for pytest; empty for runners
	// without AST support, e.g. jest at v1.0). It is folded into the test
	// fingerprint so a body change yields a distinct identity, and persisted to
	// teo.tests.ast_signature for future move/rename linking. S-14-01 / S-06-01.
	ASTSignature string   `json:"ast_signature,omitempty"`
	Tags         []string `json:"tags,omitempty"`
}

// TestManifest is the runner-specific list of tests submitted with a run.
type TestManifest struct {
	Runner string      `json:"runner"`
	Tests  []TestEntry `json:"tests"`
}

// RunBudget caps a run's resource use.
type RunBudget struct {
	MaxSeconds int `json:"max_seconds,omitempty"`
	MaxWorkers int `json:"max_workers,omitempty"`
}

// CreateRunRequest is the JSON body of POST /api/v1/runs.
type CreateRunRequest struct {
	RepoFullName    string       `json:"repo_full_name"`
	CommitSHA       string       `json:"commit_sha"`
	Branch          string       `json:"branch"`
	Manifest        TestManifest `json:"manifest"`
	Budget          *RunBudget   `json:"budget,omitempty"`
	TriggerActor    string       `json:"trigger_actor,omitempty"`
	TriggerPRNumber int          `json:"trigger_pr_number,omitempty"`
	IdempotencyKey  string       `json:"-"` // header
}

// Run is the domain object returned by the API.
type Run struct {
	ID              string     `json:"id"`
	RepoID          string     `json:"repo_id"`
	RepoFullName    string     `json:"repo_full_name"`
	CommitSHA       string     `json:"commit_sha"`
	Branch          string     `json:"branch"`
	Status          RunStatus  `json:"status"`
	StartedAt       *time.Time `json:"started_at,omitempty"`
	FinishedAt      *time.Time `json:"finished_at,omitempty"`
	TotalDurationMS int        `json:"total_duration_ms,omitempty"`
	BudgetSeconds   int        `json:"budget_seconds,omitempty"`
	PreemptionCount int        `json:"preemption_count,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
}

// FieldError reports a single request-validation issue. It lives in model so
// both the HTTP layer (internal/api) and the shared run-intake service
// (internal/runsvc) can reference it without an import cycle.
type FieldError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

// Shard is a worker assignment within a run.
type Shard struct {
	ID                  string      `json:"id"`
	RunID               string      `json:"run_id"`
	Index               int         `json:"index"`
	Status              string      `json:"status"`
	WorkerID            string      `json:"worker_id,omitempty"`
	PredictedDurationMS int         `json:"predicted_duration_ms"`
	ActualDurationMS    int         `json:"actual_duration_ms,omitempty"`
	TestCount           int         `json:"test_count"`
	StartedAt           *time.Time  `json:"started_at,omitempty"`
	FinishedAt          *time.Time  `json:"finished_at,omitempty"`
	Tests               []TestEntry `json:"tests,omitempty"`
}

// Test is the logical-test record.
type Test struct {
	ID          string   `json:"id"`
	RepoID      string   `json:"repo_id"`
	Fingerprint string   `json:"fingerprint"`
	Path        string   `json:"path"`
	Name        string   `json:"name"`
	ParamsHash  string   `json:"params_hash,omitempty"`
	Runner      string   `json:"runner"`
	OwnerTeam   string   `json:"owner_team,omitempty"`
	Tags        []string `json:"tags,omitempty"`
	Status      string   `json:"status"`
}
