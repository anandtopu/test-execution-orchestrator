# TEO — Documentation Index

This directory holds the architecture, requirements, ADRs, backlog, and process docs for the **Test Execution Orchestrator (TEO)**. The product spec is in the repository-root [`PRD.md`](../PRD.md).

## Reading order for new contributors

1. [`handoff/DEVELOPER_HANDOFF.md`](handoff/DEVELOPER_HANDOFF.md) — **start here to get productive** (build, test, navigate, gotchas).
2. [`PRD.md`](../PRD.md) — what we're building and why.
3. [`progress.md`](../progress.md) — **what's already built** (live status dashboard).
4. [`adr/0012-mvp-scope-cut.md`](adr/0012-mvp-scope-cut.md) — what's in v1.0 vs deferred.
5. [`architecture/overview.md`](architecture/overview.md) — system diagram and component summaries.
6. [`architecture/diagrams.md`](architecture/diagrams.md) — **as-built** Mermaid diagrams (services, run lifecycle, state machines, deployment).
7. [`architecture/schema.md`](architecture/schema.md) + [`architecture/er-diagram.md`](architecture/er-diagram.md) — current datastore schema + ER diagram.
8. [`architecture/tech-stack.md`](architecture/tech-stack.md) — every framework choice.
9. [`requirements/functional.md`](requirements/functional.md) — what the system must do.
10. [`backlog/epics.md`](backlog/epics.md) — how the work is broken down.

## Layout

```
docs/
├── README.md                        ← you are here
├── handoff/
│   └── DEVELOPER_HANDOFF.md         ← onboarding: build/test/navigate/gotchas
├── architecture/
│   ├── overview.md                  ← components, data flow, diagram (the spec)
│   ├── diagrams.md                  ← as-built Mermaid diagrams (services, lifecycle, state machines, deploy)
│   ├── schema.md                    ← CURRENT Postgres + ClickHouse + S3 schema (migrations 001..006)
│   ├── er-diagram.md                ← Mermaid ER diagram (Postgres)
│   ├── tech-stack.md                ← languages, frameworks, libs
│   ├── data-model.md                ← original 2026-04-30 design draft (drifted; see schema.md)
│   ├── api-design.md                ← gRPC + REST + GraphQL contracts
│   └── deployment.md                ← Helm, CI/CD, environments, DR
├── requirements/
│   ├── functional.md                ← FR-101..FR-1005
│   └── non-functional.md            ← perf, scale, sec, ops SLOs
├── adr/                             ← Architecture Decision Records
│   ├── 0001-go-backend.md
│   ├── 0002-self-hosted-single-tenant.md
│   ├── 0003-storage-tiering.md
│   ├── 0004-otel-source-of-truth.md
│   ├── 0005-scheduler-pure-function.md
│   ├── 0006-cluster-autoscaler-mvp.md
│   ├── 0007-nats-jetstream.md
│   ├── 0008-graphql-api-surface.md
│   ├── 0009-helm-chart-deployment.md
│   ├── 0010-stable-test-fingerprint.md
│   ├── 0011-wilson-interval-flake.md
│   ├── 0012-mvp-scope-cut.md
│   ├── 0013-run-manager-leader-election.md
│   ├── 0014-auth-oidc-and-api-keys.md
│   ├── 0015-secrets-k8s-native.md
│   ├── 0016-redaction-on-worker.md
│   ├── 0017-backup-and-dr.md
│   ├── 0018-license-policy-apache2.md
│   ├── 0019-ml-predictor.md
│   └── 0020-spot-interruption-checkpointing.md
├── backlog/
│   ├── epics.md                     ← E-01..E-11 with weekly milestones
│   ├── stories.md                   ← user stories with AC
│   └── tasks.md                     ← engineering tasks (≤ 2d each)
└── process/
    └── definition-of-done.md        ← per-story DoD checklist
```

## Cross-referencing convention

- **FR IDs** (Functional Requirements) — `FR-101`, `FR-302`, etc.
- **NFR IDs** (Non-Functional) — `NFR-PERF-101`, `NFR-SEC-501`, etc.
- **ADR IDs** — `ADR-0001` … `ADR-0020`.
- **Epic IDs** — `E-01` … `E-16`.
- **Story IDs** — `S-<epic>-<n>` (e.g., `S-05-01`).
- **Task IDs** — `T-<story>-<n>` (e.g., `T-05-01-02`).

PRs reference the story they implement and link relevant FR/ADR IDs in their description.

## Key constraints (from intake)

- **Self-hosted, OSS (Apache 2.0)** — see [ADR-0002](adr/0002-self-hosted-single-tenant.md), [ADR-0018](adr/0018-license-policy-apache2.md).
- **AWS-only for v1** — Karpenter + spot in scope, see [ADR-0006](adr/0006-cluster-autoscaler-mvp.md), [ADR-0020](adr/0020-spot-interruption-checkpointing.md).
- **Go backend** (with Python ML predictor) — see [ADR-0001](adr/0001-go-backend.md), [ADR-0019](adr/0019-ml-predictor.md).
- **3-month delivery, 3-4 engineers** — see [ADR-0012](adr/0012-mvp-scope-cut.md) for what's in scope vs deferred.
- **Greenfield** — no existing infra to integrate with.

## Open questions still on the table

These are listed in the `overview.md` and the relevant ADRs, surfaced here for visibility:

1. **Stack-trace fingerprinting across languages.** v1 ships Python normalization; everything else falls back to a coarse hash.
2. **Cold-start defaults.** Lean: hardcode runner-level defaults (pytest = 1.2s).
3. **Where the redactor runs.** Lean: on the worker (so secrets never traverse the wire).
4. **Open question from PRD §11 about the `test orchestrator` vs `build orchestrator` boundary.** Not addressed in MVP; revisit when Bazel adapter lands.
