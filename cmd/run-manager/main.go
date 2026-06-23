// Command run-manager drives runs through their state machine.
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/teo-dev/teo/internal/config"
	"github.com/teo-dev/teo/internal/db"
	"github.com/teo-dev/teo/internal/github"
	"github.com/teo-dev/teo/internal/logstore"
	"github.com/teo-dev/teo/internal/metrics"
	teonats "github.com/teo-dev/teo/internal/nats"
	"github.com/teo-dev/teo/internal/predictor"
	"github.com/teo-dev/teo/internal/runmanager"
	"github.com/teo-dev/teo/internal/version"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	v := version.Get("run-manager")
	logger.Info("starting", "version", v.Version, "commit", v.Commit)

	cfg := config.LoadCommon()
	if cfg.PostgresDSN == "" {
		logger.Error("TEO_POSTGRES_DSN required")
		os.Exit(1)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	pool, err := db.OpenPostgres(ctx, cfg.PostgresDSN)
	if err != nil {
		logger.Error("postgres open failed", "err", err)
		os.Exit(1)
	}
	defer pool.Close()

	reg := metrics.New()
	stopMetrics, err := reg.ServeHTTP(getEnv("TEO_METRICS_LISTEN", ":9100"))
	if err != nil {
		logger.Error("metrics listener failed", "err", err)
		os.Exit(1)
	}
	defer func() {
		shutCtx, c := context.WithTimeout(context.Background(), 5*time.Second)
		defer c()
		_ = stopMetrics(shutCtx)
	}()

	// Predictor: the Go heuristic is always present (ADR-0019). When
	// TEO_PREDICTOR_ML_URL is set, wrap it in a Fallback that tries the Python
	// LightGBM service first and transparently reverts to the heuristic on any
	// failure (timeout / connection error / non-200 / decode / length mismatch),
	// incrementing teo_predictor_fallback_total. Nothing hard-fails if the ML
	// service is down — the heuristic carries the load.
	heuristic := predictor.NewHeuristic(pool)
	var pred predictor.Predictor = heuristic
	if cfg.PredictorMLURL != "" {
		ml := predictor.NewMLClient(cfg.PredictorMLURL, cfg.PredictorMLTimeout)
		ml.Logger = logger
		// Server-side cold-start (200 + used_fallback=true) is invisible to the
		// Fallback wrapper, so count it separately to surface MAE-drift degradation.
		ml.OnServerColdStart = reg.PredictorServerColdStart.Inc
		fb := predictor.NewFallback(ml, heuristic, logger)
		fb.OnFallback = reg.PredictorFallback.Inc
		pred = fb
		logger.Info("ml predictor enabled with heuristic fallback",
			"url", cfg.PredictorMLURL, "timeout", cfg.PredictorMLTimeout)
	} else {
		logger.Info("TEO_PREDICTOR_ML_URL not set; using heuristic predictor")
	}

	mgr := &runmanager.Manager{
		Pool:                pool,
		Predictor:           pred,
		Logger:              logger,
		Metrics:             reg,
		PollInterval:        time.Second,
		BudgetCheckInterval: 5 * time.Second,
	}

	// Durable S3 archive of each run's assignment plan (S-05-04 AC1). Gated on
	// TEO_S3_BUCKET being explicitly set (config defaults the bucket name). The
	// plan still lives in Postgres, so this is best-effort.
	if os.Getenv("TEO_S3_BUCKET") != "" {
		store, err := logstore.NewS3(ctx, cfg.S3Region, cfg.S3Endpoint, cfg.S3Bucket)
		if err != nil {
			logger.Error("logstore init failed", "err", err)
			os.Exit(1)
		}
		mgr.PlanStore = store
		logger.Info("plan archive enabled", "bucket", cfg.S3Bucket)
	} else {
		logger.Warn("TEO_S3_BUCKET not set; assignment plans won't be archived to S3")
	}

	// NATS is best-effort; falling back to Postgres claim if unavailable.
	if nc, js, err := teonats.Connect(cfg.NATSURL); err == nil {
		defer nc.Close()
		if err := teonats.EnsureStreams(ctx, js); err != nil {
			logger.Warn("nats stream setup failed", "err", err)
		} else {
			mgr.JS = js
			logger.Info("nats connected", "url", cfg.NATSURL)
		}
		// Publish best-effort UI hints on every run transition so API gateways
		// can push live updates over GraphQL subscriptions (FR-706, S-09-02).
		// Core-NATS, independent of the JetStream dispatch stream above.
		mgr.Observers = append(mgr.Observers, &runmanager.UINotifyObserver{Conn: nc, Logger: logger})
	} else {
		logger.Warn("nats unavailable; using postgres-only dispatch", "err", err)
	}

	// GitHub Check Run observer — only if a token is configured. Without one
	// the run manager continues without GitHub-side effects.
	if token := os.Getenv("TEO_GITHUB_TOKEN"); token != "" {
		baseURL := os.Getenv("TEO_BASE_URL")
		if baseURL == "" {
			baseURL = "https://teo.example.com"
		}
		mgr.Observers = append(mgr.Observers, &github.CheckObserver{
			Pool:    pool,
			Logger:  logger,
			Client:  github.NewCheckClient(token),
			BaseURL: baseURL,
			AppName: getEnv("TEO_GITHUB_CHECK_NAME", "TEO"),
		})
		logger.Info("github check-run observer enabled")
	} else {
		logger.Info("TEO_GITHUB_TOKEN not set; check-run observer disabled")
	}
	if err := mgr.Run(ctx); err != nil && err != context.Canceled {
		logger.Error("manager exited", "err", err)
		os.Exit(1)
	}
}

func getEnv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
