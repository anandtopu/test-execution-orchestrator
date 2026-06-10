import { describe, expect, it } from 'vitest';
import {
  adaptClusters,
  adaptFlakes,
  adaptRun,
  adaptStatus,
  classifyCategory,
  daysSince,
  deriveOwner,
  deriveSpark,
  extractFile,
  hash32,
  type GqlCluster,
  type GqlFlake,
  type GqlRun,
  type GqlShard,
} from './teo-adapt';

// Unit coverage for the GraphQL → view-model adapters. These are pure functions;
// the contract that matters is: backend-supplied fields pass through unchanged,
// and the design-only fields are DERIVED deterministically (stable per id) when
// the backend omits them.

describe('hash32', () => {
  it('is deterministic for a given input', () => {
    expect(hash32('TestFoo')).toBe(hash32('TestFoo'));
  });

  it('is sensitive to input (different strings → different hashes, typically)', () => {
    expect(hash32('TestFoo')).not.toBe(hash32('TestBar'));
  });

  it('returns an unsigned 32-bit integer', () => {
    const h = hash32('some/long.path::TestName');
    expect(h).toBeGreaterThanOrEqual(0);
    expect(h).toBeLessThanOrEqual(0xffffffff);
    expect(Number.isInteger(h)).toBe(true);
  });

  it('handles the empty string without throwing (FNV offset basis)', () => {
    expect(hash32('')).toBe(0x811c9dc5);
  });
});

describe('classifyCategory', () => {
  // Mirrors internal/api/graphql_resolvers.go classifyClusterCategory:
  // panic → timeout → network → race → assertion.
  it('classifies each category from a representative message', () => {
    expect(classifyCategory('runtime panic: nil deref')).toBe('panic');
    expect(classifyCategory('context deadline exceeded')).toBe('timeout');
    expect(classifyCategory('i/o timeout while waiting')).toBe('timeout');
    expect(classifyCategory('dial tcp 10.0.0.1:5432: connection refused')).toBe('network');
    expect(classifyCategory('S3 NoSuchKey: missing object')).toBe('network');
    expect(classifyCategory('WARNING: DATA RACE detected')).toBe('race');
    expect(classifyCategory('go test -race flagged it')).toBe('race');
    expect(classifyCategory('expected 3 got 4')).toBe('assertion');
    expect(classifyCategory('')).toBe('assertion');
  });

  it('is case-insensitive', () => {
    expect(classifyCategory('PANIC: boom')).toBe('panic');
  });

  it('honours Go precedence: panic beats timeout', () => {
    expect(classifyCategory('panic after timeout')).toBe('panic');
  });

  it('honours Go precedence: timeout beats network', () => {
    expect(classifyCategory('timeout: dial tcp failed')).toBe('timeout');
  });

  it('honours Go precedence: network beats race', () => {
    // "connection refused" matches network; "-race" matches race; network wins.
    expect(classifyCategory('connection refused under -race')).toBe('network');
  });
});

describe('extractFile', () => {
  it('pulls the first source frame out of a multi-line stack', () => {
    const stack = [
      'panic: boom',
      '  at runtime.gopanic',
      '  at github.com/teo-dev/teo/internal/foo/bar.go:42',
      '  at testing.tRunner',
    ].join('\n');
    expect(extractFile(stack, 'fallback')).toBe('github.com/teo-dev/teo/internal/foo/bar.go:42');
  });

  it('matches python and ts frames too', () => {
    expect(extractFile('  File "tests/test_x.py:17", in test', 'fb')).toBe('tests/test_x.py:17');
    expect(extractFile('  at src/app.ts:5', 'fb')).toBe('src/app.ts:5');
  });

  it('returns the fallback when no source frame is present', () => {
    expect(extractFile('no frames here', 'sha-abc')).toBe('sha-abc');
    expect(extractFile(null, 'sha-abc')).toBe('sha-abc');
    expect(extractFile(undefined, 'sha-abc')).toBe('sha-abc');
    expect(extractFile('', 'sha-abc')).toBe('sha-abc');
  });
});

