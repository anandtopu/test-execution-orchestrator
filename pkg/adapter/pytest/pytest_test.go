package pytest

import "testing"

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

func TestStripParams(t *testing.T) {
	if got := stripParams("a::b[c-d]"); got != "a::b" {
		t.Fatalf("got %s", got)
	}
	if got := stripParams("a::b"); got != "a::b" {
		t.Fatalf("got %s", got)
	}
}
