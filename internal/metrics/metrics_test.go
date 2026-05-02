package metrics

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// expectedMetricNames lists every metric the bundled Grafana dashboards and
// PrometheusRules reference (deploy/helm/teo/templates/grafana-dashboards.yaml
// and prometheus-alerts.yaml). This test is the single place that catches a
// silent rename: if you change a metric name, this list must change too.
// Histograms are listed under their HELP-line name; the _count and _bucket
// series are auto-derived when the histogram exports.
var expectedMetricNames = []string{
	"http_server_requests_seconds",
	"teo_runs_active",
	"teo_run_transitions_total",
	"teo_runs_stuck_total",
	"teo_scheduler_plan_seconds",
	"teo_scheduler_plan_total",
	"teo_predictor_predict_total",
	"teo_predictor_cold_start_total",
	"teo_predictor_fallback_total",
	"teo_predictor_mae",
	"teo_clickhouse_inserts_total",
	"teo_clickhouse_insert_seconds",
	"teo_clickhouse_insert_failures_total",
}

func TestRegistryExposesAllExpectedMetrics(t *testing.T) {
	r := New()
	// Touch every metric once so they all appear in the gathered output.
	// (Metrics with no observations don't emit a HELP line in the wire format.)
	r.HTTPDurationSec.WithLabelValues("x", "x", "200").Observe(0)
	r.RunsActive.WithLabelValues("pending").Set(0)
	r.RunTransitions.WithLabelValues("pending").Inc()
	r.RunsStuck.Set(0)
	r.SchedulerPlanSec.Observe(0)
	r.SchedulerPlans.Inc()
	r.PredictorRequests.Inc()
	r.PredictorColdStart.Inc()
	r.PredictorFallback.Inc()
	r.PredictorMAE.Set(0)
	r.CHInserts.Inc()
	r.CHInsertSec.Observe(0)
	r.CHInsertFailures.Inc()

	srv := httptest.NewServer(r.Handler())
	t.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	output := string(body)
	for _, name := range expectedMetricNames {
		if !strings.Contains(output, "# HELP "+name) {
			t.Errorf("metric %q not exported (no HELP line)", name)
		}
	}
}

func TestNewIsIdempotentPerCall(t *testing.T) {
	// Two separate registries should not panic on duplicate registration —
	// each gets its own *prometheus.Registry.
	a := New()
	b := New()
	if a == b {
		t.Fatal("New should return a fresh Registry each call")
	}
	a.RunsStuck.Set(3)
	b.RunsStuck.Set(7)
	// They are independent.
}

func TestHTTPDurationLabelsExportAsCount(t *testing.T) {
	r := New()
	r.HTTPDurationSec.WithLabelValues("/api/v1/runs", "POST", "201").Observe(0.1)
	r.HTTPDurationSec.WithLabelValues("/api/v1/runs", "POST", "201").Observe(0.2)

	srv := httptest.NewServer(r.Handler())
	t.Cleanup(srv.Close)
	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	// The histogram's auto-derived *_count series is what the dashboard reads.
	if !strings.Contains(string(body), `http_server_requests_seconds_count{handler="/api/v1/runs",method="POST",status="201"} 2`) {
		t.Errorf("histogram count series missing; body:\n%s", string(body))
	}
}

func TestSchedulerHistogramObservation(t *testing.T) {
	r := New()
	r.SchedulerPlanSec.Observe(0.123)
	r.SchedulerPlanSec.Observe(0.456)

	srv := httptest.NewServer(r.Handler())
	t.Cleanup(srv.Close)
	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), `teo_scheduler_plan_seconds_count 2`) {
		t.Errorf("histogram count missing; body:\n%s", string(body))
	}
}
