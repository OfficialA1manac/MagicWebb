import { defineConfig, type Plugin } from 'vitest/config';
import react from '@vitejs/plugin-react';
import { compile, compileModule } from 'svelte/compiler';

// Minimal Svelte 5 compile step for component tests. The repo does not ship
// @sveltejs/vite-plugin-svelte (Astro bundles its own), so `.svelte` and
// `.svelte.ts` (runes modules) are compiled here with the client generator.
// Runs after vite:esbuild so `.svelte.ts` arrives as plain JS.
function svelteForTests(): Plugin {
  return {
    name: 'mw-svelte-tests',
    transform(code, id) {
      const clean = id.split('?')[0];
      if (clean.endsWith('.svelte')) {
        const r = compile(code, { filename: clean, generate: 'client', css: 'injected', dev: false });
        return { code: r.js.code, map: r.js.map };
      }
      if (/\.svelte\.(ts|js)$/.test(clean)) {
        const r = compileModule(code, { filename: clean, generate: 'client', dev: false });
        return { code: r.js.code, map: r.js.map };
      }
      return null;
    },
  };
}

export default defineConfig({
  plugins: [react(), svelteForTests()],
  resolve: {
    // Svelte's runtime ships server and browser builds; jsdom tests need the browser one.
    conditions: ['browser'],
  },
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
