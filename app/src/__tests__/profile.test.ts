// ── Module declaration for ?raw imports ────────────────────────────────────
declare module '*?raw' {
  const content: string;
  export default content;
}

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';

// Import profile.astro as raw text so we can extract + evaluate
// the <script is:inline> block in a jsdom environment.
import profileAstroRaw from '../pages/profile.astro?raw';

// ── Extract the inline script from raw astro source ────────────────────────
function extractInlineScript(raw: string): string {
  // The profile.astro has: <script is:inline> ... (function(){ ... })(); ... </script>
  const match = raw.match(/<script is:inline>([\s\S]*?)<\/script>/);
  if (!match) throw new Error('Could not extract inline script from profile.astro');
  return match[1].trim();
}

const PROFILE_SCRIPT = extractInlineScript(profileAstroRaw);

// ── Test addresses ─────────────────────────────────────────────────────────
const ADDR_A = '0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa';
const ADDR_B = '0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb';

// ── Default fetch response factory ─────────────────────────────────────────
function okJson(body: any) {
  return Promise.resolve({
    ok: true,
    json: () => Promise.resolve(body),
  } as Response);
}

function emptyProfileData() {
  return {
    profile: okJson({ display_name: 'Test User', bio: '', avatar_uri: '', verified: false }),
    nfts: okJson([]),
    listings: okJson([]),
    auctions: okJson([]),
    offersSent: okJson([]),
    offersRecv: okJson([]),
    metrics: okJson({ totalActiveListings: 0, totalSales: 0, totalAuctions: 0, grossVolumeWei: '0' }),
    activity: okJson([]),
  };
}

// ── Track event listeners so we can clean them up between tests ────────────
// Each setupProfilePage() evaluates the profile IIFE which registers a
// new 'mw-wallet-changed' listener. Without cleanup, listeners accumulate
// and their shared globals (_lastRenderedAddr, _loading, etc.) interfere.
const _registeredListeners: Array<{ type: string; fn: EventListener }> = [];
const _originalAddEventListener = window.addEventListener.bind(window);
const _originalRemoveEventListener = window.removeEventListener.bind(window);

function _setupListenerTracking() {
  window.addEventListener = function(type: string, fn: EventListener, opts?: any) {
    _registeredListeners.push({ type, fn });
    return _originalAddEventListener(type, fn, opts);
  } as typeof window.addEventListener;
  window.removeEventListener = _originalRemoveEventListener;
}

function _cleanupListeners() {
  // Remove all tracked listeners
  for (const { type, fn } of _registeredListeners) {
    _originalRemoveEventListener(type, fn);
  }
  _registeredListeners.length = 0;
  // Restore original addEventListener
  window.addEventListener = _originalAddEventListener;
}

// ── Helper: set up the DOM and mocks, then evaluate the profile script ─────
function setupProfilePage(opts: {
  pathname?: string;
  localStorageAddr?: string | null;
  localStorageJwt?: string | null;
} = {}) {
  // 1. Clean up listeners from previous test runs
  _cleanupListeners();
  _setupListenerTracking();

  // 2. Create root element (profile script reads #profile-root)
  document.body.innerHTML = '<div id="profile-root"></div>';

  // 3. Mock location.pathname
  Object.defineProperty(window, 'location', {
    value: {
      pathname: opts.pathname ?? '/profile',
      origin: 'http://localhost:4321',
    },
    writable: true,
    configurable: true,
  });

  // 4. Clear global state from previous evaluations (IIFEs share global scope)
  (window as any)._lastRenderedAddr = undefined;
  (window as any)._wcDebounceTimer = null;
  (window as any)._loading = false;

  // 5. Seed localStorage
  if (opts.localStorageAddr !== undefined) {
    if (opts.localStorageAddr) localStorage.setItem('mw_addr', opts.localStorageAddr);
    else localStorage.removeItem('mw_addr');
  }
  if (opts.localStorageJwt !== undefined) {
    if (opts.localStorageJwt) localStorage.setItem('mw_jwt', opts.localStorageJwt);
    else localStorage.removeItem('mw_jwt');
  }

  // 6. Evaluate the profile script (it's an IIFE)
  const fn = new Function(PROFILE_SCRIPT);
  fn();
}

// ── Helper: advance fake timers past the 500ms debounce ────────────────────
async function flushDebounce(ms = 600) {
  vi.advanceTimersByTime(ms);
  // Multiple passes: the first runAllTimersAsync flushes microtasks from
  // the debounce callback's loadProfileData call, which may schedule more
  // microtasks (e.g. nested Promise.all resolutions).
  await vi.runAllTimersAsync();
  await vi.runAllTimersAsync();
}

// ═══════════════════════════════════════════════════════════════════════════════
// Suite 1: Address Resolution (URL path vs localStorage)
// ═══════════════════════════════════════════════════════════════════════════════

