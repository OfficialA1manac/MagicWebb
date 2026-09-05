// Small JSON fixtures + the page.route API mock for the Playwright smoke
// suite (plan B6). Everything under /api/v1/** is intercepted; no backend.
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import path from 'node:path';
import type { Page, Route } from '@playwright/test';

export const COLLECTION = '0x832d74cfbb4617b50c32cd110dfe16837a359b35';
export const CREATOR = '0x1622aa8ead16278ead16278ead16278ead16278e';
export const SELLER = '0x9f8e7d6c5b4a39281706f5e4d3c2b1a098765432';

const DATA_IMG =
  'data:image/svg+xml;utf8,<svg xmlns="http://www.w3.org/2000/svg" width="80" height="80"><rect width="80" height="80" fill="%237dd3fc"/></svg>';

/** Listings rows — carry the badge contract fields every list endpoint has. */
export const listings = [
  {
    collection: COLLECTION,
    token_id: '1',
    seller: SELLER,
    price_wei: '5000000000000000000', // 5
    amount: 1,
    standard: 'erc721',
    expires_at: new Date(Date.now() + 7 * 24 * 3600 * 1000).toISOString(),
    listed_at: new Date(Date.now() - 3600 * 1000).toISOString(),
    tx_hash: '0x' + 'ab'.repeat(32),
    name: 'Meadow #1',
    image_uri: DATA_IMG,
    total_supply: 100,
    collection_name: 'Magic Meadows',
    collection_tracked: true,
    collection_verified: true,
    collection_creator: CREATOR,
  },
  {
    collection: COLLECTION,
    token_id: '2',
    seller: SELLER,
    price_wei: '8000000000000000000', // 8
    amount: 1,
    standard: 'erc721',
    expires_at: new Date(Date.now() + 7 * 24 * 3600 * 1000).toISOString(),
    listed_at: new Date(Date.now() - 7200 * 1000).toISOString(),
    tx_hash: '0x' + 'cd'.repeat(32),
    name: 'Meadow #2',
    image_uri: DATA_IMG,
    total_supply: 100,
    collection_name: 'Magic Meadows',
    collection_tracked: true,
    collection_verified: false,
    collection_creator: null,
  },
];

export const collectionDetail = {
  address: COLLECTION,
  name: 'Magic Meadows',
  symbol: 'MEAD',
  standard: 'erc721',
  verified: true,
  creator_addr: CREATOR,
  floor_price_wei: '5000000000000000000',
  volume_24h_wei: '0',
  listed_count: 2,
  verified_reason: { standard_ok: true, metadata_ok: true, creator_known: true },
};

export const tokensPage = {
  collection: collectionDetail,
  tokens: [
    { token_id: '1', owner: SELLER, name: 'Meadow #1', image: DATA_IMG, listed: true, price_wei: '5000000000000000000' },
    { token_id: '2', owner: SELLER, name: 'Meadow #2', image: DATA_IMG, listed: false },
  ],
  page: 1,
  limit: 24,
  total: 2,
};

export const collectionsList = [
  { address: COLLECTION, name: 'Magic Meadows', symbol: 'MEAD', verified: true, creator_addr: CREATOR },
];

export const statsZero = { listings: 0, liveAuctions: 0, offers: 0, soldTodayWei: '0' };

export interface MockOptions {
  /** Empty listings everywhere (home zero-state test). */
  emptyListings?: boolean;
  /** /api/v1/collections/:addr → 404 (untracked collection). */
  collection404?: boolean;
}

/**
 * Intercept every /api/v1/** call with small fixtures, and abort chain-RPC
 * traffic so on-chain fallbacks fail fast (wallet-less suite). The token
 * detail endpoint always 404s: paired with a 200 collection this is exactly
 * the "Token #N doesn't exist in this collection" state the spec tests.
 */
