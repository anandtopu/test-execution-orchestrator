// Package doctor runs connectivity + sanity checks against every TEO
// dependency and reports a structured result. Used by the `teo doctor` CLI
// (FR-1005). Each Check is parallel-safe; the runner fans them out behind a
// single deadline so a hung dependency doesn't stall the rest.
package doctor

import (
	"context"
	"sort"
	"sync"
	"time"
)

// Status is the outcome of a single Check.
type Status int

const (
	// StatusOK means the dependency is reachable and behaving as expected.
	StatusOK Status = iota
	// StatusWarn means the dependency responded but something is off
	// (e.g., schema migration version older than the binary expects).
	StatusWarn
	// StatusFail means the dependency is unreachable or returned an error.
	StatusFail
	// StatusSkipped means the operator did not configure the dependency.
	// Not an error — the cluster may legitimately be running without it.
	StatusSkipped
)

// String returns the lower-case name (used by the CLI table renderer).
func (s Status) String() string {
	switch s {
	case StatusOK:
		return "ok"
	case StatusWarn:
		return "warn"
	case StatusFail:
		return "fail"
	case StatusSkipped:
		return "skipped"
	}
	return "unknown"
}

// Result is what a single Check returns.
type Result struct {
	Name    string        `json:"name"`
	Status  Status        `json:"status"`
	Message string        `json:"message"`
	Detail  string        `json:"detail,omitempty"`
	Latency time.Duration `json:"latency_ms,omitempty"`
}

// Check is a single connectivity/sanity probe.
type Check interface {
	Name() string
	Run(ctx context.Context) Result
}

// CheckFunc adapts a function to the Check interface so callers don't need
// to declare a struct for every probe.
type CheckFunc struct {
	N string
	F func(ctx context.Context) Result
}

// Name implements Check.
func (c CheckFunc) Name() string { return c.N }

// Run implements Check.
func (c CheckFunc) Run(ctx context.Context) Result { return c.F(ctx) }

// Run executes every check in parallel under a single deadline. Results are
// returned sorted by name for stable CLI output.
func Run(ctx context.Context, checks []Check, deadline time.Duration) []Result {
	if deadline > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, deadline)
		defer cancel()
	}
	out := make([]Result, len(checks))
	var wg sync.WaitGroup
	for i, c := range checks {
		wg.Add(1)
		go func(i int, c Check) {
			defer wg.Done()
			start := time.Now()
			r := c.Run(ctx)
			if r.Name == "" {
				r.Name = c.Name()
			}
			if r.Latency == 0 {
				r.Latency = time.Since(start)
			}
			out[i] = r
		}(i, c)
	}
	wg.Wait()
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// ExitCode maps a result set to a process exit code:
//   - 0 if no Fail results (Skipped and Warn are acceptable);
//   - 1 if any Fail.
//
// All-Skipped is a valid 0 — the operator has chosen not to deploy those
// dependencies and asking for a doctor report shouldn't fail their script.
func ExitCode(results []Result) int {
	for _, r := range results {
		if r.Status == StatusFail {
			return 1
		}
	}
	return 0
}

// Summary aggregates counts per status for one-line CLI output.
type Summary struct {
	OK, Warn, Fail, Skipped int
}

// Aggregate returns counts across statuses.
func Aggregate(results []Result) Summary {
	var s Summary
	for _, r := range results {
		switch r.Status {
		case StatusOK:
			s.OK++
		case StatusWarn:
			s.Warn++
		case StatusFail:
			s.Fail++
		case StatusSkipped:
			s.Skipped++
		}
	}
	return s
}
