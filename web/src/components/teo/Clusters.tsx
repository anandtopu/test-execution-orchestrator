'use client';

// ====================================================
// TEO — Failure Clusters screen (spatial map) — the novel surface
// Ported from clusters.jsx. Mounted at "/clusters".
// ====================================================

import { useState, useMemo, useRef, useEffect } from 'react';
import { Chip, StatusBadge } from './atoms';
import { Icon } from './Icons';
import { CAT_COLOR } from '@/lib/teo-format';
import { type Cluster, type ClusterCategory } from '@/lib/teo-data';

const MAP_CATEGORIES: ClusterCategory[] = ['assertion', 'timeout', 'panic', 'network', 'race'];

interface Edge {
  a: Cluster;
  b: Cluster;
}

export function ClustersScreen({ clusters }: { clusters: Cluster[] }) {
  const [selectedId, setSelectedId] = useState<string>(clusters[0]?.id ?? '');
  const [hoverId, setHoverId] = useState<string | null>(null);
  const [categoryFilter, setCategoryFilter] = useState<'all' | ClusterCategory>('all');

  const visible = useMemo(
    () => (categoryFilter === 'all' ? clusters : clusters.filter((c) => c.category === categoryFilter)),
    [clusters, categoryFilter],
  );

  const selected = clusters.find((c) => c.id === selectedId) || clusters[0];

  // Edges between related clusters.
  // NB: every hook must run on every render (Rules of Hooks). `clusters` is a
  // server-fetched prop that can legitimately flip empty<->non-empty on an
  // in-place re-render (App Router soft-nav / revalidation / a future client
  // poll), so the empty-state early return MUST stay below all hook calls — do
  // not hoist it between `visible` and `edges`.
  const edges = useMemo(() => {
    const e: Edge[] = [];
    clusters.forEach((c) => {
      (c.related || []).forEach((r) => {
        const t = clusters.find((x) => x.id === r);
        if (!t) return;
        // avoid duplicates
        if (c.id < r) e.push({ a: c, b: t });
      });
    });
    return e;
  }, [clusters]);

  if (clusters.length === 0) {
    return (
      <div className="page-pad">
        <div className="panel">
          <div className="panel__body" style={{ padding: 32, textAlign: 'center' }}>
            <h1 style={{ margin: '0 0 8px', fontSize: 16, fontWeight: 600 }}>Failure clusters</h1>
            <p className="mono muted" style={{ margin: 0, fontSize: 12 }}>
              No failure clusters in the last 30d.
            </p>
          </div>
        </div>
      </div>
    );
  }

  return (
    <div className="clusters-grid">
      <ClusterListPanel
        clusters={visible}
        selectedId={selectedId}
        setSelectedId={setSelectedId}
        categoryFilter={categoryFilter}
        setCategoryFilter={setCategoryFilter}
        categories={MAP_CATEGORIES}
      />
      <ClusterMap
        clusters={clusters}
        visible={visible}
        edges={edges}
        selectedId={selectedId}
        setSelectedId={setSelectedId}
        hoverId={hoverId}
        setHoverId={setHoverId}
      />
      <ClusterDetail cluster={selected} clusters={clusters} setSelectedId={setSelectedId} />
    </div>
  );
}

function ClusterListPanel({
  clusters,
  selectedId,
  setSelectedId,
  categoryFilter,
  setCategoryFilter,
  categories,
}: {
  clusters: Cluster[];
  selectedId: string;
  setSelectedId: (id: string) => void;
  categoryFilter: 'all' | ClusterCategory;
  setCategoryFilter: (c: 'all' | ClusterCategory) => void;
  categories: ClusterCategory[];
}) {
  return (
    <div className="cluster-list">
      <div className="cluster-list__head">
        <div style={{ display: 'flex', alignItems: 'baseline', justifyContent: 'space-between', marginBottom: 8 }}>
          <h1 style={{ margin: 0, fontSize: 16, fontWeight: 600 }}>Failure clusters</h1>
          <span className="mono muted" style={{ fontSize: 11 }}>
            {clusters.length}
          </span>
        </div>
        <p className="mono muted" style={{ margin: '0 0 10px', fontSize: 11 }}>
          Stack-trace fingerprint dedup · last 30d
        </p>
        <div className="chip-row">
          <Chip on={categoryFilter === 'all'} onClick={() => setCategoryFilter('all')}>
            all
          </Chip>
          {categories.map((c) => (
            <Chip key={c} on={categoryFilter === c} onClick={() => setCategoryFilter(c)}>
              <span style={{ width: 6, height: 6, borderRadius: 999, background: CAT_COLOR[c] }} />
              {c}
            </Chip>
          ))}
        </div>
      </div>
      <div className="cluster-list__scroll">
        {clusters.map((c) => (
          <div
            key={c.id}
            className={`cluster-row ${selectedId === c.id ? 'is-selected' : ''}`}
            onClick={() => setSelectedId(c.id)}
          >
            <span className="cluster-row__cat" style={{ background: CAT_COLOR[c.category] }} />
            <div style={{ minWidth: 0 }}>
              <div className="cluster-row__title">{c.title}</div>
              <div className="cluster-row__sub">{c.file}</div>
              <div style={{ marginTop: 4, display: 'flex', gap: 6, alignItems: 'center' }}>
                <span
                  className="badge badge--plain"
                  style={{ background: 'transparent', border: '1px solid var(--sr-border)' }}
                >
                  {c.category}
                </span>
                <span className="cluster-row__sub" style={{ marginTop: 0 }}>
                  {c.affectedRuns} runs
                </span>
              </div>
            </div>
            <div style={{ textAlign: 'right' }}>
              <div className="cluster-row__count">{c.occurrences}</div>
              <div className="cluster-row__sub">occ</div>
            </div>
          </div>
        ))}
      </div>
    </div>
  );
}

