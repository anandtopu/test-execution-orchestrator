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

  it('FailureClusters query selects representative + occurrences', () => {
    expect(FailureClustersQuery).toContain('representativeMessage');
    expect(FailureClustersQuery).toContain('representativeStack');
    expect(FailureClustersQuery).toContain('occurrences');
  });

  it('Flakes query selects path, name, rate, wilson lower', () => {
    for (const f of ['testPath', 'testName', 'flakeRate', 'wilsonLower', 'sampleSize']) {
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
