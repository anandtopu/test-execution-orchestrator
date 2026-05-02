# E-09 Web UI — Implementation Strategy

**Status:** ✅ All phases complete. Integration tests landed (testcontainers Postgres → 13 cases over resolvers + HTTP roundtrip). WebSocket subscriptions remain deferred to v1.1 by design.
**Owner:** Frontend (with Backend support for resolver work)
**Linked artifacts:** [`progress.md`](../../progress.md), [`docs/architecture/api-design.md`](../architecture/api-design.md), [ADR-0008](../adr/0008-graphql-api-surface.md)

---

## 1. What's already done

| Surface | Status | Code |
|---|---|---|
| Next.js 15 app-router scaffold | ✅ | [`web/`](../../web) |
| Layout + nav + globals | ✅ | `web/src/app/layout.tsx`, `globals.css` |
| Run list page | 🟡 (REST) | `web/src/app/runs/page.tsx` |
| Run detail page (Gantt) | 🟡 (REST, no shards in payload) | `web/src/app/runs/[id]/page.tsx` |
| Flakes page | 🟡 (REST endpoint missing) | `web/src/app/flakes/page.tsx` |
| Clusters page | 🟡 (REST endpoint missing) | `web/src/app/clusters/page.tsx` |
| Distroless Dockerfile | ✅ | `web/Dockerfile` |
| GraphQL endpoint | ✅ partial | `internal/api/graphql.go` — runs/flakes/clusters lists; **missing** `run(id)`, `Shard`, mutations |

## 2. Gap analysis

Current REST data flow: each Server Component fetches `${TEO_API_URL}/api/v1/...` with the operator-supplied `TEO_UI_API_KEY` baked into the request header. Two URLs are dead (`/api/v1/flakes`, `/api/v1/failure-clusters`) — pages render empty rather than error, which masks the breakage.

Concretely, to close E-09:

| Gap | Impact | Effort |
|---|---|---|
| GraphQL schema has no `run(id)` resolver | RunDetailPage cannot work over GraphQL | 0.5d |
| No `Shard` type / `Run.shards` field | Gantt has nothing to render | 0.5d |
| No `rerunFailed` mutation | S-09-04 unimplementable | 0.5d |
| No live-update mechanism | FR-706 (3s update SLA) not met by static SSR | 0.5d |
| No frontend test framework | Can't enforce behaviour without integration tests | 0.5d |
| REST `/api/v1/flakes` + `/failure-clusters` unimplemented | Pages always empty | absorbed by GraphQL swap |

Total: ~2.5 frontend-days + ~1 backend-day. Fits inside one sprint.

## 3. Strategy

We do **not** add WebSocket subscriptions. Per ADR-0008 the GraphQL surface supports them, but for v1.0 we use **client-side polling at 2s** on the run-detail page (and only while the run is non-terminal). Polling is:

- Simpler to operate (no second listener / no sticky sessions / no JetStream→GraphQL bridge)
- Sufficient for the 3-second freshness SLO in NFR-PERF-107
- Trivially testable
- Easy to swap to subscriptions in v1.1 once we have a concrete reason

### 3.1 Phase A — Backend resolvers (1 day, Backend)

Extend `internal/api/graphql.go`:

```graphql
extend type Query {
  run(id: ID!): Run
}

type Run {
  # existing fields...
  shards: [Shard!]!
  failureClusters: [FailureCluster!]!
  preemptionCount: Int!
}

type Shard {
  id: ID!
  index: Int!
  status: String!
  workerId: String
  predictedDurationMs: Int!
  actualDurationMs: Int
  testCount: Int!
  startedAt: String
  finishedAt: String
}

type Mutation {
  rerunFailed(runId: ID!): Run!
}
```

`rerunFailed` reuses the existing run-creation path — produces a child run scoped to the parent's failed/quarantined tests, sets `runs.parent_run_id`. The mutation returns the new Run; the UI navigates to it.

### 3.2 Phase B — urql client wiring (0.5 day, Frontend)

Add `urql` + `@urql/next` (already declared in `package.json`). Server-side fetches from RSC; client-side hooks for the polling on detail page.

```ts
// web/src/lib/graphql.ts
import { createClient, fetchExchange, cacheExchange } from 'urql';
export function gqlClient() {
  return createClient({
    url: `${process.env.TEO_API_URL}/graphql`,
    fetchOptions: () => ({
      headers: { authorization: `Bearer ${process.env.TEO_UI_API_KEY ?? ''}` },
      cache: 'no-store',
    }),
    exchanges: [cacheExchange, fetchExchange],
  });
}
```

### 3.3 Phase C — page-by-page migration (1 day, Frontend)

Convert each page top-down. The Server Component does the initial fetch; only the run-detail page promotes to a Client Component for polling.

