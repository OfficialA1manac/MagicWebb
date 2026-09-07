// Playwright smoke suite (plan B6). Wallet-less, chromium only, runs against
// the built site (astro preview on :4321) with /api/v1/** mocked per test —
// see e2e/fixtures.ts. Desktop project 1440x900; the "mobile" project
// (390x844) runs only the mobile tab-bar test and the touch-target sweep.
import { test, expect, type Page } from '@playwright/test';
import AxeBuilder from '@axe-core/playwright';
import { mockApi, serveBuiltPage, COLLECTION, NETWORK_STATUS } from './fixtures';

const onDesktop = () => test.info().project.name === 'desktop';
const desktopOnly = () => test.skip(!onDesktop(), 'desktop project only');
const mobileOnly = () => test.skip(test.info().project.name !== 'mobile', 'mobile project only');
// Contrast sweeps run in BOTH desktop schemes (light = default project, dark = dark project).
const bothSchemesOnly = () => test.skip(!['desktop', 'dark'].includes(test.info().project.name), 'desktop + dark projects only');

/** Trading-live globals: the contract addresses light up every trade surface. */
async function liveTrading(page: Page) {
  await page.addInitScript(() => {
    const w = window as unknown as Record<string, string>;
    w.MW_MARKETPLACE = '0x1111111111111111111111111111111111111111';
    w.MW_AUCTION = '0x2222222222222222222222222222222222222222';
    w.MW_OFFERBOOK = '0x3333333333333333333333333333333333333333';
    w.MW_TRADING = 'live';
  });
}

/** Open any page with the API mocked; deep routes are served from dist like the Go server does. */
async function open(page: Page, path: string, opts: Parameters<typeof mockApi>[1] = {}) {
  await mockApi(page, opts);
  if (path.startsWith('/token/')) await serveBuiltPage(page, 'token');
  if (path.startsWith('/collection/')) await serveBuiltPage(page, 'collection');
  await page.goto(path);
  await page.locator('#main').waitFor();
  await page.waitForLoadState('networkidle');
}

const TOKEN_PATH = `/token/${COLLECTION}/1`;

// ── 1. Branded 404 ─────────────────────────────────────────────────────────
test('404 page renders branded copy with search and links', async ({ page }) => {
  desktopOnly();
  await mockApi(page);
  await page.goto('/definitely-not-a-page');
  await expect(page.getByRole('heading', { name: "We can't find that page" })).toBeVisible();
  await expect(page.getByRole('search')).toBeVisible();
  await expect(page.locator('#nf-q')).toBeVisible();
  const links = page.locator('.nf-links');
  await expect(links.getByRole('link', { name: 'Home' })).toHaveAttribute('href', '/');
  await expect(links.getByRole('link', { name: 'Listings' })).toHaveAttribute('href', '/listings');
  await expect(links.getByRole('link', { name: 'Docs' })).toHaveAttribute('href', '/docs');
});

// ── 2. Listings filters from URL + Apply stays in place ────────────────────
test('listings URL params populate the filter form; Apply uses replaceState', async ({ page }) => {
  desktopOnly();
  await mockApi(page);
  await page.goto('/listings?min=5&max=10&sort=price_desc');

  await expect(page.locator('#lf-min')).toHaveValue('5');
  await expect(page.locator('#lf-max')).toHaveValue('10');
  await expect(page.locator('#lf-sort')).toHaveValue('price_desc');
  // One chip per applied numeric filter.
  await expect(page.getByTestId('filter-chip')).toHaveCount(2);

  // Apply must NOT navigate: plant a flag that a document load would erase.
  await page.evaluate(() => ((window as unknown as Record<string, unknown>).__mwNoNav = 1));
  await page.getByRole('button', { name: 'Apply' }).click();
  await page.waitForTimeout(400);
  expect(page.url()).toContain('/listings?');
  expect(page.url()).toContain('min=5');
  expect(page.url()).toContain('max=10');
  expect(page.url()).toContain('sort=price_desc');
  expect(await page.evaluate(() => (window as unknown as Record<string, unknown>).__mwNoNav)).toBe(1);
});

