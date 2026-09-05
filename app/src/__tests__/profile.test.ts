// Profile page tests (spec B4 "Profile"), rewritten against the ProfilePage
// island + its pure helpers (the old file re-implemented profile.astro's
// inline script; that script is gone). Coverage kept equivalent: address
// resolution, debounce/overlap guards, tab labels, batch eligibility,
// snapshot cache, the refunds-at-zero card, and the edit-profile modal.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { mount, unmount, flushSync } from 'svelte';
import { waitFor } from '@testing-library/dom';

import {
  isEthAddr, pathAddr, resolveProfileAddr, tabsFor, emptyFor,
  mergeInventory, itemKey, isErc1155, batchEligible, batchBarLabel,
  validateBatch, initialsFor, snapshotKey, saveSnapshot, loadSnapshot,
  createWalletGuard, installFirstTradeDone, markFirstTradeDone,
  FIRST_TRADE_KEY, HINT_1155, SNAP_PREFIX,
  type InventoryItem,
} from '../components/ProfilePage.helpers';

// ── Module mocks so mounting ProfilePage (→ RefundsPanel) never pulls the
//    wallet/tx stack into jsdom ────────────────────────────────────────────
vi.mock('../lib/mw', () => ({
  MW: {
    pendingReturns: vi.fn(async () => [] as unknown[]),
    withdrawRefundFrom: vi.fn(async () => ({})),
    ws: { on: () => () => {} },
  },
  whenMW: () => new Promise(() => {}),
  installMW: () => ({}),
}));
vi.mock('../lib/tx/client', () => ({
  onAccountChange: (cb: (a: { address: string | null }) => void) => {
    cb({ address: localStorage.getItem('mw_addr') });
    return () => {};
  },
}));
vi.mock('../lib/tx/core', () => ({
  CORE_LABEL: { marketplace: 'Marketplace', auctionHouse: 'Auction house', offerBook: 'Offer book' },
  pendingReturns: vi.fn(async () => [] as unknown[]),
  withdrawRefundFrom: vi.fn(async () => ({})),
}));

import ProfilePage from '../components/ProfilePage.svelte';

const ADDR_A = '0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa';
const ADDR_B = '0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb';

// ── Helper: fetch mock for the three profile endpoints ─────────────────────
function composite(overrides: Record<string, unknown> = {}) {
  return {
    listings: [], auctions: [], offersSent: [], offersReceived: [],
    activity: [], createdCollections: [], ...overrides,
  };
}

function fetchMock(overrides: {
  profile?: unknown; nfts?: unknown; pp?: Record<string, unknown>;
  putSpy?: ReturnType<typeof vi.fn>;
} = {}) {
  return vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = String(input);
    const hit = (body: unknown, status = 200) => ({
      ok: status < 400, status,
      headers: { get: () => null },
      json: async () => body,
    }) as unknown as Response;
    if (init?.method === 'PUT' && url.includes('/api/v1/profile/')) {
      overrides.putSpy?.(url, init);
      return hit({ ok: true });
    }
    if (url.includes('/api/v1/profile-page/')) return hit(composite(overrides.pp ?? {}));
    if (url.includes('/api/v1/profile/')) return hit(overrides.profile ?? { display_name: 'Test User' });
    if (url.includes('/nfts')) return hit(overrides.nfts ?? []);
    // JSON-RPC balance probe and anything else.
    return hit({ result: '0x0' });
  });
}

// ── Pure helpers ───────────────────────────────────────────────────────────

describe('address resolution', () => {
  it('accepts only well-formed 0x addresses', () => {
    expect(isEthAddr(ADDR_A)).toBe(true);
    expect(isEthAddr(ADDR_A.toUpperCase().replace('0X', '0x'))).toBe(true);
    expect(isEthAddr('0x123')).toBe(false);
    expect(isEthAddr(ADDR_A.slice(0, 41) + 'g')).toBe(false);
    expect(isEthAddr(null)).toBe(false);
    expect(isEthAddr(42)).toBe(false);
  });

  it('pathAddr extracts and lowercases /profile/:addr', () => {
    expect(pathAddr(`/profile/${ADDR_A.toUpperCase().replace('0X', '0x')}`)).toBe(ADDR_A);
    expect(pathAddr(`/profile/${ADDR_A}/`)).toBe(ADDR_A);
    expect(pathAddr('/profile')).toBe('');
    expect(pathAddr('/profile/not-an-address')).toBe('');
    expect(pathAddr('/')).toBe('');
  });

  it('URL address wins over the stored wallet', () => {
    expect(resolveProfileAddr(`/profile/${ADDR_B}`, ADDR_A)).toEqual({ target: ADDR_B, fromPath: true });
  });

  it('falls back to the stored wallet (lowercased), then to nothing', () => {
    expect(resolveProfileAddr('/profile', ADDR_A.toUpperCase().replace('0X', '0x'))).toEqual({ target: ADDR_A, fromPath: false });
    expect(resolveProfileAddr('/profile', 'garbage')).toEqual({ target: '', fromPath: false });
    expect(resolveProfileAddr('/profile', null)).toEqual({ target: '', fromPath: false });
  });
});

