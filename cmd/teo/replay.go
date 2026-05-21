package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"

	"github.com/jackc/pgx/v5"

	"github.com/teo-dev/teo/internal/db"
	"github.com/teo-dev/teo/internal/logstore"
	"github.com/teo-dev/teo/internal/runmanager"
	"github.com/teo-dev/teo/internal/scheduler"
)

// runReplay implements `teo replay <run_id>` (S-05-04 AC2 / FR-304).
//
// It reads the assignment plan that was persisted at planning time
// (runs.meta.computed_plan), re-runs the pure scheduler on the same inputs with
// the same constraints, and reports whether the recomputed plan is identical —
// i.e. that scheduling is still deterministic for this run. A mismatch means the
// scheduler's behavior changed since the run was planned (a regression, or an
// intentional algorithm change that needs a plan-version bump).
func runReplay(args []string) {
	fs := flag.NewFlagSet("replay", flag.ExitOnError)
	pgDSN := fs.String("postgres-dsn", os.Getenv("TEO_POSTGRES_DSN"), "Postgres DSN (env: TEO_POSTGRES_DSN)")
	fromS3 := fs.Bool("from-s3", false, "Read the archived plan from S3 (runs/<id>/plan.json) instead of Postgres")
	s3Bucket := fs.String("s3-bucket", os.Getenv("TEO_S3_BUCKET"), "S3 bucket for --from-s3 (env: TEO_S3_BUCKET)")
	s3Region := fs.String("s3-region", envDefault("TEO_S3_REGION", "us-east-1"), "S3 region for --from-s3")
	s3Endpoint := fs.String("s3-endpoint", os.Getenv("TEO_S3_ENDPOINT"), "S3 endpoint for --from-s3 (MinIO/S3-compatible)")
	asJSON := fs.Bool("json", false, "Emit a JSON report instead of human-readable text")
	if err := fs.Parse(args); err != nil {
		os.Exit(2)
	}
	runID := fs.Arg(0)
	if runID == "" {
		exit("usage: teo replay <run_id> [--postgres-dsn=... | --from-s3 --s3-bucket=...] [--json]")
	}

	ctx := context.Background()
	var planRaw []byte
	if *fromS3 {
		planRaw = loadPlanFromS3(ctx, *s3Region, *s3Endpoint, *s3Bucket, runID)
	} else {
		planRaw = loadPlanFromPostgres(ctx, *pgDSN, runID)
	}

	var stored scheduler.Plan
	if err := json.Unmarshal(planRaw, &stored); err != nil {
		exit("stored plan is not valid JSON: %v", err)
	}

	recomputed, deterministic := scheduler.Replay(stored, scheduler.DefaultConstraints())

	if *asJSON {
		emitReplayJSON(runID, stored, recomputed, deterministic)
	} else {
		emitReplayText(runID, stored, recomputed, deterministic)
	}
	if !deterministic {
		os.Exit(1)
	}
}

// loadPlanFromPostgres reads runs.meta.computed_plan for runID.
func loadPlanFromPostgres(ctx context.Context, dsn, runID string) []byte {
	if dsn == "" {
		exit("--postgres-dsn or TEO_POSTGRES_DSN required (or use --from-s3)")
	}
	pool, err := db.OpenPostgres(ctx, dsn)
	if err != nil {
		exit("postgres open: %v", err)
	}
	defer pool.Close()

	var planRaw []byte
	err = pool.QueryRow(ctx,
		`SELECT meta->'computed_plan' FROM teo.runs WHERE id = $1`, runID).Scan(&planRaw)
	if errors.Is(err, pgx.ErrNoRows) {
		exit("run %s not found", runID)
	}
	if err != nil {
		exit("query plan: %v", err)
	}
	if len(planRaw) == 0 {
		exit("run %s has no computed plan yet (it was never scheduled past 'planning')", runID)
	}
	return planRaw
}

// loadPlanFromS3 downloads the archived plan at runs/<runID>/plan.json.
func loadPlanFromS3(ctx context.Context, region, endpoint, bucket, runID string) []byte {
	if bucket == "" {
		exit("--s3-bucket or TEO_S3_BUCKET required for --from-s3")
	}
	store, err := logstore.NewS3(ctx, region, endpoint, bucket)
	if err != nil {
		exit("s3 init: %v", err)
	}
	key := runmanager.PlanObjectKey(runID)
	planRaw, err := store.Download(ctx, key)
	if err != nil {
		exit("download %s: %v", key, err)
	}
	if len(planRaw) == 0 {
		exit("plan archive %s is empty", key)
	}
	return planRaw
}

func emitReplayText(runID string, stored, recomputed scheduler.Plan, deterministic bool) {
	fmt.Printf("run:          %s\n", runID)
	fmt.Printf("plan version: %s\n", stored.Version)
	fmt.Printf("stored:       %d shards · makespan %dms · %d quarantined\n",
		len(stored.Assignments), stored.MakespanPredictedMS, len(stored.QuarantineLane))
	fmt.Printf("recomputed:   %d shards · makespan %dms · %d quarantined\n",
		len(recomputed.Assignments), recomputed.MakespanPredictedMS, len(recomputed.QuarantineLane))
	if deterministic {
		fmt.Println("result:       ✅ deterministic — recomputed plan is identical to the stored plan")
		return
	}
	fmt.Println("result:       ❌ MISMATCH — the scheduler no longer reproduces the stored plan")
	fmt.Println("              (algorithm drift since this run was planned; bump the plan version if intended)")
}

func emitReplayJSON(runID string, stored, recomputed scheduler.Plan, deterministic bool) {
	type planSummary struct {
		Shards      int    `json:"shards"`
		MakespanMS  int    `json:"makespan_ms"`
		Quarantined int    `json:"quarantined"`
		Version     string `json:"version"`
	}
	report := struct {
		Run           string      `json:"run"`
		Deterministic bool        `json:"deterministic"`
		Stored        planSummary `json:"stored"`
		Recomputed    planSummary `json:"recomputed"`
	}{
		Run:           runID,
		Deterministic: deterministic,
		Stored: planSummary{
			Shards: len(stored.Assignments), MakespanMS: stored.MakespanPredictedMS,
			Quarantined: len(stored.QuarantineLane), Version: stored.Version,
		},
		Recomputed: planSummary{
			Shards: len(recomputed.Assignments), MakespanMS: recomputed.MakespanPredictedMS,
			Quarantined: len(recomputed.QuarantineLane), Version: recomputed.Version,
		},
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(report)
}
