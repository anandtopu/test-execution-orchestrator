// Command teo is the operator and developer CLI for the Test Execution Orchestrator.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/teo-dev/teo/internal/db"
	"github.com/teo-dev/teo/internal/digest"
	"github.com/teo-dev/teo/internal/migrate"
	"github.com/teo-dev/teo/internal/version"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "version", "--version", "-v":
		fmt.Println(version.Get("teo"))
	case "migrate":
		runMigrate(os.Args[2:])
	case "digest":
		runDigest(os.Args[2:])
	case "doctor":
		runDoctor(os.Args[2:])
	case "help", "--help", "-h":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "unknown subcommand %q\n\n", os.Args[1])
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintf(os.Stderr, `teo %s

Usage:
  teo <command> [flags]

Commands:
  migrate     Apply database migrations
  digest      Render or send the owner digest (subcommands: dry-run)
  doctor      Check connectivity to every TEO dependency
  version     Print build identity
  help        Print this help

Run 'teo <command> --help' for command-specific help.
`, version.Get("teo"))
}

func runMigrate(args []string) {
	fs := flag.NewFlagSet("migrate", flag.ExitOnError)
	cmd := "up"
	if len(args) > 0 && (args[0] == "up" || args[0] == "status") {
		cmd = args[0]
		args = args[1:]
	}
	pgDSN := fs.String("postgres-dsn", os.Getenv("TEO_POSTGRES_DSN"), "Postgres DSN (env: TEO_POSTGRES_DSN)")
	chDSN := fs.String("clickhouse-dsn", os.Getenv("TEO_CLICKHOUSE_DSN"), "ClickHouse DSN (env: TEO_CLICKHOUSE_DSN)")
	dir := fs.String("dir", envDefault("TEO_MIGRATIONS_DIR", "migrations"), "Migrations directory")
	skipPostgres := fs.Bool("skip-postgres", false, "Skip Postgres migrations")
	skipClickHouse := fs.Bool("skip-clickhouse", false, "Skip ClickHouse migrations")
	if err := fs.Parse(args); err != nil {
		os.Exit(2)
	}

	switch cmd {
	case "up":
		if !*skipPostgres {
			if *pgDSN == "" {
				exit("--postgres-dsn or TEO_POSTGRES_DSN required")
			}
			if err := migrate.Up(migrate.Postgres, *pgDSN, *dir); err != nil {
				exit("postgres migration failed: %v", err)
			}
			fmt.Println("postgres: up to date")
		}
		if !*skipClickHouse {
			if *chDSN == "" {
				exit("--clickhouse-dsn or TEO_CLICKHOUSE_DSN required")
			}
			if err := migrate.Up(migrate.ClickHouse, *chDSN, *dir); err != nil {
				exit("clickhouse migration failed: %v", err)
			}
			fmt.Println("clickhouse: up to date")
		}
	case "status":
		if *pgDSN != "" {
			v, err := migrate.Status(migrate.Postgres, *pgDSN)
			if err != nil {
				exit("postgres status failed: %v", err)
			}
			fmt.Printf("postgres:   version %d\n", v)
		}
		if *chDSN != "" {
			v, err := migrate.Status(migrate.ClickHouse, *chDSN)
			if err != nil {
				exit("clickhouse status failed: %v", err)
			}
			fmt.Printf("clickhouse: version %d\n", v)
		}
	}
}

func exit(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}

func envDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func runDigest(args []string) {
	if len(args) == 0 {
		exit("usage: teo digest <subcommand>\n  dry-run --user=<email>     render but don't send")
	}
	switch args[0] {
	case "dry-run":
		runDigestDryRun(args[1:])
	default:
		exit("unknown digest subcommand %q", args[0])
	}
}

func runDigestDryRun(args []string) {
	fs := flag.NewFlagSet("digest dry-run", flag.ExitOnError)
	user := fs.String("user", "", "Recipient email (required)")
	pgDSN := fs.String("postgres-dsn", os.Getenv("TEO_POSTGRES_DSN"), "Postgres DSN")
	format := fs.String("format", "html", "Output format: html | text")
	if err := fs.Parse(args); err != nil {
		os.Exit(2)
	}
	if *user == "" {
		exit("--user is required")
	}
	if *pgDSN == "" {
		exit("--postgres-dsn or TEO_POSTGRES_DSN required")
	}
	if *format != "html" && *format != "text" {
		exit("--format must be html or text")
	}

	ctx := context.Background()
	pool, err := db.OpenPostgres(ctx, *pgDSN)
	if err != nil {
		exit("postgres open: %v", err)
	}
	defer pool.Close()

	r := &digest.Runner{Pool: pool}
	msgs, err := r.RunForUser(ctx, *user)
	if err != nil {
		exit("dry-run failed: %v", err)
	}
	if len(msgs) == 0 {
		fmt.Fprintf(os.Stderr, "no digests would be sent to %s (no owned tests in any repo)\n", *user)
		return
	}
	for i, m := range msgs {
		if i > 0 {
			fmt.Print("\n----- next message -----\n\n")
		}
		fmt.Printf("Subject: %s\nTo: %s\nOwner: %s\n\n", m.Subject, m.Email, m.Owner)
		if *format == "html" {
			fmt.Println(m.HTML)
		} else {
			fmt.Println(m.Text)
		}
	}
}
