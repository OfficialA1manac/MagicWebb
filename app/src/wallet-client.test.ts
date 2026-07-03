// ── Module declaration for ?raw imports ────────────────────────────────────
declare module '*?raw' {
  const content: string;
  export default content;
}

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';

// Load wallet.js as a raw string so we can evaluate it in jsdom.
import walletJsSource from '../../frontend/static/wallet.js?raw';

// ── Server-injected globals that wallet.js reads at parse time ─────────────
function setServerGlobals(): void {
  (window as any).MW_MARKETPLACE    = '0x1111111111111111111111111111111111111111';
  (window as any).MW_AUCTION        = '0x2222222222222222222222222222222222222222';
  (window as any).MW_OFFERBOOK      = '0x3333333333333333333333333333333333333333';
  (window as any).MW_WC_PROJECT_ID  = 'test-project-id';
  (window as any).MW_NETWORK_NAME   = 'Flare Coston2';
  (window as any).MW_NETWORK_ID     = 114;
  (window as any).MW_RPC_URL        = 'https://coston2-api.flare.network/ext/C/rpc';
  (window as any).MW_EXPLORER       = 'https://coston2-explorer.flare.network';
  (window as any).MW_NATIVE_CURRENCY = 'C2FLR';
}

function clearServerGlobals(): void {
  delete (window as any).MW_MARKETPLACE;
  delete (window as any).MW_AUCTION;
  delete (window as any).MW_OFFERBOOK;
  delete (window as any).MW_WC_PROJECT_ID;
  delete (window as any).MW_NETWORK_NAME;
  delete (window as any).MW_NETWORK_ID;
  delete (window as any).MW_RPC_URL;
  delete (window as any).MW_EXPLORER;
  delete (window as any).MW_NATIVE_CURRENCY;
}

// ── Mock Alpine that handles both getter and setter patterns ───────────────
// wallet.js calls Alpine.store('modals', {…}) and Alpine.store('wallet', {…})
// to register the stores (setter), and Alpine.store('wallet') to read them
// (getter). The mock stores are populated by the setter calls.
//
// CRITICAL: Object.assign evaluates getters at copy time, turning computed
// properties into static values. Use Object.defineProperties to preserve
// getter/setter semantics so `shortAddr`, `connected`, etc. work correctly.
let mockWalletStore: Record<string, any> = {};
let mockModalsStore: Record<string, any> = {};

function resetStores(): void {
  mockWalletStore = {};
  mockModalsStore = {};
}

function defineStoreProps(target: Record<string, any>, source: Record<string, any>): void {
  // Clear existing own properties so we start fresh
  for (const key of Object.getOwnPropertyNames(target)) {
    try { delete (target as any)[key]; } catch (_) { /* ignore non-configurable */ }
  }
  // Copy property descriptors (preserves getters, setters, methods, values)
  const descs = Object.getOwnPropertyDescriptors(source);
  Object.defineProperties(target, descs);
}

function createMockAlpine(): { store: ReturnType<typeof vi.fn>; raw: ReturnType<typeof vi.fn> } {
  resetStores();

  const mock = {
    store: vi.fn((name: string, value?: any) => {
      if (value !== undefined) {
        // Setter: populate the appropriate mock store preserving property descriptors
        if (name === 'wallet') { defineStoreProps(mockWalletStore, value); return; }
        if (name === 'modals') { defineStoreProps(mockModalsStore, value); return; }
        return;
      }
      // Getter: return the mock store
      if (name === 'wallet') return mockWalletStore;
      if (name === 'modals') return mockModalsStore;
      return undefined;
    }),
    raw: vi.fn((obj: any) => obj), // raw() is used by the R() helper
  };

  (window as any).Alpine = mock;
  return mock;
}

/** Evaluate wallet.js IIFE. Must be called AFTER server globals and Alpine mock are set. */
function loadWalletJs(): void {
  // wallet.js IIFE captures `Alpine` from the enclosing scope by closure.
  // new Function('Alpine', source) with the param bound to the mock makes
  // `Alpine` available inside the IIFE body.
  const fn = new Function('Alpine', walletJsSource);
  fn((window as any).Alpine);
}

