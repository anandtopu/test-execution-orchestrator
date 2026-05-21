// ====================================================
// TEO — Mock data (ported from the design handoff data.js)
// Realistic-looking but invented test/repo names.
//
// The marquee TEO screens render from this fixture so the visuals match
// the design 1:1. Wiring these to live GraphQL is deferred — the schema
// doesn't yet expose several design fields (cluster map coords, sparkline
// strings, predictor deltas).
// ====================================================

export type ShardStatus = 'pass' | 'fail' | 'running' | 'preempt';

export interface Author {
  handle: string;
  name: string;
  initials: string;
  color: string;
}

export interface RunCost {
  actualUsd: number;
  projectedUsd: number;
  baselineUsd: number;
}

export interface RunPredictor {
  mae: number;
  rho: number;
  modelVersion: string;
  p50Delta: number;
  p95Delta: number;
}

export interface Run {
  id: string;
  repo: string;
  branch: string;
  commit: string;
  commitMsg: string;
  author: Author;
  triggeredBy: string;
  elapsedSec: number;
  predictedTotalSec: number;
  p95ShardSec: number;
  status: string;
  startedAt: string;
  workerCount: number;
  workerType: string;
  testCount: number;
  passed: number;
  failed: number;
  skipped: number;
  running: number;
  cost: RunCost;
  predictor: RunPredictor;
}

export interface Shard {
  i: number;
  status: ShardStatus;
  pred: number;
  actual: number;
  tests: number;
  fails: number;
  start: number;
  end: number | null;
  worker: string;
}

export type ClusterCategory =
  | 'assertion'
  | 'timeout'
  | 'panic'
  | 'network'
  | 'race';

export interface Cluster {
  id: string;
  title: string;
  file: string;
  category: ClusterCategory;
  occurrences: number;
  affectedRuns: number;
  firstSeen: string;
  lastSeen: string;
  x: number;
  y: number;
  r: number;
  tests: string[];
  affectedRunIds: string[];
  related: string[];
  stack: string[];
}

export interface FlakeOwner {
  i: string;
  c: string;
}

export interface Flake {
  id: string;
  file: string;
  name: string;
  owner: FlakeOwner;
  rate: number;
  wLo: number;
  wHi: number;
  samples: number;
  category: string;
  status: string;
  quarantinedDays: number;
  durMean: number;
  spark: string;
}

export interface TeoData {
  run: Run;
  shards: Shard[];
  clusters: Cluster[];
  flakes: Flake[];
}

// --- Current run -----------------------------------------------------
const run: Run = {
  id: 'a4f2c891-7e3b-4d12-9c8a-f1e2d3c4b5a6',
  repo: 'teo-dev/teo',
  branch: 'feat/scheduler-lpt-v2',
  commit: '9e3f12a',
  commitMsg: 'feat(scheduler): adaptive LPT with code-churn weighting',
  author: { handle: 'marco', name: 'Marco Pereira', initials: 'MP', color: 'm' },
  triggeredBy: 'github.push',
  elapsedSec: 154, // 02:34
  predictedTotalSec: 184, // 03:04
  p95ShardSec: 134,
  status: 'running',
  startedAt: '2026-05-20T14:21:08Z',
  workerCount: 24,
  workerType: 'c7g.xlarge spot',
  testCount: 1247,
  passed: 894,
  failed: 7,
  skipped: 42,
  running: 304,
  cost: { actualUsd: 0.21, projectedUsd: 0.32, baselineUsd: 0.54 },
  predictor: {
    mae: 4.2,
    rho: 0.91,
    modelVersion: 'lgbm-2026.05.18',
    p50Delta: -0.03,
    p95Delta: 0.012,
  },
};

