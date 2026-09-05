// Pure helpers for the ProfilePage island (spec B4 "Profile"). Everything a
// unit test needs to assert — address resolution, tab labels, batch
// eligibility, merge/dedupe, the wallet-change debounce/overlap guard, and
// the per-address data snapshot — lives here with no Svelte or DOM coupling.
import { toWei } from '../lib/format';

export const ADDR_RE = /^0x[0-9a-fA-F]{40}$/;

export function isEthAddr(s: unknown): s is string {
  return typeof s === 'string' && ADDR_RE.test(s);
}

/** Address from a /profile/:addr path, lowercased; '' when absent/invalid. */
export function pathAddr(pathname: string): string {
  const p = decodeURIComponent(String(pathname || ''))
    .replace(/^\/profile\/?/, '')
    .replace(/\/+$/, '');
  return isEthAddr(p) ? p.toLowerCase() : '';
}

/**
 * Which profile this page shows: the URL address wins (viewing someone), the
 * stored/connected wallet is the fallback (own profile), '' means neither.
 */
export function resolveProfileAddr(pathname: string, stored: string | null | undefined): { target: string; fromPath: boolean } {
  const fromPath = pathAddr(pathname);
  if (fromPath) return { target: fromPath, fromPath: true };
  const s = typeof stored === 'string' && isEthAddr(stored) ? stored.toLowerCase() : '';
  return { target: s, fromPath: false };
}

// ── Tabs ──────────────────────────────────────────────────────────────────

export type ProfileTabId = 'items' | 'sale' | 'auctions' | 'offers' | 'activity';
export interface ProfileTab { id: ProfileTabId; label: string }

/** Spec: `Items · For sale · Auctions · Offers · Activity`, "Your items" only on own profile. */
export function tabsFor(own: boolean): ProfileTab[] {
  return [
    { id: 'items', label: own ? 'Your items' : 'Items' },
    { id: 'sale', label: 'For sale' },
    { id: 'auctions', label: 'Auctions' },
    { id: 'offers', label: 'Offers' },
    { id: 'activity', label: 'Activity' },
  ];
}

export interface TabEmpty {
  title: string;
  body?: string;
  cta?: { label: string; href: string };
}

/** Per-tab empty copy (spec), each with one action where sensible. */
export function emptyFor(tab: ProfileTabId, own: boolean): TabEmpty {
  switch (tab) {
    case 'items':
      return own
        ? { title: 'No items yet', body: 'Every NFT this wallet holds shows up here — new mints and transfers can take a minute to appear.', cta: { label: 'Browse listings', href: '/listings' } }
        : { title: 'No items yet', body: 'Every NFT this wallet holds shows up here.' };
    case 'sale':
      return own
        ? { title: 'Nothing for sale', body: 'Listing is free — you only pay 2% when it sells.', cta: { label: 'List an NFT', href: '#items' } }
        : { title: 'Nothing for sale' };
    case 'auctions':
      return own
        ? { title: 'No auctions', body: "Start one from any NFT you own — it's free.", cta: { label: 'Your items', href: '#items' } }
        : { title: 'No auctions' };
    case 'offers':
      return own
        ? { title: 'No offers', body: 'Offers you make and offers on your NFTs appear here.', cta: { label: 'Browse listings', href: '/listings' } }
        : { title: 'No offers' };
    case 'activity':
      return { title: 'No activity yet', body: 'Sales, listings, bids and offers show up here the moment they happen.' };
  }
}

// ── Inventory merge + batch listing ───────────────────────────────────────

export interface InventoryItem {
  collection?: string;
  token_id?: string;
  tokenID?: string;
  name?: string;
  image_uri?: string;
  price_wei?: string;
  standard?: string;
  collection_verified?: boolean;
  collection_creator?: string;
  collection_name?: string;
  collection_tracked?: boolean;
  /** Escrowed in the AuctionHouse — the wallet cannot move it. */
  _escrowed?: boolean;
  [k: string]: unknown;
}

export function itemKey(it: InventoryItem): string {
  return `${(it.collection || '').toLowerCase()}:${it.token_id ?? it.tokenID ?? ''}`;
}

/**
 * Wallet-held + listed + auctioned NFTs, deduped by collection:token_id.
 * Priority wallet > listing > auction; auction rows are marked `_escrowed`
 * so the grid never offers to (batch-)list a token the wallet cannot move.
 */
export function mergeInventory(nfts: InventoryItem[], listings: InventoryItem[], auctions: InventoryItem[]): InventoryItem[] {
  const seen = new Set<string>();
  const out: InventoryItem[] = [];
  const push = (arr: InventoryItem[], mark?: (it: InventoryItem) => InventoryItem) => {
    for (const it of arr || []) {
      const key = itemKey(it);
      if (seen.has(key)) continue;
      seen.add(key);
      out.push(mark ? mark(it) : it);
    }
  };
  push(nfts);
  push(listings);
  push(auctions, (it) => ({ ...it, _escrowed: true }));
  return out;
}

export function isErc1155(it: InventoryItem): boolean {
  return String(it.standard || '').toLowerCase() === 'erc1155';
}

/**
 * Batch-select eligibility (spec: ERC-721 only, own profile, not already
 * listed, not escrowed in an auction). A missing `standard` is treated as
 * ERC-721 — wallet rows predating the A3 fields are all 721.
 */