/** Trigger the alpine:init event which wallet.js listens to. */
function dispatchAlpineInit(): void {
  window.dispatchEvent(new CustomEvent('alpine:init'));
}

// ═══════════════════════════════════════════════════════════════════════════════
// Suite 1: Alpine $store.wallet Registration
// ═══════════════════════════════════════════════════════════════════════════════

describe('$store.wallet', () => {
  beforeEach(() => {
    setServerGlobals();
    createMockAlpine();
    loadWalletJs();
    dispatchAlpineInit();
  });

  afterEach(() => {
    clearServerGlobals();
    delete (window as any).Alpine;
  });

  it('is registered with connect function after alpine:init', () => {
    expect(typeof mockWalletStore.connect).toBe('function');
  });

  describe('default state', () => {
    it('has address=null', () => {
      expect(mockWalletStore.address).toBeNull();
    });

    it('has chainId=null', () => {
      expect(mockWalletStore.chainId).toBeNull();
    });

    it('has jwt=null', () => {
      expect(mockWalletStore.jwt).toBeNull();
    });

    it('has unread=0', () => {
      expect(mockWalletStore.unread).toBe(0);
    });

    it('has busy=false', () => {
      expect(mockWalletStore.busy).toBe(false);
    });

    it('has state="idle"', () => {
      expect(mockWalletStore.state).toBe('idle');
    });

    it('has savedAddress=null', () => {
      expect(mockWalletStore.savedAddress).toBeNull();
    });

    it('has savedKind=null', () => {
      expect(mockWalletStore.savedKind).toBeNull();
    });

    it('has _raw with provider/signer/wc as null', () => {
      expect(mockWalletStore._raw).toEqual({ provider: null, signer: null, wc: null });
    });
  });

  describe('computed getters', () => {
    it('shortAddr truncates address', () => {
      mockWalletStore.address = '0x1234567890abcdef1234567890abcdef12345678';
      expect(mockWalletStore.shortAddr).toBe('0x1234…5678');
    });

    it('shortAddr returns "" when address is null', () => {
      expect(mockWalletStore.shortAddr).toBe('');
    });

    it('shortSavedAddr truncates saved address', () => {
      mockWalletStore.savedAddress = '0xabcdefabcdefabcdefabcdefabcdefabcdefabcd';
      expect(mockWalletStore.shortSavedAddr).toBe('0xabcd…abcd');
    });

    it('hasSavedWallet is true when savedAddress is set', () => {
      mockWalletStore.savedAddress = '0xabc';
      expect(mockWalletStore.hasSavedWallet).toBe(true);
    });

    it('hasSavedWallet is false when savedAddress is null', () => {
      expect(mockWalletStore.hasSavedWallet).toBe(false);
    });

    it('connected is false when address is null', () => {
      expect(mockWalletStore.connected).toBe(false);
    });

    it('connected is true when address and state=connected', () => {
      mockWalletStore.address = '0xabc';
      mockWalletStore.state = 'connected';
      expect(mockWalletStore.connected).toBe(true);
    });

    it('connected is false when state is not connected', () => {
      mockWalletStore.address = '0xabc';
      mockWalletStore.state = 'idle';
      expect(mockWalletStore.connected).toBe(false);
    });

    it('stateError returns _stateError', () => {
      const err = new Error('test');
      mockWalletStore._stateError = err;
      expect(mockWalletStore.stateError).toBe(err);
    });
  });

  describe('setState()', () => {
    it('updates state and dispatches mw-wallet-state event', () => {
      const spy = vi.spyOn(window, 'dispatchEvent');
      mockWalletStore.setState('connecting');
      expect(mockWalletStore.state).toBe('connecting');
      const ev = spy.mock.calls.find(([e]) => (e as CustomEvent).type === 'mw-wallet-state');
      expect(ev).toBeDefined();
      expect((ev![0] as CustomEvent).detail.state).toBe('connecting');
      spy.mockRestore();
    });

    it('sets _stateError when error is passed', () => {
      const err = new Error('fail');
      mockWalletStore.setState('error', { error: err });
      expect(mockWalletStore._stateError).toBe(err);
    });
  });

  describe('disconnect()', () => {
    it('resets store state and persists nothing', () => {
      mockWalletStore._raw = { provider: true, signer: true, wc: true };
      mockWalletStore.address = '0xabc';
      mockWalletStore.jwt = 'token';
      mockWalletStore.unread = 5;
      mockWalletStore.state = 'connected';

      mockWalletStore.disconnect();

      expect(mockWalletStore._raw).toEqual({ provider: null, signer: null, wc: null });
      expect(mockWalletStore.address).toBeNull();
      expect(mockWalletStore.jwt).toBeNull();
      expect(mockWalletStore.unread).toBe(0);
      expect(mockWalletStore.state).toBe('idle');
    });
  });

  describe('forgetSaved()', () => {
    it('clears savedAddress/savedKind and localStorage', () => {
      mockWalletStore.savedAddress = '0xabc';
      mockWalletStore.savedKind = 'walletconnect';
      localStorage.setItem('mw_addr', '0xabc');
      localStorage.setItem('mw_kind', 'walletconnect');

      mockWalletStore.forgetSaved();

      expect(mockWalletStore.savedAddress).toBeNull();
      expect(mockWalletStore.savedKind).toBeNull();
      expect(localStorage.getItem('mw_addr')).toBeNull();
      expect(localStorage.getItem('mw_kind')).toBeNull();
    });
  });

  describe('authHeaders()', () => {
    it('returns Bearer token when jwt is set', () => {
      mockWalletStore.jwt = 'my-token';
      const h = mockWalletStore.authHeaders();
      expect(h.Authorization).toBe('Bearer my-token');
    });

    it('omits Authorization when jwt is null', () => {
      const h = mockWalletStore.authHeaders();
      expect(h.Authorization).toBeUndefined();
    });

    it('always includes Content-Type', () => {
      const h = mockWalletStore.authHeaders();
      expect(h['Content-Type']).toBe('application/json');
    });
  });

  describe('_validateAction()', () => {
    it('returns true when ethers is not available (graceful fallback)', () => {
      const orig = (window as any).ethers;
      delete (window as any).ethers;
      const result = mockWalletStore._validateAction({
        collection: '0xabc',
        tokenId: '123',
        seller: '0xdef',
        priceWei: '1000000000000000000',
      });
      expect(result).toBe(true);
      (window as any).ethers = orig;
    });
  });
});

