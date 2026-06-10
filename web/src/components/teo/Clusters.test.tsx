import { describe, expect, it } from 'vitest';
import { render, screen, fireEvent, within } from '@testing-library/react';
import { ClustersScreen } from './Clusters';
import { adaptClusters, type GqlCluster } from '@/lib/teo-adapt';

// ui-clusters-flakes: the screen is now prop-driven (no TEO_DATA mock). These
// tests feed it adapter output built from GraphQL-shaped rows — the same path
// the /clusters page server component uses — and assert the list/detail wiring.

function twoClusters() {
  const rows: GqlCluster[] = [
    {
      id: 'fc-001',
      representativeMessage: 'AssertionError: expected 200, got 503',
      representativeStack: 'frame a\ninternal/api/runs_handler.go:142\nframe c',
      occurrences: 12,
      firstSeen: '2026-06-01T00:00:00Z',
      lastSeen: '2026-06-08T00:00:00Z',
      category: 'assertion',
      stackFingerprint: 'fp-1',
      affectedRuns: 4,
    },
    {
      id: 'fc-002',
      representativeMessage: 'panic: runtime error: invalid memory address',
      representativeStack: 'goroutine 1\ninternal/worker/worker.go:88',
      occurrences: 31,
      firstSeen: '2026-06-02T00:00:00Z',
      lastSeen: '2026-06-09T00:00:00Z',
      category: 'panic',
      stackFingerprint: 'fp-2',
      affectedRuns: 7,
    },
  ];
  return adaptClusters(rows);
}

describe('<ClustersScreen />', () => {
  it('renders both cluster titles and the count badge', () => {
    render(<ClustersScreen clusters={twoClusters()} />);
    // Titles appear in the list (and possibly the detail panel) → use getAllByText.
    expect(screen.getAllByText('AssertionError: expected 200, got 503').length).toBeGreaterThan(0);
    expect(screen.getAllByText('panic: runtime error: invalid memory address').length).toBeGreaterThan(0);
    // The list head shows the cluster count.
    expect(screen.getByText('2')).toBeInTheDocument();
  });

  it('updates the detail panel when a different row is clicked', () => {
    render(<ClustersScreen clusters={twoClusters()} />);
    // Detail starts on the first cluster (selectedId defaults to clusters[0].id).
    const detail = document.querySelector('.cluster-detail') as HTMLElement;
    expect(detail).toBeTruthy();
    expect(within(detail).getByText('AssertionError: expected 200, got 503')).toBeInTheDocument();

    // Click the second list row (the one whose title is the panic message).
    const secondRow = screen
      .getAllByText('panic: runtime error: invalid memory address')
      .map((el) => el.closest('.cluster-row'))
      .find((el): el is HTMLElement => el !== null);
    expect(secondRow).toBeTruthy();
    fireEvent.click(secondRow!);

    expect(within(detail).getByText('panic: runtime error: invalid memory address')).toBeInTheDocument();
  });

  it('renders the empty-state copy and does not throw on zero rows', () => {
    expect(() => render(<ClustersScreen clusters={[]} />)).not.toThrow();
    expect(screen.getByText(/No failure clusters/i)).toBeInTheDocument();
    // The "Failure clusters" heading still renders in the empty state.
    expect(screen.getByText('Failure clusters')).toBeInTheDocument();
  });
});