describe('deriveOwner', () => {
  it('is stable per test id', () => {
    expect(deriveOwner('pkg/foo::TestA')).toEqual(deriveOwner('pkg/foo::TestA'));
  });

  it('returns two uppercase initials and a known colour bucket', () => {
    const o = deriveOwner('pkg/foo::TestA');
    expect(o.i).toMatch(/^[A-Z]{2}$/);
    expect(['m', 'p', 'd', 's', 'a']).toContain(o.c);
  });
});

describe('deriveSpark', () => {
  it('returns exactly 20 chars of P/F', () => {
    const s = deriveSpark('t1', 0.3);
    expect(s).toHaveLength(20);
    expect(s).toMatch(/^[PF]{20}$/);
  });

  it('places ~round(rate*20) failures', () => {
    expect([...deriveSpark('t-half', 0.5)].filter((c) => c === 'F')).toHaveLength(10);
    expect([...deriveSpark('t-zero', 0)].filter((c) => c === 'F')).toHaveLength(0);
    expect([...deriveSpark('t-all', 1)].filter((c) => c === 'F')).toHaveLength(20);
  });

  it('is deterministic for a given id+rate', () => {
    expect(deriveSpark('t-x', 0.25)).toBe(deriveSpark('t-x', 0.25));
  });
});

describe('adaptClusters', () => {
  it('returns [] for null/undefined/empty input', () => {
    expect(adaptClusters(null)).toEqual([]);
    expect(adaptClusters(undefined)).toEqual([]);
    expect(adaptClusters([])).toEqual([]);
  });

  it('passes through backend x/y/r/category and splits the stack', () => {
    const rows: GqlCluster[] = [
      {
        id: 'c1',
        representativeMessage: 'expected 1 got 2',
        representativeStack: 'frame a\nframe b',
        occurrences: 5,
        firstSeen: '2026-06-01T00:00:00Z',
        lastSeen: '2026-06-02T12:00:00Z',
        x: 0.25,
        y: 0.75,
        r: 22,
        category: 'assertion',
        stackFingerprint: 'fp1',
        affectedRuns: 3,
      },
    ];
    const [c] = adaptClusters(rows);
    expect(c.x).toBe(0.25);
    expect(c.y).toBe(0.75);
    expect(c.r).toBe(22);
    expect(c.category).toBe('assertion');
    expect(c.title).toBe('expected 1 got 2');
    expect(c.occurrences).toBe(5);
    expect(c.affectedRuns).toBe(3);
    expect(c.firstSeen).toBe('2026-06-01');
    expect(c.lastSeen).toBe('2026-06-02');
    expect(c.stack).toEqual(['frame a', 'frame b']);
    // No schema source yet → empty arrays.
    expect(c.tests).toEqual([]);
    expect(c.affectedRunIds).toEqual([]);
    expect(c.related).toEqual([]);
  });

  it('derives x/y/r and category when the backend omits them', () => {
    const rows: GqlCluster[] = [
      {
        id: 'c1',
        representativeMessage: 'panic: boom',
        representativeStack: null,
        occurrences: 10,
        firstSeen: null,
        lastSeen: '2026-06-05T00:00:00Z',
        x: null,
        y: null,
        r: null,
        category: null,
        affectedRuns: null,
      },
      {
        id: 'c2',
        representativeMessage: 'expected x',
        occurrences: 1,
        lastSeen: '2026-06-01T00:00:00Z',
        x: null,
        y: null,
        r: null,
        category: null,
      },
    ];
    const out = adaptClusters(rows);
    const c1 = out.find((c) => c.id === 'c1')!;
    expect(c1.category).toBe('panic'); // derived via classifyCategory
    // Fallbacks stay within the documented ranges.
    for (const c of out) {
      expect(c.x).toBeGreaterThanOrEqual(0);
      expect(c.x).toBeLessThanOrEqual(1);
      expect(c.y).toBeGreaterThanOrEqual(0);
      expect(c.y).toBeLessThanOrEqual(1);
      expect(c.r).toBeGreaterThanOrEqual(8);
      expect(c.r).toBeLessThanOrEqual(40);
    }
    expect(c1.affectedRuns).toBe(0);
  });
});

