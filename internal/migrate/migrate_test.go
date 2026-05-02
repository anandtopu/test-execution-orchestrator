package migrate

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestSplitSQL_TwoSimpleStatements(t *testing.T) {
	in := "CREATE TABLE a (x Int);\nCREATE TABLE b (y Int);\n"
	got := splitSQL(in)
	if len(got) != 2 {
		t.Fatalf("got %d statements, want 2: %#v", len(got), got)
	}
}

func TestSplitSQL_TrailingNoSemicolon(t *testing.T) {
	in := "SELECT 1\n"
	got := splitSQL(in)
	if len(got) != 1 {
		t.Fatalf("got %d statements, want 1", len(got))
	}
}

// TestSplitSQL_DollarQuotedFunctionStaysIntact is the regression test for the
// CI failure: `001_initial.up.sql` defines a plpgsql trigger function with a
// `BEGIN ... NEW.x := now(); RETURN NEW; END;` body wrapped in $$...$$. The
// naive line-based splitter broke on the inner semicolons. This test locks in
// that splitSQL keeps the whole function as one statement.
func TestSplitSQL_DollarQuotedFunctionStaysIntact(t *testing.T) {
	in := `
CREATE OR REPLACE FUNCTION teo.set_updated_at() RETURNS trigger AS $$
BEGIN
    NEW.updated_at := now();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TABLE teo.repos (id UUID PRIMARY KEY);
`
	got := splitSQL(in)
	if len(got) != 2 {
		t.Fatalf("got %d statements, want 2: %#v", len(got), got)
	}
	if !strings.Contains(got[0], "BEGIN") || !strings.Contains(got[0], "END;") {
		t.Errorf("function body fragmented; first statement = %q", got[0])
	}
	if !strings.HasPrefix(strings.TrimSpace(got[1]), "CREATE TABLE") {
		t.Errorf("second statement should be the CREATE TABLE; got %q", got[1])
	}
}

func TestSplitSQL_NamedDollarTag(t *testing.T) {
	// $func$ is a tagged dollar quote — body has a $$ inside that should NOT
	// terminate the outer block.
	in := `CREATE FUNCTION f() RETURNS text AS $func$ SELECT 'a$$b'; $func$ LANGUAGE sql;
SELECT 1;`
	got := splitSQL(in)
	if len(got) != 2 {
		t.Fatalf("got %d statements, want 2: %#v", len(got), got)
	}
}

func TestSplitSQL_SingleQuoteWithEscapedQuoteAndSemicolon(t *testing.T) {
	// Embedded ';' inside a quoted literal must not split.
	in := `INSERT INTO t (s) VALUES ('a''b;c');
SELECT 1;`
	got := splitSQL(in)
	if len(got) != 2 {
		t.Fatalf("got %d statements, want 2: %#v", len(got), got)
	}
	if !strings.Contains(got[0], "'a''b;c'") {
		t.Errorf("escaped quote lost; first statement = %q", got[0])
	}
}

func TestSplitSQL_LineCommentWithSemicolon(t *testing.T) {
	in := `SELECT 1; -- comment with ; in it
SELECT 2;`
	got := splitSQL(in)
	if len(got) != 2 {
		t.Fatalf("got %d statements, want 2: %#v", len(got), got)
	}
}

func TestSplitSQL_BlockCommentWithSemicolon(t *testing.T) {
	in := `SELECT 1 /* ; nope ; */;
SELECT 2;`
	got := splitSQL(in)
	if len(got) != 2 {
		t.Fatalf("got %d statements, want 2: %#v", len(got), got)
	}
}

func TestSplitSQL_DoubleQuotedIdentifier(t *testing.T) {
	// Quoted identifier may contain a ';' that must not split.
	in := `CREATE TABLE "weird;name" (x int);
SELECT 1;`
	got := splitSQL(in)
	if len(got) != 2 {
		t.Fatalf("got %d statements, want 2: %#v", len(got), got)
	}
}

// TestSplitSQL_RealMigrationFile runs the live 001_initial.up.sql through the
// splitter and asserts it produces a sensible statement count. Doesn't
// validate that the SQL is valid Postgres (that needs a real server, which is
// what the integration tests do) — but catches splitter regressions and
// drift in the migration shape that future-me would otherwise only spot in
// CI logs.
func TestSplitSQL_RealMigrationFile(t *testing.T) {
	_, here, _, _ := runtime.Caller(0)
	path := filepath.Join(filepath.Dir(here), "..", "..", "migrations", "postgres", "001_initial.up.sql")
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read migration: %v", err)
	}
	stmts := splitSQL(string(body))
	if len(stmts) < 30 {
		t.Fatalf("got %d statements; the migration declares many more — splitter likely merged statements", len(stmts))
	}
	for i, s := range stmts {
		if !strings.HasSuffix(strings.TrimSpace(s), ";") && !strings.Contains(strings.ToUpper(s), "LANGUAGE PLPGSQL") {
			// Permit the function body which doesn't end the statement on `;`
			// before the LANGUAGE clause.
			t.Errorf("statement %d does not end with ';': %q", i+1, firstLine(s))
		}
	}
}
