import { FlakesScreen } from '@/components/teo/Flakes';
import { gqlFetch } from '@/lib/graphql';
import { FlakesQuery } from '@/lib/queries';
import { adaptFlakes, type GqlFlake } from '@/lib/teo-adapt';

// Flaky tests — Wilson 95% CI bars + 20-run sparklines.
// GraphQL-backed (FlakesQuery → teo-adapt) as of FR-701..705 wiring; no longer
// rendered from the TEO_DATA mock. A non-2xx API response (or a GraphQL-level
// error) coalesces to an empty list so the KPIs read 0 rather than throwing; a
// hard connection failure (fetch reject — DNS, connection refused, unset/invalid
// TEO_API_URL) is caught here and also degrades to empty rather than throwing
// the route.
export default async function FlakesPage() {
  let flakes;
  try {
    const data = await gqlFetch<{ flakes: GqlFlake[] }>(FlakesQuery);
    flakes = adaptFlakes(data?.flakes ?? []);
  } catch (err) {
    console.error('FlakesPage: failed to fetch flakes', err);
    flakes = adaptFlakes([]);
  }
  return <FlakesScreen flakes={flakes} />;
}
