package db

import (
	"testing"
	"time"
)

func TestParseClickHouseDSNFullURL(t *testing.T) {
	opts, err := parseClickHouseDSN("clickhouse://alice:s3cret@ch.example.com:9000/teo_prod")
	if err != nil {
		t.Fatal(err)
	}
	if len(opts.Addr) != 1 || opts.Addr[0] != "ch.example.com:9000" {
		t.Errorf("Addr = %v, want [ch.example.com:9000]", opts.Addr)
	}
	if opts.Auth.Username != "alice" {
		t.Errorf("Username = %q, want alice", opts.Auth.Username)
	}
	if opts.Auth.Password != "s3cret" {
		t.Errorf("Password = %q, want s3cret", opts.Auth.Password)
	}
	if opts.Auth.Database != "teo_prod" {
		t.Errorf("Database = %q, want teo_prod", opts.Auth.Database)
	}
}

func TestParseClickHouseDSNDefaultsDatabaseToTeo(t *testing.T) {
	opts, err := parseClickHouseDSN("clickhouse://ch:9000/")
	if err != nil {
		t.Fatal(err)
	}
	if opts.Auth.Database != "teo" {
		t.Errorf("Database = %q, want teo (default)", opts.Auth.Database)
	}
}

func TestParseClickHouseDSNNoUserNoPassword(t *testing.T) {
	opts, err := parseClickHouseDSN("clickhouse://ch:9000/mydb")
	if err != nil {
		t.Fatal(err)
	}
	if opts.Auth.Username != "" || opts.Auth.Password != "" {
		t.Errorf("expected empty creds, got user=%q pw=%q", opts.Auth.Username, opts.Auth.Password)
	}
	if opts.Auth.Database != "mydb" {
		t.Errorf("Database = %q, want mydb", opts.Auth.Database)
	}
}

func TestParseClickHouseDSNUserOnly(t *testing.T) {
	// Some operators set just a username (no password) for trusted networks.
	opts, err := parseClickHouseDSN("clickhouse://reader@ch:9000/teo")
	if err != nil {
		t.Fatal(err)
	}
	if opts.Auth.Username != "reader" {
		t.Errorf("Username = %q, want reader", opts.Auth.Username)
	}
	if opts.Auth.Password != "" {
		t.Errorf("Password = %q, want empty", opts.Auth.Password)
	}
}

func TestParseClickHouseDSNAppliesDefaults(t *testing.T) {
	opts, err := parseClickHouseDSN("clickhouse://ch:9000/teo")
	if err != nil {
		t.Fatal(err)
	}
	if opts.DialTimeout != 5*time.Second {
		t.Errorf("DialTimeout = %v, want 5s", opts.DialTimeout)
	}
	if opts.MaxOpenConns != 20 {
		t.Errorf("MaxOpenConns = %d, want 20", opts.MaxOpenConns)
	}
	if opts.MaxIdleConns != 5 {
		t.Errorf("MaxIdleConns = %d, want 5", opts.MaxIdleConns)
	}
	if opts.ConnMaxLifetime != time.Hour {
		t.Errorf("ConnMaxLifetime = %v, want 1h", opts.ConnMaxLifetime)
	}
}

func TestParseClickHouseDSNRejectsMalformed(t *testing.T) {
	// url.Parse is lenient; we lean on it returning an error for control bytes.
	if _, err := parseClickHouseDSN("clickhouse://ch:9000/teo\x7f"); err == nil {
		t.Fatal("expected error for control-byte URL")
	}
}