// ═══════════════════════════════════════════════════════════════════════════════
// Suite 2: Alpine $store.modals Registration
// ═══════════════════════════════════════════════════════════════════════════════

describe('$store.modals', () => {
  let warnSpy: ReturnType<typeof vi.spyOn>;

  beforeEach(() => {
    setServerGlobals();
    createMockAlpine();
    loadWalletJs();
    dispatchAlpineInit();
    // Suppress the expected "action modal auto-open blocked" console.warn
    // from wallet.js modals.open() — the test below that calls open({kind:'buy'})
    // without userInitiated intentionally triggers this production guard.
    warnSpy = vi.spyOn(console, 'warn').mockImplementation(() => {});
  });

  afterEach(() => {
    clearServerGlobals();
    delete (window as any).Alpine;
    warnSpy.mockRestore();
  });

  it('is registered with open/dismiss functions after alpine:init', () => {
    expect(typeof mockModalsStore.open).toBe('function');
    expect(typeof mockModalsStore.dismiss).toBe('function');
  });

  describe('default state', () => {
    it('has isOpen=false', () => {
      expect(mockModalsStore.isOpen).toBe(false);
    });

    it('has step=0', () => {
      expect(mockModalsStore.step).toBe(0);
    });

    it('has success=false', () => {
      expect(mockModalsStore.success).toBe(false);
    });

    it('has progress=0', () => {
      expect(mockModalsStore.progress).toBe(0);
    });
  });

  describe('open()', () => {
    it('returns null when userInitiated is not true', async () => {
      const result = await mockModalsStore.open({ kind: 'buy' });
      expect(result).toBeNull();
    });

    it('falls back to MODAL_OPTS_FALLBACK when opts is null (userInitiated=true => proceeds)', async () => {
      // null triggers the fallback to MODAL_OPTS_FALLBACK which has
      // userInitiated=true, so the modal opens and runs the fallback's
      // run() which calls fail({...}) yielding {ok: false}.
      const result = await mockModalsStore.open(null);
      expect(result).toEqual({ ok: false, error: expect.any(String) });
    });

    it('resolves with ok=true when userInitiated and run calls done()', async () => {
      const result = await mockModalsStore.open({
        userInitiated: true,
        kind: 'buy',
        run: async ({ done }: any) => {
          done({ txHash: '0xabc', title: 'Done' });
        },
      });
      expect(result).toEqual({ ok: true, txHash: '0xabc' });
    });

    it('resolves with ok=false when run calls fail()', async () => {
      const result = await mockModalsStore.open({
        userInitiated: true,
        kind: 'buy',
        run: async ({ fail }: any) => {
          fail({ title: 'Failed', body: 'nope' });
        },
      });
      expect(result).toEqual({ ok: false, error: expect.any(String) });
    });
  });

  describe('dismiss()', () => {
    it('closes the modal', () => {
      mockModalsStore.isOpen = true;
      mockModalsStore.dismiss();
      expect(mockModalsStore.isOpen).toBe(false);
    });
  });
});

