import { defineConfig } from 'astro/config';
import react from '@astrojs/react';
import svelte from '@astrojs/svelte';

// https://astro.build/config
export default defineConfig({
  integrations: [
    react(),
    svelte(),
  ],
  // Output static HTML + client-side JS islands
  output: 'static',
  // Prefetch same-origin links on hover so navigation feels instant.
  // (ViewTransitions/ClientRouter evaluated and deferred: every page's inline
  // scripts key off DOMContentLoaded, which never re-fires after a client-side
  // swap, and the client:only AppKit island would need transition:persist —
  // a full retrofit. Prefetch gives most of the win at zero risk.)
  prefetch: {
    prefetchAll: true,
    defaultStrategy: 'hover',
  },
  // In dev mode, proxy API/Auth calls to the Go Fiber backend on :8080
  server: {
    port: 4321,
  },
  // Vite config
  vite: {
    server: {
      proxy: {
        // REST API
        '/api': {
          target: 'http://localhost:8080',
          changeOrigin: true,
        },
        // SIWE auth endpoints
        '/auth': {
          target: 'http://localhost:8080',
          changeOrigin: true,
        },
        // WebSocket hub
        '/ws': {
          target: 'http://localhost:8080',
          changeOrigin: true,
          ws: true,
        },
        // Health checks
        '/healthz': {
          target: 'http://localhost:8080',
          changeOrigin: true,
        },
        '/readyz': {
          target: 'http://localhost:8080',
          changeOrigin: true,
        },
        // Static assets served by Go (if any shared ones are needed)
        '/static': {
          target: 'http://localhost:8080',
          changeOrigin: true,
        },
      },
    },
    build: {
      // Output to dist/ relative to app/
      outDir: './dist',
      // AppKit is bundled by the WalletConnect.tsx island; no separate bridge.
    },
  },
});
