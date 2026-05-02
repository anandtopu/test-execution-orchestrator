// Package adapter defines the runner-adapter SPI (E-14 docs/adapters/spi.md).
// Adapters discover tests and execute them; the worker drives them.
package adapter

import (
	"context"
	"time"

	"github.com/teo-dev/teo/internal/model"
)

// Adapter is implemented by every runner integration.
type Adapter interface {
	// Name returns the runner identifier ("pytest", "go", "jest", ...).
	Name() string

	// Discover returns the list of tests in the given working directory.
	Discover(ctx context.Context, workdir string) ([]model.TestEntry, error)

	// Execute runs the given subset of tests sequentially, calling onResult
	// for each test as it finishes. Implementations must be cancellable via ctx.
	Execute(ctx context.Context, workdir string, tests []model.TestEntry, opts ExecOptions, onResult ResultHandler) error
}

// ExecOptions tune execution.
type ExecOptions struct {
	Timeout time.Duration
	Env     map[string]string
}

// Result is one test's outcome.
type Result struct {
	Test       model.TestEntry
	Outcome    model.TestOutcome
	Started    time.Time
	Finished   time.Time
	DurationMS int
	Stdout     []byte
	Stderr     []byte
	FailureMessage string
	FailureStack   string
}

// ResultHandler receives each Result as it completes.
type ResultHandler func(Result)
