// Command predictor is a thin health-proxy / launcher front for the predictor.
//
// The always-present Go heuristic predictor runs in-process inside the Run
// Manager (internal/predictor.Heuristic); it is not a standalone service. The
// optional ML predictor is the Python LightGBM service in services/predictor-ml/
// (FastAPI, /v1/predict + /healthz).
//
// This binary's role:
//   - With NO env set, it prints build identity and exits. This no-args path is
//     also used as a smoke test (CLAUDE.md), so nothing may log/serve above it.
//   - With TEO_PREDICTOR_ML_URL set, it starts a small HTTP server that proxies
//     /healthz to the upstream ML service so a single TEO-shaped health endpoint
//     can front the Python service in a sidecar/launcher deployment. It listens
//     on TEO_PREDICTOR_LISTEN (default :8090). This listener is intended to be
//     probed by `teo doctor` when TEO_PREDICTOR_URL is pointed at it; that wiring
//     is deployment-specific and not injected by the chart today.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/teo-dev/teo/internal/version"
)

func main() {
	mlURL := os.Getenv("TEO_PREDICTOR_ML_URL")
	if mlURL == "" {
		// No-args / unconfigured path: print build identity and exit. Used as a
		// smoke test — do not add logging or a server above this line.
		fmt.Println(version.Get("predictor"))
		return
	}

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	v := version.Get("predictor")
	logger.Info("starting predictor health-proxy", "version", v.Version, "commit", v.Commit, "upstream", mlURL)

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	client := &http.Client{Timeout: 2 * time.Second}
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", healthzProxyHandler(client, mlURL))

	listen := os.Getenv("TEO_PREDICTOR_LISTEN")
	if listen == "" {
		listen = ":8090"
	}
	srv := &http.Server{Addr: listen, Handler: mux, ReadHeaderTimeout: 5 * time.Second}

	go func() {
		<-ctx.Done()
		shutCtx, c := context.WithTimeout(context.Background(), 5*time.Second)
		defer c()
		_ = srv.Shutdown(shutCtx)
	}()

	logger.Info("health-proxy listening", "addr", listen)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Error("health-proxy exited", "err", err)
		os.Exit(1)
	}
}

// healthzProxyHandler returns an http.HandlerFunc that proxies /healthz to the
// upstream ML service at mlURL. It returns the upstream's status code when the
// upstream is reachable, 503 when it is not, and 500 for a malformed mlURL.
// Extracted from main() so the proxy behavior is unit-testable against an
// httptest upstream without booting the listener.
func healthzProxyHandler(client *http.Client, mlURL string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, mlURL+"/healthz", nil)
		if err != nil {
			http.Error(w, "bad upstream url", http.StatusInternalServerError)
			return
		}
		resp, err := client.Do(req)
		if err != nil {
			http.Error(w, "upstream unreachable", http.StatusServiceUnavailable)
			return
		}
		defer func() { _ = resp.Body.Close() }()
		w.WriteHeader(resp.StatusCode)
		_, _ = fmt.Fprintf(w, `{"status":"upstream-%d"}`, resp.StatusCode)
	}
}
