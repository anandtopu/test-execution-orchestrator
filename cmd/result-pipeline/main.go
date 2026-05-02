// Command result-pipeline ingests OTLP from workers, enriches, dedupes, and writes
// to Postgres + ClickHouse + S3.
//
// Subcommands (CronJob entry points):
//   result-pipeline                  default: run the OTLP gRPC receiver
//   result-pipeline owner-digest     send the weekly per-author digest
//   result-pipeline flake-recompute  run the nightly Wilson-interval flake job
//   result-pipeline quarantine-sweep run the auto-quarantine sweeper
package main

import (
	"context"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"strconv"
	"syscall"

	"github.com/jackc/pgx/v5/pgxpool"
	collectorpb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"

	"github.com/teo-dev/teo/internal/config"
	"github.com/teo-dev/teo/internal/db"
	"github.com/teo-dev/teo/internal/digest"
	"github.com/teo-dev/teo/internal/flake"
	"github.com/teo-dev/teo/internal/github"
	"github.com/teo-dev/teo/internal/metrics"
	"github.com/teo-dev/teo/internal/quarantine"
	"github.com/teo-dev/teo/internal/resultpipeline"
	"github.com/teo-dev/teo/internal/version"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	v := version.Get("result-pipeline")
	logger.Info("starting", "version", v.Version, "commit", v.Commit)

	cfg := config.LoadCommon()
	if cfg.PostgresDSN == "" {
		logger.Error("TEO_POSTGRES_DSN required")
		os.Exit(1)
	}

	subcommand := ""
	if len(os.Args) > 1 {
		subcommand = os.Args[1]
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	pool, err := db.OpenPostgres(ctx, cfg.PostgresDSN)
	if err != nil {
		logger.Error("postgres open failed", "err", err)
		os.Exit(1)
	}
	defer pool.Close()

	switch subcommand {
	case "", "serve":
		runOTLPServer(ctx, cancel, cfg, pool, logger)
	case "owner-digest":
		runOwnerDigest(ctx, pool, logger)
	case "flake-recompute":
		runFlakeRecompute(ctx, pool, logger)
	case "quarantine-sweep":
		runQuarantineSweep(ctx, pool, logger)
	case "quarantine-sla-sweep":
		runQuarantineSLASweep(ctx, pool, logger)
	case "unquarantine-proposals":
		runUnquarantineProposals(ctx, pool, logger)
	default:
		logger.Error("unknown subcommand", "name", subcommand)
		os.Exit(2)
	}
}

func runOTLPServer(ctx context.Context, cancel context.CancelFunc, cfg config.Common, pool *pgxpool.Pool, logger *slog.Logger) {
	chConn, err := db.OpenClickHouseConn(ctx, cfg.ClickHouseDSN)
	if err != nil {
		logger.Error("clickhouse open failed", "err", err)
		os.Exit(1)
	}
	defer func() { _ = chConn.Close() }()

	reg := metrics.New()
	stopMetrics, err := reg.ServeHTTP(getEnv("TEO_METRICS_LISTEN", ":9100"))
	if err != nil {
		logger.Error("metrics listener failed", "err", err)
		os.Exit(1)
	}
	defer func() { _ = stopMetrics(context.Background()) }()

	cluster := &resultpipeline.Cluster{Pool: pool}
	receiver := &resultpipeline.OTLPReceiver{
		Pool:    pool,
		CH:      chConn,
		Cluster: cluster,
		Logger:  logger,
		Metrics: reg,
	}

	otlpAddr := getEnv("TEO_OTLP_LISTEN", ":4317")
	lis, err := net.Listen("tcp", otlpAddr)
	if err != nil {
		logger.Error("listen otlp", "err", err)
		os.Exit(1)
	}
	server := grpc.NewServer()
	collectorpb.RegisterTraceServiceServer(server, receiver)
	hs := health.NewServer()
	healthpb.RegisterHealthServer(server, hs)
	hs.SetServingStatus("", healthpb.HealthCheckResponse_SERVING)

	go func() {
		logger.Info("OTLP gRPC listening", "addr", otlpAddr)
		if err := server.Serve(lis); err != nil {
			logger.Error("grpc serve failed", "err", err)
			cancel()
		}
	}()

	<-ctx.Done()
	logger.Info("shutdown")
	server.GracefulStop()
}

func runOwnerDigest(ctx context.Context, pool *pgxpool.Pool, logger *slog.Logger) {
	sender := buildDigestSender(logger)
	r := &digest.Runner{Pool: pool, Logger: logger, Sender: sender}
	results, err := r.Run(ctx)
	if err != nil {
		logger.Error("owner-digest failed", "err", err)
		os.Exit(1)
	}
	sent, skipped, errored := 0, 0, 0
	for _, res := range results {
		switch {
		case res.SendError != "":
			errored++
		case res.Skipped:
			skipped++
		default:
			sent++
		}
	}
	logger.Info("owner-digest done", "recipients", len(results), "sent", sent, "skipped", skipped, "errored", errored)
}

func runFlakeRecompute(ctx context.Context, pool *pgxpool.Pool, logger *slog.Logger) {
	job := &flake.Job{Pool: pool, Logger: logger}
	if err := job.Run(ctx); err != nil {
		logger.Error("flake-recompute failed", "err", err)
		os.Exit(1)
	}
}

func runQuarantineSweep(ctx context.Context, pool *pgxpool.Pool, logger *slog.Logger) {
	d := &quarantine.Daemon{Pool: pool, Logger: logger}
	if token := os.Getenv("TEO_GITHUB_TOKEN"); token != "" {
		opener := &quarantine.GitHubOpener{Client: github.NewIssuesClient(token)}
		d.IssueOpener = opener
		d.IssueCommenter = opener
	} else {
		logger.Warn("TEO_GITHUB_TOKEN not set; quarantine sweep will run without issue creation")
	}
	if err := d.Run(ctx); err != nil {
		logger.Error("quarantine-sweep failed", "err", err)
		os.Exit(1)
	}
}

func runQuarantineSLASweep(ctx context.Context, pool *pgxpool.Pool, logger *slog.Logger) {
	token := os.Getenv("TEO_GITHUB_TOKEN")
	if token == "" {
		logger.Error("TEO_GITHUB_TOKEN required for SLA sweep")
		os.Exit(1)
	}
	slaDays, _ := strconv.Atoi(getEnv("TEO_QUARANTINE_SLA_DAYS", "14"))
	s := &quarantine.SLASweeper{
		Pool:      pool,
		Logger:    logger,
		Commenter: &quarantine.GitHubOpener{Client: github.NewIssuesClient(token)},
		SLADays:   slaDays,
	}
	if err := s.Run(ctx); err != nil {
		logger.Error("quarantine-sla-sweep failed", "err", err)
		os.Exit(1)
	}
}

func runUnquarantineProposals(ctx context.Context, pool *pgxpool.Pool, logger *slog.Logger) {
	token := os.Getenv("TEO_GITHUB_TOKEN")
	if token == "" {
		logger.Error("TEO_GITHUB_TOKEN required for un-quarantine proposal sweep")
		os.Exit(1)
	}
	window, _ := strconv.Atoi(getEnv("TEO_UNQUARANTINE_WINDOW", "30"))
	baseURL := getEnv("TEO_BASE_URL", "https://teo.example.com")
	p := &quarantine.UnquarantineProposer{
		Pool:                  pool,
		Logger:                logger,
		Commenter:             &quarantine.GitHubOpener{Client: github.NewIssuesClient(token)},
		ConsecutivePassWindow: window,
		BaseURL:               baseURL,
	}
	if err := p.Run(ctx); err != nil {
		logger.Error("unquarantine-proposals failed", "err", err)
		os.Exit(1)
	}
}

func buildDigestSender(logger *slog.Logger) digest.Sender {
	var senders []digest.Sender
	host := os.Getenv("TEO_SMTP_HOST")
	if host != "" {
		port, _ := strconv.Atoi(getEnv("TEO_SMTP_PORT", "587"))
		senders = append(senders, digest.NewSMTPSender(
			host, port,
			getEnv("TEO_SMTP_FROM", "teo@example.com"),
			os.Getenv("TEO_SMTP_USERNAME"),
			os.Getenv("TEO_SMTP_PASSWORD"),
		))
	}
	if hook := os.Getenv("TEO_SLACK_WEBHOOK_URL"); hook != "" {
		senders = append(senders, digest.NewSlackSender(hook))
	}
	if len(senders) == 0 {
		logger.Warn("no digest sender configured; running in dry mode (rendering only)")
		return nil
	}
	if len(senders) == 1 {
		return senders[0]
	}
	return &digest.Multiplex{
		Senders: senders,
		OnError: func(name string, err error) {
			logger.Warn("digest sender failed", "sender", name, "err", err)
		},
	}
}

func getEnv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
