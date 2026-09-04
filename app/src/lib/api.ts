// Typed fetch helpers for the Svelte islands (spec B4 "Shared").
// One place for: JSON fetch with a single 500 ms-backoff retry, 404-tolerant
// reads, and the Listings URL↔filter-state mapping (the URL is the state:
// `?collection=&min=&max=&sort=&page=`).
import { toWei } from './format';

export class ApiError extends Error {
  constructor(public status: number, message?: string) {
    super(message ?? `HTTP ${status}`);
    this.name = 'ApiError';
  }
}

const sleep = (ms: number) => new Promise((r) => setTimeout(r, ms));

/**
 * GET `url` as JSON. Retries ONCE after 500 ms on a network failure or a 5xx
 * (4xx is the server speaking clearly — no retry). Throws ApiError on !ok.
 */
export async function json<T>(url: string, init?: RequestInit, retries = 1): Promise<T> {
  let res: Response;
  try {
    res = await fetch(url, init);
  } catch (e) {
    if (retries > 0) { await sleep(500); return json<T>(url, init, retries - 1); }
    throw e;
  }
  if (res.status >= 500 && retries > 0) { await sleep(500); return json<T>(url, init, retries - 1); }
  if (!res.ok) throw new ApiError(res.status);
  return (await res.json()) as T;
}

/** json<T> that resolves null instead of throwing (404, 5xx after retry, network). */
export async function jsonOrNull<T>(url: string, init?: RequestInit): Promise<T | null> {
  try { return await json<T>(url, init); } catch { return null; }
}

// ── Listings URL state ────────────────────────────────────────────────────

export const LISTINGS_SORTS = ['recent', 'price_asc', 'price_desc', 'ending'] as const;
export type ListingsSort = (typeof LISTINGS_SORTS)[number];

export interface ListingsFilterState {
  /** 0x… collection address or ''. */
  collection: string;
  /** Human decimal price (e.g. "12.5"), NOT wei. '' = unset. */
  min: string;
  max: string;
  sort: ListingsSort;
  /** 1-based. */
  page: number;
  /** `trait:value,trait:value` (kept out of the canonical URL contract but round-tripped). */
  traits: string;
  /** Session-only "Your listings" toggle — never serialized into the URL. */
  seller: string;
}

export const EMPTY_FILTERS: ListingsFilterState = Object.freeze({
  collection: '', min: '', max: '', sort: 'recent', page: 1, traits: '', seller: '',
});

const ADDR_RE = /^0x[0-9a-fA-F]{40}$/;
const DEC_RE = /^\d+(\.\d+)?$/;

export function hasActiveFilters(f: ListingsFilterState): boolean {
  return !!(f.collection || f.min || f.max || f.traits || f.seller);
}

/**
 * Parse `?collection=&min=&max=&sort=&page=` into filter state. Invalid
 * values are IGNORED (spec: ignored + toast); their param names come back in
 * `invalid` so the page can say so once.
 */
export function parseListingsParams(search: string): { filters: ListingsFilterState; invalid: string[] } {
  const p = new URLSearchParams(search);
  const f: ListingsFilterState = { ...EMPTY_FILTERS };
  const invalid: string[] = [];
  const coll = p.get('collection');
  if (coll) { if (ADDR_RE.test(coll)) f.collection = coll.toLowerCase(); else invalid.push('collection'); }
  for (const k of ['min', 'max'] as const) {
    const v = p.get(k);
    if (v) { if (DEC_RE.test(v)) f[k] = v; else invalid.push(k); }
  }
  const sort = p.get('sort');
  if (sort) {
    if ((LISTINGS_SORTS as readonly string[]).includes(sort)) f.sort = sort as ListingsSort;
    else invalid.push('sort');
  }
  const page = p.get('page');
  if (page) {
    const n = Number(page);
    if (Number.isInteger(n) && n >= 1 && n <= 10_000) f.page = n; else invalid.push('page');
  }
  const traits = p.get('traits');
  if (traits) {
    if (f.collection && /^[^:,]+:[^:,]+(,[^:,]+:[^:,]+)*$/.test(traits)) f.traits = traits;
    else invalid.push('traits');
  }
  return { filters: f, invalid };
}

/** Serialize state back to the canonical `?collection=&min=&max=&sort=&page=` (defaults omitted). */
export function listingsSearch(f: ListingsFilterState): string {
  const p = new URLSearchParams();
  if (f.collection) p.set('collection', f.collection);
  if (f.min) p.set('min', f.min);
  if (f.max) p.set('max', f.max);
  if (f.sort !== 'recent') p.set('sort', f.sort);
  if (f.page > 1) p.set('page', String(f.page));
  if (f.traits) p.set('traits', f.traits);
  const s = p.toString();
  return s ? `?${s}` : '';
}

/**
 * Query params for GET /api/v1/listings for ONE page of the current state
 * (the backend takes wei min_price/max_price; the URL carries human units).
 */
export function listingsApiParams(f: ListingsFilterState, page: number, limit = 48): URLSearchParams {
  const p = new URLSearchParams({ limit: String(limit), sort: f.sort });
  if (page > 1) p.set('page', String(page));
  if (f.collection) p.set('collection', f.collection);
  if (f.seller) p.set('seller', f.seller);
  if (f.traits) p.set('traits', f.traits);
  for (const [k, api] of [['min', 'min_price'], ['max', 'max_price']] as const) {
    const v = f[k];
    if (!v) continue;
    try { p.set(api, toWei(v).toString()); } catch { /* validated upstream; skip */ }
  }
  return p;
}

/** The window event ListingsFilters emits and NFTGrid listens for. */
export const FILTERS_EVENT = 'mw-listings-filters';
/** Emitted by NFTGrid's "Clear filters" empty-state CTA. */
export const CLEAR_FILTERS_EVENT = 'mw-listings-clear';

export function emitFilters(f: ListingsFilterState): void {
  if (typeof window === 'undefined') return;
  window.dispatchEvent(new CustomEvent<ListingsFilterState>(FILTERS_EVENT, { detail: { ...f } }));
}
