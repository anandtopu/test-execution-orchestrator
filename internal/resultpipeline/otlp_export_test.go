package resultpipeline

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/google/uuid"
	collectorpb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
)

func TestUnixNanoToTimeClampsOnOverflow(t *testing.T) {
	// Sane current value: passes through untouched.
	now := time.Now()
	got := unixNanoToTime(uint64(now.UnixNano()))
	if !got.Equal(now) {
		t.Fatalf("round-trip failed: got %v want %v", got, now)
	}
	// uint64 max is 2× int64 max; raw cast wraps negative. Clamp must produce
	// a non-negative time so ClickHouse DateTime64 doesn't reject the row.
	clamped := unixNanoToTime(math.MaxUint64)
	if clamped.UnixNano() < 0 {
		t.Fatalf("clamp produced negative ns: %d", clamped.UnixNano())
	}
	if clamped.UnixNano() != math.MaxInt64 {
		t.Fatalf("clamp ns = %d, want MaxInt64", clamped.UnixNano())
	}
}

// These tests cover the row-flattening + Export entry-point logic without
// touching ClickHouse — the network path is exercised by the integration
// suite. The purpose here is to lock in the contract the new PrepareBatch
// path expects from flatten().

func TestExportReturnsErrorWhenCHNotConfigured(t *testing.T) {
	r := &OTLPReceiver{} // CH = nil
	_, err := r.Export(context.Background(), &collectorpb.ExportTraceServiceRequest{})
	if err == nil {
		t.Fatal("expected error when CH not configured")
	}
}

func TestFlattenProducesExpectedColumnShape(t *testing.T) {
	now := time.Now()
	repoID := uuid.New().String()
	runID := uuid.New().String()
	testID := uuid.New().String()

	req := &collectorpb.ExportTraceServiceRequest{
		ResourceSpans: []*tracepb.ResourceSpans{
			{
				Resource: resourceLite(repoID, runID),
				ScopeSpans: []*tracepb.ScopeSpans{
					{
						Spans: []*tracepb.Span{
							{
								TraceId:           []byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08, 0x09, 0x0a, 0x0b, 0x0c, 0x0d, 0x0e, 0x0f, 0x10},
								SpanId:            []byte{0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff, 0x11, 0x22},
								Name:              "pytest:tests/test_x.py::test_a",
								Kind:              tracepb.Span_SPAN_KIND_INTERNAL,
								StartTimeUnixNano: uint64(now.UnixNano()),
								EndTimeUnixNano:   uint64(now.Add(5 * time.Millisecond).UnixNano()),
								Status:            &tracepb.Status{Code: tracepb.Status_STATUS_CODE_ERROR, Message: "boom"},
								Attributes: []*commonpb.KeyValue{
									{Key: "teo.test_id", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: testID}}},
									{Key: "exception.message", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: "AssertionError: boom"}}},
									{Key: "exception.stacktrace", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: "File a.py line 1\nAssertionError"}}},
								},
							},
						},
					},
				},
			},
		},
	}

	r := &OTLPReceiver{}
	rows, failures := r.flatten(req)
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1", len(rows))
	}
	row := rows[0]
	if row.testID != testID {
		t.Errorf("testID = %s, want %s", row.testID, testID)
	}
	if row.runID != runID {
		t.Errorf("runID = %s, want %s", row.runID, runID)
	}
	if row.statusCode != 2 {
		t.Errorf("statusCode = %d, want 2 (ERROR)", row.statusCode)
	}
	if row.statusMessage != "boom" {
		t.Errorf("statusMessage = %s, want boom", row.statusMessage)
	}
	if row.traceID != "0102030405060708090a0b0c0d0e0f10" {
		t.Errorf("traceID hex wrong: %s", row.traceID)
	}
	// flatten must extract the exception.* attributes for failure clustering.
	if len(failures) != 1 {
		t.Fatalf("got %d failures, want 1", len(failures))
	}
	if failures[0].repoID != repoID {
		t.Errorf("failure repoID = %s, want %s", failures[0].repoID, repoID)
	}
	if failures[0].message != "AssertionError: boom" {
		t.Errorf("failure message = %s", failures[0].message)
	}
}

// resourceLite constructs a *tracepb.Resource with the two TEO-specific
// attributes the flattener reads (teo.repo_id, teo.run_id).
func resourceLite(repoID, runID string) *resourcepb.Resource {
	return &resourcepb.Resource{
		Attributes: []*commonpb.KeyValue{
			{Key: "teo.repo_id", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: repoID}}},
			{Key: "teo.run_id", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: runID}}},
		},
	}
}

func TestParseUUIDOrZeroHandlesInvalid(t *testing.T) {
	if got := parseUUIDOrZero(""); got != uuid.Nil {
		t.Errorf("empty input should yield uuid.Nil; got %s", got)
	}
	if got := parseUUIDOrZero("not-a-uuid"); got != uuid.Nil {
		t.Errorf("invalid input should yield uuid.Nil; got %s", got)
	}
	known := uuid.New()
	if got := parseUUIDOrZero(known.String()); got != known {
		t.Errorf("roundtrip failed: got %s, want %s", got, known)
	}
}
