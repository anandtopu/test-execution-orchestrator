// Home = the marquee "Live run" screen (shard Gantt + predictor calibration).
//
// ui-home-calibration: this is now a data-fetching Server Component. It resolves
// the most-recent run via RunsQuery, fetches its full detail via RunByIdQuery,
// adapts the failure clusters server-side, and hands the raw run snapshot to the
// LiveRunDetail client wrapper (which re-fetches + re-adapts every 2s while the
// run is live). The teo/ design renders unchanged — only the data source moved
// off the TEO_DATA mock.

import { LiveRunDetail } from '@/components/teo/LiveRunDetail';
import { gqlFetch } from '@/lib/graphql';
import { RunByIdQuery, RunsQuery, FailureClustersQuery } from '@/lib/queries';
import { adaptClusters, type GqlCluster, type GqlRun } from '@/lib/teo-adapt';

interface RunsResult {
  runs: Array<{ id: string }> | null;
}

interface RunByIdResult {
  run: GqlRun | null;
}

interface ClustersResult {
  failureClusters: GqlCluster[] | null;
}

async function fetchLatestRun(): Promise<GqlRun | null> {
  const list = await gqlFetch<RunsResult>(RunsQuery, { first: 1 });
  const latestId = list?.runs?.[0]?.id;
  if (!latestId) return null;
  const detail = await gqlFetch<RunByIdResult>(RunByIdQuery, { id: latestId });
  return detail?.run ?? null;
}

export default async function HomePage() {
  // gqlFetch returns null on non-2xx / GraphQL errors, but a hard connection
  // failure (DNS, connection refused, unset/invalid TEO_API_URL) makes the
  // underlying fetch REJECT — which would crash this (most-visited) route. Wrap
  // it so a backend outage degrades to the 'No runs yet' empty state, matching
  // the sibling clusters/flakes pages.
  let run: GqlRun | null = null;
  try {
    run = await fetchLatestRun();
  } catch (err) {
    console.error('HomePage: failed to fetch latest run', err);
  }
  if (!run) {
    return (
      <div
        style={{
          padding: 60,
          textAlign: 'center',
          color: 'var(--sr-fg-muted)',
          fontFamily: 'var(--sr-font-mono)',
          fontSize: 13,
        }}
      >
        No runs yet. Kick off a run (e.g. <code>teo discover</code> + push) and the live shard Gantt
        will appear here.
      </div>
    );
  }

  let clustersData: ClustersResult | null = null;
  try {
    clustersData = await gqlFetch<ClustersResult>(FailureClustersQuery);
  } catch (err) {
    console.error('HomePage: failed to fetch failure clusters', err);
  }
  const clusters = adaptClusters(clustersData?.failureClusters ?? []);

  return <LiveRunDetail initial={run} clusters={clusters} />;
}