// ═══════════════════════════════════════════════════════════════════════════════
// Suite 3: MW_CONNECT_WALLET Bridge (exposed on window)
// ═══════════════════════════════════════════════════════════════════════════════

describe('MW_CONNECT_WALLET bridge', () => {
  beforeEach(() => {
    setServerGlobals();
  });

  afterEach(() => {
    clearServerGlobals();
    delete (window as any).Alpine;
    delete (window as any).MW_CONNECT_WALLET;
  });

  it('is a function after wallet.js loads', () => {
    createMockAlpine();
    loadWalletJs();
    expect(typeof (window as any).MW_CONNECT_WALLET).toBe('function');
  });

  it('calls Alpine.store("wallet").connect when Alpine is ready and wallet is registered', async () => {
    createMockAlpine();
    loadWalletJs();
    dispatchAlpineInit();

    mockWalletStore.connect = vi.fn().mockResolvedValue(undefined);

    await (window as any).MW_CONNECT_WALLET();

    expect(mockWalletStore.connect).toHaveBeenCalledWith({ silent: false });
  });

  it('toasts and returns a resolved Promise when wallet store is not yet registered', () => {
    createMockAlpine();
    loadWalletJs();
    // Simulate wallet store not yet registered (no alpine:init dispatched).
    // Set mockWalletStore to null so Alpine.store('wallet') returns falsy,
    // hitting the `if (!w)` path in MW_CONNECT_WALLET which surfaces a
    // toast and returns Promise.resolve() (consistent with the happy path).
    mockWalletStore = null as any;

    const container = document.createElement('div');
    container.id = 'toasts';
    document.body.appendChild(container);

    const result = (window as any).MW_CONNECT_WALLET();

    // The if (!w) path now returns Promise.resolve() instead of undefined.
    expect(result).toBeInstanceOf(Promise);
    // Toast should be appended with the error message.
    expect(container.children.length).toBe(1);
    expect(container.children[0].textContent).toContain('Wallet store not yet registered');

    document.getElementById('toasts')?.remove();
  });

  it('handles a synchronous crash in Alpine.store() by surfacing a toast', () => {
    createMockAlpine();
    loadWalletJs();
    // Make Alpine.store() throw on its NEXT call — this hits the outer
    // `catch (e)` handler in MW_CONNECT_WALLET which surfaces a
    // "Connect failed" toast and returns undefined. Using mockImplementationOnce
    // limits the throw to one call so subsequent code doesn't trip on it.
    const alpine = (window as any).Alpine;
    alpine.store.mockImplementationOnce(() => { throw new Error('Simulated crash'); });

    // Suppress the expected console.error from the catch handler.
    const errSpy = vi.spyOn(console, 'error').mockImplementation(() => {});

    const container = document.createElement('div');
    container.id = 'toasts';
    document.body.appendChild(container);

    const result = (window as any).MW_CONNECT_WALLET();

    // The outer catch now returns Promise.resolve() for consistency.
    expect(result).toBeInstanceOf(Promise);
    // The catch handler called console.error with the crash message.
    expect(errSpy).toHaveBeenCalledWith(
      '[mw] MW_CONNECT_WALLET crashed:',
      expect.objectContaining({ message: 'Simulated crash' }),
    );
    // Toast should be appended with "Connect failed" prefix.
    expect(container.children.length).toBe(1);
    expect(container.children[0].textContent).toContain('Connect failed: Simulated crash');

    errSpy.mockRestore();
    alpine.store.mockRestore();
    document.getElementById('toasts')?.remove();
  });
});