describe('tabs (spec labels)', () => {
  it('own profile says "Your items"', () => {
    expect(tabsFor(true).map((t) => t.label)).toEqual(['Your items', 'For sale', 'Auctions', 'Offers', 'Activity']);
  });
  it('someone else\'s profile says "Items"', () => {
    expect(tabsFor(false).map((t) => t.label)).toEqual(['Items', 'For sale', 'Auctions', 'Offers', 'Activity']);
  });
  it('tab ids are stable regardless of ownership', () => {
    expect(tabsFor(true).map((t) => t.id)).toEqual(tabsFor(false).map((t) => t.id));
  });
  it('per-tab empty copy matches the spec, with one action where sensible', () => {
    expect(emptyFor('items', true).title).toBe('No items yet');
    expect(emptyFor('sale', true).title).toBe('Nothing for sale');
    expect(emptyFor('auctions', true).title).toBe('No auctions');
    expect(emptyFor('offers', true).title).toBe('No offers');
    expect(emptyFor('activity', true).title).toBe('No activity yet');
    expect(emptyFor('sale', true).cta?.label).toBe('List an NFT');
    expect(emptyFor('sale', false).cta).toBeUndefined();
  });
});

describe('batch eligibility (ERC-721 only)', () => {
  const base: InventoryItem = { collection: '0xc0ffee', token_id: '1', standard: 'erc721' };
  it('unlisted own ERC-721 is selectable', () => {
    expect(batchEligible(base, true)).toBe(true);
  });
  it('missing standard is treated as ERC-721', () => {
    expect(batchEligible({ collection: '0xc0ffee', token_id: '2' }, true)).toBe(true);
  });
  it('ERC-1155 never (spec: hint instead)', () => {
    expect(batchEligible({ ...base, standard: 'erc1155' }, true)).toBe(false);
    expect(isErc1155({ standard: 'ERC1155' })).toBe(true);
    expect(HINT_1155).toBe('List multi-edition items one at a time');
  });
  it('already-listed and auction-escrowed items never', () => {
    expect(batchEligible({ ...base, price_wei: '1000000000000000000' }, true)).toBe(false);
    expect(batchEligible({ ...base, _escrowed: true }, true)).toBe(false);
  });
  it('nothing is selectable on someone else\'s profile', () => {
    expect(batchEligible(base, false)).toBe(false);
  });
  it('sticky bar label matches the spec', () => {
    expect(batchBarLabel(3)).toBe('List 3 selected — one price, one duration');
  });
});

describe('batch validation', () => {
  it('rejects an empty selection and more than 50', () => {
    expect(validateBatch(0, '2')).toEqual({ ok: false, error: 'Select at least one NFT.' });
    expect(validateBatch(51, '2')).toEqual({ ok: false, error: 'Pick at most 50.' });
  });
  it('rejects garbage and sub-minimum prices', () => {
    expect(validateBatch(2, 'abc').ok).toBe(false);
    expect(validateBatch(2, '')).toEqual({ ok: false, error: 'Enter a price like 12.5' });
    expect(validateBatch(2, '0.5', 'C2FLR')).toEqual({ ok: false, error: 'Minimum price is 1 C2FLR.' });
  });
  it('accepts a decimal price and returns wei', () => {
    const v = validateBatch(2, '12.5');
    expect(v.ok).toBe(true);
    if (v.ok) expect(v.wei).toBe(12_500_000_000_000_000_000n);
  });
});

describe('inventory merge', () => {
  it('dedupes by collection:token with wallet > listing > auction priority', () => {
    const nfts = [{ collection: '0xC1', token_id: '1', name: 'wallet' }];
    const listings = [{ collection: '0xc1', token_id: '1', name: 'listing' }, { collection: '0xc1', token_id: '2', name: 'listed2' }];
    const auctions = [{ collection: '0xc1', token_id: '2', name: 'auc2' }, { collection: '0xc1', token_id: '3', name: 'auc3' }];
    const out = mergeInventory(nfts, listings, auctions);
    expect(out.map((i) => i.name)).toEqual(['wallet', 'listed2', 'auc3']);
  });
  it('marks auction rows as escrowed (never batch-listable)', () => {
    const out = mergeInventory([], [], [{ collection: '0xc1', token_id: '3' }]);
    expect(out[0]._escrowed).toBe(true);
    expect(batchEligible(out[0], true)).toBe(false);
  });
  it('itemKey is case-insensitive on the collection', () => {
    expect(itemKey({ collection: '0xAB', token_id: '7' })).toBe(itemKey({ collection: '0xab', token_id: '7' }));
  });
});