describe('adaptFlakes', () => {
  it('returns [] for null/undefined/empty input', () => {
    expect(adaptFlakes(null)).toEqual([]);
    expect(adaptFlakes(undefined)).toEqual([]);
    expect(adaptFlakes([])).toEqual([]);
  });

  it('passes through wilsonUpper and converts ms→s', () => {
    const rows: GqlFlake[] = [
      {
        testId: 't1',
        testPath: 'pkg/foo_test.go',
        testName: 'TestFoo',
        flakeRate: 0.2,
        wilsonLower: 0.1,
        wilsonUpper: 0.35,
        sampleSize: 100,
        category: 'async/timing',
        spark: 'PPPFPPPPPFPPPPPPPPPP',
        status: 'flagged',
        durationMeanMs: 2500,
      },
    ];
    const [f] = adaptFlakes(rows);
    expect(f.wHi).toBe(0.35); // passthrough, not the rate*1.4 fallback
    expect(f.wLo).toBe(0.1);
    expect(f.rate).toBe(0.2);
    expect(f.samples).toBe(100);
    expect(f.durMean).toBe(2.5); // ms → s
    expect(f.file).toBe('pkg/foo_test.go');
    expect(f.name).toBe('TestFoo');
    expect(f.quarantinedDays).toBe(0);
    expect(f.spark).toHaveLength(20);
  });

  it('falls back to rate*1.4 (clamped) for wHi when wilsonUpper is null', () => {
    const [f] = adaptFlakes([{ testId: 't2', flakeRate: 0.5, wilsonUpper: null }]);
    expect(f.wHi).toBeCloseTo(0.7, 6); // 0.5 * 1.4
  });

  it('clamps the wHi fallback to <= 1', () => {
    const [f] = adaptFlakes([{ testId: 't3', flakeRate: 0.9, wilsonUpper: null }]);
    expect(f.wHi).toBe(1); // 0.9 * 1.4 = 1.26 → clamped
  });

  it('derives a stable owner and a normalized 20-char spark', () => {
    const [f] = adaptFlakes([{ testId: 't4', flakeRate: 0.1 }]);
    expect(f.owner).toEqual(deriveOwner('t4'));
    expect(f.spark).toHaveLength(20);
    expect(f.spark).toMatch(/^[PFS]{20}$/);
    expect(f.name).toBe('t4'); // testName absent → falls back to testId
  });
});

// ---- ui-clusters-flakes spec cases ----------------------------------------
// The block below covers the exact contract the gap's spec enumerates: stack
// → file extraction, category bucketing for the canonical messages, the x/y/r
// finite-range guarantee, the empty-input no-throw, the quarantinedAt→status
// and ownerTeam mappings, and the deterministic-per-id sparkline.

describe('adaptClusters — spec contract', () => {
  it('maps representativeMessage→title and copies occurrences/firstSeen/lastSeen verbatim', () => {
    const rows: GqlCluster[] = [
      {
        id: 'c1',
        representativeMessage: 'AssertionError: expected 200, got 503',
        representativeStack: 'frame',
        occurrences: 17,
        firstSeen: '2026-05-01',
        lastSeen: '2026-05-09',
        category: 'assertion',
      },
    ];
    const [c] = adaptClusters(rows);
    expect(c.title).toBe('AssertionError: expected 200, got 503');
    expect(c.occurrences).toBe(17);
    expect(c.firstSeen).toBe('2026-05-01'); // already YYYY-MM-DD → verbatim
    expect(c.lastSeen).toBe('2026-05-09');
  });

  it("extracts file='internal/api/runs_handler.go:142' and splits the stack into a non-empty string[]", () => {
    const stack = [
      'goroutine 42 [running]:',
      '  at internal/api/runs_handler.go:142 +0x1f',
      '  testing.tRunner',
    ].join('\n');
    const [c] = adaptClusters([
      { id: 'c1', representativeMessage: 'boom', representativeStack: stack, occurrences: 1 },
    ]);
    expect(c.file).toBe('internal/api/runs_handler.go:142');
    expect(Array.isArray(c.stack)).toBe(true);
    expect(c.stack.length).toBeGreaterThan(0);
    expect(c.stack.every((l) => typeof l === 'string')).toBe(true);
  });

  it('buckets categories from the representative message (backend omits category)', () => {
    const cases: Array<[string, string]> = [
      ['panic: runtime error', 'panic'],
      ['context deadline exceeded', 'timeout'],
      ['WARNING: DATA RACE', 'race'],
      ['dial tcp 10.0.0.1:5432: connection refused', 'network'],
      ['expected 200, got 503', 'assertion'],
    ];
    for (const [msg, want] of cases) {
      const [c] = adaptClusters([
        { id: 'x', representativeMessage: msg, occurrences: 1, category: null },
      ]);
      expect(c.category).toBe(want);
    }
  });

  it('derives finite x∈[0,1], y∈[0,1], r∈[8,40] across a multi-row page', () => {
    const rows: GqlCluster[] = Array.from({ length: 5 }, (_, i) => ({
      id: `c${i}`,
      representativeMessage: `m${i}`,
      occurrences: (i + 1) * 3,
      lastSeen: `2026-05-0${i + 1}T00:00:00Z`,
    }));
    const out = adaptClusters(rows);
    for (const c of out) {
      expect(Number.isFinite(c.x)).toBe(true);
      expect(Number.isFinite(c.y)).toBe(true);
      expect(Number.isFinite(c.r)).toBe(true);
      expect(c.x).toBeGreaterThanOrEqual(0);
      expect(c.x).toBeLessThanOrEqual(1);
      expect(c.y).toBeGreaterThanOrEqual(0);
      expect(c.y).toBeLessThanOrEqual(1);
      expect(c.r).toBeGreaterThanOrEqual(8);
      expect(c.r).toBeLessThanOrEqual(40);
    }
  });

  it('returns [] for an empty input array without throwing', () => {
    expect(() => adaptClusters([])).not.toThrow();
    expect(adaptClusters([])).toEqual([]);
  });
});

