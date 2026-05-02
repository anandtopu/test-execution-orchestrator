//go:build integration

package api

import (
	"encoding/xml"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/teo-dev/teo/internal/testpg"
)

// All export endpoints require an authenticated principal — anonymous
// requests get 401 from the handler, and httptest.NewRequest doesn't carry
// auth. Tests in this file build their request via signedRequest from
// runs_integration_test.go (same package, same build tag) which attaches
// a Bearer JWT issued with the same secret newTestServer uses.

// TestExportJUnitHappyPath seeds the standard fixture (which has one failed
// execution attached to a failure cluster), hits GET .../export?format=junit,
// and asserts the XML structure.
func TestExportJUnitHappyPath(t *testing.T) {
	pool, cleanup := testpg.Start(t)
	t.Cleanup(cleanup)
	ids := seed(t, pool)

	srv := New(Config{
		JWTSecret: "test-secret-must-be-at-least-32-bytes-long-okay",
		JWTTTL:    time.Hour,
	}, pool)

	rr := httptest.NewRecorder()
	req := signedRequest(t, http.MethodGet,
		"/api/v1/runs/"+ids.run1ID+"/export?format=junit", nil)
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}
	if ct := rr.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/xml") {
		t.Errorf("Content-Type = %q", ct)
	}
	body := rr.Body.String()
	if !strings.HasPrefix(body, `<?xml`) {
		t.Errorf("missing XML declaration: %s", body[:min(80, len(body))])
	}

	var doc junitTestSuites
	if err := xml.Unmarshal(rr.Body.Bytes(), &doc); err != nil {
		t.Fatalf("unmarshal: %v\nbody: %s", err, body)
	}
	if doc.Tests != 1 || doc.Failures != 1 {
		t.Errorf("doc tests=%d failures=%d, want 1/1", doc.Tests, doc.Failures)
	}
	if len(doc.Suites) != 1 {
		t.Fatalf("expected 1 testsuite, got %d", len(doc.Suites))
	}
	ts := doc.Suites[0]
	if ts.Name != "shard-0" {
		t.Errorf("suite name = %q, want shard-0", ts.Name)
	}
	if len(ts.Cases) != 1 {
		t.Fatalf("expected 1 testcase, got %d", len(ts.Cases))
	}
	tc := ts.Cases[0]
	if tc.Classname != "tests/test_x.py" || tc.Name != "test_x" {
		t.Errorf("testcase = %q::%q", tc.Classname, tc.Name)
	}
	if tc.Failure == nil {
		t.Fatal("expected <failure> element on the failed testcase")
	}
	if !strings.Contains(tc.Failure.Message, "AssertionError") {
		t.Errorf("failure.message = %q (want it to embed cluster's representative_message)", tc.Failure.Message)
	}
	if !strings.Contains(tc.Failure.Body, "AssertionError") {
		t.Errorf("failure body should contain stack from failure cluster: %q", tc.Failure.Body)
	}
}

// TestExportJUnitWithMixedOutcomes verifies passed/skipped/errored mappings
// in addition to the failed case the standard seed already covers.
func TestExportJUnitWithMixedOutcomes(t *testing.T) {
	pool, cleanup := testpg.Start(t)
	t.Cleanup(cleanup)
	ids := seed(t, pool)

	// Add a passed and a skipped execution on the same shard.
	mustExec(t, pool, `
        INSERT INTO teo.tests (id, repo_id, fingerprint, path, name, runner, status)
        VALUES (gen_random_uuid(), $1, 'fp-2', 'tests/test_y.py', 'test_y_pass', 'pytest', 'active')
    `, ids.repoID)
	mustExec(t, pool, `
        INSERT INTO teo.tests (id, repo_id, fingerprint, path, name, runner, status)
        VALUES (gen_random_uuid(), $1, 'fp-3', 'tests/test_z.py', 'test_z_skip', 'pytest', 'active')
    `, ids.repoID)
	mustExec(t, pool, `
        INSERT INTO teo.test_executions (shard_id, test_id, attempt, outcome, duration_ms, started_at, finished_at)
        SELECT $1, t.id, 1, 'passed', 200,
               now() - interval '5 minutes',
               now() - interval '5 minutes' + interval '0.2 seconds'
        FROM teo.tests t WHERE t.fingerprint = 'fp-2'
    `, ids.shard1ID)
	mustExec(t, pool, `
        INSERT INTO teo.test_executions (shard_id, test_id, attempt, outcome, duration_ms, started_at, finished_at)
        SELECT $1, t.id, 1, 'skipped', 0,
               now() - interval '5 minutes',
               now() - interval '5 minutes'
        FROM teo.tests t WHERE t.fingerprint = 'fp-3'
    `, ids.shard1ID)

	srv := New(Config{
		JWTSecret: "test-secret-must-be-at-least-32-bytes-long-okay",
		JWTTTL:    time.Hour,
	}, pool)
	rr := httptest.NewRecorder()
	req := signedRequest(t, http.MethodGet,
		"/api/v1/runs/"+ids.run1ID+"/export?format=junit", nil)
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}

	var doc junitTestSuites
	if err := xml.Unmarshal(rr.Body.Bytes(), &doc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if doc.Tests != 3 || doc.Failures != 1 || doc.Skipped != 1 {
		t.Errorf("counts: tests=%d failures=%d skipped=%d, want 3/1/1",
			doc.Tests, doc.Failures, doc.Skipped)
	}

	// Find the passed and skipped cases by name and verify their elements.
	passedFound, skippedFound := false, false
	for _, ts := range doc.Suites {
		for _, tc := range ts.Cases {
			switch tc.Name {
			case "test_y_pass":
				passedFound = true
				if tc.Failure != nil || tc.Error != nil || tc.Skipped != nil {
					t.Errorf("passed testcase should have no child elements")
				}
			case "test_z_skip":
				skippedFound = true
				if tc.Skipped == nil {
					t.Errorf("skipped testcase missing <skipped/>")
				}
			}
		}
	}
	if !passedFound || !skippedFound {
		t.Errorf("missing testcases: passed=%v skipped=%v", passedFound, skippedFound)
	}
}

