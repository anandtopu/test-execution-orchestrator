'use client';

import { useRouter } from 'next/navigation';
import { useState } from 'react';

export interface RerunFailedButtonProps {
  runId: string;
  failedCount: number;
  /** Override fetch impl for tests. */
  doRerun?: (runId: string) => Promise<{ id: string } | null>;
}

async function defaultRerun(runId: string): Promise<{ id: string } | null> {
  const res = await fetch('/api/graphql/rerun', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ runId }),
  });
  if (!res.ok) return null;
  return (await res.json()) as { id: string };
}

export function RerunFailedButton({ runId, failedCount, doRerun = defaultRerun }: RerunFailedButtonProps) {
  const router = useRouter();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  if (failedCount <= 0) {
    return null; // nothing to rerun
  }

  return (
    <div className="flex items-center gap-3">
      <button
        type="button"
        disabled={busy}
        onClick={async () => {
          setBusy(true);
          setError(null);
          const next = await doRerun(runId);
          setBusy(false);
          if (!next) {
            setError('Rerun failed');
            return;
          }
          router.push(`/runs/${next.id}`);
        }}
        className="rounded bg-blue-600 px-3 py-1 text-sm text-white hover:bg-blue-700 disabled:opacity-50"
        data-testid="rerun-failed-button"
      >
        {busy ? 'Rerunning…' : `Rerun ${failedCount} failed test${failedCount === 1 ? '' : 's'}`}
      </button>
      {error && <span className="text-xs text-red-600">{error}</span>}
    </div>
  );
}