describe('Profile address resolution', () => {
  beforeEach(() => {
    vi.useFakeTimers();
    vi.spyOn(window, 'fetch').mockImplementation((_url: any) => {
      const url = String(_url);
      if (url.includes('/api/v1/profile/')) return okJson({ display_name: '', bio: '', avatar_uri: '', verified: false });
      if (url.includes('/api/v1/wallet/')) return okJson([]);
      if (url.includes('/api/v1/listings')) return okJson([]);
      if (url.includes('/api/v1/auctions')) return okJson([]);
      if (url.includes('/api/v1/offers')) return okJson([]);
      if (url.includes('/api/v1/metrics')) return okJson({});
      if (url.includes('/api/v1/activity')) return okJson([]);
      return Promise.reject(new Error('unexpected URL: ' + url));
    });
  });

  afterEach(() => {
    vi.restoreAllMocks();
    vi.useRealTimers();
    document.body.innerHTML = '';
    localStorage.clear();
  });

  it('uses address from URL path when /profile/0x... is visited', async () => {
    setupProfilePage({ pathname: '/profile/' + ADDR_A, localStorageAddr: null });
    await flushDebounce();

    const fetchCalls = (window.fetch as any).mock.calls;
    const profileCall = fetchCalls.find(([url]: [string]) => url.includes('/api/v1/profile/'));
    expect(profileCall[0]).toContain(ADDR_A.toLowerCase());
  });

  it('falls back to localStorage when URL path is just /profile', async () => {
    setupProfilePage({ pathname: '/profile', localStorageAddr: ADDR_A });
    await flushDebounce();

    const fetchCalls = (window.fetch as any).mock.calls;
    const profileCall = fetchCalls.find(([url]: [string]) => url.includes('/api/v1/profile/'));
    expect(profileCall[0]).toContain(ADDR_A.toLowerCase());
  });

  it('shows connect screen when no address in URL or localStorage', () => {
    setupProfilePage({ pathname: '/profile', localStorageAddr: null });

    const root = document.getElementById('profile-root')!;
    expect(root.innerHTML).toContain('Connect your wallet');
  });

  it('uses URL path address when wallet is connected as a different address', async () => {
    // Wallet connected as ADDR_A, but visiting /profile/ADDR_B.
    // The URL path should take priority over localStorage.
    localStorage.setItem('mw_addr', ADDR_A);

    setupProfilePage({ pathname: '/profile/' + ADDR_B });
    await flushDebounce();

    // Verify the profile API was called with ADDR_B (from URL), not ADDR_A
    const fetchCalls = (window.fetch as any).mock.calls;
    const profileCall = fetchCalls.find(([url]: [string]) => String(url).includes('/api/v1/profile/'));
    expect(profileCall[0]).toContain(ADDR_B.toLowerCase());
    expect(profileCall[0]).not.toContain(ADDR_A.toLowerCase());

    // Edit button should NOT be visible (viewing someone else's profile)
    const editBtn = document.getElementById('edit-profile-btn');
    expect(editBtn).toBeNull();
  });
});

// ═══════════════════════════════════════════════════════════════════════════════
// Suite 2: _lastRenderedAddr guard — prevents no-op re-renders
// ═══════════════════════════════════════════════════════════════════════════════

describe('_lastRenderedAddr guard', () => {
  beforeEach(() => {
    vi.useFakeTimers();
    // Seed wallet address so profile loads on boot
    localStorage.setItem('mw_addr', ADDR_A);

    let profileCallCount = 0;
    vi.spyOn(window, 'fetch').mockImplementation((_url: any) => {
      const url = String(_url);
      if (url.includes('/api/v1/profile/')) {
        profileCallCount++;
        return okJson({ display_name: 'User' + profileCallCount, bio: '', avatar_uri: '', verified: false });
      }
      if (url.includes('/api/v1/wallet/')) return okJson([]);
      if (url.includes('/api/v1/listings')) return okJson([]);
      if (url.includes('/api/v1/auctions')) return okJson([]);
      if (url.includes('/api/v1/offers')) return okJson([]);
      if (url.includes('/api/v1/metrics')) return okJson({});
      if (url.includes('/api/v1/activity')) return okJson([]);
      return Promise.reject(new Error('unexpected URL'));
    });
  });

  afterEach(() => {
    vi.restoreAllMocks();
    vi.useRealTimers();
    document.body.innerHTML = '';
    localStorage.clear();
  });

  it('does NOT re-render when mw-wallet-changed fires with same address', async () => {
    setupProfilePage({ pathname: '/profile' });

    // Wait for initial load to complete
    await flushDebounce();

    // Count profile API calls after initial load
    const afterInitCount = (window.fetch as any).mock.calls.filter(
      ([url]: [string]) => String(url).includes('/api/v1/profile/')
    ).length;

    // Dispatch wallet-changed with the SAME address
    window.dispatchEvent(new CustomEvent('mw-wallet-changed'));

    // Advance past the 500ms debounce
    await flushDebounce();

    // No additional profile fetches should have happened (guard skipped re-render)
    const afterEventCount = (window.fetch as any).mock.calls.filter(
      ([url]: [string]) => String(url).includes('/api/v1/profile/')
    ).length;
    expect(afterEventCount).toBe(afterInitCount);
  });

  it('DOES re-render when mw-wallet-changed fires with a DIFFERENT address', async () => {
    setupProfilePage({ pathname: '/profile' });

    // Wait for initial load
    await flushDebounce();

    const afterInitCount = (window.fetch as any).mock.calls.filter(
      ([url]: [string]) => String(url).includes('/api/v1/profile/')
    ).length;

    // Change localStorage to a new address, then fire event
    localStorage.setItem('mw_addr', ADDR_B);
    window.dispatchEvent(new CustomEvent('mw-wallet-changed'));

    await flushDebounce();

    // Additional profile fetch should have happened
    const afterEventCount = (window.fetch as any).mock.calls.filter(
      ([url]: [string]) => String(url).includes('/api/v1/profile/')
    ).length;
    expect(afterEventCount).toBe(afterInitCount + 1);

    // Verify the new address is in the rendered DOM
    const rootAfterEvent = document.getElementById('profile-root')!.innerHTML;
    expect(rootAfterEvent).toContain(ADDR_B.toLowerCase());
  });

  it('shows connect screen when wallet disconnects (mw_addr cleared)', async () => {
    setupProfilePage({ pathname: '/profile' });

    await flushDebounce();

    // Simulate disconnect: clear localStorage and fire event
    localStorage.removeItem('mw_addr');
    window.dispatchEvent(new CustomEvent('mw-wallet-changed'));

    await flushDebounce();

    const root = document.getElementById('profile-root')!;
    expect(root.innerHTML).toContain('Connect your wallet');
  });

  it('re-renders after disconnect then reconnect (different address cycle)', async () => {
    setupProfilePage({ pathname: '/profile' });

    await flushDebounce();

    // Step 1: disconnect
    localStorage.removeItem('mw_addr');
    window.dispatchEvent(new CustomEvent('mw-wallet-changed'));
    await flushDebounce();
    expect(document.getElementById('profile-root')!.innerHTML).toContain('Connect your wallet');

    // Step 2: reconnect with different address
    localStorage.setItem('mw_addr', ADDR_B);
    window.dispatchEvent(new CustomEvent('mw-wallet-changed'));
    await flushDebounce();
    expect(document.getElementById('profile-root')!.innerHTML).toContain(ADDR_B.toLowerCase());
  });
});

