// ── Module declaration for ?raw imports ────────────────────────────────────
// Vite's ?raw suffix imports the file content as a plain string. Vitest uses
// Vite's module resolution under the hood, so this works in test files even
// though TypeScript doesn't know about the ?raw suffix by default.
declare module '*?raw' {
  const content: string;
  export default content;
}

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';

// Load ws.js as a raw string so we can evaluate it in jsdom for testing.
import wsJsSource from '../../frontend/static/ws.js?raw';

// ── WebSocket constants mirroring the browser API ──────────────────────────
const WS_CONNECTING = 0;
const WS_OPEN = 1;
const WS_CLOSING = 2;
const WS_CLOSED = 3;

// ── Helper: evaluate the ws.js IIFE in the current jsdom context ──────────
function loadWsJs(): void {
  // new Function creates a function in global scope (closer to <script> tag
  // semantics than eval() which uses the enclosing lexical scope).
  const fn = new Function(wsJsSource);
  fn();
}

// ── Helper: create a mock WebSocket instance ───────────────────────────────
interface MockWsInstance {
  readyState: number;
  send: ReturnType<typeof vi.fn>;
  close: ReturnType<typeof vi.fn>;
  onopen: ((ev: Event) => void) | null;
  onclose: ((ev: CloseEvent) => void) | null;
  onmessage: ((ev: MessageEvent) => void) | null;
  onerror: ((ev: Event) => void) | null;
}

function createMockWs(): MockWsInstance {
  return {
    readyState: WS_CONNECTING,
    send: vi.fn(),
    close: vi.fn(),
    onopen: null,
    onclose: null,
    onmessage: null,
    onerror: null,
  };
}

// ── State shared between setup helpers and tests ───────────────────────────
let mockWs: MockWsInstance;
let dispatchCalls: CustomEvent[];
let originalWebSocket: typeof WebSocket | undefined;

function setupWsMock(): void {
  // Save the original WebSocket so we can restore it (jsdom provides one)
  originalWebSocket = globalThis.WebSocket;

  mockWs = createMockWs();

  // CRITICAL: Use a plain function, NOT an arrow function as the implementation.
  // The ws.js code calls `new WebSocket(url)` and arrow functions cannot be used
  // as constructors — `new (() => {})()` throws TypeError. Vitest's vi.fn() wraps
  // a regular function correctly for `new` invocation.
  function MockCtor(): MockWsInstance {
    return mockWs;
  }
  MockCtor.CONNECTING = WS_CONNECTING;
  MockCtor.OPEN = WS_OPEN;
  MockCtor.CLOSING = WS_CLOSING;
  MockCtor.CLOSED = WS_CLOSED;

  // Wrap in vi.fn() so we can spy on calls (mockClear, toHaveBeenCalled, etc.)
  const mock = vi.fn(MockCtor) as unknown as typeof WebSocket & {
    mockClear: () => void;
  };
  // Re-attach static properties that vi.fn() strips
  mock.CONNECTING = WS_CONNECTING;
  mock.OPEN = WS_OPEN;
  mock.CLOSING = WS_CLOSING;
  mock.CLOSED = WS_CLOSED;

  (globalThis as any).WebSocket = mock;

  // Track window.dispatchEvent calls
  dispatchCalls = [];
  vi.spyOn(window, 'dispatchEvent').mockImplementation((ev: Event) => {
    dispatchCalls.push(ev as CustomEvent);
    return true;
  });
}

function restoreWsMock(): void {
  if (originalWebSocket) {
    (globalThis as any).WebSocket = originalWebSocket;
  }
}

// ── Connection helpers ─────────────────────────────────────────────────────

/** Simulate the WebSocket opening after connect(). */
function openWs(): void {
  mockWs.readyState = WS_OPEN;
  if (mockWs.onopen) {
    mockWs.onopen(new Event('open'));
  }
}

/** Simulate receiving a JSON message on the WebSocket. */
function receiveWsMessage(data: unknown): void {
  if (mockWs.onmessage) {
    mockWs.onmessage(new MessageEvent('message', { data: JSON.stringify(data) }));
  }
}

