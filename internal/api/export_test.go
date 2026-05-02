package api

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
)

// stubSpans returns canned spans without touching ClickHouse.
type stubSpans struct{ rows []ExportedSpan }

func (s *stubSpans) QuerySpansByRun(_ context.Context, _ string) ([]ExportedSpan, error) {
	return s.rows, nil
}

func TestBuildOTLP_RoundTripsSpanFields(t *testing.T) {
	runID := uuid.New()
	testID := uuid.New()
	start := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	end := start.Add(2 * time.Millisecond)

	stub := &stubSpans{rows: []ExportedSpan{
		{
			TraceID:       "0102030405060708090a0b0c0d0e0f10",
			SpanID:        "aabbccddeeff1122",
			TestID:        testID,
			RunID:         runID,
			Name:          "pytest:tests/test_a.py::test_x",
			StartTime:     start,
			EndTime:       end,
			StatusCode:    2, // ERROR
			StatusMessage: "boom",
			Attributes:    map[string]string{"exception.message": "AssertionError: boom"},
		},
	}}

	req, err := buildOTLP(context.Background(), stub, runID.String())
	if err != nil {
		t.Fatal(err)
	}
	if len(req.ResourceSpans) != 1 {
		t.Fatalf("ResourceSpans len = %d", len(req.ResourceSpans))
	}
	rs := req.ResourceSpans[0]
	if !hasStringAttr(rs.Resource.Attributes, "teo.run_id", runID.String()) {
		t.Errorf("resource missing teo.run_id=%s", runID.String())
	}
	if len(rs.ScopeSpans) != 1 || len(rs.ScopeSpans[0].Spans) != 1 {
		t.Fatalf("expected 1 scope/1 span, got %d/%d", len(rs.ScopeSpans), len(rs.ScopeSpans[0].Spans))
	}
	span := rs.ScopeSpans[0].Spans[0]
	if span.Name != "pytest:tests/test_a.py::test_x" {
		t.Errorf("name = %s", span.Name)
	}
	if span.Status.Code != tracepb.Status_STATUS_CODE_ERROR {
		t.Errorf("status code = %v, want ERROR", span.Status.Code)
	}
	if span.StartTimeUnixNano != uint64(start.UnixNano()) {
		t.Errorf("start nanos mismatch: got %d, want %d", span.StartTimeUnixNano, start.UnixNano())
	}
	wantTrace := []byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08, 0x09, 0x0a, 0x0b, 0x0c, 0x0d, 0x0e, 0x0f, 0x10}
	if len(span.TraceId) != len(wantTrace) {
		t.Fatalf("trace_id len = %d, want %d", len(span.TraceId), len(wantTrace))
	}
	for i, b := range wantTrace {
		if span.TraceId[i] != b {
			t.Errorf("trace_id byte %d: got %x, want %x", i, span.TraceId[i], b)
			break
		}
	}
	want := map[string]string{
		"teo.run_id":        runID.String(),
		"teo.test_id":       testID.String(),
		"exception.message": "AssertionError: boom",
	}
	for k, v := range want {
		if !hasStringAttr(span.Attributes, k, v) {
			t.Errorf("missing span attr %s=%s", k, v)
		}
	}
}

func TestBuildOTLP_EmptyRunReturnsEmptyEnvelope(t *testing.T) {
	stub := &stubSpans{rows: nil}
	req, err := buildOTLP(context.Background(), stub, uuid.New().String())
	if err != nil {
		t.Fatal(err)
	}
	if len(req.ResourceSpans) != 1 {
		t.Fatalf("expected 1 ResourceSpans even when empty, got %d", len(req.ResourceSpans))
	}
	if got := req.ResourceSpans[0].ScopeSpans[0].Spans; len(got) != 0 {
		t.Errorf("expected 0 spans, got %d", len(got))
	}
}

func TestHexToBytesRoundTrips(t *testing.T) {
	cases := []string{
		"",
		"00",
		"0102030405060708090a0b0c0d0e0f10",
		"aabbccddeeff1122",
	}
	for _, c := range cases {
		got := hexToBytes(c)
		if len(got)*2 != len(c) {
			t.Errorf("hexToBytes(%q) length %d, want %d", c, len(got)*2, len(c))
		}
	}
	if hexToBytes("zz") != nil {
		t.Error("invalid hex should return nil")
	}
}

func TestFormatSecondsHasMillisResolution(t *testing.T) {
	cases := []struct {
		in   time.Duration
		want string
	}{
		{0, "0.000"},
		{1500 * time.Millisecond, "1.500"},
		{2*time.Second + 250*time.Millisecond, "2.250"},
	}
	for _, c := range cases {
		if got := formatSeconds(c.in); got != c.want {
			t.Errorf("formatSeconds(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}

func hasStringAttr(attrs []*commonpb.KeyValue, key, want string) bool {
	for _, kv := range attrs {
		if kv.Key != key || kv.Value == nil {
			continue
		}
		if v, ok := kv.Value.Value.(*commonpb.AnyValue_StringValue); ok && v.StringValue == want {
			return true
		}
	}
	return false
}
