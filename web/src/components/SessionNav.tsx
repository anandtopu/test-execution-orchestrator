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
}

const apiBase = process.env.NEXT_PUBLIC_API_BASE ?? '';

async function defaultFetcher(): Promise<Session | null> {
  const res = await fetch(`${apiBase}/auth/session`, { credentials: 'include', cache: 'no-store' });
  if (!res.ok) return null;
  return (await res.json()) as Session;
}

/** Header widget: shows the signed-in user + sign-out, or a sign-in link. */
export function SessionNav({ fetcher = defaultFetcher }: SessionNavProps) {
  // undefined = still loading (render nothing to avoid a flash).
  const [session, setSession] = useState<Session | null | undefined>(undefined);

  useEffect(() => {
    let cancelled = false;
    fetcher()
      .then((s) => {
        if (!cancelled) setSession(s);
      })
      .catch(() => {
        if (!cancelled) setSession(null);
      });
    return () => {
      cancelled = true;
    };
  }, [fetcher]);

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
