# ADR-0002: Self-hosted, single-tenant deployment model

**Status:** Accepted
**Date:** 2026-04-30

## Context
The project ships as fully OSS (Apache 2.0) and self-hosted on day 1, per the user directive. We do not run a SaaS in v1.

## Decision
TEO is **single-tenant per deployment**. There is no `tenant_id` column. RBAC is per-user and per-repo, scoped within the single deployment.

## Consequences
**+** Materially simpler schema, auth, and code paths.
**+** Operators control their own data; no cross-tenant isolation concerns.
**+** Easier compliance story for organizations that need air-gapped deployments.
**−** A future SaaS pivot will require a schema migration (adding `tenant_id` to most tables) and an authz rewrite. We accept this debt.
**−** No multi-tenant resource quotas in v1.

## Alternatives considered
- **Multi-tenant ready from day 1** (insurance against future SaaS). Rejected: the cost of carrying multi-tenant complexity outweighs the optionality value given the project posture.
