// Listings URL↔state round-trip + applied-filter chips (spec B4 "Listings").
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { mount, unmount, flushSync } from 'svelte';
import { waitFor } from '@testing-library/dom';
import { parseListingsParams, listingsSearch, EMPTY_FILTERS, FILTERS_EVENT, type ListingsFilterState } from '../lib/api';
import ListingsFilters from './ListingsFilters.svelte';

const COLL = '0x3333333333333333333333333333333333333333';

// ── pure URL↔state round-trip ────────────────────────────────────────────
describe('listings URL <-> filter state', () => {
  it('round-trips every canonical param', () => {
    const url = `?collection=${COLL}&min=1.5&max=20&sort=price_asc&page=3`;
    const { filters, invalid } = parseListingsParams(url);
    expect(invalid).toEqual([]);
    expect(filters).toMatchObject({ collection: COLL, min: '1.5', max: '20', sort: 'price_asc', page: 3 });
    expect(listingsSearch(filters)).toBe(url);
  });

  it('omits defaults on serialize', () => {
    expect(listingsSearch({ ...EMPTY_FILTERS })).toBe('');
    expect(listingsSearch({ ...EMPTY_FILTERS, sort: 'ending' })).toBe('?sort=ending');
  });

  it('ignores invalid params and reports their names (spec: ignored + toast)', () => {
    const { filters, invalid } = parseListingsParams('?collection=not-an-address&min=abc&sort=bogus&page=-2&max=3');
    expect(invalid.sort()).toEqual(['collection', 'min', 'page', 'sort']);
    expect(filters).toMatchObject({ collection: '', min: '', max: '3', sort: 'recent', page: 1 });
  });

  it('never serializes the session-only seller toggle', () => {
    expect(listingsSearch({ ...EMPTY_FILTERS, seller: '0x2222222222222222222222222222222222222222' })).toBe('');
  });
});

// ── mounted component ────────────────────────────────────────────────────
function fetchMock(map: Record<string, unknown>) {
  return vi.fn(async (input: RequestInfo | URL) => {
    const url = String(input);
    for (const [k, v] of Object.entries(map)) {
      if (url.includes(k)) return { ok: true, status: 200, json: async () => v } as Response;
    }
    return { ok: false, status: 404, json: async () => ({}) } as Response;
  });
}

describe('<ListingsFilters>', () => {
  let host: HTMLDivElement;
  let app: ReturnType<typeof mount> | undefined;

  beforeEach(() => {
    host = document.createElement('div');
    document.body.appendChild(host);
    vi.stubGlobal('fetch', fetchMock({
      '/api/v1/metrics': { totalActiveListings: 12 },
      '/api/v1/collections?limit=200': [{ address: COLL, name: 'Magic Webb Animi' }],
      '/api/v1/listings': [],
      '/traits': {},
    }));
  });
  afterEach(() => {
    if (app) unmount(app);
    app = undefined;
    host.remove();
    vi.unstubAllGlobals();
    history.replaceState(null, '', '/listings');
  });

  it('reads the URL on load, populates the form, and broadcasts the state', async () => {
    history.replaceState(null, '', `/listings?collection=${COLL}&min=1&max=5&sort=price_desc`);
    const events: ListingsFilterState[] = [];
    const onF = (e: Event) => events.push((e as CustomEvent<ListingsFilterState>).detail);
    window.addEventListener(FILTERS_EVENT, onF);
    app = mount(ListingsFilters, { target: host });
    flushSync();
    window.removeEventListener(FILTERS_EVENT, onF);

    expect(events).toHaveLength(1);
    expect(events[0]).toMatchObject({ collection: COLL, min: '1', max: '5', sort: 'price_desc' });
    expect((host.querySelector('#lf-min') as HTMLInputElement).value).toBe('1');
    expect((host.querySelector('#lf-max') as HTMLInputElement).value).toBe('5');
    expect((host.querySelector('#lf-sort') as HTMLSelectElement).value).toBe('price_desc');
    // h1 once + count pill
    expect(host.querySelectorAll('h1')).toHaveLength(1);
    expect(host.querySelector('h1')!.textContent).toBe('Listings');
    await waitFor(() => expect(host.querySelector('[data-testid="live-count"]')!.textContent).toContain('12 live'));
  });

  it('renders applied filters as chips with × that update URL + state in place', async () => {
    history.replaceState(null, '', `/listings?collection=${COLL}&min=1`);
    app = mount(ListingsFilters, { target: host });
    flushSync();

    const chips = host.querySelectorAll('[data-testid="filter-chip"]');
    expect(chips).toHaveLength(2);
    expect(chips[1].textContent).toContain('Min 1');

    const events: ListingsFilterState[] = [];
    const onF = (e: Event) => events.push((e as CustomEvent<ListingsFilterState>).detail);
    window.addEventListener(FILTERS_EVENT, onF);
    (chips[1].querySelector('button') as HTMLButtonElement).click();
    flushSync();
    window.removeEventListener(FILTERS_EVENT, onF);

    expect(events[0].min).toBe('');
    expect(events[0].collection).toBe(COLL);
    // replaceState, never navigation: URL updated in place.
    expect(location.search).toBe(`?collection=${COLL}`);
    expect(host.querySelectorAll('[data-testid="filter-chip"]')).toHaveLength(1);
  });

  it('Apply rewrites the URL via replaceState and resets to page 1', () => {
    history.replaceState(null, '', '/listings?page=4');
    app = mount(ListingsFilters, { target: host });
    flushSync();
    (host.querySelector('#lf-min') as HTMLInputElement).value = '2';
    host.querySelector('#lf-min')!.dispatchEvent(new Event('input', { bubbles: true }));
    flushSync();
    const events: ListingsFilterState[] = [];
    const onF = (e: Event) => events.push((e as CustomEvent<ListingsFilterState>).detail);
    window.addEventListener(FILTERS_EVENT, onF);
    (host.querySelector('.lf-apply') as HTMLButtonElement).click();
    flushSync();
    window.removeEventListener(FILTERS_EVENT, onF);
    expect(events[0]).toMatchObject({ min: '2', page: 1 });
    expect(location.search).toBe('?min=2');
  });

  it('save-search is visible for everyone but disabled with a Hint for viewers', () => {
    app = mount(ListingsFilters, { target: host });
    flushSync();
    const save = host.querySelector('[data-testid="save-search"]') as HTMLButtonElement;
    expect(save).toBeTruthy();
    expect(save.disabled).toBe(true);
    // The Hint reason sits right next to it.
    const hint = save.parentElement!.querySelector('[role="tooltip"]');
    expect(hint?.textContent).toBe('Connect a wallet to save searches');
  });
});
