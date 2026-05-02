// Command worker is the TEO worker agent.
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/teo-dev/teo/internal/config"
	"github.com/teo-dev/teo/internal/db"
	"github.com/teo-dev/teo/internal/logstore"
	teonats "github.com/teo-dev/teo/internal/nats"
	"github.com/teo-dev/teo/internal/spot"
	"github.com/teo-dev/teo/internal/version"
	"github.com/teo-dev/teo/internal/worker"
	"github.com/teo-dev/teo/pkg/adapter/pytest"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	v := version.Get("worker")
	logger.Info("starting", "version", v.Version, "commit", v.Commit)

	cfg := config.LoadCommon()
	if cfg.PostgresDSN == "" {
		logger.Error("TEO_POSTGRES_DSN required")
		os.Exit(1)
	}
	workdir := os.Getenv("TEO_WORKDIR")
	if workdir == "" {
		workdir = "/workdir"
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	pool, err := db.OpenPostgres(ctx, cfg.PostgresDSN)
	if err != nil {
		logger.Error("postgres open failed", "err", err)
		os.Exit(1)
	}
	defer pool.Close()

	agent := &worker.Agent{
		Pool:    pool,
		Adapter: pytest.New(),
		Logger:  logger,
		Workdir: workdir,
	}

	// Log uploader (FR-404). Default to Noop unless the operator sets a bucket;
	// that keeps dev/CI from needing AWS creds while still exercising the wiring.
	if cfg.S3Bucket != "" && os.Getenv("TEO_LOGSTORE") != "noop" {
		s3up, err := logstore.NewS3(ctx, cfg.S3Region, cfg.S3Endpoint, cfg.S3Bucket)
		if err != nil {
			logger.Warn("logstore s3 init failed; using noop", "err", err)
			agent.Uploader = logstore.Noop()
		} else {
			agent.Uploader = s3up
			logger.Info("logstore s3 enabled", "bucket", cfg.S3Bucket, "region", cfg.S3Region)
		}
	} else {
		agent.Uploader = logstore.Noop()
	}

	// EC2 spot interruption watcher. Disabled by default outside Linux pods or
	// when TEO_SPOT_WATCH=disabled — IMDS does not exist on dev laptops and
	// the poller would just log connect failures.
	if os.Getenv("TEO_SPOT_WATCH") != "disabled" {
		agent.SpotWatcher = spot.NewWatcher()
		logger.Info("spot interruption watcher enabled (IMDSv2)")
	}

	if nc, js, err := teonats.Connect(cfg.NATSURL); err == nil {
		defer nc.Close()
		if err := teonats.EnsureStreams(ctx, js); err != nil {
			logger.Warn("nats stream setup failed", "err", err)
		} else if err := agent.SubscribeNATS(ctx, js); err != nil {
			logger.Warn("nats subscribe failed", "err", err)
		}
	} else {
		logger.Warn("nats unavailable; using postgres-claim only", "err", err)
	}

	if err := agent.Run(ctx); err != nil && err != context.Canceled {
		logger.Error("worker exited", "err", err)
		os.Exit(1)
	}
}
