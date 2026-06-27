// ====================================================
// TEO — GraphQL → view-model adapters for the /clusters and /flakes screens.
//
// These are pure, React-free, fully unit-testable functions. They map the
// GraphQL row shape (FailureClustersQuery / FlakesQuery in queries.ts) onto the
// Cluster / Flake view types the redesigned teo/ components consume.
//
// The Go GraphQL backend already resolves the bulk of the design fields
// (x/y/r/category for clusters; wilsonUpper/spark/status/durationMeanMs for
// flakes — see internal/api/graphql_resolvers.go). A handful of design-only
// fields have no authoritative source in Postgres/ClickHouse yet and are
// DERIVED here deterministically so the spatial map / Wilson bars / sparklines
// still render from live data:
//
//   clusters: file (parsed from the stack), stack[] (split string), tests[] /
//             affectedRunIds[] / related[] (not in schema → []), and x/y/r /
//             category as a deterministic FALLBACK only when the backend
//             omits them.
//   flakes:   owner ({i,c} — seeded from ownerTeam when present, else a testId
//             hash), quarantinedDays (days since quarantinedAt; 0 when not
//             quarantined), wHi fallback (rate*1.4 when wilsonUpper is null
//             while the backend rolls out).
//
// When the schema/ClickHouse history grows real columns for these, drop the
// corresponding derivation. This mirrors the deferral note in teo-data.ts.
// ====================================================

import type { Cluster, ClusterCategory, Flake, Run, RunPredictor, Shard, ShardStatus } from '@/lib/teo-data';

// --- GraphQL row shapes (subset of queries.ts selections) ---

export interface GqlCluster {
  id: string;
  representativeMessage?: string | null;
  representativeStack?: string | null;
  occurrences?: number | null;
  firstSeen?: string | null;
  lastSeen?: string | null;
  x?: number | null;
  y?: number | null;
  r?: number | null;
  category?: string | null;
  stackFingerprint?: string | null;
  affectedRuns?: number | null;
  // ADR-0021 LLM root-cause hint. Null until the opt-in llm-hints cron runs.
  rootCauseHint?: string | null;
  hintCategory?: string | null;
  hintConfidence?: number | null;
}

export interface GqlFlake {
  testId: string;
  testPath?: string | null;
  testName?: string | null;
  flakeRate?: number | null;
  wilsonLower?: number | null;
  wilsonUpper?: number | null;
  sampleSize?: number | null;
  category?: string | null;
  spark?: string | null;
  status?: string | null;
  durationMeanMs?: number | null;
  // ISO timestamp the test was quarantined (null when not quarantined). When
  // set, status resolves to 'quarantined' and quarantinedDays = days since.
  quarantinedAt?: string | null;
  // CODEOWNERS-resolved owning team (e.g. "@teo-dev/platform"). Seeds the owner
  // avatar deterministically when present; falls back to a testId-hash otherwise.
  ownerTeam?: string | null;
}

const CLUSTER_CATEGORIES: ClusterCategory[] = ['assertion', 'timeout', 'panic', 'network', 'race'];

/** Deterministic 32-bit FNV-1a hash of a string. Used to seed derived fields
 *  (owner color/initials, sparkline) so a given test id always renders the
 *  same — no Math.random, stable across server renders. */
export function hash32(s: string): number {
  let h = 0x811c9dc5;
  for (let i = 0; i < s.length; i++) {
    h ^= s.charCodeAt(i);
    h = Math.imul(h, 0x01000193);
  }
  return h >>> 0;
}

function clamp(v: number, lo: number, hi: number): number {
  if (!Number.isFinite(v)) return lo;
  return v < lo ? lo : v > hi ? hi : v;
}

/** Bucket a failure message/stack into one of the five map categories. Mirrors
 *  internal/api/graphql_resolvers.go classifyClusterCategory EXACTLY — same
 *  branch precedence (panic → timeout → network → race → assertion) and the same
 *  substrings — so this fallback yields server-consistent categories on the rare
 *  path where the backend omits `category`. Keep the two in lockstep; if you add
 *  a heuristic here, add it to the Go switch too. */
export function classifyCategory(text: string): ClusterCategory {
  const m = text.toLowerCase();
  if (m.includes('panic')) return 'panic';
  if (m.includes('timeout') || m.includes('deadline exceeded')) return 'timeout';
  if (m.includes('connection refused') || m.includes('dial tcp') || m.includes('nosuchkey')) return 'network';
  if (m.includes('data race') || m.includes('-race')) return 'race';
  return 'assertion';
}

function isClusterCategory(c: string | null | undefined): c is ClusterCategory {
  return !!c && (CLUSTER_CATEGORIES as string[]).includes(c);
}

