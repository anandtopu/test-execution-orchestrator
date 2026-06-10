package pytest

import (
	"testing"

	"github.com/teo-dev/teo/internal/model"
)

func TestParseCollectBasic(t *testing.T) {
	in := []byte(`tests/test_a.py::test_one
tests/test_a.py::test_two[case1]
tests/test_b.py::TestClass::test_three
3 tests collected in 0.05s
`)
	got := parseCollect(in)
	if len(got) != 3 {
		t.Fatalf("got %d, want 3: %#v", len(got), got)
	}
	if got[0].Path != "tests/test_a.py" || got[0].Name != "test_one" {
		t.Errorf("entry 0 wrong: %+v", got[0])
	}
	if got[1].ParamsHash == "" {
		t.Error("entry 1 should have a params hash")
	}
	if got[2].Name != "TestClass::test_three" {
		t.Errorf("entry 2 name wrong: %s", got[2].Name)
	}
}

// TestAttachSignatures pins the attachment contract in Discover's loop body:
// the signature lookup is keyed on the BARE qualname (stripParams(Name)), which
// the Python helper produces, NOT on the raw entry Name. This guards the
// decoupling fix — reverting to byName[entries[i].Name] makes the parametrized
// case below resolve to "" and fails this test, even though parseCollect
// happens to pre-strip the suffix today.
func TestAttachSignatures(t *testing.T) {
	// astSignatures keys by bare qualname, no "[param]" suffix.
	sigs := map[string]map[string]string{
		"tests/test_a.py": {
			"test_one":          "sig-one",
			"TestBar::test_two": "sig-two",
		},
	}
	entries := []model.TestEntry{
		// Non-parametrized module func: resolves directly.
		{Path: "tests/test_a.py", Name: "test_one"},
		// Parametrized variant whose Name STILL carries the "[case1]" suffix
		// (the defensive branch that is unreachable through parseCollect today
		// but is exactly what the stripParams keying exists to handle).
		{Path: "tests/test_a.py", Name: "test_one[case1]"},
		// Parametrized class method, suffix present.
		{Path: "tests/test_a.py", Name: "TestBar::test_two[x-y]"},
		// Path the helper produced no signatures for: stays empty, no panic.
		{Path: "tests/test_missing.py", Name: "test_three"},
	}

	attachSignatures(entries, sigs)

	if entries[0].ASTSignature != "sig-one" {
		t.Errorf("bare name: got %q, want sig-one", entries[0].ASTSignature)
	}
	if entries[1].ASTSignature != "sig-one" {
		t.Errorf("parametrized variant should inherit base sig: got %q, want sig-one", entries[1].ASTSignature)
	}
	if entries[2].ASTSignature != "sig-two" {
		t.Errorf("parametrized class method: got %q, want sig-two", entries[2].ASTSignature)
	}
	if entries[3].ASTSignature != "" {
		t.Errorf("unknown path should stay empty: got %q", entries[3].ASTSignature)
	}

	// Explicit guard on the bare-qualname keying contract with astSignatures:
	// the raw (suffixed) Name must NOT be a key, only the stripped form is.
	byName := sigs["tests/test_a.py"]
	if _, ok := byName["test_one[case1]"]; ok {
		t.Fatal("test setup invalid: suffixed name should not be a key")
	}
	if byName[stripParams("test_one[case1]")] == "" {
		t.Fatal("stripParams(suffixed) must resolve against the bare-qualname keys")
	}
}

func TestStripParams(t *testing.T) {
	if got := stripParams("a::b[c-d]"); got != "a::b" {
		t.Fatalf("got %s", got)
	}
	if got := stripParams("a::b"); got != "a::b" {
		t.Fatalf("got %s", got)
	}
}
