// /metrics time-range filter (spec B4 "Metrics": [24h · 7d · 30d] applied
// client-side over /api/v1/activity).
import { describe, it, expect } from 'vitest';
import { RANGE_KEYS, RANGE_SECONDS, rowTimeMs, filterByRange } from '../pages/metrics/_range';

const NOW = Date.UTC(2026, 8, 4, 12, 0, 0); // fixed clock

const at = (secAgo: number) => ({ ts: Math.floor(NOW / 1000) - secAgo });

describe('rowTimeMs', () => {
  it('reads ts as unix seconds, unix ms, or ISO — and legacy timestamp', () => {
    expect(rowTimeMs({ ts: 1_700_000_000 })).toBe(1_700_000_000_000);
    expect(rowTimeMs({ ts: 1_700_000_000_000 })).toBe(1_700_000_000_000);
    expect(rowTimeMs({ ts: '1700000000' })).toBe(1_700_000_000_000);
    expect(rowTimeMs({ ts: '2026-09-04T00:00:00Z' })).toBe(Date.UTC(2026, 8, 4));
    expect(rowTimeMs({ timestamp: '2026-09-04T00:00:00Z' })).toBe(Date.UTC(2026, 8, 4));
  });
  it('returns null for unusable values', () => {
    expect(rowTimeMs({})).toBeNull();
    expect(rowTimeMs({ ts: '' })).toBeNull();
    expect(rowTimeMs({ ts: 'not a date' })).toBeNull();
    expect(rowTimeMs({ ts: 0 })).toBeNull();
  });
  it('ts wins over timestamp when both exist', () => {
    expect(rowTimeMs({ ts: 1_700_000_000, timestamp: '2020-01-01T00:00:00Z' })).toBe(1_700_000_000_000);
  });
});

describe('filterByRange', () => {
  it('exposes exactly the three spec ranges', () => {
    expect(RANGE_KEYS).toEqual(['24h', '7d', '30d']);
    expect(RANGE_SECONDS['24h']).toBe(86_400);
    expect(RANGE_SECONDS['7d']).toBe(7 * 86_400);
    expect(RANGE_SECONDS['30d']).toBe(30 * 86_400);
  });

  it('keeps only rows inside the window', () => {
    const rows = [at(60), at(2 * 86_400), at(10 * 86_400), at(40 * 86_400)];
    expect(filterByRange(rows, '24h', NOW)).toEqual([rows[0]]);
    expect(filterByRange(rows, '7d', NOW)).toEqual([rows[0], rows[1]]);
    expect(filterByRange(rows, '30d', NOW)).toEqual([rows[0], rows[1], rows[2]]);
  });

  it('a row exactly on the cutoff stays in', () => {
    const rows = [at(86_400)];
    expect(filterByRange(rows, '24h', NOW)).toEqual(rows);
  });

  it('rows without a readable time are kept, never hidden', () => {
    const rows = [{ ts: null }, { type: 'Sold' } as { ts?: number }, at(40 * 86_400)];
    expect(filterByRange(rows, '24h', NOW)).toEqual([rows[0], rows[1]]);
  });
});
