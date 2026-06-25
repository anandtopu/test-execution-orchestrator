package jest

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/teo-dev/teo/internal/model"
	"github.com/teo-dev/teo/pkg/adapter"
)

func TestDistinctPaths(t *testing.T) {
	in := []model.TestEntry{
		{Path: "a.test.ts", Name: "t1"},
		{Path: "a.test.ts", Name: "t2"},
		{Path: "b.test.ts", Name: "t3"},
	}
	got := distinctPaths(in)
	if len(got) != 2 || got[0] != "a.test.ts" || got[1] != "b.test.ts" {
		t.Fatalf("distinctPaths = %v", got)
	}
}

// parseReport attaches the matching AST signature (keyed path → built Name) to
// each emitted Result, and leaves it empty when no entry matches.
func TestParseReportAttachesASTSignature(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "report_basic.json"))
	if err != nil {
		t.Fatal(err)
	}
	rel := filepath.FromSlash("src/components/Button.test.tsx")
	sigs := map[string]map[string]string{
		rel: {
			"Button > renders the label":                     "deadbeefcafe0001",
			"Button > when disabled > does not fire onClick": "deadbeefcafe0002",
			// third assertion ("...todo") intentionally absent → empty signature
		},
	}
	var got []adapter.Result
	started := time.Date(2026, 6, 24, 10, 0, 0, 0, time.UTC)
	if err := parseReport(raw, "/work", started, sigs, func(r adapter.Result) { got = append(got, r) }); err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d results, want 3", len(got))
	}
	if got[0].Test.ASTSignature != "deadbeefcafe0001" {
		t.Errorf("result[0] sig = %q, want deadbeefcafe0001", got[0].Test.ASTSignature)
	}
	if got[1].Test.ASTSignature != "deadbeefcafe0002" {
		t.Errorf("result[1] sig = %q, want deadbeefcafe0002", got[1].Test.ASTSignature)
	}
	if got[2].Test.ASTSignature != "" {
		t.Errorf("result[2] sig = %q, want empty (no map entry)", got[2].Test.ASTSignature)
	}
}

// parseReport must tolerate a nil sigs map (the v1.0 path / parser unavailable).
func TestParseReportNilSigsIsEmptySignature(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "report_basic.json"))
	if err != nil {
		t.Fatal(err)
	}
	var got []adapter.Result
	if err := parseReport(raw, "/work", time.Now(), nil, func(r adapter.Result) { got = append(got, r) }); err != nil {
		t.Fatal(err)
	}
	for i, r := range got {
		if r.Test.ASTSignature != "" {
			t.Errorf("result[%d] sig = %q, want empty for nil sigs", i, r.Test.ASTSignature)
		}
	}
}

func TestNodeBinsDefaultAndOverride(t *testing.T) {
	if got := (&Adapter{}).nodeBins(); len(got) != 1 || got[0] != "node" {
		t.Errorf("default nodeBins = %v, want [node]", got)
	}
	if got := (&Adapter{NodeBin: "/custom/node"}).nodeBins(); len(got) != 1 || got[0] != "/custom/node" {
		t.Errorf("override nodeBins = %v", got)
	}
}

func TestAstSignaturesEmptyPathsReturnsNil(t *testing.T) {
	if got := New().astSignatures(context.Background(), t.TempDir(), nil); got != nil {
		t.Errorf("astSignatures(nil paths) = %v, want nil", got)
	}
}

// When every candidate node binary fails to launch, astSignatures exhausts the
// loop (cmd.Output error → continue) and returns nil so execution proceeds with
// empty signatures. Exercises the node-unavailable degradation without a node.
func TestAstSignaturesNodeFailureReturnsNil(t *testing.T) {
	a := &Adapter{NodeBin: "teo-nonexistent-node-xyz"}
	if got := a.astSignatures(context.Background(), t.TempDir(), []string{"x.test.ts"}); got != nil {
		t.Errorf("astSignatures with unrunnable node = %v, want nil", got)
	}
}

