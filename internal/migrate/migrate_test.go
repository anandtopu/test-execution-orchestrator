package migrate

import "testing"

func TestSplitStatements(t *testing.T) {
	in := "CREATE TABLE a (x Int);\nCREATE TABLE b (y Int);\n"
	got := splitStatements(in)
	if len(got) != 2 {
		t.Fatalf("got %d statements, want 2: %#v", len(got), got)
	}
}

func TestSplitStatementsTrailingNoSemicolon(t *testing.T) {
	in := "SELECT 1\n"
	got := splitStatements(in)
	if len(got) != 1 {
		t.Fatalf("got %d statements, want 1", len(got))
	}
}
