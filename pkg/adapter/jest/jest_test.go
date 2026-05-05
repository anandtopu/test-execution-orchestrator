package jest

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/teo-dev/teo/internal/model"
	"github.com/teo-dev/teo/pkg/adapter"
)

func TestTranslateMapsAllStatuses(t *testing.T) {
	cases := map[string]model.TestOutcome{
		"passed":          model.OutcomePassed,
		"failed":          model.OutcomeFailed,
		"skipped":         model.OutcomeSkipped,
		"pending":         model.OutcomeSkipped,
		"todo":            model.OutcomeSkipped,
		"":                model.OutcomeErrored,
		"something-weird": model.OutcomeErrored,
	}
	for in, want := range cases {
		if got := translate(in); got != want {
			t.Errorf("translate(%q) = %s, want %s", in, got, want)
		}
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

func TestParseListTestsRelativizesPaths(t *testing.T) {
	out := []byte(`["/work/src/a.test.ts","/work/src/sub/b.test.ts"]`)
	got, err := parseListTests(out, "/work")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d entries, want 2", len(got))
	}
	if got[0].Path != filepath.FromSlash("src/a.test.ts") {
		t.Errorf("entry 0 path = %s", got[0].Path)
	}
	if got[1].Path != filepath.FromSlash("src/sub/b.test.ts") {
		t.Errorf("entry 1 path = %s", got[1].Path)
	}
	for i, e := range got {
		if e.Name != "<file>" {
			t.Errorf("entry %d name = %s, want <file>", i, e.Name)
		}
	}
}

func TestParseListTestsRejectsMalformed(t *testing.T) {
	if _, err := parseListTests([]byte("not json"), "/work"); err == nil {
		t.Fatal("expected error for non-JSON input")
	}
}

func TestParseReportEmitsAssertions(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "report_basic.json"))
	if err != nil {
		t.Fatal(err)
	}
	started := time.Date(2026, 5, 4, 10, 0, 0, 0, time.UTC)
	var got []adapter.Result
	if err := parseReport(raw, "/work", started, func(r adapter.Result) { got = append(got, r) }); err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d results, want 3: %#v", len(got), got)
	}

	// First assertion: passed, no ancestor nesting beyond one level.
	r := got[0]
	if r.Outcome != model.OutcomePassed {
		t.Errorf("result[0] outcome = %s", r.Outcome)
	}
	if r.Test.Name != "Button > renders the label" {
		t.Errorf("result[0] name = %q", r.Test.Name)
	}
	if r.Test.Path != filepath.FromSlash("src/components/Button.test.tsx") {
		t.Errorf("result[0] path = %s", r.Test.Path)
	}
	if r.DurationMS != 12 {
		t.Errorf("result[0] duration = %d", r.DurationMS)
	}

	// Second assertion: failed, nested ancestors, failure messages joined.
	r = got[1]
	if r.Outcome != model.OutcomeFailed {
		t.Errorf("result[1] outcome = %s", r.Outcome)
	}
	if r.Test.Name != "Button > when disabled > does not fire onClick" {
		t.Errorf("result[1] name = %q", r.Test.Name)
	}
	if r.FailureStack == "" {
		t.Error("result[1] FailureStack empty, want joined messages")
	}

	// Third assertion: todo → skipped.
	r = got[2]
	if r.Outcome != model.OutcomeSkipped {
		t.Errorf("result[2] outcome = %s, want skipped (todo)", r.Outcome)
	}
}

func TestParseReportEmptyReportIsNoOp(t *testing.T) {
	count := 0
	if err := parseReport([]byte(`{"testResults":[]}`), "/work", time.Now(), func(adapter.Result) { count++ }); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("empty report fired onResult %d times", count)
	}
}

func TestParseReportRejectsMalformed(t *testing.T) {
	if err := parseReport([]byte("not json"), "/work", time.Now(), func(adapter.Result) {}); err == nil {
		t.Fatal("expected error for non-JSON report")
	}
}

func TestNewAdapterDefaults(t *testing.T) {
	a := New()
	if a.Name() != "jest" {
		t.Errorf("Name() = %s, want jest", a.Name())
	}
	if a.bin() != "jest" {
		t.Errorf("bin() = %s, want jest", a.bin())
	}
	a.JestBin = "/custom/jest"
	if a.bin() != "/custom/jest" {
		t.Errorf("bin() with custom JestBin = %s", a.bin())
	}
}
