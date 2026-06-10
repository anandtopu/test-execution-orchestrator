// TEO — Next.js edge middleware: soft OIDC sign-in gate.
//
// The Go backend already enforces auth on protected GraphQL/REST endpoints
// (auth.Middleware + requireMutationRole) and sets an httpOnly `teo_session`
// JWT cookie on the API origin. This middleware is a *UX* layer: it bounces
// unauthenticated browsers to /login before they see a chrome that can't load
// data, instead of letting protected calls fail one-by-one.
//
// SOFT-LOCK (important): the redirect is opt-in via NEXT_PUBLIC_REQUIRE_AUTH.
// When unset/"0" (the default) we never redirect, so an auth-DISABLED backend
// (where /auth/login returns 503 and there is no session cookie to ever get)
// stays fully usable. Operators running an OIDC-enabled deploy set
// NEXT_PUBLIC_REQUIRE_AUTH=1 to turn the edge gate on.
//
// CROSS-ORIGIN CAVEAT: middleware runs server-side and CAN read httpOnly
// request cookies, but only cookies present on the Next.js origin. That holds
// for same-origin prod deploys (NEXT_PUBLIC_API_BASE empty → /auth/* hits the
// same host, so teo_session lands on this origin). In split-origin dev (API on
// a different host) the cookie is NOT visible here — leave the flag off and
// rely on SessionNav's /auth/session (credentials: 'include') for sign-in
// state. Never try to read teo_session in client JS: it is httpOnly.
//
// Edge-runtime-safe: only `next/server` imports.

import { NextResponse, type NextRequest } from 'next/server';

const SESSION_COOKIE = 'teo_session'; // must equal backend auth.SessionCookie

// Paths that must never be redirected even if middleware is invoked for them.
// The `config.matcher` below already excludes these so Next won't call us, but
// this in-function guard is defense-in-depth: it keeps the redirect loop closed
// (and the BFF/auth routes reachable) even if the matcher is ever loosened or
// middleware is invoked directly. Anchored on exact segments.
function isAllowListed(pathname: string): boolean {
  return (
    pathname === '/login' ||
    pathname.startsWith('/login/') ||
    pathname.startsWith('/auth/') ||
    pathname.startsWith('/api/')
  );
}

export function middleware(request: NextRequest): NextResponse {
  // Soft default: gate is off unless explicitly enabled.
  if (process.env.NEXT_PUBLIC_REQUIRE_AUTH !== '1') {
    return NextResponse.next();
  }

  // Never gate the sign-in page, OIDC routes, or the BFF proxy — redirecting
  // those would loop (/login, /auth/*) or break the GraphQL proxy (/api/*).
  if (isAllowListed(request.nextUrl.pathname)) {
    return NextResponse.next();
  }

  const hasSession = request.cookies.has(SESSION_COOKIE);
  if (hasSession) {
    return NextResponse.next();
  }

  // Unauthenticated + gate on: send to /login. We intentionally do NOT carry a
  // `next`/return-to param: post-sign-in landing is owned by the backend OIDC
  // callback (it 302s to uiBaseURL or "/"), and there is no end-to-end plumbing
  // to honor a deep-link target. Adding an unread param would advertise a
  // round-trip that does not happen. The user lands on the app home after
  // sign-in. (If deep-link return-to is wanted later, it must be wired through
  // login/page.tsx → /auth/login and validated in the Go callback.)
  const url = request.nextUrl.clone();
  url.pathname = '/login';
  url.search = '';
  return NextResponse.redirect(url);
}

// Run on every path EXCEPT the allow-list. Excluding /api is mandatory: those
// are the Next BFF proxy routes that carry the server-side key and must never
// be redirected (it would break the GraphQL proxy). /auth/* are same-origin
// sign-in routes; /login is the sign-in page itself (avoids a redirect loop);
// /_next/* and any path with a file extension are static assets.
export const config = {
  matcher: [
    '/((?!api/|auth/|login(?:/|$)|_next/static|_next/image|favicon\\.ico$|.*\\..*).*)',
  ],
};