/** Format an ISO timestamp (or already-short date) to YYYY-MM-DD; passes through
 *  anything it can't parse so we never render "Invalid Date". */
function shortDate(v: string | null | undefined): string {
  if (!v) return '';
  // Already YYYY-MM-DD?
  if (/^\d{4}-\d{2}-\d{2}$/.test(v)) return v;
  const d = new Date(v);
  if (Number.isNaN(d.getTime())) return v;
  return d.toISOString().slice(0, 10);
}

/** Pull the first source frame ("....go:NN" / "....py:NN" / "....ts:NN") out of a
 *  representative stack string for the cluster's `file` field. */
export function extractFile(stack: string | null | undefined, fallback: string): string {
  if (!stack) return fallback;
  const lines = stack.split('\n');
  for (const raw of lines) {
    const line = raw.trim();
    const m = line.match(/([\w./-]+\.(?:go|py|ts|tsx|js|java|rb):\d+)/);
    if (m) return m[1];
  }
  return fallback;
}

/**
 * Map GraphQL failure-cluster rows to the Cluster view type.
 *
 * Pass-through: id, representativeMessage→title, occurrences, firstSeen/lastSeen
 * (formatted), affectedRuns. The backend already computes x/y/r/category; we
 * only re-derive them as a fallback when null/absent so the map never NaNs.
 * tests[]/affectedRunIds[]/related[] have no schema source → empty arrays
 * (the component tolerates empties). stack[] = representativeStack split.
 */
export function adaptClusters(rows: GqlCluster[] | null | undefined): Cluster[] {
  const list = rows ?? [];
  // Derived layout fallback uses occurrence rank + recency; computed once.
  const maxOcc = list.reduce((m, c) => Math.max(m, c.occurrences ?? 0), 0);
  const sortedByRecent = [...list]
    .map((c, i) => ({ id: c.id, i, ts: Date.parse(c.lastSeen ?? '') }))
    .sort((a, b) => (Number.isNaN(b.ts) ? 0 : b.ts) - (Number.isNaN(a.ts) ? 0 : a.ts));
  const recentRank = new Map<string, number>();
  sortedByRecent.forEach((e, rank) => recentRank.set(e.id, rank));

  return list.map((c) => {
    const occurrences = c.occurrences ?? 0;
    const message = c.representativeMessage ?? '';
    const stackStr = c.representativeStack ?? '';
    const category: ClusterCategory = isClusterCategory(c.category)
      ? c.category
      : classifyCategory(`${message}\n${stackStr}`);

    // x/y/r: prefer backend; else derive deterministically.
    const rank = recentRank.get(c.id) ?? 0;
    const xFallback = list.length > 1 ? rank / (list.length - 1) : 0.5;
    const yFallback =
      maxOcc > 0 ? clamp(1 - Math.log10(occurrences + 1) / Math.log10(maxOcc + 1), 0, 1) : 0.5;
    const rFallback = maxOcc > 0 ? clamp(9 + 30 * Math.sqrt(occurrences / maxOcc), 8, 40) : 9;

    const x = clamp(c.x ?? xFallback, 0, 1);
    const y = clamp(c.y ?? yFallback, 0, 1);
    const r = clamp(c.r ?? rFallback, 8, 40);

    const stack = stackStr ? stackStr.split('\n').filter((l) => l.length > 0) : [];

    return {
      id: c.id,
      title: message,
      file: extractFile(stackStr, c.stackFingerprint ?? ''),
      category,
      occurrences,
      affectedRuns: c.affectedRuns ?? 0,
      firstSeen: shortDate(c.firstSeen),
      lastSeen: shortDate(c.lastSeen),
      x,
      y,
      r,
      tests: [],
      affectedRunIds: [],
      related: [],
      stack,
      // ADR-0021 hint passthrough; null/undefined when the feature hasn't run.
      rootCauseHint: c.rootCauseHint ?? null,
      hintCategory: c.hintCategory ?? null,
      hintConfidence: c.hintConfidence ?? null,
    };
  });
}

// --- Flakes ---

const OWNER_COLORS = ['m', 'p', 'd', 's', 'a'];

/** Derive a stable owner avatar ({initials, color bucket}). When ownerTeam is
 *  present (CODEOWNERS-resolved) we seed from it so the avatar reflects the real
 *  team — initials are the team's leading alphanumerics (1–2 uppercase chars),
 *  colour bucket from the team hash. Otherwise we fall back to a deterministic
 *  testId hash. Always returns 1–2 uppercase initials and a colour in m/p/d/s/a. */
