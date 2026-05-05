// Package grpcsvc implements TEO's gRPC services on top of the protoc-gen-go
// bindings in internal/proto/teov1. The generated types own the wire shape;
// this file is just the database-side resolver for each Workers RPC.
package grpcsvc

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc"

	"github.com/teo-dev/teo/internal/audit"
	teov1 "github.com/teo-dev/teo/internal/proto/teov1"
	"github.com/teo-dev/teo/internal/resultpipeline"
)

// WorkersService implements the teov1.WorkersServer surface.
//
// Embedding teov1.UnimplementedWorkersServer is mandatory under
// require_unimplemented_servers=true: it forwards-compats this service when
// new RPCs are added to the .proto, and satisfies the testEmbeddedByValue /
// mustEmbedUnimplementedWorkersServer assertions the generated registrar
// emits at compile time.
type WorkersService struct {
	teov1.UnimplementedWorkersServer

	Pool    *pgxpool.Pool
	Audit   *audit.Logger
	Cluster *resultpipeline.Cluster
}

// Register hooks the service into a *grpc.Server using the generated
// registrar. Wire format becomes binary protobuf — no codec override needed
// at the server site.
func Register(srv *grpc.Server, ws *WorkersService) {
	teov1.RegisterWorkersServer(srv, ws)
}

// PullAssignment claims a pending shard for the worker.
//
// Returns an empty Assignment (zero-valued ShardId) when no work is available
// — the worker treats that as "poll again later". An RPC error is reserved
// for actual failures (DB unreachable, etc.) so well-known idle states don't
// trip retries.
func (s *WorkersService) PullAssignment(ctx context.Context, req *teov1.PullAssignmentRequest) (*teov1.Assignment, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	var shardID, runID, repoFull string
	var predictedMS int32
	err = tx.QueryRow(ctx, `
        UPDATE teo.shards
        SET status = 'running', worker_id = $1, started_at = COALESCE(started_at, now())
        WHERE id = (
            SELECT s.id FROM teo.shards s
            JOIN teo.runs r ON r.id = s.run_id
            WHERE s.status = 'pending'
              AND r.status IN ('dispatching','running')
            ORDER BY s.created_at ASC
            LIMIT 1 FOR UPDATE SKIP LOCKED
        )
        RETURNING id, run_id, predicted_duration_ms
    `, req.GetWorkerId()).Scan(&shardID, &runID, &predictedMS)
	if errors.Is(err, pgx.ErrNoRows) {
		return &teov1.Assignment{}, nil
	}
	if err != nil {
		return nil, err
	}
	if err := tx.QueryRow(ctx, `
        SELECT repos.full_name FROM teo.runs r JOIN teo.repos ON repos.id = r.repo_id WHERE r.id = $1
    `, runID).Scan(&repoFull); err != nil {
		return nil, err
	}

	tests, err := s.loadShardTests(ctx, tx, shardID)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &teov1.Assignment{
		ShardId:             shardID,
		RunId:               runID,
		RepoFullName:        repoFull,
		Tests:               tests,
		PredictedDurationMs: predictedMS,
	}, nil
}

