'use client';

// ====================================================
// TEO — Run detail: shard Gantt + side panels
// Ported from run-gantt.jsx.
// ====================================================

import { useState } from 'react';
import { Chip, StatusBadge } from './atoms';
import { fmt, CAT_COLOR } from '@/lib/teo-format';
import type { Run, Shard, Cluster } from '@/lib/teo-data';

type ShardFilter = 'all' | 'running' | 'fail' | 'preempt' | 'slow';

export function ShardsPanel({
  shards,
  tEnd,
  ticks,
  elapsed,
  selected,
  setSelected,
  setHover,
}: {
  shards: Shard[];
  tEnd: number;
  ticks: number[];
  elapsed: number;
  selected: number | null;
  setSelected: (i: number | null) => void;
  hover: number | null;
  setHover: (i: number | null) => void;
}) {
  const [filter, setFilter] = useState<ShardFilter>('all');

  const filtered = shards.filter((s) => {
    if (filter === 'all') return true;
    if (filter === 'running') return s.status === 'running';
    if (filter === 'fail') return s.status === 'fail';
    if (filter === 'preempt') return s.status === 'preempt';
    if (filter === 'slow') return (s.actual ?? elapsed) > s.pred * 1.1;
    return true;
  });

  // Sort longest first (LPT echo)
  const sorted = [...filtered].sort((a, b) => {
    const ad = a.end ?? elapsed;
    const bd = b.end ?? elapsed;
    return bd - ad;
  });

  return (
    <div className="panel">
      <div className="panel__head">
        <div style={{ display: 'flex', alignItems: 'center', gap: 14 }}>
          <span className="panel__title">Shard Gantt</span>
          <span className="mono muted" style={{ fontSize: 11 }}>
            LPT bin-pack · 4/3-OPT bound · sorted by actual duration
          </span>
        </div>
        <div className="chip-row">
          <Chip on={filter === 'all'} onClick={() => setFilter('all')}>
            all <span className="chip__count">{shards.length}</span>
          </Chip>
          <Chip on={filter === 'running'} onClick={() => setFilter('running')}>
            running <span className="chip__count">{shards.filter((s) => s.status === 'running').length}</span>
          </Chip>
          <Chip on={filter === 'fail'} onClick={() => setFilter('fail')}>
            fail <span className="chip__count">{shards.filter((s) => s.status === 'fail').length}</span>
          </Chip>
          <Chip on={filter === 'preempt'} onClick={() => setFilter('preempt')}>
            preempt <span className="chip__count">{shards.filter((s) => s.status === 'preempt').length}</span>
          </Chip>
          <Chip on={filter === 'slow'} onClick={() => setFilter('slow')}>
            over 10% slow
          </Chip>
        </div>
      </div>
      <div className="panel__body panel__body--flush">
        <GanttTimeline tEnd={tEnd} ticks={ticks} elapsed={elapsed} />
        <div className="gantt__axis">
          <div>shard</div>
          <div>execution · prediction band</div>
          <div style={{ textAlign: 'right' }}>actual / pred</div>
          <div style={{ textAlign: 'right' }}>status</div>
        </div>
        <div className="scroll" style={{ maxHeight: 520, overflow: 'auto' }}>
          {sorted.map((s) => (
            <GanttRow
              key={s.i}
              shard={s}
              tEnd={tEnd}
              elapsed={elapsed}
              isSelected={selected === s.i}
              onClick={() => setSelected(selected === s.i ? null : s.i)}
              onHover={setHover}
            />
          ))}
        </div>
        <ShardLegend />
      </div>
    </div>
  );
}

