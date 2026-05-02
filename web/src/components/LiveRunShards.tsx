'use client';

import { useEffect, useState } from 'react';
import { GanttBar } from './GanttBar';
import { isLive } from '@/lib/format';

export interface Shard {
  id: string;
  index: number;
  status: string;
  workerId?: string | null;
  predictedDurationMs: number;
  actualDurationMs?: number | null;
  testCount: number;
  startedAt?: string | null;
  finishedAt?: string | null;
}

export interface LiveRun {
  id: string;
  status: string;
  shards: Shard[];
}

export interface LiveRunShardsProps {
  /** Initial server-rendered run, used as the first render's source of truth. */
  initial: LiveRun;
  /** How often to refetch when the run is non-terminal. Defaults to 2000 ms. */
  pollMs?: number;
  /** Override fetch impl for tests; defaults to `window.fetch`. */
  fetcher?: (runId: string) => Promise<LiveRun | null>;
}

/** Default fetcher: hits the same Next.js route prefix the page already uses,
 * via the GraphQL endpoint exposed at /api/graphql/run. We piggyback on a
 * tiny Next API route so the browser doesn't need TEO_API_URL or the API key.
 */
async function defaultFetcher(runId: string): Promise<LiveRun | null> {
  const res = await fetch(`/api/graphql/run?id=${encodeURIComponent(runId)}`, {
    cache: 'no-store',
  });
  if (!res.ok) return null;
  return (await res.json()) as LiveRun;
}

/**
 * Renders the shard Gantt and refetches every `pollMs` while the run is live.
 * Polling stops automatically when the run reaches a terminal status.
 */
export function LiveRunShards({ initial, pollMs = 2000, fetcher = defaultFetcher }: LiveRunShardsProps) {
  const [run, setRun] = useState<LiveRun>(initial);

  useEffect(() => {
    if (!isLive(run.status)) return;
    let cancelled = false;
    const id = setInterval(async () => {
      const next = await fetcher(run.id);
      if (cancelled || !next) return;
      setRun(next);
    }, pollMs);
    return () => {
      cancelled = true;
      clearInterval(id);
    };
  }, [run.id, run.status, pollMs, fetcher]);

  const shards = run.shards ?? [];
  const maxMs = Math.max(
    ...shards.map((s) => s.actualDurationMs ?? s.predictedDurationMs),
    1,
  );

  if (shards.length === 0) {
    return <p className="text-sm text-gray-500">No shards yet.</p>;
  }
  return (
    <div className="mt-2 space-y-1" data-testid="live-run-shards" data-status={run.status}>
      {shards.map((s) => (
        <GanttBar
          key={s.id}
          index={s.index}
          status={s.status}
          durationMs={s.actualDurationMs ?? s.predictedDurationMs}
          maxMs={maxMs}
          testCount={s.testCount}
        />
      ))}
    </div>
  );
}