interface Tooltip {
  x: number;
  y: number;
  cluster: Cluster;
}

function ClusterMap({
  clusters,
  visible,
  edges,
  selectedId,
  setSelectedId,
  hoverId,
  setHoverId,
}: {
  clusters: Cluster[];
  visible: Cluster[];
  edges: Edge[];
  selectedId: string;
  setSelectedId: (id: string) => void;
  hoverId: string | null;
  setHoverId: (id: string | null) => void;
}) {
  const wrapRef = useRef<HTMLDivElement>(null);
  const [size, setSize] = useState({ w: 800, h: 600 });
  const [tooltip, setTooltip] = useState<Tooltip | null>(null);

  useEffect(() => {
    if (!wrapRef.current) return;
    const ro = new ResizeObserver(([entry]) => {
      const { width, height } = entry.contentRect;
      setSize({ w: width, h: height });
    });
    ro.observe(wrapRef.current);
    return () => ro.disconnect();
  }, []);

  const visIds = new Set(visible.map((c) => c.id));
  const selected = clusters.find((c) => c.id === selectedId);
  const relatedIds = new Set(selected ? selected.related || [] : []);

  // Project normalized coords to SVG with some padding
  const pad = 60;
  const toX = (x: number) => pad + x * (size.w - pad * 2);
  const toY = (y: number) => pad + y * (size.h - pad * 2);

  return (
    <div className="cluster-map">
      <div className="cluster-map__head">
        <div style={{ display: 'flex', alignItems: 'center', gap: 12 }}>
          <span className="panel__title">Spatial map</span>
          <span className="mono muted" style={{ fontSize: 11 }}>
            x = first seen · y = occurrences (log) · size = blast radius · edges = co-occurring runs
          </span>
        </div>
        <div className="cluster-map__legend">
          {MAP_CATEGORIES.map((k) => (
            <span key={k} className="cluster-map__legend-item">
              <span className="cluster-map__legend-dot" style={{ background: CAT_COLOR[k] }} />
              {k}
            </span>
          ))}
        </div>
      </div>
      <div ref={wrapRef} style={{ flex: 1, position: 'relative' }}>
        <svg className="cluster-map__svg" width={size.w} height={size.h}>
          {/* Grid */}
          <defs>
            <pattern id="grid" width="40" height="40" patternUnits="userSpaceOnUse">
              <path d="M 40 0 L 0 0 0 40" fill="none" stroke="var(--sr-rule)" strokeWidth="1" />
            </pattern>
            <radialGradient id="nodeShine" cx="30%" cy="30%">
              <stop offset="0%" stopColor="white" stopOpacity="0.4" />
              <stop offset="100%" stopColor="white" stopOpacity="0" />
            </radialGradient>
          </defs>
          <rect width="100%" height="100%" fill="url(#grid)" opacity="0.5" />

          {/* Axes labels */}
          <g style={{ fontFamily: 'var(--sr-font-mono)', fontSize: 10, fill: 'var(--sr-fg-muted)' }}>
            <text x={pad} y={size.h - 16}>
              ← newer
            </text>
            <text x={size.w - pad - 40} y={size.h - 16}>
              older →
            </text>
            <text x={pad} y={pad - 20}>
              ↑ more occurrences
            </text>
          </g>

          {/* Edges */}
          {edges.map((e, i) => {
            const isHi = hoverId === e.a.id || hoverId === e.b.id || selectedId === e.a.id || selectedId === e.b.id;
            return (
              <line
                key={i}
                x1={toX(e.a.x)}
                y1={toY(e.a.y)}
                x2={toX(e.b.x)}
                y2={toY(e.b.y)}
                stroke={isHi ? 'var(--sr-fg)' : 'var(--sr-border)'}
                strokeWidth={isHi ? 1.5 : 1}
                strokeDasharray={isHi ? '0' : '3 4'}
                opacity={isHi ? 0.8 : 0.5}
              />
            );
          })}

          {/* Nodes */}
          {clusters.map((c) => {
            const cx = toX(c.x);
            const cy = toY(c.y);
            const isSel = selectedId === c.id;
            const isRel = relatedIds.has(c.id);
            const isHover = hoverId === c.id;
            const dim = !visIds.has(c.id);
            const stroke = isSel ? 'var(--sr-fg)' : isRel ? CAT_COLOR[c.category] : 'transparent';
            return (
              <g
                key={c.id}
                style={{ cursor: 'pointer', opacity: dim ? 0.2 : 1, transition: 'opacity .2s' }}
                onMouseEnter={(e: React.MouseEvent<SVGGElement>) => {
                  setHoverId(c.id);
                  const svg = e.currentTarget.ownerSVGElement;
                  if (!svg) return;
                  const r = svg.getBoundingClientRect();
                  setTooltip({ x: r.left + cx, y: r.top + cy - c.r, cluster: c });
                }}
                onMouseLeave={() => {
                  setHoverId(null);
                  setTooltip(null);
                }}
                onClick={() => setSelectedId(c.id)}
              >
                <circle cx={cx} cy={cy} r={c.r + 6} fill={CAT_COLOR[c.category]} opacity={isSel || isHover ? 0.18 : 0} />
                <circle
                  cx={cx}
                  cy={cy}
                  r={c.r}
                  fill={CAT_COLOR[c.category]}
                  opacity={isSel ? 1 : isRel || isHover ? 0.95 : 0.78}
                  stroke={stroke}
                  strokeWidth={isSel ? 2 : 1.5}
                />
                <circle cx={cx} cy={cy} r={c.r} fill="url(#nodeShine)" pointerEvents="none" />
                <text
                  x={cx}
                  y={cy + 3}
                  textAnchor="middle"
                  style={{
                    fontFamily: 'var(--sr-font-mono)',
                    fontSize: c.r > 18 ? 13 : 11,
                    fontWeight: 700,
                    fill: 'white',
                    pointerEvents: 'none',
                  }}
                >
                  {c.occurrences}
                </text>
                {c.r > 18 && (
                  <text
                    x={cx}
                    y={cy + c.r + 14}
                    textAnchor="middle"
                    style={{
                      fontFamily: 'var(--sr-font-mono)',
                      fontSize: 10,
                      fill: 'var(--sr-fg-muted)',
                      pointerEvents: 'none',
                    }}
                  >
                    {c.id}
                  </text>
                )}
              </g>
            );
          })}
        </svg>
        {tooltip && (
          <div className="tooltip" style={{ left: tooltip.x, top: tooltip.y }}>
            {tooltip.cluster.title.slice(0, 56)}
            {tooltip.cluster.title.length > 56 ? '…' : ''}
            <br />
            <span style={{ opacity: 0.65 }}>
              {tooltip.cluster.occurrences} occ · {tooltip.cluster.affectedRuns} runs · {tooltip.cluster.category}
            </span>
          </div>
        )}
      </div>
    </div>
  );
}

