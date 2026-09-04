// CollectionPage (spec B4 "Collection"): unknown-address 404 copy, tabs
// (Items default · Listings · Activity), and the seller banner.
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { mount, unmount, flushSync } from 'svelte';
import { waitFor } from '@testing-library/dom';
import CollectionPage from './CollectionPage.svelte';

const ADDR = '0x3333333333333333333333333333333333333333';
const ME = '0x2222222222222222222222222222222222222222';

const collection = {
  address: ADDR, name: 'Magic Webb Animi', symbol: 'MWA', standard: 'erc721',
  verified: true, creator_addr: '', floor_price_wei: '0', volume_24h_wei: '0',
  listed_count: 0, verified_reason: { standard_ok: true, metadata_ok: true, creator_known: false },
};

function fetchMock(overrides: Record<string, unknown | (() => Response)> = {}, notFound = false) {
  return vi.fn(async (input: RequestInfo | URL) => {
    const url = String(input);
    const hit = (body: unknown, status = 200) => ({ ok: status < 400, status, json: async () => body }) as Response;
    if (url.includes(`/api/v1/collections/${ADDR}/tokens`)) {
      return hit(overrides.tokens ?? { collection, tokens: [], page: 1, limit: 48, total: 0 });
    }
    if (url.includes(`/api/v1/collections/${ADDR}`)) {
      return notFound ? hit({ error: 'not found' }, 404) : hit(overrides.collection ?? collection);
    }
    if (url.includes('/api/v1/listings')) return hit(overrides.listings ?? []);
    if (url.includes('/api/v1/activity')) return hit(overrides.activity ?? []);
    if (url.includes(`/api/v1/wallet/${ME}/nfts`)) return hit(overrides.walletNfts ?? []);
    return hit({ error: 'not found' }, 404);
  });
}

describe('<CollectionPage>', () => {
  let host: HTMLDivElement;
  let app: ReturnType<typeof mount> | undefined;

  beforeEach(() => { host = document.createElement('div'); document.body.appendChild(host); });
  afterEach(() => { if (app) unmount(app); app = undefined; host.remove(); vi.unstubAllGlobals(); });

  it('unknown address → the exact 404 copy (spec)', async () => {
    vi.stubGlobal('fetch', fetchMock({}, true));
    app = mount(CollectionPage, { target: host, props: { addr: ADDR } });
    flushSync();
    await waitFor(() => expect(host.textContent).toContain("We don't track this collection yet"));
    expect(host.textContent).toContain('Paste the address in Search to request indexing.');
    expect(host.querySelector('h1')).toBeNull(); // no header for an untracked address
  });

  it('renders header + tabs with Items selected by default; tab switch fetches in place', async () => {
    vi.stubGlobal('fetch', fetchMock({
      tokens: {
        collection,
        tokens: [{ token_id: '1', owner: ME, name: 'Animi #1', image: '', listed: true, price_wei: '2000000000000000000' }],
        page: 1, limit: 48, total: 1,
      },
    }));
    app = mount(CollectionPage, { target: host, props: { addr: ADDR } });
    flushSync();
    await waitFor(() => expect(host.querySelector('h1')?.textContent).toBe('Magic Webb Animi'));

    const tabs = host.querySelectorAll('[role="tab"]');
    expect(Array.from(tabs).map((t) => t.textContent)).toEqual(['Items', 'Listings', 'Activity']);
    expect(tabs[0].getAttribute('aria-selected')).toBe('true');
    // Items grid with the Listed chip + price
    expect(host.textContent).toContain('Animi #1');
    expect(host.textContent).toContain('Listed');
    // Stats: 1 item > 0 → segments, not "No sales yet"
    await waitFor(() => expect(host.querySelector('[data-testid="stats"]')!.textContent).toContain('1 item'));

    (tabs[2] as HTMLButtonElement).click();
    flushSync();
    await waitFor(() => expect(tabs[2].getAttribute('aria-selected')).toBe('true'));
    await waitFor(() => expect(host.textContent).toContain('No activity yet'));
  });

  it('all-zero stats → "No sales yet"', async () => {
    vi.stubGlobal('fetch', fetchMock());
    app = mount(CollectionPage, { target: host, props: { addr: ADDR } });
    flushSync();
    await waitFor(() => expect(host.querySelector('[data-testid="stats"]')!.textContent!.trim()).toBe('No sales yet'));
  });

  it('connected wallet owning items here → seller banner with count + List them link', async () => {
    localStorage.setItem('mw_addr', ME);
    vi.stubGlobal('fetch', fetchMock({
      walletNfts: [{ collection: ADDR }, { collection: ADDR }, { collection: '0x4444444444444444444444444444444444444444' }],
    }));
    app = mount(CollectionPage, { target: host, props: { addr: ADDR } });
    flushSync();
    await waitFor(() => expect(host.querySelector('[data-testid="seller-banner"]')).toBeTruthy());
    const banner = host.querySelector('[data-testid="seller-banner"]')!;
    expect(banner.textContent).toContain('You own 2 items here');
    expect(banner.querySelector('a')!.getAttribute('href')).toBe('/profile#nfts');
  });

  it('no wallet → no seller banner', async () => {
    vi.stubGlobal('fetch', fetchMock());
    app = mount(CollectionPage, { target: host, props: { addr: ADDR } });
    flushSync();
    await waitFor(() => expect(host.querySelector('h1')).toBeTruthy());
    expect(host.querySelector('[data-testid="seller-banner"]')).toBeNull();
  });
});
