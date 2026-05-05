// Package jest is the Jest runner adapter.
// Discovery: `jest --listTests --json`.
// Execution: per-test invocation via `--testNamePattern` plus `--json --outputFile`.
package jest

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/teo-dev/teo/internal/model"
	"github.com/teo-dev/teo/pkg/adapter"
)

// Adapter implements adapter.Adapter for Jest.
type Adapter struct {
	JestBin string // defaults to "npx jest"
}

// New returns a Jest adapter using `npx jest` by default.
func New() *Adapter { return &Adapter{JestBin: "jest"} }

// Name implements adapter.Adapter.
func (a *Adapter) Name() string { return "jest" }

// Discover lists Jest test files. We don't enumerate individual `it()` blocks
// at discovery time — Jest doesn't expose them without running. We surface
// each test file as a single entry; per-test fingerprinting happens at execution.
func (a *Adapter) Discover(ctx context.Context, workdir string) ([]model.TestEntry, error) {
	cmd := exec.CommandContext(ctx, a.bin(), "--listTests", "--json")
	cmd.Dir = workdir
	cmd.Env = os.Environ()
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("jest --listTests: %w", err)
	}
	return parseListTests(out, workdir)
}

// parseListTests turns the JSON array emitted by `jest --listTests --json` into
// TestEntry values with paths relative to workdir.
func parseListTests(out []byte, workdir string) ([]model.TestEntry, error) {
	var paths []string
	if err := json.Unmarshal(out, &paths); err != nil {
		return nil, fmt.Errorf("parse listTests: %w", err)
	}
	tests := make([]model.TestEntry, 0, len(paths))
	for _, p := range paths {
		rel, _ := filepath.Rel(workdir, p)
		tests = append(tests, model.TestEntry{Path: rel, Name: "<file>"})
	}
	return tests, nil
}

// jestReport models the parts of `jest --json` we read.
type jestReport struct {
	TestResults []struct {
		Name             string `json:"name"` // file path
		AssertionResults []struct {
			AncestorTitles  []string `json:"ancestorTitles"`
			Title           string   `json:"title"`
			FullName        string   `json:"fullName"`
			Status          string   `json:"status"` // passed | failed | skipped | pending | todo
			Duration        float64  `json:"duration"`
			FailureMessages []string `json:"failureMessages"`
		} `json:"assertionResults"`
	} `json:"testResults"`
}

// Execute runs Jest against the test files in `tests`.
func (a *Adapter) Execute(ctx context.Context, workdir string, tests []model.TestEntry, opts adapter.ExecOptions, onResult adapter.ResultHandler) error {
	if len(tests) == 0 {
		return nil
	}
	tmp, err := os.MkdirTemp("", "teo-jest-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	reportPath := filepath.Join(tmp, "report.json")

	args := []string{
		"--json",
		"--outputFile=" + reportPath,
		"--passWithNoTests",
	}
	for _, t := range tests {
		args = append(args, t.Path)
	}

	if opts.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, opts.Timeout)
		defer cancel()
	}

	cmd := exec.CommandContext(ctx, a.bin(), args...)
	cmd.Dir = workdir
	cmd.Env = mergeEnv(os.Environ(), opts.Env)
	started := time.Now()
	_, _ = cmd.Output()

	rep, err := os.ReadFile(reportPath)
	if err != nil {
		// Worst case: error every requested test
		for _, t := range tests {
			onResult(adapter.Result{
				Test:       t,
				Outcome:    model.OutcomeErrored,
				Started:    started,
				Finished:   time.Now(),
				DurationMS: int(time.Since(started).Milliseconds()),
			})
		}
		return nil
	}
	return parseReport(rep, workdir, started, onResult)
}

// parseReport walks a jest --json report and emits one Result per assertion.
// Returns an error only when the report is structurally invalid; an empty
// report is a valid (no-op) success.
func parseReport(rep []byte, workdir string, started time.Time, onResult adapter.ResultHandler) error {
	var jr jestReport
	if err := json.Unmarshal(rep, &jr); err != nil {
		return fmt.Errorf("parse jest report: %w", err)
	}
	for _, file := range jr.TestResults {
		rel, _ := filepath.Rel(workdir, file.Name)
		for _, ar := range file.AssertionResults {
			name := strings.Join(append(ar.AncestorTitles, ar.Title), " > ")
			r := adapter.Result{
				Test:       model.TestEntry{Path: rel, Name: name},
				Outcome:    translate(ar.Status),
				Started:    started,
				Finished:   started.Add(time.Duration(ar.Duration) * time.Millisecond),
				DurationMS: int(ar.Duration),
			}
			if len(ar.FailureMessages) > 0 {
				r.FailureStack = strings.Join(ar.FailureMessages, "\n")
			}
			onResult(r)
		}
	}
	return nil
}

func translate(s string) model.TestOutcome {
	switch s {
	case "passed":
		return model.OutcomePassed
	case "failed":
		return model.OutcomeFailed
	case "skipped", "pending", "todo":
		return model.OutcomeSkipped
	}
	return model.OutcomeErrored
}

func (a *Adapter) bin() string {
	if a.JestBin != "" {
		return a.JestBin
	}
	return "jest"
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
