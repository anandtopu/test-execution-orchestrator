# ADR-0012: v1.0 scope for 3-month delivery (revised)

**Status:** Accepted — supersedes 1-month scope cut after timeline confirmed at 3 months
**Date:** 2026-04-30
**Revised:** 2026-04-30 (timeline change from 1 → 3 months, 3-4 engineers)

## Context
The PRD §12 lays out a 32-week roadmap (Phases 0–3, plus v1.5). The user has set a **3-month delivery target with 3–4 engineers**. With ~192 person-days available (4 engineers × 12 weeks × 80% utilization), substantially more than a minimum-viable scope fits.

## Decision
v1.0 (3-month) delivers:

### Core platform (always-in)
- Postgres + ClickHouse + S3 storage tiers
- API gateway (gRPC + REST + GraphQL), OIDC humans + API-key machines, three-role RBAC, audit log
- Run Manager with HA leader election
- LPT scheduler with **history-based heuristic predictor** (cold-start fallback)
- Worker agent with heartbeats, log streaming, redaction
- OTLP-native result pipeline; failure clustering by stack-trace fingerprint
- Statistical (Wilson-interval) flake detection
- Web UI: run list, run timeline, failure clusters, test detail with flake history
- GitHub App + GitHub Checks integration
- Helm chart for self-hosted deployment, with bundled Dex (no-op default IdP)
- OTel dogfood, Grafana dashboards, Prometheus alerts
- Release pipeline with cosign signing + SBOM + multi-arch images

### Restored to scope (vs. the 1-month cut)
- **ML predictor** — LightGBM GBT model in a Python service behind the same gRPC contract as the heuristic predictor. Trained nightly on ClickHouse history (test duration features, code-churn features, failure history). Falls back to heuristic on MAE drift. See ADR-0019.
- **Karpenter + spot-aware scheduling + worker checkpointing** — replaces cluster-autoscaler. Workers receive AWS Spot interruption notices via IMDS; in-flight tests checkpoint progress and reschedule. Brings PRD goal #3 (≥30% cost reduction) back into scope. See ADR-0020.
- **`go test` + Jest runner adapters** — alongside pytest. Three runners total at v1.0.
- **Auto-quarantine workflow + auto-issue creation** — confirmed flakes (Wilson-confirmed) auto-quarantine; a GitHub Issue is opened, assigned via CODEOWNERS, with stale-after-N-days SLA. Operator can opt out per-repo.
- **Owner digest** — weekly per-author email/Slack: tests owned, flake count, CI minutes consumed, slowest tests.

### Still deferred to v1.5+
- "What-if" simulator
- Cost-budgeted execution mode
- LLM-assisted root-cause hints
- Multi-cloud (AWS-only at v1.0)
- RSpec / JUnit-direct / Bazel runner adapters
- Speculative re-execution of straggler tests
- Causal flake categorization beyond a coarse heuristic
- Cross-repo test impact analysis
- SOC 2 Type 1 (post-GA)
- Asymmetric (RS256) JWT signing — HS256 is sufficient for v1.0 single-deployment scope

## Consequences

**+** PRD goal **#1 (≥40% wall-clock reduction)** achievable from LPT alone, improved by ML predictor for cold-start and short-history repos.
**+** PRD goal **#2 (<1% false-positive flake quarantine)** achievable from Wilson + categorization heuristic.
**+** PRD goal **#3 (≥30% cost reduction)** **back in scope** with Karpenter + spot.
**+** PRD goal **#5 (5 runner adapters)** **partial** — 3 of 6 (pytest, go test, Jest); RSpec/JUnit-direct/Bazel deferred.
**+** Owner digest + auto-quarantine deliver the "feedback loop" theme from PRD §6 (#10).

**−** Python in the runtime (predictor) is a second language to operate. We accept it for the model maturity benefit. The Go heuristic predictor remains the fallback so the system functions without the Python service.
**−** Karpenter integration adds operational complexity to the chart; we mitigate via documented opinionated defaults.

## Estimation reconciliation
Total task-days expanded from ~96 (1-month cut) to ~149 (3-month cut). Detailed breakdown in [`backlog/tasks.md`](../backlog/tasks.md). 192 person-days available → ~43 days of slack covers risk on the ML and Karpenter epics.

## Alternatives considered
- **Stay with the 1-month cut and use the extra time for hardening and adoption work.** Rejected: differentiation suffers; we'd ship a competent but unspecial v1.0.
- **Add the what-if simulator instead of the owner digest.** Rejected: simulator depends on the ML predictor being well-calibrated, which is a v1.5 risk we should not stack onto v1.0.
- **Add RSpec instead of Jest.** Rejected: Jest community is materially larger and the runner protocol is simpler.
