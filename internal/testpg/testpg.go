//go:build integration

// Package testpg spins up an ephemeral Postgres via testcontainers and applies
// the project's migrations. Build-tagged so unit-test runs don't require Docker.
package testpg

import (
	"context"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/teo-dev/teo/internal/db"
	"github.com/teo-dev/teo/internal/migrate"
)

// Start launches Postgres 16, applies all migrations, returns a *pgxpool.Pool
// and a cleanup function. Caller is expected to t.Cleanup(cleanup).
func Start(t *testing.T) (*pgxpool.Pool, func()) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	container, err := postgres.Run(ctx,
		"postgres:16-alpine",
		postgres.WithDatabase("teo"),
		postgres.WithUsername("teo"),
		postgres.WithPassword("teo"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).WithStartupTimeout(60*time.Second),
		),
	)
	if err != nil {
		t.Fatalf("start postgres container: %v", err)
	}

	dsn, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		_ = testcontainers.TerminateContainer(container)
		t.Fatalf("get dsn: %v", err)
	}

	if err := migrate.Up(migrate.Postgres, dsn, repoMigrationsDir()); err != nil {
		_ = testcontainers.TerminateContainer(container)
		t.Fatalf("apply migrations: %v", err)
	}

	pool, err := db.OpenPostgres(ctx, dsn)
	if err != nil {
		_ = testcontainers.TerminateContainer(container)
		t.Fatalf("open pgx pool: %v", err)
	}
	cleanup := func() {
		pool.Close()
		_ = testcontainers.TerminateContainer(container)
	}
	return pool, cleanup
}

// repoMigrationsDir returns the absolute path to the project's `migrations/`
// folder regardless of which package's tests are running. We anchor on the
// package's own source file location.
func repoMigrationsDir() string {
	_, thisFile, _, _ := runtime.Caller(0)
	// internal/testpg/testpg.go → ../../migrations
	return filepath.Join(filepath.Dir(thisFile), "..", "..", "migrations")
}
