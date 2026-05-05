package nats

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestConnectEmptyURLReturnsUnavailable(t *testing.T) {
	nc, js, err := Connect("")
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("expected ErrUnavailable, got %v", err)
	}
	if nc != nil || js != nil {
		t.Fatal("expected nil conn and stream on unavailable")
	}
}

// TestShardDispatchJSONRoundTrip pins the wire format published by the run
// manager and consumed by the worker. Field names, time encoding, and the
// omitempty markers on the optional DispatchTest fields all matter — a silent
// rename would manifest as a runtime "no tests in shard" surprise rather than
// a compile error.
func TestShardDispatchJSONRoundTrip(t *testing.T) {
	want := ShardDispatch{
		RunID:        "run-1",
		ShardID:      "shard-1",
		RepoFullName: "owner/repo",
		Runner:       "pytest",
		Tests: []DispatchTest{
			{Path: "tests/a.py", Name: "test_x"},
			{Path: "tests/b.py", Name: "test_y", ParamsHash: "h", Tags: []string{"slow"}},
		},
		PredictedMS:  1234,
		DispatchedAt: time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC),
	}
	b, err := json.Marshal(want)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	// Field-name spot checks — a renamed JSON tag would silently fail downstream.
	for _, tag := range []string{
		`"run_id":"run-1"`,
		`"shard_id":"shard-1"`,
		`"repo_full_name":"owner/repo"`,
		`"predicted_ms":1234`,
		`"dispatched_at":"2026-05-05T12:00:00Z"`,
	} {
		if !contains(string(b), tag) {
			t.Errorf("marshalled body missing %s: %s", tag, string(b))
		}
	}
	var got ShardDispatch
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("round-trip mismatch:\n want=%#v\n  got=%#v", want, got)
	}
}

// TestDispatchTestOmitEmpty proves the optional fields don't bloat dispatch
// messages when unset. The worker must accept both forms; this test just
// confirms the encoder elides them.
func TestDispatchTestOmitEmpty(t *testing.T) {
	b, err := json.Marshal(DispatchTest{Path: "p", Name: "n"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if contains(string(b), "params_hash") {
		t.Errorf("empty params_hash should be omitted: %s", string(b))
	}
	if contains(string(b), "tags") {
		t.Errorf("nil tags should be omitted: %s", string(b))
	}
}

// TestSubjectConstants pins the wire identifiers so an accidental rename trips
// a test rather than silently breaking the cross-service contract.
func TestSubjectConstants(t *testing.T) {
	cases := map[string]string{
		StreamShards:       "TEO_SHARDS",
		SubjShardsDispatch: "teo.shards.dispatch",
		StreamResults:      "TEO_RESULTS",
		SubjTestStarted:    "teo.results.test_started",
		SubjTestFinished:   "teo.results.test_finished",
	}
	for got, want := range cases {
		if got != want {
			t.Errorf("subject constant drifted: got %q, want %q", got, want)
		}
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
