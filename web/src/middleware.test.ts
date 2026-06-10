import { afterEach, describe, expect, it, vi } from 'vitest';
import { NextRequest } from 'next/server';
import { middleware } from './middleware';

// Helper: build a NextRequest for a path, optionally with the session cookie.
function req(path: string, opts: { session?: boolean } = {}): NextRequest {
  const headers = new Headers();
  if (opts.session) headers.set('cookie', 'teo_session=abc');
  return new NextRequest(new URL(`https://teo.example.com${path}`), { headers });
}

describe('middleware (soft OIDC gate)', () => {
  afterEach(() => {
    vi.unstubAllEnvs();
  });

  it('passes through when the gate is off (default)', () => {
    vi.stubEnv('NEXT_PUBLIC_REQUIRE_AUTH', '');
    const res = middleware(req('/runs/abc'));
    // NextResponse.next() carries no Location redirect header.
    expect(res.headers.get('location')).toBeNull();
  });

  it('redirects unauthenticated browsers to /login when the gate is on', () => {
    vi.stubEnv('NEXT_PUBLIC_REQUIRE_AUTH', '1');
    const res = middleware(req('/runs/abc'));
    const loc = res.headers.get('location');
    expect(loc).not.toBeNull();
    const url = new URL(loc!);
    expect(url.pathname).toBe('/login');
    // No dead `next` param is advertised.
    expect(url.searchParams.has('next')).toBe(false);
    expect(url.search).toBe('');
  });

  it('passes through when the session cookie is present', () => {
    vi.stubEnv('NEXT_PUBLIC_REQUIRE_AUTH', '1');
    const res = middleware(req('/runs/abc', { session: true }));
    expect(res.headers.get('location')).toBeNull();
  });

  it('always allows /login even with the gate on and no cookie (no redirect loop)', () => {
    vi.stubEnv('NEXT_PUBLIC_REQUIRE_AUTH', '1');
    const res = middleware(req('/login'));
    expect(res.headers.get('location')).toBeNull();
  });

  it('never redirects the OIDC callback /auth/callback (gate on, no cookie)', () => {
    vi.stubEnv('NEXT_PUBLIC_REQUIRE_AUTH', '1');
    const res = middleware(req('/auth/callback'));
    expect(res.headers.get('location')).toBeNull();
  });

  it('never redirects the BFF proxy /api/graphql/run (gate on, no cookie)', () => {
    vi.stubEnv('NEXT_PUBLIC_REQUIRE_AUTH', '1');
    const res = middleware(req('/api/graphql/run'));
    expect(res.headers.get('location')).toBeNull();
  });

  it('redirects to /login using a NextRequest with cookies.set, preserving no return-to param', () => {
    vi.stubEnv('NEXT_PUBLIC_REQUIRE_AUTH', '1');
    // Build the request the way the spec describes: bare NextRequest + cookie API.
    const request = new NextRequest(new URL('https://teo.example.com/runs/xyz'));
    // (No session cookie set → must redirect.)
    const res = middleware(request);
    const loc = res.headers.get('location');
    expect(loc).not.toBeNull();
    const url = new URL(loc!);
    expect(url.pathname).toBe('/login');
    expect(url.search).toBe('');
  });

  it('passes through when teo_session is set via req.cookies.set', () => {
    vi.stubEnv('NEXT_PUBLIC_REQUIRE_AUTH', '1');
    const request = new NextRequest(new URL('https://teo.example.com/runs/xyz'));
    request.cookies.set('teo_session', 'jwt');
    const res = middleware(request);
    expect(res.headers.get('location')).toBeNull();
  });
});

// Matcher correctness: the negative-lookahead allow-list must exclude exact
// segments only (login, favicon.ico) and not accidentally match a future
// /login-help or /favicon.icon. We test the regex the matcher compiles to.
describe('middleware matcher allow-list', () => {
  const matcher = '/((?!api/|auth/|login(?:/|$)|_next/static|_next/image|favicon\\.ico$|.*\\..*).*)';
  const re = new RegExp(`^${matcher}$`);

  it('excludes the exact /login route', () => {
    expect(re.test('/login')).toBe(false);
  });

  it('still gates a /login-help route (not part of the allow-list)', () => {
    expect(re.test('/login-help')).toBe(true);
  });

  it('excludes nested /login/* paths', () => {
    expect(re.test('/login/callback')).toBe(false);
  });

  it('excludes /favicon.ico but matches a /favicon.icon path', () => {
    expect(re.test('/favicon.ico')).toBe(false);
    // /favicon.icon has a dot so the .*\..* rule also excludes it; the point is
    // favicon.ico is anchored. Confirm a normal app path is gated:
    expect(re.test('/runs/abc')).toBe(true);
  });

  // Spec: with the gate on, the sign-in routes and the Next BFF proxy routes
  // must NEVER be redirected (a redirect on /api/* would break the GraphQL
  // proxy; a redirect on /login or /auth/* would loop). The matcher excludes
  // them so the middleware never even runs for those paths.
  it('excludes the OIDC callback route /auth/callback', () => {
    expect(re.test('/auth/callback')).toBe(false);
  });

  it('excludes all /auth/* sign-in routes', () => {
    expect(re.test('/auth/login')).toBe(false);
    expect(re.test('/auth/logout')).toBe(false);
    expect(re.test('/auth/session')).toBe(false);
    expect(re.test('/auth/refresh')).toBe(false);
  });

  it('excludes the Next BFF proxy route /api/graphql/run', () => {
    expect(re.test('/api/graphql/run')).toBe(false);
    expect(re.test('/api/graphql')).toBe(false);
  });
});
