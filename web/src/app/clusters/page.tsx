import { ClustersScreen } from '@/components/teo/Clusters';
import { gqlFetch } from '@/lib/graphql';
import { FailureClustersQuery } from '@/lib/queries';
import { adaptClusters, type GqlCluster } from '@/lib/teo-adapt';

// Failure clusters — the spatial map (nodes sized by occurrence count).
// GraphQL-backed (FailureClustersQuery → teo-adapt) as of FR-701..705 wiring;
// no longer rendered from the TEO_DATA mock. A non-2xx API response (or a
// GraphQL-level error) coalesces to an empty list so the screen shows its empty
// state rather than crashing; a hard connection failure (fetch reject — DNS,
// connection refused, unset/invalid TEO_API_URL) is caught here and also
// degrades to the empty state instead of throwing the route.
export default async function ClustersPage() {
  let clusters;
  try {
    const data = await gqlFetch<{ failureClusters: GqlCluster[] }>(FailureClustersQuery);
    clusters = adaptClusters(data?.failureClusters ?? []);
  } catch (err) {
    console.error('ClustersPage: failed to fetch failure clusters', err);
    clusters = adaptClusters([]);
  }
  return <ClustersScreen clusters={clusters} />;
}