// ── 3. Collection page: Items tab default + badge pill ─────────────────────
test('collection page shows the Items tab by default and a badge pill', async ({ page }) => {
  desktopOnly();
  await mockApi(page);
  await serveBuiltPage(page, 'collection');
  await page.goto(`/collection/${COLLECTION}`);

  const itemsTab = page.getByRole('tab', { name: 'Items' });
  await expect(itemsTab).toBeVisible();
  await expect(itemsTab).toHaveAttribute('aria-selected', 'true');
  await expect(page.locator('#cp-panel-items')).toBeVisible();
  await expect(page.locator('#cp-panel-items')).toContainText('Meadow #1');
  // Badge pill in the header (verified + creator known → Authentic tier).
  await expect(page.locator('.cp-head .vb').first()).toBeVisible();
  await expect(page.locator('.cp-head .vb').first()).toContainText('Authentic');
});

// ── 4. Token 404 within a known collection ─────────────────────────────────
test("unknown token id shows the doesn't-exist state", async ({ page }) => {
  desktopOnly();
  test.slow(); // triples the test timeout for the fallback wait
  await mockApi(page); // token detail + listing 404, collection 200, RPC blocked
  await serveBuiltPage(page, 'token');
  await page.goto(`/token/${COLLECTION}/999999`);
  // The 404 decision waits for the on-chain fallback to fail (several viem
  // retry rounds against the mocked RPC) — slower on CI runners than locally.
  await expect(page.getByText("Token #999999 doesn't exist in this collection")).toBeVisible({ timeout: 40000 });
  await expect(page.getByRole('link', { name: 'Browse the collection' })).toHaveAttribute(
    'href',
    `/collection/${COLLECTION}`,
  );
});

// ── 5. Network switcher menu ───────────────────────────────────────────────
test('network switcher opens, arrows move focus, navigation keeps the path', async ({ page }) => {
  desktopOnly();
  await mockApi(page);
  await page.addInitScript((status) => {
    (window as unknown as Record<string, unknown>).MW_NETWORK_STATUS_JSON = status;
  }, NETWORK_STATUS);
  await page.goto('/listings');

  const btn = page.locator('#net-switcher-btn');
  const menu = page.getByRole('menu');
  await btn.click();
  await expect(menu).toBeVisible();
  await expect(menu.locator('.net-item')).toHaveCount(3);

  // Focus lands on the first enabled item (Songbird — Coston2 is current
  // and disabled), and arrow keys cycle through the enabled ones.
  await expect(page.locator('.net-item:focus')).toContainText('Songbird');
  await page.keyboard.press('ArrowDown');
  await expect(page.locator('.net-item:focus')).toContainText('Flare');
  await page.keyboard.press('ArrowUp');
  await expect(page.locator('.net-item:focus')).toContainText('Songbird');

  // Choosing another network navigates to its origin + the CURRENT pathname.
  await page.route('http://songbird.test/**', (r) =>
    r.fulfill({ status: 200, contentType: 'text/html', body: '<title>songbird</title>ok' }),
  );
  await page.locator('.net-item:focus').click();
  await page.waitForURL('http://songbird.test/listings');
});

// ── 6. Mobile: tab bar, main padding, no horizontal scroll ─────────────────
test('mobile: bottom tab bar visible, padded main, no horizontal scroll', async ({ page }) => {
  mobileOnly();
  await mockApi(page);
  await page.goto('/listings');

  await expect(page.locator('.mw-tabbar')).toBeVisible();
  await expect(page.locator('.mw-tabbar a')).toHaveCount(5);

  const padBottom = await page
    .locator('#main')
    .evaluate((el) => parseFloat(getComputedStyle(el).paddingBottom));
  expect(padBottom).toBeGreaterThanOrEqual(60);

  await expect(page.getByText('Meadow #1')).toBeVisible(); // grid rendered
  const overflow = await page.evaluate(
    () => document.documentElement.scrollWidth - document.documentElement.clientWidth,
  );
  expect(overflow).toBeLessThanOrEqual(1);
});

