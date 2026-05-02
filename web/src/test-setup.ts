// Loaded by vitest before each test file (configured via vitest.config.ts).
// Brings in jest-dom matchers for human-readable DOM assertions, and wires
// up the after-each DOM cleanup that @testing-library/react would otherwise
// register only when vitest globals are enabled (we run with globals=false).
import { afterEach } from 'vitest';
import { cleanup } from '@testing-library/react';

import '@testing-library/jest-dom/vitest';

afterEach(() => {
  cleanup();
});
