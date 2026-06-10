//go:build integration

package grpcsvc

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	"github.com/teo-dev/teo/internal/audit"
	"github.com/teo-dev/teo/internal/auth"
	teov1 "github.com/teo-dev/teo/internal/proto/teov1"
	"github.com/teo-dev/teo/internal/runsvc"
	"github.com/teo-dev/teo/internal/testpg"
)

const itJWTSecret = "test-secret-must-be-at-least-32-bytes-long-okay"

// bufHarness wires a *grpc.Server (with the production AuthUnaryInterceptor +
// RunsService over the shared runsvc.Service) onto an in-memory bufconn.Listener,
// then dials it with a real *grpc.ClientConn. This exercises the same code path
// cmd/api registers — including auth — without binding a TCP port.
type bufHarness struct {
	client teov1.RunsClient
	conn   *grpc.ClientConn
	srv    *grpc.Server
	pool   *pgxpool.Pool
	token  string // a valid bearer JWT for the engineer principal
}

func newBufHarness(t *testing.T) *bufHarness {
	t.Helper()
	pool, cleanup := testpg.Start(t)
	t.Cleanup(cleanup)

	jwtIssuer := &auth.JWTIssuer{Secret: []byte(itJWTSecret), TTL: time.Hour, Issuer: "teo"}
	token, err := jwtIssuer.Issue(uuid.New().String(), "tester@example.com", []auth.Role{auth.RoleEngineer})
	require.NoError(t, err)

	lis := bufconn.Listen(1 << 20)
	srv := grpc.NewServer(
		grpc.UnaryInterceptor(AuthUnaryInterceptor(jwtIssuer, nil)),
	)
	RegisterRuns(srv, &RunsService{
		Svc: &runsvc.Service{Pool: pool, Audit: &audit.Logger{Pool: pool}},
	})
	go func() { _ = srv.Serve(lis) }()

	conn, err := grpc.NewClient(
		"passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return lis.DialContext(ctx)
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	require.NoError(t, err)

	h := &bufHarness{client: teov1.NewRunsClient(conn), conn: conn, srv: srv, pool: pool, token: token}
	t.Cleanup(func() {
		_ = conn.Close()
		srv.Stop()
	})
	return h
}

// authCtx returns a context carrying the harness's valid bearer JWT in the
// authorization metadata header, so the AuthUnaryInterceptor admits the call.
func (h *bufHarness) authCtx(t *testing.T) (context.Context, context.CancelFunc) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	return metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+h.token), cancel
}

// seedRepo registers an enabled GitHub repo so CreateRun can resolve it.
func seedRepo(t *testing.T, pool *pgxpool.Pool) (string, string) {
	t.Helper()
	id := uuid.New().String()
	full := "owner/grpc-runs-test"
	_, err := pool.Exec(context.Background(),
		`INSERT INTO teo.repos (id, vcs, full_name) VALUES ($1, 'github', $2)`, id, full)
	require.NoError(t, err)
	return id, full
}

func validManifest() *teov1.TestManifest {
	return &teov1.TestManifest{
		Runner: "pytest",
		Tests: []*teov1.TestEntry{
			{Path: "tests/test_a.py", Name: "test_one"},
			{Path: "tests/test_a.py", Name: "test_two"},
		},
	}
}

func validCreateReq(repoFull, commit string) *teov1.CreateRunRequest {
	return &teov1.CreateRunRequest{
		RepoFullName: repoFull,
		CommitSha:    commit,
		Branch:       "main",
		Manifest:     validManifest(),
	}
}

// TestGRPCCreateRunCreatesRow mirrors api.TestPOSTRunsCreatesRow: a successful
// CreateRun returns a pending run with a non-empty id and the right repo, and
// leaves exactly one teo.runs row + one teo.run_plans row.
func TestGRPCCreateRunCreatesRow(t *testing.T) {
	h := newBufHarness(t)
	_, full := seedRepo(t, h.pool)
	ctx, cancel := h.authCtx(t)
	defer cancel()

	run, err := h.client.CreateRun(ctx, validCreateReq(full, "abc123def"))
	require.NoError(t, err)
	require.Equal(t, "pending", run.GetStatus())
	require.NotEmpty(t, run.GetId())
	require.Equal(t, full, run.GetRepoFullName())

	var runCount, planCount int
	require.NoError(t, h.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM teo.runs WHERE id = $1`, run.GetId()).Scan(&runCount))
	require.Equal(t, 1, runCount)
	require.NoError(t, h.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM teo.run_plans WHERE run_id = $1`, run.GetId()).Scan(&planCount))
	require.Equal(t, 1, planCount)
}

// TestGRPCCreateRunUnauthenticated proves the auth interceptor gates the RPC:
// no authorization metadata → codes.Unauthenticated, no DB row written.
func TestGRPCCreateRunUnauthenticated(t *testing.T) {
	h := newBufHarness(t)
	_, full := seedRepo(t, h.pool)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	_, err := h.client.CreateRun(ctx, validCreateReq(full, "abc123def"))
	require.Equal(t, codes.Unauthenticated, status.Code(err))
}

// TestGRPCCreateRunUnknownRepo: an unregistered repo → codes.NotFound.
func TestGRPCCreateRunUnknownRepo(t *testing.T) {
	h := newBufHarness(t)
	// No repo seeded.
	ctx, cancel := h.authCtx(t)
	defer cancel()

	_, err := h.client.CreateRun(ctx, validCreateReq("ghost/not-real", "abc123def"))
	require.Equal(t, codes.NotFound, status.Code(err))
}