func TestExportRejectsUnknownFormat(t *testing.T) {
	pool, cleanup := testpg.Start(t)
	t.Cleanup(cleanup)
	ids := seed(t, pool)

	srv := New(Config{
		JWTSecret: "test-secret-must-be-at-least-32-bytes-long-okay",
		JWTTTL:    time.Hour,
	}, pool)
	rr := httptest.NewRecorder()
	req := signedRequest(t, http.MethodGet,
		"/api/v1/runs/"+ids.run1ID+"/export?format=csv", nil)
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rr.Code)
	}
}

func TestExportNotFoundForUnknownRun(t *testing.T) {
	pool, cleanup := testpg.Start(t)
	t.Cleanup(cleanup)
	_ = seed(t, pool)

	srv := New(Config{
		JWTSecret: "test-secret-must-be-at-least-32-bytes-long-okay",
		JWTTTL:    time.Hour,
	}, pool)
	rr := httptest.NewRecorder()
	// A valid UUID that isn't present in the runs table.
	req := signedRequest(t, http.MethodGet,
		"/api/v1/runs/00000000-0000-0000-0000-000000000000/export?format=junit", nil)
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 (body=%s)", rr.Code, rr.Body.String())
	}
}

func TestExportOTLPReturns501WithoutClickHouse(t *testing.T) {
	pool, cleanup := testpg.Start(t)
	t.Cleanup(cleanup)
	ids := seed(t, pool)

	// Server constructed without WithClickHouseConn.
	srv := New(Config{
		JWTSecret: "test-secret-must-be-at-least-32-bytes-long-okay",
		JWTTTL:    time.Hour,
	}, pool)
	rr := httptest.NewRecorder()
	req := signedRequest(t, http.MethodGet,
		"/api/v1/runs/"+ids.run1ID+"/export?format=otlp", nil)
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusNotImplemented {
		t.Errorf("status = %d, want 501", rr.Code)
	}
}

func TestExportOTLPHappyPath_StubbedSpanQuerier(t *testing.T) {
	pool, cleanup := testpg.Start(t)
	t.Cleanup(cleanup)
	ids := seed(t, pool)

	stub := &stubSpans{rows: []ExportedSpan{
		{
			TraceID:       "0102030405060708090a0b0c0d0e0f10",
			SpanID:        "aabbccddeeff1122",
			Name:          "pytest:tests/test_x.py::test_x",
			StartTime:     time.Now().Add(-time.Minute),
			EndTime:       time.Now(),
			StatusCode:    2,
			StatusMessage: "boom",
			Attributes:    map[string]string{"exception.message": "AssertionError: boom"},
		},
	}}

	srv := New(Config{
		JWTSecret: "test-secret-must-be-at-least-32-bytes-long-okay",
		JWTTTL:    time.Hour,
	}, pool, WithSpanQuerier(stub))

	rr := httptest.NewRecorder()
	req := signedRequest(t, http.MethodGet,
		"/api/v1/runs/"+ids.run1ID+"/export?format=otlp&as=json", nil)
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}
	if ct := rr.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	body := rr.Body.String()
	if !strings.Contains(body, "pytest:tests/test_x.py::test_x") {
		t.Errorf("response missing span name; body=%s", body)
	}
}
