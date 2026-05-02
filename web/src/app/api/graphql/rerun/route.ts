// Next API route that proxies the rerun-failed mutation to GraphQL. Body shape:
//   { "runId": "<uuid>" }

import { NextRequest, NextResponse } from 'next/server';
import { gqlFetch } from '@/lib/graphql';
import { RerunFailedMutation } from '@/lib/queries';

export async function POST(req: NextRequest) {
  const body = (await req.json()) as { runId?: string };
  if (!body.runId) {
    return NextResponse.json({ error: 'runId required' }, { status: 400 });
  }
  const data = await gqlFetch<{ rerunFailed: { id: string } }>(RerunFailedMutation, {
    runId: body.runId,
  });
  if (!data?.rerunFailed) {
    return NextResponse.json({ error: 'rerun failed' }, { status: 502 });
  }
  return NextResponse.json({ id: data.rerunFailed.id });
}
