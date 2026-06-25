# TEO — Architecture Diagrams

**Status:** Current (reflects code at `main`, v1.0.0 shipped + v1.1 WebSocket subs)
**Audience:** Engineers, reviewers, new contributors
**Companion docs:** [`overview.md`](overview.md) (the spec / "should"), [`schema.md`](schema.md) (datastores), [`er-diagram.md`](er-diagram.md) (Postgres relationships), [`../../progress.md`](../../progress.md) (what's wired up — ground truth)

All diagrams are [Mermaid](https://mermaid.js.org/); GitHub, VS Code, and most IDEs render them inline. This file is descriptive of the **as-built** system, not aspirational. When it disagrees with `progress.md`, trust `progress.md` for code.

---

## 1. System context

Who talks to TEO and what it persists to. TEO is **single-tenant, AWS-only** — there are no `tenant_id` columns anywhere.

```mermaid
flowchart TB
    CI["CI pipeline<br/>(teo CLI in a build step)"]
    DEV["Developer<br/>(browser)"]
    GH["GitHub<br/>(App + webhooks + Checks)"]

    subgraph TEO["TEO Control Plane (Helm release on EKS)"]
        API["API Gateway<br/>gRPC + REST + GraphQL"]
        CORE["Cooperating Go services<br/>(see §2)"]
        WEB["Web UI<br/>Next.js 15 / GraphQL"]
    end

    PG[("Postgres<br/>OLTP")]
    CH[("ClickHouse<br/>OLAP")]
    S3[("S3<br/>logs / cold")]
    NATS["NATS JetStream<br/>dispatch"]

    CI -- "CreateRun (gRPC)" --> API
    CI -- "API key" --> API
    DEV -- "GraphQL (2s poll / WS)" --> WEB
    WEB --> API
    GH -- "webhook (HMAC SHA-256)" --> API
    API -- "Check Run updates" --> GH

    API --> CORE
    CORE --> PG
    CORE --> CH
    CORE --> S3
    CORE <--> NATS
```

---

## 2. Service fan-out (containers / processes)

Seven Go binaries (`cmd/{teo,api,run-manager,scheduler,result-pipeline,predictor,worker}`) plus the optional Python ML predictor. The scheduler is a **pure function** invoked in-process by the Run Manager (it is a binary for replay/testing, not a long-running service in the hot path).

```mermaid
flowchart LR
    CLI["teo CLI"]

    subgraph CP["Control plane"]
        API["API Gateway<br/>cmd/api"]
        RM["Run Manager<br/>cmd/run-manager<br/>state machine, leader-elected<br/>per-run via pg advisory lock"]
        SCH["Scheduler<br/>Plan(tests,preds,fleet,constraints)<br/>LPT bin-pack, ≤4/3×OPT"]
        PRED["Predictor<br/>cmd/predictor<br/>Go heuristic (always-on)"]
        MLP["predictor-ml<br/>Python LightGBM<br/>(optional, auto-fallback)"]
        RP["Result Pipeline<br/>cmd/result-pipeline<br/>OTLP :4317"]
    end

    subgraph DATA["Stores"]
        PG[("Postgres")]
        CH[("ClickHouse")]
        S3[("S3")]
        NATS["NATS JetStream<br/>teo.shards.dispatch"]
    end

    subgraph FLEET["Worker pool (Karpenter: spot + on-demand)"]
        W1["Worker agent<br/>cmd/worker"]
        ADP["runner adapters<br/>pytest / go test / jest"]
    end

    CLI -- "CreateRun" --> API
    API --> RM
    RM -- "in-proc" --> SCH
    RM -- "gRPC Predict" --> PRED
    PRED -. "HTTP /v1/predict (default-on)" .-> MLP
    RM -- "shards" --> PG
    RM -- "dispatch" --> NATS
    NATS --> W1
    W1 --> ADP
    W1 -- "OTLP spans + TestFinished" --> RP
    RP --> PG
    RP --> CH
    RP --> S3
    RM -- "UINotify hint" --> NATS
    NATS -- "runChanged" --> API
    API -- "WS / GraphQL" --> CLI
```

**Key contracts** (`proto/teov1/`):
- `Runs` service — `CreateRun`, `GetRun`, `CancelRun` (CLI/API → Run Manager).
- `Workers` service — `Register`, `Heartbeat`, `PullAssignment`, `ReportTestFinished`, `ReportShardFinished` (worker ↔ control plane).

---

## 3. Run lifecycle (sequence)

The happy path of a single run, from CI invocation to terminal status.

```mermaid
sequenceDiagram
    autonumber
    participant CLI as teo CLI
    participant API as API Gateway
    participant RM as Run Manager
    participant PR as Predictor
    participant SC as Scheduler
    participant NA as NATS
    participant WK as Worker
    participant RP as Result Pipeline
    participant PG as Postgres
    participant CH as ClickHouse

    CLI->>API: CreateRun(manifest, commit, branch, idempotency_key)
    API->>PG: INSERT run (status=pending)
    API-->>CLI: Run{id, status=pending}
    RM->>PG: claim run (advisory lock), status=planning
    RM->>PR: Predict(fingerprints) → {p50,p95,flake_prob}
    RM->>SC: Plan(tests, predictions, fleet, constraints)
    SC-->>RM: AssignmentPlan (JSON, replayable)
    RM->>PG: INSERT shards, run_plans; status=dispatching
    RM->>NA: publish teo.shards.dispatch
    WK->>NA: PullAssignment
    NA-->>WK: Assignment{shard_id, tests}
    RM->>PG: status=running
    loop per test
        WK->>WK: run via adapter (pytest/gotest/jest)
        WK->>RP: OTLP spans + ReportTestFinished
        RP->>PG: INSERT test_executions
        RP->>CH: INSERT test_runs + span_events
    end
    WK->>RP: ReportShardFinished(status)
    RM->>RM: last shard? → finalize
    RM->>PG: failure clustering, flake detection
    RM->>API: UINotify hint (teo.ui.run_changed)
    RM->>PG: status=succeeded|failed
    API-->>CLI: terminal status
```

---

## 4. Run state machine

Driven by the Run Manager (`internal/runmanager`), persisted to `teo.runs.status`. Each transition is committed under a per-run `pg_try_advisory_xact_lock` so only one Run Manager replica drives a given run (ADR-0013).

```mermaid
stateDiagram-v2
    [*] --> pending: CreateRun
    pending --> planning: claimed by Run Manager
    planning --> dispatching: AssignmentPlan ready
    dispatching --> running: shards on NATS
    running --> finalizing: last shard finished
    running --> running: reschedulePreempted (spot drain)
    finalizing --> succeeded: all shards ok
    finalizing --> failed: any shard failed
    pending --> cancelled: CancelRun
    planning --> cancelled: CancelRun
    dispatching --> cancelled: CancelRun
    running --> cancelled: CancelRun
    succeeded --> [*]
    failed --> [*]
    cancelled --> [*]
```

Valid statuses (CHECK constraint on `teo.runs.status`): `pending, planning, dispatching, running, finalizing, succeeded, failed, cancelled`.

## 4b. Shard state machine

`teo.shards.status` — note `preempted` and `lost`, which feed the reschedule sweep.

```mermaid
stateDiagram-v2
    [*] --> pending: shard created
    pending --> running: worker pulled assignment
    running --> succeeded: ReportShardFinished(succeeded)
    running --> failed: ReportShardFinished(failed)
    running --> preempted: spot interruption (IMDSv2 drain)
    running --> lost: heartbeat missed > 30s
    preempted --> [*]: residue re-sharded (attempt+1)
    lost --> [*]: residue re-sharded
    succeeded --> [*]
    failed --> [*]
```

When a shard goes `preempted`/`lost`, the Run Manager's `reschedulePreempted` sweep recomputes the residue and creates a fresh shard, recording it under `runs.meta.reshards[<new_shard_id>]`.

---

## 5. Spot-interruption drain flow

How a Spot reclaim is turned into a clean reschedule rather than lost work (`internal/spot/`, `internal/worker/drain`).

```mermaid
flowchart TB
    IMDS["IMDSv2 poller<br/>(internal/spot)"]
    DRAIN["Agent drain SM<br/>atomic draining flag"]
    NAK["NATS nak<br/>(in-flight tests returned)"]
    SWEEP["Run Manager<br/>reschedulePreempted"]
    NEWSHARD["fresh shard<br/>attempt+1 → on-demand"]
    META["runs.meta.reshards[new_id]"]

    IMDS -- "2-min reclaim notice" --> DRAIN
    DRAIN -- "stop pulling new work" --> DRAIN
    DRAIN --> NAK
    NAK --> SWEEP
    SWEEP --> NEWSHARD
    SWEEP --> META
    NEWSHARD -- "preferred NodePool" --> ONDEMAND["teo-workers-on-demand"]
```

---

## 6. Deployment topology

One Helm umbrella chart (`deploy/helm/teo/`) on EKS. Control-plane services run 2 replicas; workers are Karpenter-provisioned across two NodePools.

```mermaid
flowchart TB
    subgraph EKS["EKS cluster"]
        subgraph CPNS["namespace: teo (control plane, 2 replicas each)"]
            API["api"]
            RM["run-manager"]
            RP["result-pipeline"]
            PRED["predictor"]
            WEBUI["web (Next.js sidecar)"]
            DEX["Dex (OIDC)"]
        end
        subgraph WNS["Karpenter NodePools"]
            SPOT["teo-workers-spot<br/>(primary)"]
            OD["teo-workers-on-demand<br/>(preemption fallback)"]
        end
        subgraph STATE["stateful (subcharts)"]
            PG[("CloudNativePG<br/>HA primary")]
            CH[("ClickHouse<br/>single-shard")]
            NATS["NATS JetStream"]
        end
    end
    S3[("S3<br/>logs + cold + backups")]

    API --- PG
    RM --- PG
    RM --- NATS
    RP --- PG
    RP --- CH
    RP --- S3
    SPOT --- NATS
    OD --- NATS
    PG -- "pg_basebackup daily" --> S3
    CH -- "BACKUP daily" --> S3
```

---

## 7. How to keep these current

- These diagrams describe **wired-up behavior**. A behavior-changing PR that alters a state, a service boundary, or a contract should update the relevant diagram in the same commit (same rule as `progress.md`).
- Diagram source is plain Mermaid in this Markdown file — edit it directly; no build step.
- The authoritative status of any epic/FR is always [`progress.md`](../../progress.md); the *spec* intent is [`overview.md`](overview.md).
