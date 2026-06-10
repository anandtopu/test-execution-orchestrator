import { afterEach, describe, expect, it } from 'vitest';
import { cleanup, render, screen, within } from '@testing-library/react';
import { RunDetailScreen } from './RunDetail';
import { adaptRun, type GqlRun, type GqlShard } from '@/lib/teo-adapt';

// ui-home-calibration: RunDetailScreen is prop-driven (real GraphQL data adapted
// via teo-adapt; no TEO_DATA mock). These tests feed it a small adapted fixture
// and assert the marquee Predictor accuracy panel reflects the *real* run-level
// MAE / modelVersion and renders one predict-line per FINISHED shard with the
// correct signed delta — i.e. the overlay is wired to live data, not the mock.

function gqlShard(over: Partial<GqlShard> = {}): GqlShard {
  return {
    id: over.id ?? `s-${over.index ?? 0}`,
    index: over.index ?? 0,
    status: over.status ?? 'running',
    workerId: over.workerId ?? 'worker-r-1',
    predictedDurationMs: over.predictedDurationMs ?? null,
    actualDurationMs: over.actualDurationMs ?? null,
    testCount: over.testCount ?? 0,
    startedAt: over.startedAt ?? null,
    finishedAt: over.finishedAt ?? null,
    deltaMs: over.deltaMs ?? null,
    predictionConfidence: over.predictionConfidence ?? null,
    modelVersion: over.modelVersion ?? null,
  };
}

// Two shards: shard 0 finished (succeeded, actual > pred → +Ns delta), shard 1
// still running (no actual → must NOT appear in the predictor accuracy lines).
function fixture(): GqlRun {
  return {
    id: 'run-abcdef12',
    repoFullName: 'owner/sample',
    branch: 'main',
    commitSha: 'deadbeefcafe',
    status: 'running',
    startedAt: '2026-06-10T00:00:00Z',
    totalDurationMs: 90_000,
    predictorMae: 4200, // ms → 4.2s after adaptRun's ms→s conversion
    predictorRho: 0.91,
    modelVersion: 'lightgbm-2026.06.01',
    shards: [
      gqlShard({
        index: 0,
        status: 'succeeded',
        predictedDurationMs: 30_000, // 30s predicted
        actualDurationMs: 36_000, // 36s actual → delta +6s
        testCount: 12,
        startedAt: '2026-06-10T00:00:00Z',
        predictionConfidence: 0.82,
      }),
      gqlShard({
        index: 1,
        status: 'running',
        predictedDurationMs: 20_000,
        testCount: 7,
        startedAt: '2026-06-10T00:00:05Z',
      }),
    ],
  };
}

describe('<RunDetailScreen /> — predictor calibration overlay', () => {
  afterEach(cleanup);

  it('renders the real run-level MAE (4.2s) and modelVersion in the predictor panel', () => {
    const { run, shards } = adaptRun(fixture());
    render(<RunDetailScreen run={run} shards={shards} clusters={[]} />);

    // The Predictor accuracy panel head shows "MAE 4.2s · ρ 0.91 · <model>".
    const panel = screen.getByText('Predictor accuracy').closest('.panel') as HTMLElement;
    expect(panel).toBeTruthy();
    const head = within(panel);
    expect(head.getByText('4.2s')).toBeInTheDocument();
    expect(head.getByText('0.91')).toBeInTheDocument();
    // modelVersion text surfaces verbatim in the panel head.
    expect(head.getByText('lightgbm-2026.06.01')).toBeInTheDocument();
  });

  it('renders exactly one predict-line per FINISHED shard (running shard excluded)', () => {
    const { run, shards } = adaptRun(fixture());
    const { container } = render(<RunDetailScreen run={run} shards={shards} clusters={[]} />);
    // Only shard 0 finished → exactly one predict-line.
    const lines = container.querySelectorAll('.predict-line');
    expect(lines).toHaveLength(1);
    // The line is labelled for shard #00 with its test count.
    expect(within(lines[0] as HTMLElement).getByText(/#00/)).toBeInTheDocument();
    expect(lines[0].textContent).toContain('12 tests');
  });

  it('shows the correct signed delta (+6s) for a shard that ran slower than predicted', () => {
    const { run, shards } = adaptRun(fixture());
    const { container } = render(<RunDetailScreen run={run} shards={shards} clusters={[]} />);
    const line = container.querySelector('.predict-line') as HTMLElement;
    // actual 36s − pred 30s = +6s; the leading '+' must be present.
    expect(line.textContent).toContain('+6s');
    expect(line.textContent).not.toContain('-6s');
  });

  it('shows a negative delta (−Ns) when a shard beat its prediction', () => {
    const gql: GqlRun = {
      id: 'run-fast',
      status: 'succeeded',
      startedAt: '2026-06-10T00:00:00Z',
      predictorMae: 1500,
      modelVersion: 'heuristic',
      shards: [
        gqlShard({
          index: 0,
          status: 'succeeded',
          predictedDurationMs: 40_000, // 40s predicted
          actualDurationMs: 28_000, // 28s actual → delta −12s
          testCount: 9,
        }),
      ],
    };
    const { run, shards } = adaptRun(gql);
    const { container } = render(<RunDetailScreen run={run} shards={shards} clusters={[]} />);
    const line = container.querySelector('.predict-line') as HTMLElement;
    expect(line.textContent).toContain('-12s');
    expect(line.textContent).not.toContain('+12s');
  });

  it('surfaces per-shard prediction confidence on the finished line when present', () => {
    const { run, shards } = adaptRun(fixture());
    const { container } = render(<RunDetailScreen run={run} shards={shards} clusters={[]} />);
    const line = container.querySelector('.predict-line') as HTMLElement;
    // predictionConfidence 0.82 → "conf 82%".
    expect(line.textContent).toContain('conf 82%');
  });

  it('renders the empty-tab / no-crash state when fed a run with no finished shards', () => {
    const gql: GqlRun = {
      id: 'run-empty',
      status: 'pending',
      shards: [gqlShard({ index: 0, status: 'pending', predictedDurationMs: 10_000, testCount: 3 })],
    };
    const { run, shards } = adaptRun(gql);
    const { container } = render(<RunDetailScreen run={run} shards={shards} clusters={[]} />);
    // No finished shards → no predict-lines, but the panel still renders (MAE em-dash path).
    expect(container.querySelectorAll('.predict-line')).toHaveLength(0);
    expect(screen.getByText('Predictor accuracy')).toBeInTheDocument();
    // MAE defaulted to 0 → "0.0s" (never NaN).
    const panel = screen.getByText('Predictor accuracy').closest('.panel') as HTMLElement;
    expect(within(panel).getByText('0.0s')).toBeInTheDocument();
  });
});
