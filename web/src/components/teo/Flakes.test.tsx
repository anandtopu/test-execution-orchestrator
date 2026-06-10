import { describe, expect, it } from 'vitest';
import { render, screen, fireEvent, within } from '@testing-library/react';
import { FlakesScreen } from './Flakes';
import { adaptFlakes, type GqlFlake } from '@/lib/teo-adapt';

// ui-clusters-flakes: FlakesScreen is prop-driven (no TEO_DATA mock). We feed it
// adapter output built from GraphQL-shaped rows and assert the table + KPI +
// detail-sheet wiring, plus the zero-row NaN guard.

function threeFlakes() {
  const rows: GqlFlake[] = [
    {
      testId: 't-1',
      testPath: 'internal/scheduler/plan_test.go',
      testName: 'TestPlanDeterministic',
      flakeRate: 0.18,
      wilsonLower: 0.12,
      wilsonUpper: 0.29,
      sampleSize: 120,
      category: 'race',
      spark: 'PPFPPPPFPPPPPPPFPPPP',
      status: 'flagged',
      durationMeanMs: 2400,
    },
    {
      testId: 't-2',
      testPath: 'web/src/lib/format.test.ts',
      testName: 'formatsDurations',
      flakeRate: 0.07,
      wilsonLower: 0.051,
      wilsonUpper: 0.14,
      sampleSize: 90,
      category: 'async/timing',
      durationMeanMs: 800,
    },
    {
      testId: 't-3',
      testPath: 'internal/worker/drain_test.go',
      testName: 'TestDrainIdempotent',
      flakeRate: 0.31,
      wilsonLower: 0.22,
      wilsonUpper: 0.41,
      sampleSize: 64,
      category: 'network',
      durationMeanMs: 1500,
    },
  ];
  return adaptFlakes(rows);
}

describe('<FlakesScreen />', () => {
  it('renders one table row per test with the test names', () => {
    render(<FlakesScreen flakes={threeFlakes()} />);
    expect(screen.getByText('TestPlanDeterministic')).toBeInTheDocument();
    expect(screen.getByText('formatsDurations')).toBeInTheDocument();
    expect(screen.getByText('TestDrainIdempotent')).toBeInTheDocument();
    const rows = document.querySelectorAll('.flake-table tbody tr');
    expect(rows).toHaveLength(3);
  });

  it("shows 3 in the 'Tracked' KPI", () => {
    render(<FlakesScreen flakes={threeFlakes()} />);
    const tracked = screen.getByText('Tracked').closest('.kpi') as HTMLElement;
    expect(tracked).toBeTruthy();
    expect(within(tracked).getByText('3')).toBeInTheDocument();
  });

  it('opens the detail sheet with the test name in the header when a row is clicked', () => {
    render(<FlakesScreen flakes={threeFlakes()} />);
    const row = screen.getByText('TestDrainIdempotent').closest('tr') as HTMLElement;
    fireEvent.click(row);
    // The sheet header renders flake.name; with the row also showing it, there
    // are now >=2 occurrences in the DOM — the sheet appearing is the assertion.
    expect(screen.getAllByText('TestDrainIdempotent').length).toBeGreaterThanOrEqual(2);
  });

  it('shows 0 KPIs and does not produce NaN for an empty flake list', () => {
    render(<FlakesScreen flakes={[]} />);
    const tracked = screen.getByText('Tracked').closest('.kpi') as HTMLElement;
    expect(within(tracked).getByText('0')).toBeInTheDocument();
    // totals.wilsonMean is the only ratio over flakes.length; with 0 flakes it
    // must not surface NaN anywhere in the rendered DOM.
    expect(document.body.textContent).not.toMatch(/NaN/);
  });
});
