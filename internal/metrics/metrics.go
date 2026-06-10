// Package metrics is the single source of truth for every Prometheus metric
// TEO emits. Bundled Grafana dashboards (deploy/helm/teo) and PrometheusRules
// reference these names exactly; renaming a metric here is a breaking change
// and must be paired with a dashboard/alert update.
//
// Each binary calls New() once at startup, then injects the *Registry into
// the components that emit. We deliberately do NOT use the global
// prometheus.DefaultRegisterer — keeping a per-process registry makes tests
// trivial (`m := New(); m.Reg.Gather()`).
package metrics

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Registry bundles every TEO-defined collector and gives callers a Handler()
// for the /metrics endpoint.
type Registry struct {
	Reg *prometheus.Registry

	// API gateway. Histograms auto-emit a `<name>_count` series, which is
	// what the request-rate panel in the bundled API dashboard reads — so we
	// don't need a separate counter.
	HTTPDurationSec *prometheus.HistogramVec

	// Run Manager
	RunsActive     *prometheus.GaugeVec
	RunTransitions *prometheus.CounterVec
	RunsStuck      prometheus.Gauge

	// Scheduler
	SchedulerPlanSec prometheus.Histogram
	SchedulerPlans   prometheus.Counter

	// Predictor
	PredictorRequests        prometheus.Counter
	PredictorColdStart       prometheus.Counter
	PredictorFallback        prometheus.Counter
	PredictorServerColdStart prometheus.Counter
	PredictorMAE             prometheus.Gauge

	// Result pipeline
	CHInserts        prometheus.Counter
	CHInsertSec      prometheus.Histogram
	CHInsertFailures prometheus.Counter
}

// New constructs a Registry with all TEO collectors registered.
func New() *Registry {
	reg := prometheus.NewRegistry()
	// Standard process + Go runtime collectors.
	reg.MustRegister(
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
		collectors.NewGoCollector(),
	)

	r := &Registry{Reg: reg}

	r.HTTPDurationSec = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "http_server_requests_seconds",
		Help:    "HTTP request duration in seconds, partitioned by handler.",
		Buckets: []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10},
	}, []string{"handler", "method", "status"})

	r.RunsActive = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "teo_runs_active",
		Help: "Current count of TEO runs in each status (sample taken on every reconciliation tick).",
	}, []string{"status"})
	r.RunTransitions = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "teo_run_transitions_total",
		Help: "Run state-machine transitions, labeled by destination status.",
	}, []string{"to_status"})
	r.RunsStuck = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "teo_runs_stuck_total",
		Help: "Runs currently past 2x their declared budget without reaching a terminal status.",
	})

	r.SchedulerPlanSec = prometheus.NewHistogram(prometheus.HistogramOpts{
		Name:    "teo_scheduler_plan_seconds",
		Help:    "Wall-clock time spent in scheduler.PlanFunc.",
		Buckets: []float64{0.001, 0.005, 0.01, 0.05, 0.1, 0.25, 0.5, 1, 2, 5},
	})
	r.SchedulerPlans = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "teo_scheduler_plan_total",
		Help: "Total scheduler.PlanFunc invocations.",
	})

	r.PredictorRequests = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "teo_predictor_predict_total",
		Help: "Predictor RPCs received.",
	})
	r.PredictorColdStart = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "teo_predictor_cold_start_total",
		Help: "Predictor responses that fell back to the cold-start default.",
	})
	r.PredictorFallback = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "teo_predictor_fallback_total",
		Help: "Predictor invocations that fell through from ML to the heuristic (client-side: timeout/non-200/decode/length-mismatch).",
	})
	r.PredictorServerColdStart = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "teo_predictor_server_coldstart_total",
		Help: "ML predict calls where the server returned 200 but self-reported used_fallback=true (server-side cold-start, e.g. MAE drift / model not loaded).",
	})
	r.PredictorMAE = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "teo_predictor_mae",
		Help: "Most recent holdout MAE for the duration regressor (per nightly training).",
	})

	r.CHInserts = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "teo_clickhouse_inserts_total",
		Help: "ClickHouse insert operations completed (rows or batches).",
	})
	r.CHInsertSec = prometheus.NewHistogram(prometheus.HistogramOpts{
		Name:    "teo_clickhouse_insert_seconds",
		Help:    "ClickHouse insert latency in seconds.",
		Buckets: []float64{0.005, 0.01, 0.05, 0.1, 0.5, 1, 5, 30, 60},
	})
	r.CHInsertFailures = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "teo_clickhouse_insert_failures_total",
		Help: "ClickHouse insert operations that returned an error.",
	})

	reg.MustRegister(
		r.HTTPDurationSec,
		r.RunsActive, r.RunTransitions, r.RunsStuck,
		r.SchedulerPlanSec, r.SchedulerPlans,
		r.PredictorRequests, r.PredictorColdStart, r.PredictorFallback, r.PredictorServerColdStart, r.PredictorMAE,
		r.CHInserts, r.CHInsertSec, r.CHInsertFailures,
	)
	return r
}

// Handler returns an http.Handler that serves the registry on /metrics.
func (r *Registry) Handler() http.Handler {
	return promhttp.HandlerFor(r.Reg, promhttp.HandlerOpts{Registry: r.Reg})
}
