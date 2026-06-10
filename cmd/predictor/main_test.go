package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestHealthzProxyReturns200WhenUpstreamHealthy points the proxy handler at a
// healthy httptest upstream and asserts it mirrors the upstream 200.
func TestHealthzProxyReturns200WhenUpstreamHealthy(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/healthz", r.URL.Path)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}))
	defer upstream.Close()

	h := healthzProxyHandler(&http.Client{Timeout: 2 * time.Second}, upstream.URL)
	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))

	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), "upstream-200")
}

// TestHealthzProxyReturns503WhenUpstreamUnhealthy proves a non-200 upstream is
// surfaced verbatim (the proxy mirrors the upstream status).
func TestHealthzProxyMirrorsUpstreamStatus(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer upstream.Close()

	h := healthzProxyHandler(&http.Client{Timeout: 2 * time.Second}, upstream.URL)
	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))

	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
	require.Contains(t, rec.Body.String(), "upstream-503")
}

// TestHealthzProxyReturns503WhenUpstreamUnreachable points at a closed server.
func TestHealthzProxyReturns503WhenUpstreamUnreachable(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	upstream.Close() // closed → transport error

	h := healthzProxyHandler(&http.Client{Timeout: 500 * time.Millisecond}, upstream.URL)
	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))

	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
}

// TestNoArgsPrintsBuildIdentity guards the no-args smoke-test contract
// (CLAUDE.md): with no env set, the binary prints its build identity and exits 0
// without serving. We build the binary into a temp dir and run it with an empty
// TEO_PREDICTOR_ML_URL.
func TestNoArgsPrintsBuildIdentity(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping binary build in -short mode")
	}
	bin := filepath.Join(t.TempDir(), "predictor")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}

	pkgDir := thisDir(t)
	build := exec.Command("go", "build", "-o", bin, ".")
	build.Dir = pkgDir
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build failed: %v\n%s", err, out)
	}

	cmd := exec.Command(bin)
	// Ensure the ML URL is unset so we hit the no-args identity path, not the
	// health-proxy server (which would block).
	cmd.Env = append(os.Environ(), "TEO_PREDICTOR_ML_URL=")
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "no-args predictor must exit 0; output:\n%s", out)
	require.Contains(t, string(out), "predictor", "build identity line must name the service")
	require.NotContains(t, strings.ToLower(string(out)), "listening",
		"no-args path must NOT start the health-proxy listener")
}

func thisDir(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	require.True(t, ok, "runtime.Caller failed")
	return filepath.Dir(file)
}
