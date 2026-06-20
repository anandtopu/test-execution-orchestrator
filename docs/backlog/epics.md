# TEO — Epics (3-month v1.0)

**Status:** v1.0.0 shipped (tag `v1.0.0`, 2026-06-11). All 16 epics complete at v1.0 scope.
**Last updated:** 2026-06-20
**Implementation progress:** see [`progress.md`](../../progress.md) for the live dashboard.

Sixteen epics organized into four three-week phases. The first six weeks deliver the core platform end-to-end (skeleton run by week 3, live UI by week 6); weeks 7-9 harden result fidelity and ship GitHub integration; weeks 10-12 deliver the differentiating epics (ML predictor, Karpenter, additional adapters, auto-quarantine, owner digest) and the production release.

Each epic links to its stories in `stories.md` and to FR IDs in `requirements/functional.md`.

Status legend: ✅ done · 🟡 scaffolded with named follow-up · ⏳ pending · 📦 deferred (post-v1.0).

| ID | Epic | Status | FRs covered | Phase | Owner role |
|---|---|---|---|---|---|
| **E-01** | Foundation: monorepo, CI, base services | ✅ | NFR-MAINT-* | 1 | Backend lead |
| **E-02** | Postgres + ClickHouse schema + migrations | ✅ | FR-1002 | 1 | Backend |
| **E-03** | API gateway + auth + audit log | ✅ | FR-101..105, FR-801..805 | 1 | Backend |
| **E-04** | Run Manager state machine + leader election | ✅ | FR-101..105, NFR-AVAIL-302 | 2 | Backend lead |
| **E-05** | Scheduler (LPT) + heuristic predictor | ✅ | FR-301..306, FR-305 | 2 | Backend |
| **E-06** | Worker agent + pytest adapter + redactor | ✅ | FR-201..204, FR-401..408 | 2 | Backend |
| **E-07** | Result pipeline (OTLP, failure clustering) | ✅ | FR-501..505, FR-502 | 3 | Backend |
| **E-08** | Flake detection (Wilson-interval pipeline) | 🟡 | FR-601..606 | 3 | Backend |
| **E-09** | Web UI (Next.js + GraphQL) | ✅ | FR-701..706 | 2-3 | Frontend |
| **E-10** | GitHub App integration + Checks API | ✅ | FR-901..904 | 3 | Backend |
| **E-11** | Helm chart + observability + release pipeline | ✅ | FR-1001..1005, NFR-OBS-* | 1, 4 | Platform |
| **E-12** | **ML predictor (Python, LightGBM GBT)** | ✅ | FR-607 | 4 | Backend |
| **E-13** | **Karpenter + spot-aware scheduling + checkpointing** | ✅ | FR-308, NFR cost | 4 | Platform |
| **E-14** | **Additional runner adapters (`go test`, Jest)** | ✅ | FR-106 (partial) | 4 | Backend |
| **E-15** | **Auto-quarantine workflow + auto-issue creation** | ✅ | FR-605, FR-609 | 4 | Backend |
| **E-16** | **Owner digest (weekly Slack/email)** | ✅ | FR-708 | 4 | Backend |

**Bold rows are restored from the 1-month deferred list** (see ADR-0012 revised, ADR-0019, ADR-0020).

### Follow-ups remaining (post-v1.0)

All 16 epics shipped at v1.0 scope. Two named follow-ups remain open — neither blocked the release:

