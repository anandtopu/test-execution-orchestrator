// ====================================================
// TEO — Shared atoms: StatusBadge, Sparkline, Chip
// Ported from atoms.jsx (StatusBadge, Sparkline) and run-gantt.jsx (Chip).
// ====================================================

interface BadgeMeta {
  cls: string;
  label: string;
}

const STATUS_MAP: Record<string, BadgeMeta> = {
  pass: { cls: 'badge--pass', label: 'PASS' },
  passed: { cls: 'badge--pass', label: 'PASSED' },
  // Backend run-status vocabulary (internal/model.RunStatus): a finished run
  // reports 'succeeded', and adaptRun passes run.status through raw — without
  // these entries the marquee header mislabels every completed run as a gray
  // 'SUCCEEDED' skip-chip instead of a green pass badge.
  succeeded: { cls: 'badge--pass', label: 'SUCCEEDED' },
  fail: { cls: 'badge--fail', label: 'FAIL' },
  failed: { cls: 'badge--fail', label: 'FAILED' },
  lost: { cls: 'badge--fail', label: 'LOST' },
  running: { cls: 'badge--run', label: 'RUNNING' },
  queued: { cls: 'badge--info', label: 'QUEUED' },
  pending: { cls: 'badge--info', label: 'PENDING' },
  preempt: { cls: 'badge--warn', label: 'PREEMPT' },
  preempted: { cls: 'badge--warn', label: 'PREEMPTED' },
  // The model spells this 'canceled' (single L); SQL/isLive use 'cancelled'
  // (double L). Map both spellings until the vocabulary is reconciled.
  canceled: { cls: 'badge--skip', label: 'CANCELED' },
  cancelled: { cls: 'badge--skip', label: 'CANCELLED' },
  skip: { cls: 'badge--skip', label: 'SKIP' },
  skipped: { cls: 'badge--skip', label: 'SKIPPED' },
  quarantined: { cls: 'badge--quar', label: 'QUARANTINED' },
  flagged: { cls: 'badge--warn', label: 'FLAGGED' },
  stable: { cls: 'badge--pass', label: 'STABLE' },
};

export function StatusBadge({ status }: { status: string }) {
  const m = STATUS_MAP[status] || { cls: 'badge--skip', label: status?.toUpperCase() || '?' };
  return <span className={`badge ${m.cls}`}>{m.label}</span>;
}

export function Sparkline({ s }: { s: string }) {
  return (
    <span className="sparkline" title={s}>
      {s.split('').map((ch, i) => {
        const cls =
          ch === 'P' ? 'sparkline__tick--pass' : ch === 'F' ? 'sparkline__tick--fail' : 'sparkline__tick--skip';
        const h = ch === 'F' ? 14 : ch === 'P' ? 12 : 4;
        return <span key={i} className={`sparkline__tick ${cls}`} style={{ height: h }} />;
      })}
    </span>
  );
}

export function Chip({
  on,
  onClick,
  children,
}: {
  on?: boolean;
  onClick?: () => void;
  children: React.ReactNode;
}) {
  return (
    <span className={`chip ${on ? 'is-on' : ''}`} onClick={onClick}>
      {children}
    </span>
  );
}

// KPI tile, shared by the Run Detail KPI strip and the Flakes KPI strip
// (the prototype defined this once in run-detail.jsx and reused it globally).
export function Kpi({
  label,
  value,
  sub,
}: {
  label: React.ReactNode;
  value: React.ReactNode;
  sub?: React.ReactNode;
}) {
  return (
    <div className="kpi">
      <div className="kpi__label">{label}</div>
      <div className="kpi__value">{value}</div>
      <div className="kpi__sub">{sub}</div>
    </div>
  );
}