export function deriveOwner(testId: string, ownerTeam?: string | null): { i: string; c: string } {
  const team = (ownerTeam ?? '').trim();
  if (team) {
    // Strip a leading "@" / "@org/" prefix, then take the first two letters of
    // the team slug as initials.
    const slug = team.replace(/^@/, '').split('/').pop() ?? team;
    const letters = slug.replace(/[^a-zA-Z]/g, '').toUpperCase();
    const i = letters.length >= 2 ? letters.slice(0, 2) : letters.length === 1 ? letters : 'TM';
    const c = OWNER_COLORS[hash32(team) % OWNER_COLORS.length];
    return { i, c };
  }
  const h = hash32(testId);
  const c = OWNER_COLORS[h % OWNER_COLORS.length];
  // Two-char "initials" from the hash, mapped to A–Z.
  const a = String.fromCharCode(65 + ((h >>> 3) % 26));
  const b = String.fromCharCode(65 + ((h >>> 11) % 26));
  return { i: `${a}${b}`, c };
}

/** Whole days between an ISO timestamp and now, floored, min 1 when the test is
 *  quarantined (so a freshly-quarantined test still reads ">0 days" in the UI).
 *  Returns 0 when the input is absent/unparseable. */
export function daysSince(iso: string | null | undefined, now: number = Date.now()): number {
  if (!iso) return 0;
  const t = Date.parse(iso);
  if (Number.isNaN(t)) return 0;
  const days = Math.floor((now - t) / 86_400_000);
  return days > 0 ? days : 1;
}

/** Build a deterministic 20-char P/F sparkline seeded from the test id and rate
 *  when the backend supplies no spark string. Roughly round(rate*20) failures,
 *  pseudo-randomly distributed by a hash walk so it's stable but not clustered. */
export function deriveSpark(testId: string, rate: number): string {
  const N = 20;
  const fails = clamp(Math.round(rate * N), 0, N);
  const slots = Array<string>(N).fill('P');
  let h = hash32(testId) || 1;
  let placed = 0;
  let guard = 0;
  while (placed < fails && guard < N * 8) {
    h = (Math.imul(h, 1103515245) + 12345) >>> 0;
    const idx = h % N;
    if (slots[idx] === 'P') {
      slots[idx] = 'F';
      placed++;
    }
    guard++;
  }
  // Fallback: if the hash walk stalled, fill sequentially.
  for (let i = 0; placed < fails && i < N; i++) {
    if (slots[i] === 'P') {
      slots[i] = 'F';
      placed++;
    }
  }
  return slots.join('');
}

/** Normalize a 20-char sparkline string to exactly N chars of [PFS]. */
function normalizeSpark(spark: string, testId: string, rate: number): string {
  const cleaned = (spark || '').toUpperCase().replace(/[^PFS]/g, '');
  if (!cleaned) return deriveSpark(testId, rate);
  if (cleaned.length >= 20) return cleaned.slice(-20);
  return cleaned.padStart(20, 'P');
}

/**
 * Map GraphQL flake rows to the Flake view type.
 *
 * Pass-through: testPath→file, testName→name, flakeRate→rate, wilsonLower→wLo,
 * wilsonUpper→wHi (fallback rate*1.4 when null), sampleSize→samples, category,
 * durationMeanMs→durMean (ms→s). Derived: owner (from ownerTeam or testId),
 * status (quarantinedAt→'quarantined'; wLo>0.05→'flagged'; else backend),
 * quarantinedDays (days since quarantinedAt), spark (passthrough or derived).
 */
export function adaptFlakes(rows: GqlFlake[] | null | undefined): Flake[] {
  const list = rows ?? [];
  return list.map((f) => {
    const rate = f.flakeRate ?? 0;
    const wLo = f.wilsonLower ?? 0;
    const wHi = f.wilsonUpper != null ? f.wilsonUpper : clamp(rate * 1.4, rate, 1);
    const category = f.category && f.category.length > 0 ? f.category : 'async/timing';
    const durMean = f.durationMeanMs != null ? f.durationMeanMs / 1000 : 0;

    // Status precedence: a quarantinedAt timestamp is authoritative
    // ('quarantined'); otherwise a wilsonLower above the 5% threshold marks a
    // 'flagged' candidate; else fall through to whatever badge the backend
    // resolved (defaulting to 'flagged').
    const quarantined = !!f.quarantinedAt;
    let status: string;
    if (quarantined) {
      status = 'quarantined';
    } else if (wLo > 0.05) {
      status = 'flagged';
    } else {
      status = f.status && f.status.length > 0 ? f.status : 'flagged';
    }
    const quarantinedDays = quarantined ? daysSince(f.quarantinedAt) : 0;

    return {
      id: f.testId,
      file: f.testPath ?? '',
      name: f.testName ?? f.testId,
      owner: deriveOwner(f.testId, f.ownerTeam),
      rate,
      wLo,
      wHi,
      samples: f.sampleSize ?? 0,
      category,
      status,
      quarantinedDays,
      durMean,
      spark: normalizeSpark(f.spark ?? '', f.testId, rate),
    };
  });
}

