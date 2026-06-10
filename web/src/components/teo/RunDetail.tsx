'use client';

// ====================================================
// TEO — Run Detail screen (live Gantt) — the marquee surface
// Ported from run-detail.jsx. Mounted at "/".
// ====================================================

import { useState, useMemo, useEffect } from 'react';
import { StatusBadge, Kpi } from './atoms';
import { Icon } from './Icons';
import { ShardsPanel, PredictorAccuracy, FailureClustersPreview, FleetPanel } from './RunGantt';
import { fmt } from '@/lib/teo-format';
import { isLive } from '@/lib/format';
import { TEO_DATA, type Run, type Shard, type Cluster } from '@/lib/teo-data';

type RunTab = 'shards' | 'tests' | 'failures' | 'logs' | 'trace' | 'plan';

export interface RunDetailScreenProps {
  /** Adapted run-level view model (teo-adapt.adaptRun output). */
  run?: Run;
  /** Adapted shard view models. */
  shards?: Shard[];
  /** Failure clusters for the preview panel. */
  clusters?: Cluster[];
}

// RunDetailScreen renders the marquee live-run surface. It now reads from props
// fed by the home Server Component (real GraphQL data adapted via teo-adapt);
// the TEO_DATA mock remains the default ONLY so any import-time/Storybook usage
// without props still renders the design.
export function RunDetailScreen({
  run = TEO_DATA.run,
  shards = TEO_DATA.shards,
  clusters = TEO_DATA.clusters,
}: RunDetailScreenProps = {}) {
  const [tab, setTab] = useState<RunTab>('shards');
  const [selectedShard, setSelectedShard] = useState<number | null>(null);
  const [hoverShard, setHoverShard] = useState<number | null>(null);

  // Live elapsed counter. Two correctness rules now that data is live:
  //   (1) Resync to the server-recomputed elapsedSec whenever LiveRunDetail
  //       re-fetches and passes a fresh run prop — useState's initializer only
  //       runs on mount, so without this effect the polled value is ignored and
  //       the local counter drifts.
  //   (2) Only tick locally while the run is live; once it reaches a terminal
  //       status the elapsed must freeze on the real total duration instead of
  //       counting up forever.
  const [elapsed, setElapsed] = useState(run.elapsedSec);
  useEffect(() => {
    setElapsed(run.elapsedSec);
  }, [run.elapsedSec]);
  const live = isLive(run.status);
  useEffect(() => {
    if (!live) return;
    const id = setInterval(() => setElapsed((e) => e + 1), 1000);
    return () => clearInterval(id);
  }, [live]);

  // Bound the Gantt timeline at max(shard end OR current elapsed)
  const tEnd = useMemo(() => {
    return Math.max(...shards.map((s) => (s.end != null ? s.end : elapsed)), run.predictedTotalSec);
  }, [shards, elapsed, run.predictedTotalSec]);

  // Render time ticks every 30s
  const ticks = useMemo(() => {
    const step = 30;
    const arr: number[] = [];
    for (let t = 0; t <= tEnd; t += step) arr.push(t);
    return arr;
  }, [tEnd]);

  return (
    <div>
      <RunHeader run={run} elapsed={elapsed} />
      <KpiStrip run={run} shards={shards} />
      <RunTabs tab={tab} setTab={setTab} clusters={clusters} run={run} shardCount={shards.length} />
      <div className="page-pad" style={{ paddingTop: 16 }}>
        {tab === 'shards' && (
          <div className="section-grid">
            <ShardsPanel
              shards={shards}
              tEnd={tEnd}
              ticks={ticks}
              elapsed={elapsed}
              selected={selectedShard}
              setSelected={setSelectedShard}
              hover={hoverShard}
              setHover={setHoverShard}
            />
            <div style={{ display: 'flex', flexDirection: 'column', gap: 16 }}>
              <PredictorAccuracy run={run} shards={shards} />
              <FailureClustersPreview clusters={clusters.slice(0, 4)} />
              <FleetPanel run={run} shards={shards} />
            </div>
          </div>
        )}
        {tab !== 'shards' && <EmptyTabState tab={tab} />}
      </div>
    </div>
  );
}

