import Link from 'next/link';
import { StatusBadge } from '@/components/StatusBadge';
import { gqlFetch } from '@/lib/graphql';
import { RunsQuery } from '@/lib/queries';
import { formatDurationSec, shortSha } from '@/lib/format';

type Run = {
  id: string;
  repoFullName: string;
  branch: string;
  commitSha: string;
  status: string;
  startedAt?: string | null;
  finishedAt?: string | null;
  totalDurationMs?: number | null;
};

async function fetchRuns(): Promise<Run[]> {
  const data = await gqlFetch<{ runs: Run[] }>(RunsQuery, { first: 50 });
  return data?.runs ?? [];
}

export default async function RunsPage() {
  const runs = await fetchRuns();
  return (
    <div>
      <h1 className="text-2xl font-semibold">Recent runs</h1>
      <table className="mt-4 w-full text-sm">
        <thead className="border-b text-left">
          <tr>
            <th className="py-2">Repo</th>
            <th>Branch</th>
            <th>Commit</th>
            <th>Status</th>
            <th>Duration</th>
            <th>Started</th>
          </tr>
        </thead>
        <tbody>
          {runs.length === 0 ? (
            <tr>
              <td colSpan={6} className="py-6 text-center text-gray-500">
                No runs yet. Submit one with <code>teo run</code>.
              </td>
            </tr>
          ) : (
            runs.map((r) => (
              <tr key={r.id} className="border-b">
                <td className="py-2">
                  <Link href={`/runs/${r.id}`}>{r.repoFullName}</Link>
                </td>
                <td>{r.branch}</td>
                <td className="font-mono text-xs">{shortSha(r.commitSha)}</td>
                <td><StatusBadge status={r.status} /></td>
                <td>{formatDurationSec(r.totalDurationMs)}</td>
                <td>{r.startedAt ?? '—'}</td>
              </tr>
            ))
          )}
        </tbody>
      </table>
    </div>
  );
}