describe('adaptFlakes — spec contract', () => {
  it('maps the core fields verbatim and passes category through', () => {
    const [f] = adaptFlakes([
      {
        testId: 't1',
        testPath: 'pkg/foo_test.go',
        testName: 'TestFoo',
        flakeRate: 0.22,
        wilsonLower: 0.11,
        wilsonUpper: 0.4,
        sampleSize: 88,
        category: 'race',
        durationMeanMs: 1200,
      },
    ]);
    expect(f.file).toBe('pkg/foo_test.go');
    expect(f.name).toBe('TestFoo');
    expect(f.rate).toBe(0.22);
    expect(f.wLo).toBe(0.11);
    expect(f.wHi).toBe(0.4);
    expect(f.samples).toBe(88);
    expect(f.category).toBe('race');
    expect(f.durMean).toBe(1.2);
  });

  it('wilsonUpper present → wHi==wilsonUpper; null → wHi>rate (fallback)', () => {
    const [present] = adaptFlakes([{ testId: 'a', flakeRate: 0.2, wilsonUpper: 0.3 }]);
    expect(present.wHi).toBe(0.3);
    const [missing] = adaptFlakes([{ testId: 'b', flakeRate: 0.2, wilsonUpper: null }]);
    expect(missing.wHi).toBeGreaterThan(0.2);
  });

  it("quarantinedAt set → status 'quarantined' and quarantinedDays>0", () => {
    const tenDaysAgo = new Date(Date.now() - 10 * 86_400_000).toISOString();
    const [f] = adaptFlakes([
      { testId: 'q1', flakeRate: 0.2, wilsonLower: 0.12, quarantinedAt: tenDaysAgo },
    ]);
    expect(f.status).toBe('quarantined');
    expect(f.quarantinedDays).toBeGreaterThan(0);
  });

  it("wilsonLower>0.05 & not quarantined → status 'flagged'", () => {
    const [f] = adaptFlakes([
      { testId: 'g1', flakeRate: 0.2, wilsonLower: 0.09, status: 'stable', quarantinedAt: null },
    ]);
    expect(f.status).toBe('flagged');
    expect(f.quarantinedDays).toBe(0);
  });

  it('spark is exactly 20 chars of [PFS] and is deterministic for a fixed testId', () => {
    const first = adaptFlakes([{ testId: 'det-1', flakeRate: 0.35 }])[0].spark;
    const second = adaptFlakes([{ testId: 'det-1', flakeRate: 0.35 }])[0].spark;
    expect(first).toHaveLength(20);
    expect(first).toMatch(/^[PFS]{20}$/);
    expect(first).toBe(second);
  });

  it('owner.i is 1-2 uppercase chars and owner.c is one of m/p/d/s/a', () => {
    for (const row of [
      { testId: 'o1', flakeRate: 0.1 },
      { testId: 'o2', flakeRate: 0.1, ownerTeam: '@teo-dev/platform' },
      { testId: 'o3', flakeRate: 0.1, ownerTeam: 'x' },
    ] satisfies GqlFlake[]) {
      const [f] = adaptFlakes([row]);
      expect(f.owner.i).toMatch(/^[A-Z]{1,2}$/);
      expect(['m', 'p', 'd', 's', 'a']).toContain(f.owner.c);
    }
  });

  it('durMean defaults to 0 when durationMeanMs is absent', () => {
    const [f] = adaptFlakes([{ testId: 'd1', flakeRate: 0.1 }]);
    expect(f.durMean).toBe(0);
  });
});