describe('last-known-good snapshot (JSON, per address)', () => {
  it('round-trips through a storage', () => {
    const store: Record<string, string> = {};
    const storage = { getItem: (k: string) => store[k] ?? null, setItem: (k: string, v: string) => { store[k] = v; } };
    saveSnapshot(ADDR_A, { hello: 1 }, storage);
    expect(Object.keys(store)).toEqual([snapshotKey(ADDR_A)]);
    expect(snapshotKey(ADDR_A)).toBe(SNAP_PREFIX + ADDR_A);
    expect(loadSnapshot(ADDR_A, storage)).toEqual({ hello: 1 });
    expect(loadSnapshot(ADDR_B, storage)).toBeNull();
  });
  it('is address-scoped and survives corrupted JSON', () => {
    const store: Record<string, string> = { [snapshotKey(ADDR_A)]: '{not json' };
    const storage = { getItem: (k: string) => store[k] ?? null, setItem: () => {} };
    expect(loadSnapshot(ADDR_A, storage)).toBeNull();
  });
  it('swallows quota errors on save', () => {
    const storage = { setItem: () => { throw new Error('quota'); } };
    expect(() => saveSnapshot(ADDR_A, { x: 1 }, storage)).not.toThrow();
  });
});

describe('wallet-change guard (debounce + overlap)', () => {
  beforeEach(() => vi.useFakeTimers());
  afterEach(() => vi.useRealTimers());

  it('debounces rapid events into one onChange', () => {
    const spy = vi.fn();
    const g = createWalletGuard(spy, 500);
    g.notify(ADDR_A);
    g.notify(ADDR_B);
    g.notify(ADDR_A);
    vi.advanceTimersByTime(499);
    expect(spy).not.toHaveBeenCalled();
    vi.advanceTimersByTime(1);
    expect(spy).toHaveBeenCalledTimes(1);
    expect(spy).toHaveBeenCalledWith(ADDR_A);
  });

  it('skips a re-fire for the address already rendered', () => {
    const spy = vi.fn();
    const g = createWalletGuard(spy, 500);
    g.beginLoad(ADDR_A);
    g.endLoad();
    g.notify(ADDR_A);
    vi.advanceTimersByTime(500);
    expect(spy).not.toHaveBeenCalled();
  });

  it('defers a change that lands mid-load and hands it back at endLoad', () => {
    const spy = vi.fn();
    const g = createWalletGuard(spy, 500);
    expect(g.beginLoad(ADDR_A)).toBe(true);
    g.notify(ADDR_B);
    vi.advanceTimersByTime(500);
    expect(spy).not.toHaveBeenCalled(); // deferred, not dropped
    expect(g.endLoad()).toBe(ADDR_B);
    expect(g.lastAddr).toBe(ADDR_B);
  });

  it('blocks overlapping loads', () => {
    const g = createWalletGuard(() => {}, 500);
    expect(g.beginLoad(ADDR_A)).toBe(true);
    expect(g.beginLoad(ADDR_A)).toBe(false);
    g.endLoad();
    expect(g.beginLoad(ADDR_A)).toBe(true);
  });

  it('reset() lets the same address load again after a failure', () => {
    const spy = vi.fn();
    const g = createWalletGuard(spy, 500);
    g.beginLoad(ADDR_A);
    g.endLoad();
    g.reset();
    g.notify(ADDR_A);
    vi.advanceTimersByTime(500);
    expect(spy).toHaveBeenCalledWith(ADDR_A);
  });
});

describe('misc helpers', () => {
  it('initials come from the name or the address sans 0x', () => {
    expect(initialsFor('Magic Webb')).toBe('MA');
    expect(initialsFor(ADDR_A)).toBe('AA');
    expect(initialsFor('')).toBe('?');
  });
  it('installFirstTradeDone wires the shared window setter to the home-strip key', () => {
    installFirstTradeDone();
    expect(typeof window.mwSetFirstTradeDone).toBe('function');
    markFirstTradeDone();
    expect(localStorage.getItem(FIRST_TRADE_KEY)).toBe('1');
  });
});

