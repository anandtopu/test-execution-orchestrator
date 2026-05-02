// Package db opens the Postgres pool and ClickHouse connection used across services.
package db

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
	chdriver "github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/jackc/pgx/v5/pgxpool"
)

// OpenPostgres returns a configured pgx pool.
func OpenPostgres(ctx context.Context, dsn string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse pg dsn: %w", err)
	}
	cfg.MaxConns = 25
	cfg.MinConns = 2
	cfg.MaxConnLifetime = time.Hour
	cfg.MaxConnIdleTime = 30 * time.Minute
	cfg.ConnConfig.RuntimeParams["application_name"] = "teo"
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, err
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("pg ping: %w", err)
	}
	return pool, nil
}

// OpenClickHouse returns a *sql.DB pointing at ClickHouse. Used by the
// migration runner and any path that wants the database/sql idiom.
// Performance-sensitive write paths should use OpenClickHouseConn instead —
// the native driver.Conn supports column-major batched inserts via
// PrepareBatch, which is materially cheaper than the database/sql wrapper
// for high-volume writes (like the OTLP span ingest).
func OpenClickHouse(ctx context.Context, dsn string) (*sql.DB, error) {
	conn, err := sql.Open("clickhouse", dsn)
	if err != nil {
		return nil, err
	}
	conn.SetMaxOpenConns(20)
	conn.SetMaxIdleConns(5)
	conn.SetConnMaxLifetime(time.Hour)
	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := conn.PingContext(pingCtx); err != nil {
		conn.Close()
		return nil, fmt.Errorf("ch ping: %w", err)
	}
	return conn, nil
}

// OpenClickHouseConn returns a native clickhouse-go/v2 driver.Conn. Use this
// for the OTLP write path — `conn.PrepareBatch(ctx, sql)` returns a
// column-major Batch the caller appends rows to and Sends in one network
// round-trip. The TEO result-pipeline takes this path; everything else uses
// OpenClickHouse and the database/sql idiom.
func OpenClickHouseConn(ctx context.Context, dsn string) (chdriver.Conn, error) {
	opts, err := parseClickHouseDSN(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse ch dsn: %w", err)
	}
	conn, err := clickhouse.Open(opts)
	if err != nil {
		return nil, err
	}
	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := conn.Ping(pingCtx); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("ch native ping: %w", err)
	}
	return conn, nil
}

// parseClickHouseDSN turns "clickhouse://user:pass@host:port/db?param=val"
// into a *clickhouse.Options. The std-lib URL parser handles auth + host and
// the query string carries any driver-specific knobs.
func parseClickHouseDSN(dsn string) (*clickhouse.Options, error) {
	u, err := url.Parse(dsn)
	if err != nil {
		return nil, err
	}
	opts := &clickhouse.Options{
		Addr: []string{u.Host},
	}
	if u.User != nil {
		opts.Auth.Username = u.User.Username()
		if pw, ok := u.User.Password(); ok {
			opts.Auth.Password = pw
		}
	}
	if db := strings.TrimPrefix(u.Path, "/"); db != "" {
		opts.Auth.Database = db
	} else {
		opts.Auth.Database = "teo"
	}
	// Reasonable defaults; operators can tune via env later.
	opts.DialTimeout = 5 * time.Second
	opts.MaxOpenConns = 20
	opts.MaxIdleConns = 5
	opts.ConnMaxLifetime = time.Hour
	return opts, nil
}