// --- Shards (24) -----------------------------------------------------
// Each: status, predicted, actual, testCount, fail count, start offset (s),
// end offset (s or null if running).
const shards: Shard[] = [
  { i: 0, status: 'pass', pred: 98, actual: 102, tests: 47, fails: 0, start: 0, end: 102, worker: 'w-3a1b' },
  { i: 1, status: 'pass', pred: 121, actual: 108, tests: 52, fails: 0, start: 0, end: 108, worker: 'w-3a1c' },
  { i: 2, status: 'running', pred: 134, actual: 141, tests: 38, fails: 1, start: 0, end: null, worker: 'w-3a1d' },
  { i: 3, status: 'pass', pred: 87, actual: 79, tests: 41, fails: 0, start: 0, end: 79, worker: 'w-3a1e' },
  { i: 4, status: 'fail', pred: 112, actual: 64, tests: 49, fails: 3, start: 0, end: 64, worker: 'w-3a1f' },
  { i: 5, status: 'pass', pred: 103, actual: 119, tests: 56, fails: 0, start: 0, end: 119, worker: 'w-3a20' },
  { i: 6, status: 'running', pred: 128, actual: 152, tests: 33, fails: 0, start: 0, end: null, worker: 'w-3a21' },
  { i: 7, status: 'pass', pred: 76, actual: 82, tests: 39, fails: 0, start: 0, end: 82, worker: 'w-3a22' },
  { i: 8, status: 'preempt', pred: 94, actual: 41, tests: 22, fails: 0, start: 0, end: 41, worker: 'w-3a23' },
  { i: 9, status: 'running', pred: 118, actual: 134, tests: 45, fails: 0, start: 41, end: null, worker: 'w-3a23r' },
  { i: 10, status: 'pass', pred: 88, actual: 93, tests: 51, fails: 0, start: 0, end: 93, worker: 'w-3a24' },
  { i: 11, status: 'pass', pred: 142, actual: 138, tests: 44, fails: 0, start: 0, end: 138, worker: 'w-3a25' },
  { i: 12, status: 'running', pred: 156, actual: 154, tests: 51, fails: 0, start: 0, end: null, worker: 'w-3a26' },
  { i: 13, status: 'pass', pred: 91, actual: 88, tests: 48, fails: 0, start: 0, end: 88, worker: 'w-3a27' },
  { i: 14, status: 'fail', pred: 109, actual: 73, tests: 53, fails: 2, start: 0, end: 73, worker: 'w-3a28' },
  { i: 15, status: 'pass', pred: 116, actual: 124, tests: 46, fails: 0, start: 0, end: 124, worker: 'w-3a29' },
  { i: 16, status: 'running', pred: 134, actual: 148, tests: 42, fails: 1, start: 0, end: null, worker: 'w-3a2a' },
  { i: 17, status: 'pass', pred: 79, actual: 84, tests: 55, fails: 0, start: 0, end: 84, worker: 'w-3a2b' },
  { i: 18, status: 'pass', pred: 106, actual: 98, tests: 49, fails: 0, start: 0, end: 98, worker: 'w-3a2c' },
  { i: 19, status: 'running', pred: 124, actual: 138, tests: 43, fails: 0, start: 0, end: null, worker: 'w-3a2d' },
  { i: 20, status: 'pass', pred: 93, actual: 87, tests: 50, fails: 0, start: 0, end: 87, worker: 'w-3a2e' },
  { i: 21, status: 'running', pred: 138, actual: 152, tests: 47, fails: 0, start: 0, end: null, worker: 'w-3a2f' },
  { i: 22, status: 'pass', pred: 101, actual: 94, tests: 48, fails: 0, start: 0, end: 94, worker: 'w-3a30' },
  { i: 23, status: 'running', pred: 147, actual: 154, tests: 36, fails: 0, start: 0, end: null, worker: 'w-3a31' },
];