export function batchEligible(it: InventoryItem, own: boolean): boolean {
  if (!own) return false;
  if (isErc1155(it)) return false;
  if (it.price_wei) return false; // already listed
  if (it._escrowed) return false;
  return true;
}

export const HINT_1155 = 'List multi-edition items one at a time';
export const BATCH_MAX = 50;
export const MIN_PRICE_WEI = 1_000_000_000_000_000_000n; // 1 native token

/** Sticky-bar label (spec): `List 3 selected — one price, one duration`. */
export function batchBarLabel(n: number): string {
  return `List ${n} selected — one price, one duration`;
}

export type BatchValidation = { ok: true; wei: bigint } | { ok: false; error: string };

export function validateBatch(count: number, priceStr: string, currency = 'C2FLR'): BatchValidation {
  if (count <= 0) return { ok: false, error: 'Select at least one NFT.' };
  if (count > BATCH_MAX) return { ok: false, error: `Pick at most ${BATCH_MAX}.` };
  let wei: bigint;
  try { wei = toWei(priceStr); } catch { return { ok: false, error: 'Enter a price like 12.5' }; }
  if (wei < MIN_PRICE_WEI) return { ok: false, error: `Minimum price is 1 ${currency}.` };
  return { ok: true, wei };
}

// ── Header bits ───────────────────────────────────────────────────────────

/** Avatar initials: first two useful characters of the name or address. */
export function initialsFor(nameOrAddr: string): string {
  const s = String(nameOrAddr || '').trim().replace(/^0x/, '');
  return s.slice(0, 2).toUpperCase() || '?';
}

// ── Last-known-good data snapshot (per address, session-scoped) ───────────
//
// sessionStorage lives for one tab/deploy, so a stored JSON payload always
// matches the current page's expectations — no cross-deploy staleness. Lets
// a fresh navigation or a transient API failure repaint REAL data instantly
// instead of a confident zero state.

export const SNAP_PREFIX = 'mw_profile_snap:';

export function snapshotKey(addr: string): string {
  return SNAP_PREFIX + String(addr || '').toLowerCase();
}

export function saveSnapshot(addr: string, data: unknown, storage: Pick<Storage, 'setItem'> | null = typeof sessionStorage !== 'undefined' ? sessionStorage : null): void {
  if (!storage) return;
  try { storage.setItem(snapshotKey(addr), JSON.stringify(data)); } catch { /* quota/private mode */ }
}

export function loadSnapshot<T>(addr: string, storage: Pick<Storage, 'getItem'> | null = typeof sessionStorage !== 'undefined' ? sessionStorage : null): T | null {
  if (!storage) return null;
  try {
    const raw = storage.getItem(snapshotKey(addr));
    if (!raw) return null;
    return JSON.parse(raw) as T;
  } catch { return null; }
}

// ── Wallet-change guard (debounce + overlap semantics) ────────────────────
//
// Mirrors the old inline script's behaviour: rapid `mw-wallet-changed`
// events during wagmi hydration are debounced; an event for the address
// already rendered is a no-op; an event that lands while a load is in
// flight is deferred and applied when the load settles.

export interface WalletGuard {
  /** Debounced entry point for wallet-change events. */
  notify(newAddr: string): void;
  /** Mark a load started for `addr`. False when one is already in flight. */
  beginLoad(addr: string): boolean;
  /** Mark the load settled; returns a deferred address to load next, if any. */
  endLoad(): string | null;
  /** The last address handed to onChange / beginLoad. */
  readonly lastAddr: string | null;
  /** Forget the last address so the next event always re-fires (error path). */
  reset(): void;
  destroy(): void;
}

export function createWalletGuard(onChange: (addr: string) => void, debounceMs = 500): WalletGuard {
  let timer: ReturnType<typeof setTimeout> | null = null;
  let loading = false;
  let pending: string | null = null;
  let last: string | null = null;
  return {
    get lastAddr() { return last; },
    notify(newAddr: string) {
      if (timer) clearTimeout(timer);
      timer = setTimeout(() => {
        timer = null;
        if (newAddr === last) return;
        if (loading) { pending = newAddr; return; }
        last = newAddr;
        onChange(newAddr);
      }, debounceMs);
    },
    beginLoad(addr: string) {
      if (loading) return false;
      loading = true;
      last = addr;
      return true;
    },
    endLoad() {
      loading = false;
      const p = pending;
      pending = null;
      if (p !== null && p !== last) { last = p; return p; }
      return null;
    },
    reset() { last = null; },
    destroy() { if (timer) clearTimeout(timer); },
  };
}

// ── First-trade signal (home strip reads this key) ────────────────────────

export const FIRST_TRADE_KEY = 'mw-first-trade-done';

declare global {
  interface Window {
    /** Shared setter for the home first-run strip's step-3 checkmark. */
    mwSetFirstTradeDone?: () => void;
  }
}

/** Installs window.mwSetFirstTradeDone (idempotent). */
export function installFirstTradeDone(): void {
  if (typeof window === 'undefined') return;
  window.mwSetFirstTradeDone = () => {
    try { localStorage.setItem(FIRST_TRADE_KEY, '1'); } catch { /* private mode */ }
  };
}

/** Call after a successful trade-shaped tx (e.g. the batch-list success path). */
export function markFirstTradeDone(): void {
  if (typeof window === 'undefined') return;
  window.mwSetFirstTradeDone?.();
}
