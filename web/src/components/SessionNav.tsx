'use client';

import { useEffect, useState } from 'react';

export interface Session {
  userId: string;
  email: string;
  roles: string[];
}

export interface SessionNavProps {
  /** Override fetch impl for tests; defaults to the API's /auth/session. */
  fetcher?: () => Promise<Session | null>;
  /**
   * Override the proactive refresh impl for tests; defaults to POST
   * /auth/refresh. Resolves true on a 2xx, false otherwise (which flips the
   * widget back to the sign-in link).
   */
  refresher?: () => Promise<boolean>;
  /** Refresh interval in ms; defaults to NEXT_PUBLIC_SESSION_REFRESH_MS (~10m). */
  refreshMs?: number;
}

const apiBase = process.env.NEXT_PUBLIC_API_BASE ?? '';

// /auth/session carries no expiry today, so we refresh on a fixed cadence
// rather than expiry-driven timing. Default ~10 minutes. A non-finite or <=0
// override is treated as "disable proactive refresh", NOT "fall back to the
// default" — so NEXT_PUBLIC_SESSION_REFRESH_MS=0 cleanly turns refresh off
// instead of silently re-enabling the 10m interval (and avoids a tight loop
// from a negative value).
const _envRefreshMs = Number(process.env.NEXT_PUBLIC_SESSION_REFRESH_MS);
const DEFAULT_REFRESH_MS =
  process.env.NEXT_PUBLIC_SESSION_REFRESH_MS === undefined
    ? 10 * 60 * 1000
    : Number.isFinite(_envRefreshMs)
      ? _envRefreshMs
      : 10 * 60 * 1000;

async function defaultFetcher(): Promise<Session | null> {
  const res = await fetch(`${apiBase}/auth/session`, { credentials: 'include', cache: 'no-store' });
  if (!res.ok) return null;
  return (await res.json()) as Session;
}

async function defaultRefresher(): Promise<boolean> {
  const res = await fetch(`${apiBase}/auth/refresh`, { method: 'POST', credentials: 'include' });
  return res.ok;
}

/** Header widget: shows the signed-in user + sign-out, or a sign-in link. */
export function SessionNav({
  fetcher = defaultFetcher,
  refresher = defaultRefresher,
  refreshMs = DEFAULT_REFRESH_MS,
}: SessionNavProps) {
  // undefined = still loading (render nothing to avoid a flash).
  const [session, setSession] = useState<Session | null | undefined>(undefined);

  useEffect(() => {
    let cancelled = false;
    let timer: ReturnType<typeof setInterval> | undefined;

    fetcher()
      .then((s) => {
        if (cancelled) return;
        setSession(s);
        // Only schedule proactive refreshes for an authenticated session, and
        // only when refreshMs is a positive interval (<=0 disables refresh).
        if (s && refreshMs > 0) {
          timer = setInterval(() => {
            refresher()
              .then((ok) => {
                if (cancelled) return;
                if (!ok) {
                  // Refresh failed: flip to signed-out and stop the now-pointless
                  // interval (it would otherwise keep POSTing /auth/refresh).
                  setSession(null);
                  if (timer) clearInterval(timer);
                }
              })
              .catch(() => {
                if (cancelled) return;
                setSession(null);
                if (timer) clearInterval(timer);
              });
          }, refreshMs);
        }
      })
      .catch(() => {
        if (!cancelled) setSession(null);
      });

    return () => {
      cancelled = true;
      if (timer) clearInterval(timer);
    };
  }, [fetcher, refresher, refreshMs]);

  async function signOut() {
    await fetch(`${apiBase}/auth/logout`, { method: 'POST', credentials: 'include' }).catch(() => {});
    window.location.href = '/login';
  }

  if (session === undefined) return null;

  if (!session) {
    return (
      <a href="/login" className="text-blue-600 hover:underline" data-testid="signin-link">
        Sign in
      </a>
    );
  }

  return (
    <span className="flex items-center gap-2" data-testid="session-user">
      <span className="text-gray-600">{session.email}</span>
      <button type="button" onClick={signOut} className="text-blue-600 hover:underline" data-testid="signout">
        Sign out
      </button>
    </span>
  );
}
