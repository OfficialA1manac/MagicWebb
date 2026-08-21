// Shared formatting helpers. Every page used to redefine esc() — and most
// of those copies forgot the single quote, which matters because values are
// interpolated into single-quoted inline style/onclick attributes.
import { formatUnits, parseUnits } from 'viem';

const ESC: Record<string, string> = { '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' };
export function esc(v: unknown): string {
  return String(v ?? '').replace(/[&<>"']/g, (c) => ESC[c]);
}

export function shortAddr(a: string | null | undefined, head = 6, tail = 4): string {
  if (!a) return '';
  return a.length <= head + tail + 2 ? a : `${a.slice(0, head)}…${a.slice(-tail)}`;
}

/** Wei (string|bigint) → human string with up to `maxFrac` decimals, trailing zeros trimmed. */
export function fmtPrice(wei: string | bigint | null | undefined, maxFrac = 4): string {
  if (wei === null || wei === undefined || wei === '') return '—';
  let n: bigint;
  try { n = typeof wei === 'bigint' ? wei : BigInt(wei); } catch { return '—'; }
  const s = formatUnits(n, 18);
  const [int, frac = ''] = s.split('.');
  const f = frac.slice(0, maxFrac).replace(/0+$/, '');
  const intFmt = int.replace(/\B(?=(\d{3})+(?!\d))/g, ',');
  return f ? `${intFmt}.${f}` : intFmt;
}

/** Human decimal string → wei bigint. Throws on garbage. */
export function toWei(v: string): bigint {
  const t = v.trim();
  if (!/^\d*\.?\d*$/.test(t) || t === '' || t === '.') throw new Error('Enter a number');
  return parseUnits(t, 18);
}

export function timeAgo(ts: string | number | Date): string {
  const d = ts instanceof Date ? ts : new Date(ts);
  const s = Math.max(0, Math.floor((Date.now() - d.getTime()) / 1000));
  if (s < 60) return 'just now';
  const m = Math.floor(s / 60); if (m < 60) return `${m}m ago`;
  const h = Math.floor(m / 60); if (h < 24) return `${h}h ago`;
  const dd = Math.floor(h / 24); if (dd < 30) return `${dd}d ago`;
  return d.toLocaleDateString();
}

/** Seconds remaining → "2h 03m 12s" / "Ended". */
export function fmtCountdown(endsAtSec: number, nowMs = Date.now()): string {
  const left = Math.floor(endsAtSec - nowMs / 1000);
  if (left <= 0) return 'Ended';
  const h = Math.floor(left / 3600), m = Math.floor((left % 3600) / 60), s = left % 60;
  const p = (n: number) => String(n).padStart(2, '0');
  if (h >= 48) return `${Math.floor(h / 24)}d ${h % 24}h`;
  return h > 0 ? `${h}h ${p(m)}m ${p(s)}s` : `${m}m ${p(s)}s`;
}