// The Node helper keys its output map by the verbatim argv path. parseReport
// looks signatures up with filepath.Rel(workdir, reportName) — an OS-native
// (backslash-on-Windows) relative path. This pins that the two agree for a
// multi-segment path: the Node outer key must equal the OS-native relative path
// parseReport would compute, or every signature silently goes empty. Guards the
// one cross-language invariant the separator-free fixtures can't reach.
func TestAstSignaturesKeyedByOSNativeSubdirPath(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not available")
	}
	parserPaths := babelParserPaths(t)
	if parserPaths == "" {
		t.Skip("@babel/parser not available (web/node_modules not installed)")
	}
	t.Setenv("TEO_JS_PARSER_PATHS", parserPaths)

	dir := t.TempDir()
	subdir := filepath.Join("src", "components")
	if err := os.MkdirAll(filepath.Join(dir, subdir), 0o750); err != nil {
		t.Fatal(err)
	}
	relPath := filepath.Join(subdir, "Button.test.tsx") // OS-native separators
	src := "it('renders', () => { expect(1).toBe(1); });\n"
	if err := os.WriteFile(filepath.Join(dir, relPath), []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}

	out := New().astSignatures(context.Background(), dir, []string{relPath})
	if out == nil {
		t.Fatal("astSignatures returned nil (node ran but produced no output?)")
	}
	// The outer key must be the exact OS-native relative path...
	sigs, ok := out[relPath]
	if !ok {
		t.Fatalf("output not keyed by OS-native path %q; got keys %v", relPath, keysOf(out))
	}
	// ...which is exactly what parseReport computes via filepath.Rel from the
	// report's absolute file name. If these ever diverge, the lookup misses.
	wantKey, err := filepath.Rel(dir, filepath.Join(dir, relPath))
	if err != nil {
		t.Fatal(err)
	}
	if wantKey != relPath {
		t.Fatalf("filepath.Rel key %q != argv key %q", wantKey, relPath)
	}
	if sigs["renders"] == "" {
		t.Errorf("missing signature for it('renders') under %q; got %v", relPath, sigs)
	}
}

func keysOf(m map[string]map[string]string) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	return ks
}

// babelParserPaths returns an absolute search path for @babel/parser, preferring
// the repo's web/node_modules (the Go test job may not have it installed). It
// returns "" when no resolvable parser location is found.
func babelParserPaths(t *testing.T) string {
	t.Helper()
	abs, err := filepath.Abs(filepath.Join("..", "..", "..", "web", "node_modules"))
	if err != nil {
		return ""
	}
	if _, err := os.Stat(filepath.Join(abs, "@babel", "parser", "package.json")); err != nil {
		return ""
	}
	return abs
}

// astSigsFor runs the embedded Node helper against a single fixture file, using
// the repo's @babel/parser via TEO_JS_PARSER_PATHS. Skips when node or the parser
// is unavailable (e.g. a CI job without the web deps installed) — the same
// degrade-gracefully contract pytest's tests rely on for a missing interpreter.
func astSigsFor(t *testing.T, src string) map[string]string {
	t.Helper()
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not available")
	}
	parserPaths := babelParserPaths(t)
	if parserPaths == "" {
		t.Skip("@babel/parser not available (web/node_modules not installed)")
	}
	t.Setenv("TEO_JS_PARSER_PATHS", parserPaths)

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "x.test.ts"), []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	out := New().astSignatures(context.Background(), dir, []string{"x.test.ts"})
	if out == nil {
		t.Fatal("astSignatures returned nil (node ran but produced no output?)")
	}
	return out["x.test.ts"]
}

// Keys must match the per-test Name parseReport builds: ancestor describe titles
// joined to the test title with " > ".
func TestJestASTSignatureKeysMatchReportNames(t *testing.T) {
	src := `describe('Button', () => {
  it('renders the label', () => {
    const x = 1;
    expect(x).toBe(1);
  });
  describe('when disabled', () => {
    test('does not fire onClick', () => {
      expect(true).toBe(true);
    });
  });
});
`
	got := astSigsFor(t, src)
	if got["Button > renders the label"] == "" {
		t.Errorf("missing signature for nested it(); got keys %v", got)
	}
	if got["Button > when disabled > does not fire onClick"] == "" {
		t.Errorf("missing signature for doubly-nested test(); got keys %v", got)
	}
}

// Reformatting + comment edits must not change the signature; a logic change must.
func TestJestASTSignatureStability(t *testing.T) {
	base := `it('t', () => {
  const x = 1;
  expect(x).toBe(1);
});
`
	reformatted := `it('t', () => {
  // a comment
  const x = 1;

  expect(x).toBe(1); // trailing
});
`
	changed := `it('t', () => {
  const x = 2;
  expect(x).toBe(2);
});
`
	s1 := astSigsFor(t, base)["t"]
	s2 := astSigsFor(t, reformatted)["t"]
	s3 := astSigsFor(t, changed)["t"]

	if s1 == "" {
		t.Fatal("empty signature for base")
	}
	if s1 != s2 {
		t.Errorf("signature changed across formatting/comment edits: %s vs %s", s1, s2)
	}
	if s1 == s3 {
		t.Error("signature should change when the test logic changes")
	}
}

// Dynamic titles (it.each, template interpolation) can't be matched to a report
// Name, so they're skipped rather than keyed under an unpredictable string.
func TestJestASTSignatureSkipsDynamicTitles(t *testing.T) {
	src := "const n = 5;\n" +
		"it(`computed ${n}`, () => { expect(n).toBe(5); });\n" +
		"it.each([1,2])('each %i', (v) => { expect(v).toBeGreaterThan(0); });\n" +
		"it('static', () => { expect(true).toBe(true); });\n"
	got := astSigsFor(t, src)
	if got["static"] == "" {
		t.Error("expected a signature for the static-title test")
	}
	if len(got) != 1 {
		t.Errorf("expected only the static test to be keyed, got %v", got)
	}
}
