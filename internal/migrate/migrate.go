// Package migrate runs forward-only schema migrations against Postgres and ClickHouse.
// Migration files live on disk under <dir>/postgres and <dir>/clickhouse. The container
// image copies the repository's migrations/ tree to /opt/teo/migrations.
package migrate

import (
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	_ "github.com/ClickHouse/clickhouse-go/v2"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

// Backend identifies the target store.
type Backend string

const (
	Postgres   Backend = "postgres"
	ClickHouse Backend = "clickhouse"
)

// File represents one .up.sql or .down.sql migration.
type File struct {
	Version  int
	Name     string
	Down     bool
	Contents string
}

func loadFiles(dir string) ([]File, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read migration dir %s: %w", dir, err)
	}
	var files []File
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		base := e.Name()
		if !strings.HasSuffix(base, ".sql") {
			continue
		}
		var version int
		var rest string
		if _, err := fmt.Sscanf(base, "%d_%s", &version, &rest); err != nil {
			return nil, fmt.Errorf("malformed migration filename %q: %w", base, err)
		}
		down := strings.HasSuffix(base, ".down.sql")
		b, err := fs.ReadFile(os.DirFS(dir), base)
		if err != nil {
			return nil, err
		}
		files = append(files, File{
			Version:  version,
			Name:     base,
			Down:     down,
			Contents: string(b),
		})
	}
	sort.Slice(files, func(i, j int) bool {
		if files[i].Version != files[j].Version {
			return files[i].Version < files[j].Version
		}
		return !files[i].Down
	})
	return files, nil
}

// Up applies all unapplied up migrations to the backend at dsn.
// dir is the parent migrations directory; subdirectory <backend>/ is read.
func Up(b Backend, dsn, dir string) error {
	subdir := filepath.Join(dir, string(b))
	files, err := loadFiles(subdir)
	if err != nil {
		return err
	}

	driver, err := openDriver(b, dsn)
	if err != nil {
		return err
	}
	defer driver.Close()

	if err := ensureSchemaTable(b, driver); err != nil {
		return fmt.Errorf("ensure schema table: %w", err)
	}
	current, err := currentVersion(driver)
	if err != nil {
		return fmt.Errorf("read current version: %w", err)
	}

	for _, f := range files {
		if f.Down {
			continue
		}
		if f.Version <= current {
			continue
		}
		fmt.Printf("[migrate %s] applying %s\n", b, f.Name)
		if err := applyMigration(b, driver, f); err != nil {
			return fmt.Errorf("apply %s: %w", f.Name, err)
		}
		if err := recordVersion(b, driver, f.Version, f.Name); err != nil {
			return fmt.Errorf("record %s: %w", f.Name, err)
		}
	}
	return nil
}

// Status returns the current applied version (0 if none).
func Status(b Backend, dsn string) (int, error) {
	driver, err := openDriver(b, dsn)
	if err != nil {
		return 0, err
	}
	defer driver.Close()
	if err := ensureSchemaTable(b, driver); err != nil {
		return 0, err
	}
	return currentVersion(driver)
}

func openDriver(b Backend, dsn string) (*sql.DB, error) {
	switch b {
	case Postgres:
		// Migrations are applied as multi-statement SQL files containing
		// plpgsql function bodies wrapped in $$...$$ dollar quotes. pgx's
		// default QueryExecModeCacheStatement uses the extended protocol
		// and scans the SQL for $N parameter placeholders, which corrupts
		// dollar-quoted strings (parser sees the rewritten text and bails
		// with "syntax error at or near (" on the first CREATE TABLE).
		// Simple protocol forwards the SQL byte-for-byte to Postgres.
		cfg, err := pgx.ParseConfig(dsn)
		if err != nil {
			return nil, fmt.Errorf("parse pg dsn: %w", err)
		}
		cfg.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol
		db := stdlib.OpenDB(*cfg)
		return db, db.Ping()
	case ClickHouse:
		db, err := sql.Open("clickhouse", dsn)
		if err != nil {
			return nil, err
		}
		return db, db.Ping()
	}
	return nil, fmt.Errorf("unknown backend %s", b)
}

func ensureSchemaTable(b Backend, db *sql.DB) error {
	switch b {
	case Postgres:
		_, err := db.Exec(`
            CREATE SCHEMA IF NOT EXISTS teo;
            CREATE TABLE IF NOT EXISTS teo.schema_migrations (
                version INT PRIMARY KEY,
                name TEXT NOT NULL,
                applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
            );
        `)
		return err
	case ClickHouse:
		if _, err := db.Exec(`CREATE DATABASE IF NOT EXISTS teo`); err != nil {
			return err
		}
		_, err := db.Exec(`
            CREATE TABLE IF NOT EXISTS teo.schema_migrations (
                version Int32,
                name String,
                applied_at DateTime DEFAULT now()
            ) ENGINE = ReplacingMergeTree ORDER BY version
        `)
		return err
	}
	return errors.New("unsupported backend")
}