export async function mockApi(page: Page, opts: MockOptions = {}): Promise<void> {
  // No real chain: answer RPC calls from the on-chain fallback readers with a
  // proper JSON-RPC error (aborting instead sends ethers into a long retry
  // loop that keeps the token page in its skeleton past the test timeout).
  await page.route(/^https?:\/\/[^/]*flare\.network\//, async (route) => {
    let ids: unknown[] = [0];
    try {
      const body = JSON.parse(route.request().postData() || '{}');
      ids = Array.isArray(body) ? body.map((b) => b.id ?? 0) : [body.id ?? 0];
    } catch { /* keep default */ }
    const err = (id: unknown) => ({ jsonrpc: '2.0', id, error: { code: -32000, message: 'e2e: no chain' } });
    const payload = ids.length === 1 ? err(ids[0]) : ids.map(err);
    await route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(payload) });
  });

  await page.route('**/api/v1/**', async (route: Route) => {
    const req = route.request();
    const u = new URL(req.url());
    const p = u.pathname;
    const json = (body: unknown, status = 200) =>
      route.fulfill({ status, contentType: 'application/json', body: JSON.stringify(body) });

    // POST view-counter beacon and anything non-GET: accept quietly.
    if (req.method() !== 'GET') return json({ ok: true });

    if (p === '/api/v1/stats') return json(statsZero);
    if (p === '/api/v1/metrics') return json({ totalActiveListings: opts.emptyListings ? 0 : listings.length });
    if (p === '/api/v1/activity') return json([]);
    if (p === '/api/v1/auctions') return json([]);
    if (p === '/api/v1/offers') return json([]);
    if (p === '/api/v1/collections') return json(opts.emptyListings ? [] : collectionsList);
    if (p.startsWith('/api/v1/collections/')) {
      if (p.endsWith('/tokens')) return json(tokensPage);
      if (p.endsWith('/traits')) return json({});
      return opts.collection404 ? json({ error: 'not found' }, 404) : json(collectionDetail);
    }
    if (p === '/api/v1/listings') return json(opts.emptyListings ? [] : listings);
    if (p.startsWith('/api/v1/listings/')) return json({ error: 'not found' }, 404); // single listing
    if (p.startsWith('/api/v1/token/')) return json({ error: 'not found' }, 404); // token detail 404
    if (p.startsWith('/api/v1/wallet/')) return json([]);
    if (p.startsWith('/api/v1/profile/')) return json({ error: 'not found' }, 404);
    if (p.startsWith('/api/v1/notifications')) return json({ notifications: [], unread: 0 });
    return json({ error: 'unmocked endpoint in e2e: ' + p }, 404);
  });
}

const distDir = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..', 'dist');
const builtHtml = new Map<string, string>();

/**
 * astro preview serves only exact files, so deep routes like
 * /collection/0x…/ would fall through to 404.html. In production the Go
 * server rewrites them onto the built page; this mirrors that rewrite by
 * fulfilling DOCUMENT requests for `/<base>/…` with dist/<base>/index.html.
 */
export async function serveBuiltPage(page: Page, base: 'collection' | 'token'): Promise<void> {
  if (!builtHtml.has(base)) builtHtml.set(base, readFileSync(path.join(distDir, base, 'index.html'), 'utf8'));
  await page.route(`**/${base}/**`, async (route) => {
    if (route.request().resourceType() !== 'document') return route.fallback();
    await route.fulfill({ status: 200, contentType: 'text/html', body: builtHtml.get(base)! });
  });
}

/** Three-network switcher fixture (current = Coston2 on the preview origin). */
export const NETWORK_STATUS = JSON.stringify([
  { chainId: 114, name: 'Coston2', origin: 'http://127.0.0.1:4321', status: 'trading', testnet: true },
  { chainId: 19, name: 'Songbird', origin: 'http://songbird.test', status: 'browse-only', testnet: false },
  { chainId: 14, name: 'Flare', origin: 'http://flare.test', status: 'browse-only', testnet: false },
]);
