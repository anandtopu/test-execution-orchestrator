# TEO — Deployment Strategy

**Status:** Draft
**Date:** 2026-04-30

TEO is OSS, self-hosted, AWS-only for v1. The reference deployment is a **Helm chart** on EKS. Operators with their own k8s flavor can adapt.

---

## 1. Reference topology

```
┌──────────────────────────── AWS account ────────────────────────────┐
│                                                                     │
│  Route 53 ──▶ ALB ──▶ ┌──────────── EKS cluster ──────────────┐     │
│                       │                                         │     │
│                       │  namespace: teo                         │     │
│                       │   ┌────────────┐  ┌────────────┐       │     │
│                       │   │ api (×2)   │  │ run-mgr (×2│       │     │
│                       │   └────────────┘  │  HA leader)│       │     │
│                       │   ┌────────────┐  └────────────┘       │     │
│                       │   │ scheduler  │  ┌────────────┐       │     │
│                       │   │ (×1, lead) │  │result-pipe │       │     │
│                       │   └────────────┘  │   (×2)     │       │     │
│                       │   ┌────────────┐  └────────────┘       │     │
│                       │   │predictor   │  ┌────────────┐       │     │
│                       │   │  (×1)      │  │  web-ui    │       │     │
│                       │   └────────────┘  │  (×2)      │       │     │
│                       │                   └────────────┘       │     │
│                       │  namespace: teo-data                    │     │
│                       │   CloudNativePG (Postgres, primary+1)   │     │
│                       │   ClickHouse (single shard, 1 replica)  │     │
│                       │   NATS JetStream (3-node cluster)       │     │
│                       │   MinIO (or external S3)                │     │
│                       │                                         │     │
│                       │  namespace: teo-workers                 │     │
│                       │   Worker pods (HPA on queue depth)      │     │
│                       │   Karpenter NodePools:                  │     │
│                       │     - teo-workers-spot (preferred)      │     │
│                       │     - teo-workers-on-demand (fallback)  │     │
│                       └─────────────────────────────────────────┘     │
│                                                                     │
│  S3: teo-artifacts (cold archive)                                   │
│  ECR: not used — images on GHCR                                     │
└─────────────────────────────────────────────────────────────────────┘
```

---

## 2. Helm chart layout

```
deploy/helm/teo/
  Chart.yaml                       # umbrella
  values.yaml                      # all defaults, well-documented
  values-dev.yaml                  # local kind/minikube
  values-prod.yaml                 # production hardening template
  templates/
    api/                           # Deployment, Service, ServiceMonitor, NetworkPolicy
    run-manager/
    scheduler/
    result-pipeline/
    predictor/
    web-ui/
    workers/                       # Deployment template per supported runner
    ingress.yaml
    serviceaccounts.yaml
    rbac.yaml
    secrets-template.yaml          # not the actual secret; pattern only
    nats.yaml                      # subchart values
    postgres.yaml                  # CloudNativePG manifest
    clickhouse.yaml                # CH operator manifest
  charts/                          # vendored subcharts
    cnpg/                          # CloudNativePG
    clickhouse-operator/
    nats/
    minio/
    dex/                           # OIDC
```

Operators install with:
```bash
helm install teo deploy/helm/teo \
  -n teo --create-namespace \
  -f my-values.yaml
```

`my-values.yaml` overrides domain, OIDC config, storage classes, and worker runner images.

---

## 3. Environments

| Env | Purpose | Notes |
|---|---|---|
| **dev** | Local kind/minikube | One-replica everything; in-cluster MinIO; Dex with mock provider |
| **staging** | Internal pre-release, runs TEO's own tests | Mirrors prod values; smaller instance types |
| **prod** | What we publish to users via Helm | Documented hardening checklist; SOC2 deferred to post-MVP |

The Helm chart is the same across environments; only `values-*.yaml` differ.

---

## 4. CI/CD (for the TEO repo itself)

GitHub Actions workflows in `.github/workflows/`:

| Workflow | Trigger | Steps |
|---|---|---|
| `ci.yml` | push, PR | lint (`golangci-lint`), unit tests, integration tests, Trivy scan, SBOM (syft), Helm lint |
| `release.yml` | tag `v*` | `goreleaser` (binaries to GH Releases), Docker build (multi-arch amd64/arm64) → GHCR, cosign sign, Helm chart publish to `gh-pages` |
| `dogfood.yml` | nightly | Deploy latest `main` to staging EKS; run smoke suite via TEO itself |

