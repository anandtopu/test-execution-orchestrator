package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/teo-dev/teo/internal/model"
	"github.com/teo-dev/teo/pkg/adapter"
	"github.com/teo-dev/teo/pkg/adapter/gotest"
	"github.com/teo-dev/teo/pkg/adapter/jest"
	"github.com/teo-dev/teo/pkg/adapter/pytest"
)

// runDiscover implements `teo discover --runner <r> [dir]` (S-06-01 AC1). It
// runs the runner adapter's discovery — including AST-signature computation
// (S-14-01 / S-06-01) — and prints a manifest JSON suitable for the `manifest`
// field of POST /api/v1/runs. CI pipelines pipe this into a run submission.
func runDiscover(args []string) {
	fs := flag.NewFlagSet("discover", flag.ExitOnError)
	runner := fs.String("runner", "", "Test runner: pytest | go | jest (required)")
	output := fs.String("output", "", "Write the manifest to this file instead of stdout")
	timeoutSec := fs.Int("timeout", 120, "Discovery timeout in seconds")
	if err := fs.Parse(args); err != nil {
		os.Exit(2)
	}
	dir := fs.Arg(0)
	if dir == "" {
		dir = "."
	}

	var ad adapter.Adapter
	switch *runner {
	case "pytest":
		ad = pytest.New()
	case "go":
		ad = gotest.New()
	case "jest":
		ad = jest.New()
	case "":
		exit("--runner is required (pytest | go | jest)")
	default:
		exit("unknown runner %q (want pytest | go | jest)", *runner)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(*timeoutSec)*time.Second)
	defer cancel()

	tests, err := ad.Discover(ctx, dir)
	if err != nil {
		exit("discover failed: %v", err)
	}

	manifest := model.TestManifest{Runner: ad.Name(), Tests: tests}
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		exit("encode manifest: %v", err)
	}

	if *output != "" {
		if err := os.WriteFile(*output, data, 0o644); err != nil { //nolint:gosec // operator-chosen output path
			exit("write %s: %v", *output, err)
		}
		fmt.Fprintf(os.Stderr, "discovered %d %s tests → %s\n", len(tests), ad.Name(), *output)
		return
	}
	fmt.Println(string(data))
	fmt.Fprintf(os.Stderr, "discovered %d %s tests\n", len(tests), ad.Name())
}
