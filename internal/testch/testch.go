//go:build integration

// Package testch spins up an ephemeral ClickHouse via testcontainers, applies
// the project's ClickHouse migrations, and returns a native driver.Conn ready
// for the OTLP span-ingest write path. Build-tagged so unit-test runs don't
// require Docker.
//
// Mirrors internal/testpg (Postgres) and internal/testminio (MinIO). The
// dedicated clickhouse testcontainers module is not vendored, so we use the
// generic testcontainers.Run on the official clickhouse-server image and gate
// readiness on an HTTP ping of the 8123 interface — never time.Sleep.
package testch

import (
	"context"
	"fmt"
	"net/http"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	chdriver "github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/teo-dev/teo/internal/db"
	"github.com/teo-dev/teo/internal/migrate"
)

// Default single-node ClickHouse credentials. The clickhouse-server image
// serves the `default` user with an empty password and a `default` database;
// the TEO migrations create the `teo` database themselves, so we point the DSN
// at `default` for the migrate connection and rely on `CREATE DATABASE IF NOT
// EXISTS teo` in 001_initial.
const (
	DefaultUser     = "default"
	DefaultPassword = ""
	DefaultDatabase = "teo"
)

// Start launches ClickHouse, applies all ClickHouse migrations, and returns a
// native driver.Conn pointed at the `teo` database, the DSN, and a cleanup
// function. Callers do `t.Cleanup(cleanup)`.
//
// Readiness is gated on an HTTP 200 from the /ping endpoint on the 8123 HTTP
// port, which ClickHouse only answers once it can serve queries — this avoids
// the racey "container started but server not accepting connections" gap that
// a bare ForListeningPort wait leaves open. No time.Sleep anywhere.
func Start(t *testing.T) (conn chdriver.Conn, dsn string, cleanup func()) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	container, err := testcontainers.Run(ctx,
		"clickhouse/clickhouse-server:24.8-alpine",
		testcontainers.WithEnv(map[string]string{
			"CLICKHOUSE_USER":     DefaultUser,
			"CLICKHOUSE_PASSWORD": DefaultPassword,
			"CLICKHOUSE_DB":       "default",
			// Lets the default user create the `teo` database via migrations.
			"CLICKHOUSE_DEFAULT_ACCESS_MANAGEMENT": "1",
		}),
		testcontainers.WithExposedPorts("9000/tcp", "8123/tcp"),
		testcontainers.WithWaitStrategyAndDeadline(90*time.Second,
			wait.ForHTTP("/ping").
				WithPort("8123/tcp").
				WithStatusCodeMatcher(func(status int) bool { return status == http.StatusOK }),
		),
	)
	if err != nil {
		t.Fatalf("start clickhouse container: %v", err)
	}

	host, err := container.Host(ctx)
	if err != nil {
		_ = testcontainers.TerminateContainer(container)
		t.Fatalf("clickhouse host: %v", err)
	}
	port, err := container.MappedPort(ctx, "9000/tcp")
	if err != nil {
		_ = testcontainers.TerminateContainer(container)
		t.Fatalf("clickhouse mapped port: %v", err)
	}

	// Two DSNs, same server. The MIGRATE connection must target a database that
	// already exists: the `teo` database is created by 001_initial itself
	// (CREATE DATABASE IF NOT EXISTS teo) and every table is fully qualified
	// `teo.*`, so connecting the migrate driver to `teo` up front fails with
	// "bad connection" (the database isn't there yet). Run migrations against
	// the always-present `default` database; return the WRITE-path DSN/conn
	// pointed at `teo`, where the migrations land span_events.
	migrateDSN := fmt.Sprintf("clickhouse://%s:%s@%s:%s/%s",
		DefaultUser, DefaultPassword, host, port.Port(), "default")
	dsn = fmt.Sprintf("clickhouse://%s:%s@%s:%s/%s",
		DefaultUser, DefaultPassword, host, port.Port(), DefaultDatabase)

	if err := migrate.Up(migrate.ClickHouse, migrateDSN, repoMigrationsDir()); err != nil {
		_ = testcontainers.TerminateContainer(container)
		t.Fatalf("apply clickhouse migrations: %v", err)
	}

	conn, err = db.OpenClickHouseConn(ctx, dsn)
	if err != nil {
		_ = testcontainers.TerminateContainer(container)
		t.Fatalf("open clickhouse native conn: %v", err)
	}

	cleanup = func() {
		_ = conn.Close()
		_ = testcontainers.TerminateContainer(container)
	}
	return conn, dsn, cleanup
}

// repoMigrationsDir returns the absolute path to the project's `migrations/`
// folder, anchored on this package's own source-file location so it resolves
// regardless of which package's tests invoke Start. Mirrors
// internal/testpg.repoMigrationsDir.
func repoMigrationsDir() string {
	_, thisFile, _, _ := runtime.Caller(0)
	// internal/testch/testch.go → ../../migrations
	return filepath.Join(filepath.Dir(thisFile), "..", "..", "migrations")
}