**No** auto-promote to prod — release is manual via tag.

---

## 5. Image policy

- Base: `gcr.io/distroless/static-debian12` for Go services; `gcr.io/distroless/nodejs22-debian12` for the UI.
- Worker runner images: `python:3.12-slim` + the worker agent baked in. One image per supported runner.
- All images signed with cosign; signature verified on pull via `policy-controller`.
- Vulnerability gate: HIGH+ blocks release.

---

## 6. Configuration precedence

1. CLI flag (highest)
2. Environment variable (`TEO_*`)
3. Config file (`/etc/teo/config.yaml`)
4. Default (lowest)

Sensitive values (DB password, API keys, GitHub App private key) come from k8s Secrets via env, never config file. Helm chart never bakes secrets.

---

## 7. Day-2 operations

| Operation | Mechanism |
|---|---|
| Schema migration | `teo migrate up` job, run as Helm pre-upgrade hook |
| Postgres backup | CloudNativePG continuous WAL to S3 |
| ClickHouse backup | `clickhouse-backup` operator, daily to S3 |
| Restore drill | Documented runbook; quarterly required by chart README |
| Rotate API keys | UI flow + audit log entry |
| Rotate JWT signing key | k8s Secret + rolling restart |

---

## 8. Upgrade strategy

- Patch (z): Helm upgrade in place; rolling restart of Deployments. No DB changes.
- Minor (y): May ship migrations; pre-upgrade Helm hook applies them. **Forward-only**.
- Major (x): Documented breaking-change list; users opt in. We commit to a 6-month deprecation window for any breaking API change (post-1.0).

Rollback for migrations is **forward only** — we ship a forward-fix migration, never a `down`.

---

## 9. Observability

- TEO emits OTel into its own pipeline (dogfood). Operators get a separate, out-of-band Tempo/Jaeger sink for debugging when TEO is degraded.
- Bundled Grafana dashboards (in `deploy/helm/teo/templates/grafana-dashboards/`) for: API latency, run state-machine, scheduler decision time, ClickHouse insert lag, NATS consumer lag.
- Prometheus alerting rules: API p95 > 500ms for 5m, run stuck in `running` > budget × 2, ClickHouse insert lag > 30s.

---

## 10. Hardening checklist (prod values)

- [ ] All Deployments have CPU/memory requests + limits.
- [ ] PodSecurityStandards: `restricted` for everything except worker runner pods (which need privileged for some integration tests; document the trade-off).
- [ ] NetworkPolicy: deny-all default; explicit allow per service.
- [ ] TLS terminated at ALB; mTLS between control-plane services (cert-manager).
- [ ] Postgres + ClickHouse encrypted at rest (EBS encryption) and in transit (TLS).
- [ ] S3 bucket encrypted (SSE-KMS), versioning + lifecycle policy enabled.
- [ ] OIDC required for the UI; API keys scoped + rotatable.
- [ ] Audit log retention ≥ 1 year.

---

## 11. Disaster recovery

| Aspect | Target | Mechanism |
|---|---|---|
| RPO (Postgres) | ≤ 5 min | WAL streaming to S3 |
| RPO (ClickHouse) | ≤ 1 day | Daily backup; ClickHouse loss is recoverable from `test_executions` in Postgres |
| RPO (S3 artifacts) | 0 | Versioning + cross-region replication (operator-config) |
| RTO | ≤ 1 hour | Documented restore runbook; tested quarterly |

If ClickHouse is fully lost, runs continue (Postgres has the transactional record) but analytics dashboards degrade. This is an intentional design property: **the OLAP store is rebuildable**, the OLTP store is not.

---

## 12. What we do NOT deploy

- Multi-region active-active. Single-region for v1; DR is restore-from-backup.
- Service mesh (Linkerd/Istio). cert-manager + NetworkPolicy is sufficient.
- External Kafka. NATS JetStream is in-cluster.
- Managed Postgres (RDS) by default. CloudNativePG is the chart default; users can disable and point at RDS via values.
