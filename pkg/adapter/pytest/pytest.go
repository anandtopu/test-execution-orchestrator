// Package pytest is the pytest runner adapter for TEO.
// Discovery: `pytest --collect-only -q` (one-line-per-test format).
// Execution: invoke pytest with --json-report (pytest-json-report plugin) to
// produce a machine-readable result file we parse into Results.
package pytest

import (
	"bufio"
	"bytes"
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

// Adapter implements adapter.Adapter for pytest.
type Adapter struct {
	// Bin is the pytest executable; defaults to "pytest".
	Bin string
}

// New returns a configured pytest adapter.
func New() *Adapter { return &Adapter{Bin: "pytest"} }

// Name implements adapter.Adapter.
func (a *Adapter) Name() string { return "pytest" }

// Discover lists tests via pytest's collect-only mode.
func (a *Adapter) Discover(ctx context.Context, workdir string) ([]model.TestEntry, error) {
	cmd := exec.CommandContext(ctx, a.bin(), "--collect-only", "-q", "--no-header")
	cmd.Dir = workdir
	cmd.Env = os.Environ()
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("pytest collect: %w (stderr: %s)", err, stderrOf(err))
	}
	return parseCollect(out), nil
}

// parseCollect handles the one-line-per-test format from `pytest -q --collect-only`.
// Lines look like: tests/test_foo.py::TestBar::test_baz[param1-param2]
func parseCollect(b []byte) []model.TestEntry {
	var out []model.TestEntry
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "=") || strings.HasPrefix(line, "no tests ran") {
			continue
		}
		// summary lines like "5 tests collected" — skip
		if strings.Contains(line, "test") && strings.Contains(line, "collected") {
			continue
		}
		// nodeid expected: path.py::name[params]
		idx := strings.Index(line, "::")
		if idx < 0 {
			continue
		}
		path := line[:idx]
		name := line[idx+2:]
		params := ""
		if br := strings.Index(name, "["); br > 0 {
			params = name[br+1 : len(name)-1]
			name = name[:br]
		}
		out = append(out, model.TestEntry{
			Path:       path,
			Name:       name,
			ParamsHash: hashParams(params),
		})
	}
	return out
}

// jsonReport models the pytest-json-report output we care about.
type jsonReport struct {
	Tests []jsonTest `json:"tests"`
}

type jsonTest struct {
	NodeID   string  `json:"nodeid"`
	Outcome  string  `json:"outcome"`
	Duration float64 `json:"duration"`
	Call     struct {
		Longrepr string `json:"longrepr"`
		Crash    struct {
			Message string `json:"message"`
		} `json:"crash"`
	} `json:"call"`
}

// Execute runs pytest with the given selectors and emits Results as the report
// is parsed.
func (a *Adapter) Execute(ctx context.Context, workdir string, tests []model.TestEntry, opts adapter.ExecOptions, onResult adapter.ResultHandler) error {
	if len(tests) == 0 {
		return nil
	}
	tmpDir, err := os.MkdirTemp("", "teo-pytest-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmpDir)
	reportPath := filepath.Join(tmpDir, "report.json")

	args := []string{
		"--json-report",
		"--json-report-file=" + reportPath,
		"--no-header",
		"-q",
	}
	for _, t := range tests {
		nodeID := t.Path + "::" + t.Name
		args = append(args, nodeID)
	}

	// Wrap with timeout BEFORE constructing the command so cancellation
	// actually propagates to the subprocess. (Pre-fix the timeout context
	// was created after exec.CommandContext, which silently bound the cmd
	// to the original ctx.)
	if opts.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, opts.Timeout)
		defer cancel()
	}

	cmd := exec.CommandContext(ctx, a.bin(), args...)
	cmd.Dir = workdir
	cmd.Env = mergeEnv(os.Environ(), opts.Env)

	started := time.Now()
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, _ := cmd.Output()
	_ = out

	report, err := os.ReadFile(reportPath)
	if err != nil {
		// Worst case: emit a single errored result for every test.
		for _, t := range tests {
			onResult(adapter.Result{
				Test:           t,
				Outcome:        model.OutcomeErrored,
				Started:        started,
				Finished:       time.Now(),
				DurationMS:     int(time.Since(started).Milliseconds()),
				FailureMessage: "pytest produced no JSON report: " + err.Error(),
				Stderr:         stderr.Bytes(),
			})
		}
		return nil
	}
	var rep jsonReport
	if err := json.Unmarshal(report, &rep); err != nil {
		return fmt.Errorf("parse pytest report: %w", err)
	}
	indexByID := make(map[string]model.TestEntry, len(tests))
	for _, t := range tests {
		indexByID[t.Path+"::"+t.Name] = t
	}
	for _, jt := range rep.Tests {
		entry, ok := indexByID[stripParams(jt.NodeID)]
		if !ok {
			// fall back to a synthetic entry
			entry = model.TestEntry{Path: "?", Name: jt.NodeID}
		}
		onResult(adapter.Result{
			Test:           entry,
			Outcome:        translateOutcome(jt.Outcome),
			Started:        started,
			Finished:       started.Add(time.Duration(jt.Duration * float64(time.Second))),
			DurationMS:     int(jt.Duration * 1000),
			FailureMessage: jt.Call.Crash.Message,
			FailureStack:   jt.Call.Longrepr,
		})
	}
	return nil
}

func translateOutcome(s string) model.TestOutcome {
	switch s {
	case "passed":
		return model.OutcomePassed
	case "failed":
		return model.OutcomeFailed
	case "skipped":
		return model.OutcomeSkipped
	case "error":
		return model.OutcomeErrored
	}
	return model.OutcomeErrored
}

func (a *Adapter) bin() string {
	if a.Bin != "" {
		return a.Bin
	}
	return "pytest"
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

func stderrOf(err error) string {
	if e, ok := err.(*exec.ExitError); ok {
		return string(e.Stderr)
	}
	return ""
}

func stripParams(nodeID string) string {
	if br := strings.Index(nodeID, "["); br > 0 {
		return nodeID[:br]
	}
	return nodeID
}

func hashParams(params string) string {
	if params == "" {
		return ""
	}
	// not cryptographic; fingerprint includes this as a plain hash
	return fmt.Sprintf("%x", []byte(params))
}
