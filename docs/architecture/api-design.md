# TEO — API Design

**Status:** Draft
**Date:** 2026-04-30

Three surfaces: **gRPC** (worker traffic, internal), **REST** (CI integrations, webhooks), **GraphQL** (UI reads + dashboards). All share a single auth layer (JWT for humans, API keys for machines).

---

## 1. gRPC services (internal + worker plane)

Protos live in `proto/teov1/` (flat layout; the Protobuf package stays `teo.v1`, wire-compatible). Generated code lands in `internal/proto/teov1/` via `make proto`. Wire compatibility = backward-compat additive only. Breaking changes require a `v2` package.

### `Runs` service

**Implementation status (`runs-grpc`):** `CreateRun`, `GetRun`, and `CancelRun` are wired in `internal/grpcsvc/runs.go` (`RunsService` embeds `teov1.UnimplementedRunsServer`) over the shared transport-agnostic `internal/runsvc.Service` — the same intake/validation/idempotency core HTTP (`internal/api/runs.go`) and GraphQL use, so the three transports can't drift. Domain errors map to gRPC codes (validation→`InvalidArgument`, missing repo/run→`NotFound`, idempotency conflict on a different commit→`AlreadyExists`, else→`Internal` with the raw error logged server-side). The Runs RPCs require auth via `grpcsvc.AuthUnaryInterceptor` (`authorization` metadata, same primitives as the HTTP middleware) and reject unauthenticated callers with `codes.Unauthenticated`; the internal `Workers` dispatch RPCs stay open. `StreamRunEvents` is declared but **not yet implemented** (no UI/CLI consumer at v1.0). Registered in `cmd/api/main.go` next to `WorkersService`.
```proto
service Runs {
  rpc CreateRun(CreateRunRequest) returns (Run);
  rpc GetRun(GetRunRequest) returns (Run);
  rpc CancelRun(CancelRunRequest) returns (Run);
  rpc StreamRunEvents(StreamRunEventsRequest) returns (stream RunEvent);
}

message CreateRunRequest {
  string repo_full_name = 1;        // "owner/name"
  string commit_sha = 2;
  string branch = 3;
  TestManifest manifest = 4;
  RunBudget budget = 5;
  string trigger_actor = 6;
  int32 trigger_pr_number = 7;
}

message TestManifest {
  string runner = 1;                 // "pytest"
  repeated TestEntry tests = 2;
}

message TestEntry {
  string path = 1;
  string name = 2;                   // fully qualified
  string params_hash = 3;
  repeated string tags = 4;          // "needs-postgres", "exclusive-port-5432"
}

message RunBudget {
  int32 max_seconds = 1;
  int32 max_workers = 2;
  // No cost-budget mode in MVP
}
```

### `Workers` service (worker → control plane)
```proto
service Workers {
  rpc Register(RegisterRequest) returns (RegisterResponse);
  rpc Heartbeat(HeartbeatRequest) returns (HeartbeatResponse);
  rpc PullAssignment(PullAssignmentRequest) returns (Assignment);
  rpc ReportTestStarted(TestStarted) returns (Ack);
  rpc ReportTestFinished(TestFinished) returns (Ack);
  rpc ReportShardFinished(ShardFinished) returns (Ack);
}

message Assignment {
  string shard_id = 1;
  string run_id = 2;
  repeated TestEntry tests = 3;
  int32 predicted_duration_ms = 4;
  string runner_image = 5;
  map<string,string> env = 6;
}

message TestFinished {
  string run_id = 1;
  string shard_id = 2;
  string test_path = 3;
  string test_name = 4;
  string params_hash = 5;
  int32 attempt = 6;
  TestOutcome outcome = 7;
  int32 duration_ms = 8;
  bytes otlp_span_proto = 9;        // single span for the test, embedded
  string log_object_key = 10;       // S3 key for streamed logs
  Failure failure = 11;             // populated iff outcome != PASSED
}

message Failure {
  string message = 1;
  string stack = 2;
  string normalized_stack_fingerprint = 3;  // computed on worker (cheap)
}

enum TestOutcome { PASSED=0; FAILED=1; SKIPPED=2; ERRORED=3; TIMED_OUT=4; }
```

