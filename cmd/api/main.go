// Command api is the TEO API gateway.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"google.golang.org/grpc"
	_ "google.golang.org/grpc/encoding/proto"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/teo-dev/teo/internal/api"
	"github.com/teo-dev/teo/internal/audit"
	"github.com/teo-dev/teo/internal/auth"
	"github.com/teo-dev/teo/internal/config"
	"github.com/teo-dev/teo/internal/db"
	teogithub "github.com/teo-dev/teo/internal/github"
	"github.com/teo-dev/teo/internal/grpcsvc"
	"github.com/teo-dev/teo/internal/logstore"
	"github.com/teo-dev/teo/internal/oidc"
	"github.com/teo-dev/teo/internal/resultpipeline"
	"github.com/teo-dev/teo/internal/runsvc"
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

	// Per-test log retrieval (S-09-03 / FR-703-704). Gated on TEO_S3_BUCKET
	// being explicitly set — config defaults the bucket name, so checking the
	// raw env (as the worker does) is what tells us logs are actually stored.
	// Without it, /api/v1/runs/{id}/tests/{execId}/log returns 501.
	if os.Getenv("TEO_S3_BUCKET") != "" {
		store, err := logstore.NewS3(ctx, cfg.S3Region, cfg.S3Endpoint, cfg.S3Bucket)
		if err != nil {
			logger.Error("logstore init failed", "err", err)
			os.Exit(1)
		}
		apiOpts = append(apiOpts, api.WithLogPresigner(store))
		logger.Info("per-test log endpoint enabled", "bucket", cfg.S3Bucket)
	} else {
		logger.Warn("TEO_S3_BUCKET not set; per-test log endpoint will return 501")
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

	// OIDC sign-in (FR-801, S-03-02). Enabled when issuer + client id are set.
	// Discovery happens at startup; a misconfigured issuer fails fast rather
	// than 500ing on the first sign-in attempt.
	if cfg.OIDCIssuer != "" && cfg.OIDCClientID != "" {
		// The redirect_uri must point at the API's own /auth/callback. In the
		// standard same-origin deployment (UI + API behind one host, /auth/*
		// proxied to the API) it can be derived from UIBaseURL; otherwise the
		// operator sets TEO_OIDC_REDIRECT_URL explicitly to match the IdP.
		redirect := cfg.OIDCRedirectURL
		if redirect == "" && cfg.UIBaseURL != "" {
			redirect = strings.TrimRight(cfg.UIBaseURL, "/") + "/auth/callback"
		}
		provider, err := oidc.NewProvider(ctx, oidc.Config{
			IssuerURL:    cfg.OIDCIssuer,
			ClientID:     cfg.OIDCClientID,
			ClientSecret: cfg.OIDCClientSecret,
			RedirectURL:  redirect,
		}, nil)
		if err != nil {
			logger.Error("oidc discovery failed", "issuer", cfg.OIDCIssuer, "err", err)
			os.Exit(1)
		}
		apiOpts = append(apiOpts,
			api.WithOIDC(provider, cfg.UIBaseURL),
			api.WithRoleResolver(api.DBRoleResolver(pool)),
		)
		logger.Info("oidc sign-in enabled", "issuer", cfg.OIDCIssuer, "redirect", redirect)
	} else {
		logger.Warn("TEO_OIDC_ISSUER/CLIENT_ID not set; /auth/* sign-in routes will return 503")
	}

	// Shared run-intake service: one instance backs both the HTTP/GraphQL
	// gateway and the gRPC Runs service so the two transports share one code
	// path (CreateRun/GetRun/CancelRun).
	runSvc := &runsvc.Service{Pool: pool, Audit: &audit.Logger{Pool: pool}}
	apiOpts = append(apiOpts, api.WithRunService(runSvc))

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
	// The Runs gRPC RPCs are state-mutating (create/cancel runs), so — unlike
	// the internal worker-dispatch RPCs — they MUST be authenticated. Install a
	// unary interceptor that validates the same JWT/API-key credentials the HTTP
	// middleware accepts and rejects unauthenticated callers on the Runs methods
	// with codes.Unauthenticated. The Workers RPCs remain open (internal traffic).
	jwtIssuer := &auth.JWTIssuer{
		Secret: []byte(cfg.JWTSecret),
		TTL:    cfg.JWTTTL,
		Issuer: "teo",
	}
	grpcSrv := grpc.NewServer(
		grpc.UnaryInterceptor(grpcsvc.AuthUnaryInterceptor(jwtIssuer, grpcAPIKeyResolver(pool))),
	)
	grpcsvc.Register(grpcSrv, &grpcsvc.WorkersService{
		Pool:    pool,
		Audit:   &audit.Logger{Pool: pool},
		Cluster: &resultpipeline.Cluster{Pool: pool},
	})
	// Runs gRPC service (CreateRun/GetRun/CancelRun) over the same shared
	// run-intake service as HTTP. Auth is enforced by AuthUnaryInterceptor above.
	grpcsvc.RegisterRuns(grpcSrv, &grpcsvc.RunsService{Svc: runSvc})

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

// grpcAPIKeyResolver returns an auth.Resolver that verifies a teo_* API key
// against teo.api_keys, mirroring the HTTP gateway's resolveAPIKey (minus the
// in-process cache, which is owned by the HTTP server). Used by the gRPC auth
// interceptor so CI clients can authenticate run RPCs with an API key.
func grpcAPIKeyResolver(pool *pgxpool.Pool) auth.Resolver {
	return func(ctx context.Context, prefix, display string) (*auth.Principal, error) {
		var (
			id     string
			hash   string
			scopes []string
		)
		err := pool.QueryRow(ctx, `
            SELECT id, hash, scopes FROM teo.api_keys
            WHERE prefix = $1 AND revoked_at IS NULL
              AND (expires_at IS NULL OR expires_at > now())
        `, prefix).Scan(&id, &hash, &scopes)
		if err != nil {
			return nil, err
		}
		if _, ok := auth.VerifyAPIKey(display, hash); !ok {
			return nil, errors.New("api key verification failed")
		}
		return &auth.Principal{
			APIKeyID: id,
			Roles:    []auth.Role{auth.RoleEngineer},
			Scopes:   scopes,
			IsAPIKey: true,
		}, nil
	}
}
