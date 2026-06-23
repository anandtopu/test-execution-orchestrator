# ADR-0008: GraphQL for the UI read API; gRPC + REST elsewhere

**Status:** Accepted
**Date:** 2026-04-30

## Context
The UI needs flexible reads over runs, shards, tests, failure clusters, flake records, with live updates. CI integrations need a stable, simple POST endpoint. Workers need high-throughput, strongly-typed RPCs.

## Decision
- **gRPC** for worker traffic and internal services.
- **REST** (`/api/v1`) for CI integrations and webhooks.
- **GraphQL** (`/graphql`, `/graphql/subscriptions`) for the UI and power users.

## Consequences
**+** Each surface fits its consumer. UI gets one endpoint, no over-fetching, live updates via subscriptions.
**−** Three API surfaces to maintain. We mitigate by having all three call into the same service layer (no duplicated business logic).
**−** GraphQL caching is harder than REST caching. UI uses urql with document caching; server-side caching is post-MVP.

### Implementation notes (as built)
- The Go server uses **`graphql-go/graphql`** (programmatic schema in `internal/api/graphql.go`), **not** `gqlgen` as this ADR originally anticipated. The SDL at `/graphql/schema` is hand-maintained and kept in sync by a parity test (`TestSDLAgreesWithProgrammaticSchema`).
- **Subscriptions (delivered v1.1, 2026-06-23, FR-706 / S-09-02 AC3):** `/graphql/subscriptions` serves the `graphql-transport-ws` subprotocol, hand-rolled on `coder/websocket` (the library ships no transport for `graphql-go`). Live updates do **not** push event payloads over a bus; instead the run-manager emits a best-effort **core-NATS** hint (`teo.ui.run_changed`, ephemeral, non-JetStream — see ADR-0007) per committed transition, every API replica core-subscribes, and an in-process hub re-reads the **authoritative** run from Postgres and pushes the full snapshot. This keeps Postgres the single source of truth (a missed hint self-heals on the next read), needs **no sticky sessions** (any replica serves any run), and degrades to client-side 2 s polling when NATS is unconfigured. WS auth reuses the `teo_session` cookie already validated by the HTTP middleware (browsers can't set `Authorization` on a WS upgrade).

## Alternatives considered
- **REST everywhere.** Rejected: UI experience suffers materially on the run page (multiple round trips).
- **gRPC-Web for the UI.** Rejected: more boilerplate per page than GraphQL gives us, and live updates are still ad hoc.
