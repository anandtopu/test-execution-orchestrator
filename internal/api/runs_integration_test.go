//go:build integration

package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/teo-dev/teo/internal/auth"
	"github.com/teo-dev/teo/internal/testpg"
)

const testJWTSecret = "test-secret-must-be-at-least-32-bytes-long-okay"

// signedRequest builds an HTTP request with a Bearer JWT issued by the same
// secret the test server uses. Without this, every request is anonymous and
// the runs handler returns 401 — which is what TestPOSTRunsRequires401 asserts,
// but every other test needs a valid principal.
func signedRequest(t *testing.T, method, path string, body []byte) *http.Request {
	t.Helper()
	issuer := &auth.JWTIssuer{
		Secret: []byte(testJWTSecret),
		TTL:    time.Hour,
		Issuer: "teo",
	}
	tok, err := issuer.Issue(uuid.New().String(), "tester@example.com", []auth.Role{auth.RoleEngineer})
	if err != nil {
		t.Fatalf("issue jwt: %v", err)
	}
	var b *bytes.Reader
	if body != nil {
		b = bytes.NewReader(body)
	}
	var req *http.Request
	if b != nil {
		req = httptest.NewRequest(method, path, b)
	} else {
		req = httptest.NewRequest(method, path, nil)
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", "application/json")
	return req
}

// seedRepo registers a repo in teo.repos so POST /runs can find it. Returns
// the repo id and full name.
func seedRepo(t *testing.T, pool *pgxpool.Pool) (string, string) {
	t.Helper()
	id := uuid.New().String()
	full := "owner/runs-test"
	mustExec(t, pool,
		`INSERT INTO teo.repos (id, vcs, full_name) VALUES ($1, 'github', $2)`, id, full)
	return id, full
}

func newTestServer(pool *pgxpool.Pool) *Server {
	return New(Config{
		JWTSecret: testJWTSecret,
		JWTTTL:    time.Hour,
	}, pool)
}

func validCreateRunBody(repoFull string) []byte {
	body, _ := json.Marshal(map[string]any{
		"repo_full_name": repoFull,
		"commit_sha":     "abc123def",
		"branch":         "main",
		"manifest": map[string]any{
			"runner": "pytest",
			"tests": []map[string]any{
				{"path": "tests/test_a.py", "name": "test_one"},
				{"path": "tests/test_a.py", "name": "test_two"},
			},
		},
	})
	return body
}

func TestPOSTRunsCreatesRow(t *testing.T) {
	pool, cleanup := testpg.Start(t)
	t.Cleanup(cleanup)
	_, full := seedRepo(t, pool)

	rr := httptest.NewRecorder()
	req := signedRequest(t, http.MethodPost, "/api/v1/runs", validCreateRunBody(full))
	newTestServer(pool).Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}
	var got map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got["status"] != "pending" {
		t.Errorf("status = %v, want pending", got["status"])
	}
	if got["repo_full_name"] != full {
		t.Errorf("repo_full_name = %v", got["repo_full_name"])
	}

	// Run plan should have been persisted alongside the run.
	var planCount int
	err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM teo.run_plans WHERE run_id = $1`, got["id"]).Scan(&planCount)
	if err != nil || planCount != 1 {
		t.Errorf("run_plans count = %d (err=%v); want 1", planCount, err)
	}
}

func TestPOSTRunsRequires401WithoutAuth(t *testing.T) {
	pool, cleanup := testpg.Start(t)
	t.Cleanup(cleanup)
	_, full := seedRepo(t, pool)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/runs",
		bytes.NewReader(validCreateRunBody(full)))
	req.Header.Set("Content-Type", "application/json")
	// No Authorization header.
	newTestServer(pool).Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rr.Code)
	}
}

func TestPOSTRunsValidationErrors(t *testing.T) {
	pool, cleanup := testpg.Start(t)
	t.Cleanup(cleanup)
	seedRepo(t, pool)

	body, _ := json.Marshal(map[string]any{
		// repo_full_name omitted, manifest empty → multiple field errors
		"commit_sha": "abc",
		"branch":     "main",
		"manifest":   map[string]any{},
	})
	rr := httptest.NewRecorder()
	req := signedRequest(t, http.MethodPost, "/api/v1/runs", body)
	newTestServer(pool).Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "repo_full_name") {
		t.Errorf("expected validation error on repo_full_name; got %s", rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "manifest.runner") {
		t.Errorf("expected validation error on manifest.runner; got %s", rr.Body.String())
	}
}

func TestPOSTRunsReturns404ForUnknownRepo(t *testing.T) {
	pool, cleanup := testpg.Start(t)
	t.Cleanup(cleanup)
	// No repo seeded.

	rr := httptest.NewRecorder()
	req := signedRequest(t, http.MethodPost, "/api/v1/runs", validCreateRunBody("ghost/not-real"))
	newTestServer(pool).Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 (body=%s)", rr.Code, rr.Body.String())
	}
}

func TestPOSTRunsIdempotencyKeyReturnsSameRun(t *testing.T) {
	pool, cleanup := testpg.Start(t)
	t.Cleanup(cleanup)
	_, full := seedRepo(t, pool)
	srv := newTestServer(pool)

	body := validCreateRunBody(full)

	rr1 := httptest.NewRecorder()
	req1 := signedRequest(t, http.MethodPost, "/api/v1/runs", body)
	req1.Header.Set("Idempotency-Key", "client-key-001")
	srv.Handler().ServeHTTP(rr1, req1)
	if rr1.Code != http.StatusCreated {
		t.Fatalf("first call status = %d, body = %s", rr1.Code, rr1.Body.String())
	}
	var first map[string]any
	_ = json.Unmarshal(rr1.Body.Bytes(), &first)

	rr2 := httptest.NewRecorder()
	req2 := signedRequest(t, http.MethodPost, "/api/v1/runs", body)
	req2.Header.Set("Idempotency-Key", "client-key-001")
	srv.Handler().ServeHTTP(rr2, req2)
	// runs.go returns 200 (not 201) on idempotent replay.
	if rr2.Code != http.StatusOK {
		t.Fatalf("second call status = %d, want 200; body = %s", rr2.Code, rr2.Body.String())
	}
	var second map[string]any
	_ = json.Unmarshal(rr2.Body.Bytes(), &second)
	if first["id"] != second["id"] {
		t.Errorf("idempotency-key replay returned different id: %v vs %v",
			first["id"], second["id"])
	}

	// And only one row should exist.
	var n int
	_ = pool.QueryRow(context.Background(),
		`SELECT count(*) FROM teo.runs WHERE meta->>'idempotency_key' = $1`,
		"client-key-001").Scan(&n)
	if n != 1 {
		t.Errorf("runs count for idempotency key = %d, want 1", n)
	}
}

// TestPOSTRunsIdempotencyKeyDifferentCommitConflicts locks in the C3 fix:
// reusing the same Idempotency-Key for a *different* commit must not silently
// return the first run. The handler now returns 409.
func TestPOSTRunsIdempotencyKeyDifferentCommitConflicts(t *testing.T) {
	pool, cleanup := testpg.Start(t)
	t.Cleanup(cleanup)
	_, full := seedRepo(t, pool)
	srv := newTestServer(pool)

	rr1 := httptest.NewRecorder()
	req1 := signedRequest(t, http.MethodPost, "/api/v1/runs", validCreateRunBody(full))
	req1.Header.Set("Idempotency-Key", "client-key-conflict")
	srv.Handler().ServeHTTP(rr1, req1)
	if rr1.Code != http.StatusCreated {
		t.Fatalf("first call status = %d, body = %s", rr1.Code, rr1.Body.String())
	}

	// Same key, different commit_sha — must 409.
	body2, _ := json.Marshal(map[string]any{
		"repo_full_name": full,
		"commit_sha":     "different-commit-sha",
		"branch":         "main",
		"manifest": map[string]any{
			"runner": "pytest",
			"tests":  []map[string]any{{"path": "p", "name": "n"}},
		},
	})
	rr2 := httptest.NewRecorder()
	req2 := signedRequest(t, http.MethodPost, "/api/v1/runs", body2)
	req2.Header.Set("Idempotency-Key", "client-key-conflict")
	srv.Handler().ServeHTTP(rr2, req2)
	if rr2.Code != http.StatusConflict {
		t.Fatalf("second call status = %d, want 409, body = %s", rr2.Code, rr2.Body.String())
	}

	// Only one run should exist for the key.
	var n int
	_ = pool.QueryRow(context.Background(),
		`SELECT count(*) FROM teo.runs WHERE meta->>'idempotency_key' = $1`,
		"client-key-conflict").Scan(&n)
	if n != 1 {
		t.Errorf("runs count for idempotency key = %d, want 1", n)
	}
}

func TestGETRunByIDHappyPath(t *testing.T) {
	pool, cleanup := testpg.Start(t)
	t.Cleanup(cleanup)
	_, full := seedRepo(t, pool)
	srv := newTestServer(pool)

	// Create via the API to avoid duplicating insert SQL.
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr,
		signedRequest(t, http.MethodPost, "/api/v1/runs", validCreateRunBody(full)))
	var created map[string]any
	_ = json.Unmarshal(rr.Body.Bytes(), &created)
	id := created["id"].(string)

	rr2 := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr2,
		signedRequest(t, http.MethodGet, "/api/v1/runs/"+id, nil))
	if rr2.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rr2.Code, rr2.Body.String())
	}
	var got map[string]any
	_ = json.Unmarshal(rr2.Body.Bytes(), &got)
	if got["id"] != id || got["repo_full_name"] != full {
		t.Errorf("GET round-trip mismatch: %+v", got)
	}
}

func TestGETRunByIDNotFound(t *testing.T) {
	pool, cleanup := testpg.Start(t)
	t.Cleanup(cleanup)

	rr := httptest.NewRecorder()
	newTestServer(pool).Handler().ServeHTTP(rr,
		signedRequest(t, http.MethodGet,
			"/api/v1/runs/00000000-0000-0000-0000-000000000000", nil))
	if rr.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 (body=%s)", rr.Code, rr.Body.String())
	}
}

func TestGETRunByIDRequires401WithoutAuth(t *testing.T) {
	pool, cleanup := testpg.Start(t)
	t.Cleanup(cleanup)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet,
		"/api/v1/runs/"+uuid.New().String(), nil)
	newTestServer(pool).Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rr.Code)
	}
}

func TestPOSTRunsCancelTransitionsRunningToCancelled(t *testing.T) {
	pool, cleanup := testpg.Start(t)
	t.Cleanup(cleanup)
	repoID, _ := seedRepo(t, pool)
	srv := newTestServer(pool)

	// Insert a run already in 'running' so cancel has work to do.
	id := uuid.New().String()
	mustExec(t, pool, `
        INSERT INTO teo.runs (id, repo_id, commit_sha, branch, triggered_by, status, started_at)
        VALUES ($1, $2, 'cafe', 'main', 'api', 'running', now())
    `, id, repoID)

	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr,
		signedRequest(t, http.MethodPost, "/api/v1/runs/"+id+"/cancel", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}
	var status string
	_ = pool.QueryRow(context.Background(),
		`SELECT status FROM teo.runs WHERE id = $1`, id).Scan(&status)
	if status != "cancelled" {
		t.Errorf("DB status = %q, want cancelled", status)
	}
}

func TestPOSTRunsCancelIsIdempotentOnTerminalRun(t *testing.T) {
	pool, cleanup := testpg.Start(t)
	t.Cleanup(cleanup)
	repoID, _ := seedRepo(t, pool)
	srv := newTestServer(pool)

	// Run already in a terminal state — cancel should return the run unchanged.
	id := uuid.New().String()
	mustExec(t, pool, `
        INSERT INTO teo.runs (id, repo_id, commit_sha, branch, triggered_by, status,
                              started_at, finished_at)
        VALUES ($1, $2, 'cafe', 'main', 'api', 'succeeded',
                now() - interval '5 minutes', now() - interval '4 minutes')
    `, id, repoID)

	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr,
		signedRequest(t, http.MethodPost, "/api/v1/runs/"+id+"/cancel", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}
	var status string
	_ = pool.QueryRow(context.Background(),
		`SELECT status FROM teo.runs WHERE id = $1`, id).Scan(&status)
	if status != "succeeded" {
		t.Errorf("status changed by cancel on terminal run: %q, want succeeded", status)
	}
}

func TestPOSTRunsCancelMissingRunReturns404(t *testing.T) {
	pool, cleanup := testpg.Start(t)
	t.Cleanup(cleanup)

	rr := httptest.NewRecorder()
	newTestServer(pool).Handler().ServeHTTP(rr,
		signedRequest(t, http.MethodPost,
			"/api/v1/runs/00000000-0000-0000-0000-000000000000/cancel", nil))
	if rr.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 (body=%s)", rr.Code, rr.Body.String())
	}
}
