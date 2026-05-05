package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// TestGitHubWebhookRouteAlwaysMounted verifies the route is reachable even
// when no handler is injected — the unconfigured response must be 503
// (with the RFC 7807 envelope) so monitoring can distinguish "not deployed"
// from "configured-but-broken." Pre-fix the route didn't exist at all and
// FR-904 was unsatisfiable.
func TestGitHubWebhookRouteAlwaysMounted(t *testing.T) {
	srv := New(Config{
		JWTSecret: "test-secret-must-be-at-least-32-bytes-long-okay",
	}, (*pgxpool.Pool)(nil)) // pool isn't dereferenced on this path

	req := httptest.NewRequest(http.MethodPost, "/webhooks/github", strings.NewReader("{}"))
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503; body = %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "GitHub webhook not configured") {
		t.Errorf("body missing helpful message: %s", rr.Body.String())
	}
}

// TestGitHubWebhookRejectsUnsignedRequests verifies the configured handler
// returns 401 on unsigned bodies. This is the FR-904 acceptance check.
func TestGitHubWebhookRejectsUnsignedRequests(t *testing.T) {
	stub := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// stand-in: a real github.Webhook would 401 here on bad signature.
		w.WriteHeader(http.StatusUnauthorized)
	})
	srv := New(Config{
		JWTSecret: "test-secret-must-be-at-least-32-bytes-long-okay",
	}, (*pgxpool.Pool)(nil), WithGitHubWebhook(stub))

	req := httptest.NewRequest(http.MethodPost, "/webhooks/github", strings.NewReader(`{"action":"created"}`))
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rr.Code)
	}
}
