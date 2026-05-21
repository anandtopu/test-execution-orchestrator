package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/teo-dev/teo/internal/auth"
	"github.com/teo-dev/teo/internal/oidc"
)

type fakeOIDC struct {
	lastState string
	lastNonce string
	claims    *oidc.IDClaims
	verifyErr error
}

func (f *fakeOIDC) AuthCodeURL(state, nonce string) string {
	f.lastState, f.lastNonce = state, nonce
	return "https://idp.example/auth?state=" + state + "&nonce=" + nonce
}
func (f *fakeOIDC) Exchange(_ context.Context, _ string) (string, error) { return "raw-id-token", nil }
func (f *fakeOIDC) VerifyIDToken(_ context.Context, _, nonce string) (*oidc.IDClaims, error) {
	if f.verifyErr != nil {
		return nil, f.verifyErr
	}
	f.lastNonce = nonce
	return f.claims, nil
}

func newOIDCServer(fake oidcAuthenticator) *Server {
	return New(Config{JWTSecret: strings.Repeat("x", 32), JWTTTL: time.Hour}, nil,
		WithOIDC(fake, "https://ui.example"))
}

func cookieByName(cks []*http.Cookie, name string) *http.Cookie {
	for _, c := range cks {
		if c.Name == name {
			return c
		}
	}
	return nil
}

func TestOIDCDisabledReturns503(t *testing.T) {
	s := New(Config{JWTSecret: strings.Repeat("x", 32)}, nil) // no WithOIDC
	rec := httptest.NewRecorder()
	s.oidcLogin(rec, httptest.NewRequest(http.MethodGet, "/auth/login", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("want 503, got %d", rec.Code)
	}
}

func TestOIDCLoginRedirectsAndSetsCookies(t *testing.T) {
	fake := &fakeOIDC{}
	s := newOIDCServer(fake)

	rec := httptest.NewRecorder()
	s.oidcLogin(rec, httptest.NewRequest(http.MethodGet, "/auth/login", nil))

	if rec.Code != http.StatusFound {
		t.Fatalf("want 302, got %d", rec.Code)
	}
	loc := rec.Header().Get("Location")
	if !strings.HasPrefix(loc, "https://idp.example/auth?") {
		t.Fatalf("unexpected redirect: %s", loc)
	}
	cks := rec.Result().Cookies()
	if cookieByName(cks, stateCookie) == nil || cookieByName(cks, nonceCookie) == nil {
		t.Fatal("login must set state and nonce cookies")
	}
	if !strings.Contains(loc, "state="+fake.lastState) {
		t.Fatal("redirect state must match the cookie state")
	}
}

func TestOIDCCallbackIssuesSession(t *testing.T) {
	fake := &fakeOIDC{claims: &oidc.IDClaims{Subject: "alice", Email: "alice@example.com", EmailVerified: true}}
	s := newOIDCServer(fake)

	// 1. Login to obtain valid state/nonce cookies.
	loginRec := httptest.NewRecorder()
	s.oidcLogin(loginRec, httptest.NewRequest(http.MethodGet, "/auth/login", nil))
	state := cookieByName(loginRec.Result().Cookies(), stateCookie)

	// 2. Callback with the matching state + cookies.
	req := httptest.NewRequest(http.MethodGet, "/auth/callback?code=abc&state="+state.Value, nil)
	for _, c := range loginRec.Result().Cookies() {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	s.oidcCallback(rec, req)

	if rec.Code != http.StatusFound {
		t.Fatalf("want 302, got %d (%s)", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Location"); got != "https://ui.example" {
		t.Fatalf("want redirect to UI, got %s", got)
	}
	sess := cookieByName(rec.Result().Cookies(), auth.SessionCookie)
	if sess == nil || sess.Value == "" {
		t.Fatal("callback must set the session cookie")
	}
	if !sess.HttpOnly || !sess.Secure {
		t.Fatal("session cookie must be HttpOnly and Secure for an https UI")
	}
	// The cookie must be a valid TEO JWT carrying the OIDC identity.
	p, err := s.jwt.Verify(sess.Value)
	if err != nil {
		t.Fatalf("session cookie is not a valid JWT: %v", err)
	}
	if p.UserID != "alice" || p.Email != "alice@example.com" {
		t.Fatalf("JWT principal mismatch: %+v", p)
	}
}

func TestOIDCCallbackRejectsStateMismatch(t *testing.T) {
	fake := &fakeOIDC{claims: &oidc.IDClaims{Subject: "alice"}}
	s := newOIDCServer(fake)
	req := httptest.NewRequest(http.MethodGet, "/auth/callback?code=abc&state=attacker", nil)
	req.AddCookie(&http.Cookie{Name: stateCookie, Value: "legit"})
	rec := httptest.NewRecorder()
	s.oidcCallback(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("want 400 on state mismatch, got %d", rec.Code)
	}
}

func TestSessionAndRefresh(t *testing.T) {
	s := newOIDCServer(&fakeOIDC{})
	token, err := s.jwt.Issue("bob", "bob@example.com", []auth.Role{auth.RoleEngineer})
	if err != nil {
		t.Fatal(err)
	}

	// /auth/session and /auth/refresh read the principal the middleware sets
	// from the cookie, so exercise them through the full handler chain.
	h := s.Handler()

	sessReq := httptest.NewRequest(http.MethodGet, "/auth/session", nil)
	sessReq.AddCookie(&http.Cookie{Name: auth.SessionCookie, Value: token})
	sessRec := httptest.NewRecorder()
	h.ServeHTTP(sessRec, sessReq)
	if sessRec.Code != http.StatusOK || !strings.Contains(sessRec.Body.String(), "bob@example.com") {
		t.Fatalf("session: code=%d body=%s", sessRec.Code, sessRec.Body.String())
	}

	refReq := httptest.NewRequest(http.MethodPost, "/auth/refresh", nil)
	refReq.AddCookie(&http.Cookie{Name: auth.SessionCookie, Value: token})
	refRec := httptest.NewRecorder()
	h.ServeHTTP(refRec, refReq)
	if refRec.Code != http.StatusOK {
		t.Fatalf("refresh: code=%d body=%s", refRec.Code, refRec.Body.String())
	}
	if cookieByName(refRec.Result().Cookies(), auth.SessionCookie) == nil {
		t.Fatal("refresh must re-set the session cookie")
	}
}

func TestSessionUnauthenticated(t *testing.T) {
	s := newOIDCServer(&fakeOIDC{})
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/auth/session", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", rec.Code)
	}
}
