import { afterEach, describe, expect, it } from 'vitest';
import { cleanup, fireEvent, render, screen } from '@testing-library/react';
import { RunsTable, type Run } from './RunsTable';

function makeRuns(n: number): Run[] {
  return Array.from({ length: n }, (_, i) => ({
    id: `run-${i}`,
    repoFullName: `acme/repo-${i}`,
    branch: 'main',
    commitSha: `deadbeef${i.toString().padStart(4, '0')}`,
    status: i % 2 === 0 ? 'succeeded' : 'failed',
    startedAt: '2026-06-09T12:00:00Z',
    finishedAt: '2026-06-09T12:01:00Z',
    totalDurationMs: 60000,
  }));
}

const ROW_HEIGHT = 36;
const HEIGHT = 360;
const OVERSCAN = 6;

describe('<RunsTable />', () => {
  afterEach(cleanup);

  it('windows the rows — far fewer than the total render on first paint', () => {
    const total = 300;
    render(
      <RunsTable
        runs={makeRuns(total)}
        rowHeight={ROW_HEIGHT}
        height={HEIGHT}
        overscan={OVERSCAN}
      />,
    );
    const rows = screen.getAllByTestId('run-row');
    // At scrollTop=0 (jsdom): startIndex clamps to 0, so the window is
    // [0, visibleCount) where visibleCount = ceil(height/rowHeight)+overscan*2.
    const visibleCount = Math.ceil(HEIGHT / ROW_HEIGHT) + OVERSCAN * 2;
    expect(rows.length).toBe(visibleCount);
    expect(rows.length).toBeLessThan(total);
  });

  it('spacer rows preserve the full scroll extent', () => {
    const total = 300;
    const { container } = render(
      <RunsTable
        runs={makeRuns(total)}
        rowHeight={ROW_HEIGHT}
        height={HEIGHT}
        overscan={OVERSCAN}
      />,
    );
    const windowed = screen.getAllByTestId('run-row').length;
    const spacers = container.querySelectorAll('tr[aria-hidden="true"] > td');
    // At the top of the list only the bottom spacer is present (top spacer is 0).
    const spacerPx = Array.from(spacers).reduce(
      (sum, td) => sum + parseInt((td as HTMLElement).style.height || '0', 10),
      0,
    );
    expect(spacerPx).toBe((total - windowed) * ROW_HEIGHT);
    // The scrollbar extent must equal the full list height: the two spacer rows
    // plus the windowed (rendered) rows together account for every row's px, so
    // the viewport's scrollable content is exactly total * rowHeight even though
    // only `windowed` real rows are in the DOM.
    expect(spacerPx + windowed * ROW_HEIGHT).toBe(total * ROW_HEIGHT);
  });

  it('scrolling the viewport advances the rendered window', () => {
    const total = 300;
    render(
      <RunsTable
        runs={makeRuns(total)}
        rowHeight={ROW_HEIGHT}
        height={HEIGHT}
        overscan={OVERSCAN}
      />,
    );
    // Before scroll: window starts at row 0.
    expect(screen.queryByText('acme/repo-0')).toBeInTheDocument();

    const viewport = screen.getByTestId('runs-scroll');
    // Scroll down 100 rows worth of pixels. scrollTop is a pure data-row offset
    // (header is in a separate non-scrolling table), so startIndex should be
    // floor(3600/36) - overscan = 100 - 6 = 94.
    fireEvent.scroll(viewport, { target: { scrollTop: 100 * ROW_HEIGHT } });

    expect(screen.queryByText('acme/repo-0')).not.toBeInTheDocument();
    expect(screen.queryByText('acme/repo-100')).toBeInTheDocument();
    // The earliest row in the window is startIndex 94 (100 - overscan).
    expect(screen.queryByText('acme/repo-94')).toBeInTheDocument();
    expect(screen.queryByText('acme/repo-93')).not.toBeInTheDocument();
  });

  it('overscan=0 still renders the correct window (no header offset to mask)', () => {
    const total = 300;
    render(
      <RunsTable runs={makeRuns(total)} rowHeight={ROW_HEIGHT} height={HEIGHT} overscan={0} />,
    );
    const viewport = screen.getByTestId('runs-scroll');
    fireEvent.scroll(viewport, { target: { scrollTop: 50 * ROW_HEIGHT } });
    // With overscan=0 the first visible data row is exactly floor(1800/36)=50.
    expect(screen.queryByText('acme/repo-50')).toBeInTheDocument();
    expect(screen.queryByText('acme/repo-49')).not.toBeInTheDocument();
  });

  it('renders the empty state when there are no runs', () => {
    render(<RunsTable runs={[]} />);
    expect(screen.getByText(/No runs yet/)).toBeInTheDocument();
    expect(screen.queryByTestId('run-row')).not.toBeInTheDocument();
  });
});