function RunHeader({ run, elapsed }: { run: Run; elapsed: number }) {
  return (
    <div className="run-header">
      <div className="run-title-row">
        <h1 className="run-title">
          <StatusBadge status={run.status} />
          <span className="mono" style={{ fontSize: 14, color: 'var(--sr-fg-muted)' }}>
            run
          </span>
          <span className="mono" style={{ fontSize: 16 }}>
            {run.id.slice(0, 8)}
          </span>
          <span className="muted" style={{ fontSize: 13, fontWeight: 400 }}>
            ·
          </span>
          <span style={{ fontWeight: 500 }}>{run.commitMsg}</span>
        </h1>
        <div className="run-actions">
          <button className="btn">
            <Icon.Refresh /> Re-run
          </button>
          <button className="btn">
            <Icon.ExternalLink /> Trace
          </button>
          <button className="btn">
            <Icon.Download /> Plan JSON
          </button>
        </div>
      </div>
      <div className="run-meta">
        <span>
          <Icon.GitBranch />{' '}
        </span>
        <span style={{ marginLeft: -8 }}>{run.repo}</span>
        <span className="run-meta__sep">/</span>
        <span>{run.branch}</span>
        <span className="run-meta__sep">·</span>
        <span className="run-commit">{run.commit}</span>
        <span className="run-meta__sep">·</span>
        <span>
          <span className={`owner owner--${run.author.color}`}>{run.author.initials}</span> &nbsp;@{run.author.handle}
        </span>
        <span className="run-meta__sep">·</span>
        <span>via {run.triggeredBy}</span>
        <span className="run-meta__sep">·</span>
        <span>
          elapsed <span style={{ color: 'var(--sr-fg)' }}>{fmt.duration(elapsed)}</span> / projected{' '}
          {fmt.duration(run.predictedTotalSec)}
        </span>
      </div>
    </div>
  );
}

function KpiStrip({ run, shards }: { run: Run; shards: Shard[] }) {
  const done = shards.filter((s) => s.end != null).length;
  const failed = shards.filter((s) => s.status === 'fail').length;
  const preempted = shards.filter((s) => s.status === 'preempt').length;
  // Time-to-first-fail
  const failShard = shards.find((s) => s.status === 'fail');
  const ttffSec = failShard ? failShard.end : null;
  return (
    <div className="page-pad" style={{ paddingBottom: 0 }}>
      <div className="kpi-strip">
        <Kpi
          label="Tests"
          value={run.testCount.toLocaleString()}
          sub={
            <>
              <span style={{ color: 'var(--sr-pass)' }}>{run.passed} pass</span> ·{' '}
              <span style={{ color: 'var(--sr-fail)' }}>{run.failed} fail</span> ·{' '}
              <span style={{ color: 'var(--sr-info)' }}>{run.running} running</span>
            </>
          }
        />
        <Kpi
          label="Shards"
          value={
            <>
              {done}
              <span style={{ color: 'var(--sr-fg-muted)', fontSize: 16 }}> / {shards.length}</span>
            </>
          }
          sub={<>{failed} failed · {preempted} preempt</>}
        />
        <Kpi label="p95 shard" value={fmt.duration(run.p95ShardSec)} sub={<>vs predicted {fmt.duration(run.predictedTotalSec)}</>} />
        <Kpi label="TTFF" value={ttffSec != null ? fmt.duration(ttffSec) : '—'} sub={<>failure-first ordering hit</>} />
        <Kpi
          label="Cost"
          value={fmt.dollars(run.cost.projectedUsd)}
          sub={
            <>
              vs baseline {fmt.dollars(run.cost.baselineUsd)}{' '}
              <span style={{ color: 'var(--sr-pass)' }}>
                −{Math.round((1 - run.cost.projectedUsd / run.cost.baselineUsd) * 100)}%
              </span>
            </>
          }
        />
        <Kpi
          label="Predictor MAE"
          value={`${run.predictor.mae.toFixed(1)}s`}
          sub={
            <>
              ρ {run.predictor.rho.toFixed(2)}
              {run.predictor.modelVersion ? <> · {run.predictor.modelVersion.slice(-12)}</> : null}
            </>
          }
        />
      </div>
    </div>
  );
}

function RunTabs({
  tab,
  setTab,
  clusters,
  run,
  shardCount,
}: {
  tab: RunTab;
  setTab: (t: RunTab) => void;
  clusters: Cluster[];
  run: Run;
  shardCount: number;
}) {
  const tabs: { id: RunTab; label: string; count?: number }[] = [
    { id: 'shards', label: 'Shards', count: shardCount },
    { id: 'tests', label: 'Tests', count: run.testCount },
    { id: 'failures', label: 'Failures', count: clusters.length },
    { id: 'logs', label: 'Logs' },
    { id: 'trace', label: 'Trace' },
    { id: 'plan', label: 'Plan' },
  ];
  return (
    <div style={{ padding: '0 24px' }}>
      <div className="tabs">
        {tabs.map((t) => (
          <div key={t.id} className={`tab ${tab === t.id ? 'is-active' : ''}`} onClick={() => setTab(t.id)}>
            {t.label}
            {t.count != null && <span className="tab__count">{t.count}</span>}
          </div>
        ))}
      </div>
    </div>
  );
}

function EmptyTabState({ tab }: { tab: string }) {
  return (
    <div
      style={{
        padding: 60,
        textAlign: 'center',
        color: 'var(--sr-fg-muted)',
        fontFamily: 'var(--sr-font-mono)',
        fontSize: 12,
        border: '1px dashed var(--sr-border)',
        borderRadius: 'var(--sr-radius)',
      }}
    >
      <strong style={{ color: 'var(--sr-fg)', fontSize: 13 }}>{tab}</strong> tab — not designed in this living mock. The{' '}
      <em>Shards</em> tab is the marquee surface.
    </div>
  );
}