function ClusterDetail({
  cluster,
  clusters,
  setSelectedId,
}: {
  cluster: Cluster;
  clusters: Cluster[];
  setSelectedId: (id: string) => void;
}) {
  if (!cluster) return null;
  const related = (cluster.related || [])
    .map((id) => clusters.find((c) => c.id === id))
    .filter((c): c is Cluster => Boolean(c));

  return (
    <div className="cluster-detail">
      <div className="cluster-detail__head">
        <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 8 }}>
          <span
            className="badge badge--plain"
            style={{
              background: CAT_COLOR[cluster.category] + '22',
              color: CAT_COLOR[cluster.category],
              border: `1px solid ${CAT_COLOR[cluster.category]}55`,
            }}
          >
            {cluster.category}
          </span>
          <span className="mono muted" style={{ fontSize: 11 }}>
            {cluster.id}
          </span>
          <span style={{ marginLeft: 'auto' }}>
            <button className="btn">
              <Icon.Copy />
            </button>
          </span>
        </div>
        <div className="cluster-detail__title">{cluster.title}</div>
        <div className="mono muted" style={{ fontSize: 11, marginTop: 6 }}>
          {cluster.file}
        </div>
      </div>
      <div className="cluster-detail__body">
        {cluster.rootCauseHint ? (
          <div
            className="cluster-detail__hint"
            style={{
              display: 'flex',
              gap: 8,
              padding: '10px 12px',
              marginBottom: 12,
              borderRadius: 8,
              background: 'var(--accent-soft, #f3efe6)',
              border: '1px solid var(--border, #e2dccd)',
            }}
          >
            <span aria-hidden style={{ flex: '0 0 auto' }}>
              💡
            </span>
            <div>
              <div style={{ fontWeight: 600, fontSize: 12, marginBottom: 2 }}>
                Likely cause
                {cluster.hintCategory ? <span className="muted"> · {cluster.hintCategory}</span> : null}
                {typeof cluster.hintConfidence === 'number' ? (
                  <span className="muted"> · {Math.round(cluster.hintConfidence * 100)}% conf</span>
                ) : null}
              </div>
              <div style={{ fontSize: 13 }}>{cluster.rootCauseHint}</div>
            </div>
          </div>
        ) : null}
        <div className="stat-grid">
          <div className="stat-cell">
            <div className="stat-cell__label">Occurrences</div>
            <div className="stat-cell__value">{cluster.occurrences}</div>
          </div>
          <div className="stat-cell">
            <div className="stat-cell__label">Affected runs</div>
            <div className="stat-cell__value">{cluster.affectedRuns}</div>
          </div>
          <div className="stat-cell">
            <div className="stat-cell__label">First seen</div>
            <div className="stat-cell__value" style={{ fontSize: 12 }}>
              {cluster.firstSeen}
            </div>
          </div>
          <div className="stat-cell">
            <div className="stat-cell__label">Last seen</div>
            <div className="stat-cell__value" style={{ fontSize: 12 }}>
              {cluster.lastSeen}
            </div>
          </div>
        </div>

        <div>
          <div className="panel__title" style={{ marginBottom: 6 }}>
            Stack fingerprint
          </div>
          <pre className="cluster-detail__stack">
            {cluster.stack.map((line, i) => {
              const isApp = line.includes('(app)') || line.includes('.go:') || line.includes('.py:');
              const isLib = line.includes('(lib)');
              const cls = isLib
                ? 'frame--lib'
                : isApp && (line.startsWith('  at ') || line.trim().startsWith('at '))
                  ? 'frame--app'
                  : '';
              return (
                <div key={i} className={cls}>
                  {line}
                </div>
              );
            })}
          </pre>
        </div>

        <div>
          <div className="panel__title" style={{ marginBottom: 6 }}>
            Affected tests
          </div>
          <div style={{ display: 'flex', flexDirection: 'column', gap: 2 }}>
            {cluster.tests.map((t, i) => (
              <div
                key={i}
                className="mono"
                style={{
                  fontSize: 11,
                  padding: '4px 8px',
                  background: 'var(--sr-bg-muted)',
                  borderRadius: 'var(--sr-radius-sm)',
                }}
              >
                {t}
              </div>
            ))}
          </div>
        </div>

        <div>
          <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'baseline', marginBottom: 6 }}>
            <div className="panel__title">Affected runs</div>
            <span className="mono muted" style={{ fontSize: 10 }}>
              {cluster.affectedRunIds.length} most recent
            </span>
          </div>
          <div className="affected-runs">
            {cluster.affectedRunIds.map((id) => (
              <div key={id} className="affected-run">
                <StatusBadge status="failed" />
                <span className="affected-run__sha">{id}</span>
                <span className="affected-run__when">2026-05-{20 - cluster.affectedRunIds.indexOf(id)}</span>
              </div>
            ))}
          </div>
        </div>

        {related.length > 0 && (
          <div>
            <div className="panel__title" style={{ marginBottom: 6 }}>
              Co-occurring clusters
            </div>
            <div style={{ display: 'flex', flexDirection: 'column', gap: 4 }}>
              {related.map((r) => (
                <div
                  key={r.id}
                  onClick={() => setSelectedId(r.id)}
                  style={{
                    display: 'grid',
                    gridTemplateColumns: 'auto 1fr auto',
                    gap: 8,
                    alignItems: 'center',
                    padding: '6px 8px',
                    border: '1px solid var(--sr-border)',
                    borderRadius: 'var(--sr-radius-sm)',
                    cursor: 'pointer',
                  }}
                >
                  <span style={{ width: 8, height: 8, borderRadius: 999, background: CAT_COLOR[r.category] }} />
                  <span
                    className="mono"
                    style={{ fontSize: 11, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}
                  >
                    {r.title}
                  </span>
                  <span className="mono muted" style={{ fontSize: 10 }}>
                    {r.occurrences} occ
                  </span>
                </div>
              ))}
            </div>
          </div>
        )}

        <div style={{ display: 'flex', gap: 6, marginTop: 4 }}>
          <button className="btn btn--primary">
            <Icon.Bug /> Open trace
          </button>
          <button className="btn">Assign owner</button>
          <button className="btn">Suppress</button>
        </div>
      </div>
    </div>
  );
}