### `Predictor` service

**Implementation status (`ml-predictor`, FR-607):** the Run Manager calls the Python LightGBM service over **HTTP** (`POST <TEO_PREDICTOR_ML_URL>/v1/predict`, snake_case JSON) via `internal/predictor.MLClient`, not the gRPC `Predictor` contract below — a deliberate, documented divergence from ADR-0019 (the call is in-cluster, low-QPS, and the Go heuristic fallback makes the wire format non-load-bearing). `internal/predictor.Fallback` tries ML first and reverts to the always-on Go `Heuristic` on any failure, so the system runs with the Python service down (the ADR-0019 non-negotiable). The proto service is retained for a future migration. The gRPC contract:

```proto
service Predictor {
  rpc Predict(PredictRequest) returns (PredictResponse);
}

message PredictRequest {
  string repo_full_name = 1;
  repeated TestEntry tests = 2;
}

message PredictResponse {
  message Prediction {
    string fingerprint = 1;
    int32 p50_duration_ms = 2;
    int32 p95_duration_ms = 3;
    float flake_probability = 4;
    bool is_cold_start = 5;       // true if we used a fallback heuristic
  }
  repeated Prediction predictions = 1;
}
```

### Auth & metadata
- All RPCs require auth metadata: `authorization: Bearer <jwt-or-api-key>`.
- Idempotency: `Workers.ReportTestFinished` is idempotent on `(shard_id, test_path, test_name, params_hash, attempt)`; reasons: at-least-once delivery from worker on retry.
- Deadlines: clients MUST set deadlines. Server enforces a max of 30s on synchronous RPCs and uses streaming for long-running flows.

---

## 2. REST endpoints (CI integrations)

`/api/v1` prefix. JSON only. Used primarily by the CLI and by webhook receivers.

| Method | Path | Purpose | Auth |
|---|---|---|---|
| POST | `/runs` | Create run (CLI) | API key |
| GET | `/runs/{id}` | Run snapshot | JWT or API key |
| POST | `/runs/{id}/cancel` | Cancel | JWT or API key |
| GET | `/runs/{id}/junit.xml` | Download JUnit XML | JWT or API key |
| GET | `/runs/{id}/otlp` | Download OTLP proto | JWT or API key |
| POST | `/webhooks/github` | GitHub App events | HMAC signature |
| GET | `/healthz` | Liveness | none |
| GET | `/readyz` | Readiness | none |
| GET | `/metrics` | Prometheus | network policy |

