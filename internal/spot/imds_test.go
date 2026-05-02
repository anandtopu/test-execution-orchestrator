package spot

import "testing"

func TestParseActionTerminate(t *testing.T) {
	in := []byte(`{"action":"terminate","time":"2026-04-30T08:00:00Z"}`)
	got, ok := parseAction(in)
	if !ok {
		t.Fatal("expected detection")
	}
	if got.Action != "terminate" {
		t.Errorf("action = %s, want terminate", got.Action)
	}
}

func TestParseActionEmpty(t *testing.T) {
	if _, ok := parseAction([]byte("")); ok {
		t.Fatal("empty body should not detect")
	}
	if _, ok := parseAction([]byte("not-json")); ok {
		t.Fatal("invalid json should not detect")
	}
}
