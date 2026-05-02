# ADR-0010: Stable test identity via AST + path fingerprint

**Status:** Accepted
**Date:** 2026-04-30

## Context
Test history is only useful if a test's identity survives renames, file moves, and parameter additions. PRD §5.2 calls for `(repo, file path, FQN, param set)` plus AST-level identity for rename resilience.

## Decision
Compute a `fingerprint` per test as:
```
fingerprint = sha256(repo_id || normalized_fqn || params_hash || ast_signature)
```
Where `ast_signature` is a stable hash of the test's AST shape (function name, decorator stack, top-level statement kinds), language-specific. For pytest in MVP, `ast_signature` is computed by a Python AST walker bundled with the worker.

If only the file path changes (no AST change), the fingerprint is unchanged → history preserved.
If the test body materially changes (new assertions, restructured logic), the fingerprint changes → new test row, new history.

`params_hash` is a hash of parametrize values (for `pytest.mark.parametrize`).

## Consequences
**+** Renames don't lose history.
**+** Parametrized tests get distinct identities per param set.
**−** The AST walker is per-language work. v1 ships pytest only; other runners get path+FQN-only fingerprinting until their AST walker lands.
**−** False splits possible if a refactor unintentionally changes the AST signature (e.g., extracting a helper). We accept this — we err on the side of new identity rather than collapsing distinct tests.

## Alternatives considered
- **Path + FQN only.** Rejected: doesn't survive renames.
- **Content-hash of test body.** Rejected: trivial edits would create new identities, destroying history.
- **Operator-managed mapping table.** Rejected: too much friction for users.
