package api

import (
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/teo-dev/teo/internal/metrics"
)

// statusRecorder wraps http.ResponseWriter to capture the status code.
type statusRecorder struct {
	http.ResponseWriter
	code int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.code = code
	s.ResponseWriter.WriteHeader(code)
}

// metricsMiddleware records request count + duration per (handler, method, status).
// We use chi's RoutePattern() so high-cardinality URL params (e.g., run IDs)
// don't pollute the label space.
func metricsMiddleware(reg *metrics.Registry) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			rec := &statusRecorder{ResponseWriter: w, code: 200}
			next.ServeHTTP(rec, r)

			handler := chi.RouteContext(r.Context()).RoutePattern()
			if handler == "" {
				handler = "unknown"
			}
			status := strconv.Itoa(rec.code)
			// The histogram's auto-emitted *_count series doubles as the
			// request counter, so we don't need a separate counter.
			reg.HTTPDurationSec.WithLabelValues(handler, r.Method, status).
				Observe(time.Since(start).Seconds())
		})
	}
}