function GanttTimeline({ tEnd, ticks, elapsed }: { tEnd: number; ticks: number[]; elapsed: number }) {
  // The "now" cursor reflects elapsed seconds along the same scale
  const nowPct = (elapsed / tEnd) * 100;
  return (
    <div
      className="gantt__timeline"
      style={{ display: 'grid', gridTemplateColumns: '64px 1fr 90px 56px', gap: 12, padding: '0 14px' }}
    >
      <div />
      <div style={{ position: 'relative', height: 24 }}>
        {ticks.map((t, i) => {
          const left = (t / tEnd) * 100;
          return (
            <div
              key={i}
              style={{
                position: 'absolute',
                left: `${left}%`,
                top: 0,
                bottom: 0,
                borderLeft: i === 0 ? '0' : '1px dashed var(--sr-rule)',
                paddingLeft: 4,
                display: 'flex',
                alignItems: 'center',
                fontSize: 10,
                color: 'var(--sr-fg-muted)',
                fontFamily: 'var(--sr-font-mono)',
              }}
            >
              {fmt.duration(t)}
            </div>
          );
        })}
        <div className="gantt__now" style={{ left: `${nowPct}%` }} />
      </div>
      <div />
      <div />
    </div>
  );
}

function GanttRow({
  shard,
  tEnd,
  elapsed,
  isSelected,
  onClick,
  onHover,
}: {
  shard: Shard;
  tEnd: number;
  elapsed: number;
  isSelected: boolean;
  onClick: () => void;
  onHover: (i: number | null) => void;
}) {
  const s = shard;
  const dur = s.end != null ? s.end : elapsed;
  const startPct = (s.start / tEnd) * 100;
  const widthPct = ((dur - s.start) / tEnd) * 100;
  const predLeft = (s.start / tEnd) * 100;
  const predWidth = (s.pred / tEnd) * 100;

  const cls = (() => {
    switch (s.status) {
      case 'pass':
        return 'gantt__bar--pass';
      case 'fail':
        return 'gantt__bar--fail';
      case 'running':
        return 'gantt__bar--run';
      case 'preempt':
        return 'gantt__bar--preempt';
      default:
        return '';
    }
  })();

  const dotColor = (() => {
    switch (s.status) {
      case 'pass':
        return 'var(--sr-pass)';
      case 'fail':
        return 'var(--sr-fail)';
      case 'running':
        return 'var(--sr-info)';
      case 'preempt':
        return 'var(--sr-warn)';
      default:
        return 'var(--sr-skip)';
    }
  })();

  // Test cells: distribute s.tests count along the bar with s.fails marked
  const cells: string[] = Array.from({ length: Math.min(s.tests, 40) }, (_, i) => (i < s.fails ? 'fail' : 'ok'));

  return (
    <div
      className={`gantt__row ${isSelected ? 'is-selected' : ''}`}
      onClick={onClick}
      onMouseEnter={() => onHover(s.i)}
      onMouseLeave={() => onHover(null)}
    >
      <div className="gantt__idx">
        <span className="gantt__idx-dot" style={{ background: dotColor }} />#{s.i.toString().padStart(2, '0')}
      </div>
      <div className="gantt__track">
        {/* Prediction band */}
        <div className="gantt__bar gantt__bar--pred" style={{ left: `${predLeft}%`, width: `${predWidth}%` }} />
        {/* Actual bar */}
        <div className={`gantt__bar ${cls}`} style={{ left: `${startPct}%`, width: `${widthPct}%` }}>
          {widthPct > 14 && (
            <span style={{ position: 'relative', zIndex: 1, mixBlendMode: 'screen' }}>{s.tests} tests</span>
          )}
          <div className="gantt__cells">
            {cells.map((c, i) => (
              <div key={i} className={`gantt__cell ${c === 'fail' ? 'gantt__cell--fail' : ''}`} />
            ))}
          </div>
        </div>
      </div>
      <div className="gantt__time">
        <small>{fmt.duration(dur)}</small> / {fmt.duration(s.pred)}
      </div>
      <div className="gantt__status">
        <StatusBadge status={s.status} />
      </div>
    </div>
  );
}

