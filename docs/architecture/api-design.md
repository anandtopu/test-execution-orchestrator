# TEO — API Design

**Status:** Draft
**Date:** 2026-04-30

Three surfaces: **gRPC** (worker traffic, internal), **REST** (CI integrations, webhooks), **GraphQL** (UI reads + dashboards). All share a single auth layer (JWT for humans, API keys for machines).

---

## 1. gRPC services (internal + worker plane)

Protos live in `proto/teo/v1/`. Wire compatibility = backward-compat additive only. Breaking changes require a `v2` package.

### `Runs` service
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
