# ADR-0021: LLM-generated root-cause hints for failure clusters

**Status:** Accepted
**Date:** 2026-06-26

## Context
PRD §6 lists "LLM-assisted root-cause hints" as a triage accelerator: for each failure cluster, a short natural-language explanation of the likely cause beside the representative stack trace. ADR-0012 (revised) deferred it out of v1.0 scope (📦). With the v1.0 / v1.1 / v1.5 backlog now closed, it is the first 📦 item pulled forward for v1.x.

The substrate already exists. `teo.failure_clusters` stores `representative_message`, `representative_stack`, and `stack_fingerprint` (`migrations/postgres/001_initial.up.sql`); the GitHub Check finalize summary already renders the top-N clusters (`internal/github/check_observer.go` `buildClusterMarkdown`); the `FailureCluster` GraphQL type and the `/clusters` UI already render per-cluster metadata. What's missing is a generated hint and a place to store it.

The load-bearing concern is **data egress**: a hint requires sending failure messages and stack traces — which may contain accidentally-printed secrets — to a third-party LLM API. ADR-0016 already established that redaction happens on the worker before logs leave the pod; this ADR extends that boundary to cover the hint path.

## Decision

- **Provider / SDK:** Claude via the official Go SDK (`github.com/anthropics/anthropic-sdk-go`). No LLM provider exists in the repo today and the orchestrator is Go, so a single new Apache-2.0 dependency is the smallest footprint. Default model `claude-opus-4-8`, overridable via `TEO_LLM_HINTS_MODEL`.
- **Call shape:** single-shot summarization, not an agent. Adaptive thinking; **structured outputs** (`output_config.format`) so each response is a validated `{category, hint, confidence}` object rather than free text to parse. A stable, prompt-cached system prompt amortizes input tokens across clusters.
- **Batch over synchronous:** the nightly run uses the **Message Batches API** (`client.Messages.Batches`) — the work is non-latency-sensitive, results key by `custom_id`, and the batch tier is materially cheaper. The synchronous Messages API is reserved for a possible future on-demand "explain this cluster" button (not in this ADR).
- **Redaction before egress (non-negotiable):** `internal/redact` — the same redactor the worker applies to logs — scrubs `representative_message` and `representative_stack` **before** the API call. Secrets never reach the LLM provider, consistent with ADR-0016.
- **Opt-in, default off:** the feature is gated on `TEO_LLM_HINTS_ENABLED` (default off) plus `ANTHROPIC_API_KEY`. An operator who does not want failure data leaving their environment does nothing and gets exactly today's behavior.
- **Graceful degradation:** any error (disabled, missing key, API failure, decode/length mismatch, refusal) yields **no hint** — never a blocked run, never a partial write. Every display surface renders an em-dash when the hint is null. There is no fallback "fake" hint; absence is a first-class state. This mirrors the predictor's fallback property in ADR-0019 (the system runs without the LLM).
- **Storage:** migration `007_failure_cluster_hint` adds nullable `root_cause_hint TEXT`, `hint_category TEXT`, `hint_confidence REAL`, `hint_generated_at TIMESTAMPTZ` to `teo.failure_clusters` (forward-only, paired down).
- **Job:** new `internal/llmhints` package (a `client.go` mirroring `internal/predictor/mlclient.go` seams for offline unit-testing, plus a `job.go` `Backfiller`-style runner) wired as `result-pipeline llm-hints [--restale] [--dry-run]` and a default-off Helm CronJob — mirroring `backfill-clusters`. **Idempotent:** only clusters with `root_cause_hint IS NULL` are summarized (`--restale` re-summarizes after a prompt change), so re-runs do not re-bill.
- **Display surfaces (all additive, all null-safe):** `buildClusterMarkdown` renders the hint under each cluster's message; the `FailureCluster` GraphQL type exposes `rootCauseHint`/`hintCategory`/`hintConfidence` via `mapResolve`; the `/clusters` UI detail pane renders it through the existing `teo-adapt.ts` adapter.
- **Metrics:** `teo_llm_hints_generated_total` / `teo_llm_hints_failures_total` in `internal/metrics`.