function ShardLegend() {
  return (
    <div
      style={{
        display: 'flex',
        gap: 16,
        padding: '8px 14px',
        borderTop: '1px solid var(--sr-border)',
        fontFamily: 'var(--sr-font-mono)',
        fontSize: 10,
        color: 'var(--sr-fg-muted)',
      }}
    >
      <span style={{ display: 'inline-flex', alignItems: 'center', gap: 6 }}>
        <span style={{ width: 14, height: 8, background: 'var(--sr-pass)', borderRadius: 2 }} /> pass
      </span>
      <span style={{ display: 'inline-flex', alignItems: 'center', gap: 6 }}>
        <span style={{ width: 14, height: 8, background: 'var(--sr-fail)', borderRadius: 2 }} /> fail
      </span>
      <span style={{ display: 'inline-flex', alignItems: 'center', gap: 6 }}>
        <span style={{ width: 14, height: 8, background: 'var(--sr-info)', borderRadius: 2 }} /> running
      </span>
      <span style={{ display: 'inline-flex', alignItems: 'center', gap: 6 }}>
        <span
          style={{
            width: 14,
            height: 8,
            background: 'var(--sr-warn)',
            borderRadius: 2,
            backgroundImage: 'repeating-linear-gradient(45deg, transparent 0 3px, rgba(0,0,0,.2) 3px 6px)',
          }}
        />{' '}
        preempt
      </span>
      <span style={{ display: 'inline-flex', alignItems: 'center', gap: 6 }}>
        <span style={{ width: 14, height: 8, border: '1px dashed var(--sr-border)', borderRadius: 2 }} /> predicted band
      </span>
      <span style={{ display: 'inline-flex', alignItems: 'center', gap: 6 }}>
        <span style={{ width: 1, height: 14, background: 'var(--sr-fail)' }} /> NOW
      </span>
      <span style={{ marginLeft: 'auto' }}>click a row to inspect · drag the timeline to zoom</span>
    </div>
  );
}

// --- Predictor accuracy panel ---
export function PredictorAccuracy({ run, shards }: { run: Run; shards: Shard[] }) {
  // Top 8 by actual duration, showing predicted vs actual
  const top = [...shards]
    .filter((s) => s.end != null)
    .sort((a, b) => (b.end as number) - (a.end as number))
    .slice(0, 8);

  const maxV = Math.max(...top.map((s) => Math.max(s.pred, s.end as number)));

  return (
    <div className="panel">
      <div className="panel__head">
        <span className="panel__title">Predictor accuracy</span>
        <span className="mono" style={{ fontSize: 11, color: 'var(--sr-fg-muted)' }}>
          MAE <span style={{ color: 'var(--sr-fg)' }}>{run.predictor.mae.toFixed(1)}s</span> · ρ{' '}
          <span style={{ color: 'var(--sr-fg)' }}>{run.predictor.rho.toFixed(2)}</span>
          {run.predictor.modelVersion ? (
            <>
              {' · '}
              <span style={{ color: 'var(--sr-fg)' }}>{run.predictor.modelVersion}</span>
            </>
          ) : null}
        </span>
      </div>
      <div className="panel__body">
        <div className="predictor">
          {top.map((s) => {
            const end = s.end as number;
            const predPct = (s.pred / maxV) * 100;
            const actualPct = (end / maxV) * 100;
            const delta = end - s.pred;
            const off = delta / s.pred;
            return (
              <div key={s.i} className="predict-line">
                <div>
                  <div className="predict-line__label">
                    #{s.i.toString().padStart(2, '0')} · {s.tests} tests
                    {s.confidence != null ? (
                      <span style={{ marginLeft: 6, opacity: 0.7 }}>
                        · conf {(s.confidence * 100).toFixed(0)}%
                      </span>
                    ) : null}
                  </div>
                  <div className="predict-line__bars" style={{ marginTop: 4 }}>
                    <div className="predict-line__bar-pred" style={{ width: `${predPct}%` }} />
                    <div className="predict-line__bar-actual" style={{ width: `${actualPct}%`, opacity: 0.85 }} />
                  </div>
                </div>
                <div
                  style={{
                    fontFamily: 'var(--sr-font-mono)',
                    fontSize: 11,
                    color: Math.abs(off) < 0.1 ? 'var(--sr-pass)' : off > 0 ? 'var(--sr-fail)' : 'var(--sr-warn)',
                    textAlign: 'right',
                    minWidth: 56,
                  }}
                >
                  {delta >= 0 ? '+' : ''}
                  {delta}s<div style={{ fontSize: 10, opacity: 0.7 }}>{(off * 100).toFixed(0)}%</div>
                </div>
              </div>
            );
          })}
        </div>
        <div className="hr" style={{ margin: '10px 0 0' }} />
        <div
          style={{
            marginTop: 10,
            display: 'grid',
            gridTemplateColumns: '1fr 1fr',
            gap: 8,
            fontFamily: 'var(--sr-font-mono)',
            fontSize: 11,
            color: 'var(--sr-fg-muted)',
          }}
        >
          <div>
            p50 delta{' '}
            <span style={{ color: 'var(--sr-pass)' }}>
              {run.predictor.p50Delta > 0 ? '+' : ''}
              {(run.predictor.p50Delta * 100).toFixed(1)}%
            </span>
          </div>
          <div>
            p95 delta{' '}
            <span style={{ color: 'var(--sr-warn)' }}>
              {run.predictor.p95Delta > 0 ? '+' : ''}
              {(run.predictor.p95Delta * 100).toFixed(1)}%
            </span>
          </div>
        </div>
      </div>
    </div>
  );
}

