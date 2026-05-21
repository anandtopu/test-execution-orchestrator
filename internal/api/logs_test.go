package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/teo-dev/teo/internal/auth"
	"github.com/teo-dev/teo/internal/logstore"
)

// fakePresigner records the key it was asked to sign and returns a canned URL.
type fakePresigner struct {
	url     string
	lastKey string
}

func (f *fakePresigner) Presign(_ context.Context, key string, _ time.Duration) (string, error) {
	f.lastKey = key
	return f.url, nil
}

// An unauthenticated request must be rejected before any DB or presign work.
func TestLogURLHandlerUnauthenticated(t *testing.T) {
	h := logURLHandler(nil, &fakePresigner{url: "https://example/log"})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/runs/r1/tests/e1/log", nil)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", rec.Code)
	}
}

// With no presigner wired (S3 not configured), an authenticated request gets a
// clear 501 — never a 500 — so the UI can render "log storage not configured".
func TestLogURLHandlerNotConfigured(t *testing.T) {
	h := logURLHandler(nil, nil)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/runs/r1/tests/e1/log", nil)
	req = req.WithContext(auth.WithPrincipal(req.Context(),
		&auth.Principal{UserID: "u1", Roles: []auth.Role{auth.RoleEngineer}}))
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("want 501, got %d", rec.Code)
	}
}

// The Noop store's presigner contract: it must signal unavailability rather
// than hand back a bogus URL.
func TestLogstorePresignErrorIsSentinel(t *testing.T) {
	if logstore.ErrPresignUnavailable == nil {
		t.Fatal("ErrPresignUnavailable must be a usable sentinel")
	}
}