describe('daysSince', () => {
  it('returns whole days since an ISO timestamp', () => {
    const now = Date.parse('2026-06-10T00:00:00Z');
    expect(daysSince('2026-06-01T00:00:00Z', now)).toBe(9);
  });
  it('returns at least 1 for a same-day quarantine', () => {
    const now = Date.parse('2026-06-10T12:00:00Z');
    expect(daysSince('2026-06-10T00:00:00Z', now)).toBe(1);
  });
  it('returns 0 for absent/unparseable input', () => {
    expect(daysSince(null)).toBe(0);
    expect(daysSince(undefined)).toBe(0);
    expect(daysSince('not-a-date')).toBe(0);
  });
});

// ====================================================
// Run adapter (ui-home-calibration) — adaptStatus / adaptRun / buildRunPredictor
// ====================================================

describe('adaptStatus', () => {
  it('maps backend success vocabulary to pass', () => {
    for (const s of ['succeeded', 'passed', 'pass', 'PASSED']) {
      expect(adaptStatus(s)).toBe('pass');
    }
  });
  it('maps failure / terminal-loss vocabulary to fail', () => {
    for (const s of ['failed', 'errored', 'timed_out', 'lost', 'cancelled']) {
      expect(adaptStatus(s)).toBe('fail');
    }
  });
  it('maps preemption vocabulary to preempt', () => {
    for (const s of ['preempted', 'preempt']) {
      expect(adaptStatus(s)).toBe('preempt');
    }
  });
  it('maps in-flight + unknown + nullish status to running (styled default)', () => {
    for (const s of ['running', 'pending', 'queued', 'dispatched', 'whatever', '', null, undefined]) {
      expect(adaptStatus(s)).toBe('running');
    }
  });
});

