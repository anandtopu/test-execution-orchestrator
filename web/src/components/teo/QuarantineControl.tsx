'use client';

// Operator-initiated manual quarantine control (S-08-03, T-08-03-03).
// Toggles a test between active and quarantined via the /api/graphql/quarantine
// proxy. Quarantining opens an inline confirm with an optional reason; the
// server records an audit row and enforces the engineer/admin role.

import { useRouter } from 'next/navigation';
import { useState } from 'react';
import { Icon } from './Icons';

export interface QuarantineToggleInput {
  testId: string;
  quarantine: boolean;
  reason?: string;
}

export interface QuarantineControlProps {
  testId: string;
  quarantined: boolean;
  /** Override the network call for tests. */
  doToggle?: (input: QuarantineToggleInput) => Promise<{ id: string; status: string } | null>;
}

async function defaultToggle(input: QuarantineToggleInput): Promise<{ id: string; status: string } | null> {
  const res = await fetch('/api/graphql/quarantine', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(input),
  });
  if (!res.ok) return null;
  return (await res.json()) as { id: string; status: string };
}

export function QuarantineControl({ testId, quarantined, doToggle = defaultToggle }: QuarantineControlProps) {
  const router = useRouter();
  const [open, setOpen] = useState(false);
  const [reason, setReason] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const submit = async (quarantine: boolean) => {
    setBusy(true);
    setError(null);
    const next = await doToggle({
      testId,
      quarantine,
      reason: quarantine ? reason.trim() || undefined : undefined,
    });
    setBusy(false);
    if (!next) {
      setError(quarantine ? 'Quarantine failed' : 'Unquarantine failed');
      return;
    }
    setOpen(false);
    setReason('');
    router.refresh(); // re-fetch the server component so the lane/badge updates
  };

  if (quarantined) {
    return (
      <span style={{ display: 'inline-flex', alignItems: 'center', gap: 6 }}>
        <button
          type="button"
          className="btn"
          disabled={busy}
          onClick={() => submit(false)}
          data-testid="unquarantine-button"
        >
          {busy ? 'Restoring…' : 'Unquarantine'}
        </button>
        {error && <span style={{ fontSize: 11, color: 'var(--sr-fail)' }}>{error}</span>}
      </span>
    );
  }

  if (!open) {
    return (
      <button
        type="button"
        className="btn btn--primary"
        onClick={() => setOpen(true)}
        data-testid="quarantine-button"
      >
        <Icon.Bug /> Quarantine
      </button>
    );
  }

  return (
    <span style={{ display: 'inline-flex', alignItems: 'center', gap: 6 }} data-testid="quarantine-confirm">
      <input
        type="text"
        value={reason}
        onChange={(e) => setReason(e.target.value)}
        placeholder="reason (optional)"
        aria-label="quarantine reason"
        disabled={busy}
        style={{
          fontFamily: 'var(--sr-font-mono)',
          fontSize: 12,
          padding: '4px 8px',
          borderRadius: 'var(--sr-radius-sm)',
          border: '1px solid var(--sr-border)',
          background: 'var(--sr-bg)',
          color: 'var(--sr-fg)',
          width: 200,
        }}
      />
      <button
        type="button"
        className="btn btn--primary"
        disabled={busy}
        onClick={() => submit(true)}
        data-testid="quarantine-confirm-button"
      >
        {busy ? 'Quarantining…' : 'Confirm'}
      </button>
      <button
        type="button"
        className="btn btn--ghost"
        disabled={busy}
        onClick={() => {
          setOpen(false);
          setReason('');
          setError(null);
        }}
      >
        Cancel
      </button>
      {error && <span style={{ fontSize: 11, color: 'var(--sr-fail)' }}>{error}</span>}
    </span>
  );
}
