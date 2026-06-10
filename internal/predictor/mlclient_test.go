package predictor

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/teo-dev/teo/internal/model"
)

func cannedResponse(preds []mlPrediction, usedFallback bool) mlPredictResponse {
	return mlPredictResponse{
		Predictions:      preds,
		UsedFallback:     usedFallback,
		UsedModelVersion: "ml-v1",
	}
}

func TestMLClientPredictHappyPath(t *testing.T) {
	var gotReq mlPredictRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/predict" {
			t.Errorf("path = %q, want /v1/predict", r.URL.Path)
		}
		_ = json.NewDecoder(r.Body).Decode(&gotReq)
		_ = json.NewEncoder(w).Encode(cannedResponse([]mlPrediction{
			{Fingerprint: "a::T1", P50DurationMS: 100, P95DurationMS: 300, FlakeProbability: 0.1},
			{Fingerprint: "b::T2", P50DurationMS: 200, P95DurationMS: 600, IsColdStart: true},
		}, false))
	}))
	defer srv.Close()

	c := NewMLClient(srv.URL, time.Second)
	preds, err := c.Predict(context.Background(), "owner/repo", twoTests())
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if len(preds) != 2 {
		t.Fatalf("got %d preds, want 2", len(preds))
	}
	// snake_case JSON round-trip: fields must map, not silently zero.
	if preds[0].P50DurationMS != 100 || preds[0].P95DurationMS != 300 {
		t.Errorf("pred[0] durations = %d/%d, want 100/300", preds[0].P50DurationMS, preds[0].P95DurationMS)
	}
	if preds[0].FlakeProbability != 0.1 {
		t.Errorf("pred[0] flake = %v, want 0.1", preds[0].FlakeProbability)
	}
	if !preds[1].IsColdStart {
		t.Error("pred[1] IsColdStart should round-trip true")
	}
	if gotReq.RepoFullName != "owner/repo" || len(gotReq.Tests) != 2 {
		t.Errorf("request body not marshaled correctly: %+v", gotReq)
	}
}

func TestMLClientEmptyInputNoRequest(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		called = true
	}))
	defer srv.Close()

	c := NewMLClient(srv.URL, time.Second)
	preds, err := c.Predict(context.Background(), "owner/repo", nil)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if preds != nil {
		t.Errorf("preds = %v, want nil", preds)
	}
	if called {
		t.Error("empty input must not issue an HTTP request")
	}
}

func TestMLClientNon200IsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := NewMLClient(srv.URL, time.Second)
	if _, err := c.Predict(context.Background(), "owner/repo", twoTests()); err == nil {
		t.Fatal("expected error on non-200 status")
	}
}

func TestMLClientDecodeErrorIsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("not json"))
	}))
	defer srv.Close()

	c := NewMLClient(srv.URL, time.Second)
	if _, err := c.Predict(context.Background(), "owner/repo", twoTests()); err == nil {
		t.Fatal("expected error on undecodable body")
	}
}

func TestMLClientLengthMismatchIsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// One prediction for two tests.
		_ = json.NewEncoder(w).Encode(cannedResponse([]mlPrediction{{Fingerprint: "a::T1"}}, false))
	}))
	defer srv.Close()

	c := NewMLClient(srv.URL, time.Second)
	if _, err := c.Predict(context.Background(), "owner/repo", twoTests()); err == nil {
		t.Fatal("expected error on length mismatch")
	}
}

func TestMLClientTransportErrorIsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	srv.Close() // closed server -> transport error

	c := NewMLClient(srv.URL, 200*time.Millisecond)
	if _, err := c.Predict(context.Background(), "owner/repo", twoTests()); err == nil {
		t.Fatal("expected transport error against a closed server")
	}
}

func TestMLClientUnconfiguredIsError(t *testing.T) {
	c := NewMLClient("", time.Second)
	if _, err := c.Predict(context.Background(), "owner/repo", twoTests()); err == nil {
		t.Fatal("expected error from unconfigured client (empty BaseURL)")
	}
}