1. `runs/page.tsx` → `query Runs { runs { ... } }`
2. `clusters/page.tsx` → `query FailureClusters { failureClusters { ... } }`
3. `flakes/page.tsx` → `query Flakes { flakes { ... } }`
4. `runs/[id]/page.tsx` → `query Run($id: ID!) { run(id:$id) { ... shards{...} } }`
   - Wrap the shard list in a Client Component that polls `useQuery({ pause: terminal })` every 2s while the run is non-terminal.
5. **Delete** the dead REST handlers' UI dependencies (none consumed elsewhere).

### 3.4 Phase D — rerun-failed flow (0.5 day, Frontend + Backend)

`mutation RerunFailed($runId: ID!) { rerunFailed(runId: $runId) { id } }` — backend filters parent's `test_executions` by outcome, creates a new run with that manifest, returns it. Frontend renders a button on the run-detail page when status is terminal AND there were failures; on click, route to the new run's URL.

### 3.5 Phase E — frontend test framework (0.5 day, Frontend)

Set up **Vitest + @testing-library/react + jsdom**. Why these:
- **Vitest** runs the same Vite that Next.js 15 already uses internally — fastest local loop
- **Testing Library** gives accessibility-first selectors and matches industry practice
- **jsdom** is the standard happy-DOM alternative; either works, jsdom is more compatible with downstream tooling

Coverage target: **all pure formatting and conditional logic**. We do *not* test against the live GraphQL server here — that's covered by Go resolver tests (Phase F) and by Playwright e2e in v1.1.

### 3.6 Phase F — Go resolver tests (0.5 day, Backend)

The existing `graphql_test.go` only checks the SDL string. Extend with:

- Parsed-schema introspection: every documented type/field is reachable.
- Argument validation: `runs(first: -1)` clamps; `runs(first: 9999)` clamps.
- Resolver execution against a stub Source (no DB needed for the field-resolver-from-map mechanism).
- Mutation arg shape.

Resolver tests that depend on Postgres (`queryRuns`, `queryFlakes`, etc.) get the `// +build integration` tag and run in the testcontainers job.

## 4. Sequencing

```
Day 1  (Backend)   Phase A — schema + resolvers + Go tests (Phase F)
Day 2  (Frontend)  Phase B + Phase E      ← unblocks all UI work
Day 3  (Frontend)  Phase C — migrate runs and clusters pages
Day 4  (Frontend)  Phase C — migrate flakes + run-detail (with polling)
Day 5  (Frontend)  Phase D — rerun-failed flow + integration polish
```

Backend Day 1 unblocks Frontend Day 2; the rest is sequential frontend work.

## 5. Test pyramid

| Tier | Where | What we cover |
|---|---|---|
| **Unit** | `web/src/**/*.test.tsx` (Vitest) | StatusBadge color logic, Gantt bar width math, FlakesPage formatting, empty states |
| **Unit** | `internal/api/graphql_*_test.go` | Schema completeness, arg validation, resolver execution against stubs |
| **Integration** | `internal/api/runs_integration_test.go` (`+build integration`) | Real Postgres, GraphQL POST → JSON shape verified |
| **E2E** | _v1.1, deferred_ | Playwright against Helm-deployed staging |

We commit to ≥ **70% statement coverage** for the converted UI files and the GraphQL resolver layer. Coverage gate is informational in CI for v1.0; promotes to blocking at v1.1.

## 6. Risks

| Risk | Mitigation |
|---|---|
| `urql` Server Component support is still maturing in Next 15 | Stick to the documented `@urql/next` patterns; if they break, fall back to direct `fetch` against `/graphql` (we already do that for the JSON-codec gRPC) |
| Polling at 2s × N tabs scales API load | API gateway already has request rate caps per key; polling stops on terminal runs; a single user opening 50 tabs is acceptable for a self-hosted single-tenant install |
| GraphQL field churn during migration breaks the UI | All UI queries live in `web/src/lib/graphql/` and are referenced once; field renames trigger a single-file change |
| Rerun-failed creates an orphan run if the parent has no failures | Mutation refuses with a typed GraphQL error; UI button is disabled when `failedCount === 0` |

## 7. Definition of Done for E-09

A reviewer can refuse merge if any of:

- [ ] All four pages render against GraphQL only (no `/api/v1/*` REST calls from `web/`)
- [ ] Run-detail page updates within 3 seconds of a worker reporting a test (verified manually + integration test)
- [ ] Rerun-failed button creates a child run with the correct manifest
- [ ] `vitest run` is green in CI
- [ ] Go resolver tests added; coverage on `internal/api/graphql.go` ≥ 70%
- [ ] `progress.md` row for E-09 flips to ✅
- [ ] CHANGELOG entry under "E-09 Web UI (completed)"

## 8. Out of scope (explicitly)

- WebSocket / SSE subscriptions — deferred to v1.1
- Playwright e2e — deferred to v1.1
- Server-side GraphQL response caching — deferred; per-request is cheap enough at our scale
- Internationalization, dark mode persistence, mobile layouts — not in v1.0
- The "what-if" simulator UI — deferred per ADR-0012
