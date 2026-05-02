//go:build integration

// Package testminio spins up an ephemeral MinIO via testcontainers for the
// logstore S3 round-trip tests. Build-tagged so unit-test runs don't require
// Docker.
package testminio

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

// DefaultRoot creds match the MinIO image defaults; tests can rely on them.
const (
	DefaultAccessKey = "minioadmin"
	DefaultSecretKey = "minioadmin"
	DefaultRegion    = "us-east-1"
)

// Start launches MinIO, returns the HTTP endpoint URL (e.g. "http://localhost:32768")
// plus a cleanup function. Callers do `t.Cleanup(cleanup)`.
//
// Wait strategy probes /minio/health/ready, which MinIO flips to 200 only once
// it can serve requests — avoids the racey "container started but not ready"
// gap that bites generic ForListeningPort waits.
func Start(t *testing.T) (endpoint, accessKey, secretKey, region string, cleanup func()) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	container, err := testcontainers.Run(ctx,
		"minio/minio:RELEASE.2025-01-20T14-49-07Z",
		testcontainers.WithEnv(map[string]string{
			"MINIO_ROOT_USER":     DefaultAccessKey,
			"MINIO_ROOT_PASSWORD": DefaultSecretKey,
		}),
		testcontainers.WithCmd("server", "/data"),
		testcontainers.WithExposedPorts("9000/tcp"),
		testcontainers.WithWaitStrategyAndDeadline(60*time.Second,
			wait.ForHTTP("/minio/health/ready").
				WithPort("9000/tcp").
				WithStatusCodeMatcher(func(status int) bool { return status == http.StatusOK }),
		),
	)
	if err != nil {
		t.Fatalf("start minio container: %v", err)
	}

	host, err := container.Host(ctx)
	if err != nil {
		_ = testcontainers.TerminateContainer(container)
		t.Fatalf("minio host: %v", err)
	}
	port, err := container.MappedPort(ctx, "9000/tcp")
	if err != nil {
		_ = testcontainers.TerminateContainer(container)
		t.Fatalf("minio mapped port: %v", err)
	}
	endpoint = fmt.Sprintf("http://%s:%s", host, port.Port())
	cleanup = func() { _ = testcontainers.TerminateContainer(container) }
	return endpoint, DefaultAccessKey, DefaultSecretKey, DefaultRegion, cleanup
}
