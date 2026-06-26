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
  // In dev mode, proxy API/Auth/SSE calls to the Go Fiber backend on :8080
  server: {
    port: 4321,
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
      // SSE events (wallet updates, auction ticks)
      '/events': {
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
  },    // Vite config
  vite: {
    build: {
      // Output to dist/ relative to app/
      outDir: './dist',
    },
  },
});
