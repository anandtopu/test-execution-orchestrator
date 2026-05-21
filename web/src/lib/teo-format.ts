// ====================================================
// TEO — Formatters + category colors
// Ported from the design handoff (atoms.jsx `fmt` + CAT_COLOR).
//
// These intentionally differ from src/lib/format.ts: the design fixture
// works in *seconds* (not ms) and renders durations as "m:ss". Keeping a
// separate module avoids changing format.ts, which the existing pages and
// their unit tests depend on.
// ====================================================

export interface Delta {
  txt: string;
  good: boolean;
}

export const fmt = {
  /** Seconds → "Ns" under a minute, otherwise "m:ss". */
  duration(sec: number | null | undefined): string {
    if (sec == null) return '—';
    if (sec < 60) return `${sec.toFixed(0)}s`;
    const m = Math.floor(sec / 60);
    const s = Math.round(sec % 60)
      .toString()
      .padStart(2, '0');
    return `${m}:${s}`;
  },
  pct(x: number | null | undefined): string {
    if (x == null) return '—';
    return `${(x * 100).toFixed(1)}%`;
  },
  pctShort(x: number | null | undefined): string {
    if (x == null) return '—';
    return `${(x * 100).toFixed(0)}%`;
  },
  dollars(x: number | null | undefined): string {
    if (x == null) return '—';
    return `$${x.toFixed(2)}`;
  },
  sha(s: string | null | undefined): string {
    return (s || '').slice(0, 7);
  },
  delta(x: number | null | undefined, { invert = false }: { invert?: boolean } = {}): Delta | null {
    if (x == null) return null;
    const good = invert ? x < 0 : x > 0;
    const sign = x > 0 ? '+' : '';
    return { txt: `${sign}${(x * 100).toFixed(1)}%`, good };
  },
};

// --- Category color (for failure clusters & flakes) ---
export const CAT_COLOR: Record<string, string> = {
  assertion: 'hsl(0 72% 55%)',
  timeout: 'hsl(32 90% 50%)',
  panic: 'hsl(280 65% 55%)',
  network: 'hsl(217 91% 60%)',
  race: 'hsl(160 70% 42%)',
  'order-dependent': 'hsl(280 65% 55%)',
  'async/timing': 'hsl(32 90% 50%)',
  'resource-leak': 'hsl(217 91% 60%)',
  'env-dependent': 'hsl(160 70% 42%)',
  randomness: 'hsl(320 65% 55%)',
};
