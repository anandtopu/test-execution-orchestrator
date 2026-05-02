package version

import (
	"strings"
	"testing"
)

func TestGetIncludesService(t *testing.T) {
	got := Get("api")
	if got.Service != "api" {
		t.Fatalf("Service = %q, want %q", got.Service, "api")
	}
	if got.GoVersion == "" {
		t.Fatal("GoVersion should not be empty")
	}
	if got.Platform == "" {
		t.Fatal("Platform should not be empty")
	}
}

func TestStringContainsAllFields(t *testing.T) {
	info := Info{
		Service:   "api",
		Version:   "v1.2.3",
		Commit:    "abc123",
		Date:      "2026-04-30T00:00:00Z",
		GoVersion: "go1.23.0",
		Platform:  "linux/amd64",
	}
	s := info.String()
	for _, want := range []string{"api", "v1.2.3", "abc123", "2026-04-30T00:00:00Z", "go1.23.0", "linux/amd64"} {
		if !strings.Contains(s, want) {
			t.Errorf("String() = %q; missing %q", s, want)
		}
	}
}
