package doctor

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// stubCheck lets each test inject a known result + delay.
type stubCheck struct {
	name   string
	result Result
	delay  time.Duration
	calls  atomic.Int32
}

func (s *stubCheck) Name() string { return s.name }
func (s *stubCheck) Run(ctx context.Context) Result {
	s.calls.Add(1)
	if s.delay > 0 {
		select {
		case <-time.After(s.delay):
		case <-ctx.Done():
			return Result{Name: s.name, Status: StatusFail, Message: "deadline exceeded"}
		}
	}
	r := s.result
	if r.Name == "" {
		r.Name = s.name
	}
	return r
}

func TestRunFanoutInParallelAndSortsByName(t *testing.T) {
	checks := []Check{
		&stubCheck{name: "zeta", result: Result{Status: StatusOK}, delay: 50 * time.Millisecond},
		&stubCheck{name: "alpha", result: Result{Status: StatusOK}, delay: 50 * time.Millisecond},
		&stubCheck{name: "mike", result: Result{Status: StatusOK}, delay: 50 * time.Millisecond},
	}
	start := time.Now()
	results := Run(context.Background(), checks, time.Second)
	elapsed := time.Since(start)

	// If serial, this would be ~150ms; in parallel, ~50ms.
	if elapsed > 130*time.Millisecond {
		t.Errorf("Run was serial; took %s, expected ~50ms", elapsed)
	}
	if len(results) != 3 {
		t.Fatalf("got %d results, want 3", len(results))
	}
	wantOrder := []string{"alpha", "mike", "zeta"}
	for i, want := range wantOrder {
		if results[i].Name != want {
			t.Errorf("results[%d].Name = %s, want %s", i, results[i].Name, want)
		}
	}
}

func TestRunRespectsDeadline(t *testing.T) {
	hanger := &stubCheck{name: "hanger", result: Result{Status: StatusOK}, delay: 5 * time.Second}
	fast := &stubCheck{name: "fast", result: Result{Status: StatusOK}, delay: 10 * time.Millisecond}

	start := time.Now()
	results := Run(context.Background(), []Check{hanger, fast}, 100*time.Millisecond)
	elapsed := time.Since(start)
	if elapsed > 250*time.Millisecond {
		t.Errorf("Run did not honor deadline; elapsed %s", elapsed)
	}
	// hanger's stub returns "deadline exceeded" status when its ctx is canceled.
	// fast's result must still be OK.
	byName := map[string]Result{}
	for _, r := range results {
		byName[r.Name] = r
	}
	if byName["fast"].Status != StatusOK {
		t.Errorf("fast: status = %s", byName["fast"].Status)
	}
	if byName["hanger"].Status != StatusFail {
		t.Errorf("hanger: status = %s, want fail (deadline)", byName["hanger"].Status)
	}
}

func TestExitCodeMapping(t *testing.T) {
	cases := []struct {
		name    string
		results []Result
		want    int
	}{
		{"empty", []Result{}, 0},
		{"all skipped", []Result{{Status: StatusSkipped}, {Status: StatusSkipped}}, 0},
		{"all ok", []Result{{Status: StatusOK}, {Status: StatusOK}}, 0},
		{"warn only", []Result{{Status: StatusWarn}, {Status: StatusOK}}, 0},
		{"one fail", []Result{{Status: StatusOK}, {Status: StatusFail}}, 1},
		{"all fail", []Result{{Status: StatusFail}, {Status: StatusFail}}, 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ExitCode(c.results); got != c.want {
				t.Errorf("ExitCode = %d, want %d", got, c.want)
			}
		})
	}
}

func TestAggregateCounts(t *testing.T) {
	rs := []Result{
		{Status: StatusOK}, {Status: StatusOK}, {Status: StatusOK},
		{Status: StatusWarn},
		{Status: StatusFail}, {Status: StatusFail},
		{Status: StatusSkipped},
	}
	got := Aggregate(rs)
	if got.OK != 3 || got.Warn != 1 || got.Fail != 2 || got.Skipped != 1 {
		t.Errorf("got %+v, want OK=3 Warn=1 Fail=2 Skipped=1", got)
	}
}