func currentVersion(db *sql.DB) (int, error) {
	var v sql.NullInt64
	if err := db.QueryRow(`SELECT max(version) FROM teo.schema_migrations`).Scan(&v); err != nil {
		return 0, err
	}
	if !v.Valid {
		return 0, nil
	}
	return int(v.Int64), nil
}

func applyMigration(_ Backend, db *sql.DB, f File) error {
	stmts := splitSQL(f.Contents)
	for i, stmt := range stmts {
		if strings.TrimSpace(stmt) == "" {
			continue
		}
		if _, err := db.Exec(stmt); err != nil {
			return fmt.Errorf("statement %d %q: %w", i+1, firstLine(stmt), err)
		}
	}
	return nil
}

func recordVersion(b Backend, db *sql.DB, v int, name string) error {
	switch b {
	case Postgres:
		_, err := db.Exec(`INSERT INTO teo.schema_migrations (version, name) VALUES ($1, $2)`, v, name)
		return err
	case ClickHouse:
		_, err := db.Exec(`INSERT INTO teo.schema_migrations (version, name) VALUES (?, ?)`, v, name)
		return err
	}
	return errors.New("unsupported backend")
}

// splitSQL breaks a multi-statement SQL script into individual statements,
// respecting Postgres-flavour syntax that the naive line-based split can't
// handle:
//
//   - $$ ... $$ and $tag$ ... $tag$ dollar-quoted strings (plpgsql function
//     bodies live in these; their internal `;`s must NOT split the function)
//   - '...' single-quoted literals with the SQL standard '' escape
//   - "..." double-quoted identifiers
//   - -- ... \n line comments
//   - /* ... */ block comments
//
// Splits on top-level ';'. Empty / whitespace-only statements are dropped.
//
// Why this exists at all: pgx/v5's stdlib Exec rewrites SQL client-side
// (parameter sanitization) and that has corrupted dollar-quoted blocks for
// us in CI even with QueryExecModeSimpleProtocol. Statement-by-statement
// Exec sidesteps the entire pgx rewrite path and gives per-statement error
// messages when something does break.
func splitSQL(sql string) []string {
	var out []string
	var cur strings.Builder
	flush := func() {
		s := strings.TrimSpace(cur.String())
		if s != "" {
			out = append(out, s)
		}
		cur.Reset()
	}
	i, n := 0, len(sql)
	for i < n {
		c := sql[i]
		switch {
		case c == '-' && i+1 < n && sql[i+1] == '-':
			// line comment to end of line
			for i < n && sql[i] != '\n' {
				cur.WriteByte(sql[i])
				i++
			}
		case c == '/' && i+1 < n && sql[i+1] == '*':
			cur.WriteByte('/')
			cur.WriteByte('*')
			i += 2
			for i+1 < n && !(sql[i] == '*' && sql[i+1] == '/') {
				cur.WriteByte(sql[i])
				i++
			}
			if i+1 < n {
				cur.WriteByte('*')
				cur.WriteByte('/')
				i += 2
			}
		case c == '\'':
			cur.WriteByte('\'')
			i++
			for i < n {
				if sql[i] == '\'' {
					cur.WriteByte('\'')
					i++
					if i < n && sql[i] == '\'' {
						cur.WriteByte('\'')
						i++
						continue
					}
					break
				}
				cur.WriteByte(sql[i])
				i++
			}
		case c == '"':
			cur.WriteByte('"')
			i++
			for i < n && sql[i] != '"' {
				cur.WriteByte(sql[i])
				i++
			}
			if i < n {
				cur.WriteByte('"')
				i++
			}
		case c == '$':
			// Look for a $tag$ open (tag may be empty, giving $$)
			tagEnd := -1
			for j := i + 1; j < n; j++ {
				b := sql[j]
				if b == '$' {
					tagEnd = j
					break
				}
				if !isDollarTagByte(b) {
					break
				}
			}
			if tagEnd < 0 {
				cur.WriteByte('$')
				i++
				continue
			}
			tag := sql[i : tagEnd+1] // includes leading + trailing $
			cur.WriteString(tag)
			i = tagEnd + 1
			// Read until the same tag closes the block.
			closeIdx := strings.Index(sql[i:], tag)
			if closeIdx < 0 {
				// Unterminated dollar quote — copy the rest verbatim.
				cur.WriteString(sql[i:])
				i = n
			} else {
				cur.WriteString(sql[i : i+closeIdx])
				cur.WriteString(tag)
				i += closeIdx + len(tag)
			}
		case c == ';':
			cur.WriteByte(';')
			i++
			flush()
		default:
			cur.WriteByte(c)
			i++
		}
	}
	flush()
	return out
}

// isDollarTagByte reports whether b is a legal character inside a Postgres
// dollar-quote tag. Per the manual: the tag may consist of letters, digits,
// and underscores; it must not start with a digit (we don't enforce that —
// the leading dollar already constrains where this is called).
func isDollarTagByte(b byte) bool {
	return (b >= 'a' && b <= 'z') ||
		(b >= 'A' && b <= 'Z') ||
		(b >= '0' && b <= '9') ||
		b == '_'
}


func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i > 0 {
		return s[:i]
	}
	return s
}
