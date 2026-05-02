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
**+** `gqlgen` (Go) gives type-safe codegen from schema-first SDL.
**−** Three API surfaces to maintain. We mitigate by having all three call into the same service layer (no duplicated business logic).
**−** GraphQL caching is harder than REST caching. UI uses urql with document caching; server-side caching is post-MVP.

## Alternatives considered
- **REST everywhere.** Rejected: UI experience suffers materially on the run page (multiple round trips).
- **gRPC-Web for the UI.** Rejected: more boilerplate per page than GraphQL gives us, and live updates are still ad hoc.
