// ── Module declaration for ?raw imports ────────────────────────────────────
declare module '*?raw' {
  const content: string;
  export default content;
}

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';

import sseJsSource from '../../frontend/static/sse.js?raw';

// ── Mock HTMX internal API ─────────────────────────────────────────────────
// sse.js calls htmx.defineExtension('sse', { ... }) which passes an internal
// API ref to init(). The extension stores it and uses it for DOM operations,
// event triggering, and attribute reading.

// Track registered listeners, event sources, and events
interface ListenerEntry {
  eventName: string;
  listener: (event: any) => void;
}

let mockEventSource: any;
let mockEventSourceCtor: any;
let listeners: ListenerEntry[];
let triggeredEvents: Array<{ elt: any; eventName: string; detail: any }>;

/** Fake internalData storage per element */
const internalDataMap = new WeakMap<object, Record<string, any>>();

function getFakeInternalData(elt: any): Record<string, any> {
  if (!internalDataMap.has(elt)) {
    internalDataMap.set(elt, {});
  }
  return internalDataMap.get(elt)!;
}

function setupHtmxMock(): void {
  listeners = [];
  triggeredEvents = [];

  mockEventSource = {
    readyState: 0, // CONNECTING
    close: vi.fn(),
    addEventListener: vi.fn((eventName: string, listener: (e: any) => void) => {
      listeners.push({ eventName, listener });
    }),
    removeEventListener: vi.fn((eventName: string, listener: (e: any) => void) => {
      listeners = listeners.filter(
        (l) => l.eventName !== eventName || l.listener !== listener,
      );
    }),
    onopen: null as any,
    onerror: null as any,
  };

  // EventSource constructor mock — must use a regular function, NOT an arrow,
  // because the SSE code does `new EventSource(url, opts)` and arrow functions
  // cannot be used as constructors (TypeError: not a constructor).
  function EventSourceCtor(): any {
    return mockEventSource;
  }
  EventSourceCtor.CONNECTING = 0;
  EventSourceCtor.OPEN = 1;
  EventSourceCtor.CLOSED = 2;

  mockEventSourceCtor = vi.fn(EventSourceCtor);
  mockEventSourceCtor.CONNECTING = 0;
  mockEventSourceCtor.OPEN = 1;
  mockEventSourceCtor.CLOSED = 2;
  (globalThis as any).EventSource = mockEventSourceCtor;

  const mockApi = {
    getInternalData: vi.fn((elt: any) => getFakeInternalData(elt)),
    getAttributeValue: vi.fn((elt: any, attr: string) => {
      return elt.getAttribute(attr);
    }),
    getClosestMatch: vi.fn((elt: any, predicate: (node: any) => boolean) => {
      let current = elt;
      while (current) {
        if (predicate(current)) return current;
        current = current.parentElement;
      }
      return null;
    }),
    triggerEvent: vi.fn((elt: any, eventName: string, detail: any) => {
      triggeredEvents.push({ elt, eventName, detail });
      return true;
    }),
    triggerErrorEvent: vi.fn((elt: any, eventName: string, detail: any) => {
      triggeredEvents.push({ elt, eventName, detail });
      return true;
    }),
    bodyContains: vi.fn((elt: any) => {
      return document.body.contains(elt);
    }),
    getSwapSpecification: vi.fn(() => ({ swapStyle: 'innerHTML' })),
    getTarget: vi.fn((elt: any) => elt),
    swap: vi.fn(),
    withExtensions: vi.fn((elt: any, fn: (ext: any) => void) => {
      // No-op: no extensions to apply in tests
    }),
    getTriggerSpecs: vi.fn((elt: any) => {
      const triggerAttr = elt.getAttribute('hx-trigger');
      if (!triggerAttr) return [];
      return triggerAttr.split(',').map((t: string) => ({ trigger: t.trim() }));
    }),
  };

  (globalThis as any).htmx = {
    defineExtension: vi.fn((name: string, ext: any) => {
      // Store the extension so we can call its methods in tests
      (globalThis as any).__sseExtension = ext;
      if (ext.init) ext.init(mockApi);
    }),
    trigger: vi.fn((elt: any, eventName: string, event: any) => {
      triggeredEvents.push({ elt, eventName, detail: event });
    }),
    createEventSource: undefined as any,
  };
}

function loadSseJs(): void {
  const fn = new Function(sseJsSource);
  fn();
}

