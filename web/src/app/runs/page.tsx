import { gqlFetch } from '@/lib/graphql';
import { RunsQuery } from '@/lib/queries';
import { RunsTable, type Run } from '@/components/RunsTable';

async function fetchRuns(): Promise<Run[]> {
  // 200 is the backend clampFirst max (internal/api/graphql.go); values above
  // are silently capped. Windowing in RunsTable keeps 100+ rows performant.
  const data = await gqlFetch<{ runs: Run[] }>(RunsQuery, { first: 200 });
  return data?.runs ?? [];
}

export default async function RunsPage() {
  const runs = await fetchRuns();
  return <RunsTable runs={runs} />;
}
