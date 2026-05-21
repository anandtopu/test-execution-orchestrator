'use client';

// ====================================================
// TEO — App shell: left rail + topbar + theme switch
// Ported from app.jsx (Rail + Topbar). Hash routing in the prototype is
// replaced by Next.js file routing + usePathname for the active state.
// ====================================================

import { useEffect, useState } from 'react';
import Link from 'next/link';
import type { Route } from 'next';
import { usePathname } from 'next/navigation';
import { Icon } from './Icons';
import { TEO_DATA } from '@/lib/teo-data';

type Theme = 'light' | 'dark' | 'contrast';
const THEMES: Theme[] = ['light', 'dark', 'contrast'];
const STORAGE_KEY = 'teo-theme';

interface RailItem {
  href: Route;
  icon: () => React.JSX.Element;
  label: string;
}

const RAIL_ITEMS: RailItem[] = [
  { href: '/', icon: Icon.Activity, label: 'Live run' },
  { href: '/runs', icon: Icon.Layers, label: 'Runs' },
  { href: '/clusters', icon: Icon.Bug, label: 'Failure clusters' },
  { href: '/flakes', icon: Icon.Zap, label: 'Flakes' },
  { href: '/tests', icon: Icon.Beaker, label: 'Tests' },
  { href: '/cost', icon: Icon.Wallet, label: 'Cost' },
];

/** Highlight a rail item for its own route and any nested children, except
 * "Live run" (/) which is only active on the exact home path. */
function isActive(pathname: string, href: string): boolean {
  if (href === '/') return pathname === '/';
  return pathname === href || pathname.startsWith(`${href}/`);
}

function crumbsFor(pathname: string): string[] {
  const seg = pathname.split('/').filter(Boolean)[0] ?? '';
  switch (seg) {
    case '':
      return ['teo-dev/teo', 'runs', TEO_DATA.run.id.slice(0, 7)];
    case 'runs':
      return ['teo-dev/teo', 'runs'];
    case 'clusters':
      return ['teo-dev/teo', 'failure clusters'];
    case 'flakes':
      return ['teo-dev/teo', 'flakes'];
    case 'tests':
      return ['teo-dev/teo', 'tests'];
    case 'cost':
      return ['teo-dev/teo', 'cost'];
    default:
      return ['teo-dev/teo', seg];
  }
}

function Rail({ pathname }: { pathname: string }) {
  return (
    <aside className="rail">
      <div className="rail__brand">
        <div className="rail__brand-mark">teo</div>
      </div>
      <nav className="rail__nav">
        {RAIL_ITEMS.map((it) => {
          const ItemIcon = it.icon;
          return (
            <Link
              key={it.href}
              href={it.href}
              className={`rail__btn ${isActive(pathname, it.href) ? 'is-active' : ''}`}
            >
              <ItemIcon />
              <span className="rail__tip">{it.label}</span>
            </Link>
          );
        })}
      </nav>
      <div className="rail__spacer" />
      <nav className="rail__nav">
        <div className="rail__btn" role="button" tabIndex={0} aria-label="Settings">
          <Icon.Settings />
          <span className="rail__tip">Settings</span>
        </div>
      </nav>
    </aside>
  );
}

function ThemeSwitch({ theme, setTheme }: { theme: Theme; setTheme: (t: Theme) => void }) {
  return (
    <div className="topbar__theme" role="radiogroup" aria-label="Theme">
      {THEMES.map((t) => (
        <button
          key={t}
          type="button"
          role="radio"
          aria-checked={theme === t}
          className={theme === t ? 'is-on' : ''}
          onClick={() => setTheme(t)}
        >
          {t === 'light' ? 'Light' : t === 'dark' ? 'Dark' : 'Contrast'}
        </button>
      ))}
    </div>
  );
}

function Topbar({
  pathname,
  theme,
  setTheme,
}: {
  pathname: string;
  theme: Theme;
  setTheme: (t: Theme) => void;
}) {
  const crumbs = crumbsFor(pathname);
  return (
    <header className="topbar">
      <div className="crumbs">
        {crumbs.map((c, i) => (
          <span key={i} style={{ display: 'inline-flex', alignItems: 'center', gap: 6 }}>
            {i > 0 && <span className="crumbs__sep">/</span>}
            <span className={i === crumbs.length - 1 ? 'crumbs__cur' : ''}>{c}</span>
          </span>
        ))}
      </div>
      <div className="topbar__spacer" />
      <ThemeSwitch theme={theme} setTheme={setTheme} />
      <div className="topbar__env">prod · us-west-2</div>
      <div className="topbar__search-wrap">
        <Icon.Search />
        <input className="topbar__search" placeholder="Search tests, runs, clusters…" />
        <span className="topbar__kbd">⌘K</span>
      </div>
      <div className="topbar__user" title="Marco Pereira">
        MP
      </div>
    </header>
  );
}

export function Shell({ children }: { children: React.ReactNode }) {
  const pathname = usePathname() ?? '/';
  const [theme, setThemeState] = useState<Theme>('light');

  // Sync from whatever the no-flash inline script (in layout) already applied
  // to <html data-theme>, so the control reflects reality on first paint.
  useEffect(() => {
    const current = document.documentElement.getAttribute('data-theme');
    if (current === 'dark' || current === 'contrast' || current === 'light') {
      setThemeState(current);
    }
  }, []);

  const setTheme = (t: Theme) => {
    setThemeState(t);
    document.documentElement.setAttribute('data-theme', t);
    try {
      localStorage.setItem(STORAGE_KEY, t);
    } catch {
      /* private mode / storage disabled — theme just won't persist */
    }
  };

  // The login screen is chrome-free (centered SSO card); don't wrap it.
  if (pathname.startsWith('/login')) {
    return <main>{children}</main>;
  }

  // Redesigned screens go edge-to-edge (they own their padding); legacy
  // GraphQL-backed pages get a fallback gutter.
  const isRedesigned =
    pathname === '/' ||
    pathname.startsWith('/clusters') ||
    pathname.startsWith('/flakes') ||
    pathname.startsWith('/tests');

  return (
    <div className="app">
      <Rail pathname={pathname} />
      <Topbar pathname={pathname} theme={theme} setTheme={setTheme} />
      <main className={`content ${isRedesigned ? '' : 'content--legacy'}`} data-screen-label={`${pathname} screen`}>
        {children}
      </main>
    </div>
  );
}