Errors follow [RFC 7807](https://datatracker.ietf.org/doc/html/rfc7807) (`application/problem+json`).

---

## 3. GraphQL (UI + power users)

Schema-first via `gqlgen`. One GraphQL endpoint at `/graphql` (POST) and `/graphql/subscriptions` (WebSocket) for live updates.

```graphql
type Query {
  run(id: ID!): Run
  runs(
    repo: String,
    branch: String,
    status: [RunStatus!],
    first: Int = 25,
    after: String
  ): RunConnection!

  test(repoFullName: String!, fingerprint: String!): Test
  tests(repo: String!, status: TestStatus, first: Int = 50): TestConnection!

  failureCluster(id: ID!): FailureCluster
  failureClusters(repo: String!, since: DateTime): [FailureCluster!]!

  flakeReport(repo: String!): FlakeReport!
}

type Mutation {
  cancelRun(runId: ID!): Run!
  rerun(runId: ID!, only: RerunFilter): Run!     # only=failed|quarantined|all
  quarantine(testId: ID!, reason: String!): Test!
  unquarantine(testId: ID!): Test!
}

type Subscription {
  runEvents(runId: ID!): RunEvent!
}

type Run {
  id: ID!
  repo: Repo!
  commitSha: String!
  branch: String!
  status: RunStatus!
  startedAt: DateTime
  finishedAt: DateTime
  totalDurationMs: Int
  shards: [Shard!]!
  failureClusters: [FailureCluster!]!
  testExecutions(outcome: [TestOutcome!]): [TestExecution!]!
}
```

Pagination is Relay-style (`Connection`/`Edge`/`PageInfo`).

### UI-observability fields (`graphql-schema-fields`)

The redesigned marquee UI (Clusters spatial map, Flakes sparklines, Run-detail predictor-calibration overlay) is served by **additive** fields on the existing types — no migration, every value computed server-side from existing Postgres tables. The hand-written SDL at `/graphql/schema` (`internal/api/server.go`) and the resolvers (`internal/api/graphql.go` / `graphql_resolvers.go`) carry them:

- **`FailureCluster`** gains spatial-map coordinates `x`/`y`/`r` (`computeClusterLayout`: x = last_seen newest→left, y = log-scaled occurrences, r = blast-radius px), `category` (`classifyClusterCategory` keyword heuristic — panic/timeout/network/race/assertion, parity with the web `classifyCategory` in `web/src/lib/teo-adapt.ts`), `stackFingerprint`, and `affectedRuns` (distinct-run subquery over `test_executions.failure_cluster_id`). x/y/r are presentation-only and relative to the returned page.
- **`FlakeRecord`** gains `wilsonUpper`, `spark` (last-20 P/F/S outcomes, chronological, one batched `test_id = ANY($1::uuid[])` query), `status`, `durationMeanMs`, plus `quarantinedAt` (RFC3339, null when not quarantined — `COALESCE(fr.quarantined_at, t.quarantined_at)`) and `ownerTeam`.
- **`Run`** gains `predictor { mae rho modelVersion p50DeltaMs p95DeltaMs sampleCount confidence }` (`queryRunPredictor`, computed from finished shards; `modelVersion` reads `runs.meta->>'predictor_model'`, falls back to `heuristic`; null for <2 finished shards). The same values are also exposed as flat `predictorMae`/`predictorRho`/`modelVersion` Run fields (memoized per request via `cachedRunPredictor`) for the calibration overlay.
- **`Shard`** gains `deltaMs` (actual−predicted) plus `predictionConfidence`/`modelVersion`; the per-shard confidence/model_version resolve to **null** until a future migration adds `teo.shards.prediction_confidence`/`model_version` columns (`queryShards` does not SELECT them yet — they do not light up automatically).

The web consumers are `web/src/lib/queries.ts` (named operations) and the prop-driven `web/src/components/teo/{Clusters,Flakes}.tsx` + `RunDetailScreen` screens, which adapt the rows through the pure `web/src/lib/teo-adapt.ts` (`adaptClusters`/`adaptFlakes`/`adaptRun`/`adaptStatus`). All four marquee screens render from GraphQL — the `teo-data.ts` MOCK is no longer imported by any `web/src/app/` route.

---

## 4. Versioning, deprecation, compatibility

- **gRPC**: package `teo.v1`. Adding fields = OK; renumbering or removing = breaking, requires `v2`. Deprecated fields keep ticking for one release after deprecation announcement.
- **REST**: `/api/v1`. Same rules. New fields are additive; removal is a `v2`.
- **GraphQL**: deprecation via `@deprecated(reason: "...")`. UI consumers update at their pace.
- **Worker protocol compatibility**: workers report their `agent_version` on Register. Control plane refuses workers older than `MIN_AGENT_VERSION` (set per release).

---

## 5. Idempotency & retries

| Endpoint | Idempotency key | Retry policy |
|---|---|---|
| `POST /runs` | `Idempotency-Key` header (UUID) | Client may retry on 5xx for 24h |
| `Workers.ReportTestFinished` | `(shard_id, test_path, test_name, params_hash, attempt)` | Server dedupes |
| GraphQL mutations | None (UI handles user-initiated retries) | n/a |
| Webhooks (GitHub) | GitHub provides delivery ID; we dedupe | GitHub retries on non-2xx |

---

## 6. Rate limits

- Per API key: 100 req/s burst, 10 req/s sustained for run creation.
- Workers: no rate limit; backpressure handled at NATS level.
- UI: no per-user limit in v1.

---

## 7. Documentation

- `proto/teo/v1/*.proto` is canonical for gRPC.
- OpenAPI spec generated from REST handlers (`oapi-codegen`); served at `/api/v1/openapi.json`.
- GraphQL schema served at `/graphql/schema`; introspection enabled in dev, gated by role in prod.
