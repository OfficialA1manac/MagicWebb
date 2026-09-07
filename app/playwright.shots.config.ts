import { defineConfig, devices } from '@playwright/test';

// Docs screenshot runner (v3.6 wave 8) — `npm run shots`. Separate from the
// smoke config: no webServer (defaults to the live Coston2 app), four
// device × scheme projects, one worker so the live app is not hammered.
// See e2e/screenshots.spec.ts for the page list and output path.
const base = process.env.MW_SHOTS_BASE || 'https://magicwebb.fly.dev';

const desktop = { ...devices['Desktop Chrome'], viewport: { width: 1440, height: 900 } };
const mobile = { ...devices['Desktop Chrome'], viewport: { width: 390, height: 844 }, hasTouch: true };

export default defineConfig({
  testDir: './e2e',
  testMatch: /screenshots\.spec\.ts$/,
  timeout: 120_000,
  fullyParallel: false,
  workers: 1,
  retries: 1,
  reporter: [['list']],
  use: {
    baseURL: base,
    reducedMotion: 'reduce',
    trace: 'off',
  },
  projects: [
    { name: 'desktop-light', use: { ...desktop, colorScheme: 'light' } },
    { name: 'desktop-dark', use: { ...desktop, colorScheme: 'dark' } },
    { name: 'mobile-light', use: { ...mobile, colorScheme: 'light' } },
    { name: 'mobile-dark', use: { ...mobile, colorScheme: 'dark' } },
  ],
});
