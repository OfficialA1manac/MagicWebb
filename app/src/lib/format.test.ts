import { describe, it, expect } from 'vitest';
import { esc, fmtPrice, toWei, shortAddr, fmtCountdown } from './format';

describe('esc', () => {
  it('escapes all five HTML-significant characters, including single quotes', () => {
    expect(esc(`<a href="x" onclick='y'>&</a>`)).toBe('&lt;a href=&quot;x&quot; onclick=&#39;y&#39;&gt;&amp;&lt;/a&gt;');
  });
  it('handles null/undefined/numbers', () => {
    expect(esc(null)).toBe(''); expect(esc(undefined)).toBe(''); expect(esc(42)).toBe('42');
  });
});

describe('fmtPrice', () => {
  it('trims trailing zeros and groups thousands', () => {
    expect(fmtPrice('1000000000000000000')).toBe('1');
    expect(fmtPrice('12500000000000000000')).toBe('12.5');
    expect(fmtPrice(1234567n * 10n ** 18n)).toBe('1,234,567');
    expect(fmtPrice('1234500000000000000', 2)).toBe('1.23');
  });
  it('returns a dash for garbage', () => { expect(fmtPrice('abc')).toBe('—'); expect(fmtPrice(null)).toBe('—'); });
});

describe('toWei', () => {
  it('parses decimals', () => { expect(toWei('12.5')).toBe(12500000000000000000n); expect(toWei(' 1 ')).toBe(10n ** 18n); });
  it('rejects non-numbers', () => { expect(() => toWei('1e5')).toThrow(); expect(() => toWei('')).toThrow(); expect(() => toWei('.')).toThrow(); });
});

describe('shortAddr / fmtCountdown', () => {
  it('shortens', () => { expect(shortAddr('0x34b53209eC694Ce243e28606233CFc72D0673436')).toBe('0x34b5…3436'); expect(shortAddr('')).toBe(''); });
  it('counts down', () => {
    const now = 1_700_000_000_000;
    expect(fmtCountdown(1_700_000_000 + 3723, now)).toBe('1h 02m 03s');
    expect(fmtCountdown(1_700_000_000 + 59, now)).toBe('0m 59s');
    expect(fmtCountdown(1_700_000_000 - 1, now)).toBe('Ended');
    expect(fmtCountdown(1_700_000_000 + 3 * 86400, now)).toBe('3d 0h');
  });
});