// ── Component behaviour ────────────────────────────────────────────────────

describe('<ProfilePage>', () => {
  let host: HTMLDivElement;
  let app: ReturnType<typeof mount> | undefined;

  beforeEach(() => {
    host = document.createElement('div');
    document.body.appendChild(host);
    sessionStorage.clear();
  });
  afterEach(async () => {
    if (app) unmount(app);
    app = undefined;
    host.remove();
    vi.unstubAllGlobals();
    const w = window as unknown as Record<string, unknown>;
    delete w.MW;
    delete w.MW_MARKETPLACE;
    delete w.MW_AUCTION;
    delete w.MW_OFFERBOOK;
    (await import('../lib/chains'))._resetChainCache();
  });

  it('no wallet & no address → the exact connect empty state with an h1', async () => {
    vi.stubGlobal('fetch', fetchMock());
    app = mount(ProfilePage, { target: host });
    flushSync();
    await waitFor(() => expect(host.textContent).toContain('Connect your wallet to see your NFTs'));
    expect(host.querySelector('h1')?.textContent).toBe('Profile');
    expect(host.textContent).toContain('Looking for someone? Paste their address in Search');
    const search = Array.from(host.querySelectorAll('a')).find((a) => a.getAttribute('href') === '/search');
    expect(search).toBeTruthy();
  });

  it('own profile: "This is you" chip, "Your items" tab, balance, refunds card at 0', async () => {
    localStorage.setItem('mw_addr', ADDR_A);
    vi.stubGlobal('fetch', fetchMock({ profile: { display_name: 'Alice' } }));
    app = mount(ProfilePage, { target: host, props: { addr: ADDR_A } });
    flushSync();
    await waitFor(() => expect(host.querySelector('h1')?.textContent).toBe('Alice'));
    expect(host.textContent).toContain('This is you');
    const tabs = Array.from(host.querySelectorAll('[role="tab"]')).map((t) => t.textContent?.trim());
    expect(tabs).toEqual(['Your items', 'For sale', 'Auctions', 'Offers', 'Activity']);
    expect(host.textContent).toContain('Balance:');
    // Refunds card is ALWAYS visible on the own profile, even at zero.
    await waitFor(() => expect(host.textContent).toContain('Refunds:'));
    expect(host.textContent).toContain('Outbid or declined? Funds come back here, never lost.');
    // No withdraw button at zero.
    expect(Array.from(host.querySelectorAll('button')).some((b) => b.textContent?.trim() === 'Withdraw')).toBe(false);
  });

  it("someone else's profile: no chip, no balance, no batch tools, plain Items tab", async () => {
    localStorage.setItem('mw_addr', ADDR_A);
    vi.stubGlobal('fetch', fetchMock({
      profile: { display_name: 'Bob' },
      nfts: [{ collection: '0x3333333333333333333333333333333333333333', token_id: '1', name: 'Animi #1', standard: 'erc721' }],
    }));
    app = mount(ProfilePage, { target: host, props: { addr: ADDR_B } });
    flushSync();
    await waitFor(() => expect(host.querySelector('h1')?.textContent).toBe('Bob'));
    expect(host.textContent).not.toContain('This is you');
    expect(host.textContent).not.toContain('Balance:');
    expect(host.textContent).not.toContain('Refunds:');
    const tabs = Array.from(host.querySelectorAll('[role="tab"]')).map((t) => t.textContent?.trim());
    expect(tabs[0]).toBe('Items');
    // The 721 card renders but carries no batch checkbox on a foreign profile.
    await waitFor(() => expect(host.textContent).toContain('Animi #1'));
    expect(host.querySelector('input[type="checkbox"]')).toBeNull();
  });

  it('per-tab empty copy renders with its action', async () => {
    localStorage.setItem('mw_addr', ADDR_A);
    vi.stubGlobal('fetch', fetchMock());
    app = mount(ProfilePage, { target: host, props: { addr: ADDR_A } });
    flushSync();
    await waitFor(() => expect(host.textContent).toContain('No items yet'));
    (Array.from(host.querySelectorAll('[role="tab"]'))[1] as HTMLButtonElement).click();
    flushSync();
    expect(host.textContent).toContain('Nothing for sale');
    (Array.from(host.querySelectorAll('[role="tab"]'))[4] as HTMLButtonElement).click();
    flushSync();
    expect(host.textContent).toContain('No activity yet');
  });

  it('edit-profile modal: opens, Escape closes, save PUTs via MW.authFetch (SIWE path)', async () => {
    localStorage.setItem('mw_addr', ADDR_A);
    const putSpy = vi.fn();
    const mockFetch = fetchMock({ profile: { display_name: 'Alice' }, putSpy });
    vi.stubGlobal('fetch', mockFetch);
    const authFetch = vi.fn(async (url: string, init?: RequestInit) => mockFetch(url, init));
    (window as unknown as { MW: { address: () => string; authFetch: typeof authFetch } }).MW = {
      address: () => ADDR_A,
      authFetch,
    };
    app = mount(ProfilePage, { target: host, props: { addr: ADDR_A } });
    flushSync();
    await waitFor(() => expect(host.querySelector('h1')?.textContent).toBe('Alice'));

    const editBtn = Array.from(host.querySelectorAll('button')).find((b) => b.textContent?.trim() === 'Edit profile')!;
    expect(editBtn).toBeTruthy();
    editBtn.click();
    flushSync();
    const dialog = host.querySelector('[role="dialog"]')!;
    expect(dialog).toBeTruthy();
    expect(dialog.getAttribute('aria-modal')).toBe('true');

    // Escape closes without saving.
    dialog.closest('.pp-overlay')!.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }));
    flushSync();
    expect(host.querySelector('[role="dialog"]')).toBeNull();
    expect(authFetch).not.toHaveBeenCalled();

    // Reopen, edit, save → PUT through the SIWE-gated authFetch.
    editBtn.click();
    flushSync();
    const nameInput = host.querySelector<HTMLInputElement>('input[name="display_name"]')!;
    nameInput.value = 'Alice Prime';
    nameInput.dispatchEvent(new Event('input', { bubbles: true }));
    flushSync();
    host.querySelector('form')!.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }));
    await waitFor(() => expect(authFetch).toHaveBeenCalledTimes(1));
    const [url, init] = authFetch.mock.calls[0];
    expect(url).toBe(`/api/v1/profile/${ADDR_A}`);
    expect(init?.method).toBe('PUT');
    expect(JSON.parse(String(init?.body)).display_name).toBe('Alice Prime');
    await waitFor(() => expect(host.querySelector('[role="dialog"]')).toBeNull());
  });

  it('paints the sessionStorage snapshot instantly for a known address', async () => {
    localStorage.setItem('mw_addr', ADDR_A);
    saveSnapshot(ADDR_A, {
      profile: { display_name: 'Cached Alice' },
      nfts: [],
      pp: composite(),
    });
    // Fetches never resolve — only the snapshot can paint.
    vi.stubGlobal('fetch', vi.fn(() => new Promise(() => {})));
    app = mount(ProfilePage, { target: host, props: { addr: ADDR_A } });
    flushSync();
    await waitFor(() => expect(host.querySelector('h1')?.textContent).toBe('Cached Alice'));
    expect(host.textContent).toContain('Showing your last data while we refresh…');
  });

  it('1155 cards get the hint instead of a checkbox on the own profile', async () => {
    // Batch tools require tradingLive(): all three contract addresses set.
    const w = window as unknown as Record<string, string>;
    w.MW_MARKETPLACE = '0x1111111111111111111111111111111111111111';
    w.MW_AUCTION = '0x2222222222222222222222222222222222222222';
    w.MW_OFFERBOOK = '0x4444444444444444444444444444444444444444';
    const { _resetChainCache } = await import('../lib/chains');
    _resetChainCache();
    localStorage.setItem('mw_addr', ADDR_A);
    vi.stubGlobal('fetch', fetchMock({
      nfts: [
        { collection: '0x3333333333333333333333333333333333333333', token_id: '1', name: 'Single', standard: 'erc721' },
        { collection: '0x3333333333333333333333333333333333333333', token_id: '2', name: 'Multi', standard: 'erc1155' },
      ],
    }));
    app = mount(ProfilePage, { target: host, props: { addr: ADDR_A } });
    flushSync();
    await waitFor(() => expect(host.textContent).toContain('Single'));
    // One checkbox (the 721), one hint button (the 1155).
    expect(host.querySelectorAll('input[type="checkbox"]').length).toBe(1);
    const hintBtn = host.querySelector('.pp-hint1155 button');
    expect(hintBtn).toBeTruthy();
    // The sticky batch bar appears only once something is selected.
    expect(host.textContent).not.toContain('selected — one price, one duration');
    const cb = host.querySelector<HTMLInputElement>('input[type="checkbox"]')!;
    cb.checked = true;
    cb.dispatchEvent(new Event('change', { bubbles: true }));
    flushSync();
    await waitFor(() => expect(host.textContent).toContain('List 1 selected — one price, one duration'));
  });
});
