import { defineConfig } from 'vitest/config';
import path from 'node:path';

export default defineConfig({
  // Use the automatic JSX runtime (React 17+). The component test files use
  // JSX without `import React from 'react'` because Next.js does the same in
  // production. Without this, esbuild falls back to the classic transform
  // and tests fail with "React is not defined".
  esbuild: {
    jsx: 'automatic',
  },
  resolve: {
    alias: {
      '@': path.resolve(__dirname, 'src'),
    },
  },
  test: {
    environment: 'jsdom',
    globals: false,
    include: ['src/**/*.test.{ts,tsx}'],
    setupFiles: ['src/test-setup.ts'],
    coverage: {
      provider: 'v8',
      reporter: ['text', 'html'],
      include: ['src/**/*.{ts,tsx}'],
      exclude: ['src/**/*.test.{ts,tsx}', 'src/app/**/*.tsx', 'src/test-setup.ts'],
    },
  },
});
