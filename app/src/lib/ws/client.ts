// The one WebSocket client for the UI. Talks to GET /ws on the same origin
// (each network is its own origin, so the socket is always the right chain).
//
// Wire format (backend/internal/ws/message.go):
//   client → {type:"subscribe"|"unsubscribe", data:{channels:[...]}}
//            {type:"ping"}  {type:"retry", data:{from_seq:N}}
//   server → {type:"listing-updated"|"auction-updated"|"offer-updated"|
//             "notification"|"activity"|"tx-indexed", seq:N, data:{...}}
//            {type:"subscribed"|"pong"|"ack"|"error"|"replay_complete", data:{...}}

export type WsEventType = 'listing-updated' | 'auction-updated' | 'offer-updated' | 'notification' | 'activity' | 'tx-indexed';
export type WsStatus = 'idle' | 'connecting' | 'open' | 'closed';

type Handler = (data: unknown, meta: { type: WsEventType; seq?: number }) => void;

const EVENT_TYPES: ReadonlySet<string> = new Set(['listing-updated', 'auction-updated', 'offer-updated', 'notification', 'activity', 'tx-indexed']);

export class MwSocket {
  private sock: WebSocket | null = null;
  private url: string;
  private channels = new Set<string>();
  private handlers = new Map<string, Set<Handler>>();
  private statusHandlers = new Set<(s: WsStatus) => void>();
  private lastSeq = 0;
  private attempt = 0;
  private timer: ReturnType<typeof setTimeout> | null = null;
  private pingTimer: ReturnType<typeof setInterval> | null = null;
  private closedByUser = false;
  status: WsStatus = 'idle';

  constructor(url?: string) {
    this.url = url ?? (typeof location !== 'undefined' ? `${location.origin.replace(/^http/, 'ws')}/ws` : 'ws://localhost:8080/ws');
  }

  /** Idempotent. Safe to call from every page. */
  connect(): void {
    if (typeof WebSocket === 'undefined') return;
    if (this.sock && (this.sock.readyState === WebSocket.OPEN || this.sock.readyState === WebSocket.CONNECTING)) return;
    this.closedByUser = false;
    this.setStatus('connecting');
    let s: WebSocket;
    try { s = new WebSocket(this.url); } catch { this.scheduleReconnect(); return; }
    this.sock = s;
    s.onopen = () => {
      this.attempt = 0;
      this.setStatus('open');
      if (this.channels.size) this.send({ type: 'subscribe', data: { channels: [...this.channels] } });
      if (this.lastSeq > 0) this.send({ type: 'retry', data: { from_seq: this.lastSeq + 1 } });
      this.pingTimer = setInterval(() => this.send({ type: 'ping' }), 25_000);
    };
    s.onmessage = (ev) => this.onFrame(typeof ev.data === 'string' ? ev.data : '');
    s.onclose = () => { this.cleanup(); this.setStatus('closed'); if (!this.closedByUser) this.scheduleReconnect(); };
    s.onerror = () => { /* onclose follows */ };
  }

  close(): void { this.closedByUser = true; this.cleanup(); this.sock?.close(); this.sock = null; this.setStatus('closed'); }

  subscribe(...chs: string[]): void {
    const fresh = chs.filter((c) => c && !this.channels.has(c));
    for (const c of fresh) this.channels.add(c);
    if (fresh.length && this.isOpen()) this.send({ type: 'subscribe', data: { channels: fresh } });
    this.connect();
  }

  unsubscribe(...chs: string[]): void {
    const gone = chs.filter((c) => this.channels.delete(c));
    if (gone.length && this.isOpen()) this.send({ type: 'unsubscribe', data: { channels: gone } });
  }

  on(type: WsEventType | '*', fn: Handler): () => void {
    const set = this.handlers.get(type) ?? new Set<Handler>();
    set.add(fn); this.handlers.set(type, set);
    this.connect();
    return () => { set.delete(fn); };
  }

  onStatus(fn: (s: WsStatus) => void): () => void {
    this.statusHandlers.add(fn); fn(this.status);
    return () => { this.statusHandlers.delete(fn); };
  }

  /**
   * Resolve with the first event of `type` matching `pred`, or reject after
   * `timeoutMs`. Optionally subscribes to `channel` for the wait and
   * unsubscribes afterwards.
   */
  waitFor(type: WsEventType, pred: (data: unknown) => boolean, timeoutMs: number, channel?: string): Promise<unknown> {
    if (channel) this.subscribe(channel);
    return new Promise((res, rej) => {
      const done = (fn: () => void) => { off(); clearTimeout(t); if (channel) this.unsubscribe(channel); fn(); };
      const off = this.on(type, (data) => { try { if (pred(data)) done(() => res(data)); } catch { /* ignore */ } });
      const t = setTimeout(() => done(() => rej(new Error('timeout'))), timeoutMs);
    });
  }

  /** Test hook / diagnostics. */
  get subscribedChannels(): string[] { return [...this.channels]; }
  get lastSequence(): number { return this.lastSeq; }

  // ── internals ──────────────────────────────────────────────────────────
  private isOpen() { return !!this.sock && this.sock.readyState === WebSocket.OPEN; }

  private send(msg: unknown) { if (this.isOpen()) { try { this.sock!.send(JSON.stringify(msg)); } catch { /* closed */ } } }

  private onFrame(raw: string) {
    let msg: { type?: string; seq?: number; data?: unknown };
    try { msg = JSON.parse(raw); } catch { return; }
    const type = msg.type ?? '';
    if (!EVENT_TYPES.has(type)) return; // control frames: subscribed/pong/ack/error/replay_complete
    if (typeof msg.seq === 'number' && msg.seq > 0) {
      if (this.lastSeq > 0 && msg.seq > this.lastSeq + 1) this.send({ type: 'retry', data: { from_seq: this.lastSeq + 1 } });
      if (msg.seq > this.lastSeq) this.lastSeq = msg.seq;
    }
    const meta = { type: type as WsEventType, seq: msg.seq };
    for (const fn of this.handlers.get(type) ?? []) { try { fn(msg.data, meta); } catch (e) { console.error('[mw-ws] handler', e); } }
    for (const fn of this.handlers.get('*') ?? []) { try { fn(msg.data, meta); } catch (e) { console.error('[mw-ws] handler', e); } }
    // Legacy bridge for inline page scripts that still listen on window.
    if (typeof window !== 'undefined') window.dispatchEvent(new CustomEvent('mw-ws-event', { detail: { type, data: msg.data, seq: msg.seq } }));
  }

  private scheduleReconnect() {
    if (this.timer) return;
    const delay = Math.min(30_000, 500 * 2 ** Math.min(this.attempt++, 6)) + Math.random() * 250;
    this.timer = setTimeout(() => { this.timer = null; this.connect(); }, delay);
  }

  private cleanup() {
    if (this.pingTimer) { clearInterval(this.pingTimer); this.pingTimer = null; }
  }

  private setStatus(s: WsStatus) {
    this.status = s;
    for (const fn of this.statusHandlers) { try { fn(s); } catch { /* ignore */ } }
    if (typeof window !== 'undefined' && s === 'open') window.dispatchEvent(new CustomEvent('mw-ws-open'));
  }
}

/** Page-wide singleton. */
export const ws: MwSocket = (typeof window !== 'undefined'
  ? ((window as unknown as { __MW_WS__?: MwSocket }).__MW_WS__ ??= new MwSocket())
  : new MwSocket());
