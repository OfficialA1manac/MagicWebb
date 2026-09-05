// Time-range filtering for the /metrics activity feed (spec B4 "Metrics"):
// segmented control [24h · 7d · 30d], applied client-side over
// GET /api/v1/activity. Underscore prefix keeps Astro from routing this file.

export const RANGE_KEYS = ['24h', '7d', '30d'] as const;
export type RangeKey = (typeof RANGE_KEYS)[number];

export const RANGE_SECONDS: Record<RangeKey, number> = {
  '24h': 24 * 60 * 60,
  '7d': 7 * 24 * 60 * 60,
  '30d': 30 * 24 * 60 * 60,
};

export interface TimedRow {
  /** New A3 field (unix seconds, unix ms, or ISO string). */
  ts?: string | number | null;
  /** Legacy field name on older rows. */
  timestamp?: string | number | null;
}

/**
 * Best-effort event time in epoch ms; null when the row carries no usable
 * time. Numeric values below 1e12 are treated as unix SECONDS.
 */
export function rowTimeMs(row: TimedRow): number | null {
  const v = row.ts ?? row.timestamp;
  if (v === null || v === undefined || v === '') return null;
  if (typeof v === 'number') {
    if (!Number.isFinite(v) || v <= 0) return null;
    return v < 1e12 ? v * 1000 : v;
  }
  const s = String(v).trim();
  if (/^\d+$/.test(s)) {
    const n = Number(s);
    return n <= 0 ? null : n < 1e12 ? n * 1000 : n;
  }
  const parsed = Date.parse(s);
  return Number.isFinite(parsed) ? parsed : null;
}

/**
 * Rows inside the window. Rows with no readable time are KEPT — hiding an
 * event because its timestamp failed to parse would misreport activity.
 */
export function filterByRange<T extends TimedRow>(rows: T[], range: RangeKey, nowMs = Date.now()): T[] {
  const cutoff = nowMs - RANGE_SECONDS[range] * 1000;
  return rows.filter((r) => {
    const t = rowTimeMs(r);
    return t === null || t >= cutoff;
  });
}
