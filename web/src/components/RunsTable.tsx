'use client';

import { useState } from 'react';
import Link from 'next/link';
import { StatusBadge } from '@/components/StatusBadge';
import { formatDurationSec, shortSha } from '@/lib/format';

export type Run = {
  id: string;
  repoFullName: string;
  branch: string;
  commitSha: string;
  status: string;
  startedAt?: string | null;
  finishedAt?: string | null;
  totalDurationMs?: number | null;
};

export interface RunsTableProps {
  /** Server-fetched rows (page passes runs(first: 200) down as props). */
  runs: Run[];
  /** Fixed height of each rendered row in px. MUST equal the rendered
   * single-line row height *including* its bottom border, otherwise the spacer
   * math desyncs from real pixel positions and rows misalign on scroll. Cells
   * are clamped to a single line (truncate / whitespace-nowrap) precisely so a
   * row can never grow past this value. */
  rowHeight?: number;
  /** Height of the scroll viewport in px. */
  height?: number;
  /** Extra rows rendered above/below the visible window to avoid blank flashes
   * during fast scrolls. Correctness no longer depends on overscan masking a
   * header offset — the header lives in its own non-scrolling table now, so
   * overscan=0 renders the correct window. */
  overscan?: number;
}

const COLUMNS = ['Repo', 'Branch', 'Commit', 'Status', 'Duration', 'Started'] as const;

/** Shared column widths so the fixed (non-scrolling) header table and the
 * scrolling body table stay aligned. table-layout: fixed makes these binding. */
const COL_WIDTHS = ['28%', '14%', '12%', '14%', '14%', '18%'] as const;

function Colgroup() {
  return (
    <colgroup>
      {COL_WIDTHS.map((w, i) => (
        <col key={i} style={{ width: w }} />
      ))}
    </colgroup>
  );
}

/**
 * RunsTable renders the run list with fixed-row-height windowing so the table
 * stays performant at 100+ rows. Rather than pull in @tanstack/react-virtual
 * (not a project dependency — the repo intentionally avoids extra UI deps, see
 * the cost dashboard's dependency-free CSS bars), this renders only the rows
 * whose index falls inside the visible scroll window plus overscan, with two
 * aria-hidden spacer rows preserving the full scroll extent.
 *
 * The header is rendered in a SEPARATE non-scrolling table above the scroll
 * viewport. Consequences that matter:
 *   1. The header stays pinned while the body scrolls (UX parity with the old
 *      full-render page-scroll table).
 *   2. scrollTop is a pure data-row offset — it never includes header pixels —
 *      so `Math.floor(scrollTop / rowHeight)` is exactly correct regardless of
 *      overscan. Both tables use table-layout: fixed + shared <colgroup> so the
 *      header columns line up with the body columns.
 */
export function RunsTable({
  runs,
  rowHeight = 36,
  height = 600,
  overscan = 6,
}: RunsTableProps) {
  const [scrollTop, setScrollTop] = useState(0);

  if (runs.length === 0) {
    return (
      <div>
        <h1 className="text-2xl font-semibold">Recent runs</h1>
        <table className="mt-4 w-full text-sm" style={{ tableLayout: 'fixed' }}>
          <Colgroup />
          <thead className="border-b text-left">
            <tr>
              {COLUMNS.map((c) => (
                <th key={c} className="py-2">
                  {c}
                </th>
              ))}
            </tr>
          </thead>
          <tbody>
            <tr>
              <td colSpan={COLUMNS.length} className="py-6 text-center text-gray-500">
                No runs yet. Submit one with <code>teo run</code>.
              </td>
            </tr>
          </tbody>
        </table>
      </div>
    );
  }

  // visibleCount is derived purely from props (height/rowHeight + overscan) so
  // it is deterministic under jsdom, which has no layout engine and reports
  // clientHeight/scrollTop as 0.
  const visibleCount = Math.ceil(height / rowHeight) + overscan * 2;
  // scrollTop is the scroll offset of the BODY viewport only — the header is in
  // its own non-scrolling table, so scrollTop=0 maps to data row 0 exactly. No
  // header-height correction is needed (and overscan no longer has to mask one).
  const startIndex = Math.max(0, Math.floor(scrollTop / rowHeight) - overscan);
  const endIndex = Math.min(runs.length, startIndex + visibleCount);

  const windowed = runs.slice(startIndex, endIndex);
  const topSpacer = startIndex * rowHeight;
  const bottomSpacer = (runs.length - endIndex) * rowHeight;

  return (
    <div>
      <h1 className="text-2xl font-semibold">Recent runs</h1>
      {/* Non-scrolling header table — stays pinned above the scroll viewport. */}
      <table className="mt-4 w-full text-sm" style={{ tableLayout: 'fixed' }}>
        <Colgroup />
        <thead className="border-b text-left">
          <tr>
            {COLUMNS.map((c) => (
              <th key={c} className="py-2">
                {c}
              </th>
            ))}
          </tr>
        </thead>
      </table>
      <div
        data-testid="runs-scroll"
        className="overflow-y-auto"
        style={{ height }}
        onScroll={(e) => setScrollTop(e.currentTarget.scrollTop)}
      >
        <table className="w-full text-sm" style={{ tableLayout: 'fixed' }}>
          <Colgroup />
          <tbody>
            {topSpacer > 0 && (
              <tr aria-hidden="true">
                <td colSpan={COLUMNS.length} style={{ height: topSpacer, padding: 0 }} />
              </tr>
            )}
            {windowed.map((r) => (
              <tr
                key={r.id}
                data-testid="run-row"
                className="border-b"
                style={{ height: rowHeight }}
              >
                <td className="truncate py-2">
                  <Link href={`/runs/${r.id}`}>{r.repoFullName}</Link>
                </td>
                <td className="truncate">{r.branch}</td>
                <td className="truncate font-mono text-xs">{shortSha(r.commitSha)}</td>
                <td className="truncate">
                  <StatusBadge status={r.status} />
                </td>
                <td className="truncate">{formatDurationSec(r.totalDurationMs)}</td>
                <td className="truncate">{r.startedAt ?? '—'}</td>
              </tr>
            ))}
            {bottomSpacer > 0 && (
              <tr aria-hidden="true">
                <td colSpan={COLUMNS.length} style={{ height: bottomSpacer, padding: 0 }} />
              </tr>
            )}
          </tbody>
        </table>
      </div>
    </div>
  );
}
