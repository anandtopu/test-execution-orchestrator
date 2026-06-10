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

	// Add a second finished shard so the run has >=2 actuals and the predictor
	// aggregate resolves to a concrete object (mae) rather than null.
	mustExec(t, pool, `
        INSERT INTO teo.shards (id, run_id, index, status, predicted_duration_ms, actual_duration_ms, test_count, worker_id)
        VALUES (gen_random_uuid(), $1, 1, 'succeeded', 20000, 23000, 3, 'worker-D')
    `, ids.run1ID)

	q := `query Run($id: ID!) {
		run(id: $id) {
			id
			status
			failedTestCount
			preemptionCount
			predictor { mae p95DeltaMs modelVersion }
			shards { index status testCount workerId predictedDurationMs deltaMs }
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

	// Additive Run.predictor resolves end-to-end (mae = mean abs delta).
	pred, ok := run["predictor"].(map[string]any)
	if !ok {
		t.Fatalf("predictor not an object: %T %v", run["predictor"], run["predictor"])
	}
	if mae, _ := pred["mae"].(float64); mae < 1999 || mae > 2001 {
		t.Errorf("predictor.mae = %v, want ~2000", pred["mae"])
	}
	if pred["modelVersion"] != "heuristic" {
		t.Errorf("predictor.modelVersion = %v, want heuristic", pred["modelVersion"])
	}

	shards := run["shards"].([]any)
	if len(shards) != 2 {
		t.Fatalf("shards len = %d, want 2", len(shards))
	}
	first := shards[0].(map[string]any)
	if first["workerId"] != "worker-A" {
		t.Errorf("workerId = %v", first["workerId"])
	}
	if int(first["testCount"].(float64)) != 5 {
		t.Errorf("testCount = %v", first["testCount"])
	}
	// Additive Shard.deltaMs = actual-predicted = 31000-30000 = 1000 for shard0.
	if dm, ok := first["deltaMs"].(float64); !ok || int(dm) != 1000 {
		t.Errorf("shard0 deltaMs = %v, want 1000", first["deltaMs"])
	}
}

// TestGraphQLEndpoint_RunByID_PredictorCalibration exercises the exact
// RunByIdQuery the home overlay (ui-home-calibration) issues, including the
// per-shard calibration fields (predictionConfidence / modelVersion) and the
// run-level predictor block.
//
//   - A run with >=2 finished shards resolves a non-null predictor with a real
//     mae and a modelVersion ("heuristic" fallback when meta carries none).
//   - The per-shard predictionConfidence / modelVersion fields resolve without
//     error. They are currently NULL because teo.shards has no
//     prediction_confidence / model_version columns yet (that migration + the
//     queryShards SELECT are tracked under graphql-schema-fields / Phase A). This
//     test asserts the query SHAPE is valid today and pins the field names; once
//     the columns land + are SELECTed, the documented assertions below flip from
//     "null is acceptable" to "equals the seeded value".
//   - A run with <2 finished shards (run2: one running shard) resolves predictor
//     == null WITHOUT a GraphQL error.
func TestGraphQLEndpoint_RunByID_PredictorCalibration(t *testing.T) {
	pool, cleanup := testpg.Start(t)
	t.Cleanup(cleanup)
	ids := seed(t, pool)

	srv := New(Config{
		JWTSecret: "test-secret-must-be-at-least-32-bytes-long-okay",
		JWTTTL:    time.Hour,
	}, pool)

	// run1 has one finished shard (shard1, predicted 30000 / actual 31000). Add a
	// second finished shard so the predictor aggregate resolves to a concrete
	// object (>=2 finished shards). mae = (|31000-30000| + |23000-20000|)/2 = 2000.
	mustExec(t, pool, `
        INSERT INTO teo.shards (id, run_id, index, status, predicted_duration_ms, actual_duration_ms, test_count, worker_id)
        VALUES (gen_random_uuid(), $1, 1, 'succeeded', 20000, 23000, 3, 'worker-D')
    `, ids.run1ID)

	q := `query Run($id: ID!) {
		run(id: $id) {
			id
			status
			predictorMae
			predictorRho
			modelVersion
			predictor { mae rho modelVersion p95DeltaMs }
			shards { index status predictionConfidence modelVersion }
		}
	}`

	resp := gqlPost(t, srv.Handler(), q, map[string]any{"id": ids.run1ID})
	if resp["errors"] != nil {
		t.Fatalf("unexpected errors: %v", resp["errors"])
	}
	run := resp["data"].(map[string]any)["run"].(map[string]any)

	// Flat run-level aggregate the home adapter reads first. Non-null with a real
	// mean-absolute-delta (~2000ms).
	if mae, ok := run["predictorMae"].(float64); !ok || mae < 1999 || mae > 2001 {
		t.Errorf("predictorMae = %v, want ~2000", run["predictorMae"])
	}
	// modelVersion falls back to "heuristic" when run meta carries no model id.
	if run["modelVersion"] != "heuristic" {
		t.Errorf("run.modelVersion = %v, want heuristic", run["modelVersion"])
	}
	// Nested predictor block resolves the same aggregate.
	pred, ok := run["predictor"].(map[string]any)
	if !ok {
		t.Fatalf("predictor not an object: %T %v", run["predictor"], run["predictor"])
	}
	if mae, _ := pred["mae"].(float64); mae < 1999 || mae > 2001 {
		t.Errorf("predictor.mae = %v, want ~2000", pred["mae"])
	}

	// Per-shard calibration fields: the query is valid and the fields are present
	// in the response. They are null until the prediction_confidence /
	// model_version columns land (Phase A); assert presence + correct null/typed
	// shape so the contract is pinned without depending on the not-yet-migrated
	// columns.
	shards := run["shards"].([]any)
	if len(shards) < 1 {
		t.Fatalf("expected at least one shard")
	}
	for _, raw := range shards {
		s := raw.(map[string]any)
		// predictionConfidence: must be either null (current state) or a float in
		// [0,1] once the column lands. Never a wrong type.
		if pc, present := s["predictionConfidence"]; present && pc != nil {
			f, ok := pc.(float64)
			if !ok {
				t.Errorf("shard predictionConfidence is %T, want float64 or null", pc)
			} else if f < 0 || f > 1 {
				t.Errorf("shard predictionConfidence = %v, want in [0,1]", f)
			}
		}
		// modelVersion: null (current) or a string once the column lands.
		if mv, present := s["modelVersion"]; present && mv != nil {
			if _, ok := mv.(string); !ok {
				t.Errorf("shard modelVersion is %T, want string or null", mv)
			}
		}
	}

	// A run with <2 finished shards (run2: a single running shard) → predictor
	// resolves to null WITHOUT an error, and the flat predictorMae is null too.
	resp2 := gqlPost(t, srv.Handler(), q, map[string]any{"id": ids.run2ID})
	if resp2["errors"] != nil {
		t.Fatalf("run2 unexpected errors: %v", resp2["errors"])
	}
	run2 := resp2["data"].(map[string]any)["run"].(map[string]any)
	if run2["predictor"] != nil {
		t.Errorf("run2 predictor = %v, want null (<2 finished shards)", run2["predictor"])
	}
	if run2["predictorMae"] != nil {
		t.Errorf("run2 predictorMae = %v, want null", run2["predictorMae"])
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
		flakes { testId testPath testName flakeRate wilsonLower wilsonUpper sampleSize spark status quarantinedAt ownerTeam }
		failureClusters { id representativeMessage occurrences x y r category stackFingerprint affectedRuns }
	}`, nil)
	if resp["errors"] != nil {
		t.Fatalf("unexpected errors: %v", resp["errors"])
	}
	data := resp["data"].(map[string]any)
	flakes := data["flakes"].([]any)
	if len(flakes) != 1 {
		t.Fatalf("got %d flakes, want 1", len(flakes))
	}
	flake := flakes[0].(map[string]any)
	if flake["testPath"] != "tests/test_x.py" {
		t.Errorf("path = %v", flake["testPath"])
	}
	// Additive Flake fields resolve end-to-end and are correctly typed.
	if _, ok := flake["wilsonUpper"].(float64); !ok {
		t.Errorf("wilsonUpper not a float: %T %v", flake["wilsonUpper"], flake["wilsonUpper"])
	}
	spark, ok := flake["spark"].(string)
	if !ok || spark == "" {
		t.Errorf("spark not a non-empty string: %T %v", flake["spark"], flake["spark"])
	}
	if _, ok := flake["status"].(string); !ok {
		t.Errorf("status not a string: %T %v", flake["status"], flake["status"])
	}
	// ui-clusters-flakes additive fields round-trip over HTTP. The seed
	// quarantines the test, so status is 'quarantined' and quarantinedAt is a
	// non-empty RFC3339 string; ownerTeam carries the CODEOWNERS value.
	if flake["status"] != "quarantined" {
		t.Errorf("status = %v, want quarantined", flake["status"])
	}
	if qAt, ok := flake["quarantinedAt"].(string); !ok || qAt == "" {
		t.Errorf("quarantinedAt not a non-empty string: %T %v", flake["quarantinedAt"], flake["quarantinedAt"])
	}
	if flake["ownerTeam"] != "@teo-dev/platform" {
		t.Errorf("ownerTeam = %v, want @teo-dev/platform", flake["ownerTeam"])
	}

	clusters := data["failureClusters"].([]any)
	if len(clusters) != 2 {
		t.Fatalf("got %d clusters, want 2", len(clusters))
	}
	c0 := clusters[0].(map[string]any)
	for _, f := range []string{"x", "y", "r"} {
		if _, ok := c0[f].(float64); !ok {
			t.Errorf("cluster.%s not a float: %T %v", f, c0[f], c0[f])
		}
	}
	if cat, ok := c0["category"].(string); !ok || cat == "" {
		t.Errorf("category not a non-empty string: %T %v", c0["category"], c0["category"])
	}
	if _, ok := c0["stackFingerprint"].(string); !ok {
		t.Errorf("stackFingerprint not a string: %T %v", c0["stackFingerprint"], c0["stackFingerprint"])
	}
	if _, ok := c0["affectedRuns"].(float64); !ok {
		t.Errorf("affectedRuns not a number: %T %v", c0["affectedRuns"], c0["affectedRuns"])
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
