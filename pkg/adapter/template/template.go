// Package template is a copy-and-fill skeleton for new TEO runner adapters.
// See docs/adapters/spi.md for the contract this implements.
//
// To use: copy this directory under a new name, rename the package + Adapter
// struct, then fill in the TODO blocks. The structural patterns (timeout
// propagation, env merge, no-output fallback) are correct as-shipped — don't
// remove them, fill in around them.
package template

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"time"

	"github.com/teo-dev/teo/internal/model"
	"github.com/teo-dev/teo/pkg/adapter"
)

// Adapter implements adapter.Adapter for <YOUR-RUNNER>.
type Adapter struct {
	// Bin is the runner executable. Defaults to the runner's canonical name.
	Bin string
}

// New returns a configured adapter using the runner's default binary name.
func New() *Adapter { return &Adapter{Bin: "your-runner"} }

// Name implements adapter.Adapter. Lowercase, no spaces.
func (a *Adapter) Name() string { return "your-runner" }

// Discover lists the tests in workdir.
//
// Most runs ship a precomputed manifest, so Discover is best-effort. If your
// runner can't enumerate individual tests without executing them (e.g. Jest),
// return one TestEntry per test file with a sentinel Name; per-test results
// can still be synthesized in Execute.
//
// Return (nil, nil) — no error, empty slice — when the workdir genuinely has
// no tests. Return an error only on infrastructure failure (binary missing,
// workdir invalid).
func (a *Adapter) Discover(ctx context.Context, workdir string) ([]model.TestEntry, error) {
	cmd := exec.CommandContext(ctx, a.bin(), "--list-tests" /* TODO: runner's enumeration flag */)
	cmd.Dir = workdir
	cmd.Env = os.Environ()
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("%s discover: %w", a.Name(), err)
	}

	// TODO: parse `out` into TestEntry values.
	//
	// Required per entry:
	//   - Path: the addressable unit your runner accepts as a selector
	//   - Name: the test name within Path
	// Optional:
	//   - ParamsHash: hash of parametrization values (stable across runs)
	//   - Tags: free-form labels
	//
	// The composite (Path, Name, ParamsHash) is what TEO uses as a stable
	// fingerprint — it must uniquely identify a logically-distinct test.
	_ = out
	return nil, nil
}

// Execute runs the requested subset of tests and emits one Result per test.
//
// Stream results as they arrive — do not batch. The worker uses the onResult
// callback to keep dashboards and S3 log uploads warm in real time.
func (a *Adapter) Execute(ctx context.Context, workdir string, tests []model.TestEntry, opts adapter.ExecOptions, onResult adapter.ResultHandler) error {
	if len(tests) == 0 {
		return nil
	}

	// IMPORTANT: build the timeout context BEFORE exec.CommandContext.
	// exec.CommandContext binds the cmd to the ctx at construction time, so
	// constructing the timeout context afterwards silently leaves the cmd
	// bound to the original (non-timing-out) ctx. This was a real bug in the
	// pytest adapter; don't reintroduce it.
	if opts.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, opts.Timeout)
		defer cancel()
	}

	// TODO: build the runner command. Selectors come straight from `tests` —
	// don't sanitize, the gosec G204 exemption on this package is legitimate.
	args := []string{ /* TODO: runner flags */ }
	for _, t := range tests {
		args = append(args, t.Path /* TODO: + t.Name in your runner's nodeid format */)
	}

	cmd := exec.CommandContext(ctx, a.bin(), args...)
	cmd.Dir = workdir
	cmd.Env = mergeEnv(os.Environ(), opts.Env)
	started := time.Now()
	_, runErr := cmd.Output()

	// TODO: parse the runner's output (stdout, stderr, or a structured report
	// file you wrote with --report=<tmp>) into Results.
	//
	// For each test that finished, call onResult with:
	//   - Test:           the matching TestEntry from `tests`
	//   - Outcome:        translated via translateOutcome (below)
	//   - Started/Finished/DurationMS
	//   - FailureMessage / FailureStack on failure
	//   - Stdout/Stderr if the runner streams per-test output (most don't)
	parsed := false // TODO: set true once you've parsed and emitted results

	if !parsed {
		// Worst-case fallback: runner produced no usable output. Emit one
		// errored Result per requested test so the worker doesn't orphan
		// them in the pending state. pytest and jest both do this.
		for _, t := range tests {
			onResult(adapter.Result{
				Test:           t,
				Outcome:        model.OutcomeErrored,
				Started:        started,
				Finished:       time.Now(),
				DurationMS:     int(time.Since(started).Milliseconds()),
				FailureMessage: fmt.Sprintf("%s produced no parseable output: %v", a.Name(), runErr),
			})
		}
	}
	return nil
}

// translateOutcome maps your runner's status vocabulary to model.TestOutcome.
// Unknown statuses default to OutcomeErrored — don't over-engineer the mapping.
func translateOutcome(s string) model.TestOutcome {
	switch s {
	case "passed", "pass", "ok":
		return model.OutcomePassed
	case "failed", "fail":
		return model.OutcomeFailed
	case "skipped", "skip", "pending", "todo":
		return model.OutcomeSkipped
	}
	return model.OutcomeErrored
}

func (a *Adapter) bin() string {
	if a.Bin != "" {
		return a.Bin
	}
	return "your-runner"
}

func mergeEnv(base []string, extra map[string]string) []string {
	if len(extra) == 0 {
		return base
	}
	out := make([]string, len(base), len(base)+len(extra))
	copy(out, base)
	for k, v := range extra {
		out = append(out, k+"="+v)
	}
	return out
}

// Compile-time interface check. Keep this — it's the cheapest way to catch
// a contract drift the moment it happens.
var _ adapter.Adapter = (*Adapter)(nil)