function teardownHtmxMock(): void {
  delete (globalThis as any).htmx;
  delete (globalThis as any).__sseExtension;
  delete (globalThis as any).EventSource;
  internalDataMap.delete({} as any);
}

/** Get the registered SSE extension object. */
function getExtension(): any {
  return (globalThis as any).__sseExtension;
}

// ═══════════════════════════════════════════════════════════════════════════════
// Suite 1: Extension Registration
// ═══════════════════════════════════════════════════════════════════════════════

describe('Extension registration', () => {
  beforeEach(() => {
    setupHtmxMock();
    loadSseJs();
  });

  afterEach(() => {
    teardownHtmxMock();
  });

  it('calls htmx.defineExtension with name "sse"', () => {
    expect((globalThis as any).htmx.defineExtension).toHaveBeenCalledWith(
      'sse',
      expect.any(Object),
    );
  });

  it('registers a getSelectors method returning SSE-related selectors', () => {
    const ext = getExtension();
    expect(typeof ext.getSelectors).toBe('function');
    const selectors = ext.getSelectors();
    expect(selectors).toContain('[sse-connect]');
    expect(selectors).toContain('[data-sse-connect]');
    expect(selectors).toContain('[sse-swap]');
    expect(selectors).toContain('[data-sse-swap]');
  });

  it('hoists htmx.createEventSource to the createEventSource function', () => {
    expect(typeof (globalThis as any).htmx.createEventSource).toBe('function');
    const es = (globalThis as any).htmx.createEventSource('/events');
    expect(es).toBe(mockEventSource);
    expect(mockEventSourceCtor).toHaveBeenCalledWith('/events', { withCredentials: true });
  });
});

// ═══════════════════════════════════════════════════════════════════════════════
// Suite 2: EventSource Connection Lifecycle
// ═══════════════════════════════════════════════════════════════════════════════

describe('EventSource connection lifecycle', () => {
  beforeEach(() => {
    vi.useFakeTimers();
    setupHtmxMock();
    loadSseJs();
  });

  afterEach(() => {
    vi.restoreAllMocks();
    vi.useRealTimers();
    teardownHtmxMock();
  });

  it('htmx:afterProcessNode creates an EventSource for elements with sse-connect', () => {
    const elt = document.createElement('div');
    elt.setAttribute('sse-connect', '/events');

    const ext = getExtension();
    ext.onEvent('htmx:afterProcessNode', { target: elt });

    expect(mockEventSourceCtor).toHaveBeenCalledWith('/events', { withCredentials: true });
  });

  it('fires htmx:sseOpen on successful connection', () => {
    const elt = document.createElement('div');
    elt.setAttribute('sse-connect', '/events');

    const ext = getExtension();
    ext.onEvent('htmx:afterProcessNode', { target: elt });

    // Simulate onopen
    mockEventSource.onopen({});

    const openEvents = triggeredEvents.filter((e) => e.eventName === 'htmx:sseOpen');
    expect(openEvents.length).toBe(1);
  });

  it('stores the EventSource in internalData', () => {
    const elt = document.createElement('div');
    elt.setAttribute('sse-connect', '/events');

    const ext = getExtension();
    ext.onEvent('htmx:afterProcessNode', { target: elt });

    const data = getFakeInternalData(elt);
    expect(data.sseEventSource).toBeDefined();
    expect(data.sseEventSource).toBe(mockEventSource);
  });

  it('fires htmx:sseError on connection error', () => {
    const elt = document.createElement('div');
    elt.setAttribute('sse-connect', '/events');

    const ext = getExtension();
    ext.onEvent('htmx:afterProcessNode', { target: elt });

    mockEventSource.onerror(new Event('error'));

    const errorEvents = triggeredEvents.filter((e) => e.eventName === 'htmx:sseError');
    expect(errorEvents.length).toBe(1);
  });

  it('closes the EventSource when the element is cleaned up', () => {
    const elt = document.createElement('div');
    elt.setAttribute('sse-connect', '/events');

    const ext = getExtension();
    ext.onEvent('htmx:afterProcessNode', { target: elt });

    // Store the event source
    const data = getFakeInternalData(elt);
    data.sseEventSource = mockEventSource;

    ext.onEvent('htmx:beforeCleanupElement', { target: elt });

    expect(mockEventSource.close).toHaveBeenCalled();
    const closeEvents = triggeredEvents.filter((e) => e.eventName === 'htmx:sseClose');
    expect(closeEvents.length).toBe(1);
    expect(closeEvents[0].detail.type).toBe('nodeReplaced');
  });

  it('fires htmx:sseClose when sse-close attribute event is received', () => {
    const elt = document.createElement('div');
    elt.setAttribute('sse-connect', '/events');
    elt.setAttribute('sse-close', 'close-me');

    const ext = getExtension();
    ext.onEvent('htmx:afterProcessNode', { target: elt });

    // Find the 'close-me' listener that was registered
    const closeListener = listeners.find((l) => l.eventName === 'close-me');
    expect(closeListener).toBeDefined();

    // Fire the close listener
    closeListener!.listener({});

    expect(mockEventSource.close).toHaveBeenCalled();
    const closeEvents = triggeredEvents.filter(
      (e) => e.eventName === 'htmx:sseClose' && e.detail.type === 'message',
    );
    expect(closeEvents.length).toBe(1);
  });
});

