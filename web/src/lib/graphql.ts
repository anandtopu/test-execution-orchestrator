// urql client factory used by Server Components and the polling Client Component
// on the run-detail page. Per E-09 strategy §3.2.

import { cacheExchange, createClient, fetchExchange } from 'urql';

export function gqlClient() {
  return createClient({
    url: `${process.env.TEO_API_URL}/graphql`,
    fetchOptions: () => ({
      headers: {
        authorization: `Bearer ${process.env.TEO_UI_API_KEY ?? ''}`,
      },
      cache: 'no-store',
    }),
    exchanges: [cacheExchange, fetchExchange],
  });
}

// Standalone fetch helper for Server Components that don't want a urql instance.
// Mirrors the same auth + cache=no-store behaviour.
//
// Returns null on BOTH a non-2xx transport response and a GraphQL-level error
// ({data:null, errors:[...]} with HTTP 200). Callers (the /clusters and /flakes
// pages) intentionally coalesce that null to [] so a transient backend hiccup
// renders the screen's empty state rather than crashing. That deliberately
// conflates "outage" with "no data" at the UI layer; to keep the outage
// OBSERVABLE we log j.errors here (server-side console → captured by the Node
// runtime logs) so a backend regression isn't silently masked as empty data.
export async function gqlFetch<T = unknown>(query: string, variables?: Record<string, unknown>): Promise<T | null> {
  const res = await fetch(`${process.env.TEO_API_URL}/graphql`, {
    method: 'POST',
    cache: 'no-store',
    headers: {
      'Content-Type': 'application/json',
      authorization: `Bearer ${process.env.TEO_UI_API_KEY ?? ''}`,
    },
    body: JSON.stringify({ query, variables }),
  });
  if (!res.ok) {
    console.error(`gqlFetch: non-2xx GraphQL response (${res.status} ${res.statusText})`);
    return null;
  }
  const j = (await res.json()) as { data?: T; errors?: unknown };
  if (j.errors) {
    // GraphQL-level error with HTTP 200 — surface it so an outage during the
    // cutover is observable rather than indistinguishable from an empty dataset.
    console.error('gqlFetch: GraphQL errors', JSON.stringify(j.errors));
  }
  return j.data ?? null;
}
