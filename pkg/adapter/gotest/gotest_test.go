package gotest

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/teo-dev/teo/internal/model"
	"github.com/teo-dev/teo/pkg/adapter"
)

func TestDedupePreservesOrder(t *testing.T) {
	got := dedupe([]string{"a", "b", "a", "c", "b"})
	want := []string{"a", "b", "c"}
	if len(got) != len(want) {
		t.Fatalf("dedupe len = %d, want %d (got %v)", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("dedupe[%d] = %s, want %s", i, got[i], want[i])
		}
	}
}

func TestDedupeEmpty(t *testing.T) {
	if got := dedupe(nil); len(got) != 0 {
		t.Fatalf("dedupe(nil) returned %v", got)
	}
}

func TestMergeEnvAppends(t *testing.T) {
	got := mergeEnv([]string{"A=1"}, map[string]string{"B": "2"})
	if len(got) != 2 || got[0] != "A=1" || got[1] != "B=2" {
		t.Fatalf("mergeEnv = %v", got)
	}
}

func TestMergeEnvNilExtraReturnsBase(t *testing.T) {
	base := []string{"A=1"}
	if &mergeEnv(base, nil)[0] != &base[0] {
		t.Fatal("mergeEnv with nil extras should return base unchanged")
	}
}

func TestProcessEventsBasic(t *testing.T) {
	pkg := "example.com/pkg/foo"
	raw, err := os.ReadFile(filepath.Join("testdata", "events_basic.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	indexByKey := map[string]model.TestEntry{
		pkg + "::TestPassing": {Path: pkg, Name: "TestPassing"},
		pkg + "::TestFailing": {Path: pkg, Name: "TestFailing"},
		// TestSkipped omitted on purpose — exercises the synthetic-entry fallback.
	}

	var got []adapter.Result
	processEvents(bytes.NewReader(raw), pkg, indexByKey, time.Now(), func(r adapter.Result) {
		got = append(got, r)
	})

	if len(got) != 3 {
		t.Fatalf("got %d results, want 3: %#v", len(got), got)
	}

	type expected struct {
		name    string
		outcome model.TestOutcome
		dur     int
	}
	want := []expected{
		{"TestPassing", model.OutcomePassed, 10},
		{"TestFailing", model.OutcomeFailed, 20},
		{"TestSkipped", model.OutcomeSkipped, 0},
	}
	for i, w := range want {
		if got[i].Test.Name != w.name {
			t.Errorf("result[%d] name = %s, want %s", i, got[i].Test.Name, w.name)
		}
		if got[i].Outcome != w.outcome {
			t.Errorf("result[%d] outcome = %s, want %s", i, got[i].Outcome, w.outcome)
		}
		if got[i].DurationMS != w.dur {
			t.Errorf("result[%d] duration = %dms, want %dms", i, got[i].DurationMS, w.dur)
		}
		if got[i].Test.Path != pkg {
			t.Errorf("result[%d] path = %s, want %s", i, got[i].Test.Path, pkg)
		}
	}
}

func TestProcessEventsIgnoresPackageLevelEvents(t *testing.T) {
	// Events with empty Test (package-level start/output/fail) must not produce
	// Results, even when their Action is one we'd otherwise translate.
	stream := []byte(`{"Action":"start","Package":"p"}
{"Action":"fail","Package":"p","Elapsed":1.5}
{"Action":"output","Package":"p","Output":"FAIL\n"}
`)
	count := 0
	processEvents(bytes.NewReader(stream), "p", nil, time.Now(), func(adapter.Result) { count++ })
	if count != 0 {
		t.Fatalf("package-level events produced %d results, want 0", count)
	}
}

func TestProcessEventsSkipsMalformedLines(t *testing.T) {
	// Garbage interleaved with a valid event — only the valid one becomes a
	// Result. Mirrors what `go test` sometimes prints on its way to crashing.
	stream := []byte(`not json at all
{"Action":"run","Package":"p","Test":"TestA"}
{"Action":"pass","Package":"p","Test":"TestA","Elapsed":0.5}
`)
	var got []adapter.Result
	processEvents(bytes.NewReader(stream), "p", nil, time.Now(), func(r adapter.Result) { got = append(got, r) })
	if len(got) != 1 || got[0].Test.Name != "TestA" || got[0].Outcome != model.OutcomePassed {
		t.Fatalf("got %#v", got)
	}
}

func TestProcessEventsSyntheticEntryWhenNotIndexed(t *testing.T) {
	stream := []byte(`{"Action":"run","Package":"p","Test":"TestUnknown"}
{"Action":"pass","Package":"p","Test":"TestUnknown","Elapsed":0.001}
`)
	var got adapter.Result
	processEvents(bytes.NewReader(stream), "p", map[string]model.TestEntry{}, time.Now(), func(r adapter.Result) { got = r })
	if got.Test.Path != "p" || got.Test.Name != "TestUnknown" {
		t.Fatalf("synthetic entry wrong: %+v", got.Test)
	}
}

func TestNewAdapterDefaults(t *testing.T) {
	a := New()
	if a.Name() != "go" {
		t.Errorf("Name() = %s, want go", a.Name())
	}
	if a.bin() != "go" {
		t.Errorf("bin() = %s, want go", a.bin())
	}
	a.GoBin = "/custom/go"
	if a.bin() != "/custom/go" {
		t.Errorf("bin() with custom GoBin = %s", a.bin())
	}
}