// ═══════════════════════════════════════════════════════════════════════════════
// Suite 3: Reconnection Backoff
// ═══════════════════════════════════════════════════════════════════════════════

describe('Reconnection backoff', () => {
  /** Track elements appended to document.body for cleanup. */
  const bodyElements: HTMLElement[] = [];

  beforeEach(() => {
    vi.useFakeTimers();
    setupHtmxMock();
    loadSseJs();
  });

  afterEach(() => {
    // Clean up any elements that were appended to body
    for (const elt of bodyElements) {
      if (elt.parentElement) {
        elt.parentElement.removeChild(elt);
      }
    }
    bodyElements.length = 0;
    vi.restoreAllMocks();
    vi.useRealTimers();
    teardownHtmxMock();
  });

  /** Helper: create a sse-connect element, append to body, and process it. */
  function createConnectedElt(): HTMLElement {
    const elt = document.createElement('div');
    elt.setAttribute('sse-connect', '/events');
    // The element MUST be in document.body for maybeCloseSSESource() to
    // return false — otherwise reconnection is skipped entirely because
    // the SSE code treats a missing parent as a permanent disconnect.
    document.body.appendChild(elt);
    bodyElements.push(elt);

    const ext = getExtension();
    ext.onEvent('htmx:afterProcessNode', { target: elt });
    return elt;
  }

  it('schedules reconnect on error when EventSource is CLOSED', () => {
    createConnectedElt();

    // Clear initial connect call count
    mockEventSourceCtor.mockClear();
    mockEventSource.readyState = EventSource.CLOSED; // 2

    // Trigger onerror
    mockEventSource.onerror(new Event('error'));

    // Should schedule a reconnect with base timeout (retryCount=0 → retryCount=1 → 1*500=500ms)
    vi.advanceTimersByTime(500);

    // Should have created a new EventSource
    expect(mockEventSourceCtor).toHaveBeenCalled();
  });

  it('doubles backoff delay on consecutive failures (exponential)', () => {
    createConnectedElt();

    // Simulate first failure: retryCount starts at 0, becomes 1
    mockEventSourceCtor.mockClear();
    mockEventSource.readyState = EventSource.CLOSED;

    // Track the current mockEventSource reference
    let currentSource = mockEventSource;
    currentSource.onerror(new Event('error'));

    // After 500ms (1 * 500), first reconnect fires
    vi.advanceTimersByTime(500);
    expect(mockEventSourceCtor).toHaveBeenCalledTimes(1);

    // The reconnect creates a new EventSource. Our mock always returns
    // the same object, but the SSE code overwrites onerror/onopen with
    // new closures over the updated retryCount.
    mockEventSourceCtor.mockClear();
    currentSource = mockEventSource;
    currentSource.readyState = EventSource.CLOSED;
    currentSource.onerror(new Event('error'));

    // Second reconnect should be at 1000ms (retryCount=2 × 500)
    // From t=500, advance to t=1000: timeout hasn't fired yet (fires at t=1500)
    vi.advanceTimersByTime(500);
    expect(mockEventSourceCtor).not.toHaveBeenCalled(); // Not yet, needs another 500ms

    // From t=1000, advance to t=1500: timeout fires
    vi.advanceTimersByTime(500);
    expect(mockEventSourceCtor).toHaveBeenCalledTimes(1);
  });

  it('caps backoff at 64 seconds (128 * 500ms)',
    { timeout: 160000 },
    () => {
      createConnectedElt();

      // Simulate many failures to reach the cap
      // retryCount progression: undef→1, 1→2, 2→4, 4→8, 8→16, 16→32, 32→64, 64→128 (cap)

      for (let i = 0; i < 8; i++) {
        mockEventSourceCtor.mockClear();
        mockEventSource.readyState = EventSource.CLOSED;
        mockEventSource.onerror(new Event('error'));

        // Advance enough time for the reconnect to fire
        // The timeout is retryCount * 500ms, which caps at 128 * 500 = 64000ms
        vi.advanceTimersByTime(65000);
      }

      // After hitting the cap, verify the timeout is stable at ~64s
      mockEventSourceCtor.mockClear();
      mockEventSource.readyState = EventSource.CLOSED;
      mockEventSource.onerror(new Event('error'));

      // Should NOT fire at 63s (just under the cap)
      vi.advanceTimersByTime(63000);
      expect(mockEventSourceCtor).not.toHaveBeenCalled();

      // Should fire at 64s
      vi.advanceTimersByTime(1000);
      expect(mockEventSourceCtor).toHaveBeenCalled();
    });

  it('resets retryCount to 0 on successful reconnect (onopen)', () => {
    createConnectedElt();

    // Trigger a failure to set retryCount to a non-zero value
    mockEventSourceCtor.mockClear();
    mockEventSource.readyState = EventSource.CLOSED;
    mockEventSource.onerror(new Event('error'));

    // Advance to reconnect
    vi.advanceTimersByTime(500);
    expect(mockEventSourceCtor).toHaveBeenCalled();

    // Simulate successful reconnection — onopen fires
    mockEventSource.onopen({});

    // The onopen handler sets retryCount = 0 internally.
    // Then a subsequent failure should start from retryCount=0 again,
    // giving a 500ms (1*500) timeout instead of a longer one.

    // Trigger another failure
    mockEventSourceCtor.mockClear();
    mockEventSource.readyState = EventSource.CLOSED;
    mockEventSource.onerror(new Event('error'));

    // Should reconnect at 500ms (not 1000ms or more)
    vi.advanceTimersByTime(500);
    expect(mockEventSourceCtor).toHaveBeenCalled();
  });
});

