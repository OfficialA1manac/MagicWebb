import { defineConfig } from 'vitest/config';
import react from '@vitejs/plugin-react';

export default defineConfig({
  plugins: [react()],
  test: {
    environment: 'jsdom',
    setupFiles: ['./src/test-setup.ts'],
    include: ['src/**/*.test.{ts,tsx}'],
    globals: true,
  },
  // Mock the Reown project ID env var so initAppKit() succeeds in tests.
  // Vite's define option replaces import.meta.env references at build time.
  define: {
    'import.meta.env.PUBLIC_REOWN_PROJECT_ID': '"test-project-id-for-unit-tests"',
  },
});
