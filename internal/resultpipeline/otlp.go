package resultpipeline

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"strings"
	"time"

	chdriver "github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	collectorpb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"

	teometrics "github.com/teo-dev/teo/internal/metrics"
)

// OTLPReceiver implements the OTLP gRPC TraceService.
// It writes spans to ClickHouse teo.span_events and links failures into
// teo.failure_clusters via the existing Cluster path.
//
// Writes use the native clickhouse-go/v2 conn.PrepareBatch path — column-major,
// one network round-trip per Export call. The earlier database/sql tx +
// PrepareContext + per-row ExecContext loop is kept in the codebase only
// for migrations, where throughput doesn't matter.
type OTLPReceiver struct {
	collectorpb.UnimplementedTraceServiceServer

	Pool    *pgxpool.Pool
	CH      chdriver.Conn // native ClickHouse driver, supports PrepareBatch
	Cluster *Cluster
	Logger  *slog.Logger
	Metrics *teometrics.Registry // optional; nil = no-op
}

// Export is the OTLP entry point. It is called per-batch by clients (workers,
// in our case) and must return promptly. We accept at-least-once delivery,
// dedupe via the ClickHouse ReplacingMergeTree if it ever collides.
func (r *OTLPReceiver) Export(ctx context.Context, req *collectorpb.ExportTraceServiceRequest) (*collectorpb.ExportTraceServiceResponse, error) {
	if r == nil || r.CH == nil {
		return nil, errors.New("OTLP receiver not configured")
	}
	rows, failures := r.flatten(req)
	if len(rows) == 0 {
		return &collectorpb.ExportTraceServiceResponse{}, nil
	}
	insertStart := time.Now()
	if err := r.writeSpans(ctx, rows); err != nil {
		r.Logger.Error("clickhouse insert spans", "err", err, "count", len(rows))
		if r.Metrics != nil {
			r.Metrics.CHInsertFailures.Inc()
		}
		return nil, err
	}
	if r.Metrics != nil {
		r.Metrics.CHInserts.Add(float64(len(rows)))
		r.Metrics.CHInsertSec.Observe(time.Since(insertStart).Seconds())
	}
	for _, f := range failures {
		if _, err := r.Cluster.UpsertCluster(ctx, f.repoID, f.stack, f.message); err != nil {
			r.Logger.Warn("upsert cluster", "err", err)
		}
	}
	return &collectorpb.ExportTraceServiceResponse{}, nil
}

// rowSpan is one ClickHouse row for teo.span_events.
type rowSpan struct {
	traceID, spanID, parentSpanID string
	testID, runID                 string
	name                          string
	kind                          int8
	startTime, endTime            time.Time
	statusCode                    int8
	statusMessage                 string
	attrKeys                      []string
	attrVals                      []string
}

type failure struct {
	repoID, stack, message string
}

func (r *OTLPReceiver) flatten(req *collectorpb.ExportTraceServiceRequest) ([]rowSpan, []failure) {
	var spans []rowSpan
	var failures []failure
	for _, rs := range req.ResourceSpans {
		resAttrs := attrsToMap(rs.Resource.GetAttributes())
		repoID := resAttrs["teo.repo_id"]
		runID := resAttrs["teo.run_id"]
		for _, ss := range rs.ScopeSpans {
			for _, sp := range ss.Spans {
				spanAttrs := attrsToMap(sp.Attributes)
				testID := pickTestID(spanAttrs, runID)
				row := rowSpan{
					traceID:       hexBytes(sp.TraceId),
					spanID:        hexBytes(sp.SpanId),
					parentSpanID:  hexBytes(sp.ParentSpanId),
					testID:        firstNonEmpty(spanAttrs["teo.test_id"], testID),
					runID:         firstNonEmpty(spanAttrs["teo.run_id"], runID),
					name:          sp.Name,
					kind:          int8(sp.Kind),
					startTime:     unixNanoToTime(sp.StartTimeUnixNano),
					endTime:       unixNanoToTime(sp.EndTimeUnixNano),
					statusCode:    statusToCode(sp.GetStatus()),
					statusMessage: sp.GetStatus().GetMessage(),
				}
				for k, v := range spanAttrs {
					row.attrKeys = append(row.attrKeys, k)
					row.attrVals = append(row.attrVals, v)
				}
				spans = append(spans, row)

				// On error, route to failure clustering using span attributes.
				if row.statusCode == 2 { // ERROR
					failures = append(failures, failure{
						repoID:  repoID,
						stack:   spanAttrs["exception.stacktrace"],
						message: firstNonEmpty(spanAttrs["exception.message"], row.statusMessage),
					})
				}
			}
		}
	}
	return spans, failures
}

