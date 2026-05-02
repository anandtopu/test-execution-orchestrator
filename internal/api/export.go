package api

import (
	"context"
	"encoding/xml"
	"fmt"
	"net/http"
	"strings"
	"time"

	chdriver "github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	collectorpb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	"github.com/teo-dev/teo/internal/auth"
)

// SpanQuerier loads OTLP span rows for a run from ClickHouse. Defined as an
// interface so unit tests can stub it without a live ClickHouse.
type SpanQuerier interface {
	QuerySpansByRun(ctx context.Context, runID string) ([]ExportedSpan, error)
}

// ExportedSpan is the row shape returned by SpanQuerier — close to the
// teo.span_events column layout so the OTLP marshaller is straightforward.
type ExportedSpan struct {
	TraceID       string
	SpanID        string
	ParentSpanID  string
	TestID        uuid.UUID
	RunID         uuid.UUID
	Name          string
	Kind          int8
	StartTime     time.Time
	EndTime       time.Time
	StatusCode    int8
	StatusMessage string
	Attributes    map[string]string
}

// chSpanQuerier is the production SpanQuerier backed by ClickHouse.
type chSpanQuerier struct {
	conn chdriver.Conn
}

// QuerySpansByRun pulls every span for a run, ordered by start_time.
func (q *chSpanQuerier) QuerySpansByRun(ctx context.Context, runID string) ([]ExportedSpan, error) {
	rid, err := uuid.Parse(runID)
	if err != nil {
		return nil, fmt.Errorf("parse run id: %w", err)
	}
	rows, err := q.conn.Query(ctx, `
        SELECT trace_id, span_id, parent_span_id, test_id, run_id,
               name, kind, start_time, end_time, status_code, status_message,
               attributes
        FROM teo.span_events
        WHERE run_id = ?
        ORDER BY start_time
    `, rid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ExportedSpan
	for rows.Next() {
		var s ExportedSpan
		if err := rows.Scan(
			&s.TraceID, &s.SpanID, &s.ParentSpanID, &s.TestID, &s.RunID,
			&s.Name, &s.Kind, &s.StartTime, &s.EndTime, &s.StatusCode, &s.StatusMessage,
			&s.Attributes,
		); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// exportHandler returns an http.Handler that serves the run-export endpoint.
// pool is required (JUnit + run lookup); spans is optional — when nil, the OTLP
// format returns 501.
func exportHandler(pool *pgxpool.Pool, spans SpanQuerier) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if auth.PrincipalFrom(r.Context()) == nil {
			writeProblem(w, http.StatusUnauthorized, "Unauthorized", "authentication required")
			return
		}
		runID := chi.URLParam(r, "id")
		if _, err := uuid.Parse(runID); err != nil {
			writeProblem(w, http.StatusBadRequest, "Invalid id", "run id must be a UUID")
			return
		}

		// Confirm the run exists. 404 here gives a cleaner error than an empty payload.
		var exists bool
		err := pool.QueryRow(r.Context(),
			`SELECT EXISTS(SELECT 1 FROM teo.runs WHERE id = $1)`, runID).Scan(&exists)
		if err != nil {
			writeProblem(w, http.StatusInternalServerError, "Database error", err.Error())
			return
		}
		if !exists {
			writeProblem(w, http.StatusNotFound, "Not found", "run "+runID+" does not exist")
			return
		}

		switch strings.ToLower(r.URL.Query().Get("format")) {
		case "junit":
			doc, err := buildJUnit(r.Context(), pool, runID)
			if err != nil {
				writeProblem(w, http.StatusInternalServerError, "Build failed", err.Error())
				return
			}
			body, err := xml.MarshalIndent(doc, "", "  ")
			if err != nil {
				writeProblem(w, http.StatusInternalServerError, "Marshal failed", err.Error())
				return
			}
			w.Header().Set("Content-Type", "application/xml; charset=utf-8")
			w.Header().Set("Content-Disposition",
				fmt.Sprintf(`attachment; filename="run-%s.junit.xml"`, runID))
			_, _ = w.Write([]byte(xml.Header))
			_, _ = w.Write(body)
		case "otlp":
			if spans == nil {
				writeProblem(w, http.StatusNotImplemented, "OTLP export unavailable",
					"server is not configured with a ClickHouse connection")
				return
			}
			req, err := buildOTLP(r.Context(), spans, runID)
			if err != nil {
				writeProblem(w, http.StatusInternalServerError, "Build failed", err.Error())
				return
			}
			if r.URL.Query().Get("as") == "json" {
				body, err := protojson.Marshal(req)
				if err != nil {
					writeProblem(w, http.StatusInternalServerError, "Marshal failed", err.Error())
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write(body)
				return
			}
			body, err := proto.Marshal(req)
			if err != nil {
				writeProblem(w, http.StatusInternalServerError, "Marshal failed", err.Error())
				return
			}
			w.Header().Set("Content-Type", "application/x-protobuf")
			w.Header().Set("Content-Disposition",
				fmt.Sprintf(`attachment; filename="run-%s.otlp.pb"`, runID))
			_, _ = w.Write(body)
		default:
			writeProblem(w, http.StatusBadRequest, "Invalid format",
				`format must be "junit" or "otlp"`)
		}
	}
}

// --- JUnit XML --------------------------------------------------------------

// junitTestSuites is the root <testsuites> element. Field names are the
// JUnit-XML standard; one testsuite per shard.
type junitTestSuites struct {
	XMLName  xml.Name         `xml:"testsuites"`
	Name     string           `xml:"name,attr"`
	Tests    int              `xml:"tests,attr"`
	Failures int              `xml:"failures,attr"`
	Errors   int              `xml:"errors,attr"`
	Skipped  int              `xml:"skipped,attr"`
	Time     string           `xml:"time,attr"`
	Suites   []junitTestSuite `xml:"testsuite"`
}

type junitTestSuite struct {
	Name      string          `xml:"name,attr"`
	Tests     int             `xml:"tests,attr"`
	Failures  int             `xml:"failures,attr"`
	Errors    int             `xml:"errors,attr"`
	Skipped   int             `xml:"skipped,attr"`
	Time      string          `xml:"time,attr"`
	Timestamp string          `xml:"timestamp,attr,omitempty"`
	Cases     []junitTestCase `xml:"testcase"`
}

type junitTestCase struct {
	Classname string        `xml:"classname,attr"`
	Name      string        `xml:"name,attr"`
	Time      string        `xml:"time,attr"`
	Failure   *junitFailure `xml:"failure,omitempty"`
	Error     *junitFailure `xml:"error,omitempty"`
	Skipped   *junitElement `xml:"skipped,omitempty"`
}

type junitFailure struct {
	Message string `xml:"message,attr"`
	Type    string `xml:"type,attr,omitempty"`
	Body    string `xml:",chardata"`
}

type junitElement struct {
	Message string `xml:"message,attr,omitempty"`
}

// buildJUnit pulls every test_execution for a run, joined with the test row
// and any failure_cluster, and folds them into a JUnit-XML document. One
// testsuite per shard so users can identify the worker that produced a result.
func buildJUnit(ctx context.Context, pool *pgxpool.Pool, runID string) (*junitTestSuites, error) {
	rows, err := pool.Query(ctx, `
        SELECT s.id::text, s.index, s.started_at,
               t.path, t.name,
               te.outcome, te.duration_ms,
               COALESCE(fc.representative_message, ''),
               COALESCE(fc.representative_stack, '')
        FROM teo.test_executions te
        JOIN teo.shards s ON s.id = te.shard_id
        JOIN teo.tests t ON t.id = te.test_id
        LEFT JOIN teo.failure_clusters fc ON fc.id = te.failure_cluster_id
        WHERE s.run_id = $1
        ORDER BY s.index, t.path, t.name, te.attempt
    `, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	type row struct {
		shardID   string
		shardIdx  int
		startedAt *time.Time
		path      string
		name      string
		outcome   string
		duration  int
		message   string
		stack     string
	}
	bySuite := make(map[string]*junitTestSuite)
	suiteDur := make(map[string]time.Duration)
	var suiteOrder []string
	totalTests, totalFail, totalErr, totalSkip := 0, 0, 0, 0
	var totalDur time.Duration

	for rows.Next() {
		var r row
		if err := rows.Scan(&r.shardID, &r.shardIdx, &r.startedAt,
			&r.path, &r.name, &r.outcome, &r.duration, &r.message, &r.stack); err != nil {
			return nil, err
		}
		suite, ok := bySuite[r.shardID]
		if !ok {
			suite = &junitTestSuite{Name: fmt.Sprintf("shard-%d", r.shardIdx)}
			if r.startedAt != nil && !r.startedAt.IsZero() {
				suite.Timestamp = r.startedAt.UTC().Format(time.RFC3339)
			}
			bySuite[r.shardID] = suite
			suiteOrder = append(suiteOrder, r.shardID)
		}
		dur := time.Duration(r.duration) * time.Millisecond
		c := junitTestCase{
			Classname: r.path,
			Name:      r.name,
			Time:      formatSeconds(dur),
		}
		switch r.outcome {
		case "passed":
			// no child element
		case "failed":
			c.Failure = &junitFailure{Message: r.message, Type: "failed", Body: r.stack}
			suite.Failures++
			totalFail++
		case "skipped":
			c.Skipped = &junitElement{}
			suite.Skipped++
			totalSkip++
		case "errored", "timed_out", "interrupted":
			c.Error = &junitFailure{Message: r.message, Type: r.outcome, Body: r.stack}
			suite.Errors++
			totalErr++
		}
		suite.Cases = append(suite.Cases, c)
		suite.Tests++
		totalTests++
		totalDur += dur
		suiteDur[r.shardID] += dur
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	doc := &junitTestSuites{
		Name:     "run-" + runID,
		Tests:    totalTests,
		Failures: totalFail,
		Errors:   totalErr,
		Skipped:  totalSkip,
		Time:     formatSeconds(totalDur),
	}
	for _, sid := range suiteOrder {
		s := bySuite[sid]
		s.Time = formatSeconds(suiteDur[sid])
		doc.Suites = append(doc.Suites, *s)
	}
	return doc, nil
}

// formatSeconds renders a duration as a JUnit-style decimal seconds string
// with millisecond resolution: "0.000".
func formatSeconds(d time.Duration) string {
	return fmt.Sprintf("%.3f", d.Seconds())
}

// --- OTLP -------------------------------------------------------------------

// buildOTLP pulls span rows from the SpanQuerier and folds them into a
// collectorpb.ExportTraceServiceRequest. We emit a single ResourceSpans whose
// resource carries teo.run_id, with one ScopeSpans containing every span.
func buildOTLP(ctx context.Context, spans SpanQuerier, runID string) (*collectorpb.ExportTraceServiceRequest, error) {
	rows, err := spans.QuerySpansByRun(ctx, runID)
	if err != nil {
		return nil, err
	}
	resource := &resourcepb.Resource{
		Attributes: []*commonpb.KeyValue{
			stringKV("teo.run_id", runID),
			stringKV("service.name", "teo"),
		},
	}
	out := make([]*tracepb.Span, 0, len(rows))
	for _, s := range rows {
		span := &tracepb.Span{
			TraceId:           hexToBytes(s.TraceID),
			SpanId:            hexToBytes(s.SpanID),
			ParentSpanId:      hexToBytes(s.ParentSpanID),
			Name:              s.Name,
			Kind:              tracepb.Span_SpanKind(s.Kind),
			StartTimeUnixNano: uint64(s.StartTime.UnixNano()),
			EndTimeUnixNano:   uint64(s.EndTime.UnixNano()),
			Status:            statusFromCode(s.StatusCode, s.StatusMessage),
		}
		// Always include teo.run_id and teo.test_id so the export is
		// self-contained; map attributes round-trip after that.
		span.Attributes = append(span.Attributes,
			stringKV("teo.run_id", s.RunID.String()),
			stringKV("teo.test_id", s.TestID.String()),
		)
		for k, v := range s.Attributes {
			if k == "teo.run_id" || k == "teo.test_id" {
				continue
			}
			span.Attributes = append(span.Attributes, stringKV(k, v))
		}
		out = append(out, span)
	}
	return &collectorpb.ExportTraceServiceRequest{
		ResourceSpans: []*tracepb.ResourceSpans{
			{
				Resource:   resource,
				ScopeSpans: []*tracepb.ScopeSpans{{Spans: out}},
			},
		},
	}, nil
}

func stringKV(k, v string) *commonpb.KeyValue {
	return &commonpb.KeyValue{
		Key: k,
		Value: &commonpb.AnyValue{
			Value: &commonpb.AnyValue_StringValue{StringValue: v},
		},
	}
}

func statusFromCode(code int8, msg string) *tracepb.Status {
	switch code {
	case 1:
		return &tracepb.Status{Code: tracepb.Status_STATUS_CODE_OK, Message: msg}
	case 2:
		return &tracepb.Status{Code: tracepb.Status_STATUS_CODE_ERROR, Message: msg}
	default:
		return &tracepb.Status{Code: tracepb.Status_STATUS_CODE_UNSET, Message: msg}
	}
}

// hexToBytes decodes a lowercase hex string into bytes. The receiver writes
// span_events with hexBytes() — invertible here without external deps.
func hexToBytes(s string) []byte {
	if s == "" {
		return nil
	}
	out := make([]byte, len(s)/2)
	for i := range out {
		hi, ok1 := hexDigit(s[i*2])
		lo, ok2 := hexDigit(s[i*2+1])
		if !ok1 || !ok2 {
			return nil
		}
		out[i] = hi<<4 | lo
	}
	return out
}

func hexDigit(b byte) (byte, bool) {
	switch {
	case b >= '0' && b <= '9':
		return b - '0', true
	case b >= 'a' && b <= 'f':
		return b - 'a' + 10, true
	case b >= 'A' && b <= 'F':
		return b - 'A' + 10, true
	}
	return 0, false
}
