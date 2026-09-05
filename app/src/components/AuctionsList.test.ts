// Auctions URL↔state round-trip + segmented control (spec B4 "Auctions").
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { mount, unmount, flushSync } from 'svelte';
import AuctionsList, {
  parseAuctionsParams, auctionsSearch, withSegment, sellerError, sortAuctionRows,
  EMPTY_AUCTION_FILTERS, type AuctionsFilterState,
} from './AuctionsList.svelte';

const COLL = '0x3333333333333333333333333333333333333333';
const SELLER = '0x2222222222222222222222222222222222222222';

// ── pure URL↔state round-trip ────────────────────────────────────────────
describe('auctions URL <-> filter state', () => {
  it('round-trips every canonical param', () => {
    const url = `?status=ended&collection=${COLL}&min=1.5&max=20&seller=${SELLER}&page=3`;
    const { filters, invalid } = parseAuctionsParams(url);
    expect(invalid).toEqual([]);
    expect(filters).toMatchObject({ segment: 'ended', collection: COLL, min: '1.5', max: '20', seller: SELLER, page: 3 });
    expect(auctionsSearch(filters)).toBe(url);
  });

  it('maps the segmented control exactly per spec: live | active&sort=ending | ended', () => {
    // Live: status=active, nothing serialized.
    expect(auctionsSearch({ ...EMPTY_AUCTION_FILTERS })).toBe('');
    expect(parseAuctionsParams('').filters.segment).toBe('live');
    expect(parseAuctionsParams('?status=active').filters.segment).toBe('live');
    // Ending soon: active + sort=ending.
    const ending = withSegment({ ...EMPTY_AUCTION_FILTERS }, 'ending');
    expect(auctionsSearch(ending)).toBe('?sort=ending');
    expect(parseAuctionsParams('?sort=ending').filters.segment).toBe('ending');
    // Ended: status=ended.
    const ended = withSegment({ ...EMPTY_AUCTION_FILTERS }, 'ended');
    expect(auctionsSearch(ended)).toBe('?status=ended');
    expect(parseAuctionsParams('?status=ended').filters.segment).toBe('ended');
  });

  it('leaving the ending segment restores the default sort', () => {
    const ending = withSegment({ ...EMPTY_AUCTION_FILTERS }, 'ending');
    expect(ending.sort).toBe('ending');
    const back = withSegment(ending, 'live');
    expect(back.sort).toBe('recent');
    expect(auctionsSearch(back)).toBe('');
  });

  it('ignores invalid params and reports their names', () => {
    const { filters, invalid } = parseAuctionsParams('?status=bogus&collection=nope&min=abc&seller=xyz&page=-1&max=3');
    expect(invalid.sort()).toEqual(['collection', 'min', 'page', 'seller', 'status']);
    expect(filters).toMatchObject({ segment: 'live', collection: '', min: '', max: '3', seller: '', page: 1 });
  });

  it('segment changes reset to page 1 and keep the other filters', () => {
    const f: AuctionsFilterState = { ...EMPTY_AUCTION_FILTERS, collection: COLL, min: '2', page: 4 };
    const next = withSegment(f, 'ended');
    expect(next).toMatchObject({ segment: 'ended', collection: COLL, min: '2', page: 1 });
  });
});

describe('seller inline validation', () => {
  it('flags a non-address with the exact spec copy', () => {
    expect(sellerError('not-an-address')).toBe('Enter a wallet address (0x…)');
    expect(sellerError('0x123')).toBe('Enter a wallet address (0x…)');
  });
  it('accepts empty and a full address', () => {
    expect(sellerError('')).toBe('');
    expect(sellerError(SELLER)).toBe('');
  });
});

