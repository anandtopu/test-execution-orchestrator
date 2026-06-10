'use client';

// ====================================================
// TEO — Flaky tests screen (Wilson interval + sparkline)
// Ported from flakes.jsx. Mounted at "/flakes".
// ====================================================

import { useState, useMemo } from 'react';
import { Chip, StatusBadge, Sparkline, Kpi } from './atoms';
import { Icon } from './Icons';
import { fmt, CAT_COLOR } from '@/lib/teo-format';
import { type Flake } from '@/lib/teo-data';

type SortKey = keyof Flake;
type SortDir = 'asc' | 'desc';

/** Coerce a flake field to something comparable; objects (owner) collapse to a
 * stable constant so sorting by them is a no-op, matching the prototype. */
function cmpVal(v: unknown): string | number {
  if (typeof v === 'number') return v;
  if (typeof v === 'string') return v;
  return '';
}

export function FlakesScreen({ flakes }: { flakes: Flake[] }) {
  const [sortKey, setSortKey] = useState<SortKey>('rate');
  const [sortDir, setSortDir] = useState<SortDir>('desc');
  const [catFilter, setCatFilter] = useState<string>('all');
  const [statusFilter, setStatusFilter] = useState<string>('all');
  const [selectedId, setSelectedId] = useState<string | null>(null);

  const categories = [...new Set(flakes.map((f) => f.category))];

  const filtered = flakes.filter((f) => {
    if (catFilter !== 'all' && f.category !== catFilter) return false;
    if (statusFilter !== 'all' && f.status !== statusFilter) return false;
    return true;
  });

  const sorted = useMemo(() => {
    const arr = [...filtered];
    arr.sort((a, b) => {
      const av = cmpVal(a[sortKey]);
      const bv = cmpVal(b[sortKey]);
      const cmp = av < bv ? -1 : av > bv ? 1 : 0;
      return sortDir === 'asc' ? cmp : -cmp;
    });
    return arr;
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [filtered, sortKey, sortDir]);

  const handleSort = (key: SortKey) => {
    if (sortKey === key) setSortDir((d) => (d === 'asc' ? 'desc' : 'asc'));
    else {
      setSortKey(key);
      setSortDir('desc');
    }
  };

  // Aggregate stats
  const totals = {
    flagged: flakes.filter((f) => f.status === 'flagged').length,
    quarantined: flakes.filter((f) => f.status === 'quarantined').length,
    minutesCost: flakes.reduce((m, f) => m + f.durMean * f.samples * f.rate, 0) / 60,
    wilsonMean: flakes.length > 0 ? flakes.reduce((s, f) => s + f.wLo, 0) / flakes.length : 0,
  };

  const selected = flakes.find((f) => f.id === selectedId);

  return (
    <div>
      <div className="page-header" style={{ display: 'flex', alignItems: 'baseline', gap: 16 }}>
        <div>
          <h1>Flaky tests</h1>
          <p>
            Wilson 95% lower-bound flake rate · {flakes.length} tests · last 30d · {totals.minutesCost.toFixed(1)} min CI
            burned
          </p>
        </div>
        <div style={{ marginLeft: 'auto', display: 'flex', gap: 6 }}>
          <button className="btn">
            <Icon.Download /> Export
          </button>
          <button className="btn">
            <Icon.Filter /> Saved views
          </button>
        </div>
      </div>

      <div className="page-pad">
        <div className="kpi-strip" style={{ gridTemplateColumns: 'repeat(4, 1fr)', marginBottom: 16 }}>
          <Kpi label="Tracked" value={flakes.length} sub="≥30 samples, ≥1 failure last 30d" />
          <Kpi label="Flagged" value={totals.flagged} sub={<>candidates · awaiting Wilson confirm</>} />
          <Kpi
            label="Quarantined"
            value={totals.quarantined}
            sub={
              <>
                auto-issued ·{' '}
                {flakes.filter((f) => f.status === 'quarantined').reduce((s, f) => s + f.quarantinedDays, 0)} days total
              </>
            }
          />
          <Kpi
            label="CI burn"
            value={`${totals.minutesCost.toFixed(0)}m`}
            sub={<>=&nbsp;{fmt.dollars(totals.minutesCost * 0.012)} spot / wk</>}
          />
        </div>

        <div className="panel">
          <div className="panel__head">
            <div style={{ display: 'flex', alignItems: 'center', gap: 12, flexWrap: 'wrap' }}>
              <span className="panel__title">Flake registry</span>
              <div className="chip-row">
                <Chip on={catFilter === 'all'} onClick={() => setCatFilter('all')}>
                  all categories
                </Chip>
                {categories.map((c) => (
                  <Chip key={c} on={catFilter === c} onClick={() => setCatFilter(c)}>
                    <span style={{ width: 6, height: 6, borderRadius: 999, background: CAT_COLOR[c] || 'var(--sr-skip)' }} />
                    {c}
                  </Chip>
                ))}
              </div>
            </div>
            <div className="chip-row">
              <Chip on={statusFilter === 'all'} onClick={() => setStatusFilter('all')}>
                all
              </Chip>
              <Chip on={statusFilter === 'flagged'} onClick={() => setStatusFilter('flagged')}>
                flagged
              </Chip>
              <Chip on={statusFilter === 'quarantined'} onClick={() => setStatusFilter('quarantined')}>
                quarantined
              </Chip>
            </div>
          </div>
          <div className="panel__body panel__body--flush">
            <FlakeTable
              flakes={sorted}
              sortKey={sortKey}
              sortDir={sortDir}
              onSort={handleSort}
              selectedId={selectedId}
              setSelectedId={setSelectedId}
            />
          </div>
        </div>

        {selected && <FlakeDetailSheet flake={selected} onClose={() => setSelectedId(null)} />}
      </div>
    </div>
  );
}

function FlakeTable({
  flakes,
  sortKey,
  sortDir,
  onSort,
  selectedId,
  setSelectedId,
}: {
  flakes: Flake[];
  sortKey: SortKey;
  sortDir: SortDir;
  onSort: (key: SortKey) => void;
  selectedId: string | null;
  setSelectedId: (id: string | null) => void;
}) {
  const headers: { key: SortKey; label: string; w: string }[] = [
    { key: 'name', label: 'Test', w: 'minmax(280px, 2fr)' },
    { key: 'owner', label: 'Owner', w: '80px' },
    { key: 'rate', label: 'Rate', w: '70px' },
    { key: 'wLo', label: 'Wilson 95% CI', w: '200px' },
    { key: 'samples', label: 'Samples', w: '80px' },
    { key: 'category', label: 'Category', w: '140px' },
    { key: 'spark', label: 'Last 20', w: '120px' },
    { key: 'status', label: 'Status', w: '120px' },
    { key: 'durMean', label: 'μ dur', w: '60px' },
  ];
  const arrow = (k: SortKey) => (sortKey === k ? (sortDir === 'asc' ? '↑' : '↓') : '');

  return (
    <table className="flake-table">
      <thead>
        <tr>
          {headers.map((h) => (
            <th key={h.key} onClick={() => onSort(h.key)} className={sortKey === h.key ? 'is-sorted' : ''} style={{ width: h.w }}>
              {h.label} <span style={{ marginLeft: 2 }}>{arrow(h.key)}</span>
            </th>
          ))}
        </tr>
      </thead>
      <tbody>
        {flakes.map((f) => (
          <tr
            key={f.id}
            className={selectedId === f.id ? 'is-selected' : ''}
            onClick={() => setSelectedId(selectedId === f.id ? null : f.id)}
          >
            <td>
              <div className="flake-path">
                <span className="flake-path__name">{f.name}</span>
                <span className="flake-path__file">{f.file}</span>
              </div>
            </td>
            <td>
              <span className={`owner owner--${f.owner.c}`}>{f.owner.i}</span>
            </td>
            <td className="mono" style={{ fontWeight: 600 }}>
              {fmt.pct(f.rate)}
            </td>
            <td>
              <WilsonBar wLo={f.wLo} wHi={f.wHi} rate={f.rate} />
              <div className="wilson-numbers">
                <span>{fmt.pctShort(f.wLo)}</span>
                <span>{fmt.pctShort(f.wHi)}</span>
              </div>
            </td>
            <td className="mono">{f.samples}</td>
            <td>
              <span
                className="badge badge--plain"
                style={{
                  background: (CAT_COLOR[f.category] || 'var(--sr-skip)') + '22',
                  color: CAT_COLOR[f.category] || 'var(--sr-skip)',
                }}
              >
                {f.category}
              </span>
            </td>
            <td>
              <Sparkline s={f.spark} />
            </td>
            <td>
              <StatusBadge status={f.status} />
              {f.status === 'quarantined' && (
                <div className="mono muted" style={{ fontSize: 10, marginTop: 2 }}>
                  {f.quarantinedDays}d
                </div>
              )}
            </td>
            <td className="mono">{f.durMean.toFixed(1)}s</td>
          </tr>
        ))}
      </tbody>
    </table>
  );
}

function WilsonBar({ wLo, wHi, rate }: { wLo: number; wHi: number; rate: number }) {
  // Bar goes 0..50%. Threshold marker at 5%.
  const SCALE = 0.5;
  const pctOf = (x: number) => Math.min(100, (x / SCALE) * 100);
  return (
    <div className="wilson-bar">
      <div className="wilson-bar__threshold" style={{ left: `${pctOf(0.05)}%` }} title="threshold 5%" />
      <div
        className="wilson-bar__range"
        style={{
          left: `${pctOf(wLo)}%`,
          width: `${pctOf(wHi - wLo)}%`,
        }}
      />
      <div className="wilson-bar__point" style={{ left: `${pctOf(rate)}%` }} />
    </div>
  );
}

function FlakeDetailSheet({ flake, onClose }: { flake: Flake; onClose: () => void }) {
  return (
    <div
      style={{
        marginTop: 16,
        border: '1px solid var(--sr-border)',
        borderRadius: 'var(--sr-radius)',
        background: 'var(--sr-bg)',
        overflow: 'hidden',
      }}
    >
      <div
        style={{
          padding: '12px 16px',
          borderBottom: '1px solid var(--sr-border)',
          display: 'flex',
          alignItems: 'center',
          gap: 12,
        }}
      >
        <span className="mono" style={{ fontSize: 13, fontWeight: 600 }}>
          {flake.name}
        </span>
        <span className="mono muted" style={{ fontSize: 11 }}>
          {flake.file}
        </span>
        <span style={{ marginLeft: 'auto', display: 'flex', gap: 6 }}>
          <button className="btn btn--primary">
            <Icon.Bug /> Quarantine
          </button>
          <button className="btn">Open issue</button>
          <button className="btn">Reassign</button>
          <button className="btn btn--ghost" onClick={onClose}>
            ✕
          </button>
        </span>
      </div>
      <div style={{ padding: 16, display: 'grid', gridTemplateColumns: '1fr 1fr 1fr', gap: 16 }}>
        <Panel title="Wilson detail">
          <div style={{ display: 'flex', flexDirection: 'column', gap: 8, fontFamily: 'var(--sr-font-mono)', fontSize: 12 }}>
            <Row label="Observed rate" value={fmt.pct(flake.rate)} />
            <Row label="Wilson lower" value={fmt.pct(flake.wLo)} good={flake.wLo > 0.05 ? 'bad' : 'ok'} />
            <Row label="Wilson upper" value={fmt.pct(flake.wHi)} />
            <Row label="Sample size" value={`n=${flake.samples}`} />
            <Row label="Confidence" value="95%" />
            <Row label="Threshold" value="5%" />
          </div>
          <div style={{ marginTop: 10 }}>
            <WilsonBar wLo={flake.wLo} wHi={flake.wHi} rate={flake.rate} />
            <div className="wilson-numbers">
              <span>0%</span>
              <span>50%</span>
            </div>
          </div>
        </Panel>
        <Panel title="Recent runs">
          <div style={{ display: 'flex', flexDirection: 'column', gap: 4 }}>
            {flake.spark
              .split('')
              .slice(0, 8)
              .map((ch, i) => (
                <div
                  key={i}
                  style={{
                    display: 'grid',
                    gridTemplateColumns: 'auto 1fr auto auto',
                    gap: 8,
                    alignItems: 'center',
                    padding: '4px 6px',
                    fontFamily: 'var(--sr-font-mono)',
                    fontSize: 11,
                    borderRadius: 'var(--sr-radius-sm)',
                    background: i % 2 ? 'var(--sr-bg-muted)' : 'transparent',
                  }}
                >
                  <StatusBadge status={ch === 'P' ? 'passed' : ch === 'F' ? 'failed' : 'skipped'} />
                  <span className="muted">a{Math.random().toString(16).slice(2, 9)}</span>
                  <span className="muted">{(flake.durMean + (Math.random() - 0.5) * 2).toFixed(1)}s</span>
                  <span className="muted">{i}h ago</span>
                </div>
              ))}
          </div>
        </Panel>
        <Panel title="Suspected cause">
          <div style={{ fontSize: 12, lineHeight: '18px' }}>
            <p style={{ margin: '0 0 8px' }}>
              Heuristic categorizer classifies this as{' '}
              <strong style={{ color: CAT_COLOR[flake.category] || 'var(--sr-fg)' }}>{flake.category}</strong>. Signals:
            </p>
            <ul
              style={{
                margin: 0,
                paddingLeft: 18,
                color: 'var(--sr-fg-muted)',
                fontFamily: 'var(--sr-font-mono)',
                fontSize: 11,
              }}
            >
              {flake.category === 'async/timing' && (
                <>
                  <li>
                    contains <code>time.Sleep</code> + <code>time.Since</code>
                  </li>
                  <li>variance(σ) = 1.42s — 31% of mean</li>
                  <li>fails cluster around CI cold-start windows</li>
                </>
              )}
              {flake.category === 'race' && (
                <>
                  <li>
                    shared <code>state.mu</code> across 3 goroutines
                  </li>
                  <li>
                    only fails under <code>-race</code>
                  </li>
                  <li>fail rate ↑ on c7g.xlarge spot vs on-demand</li>
                </>
              )}
              {flake.category === 'network' && (
                <>
                  <li>
                    uses <code>http.Get</code> against localhost:5432
                  </li>
                  <li>fails cluster around testcontainers startup</li>
                  <li>retry-after-30s would have caught 8 of 11</li>
                </>
              )}
              {flake.category === 'order-dependent' && (
                <>
                  <li>passes in isolation, fails after TestOther</li>
                  <li>
                    writes to global <code>fixtures/</code> dir
                  </li>
                </>
              )}
              {flake.category === 'resource-leak' && (
                <>
                  <li>goroutine count grows monotonically</li>
                  <li>db conn pool exhausted at run 47</li>
                </>
              )}
              {flake.category === 'env-dependent' && (
                <>
                  <li>fails only on linux/arm64</li>
                  <li>uses time.Local instead of UTC</li>
                </>
              )}
              {flake.category === 'randomness' && (
                <>
                  <li>seeded with time.Now().UnixNano()</li>
                  <li>asserts on order of map iteration</li>
                </>
              )}
            </ul>
            <p
              style={{
                margin: '10px 0 0',
                color: 'var(--sr-fg-muted)',
                fontFamily: 'var(--sr-font-mono)',
                fontSize: 11,
              }}
            >
              FlakyFix v1 (opt-in) is off — see Settings · Predictor.
            </p>
          </div>
        </Panel>
      </div>
    </div>
  );
}

function Panel({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <div className="panel">
      <div className="panel__head" style={{ minHeight: 32, padding: '6px 12px' }}>
        <span className="panel__title">{title}</span>
      </div>
      <div className="panel__body">{children}</div>
    </div>
  );
}

function Row({ label, value, good }: { label: string; value: string; good?: 'ok' | 'bad' }) {
  const color = good === 'ok' ? 'var(--sr-pass)' : good === 'bad' ? 'var(--sr-fail)' : 'var(--sr-fg)';
  return (
    <div style={{ display: 'flex', justifyContent: 'space-between' }}>
      <span className="muted">{label}</span>
      <span style={{ color }}>{value}</span>
    </div>
  );
}
