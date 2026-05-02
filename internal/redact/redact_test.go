package redact

import (
	"strings"
	"testing"
)

func TestAWSAccessKeyRedacted(t *testing.T) {
	r := New()
	in := "key=AKIAIOSFODNN7EXAMPLE plus tail"
	got := r.String(in)
	if strings.Contains(got, "AKIAIOSFODNN7EXAMPLE") {
		t.Fatalf("AWS key not redacted: %s", got)
	}
	if !strings.Contains(got, "[REDACTED:aws_access_key]") {
		t.Fatalf("missing replacement marker: %s", got)
	}
}

func TestJWTRedacted(t *testing.T) {
	r := New()
	jwt := "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiJ4In0.signature_xyz_abc_123"
	got := r.String("token: " + jwt)
	if strings.Contains(got, jwt) {
		t.Fatalf("JWT not redacted: %s", got)
	}
}

func TestNoRedactionWhenClean(t *testing.T) {
	r := New()
	got := r.String("ordinary log line, nothing sensitive")
	if got != "ordinary log line, nothing sensitive" {
		t.Fatalf("clean text was modified: %s", got)
	}
}

func TestGitHubPATRedacted(t *testing.T) {
	r := New()
	pat := "ghp_aBcDeFgHiJkLmNoPqRsTuVwXyZ0123456789"
	got := r.String("auth: " + pat)
	if strings.Contains(got, pat) {
		t.Fatalf("PAT not redacted: %s", got)
	}
}
