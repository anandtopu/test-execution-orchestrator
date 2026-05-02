# ADR-0014: OIDC for humans, API keys for machines

**Status:** Accepted
**Date:** 2026-04-30

## Context
Self-hosted, greenfield. No existing IdP. We need humans signing into the UI and CI jobs (machines) calling the API. PRD §11 mentions SOC2 (deferred), but the auth model must be defensible day 1.

## Decision
- **Humans:** OIDC. The Helm chart bundles **Dex** preconfigured to a no-op identity source; operators wire Dex to their own IdP (Google, GitHub, OIDC of choice).
- **Machines:** **API keys** with a `teo_<scope>_<random>` prefix format. Stored as argon2id hashes; the plaintext is shown once at creation. Scopes: `runs.write`, `results.write`, `read.all`, etc.
- **Internal services:** **mTLS** between control-plane services using cert-manager-issued certs. Cluster-internal traffic is otherwise blocked by NetworkPolicy.

JWT signing key (HS256 in v1, asymmetric in v1.5) is stored as a k8s Secret and rotatable.

## Consequences
**+** Standard, debuggable, federable.
**+** API keys are explicit and revocable; audit-friendly.
**−** Dex adds another component to operate. We accept it for the federation flexibility.
**−** Asymmetric JWT (RS256) is post-MVP. HS256 is fine for single-deployment scope.

## Alternatives considered
- **Username/password only.** Rejected: poor security posture.
- **Bake an IdP.** Rejected: out of scope.
- **OAuth Bearer through GitHub directly.** Rejected: ties auth to a single VCS.
