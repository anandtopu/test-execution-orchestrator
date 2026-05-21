// BFF route for the per-test log-tail viewer (S-09-03 / FR-703-704).
//
// Flow: ask the TEO API for a short-lived *presigned S3 URL* for the test
// execution's captured log (API key stays server-side), then fetch just the
// tail of that object with an HTTP suffix-Range and stream the text back to the
// browser. Proxying through the BFF means the browser never needs to reach
// in-cluster MinIO/S3 and there's no CORS to configure. "Pagination" = the
// viewer asks for a larger tail (more of the end of the log) on demand.

import { NextRequest, NextResponse } from 'next/server';

const DEFAULT_TAIL_BYTES = 64 * 1024;
const MAX_TAIL_BYTES = 4 * 1024 * 1024;

interface LogURLResponse {
  url: string;
  key: string;
  expiresInSeconds: number;
}

export async function GET(req: NextRequest) {
  const runId = req.nextUrl.searchParams.get('runId');
  const execId = req.nextUrl.searchParams.get('execId');
  if (!runId || !execId) {
    return NextResponse.json({ error: 'runId and execId are required' }, { status: 400 });
  }

  let tailBytes = Number(req.nextUrl.searchParams.get('tailBytes')) || DEFAULT_TAIL_BYTES;
  tailBytes = Math.max(1024, Math.min(tailBytes, MAX_TAIL_BYTES));

  const apiBase = process.env.TEO_API_URL ?? '';
  const apiKey = process.env.TEO_UI_API_KEY ?? '';

  // 1. Mint the presigned URL via the TEO API.
  const metaRes = await fetch(
    `${apiBase}/api/v1/runs/${encodeURIComponent(runId)}/tests/${encodeURIComponent(execId)}/log`,
    { cache: 'no-store', headers: { authorization: `Bearer ${apiKey}` } },
  );
  if (metaRes.status === 501) {
    return NextResponse.json({ error: 'Log storage is not configured on this deployment.' }, { status: 501 });
  }
  if (metaRes.status === 404) {
    return NextResponse.json({ error: 'No captured log for this test execution.' }, { status: 404 });
  }
  if (!metaRes.ok) {
    return NextResponse.json({ error: 'Could not retrieve the log URL.' }, { status: 502 });
  }
  const meta = (await metaRes.json()) as LogURLResponse;

  // 2. Fetch the tail of the object directly from S3 via suffix-Range.
  const objRes = await fetch(meta.url, {
    cache: 'no-store',
    headers: { Range: `bytes=-${tailBytes}` },
  });
  if (!objRes.ok && objRes.status !== 206) {
    return NextResponse.json({ error: 'Could not fetch the log object.' }, { status: 502 });
  }
  const text = await objRes.text();

  // content-range is "bytes start-end/total"; start > 0 means we only showed
  // the tail and earlier content exists (so "Load more" is meaningful).
  let truncated = false;
  let totalBytes: number | null = null;
  const cr = objRes.headers.get('content-range');
  if (cr) {
    const m = /bytes (\d+)-(\d+)\/(\d+|\*)/.exec(cr);
    if (m) {
      truncated = Number(m[1]) > 0;
      totalBytes = m[3] === '*' ? null : Number(m[3]);
    }
  }

  return NextResponse.json(
    { text, truncated, totalBytes, tailBytes },
    { headers: { 'Cache-Control': 'no-store' } },
  );
}
