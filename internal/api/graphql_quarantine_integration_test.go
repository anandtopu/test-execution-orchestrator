//go:build integration

package api

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/teo-dev/teo/internal/audit"
	"github.com/teo-dev/teo/internal/testpg"
)

// TestQuarantineTransitionRoundTrip exercises the S-08-03 operator quarantine
// resolvers against a real Postgres: quarantine an active test, verify the
// teo.tests + flake_records bookkeeping and the audit row, then unquarantine
// the seeded quarantined test and verify the inverse, plus the error paths.
func TestQuarantineTransitionRoundTrip(t *testing.T) {
	pool, cleanup := testpg.Start(t)
	t.Cleanup(cleanup)
	ids := seed(t, pool)
	aud := &audit.Logger{Pool: pool}
	ctx := context.Background()

	// A fresh active test to quarantine (the seeded test is already quarantined).
	activeID := uuid.New().String()
	mustExec(t, pool, `
        INSERT INTO teo.tests (id, repo_id, fingerprint, path, name, runner, status)
        VALUES ($1, $2, 'fp-active', 'tests/test_a.py', 'test_a', 'pytest', 'active')
    `, activeID, ids.repoID)

	// --- quarantine with an explicit reason ---
	got, err := quarantineTest(ctx, pool, aud, activeID, "  flaky on CI  ")
	if err != nil {
		t.Fatalf("quarantineTest: %v", err)
	}
	if got["status"] != "quarantined" {
		t.Errorf("status = %v, want quarantined", got["status"])
	}
	if r, ok := got["quarantine_reason"].(*string); !ok || r == nil || *r != "flaky on CI" { // trimmed; nullable column → *string
		t.Errorf("reason = %v, want trimmed 'flaky on CI'", derefStr(got["quarantine_reason"]))
	}
	if got["quarantined_at"] == nil {
		t.Error("quarantined_at should be set on the returned row")
	}
	var dbStatus string
	if err := pool.QueryRow(ctx, `SELECT status FROM teo.tests WHERE id = $1`, activeID).Scan(&dbStatus); err != nil {
		t.Fatal(err)
	}
	if dbStatus != "quarantined" {
		t.Errorf("db status = %q, want quarantined", dbStatus)
	}
	var auditN int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM teo.audit_log WHERE action = 'test.quarantine' AND target_id = $1`, activeID).Scan(&auditN); err != nil {
		t.Fatal(err)
	}
	if auditN != 1 {
		t.Errorf("audit rows = %d, want 1", auditN)
	}

	// --- empty reason defaults ---
	active2 := uuid.New().String()
	mustExec(t, pool, `
        INSERT INTO teo.tests (id, repo_id, fingerprint, path, name, runner, status)
        VALUES ($1, $2, 'fp-active2', 'tests/test_b.py', 'test_b', 'pytest', 'active')
    `, active2, ids.repoID)
	got2, err := quarantineTest(ctx, pool, aud, active2, "   ")
	if err != nil {
		t.Fatal(err)
	}
	if r, ok := got2["quarantine_reason"].(*string); !ok || r == nil || *r != "manual: operator quarantine" {
		t.Errorf("default reason = %v", derefStr(got2["quarantine_reason"]))
	}

	// --- unquarantine the seeded (already quarantined) test ---
	un, err := unquarantineTest(ctx, pool, aud, ids.testID)
	if err != nil {
		t.Fatalf("unquarantineTest: %v", err)
	}
	if un["status"] != "active" {
		t.Errorf("status = %v, want active", un["status"])
	}
	if un["quarantined_at"] != nil {
		t.Error("quarantined_at should be cleared")
	}
	// flake_records: quarantined_at cleared, unquarantined_at set.
	var qAt, unAt *time.Time
	if err := pool.QueryRow(ctx,
		`SELECT quarantined_at, unquarantined_at FROM teo.flake_records WHERE test_id = $1`, ids.testID).Scan(&qAt, &unAt); err != nil {
		t.Fatal(err)
	}
	if qAt != nil {
		t.Error("flake_records.quarantined_at should be NULL after unquarantine")
	}
	if unAt == nil {
		t.Error("flake_records.unquarantined_at should be set after unquarantine")
	}

	// --- error paths ---
	if _, err := quarantineTest(ctx, pool, aud, uuid.New().String(), "x"); !errors.Is(err, errTestNotFound) {
		t.Errorf("missing test: got %v, want errTestNotFound", err)
	}
	delID := uuid.New().String()
	mustExec(t, pool, `
        INSERT INTO teo.tests (id, repo_id, fingerprint, path, name, runner, status)
        VALUES ($1, $2, 'fp-del', 'tests/test_c.py', 'test_c', 'pytest', 'deleted')
    `, delID, ids.repoID)
	if _, err := quarantineTest(ctx, pool, aud, delID, "x"); !errors.Is(err, errCannotQuarantineDeleted) {
		t.Errorf("deleted test: got %v, want errCannotQuarantineDeleted", err)
	}

	// --- idempotent unquarantine: activeID is still quarantined from above;
	// the first call clears it, the second is a no-op success returning active.
	if _, err := unquarantineTest(ctx, pool, aud, activeID); err != nil {
		t.Fatalf("first unquarantine: %v", err)
	}
	again, err := unquarantineTest(ctx, pool, aud, activeID)
	if err != nil {
		t.Errorf("second unquarantine should be idempotent no-op, got %v", err)
	} else if again["status"] != "active" {
		t.Errorf("idempotent unquarantine status = %v, want active", again["status"])
	}
}

// derefStr renders a *string map value (nullable column) for error messages,
// printing <nil> rather than a pointer address.
func derefStr(v any) string {
	if p, ok := v.(*string); ok && p != nil {
		return *p
	}
	return "<nil>"
}