// ═══════════════════════════════════════════════════════════════════════════════
// Suite 3: _loading flag — prevents overlapping loadProfileData calls
// ═══════════════════════════════════════════════════════════════════════════════

describe('_loading flag — prevents overlapping calls', () => {
  let resolveFirstFetch: () => void;

  beforeEach(() => {
    vi.useFakeTimers();
    localStorage.setItem('mw_addr', ADDR_A);

    // Set up a fetch that hangs until we manually resolve it
    vi.spyOn(window, 'fetch').mockImplementation((_url: any) => {
      const url = String(_url);
      if (url.includes('/api/v1/profile/')) {
        // Return a promise that blocks until we release it
        return new Promise<Response>((resolve) => {
          resolveFirstFetch = () => resolve({
            ok: true,
            json: () => Promise.resolve({ display_name: 'User', bio: '', avatar_uri: '', verified: false }),
          } as Response);
        });
      }
      return okJson([]);
    });
  });

  afterEach(() => {
    vi.restoreAllMocks();
    vi.useRealTimers();
    document.body.innerHTML = '';
    localStorage.clear();
  });

  it('blocks a second loadProfileData call while one is in-flight', async () => {
    setupProfilePage({ pathname: '/profile' });

    // The initial load is now hanging (waiting for resolveFirstFetch)
    // The root should show "Loading profile…"
    const root = document.getElementById('profile-root')!;
    expect(root.innerHTML).toContain('Loading profile');

    // Now dispatch wallet-changed which would trigger another loadProfileData call
    localStorage.setItem('mw_addr', ADDR_B);
    window.dispatchEvent(new CustomEvent('mw-wallet-changed'));

    // Advance past debounce — but the second call should be blocked by _loading=true
    await flushDebounce();

    // The loading indicator should still be the original one — NOT re-entered
    expect(root.innerHTML).toContain('Loading profile');

    // Now resolve the first fetch
    resolveFirstFetch();
    await vi.runAllTimersAsync();

    // The profile should now be rendered (first call completed)
    // The second call was blocked, so the rendered address is still ADDR_A
    expect(root.innerHTML).toContain(ADDR_A.toLowerCase());
  });

  it('_loading is reset after successful load, allowing subsequent calls', async () => {
    // Use a fetch that resolves immediately but we still use fake timers
    vi.restoreAllMocks();
    vi.spyOn(window, 'fetch').mockImplementation((_url: any) => {
      const url = String(_url);
      if (url.includes('/api/v1/profile/')) {
        return okJson({ display_name: 'User', bio: '', avatar_uri: '', verified: false });
      }
      return okJson([]);
    });

    setupProfilePage({ pathname: '/profile' });
    await flushDebounce();

    // First load completed. Now dispatch another wallet change.
    localStorage.setItem('mw_addr', ADDR_B);
    window.dispatchEvent(new CustomEvent('mw-wallet-changed'));
    await flushDebounce();

    // The second load should have happened — rendered address should be ADDR_B
    const root = document.getElementById('profile-root')!;
    expect(root.innerHTML).toContain(ADDR_B.toLowerCase());
  });
});

// ═══════════════════════════════════════════════════════════════════════════════
// Suite 4: _lastRenderedAddr reset on API failure
// ═══════════════════════════════════════════════════════════════════════════════

