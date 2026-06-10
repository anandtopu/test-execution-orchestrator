// Loaded by vitest before each test file (configured via vitest.config.ts).
// Brings in jest-dom matchers for human-readable DOM assertions, and wires
// up the after-each DOM cleanup that @testing-library/react would otherwise
// register only when vitest globals are enabled (we run with globals=false).
import { afterEach } from 'vitest';
import { cleanup } from '@testing-library/react';

import '@testing-library/jest-dom/vitest';

// jsdom has no ResizeObserver; the Clusters spatial map uses it to size the SVG.
// Provide a no-op stub so components that observe element size can mount under
// test. Only installed when missing so a real implementation (if ever present)
// is left untouched.
if (typeof globalThis.ResizeObserver === 'undefined') {
  class ResizeObserverStub {
    observe(): void {}
    unobserve(): void {}
    disconnect(): void {}
  }
  globalThis.ResizeObserver = ResizeObserverStub as unknown as typeof ResizeObserver;
}

afterEach(() => {
  cleanup();
});
