import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { act, cleanup, render, screen } from '@testing-library/react';
import { LiveRunShards, type LiveRun } from './LiveRunShards';

const seed: LiveRun = {
  id: 'run-1',
  status: 'running',
  shards: [
    {
      id: 's-0',
      index: 0,
      status: 'running',
      predictedDurationMs: 5000,
      testCount: 10,
      // ui-home-calibration: per-shard calibration metadata.
      predictionConfidence: 0.78,
      modelVersion: 'lightgbm-2026.06.01',
    },
    {
      id: 's-1',
      index: 1,
      status: 'pending',
      predictedDurationMs: 8000,
      testCount: 12,
      predictionConfidence: 0.64,
      modelVersion: 'lightgbm-2026.06.01',
    },
  ],
};

describe('<LiveRunShards />', () => {
  beforeEach(() => {
    vi.useFakeTimers();
  });
  afterEach(() => {
    vi.useRealTimers();
    cleanup();
  });

  it('renders the seeded shards on first paint', () => {
    render(<LiveRunShards initial={seed} fetcher={vi.fn()} />);
    expect(screen.getByTestId('live-run-shards')).toHaveAttribute('data-status', 'running');
    expect(screen.getByTestId('gantt-row-0')).toHaveTextContent('10 tests');
    expect(screen.getByTestId('gantt-row-1')).toHaveTextContent('12 tests');
  });

  it('polls every pollMs while the run is non-terminal', async () => {
    const fetcher = vi.fn().mockResolvedValue({
      ...seed,
      shards: [{ ...seed.shards[0], status: 'succeeded', actualDurationMs: 4200 }, seed.shards[1]],
    });
    render(<LiveRunShards initial={seed} pollMs={2000} fetcher={fetcher} />);
    expect(fetcher).not.toHaveBeenCalled();

    await act(async () => {
      await vi.advanceTimersByTimeAsync(2000);
    });
    expect(fetcher).toHaveBeenCalledTimes(1);
    expect(fetcher).toHaveBeenCalledWith('run-1');

    await act(async () => {
      await vi.advanceTimersByTimeAsync(4000);
    });
    expect(fetcher).toHaveBeenCalledTimes(3);
  });

  it('stops polling once the status reaches terminal', async () => {
    const fetcher = vi
      .fn()
      .mockResolvedValueOnce({ ...seed, status: 'succeeded' });
    render(<LiveRunShards initial={seed} pollMs={1000} fetcher={fetcher} />);

    await act(async () => {
      await vi.advanceTimersByTimeAsync(1000);
    });
    // The first tick fired and updated state to succeeded.
    expect(fetcher).toHaveBeenCalledTimes(1);
    expect(screen.getByTestId('live-run-shards')).toHaveAttribute('data-status', 'succeeded');

    await act(async () => {
      await vi.advanceTimersByTimeAsync(5000);
    });
    // No more calls should have happened.
    expect(fetcher).toHaveBeenCalledTimes(1);
  });

  it('does not start polling at all when the initial run is already terminal', async () => {
    const fetcher = vi.fn();
    render(<LiveRunShards initial={{ ...seed, status: 'failed' }} pollMs={500} fetcher={fetcher} />);
    await act(async () => {
      await vi.advanceTimersByTimeAsync(2000);
    });
    expect(fetcher).not.toHaveBeenCalled();
  });

  it('shows an empty-state when there are no shards', () => {
    render(<LiveRunShards initial={{ ...seed, shards: [] }} fetcher={vi.fn()} />);
    expect(screen.getByText('No shards yet.')).toBeInTheDocument();
  });

  // ui-home-calibration: a FINISHED shard must render the predicted band AND the
  // actual bar as two DISTINCT elements — it is no longer a single
  // `actual ?? predicted` bar. The seeded prediction confidence must also surface
  // in the row when present.
  it('renders both the predicted band and a distinct actual bar for a finished shard', () => {
    const finished: LiveRun = {
      ...seed,
      shards: [
        {
          ...seed.shards[0],
          status: 'succeeded',
          predictedDurationMs: 5000,
          actualDurationMs: 4200, // distinct from predicted → two visible widths
        },
      ],
    };
    render(<LiveRunShards initial={{ ...finished, status: 'succeeded' }} fetcher={vi.fn()} />);

    // Two distinct testids: a predicted band and the actual bar.
    const pred = screen.getByTestId('gantt-pred-0') as HTMLElement;
    const bar = screen.getByTestId('gantt-bar-0') as HTMLElement;
    expect(pred).toBeInTheDocument();
    expect(bar).toBeInTheDocument();
    // The predicted band is sized off the predicted ms (5000 == maxMs → 100%);
    // the actual bar is sized off the actual ms (4200/5000 → 84%). They differ,
    // proving it is a true predicted-vs-observed view, not one fused bar.
    expect(pred.style.width).toBe('100%');
    expect(bar.style.width).toBe('84%');
    expect(pred.style.width).not.toBe(bar.style.width);
  });

  it('surfaces the prediction confidence in a shard row when present', () => {
    render(<LiveRunShards initial={seed} fetcher={vi.fn()} />);
    // predictionConfidence 0.78 → "conf 78%" rendered in its own testid.
    const conf = screen.getByTestId('gantt-conf-0');
    expect(conf).toHaveTextContent('conf 78%');
  });
});
