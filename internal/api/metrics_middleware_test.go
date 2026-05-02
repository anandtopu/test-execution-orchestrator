package api

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/teo-dev/teo/internal/metrics"
)

func TestMetricsMiddlewareRecordsRequestAndDuration(t *testing.T) {
	reg := metrics.New()
	r := chi.NewRouter()
	r.Use(metricsMiddleware(reg))
	r.Get("/api/v1/runs/{id}", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/runs/abc", nil)
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}

	// Scrape the registry and assert the labeled count.
	srv := httptest.NewServer(reg.Handler())
	t.Cleanup(srv.Close)
	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	got := string(body)

	// The chi RoutePattern (not the URL value) must be the handler label —
	// otherwise per-id requests would explode label cardinality.
	if !strings.Contains(got, `handler="/api/v1/runs/{id}"`) {
		t.Errorf("expected route pattern in handler label; got:\n%s", got)
	}
	if !strings.Contains(got, `status="200"`) {
		t.Errorf("expected status=200; got:\n%s", got)
	}
	if !strings.Contains(got, `method="GET"`) {
		t.Errorf("expected method=GET; got:\n%s", got)
	}
	if !strings.Contains(got, `http_server_requests_seconds_count{handler="/api/v1/runs/{id}",method="GET",status="200"} 1`) {
		t.Errorf("counter row missing; got:\n%s", got)
	}
}

func TestMetricsMiddlewareRecords500s(t *testing.T) {
	reg := metrics.New()
	r := chi.NewRouter()
	r.Use(metricsMiddleware(reg))
	r.Get("/boom", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})

	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/boom", nil))

	srv := httptest.NewServer(reg.Handler())
	t.Cleanup(srv.Close)
	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), `status="500"`) {
		t.Errorf("expected status=500; got:\n%s", string(body))
	}
}