func TestStatusString(t *testing.T) {
	cases := map[Status]string{
		StatusOK:      "ok",
		StatusWarn:    "warn",
		StatusFail:    "fail",
		StatusSkipped: "skipped",
		Status(99):    "unknown",
	}
	for s, want := range cases {
		if got := s.String(); got != want {
			t.Errorf("Status(%d).String() = %s, want %s", s, got, want)
		}
	}
}

func TestCheckFuncAdapter(t *testing.T) {
	called := false
	c := CheckFunc{
		N: "x",
		F: func(_ context.Context) Result {
			called = true
			return Result{Status: StatusOK, Message: "fine"}
		},
	}
	if c.Name() != "x" {
		t.Errorf("Name = %s, want x", c.Name())
	}
	r := c.Run(context.Background())
	if !called {
		t.Error("F never invoked")
	}
	if r.Status != StatusOK {
		t.Errorf("status = %s", r.Status)
	}
}

// --- HTTPCheck ---

func TestHTTPCheckOKOn2xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	c := HTTPCheck{N: "api", URL: srv.URL + "/healthz"}
	r := c.Run(context.Background())
	if r.Status != StatusOK {
		t.Errorf("status = %s; want ok", r.Status)
	}
	if !strings.Contains(r.Message, "200") {
		t.Errorf("message missing 200: %s", r.Message)
	}
}

func TestHTTPCheckFailOn5xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	r := HTTPCheck{N: "api", URL: srv.URL}.Run(context.Background())
	if r.Status != StatusFail {
		t.Errorf("status = %s; want fail", r.Status)
	}
}

func TestHTTPCheckSkipOnEmptyURL(t *testing.T) {
	r := HTTPCheck{N: "api"}.Run(context.Background())
	if r.Status != StatusSkipped {
		t.Errorf("status = %s; want skipped", r.Status)
	}
}

func TestHTTPCheckFailOnUnreachable(t *testing.T) {
	r := HTTPCheck{
		N:      "api",
		URL:    "http://127.0.0.1:1", // unprivileged refused port
		Client: &http.Client{Timeout: 200 * time.Millisecond},
	}.Run(context.Background())
	if r.Status != StatusFail {
		t.Errorf("status = %s; want fail", r.Status)
	}
}

// --- Skipped checks ---

func TestPostgresCheckSkipsWithoutDSN(t *testing.T) {
	r := PostgresCheck{}.Run(context.Background())
	if r.Status != StatusSkipped {
		t.Errorf("status = %s; want skipped", r.Status)
	}
}

func TestClickHouseCheckSkipsWithoutDSN(t *testing.T) {
	r := ClickHouseCheck{}.Run(context.Background())
	if r.Status != StatusSkipped {
		t.Errorf("status = %s; want skipped", r.Status)
	}
}

func TestNATSCheckSkipsWithoutURL(t *testing.T) {
	r := NATSCheck{}.Run(context.Background())
	if r.Status != StatusSkipped {
		t.Errorf("status = %s; want skipped", r.Status)
	}
}

func TestPoolCheckSkipsWithNilPool(t *testing.T) {
	r := PoolCheck{}.Run(context.Background())
	if r.Status != StatusSkipped {
		t.Errorf("status = %s; want skipped", r.Status)
	}
}

// Compile-time interface assertions catch a refactor that breaks the contract.
var (
	_ Check = PostgresCheck{}
	_ Check = ClickHouseCheck{}
	_ Check = NATSCheck{}
	_ Check = HTTPCheck{}
	_ Check = PoolCheck{}
	_ Check = SQLPingCheck{}
	_ Check = CheckFunc{}
)

// stub interface usage so the linter doesn't drop "errors" if every test gets
// trimmed in a future refactor.
var _ = errors.New
