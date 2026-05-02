// Package runmanager drives a Run through its lifecycle. The reconciliation
// loop polls Postgres for runs needing attention; per-run leader election
// (ADR-0013) uses pg_try_advisory_xact_lock so two replicas don't double-process.
package runmanager

import (
	"errors"
	"slices"

	"github.com/teo-dev/teo/internal/model"
)

// allowed maps the legal next-states for each current state.
var allowed = map[model.RunStatus][]model.RunStatus{
	model.RunPending:     {model.RunPlanning, model.RunCancelled, model.RunFailed},
	model.RunPlanning:    {model.RunDispatching, model.RunCancelled, model.RunFailed},
	model.RunDispatching: {model.RunRunning, model.RunCancelled, model.RunFailed},
	model.RunRunning:     {model.RunFinalizing, model.RunCancelled, model.RunFailed},
	model.RunFinalizing:  {model.RunSucceeded, model.RunFailed},
	model.RunSucceeded:   {},
	model.RunFailed:      {},
	model.RunCancelled:   {},
}

// IsTerminal returns true if the status is a terminal state.
func IsTerminal(s model.RunStatus) bool {
	return s == model.RunSucceeded || s == model.RunFailed || s == model.RunCancelled
}

// CanTransition reports whether from→to is a legal state move.
func CanTransition(from, to model.RunStatus) bool {
	return slices.Contains(allowed[from], to)
}

// ErrInvalidTransition is returned by Transition for illegal moves.
var ErrInvalidTransition = errors.New("invalid run-state transition")
