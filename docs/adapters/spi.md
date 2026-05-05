# Runner Adapter SPI

**Audience:** anyone writing a new TEO test-runner adapter (RSpec, Mocha, Cargo, JUnit, ...).
**Status:** stable for v1.0. Reference implementations: `pkg/adapter/{pytest,gotest,jest}`.
**Story:** S-14-03 / T-14-03-01.

The runner adapter is the only TEO component that knows about a specific test framework. It does two things:

1. **Discover** the tests in a workdir.
2. **Execute** a subset of those tests and stream a `Result` per test as it finishes.

Everything else — log redaction, S3 upload, OTel emission, fingerprint composition, DB writes, retries, NATS, leader election — is owned by the worker agent (`internal/worker`) and the rest of the control plane. Adapters stay small and stateless.

## Interface

The contract lives in `pkg/adapter/adapter.go`:

```go
type Adapter interface {
    Name() string
    Discover(ctx context.Context, workdir string) ([]model.TestEntry, error)
    Execute(ctx context.Context, workdir string, tests []model.TestEntry,
            opts ExecOptions, onResult ResultHandler) error
}

type ExecOptions struct {
    Timeout time.Duration
    Env     map[string]string
}

type Result struct {
    Test           model.TestEntry
    Outcome        model.TestOutcome
    Started        time.Time
    Finished       time.Time
    DurationMS     int
    Stdout         []byte
    Stderr         []byte
    FailureMessage string
    FailureStack   string
}

type ResultHandler func(Result)
```

`model.TestEntry` and `model.TestOutcome` come from `internal/model/model.go`. Adapters depend on those two type names; they don't depend on anything else under `internal/`.

## `Name()`

Returns the runner identifier registered with the worker: `"pytest"`, `"go"`, `"jest"`. Used to populate `tests.runner` in Postgres and to route manifests to the right adapter on the worker side. Lowercase, no spaces.

## `Discover(ctx, workdir) ([]TestEntry, error)`

Returns every test the runner can see in `workdir`. The worker calls this when a manifest is missing or stale; most production runs ship a precomputed manifest with the run, so `Discover` is best-effort.

What you must populate per `TestEntry`:

| Field | Meaning | Required? |
|---|---|---|
| `Path` | File path or package — the addressable unit the runner accepts as a selector | yes |
| `Name` | Test name within `Path` (function, `it()` block, `t.Run` subtest base) | yes |
| `ParamsHash` | Hash of parametrization values for parametrized tests; empty otherwise | only if the runner supports parametrization |
| `Tags` | Free-form labels (e.g. pytest marks); not used by the scheduler in v1.0 | optional |

The composite **fingerprint** the worker stores is `Path + "::" + Name + "::" + ParamsHash` (see `internal/worker/worker.go:recordResult`). That triple must be unique for a logically-distinct test, and it must be **stable** across runs — the flake detector and quarantine machinery key on it. Reuse of the underlying fixture file path is fine; collisions on `(Path, Name, ParamsHash)` are not.

