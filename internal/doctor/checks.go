package doctor

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nats-io/nats.go"

	"github.com/teo-dev/teo/internal/db"
	"github.com/teo-dev/teo/internal/migrate"
)

// PostgresCheck pings Postgres and reports the current schema migration version.
type PostgresCheck struct {
	DSN string
}

// Name implements Check.
func (PostgresCheck) Name() string { return "postgres" }

// Run implements Check.
func (c PostgresCheck) Run(ctx context.Context) Result {
	if c.DSN == "" {
		return Result{Name: c.Name(), Status: StatusSkipped, Message: "TEO_POSTGRES_DSN not set"}
	}
	pingCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	pool, err := db.OpenPostgres(pingCtx, c.DSN)
	if err != nil {
		return Result{Name: c.Name(), Status: StatusFail, Message: "open: " + err.Error()}
	}
	defer pool.Close()

	v, err := migrate.Status(migrate.Postgres, c.DSN)
	if err != nil {
		// Connected but migrations table missing (or unreadable). That's a warn:
		// the dependency is up, but the operator hasn't run migrations yet.
		return Result{
			Name:    c.Name(),
			Status:  StatusWarn,
			Message: "connected; migration status unavailable",
			Detail:  err.Error(),
		}
	}
	return Result{
		Name:    c.Name(),
		Status:  StatusOK,
		Message: fmt.Sprintf("up; schema version %d", v),
	}
}

// ClickHouseCheck pings ClickHouse and reports the current schema migration version.
type ClickHouseCheck struct {
	DSN string
}

// Name implements Check.
func (ClickHouseCheck) Name() string { return "clickhouse" }

// Run implements Check.
func (c ClickHouseCheck) Run(ctx context.Context) Result {
	if c.DSN == "" {
		return Result{Name: c.Name(), Status: StatusSkipped, Message: "TEO_CLICKHOUSE_DSN not set"}
	}
	pingCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	conn, err := db.OpenClickHouse(pingCtx, c.DSN)
	if err != nil {
		return Result{Name: c.Name(), Status: StatusFail, Message: "open: " + err.Error()}
	}
	defer func() { _ = conn.Close() }()

	v, err := migrate.Status(migrate.ClickHouse, c.DSN)
	if err != nil {
		return Result{
			Name:    c.Name(),
			Status:  StatusWarn,
			Message: "connected; migration status unavailable",
			Detail:  err.Error(),
		}
	}
	return Result{
		Name:    c.Name(),
		Status:  StatusOK,
		Message: fmt.Sprintf("up; schema version %d", v),
	}
}

// NATSCheck verifies the JetStream cluster is reachable.
type NATSCheck struct {
	URL string
}

// Name implements Check.
func (NATSCheck) Name() string { return "nats" }

// Run implements Check.
func (c NATSCheck) Run(_ context.Context) Result {
	if c.URL == "" {
		return Result{Name: c.Name(), Status: StatusSkipped, Message: "TEO_NATS_URL not set"}
	}
	nc, err := nats.Connect(c.URL,
		nats.Timeout(3*time.Second),
		nats.MaxReconnects(0),
	)
	if err != nil {
		return Result{Name: c.Name(), Status: StatusFail, Message: err.Error()}
	}
	defer nc.Close()
	if !nc.IsConnected() {
		return Result{Name: c.Name(), Status: StatusFail, Message: "connect did not establish"}
	}
	rtt, _ := nc.RTT()
	return Result{
		Name:    c.Name(),
		Status:  StatusOK,
		Message: fmt.Sprintf("up; rtt %s", rtt.Round(time.Microsecond)),
	}
}

// HTTPCheck issues a GET against an HTTP endpoint and considers any 2xx OK.
// Used to probe `<service>/healthz` URLs from the doctor output.
type HTTPCheck struct {
	N      string
	URL    string
	Client *http.Client
}

// Name implements Check.
func (c HTTPCheck) Name() string {
	if c.N != "" {
		return c.N
	}
	return "http"
}

// Run implements Check.
func (c HTTPCheck) Run(ctx context.Context) Result {
	if c.URL == "" {
		return Result{Name: c.Name(), Status: StatusSkipped, Message: "URL not set"}
	}
	client := c.Client
	if client == nil {
		client = &http.Client{Timeout: 3 * time.Second}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.URL, nil)
	if err != nil {
		return Result{Name: c.Name(), Status: StatusFail, Message: err.Error()}
	}
	resp, err := client.Do(req)
	if err != nil {
		return Result{Name: c.Name(), Status: StatusFail, Message: err.Error()}
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return Result{
			Name:    c.Name(),
			Status:  StatusOK,
			Message: fmt.Sprintf("HTTP %d", resp.StatusCode),
		}
	}
	return Result{
		Name:    c.Name(),
		Status:  StatusFail,
		Message: fmt.Sprintf("HTTP %d", resp.StatusCode),
	}
}

// PoolCheck verifies an existing Postgres pool (used when doctor is wired
// from inside a long-running service that already has a pool open). Kept
// here so the in-process diagnose endpoint can reuse the same Check shape.
type PoolCheck struct {
	N    string
	Pool *pgxpool.Pool
}

// Name implements Check.
func (c PoolCheck) Name() string {
	if c.N != "" {
		return c.N
	}
	return "postgres-pool"
}

// Run implements Check.
func (c PoolCheck) Run(ctx context.Context) Result {
	if c.Pool == nil {
		return Result{Name: c.Name(), Status: StatusSkipped, Message: "pool nil"}
	}
	pingCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if err := c.Pool.Ping(pingCtx); err != nil {
		return Result{Name: c.Name(), Status: StatusFail, Message: err.Error()}
	}
	stats := c.Pool.Stat()
	return Result{
		Name:    c.Name(),
		Status:  StatusOK,
		Message: fmt.Sprintf("up; %d/%d conns", stats.AcquiredConns(), stats.MaxConns()),
	}
}

// SQLPingCheck is for any sql.DB pre-opened by the caller. Kept so the
// existing migrate-status integration can reuse the test conn without
// dialing again.
type SQLPingCheck struct {
	N  string
	DB *sql.DB
}

// Name implements Check.
func (c SQLPingCheck) Name() string {
	if c.N != "" {
		return c.N
	}
	return "sql"
}

// Run implements Check.
func (c SQLPingCheck) Run(ctx context.Context) Result {
	if c.DB == nil {
		return Result{Name: c.Name(), Status: StatusSkipped, Message: "DB nil"}
	}
	pingCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if err := c.DB.PingContext(pingCtx); err != nil {
		return Result{Name: c.Name(), Status: StatusFail, Message: err.Error()}
	}
	return Result{Name: c.Name(), Status: StatusOK, Message: "up"}
}
