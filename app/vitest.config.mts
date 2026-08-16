import { dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { defineConfig } from 'vitest/config';
import react from '@vitejs/plugin-react';

// The repo root, one level above app/.
const repoRoot = resolve(dirname(fileURLToPath(import.meta.url)), '..');

export default defineConfig({
  plugins: [react()],
  test: {
    environment: 'jsdom',
    setupFiles: ['./src/test-setup.ts'],
    include: ['src/**/*.test.{ts,tsx}'],
    globals: true,
  },
  // wallet-client.test.ts and ws-client.test.ts pull the browser sources in
  // as strings via `../../frontend/static/{wallet,ws}.js?raw` and eval them
  // in jsdom. Those live outside app/, and Vite derives its serving root from
  // the nearest lockfile — which is app/package-lock.json — so the imports sit
  // outside the default allow-list and load fails with "Denied ID". Windows
  // does not enforce the check, so this only ever broke on Linux CI. Widening
  // the allow-list to the repo root is what makes the two agree.
  server: {
    fs: {
      allow: [repoRoot],
    },
  },
  // Mock the Reown project ID env var so initAppKit() succeeds in tests.
  // Vite's define option replaces import.meta.env references at build time.
  define: {
    'import.meta.env.PUBLIC_REOWN_PROJECT_ID': '"test-project-id-for-unit-tests"',
  },
});
