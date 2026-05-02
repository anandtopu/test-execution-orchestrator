import { gqlFetch } from '@/lib/graphql';
import { FailureClustersQuery } from '@/lib/queries';

type Cluster = {
  id: string;
  representativeMessage: string;
  representativeStack: string;
  occurrences: number;
  firstSeen: string;
  lastSeen: string;
};

async function fetchClusters(): Promise<Cluster[]> {
  const data = await gqlFetch<{ failureClusters: Cluster[] }>(FailureClustersQuery);
  return data?.failureClusters ?? [];
}

export default async function ClustersPage() {
  const clusters = await fetchClusters();
  return (
    <div>
      <h1 className="text-2xl font-semibold">Failure clusters</h1>
      <p className="mt-1 text-sm text-gray-600">
        Distinct error patterns grouped by normalized stack-trace fingerprint.
      </p>
      <ul className="mt-4 space-y-3">
        {clusters.length === 0 ? (
          <li className="text-sm text-gray-500">No failures yet.</li>
        ) : (
          clusters.map((c) => (
            <li key={c.id} className="rounded border p-3">
              <div className="flex items-center justify-between">
                <span className="text-sm font-medium">
                  {c.representativeMessage || '(no message)'}
                </span>
                <span className="text-xs text-gray-500">{c.occurrences} occurrences</span>
              </div>
              <pre className="mt-2 max-h-40 overflow-auto rounded bg-gray-50 p-2 text-xs">
                {c.representativeStack}
              </pre>
              <div className="mt-1 text-xs text-gray-500">
                first {c.firstSeen} · last {c.lastSeen}
              </div>
            </li>
          ))
        )}
      </ul>
    </div>
  );
}