describe('_lastRenderedAddr reset on error', () => {
  beforeEach(() => {
    vi.useFakeTimers();
    localStorage.setItem('mw_addr', ADDR_A);
  });

  afterEach(() => {
    vi.restoreAllMocks();
    vi.useRealTimers();
    document.body.innerHTML = '';
    localStorage.clear();
  });

  it('resets _lastRenderedAddr so retry works after a transient failure', async () => {
    // The profile.astro code wraps each fetch in .catch(function(){return null;}),
    // so Promise.reject won't reach the outer catch block. We must throw
    // synchronously from the mock to bypass the individual catch handlers
    // and trigger the outer try/catch.
    let callCount = 0;
    vi.spyOn(window, 'fetch').mockImplementation((_url: any) => {
      const url = String(_url);
      if (url.includes('/api/v1/profile/')) {
        callCount++;
        if (callCount === 1) {
          // Synchronous throw — bypasses the .catch() on the fetch promise
          throw new Error('Network error');
        }
        // Second call: succeed
        return okJson({ display_name: 'User', bio: '', avatar_uri: '', verified: false });
      }
      return okJson([]);
    });

    setupProfilePage({ pathname: '/profile' });
    await flushDebounce();

    // After first failure, error screen should show
    const root = document.getElementById('profile-root')!;
    expect(root.innerHTML).toContain('Could not load profile');

    // Now dispatch wallet-changed AGAIN with the same address.
    // The catch block reset _lastRenderedAddr to null, so the guard
    // won't skip this retry.
    window.dispatchEvent(new CustomEvent('mw-wallet-changed'));
    await flushDebounce();

    // Second attempt should succeed — profile should render
    expect(root.innerHTML).not.toContain('Could not load profile');
    expect(root.innerHTML).toContain('User');
  });
});

// ═══════════════════════════════════════════════════════════════════════════════
// Suite 5: Debounce — rapid events coalesce
// ═══════════════════════════════════════════════════════════════════════════════

describe('Debounce behavior', () => {
  beforeEach(() => {
    vi.useFakeTimers();
    localStorage.setItem('mw_addr', ADDR_A);
    vi.spyOn(window, 'fetch').mockImplementation((_url: any) => {
      const url = String(_url);
      if (url.includes('/api/v1/profile/')) return okJson({ display_name: 'User', bio: '', avatar_uri: '', verified: false });
      if (url.includes('/api/v1/wallet/')) return okJson([]);
      if (url.includes('/api/v1/listings')) return okJson([]);
      if (url.includes('/api/v1/auctions')) return okJson([]);
      if (url.includes('/api/v1/offers')) return okJson([]);
      if (url.includes('/api/v1/metrics')) return okJson({});
      if (url.includes('/api/v1/activity')) return okJson([]);
      return Promise.reject(new Error('unexpected URL'));
    });
  });

  afterEach(() => {
    vi.restoreAllMocks();
    vi.useRealTimers();
    document.body.innerHTML = '';
    localStorage.clear();
  });

  it('coalesces multiple rapid mw-wallet-changed events into a single render', async () => {
    setupProfilePage({ pathname: '/profile' });
    await flushDebounce();

    // Count how many times the profile endpoint is called after the initial load
    const initialCallCount = (window.fetch as any).mock.calls.filter(
      ([url]: [string]) => String(url).includes('/api/v1/profile/')
    ).length;

    // Fire THREE rapid wallet-changed events
    localStorage.setItem('mw_addr', ADDR_B);
    window.dispatchEvent(new CustomEvent('mw-wallet-changed'));

    localStorage.setItem('mw_addr', ADDR_A); // back to A
    window.dispatchEvent(new CustomEvent('mw-wallet-changed'));

    localStorage.setItem('mw_addr', ADDR_B); // back to B
    window.dispatchEvent(new CustomEvent('mw-wallet-changed'));

    // Only advance 100ms — NOT past the 500ms debounce
    vi.advanceTimersByTime(100);

    // No additional fetches should have fired yet
    const midCallCount = (window.fetch as any).mock.calls.filter(
      ([url]: [string]) => String(url).includes('/api/v1/profile/')
    ).length;
    expect(midCallCount).toBe(initialCallCount);

    // Now advance past the debounce
    await flushDebounce();

    // Only ONE additional profile fetch should have happened
    const finalCallCount = (window.fetch as any).mock.calls.filter(
      ([url]: [string]) => String(url).includes('/api/v1/profile/')
    ).length;
    expect(finalCallCount).toBe(initialCallCount + 1);
  });

  it('honors the 500ms debounce — fires after the delay, not before', async () => {
    setupProfilePage({ pathname: '/profile' });
    await flushDebounce();

    const initialCount = (window.fetch as any).mock.calls.filter(
      ([url]: [string]) => String(url).includes('/api/v1/profile/')
    ).length;

    // Fire event at t=0 (relative to current fake time)
    localStorage.setItem('mw_addr', ADDR_B);
    window.dispatchEvent(new CustomEvent('mw-wallet-changed'));

    // Advance 400ms — NOT past 500ms debounce.
    // IMPORTANT: use vi.runAllTicks() (microtasks only) instead of
    // vi.runAllTimersAsync() which would run ALL pending timers including
    // the future 500ms debounce, causing a false-positive fetch.
    vi.advanceTimersByTime(400);
    await vi.runAllTicks();

    let count = (window.fetch as any).mock.calls.filter(
      ([url]: [string]) => String(url).includes('/api/v1/profile/')
    ).length;
    expect(count).toBe(initialCount); // Not yet fired

    // Advance past the remaining 100ms+ to cross the 500ms threshold
    vi.advanceTimersByTime(200);
    await vi.runAllTimersAsync();

    count = (window.fetch as any).mock.calls.filter(
      ([url]: [string]) => String(url).includes('/api/v1/profile/')
    ).length;
    expect(count).toBe(initialCount + 1); // Now fired
  });
});

