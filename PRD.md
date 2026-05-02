# Product Requirements Document: Test Execution Orchestrator (TEO)

**Version:** 0.1 (Draft)
**Date:** 2026-04-30
**Status:** Proposal / Research-backed Draft

---

## 1. Executive Summary

The Test Execution Orchestrator (TEO) is a language- and CI-agnostic control plane that ingests a test suite, schedules its execution across an elastic worker pool, aggregates results into a single source of truth, and continuously learns which tests are flaky, slow, or low-signal. TEO targets engineering teams whose CI wall-clock has outgrown a single runner — typically 500+ tests, >5 minute pipelines, or multi-repo monorepos — and who currently rely on hand-rolled matrix sharding, spreadsheet-tracked flakes, and "rerun until green" culture.

The product's core wedge is a **prediction-driven scheduler**: instead of splitting tests into N equal-count shards, TEO uses historical duration, failure-correlation, and code-change signals to produce shards of equal *predicted wall-clock* and to run the tests most likely to fail *first*. This is paired with **trace-native result aggregation** (OpenTelemetry spans rather than JUnit XML) and a **statistical flaky-test pipeline** that quarantines flakes automatically with bounded false-positive rates.

---

## 2. Problem Statement

### 2.1 Pain points observed in the market

| Pain | Evidence |
|---|---|
| CI wall-clock dominated by the slowest shard | Naive count-based sharding leaves fast shards idle while one shard finishes a 12-minute integration test |
| Flakiness erodes trust and burns money | Google reports ~73K flaky failures/day out of 1.6M test runs (~4.6%); teams rerun blindly |
| Result data is fragmented | JUnit XML per shard, console logs in artifacts, screenshots in S3, traces nowhere — no unified failure view |
| Compute waste | Workers idle between jobs; spot interruptions kill long tests with no checkpointing; on-demand fleets sized for peak |
| No feedback loop | Test ownership, duration trends, and flake rates are not surfaced back to the authors who can fix them |

### 2.2 Who feels it

- **Platform/DevEx engineers** owning CI runtime and budget.
- **Tech leads** triaging "is the build red because of my change or a flake?"
- **EMs** trying to reduce PR cycle time without expanding the cloud bill.

### 2.3 Why now

- Monorepos and microservice fan-out have made matrix sharding unmanageable.
- Spot/preemptible compute is mature enough to halve CI cost — *if* the orchestrator handles interruption.
- OpenTelemetry has become the de-facto wire format for distributed observability, opening a path beyond JUnit XML.
- Predictive test selection (Meta) and ML-driven test subsetting (Launchable) have proven the ROI of learning from test history; the techniques are now well-documented and reproducible.

---

## 3. Goals & Non-Goals

### 3.1 Goals (v1.0)

1. Reduce 95th-percentile CI wall-clock by **≥40%** vs. naive count-based sharding on the same hardware budget.
2. Auto-detect and quarantine flaky tests with **<1% false-positive rate** (a stable test wrongly quarantined) measured against a labeled holdout set.
3. Provide a single, queryable "test result graph" that survives shard failures, worker preemption, and partial reruns.
4. Cut compute spend by **≥30%** vs. on-demand-only fleets via spot-aware scheduling and idle-worker reclaim.
5. First-class plug-ins for JUnit, pytest, Go test, Jest, RSpec, and Bazel test runner — no source-code modification required to onboard.

### 3.2 Non-Goals (v1.0)