// ====================================================
// Run adapter (ui-home-calibration)
//
// Maps the GraphQL Run/Shard shape (RunByIdQuery) into the teo/ Run/Shard prop
// shapes the marquee home screen renders from. This is the ONLY place that maps
// the backend status vocabulary onto pass/fail/running/preempt and converts
// ms→seconds, so the conversion lives in one tested spot.
// ====================================================

export interface GqlShard {
  id: string;
  index: number;
  status: string;
  workerId?: string | null;
  predictedDurationMs?: number | null;
  actualDurationMs?: number | null;
  testCount?: number | null;
  startedAt?: string | null;
  finishedAt?: string | null;
  deltaMs?: number | null;
  predictionConfidence?: number | null;
  modelVersion?: string | null;
}

export interface GqlRunPredictor {
  mae?: number | null;
  rho?: number | null;
  modelVersion?: string | null;
  p50DeltaMs?: number | null;
  p95DeltaMs?: number | null;
  sampleCount?: number | null;
  confidence?: number | null;
}

export interface GqlRun {
  id: string;
  repoFullName?: string | null;
  branch?: string | null;
  commitSha?: string | null;
  status: string;
  totalDurationMs?: number | null;
  preemptionCount?: number | null;
  startedAt?: string | null;
  finishedAt?: string | null;
  failedTestCount?: number | null;
  predictorMae?: number | null;
  predictorRho?: number | null;
  modelVersion?: string | null;
  predictor?: GqlRunPredictor | null;
  shards?: GqlShard[] | null;
}

export interface AdaptedRun {
  run: Run;
  shards: Shard[];
}

/**
 * Map the backend shard/run status vocabulary onto the teo/ ShardStatus the
 * Gantt CSS classes key off (gantt__bar--pass etc.). Exhaustive switch with a
 * 'running' default so an unknown status renders a styled (info-colored) bar
 * rather than an unstyled one.
 */
export function adaptStatus(s: string | null | undefined): ShardStatus {
  switch ((s ?? '').toLowerCase()) {
    case 'succeeded':
    case 'passed':
    case 'pass':
      return 'pass';
    case 'failed':
    case 'errored':
    case 'timed_out':
    case 'lost':
    case 'cancelled':
    case 'fail':
      return 'fail';
    case 'preempted':
    case 'preempt':
      return 'preempt';
    case 'running':
    case 'pending':
    case 'queued':
    case 'dispatched':
      return 'running';
    default:
      return 'running';
  }
}

/** Whether a mapped shard status represents a finished (terminal) shard. */
function isFinishedStatus(status: ShardStatus): boolean {
  return status === 'pass' || status === 'fail' || status === 'preempt';
}

/** ms → seconds, rounded; null/undefined/NaN → 0. */
function msToSec(ms: number | null | undefined): number {
  if (ms == null || Number.isNaN(ms)) return 0;
  return Math.round(ms / 1000);
}

/** Nearest-rank percentile of a numeric slice (sorted ascending, sign-preserving). */
function percentile(values: number[], p: number): number {
  if (values.length === 0) return 0;
  const sorted = [...values].sort((a, b) => a - b);
  if (p <= 0) return sorted[0];
  if (p >= 1) return sorted[sorted.length - 1];
  const idx = Math.min(sorted.length - 1, Math.max(0, Math.ceil(p * sorted.length) - 1));
  return sorted[idx];
}

/** First defined, non-NaN number in the list; else 0. */
function firstNum(...vals: Array<number | null | undefined>): number {
  for (const v of vals) {
    if (typeof v === 'number' && !Number.isNaN(v)) return v;
  }
  return 0;
}

/** Parse an ISO timestamp to epoch ms; null on missing/invalid. */
function parseMs(iso: string | null | undefined): number | null {
  if (!iso) return null;
  const t = Date.parse(iso);
  return Number.isNaN(t) ? null : t;
}

/**
 * Adapt one GraphQL Run (nested shards + predictor) into the teo/ component prop
 * shapes. Pure and total: never throws, never returns NaN — missing fields fall
 * back to numeric/string defaults so the overlay renders an em-dash, not a crash.
 */
