// Command api is the TEO API gateway.
package main

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"google.golang.org/grpc"
	_ "google.golang.org/grpc/encoding/proto"

	"github.com/teo-dev/teo/internal/api"
	"github.com/teo-dev/teo/internal/audit"
	"github.com/teo-dev/teo/internal/config"
	"github.com/teo-dev/teo/internal/db"
	teogithub "github.com/teo-dev/teo/internal/github"
	"github.com/teo-dev/teo/internal/grpcsvc"
	"github.com/teo-dev/teo/internal/resultpipeline"
	"github.com/teo-dev/teo/internal/version"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	v := version.Get("api")
	logger.Info("starting", "version", v.Version, "commit", v.Commit, "go", v.GoVersion)

	cfg := config.LoadCommon()
	if cfg.PostgresDSN == "" {
		logger.Error("TEO_POSTGRES_DSN is required")
		os.Exit(1)
	}
	if cfg.JWTSecret == "" {
		logger.Error("TEO_JWT_SECRET is required")
		os.Exit(1)
	}
	// HS256 best-practice: secret must be at least as long as the hash output
	// (32 bytes for SHA-256). Shorter secrets reduce HMAC strength and the
	// blame-radius of a leaked log/env dump.
	if len(cfg.JWTSecret) < 32 {
		logger.Error("TEO_JWT_SECRET must be at least 32 bytes", "len", len(cfg.JWTSecret))
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

	// ClickHouse is optional here: the API gateway only uses it for the
	// OTLP run-export path. When unset, /api/v1/runs/{id}/export?format=otlp
	// returns 501 and JUnit export still works.
	var apiOpts []api.Option
	if cfg.ClickHouseDSN != "" {
		chConn, err := db.OpenClickHouseConn(ctx, cfg.ClickHouseDSN)
		if err != nil {
			logger.Error("clickhouse open failed", "err", err)
			os.Exit(1)
		}
		defer chConn.Close()
		apiOpts = append(apiOpts, api.WithClickHouseConn(chConn))
	}

	// GitHub webhook receiver (FR-901..904). Only mounted when the secret is
	// configured; without it, /webhooks/github returns 503 instead of
	// silently accepting unverified traffic.
	if cfg.GitHubWebhookSecret != "" {
		hook := &teogithub.Webhook{
			Pool:   pool,
			Logger: logger,
			Secret: []byte(cfg.GitHubWebhookSecret),
		}
		apiOpts = append(apiOpts, api.WithGitHubWebhook(hook))
		logger.Info("github webhook enabled", "path", "/webhooks/github")
	} else {
		logger.Warn("TEO_GITHUB_WEBHOOK_SECRET not set; /webhooks/github will return 503")
	}

	srv := api.New(api.Config{
		JWTSecret: cfg.JWTSecret,
		JWTTTL:    cfg.JWTTTL,
	}, pool, apiOpts...)

	httpSrv := &http.Server{
		Addr:              cfg.HTTPListenAddr,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}

	// gRPC server for high-throughput worker traffic.
	grpcLis, err := net.Listen("tcp", cfg.GRPCListenAddr)
	if err != nil {
		logger.Error("grpc listen failed", "err", err)
		os.Exit(1)
	}
	grpcSrv := grpc.NewServer()
	grpcsvc.Register(grpcSrv, &grpcsvc.WorkersService{
		Pool:    pool,
		Audit:   &audit.Logger{Pool: pool},
		Cluster: &resultpipeline.Cluster{Pool: pool},
	})

	go func() {
		logger.Info("http listening", "addr", cfg.HTTPListenAddr)
		if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("http server failed", "err", err)
			cancel()
		}
	}()
	go func() {
		logger.Info("grpc listening", "addr", cfg.GRPCListenAddr)
		if err := grpcSrv.Serve(grpcLis); err != nil {
			logger.Error("grpc server failed", "err", err)
			cancel()
		}
	}()

	<-ctx.Done()
	logger.Info("shutdown")
	grpcSrv.GracefulStop()
	shutCtx, shutCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutCancel()
	_ = httpSrv.Shutdown(shutCtx)
}
