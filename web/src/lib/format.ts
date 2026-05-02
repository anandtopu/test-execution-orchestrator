// Pure helpers used by the page components. Extracted into their own module
// so they can be unit-tested without rendering React.

/** Map a run status to its Tailwind color classes. Used by RunsPage StatusBadge. */
export function statusColorClass(status: string): string {
  switch (status) {
    case 'succeeded':
      return 'bg-green-100 text-green-800';
    case 'failed':
      return 'bg-red-100 text-red-800';
    case 'cancelled':
      return 'bg-gray-100 text-gray-800';
    default:
      return 'bg-blue-100 text-blue-800';
  }
}

/** Compute the percentage width of a Gantt bar relative to the longest shard. */
export function ganttWidthPct(durationMs: number, maxMs: number): number {
  if (maxMs <= 0) return 0;
  const pct = (durationMs / maxMs) * 100;
  if (pct < 0) return 0;
  if (pct > 100) return 100;
  return pct;
}

/** Render a duration in seconds with an "s" suffix; em-dash for missing. */
export function formatDurationSec(ms: number | undefined | null): string {
  if (ms == null || ms <= 0) return '—';
  return `${Math.round(ms / 1000)}s`;
}

/** Render a flake rate as a one-decimal percent. */
export function formatPercent(rate: number | undefined | null): string {
  if (rate == null) return '—';
  return `${(rate * 100).toFixed(1)}%`;
}

/** Truncate a commit SHA to short form. */
export function shortSha(sha: string | undefined | null): string {
  if (!sha) return '';
  return sha.slice(0, 7);
}

/** A run is "live" (UI should poll) when its status is not terminal. */
export function isLive(status: string): boolean {
  return !['succeeded', 'failed', 'cancelled'].includes(status);
}

/**
 * Render a USD amount with two decimals, em-dash for null/undefined. The cost
 * dashboard sums per-week values; we don't try to localize currency formatting
 * (most operators read these numbers in a backend deployment context, not a
 * consumer UI), so a plain "$" prefix is enough.
 */
export function formatDollars(amount: number | undefined | null): string {
  if (amount == null || Number.isNaN(amount)) return '—';
  return `$${amount.toFixed(2)}`;
}