func TestMLClientRequestBodyShape(t *testing.T) {
	// Assert the request the server receives decodes to the expected
	// {repo_full_name, tests:[{path,name,...}]} snake_case shape, in order.
	var gotReq mlPredictRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "/v1/predict", r.URL.Path)
		require.Equal(t, "application/json", r.Header.Get("Content-Type"))
		require.NoError(t, json.NewDecoder(r.Body).Decode(&gotReq))
		_ = json.NewEncoder(w).Encode(cannedResponse([]mlPrediction{
			{Fingerprint: "a::T1"}, {Fingerprint: "b::T2"},
		}, false))
	}))
	defer srv.Close()

	tests := []model.TestEntry{
		{Path: "pkg/a", Name: "TestA", ParamsHash: "h1", Tags: []string{"slow"}},
		{Path: "pkg/b", Name: "TestB"},
	}
	c := NewMLClient(srv.URL, time.Second)
	_, err := c.Predict(context.Background(), "owner/repo", tests)
	require.NoError(t, err)

	require.Equal(t, "owner/repo", gotReq.RepoFullName)
	require.Len(t, gotReq.Tests, 2)
	require.Equal(t, "pkg/a", gotReq.Tests[0].Path)
	require.Equal(t, "TestA", gotReq.Tests[0].Name)
	require.Equal(t, "h1", gotReq.Tests[0].ParamsHash)
	require.Equal(t, []string{"slow"}, gotReq.Tests[0].Tags)
	require.Equal(t, "pkg/b", gotReq.Tests[1].Path)
	require.Equal(t, "TestB", gotReq.Tests[1].Name)
}

func TestMLClientNon200ErrorMessageMentionsStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := NewMLClient(srv.URL, time.Second)
	_, err := c.Predict(context.Background(), "owner/repo", twoTests())
	require.Error(t, err)
	require.Contains(t, err.Error(), "unexpected status 500")
}

func TestMLClientLengthMismatchMentionsCounts(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(cannedResponse([]mlPrediction{{Fingerprint: "a::T1"}}, false))
	}))
	defer srv.Close()

	c := NewMLClient(srv.URL, time.Second)
	_, err := c.Predict(context.Background(), "owner/repo", twoTests())
	require.Error(t, err)
	require.True(t, strings.Contains(err.Error(), "length mismatch"),
		"error should mention length mismatch, got: %v", err)
}

// TestMLClientSlowServerDeadline exercises the client-side Timeout against a
// server whose handler blocks past it. We use a quit channel + bounded-deadline
// poll to release the handler — NO time.Sleep in the assertion path (CLAUDE.md).
func TestMLClientSlowServerDeadline(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// Block until the test releases us (or the request context is canceled by
		// the client's deadline, whichever comes first). No fixed sleep.
		<-release
		_ = json.NewEncoder(w).Encode(cannedResponse([]mlPrediction{
			{Fingerprint: "a::T1"}, {Fingerprint: "b::T2"},
		}, false))
	}))
	defer srv.Close()
	defer close(release)

	c := NewMLClient(srv.URL, 50*time.Millisecond)

	// The Predict call itself must return a deadline/transport error well before
	// any fixed wall-clock budget; we assert it returns non-nil within a bounded
	// deadline rather than sleeping.
	type result struct {
		err error
	}
	done := make(chan result, 1)
	go func() {
		_, err := c.Predict(context.Background(), "owner/repo", twoTests())
		done <- result{err: err}
	}()

	deadline := time.After(2 * time.Second)
	select {
	case r := <-done:
		require.Error(t, r.err, "a 50ms client timeout against a blocked handler must error")
	case <-deadline:
		t.Fatal("Predict did not return within the bounded deadline; the client Timeout was not honored")
	}
}

func TestMLClientServerColdStartHookFires(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(cannedResponse([]mlPrediction{
			{Fingerprint: "a::T1"}, {Fingerprint: "b::T2"},
		}, true)) // used_fallback=true: server degraded to cold-start
	}))
	defer srv.Close()

	coldStarts := 0
	c := NewMLClient(srv.URL, time.Second)
	c.OnServerColdStart = func() { coldStarts++ }

	preds, err := c.Predict(context.Background(), "owner/repo", twoTests())
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if len(preds) != 2 {
		t.Fatalf("got %d preds, want 2", len(preds))
	}
	if coldStarts != 1 {
		t.Errorf("OnServerColdStart fired %d times, want exactly 1", coldStarts)
	}
}

func TestMLClientServerColdStartHookSilentWhenNotDegraded(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(cannedResponse([]mlPrediction{
			{Fingerprint: "a::T1"}, {Fingerprint: "b::T2"},
		}, false))
	}))
	defer srv.Close()

	coldStarts := 0
	c := NewMLClient(srv.URL, time.Second)
	c.OnServerColdStart = func() { coldStarts++ }

	if _, err := c.Predict(context.Background(), "owner/repo", twoTests()); err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if coldStarts != 0 {
		t.Errorf("OnServerColdStart fired %d times, want 0", coldStarts)
	}
}