// ═══════════════════════════════════════════════════════════════════════════════
// Suite 4: Global Utility Functions (exposed on window)
// ═══════════════════════════════════════════════════════════════════════════════

describe('Globals exposed on window', () => {
  beforeEach(() => {
    setServerGlobals();
    createMockAlpine();
    loadWalletJs();
    dispatchAlpineInit();
  });

  afterEach(() => {
    clearServerGlobals();
    delete (window as any).Alpine;
  });

  describe('MW_HIDE_ALL()', () => {
    it('is callable without throwing', () => {
      expect(typeof (window as any).MW_HIDE_ALL).toBe('function');
      expect(() => (window as any).MW_HIDE_ALL()).not.toThrow();
    });
  });

  describe('MW_WC_OPEN_OVERLAY()', () => {
    it('is callable without throwing (no-op bridge)', () => {
      expect(typeof (window as any).MW_WC_OPEN_OVERLAY).toBe('function');
      expect(() => (window as any).MW_WC_OPEN_OVERLAY()).not.toThrow();
    });
  });
});

// ═══════════════════════════════════════════════════════════════════════════════
// Suite 5: Document Event Bus Handlers
// ═══════════════════════════════════════════════════════════════════════════════

describe('Event bus handlers', () => {
  beforeEach(() => {
    setServerGlobals();
    createMockAlpine();
    loadWalletJs();
    dispatchAlpineInit();
  });

  afterEach(() => {
    clearServerGlobals();
    delete (window as any).Alpine;
  });

  it('handles buy event by calling wallet.buy()', () => {
    mockWalletStore.buy = vi.fn().mockResolvedValue({ ok: true });

    document.dispatchEvent(new CustomEvent('buy', {
      detail: {
        collection: '0xabc',
        tokenId: '123',
        seller: '0xdef',
        price: '1000000000000000000',
      },
    }));

    expect(mockWalletStore.buy).toHaveBeenCalledWith(
      '0xabc', '123', '0xdef', '1000000000000000000',
    );
  });

  it('handles cancel-listing event by calling wallet.cancel()', () => {
    mockWalletStore.cancel = vi.fn().mockResolvedValue({ ok: true });

    document.dispatchEvent(new CustomEvent('cancel-listing', {
      detail: { collection: '0xabc', tokenId: '123' },
    }));

    expect(mockWalletStore.cancel).toHaveBeenCalledWith('0xabc', '123');
  });

  it('handles settle-auction event by calling wallet.settle()', () => {
    mockWalletStore.settle = vi.fn().mockResolvedValue({ ok: true });

    document.dispatchEvent(new CustomEvent('settle-auction', {
      detail: { auctionId: 42 },
    }));

    expect(mockWalletStore.settle).toHaveBeenCalledWith(42);
  });

  it('handles mw-notification event by calling refreshUnread()', () => {
    mockWalletStore.jwt = 'test-jwt';
    mockWalletStore.refreshUnread = vi.fn().mockResolvedValue(undefined);

    window.dispatchEvent(new Event('mw-notification'));

    expect(mockWalletStore.refreshUnread).toHaveBeenCalled();
  });
});

// ═══════════════════════════════════════════════════════════════════════════════
// Suite 6: Alpine Guards — Event Listener Safety
// ═══════════════════════════════════════════════════════════════════════════════
// wallet.js registers event listeners at the bottom of the IIFE, outside the
// alpine:init handler. These listeners reference Alpine directly. The v35
// Alpine guards prevent "Cannot read properties of undefined (reading 'store')"
// when Alpine hasn't loaded yet (wallet.js parses before alpine.js via defer).
//
// Each nested describe loads wallet.js once (no duplicate listeners) so
// alpine:init handler errors don't leak between the two scenarios.

