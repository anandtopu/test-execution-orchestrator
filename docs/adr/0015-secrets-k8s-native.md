# ADR-0015: Kubernetes Secrets for v1; SealedSecrets recommended

**Status:** Accepted
**Date:** 2026-04-30

## Context
TEO needs a place to hold DB passwords, the GitHub App private key, the JWT signing key, and per-operator OIDC client secrets. Operator preference varies (Vault, AWS Secrets Manager, sealed-secrets, plain Secrets).

## Decision
- v1: **Kubernetes Secrets** are the contract surface — every TEO Deployment reads its sensitive config from env vars projected from a Secret.
- The Helm chart **does not bake secrets**; operators populate Secrets through their own pipeline.
- Documentation recommends **SealedSecrets** (Bitnami) for GitOps workflows; the chart includes optional SealedSecrets templates for operators who use it.
- Vault / AWS SM integration is **post-MVP**; operators who need it use the External Secrets Operator with our Helm chart in passthrough mode.

## Consequences
**+** No proprietary secret-store dependency in MVP.
**+** Works in any kube cluster.
**−** Plain Secrets are base64, not encrypted at rest unless EKS encryption-at-rest (KMS) is enabled. We document this requirement in the prod hardening checklist.

## Alternatives considered
- **Bake Vault into the chart.** Rejected: heavy dependency, operator preference varies.
- **External Secrets Operator as a hard dependency.** Rejected: same.
