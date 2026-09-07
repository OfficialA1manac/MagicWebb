// Docs screenshots (v3.6 wave 8). Not part of the smoke gate — the default
// playwright.config.ts ignores this file; run it with
//
//   npm run shots                     # live Coston2 app (default base)
//   MW_SHOTS_BASE=http://127.0.0.1:4321 npm run shots   # a local `astro preview`
//
// Four projects (desktop / mobile × light / dark) write
// docs/images/v3.6/<page>-<device>-<scheme>.jpg. The listings page is shot
// against the e2e sample fixtures because the live testnet may have no open
// listings; every other page is the live product.
import { test } from '@playwright/test';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { mockApi, NETWORK_STATUS } from './fixtures';

const HERE = path.dirname(fileURLToPath(import.meta.url));
const OUT = process.env.MW_SHOTS_OUT || path.resolve(HERE, '..', '..', 'docs', 'images', 'v3.6');

// MagicWebb Genesis (Coston2 seed collection) and its creator.
const GENESIS = '0x642eb8dca3e9e1eec4302f0c51278c4dda44207e';
const CREATOR = '0x14080c66253dfc5042ad5226c7d8730f8bc69f91';

const PAGES: ReadonlyArray<{ slug: string; path: string; mock?: boolean; live?: boolean }> = [
  { slug: 'home', path: '/' },
  { slug: 'listings', path: '/listings', mock: true },
  { slug: 'token', path: `/token/${GENESIS}/1` },
  { slug: 'collection', path: `/collection/${GENESIS}` },
  { slug: 'auctions', path: '/auctions' },
  { slug: 'auction', path: '/auction/1' },
  { slug: 'offers', path: '/offers' },
  { slug: 'profile', path: `/profile/${CREATOR}` },
  { slug: 'search', path: '/search?q=genesis' },
  { slug: 'status', path: '/status' },
  { slug: 'docs', path: '/docs' },
  { slug: 'docs-start-here', path: '/docs/start-here' },
  // Not live until this wave deploys: shoot it from a local preview —
  //   npx astro preview & MW_SHOTS_BASE=http://127.0.0.1:4321 npx playwright test -c playwright.shots.config.ts -g docs-system
  { slug: 'docs-system', path: '/docs/system', live: true },
];

for (const p of PAGES) {
  test(`screenshot ${p.slug}`, async ({ page }, info) => {
    test.slow(); // live RPC reads + AppKit boot can take a while on a cold app
    const [device, scheme] = info.project.name.split('-');
    if (p.mock) await mockApi(page);
    if (p.live) {
      // A local preview has no backend, so the shell would render browse-only.
      // Inject the trading globals the smoke suite uses (no-op on the live app).
      await page.addInitScript((status) => {
        const w = window as unknown as Record<string, string>;
        w.MW_MARKETPLACE = w.MW_MARKETPLACE || '0x1111111111111111111111111111111111111111';
        w.MW_AUCTION = w.MW_AUCTION || '0x2222222222222222222222222222222222222222';
        w.MW_OFFERBOOK = w.MW_OFFERBOOK || '0x3333333333333333333333333333333333333333';
        w.MW_TRADING = w.MW_TRADING || 'live';
        w.MW_NETWORK_STATUS_JSON = w.MW_NETWORK_STATUS_JSON || status;
      }, NETWORK_STATUS);
    }
    await page.goto(p.path);
    await page.locator('#main').waitFor();
    await page.waitForLoadState('networkidle').catch(() => undefined);
    // Let fonts, images and the (reduced) reveal transitions settle.
    await page.waitForTimeout(1500);
    fs.mkdirSync(OUT, { recursive: true });
    await page.screenshot({
      path: path.join(OUT, `${p.slug}-${device}-${scheme}.jpg`),
      type: 'jpeg',
      quality: 85,
    });
  });
}