// ═══════════════════════════════════════════════════════════════════════════════
// Suite 4: HTMX Event Dispatching via sse-swap
// ═══════════════════════════════════════════════════════════════════════════════

describe('HTMX event dispatching (sse-swap)', () => {
  beforeEach(() => {
    setupHtmxMock();
    loadSseJs();
  });

  afterEach(() => {
    teardownHtmxMock();
  });

  it('registers event listeners for sse-swap attribute', () => {
    // Create parent with sse-connect and child with sse-swap
    const parent = document.createElement('div');
    parent.setAttribute('sse-connect', '/events');

    const child = document.createElement('div');
    child.setAttribute('sse-swap', 'listing-updated');
    parent.appendChild(child);

    // Process parent first to create the EventSource
    const ext = getExtension();
    ext.onEvent('htmx:afterProcessNode', { target: parent });

    // Store the EventSource reference in internalData
    const parentData = getFakeInternalData(parent);
    parentData.sseEventSource = mockEventSource;

    // Process child to register listeners
    ext.onEvent('htmx:afterProcessNode', { target: child });

    // Should have registered a listener for 'listing-updated'
    expect(mockEventSource.addEventListener).toHaveBeenCalledWith(
      'listing-updated',
      expect.any(Function),
    );
  });

  it('fires htmx:sseBeforeMessage and htmx:sseMessage on received event', () => {
    const parent = document.createElement('div');
    parent.setAttribute('sse-connect', '/events');
    document.body.appendChild(parent);

    const child = document.createElement('div');
    child.setAttribute('sse-swap', 'listing-updated');
    parent.appendChild(child);

    const ext = getExtension();
    ext.onEvent('htmx:afterProcessNode', { target: parent });

    const parentData = getFakeInternalData(parent);
    parentData.sseEventSource = mockEventSource;

    ext.onEvent('htmx:afterProcessNode', { target: child });

    // Find the registered listener and fire it
    const msgListener = listeners.find((l) => l.eventName === 'listing-updated');
    expect(msgListener).toBeDefined();

    msgListener!.listener({ data: '<div>New content</div>' });

    // Should have triggered htmx:sseBeforeMessage and htmx:sseMessage
    const beforeMsgEvents = triggeredEvents.filter(
      (e) => e.eventName === 'htmx:sseBeforeMessage',
    );
    expect(beforeMsgEvents.length).toBe(1);

    const msgEvents = triggeredEvents.filter((e) => e.eventName === 'htmx:sseMessage');
    expect(msgEvents.length).toBe(1);

    // Clean up
    document.body.removeChild(parent);
  });

  it('does not register listeners when no parent has an EventSource', () => {
    const orphan = document.createElement('div');
    orphan.setAttribute('sse-swap', 'listing-updated');
    document.body.appendChild(orphan);

    const ext = getExtension();
    ext.onEvent('htmx:afterProcessNode', { target: orphan });

    // Should NOT have registered any listener
    expect(mockEventSource.addEventListener).not.toHaveBeenCalled();

    document.body.removeChild(orphan);
  });

  it('removes listener when element leaves the body', () => {
    const parent = document.createElement('div');
    parent.setAttribute('sse-connect', '/events');
    document.body.appendChild(parent);

    const child = document.createElement('div');
    child.setAttribute('sse-swap', 'listing-updated');
    parent.appendChild(child);

    const ext = getExtension();
    ext.onEvent('htmx:afterProcessNode', { target: parent });

    const parentData = getFakeInternalData(parent);
    parentData.sseEventSource = mockEventSource;

    ext.onEvent('htmx:afterProcessNode', { target: child });

    const msgListener = listeners.find((l) => l.eventName === 'listing-updated');
    expect(msgListener).toBeDefined();

    // Remove child from DOM — this makes api.bodyContains(child) return
    // false when the listener fires, triggering self-removal.
    parent.removeChild(child);

    // Fire the listener — should detect body no longer contains child,
    // then self-remove via source.removeEventListener
    msgListener!.listener({ data: 'test' });

    // The listener should have been removed from the EventSource
    expect(mockEventSource.removeEventListener).toHaveBeenCalledWith(
      'listing-updated',
      expect.any(Function),
    );

    document.body.removeChild(parent);
  });
});

