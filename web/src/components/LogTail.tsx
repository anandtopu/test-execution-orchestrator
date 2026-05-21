'use client';

import { useCallback, useEffect, useState } from 'react';

export interface LogTailResponse {
  text: string;
  truncated: boolean;
  totalBytes: number | null;
  tailBytes: number;
}

export interface LogTailProps {
  runId: string;
  execId: string;
  /** Initial tail size in bytes. Defaults to 64 KiB. */
  initialTailBytes?: number;
  /** Override fetch impl for tests; defaults to the BFF /api/logs route. */
  fetcher?: (runId: string, execId: string, tailBytes: number) => Promise<LogTailResponse | { error: string; status: number }>;
}

const DEFAULT_TAIL = 64 * 1024;

async function defaultFetcher(
  runId: string,
  execId: string,
  tailBytes: number,
): Promise<LogTailResponse | { error: string; status: number }> {
  const res = await fetch(
    `/api/logs?runId=${encodeURIComponent(runId)}&execId=${encodeURIComponent(execId)}&tailBytes=${tailBytes}`,
    { cache: 'no-store' },
  );
  if (!res.ok) {
    const body = (await res.json().catch(() => ({}))) as { error?: string };
    return { error: body.error ?? 'Failed to load log.', status: res.status };
  }
  return (await res.json()) as LogTailResponse;
}

/**
 * Tail viewer for a single test execution's captured log. Shows the last N
 * bytes; "Load earlier" doubles the tail window (paginating backwards through
 * the log). Backed by a presigned S3 URL fetched server-side — see
 * /api/logs/route.ts.
 */
export function LogTail({ runId, execId, initialTailBytes = DEFAULT_TAIL, fetcher = defaultFetcher }: LogTailProps) {
  const [tailBytes, setTailBytes] = useState(initialTailBytes);
  const [data, setData] = useState<LogTailResponse | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);

  const load = useCallback(
    async (bytes: number) => {
      setLoading(true);
      setError(null);
      const result = await fetcher(runId, execId, bytes);
      if ('error' in result) {
        setError(result.error);
        setData(null);
      } else {
        setData(result);
      }
      setLoading(false);
    },
    [runId, execId, fetcher],
  );

  useEffect(() => {
    void load(tailBytes);
  }, [load, tailBytes]);

  return (
    <div data-testid="log-tail">
      <div className="mb-2 flex items-center gap-3 text-sm">
        <span className="font-medium">Log tail</span>
        {data?.truncated && (
          <button
            type="button"
            className="rounded border px-2 py-0.5 text-xs hover:bg-gray-100"
            onClick={() => setTailBytes((b) => b * 2)}
            disabled={loading}
            data-testid="log-load-earlier"
          >
            Load earlier
          </button>
        )}
        {data && (
          <span className="text-xs text-gray-500" data-testid="log-meta">
            {data.truncated ? 'showing tail' : 'full log'}
            {data.totalBytes != null && ` · ${data.totalBytes.toLocaleString()} bytes`}
          </span>
        )}
      </div>

      {loading && !data && <p className="text-sm text-gray-500">Loading log…</p>}
      {error && (
        <p className="text-sm text-red-600" data-testid="log-error">
          {error}
        </p>
      )}
      {data && (
        <pre
          className="max-h-[28rem] overflow-auto rounded bg-gray-950 p-3 font-mono text-xs leading-relaxed text-gray-100"
          data-testid="log-content"
        >
          {data.text || '(empty)'}
        </pre>
      )}
    </div>
  );
}
