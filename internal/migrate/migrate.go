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
	_ "github.com/jackc/pgx/v5/stdlib"
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
		db, err := sql.Open("pgx", dsn)
		if err != nil {
			return nil, err
		}
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

func applyMigration(b Backend, db *sql.DB, f File) error {
	if b == ClickHouse {
		for _, stmt := range splitStatements(f.Contents) {
			if strings.TrimSpace(stmt) == "" {
				continue
			}
			if _, err := db.Exec(stmt); err != nil {
				return fmt.Errorf("statement %q: %w", firstLine(stmt), err)
			}
		}
		return nil
	}
	_, err := db.Exec(f.Contents)
	return err
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

func splitStatements(sql string) []string {
	var out []string
	var cur strings.Builder
	for _, line := range strings.Split(sql, "\n") {
		cur.WriteString(line)
		cur.WriteString("\n")
		if strings.HasSuffix(strings.TrimSpace(line), ";") {
			out = append(out, cur.String())
			cur.Reset()
		}
	}
	if strings.TrimSpace(cur.String()) != "" {
		out = append(out, cur.String())
	}
	return out
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i > 0 {
		return s[:i]
	}
	return s
}
