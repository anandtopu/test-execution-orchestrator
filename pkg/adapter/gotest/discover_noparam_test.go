package gotest

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestGoDiscoverNoParamSuffix documents (and verifies) why the pytest
// parametrized-signature fix is unnecessary for the Go adapter: `go test -list`
// emits only bare top-level TestXxx names — subtests/table cases are never
// enumerated — so a discovered Name never carries a "[param]" suffix and its
// signature attaches directly via sigs[pkg+"::"+name] with no stripParams step.
//
// We build a temp module with a table-driven test and assert Discover yields the
// single bare name with a non-empty signature.
func TestGoDiscoverNoParamSuffix(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not available")
	}
	dir := t.TempDir()
	write := func(name, content string) {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600))
	}
	write("go.mod", "module example.com/m\n\ngo 1.25\n")
	// A table-driven test: the subtest case names ("a", "b") must NOT appear in
	// `go test -list` output.
	write("foo_test.go", "package m\n\nimport \"testing\"\n\n"+
		"func TestTable(t *testing.T) {\n"+
		"\tcases := []string{\"a\", \"b\"}\n"+
		"\tfor _, c := range cases {\n"+
		"\t\tt.Run(c, func(t *testing.T) { _ = c })\n"+
		"\t}\n"+
		"}\n")

	entries, err := New().Discover(context.Background(), dir)
	require.NoError(t, err)
	require.Len(t, entries, 1, "go test -list yields only the top-level test name")

	e := entries[0]
	require.Equal(t, "TestTable", e.Name)
	require.NotContains(t, e.Name, "[", "go discovery names never carry a param suffix")
	require.NotContains(t, e.Name, "/", "go discovery names never carry a subtest path")
	require.Equal(t, "example.com/m", e.Path)
	require.NotEmpty(t, e.ASTSignature, "signature attaches via sigs[pkg+\"::\"+name]")
}
