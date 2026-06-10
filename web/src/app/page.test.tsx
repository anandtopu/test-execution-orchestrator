import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { cleanup, render, screen } from '@testing-library/react';

// ui-home-calibration: the home screen (/) is an async Server Component that
// resolves the latest run and feeds LiveRunDetail. These tests mock gqlFetch so
// we can drive both the happy path (a run exists) and — the spec's headline
// case — the EMPTY path (latest-run fetch returns null), asserting the home
// screen renders the empty-state instead of throwing.

const gqlFetch = vi.fn();
vi.mock('@/lib/graphql', () => ({
  gqlFetch: (...args: unknown[]) => gqlFetch(...args),
}));

// LiveRunDetail is a client component with effects/polling we don't exercise
// here; stub it to a sentinel so the page test stays a pure routing/empty-state
// assertion.
vi.mock('@/components/teo/LiveRunDetail', () => ({
  LiveRunDetail: () => <div data-testid="live-run-detail">live</div>,
}));

import HomePage from './page';
import { RunsQuery, RunByIdQuery, FailureClustersQuery } from '@/lib/queries';

describe('HomePage (/) — ui-home-calibration', () => {
  beforeEach(() => {
    gqlFetch.mockReset();
  });
  afterEach(cleanup);

  it('renders the empty-state (not a crash) when the latest-run fetch returns null', async () => {
    // RunsQuery returns an empty list → no latest id → fetchLatestRun returns null.
    gqlFetch.mockImplementation(async (query: string) => {
      if (query === RunsQuery) return { runs: [] };
      return null;
    });

    const ui = await HomePage();
    render(ui);

    expect(screen.getByText(/No runs yet/i)).toBeInTheDocument();
    expect(screen.queryByTestId('live-run-detail')).not.toBeInTheDocument();
  });

  it('renders the empty-state when the latest-run fetch REJECTS (backend outage)', async () => {
    // A hard connection failure makes gqlFetch reject; HomePage must catch it and
    // degrade to the empty state rather than throwing through the route.
    gqlFetch.mockRejectedValue(new Error('connection refused'));

    const ui = await HomePage();
    render(ui);
    expect(screen.getByText(/No runs yet/i)).toBeInTheDocument();
  });

  it('renders LiveRunDetail when a latest run exists', async () => {
    gqlFetch.mockImplementation(async (query: string) => {
      if (query === RunsQuery) return { runs: [{ id: 'run-1' }] };
      if (query === RunByIdQuery) return { run: { id: 'run-1', status: 'running', shards: [] } };
      if (query === FailureClustersQuery) return { failureClusters: [] };
      return null;
    });

    const ui = await HomePage();
    render(ui);
    expect(screen.getByTestId('live-run-detail')).toBeInTheDocument();
    expect(screen.queryByText(/No runs yet/i)).not.toBeInTheDocument();
  });
});
