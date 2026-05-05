//go:build integration

package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/teo-dev/teo/internal/testpg"
)

// gqlPost issues a signed POST against /graphql and returns the parsed JSON envelope.
// The auth check on /graphql matches the inline pattern in runs.go; tests
// share signedRequest from runs_integration_test.go.
func gqlPost(t *testing.T, h http.Handler, query string, vars map[string]any) map[string]any {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"query": query, "variables": vars})
	req := signedRequest(t, http.MethodPost, "/graphql", body)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}
	var out map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode response: %v\nbody: %s", err, rr.Body.String())
	}
	return out
}

// TestGraphQLEndpoint_RequiresAuth locks in the C2 fix: anonymous POSTs to
// /graphql must be rejected with 401 (same RFC 7807 envelope used elsewhere).
func TestGraphQLEndpoint_RequiresAuth(t *testing.T) {
	pool, cleanup := testpg.Start(t)
	t.Cleanup(cleanup)

	srv := New(Config{
		JWTSecret: testJWTSecret,
		JWTTTL:    time.Hour,
	}, pool)

	body, _ := json.Marshal(map[string]any{"query": `{ runs(first: 1) { id } }`})
	req := httptest.NewRequest(http.MethodPost, "/graphql", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401, body = %s", rr.Code, rr.Body.String())
	}
}

func TestGraphQLEndpoint_Runs_HappyPath(t *testing.T) {
	pool, cleanup := testpg.Start(t)
	t.Cleanup(cleanup)
	seed(t, pool)

	srv := New(Config{
		JWTSecret: "test-secret-must-be-at-least-32-bytes-long-okay",
		JWTTTL:    time.Hour,
	}, pool)

	resp := gqlPost(t, srv.Handler(), `query { runs(first: 10) { id status repoFullName branch commitSha totalDurationMs } }`, nil)
	if resp["errors"] != nil {
		t.Fatalf("unexpected errors: %v", resp["errors"])
	}
	data := resp["data"].(map[string]any)
	runs := data["runs"].([]any)
	if len(runs) != 2 {
		t.Fatalf("got %d runs, want 2", len(runs))
	}
	first := runs[0].(map[string]any)
	if first["status"] != "running" {
		t.Errorf("first.status = %v, want running", first["status"])
	}
	if first["repoFullName"] != "owner/sample" {
		t.Errorf("repoFullName = %v", first["repoFullName"])
	}
}

func TestGraphQLEndpoint_RunByID_WithShardsAndFailedCount(t *testing.T) {
	pool, cleanup := testpg.Start(t)
	t.Cleanup(cleanup)
	ids := seed(t, pool)

	srv := New(Config{
		JWTSecret: "test-secret-must-be-at-least-32-bytes-long-okay",
		JWTTTL:    time.Hour,
	}, pool)

	q := `query Run($id: ID!) {
		run(id: $id) {
			id
			status
			failedTestCount
			preemptionCount
			shards { index status testCount workerId predictedDurationMs }
		}
	}`
	resp := gqlPost(t, srv.Handler(), q, map[string]any{"id": ids.run1ID})
	if resp["errors"] != nil {
		t.Fatalf("unexpected errors: %v", resp["errors"])
	}
	run := resp["data"].(map[string]any)["run"].(map[string]any)
	if run["status"] != "failed" {
		t.Errorf("status = %v", run["status"])
	}
	if int(run["failedTestCount"].(float64)) != 1 {
		t.Errorf("failedTestCount = %v, want 1", run["failedTestCount"])
	}
	if int(run["preemptionCount"].(float64)) != 1 {
		t.Errorf("preemptionCount = %v, want 1", run["preemptionCount"])
	}
	shards := run["shards"].([]any)
	if len(shards) != 1 {
		t.Fatalf("shards len = %d, want 1", len(shards))
	}
	first := shards[0].(map[string]any)
	if first["workerId"] != "worker-A" {
		t.Errorf("workerId = %v", first["workerId"])
	}
	if int(first["testCount"].(float64)) != 5 {
		t.Errorf("testCount = %v", first["testCount"])
	}
}

func TestGraphQLEndpoint_FlakesAndClusters(t *testing.T) {
	pool, cleanup := testpg.Start(t)
	t.Cleanup(cleanup)
	seed(t, pool)

	srv := New(Config{
		JWTSecret: "test-secret-must-be-at-least-32-bytes-long-okay",
		JWTTTL:    time.Hour,
	}, pool)

	resp := gqlPost(t, srv.Handler(), `{
		flakes { testId testPath testName flakeRate wilsonLower sampleSize }
		failureClusters { id representativeMessage occurrences }
	}`, nil)
	if resp["errors"] != nil {
		t.Fatalf("unexpected errors: %v", resp["errors"])
	}
	data := resp["data"].(map[string]any)
	flakes := data["flakes"].([]any)
	if len(flakes) != 1 {
		t.Fatalf("got %d flakes, want 1", len(flakes))
	}
	if flakes[0].(map[string]any)["testPath"] != "tests/test_x.py" {
		t.Errorf("path = %v", flakes[0].(map[string]any)["testPath"])
	}
	clusters := data["failureClusters"].([]any)
	if len(clusters) != 1 {
		t.Fatalf("got %d clusters, want 1", len(clusters))
	}
}

func TestGraphQLEndpoint_RerunFailedMutation(t *testing.T) {
	pool, cleanup := testpg.Start(t)
	t.Cleanup(cleanup)
	ids := seed(t, pool)

	srv := New(Config{
		JWTSecret: "test-secret-must-be-at-least-32-bytes-long-okay",
		JWTTTL:    time.Hour,
	}, pool)

	resp := gqlPost(t, srv.Handler(),
		`mutation Rerun($id: ID!) { rerunFailed(runId: $id) { id status } }`,
		map[string]any{"id": ids.run1ID})
	if resp["errors"] != nil {
		t.Fatalf("unexpected errors: %v", resp["errors"])
	}
	r := resp["data"].(map[string]any)["rerunFailed"].(map[string]any)
	if r["status"] != "pending" {
		t.Errorf("new run status = %v", r["status"])
	}
	if r["id"] == ids.run1ID {
		t.Fatal("rerun should produce a new id")
	}
}

func TestGraphQLEndpoint_BadQueryReturnsErrorEnvelope(t *testing.T) {
	pool, cleanup := testpg.Start(t)
	t.Cleanup(cleanup)
	seed(t, pool)

	srv := New(Config{
		JWTSecret: "test-secret-must-be-at-least-32-bytes-long-okay",
		JWTTTL:    time.Hour,
	}, pool)

	body, _ := json.Marshal(map[string]any{"query": `{ nonexistent }`})
	req := signedRequest(t, http.MethodPost, "/graphql", body)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if !strings.Contains(rr.Body.String(), `"errors"`) {
		t.Errorf("expected GraphQL errors in body: %s", rr.Body.String())
	}
}
