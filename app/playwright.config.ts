import { defineConfig, devices } from '@playwright/test';

// Playwright smoke suite (plan B6). Wallet-less: runs against the BUILT site
// (`npm run build` first) served by `astro preview`; every /api/v1/** call is
// intercepted per-test via page.route (see e2e/fixtures.ts) so no backend or
// chain is needed. Chromium only — this is a smoke gate, not a browser matrix.
export default defineConfig({
  testDir: './e2e',
  // Docs screenshots have their own runner (playwright.shots.config.ts, `npm run shots`).
  testIgnore: /screenshots\.spec\.ts$/,
  timeout: 60_000,
  expect: { timeout: 15_000 },
  fullyParallel: true,
  forbidOnly: !!process.env.CI,
  retries: process.env.CI ? 1 : 0,
  reporter: process.env.CI ? [['list'], ['github']] : [['list']],
  use: {
    baseURL: 'http://127.0.0.1:4321',
    trace: 'retain-on-failure',
  },
  projects: [
    {
      name: 'desktop',
      use: { ...devices['Desktop Chrome'], viewport: { width: 1440, height: 900 } },
    },
    {
      // v3.6: the same desktop viewport in the dark colour scheme — every
      // axe contrast sweep runs in both schemes (tokens.css swaps palettes).
      name: 'dark',
      use: { ...devices['Desktop Chrome'], viewport: { width: 1440, height: 900 }, colorScheme: 'dark' },
    },
    {
      // One small-screen project (iPhone 14-ish logical viewport) for the
      // mobile tab bar + touch-target sweeps. Same chromium binary.
      name: 'mobile',
      use: { ...devices['Desktop Chrome'], viewport: { width: 390, height: 844 }, hasTouch: true },
    },
  ],
  webServer: {
    // Serves dist/ (build must have run). astro preview serves dist/404.html
    // for unknown routes; deep routes like /collection/0x… are fulfilled from
    // the built HTML via page.route in the tests (prod: Go server rewrites).
    command: 'npx astro preview --host 127.0.0.1 --port 4321',
    // astro ≥7.3 auto-detects an AI-agent shell (am-i-vibing: CLAUDECODE,
    // CODEX_THREAD_ID, …) and daemonizes the preview server, which Playwright
    // reports as "exited early". An empty value counts as unset for that
    // detector, so the server stays a plain foreground child here and in CI.
    env: { CLAUDECODE: '', CODEX_THREAD_ID: '', GEMINI_CLI: '' },
    url: 'http://127.0.0.1:4321/',
    reuseExistingServer: !process.env.CI,
    timeout: 120_000,
  },
});
