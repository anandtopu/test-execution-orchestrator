package metrics

import (
	"context"
	"net/http"
	"time"
)

// ServeHTTP starts a tiny dedicated metrics listener on addr (default :9100).
// The returned Stop function shuts it down. Used by the headless services
// (run-manager, result-pipeline, predictor) that don't otherwise listen on HTTP.
func (r *Registry) ServeHTTP(addr string) (stop func(context.Context) error, err error) {
	if addr == "" {
		addr = ":9100"
	}
	mux := http.NewServeMux()
	mux.Handle("/metrics", r.Handler())
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	srv := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		_ = srv.ListenAndServe()
	}()
	return srv.Shutdown, nil
}
