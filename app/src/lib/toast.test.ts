// @vitest-environment jsdom
// The mw-toast bridge must not lose events dispatched before the toast island
// hydrated: BaseLayout's inline script queues them on window.__mwToastQ and
// installToastBridge() drains that queue on mount (the wave-7 "unreachable
// sibling" flake: both toasts fired into the void while .toasts was attached
// but not yet hydrated).
import { describe, it, expect, beforeEach } from 'vitest';
import { toasts, clearToasts, installToastBridge } from './toast.svelte';

type W = { __mwToastQ?: unknown[]; __mwToastLive?: boolean };
const w = () => window as unknown as W;

describe('mw-toast bridge', () => {
  beforeEach(() => {
    clearToasts();
    w().__mwToastQ = undefined;
    w().__mwToastLive = undefined;
  });

  it('drains events queued before the island mounted, in order, then goes live', () => {
    w().__mwToastQ = [
      { message: 'Opening Songbird…', variant: 'info' },
      { message: 'Songbird site is unreachable right now — try again in a minute', variant: 'error' },
    ];
    const off = installToastBridge();
    expect(toasts.map((t) => t.message)).toEqual([
      'Opening Songbird…',
      'Songbird site is unreachable right now — try again in a minute',
    ]);
    expect(toasts[1].variant).toBe('error');
    expect(w().__mwToastQ).toEqual([]);
    expect(w().__mwToastLive).toBe(true);

    window.dispatchEvent(new CustomEvent('mw-toast', { detail: { message: 'live', variant: 'success' } }));
    expect(toasts.map((t) => t.message)).toContain('live');

    off();
    expect(w().__mwToastLive).toBe(false);
  });

  it('works without the inline queue (string-built pages, tests)', () => {
    const off = installToastBridge();
    window.dispatchEvent(new CustomEvent('mw-toast', { detail: { message: 'hello' } }));
    expect(toasts.map((t) => t.message)).toEqual(['hello']);
    off();
  });

  it('ignores queued entries without a message', () => {
    w().__mwToastQ = [{}, { variant: 'info' }, { message: 'ok' }];
    const off = installToastBridge();
    expect(toasts.map((t) => t.message)).toEqual(['ok']);
    off();
  });
});