// ═══════════════════════════════════════════════════════════════════════════════
// Suite 5: HTMX Event Dispatching via hx-trigger="sse:*"
// ═══════════════════════════════════════════════════════════════════════════════

describe('HTMX event dispatching (hx-trigger="sse:*")', () => {
  beforeEach(() => {
    setupHtmxMock();
    loadSseJs();
  });

  afterEach(() => {
    teardownHtmxMock();
  });

  it('registers SSE listeners for hx-trigger="sse:eventname"', () => {
    const parent = document.createElement('div');
    parent.setAttribute('sse-connect', '/events');
    document.body.appendChild(parent);

    const child = document.createElement('div');
    child.setAttribute('hx-trigger', 'sse:listing-updated');
    parent.appendChild(child);

    const ext = getExtension();
    ext.onEvent('htmx:afterProcessNode', { target: parent });

    const parentData = getFakeInternalData(parent);
    parentData.sseEventSource = mockEventSource;

    ext.onEvent('htmx:afterProcessNode', { target: child });

    // Should have registered a listener for 'listing-updated' on the EventSource
    expect(mockEventSource.addEventListener).toHaveBeenCalledWith(
      'listing-updated',
      expect.any(Function),
    );
  });

  it('triggers htmx.sseMessage on received SSE message through hx-trigger', () => {
    const parent = document.createElement('div');
    parent.setAttribute('sse-connect', '/events');
    document.body.appendChild(parent);

    const child = document.createElement('div');
    child.setAttribute('hx-trigger', 'sse:listing-updated');
    parent.appendChild(child);

    const ext = getExtension();
    ext.onEvent('htmx:afterProcessNode', { target: parent });

    const parentData = getFakeInternalData(parent);
    parentData.sseEventSource = mockEventSource;

    ext.onEvent('htmx:afterProcessNode', { target: child });

    // Find the listener
    const msgListener = listeners.find((l) => l.eventName === 'listing-updated');
    expect(msgListener).toBeDefined();

    // Fire it
    msgListener!.listener({ data: 'event data' });

    // Should trigger htmx.sseMessage
    const sseMsgEvents = triggeredEvents.filter((e) => e.eventName === 'htmx:sseMessage');
    expect(sseMsgEvents.length).toBe(1);

    // Clean up
    document.body.removeChild(parent);
  });
});
