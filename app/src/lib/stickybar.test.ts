// @vitest-environment jsdom
import { describe, it, expect } from 'vitest';
import { bindStickyBarHeight, STICKY_BAR_VAR } from './stickybar';

describe('bindStickyBarHeight', () => {
  it('publishes the bar height while bound and resets to 0px on cleanup', () => {
    const el = document.createElement('div');
    document.body.appendChild(el);
    el.getBoundingClientRect = () => ({ height: 64, width: 390, top: 0, left: 0, right: 390, bottom: 64, x: 0, y: 0, toJSON() {} }) as DOMRect;
    const off = bindStickyBarHeight(el);
    expect(document.documentElement.style.getPropertyValue(STICKY_BAR_VAR)).toBe('64px');
    off();
    expect(document.documentElement.style.getPropertyValue(STICKY_BAR_VAR)).toBe('0px');
    el.remove();
  });

  it('reports 0px for a bar hidden by CSS (desktop)', () => {
    const el = document.createElement('div');
    el.style.display = 'none';
    document.body.appendChild(el);
    const off = bindStickyBarHeight(el);
    expect(document.documentElement.style.getPropertyValue(STICKY_BAR_VAR)).toBe('0px');
    off();
    el.remove();
  });
});