// ── 7. Assertion sweeps ────────────────────────────────────────────────────
const SWEEP_PAGES = ['/', '/listings'] as const;
// v3.6 wave 7: the touch-target sweep also covers the four action pages, and
// the axe contrast sweep covers every page in both colour schemes.
const TOUCH_PAGES = [...SWEEP_PAGES, TOKEN_PATH, '/auction/1', '/offers', '/profile'] as const;
const AXE_PAGES = [...TOUCH_PAGES, '/auctions', '/status', '/docs/start-here', '/docs/system'] as const;

// KNOWN DEBT (predates this suite; fixing is a design-pass task, not a test
// task — remove entries as the CSS is fixed so regressions elsewhere still
// fail the gate):
//  - .nav-brand: 28px wide on <=480px viewports (logo text hidden, icon only).
//  - .nft-card internals: 9-11px text (standard-badge, price-symbol,
//    collection-addr, supply-text, buy-btn) and low-contrast captions.
//  - .vb badge pill: 11px label at size sm; axe flags .vb.is-authentic
//    (gold on dark) as serious contrast.
//  - .hs-more / .lf-pill: low-contrast caption links/pills.
const TARGET_SIZE_EXEMPT = '.nav-brand';
const FONT_SWEEP_EXEMPT = '[data-font-sweep-exempt]';
const AXE_EXEMPT = [] as const;

async function settle(page: Page, path: string) {
  await mockApi(page);
  await page.goto(path);
  // Islands hydrated: the mocked listings render a card on both pages.
  await expect(page.getByText('Meadow #1').first()).toBeVisible();
}

for (const path of TOUCH_PAGES) {
  test(`touch-target sweep (>=40x40 in header/forms) on ${path}`, async ({ page }) => {
    mobileOnly();
    await liveTrading(page);
    await open(page, path, { tokenKnown: true });
    const bad = await page.evaluate((exempt) => {
      const out: string[] = [];
      const sel = 'header a, header button, header input, header select, form a, form button, form input, form select';
      for (const el of Array.from(document.querySelectorAll<HTMLElement>(sel))) {
        if (el.closest('[hidden], [aria-hidden="true"]')) continue;
        if (el.closest(exempt)) continue; // documented known debt
        const cs = getComputedStyle(el);
        if (cs.display === 'none' || cs.visibility === 'hidden') continue;
        const r = el.getBoundingClientRect();
        if (r.width === 0 || r.height === 0) continue; // collapsed/hidden
        // 44px target with 40px tolerance.
        if (r.width < 40 || r.height < 40) {
          const label = (el.getAttribute('aria-label') || el.textContent || '').trim().slice(0, 40);
          out.push(`<${el.tagName.toLowerCase()} class="${el.className}"> ${Math.round(r.width)}x${Math.round(r.height)} "${label}"`);
        }
      }
      return out;
    }, TARGET_SIZE_EXEMPT);
    expect(bad, `interactive elements under 40x40 on ${path}:\n${bad.join('\n')}`).toEqual([]);
  });

  test(`no visible text below 12px on ${path}`, async ({ page }) => {
    desktopOnly();
    await liveTrading(page);
    await open(page, path, { tokenKnown: true });
    const bad = await page.evaluate((exempt) => {
      const out: string[] = [];
      const walker = document.createTreeWalker(document.body, NodeFilter.SHOW_TEXT);
      while (walker.nextNode()) {
        const t = walker.currentNode as Text;
        if (!t.textContent || !t.textContent.trim()) continue;
        const el = t.parentElement;
        if (!el) continue;
        if (el.closest('script, style, noscript, [hidden], [aria-hidden="true"]')) continue;
        if (el.closest(exempt)) continue; // documented known debt
        const cs = getComputedStyle(el);
        if (cs.display === 'none' || cs.visibility === 'hidden') continue;
        const r = el.getBoundingClientRect();
        if (r.width === 0 && r.height === 0) continue;
        const fs = parseFloat(cs.fontSize);
        if (fs < 12) out.push(`<${el.tagName.toLowerCase()} class="${el.className}"> ${fs}px "${t.textContent.trim().slice(0, 40)}"`);
      }
      return out;
    }, FONT_SWEEP_EXEMPT);
    expect(bad, `text under 12px on ${path}:\n${bad.join('\n')}`).toEqual([]);
  });
}

