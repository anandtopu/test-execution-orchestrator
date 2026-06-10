// Next API route that proxies the polling Client Component (LiveRunShards) to
// the GraphQL backend. The browser never sees TEO_API_URL or the API key.

import { NextRequest, NextResponse } from 'next/server';
import { gqlFetch } from '@/lib/graphql';
import { RunByIdQuery } from '@/lib/queries';

interface RunResponse {
  run: {
    id: string;
    status: string;
    // ui-home-calibration: run-level predictor aggregates flow through the proxy
    // so the home overlay's re-adapt on each 2s poll sees real MAE/rho/version.
    predictorMae?: number | null;
    predictorRho?: number | null;
    modelVersion?: string | null;
    startedAt?: string | null;
    totalDurationMs?: number | null;
    repoFullName?: string | null;
    branch?: string | null;
    commitSha?: string | null;
    shards: Array<{
      id: string;
      index: number;
      status: string;
      workerId?: string | null;
      predictedDurationMs: number;
      actualDurationMs?: number | null;
      testCount: number;
      startedAt?: string | null;
      finishedAt?: string | null;
      // ui-home-calibration: per-shard calibration metadata (null until the
      // sibling migration adds the columns) for the predicted-vs-observed view.
      predictionConfidence?: number | null;
      modelVersion?: string | null;
    }>;
  } | null;
}

export async function GET(req: NextRequest) {
  const id = req.nextUrl.searchParams.get('id');
  if (!id) {
    return NextResponse.json({ error: 'id required' }, { status: 400 });
  }
  const data = await gqlFetch<RunResponse>(RunByIdQuery, { id });
  if (!data?.run) {
    return NextResponse.json({ error: 'not found' }, { status: 404 });
  }
  return NextResponse.json(data.run, {
    headers: { 'Cache-Control': 'no-store' },
  });
}
