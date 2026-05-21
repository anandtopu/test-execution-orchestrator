// ====================================================
// TEO — "not designed yet" placeholder (ported from app.jsx Placeholder)
// ====================================================

import Link from 'next/link';

export function Placeholder({ title, subtitle }: { title: string; subtitle: string }) {
  return (
    <div className="page-pad">
      <div className="page-header" style={{ padding: 0 }}>
        <h1>{title}</h1>
        <p>{subtitle}</p>
      </div>
      <div
        style={{
          marginTop: 32,
          border: '1px dashed var(--sr-border)',
          borderRadius: 'var(--sr-radius)',
          padding: 48,
          textAlign: 'center',
          color: 'var(--sr-fg-muted)',
          fontFamily: 'var(--sr-font-mono)',
          fontSize: 12,
        }}
      >
        Not designed in this prototype.
        <br />
        See{' '}
        <Link href="/" style={{ color: 'var(--sr-fg)' }}>
          Live run
        </Link>
        ,{' '}
        <Link href="/clusters" style={{ color: 'var(--sr-fg)' }}>
          Failure clusters
        </Link>
        , and{' '}
        <Link href="/flakes" style={{ color: 'var(--sr-fg)' }}>
          Flakes
        </Link>{' '}
        for the polished surfaces.
      </div>
    </div>
  );
}