// Build a GqlShard with sane defaults so each test overrides only what it asserts.
function shard(over: Partial<GqlShard> = {}): GqlShard {
  return {
    id: over.id ?? `s-${over.index ?? 0}`,
    index: over.index ?? 0,
    status: over.status ?? 'running',
    workerId: over.workerId ?? 'w-1',
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

describe('adaptRun', () => {
  it('rolls up pass/fail/running counts, test count and converts ms→s', () => {
    const gql: GqlRun = {
      id: 'run-1',
      status: 'running',
      startedAt: '2026-06-10T00:00:00Z',
      totalDurationMs: 90_000,
      shards: [
        shard({ index: 0, status: 'succeeded', predictedDurationMs: 30_000, actualDurationMs: 28_500, testCount: 10, startedAt: '2026-06-10T00:00:00Z' }),
        shard({ index: 1, status: 'failed', predictedDurationMs: 40_000, actualDurationMs: 52_000, testCount: 5, startedAt: '2026-06-10T00:00:10Z' }),
        shard({ index: 2, status: 'running', predictedDurationMs: 20_000, testCount: 7 }),
      ],
    };
    const { run, shards } = adaptRun(gql);
    expect(run.testCount).toBe(22);
    expect(run.passed).toBe(1);
    expect(run.failed).toBe(1);
    expect(run.running).toBe(1);
    // predictedTotalSec = max shard predicted (40_000ms → 40s)
    expect(run.predictedTotalSec).toBe(40);
    // elapsed from totalDurationMs (90_000ms → 90s)
    expect(run.elapsedSec).toBe(90);
    // ms→s rounding on shards
    expect(shards[0].pred).toBe(30);
    expect(shards[0].actual).toBe(29); // 28_500 → 28.5 → round 29
    expect(shards[0].end).toBe(29);
    // running shard has no end
    expect(shards[2].end).toBeNull();
    // status passed through raw for the header badge
    expect(run.status).toBe('running');
  });

  it('computes elapsed from startedAt when totalDurationMs is absent', () => {
    const now = Date.parse('2026-06-10T00:02:00Z');
    const gql: GqlRun = {
      id: 'run-2',
      status: 'running',
      startedAt: '2026-06-10T00:00:00Z',
      totalDurationMs: 0,
      shards: [],
    };
    expect(adaptRun(gql, now).run.elapsedSec).toBe(120);
  });

  it('derives p95ShardSec from finished shard durations', () => {
    const gql: GqlRun = {
      id: 'run-3',
      status: 'succeeded',
      shards: [
        shard({ index: 0, status: 'succeeded', actualDurationMs: 10_000 }),
        shard({ index: 1, status: 'succeeded', actualDurationMs: 20_000 }),
        shard({ index: 2, status: 'succeeded', actualDurationMs: 30_000 }),
      ],
    };
    expect(adaptRun(gql).run.p95ShardSec).toBe(30);
  });

  it('never throws and never returns NaN on an all-null GqlRun', () => {
    const gql = { id: 'run-empty', status: 'pending' } as GqlRun;
    const { run, shards } = adaptRun(gql);
    expect(shards).toEqual([]);
    expect(run.testCount).toBe(0);
    expect(run.predictedTotalSec).toBe(0);
    expect(run.p95ShardSec).toBe(0);
    expect(Number.isNaN(run.elapsedSec)).toBe(false);
    expect(run.predictor.mae).toBe(0);
    expect(run.predictor.rho).toBe(0);
    expect(run.predictor.p50Delta).toBe(0);
    expect(run.predictor.p95Delta).toBe(0);
  });
});

describe('buildRunPredictor (via adaptRun)', () => {
  it('prefers flat predictorMae/Rho and converts mae ms→s', () => {
    const gql: GqlRun = {
      id: 'run-p1',
      status: 'succeeded',
      predictorMae: 4200, // ms
      predictorRho: 0.91,
      modelVersion: 'flat-model-v3',
      predictor: { mae: 9999, rho: 0.1, modelVersion: 'nested-should-lose' },
      shards: [],
    };
    const p = adaptRun(gql).run.predictor;
    expect(p.mae).toBeCloseTo(4.2, 5); // 4200ms → 4.2s
    expect(p.rho).toBe(0.91);
    expect(p.modelVersion).toBe('flat-model-v3');
  });

  it('falls back to the nested predictor object then 0', () => {
    const nested: GqlRun = {
      id: 'run-p2',
      status: 'succeeded',
      predictor: { mae: 3000, rho: 0.5, modelVersion: 'nested-v1' },
      shards: [],
    };
    const np = adaptRun(nested).run.predictor;
    expect(np.mae).toBeCloseTo(3.0, 5);
    expect(np.rho).toBe(0.5);
    expect(np.modelVersion).toBe('nested-v1');

    const none = { id: 'run-p3', status: 'succeeded', shards: [] } as GqlRun;
    const zp = adaptRun(none).run.predictor;
    expect(zp.mae).toBe(0);
    expect(zp.rho).toBe(0);
    expect(zp.modelVersion).toBe('');
  });

  it('computes p50/p95 fractional deltas from finished shards', () => {
    // pred=100s, actual=150s → fractional (150-100)/100 = +0.5 for both shards.
    const gql: GqlRun = {
      id: 'run-p4',
      status: 'succeeded',
      shards: [
        shard({ index: 0, status: 'succeeded', predictedDurationMs: 100_000, actualDurationMs: 150_000 }),
        shard({ index: 1, status: 'succeeded', predictedDurationMs: 100_000, actualDurationMs: 150_000 }),
      ],
    };
    const p = adaptRun(gql).run.predictor;
    expect(p.p50Delta).toBeCloseTo(0.5, 5);
    expect(p.p95Delta).toBeCloseTo(0.5, 5);
  });
});