// ═══════════════════════════════════════════════════════════════════════════════
// Suite 6: isEthAddr validation
// ═══════════════════════════════════════════════════════════════════════════════

describe('isEthAddr validation', () => {
  beforeEach(() => {
    vi.useFakeTimers();
    vi.spyOn(window, 'fetch').mockImplementation((_url: any) => {
      const url = String(_url);
      if (url.includes('/api/v1/profile/')) return okJson({ display_name: '', bio: '', avatar_uri: '', verified: false });
      return okJson([]);
    });
  });

  afterEach(() => {
    vi.restoreAllMocks();
    vi.useRealTimers();
    document.body.innerHTML = '';
    localStorage.clear();
  });

  it('rejects non-eth-address strings from localStorage', async () => {
    // Invalid address — should not be used
    setupProfilePage({ pathname: '/profile', localStorageAddr: 'not-an-address' });
    await flushDebounce();

    const root = document.getElementById('profile-root')!;
    // Should show connect screen because the address is invalid
    expect(root.innerHTML).toContain('Connect your wallet');
  });

  it('rejects address strings that are too short', async () => {
    setupProfilePage({ pathname: '/profile', localStorageAddr: '0xabc' });
    await flushDebounce();

    const root = document.getElementById('profile-root')!;
    expect(root.innerHTML).toContain('Connect your wallet');
  });

  it('accepts a valid 0x-prefixed 42-char address', async () => {
    setupProfilePage({ pathname: '/profile', localStorageAddr: ADDR_A });
    await flushDebounce();

    const fetchCalls = (window.fetch as any).mock.calls;
    const profileCall = fetchCalls.find(([url]: [string]) => String(url).includes('/api/v1/profile/'));
    expect(profileCall[0]).toContain(ADDR_A.toLowerCase());
  });
});

// ═══════════════════════════════════════════════════════════════════════════════
// Suite 7: emptyWalletNotice
// ═══════════════════════════════════════════════════════════════════════════════

describe('emptyWalletNotice', () => {
  beforeEach(() => {
    vi.useFakeTimers();
    localStorage.setItem('mw_addr', ADDR_A);
  });

  afterEach(() => {
    vi.restoreAllMocks();
    vi.useRealTimers();
    document.body.innerHTML = '';
    localStorage.clear();
  });

  it('shows notice when NFTs are empty but listings/auctions exist', async () => {
    vi.spyOn(window, 'fetch').mockImplementation((_url: any) => {
      const url = String(_url);
      if (url.includes('/api/v1/profile/')) return okJson({ display_name: 'Seller', bio: '', avatar_uri: '', verified: false });
      if (url.includes('/api/v1/wallet/')) return okJson([]);
      // Has listings but no NFTs
      if (url.includes('/api/v1/listings')) return okJson([{ collection: ADDR_A, token_id: '1', price_wei: '1000000000000000000' }]);
      if (url.includes('/api/v1/auctions')) return okJson([]);
      if (url.includes('/api/v1/offers')) return okJson([]);
      if (url.includes('/api/v1/metrics')) return okJson({});
      if (url.includes('/api/v1/activity')) return okJson([]);
      return Promise.reject(new Error('unexpected URL'));
    });

    setupProfilePage({ pathname: '/profile' });
    await flushDebounce();

    const root = document.getElementById('profile-root')!;
    expect(root.innerHTML).toContain('indexer hasn');
  });

  it('does NOT show notice when NFTs are non-empty', async () => {
    vi.spyOn(window, 'fetch').mockImplementation((_url: any) => {
      const url = String(_url);
      if (url.includes('/api/v1/profile/')) return okJson({ display_name: 'Holder', bio: '', avatar_uri: '', verified: false });
      if (url.includes('/api/v1/wallet/')) return okJson([
        { collection: ADDR_A, token_id: '1', name: 'Test NFT', image_uri: '/api/v1/img/abc123', units: '1', standard: 'erc721' }
      ]);
      if (url.includes('/api/v1/listings')) return okJson([]);
      if (url.includes('/api/v1/auctions')) return okJson([]);
      if (url.includes('/api/v1/offers')) return okJson([]);
      if (url.includes('/api/v1/metrics')) return okJson({});
      if (url.includes('/api/v1/activity')) return okJson([]);
      return Promise.reject(new Error('unexpected URL'));
    });

    setupProfilePage({ pathname: '/profile' });
    await flushDebounce();

    const root = document.getElementById('profile-root')!;
    expect(root.innerHTML).not.toContain('indexer hasn');
    expect(root.innerHTML).toContain('Test NFT');
  });

  it('does NOT show notice when both NFTs and listings are empty', async () => {
    vi.spyOn(window, 'fetch').mockImplementation((_url: any) => {
      const url = String(_url);
      if (url.includes('/api/v1/profile/')) return okJson({ display_name: 'NewUser', bio: '', avatar_uri: '', verified: false });
      if (url.includes('/api/v1/wallet/')) return okJson([]);
      if (url.includes('/api/v1/listings')) return okJson([]);
      if (url.includes('/api/v1/auctions')) return okJson([]);
      if (url.includes('/api/v1/offers')) return okJson([]);
      if (url.includes('/api/v1/metrics')) return okJson({});
      if (url.includes('/api/v1/activity')) return okJson([]);
      return Promise.reject(new Error('unexpected URL'));
    });

    setupProfilePage({ pathname: '/profile' });
    await flushDebounce();

    const root = document.getElementById('profile-root')!;
    expect(root.innerHTML).not.toContain('indexer hasn');
  });
});