/** Simulate the WebSocket closing. */
function closeWs(code = 1000, reason = ''): void {
  mockWs.readyState = WS_CLOSED;
  if (mockWs.onclose) {
    mockWs.onclose(new CloseEvent('close', { code, reason }));
  }
}

/** Find a dispatched CustomEvent by event type. */
function findEvent(type: string): CustomEvent | undefined {
  return dispatchCalls.find((e) => e.type === type);
}

/** Get the WebSocket mock spy (created by vi.fn). */
function getWebSocketMock(): ReturnType<typeof vi.fn> {
  return (globalThis as any).WebSocket as ReturnType<typeof vi.fn>;
}

// ═══════════════════════════════════════════════════════════════════════════════
// Suite 1: MW_WS Public API
// ═══════════════════════════════════════════════════════════════════════════════

describe('MW_WS API', () => {
  beforeEach(() => {
    vi.useFakeTimers();
    setupWsMock();
    loadWsJs();
  });

  afterEach(() => {
    const api = (window as any).MW_WS;
    if (api && typeof api.close === 'function') {
      try { api.close(); } catch (_) { /* ignore */ }
    }
    delete (window as any).MW_WS;
    vi.restoreAllMocks();
    restoreWsMock();
    vi.useRealTimers();
  });

  it('exposes MW_WS globally after loading', () => {
    expect((window as any).MW_WS).toBeDefined();
    expect(typeof (window as any).MW_WS.send).toBe('function');
    expect(typeof (window as any).MW_WS.subscribe).toBe('function');
    expect(typeof (window as any).MW_WS.unsubscribe).toBe('function');
    expect(typeof (window as any).MW_WS.getListing).toBe('function');
    expect(typeof (window as any).MW_WS.getAuction).toBe('function');
    expect(typeof (window as any).MW_WS.getOffer).toBe('function');
    expect(typeof (window as any).MW_WS.getToken).toBe('function');
    expect(typeof (window as any).MW_WS.close).toBe('function');
    expect(typeof (window as any).MW_WS.reconnect).toBe('function');
  });

  it('connected returns false when not connected', () => {
    expect((window as any).MW_WS.connected).toBe(false);
  });

  it('send returns false when not connected', () => {
    expect((window as any).MW_WS.send({ type: 'ping' })).toBe(false);
  });

  it('connected returns true and send works after connect + onopen', () => {
    const api = (window as any).MW_WS;
    api.reconnect();

    // Not yet open — send should be queued but not delivered
    expect(mockWs.send).not.toHaveBeenCalled();
    expect(api.connected).toBe(false);

    // Simulate the WebSocket opening
    openWs();

    expect(api.connected).toBe(true);

    // Send a message
    const result = api.send({ type: 'ping' });
    expect(result).toBe(true);
    expect(mockWs.send).toHaveBeenCalledWith(JSON.stringify({ type: 'ping' }));
  });

  it('subscribe sends the correct message', () => {
    const api = (window as any).MW_WS;
    api.reconnect();
    openWs();
    api.subscribe(['token:0xabc:1', 'collection:0xdef']);
    expect(mockWs.send).toHaveBeenCalledWith(
      JSON.stringify({
        type: 'subscribe',
        data: { channels: ['token:0xabc:1', 'collection:0xdef'] },
      }),
    );
  });

  it('unsubscribe sends the correct message', () => {
    const api = (window as any).MW_WS;
    api.reconnect();
    openWs();
    api.unsubscribe(['token:0xabc:1']);
    expect(mockWs.send).toHaveBeenCalledWith(
      JSON.stringify({ type: 'unsubscribe', data: { channels: ['token:0xabc:1'] } }),
    );
  });

  it('getListing sends the correct message', () => {
    const api = (window as any).MW_WS;
    api.reconnect();
    openWs();
    api.getListing('0xabc', '123');
    expect(mockWs.send).toHaveBeenCalledWith(
      JSON.stringify({
        type: 'action',
        data: { action: 'get_listing', params: { collection: '0xabc', token_id: '123' } },
      }),
    );
  });

  it('getAuction sends the correct message', () => {
    const api = (window as any).MW_WS;
    api.reconnect();
    openWs();
    api.getAuction(42);
    expect(mockWs.send).toHaveBeenCalledWith(
      JSON.stringify({
        type: 'action',
        data: { action: 'get_auction', params: { auction_id: 42 } },
      }),
    );
  });

  it('getOffer sends the correct message', () => {
    const api = (window as any).MW_WS;
    api.reconnect();
    openWs();
    api.getOffer('offer-xyz');
    expect(mockWs.send).toHaveBeenCalledWith(
      JSON.stringify({
        type: 'action',
        data: { action: 'get_offer', params: { offer_id: 'offer-xyz' } },
      }),
    );
  });

  it('getToken sends the correct message', () => {
    const api = (window as any).MW_WS;
    api.reconnect();
    openWs();
    api.getToken('0xabc', '123');
    expect(mockWs.send).toHaveBeenCalledWith(
      JSON.stringify({
        type: 'action',
        data: { action: 'get_token', params: { collection: '0xabc', token_id: '123' } },
      }),
    );
  });

  it('close terminates the connection and blocks auto-reconnect', () => {
    const api = (window as any).MW_WS;
    api.reconnect();
    openWs();
    expect(api.connected).toBe(true);

    // close() should call ws.close() and set closing=true
    api.close();
    expect(mockWs.close).toHaveBeenCalled();
    expect(api.connected).toBe(false);

    // Simulate the onclose event that the real WebSocket would fire.
    // With closing=true, scheduleReconnect should be a no-op.
    getWebSocketMock().mockClear();
    closeWs(1000, 'Normal closure');

    // Advance fake timers — scheduleReconnect uses setTimeout, but it should
    // bail out early because closing=true prevents any new connection.
    vi.advanceTimersByTime(5000);
    expect(getWebSocketMock()).not.toHaveBeenCalled();
  });

  it('reconnect() explicitly resets the closing flag and reconnects', () => {
    const api = (window as any).MW_WS;
    api.reconnect();
    openWs();
    api.close();
    expect(api.connected).toBe(false);

    // reconnect() sets closing=false and calls connect()
    getWebSocketMock().mockClear();
    api.reconnect();
    expect(getWebSocketMock()).toHaveBeenCalled();
    expect(getWebSocketMock()).toHaveBeenCalledWith(expect.stringContaining('/ws'));
  });
});