describe('client-side sort', () => {
  const row = (id: number, reserve: string, bid: string, starts: string, ends: string) => ({
    auction_id: id, reserve_price_wei: reserve, highest_bid_wei: bid, starts_at: starts, ends_at: ends,
  });
  const rows = [
    row(1, '10000000000000000000', '0', '2026-09-01T00:00:00Z', '2026-09-05T00:00:00Z'),
    row(2, '1000000000000000000', '30000000000000000000', '2026-09-03T00:00:00Z', '2026-09-04T00:00:00Z'),
    row(3, '20000000000000000000', '0', '2026-09-02T00:00:00Z', '2026-09-06T00:00:00Z'),
  ];
  it('ending = soonest first, recent = newest first', () => {
    expect(sortAuctionRows(rows, 'ending').map((r) => r.auction_id)).toEqual([2, 1, 3]);
    expect(sortAuctionRows(rows, 'recent').map((r) => r.auction_id)).toEqual([2, 3, 1]);
  });
  it('price sorts by current bid when present, else reserve', () => {
    expect(sortAuctionRows(rows, 'price_asc').map((r) => r.auction_id)).toEqual([1, 3, 2]);
    expect(sortAuctionRows(rows, 'price_desc').map((r) => r.auction_id)).toEqual([2, 3, 1]);
  });
});

// ── mounted segmented control ────────────────────────────────────────────
function fetchMock(map: Record<string, unknown>) {
  return vi.fn(async (input: RequestInfo | URL) => {
    const url = String(input);
    for (const [k, v] of Object.entries(map)) {
      if (url.includes(k)) return { ok: true, status: 200, json: async () => v } as Response;
    }
    return { ok: true, status: 200, json: async () => [] } as Response;
  });
}

describe('<AuctionsList> segmented control', () => {
  let host: HTMLDivElement;
  let app: ReturnType<typeof mount> | undefined;

  beforeEach(() => {
    host = document.createElement('div');
    document.body.appendChild(host);
    vi.stubGlobal('fetch', fetchMock({ '/api/v1/auctions': [], '/api/v1/collections': [] }));
  });
  afterEach(() => {
    if (app) unmount(app);
    app = undefined;
    host.remove();
    vi.unstubAllGlobals();
    history.replaceState(null, '', '/auctions');
  });

  it('renders h1 "Auctions" + the three segments, Live on by default', () => {
    history.replaceState(null, '', '/auctions');
    app = mount(AuctionsList, { target: host });
    flushSync();
    expect(host.querySelector('h1')!.textContent).toBe('Auctions');
    expect(host.querySelector('[data-testid="seg-live"]')!.getAttribute('aria-pressed')).toBe('true');
    expect(host.querySelector('[data-testid="seg-ending"]')!.textContent).toBe('Ending soon');
    expect(host.querySelector('[data-testid="seg-ended"]')!.textContent).toBe('Ended');
  });

  it('clicking a segment rewrites the URL in place (replaceState, per spec mapping)', () => {
    history.replaceState(null, '', '/auctions');
    app = mount(AuctionsList, { target: host });
    flushSync();
    (host.querySelector('[data-testid="seg-ending"]') as HTMLButtonElement).click();
    flushSync();
    expect(location.search).toBe('?sort=ending');
    expect(host.querySelector('[data-testid="seg-ending"]')!.getAttribute('aria-pressed')).toBe('true');
    (host.querySelector('[data-testid="seg-ended"]') as HTMLButtonElement).click();
    flushSync();
    expect(location.search).toBe('?status=ended');
    (host.querySelector('[data-testid="seg-live"]') as HTMLButtonElement).click();
    flushSync();
    expect(location.search).toBe('');
  });

  it('reads the segment from the URL on load', () => {
    history.replaceState(null, '', '/auctions?status=ended');
    app = mount(AuctionsList, { target: host });
    flushSync();
    expect(host.querySelector('[data-testid="seg-ended"]')!.getAttribute('aria-pressed')).toBe('true');
  });

  it('invalid seller input shows the inline error and does not apply', () => {
    history.replaceState(null, '', '/auctions');
    app = mount(AuctionsList, { target: host });
    flushSync();
    const seller = host.querySelector('#al-seller') as HTMLInputElement;
    seller.value = 'garbage';
    seller.dispatchEvent(new Event('input', { bubbles: true }));
    flushSync();
    (host.querySelector('form.al-bar') as HTMLFormElement).dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }));
    flushSync();
    expect(host.querySelector('[data-testid="seller-error"]')!.textContent).toBe('Enter a wallet address (0x…)');
    expect(location.search).toBe('');
  });
});