// --- Failure clusters ------------------------------------------------
// x,y are normalized 0..1 in the spatial map. Categories drive color.
const clusters: Cluster[] = [
  {
    id: 'fc-7e3a',
    title: 'AssertionError: expected 200, got 503',
    file: 'internal/api/runs_handler.go:142',
    category: 'assertion',
    occurrences: 47,
    affectedRuns: 12,
    firstSeen: '2026-05-12',
    lastSeen: '2026-05-20',
    x: 0.62, y: 0.42, r: 38,
    tests: ['TestRunsList_ServerError', 'TestRunsList_Timeout', 'TestRunsList_503Retry'],
    affectedRunIds: ['a4f2c89', '9b3d12f', '8c4e23a', '7d5f34b', '6e6a45c'],
    related: ['fc-9b2c', 'fc-4d5e'],
    stack: [
      '  at TestRunsList_ServerError (runs_handler_test.go:142)',
      '    runs_handler_test.go:142:  require.Equal(t, 200, w.Code)',
      '        Error:  Not equal:',
      '                expected: 200',
      '                actual  : 503',
      '  at internal/api/runs_handler.go:88 (lib)',
      '  at internal/api/middleware/timeout.go:42 (lib)',
    ],
  },
  {
    id: 'fc-9b2c',
    title: 'TimeoutError: context deadline exceeded (5s)',
    file: 'internal/scheduler/dispatch.go:204',
    category: 'timeout',
    occurrences: 31,
    affectedRuns: 9,
    firstSeen: '2026-05-14',
    lastSeen: '2026-05-20',
    x: 0.52, y: 0.55, r: 32,
    tests: ['TestSchedulerDispatch_SlowWorker', 'TestSchedulerDispatch_NATSLag'],
    affectedRunIds: ['a4f2c89', '9b3d12f', '8c4e23a'],
    related: ['fc-7e3a'],
    stack: [
      '  at TestSchedulerDispatch_SlowWorker (dispatch_test.go:204)',
      '    context deadline exceeded after 5.001s',
      '  at internal/scheduler/dispatch.go:78 (app)',
      '  at internal/nats/publisher.go:34 (lib)',
    ],
  },
  {
    id: 'fc-4d5e',
    title: 'panic: runtime error: invalid memory address or nil pointer dereference',
    file: 'internal/predictor/lgbm_client.go:91',
    category: 'panic',
    occurrences: 18,
    affectedRuns: 4,
    firstSeen: '2026-05-18',
    lastSeen: '2026-05-20',
    x: 0.42, y: 0.30, r: 24,
    tests: ['TestPredictorFallback_NilModel'],
    affectedRunIds: ['a4f2c89', '9b3d12f'],
    related: ['fc-7e3a', 'fc-2a8b'],
    stack: [
      '  at TestPredictorFallback_NilModel (lgbm_client_test.go:91)',
      '    goroutine 47 [running]:',
      '    panic: runtime error: invalid memory address or nil pointer dereference',
      '  at internal/predictor/lgbm_client.go:91 (app)',
      '    *Client.Predict(0x0, ...)',
      '  at internal/predictor/fallback.go:34 (app)',
    ],
  },
  {
    id: 'fc-2a8b',
    title: 'connection refused: dial tcp 127.0.0.1:5432',
    file: 'internal/testpg/container.go:67',
    category: 'network',
    occurrences: 12,
    affectedRuns: 6,
    firstSeen: '2026-05-15',
    lastSeen: '2026-05-19',
    x: 0.33, y: 0.46, r: 19,
    tests: ['TestPgConn_Reuse', 'TestPgConn_Migrate'],
    affectedRunIds: ['9b3d12f', '8c4e23a', '7d5f34b'],
    related: ['fc-4d5e'],
    stack: [
      '  at TestPgConn_Reuse (container_test.go:67)',
      '    dial tcp 127.0.0.1:5432: connect: connection refused',
      '  at internal/testpg/container.go:67 (app)',
    ],
  },
  {
    id: 'fc-1f2e',
    title: 'race detected during execution of test',
    file: 'internal/runmanager/state.go:312',
    category: 'race',
    occurrences: 9,
    affectedRuns: 5,
    firstSeen: '2026-05-13',
    lastSeen: '2026-05-20',
    x: 0.72, y: 0.62, r: 16,
    tests: ['TestRunManagerState_Concurrent'],
    affectedRunIds: ['a4f2c89', '8c4e23a'],
    related: ['fc-9b2c'],
    stack: [
      '  at TestRunManagerState_Concurrent (state_test.go:312)',
      '    WARNING: DATA RACE',
      '    Read at 0x00c0001a4030 by goroutine 7',
      '  at internal/runmanager/state.go:312 (app)',
    ],
  },
  {
    id: 'fc-8a4d',
    title: 'json: cannot unmarshal string into Go value of type int64',
    file: 'internal/resultpipeline/otlp.go:198',
    category: 'assertion',
    occurrences: 6,
    affectedRuns: 3,
    firstSeen: '2026-05-17',
    lastSeen: '2026-05-19',
    x: 0.78, y: 0.36, r: 13,
    tests: ['TestOTLPParse_LegacySpan'],
    affectedRunIds: ['9b3d12f', '7d5f34b'],
    related: [],
    stack: [
      '  at TestOTLPParse_LegacySpan (otlp_test.go:198)',
      '    json: cannot unmarshal string into Go value of type int64',
      '  at internal/resultpipeline/otlp.go:198 (app)',
    ],
  },
  {
    id: 'fc-3b1f',
    title: 'Wilson interval edge: zero-sample test promoted',
    file: 'internal/flake/wilson.go:54',
    category: 'assertion',
    occurrences: 4,
    affectedRuns: 2,
    firstSeen: '2026-05-19',
    lastSeen: '2026-05-20',
    x: 0.55, y: 0.78, r: 11,
    tests: ['TestWilsonInterval_ZeroSample'],
    affectedRunIds: ['a4f2c89'],
    related: [],
    stack: [
      '  at TestWilsonInterval_ZeroSample (wilson_test.go:54)',
      '    division by zero: n=0',
      '  at internal/flake/wilson.go:54 (app)',
    ],
  },
  {
    id: 'fc-6c9a',
    title: 'expected file exists, was missing',
    file: 'internal/logstore/s3.go:121',
    category: 'network',
    occurrences: 3,
    affectedRuns: 2,
    firstSeen: '2026-05-19',
    lastSeen: '2026-05-20',
    x: 0.25, y: 0.68, r: 9,
    tests: ['TestLogStore_Upload'],
    affectedRunIds: ['a4f2c89', '9b3d12f'],
    related: ['fc-2a8b'],
    stack: [
      '  at TestLogStore_Upload (s3_test.go:121)',
      '    NoSuchKey: The specified key does not exist.',
      '  at internal/logstore/s3.go:121 (app)',
    ],
  },
];

