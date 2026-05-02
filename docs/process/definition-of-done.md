# Definition of Done

**Status:** Draft
**Date:** 2026-04-30

A story is **Done** only when every item below is true. Reviewers refuse to merge a PR that doesn't satisfy this list.

---

## Code

- [ ] Implements every Acceptance Criterion of the story.
- [ ] Code follows the linting rules in `.golangci.yml` (Go) / `.eslintrc` (UI). No suppressions without a code comment explaining why.
- [ ] No TODOs left behind without a linked GitHub Issue.
- [ ] No commented-out code.
- [ ] Public packages have package-level docstrings.
- [ ] Errors are wrapped with `fmt.Errorf("%w: ...", err, ...)` or equivalent and contain enough context to diagnose.

## Tests

- [ ] Unit tests cover the changed logic; coverage ≥ 70% for critical packages (scheduler, predictor, run manager, result pipeline).
- [ ] Integration tests cover at least one happy path + one failure path through changed boundaries (DB, RPC, queue).
- [ ] Property tests where mathematical invariants exist (e.g., scheduler makespan).
- [ ] Tests are deterministic; no `time.Sleep` to wait for events; use `eventually` helpers with bounded timeouts.

## Schema & API

- [ ] DB schema changes are forward-only migrations in `migrations/`.
- [ ] Migrations are tested with both empty DB and a populated fixture.
- [ ] gRPC proto changes are additive (no field removals or renumbers).
- [ ] REST endpoint changes follow the documented versioning policy.
- [ ] GraphQL schema changes use `@deprecated` for soft removals; breaking changes require a doc note.
- [ ] If the public API changed, `CHANGELOG.md` is updated.

## Documentation

- [ ] Changes that affect operators are reflected in `README.md` and chart `values.yaml` documentation.
- [ ] Architectural decisions land as an ADR (`docs/adr/NNNN-*.md`) before merge.
- [ ] User-visible changes to the CLI/UI are reflected in `docs/`.

## Security

- [ ] No secrets committed (gitleaks check passes).
- [ ] User input is validated at boundaries; SQL uses parameterized queries; templates auto-escape.
- [ ] AuthN + AuthZ paths are tested for the new endpoint.
- [ ] Trivy scan passes on the produced image (no HIGH+).

## Observability

- [ ] New code paths emit structured logs with the standard fields (`service`, `level`, `time`, `trace_id`, `span_id`).
- [ ] New RPCs / endpoints emit metrics: request rate, error rate, duration histogram.
- [ ] New RPCs / endpoints are wrapped in OTel spans.

## Operations

- [ ] If a new component is added, it has CPU/memory requests + limits in the Helm chart.
- [ ] If a new component is added, it has a NetworkPolicy.
- [ ] If the change introduces a failure mode, an alert rule + runbook URL exists for it.

## Review

- [ ] PR title follows Conventional Commits (`feat(scheduler): ...`).
- [ ] PR description references the story ID (e.g., `S-05-01`) and FR/ADR IDs.
- [ ] At least one approving review from a code owner.
- [ ] CI is green; no override.

## Release readiness (story-by-story, not every PR)

- [ ] Manual QA pass on at least one non-trivial happy path against a staged deployment.
- [ ] User-visible behavior demonstrated to PM/lead before "story closed."

---

## Per-component Definition of Ready

A story is **Ready** to be picked up only when:

- [ ] Acceptance criteria are testable and unambiguous.
- [ ] Dependencies (other stories, external services, design) are noted.
- [ ] FR / ADR links are filled in.
- [ ] An estimate exists (≤ 2-day tasks).

Stories that don't meet "Ready" go back to refinement.
