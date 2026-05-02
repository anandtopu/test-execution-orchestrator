import { StatusBadge } from '@/components/StatusBadge';
import { LiveRunShards, type LiveRun } from '@/components/LiveRunShards';
import { RerunFailedButton } from '@/components/RerunFailedButton';
import { gqlFetch } from '@/lib/graphql';
import { RunByIdQuery } from '@/lib/queries';
import { formatDurationSec, isLive, shortSha } from '@/lib/format';

interface RunDetail extends LiveRun {
  repoFullName: string;
  branch: string;
  commitSha: string;
  totalDurationMs?: number | null;
  preemptionCount: number;
  startedAt?: string | null;
  finishedAt?: string | null;
  failedTestCount: number;
}

async function fetchRun(id: string): Promise<RunDetail | null> {
  const data = await gqlFetch<{ run: RunDetail | null }>(RunByIdQuery, { id });
  return data?.run ?? null;
}

export default async function RunDetailPage({ params }: { params: Promise<{ id: string }> }) {
  const { id } = await params;
  const run = await fetchRun(id);
  if (!run) return <div>Run not found.</div>;

  const showRerun = !isLive(run.status) && run.failedTestCount > 0;

  return (
    <div>
      <div className="flex items-center justify-between">
        <h1 className="text-2xl font-semibold">Run {run.id.slice(0, 8)}</h1>
        <StatusBadge status={run.status} />
      </div>
      <dl className="mt-4 grid grid-cols-2 gap-y-1 text-sm sm:grid-cols-4">
        <dt className="text-gray-500">Repo</dt>
        <dd className="font-mono">{run.repoFullName}</dd>
        <dt className="text-gray-500">Branch</dt>
        <dd>{run.branch}</dd>
        <dt className="text-gray-500">Commit</dt>
        <dd className="font-mono">{shortSha(run.commitSha)}</dd>
        <dt className="text-gray-500">Duration</dt>
        <dd>{formatDurationSec(run.totalDurationMs)}</dd>
        {run.preemptionCount > 0 && (
          <>
            <dt className="text-gray-500">Preemptions</dt>
            <dd>{run.preemptionCount}</dd>
          </>
        )}
      </dl>

      <div className="mt-6 flex items-center justify-between">
        <h2 className="text-lg font-medium">Shards</h2>
        {showRerun && (
          <RerunFailedButton runId={run.id} failedCount={run.failedTestCount} />
        )}
      </div>
      <LiveRunShards initial={run} />
    </div>
  );
}