// TestGRPCCreateRunValidationError: empty manifest + missing repo_full_name →
// codes.InvalidArgument.
func TestGRPCCreateRunValidationError(t *testing.T) {
	h := newBufHarness(t)
	seedRepo(t, h.pool)
	ctx, cancel := h.authCtx(t)
	defer cancel()

	_, err := h.client.CreateRun(ctx, &teov1.CreateRunRequest{
		// RepoFullName omitted, no manifest.
		CommitSha: "abc",
		Branch:    "main",
	})
	require.Equal(t, codes.InvalidArgument, status.Code(err))
}

// TestGRPCCreateRunIdempotentSameCommit: same IdempotencyKey + same commit
// returns the SAME id and leaves exactly one teo.runs row for that key.
func TestGRPCCreateRunIdempotentSameCommit(t *testing.T) {
	h := newBufHarness(t)
	_, full := seedRepo(t, h.pool)
	ctx, cancel := h.authCtx(t)
	defer cancel()

	req := validCreateReq(full, "abc123def")
	req.IdempotencyKey = "grpc-key-001"

	first, err := h.client.CreateRun(ctx, req)
	require.NoError(t, err)
	second, err := h.client.CreateRun(ctx, req)
	require.NoError(t, err)
	require.Equal(t, first.GetId(), second.GetId())

	var n int
	require.NoError(t, h.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM teo.runs WHERE meta->>'idempotency_key' = $1`, "grpc-key-001").Scan(&n))
	require.Equal(t, 1, n)
}

// TestGRPCCreateRunIdempotentDifferentCommit: reusing an IdempotencyKey with a
// DIFFERENT commit_sha → codes.AlreadyExists (grpcErr maps the runsvc conflict
// sentinel there), and still only one row exists.
func TestGRPCCreateRunIdempotentDifferentCommit(t *testing.T) {
	h := newBufHarness(t)
	_, full := seedRepo(t, h.pool)
	ctx, cancel := h.authCtx(t)
	defer cancel()

	req1 := validCreateReq(full, "commit-one")
	req1.IdempotencyKey = "grpc-key-conflict"
	_, err := h.client.CreateRun(ctx, req1)
	require.NoError(t, err)

	req2 := validCreateReq(full, "commit-two")
	req2.IdempotencyKey = "grpc-key-conflict"
	_, err = h.client.CreateRun(ctx, req2)
	require.Equal(t, codes.AlreadyExists, status.Code(err))

	var n int
	require.NoError(t, h.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM teo.runs WHERE meta->>'idempotency_key' = $1`, "grpc-key-conflict").Scan(&n))
	require.Equal(t, 1, n)
}

// TestGRPCGetRunRoundTrip: GetRun on a created id round-trips id + repo_full_name;
// GetRun on a random uuid → codes.NotFound.
func TestGRPCGetRunRoundTrip(t *testing.T) {
	h := newBufHarness(t)
	_, full := seedRepo(t, h.pool)
	ctx, cancel := h.authCtx(t)
	defer cancel()

	created, err := h.client.CreateRun(ctx, validCreateReq(full, "abc123def"))
	require.NoError(t, err)

	got, err := h.client.GetRun(ctx, &teov1.GetRunRequest{Id: created.GetId()})
	require.NoError(t, err)
	require.Equal(t, created.GetId(), got.GetId())
	require.Equal(t, full, got.GetRepoFullName())

	_, err = h.client.GetRun(ctx, &teov1.GetRunRequest{Id: uuid.New().String()})
	require.Equal(t, codes.NotFound, status.Code(err))
}

// TestGRPCCancelRunTransitions: CancelRun on a 'running' run returns a run and
// the DB status becomes 'cancelled' (two l's); CancelRun on a 'succeeded' run is
// idempotent (status stays 'succeeded', no error); CancelRun on a missing uuid →
// codes.NotFound.
func TestGRPCCancelRunTransitions(t *testing.T) {
	h := newBufHarness(t)
	repoID, _ := seedRepo(t, h.pool)
	ctx, cancel := h.authCtx(t)
	defer cancel()

	// running → cancelled
	runningID := uuid.New().String()
	_, err := h.pool.Exec(context.Background(), `
        INSERT INTO teo.runs (id, repo_id, commit_sha, branch, triggered_by, status, started_at)
        VALUES ($1, $2, 'cafe', 'main', 'api', 'running', now())
    `, runningID, repoID)
	require.NoError(t, err)

	run, err := h.client.CancelRun(ctx, &teov1.CancelRunRequest{Id: runningID})
	require.NoError(t, err)
	require.NotNil(t, run)
	var dbStatus string
	require.NoError(t, h.pool.QueryRow(context.Background(),
		`SELECT status FROM teo.runs WHERE id = $1`, runningID).Scan(&dbStatus))
	require.Equal(t, "cancelled", dbStatus)

	// succeeded → idempotent no-op
	succeededID := uuid.New().String()
	_, err = h.pool.Exec(context.Background(), `
        INSERT INTO teo.runs (id, repo_id, commit_sha, branch, triggered_by, status,
                              started_at, finished_at)
        VALUES ($1, $2, 'cafe', 'main', 'api', 'succeeded',
                now() - interval '5 minutes', now() - interval '4 minutes')
    `, succeededID, repoID)
	require.NoError(t, err)

	_, err = h.client.CancelRun(ctx, &teov1.CancelRunRequest{Id: succeededID})
	require.NoError(t, err)
	require.NoError(t, h.pool.QueryRow(context.Background(),
		`SELECT status FROM teo.runs WHERE id = $1`, succeededID).Scan(&dbStatus))
	require.Equal(t, "succeeded", dbStatus)

	// missing uuid → NotFound
	_, err = h.client.CancelRun(ctx, &teov1.CancelRunRequest{Id: uuid.New().String()})
	require.Equal(t, codes.NotFound, status.Code(err))
}
