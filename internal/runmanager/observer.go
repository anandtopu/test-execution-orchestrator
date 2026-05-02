package runmanager

import (
	"context"
	"time"

	"github.com/teo-dev/teo/internal/model"
)

// RunSnapshot is the data passed to RunObservers when a run transitions.
// It is a value type — observers do not see the live Manager state.
type RunSnapshot struct {
	ID                   string
	RepoFullName         string
	CommitSHA            string
	Branch               string
	Status               model.RunStatus
	StartedAt            *time.Time
	FinishedAt           *time.Time
	TotalDurationMS      int
	PreemptionCount      int
	GitHubCheckRunID     *int64
	GitHubInstallationID *int64
}

// RunObserver receives a callback for every successful run-state transition.
// Implementations MUST be non-blocking-tolerant: the Manager invokes them
// synchronously after committing the transition; an error is logged but does
// not roll back the transition. (Side-effects on third-party APIs that fail
// are best handled by retrying on the next reconciliation tick.)
type RunObserver interface {
	OnRunStateChanged(ctx context.Context, snap RunSnapshot, prev model.RunStatus) error
}

// RunObserverFunc adapts a function to the RunObserver interface.
type RunObserverFunc func(ctx context.Context, snap RunSnapshot, prev model.RunStatus) error

// OnRunStateChanged implements RunObserver.
func (f RunObserverFunc) OnRunStateChanged(ctx context.Context, snap RunSnapshot, prev model.RunStatus) error {
	return f(ctx, snap, prev)
}
