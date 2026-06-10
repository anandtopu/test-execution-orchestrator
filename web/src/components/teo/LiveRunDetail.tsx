'use client';

// ====================================================
// TEO — Live wrapper around RunDetailScreen (ui-home-calibration)
//
// Thin Client Component that seeds RunDetailScreen with the server-adapted first
// snapshot, then re-fetches /api/graphql/run?id= every `pollMs` while the run is
// non-terminal and re-adapts the result. Mirrors LiveRunShards' isLive() gating
// so polling stops automatically when the run reaches a terminal status — the
// home overlay then freezes on the final predicted-vs-observed view.
// ====================================================

import { useEffect, useState } from 'react';
import { RunDetailScreen } from './RunDetail';
import { adaptRun, type GqlRun } from '@/lib/teo-adapt';
import type { Cluster } from '@/lib/teo-data';
import { isLive } from '@/lib/format';

export interface LiveRunDetailProps {
  /** Server-rendered first snapshot (raw GraphQL Run), used as initial source. */
  initial: GqlRun;
  /** Failure clusters for the preview panel (adapted server-side). */
  clusters: Cluster[];
  /** Poll interval while live; defaults to 2000 ms. */
  pollMs?: number;
  /** Override fetch impl for tests; defaults to the /api/graphql/run proxy. */
  fetcher?: (runId: string) => Promise<GqlRun | null>;
}

async function defaultFetcher(runId: string): Promise<GqlRun | null> {
  const res = await fetch(`/api/graphql/run?id=${encodeURIComponent(runId)}`, {
    cache: 'no-store',
  });
  if (!res.ok) return null;
  return (await res.json()) as GqlRun;
}

export function LiveRunDetail({ initial, clusters, pollMs = 2000, fetcher = defaultFetcher }: LiveRunDetailProps) {
  const [gqlRun, setGqlRun] = useState<GqlRun>(initial);

  useEffect(() => {
    if (!isLive(gqlRun.status)) return;
    let cancelled = false;
    const id = setInterval(async () => {
      const next = await fetcher(gqlRun.id);
      if (cancelled || !next) return;
      setGqlRun(next);
    }, pollMs);
    return () => {
      cancelled = true;
      clearInterval(id);
    };
  }, [gqlRun.id, gqlRun.status, pollMs, fetcher]);

  const { run, shards } = adaptRun(gqlRun);
  return <RunDetailScreen run={run} shards={shards} clusters={clusters} />;
}
