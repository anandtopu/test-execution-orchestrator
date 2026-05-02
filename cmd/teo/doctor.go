package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/teo-dev/teo/internal/doctor"
)

func runDoctor(args []string) {
	fs := flag.NewFlagSet("doctor", flag.ExitOnError)
	pgDSN := fs.String("postgres-dsn", os.Getenv("TEO_POSTGRES_DSN"), "Postgres DSN (env: TEO_POSTGRES_DSN)")
	chDSN := fs.String("clickhouse-dsn", os.Getenv("TEO_CLICKHOUSE_DSN"), "ClickHouse DSN (env: TEO_CLICKHOUSE_DSN)")
	natsURL := fs.String("nats-url", os.Getenv("TEO_NATS_URL"), "NATS URL (env: TEO_NATS_URL)")
	apiURL := fs.String("api-url", os.Getenv("TEO_API_URL"), "API base URL; pings <api>/healthz")
	predURL := fs.String("predictor-url", os.Getenv("TEO_PREDICTOR_URL"), "Predictor base URL; pings <pred>/healthz")
	asJSON := fs.Bool("json", false, "Emit JSON instead of a table (for scripting)")
	deadlineSec := fs.Int("deadline", 10, "Overall deadline in seconds")
	if err := fs.Parse(args); err != nil {
		os.Exit(2)
	}

	httpClient := &http.Client{Timeout: 3 * time.Second}
	checks := []doctor.Check{
		doctor.PostgresCheck{DSN: *pgDSN},
		doctor.ClickHouseCheck{DSN: *chDSN},
		doctor.NATSCheck{URL: *natsURL},
		doctor.HTTPCheck{N: "api", URL: joinURL(*apiURL, "/healthz"), Client: httpClient},
		doctor.HTTPCheck{N: "predictor", URL: joinURL(*predURL, "/healthz"), Client: httpClient},
	}
	results := doctor.Run(context.Background(), checks, time.Duration(*deadlineSec)*time.Second)

	if *asJSON {
		emitJSON(results)
	} else {
		emitTable(results)
	}
	os.Exit(doctor.ExitCode(results))
}

func joinURL(base, suffix string) string {
	if base == "" {
		return ""
	}
	return strings.TrimRight(base, "/") + suffix
}

func emitJSON(results []doctor.Result) {
	type wire struct {
		Name      string `json:"name"`
		Status    string `json:"status"`
		Message   string `json:"message"`
		Detail    string `json:"detail,omitempty"`
		LatencyMS int64  `json:"latency_ms,omitempty"`
	}
	out := make([]wire, len(results))
	for i, r := range results {
		out[i] = wire{
			Name:      r.Name,
			Status:    r.Status.String(),
			Message:   r.Message,
			Detail:    r.Detail,
			LatencyMS: r.Latency.Milliseconds(),
		}
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(out)
}

func emitTable(results []doctor.Result) {
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "CHECK\tSTATUS\tLATENCY\tMESSAGE")
	for _, r := range results {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n",
			r.Name, statusGlyph(r.Status), r.Latency.Round(time.Millisecond), r.Message)
		if r.Detail != "" {
			for _, line := range strings.Split(strings.TrimSpace(r.Detail), "\n") {
				fmt.Fprintf(w, "  \t  \t  \t  %s\n", line)
			}
		}
	}
	_ = w.Flush()

	s := doctor.Aggregate(results)
	fmt.Fprintf(os.Stdout, "\n%d ok · %d warn · %d fail · %d skipped\n",
		s.OK, s.Warn, s.Fail, s.Skipped)
}

// statusGlyph turns a Status into a short, terminal-friendly tag. Operators
// reading the doctor output expect to skim the rightmost FAIL — so failures
// stand out without ANSI colour (which gets eaten by CI log viewers).
func statusGlyph(s doctor.Status) string {
	switch s {
	case doctor.StatusOK:
		return "OK"
	case doctor.StatusWarn:
		return "WARN"
	case doctor.StatusFail:
		return "FAIL"
	case doctor.StatusSkipped:
		return "skip"
	}
	return "??"
}