// ── 8. Home: first-run strip + zero-stats state ────────────────────────────
test('home shows the first-run strip (3 steps) and the zero-stats empty state', async ({ page }) => {
  desktopOnly();
  await mockApi(page, { emptyListings: true }); // stats all zero, nothing listed
  // The strip renders on trading networks: light up the contract globals.
  await page.addInitScript(() => {
    const w = window as unknown as Record<string, string>;
    w.MW_MARKETPLACE = '0x1111111111111111111111111111111111111111';
    w.MW_AUCTION = '0x2222222222222222222222222222222222222222';
    w.MW_OFFERBOOK = '0x3333333333333333333333333333333333333333';
    w.MW_TRADING = 'live';
  });
  await page.goto('/');

  const strip = page.getByTestId('first-run-strip');
  await expect(strip).toBeVisible();
  await expect(strip.locator('.hs-step')).toHaveCount(3);
  await expect(strip).toContainText('Connect your wallet');
  await expect(strip).toContainText('Buy or list your first NFT');

  // Stats all zero → the sentence hides and the zero state shows instead.
  await expect(page.getByTestId('right-now-empty')).toContainText('Nothing is listed yet');
  await expect(page.getByText('Nothing is listed yet', { exact: false }).first()).toBeVisible();
});

// ── 9. axe contrast scan on every page, light + dark ───────────────────────
for (const path of AXE_PAGES) {
  test(`axe: no serious/critical contrast violations on ${path}`, async ({ page }) => {
    bothSchemesOnly();
    await liveTrading(page);
    await open(page, path, { tokenKnown: true });
    const builder = new AxeBuilder({ page });
    for (const sel of AXE_EXEMPT) builder.exclude(sel); // documented known debt
    const results = await builder.analyze();
    const contrast = results.violations.filter(
      (v) => v.id.includes('contrast') && (v.impact === 'serious' || v.impact === 'critical'),
    );
    const detail = contrast
      .map((v) => `${v.id}: ${v.nodes.map((n) => n.target.join(' ')).join(', ')}`)
      .join('\n');
    expect(contrast, `contrast violations on ${path}:\n${detail}`).toEqual([]);
  });
}

// ── 9b. v3.6 wave 8: the system breakdown renders its diagrams client-side ──

test('docs/system renders every mermaid fence as an SVG diagram', async ({ page }) => {
  desktopOnly();
  await open(page, '/docs/system');
  // DocLayout swaps each <pre><code class="language-mermaid"> for a <figure> once the
  // lazy mermaid chunk has rendered it; the source block stays in the DOM, hidden.
  const figures = page.locator('figure.mermaid-diagram svg');
  await expect(figures.first()).toBeVisible({ timeout: 30000 });
  const sources = await page.locator('.doc-body pre > code.language-mermaid').count();
  expect(sources).toBeGreaterThanOrEqual(7);
  await expect(figures).toHaveCount(sources);
  await expect(page.locator('.doc-body pre > code.language-mermaid').first()).toBeHidden();
});

// ── 10. v3.6 wave 5 flows (wallet-less) ────────────────────────────────────

test('token happy path: known token renders, #offer opens the offer panel', async ({ page }) => {
  desktopOnly();
  test.slow(); // CI runners have no Reown id: the owner read waits out the 15s wagmi timeout before the page settles
  await liveTrading(page);
  await open(page, TOKEN_PATH + '#offer', { tokenKnown: true });
  await expect(page.locator('h1.tp-title')).toContainText('Meadow #1', { timeout: 40000 });
  await expect(page.locator('.tp-media img')).toBeVisible();
  await expect(page.getByText('Created by')).toBeVisible();
  // Wallet-less visitor: the deep link still opens the offer panel (spec G6).
  await expect(page.locator('.tp-panel')).toBeVisible();
  await expect(page.locator('#price-in')).toBeVisible();
});

test('profile #nfts selects the Items tab for a stored wallet', async ({ page }) => {
  desktopOnly();
  await liveTrading(page);
  await page.addInitScript(() => { localStorage.setItem('mw_addr', '0x9f8e7d6c5b4a39281706f5e4d3c2b1a098765432'); });
  await open(page, '/profile#nfts');
  const items = page.locator('#pp-tab-items');
  await expect(items).toBeVisible();
  await expect(items).toHaveAttribute('aria-selected', 'true');
});

