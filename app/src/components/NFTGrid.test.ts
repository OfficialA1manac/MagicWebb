// NFTGrid state table (spec B4 "Listings"): empty (no filters) vs no-match
// (filters applied) render different copy and different CTAs.
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { mount, unmount, flushSync } from 'svelte';
import { waitFor } from '@testing-library/dom';

// jsdom has no Web Animations API; NFTCard's fly transition calls animate().
if (typeof Element.prototype.animate !== 'function') {
  Element.prototype.animate = function () {
    const a = { cancel() {}, pause() {}, play() {}, finish() {}, currentTime: 0, playState: 'finished', finished: Promise.resolve(), oncancel: null as null | (() => void) };
    let fin: null | (() => void) = null;
    Object.defineProperty(a, 'onfinish', { get: () => fin, set(fn: () => void) { fin = fn; queueMicrotask(() => fn?.()); } });
    return a as unknown as Animation;
  };
}
import { _resetChainCache } from '../lib/chains';
import { EMPTY_FILTERS } from '../lib/api';
import NFTGrid from './NFTGrid.svelte';

const COLL = '0x3333333333333333333333333333333333333333';
const ADDR = '0x1111111111111111111111111111111111111111';

function fetchMock(listings: unknown[]) {
  return vi.fn(async (input: RequestInfo | URL) => {
    const url = String(input);
    if (url.includes('/api/v1/metrics')) return { ok: true, status: 200, json: async () => ({ totalActiveListings: listings.length }) } as Response;
    if (url.includes('/api/v1/listings')) return { ok: true, status: 200, json: async () => listings } as Response;
    return { ok: false, status: 404, json: async () => ({}) } as Response;
  });
}

describe('<NFTGrid> empty vs no-match', () => {
  let host: HTMLDivElement;
  let app: ReturnType<typeof mount> | undefined;

  beforeEach(() => {
    host = document.createElement('div');
    document.body.appendChild(host);
    // Trading live: all three contract addresses present.
    window.MW_TRADING = 'live';
    window.MW_MARKETPLACE = ADDR;
    window.MW_AUCTION = ADDR;
    window.MW_OFFERBOOK = ADDR;
    _resetChainCache();
  });
  afterEach(() => {
    if (app) unmount(app);
    app = undefined;
    host.remove();
    vi.unstubAllGlobals();
    delete window.MW_TRADING;
    _resetChainCache();
  });

  it('no listings + no filters → "Nothing is listed yet" with the free-listing copy', async () => {
    vi.stubGlobal('fetch', fetchMock([]));
    app = mount(NFTGrid, { target: host, props: { filters: { ...EMPTY_FILTERS } } });
    flushSync();
    await waitFor(() => expect(host.textContent).toContain('Nothing is listed yet'));
    expect(host.textContent).toContain('Listing is free — you only pay 2% when it sells.');
    expect(host.textContent).toContain('List an NFT');
    expect(host.textContent).not.toContain('No listings match');
  });

  it('no listings + filters applied → "No listings match" with Clear filters', async () => {
    vi.stubGlobal('fetch', fetchMock([]));
    app = mount(NFTGrid, { target: host, props: { filters: { ...EMPTY_FILTERS, collection: COLL } } });
    flushSync();
    await waitFor(() => expect(host.textContent).toContain('No listings match'));
    expect(host.textContent).toContain('Clear filters');
    expect(host.textContent).not.toContain('Nothing is listed yet');
  });

  it('rows render cards + the "Showing X of N" footer', async () => {
    const row = {
      collection: COLL, token_id: '42', seller: ADDR, price_wei: '1000000000000000000',
      amount: 1, standard: 'erc721', expires_at: '', listed_at: '', tx_hash: '',
      name: 'Animi #42', image_uri: '', total_supply: 1, collection_verified: true,
    };
    vi.stubGlobal('fetch', fetchMock([row]));
    app = mount(NFTGrid, { target: host, props: { filters: { ...EMPTY_FILTERS } } });
    flushSync();
    await waitFor(() => expect(host.textContent).toContain('Animi #42'));
    await waitFor(() => expect(host.querySelector('[data-testid="showing"]')!.textContent).toBe('Showing 1 of 1'));
    // 1 row < 48/page → no Load more.
    expect(host.textContent).not.toContain('Load more');
  });

  it('fetch failure → "Failed to load listings" + Retry that refetches', async () => {
    let calls = 0;
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.includes('/api/v1/listings')) {
        calls += 1;
        return { ok: false, status: 404, json: async () => ({}) } as Response; // 4xx: no retry loop
      }
      return { ok: true, status: 200, json: async () => ({}) } as Response;
    }));
    app = mount(NFTGrid, { target: host, props: { filters: { ...EMPTY_FILTERS } } });
    flushSync();
    await waitFor(() => expect(host.textContent).toContain('Failed to load listings'));
    const before = calls;
    (Array.from(host.querySelectorAll('button')).find((b) => b.textContent!.includes('Retry')) as HTMLButtonElement).click();
    await waitFor(() => expect(calls).toBeGreaterThan(before));
  });
});