// ═══════════════════════════════════════════════════════════════════════════════
// Suite 2: CustomEvent Dispatching
// ═══════════════════════════════════════════════════════════════════════════════

describe('ws.js CustomEvent dispatching', () => {
  beforeEach(() => {
    vi.useFakeTimers();
    setupWsMock();
    loadWsJs();
    // Establish a baseline connection then clear open/dispatch tracking
    (window as any).MW_WS.reconnect();
    openWs();
    dispatchCalls = [];
  });

  afterEach(() => {
    const api = (window as any).MW_WS;
    if (api && typeof api.close === 'function') {
      try { api.close(); } catch (_) { /* ignore */ }
    }
    delete (window as any).MW_WS;
    vi.restoreAllMocks();
    restoreWsMock();
    vi.useRealTimers();
  });

  it('dispatches mw-ws-open on WebSocket open', () => {
    // We already open()'d in beforeEach; simulate a fresh open cycle.
    const api = (window as any).MW_WS;
    api.close();
    dispatchCalls = [];
    getWebSocketMock().mockClear();

    api.reconnect();
    openWs();

    const ev = findEvent('mw-ws-open');
    expect(ev).toBeDefined();
  });

  it('dispatches mw-ws-close on WebSocket close', () => {
    closeWs();

    const ev = findEvent('mw-ws-close');
    expect(ev).toBeDefined();
  });

  it('dispatches mw-ws-message on every message', () => {
    receiveWsMessage({ type: 'pong' });

    const ev = findEvent('mw-ws-message');
    expect(ev).toBeDefined();
    expect(ev!.detail).toEqual({ type: 'pong' });
  });

  it('does NOT dispatch a specific event for pong messages (no-op)', () => {
    receiveWsMessage({ type: 'pong' });

    expect(findEvent('mw-ws-error')).toBeUndefined();
    expect(findEvent('mw-ws-subscribed')).toBeUndefined();
    expect(findEvent('mw-ws-unsubscribed')).toBeUndefined();
    expect(findEvent('mw-ws-state')).toBeUndefined();
    expect(findEvent('mw-ws-event')).toBeUndefined();
  });

  it('dispatches mw-ws-error for error messages', () => {
    receiveWsMessage({ type: 'error', data: { message: 'Something went wrong' } });

    const ev = findEvent('mw-ws-error');
    expect(ev).toBeDefined();
    expect(ev!.detail).toEqual({ message: 'Something went wrong' });
  });

  it('does NOT dispatch a specific event for ack messages (no-op)', () => {
    receiveWsMessage({ type: 'ack', data: { status: 'ok' } });

    expect(findEvent('mw-ws-error')).toBeUndefined();
    expect(findEvent('mw-ws-subscribed')).toBeUndefined();
    expect(findEvent('mw-ws-unsubscribed')).toBeUndefined();
    expect(findEvent('mw-ws-state')).toBeUndefined();
    expect(findEvent('mw-ws-event')).toBeUndefined();
  });

  it('dispatches mw-ws-subscribed for subscribed messages', () => {
    receiveWsMessage({
      type: 'subscribed',
      data: { channels: ['token:0xabc:1'] },
    });

    const ev = findEvent('mw-ws-subscribed');
    expect(ev).toBeDefined();
    expect(ev!.detail).toEqual({ channels: ['token:0xabc:1'] });
  });

  it('dispatches mw-ws-unsubscribed for unsubscribed messages', () => {
    receiveWsMessage({
      type: 'unsubscribed',
      data: { channels: ['token:0xabc:1'] },
    });

    const ev = findEvent('mw-ws-unsubscribed');
    expect(ev).toBeDefined();
    expect(ev!.detail).toEqual({ channels: ['token:0xabc:1'] });
  });

  it('dispatches mw-ws-state for state messages', () => {
    receiveWsMessage({
      type: 'state',
      data: { listing: { price_wei: '1000000000000000000', token_id: '123' } },
    });

    const ev = findEvent('mw-ws-state');
    expect(ev).toBeDefined();
    expect(ev!.detail).toEqual({
      listing: { price_wei: '1000000000000000000', token_id: '123' },
    });
  });

  it('dispatches mw-ws-event for unknown message types with data', () => {
    receiveWsMessage({ type: 'listing-updated', data: { listing_id: 1 } });

    const ev = findEvent('mw-ws-event');
    expect(ev).toBeDefined();
    expect(ev!.detail).toEqual({
      type: 'listing-updated',
      data: { listing_id: 1 },
    });
  });

  it('ignores malformed JSON silently (no dispatch)', () => {
    // Only trigger onmessage. The ws.js try/catch catches the JSON parse
    // error and returns early, so nothing gets dispatched.
    if (mockWs.onmessage) {
      mockWs.onmessage(new MessageEvent('message', { data: 'not valid json' }));
    }

    expect(findEvent('mw-ws-message')).toBeUndefined();
    expect(findEvent('mw-ws-error')).toBeUndefined();
  });
});

