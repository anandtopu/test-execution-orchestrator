# ADR-0009: Helm chart as the reference deployment

**Status:** Accepted
**Date:** 2026-04-30

## Context
TEO is self-hosted, OSS, Kubernetes-targeted (per ADR-0002). Operators expect a deployable artifact, not "git clone and figure it out."

## Decision
The reference deployment is an **umbrella Helm chart** at `deploy/helm/teo/`, with subcharts for the bundled stateful dependencies (CloudNativePG, ClickHouse Operator, NATS, MinIO, Dex). Operators can disable any subchart and point at their own infrastructure.

## Consequences
**+** Standard tooling; operators can `helm template` to inspect, `helm upgrade` to roll forward, `helm rollback` to revert.
**+** Subchart-toggling supports a wide range of operator preferences (managed vs in-cluster databases).
**−** Helm template complexity is real. We mitigate via per-component subchart templates and a documented `values.yaml`.
**−** No Kustomize-native support in v1. Operators preferring Kustomize can `helm template` and feed the output.

## Alternatives considered
- **Operator pattern (CRDs + Go controller).** Rejected: 3-4 weeks more work.
- **Plain manifests in `deploy/k8s/`.** Rejected: poor lifecycle story.
- **Kustomize.** Less mainstream for charts; subchart story is weaker.
