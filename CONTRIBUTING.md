# Contributing to TEO

Thanks for taking the time to contribute. TEO is fully OSS under Apache 2.0.

## Before you start

- Read [`PRD.md`](PRD.md) for product context and [`docs/architecture/overview.md`](docs/architecture/overview.md) for the system shape.
- Skim [`docs/adr/`](docs/adr/) to understand the binding architectural decisions.
- The [Definition of Done](docs/process/definition-of-done.md) is the bar every PR must clear.

## Development workflow

1. Open or pick up an issue tied to a story (`S-<epic>-<n>`) from [`docs/backlog/stories.md`](docs/backlog/stories.md).
2. Branch from `main`: `git checkout -b feat/S-05-01-lpt-scheduler`.
3. Make changes; keep them scoped to the story.
4. Run `make all` locally before pushing.
5. Open a PR. Title follows Conventional Commits: `feat(scheduler): land LPT bin-packing (S-05-01)`.
6. PR description references the story ID and any FR / ADR IDs.

## Code conventions

- **Format:** `gofmt` + `goimports` (local prefix `github.com/teo-dev/teo`).
- **Lint:** `golangci-lint` config in [`.golangci.yml`](.golangci.yml). No suppressions without a code comment explaining why.
- **Errors:** wrap with `fmt.Errorf("%w: ...", err, ...)` and include enough context to diagnose without a debugger.
- **Logs:** structured JSON via `log/slog`. Standard fields: `service`, `level`, `time`, `trace_id`, `span_id`.
- **Tests:** stdlib `testing` + `testify/require`. No `time.Sleep` to wait on events — use eventually-helpers with bounded timeouts.

## Architectural changes

Material decisions land as an ADR before merge. Use the existing files in [`docs/adr/`](docs/adr/) as templates. Keep ADRs short — context, decision, consequences, alternatives.

## Reporting security issues

See [`SECURITY.md`](SECURITY.md). Do **not** open a public issue for a vulnerability.

## Code of conduct

By participating, you agree to abide by [`CODE_OF_CONDUCT.md`](CODE_OF_CONDUCT.md).

## License

By contributing, you agree your contributions are licensed under Apache 2.0 (see [`LICENSE`](LICENSE)). The `Signed-off-by` trailer is recommended but not required:

```
git commit -s -m "feat(scheduler): ..."
```
