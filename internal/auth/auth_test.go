package auth

import (
	"testing"
	"time"
)

func TestJWTRoundTrip(t *testing.T) {
	j := &JWTIssuer{Secret: []byte("test-secret-1234567890"), TTL: time.Hour, Issuer: "test"}
	tok, err := j.Issue("user-1", "alice@example.com", []Role{RoleEngineer})
	if err != nil {
		t.Fatal(err)
	}
	p, err := j.Verify(tok)
	if err != nil {
		t.Fatal(err)
	}
	if p.UserID != "user-1" || p.Email != "alice@example.com" {
		t.Fatalf("bad principal: %+v", p)
	}
	if !p.HasRole(RoleEngineer) {
		t.Fatal("missing engineer role")
	}
	if p.HasRole(RoleAdmin) {
		t.Fatal("should not have admin role")
	}
}

func TestAPIKeyHashAndVerify(t *testing.T) {
	display, prefix, hash, err := GenerateAPIKey("ci")
	if err != nil {
		t.Fatal(err)
	}
	if prefix == "" || hash == "" || display == "" {
		t.Fatal("empty fields")
	}
	gotPrefix, ok := VerifyAPIKey(display, hash)
	if !ok {
		t.Fatal("verify failed")
	}
	if gotPrefix != prefix {
		t.Fatalf("prefix mismatch: %s != %s", gotPrefix, prefix)
	}
	// Tampering should fail
	bad := display[:len(display)-1] + "X"
	if _, ok := VerifyAPIKey(bad, hash); ok {
		t.Fatal("tampered key verified")
	}
}