export function adaptRun(gql: GqlRun, now: number = Date.now()): AdaptedRun {
  const startedMs = parseMs(gql.startedAt);
  const gqlShards = gql.shards ?? [];

  const shards: Shard[] = gqlShards.map((s) => {
    const status = adaptStatus(s.status);
    const finished = isFinishedStatus(status);
    const pred = msToSec(s.predictedDurationMs);
    const actual = msToSec(s.actualDurationMs);
    const shardStartMs = parseMs(s.startedAt);
    const start =
      startedMs != null && shardStartMs != null
        ? Math.max(0, Math.round((shardStartMs - startedMs) / 1000))
        : 0;
    // end is null for non-terminal shards or when actual duration is absent.
    const end = finished && s.actualDurationMs != null ? actual : null;
    return {
      i: s.index,
      status,
      pred,
      actual,
      tests: s.testCount ?? 0,
      fails: status === 'fail' ? 1 : 0,
      start,
      end,
      worker: s.workerId ?? '',
      confidence: s.predictionConfidence ?? null,
      modelVersion: s.modelVersion ?? null,
    };
  });

  const testCount = shards.reduce((acc, s) => acc + s.tests, 0);
  const passed = shards.filter((s) => s.status === 'pass').length;
  const failed = shards.filter((s) => s.status === 'fail').length;
  const running = shards.filter((s) => s.status === 'running').length;
  const predictedTotalSec = shards.reduce((acc, s) => Math.max(acc, s.pred), 0);

  const finishedDurations = shards.filter((s) => s.end != null).map((s) => s.end as number);
  const p95ShardSec = Math.round(percentile(finishedDurations, 0.95));

  let elapsedSec = msToSec(gql.totalDurationMs);
  if (elapsedSec === 0 && startedMs != null) {
    elapsedSec = Math.max(0, Math.round((now - startedMs) / 1000));
  }

  const predictor = buildRunPredictor(gql, shards);

  const run: Run = {
    id: gql.id,
    repo: gql.repoFullName ?? '',
    branch: gql.branch ?? '',
    commit: (gql.commitSha ?? '').slice(0, 7),
    commitMsg: gql.commitSha ? `commit ${gql.commitSha.slice(0, 7)}` : '',
    author: { handle: '', name: '', initials: '··', color: 'm' },
    triggeredBy: 'github.push',
    elapsedSec,
    predictedTotalSec,
    p95ShardSec,
    status: gql.status,
    startedAt: gql.startedAt ?? '',
    workerCount: shards.length,
    workerType: 'spot',
    testCount,
    passed,
    failed,
    skipped: 0,
    running,
    cost: { actualUsd: 0, projectedUsd: 0, baselineUsd: 0 },
    predictor,
  };

  return { run, shards };
}

/**
 * Build the run-level predictor block. mae/rho/modelVersion pass through from the
 * flat GraphQL fields (predictorMae/predictorRho/modelVersion), falling back to
 * the nested predictor object. p50/p95 delta are computed from finished shards'
 * fractional miss (actual−pred)/pred so the overlay shows a real distribution
 * even when the backend aggregates are null. Null-safe (never NaN).
 */
function buildRunPredictor(gql: GqlRun, shards: Shard[]): RunPredictor {
  const nested = gql.predictor ?? {};
  // The Go resolver (queryRunPredictor) computes mae as the mean absolute delta
  // of *_duration_ms values — i.e. MILLISECONDS. The UI (RunDetail/RunGantt)
  // renders it as `${mae.toFixed(1)}s` and the fixture uses ~4.2s, so convert
  // ms→s here (matching msToSec elsewhere). rho is a Pearson correlation
  // (unitless) and needs no conversion.
  const mae = firstNum(gql.predictorMae, nested.mae, 0) / 1000;
  const rho = firstNum(gql.predictorRho, nested.rho, 0);
  const modelVersion = gql.modelVersion ?? nested.modelVersion ?? '';

  // NOTE: the backend nested predictor exposes p50DeltaMs/p95DeltaMs (absolute
  // deltas in MS). We deliberately do NOT use them — the overlay shows a
  // unitless FRACTIONAL miss (actual−pred)/pred per finished shard, which is a
  // different quantity. The ms fields stay in GqlRunPredictor/the query for the
  // nested calibration block but are intentionally unread here.
  const fractional = shards
    .filter((s) => s.end != null && s.pred > 0)
    .map((s) => ((s.end as number) - s.pred) / s.pred);

  const p50Delta = fractional.length > 0 ? percentile(fractional, 0.5) : 0;
  const p95Delta = fractional.length > 0 ? percentile(fractional, 0.95) : 0;

  return { mae, rho, modelVersion, p50Delta, p95Delta };
}