test('mobile: sticky action bar publishes --sticky-bar-h and toasts stack above it', async ({ page }) => {
  mobileOnly();
  test.slow(); // same 15s wagmi timeout on CI as the token happy path
  await liveTrading(page);
  await open(page, TOKEN_PATH, { tokenKnown: true });
  const bar = page.getByTestId('sticky-bar');
  await expect(bar).toBeVisible({ timeout: 40000 });
  const barTop = (await bar.boundingBox())!.y;
  const h = await page.evaluate(() => parseFloat(getComputedStyle(document.documentElement).getPropertyValue('--sticky-bar-h')));
  expect(h).toBeGreaterThanOrEqual(40);
  await page.evaluate(() => window.dispatchEvent(new CustomEvent('mw-toast', { detail: { message: 'Stacked above the bar', variant: 'info' } })));
  const toast = page.locator('.toast').first();
  await expect(toast).toBeVisible();
  const tb = (await toast.boundingBox())!;
  expect(tb.y + tb.height).toBeLessThanOrEqual(barTop + 1);
  // And the bar itself sits above the tab bar.
  const tab = (await page.locator('.mw-tabbar').boundingBox())!;
  const bb = (await bar.boundingBox())!;
  expect(bb.y + bb.height).toBeLessThanOrEqual(tab.y + 1);
});

test('no-wallet sheet opens from the header, closes on Escape, returns focus', async ({ page }) => {
  desktopOnly();
  await liveTrading(page);
  await open(page, '/');
  const btn = page.locator('.wc-nowallet');
  await expect(btn).toBeVisible();
  await btn.click();
  const sheet = page.getByTestId('nowallet-sheet');
  await expect(sheet).toBeVisible();
  await expect(sheet).toContainText('A wallet is a free app');
  await expect(sheet.getByRole('link', { name: /MetaMask/ })).toHaveAttribute('href', /metamask\.io/);
  await page.keyboard.press('Escape');
  await expect(sheet).toBeHidden();
  await expect(btn).toBeFocused();
});

// FIXME (v3.6 wave 7): passes ~1 run in 3. The handler probes the sibling
// origin with a no-cors HEAD before navigating; under Playwright's route
// abort the probe sometimes still resolves and the page navigates away, so
// the "unreachable" toast never renders. Diagnose with the error-context
// snapshot (does the URL change?) before re-enabling.
test.fixme('network switch to an unreachable sibling stays on the page with a toast', async ({ page }) => {
  desktopOnly();
  await mockApi(page);
  await page.addInitScript((status) => { (window as unknown as Record<string, unknown>).MW_NETWORK_STATUS_JSON = status; }, NETWORK_STATUS);
  await page.route('http://songbird.test/**', (r) => r.abort('connectionrefused'));
  await page.goto('/listings');
  // The toast host is a Svelte island (MwRuntime); a click before it hydrates
  // would dispatch mw-toast into the void. Wait for it.
  await page.locator('.toasts').waitFor({ state: 'attached' });
  await page.locator('#net-switcher-btn').click();
  const songbird = page.locator('.net-item', { hasText: 'Songbird' });
  await expect(songbird).toContainText("you'll reconnect your wallet there");
  await songbird.click();
  await expect(page.locator('.toast', { hasText: 'unreachable right now' })).toBeVisible();
  expect(page.url()).toContain('/listings');
  expect(page.url()).not.toContain('songbird.test');
});

test('banner priority: a wrong-network banner hides the testnet banner', async ({ page }) => {
  desktopOnly();
  await liveTrading(page); // browse-only would already hide the testnet banner (browse-only > testnet)
  await open(page, '/');
  const dev = page.locator('#mw-devbanner');
  await expect(dev).toBeVisible();
  await page.evaluate(() => { document.documentElement.dataset.mwBanner = 'mismatch'; });
  await expect(dev).toBeHidden();
  await page.evaluate(() => { delete document.documentElement.dataset.mwBanner; });
  await expect(dev).toBeVisible();
});
