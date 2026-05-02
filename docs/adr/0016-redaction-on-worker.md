# ADR-0016: Secret redaction on the worker, before transmission

**Status:** Accepted
**Date:** 2026-04-30

## Context
Test logs frequently include accidentally-printed credentials, JWTs, AWS keys, and the like. PRD §11 lists this as a real concern. We must redact, but we must also preserve enough information for triage.

## Decision
Redaction runs **on the worker**, before logs leave the pod, against a configurable pattern set:
- AWS access keys (`AKIA[0-9A-Z]{16}`)
- AWS secret keys (heuristic: `[A-Za-z0-9/+=]{40}` near `AWS_SECRET`)
- JWTs (`eyJ[A-Za-z0-9_-]+\.eyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+`)
- Generic high-entropy tokens > 24 chars matching configurable allowlist tags
- Operator-supplied regex patterns (in `values.yaml`)

Replacement is `[REDACTED:<rule_id>]` — the rule ID survives so triage can confirm the redactor fired and which pattern matched.

## Consequences
**+** Secrets never traverse the wire, never land in S3, never appear in dashboards.
**+** Pattern set is shipped as defaults; operators extend.
**−** False positives possible — a 40-char base64 string may be redacted even when not a secret. We accept this; the rule ID lets users see what fired.
**−** Adds CPU on the worker. Empirically <2% overhead at typical log volumes.

## Alternatives considered
- **Redact in the result pipeline.** Rejected: secrets already crossed the wire.
- **Don't redact; rely on operator hygiene.** Rejected: defaults must be safe.
