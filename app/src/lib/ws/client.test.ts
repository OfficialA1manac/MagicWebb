import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { MwSocket } from './client';

class MockWS {
  static instances: MockWS[] = [];
  static OPEN = 1; static CONNECTING = 0; static CLOSED = 3;
  readyState = 0;
  sent: string[] = [];
  onopen: (() => void) | null = null;
  onmessage: ((e: { data: string }) => void) | null = null;
  onclose: (() => void) | null = null;
  onerror: (() => void) | null = null;
  constructor(public url: string) { MockWS.instances.push(this); }
  send(s: string) { this.sent.push(s); }
  close() { this.readyState = 3; this.onclose?.(); }
  open() { this.readyState = 1; this.onopen?.(); }
  push(obj: unknown) { this.onmessage?.({ data: JSON.stringify(obj) }); }
  frames() { return this.sent.map((s) => JSON.parse(s)); }
}

let orig: typeof WebSocket;
beforeEach(() => { orig = globalThis.WebSocket; (globalThis as unknown as { WebSocket: unknown }).WebSocket = MockWS; MockWS.instances = []; vi.useFakeTimers(); });
afterEach(() => { globalThis.WebSocket = orig; vi.useRealTimers(); });

describe('MwSocket', () => {
  it('subscribes on open and re-subscribes after reconnect', () => {
    const s = new MwSocket('ws://x/ws');
    s.subscribe('token:0xabc:1', 'activity');
    const w1 = MockWS.instances[0]; w1.open();
    expect(w1.frames()[0]).toEqual({ type: 'subscribe', data: { channels: ['token:0xabc:1', 'activity'] } });
    w1.close();
    vi.advanceTimersByTime(2000);
    const w2 = MockWS.instances[1]; expect(w2).toBeDefined(); w2.open();
    expect(w2.frames()[0]).toEqual({ type: 'subscribe', data: { channels: ['token:0xabc:1', 'activity'] } });
  });

  it('dispatches typed events and bridges to window mw-ws-event', () => {
    const s = new MwSocket('ws://x/ws');
    const got: unknown[] = [];
    s.on('listing-updated', (d) => got.push(d));
    const legacy = vi.fn(); window.addEventListener('mw-ws-event', legacy);
    const w = MockWS.instances[0]; w.open();
    w.push({ type: 'listing-updated', seq: 1, data: { collection: '0xabc' } });
    w.push({ type: 'subscribed', data: { channels: [] } }); // control frame ignored
    expect(got).toEqual([{ collection: '0xabc' }]);
    expect(legacy).toHaveBeenCalledTimes(1);
  });

  it('detects a seq gap and asks for replay from last+1', () => {
    const s = new MwSocket('ws://x/ws');
    s.on('activity', () => {});
    const w = MockWS.instances[0]; w.open();
    w.push({ type: 'activity', seq: 5, data: {} });
    w.push({ type: 'activity', seq: 9, data: {} });
    expect(w.frames().some((f) => f.type === 'retry' && f.data.from_seq === 6)).toBe(true);
    expect(s.lastSequence).toBe(9);
  });

  it('waitFor resolves on a matching event and unsubscribes the temp channel', async () => {
    const s = new MwSocket('ws://x/ws');
    const w0 = MockWS.instances[0] ?? (s.connect(), MockWS.instances[0]);
    const p = s.waitFor('tx-indexed', (d) => (d as { tx_hash: string }).tx_hash === '0xaa', 5000, 'tx:0xaa');
    const w = MockWS.instances[0]; w.open();
    expect(s.subscribedChannels).toContain('tx:0xaa');
    w.push({ type: 'tx-indexed', seq: 1, data: { tx_hash: '0xbb' } });
    w.push({ type: 'tx-indexed', seq: 2, data: { tx_hash: '0xaa' } });
    await expect(p).resolves.toEqual({ tx_hash: '0xaa' });
    expect(s.subscribedChannels).not.toContain('tx:0xaa');
    void w0;
  });

  it('waitFor rejects on timeout', async () => {
    const s = new MwSocket('ws://x/ws');
    const p = s.waitFor('tx-indexed', () => true, 1000);
    MockWS.instances[0].open();
    vi.advanceTimersByTime(1001);
    await expect(p).rejects.toThrow('timeout');
  });
});
