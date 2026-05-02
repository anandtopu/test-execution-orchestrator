# Security Policy

## Supported versions

TEO is pre-1.0. Security fixes are applied to the `main` branch and the most recent
tagged release. Once 1.0 ships, the latest minor version on the current major is
supported; older versions are best-effort.

## Reporting a vulnerability

**Do not open a public GitHub issue.** Email `security@teo.dev` (placeholder — replace
with the project's actual contact) with:

- A description of the issue and its impact.
- Steps to reproduce, ideally with a minimal repro.
- Affected versions.
- Any suggested mitigations.

We aim to:

- Acknowledge reports within **3 business days**.
- Provide an initial assessment within **7 business days**.
- Issue a fix and CVE (where applicable) on a coordinated-disclosure timeline,
  typically within 90 days.

If the issue affects a third-party dependency, we will coordinate upstream as well.

## Scope

In scope:

- Code in `cmd/`, `internal/`, `pkg/`, `services/`.
- The published Helm chart in `deploy/helm/teo/`.
- Container images published to `ghcr.io/teo-dev/*`.

Out of scope:

- Issues that require running modified TEO source.
- Findings whose primary impact is on operator-supplied configuration (e.g.,
  weak passwords, public S3 buckets).
- Self-XSS, missing security headers without a demonstrated impact, denial of
  service via excessive resource use.

## Hardening guidance for operators

See [`docs/architecture/deployment.md`](docs/architecture/deployment.md) §10 for the
production hardening checklist.