- Authoring or generating tests (no LLM test-writing in v1).
- Replacing CI providers (GitHub Actions, Buildkite, Jenkins, GitLab CI). TEO runs *as a step* inside them.
- Build-system caching à la Bazel Remote Cache. (We integrate, we don't replace.)
- Production observability. TEO is for pre-prod test runs.

---

## 4. Personas

| Persona | Goal | Success metric |
|---|---|---|
| **Priya, Platform Eng** | Stable, fast, cheap CI | p95 pipeline time, $/build, % red builds caused by infra |
| **Marco, Senior IC** | Get my PR merged today | Time from push → merge-ready signal, signal trustworthiness |
| **Devi, EM** | Predictable team velocity | Flake rate trend, deploy frequency, CI-induced rollbacks |
| **Sam, SRE-on-call** | Diagnose pipeline outages fast | Mean time to root cause for a red build |

---

## 5. Core Features

### 5.1 Parallel Test Execution

**What:** Distribute a test run across N workers such that wall-clock time is minimized within a worker-count or budget cap.

**Capabilities:**
- **Adaptive sharding by predicted duration.** Tests are partitioned via a Longest-Processing-Time-first (LPT) bin-packing heuristic over predicted durations, not test count. (LPT is provably within 4/3 of optimal for makespan; the standard textbook result.)
- **Critical-path-first ordering.** Within a shard, the longest tests start first so any single overrun does not delay the whole shard's last assignment.
- **Speculative re-execution.** When a worker is idle and another shard's tail is still running, TEO can speculatively re-launch that tail's last test on the idle worker; first-to-finish wins. Inspired by MapReduce stragglers.
- **Dependency-aware scheduling.** Tests can declare "needs-postgres," "exclusive-port-5432," or arbitrary resource tags; the scheduler treats these as constraints rather than hardcoding test types.
- **Spot-interruption resilience.** Each worker streams progress. On preemption, the test in flight is rescheduled on another worker; completed-and-reported tests are not re-run.
- **Partial reruns.** Authors can request "rerun only the failed and quarantined tests from build #12345" without re-executing the full suite.

**Non-feature:** TEO does not modify test code to make it parallel-safe. Tests with shared state must be tagged or fail loudly.

### 5.2 Result Aggregation

**What:** Capture every test outcome from every worker into a single, queryable, traceable record.

**Capabilities:**
- **OpenTelemetry-native ingestion.** Each test execution is a span; suite is a trace. Stdout/stderr, screenshots, and custom attributes attach as span events/links. JUnit XML is a supported *input adapter* but not the source of truth.
- **Streaming rather than end-of-run upload.** Results stream as they finish, so the UI updates live and a worker dying does not lose its prior results.
- **Stable test identity.** A test is identified by `(repo, file path, fully qualified name, parameter set)` and assigned a content-addressed fingerprint. This survives renames better than naive name-matching by also tracking AST-level identity.
- **Failure deduplication.** Identical stack traces across shards or reruns collapse into a single "failure cluster" with N occurrences, so triage isn't drowned in copies of the same error.
- **Diff-aware rerun comparison.** "Show me tests that pass now but failed in the last run on main, scoped to files I touched."
- **Exports:** JUnit XML, TestAnything Protocol, OTLP, and a GraphQL query API. Native integrations with GitHub Checks, Slack, and Linear.

### 5.3 Flaky Test Detection

**What:** Statistically classify each test as `stable | flaky | broken | new` and act on it without human intervention.

**Capabilities:**
- **Rerun-budgeted detection.** On a failure, TEO can rerun the test up to K times *on a different worker* with a different RNG seed and a clean filesystem. If outcomes diverge, it is flagged flaky-candidate.
- **Statistical confirmation.** A test is promoted from flaky-candidate to flaky only after observing a Wilson-confidence-interval lower bound > threshold on flake rate across ≥M independent runs. This bounds false positives.
- **ML-assisted prediction (optional).** A gradient-boosted-tree model trained on (test history, code-churn, duration variance, failure-message clustering) predicts which *new or recently-changed* tests are likely flaky before enough history exists. Feature design draws on published work (FlaKat, FlakyFix, Meta's Predictive Test Selection).
- **Root-cause categorization.** Flakes are classified into common causes — order-dependent, async/timing, network, resource leak, randomness, environment-dependent — using heuristics over span attributes (e.g., presence of `sleep`, network calls, `time.now`, shared fixtures).
- **Quarantine workflow.** Confirmed flakes are auto-moved to a "quarantine lane" that runs in parallel but does not block PR merge. A GitHub issue is opened, assigned to the test's `CODEOWNERS`, with a stale-after-N-days SLA.
- **Un-quarantine.** If a quarantined test passes K times in a row across diverse environments, TEO proposes restoring it.

---

## 6. Differentiating / Innovative Ideas

The features below are where TEO can leapfrog the existing tools. Each is grounded in cited research.

| # | Idea | Source / Inspiration | Why it matters |
|---|---|---|---|
| 1 | **Predicted-duration sharding (LPT) instead of count-based** | Bin-packing / LPT scheduling literature | Eliminates the long-tail-shard-blocks-everyone failure mode that plagues GitHub Actions matrix users |
| 2 | **Trace-native result model (OTel)** | Dynatrace JUnit-OTel extension; trace-based testing in the OpenTelemetry Demo | Unifies test results with the same observability stack the prod system uses; enables span-level flake root-cause |
| 3 | **Failure-first ordering** | Meta's Predictive Test Selection (gradient-boosted tree on code churn + history) | Surface red signal in the first 30 seconds of CI rather than minute 12 |
| 4 | **Speculative execution of straggler tests** | MapReduce / Spark straggler mitigation | Hides p99 test latency when worker noise is high (cloud, shared CI) |
| 5 | **Spot-aware checkpointed scheduling** | Karpenter + PDB patterns | Cuts cloud spend ~70% on spot vs. on-demand without losing work to interruptions |
| 6 | **Causal flake categorization, not just detection** | FlaKat (order-dependent vs. impl-dependent taxonomy) | Tells the developer *why* it's flaky, not just *that* it's flaky — drastically cuts time-to-fix |
| 7 | **Wilson-interval flake confirmation** | Standard binomial confidence-interval statistics | Bounded false-positive quarantine rate; gives the system a defensible "we're 95% sure this is flaky" claim |
| 8 | **Failure clustering by stack-trace fingerprint** | Inspired by Sentry-style error grouping | A 50-shard suite with one bug shows as one failure, not 50 |
| 9 | **LLM-assisted root cause hint (opt-in, post-MVP)** | FlakyFix paper | One-shot suggestion attached to the failure; never auto-merged. Off by default to stay deterministic |
| 10 | **Test ownership feedback loop** | Novel — under-served gap | Weekly digest to each author: "your tests cost the team 14 minutes of CI this week, top offender is X" — turns flakiness into a graphable, owned problem |
| 11 | **"What-if" scheduling simulator** | Novel | Engineers can replay yesterday's run with different worker counts/types and see the cost/time Pareto curve before changing config |
| 12 | **Cost-budgeted execution mode** | Novel | "Spend at most $0.40 on this PR's test run" — scheduler chooses spot/on-demand mix and may *defer* slow low-value tests to a nightly suite, surfacing the trade-off transparently |

Of these, **#1, #3, and #6** are the highest-leverage user-visible wins; **#5 and #12** are the strongest budget-side wins; **#11** is the most defensible product moat once we have the simulator.

---

## 7. System Architecture (high level)

```
┌─────────────┐      ┌───────────────────────────────────┐      ┌────────────┐
│ CI provider │─────▶│ TEO API (gRPC + REST)             │◀────▶│ Postgres   │
│ (GH/GL/BK)  │      │  • run intake                     │      │ + ClickHouse│
└─────────────┘      │  • auth / RBAC                    │      └────────────┘
                     └────────────┬──────────────────────┘
                                  │
                  ┌───────────────┴────────────────┐
                  ▼                                ▼
          ┌──────────────┐                ┌────────────────┐
          │  Scheduler   │                │ Result Pipeline│
          │  • predict   │                │ • OTLP intake  │
          │  • LPT pack  │                │ • dedup/cluster│
          │  • dispatch  │                │ • flake classify│
          └──────┬───────┘                └────────┬───────┘
                 │                                 │
                 ▼                                 ▼
       ┌──────────────────┐               ┌──────────────────┐
       │ Worker Pool      │──reports────▶│  Object store    │
       │ (k8s + Karpenter,│               │  (logs, screens) │
       │  spot + on-dem.) │               └──────────────────┘
       └──────────────────┘
```

**Key components:**
1. **API gateway** — accepts a `RunRequest` (manifest of tests + suite metadata + budget hints) from CI.
2. **Predictor service** — gradient-boosted-tree model serving p50/p95 duration estimates + flake-probability per test. Trained nightly from ClickHouse history. Cold-start fallback: per-file mean duration.
3. **Scheduler** — pure function `(tests, predictions, worker_pool, constraints) → assignment plan`. Bin-packs by duration, prioritizes high-flake-probability tests early, respects exclusivity tags.
4. **Worker agent** — runs inside a k8s pod (or VM); pulls assignments, executes the native test runner (pytest, go test, etc.) via a thin adapter, streams OTLP spans to the result pipeline. Heartbeats every 5s.
5. **Result pipeline** — OTLP collector → enrichment (attach repo, branch, runner-image hash) → ClickHouse (hot 30-day) + S3 (cold archive). Failure clustering and flake-classification jobs run on this stream.
6. **Web UI** — run timeline (Gantt over workers), failure clusters, flake dashboard, "what-if" simulator, owner digest.

---

## 8. User Experience (key flows)

1. **Onboarding (target: <30 min).**
   - Install GitHub App / GitLab integration → grant repo access.
   - Add one CI step: `teo run --suite pytest`.
   - First 3 runs are observation-only (collect timing data); from run 4 the scheduler activates.
2. **PR run.** Author pushes; PR check shows live shard status; failures bubble up with stack-trace fingerprint, prior-occurrence count, and quarantine status. Click → trace view.
3. **Flake report.** When a test crosses the flake threshold, an issue is opened with: failure-cluster snapshot, suspected category, last 20 runs visualized, suggested CODEOWNERS.
4. **Weekly owner digest.** Email/Slack: "You own 7 tests. 1 is flaky. Your tests cost 14 min CI this week (down 8% WoW). Slowest: `test_e2e_checkout` (avg 47s)."
5. **Platform admin.** Cost dashboard, fleet utilization, scheduler-vs-baseline counterfactual savings, "what-if" simulator.

---

## 9. Data Model (essentials)

- **Run** — a single TEO invocation. Has `id`, `repo`, `commit`, `branch`, `triggered_by`, `budget`, `started_at`, `finished_at`, `status`.
- **Shard** — a worker assignment within a run. `id`, `run_id`, `worker_id`, `tests[]`, `predicted_duration`, `actual_duration`.
- **TestExecution** — single test run. `id`, `shard_id`, `test_fingerprint`, `outcome`, `duration_ms`, `attempt_n`, `otel_trace_id`.
- **Test (logical)** — `fingerprint`, `repo`, `path`, `name`, `params`, `owner_team`, `tags[]`, `status (active|quarantined|deleted)`.
- **FailureCluster** — `id`, `stack_fingerprint`, `first_seen`, `last_seen`, `occurrences`, `affected_runs[]`.
- **FlakeRecord** — `test_id`, `wilson_lower_bound`, `category`, `quarantined_at`, `evidence_runs[]`.

---

## 10. Metrics & Success Criteria

| Metric | Target (v1.0) | How measured |
|---|---|---|
| p95 wall-clock reduction vs naive sharding | ≥40% | A/B comparing scheduler ON vs OFF on identical commits |
| Flake quarantine false-positive rate | <1% | Manual labeling of 200 quarantined tests / quarter |
| CI infra cost reduction | ≥30% | $/build before vs after, normalized per test-second |
| Onboarding time | <30 min p50 | Self-reported + telemetry from first-run-to-fourth-run latency |
| % of failures with auto-attached root-cause hint | ≥60% | Span-attribute heuristics coverage on failure dataset |
| Adoption: weekly active runs / customer | ≥500 by month 3 | Product analytics |

---

## 11. Risks & Open Questions

| Risk | Mitigation |
|---|---|
| Cold-start: scheduler has no history for a new repo | Fall back to count-based sharding for the first N runs; clearly label "learning mode" in UI |
| ML predictor drift on rapidly-changing codebases | Online updates daily; track prediction error and disable predictor if MAE exceeds threshold |
| Trace volume cost (OTel spans for every test) | Tail sampling for green tests after aggregation; full retention only for failures and 1% of green |
| Customer security posture (test logs may contain secrets) | Self-hosted control plane option; redaction pipeline; SOC2 from day one |
| Mis-quarantined test masks a real bug | Quarantine still runs the test in non-blocking mode; if a quarantined test starts failing 100% of the time, escalate as `broken`, not `flaky` |
| Lock-in fear | All data exportable as JUnit XML + OTLP; scheduler decisions explainable via per-run JSON plan |

**Open questions:**
1. Do we ship a single binary self-hosted offering on day one, or SaaS-only? (Lean: SaaS first, self-hosted at GA.)
2. Per-test pricing vs per-worker-minute pricing? Per-test aligns with value but is alien to platform buyers.
3. Should the predictor use code embeddings (code2vec / AST) in v1, or only history-based features? (Lean: history-only in v1; embeddings in v1.5 once we have a labeled dataset.)
4. Where do we draw the line between "test orchestrator" and "build orchestrator"? Bazel users will ask.

---

## 12. Phased Roadmap

**Phase 0 — Spike (4 weeks)**
- Worker agent for pytest + Go test.
- LPT scheduler with synthetic duration data.
- Single-tenant demo: one repo, manual config, ClickHouse + Postgres.

**Phase 1 — Alpha (10 weeks)**
- Predictor service with history-based gradient-boosted-tree model.
- OTLP-native result pipeline.
- Failure clustering.
- Statistical (Wilson interval) flake detection.
- GitHub App + GitHub Checks integration.
- 3 design partners.

**Phase 2 — Beta (10 weeks)**
- Spot-aware worker fleet + checkpointing.
- Quarantine workflow + auto-issue creation.
- JUnit / Jest / RSpec / Bazel adapters.
- Owner digest.
- Self-serve onboarding.

**Phase 3 — GA (8 weeks)**
- "What-if" simulator.
- Cost-budgeted execution mode.
- Causal flake categorization v1.
- SOC2 Type 1.
- Self-hosted reference deployment (Helm chart).

**Post-GA / v1.5**
- LLM root-cause hints (opt-in).
- Code-embedding features in predictor.
- Cross-repo test impact analysis.

---

## 13. Appendix: Research References

- Meta — *Predictive Test Selection*, ICSE 2019. Gradient-boosted-tree model; ~50% infra cost reduction at >95% failure recall.
- *FlaKat: A Machine Learning-Based Categorization Framework for Flaky Tests* — arXiv 2403.01003.
- *FlakyFix: Using LLMs for Predicting Flaky Test Fix Categories and Test Code Repair* — arXiv 2307.00012.
- *Empirically evaluating flaky test detection techniques combining test case rerunning and machine learning* — Empirical Software Engineering, 2023.
- Atlassian Engineering — *Taming Test Flakiness: How We Built a Scalable Tool to Detect and Manage Flaky Tests*.
- EngFlow Documentation — Test sharding, scheduler, and remote execution model.
- Launchable / CloudBees — ML-driven dynamic test subsetting.
- Dynatrace — *JUnit Jupiter OpenTelemetry Extension*; trace-based test instrumentation.
- OpenTelemetry — *Trace-Based Testing in the OpenTelemetry Demo*.
- *An Application of Bin-Packing to Multiprocessor Scheduling*, SIAM Journal on Computing — LPT heuristic foundation.
- Kubernetes Cluster Autoscaler + Karpenter — patterns for spot-instance CI workloads.