func (r *OTLPReceiver) writeSpans(ctx context.Context, rows []rowSpan) error {
	if len(rows) == 0 {
		return nil
	}
	batch, err := r.CH.PrepareBatch(ctx, `
        INSERT INTO teo.span_events
            (trace_id, span_id, parent_span_id, test_id, run_id,
             name, kind, start_time, end_time, status_code, status_message,
             attributes, event_times, event_names, event_attributes)
    `)
	if err != nil {
		return fmt.Errorf("prepare batch: %w", err)
	}
	for _, s := range rows {
		attr := buildMap(s.attrKeys, s.attrVals)
		if err := batch.Append(
			s.traceID, s.spanID, s.parentSpanID,
			parseUUIDOrZero(s.testID), parseUUIDOrZero(s.runID),
			s.name, s.kind, s.startTime, s.endTime, s.statusCode, s.statusMessage,
			attr,
			// Nested events left empty in v1.0 — we do not flatten span events yet.
			[]time.Time{}, []string{}, []map[string]string{},
		); err != nil {
			_ = batch.Abort()
			return fmt.Errorf("append span: %w", err)
		}
	}
	if err := batch.Send(); err != nil {
		return fmt.Errorf("send batch: %w", err)
	}
	return nil
}

// --- helpers ---------------------------------------------------------------

func attrsToMap(in []*commonpb.KeyValue) map[string]string {
	out := make(map[string]string, len(in))
	for _, kv := range in {
		if kv == nil || kv.Value == nil {
			continue
		}
		out[kv.Key] = anyValueString(kv.Value)
	}
	return out
}

func anyValueString(v *commonpb.AnyValue) string {
	if v == nil {
		return ""
	}
	switch x := v.Value.(type) {
	case *commonpb.AnyValue_StringValue:
		return x.StringValue
	case *commonpb.AnyValue_IntValue:
		return fmt.Sprintf("%d", x.IntValue)
	case *commonpb.AnyValue_DoubleValue:
		return fmt.Sprintf("%g", x.DoubleValue)
	case *commonpb.AnyValue_BoolValue:
		if x.BoolValue {
			return "true"
		}
		return "false"
	}
	return ""
}

// unixNanoToTime converts an OTLP *_UnixNano (uint64) into a time.Time without
// silently wrapping when the value exceeds math.MaxInt64. The proto type permits
// values past 2262-04-11 23:47Z that would otherwise cast negative; we clamp
// instead of panicking or writing garbage to ClickHouse.
func unixNanoToTime(n uint64) time.Time {
	if n > math.MaxInt64 {
		return time.Unix(0, math.MaxInt64)
	}
	return time.Unix(0, int64(n))
}

func statusToCode(s *tracepb.Status) int8 {
	if s == nil {
		return 0
	}
	switch s.Code {
	case tracepb.Status_STATUS_CODE_OK:
		return 1
	case tracepb.Status_STATUS_CODE_ERROR:
		return 2
	}
	return 0
}

func hexBytes(b []byte) string {
	if len(b) == 0 {
		return ""
	}
	const hex = "0123456789abcdef"
	out := make([]byte, len(b)*2)
	for i, c := range b {
		out[i*2] = hex[c>>4]
		out[i*2+1] = hex[c&0x0f]
	}
	return string(out)
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func pickTestID(attrs map[string]string, _ string) string {
	if v, ok := attrs["teo.test_id"]; ok {
		return v
	}
	return ""
}

func buildMap(keys, vals []string) map[string]string {
	out := make(map[string]string, len(keys))
	for i, k := range keys {
		if i < len(vals) {
			out[k] = vals[i]
		}
	}
	return out
}

// parseUUIDOrZero coerces a string into a UUID, returning the zero UUID on
// empty/invalid input. ClickHouse accepts a zero UUID (00000000-…) which we
// later filter out at query time if necessary.
func parseUUIDOrZero(s string) uuid.UUID {
	if s == "" {
		return uuid.Nil
	}
	u, err := uuid.Parse(strings.TrimSpace(s))
	if err != nil {
		return uuid.Nil
	}
	return u
}
