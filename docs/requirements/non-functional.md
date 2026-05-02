# TEO — Non-Functional Requirements (NFR)

**Status:** Draft
**Date:** 2026-04-30 (revised, scope per ADR-0012)
**Scope:** 3-month v1.0. Aggressive targets are flagged.
**Implementation status:** see [`progress.md`](../../progress.md) for which NFRs are verified vs. pending integration tests. As of 2026-04-30, NFR coverage is unit-test-level only; load-test verification (NFR-PERF-103/104/105) is pending the testcontainers integration job.

---

## 1. Performance / SLOs

| ID | Requirement | Measurement |
|---|---|---|
| NFR-PERF-101 | API gateway p95 latency < 250ms for read endpoints | Prometheus histogram per endpoint |
| NFR-PERF-102 | API gateway p99 latency < 1s | Prometheus |
| NFR-PERF-103 | Run creation (POST /runs) p95 < 500ms for manifests up to 5,000 tests | Load test in CI |
| NFR-PERF-104 | Scheduler decision latency p95 < 2s for 5,000 tests across 50 shards | Unit benchmark |
| NFR-PERF-105 | OTLP ingest sustained 5,000 spans/sec per result-pipeline replica | Load test |
| NFR-PERF-106 | UI initial page render < 2s on a run with up to 100 shards | Lighthouse + manual |
| NFR-PERF-107 | UI live updates: a worker reporting a test finished is reflected in the UI within 3s | Manual + e2e |

## 2. Scalability

| ID | Requirement |
|---|---|
| NFR-SCALE-201 | Support 100 concurrent runs and 500 concurrent worker pods on a single control-plane deployment |
| NFR-SCALE-202 | Support 1M `test_executions` rows per day in Postgres without index-induced write degradation |
| NFR-SCALE-203 | Support 10M `span_events` rows per day in ClickHouse |
| NFR-SCALE-204 | Horizontal scaling of API and result-pipeline by replica count; no shared state in-process |

## 3. Availability

| ID | Requirement |
|---|---|
| NFR-AVAIL-301 | Control-plane services run with ≥ 2 replicas; rolling restarts cause zero failed runs |
| NFR-AVAIL-302 | Run Manager uses leader-elected high-availability; failover < 10s |
| NFR-AVAIL-303 | Postgres in HA primary+1 (CloudNativePG); failover < 30s |
| NFR-AVAIL-304 | ClickHouse single-shard, single-replica is acceptable for v1 (loss is rebuildable from Postgres) |
| NFR-AVAIL-305 | Control plane SLO: 99.5% monthly availability (excluding planned maintenance) |

## 4. Reliability

| ID | Requirement |
|---|---|
| NFR-REL-401 | All worker→control-plane RPCs are idempotent on a documented key |
| NFR-REL-402 | The scheduler is deterministic; replaying a saved plan with the same inputs yields the same shards |
| NFR-REL-403 | A test execution recorded in Postgres survives loss of ClickHouse and is recoverable |
| NFR-REL-404 | A worker crash mid-test results in the test being rescheduled, not lost |
| NFR-REL-405 | NATS JetStream uses 3-replica streams; message loss requires 2+ NATS pod failures simultaneously |

## 5. Security

| ID | Requirement |
|---|---|
| NFR-SEC-501 | All inter-service traffic uses mTLS (cert-manager-issued certs) |
| NFR-SEC-502 | Public endpoints serve only TLS 1.2+ |
| NFR-SEC-503 | Postgres passwords, GitHub App private key, JWT signing key are stored only in k8s Secrets |
| NFR-SEC-504 | API keys hashed with argon2id (`memory=64MB, iterations=3, parallelism=1`); plaintext never persisted |
| NFR-SEC-505 | Audit log captures all auth events and mutations; tamper-evident via append-only and external backup |
| NFR-SEC-506 | Test logs are redacted on the worker for known secret patterns (AWS keys, JWTs, generic high-entropy tokens) before transmission |
| NFR-SEC-507 | Container images are signed with cosign; deployment policy enforces signature verification |
| NFR-SEC-508 | Vulnerability scan in CI; HIGH+ blocks release |
| NFR-SEC-509 | Threat model documented for the auth, worker, and result-ingestion paths (see `docs/security/threat-model.md`) `[post-MVP]` |

## 6. Privacy & data handling

| ID | Requirement |
|---|---|
| NFR-PRIV-601 | The operator can configure log retention; default is 30 days hot, 365 days cold |
| NFR-PRIV-602 | A `DELETE /repos/{id}` cascades to all runs, shards, executions, logs |
| NFR-PRIV-603 | The operator can opt out of any external network calls (e.g., dependency telemetry) |

## 7. Observability

| ID | Requirement |
|---|---|
| NFR-OBS-701 | Every service emits OTel spans for every public RPC |
| NFR-OBS-702 | Every service exposes a Prometheus `/metrics` endpoint with: request rate, error rate, duration histogram, in-flight gauge |
| NFR-OBS-703 | Logs are structured JSON; every log line has `service`, `level`, `time`, `trace_id`, `span_id` |
| NFR-OBS-704 | Bundled Grafana dashboards cover: API latency, scheduler decision time, run state machine, ClickHouse insert lag, NATS consumer lag |
| NFR-OBS-705 | Bundled Prometheus alerts: API p95 high, run stuck, ClickHouse lag, NATS lag, replica down |

## 8. Maintainability

| ID | Requirement |
|---|---|
| NFR-MAINT-801 | Code coverage ≥ 70% for critical packages (scheduler, predictor, run manager, result pipeline) |
| NFR-MAINT-802 | Linting (`golangci-lint`) + formatting required in CI; no merge without green check |
| NFR-MAINT-803 | All public packages have package-level docstrings explaining intent |
| NFR-MAINT-804 | Schema changes require an ADR if they alter the public API surface |
| NFR-MAINT-805 | Every external API change is documented in `CHANGELOG.md` |

## 9. Portability

| ID | Requirement |
|---|---|
| NFR-PORT-901 | The Helm chart runs on Kubernetes 1.29+ |
| NFR-PORT-902 | The chart supports both AWS S3 and MinIO for object storage |
| NFR-PORT-903 | The chart supports both AWS-managed Postgres (RDS) and CloudNativePG |
| NFR-PORT-904 | All container images are multi-arch (amd64, arm64) |

## 10. Compliance

| ID | Requirement |
|---|---|
| NFR-COMP-1001 | Apache 2.0 license throughout |
| NFR-COMP-1002 | All transitive dependencies are licensed compatibly with Apache 2.0 (no AGPL) |
| NFR-COMP-1003 | SOC 2 Type 1 — `[deferred to post-MVP]` |
| NFR-COMP-1004 | GDPR — operator-controlled retention + delete is sufficient given self-hosted model |

## 11. Cost (operator-side, informative)

| Aspect | MVP target |
|---|---|
| Control plane footprint at idle | ≤ 4 vCPU, 8 GB RAM total across all services |
| Storage growth at 500 runs/day | < 30 GB/month combined |
| Worker pool — operator-controlled | Cluster-autoscaler scales workers; cost = test count × duration |
