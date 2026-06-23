// Next API route proxying the operator quarantine mutations to GraphQL, keeping
// the API key server-side (S-08-03). Body shape:
//   { "testId": "<uuid>", "quarantine": true|false, "reason"?: "<text>" }

import { NextRequest, NextResponse } from 'next/server';
import { gqlFetch } from '@/lib/graphql';
import { QuarantineTestMutation, UnquarantineTestMutation } from '@/lib/queries';

type TestResult = { id: string; status: string; quarantinedAt: string | null };

export async function POST(req: NextRequest) {
  const body = (await req.json()) as { testId?: string; quarantine?: boolean; reason?: string };
  if (!body.testId) {
    return NextResponse.json({ error: 'testId required' }, { status: 400 });
  }

  if (body.quarantine) {
    const data = await gqlFetch<{ quarantineTest: TestResult | null }>(QuarantineTestMutation, {
      testId: body.testId,
      reason: body.reason ?? null,
    });
    if (!data?.quarantineTest) {
      return NextResponse.json({ error: 'quarantine failed' }, { status: 502 });
    }
    return NextResponse.json(data.quarantineTest);
  }

  const data = await gqlFetch<{ unquarantineTest: TestResult | null }>(UnquarantineTestMutation, {
    testId: body.testId,
  });
  if (!data?.unquarantineTest) {
    return NextResponse.json({ error: 'unquarantine failed' }, { status: 502 });
  }
  return NextResponse.json(data.unquarantineTest);
}
