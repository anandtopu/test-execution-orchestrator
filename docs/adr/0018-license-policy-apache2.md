# ADR-0018: Apache 2.0 license; Apache-compatible deps only

**Status:** Accepted
**Date:** 2026-04-30

## Context
Project is fully OSS (per intake question #5). License choice affects what we can depend on and how downstream operators (including commercial users) can consume TEO.

## Decision
- TEO source code is licensed **Apache License 2.0**.
- All direct and transitive dependencies must be **Apache 2.0-compatible**: Apache 2.0, BSD-2/3, MIT, ISC, or MPL 2.0 (with caveats).
- **AGPL is forbidden.** GPL is forbidden in the binary distribution; LGPL is allowed only as dynamic linking.
- License compliance is enforced in CI via `go-licenses` (Go) and `license-checker` (npm). Build fails on non-compliant licenses.
- A `NOTICE` file aggregates third-party attributions per Apache 2.0 §4(d).

## Consequences
**+** Maximally permissive for operators, including commercial use.
**+** Compatible with the OSS components we already depend on (NATS, ClickHouse, Postgres, OpenTelemetry, Helm, etc., all Apache 2.0).
**−** We cannot use AGPL alternatives (e.g., MongoDB, Redis after 2024 license change) without re-evaluating.
**−** Some scientific Python libraries are BSD/MIT but a few (e.g., GPL-licensed components) would block predictor work in v1.5; we'll vet per addition.

## Alternatives considered
- **MIT.** Equivalent practical effect; Apache 2.0 chosen for explicit patent grant.
- **MPL 2.0.** File-level copyleft; we don't need it.
- **AGPL.** Strong copyleft; conflicts with our "downstream commercial use is fine" goal.
