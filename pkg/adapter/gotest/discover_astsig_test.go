package gotest

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// End-to-end: astSignatures must populate a signature for a real test function
// discovered via `go list` in a temp module.
func TestAstSignaturesEndToEnd(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not available")
	}
	dir := t.TempDir()
	write := func(name, content string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("go.mod", "module example.com/m\n\ngo 1.25\n")
	write("foo_test.go", "package m\nimport \"testing\"\nfunc TestFoo(t *testing.T) { t.Log(\"hi\") }\n")

	sigs := New().astSignatures(context.Background(), dir)
	if sigs["example.com/m::TestFoo"] == "" {
		t.Fatalf("expected a signature for example.com/m::TestFoo, got map: %#v", sigs)
	}
}