// ═══════════════════════════════════════════════════════════════════════════════
// Suite 8: Edit Profile Modal — open, close, form, save, error handling
// ═══════════════════════════════════════════════════════════════════════════════

describe('Edit Profile Modal', () => {
  // Shared profile data used for pre-filling form fields
  const profileData = {
    display_name: 'Alice',
    bio: 'NFT collector & builder',
    avatar_uri: 'https://example.com/alice.png',
    twitter: '@alice_nft',
    website: 'https://alice.xyz',
    verified: false,
  };

  // ── Shared helpers for PUT behavior overrides ────────────────────────
  type PutBehavior =
    | { type: 'success' }
    | { type: 'serverError'; status: number; error: string }
    | { type: 'networkError'; message: string };

  interface LoadOwnProfileOpts {
    jwt?: string;
    putBehavior?: PutBehavior;
    /** Called each time the profile GET endpoint is hit (for tracking call count) */
    onProfileGet?: () => void;
    /** If set, pathname is /profile/{viewingAddr} instead of /profile */
    viewingAddr?: string;
    /** Override the localStorage mw_addr (defaults to ADDR_A) */
    ownAddr?: string;
  }

  // Helper: set up a fully-rendered own profile with edit button visible.
  // Accepts PUT behavior overrides so all Suite 8 tests share a single
  // fetch mock instead of duplicating it.
  async function loadOwnProfileWithPut(opts: LoadOwnProfileOpts = {}) {
    const addr = opts.ownAddr ?? ADDR_A;
    localStorage.setItem('mw_addr', addr);
    if (opts.jwt) {
      localStorage.setItem('mw_jwt', opts.jwt);
    } else {
      localStorage.removeItem('mw_jwt');
    }

    const putCalls: Array<{ url: string; body: any }> = [];
    const putBehavior = opts.putBehavior ?? { type: 'success' as const };

    vi.spyOn(window, 'fetch').mockImplementation((_url: any, fetchOpts?: any) => {
      const url = String(_url);
      const method = (fetchOpts && fetchOpts.method) || 'GET';

      // PUT to profile endpoint — honors putBehavior override
      if (url.includes('/api/v1/profile/') && method === 'PUT') {
        putCalls.push({ url, body: fetchOpts ? JSON.parse(fetchOpts.body) : {} });
        if (putBehavior.type === 'networkError') {
          return Promise.reject(new Error(putBehavior.message));
        }
        if (putBehavior.type === 'serverError') {
          return Promise.resolve({
            ok: false,
            status: putBehavior.status,
            json: () => Promise.resolve({ error: putBehavior.error }),
          } as Response);
        }
        return okJson({ ok: true });
      }

      // GET profile
      if (url.includes('/api/v1/profile/') && !url.includes('metrics')) {
        if (opts.onProfileGet) opts.onProfileGet();
        return okJson(profileData);
      }

      // All other endpoints: return empty
      if (url.includes('/api/v1/wallet/')) return okJson([]);
      if (url.includes('/api/v1/listings')) return okJson([]);
      if (url.includes('/api/v1/auctions')) return okJson([]);
      if (url.includes('/api/v1/offers')) return okJson([]);
      if (url.includes('/api/v1/metrics')) return okJson({});
      if (url.includes('/api/v1/activity')) return okJson([]);
      return Promise.reject(new Error('unexpected URL: ' + url));
    });

    const pathname = opts.viewingAddr ? `/profile/${opts.viewingAddr}` : '/profile';
    setupProfilePage({ pathname });
    await flushDebounce();

    return { putCalls };
  }

  beforeEach(() => {
    vi.useFakeTimers();
  });

  afterEach(() => {
    vi.restoreAllMocks();
    vi.useRealTimers();
    document.body.innerHTML = '';
    localStorage.clear();
  });

  // ── Open / Close ──────────────────────────────────────────────────────────

  it('opens the modal when Edit Profile button is clicked', async () => {
    await loadOwnProfileWithPut({ jwt: 'test-jwt' });

    const editBtn = document.getElementById('edit-profile-btn')!;
    expect(editBtn).not.toBeNull();

    editBtn.click();

    // Overlay should now be in the DOM
    const overlay = document.getElementById('edit-profile-overlay');
    expect(overlay).not.toBeNull();
    expect(overlay!.querySelector('#edit-profile-form')).not.toBeNull();
  });

  it('edit button is NOT rendered when viewing someone else\'s profile', async () => {
    // Wallet is connected as ADDR_A but profile URL is /profile/ADDR_B
    await loadOwnProfileWithPut({ ownAddr: ADDR_A, viewingAddr: ADDR_B });

    const editBtn = document.getElementById('edit-profile-btn');
    expect(editBtn).toBeNull();
  });

  it('closes modal when X close button is clicked', async () => {
    await loadOwnProfileWithPut({ jwt: 'test-jwt' });

    document.getElementById('edit-profile-btn')!.click();
    expect(document.getElementById('edit-profile-overlay')).not.toBeNull();

    document.getElementById('edit-profile-close')!.click();

    // Overlay should be removed from DOM
    expect(document.getElementById('edit-profile-overlay')).toBeNull();
  });

  it('closes modal when Cancel button is clicked', async () => {
    await loadOwnProfileWithPut({ jwt: 'test-jwt' });

    document.getElementById('edit-profile-btn')!.click();
    expect(document.getElementById('edit-profile-overlay')).not.toBeNull();

    document.getElementById('edit-profile-cancel')!.click();

    expect(document.getElementById('edit-profile-overlay')).toBeNull();
  });

  it('closes modal when overlay background is clicked', async () => {
    await loadOwnProfileWithPut({ jwt: 'test-jwt' });

    document.getElementById('edit-profile-btn')!.click();
    const overlay = document.getElementById('edit-profile-overlay')!;
    expect(overlay).not.toBeNull();

    // Click the overlay itself (not the modal child)
    overlay.dispatchEvent(new MouseEvent('click', { bubbles: true }));

    expect(document.getElementById('edit-profile-overlay')).toBeNull();
  });

  it('closes modal when Escape key is pressed', async () => {
    await loadOwnProfileWithPut({ jwt: 'test-jwt' });

    document.getElementById('edit-profile-btn')!.click();
    expect(document.getElementById('edit-profile-overlay')).not.toBeNull();

    document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }));

    expect(document.getElementById('edit-profile-overlay')).toBeNull();
  });

  // ── Form Pre-fill ────────────────────────────────────────────────────────

  it('pre-fills form fields with current profile data', async () => {
    await loadOwnProfileWithPut({ jwt: 'test-jwt' });

    document.getElementById('edit-profile-btn')!.click();

    const nameInput = document.querySelector('input[name="display_name"]') as HTMLInputElement;
    const bioInput = document.querySelector('textarea[name="bio"]') as HTMLTextAreaElement;
    const avatarInput = document.querySelector('input[name="avatar_uri"]') as HTMLInputElement;
    const twitterInput = document.querySelector('input[name="twitter"]') as HTMLInputElement;
    const websiteInput = document.querySelector('input[name="website"]') as HTMLInputElement;

    expect(nameInput).not.toBeNull();
    expect(nameInput.value).toBe(profileData.display_name);
    expect(bioInput.value).toBe(profileData.bio);
    expect(avatarInput.value).toBe(profileData.avatar_uri);
    expect(twitterInput.value).toBe(profileData.twitter);
    expect(websiteInput.value).toBe(profileData.website);
  });

  // ── JWT Requirement ──────────────────────────────────────────────────────

  it('shows error when submitting without JWT', async () => {
    // No JWT set
    await loadOwnProfileWithPut();

    document.getElementById('edit-profile-btn')!.click();

    // Submit the form (JWT check is synchronous, no await needed)
    const form = document.getElementById('edit-profile-form') as HTMLFormElement;
    form.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }));

    // Should show JWT error immediately (no async fetch involved)
    const statusEl = document.getElementById('edit-profile-status')!;
    expect(statusEl.style.display).toBe('block');
    expect(statusEl.innerHTML).toContain('reconnect your wallet');
  });

  // ── Successful Save ──────────────────────────────────────────────────────

  it('saves successfully and shows success message', async () => {
    const { putCalls } = await loadOwnProfileWithPut({ jwt: 'test-jwt' });

    document.getElementById('edit-profile-btn')!.click();

    // Fill in new values
    const nameInput = document.querySelector('input[name="display_name"]') as HTMLInputElement;
    nameInput.value = 'Bob Updated';
    // Trigger input event so FormData picks up the new value
    nameInput.dispatchEvent(new Event('input', { bubbles: true }));

    const bioInput = document.querySelector('textarea[name="bio"]') as HTMLTextAreaElement;
    bioInput.value = 'Updated bio';
    bioInput.dispatchEvent(new Event('input', { bubbles: true }));

    // Submit the form
    const form = document.getElementById('edit-profile-form') as HTMLFormElement;
    form.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }));

    // Flush microtasks only (not timers!) so the async fetch + response
    // pipeline completes without firing the 800ms closeModal setTimeout.
    await vi.runAllTicks();

    // Verify PUT was called with the correct body
    expect(putCalls.length).toBe(1);
    expect(putCalls[0].body.display_name).toBe('Bob Updated');
    expect(putCalls[0].body.bio).toBe('Updated bio');
    expect(putCalls[0].body.avatar_uri).toBe(profileData.avatar_uri);

    // Save button should be disabled during save
    const saveBtn = document.getElementById('edit-profile-save') as HTMLButtonElement;
    expect(saveBtn.disabled).toBe(true);
    expect(saveBtn.textContent).toBe('Saving…');

    // Success message should appear
    const statusEl = document.getElementById('edit-profile-status')!;
    expect(statusEl.style.display).toBe('block');
    expect(statusEl.innerHTML).toContain('Profile saved');
  });

  // ── Form Field Trimming ─────────────────────────────────────────────────

  it('trims leading/trailing whitespace from form fields in PUT payload', async () => {
    const { putCalls } = await loadOwnProfileWithPut({ jwt: 'test-jwt' });

    document.getElementById('edit-profile-btn')!.click();

    // Fill in values with leading/trailing whitespace
    const nameInput = document.querySelector('input[name="display_name"]') as HTMLInputElement;
    nameInput.value = '  Bob Updated  ';
    nameInput.dispatchEvent(new Event('input', { bubbles: true }));

    const bioInput = document.querySelector('textarea[name="bio"]') as HTMLTextAreaElement;
    bioInput.value = '\t  Updated bio\n\n  ';
    bioInput.dispatchEvent(new Event('input', { bubbles: true }));

    const twitterInput = document.querySelector('input[name="twitter"]') as HTMLInputElement;
    twitterInput.value = '  @new_handle  ';
    twitterInput.dispatchEvent(new Event('input', { bubbles: true }));

    const websiteInput = document.querySelector('input[name="website"]') as HTMLInputElement;
    websiteInput.value = '  https://new-site.com  ';
    websiteInput.dispatchEvent(new Event('input', { bubbles: true }));

    // Submit the form
    const form = document.getElementById('edit-profile-form') as HTMLFormElement;
    form.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }));

    // Flush microtasks so the async fetch + response pipeline completes
    await vi.runAllTicks();

    // Verify PUT payload has trimmed values
    expect(putCalls.length).toBe(1);
    expect(putCalls[0].body.display_name).toBe('Bob Updated');
    expect(putCalls[0].body.bio).toBe('Updated bio');
    expect(putCalls[0].body.twitter).toBe('@new_handle');
    expect(putCalls[0].body.website).toBe('https://new-site.com');
  });

  // ── In-place Re-render after Save ─────────────────────────────────────────

  it('closes modal and re-renders profile in-place after successful save', async () => {
    // Track how many profile GET calls happen
    let profileGetCount = 0;
    await loadOwnProfileWithPut({ jwt: 'test-jwt', onProfileGet: () => { profileGetCount++; } });
    const initialGetCount = profileGetCount;

    // Open modal and submit
    document.getElementById('edit-profile-btn')!.click();
    const form = document.getElementById('edit-profile-form') as HTMLFormElement;
    form.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }));

    // Flush microtasks only (not timers!) so the async fetch resolves
    // but the 800ms closeModal setTimeout remains pending.
    await vi.runAllTicks();

    // The 800ms setTimeout hasn't fired yet
    expect(document.getElementById('edit-profile-overlay')).not.toBeNull();

    // Advance past the 800ms delay
    vi.advanceTimersByTime(900);
    await vi.runAllTimersAsync();

    // Modal should be closed
    expect(document.getElementById('edit-profile-overlay')).toBeNull();

    // Profile should have re-fetched (loadProfileData was called again)
    expect(profileGetCount).toBeGreaterThan(initialGetCount);
  });

  // ── Server Error ─────────────────────────────────────────────────────────

  it('shows server error message when PUT returns non-OK', async () => {
    await loadOwnProfileWithPut({
      jwt: 'test-jwt',
      putBehavior: { type: 'serverError', status: 400, error: 'Display name too long' },
    });

    document.getElementById('edit-profile-btn')!.click();
    const form = document.getElementById('edit-profile-form') as HTMLFormElement;
    form.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }));

    // Flush microtasks for the async fetch + error response pipeline.
    // The error path calls await res.json() which creates a second
    // microtask level — runAllTicks twice to flush both levels.
    await vi.runAllTicks();
    await vi.runAllTicks();

    // Should show server error
    const statusEl = document.getElementById('edit-profile-status')!;
    expect(statusEl.style.display).toBe('block');
    expect(statusEl.innerHTML).toContain('Display name too long');

    // Save button should be re-enabled
    const saveBtn = document.getElementById('edit-profile-save') as HTMLButtonElement;
    expect(saveBtn.disabled).toBe(false);
    expect(saveBtn.textContent).toBe('Save Changes');
  });

  // ── Network Error ────────────────────────────────────────────────────────

  it('shows network error when PUT fetch throws', async () => {
    await loadOwnProfileWithPut({
      jwt: 'test-jwt',
      putBehavior: { type: 'networkError', message: 'Network failure' },
    });

    document.getElementById('edit-profile-btn')!.click();
    const form = document.getElementById('edit-profile-form') as HTMLFormElement;
    form.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }));

    // Flush microtasks for the async fetch + rejection pipeline
    await vi.runAllTicks();

    // Should show network error
    const statusEl = document.getElementById('edit-profile-status')!;
    expect(statusEl.style.display).toBe('block');
    expect(statusEl.innerHTML).toContain('Network error');

    // Save button should be re-enabled
    const saveBtn = document.getElementById('edit-profile-save') as HTMLButtonElement;
    expect(saveBtn.disabled).toBe(false);
    expect(saveBtn.textContent).toBe('Save Changes');
  });
});
