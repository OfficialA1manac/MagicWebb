// Standalone Vite config for the AppKit bridge bundle.
// The Astro build hashes JS output into _astro/ but the Go embed serves
// from frontend/static/. This config builds a single, non-hashed IIFE
// bundle directly into frontend/static/appkit-bridge.js so the Go
// embed picks it up with no CDN dependency.
//
// Usage: npx vite build --config vite.bridge.config.mjs

import { defineConfig } from 'vite';
import { resolve, dirname } from 'path';
import { fileURLToPath } from 'url';

const __dirname = dirname(fileURLToPath(import.meta.url));

export default defineConfig({
  build: {
    // Output within the app directory so Docker builds work (the astro-build
    // stage has /astro/ but NOT /frontend/). The Makefile and Dockerfile each
    // copy this to ../frontend/static/ for the Go embed.
    outDir: resolve(__dirname, 'dist', 'static'),
    emptyOutDir: true, // safe: this is a dedicated subdirectory
    lib: {
      entry: resolve(__dirname, 'src', 'appkit-bridge.js'),
      name: 'MWAppKitBridge',
      formats: ['iife'],
      fileName: () => 'appkit-bridge.js',
    },
    rollupOptions: {
      // Bundle ALL dependencies — nothing external.
      external: [],
    },
    minify: 'esbuild',
  sourcemap: false,
  },
});
