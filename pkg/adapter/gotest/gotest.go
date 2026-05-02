// Package gotest is the `go test` runner adapter.
// Discovery: `go test -list . ./...` per package.
// Execution: `go test -run '^(NameA|NameB)$' -json ./...` and parse streaming events.
package gotest

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/teo-dev/teo/internal/model"
	"github.com/teo-dev/teo/pkg/adapter"
)

// Adapter implements adapter.Adapter for the Go toolchain.
type Adapter struct {
	GoBin string // defaults to "go"
}

// New returns an Adapter using the system go binary.
func New() *Adapter { return &Adapter{GoBin: "go"} }

// Name implements adapter.Adapter.
func (a *Adapter) Name() string { return "go" }

// Discover lists tests in workdir.
func (a *Adapter) Discover(ctx context.Context, workdir string) ([]model.TestEntry, error) {
	listCmd := exec.CommandContext(ctx, a.bin(), "list", "./...")
	listCmd.Dir = workdir
	listCmd.Env = os.Environ()
	out, err := listCmd.Output()
	if err != nil {
		return nil, fmt.Errorf("go list: %w", err)
	}
	var packages []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if t := strings.TrimSpace(line); t != "" {
			packages = append(packages, t)
		}
	}

	var tests []model.TestEntry
	for _, pkg := range packages {
		cmd := exec.CommandContext(ctx, a.bin(), "test", "-list", ".", pkg)
		cmd.Dir = workdir
		cmd.Env = os.Environ()
		out, err := cmd.Output()
		if err != nil {
			continue // package may have no tests
		}
		for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			t := strings.TrimSpace(line)
			if t == "" || strings.HasPrefix(t, "ok") || strings.HasPrefix(t, "?") || strings.HasPrefix(t, "FAIL") {
				continue
			}
			if !strings.HasPrefix(t, "Test") && !strings.HasPrefix(t, "Benchmark") && !strings.HasPrefix(t, "Example") {
				continue
			}
			tests = append(tests, model.TestEntry{Path: pkg, Name: t})
		}
	}
	return tests, nil
}

// goEvent is one line of `go test -json` output.
type goEvent struct {
	Action  string  `json:"Action"`
	Package string  `json:"Package"`
	Test    string  `json:"Test,omitempty"`
	Output  string  `json:"Output,omitempty"`
	Elapsed float64 `json:"Elapsed,omitempty"`
}

// Execute runs the requested tests and emits Results as their `pass`/`fail`/`skip`
// events arrive.
func (a *Adapter) Execute(ctx context.Context, workdir string, tests []model.TestEntry, opts adapter.ExecOptions, onResult adapter.ResultHandler) error {
	if len(tests) == 0 {
		return nil
	}
	// Group by package so we can issue one `go test` per package with a regex of names.
	byPkg := map[string][]string{}
	indexByKey := map[string]model.TestEntry{}
	for _, t := range tests {
		// Top-level test name (strip subtest path) for the regex.
		base := t.Name
		if i := strings.Index(base, "/"); i > 0 {
			base = base[:i]
		}
		byPkg[t.Path] = append(byPkg[t.Path], base)
		indexByKey[t.Path+"::"+t.Name] = t
	}

	if opts.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, opts.Timeout)
		defer cancel()
	}

	startWall := time.Now()
	for pkg, names := range byPkg {
		uniq := dedupe(names)
		regex := "^(" + strings.Join(uniq, "|") + ")$"
		args := []string{"test", "-json", "-run", regex, pkg}
		cmd := exec.CommandContext(ctx, a.bin(), args...)
		cmd.Dir = workdir
		cmd.Env = mergeEnv(os.Environ(), opts.Env)

		stdout, err := cmd.StdoutPipe()
		if err != nil {
			return err
		}
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		if err := cmd.Start(); err != nil {
			return err
		}

		startedAt := map[string]time.Time{}
		sc := bufio.NewScanner(stdout)
		sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
		for sc.Scan() {
			var ev goEvent
			if err := json.Unmarshal(sc.Bytes(), &ev); err != nil {
				continue
			}
			if ev.Test == "" {
				continue
			}
			key := pkg + "::" + ev.Test
			entry, ok := indexByKey[key]
			if !ok {
				entry = model.TestEntry{Path: pkg, Name: ev.Test}
			}
			switch ev.Action {
			case "run":
				startedAt[key] = time.Now()
			case "pass", "fail", "skip":
				start := startedAt[key]
				if start.IsZero() {
					start = startWall
				}
				outcome := model.OutcomePassed
				switch ev.Action {
				case "fail":
					outcome = model.OutcomeFailed
				case "skip":
					outcome = model.OutcomeSkipped
				}
				dur := time.Duration(ev.Elapsed * float64(time.Second))
				onResult(adapter.Result{
					Test:       entry,
					Outcome:    outcome,
					Started:    start,
					Finished:   start.Add(dur),
					DurationMS: int(dur.Milliseconds()),
				})
			}
		}
		_ = cmd.Wait()
	}
	return nil
}

func (a *Adapter) bin() string {
	if a.GoBin != "" {
		return a.GoBin
	}
	return "go"
}

func dedupe(in []string) []string {
	seen := make(map[string]struct{})
	out := in[:0]
	for _, s := range in {
		if _, ok := seen[s]; !ok {
			seen[s] = struct{}{}
			out = append(out, s)
		}
	}
	return out
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