## Consequences

**+** Triage starts from a plain-language hypothesis instead of a raw stack trace; the GitHub Check summary becomes self-explanatory for the top failures.
**+** Zero blast radius when disabled — opt-in default, graceful no-hint degradation, additive schema and GraphQL changes.
**+** Redaction-before-egress keeps the secret-handling boundary identical to ADR-0016; no new class of data leaves the environment unredacted.
**+** Batch tier + idempotency guard + prompt caching bound cost to roughly one request per *new* cluster.

**−** A net-new external dependency (the Claude API) and a per-cluster cost. Mitigated by opt-in default, batch pricing, and the NULL-only idempotency guard.
**−** Failure data (post-redaction) leaves the operator's environment when the feature is enabled. This is disclosed here and surfaced in the Helm values comment; it is the operator's explicit choice via `TEO_LLM_HINTS_ENABLED`.
**−** Redaction false-negatives are possible — a novel secret format could pass the regex set. We accept the same residual risk ADR-0016 accepts for logs, and the feature is off by default.
**−** Hints are advisory and can be wrong. They are labelled as generated and carry a confidence; they never gate quarantine, scheduling, or any control-plane decision.

## Implementation note (PR A)

The engine ships over **raw `net/http`** against the synchronous Messages API (`POST /v1/messages`), **not** the `anthropic-sdk-go` SDK or the Message Batches API named in the Decision — a deliberate, documented divergence that mirrors ADR-0019 (whose predictor `MLClient` likewise chose raw HTTP over its decided gRPC contract for a low-QPS, single-call-site external service). Rationale: it adds **no new dependency** (no `go.sum` churn, nothing for `make licenses` to clear, builds and tests fully offline), matches the established external-service pattern in `internal/predictor/mlclient.go`, and the opt-in default plus graceful no-hint degradation make the wire format non-load-bearing. The nightly cron summarizes one cluster per request; the **Message Batches API remains the retained optimization** for when cluster volume makes the per-request cost matter.

For the same robustness reasons the response is parsed leniently (first-`{`-to-last-`}` extraction) from a JSON-only system prompt rather than constrained with `output_config.format`; **structured outputs are a documented future hardening**. None of this reopens the Decision — the seam (`Summarizer`) is unchanged, so swapping in the SDK + Batches + structured outputs later is an internal change to `client.go` with no migration or schema impact.

Delivered in PR A: migration 007, the `internal/llmhints` package (`Runner` + `Summarizer`/`ClusterSource` seams, the raw-HTTP `Client` with redaction-before-egress, the Postgres `PGClusterSource`), the `result-pipeline llm-hints` CLI subcommand, the default-off Helm CronJob, and offline unit tests (stubbed `Summarizer`/source for the Runner; `httptest` for the Client, asserting secrets are redacted before egress). PR B adds the three display surfaces.

## Alternatives considered
- **Synchronous Messages API, on-demand per cluster.** Rejected for the default path: higher per-call cost and no batching benefit for a nightly sweep. Retained as a possible future on-demand button.
- **A non-Claude provider, or a provider-neutral abstraction.** Rejected: no provider is in the repo, the orchestrator is Go, and a single official SDK is the smallest, best-supported footprint. A neutral abstraction is premature for one call site.
- **Self-hosted / local model.** Rejected for v1.x: adds a third runtime to the chart (cf. the Python predictor cost in ADR-0019) for a feature that is opt-in and non-critical. Revisit if operator demand for fully-local inference appears.
- **Redact in the result pipeline instead of before the API call.** Rejected for the same reason ADR-0016 rejected it for logs: the data would already have crossed the wire.
- **Store the hint in ClickHouse alongside spans.** Rejected: the hint is per-cluster (Postgres `failure_clusters`), not per-span; it belongs with the cluster row it annotates.