// ═══════════════════════════════════════════════════════════════════════════════
// Suite 3: Alpine Store Event Handlers
// ═══════════════════════════════════════════════════════════════════════════════

describe('Alpine store event handlers', () => {
  // Track registered event listeners for cleanup between tests.
  const listenerMeta: Array<{ type: string; handler: EventListenerOrEventListenerObject }> = [];

  function addTracked(type: string, handler: EventListenerOrEventListenerObject): void {
    window.addEventListener(type, handler);
    listenerMeta.push({ type, handler });
  }

  function removeAllTracked(): void {
    for (const { type, handler } of listenerMeta) {
      window.removeEventListener(type, handler);
    }
    listenerMeta.length = 0;
  }

  // Create a fresh WS Alpine store (mirrors layout.html's alpine:init handler)
  function createWsStore(): Record<string, any> {
    return {
      connected: false,
      subscribed: [] as string[],
      lastState: null as any,
      lastStateAt: 0,
      get hasSubscriptions(): boolean {
        return this.subscribed && this.subscribed.length > 0;
      },
      get subscriptionSummary(): string {
        if (!this.subscribed || this.subscribed.length === 0) return 'none';
        const tokens = this.subscribed.filter((c: string) => c.startsWith('token:'));
        const collections = this.subscribed.filter((c: string) => c.startsWith('collection:'));
        const parts: string[] = [];
        if (tokens.length) {
          parts.push(`${tokens.length} token${tokens.length > 1 ? 's' : ''}`);
        }
        if (collections.length) {
          parts.push(`${collections.length} collection${collections.length > 1 ? 's' : ''}`);
        }
        return parts.join(', ') ||
          `${this.subscribed.length} channel${this.subscribed.length > 1 ? 's' : ''}`;
      },
    };
  }

  let wsStore: Record<string, any>;
  let mockAlpine: { store: ReturnType<typeof vi.fn> };

  function setupAlpine(): void {
    wsStore = createWsStore();
    mockAlpine = {
      store: vi.fn((name: string) => {
        if (name === 'ws') return wsStore;
        return undefined;
      }),
    };
    (window as any).Alpine = mockAlpine;
  }

  function teardownAlpine(): void {
    delete (window as any).Alpine;
  }

  // Register the exact event handlers from layout.html's inline <script> blocks.
  function registerHandlers(): void {
    addTracked('mw-ws-subscribed', ((e: Event) => {
      const ce = e as CustomEvent;
      const channels = (ce.detail && ce.detail.channels) || [];
      if (window.Alpine && (window.Alpine as any).store) {
        const s = (window.Alpine as any).store('ws');
        if (s) s.subscribed = channels;
      }
    }) as EventListener);

    addTracked('mw-ws-unsubscribed', ((e: Event) => {
      const ce = e as CustomEvent;
      const channels = (ce.detail && ce.detail.channels) || [];
      if (window.Alpine && (window.Alpine as any).store) {
        const s = (window.Alpine as any).store('ws');
        if (s) {
          s.subscribed = (s.subscribed || []).filter(
            (c: string) => channels.indexOf(c) === -1,
          );
        }
      }
    }) as EventListener);

    addTracked('mw-ws-state', ((e: Event) => {
      if (!window.Alpine || !(window.Alpine as any).store) return;
      const s = (window.Alpine as any).store('ws');
      if (!s) return;
      const ce = e as CustomEvent;
      s.lastState = ce.detail;
      s.lastStateAt = Date.now();
    }) as EventListener);

    addTracked('mw-ws-open', (() => {
      if (window.Alpine && (window.Alpine as any).store) {
        const s = (window.Alpine as any).store('ws');
        if (s) s.connected = true;
      }
    }) as EventListener);

    addTracked('mw-ws-close', (() => {
      if (window.Alpine && (window.Alpine as any).store) {
        const s = (window.Alpine as any).store('ws');
        if (s) {
          s.connected = false;
          s.subscribed = [];
        }
      }
    }) as EventListener);
  }

  beforeEach(() => {
    setupAlpine();
    registerHandlers();
  });

  afterEach(() => {
    removeAllTracked();
    teardownAlpine();
    vi.restoreAllMocks();
  });

  // ── Store defaults ──────────────────────────────────────────────────

  it('has default connected=false', () => {
    expect(wsStore.connected).toBe(false);
  });

  it('has default subscribed=[]', () => {
    expect(wsStore.subscribed).toEqual([]);
  });

  it('has default lastState=null and lastStateAt=0', () => {
    expect(wsStore.lastState).toBeNull();
    expect(wsStore.lastStateAt).toBe(0);
  });

  // ── Computed getters ────────────────────────────────────────────────

  it('hasSubscriptions returns false for empty subscribed', () => {
    wsStore.subscribed = [];
    expect(wsStore.hasSubscriptions).toBe(false);
  });

  it('hasSubscriptions returns true for non-empty subscribed', () => {
    wsStore.subscribed = ['token:0xabc:1'];
    expect(wsStore.hasSubscriptions).toBe(true);
  });

  it('subscriptionSummary returns "none" for empty subscribed', () => {
    wsStore.subscribed = [];
    expect(wsStore.subscriptionSummary).toBe('none');
  });

  it('subscriptionSummary formats single token', () => {
    wsStore.subscribed = ['token:0xabc:1'];
    expect(wsStore.subscriptionSummary).toBe('1 token');
  });

  it('subscriptionSummary formats multiple tokens', () => {
    wsStore.subscribed = ['token:0xabc:1', 'token:0xdef:2'];
    expect(wsStore.subscriptionSummary).toBe('2 tokens');
  });

  it('subscriptionSummary formats token and collection together', () => {
    wsStore.subscribed = [
      'token:0xabc:1',
      'token:0xdef:2',
      'collection:0xabc',
    ];
    expect(wsStore.subscriptionSummary).toBe('2 tokens, 1 collection');
  });

  it('subscriptionSummary falls back to channel count for unknown prefixes', () => {
    wsStore.subscribed = ['custom-channel', 'another-channel'];
    expect(wsStore.subscriptionSummary).toBe('2 channels');
  });

  // ── Event handlers ──────────────────────────────────────────────────

  it('mw-ws-open sets connected=true', () => {
    window.dispatchEvent(new CustomEvent('mw-ws-open'));
    expect(wsStore.connected).toBe(true);
  });

  it('mw-ws-close sets connected=false and clears subscribed', () => {
    wsStore.connected = true;
    wsStore.subscribed = ['token:0xabc:1'];
    window.dispatchEvent(new CustomEvent('mw-ws-close'));
    expect(wsStore.connected).toBe(false);
    expect(wsStore.subscribed).toEqual([]);
  });

  it('mw-ws-subscribed updates subscribed channels', () => {
    window.dispatchEvent(
      new CustomEvent('mw-ws-subscribed', {
        detail: { channels: ['token:0xabc:1', 'collection:0xdef'] },
      }),
    );
    expect(wsStore.subscribed).toEqual(['token:0xabc:1', 'collection:0xdef']);
  });

  it('mw-ws-subscribed with no detail defaults to empty array', () => {
    wsStore.subscribed = ['old:channel'];
    window.dispatchEvent(new CustomEvent('mw-ws-subscribed'));
    expect(wsStore.subscribed).toEqual([]);
  });

  it('mw-ws-unsubscribed removes specified channels', () => {
    wsStore.subscribed = ['token:0xabc:1', 'collection:0xdef', 'token:0xghi:2'];
    window.dispatchEvent(
      new CustomEvent('mw-ws-unsubscribed', {
        detail: { channels: ['token:0xabc:1', 'collection:0xdef'] },
      }),
    );
    expect(wsStore.subscribed).toEqual(['token:0xghi:2']);
  });

  it('mw-ws-unsubscribed with no detail is a no-op', () => {
    wsStore.subscribed = ['token:0xabc:1'];
    window.dispatchEvent(new CustomEvent('mw-ws-unsubscribed'));
    expect(wsStore.subscribed).toEqual(['token:0xabc:1']);
  });

  it('mw-ws-state updates lastState and lastStateAt', () => {
    const stateData = { listing: { price_wei: '5000000000000000000', token_id: '42' } };
    window.dispatchEvent(new CustomEvent('mw-ws-state', { detail: stateData }));
    expect(wsStore.lastState).toEqual(stateData);
    expect(typeof wsStore.lastStateAt).toBe('number');
  });

  it('mw-ws-state gracefully does nothing when Alpine.store returns undefined', () => {
    (window as any).Alpine.store = vi.fn(() => undefined);
    expect(() => {
      window.dispatchEvent(
        new CustomEvent('mw-ws-state', { detail: { foo: 'bar' } }),
      );
    }).not.toThrow();
  });
});
