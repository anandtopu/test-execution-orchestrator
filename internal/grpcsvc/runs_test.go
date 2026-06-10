package grpcsvc

import (
	"errors"
	"fmt"
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/teo-dev/teo/internal/model"
	teov1 "github.com/teo-dev/teo/internal/proto/teov1"
	"github.com/teo-dev/teo/internal/runsvc"
)

func TestGrpcErr(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		err      error
		wantCode codes.Code
		wantMsg  string // exact message; "" means don't assert exact
	}{
		{"validation sentinel", runsvc.ErrValidation, codes.InvalidArgument, ""},
		{
			"wrapped ValidationError",
			&runsvc.ValidationError{Fields: []model.FieldError{{Field: "branch", Message: "required"}}},
			codes.InvalidArgument, "",
		},
		{"repo not found", fmt.Errorf("%w: org/x", runsvc.ErrRepoNotFound), codes.NotFound, ""},
		{"run not found", fmt.Errorf("%w: id", runsvc.ErrRunNotFound), codes.NotFound, ""},
		{"idempotency conflict", runsvc.ErrIdempotencyConflict, codes.AlreadyExists, ""},
		{
			"generic error is masked",
			fmt.Errorf("insert run: %w", errors.New("pq: connection refused")),
			codes.Internal, "internal error",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			st, ok := status.FromError(grpcErr(tc.err))
			require.True(t, ok)
			require.Equal(t, tc.wantCode, st.Code())
			if tc.wantMsg != "" {
				require.Equal(t, tc.wantMsg, st.Message())
			}
			if tc.wantCode == codes.Internal {
				// Internal errors must not leak the wrapped raw error string.
				require.NotContains(t, st.Message(), "connection refused")
				require.NotContains(t, st.Message(), "insert run")
			}
		})
	}
}

func TestRunToProto(t *testing.T) {
	t.Parallel()

	t.Run("nil input", func(t *testing.T) {
		t.Parallel()
		require.Nil(t, runToProto(nil))
	})

	t.Run("timestamps nil and set", func(t *testing.T) {
		t.Parallel()
		started := time.Date(2026, 6, 9, 1, 2, 3, 0, time.UTC)
		// FinishedAt nil, StartedAt set.
		out := runToProto(&model.Run{
			ID:        "r1",
			Status:    model.RunStatus("running"),
			StartedAt: &started,
		})
		require.Equal(t, "r1", out.GetId())
		require.Equal(t, "running", out.GetStatus())
		require.NotNil(t, out.GetStartedAt())
		require.Equal(t, started.Unix(), out.GetStartedAt().GetSeconds())
		require.Nil(t, out.GetFinishedAt())
	})

	t.Run("both timestamps set", func(t *testing.T) {
		t.Parallel()
		ts := time.Now().UTC()
		out := runToProto(&model.Run{StartedAt: &ts, FinishedAt: &ts})
		require.NotNil(t, out.GetStartedAt())
		require.NotNil(t, out.GetFinishedAt())
	})

	t.Run("int32 overflow saturates not wraps", func(t *testing.T) {
		t.Parallel()
		// > MaxInt32 ms must NOT wrap to a negative value on the wire.
		out := runToProto(&model.Run{TotalDurationMS: math.MaxInt32 + 1000})
		require.Equal(t, int32(math.MaxInt32), out.GetTotalDurationMs())
		require.Positive(t, out.GetTotalDurationMs())
	})
}

func TestClampInt32(t *testing.T) {
	t.Parallel()

	tests := []struct {
		in   int
		want int32
	}{
		{0, 0},
		{42, 42},
		{math.MaxInt32, math.MaxInt32},
		{math.MaxInt32 + 1, math.MaxInt32},
		{math.MinInt32, math.MinInt32},
		{math.MinInt32 - 1, math.MinInt32},
	}
	for _, tc := range tests {
		require.Equal(t, tc.want, clampInt32(tc.in), "in=%d", tc.in)
	}
}

func TestProtoToCreateReq(t *testing.T) {
	t.Parallel()

	t.Run("nil manifest and budget", func(t *testing.T) {
		t.Parallel()
		got := protoToCreateReq(&teov1.CreateRunRequest{
			RepoFullName:    "org/repo",
			CommitSha:       "abc123",
			Branch:          "main",
			TriggerActor:    "alice",
			TriggerPrNumber: 7,
			IdempotencyKey:  "key-1",
		})
		require.Equal(t, "org/repo", got.RepoFullName)
		require.Equal(t, "abc123", got.CommitSHA) // CommitSha -> CommitSHA
		require.Equal(t, "main", got.Branch)
		require.Equal(t, "alice", got.TriggerActor)
		require.Equal(t, 7, got.TriggerPRNumber) // int32 -> int
		require.Equal(t, "key-1", got.IdempotencyKey)
		require.Empty(t, got.Manifest.Runner)
		require.Empty(t, got.Manifest.Tests)
		require.Nil(t, got.Budget)
	})

	t.Run("populated manifest and budget", func(t *testing.T) {
		t.Parallel()
		got := protoToCreateReq(&teov1.CreateRunRequest{
			Manifest: &teov1.TestManifest{
				Runner: "pytest",
				Tests: []*teov1.TestEntry{
					{Path: "a.py", Name: "test_a", ParamsHash: "h1", Tags: []string{"slow"}},
				},
			},
			Budget: &teov1.RunBudget{MaxSeconds: 600, MaxWorkers: 4},
		})
		require.Equal(t, "pytest", got.Manifest.Runner)
		require.Len(t, got.Manifest.Tests, 1)
		require.Equal(t, "a.py", got.Manifest.Tests[0].Path)
		require.Equal(t, []string{"slow"}, got.Manifest.Tests[0].Tags)
		require.NotNil(t, got.Budget)
		require.Equal(t, 600, got.Budget.MaxSeconds)
		require.Equal(t, 4, got.Budget.MaxWorkers)
	})
}
