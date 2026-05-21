package pytest

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/teo-dev/teo/internal/model"
)

func pythonAvailable() bool {
	for _, p := range []string{"python3", "python"} {
		if _, err := exec.LookPath(p); err == nil {
			return true
		}
	}
	return false
}

func writeTemp(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func sigsFor(t *testing.T, src string) map[string]string {
	t.Helper()
	dir := t.TempDir()
	writeTemp(t, dir, "test_x.py", src)
	out := New().astSignatures(context.Background(), dir, []string{"test_x.py"})
	if out == nil {
		t.Fatal("astSignatures returned nil (python ran but produced no output?)")
	}
	return out["test_x.py"]
}

// Module functions and Test* class methods both get signatures, keyed to match
// pytest nodeids ("test_foo" and "TestBar::test_baz").
func TestPythonASTSignaturesKeys(t *testing.T) {
	if !pythonAvailable() {
		t.Skip("python interpreter not available")
	}
	src := "def test_foo():\n    x = 1\n    assert x == 1\n\n" +
		"class TestBar:\n    def test_baz(self):\n        assert True\n"
	got := sigsFor(t, src)
	if got["test_foo"] == "" {
		t.Error("expected a signature for module function test_foo")
	}
	if got["TestBar::test_baz"] == "" {
		t.Error("expected a signature for method TestBar::test_baz")
	}
}

// Reformatting + comment edits must not change the signature; a logic change must.
func TestPythonASTSignatureStability(t *testing.T) {
	if !pythonAvailable() {
		t.Skip("python interpreter not available")
	}
	base := "def test_foo():\n    x = 1\n    assert x == 1\n"
	reformatted := "def test_foo():\n\n    # a comment\n    x = 1\n    assert x == 1  # trailing\n"
	changed := "def test_foo():\n    x = 2\n    assert x == 2\n"

	s1 := sigsFor(t, base)["test_foo"]
	s2 := sigsFor(t, reformatted)["test_foo"]
	s3 := sigsFor(t, changed)["test_foo"]

	if s1 == "" {
		t.Fatal("empty signature")
	}
	if s1 != s2 {
		t.Errorf("signature changed across formatting/comment edits: %s vs %s", s1, s2)
	}
	if s1 == s3 {
		t.Error("signature should change when the test logic changes")
	}
}

// distinctPaths dedupes while preserving order.
func TestDistinctPaths(t *testing.T) {
	in := []model.TestEntry{
		{Path: "a.py", Name: "t1"},
		{Path: "a.py", Name: "t2"},
		{Path: "b.py", Name: "t3"},
	}
	got := distinctPaths(in)
	if len(got) != 2 || got[0] != "a.py" || got[1] != "b.py" {
		t.Fatalf("distinctPaths = %v", got)
	}
}