// --- Failure clusters preview ---
export function FailureClustersPreview({ clusters }: { clusters: Cluster[] }) {
  return (
    <div className="panel">
      <div className="panel__head">
        <span className="panel__title">Failure clusters · this run</span>
        <a
          className="mono"
          style={{ fontSize: 11, color: 'var(--sr-info)', cursor: 'pointer' }}
          href="/clusters"
        >
          open map →
        </a>
      </div>
      <div className="panel__body panel__body--flush">
        {clusters.map((c) => (
          <div key={c.id} className="cluster-row" style={{ borderBottom: '1px solid var(--sr-rule)' }}>
            <span className="cluster-row__cat" style={{ background: CAT_COLOR[c.category] }} />
            <div style={{ minWidth: 0 }}>
              <div className="cluster-row__title">{c.title}</div>
              <div className="cluster-row__sub">
                {c.file} · {c.category}
              </div>
            </div>
            <div style={{ textAlign: 'right' }}>
              <div className="cluster-row__count">{c.occurrences}</div>
              <div className="cluster-row__sub">{c.affectedRuns} runs</div>
            </div>
          </div>
        ))}
      </div>
    </div>
  );
}

// --- Fleet panel ---
export function FleetPanel({ run, shards }: { run: Run; shards: Shard[] }) {
  const spot = shards.filter((s) => s.worker.includes('r')).length;
  const preempted = shards.filter((s) => s.status === 'preempt').length;
  return (
    <div className="panel">
      <div className="panel__head">
        <span className="panel__title">Fleet</span>
        <span className="mono" style={{ fontSize: 11, color: 'var(--sr-fg-muted)' }}>
          {run.workerType}
        </span>
      </div>
      <div className="panel__body">
        <div className="stat-grid">
          <div className="stat-cell">
            <div className="stat-cell__label">Workers</div>
            <div className="stat-cell__value">{run.workerCount}</div>
          </div>
          <div className="stat-cell">
            <div className="stat-cell__label">Spot share</div>
            <div className="stat-cell__value">{Math.round((spot / shards.length) * 100)}%</div>
          </div>
          <div className="stat-cell">
            <div className="stat-cell__label">Preemptions</div>
            <div className="stat-cell__value" style={{ color: preempted ? 'var(--sr-warn)' : 'var(--sr-fg)' }}>
              {preempted}
            </div>
          </div>
          <div className="stat-cell">
            <div className="stat-cell__label">Reshard cost</div>
            <div className="stat-cell__value">{fmt.duration(preempted * 12)}</div>
          </div>
        </div>
        <div style={{ marginTop: 12, fontFamily: 'var(--sr-font-mono)', fontSize: 11, color: 'var(--sr-fg-muted)' }}>
          IMDSv2 poller · 5s heartbeat · drain → NAK → reshard residue
        </div>
      </div>
    </div>
  );
}
