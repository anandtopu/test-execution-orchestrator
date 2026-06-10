import { describe, expect, it } from 'vitest';
import {
  CostSummaryQuery,
  FailureClustersQuery,
  FlakesQuery,
  RerunFailedMutation,
  RunByIdQuery,
  RunsQuery,
} from './queries';

// These tests are deliberately structural. They guard against silent renames
// of the GraphQL operation names or the field set the UI depends on.

describe('GraphQL queries', () => {
  it('Runs query selects all fields the UI table consumes', () => {
    for (const f of ['id', 'repoFullName', 'branch', 'commitSha', 'status', 'totalDurationMs', 'startedAt']) {
      expect(RunsQuery).toContain(f);
    }
  });

  it('Run by id selects shards + nested fields for the Gantt', () => {
    for (const f of ['shards', 'predictedDurationMs', 'actualDurationMs', 'testCount', 'workerId', 'failedTestCount']) {
      expect(RunByIdQuery).toContain(f);
    }
  });

  it('Run by id selects predictor calibration + per-shard delta', () => {
    // Assert the nested predictor{...} block distinctly from the flat fields:
    // a plain toContain('predictor') passes trivially because it's a substring
    // of predictorMae/predictorRho, so the nested block could be deleted
    // undetected. Anchor the nested object with the opening brace.
    expect(RunByIdQuery).toMatch(/predictor\s*\{/);
    // Flat run-level predictor fields the home adapter reads first.
    for (const f of ['predictorMae', 'predictorRho']) {
      expect(RunByIdQuery).toContain(f);
    }
    // Nested-block + per-shard fields.
    for (const f of ['mae', 'modelVersion', 'p95DeltaMs', 'deltaMs', 'predictionConfidence']) {
      expect(RunByIdQuery).toContain(f);
    }
  });

  it('FailureClusters query selects representative + occurrences', () => {
    expect(FailureClustersQuery).toContain('representativeMessage');
    expect(FailureClustersQuery).toContain('representativeStack');
    expect(FailureClustersQuery).toContain('occurrences');
  });

  it('FailureClusters query selects the spatial-map + provenance fields', () => {
    // The 1-char fields (x/y/r) are selection tokens, not substrings: a plain
    // toContain('x') passes trivially against "representativeMessage". Anchor to
    // selection-set whitespace (own line) so we actually assert they're selected.
    for (const f of ['x', 'y', 'r']) {
      expect(FailureClustersQuery).toMatch(new RegExp(`(^|\\n)\\s*${f}\\s*(\\n|$)`));
    }
    for (const f of ['category', 'stackFingerprint', 'affectedRuns']) {
      expect(FailureClustersQuery).toContain(f);
    }
  });

  it('Flakes query selects path, name, rate, wilson lower', () => {
    for (const f of ['testPath', 'testName', 'flakeRate', 'wilsonLower', 'sampleSize']) {
      expect(FlakesQuery).toContain(f);
    }
  });

  it('Flakes query selects wilson upper, sparkline + status', () => {
    for (const f of ['wilsonUpper', 'spark', 'status']) {
      expect(FlakesQuery).toContain(f);
    }
  });

  it('Flakes query selects the quarantine + owner fields the adapter maps', () => {
    // ui-clusters-flakes: quarantinedAt drives status='quarantined' +
    // quarantinedDays; ownerTeam seeds the owner avatar.
    for (const f of ['wilsonUpper', 'status', 'quarantinedAt', 'ownerTeam']) {
      expect(FlakesQuery).toContain(f);
    }
  });

  it('RerunFailed mutation takes runId and returns id', () => {
    expect(RerunFailedMutation).toMatch(/mutation\s+RerunFailed\(\$runId:\s*ID!\)/);
    expect(RerunFailedMutation).toContain('rerunFailed(runId: $runId)');
  });

  it('CostSummary query selects every field the dashboard renders', () => {
    for (const f of [
      'weekStart',
      'runs',
      'spotMinutes',
      'onDemandMinutes',
      'totalCost',
      'costPerBuild',
      'spotShare',
    ]) {
      expect(CostSummaryQuery).toContain(f);
    }
    expect(CostSummaryQuery).toMatch(/\$weeks:\s*Int/);
  });

  it('All operations have a name (no anonymous queries)', () => {
    for (const op of [RunsQuery, RunByIdQuery, FailureClustersQuery, FlakesQuery, RerunFailedMutation, CostSummaryQuery]) {
      expect(op).toMatch(/\b(query|mutation)\s+\w+/);
    }
  });
});
