/** @type {import('next').NextConfig} */
const nextConfig = {
  reactStrictMode: true,
  output: 'standalone',
  experimental: {
    typedRoutes: true,
  },
  env: {
    TEO_API_URL: process.env.TEO_API_URL || 'http://localhost:8080',
    // NEXT_PUBLIC_* vars are auto-inlined by Next; these entries are for
    // discoverability only.
    //   NEXT_PUBLIC_REQUIRE_AUTH   '1' enables the edge sign-in gate in
    //     web/src/middleware.ts. Default off so an OIDC-disabled backend
    //     (login 503) stays usable. Only effective for same-origin deploys
    //     (NEXT_PUBLIC_API_BASE empty) where teo_session is on this origin.
    //   NEXT_PUBLIC_SESSION_REFRESH_MS  proactive /auth/refresh interval in
    //     SessionNav. Default 600000 (10 min).
    NEXT_PUBLIC_REQUIRE_AUTH: process.env.NEXT_PUBLIC_REQUIRE_AUTH || '0',
    NEXT_PUBLIC_SESSION_REFRESH_MS: process.env.NEXT_PUBLIC_SESSION_REFRESH_MS || '600000',
  },
};

module.exports = nextConfig;