describe('Alpine guards (event listener safety)', () => {
  beforeEach(() => {
    setServerGlobals();
    // Clear localStorage to prevent leaked mw_addr from previous suites
    // from triggering the alpine:init auto-reconnect code path, which
    // would call mockWalletStore.connect() before we've set it up.
    localStorage.clear();
  });
  afterEach(() => {
    clearServerGlobals();
    delete (window as any).Alpine;
    delete (window as any).MW_CONNECT_WALLET;
    delete (window as any).MW_HIDE_ALL;
    delete (window as any).MW_WC_OPEN_OVERLAY;
  });

  // NOTE: "available" must run BEFORE "undefined". After "undefined" loads
  // wallet.js with Alpine=undefined (creating listeners that capture
  // Alpine=undefined), any subsequent dispatchAlpineInit() would fire the
  // unguarded alpine:init handler from that closure and produce 16 unhandled
  // TypeErrors. Running "available" first avoids stale undefined-closure
  // listeners during the alpine:init phase.
  describe('when Alpine is available (guards let events through)', () => {
    beforeEach(() => {
      createMockAlpine();
      loadWalletJs();
      dispatchAlpineInit();
    });

    it('buy event calls wallet.buy()', () => {
      mockWalletStore.buy = vi.fn().mockResolvedValue({ ok: true });

      document.dispatchEvent(new CustomEvent('buy', {
        detail: {
          collection: '0xabc',
          tokenId: '123',
          seller: '0xdef',
          price: '1000000000000000000',
        },
      }));

      expect(mockWalletStore.buy).toHaveBeenCalledWith(
        '0xabc', '123', '0xdef', '1000000000000000000',
      );
    });

    it('cancel-listing event calls wallet.cancel()', () => {
      mockWalletStore.cancel = vi.fn().mockResolvedValue({ ok: true });

      document.dispatchEvent(new CustomEvent('cancel-listing', {
        detail: { collection: '0xabc', tokenId: '123' },
      }));

      expect(mockWalletStore.cancel).toHaveBeenCalledWith('0xabc', '123');
    });

    it('settle-auction event calls wallet.settle()', () => {
      mockWalletStore.settle = vi.fn().mockResolvedValue({ ok: true });

      document.dispatchEvent(new CustomEvent('settle-auction', {
        detail: { auctionId: 42 },
      }));

      expect(mockWalletStore.settle).toHaveBeenCalledWith(42);
    });

    it('mw-notification event calls wallet.refreshUnread()', () => {
      mockWalletStore.jwt = 'test-jwt';
      mockWalletStore.refreshUnread = vi.fn().mockResolvedValue(undefined);

      window.dispatchEvent(new Event('mw-notification'));

      expect(mockWalletStore.refreshUnread).toHaveBeenCalled();
    });
  });

  describe('when Alpine is undefined (guards prevent TypeError)', () => {
    beforeEach(() => {
      // Intentionally do NOT call createMockAlpine() — Alpine is undefined.
      // loadWalletJs() passes `window.Alpine` (undefined) as the IIFE parameter,
      // so the event listeners capture Alpine=undefined in their closure.
      loadWalletJs();
    });

    it('does not throw when buy event fires', () => {
      expect(() => {
        document.dispatchEvent(new CustomEvent('buy', {
          detail: {
            collection: '0xabc',
            tokenId: '123',
            seller: '0xdef',
            price: '1000000000000000000',
          },
        }));
      }).not.toThrow();
    });

    it('does not throw when cancel-listing event fires', () => {
      expect(() => {
        document.dispatchEvent(new CustomEvent('cancel-listing', {
          detail: { collection: '0xabc', tokenId: '123' },
        }));
      }).not.toThrow();
    });

    it('does not throw when settle-auction event fires', () => {
      expect(() => {
        document.dispatchEvent(new CustomEvent('settle-auction', {
          detail: { auctionId: 42 },
        }));
      }).not.toThrow();
    });

    it('does not throw when mw-notification event fires', () => {
      expect(() => {
        window.dispatchEvent(new Event('mw-notification'));
      }).not.toThrow();
    });
  });
});