If discovery is impossible without running the framework (Jest can't list `it()` blocks without execution), enumerate at the **file level** with a sentinel `Name`, and let `Execute` emit a `Result` per real test it finds — that's what `pkg/adapter/jest` does.

`Discover` returns `error` only on infrastructure failure (binary missing, workdir invalid). An empty test list is a valid success.

## `Execute(ctx, workdir, tests, opts, onResult) error`

Run the subset of tests in `tests` (which the worker selected — never run more than this). For each test that finishes, call `onResult(Result{...})`. **Stream results as they arrive**; do not batch and emit at the end. The worker uses `onResult` calls to keep run state, dashboards, and S3 log uploads warm.

Hard rules:

- **Selectors are caller-supplied paths.** Pass them directly to the runner (`pytest <nodeids>`, `go test -run '^(...)$' <pkg>`, `jest <files>`). gosec G204/G304 exemptions on these subprocess spawns are legitimate; don't try to sanitize away a perfectly valid file path.
- **Honor `opts.Timeout`** by wrapping `ctx` with `context.WithTimeout` *before* you build the `exec.Cmd` — `exec.CommandContext` binds the cmd to the ctx at construction time. (See the comment in `pytest.go:Execute` — this was a real bug.) When the timeout fires, the runner subprocess is killed and the adapter should still emit a `Result` for any tests that finished cleanly before the kill.
- **Honor cancellation** via `ctx.Done()`. The worker cancels mid-shard on spot-interruption draining (E-13).
- **Merge `opts.Env`** into the subprocess env on top of `os.Environ()`. Pattern: `func mergeEnv(base []string, extra map[string]string) []string` (copy-pasted across all three reference adapters).
- **Do not panic.** A misbehaving runner is normal. Translate it to `Result{Outcome: OutcomeErrored, FailureMessage: ...}` instead.
- **Emit `OutcomeErrored` per requested test** when the runner produced no usable output (no JSON report, no streaming events). pytest and jest both follow this fallback. The worker treats that as "we tried, it didn't go" and the run continues; failing to emit anything would orphan the test in `pending` forever.
- **Sequential is fine.** The worker treats one shard as one adapter invocation; concurrency across tests is the runner's choice (pytest-xdist, jest workers). The scheduler does cross-shard parallelism; you don't need to.

## Outcome translation

Map your runner's vocabulary to `model.TestOutcome`:

| Runner term | TEO outcome |
|---|---|
| pass / passed / ok | `OutcomePassed` |
| fail / failed | `OutcomeFailed` |
| skip / skipped / pending / todo | `OutcomeSkipped` |
| error / collection error / unknown | `OutcomeErrored` |
| context.DeadlineExceeded fired | `OutcomeTimedOut` |
| ctx canceled mid-test | `OutcomeInterrupted` |

`OutcomeErrored` is the catch-all for "the runner produced something we don't understand." Don't over-engineer the mapping.

## Boundaries — what the adapter does NOT do

The worker (`internal/worker/worker.go`) handles everything below. Don't duplicate.

| Concern | Owner | Where |
|---|---|---|
| Log redaction (AWS keys, JWTs, high-entropy) | worker | `worker.uploadLog` calls `a.redactor.String(...)` |
| S3 log upload (multipart > 16MB) | worker | `worker.uploadLog` → `internal/logstore` |
| Test fingerprint composition | worker | `worker.recordResult`: `Path + "::" + Name + "::" + ParamsHash` |
| `tests` / `test_executions` Postgres writes | worker | `worker.recordResult` |
| OTel span emission | result-pipeline / worker telemetry layer | not the adapter's job |
| Retry on flake | run manager + scheduler | re-shards with `attempt+1` |
| NATS publish, ack/nak | worker / agent | not the adapter's job |
| Spot-interruption draining | worker | `Agent.beginDrain` |

The adapter just produces `Result`s. The pipeline takes them from there.

## ExecOptions reference

```go
type ExecOptions struct {
    Timeout time.Duration   // hard ceiling for the whole Execute call; 0 = no timeout
    Env     map[string]string // extra env vars on top of os.Environ()
}
```

The worker currently sets `Timeout: 30 * time.Minute` per shard (`worker.executeShard`). Per-test timeouts, retries, and resource caps are not in v1.0 of `ExecOptions` — argue for them in a follow-up if you need them; the struct is additive-friendly.

## Stdout/Stderr capture

The worker uploads `Result.Stdout` and `Result.Stderr` to S3 after running them through the redactor. Capture is best-effort:

- If the runner produces a structured report file (pytest-json-report, jest `--json --outputFile`), you usually won't have per-test stdout — that's fine, leave the byte slices empty.
- If the runner streams events with output lines (`go test -json` emits `Output` actions), you *can* attach the relevant slice to the `Result` for that test. None of the reference adapters do this in v1.0; it's a tracked enhancement.
- Empty `Stdout` and `Stderr` mean the worker stores `log_object_key = NULL`. That's a valid state.

## Conformance checklist

A new adapter is ready to merge when:

- [ ] `Name()` returns a stable lowercase identifier.
- [ ] `Discover` returns at least one `TestEntry` against a representative fixture, with non-empty `Path` and `Name`.
- [ ] `Discover` returns `(nil, nil)` (no error, empty list) for a workdir with no tests.
- [ ] `Execute` with an empty `tests` slice returns `nil` immediately.
- [ ] `Execute` calls `onResult` exactly once per requested test under the happy path.
- [ ] `Execute` falls back to `OutcomeErrored` per requested test when the runner produces no parseable output.
- [ ] Outcome translation maps every documented runner status to a `model.TestOutcome`; unknown statuses default to `OutcomeErrored`.
- [ ] `opts.Timeout` propagates: build the timeout context **before** `exec.CommandContext`.
- [ ] `opts.Env` merges on top of `os.Environ()`.
- [ ] Subprocess paths come from `tests[i].Path` / `tests[i].Name` — no further sanitization, gosec G204 is exempted at the package level.
- [ ] Unit tests against fixtures in a `testdata/` directory; no live network, no real container spin-up.
- [ ] If parametrized tests are supported, `ParamsHash` is populated and stable across runs given the same parameter values.

## Getting started

`pkg/adapter/template/` is a compileable skeleton with stubs for `Name`, `Discover`, and `Execute`, plus a `template_test.go` that exercises the empty-slice and discovery-error paths. Copy the directory, rename, fill in the runner-specific bits.

Wire it on the worker by registering an instance with whatever runner-routing the worker uses (currently `Agent.Adapter` is set in `cmd/worker/main.go`; multi-runner workers will land with E-17).

## Reference reading

- `pkg/adapter/adapter.go` — interface definition
- `pkg/adapter/pytest/pytest.go` — full-featured: structured report file, parametrization, fallback path
- `pkg/adapter/gotest/gotest.go` — streaming events, sub-test handling, regex-based selector composition
- `pkg/adapter/jest/jest.go` — discovery-at-file-level, per-test results synthesized at execution
- `internal/worker/worker.go` — `executeShard` / `recordResult` / `uploadLog` show how the worker drives an adapter
