package grpcsvc

import (
	"context"
	"errors"
	"log/slog"
	"math"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/teo-dev/teo/internal/model"
	teov1 "github.com/teo-dev/teo/internal/proto/teov1"
	"github.com/teo-dev/teo/internal/runsvc"
)

// RunsService implements the teov1.RunsServer surface on top of the shared
// run-intake logic in internal/runsvc, so the gRPC CreateRun/GetRun/CancelRun
// RPCs share exactly one code path with the HTTP/GraphQL gateway.
//
// Embedding teov1.UnimplementedRunsServer by value is mandatory under
// require_unimplemented_servers=true: it forwards-compats this service when new
// RPCs are added to the .proto and satisfies the testEmbeddedByValue /
// mustEmbedUnimplementedRunsServer assertions emitted by the generated
// registrar.
//
// Unlike WorkersService (internal worker dispatch traffic), the Runs RPCs are
// state-mutating run-create/cancel operations and MUST be authenticated. The
// gRPC server they are registered on installs AuthUnaryInterceptor, which
// validates a JWT/API-key from request metadata and rejects unauthenticated
// callers with codes.Unauthenticated before any handler runs — mirroring the
// auth.PrincipalFrom gate the HTTP RunsHandler enforces for the identical
// operations. See cmd/api/main.go and AuthUnaryInterceptor below.
type RunsService struct {
	teov1.UnimplementedRunsServer

	Svc *runsvc.Service
}

// RegisterRuns hooks the Runs service into a *grpc.Server using the generated
// registrar. Kept separate from Register(srv, *WorkersService) so the existing
// workers registration call site is untouched.
func RegisterRuns(srv *grpc.Server, rs *RunsService) {
	if rs == nil || rs.Svc == nil {
		// Fail loudly at wiring time rather than nil-derefing inside the first
		// RPC handler (which would crash the goroutine / surface as Internal).
		panic("grpcsvc: RunsService.Svc is required")
	}
	teov1.RegisterRunsServer(srv, rs)
}

// CreateRun handles the CreateRun RPC.
func (s *RunsService) CreateRun(ctx context.Context, req *teov1.CreateRunRequest) (*teov1.Run, error) {
	run, _, err := s.Svc.Create(ctx, protoToCreateReq(req))
	if err != nil {
		return nil, grpcErr(err)
	}
	return runToProto(run), nil
}

// GetRun handles the GetRun RPC.
func (s *RunsService) GetRun(ctx context.Context, req *teov1.GetRunRequest) (*teov1.Run, error) {
	run, err := s.Svc.Get(ctx, req.GetId())
	if err != nil {
		return nil, grpcErr(err)
	}
	return runToProto(run), nil
}

// CancelRun handles the CancelRun RPC.
func (s *RunsService) CancelRun(ctx context.Context, req *teov1.CancelRunRequest) (*teov1.Run, error) {
	run, err := s.Svc.Cancel(ctx, req.GetId())
	if err != nil {
		return nil, grpcErr(err)
	}
	return runToProto(run), nil
}

// grpcErr maps a runsvc sentinel error to a gRPC status. Validation →
// InvalidArgument; missing repo/run → NotFound; idempotency conflict →
// AlreadyExists (the closest analog to the HTTP 409); anything else → Internal.
func grpcErr(err error) error {
	switch {
	case errors.Is(err, runsvc.ErrValidation):
		return status.Error(codes.InvalidArgument, err.Error())
	case errors.Is(err, runsvc.ErrRepoNotFound):
		return status.Error(codes.NotFound, err.Error())
	case errors.Is(err, runsvc.ErrRunNotFound):
		return status.Error(codes.NotFound, err.Error())
	case errors.Is(err, runsvc.ErrIdempotencyConflict):
		return status.Error(codes.AlreadyExists, err.Error())
	default:
		// Don't leak wrapped DB/SQL error strings ("insert run: ...",
		// "repo lookup: ...") to the wire. Log the detail server-side and
		// return a generic Internal status to the caller.
		slog.Error("runs grpc: internal error", "err", err)
		return status.Error(codes.Internal, "internal error")
	}
}

// protoToCreateReq converts a wire CreateRunRequest to the domain request.
func protoToCreateReq(req *teov1.CreateRunRequest) model.CreateRunRequest {
	out := model.CreateRunRequest{
		RepoFullName:    req.GetRepoFullName(),
		CommitSHA:       req.GetCommitSha(),
		Branch:          req.GetBranch(),
		TriggerActor:    req.GetTriggerActor(),
		TriggerPRNumber: int(req.GetTriggerPrNumber()),
		IdempotencyKey:  req.GetIdempotencyKey(),
	}
	if m := req.GetManifest(); m != nil {
		out.Manifest.Runner = m.GetRunner()
		for _, t := range m.GetTests() {
			out.Manifest.Tests = append(out.Manifest.Tests, model.TestEntry{
				Path:       t.GetPath(),
				Name:       t.GetName(),
				ParamsHash: t.GetParamsHash(),
				Tags:       t.GetTags(),
			})
		}
	}
	if b := req.GetBudget(); b != nil {
		out.Budget = &model.RunBudget{
			MaxSeconds: int(b.GetMaxSeconds()),
			MaxWorkers: int(b.GetMaxWorkers()),
		}
	}
	return out
}

// runToProto converts a domain Run to the wire Run. The proto Run message has
// no repo_id/created_at/updated_at fields, so those are dropped (gRPC clients
// get less than the REST JSON; additive-safe).
func runToProto(r *model.Run) *teov1.Run {
	if r == nil {
		return nil
	}
	// The proto Run fields are int32 (additive-only contract; widening to int64
	// would renumber). The model values are Go int, so saturate rather than let
	// a large millisecond aggregate wrap to a negative/garbage value on the wire.
	// TotalDurationMs is the realistic overflow (int32 caps at ~2.1B ms ≈ 24.8d
	// of cumulative test time); budget/preemption are practically bounded.
	out := &teov1.Run{
		Id:              r.ID,
		RepoFullName:    r.RepoFullName,
		CommitSha:       r.CommitSHA,
		Branch:          r.Branch,
		Status:          string(r.Status),
		TotalDurationMs: clampInt32(r.TotalDurationMS),
		BudgetSeconds:   clampInt32(r.BudgetSeconds),
		PreemptionCount: clampInt32(r.PreemptionCount),
	}
	if r.StartedAt != nil {
		out.StartedAt = timestamppb.New(*r.StartedAt)
	}
	if r.FinishedAt != nil {
		out.FinishedAt = timestamppb.New(*r.FinishedAt)
	}
	return out
}

// clampInt32 saturates a Go int into the int32 range so a value that exceeds
// the proto field width is pinned to MaxInt32/MinInt32 instead of wrapping
// around to a garbage value on the wire. //nolint:gosec is unnecessary — the
// bounds check makes the conversion provably in-range.
func clampInt32(v int) int32 {
	switch {
	case v > math.MaxInt32:
		return math.MaxInt32
	case v < math.MinInt32:
		return math.MinInt32
	default:
		return int32(v)
	}
}
