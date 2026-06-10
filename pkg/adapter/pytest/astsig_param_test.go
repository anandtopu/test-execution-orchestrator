package pytest

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/teo-dev/teo/internal/model"
)

// attachByStripParams replicates Discover's attach loop body for a single path's
// signature map: it keys the lookup on stripParams(Name) so every parametrized
// variant inherits its base function body's signature. Kept local to the tests
// so the keying contract is exercised exactly as Discover would, without
// spawning Python. Mirrors attachSignatures for a single file.
func attachByStripParams(entries []model.TestEntry, byName map[string]string) {
	for i := range entries {
		entries[i].ASTSignature = byName[stripParams(entries[i].Name)]
	}
}

// TestParametrizedVariantsInheritBaseSignature: both [case1] and [case2]
// variants of a parametrized test resolve to the SAME base-function signature,
// and a non-parametrized sibling resolves to its own. This is the core
// regression: the lookup must key on the bare qualname, not the suffixed Name.
func TestParametrizedVariantsInheritBaseSignature(t *testing.T) {
	collect := []byte("test_x.py::test_two[case1]\n" +
		"test_x.py::test_two[case2]\n" +
		"test_x.py::test_one\n")
	entries := parseCollect(collect)
	require.Len(t, entries, 3)

	sigs := map[string]map[string]string{
		"test_x.py": {
			"test_two": "aaaaaaaaaaaaaaaa",
			"test_one": "bbbbbbbbbbbbbbbb",
		},
	}

	// Replicate the Discover attach loop using stripParams(entries[i].Name).
	for i := range entries {
		if byName, ok := sigs[entries[i].Path]; ok {
			entries[i].ASTSignature = byName[stripParams(entries[i].Name)]
		}
	}

	require.Equal(t, "aaaaaaaaaaaaaaaa", entries[0].ASTSignature, "[case1] variant inherits base sig")
	require.Equal(t, "aaaaaaaaaaaaaaaa", entries[1].ASTSignature, "[case2] variant inherits base sig")
	require.Equal(t, "bbbbbbbbbbbbbbbb", entries[2].ASTSignature, "non-parametrized test keeps own sig")
}

// TestParametrizedClassMethodLookupKey: a parametrized Test* class method's
// parsed Name is the bare "Class::method" (parseCollect peels the "[param]"
// suffix), and stripParams is a no-op on it — so it matches the Python helper's
// "Class::method" key directly.
func TestParametrizedClassMethodLookupKey(t *testing.T) {
	entries := parseCollect([]byte("test_x.py::TestClass::test_three[case1]\n"))
	require.Len(t, entries, 1)
	require.Equal(t, "TestClass::test_three", entries[0].Name)
	require.Equal(t, "TestClass::test_three", stripParams(entries[0].Name))
}

// TestNonParametrizedAttachUnchanged is a regression guard: for entries whose
// Name carries no "[param]" suffix, stripParams returns them unchanged and the
// attached signature equals byName[Name] exactly. Guards against the new
// stripParams keying altering non-parametrized behavior.
func TestNonParametrizedAttachUnchanged(t *testing.T) {
	byName := map[string]string{
		"test_one":              "1111111111111111",
		"TestClass::test_three": "3333333333333333",
	}
	entries := []model.TestEntry{
		{Path: "test_x.py", Name: "test_one"},
		{Path: "test_x.py", Name: "TestClass::test_three"},
	}
	for _, e := range entries {
		require.Equal(t, e.Name, stripParams(e.Name), "stripParams must be a no-op on bracket-free names")
	}
	attachByStripParams(entries, byName)
	require.Equal(t, byName["test_one"], entries[0].ASTSignature)
	require.Equal(t, byName["TestClass::test_three"], entries[1].ASTSignature)
}

// TestStripParamsBareAndClass locks the helper contract used by the new lookup:
// a bare parametrized name and a parametrized class method both strip down to
// their base qualname.
func TestStripParamsBareAndClass(t *testing.T) {
	require.Equal(t, "test_two", stripParams("test_two[case1]"))
	require.Equal(t, "TestClass::test_three", stripParams("TestClass::test_three[a-b]"))
}

// TestParametrizedVariantsInheritBaseSignaturePython is the Python-gated
// end-to-end: a real Python interpreter computes the body signature for
// test_two, and both parametrized variants (built via parseCollect) attach the
// same non-empty signature through the stripParams lookup.
func TestParametrizedVariantsInheritBaseSignaturePython(t *testing.T) {
	if !pythonAvailable() {
		t.Skip("python interpreter not available")
	}
	dir := t.TempDir()
	// astSignatures keys on the def, so a plain function body suffices; the
	// @pytest.mark.parametrize decorator is irrelevant to the signature.
	src := "import pytest\n\n" +
		"@pytest.mark.parametrize(\"n\", [1, 2])\n" +
		"def test_two(n):\n" +
		"    x = n + 1\n" +
		"    assert x > n\n"
	writeTemp(t, dir, "test_x.py", src)

	sigs := New().astSignatures(context.Background(), dir, []string{"test_x.py"})
	require.NotNil(t, sigs, "astSignatures returned nil (python ran but produced no output?)")
	baseSig := sigs["test_x.py"]["test_two"]
	require.NotEmpty(t, baseSig, "expected a non-empty body signature for test_two")

	entries := parseCollect([]byte("test_x.py::test_two[case1]\ntest_x.py::test_two[case2]\n"))
	require.Len(t, entries, 2)
	for i := range entries {
		if byName, ok := sigs[entries[i].Path]; ok {
			entries[i].ASTSignature = byName[stripParams(entries[i].Name)]
		}
	}
	require.Equal(t, baseSig, entries[0].ASTSignature)
	require.Equal(t, baseSig, entries[1].ASTSignature)
}
