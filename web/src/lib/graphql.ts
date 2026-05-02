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
  if (!res.ok) return null;
  const j = (await res.json()) as { data?: T; errors?: unknown };
  return j.data ?? null;
}
