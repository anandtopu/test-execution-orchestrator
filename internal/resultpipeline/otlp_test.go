package resultpipeline

import (
	"testing"

	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
)

func TestStatusToCodeOk(t *testing.T) {
	if got := statusToCode(&tracepb.Status{Code: tracepb.Status_STATUS_CODE_OK}); got != 1 {
		t.Errorf("OK = %d, want 1", got)
	}
	if got := statusToCode(&tracepb.Status{Code: tracepb.Status_STATUS_CODE_ERROR}); got != 2 {
		t.Errorf("ERROR = %d, want 2", got)
	}
	if got := statusToCode(nil); got != 0 {
		t.Errorf("nil = %d, want 0", got)
	}
}

func TestAttrsToMapStrings(t *testing.T) {
	in := []*commonpb.KeyValue{
		{Key: "teo.test_id", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: "abc"}}},
		{Key: "code.line", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_IntValue{IntValue: 42}}},
		{Key: "is.flake", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_BoolValue{BoolValue: true}}},
	}
	got := attrsToMap(in)
	if got["teo.test_id"] != "abc" {
		t.Error("string attr missing")
	}
	if got["code.line"] != "42" {
		t.Error("int attr not stringified")
	}
	if got["is.flake"] != "true" {
		t.Error("bool attr not stringified")
	}
}

func TestHexBytes(t *testing.T) {
	got := hexBytes([]byte{0x00, 0x01, 0xab, 0xff})
	if got != "0001abff" {
		t.Errorf("got %s", got)
	}
	if got := hexBytes(nil); got != "" {
		t.Error("empty bytes should produce empty string")
	}
}