- **S-08-03 — operator-initiated manual quarantine (E-08).** 🟡 Functional gap. Auto-quarantine (E-15) and the scheduler non-blocking lane are done, but there is no operator `quarantine`/`unquarantine` GraphQL mutation (T-08-03-01) and no UI button/modal (T-08-03-03). This is the one v1.0-backlog item `progress.md` does not track as a gap. **Tracked for v1.1** — see the S-08-03 status note in [`stories.md`](stories.md#s-08-03-operator-can-quarantine-a-flaky-test).
- **S-06-03 — kill-mid-test integration test (E-06).** 🟡 Test-debt only. The SIGTERM/graceful-cancel handler is implemented (`cmd/worker/main.go`); the full kill-worker-mid-test integration test is still to be written.

Deferred-by-decision items (WebSocket subscriptions → v1.1, Jest AST fingerprint → v1.5, plus the ADR-0012 📦 list) are not counted here.

---

## Phase 1 — Foundation (Weeks 1-3)

**Goal:** A pytest run can be submitted, persisted, planned, dispatched to one worker pod, and reported back end-to-end on a developer kind cluster. No UI yet beyond placeholder.

| Week | Focus |
|---|---|
| **1** | Repo bootstrap (E-01); Postgres + ClickHouse migrations 001 (E-02); Helm chart skeleton on kind (E-11 partial) |
| **2** | API gateway with `POST /runs`, OIDC, API keys, audit log (E-03); ADR-0014 implementation (E-03) |
| **3** | Run Manager state machine + leader election (E-04); LPT scheduler + heuristic predictor (E-05) |

**Demo at end of week 3:** `teo run --runner pytest .` → pytest discovery → API → Run Manager → Scheduler → one Worker pod → results in Postgres + ClickHouse. No UI, but `psql` + Grafana show the data.

## Phase 2 — Core run path (Weeks 4-6)

**Goal:** Multi-shard pytest run with reliable workers and a working live UI.

| Week | Focus |
|---|---|
| **4** | Worker agent reliability: heartbeat, log streaming, redaction, retries (E-06); UI: run list page (E-09) |
| **5** | Worker scaling to N shards; pytest adapter polish; UI: run detail with Gantt timeline (E-09) |
| **6** | Result pipeline OTLP intake (E-07 partial); UI: live updates via subscription (E-09) |

**Demo at end of week 6:** A 1,000-test pytest suite runs across 10 shards, results stream into the UI in real time.

## Phase 3 — Result fidelity + GitHub (Weeks 7-9)

**Goal:** OTLP completes, failures cluster, flake detection works, GitHub Check Runs deep-link into TEO.

| Week | Focus |
|---|---|
| **7** | Failure clustering by stack-trace fingerprint (E-07); test detail UI with sparkline + log tail (E-09) |
| **8** | Wilson-interval flake detection job (E-08); manual quarantine UI + scheduler non-blocking lane (E-08) |
| **9** | GitHub App + webhooks + Check Runs with summary + deep links (E-10); JUnit/OTLP exports (E-07) |

**Demo at end of week 9:** Push to a PR → Check Run goes live → drill from Check Run into TEO → see failure cluster → see flake history → manually quarantine.

## Phase 4 — Differentiation + release (Weeks 10-12)

**Goal:** Ship the differentiating epics and harden everything for v1.0.0 release.

| Week | Focus |
|---|---|
| **10** | ML predictor service in Python + nightly training (E-12); `go test` adapter (E-14); Karpenter NodePool config + spot subchart (E-13) |
| **11** | Worker IMDS poller + interruption handling (E-13); Jest adapter (E-14); auto-quarantine workflow + GitHub Issue creation (E-15) |
| **12** | Owner digest emails / Slack (E-16); Helm chart hardening, prod values, Grafana dashboards, alert rules, restore drill, release pipeline (E-11) → tag **v1.0.0** |

**Demo at end of week 12:** Greenfield kind/EKS cluster → `helm install teo` → fully working stack with sample pytest + go test + Jest repo. ML predictor model trained from synthetic history is making predictions. A Spot kill in EKS does not lose tests.

---

## Risks per epic

| Epic | Risk | Mitigation |
|---|---|---|
| E-04 | Leader election bugs cause split-brain | Postgres advisory locks (ADR-0013) + 2-replica chaos test |
| E-05 | LPT bug → unbalanced shards | Property tests on the makespan ratio bound (4/3) |
| E-06 | pytest discovery non-determinism across plugins | Pin pytest version in worker image; integration tests on canonical sample repos |
| E-07 | OTel SDK churn in pytest | Ship our own thin adapter; do not depend on `pytest-opentelemetry` |
| E-08 | Wilson interval over too-small samples → noise | `min_samples` config + UI label "insufficient data" |
| E-09 | UI complexity exceeds budget | Vertical slicing: list → detail → live → cluster page |
| E-10 | GitHub App approval flow gnarly for first install | Document bot-account flow with example app manifest |
| E-11 | Helm chart correctness across k8s versions | Chart-testing CI on kind 1.29 + 1.30 |
| **E-12** | **Predictor MAE worse than heuristic baseline** | **Champion/challenger gating; auto-fallback to heuristic on bad metrics (ADR-0019)** |
| **E-13** | **Spot preemption storms cause runaway retries** | **Preempted retries route to on-demand pool by default (ADR-0020); preemption-count metric + alert** |
| **E-14** | **Adapter quirks per runner blow up scope** | **Define a runner-adapter SPI early; one engineer per adapter; refuse non-essential per-runner features** |
| **E-15** | **Auto-quarantine masks a real bug** | **`broken` vs `flaky` distinction (PRD §11): a quarantined test that starts failing 100% of the time is escalated, not silenced** |
| **E-16** | **Digest spam erodes trust** | **Opt-out per repo + per user; default digest is weekly, not daily** |

---

## Out of scope for v1.0 (revisit at end of phase 4)

Each of these has its own future epic, sequenced after v1.0 release. See ADR-0012 for the formal cut.

- E-17: Additional runner adapters (RSpec, JUnit-direct, Bazel)
- E-18: Speculative re-execution of straggler tests
- E-19: "What-if" simulator
- E-20: Cost-budgeted execution mode
- E-21: LLM root-cause hints
- E-22: Causal flake categorization v2 (beyond coarse heuristic)
- E-23: Cross-repo test impact analysis
- E-24: Multi-cloud worker pool (GCP, Azure)
- E-25: SOC 2 Type 1 readiness work
