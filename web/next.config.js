/** @type {import('next').NextConfig} */
const nextConfig = {
  reactStrictMode: true,
  output: 'standalone',
  experimental: {
    typedRoutes: true,
  },
  env: {
    TEO_API_URL: process.env.TEO_API_URL || 'http://localhost:8080',
  },
};

module.exports = nextConfig;