// --- Flaky tests -----------------------------------------------------
const flakes: Flake[] = [
  {
    id: 't-e3a1',
    file: 'pkg/adapter/pytest/runner.py', name: 'test_pytest_collect_when_no_tests',
    owner: { i: 'MP', c: 'm' },
    rate: 0.31, wLo: 0.22, wHi: 0.41, samples: 124,
    category: 'order-dependent', status: 'quarantined', quarantinedDays: 4,
    durMean: 3.2, spark: 'PFFPPFFPPPFPPFPPFFPP',
  },
  {
    id: 't-8b2c',
    file: 'internal/scheduler/dispatch_test.go', name: 'TestSchedulerDispatch_SlowWorker',
    owner: { i: 'PK', c: 'p' },
    rate: 0.27, wLo: 0.19, wHi: 0.36, samples: 142,
    category: 'async/timing', status: 'quarantined', quarantinedDays: 2,
    durMean: 8.7, spark: 'PPFFPPFPPFPFPPFPPFPP',
  },
  {
    id: 't-1d4e',
    file: 'internal/predictor/lgbm_client_test.go', name: 'TestPredictorFallback_NilModel',
    owner: { i: 'PK', c: 'p' },
    rate: 0.22, wLo: 0.14, wHi: 0.32, samples: 98,
    category: 'network', status: 'flagged', quarantinedDays: 0,
    durMean: 2.1, spark: 'PPPPFPPFPPPPPPFPPFPP',
  },
  {
    id: 't-7f5a',
    file: 'internal/api/runs_handler_test.go', name: 'TestRunsList_503Retry',
    owner: { i: 'DR', c: 'd' },
    rate: 0.18, wLo: 0.11, wHi: 0.27, samples: 114,
    category: 'async/timing', status: 'flagged', quarantinedDays: 0,
    durMean: 1.4, spark: 'PPPPPFPPPPFPPPPFPPPP',
  },
  {
    id: 't-2c6b',
    file: 'internal/runmanager/state_test.go', name: 'TestRunManagerState_Concurrent',
    owner: { i: 'SH', c: 's' },
    rate: 0.16, wLo: 0.09, wHi: 0.25, samples: 87,
    category: 'race', status: 'flagged', quarantinedDays: 0,
    durMean: 5.6, spark: 'PPPPPFPPFPPPPPFPPPPP',
  },
  {
    id: 't-9d8e',
    file: 'internal/flake/wilson_test.go', name: 'TestWilsonInterval_ZeroSample',
    owner: { i: 'AC', c: 'a' },
    rate: 0.14, wLo: 0.07, wHi: 0.24, samples: 64,
    category: 'env-dependent', status: 'flagged', quarantinedDays: 0,
    durMean: 0.4, spark: 'PPPPPPFPPPPPFPPPFPPP',
  },
  {
    id: 't-4a3c',
    file: 'internal/testpg/container_test.go', name: 'TestPgConn_Reuse',
    owner: { i: 'PK', c: 'p' },
    rate: 0.13, wLo: 0.07, wHi: 0.21, samples: 142,
    category: 'resource-leak', status: 'quarantined', quarantinedDays: 11,
    durMean: 2.9, spark: 'PPPPPPPFPPPPPFPPPFPP',
  },
  {
    id: 't-6e1d',
    file: 'internal/resultpipeline/otlp_test.go', name: 'TestOTLPParse_LegacySpan',
    owner: { i: 'MP', c: 'm' },
    rate: 0.11, wLo: 0.05, wHi: 0.20, samples: 73,
    category: 'randomness', status: 'flagged', quarantinedDays: 0,
    durMean: 0.8, spark: 'PPPPPPPPFPPPPPPFPPPP',
  },
  {
    id: 't-5b7a',
    file: 'internal/worker/drain_test.go', name: 'TestWorkerDrain_GracefulSIGTERM',
    owner: { i: 'PK', c: 'p' },
    rate: 0.10, wLo: 0.04, wHi: 0.18, samples: 91,
    category: 'async/timing', status: 'flagged', quarantinedDays: 0,
    durMean: 4.2, spark: 'PPPPPPPPPPFPPPPPPFPP',
  },
  {
    id: 't-0a9c',
    file: 'internal/logstore/s3_test.go', name: 'TestLogStore_Upload',
    owner: { i: 'SH', c: 's' },
    rate: 0.09, wLo: 0.04, wHi: 0.18, samples: 86,
    category: 'network', status: 'flagged', quarantinedDays: 0,
    durMean: 1.7, spark: 'PPPPPPPPPPPFPPPPPFPP',
  },
  {
    id: 't-3f2b',
    file: 'internal/api/middleware/timeout_test.go', name: 'TestTimeout_RetryBudget',
    owner: { i: 'DR', c: 'd' },
    rate: 0.08, wLo: 0.03, wHi: 0.17, samples: 79,
    category: 'async/timing', status: 'flagged', quarantinedDays: 0,
    durMean: 1.1, spark: 'PPPPPPPPPPPPPFPPPFPP',
  },
  {
    id: 't-7c4d',
    file: 'internal/nats/publisher_test.go', name: 'TestNATSPublisher_ReconnectAfterDisconnect',
    owner: { i: 'PK', c: 'p' },
    rate: 0.07, wLo: 0.03, wHi: 0.14, samples: 124,
    category: 'network', status: 'stable', quarantinedDays: 0,
    durMean: 2.3, spark: 'PPPPPPPPPPPPPPPPFPFP',
  },
];

export const TEO_DATA: TeoData = { run, shards, clusters, flakes };