func (s *WorkersService) loadShardTests(ctx context.Context, tx pgx.Tx, shardID string) ([]*teov1.TestEntryRef, error) {
	var planRaw []byte
	var index, total int
	err := tx.QueryRow(ctx, `
        SELECT rp.plan, sh.index,
               (SELECT count(*) FROM teo.shards WHERE run_id = sh.run_id)
        FROM teo.shards sh
        JOIN teo.run_plans rp ON rp.run_id = sh.run_id
        WHERE sh.id = $1
    `, shardID).Scan(&planRaw, &index, &total)
	if err != nil {
		return nil, err
	}
	// teo.run_plans always holds the manifest-v1 shape; the scheduler.Plan
	// lives in runs.meta.computed_plan. The structured-plan branch below is
	// kept as a forward-compatibility tolerance (an older row written by the
	// pre-fix manager will still resolve correctly through this path).
	var plan struct {
		Assignments []struct {
			ShardIndex int `json:"shard_index"`
			Tests      []struct {
				Entry struct {
					Path       string `json:"path"`
					Name       string `json:"name"`
					ParamsHash string `json:"params_hash"`
				} `json:"entry"`
			} `json:"tests"`
		} `json:"assignments"`
	}
	if err := json.Unmarshal(planRaw, &plan); err == nil && len(plan.Assignments) > 0 {
		for _, a := range plan.Assignments {
			if a.ShardIndex == index {
				out := make([]*teov1.TestEntryRef, 0, len(a.Tests))
				for _, t := range a.Tests {
					out = append(out, &teov1.TestEntryRef{
						Path: t.Entry.Path, Name: t.Entry.Name, ParamsHash: t.Entry.ParamsHash,
					})
				}
				return out, nil
			}
		}
	}
	// Fall through: manifest round-robin.
	var manifest struct {
		Tests []struct {
			Path       string `json:"path"`
			Name       string `json:"name"`
			ParamsHash string `json:"params_hash"`
		} `json:"tests"`
	}
	if err := json.Unmarshal(planRaw, &manifest); err != nil {
		return nil, err
	}
	var out []*teov1.TestEntryRef
	for i, t := range manifest.Tests {
		if i%total == index {
			out = append(out, &teov1.TestEntryRef{
				Path: t.Path, Name: t.Name, ParamsHash: t.ParamsHash,
			})
		}
	}
	return out, nil
}

// ReportTestFinished accepts a single test outcome from a worker. Idempotent
// on (shard_id, test_id, attempt) via the unique constraint.
func (s *WorkersService) ReportTestFinished(ctx context.Context, t *teov1.TestFinished) (*teov1.Ack, error) {
	if t == nil || t.GetShardId() == "" {
		return &teov1.Ack{Ok: false}, errors.New("invalid TestFinished")
	}
	var repoID string
	if err := s.Pool.QueryRow(ctx, `
        SELECT r.repo_id FROM teo.shards sh
        JOIN teo.runs r ON r.id = sh.run_id
        WHERE sh.id = $1
    `, t.GetShardId()).Scan(&repoID); err != nil {
		return &teov1.Ack{Ok: false}, err
	}
	fingerprint := t.GetTestPath() + "::" + t.GetTestName() + "::" + t.GetParamsHash()
	var testID string
	if err := s.Pool.QueryRow(ctx, `
        INSERT INTO teo.tests (id, repo_id, fingerprint, path, name, params_hash, runner, status)
        VALUES ($1, $2, $3, $4, $5, $6, 'pytest', 'active')
        ON CONFLICT (repo_id, fingerprint) DO UPDATE SET last_seen = now()
        RETURNING id
    `, uuid.New().String(), repoID, fingerprint, t.GetTestPath(), t.GetTestName(), t.GetParamsHash()).Scan(&testID); err != nil {
		return &teov1.Ack{Ok: false}, err
	}

	var clusterID *string
	outcome := t.GetOutcome()
	if (outcome == "failed" || outcome == "errored" || outcome == "timed_out") && t.GetFailureStack() != "" {
		cid, err := s.Cluster.UpsertCluster(ctx, repoID, t.GetFailureStack(), t.GetFailureMessage())
		if err == nil && cid != "" {
			clusterID = &cid
		}
	}
	if _, err := s.Pool.Exec(ctx, `
        INSERT INTO teo.test_executions
            (shard_id, test_id, attempt, outcome, duration_ms, started_at, finished_at, failure_cluster_id)
        VALUES ($1, $2, $3, $4, $5, now() - make_interval(secs => $5/1000.0), now(), $6)
        ON CONFLICT (shard_id, test_id, attempt) DO NOTHING
    `, t.GetShardId(), testID, t.GetAttempt(), outcome, t.GetDurationMs(), clusterID); err != nil {
		return &teov1.Ack{Ok: false}, err
	}
	return &teov1.Ack{Ok: true}, nil
}
