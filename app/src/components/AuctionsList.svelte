<script module lang="ts">
  // Auctions list URL contract (spec B4 "Auctions") — mirrors the Listings
  // pattern: the URL is the state, Apply/Clear rewrite it with replaceState
  // and refetch in place. Pure helpers exported for the component tests.
  //
  // Segmented control [Live · Ending soon · Ended] maps to:
  //   live   → status=active                (default; nothing serialized)
  //   ending → status=active & sort=ending  (?sort=ending)
  //   ended  → status=ended                 (?status=ended)
  export type AuctionsSegment = 'live' | 'ending' | 'ended';
  export const AUCTIONS_SORTS = ['recent', 'ending', 'price_asc', 'price_desc'] as const;
  export type AuctionsSort = (typeof AUCTIONS_SORTS)[number];

  export interface AuctionsFilterState {
    segment: AuctionsSegment;
    collection: string;
    /** Human decimal starting price bounds (not wei). '' = unset. */
    min: string;
    max: string;
    sort: AuctionsSort;
    seller: string;
    page: number;
  }

  export const EMPTY_AUCTION_FILTERS: AuctionsFilterState = Object.freeze({
    segment: 'live', collection: '', min: '', max: '', sort: 'recent', seller: '', page: 1,
  });

  const A_ADDR_RE = /^0x[0-9a-fA-F]{40}$/;
  const A_DEC_RE = /^\d+(\.\d+)?$/;

  export function hasAuctionFilters(f: AuctionsFilterState): boolean {
    return !!(f.collection || f.min || f.max || f.seller);
  }

  /** Inline validation for the seller field (spec copy). '' = valid/empty. */
  export function sellerError(v: string): string {
    const t = v.trim();
    if (!t) return '';
    return A_ADDR_RE.test(t) ? '' : 'Enter a wallet address (0x…)';
  }

  /** Parse `?status=&sort=&collection=&min=&max=&seller=&page=`. Invalid values are ignored + reported. */
  export function parseAuctionsParams(search: string): { filters: AuctionsFilterState; invalid: string[] } {
    const p = new URLSearchParams(search);
    const f: AuctionsFilterState = { ...EMPTY_AUCTION_FILTERS };
    const invalid: string[] = [];
    const status = p.get('status');
    if (status) {
      if (status === 'ended') f.segment = 'ended';
      else if (status !== 'active') invalid.push('status');
    }
    const sort = p.get('sort');
    if (sort) {
      if ((AUCTIONS_SORTS as readonly string[]).includes(sort)) f.sort = sort as AuctionsSort;
      else invalid.push('sort');
    }
    if (f.segment !== 'ended' && f.sort === 'ending') f.segment = 'ending';
    const coll = p.get('collection');
    if (coll) { if (A_ADDR_RE.test(coll)) f.collection = coll.toLowerCase(); else invalid.push('collection'); }
    for (const k of ['min', 'max'] as const) {
      const v = p.get(k);
      if (v) { if (A_DEC_RE.test(v)) f[k] = v; else invalid.push(k); }
    }
    const seller = p.get('seller');
    if (seller) { if (A_ADDR_RE.test(seller)) f.seller = seller.toLowerCase(); else invalid.push('seller'); }
    const page = p.get('page');
    if (page) {
      const n = Number(page);
      if (Number.isInteger(n) && n >= 1 && n <= 10_000) f.page = n; else invalid.push('page');
    }
    return { filters: f, invalid };
  }

  /** Serialize back to the canonical search string (defaults omitted). */
  export function auctionsSearch(f: AuctionsFilterState): string {
    const p = new URLSearchParams();
    if (f.segment === 'ended') p.set('status', 'ended');
    const sort = f.segment === 'ending' ? 'ending' : f.sort;
    if (sort !== 'recent') p.set('sort', sort);
    if (f.collection) p.set('collection', f.collection);
    if (f.min) p.set('min', f.min);
    if (f.max) p.set('max', f.max);
    if (f.seller) p.set('seller', f.seller);
    if (f.page > 1) p.set('page', String(f.page));
    const s = p.toString();
    return s ? `?${s}` : '';
  }

  /** Segment change keeps the other filters; ending forces sort=ending, leaving it restores recent. */
  export function withSegment(f: AuctionsFilterState, seg: AuctionsSegment): AuctionsFilterState {
    const next = { ...f, segment: seg, page: 1 };
    if (seg === 'ending') next.sort = 'ending';
    else if (f.segment === 'ending' && next.sort === 'ending') next.sort = 'recent';
    return next;
  }

  export interface AuctionRowLike {
    auction_id: number; reserve_price_wei: string; highest_bid_wei: string;
    starts_at: string; ends_at: string;
  }

  /** Effective price of a row: current bid when there is one, else the reserve. */
  export function rowPriceWei(r: AuctionRowLike): bigint {
    try {
      const hb = BigInt(r.highest_bid_wei || '0');
      return hb > 0n ? hb : BigInt(r.reserve_price_wei || '0');
    } catch { return 0n; }
  }

  /** Client-side sort (the API always returns ends_at ASC). */
  export function sortAuctionRows<T extends AuctionRowLike>(rows: T[], sort: AuctionsSort): T[] {
    const out = rows.slice();
    switch (sort) {
      case 'ending': out.sort((a, b) => new Date(a.ends_at).getTime() - new Date(b.ends_at).getTime()); break;
      case 'recent': out.sort((a, b) => new Date(b.starts_at).getTime() - new Date(a.starts_at).getTime()); break;
      case 'price_asc': out.sort((a, b) => (rowPriceWei(a) < rowPriceWei(b) ? -1 : rowPriceWei(a) > rowPriceWei(b) ? 1 : 0)); break;
      case 'price_desc': out.sort((a, b) => (rowPriceWei(a) > rowPriceWei(b) ? -1 : rowPriceWei(a) < rowPriceWei(b) ? 1 : 0)); break;
    }
    return out;
  }
</script>

<script lang="ts">
  import { onMount } from 'svelte';
  import Icon from './Icon.svelte';
  import Skeleton from './Skeleton.svelte';
  import EmptyState from './EmptyState.svelte';
  import ErrorState from './ErrorState.svelte';
  import VerifiedBadge from './VerifiedBadge.svelte';
  import { toast } from '../lib/toast.svelte';
  import { currentChain } from '../lib/chains';
  import { jsonOrNull, json } from '../lib/api';
  import { fmtPrice, shortAddr, fmtCountdownShort, countdownUrgent, toWei } from '../lib/format';
  import { resolveImageUri } from '../lib/image-uri';

  type AuctionRow = {
    auction_id: number; collection: string; token_id: string; seller: string; standard: string;
    reserve_price_wei: string; highest_bid_wei: string; highest_bidder: string;
    starts_at: string; ends_at: string; status: string; name: string; image_uri: string;
    collection_verified: boolean; collection_creator?: string; collection_name?: string; collection_tracked?: boolean;
  };
  type CollectionRow = { address: string; name: string };

  const sym = currentChain().currency;
  const PAGE = 48;

  let applied = $state<AuctionsFilterState>({ ...EMPTY_AUCTION_FILTERS });
  let draftCollection = $state('');
  let draftMin = $state('');
  let draftMax = $state('');
  let draftSort = $state<AuctionsSort>('recent');
  let draftSeller = $state('');
  let sellerErr = $state('');

  let rows = $state<AuctionRow[]>([]);
  let bidCounts = $state<Record<number, number>>({});
  let loading = $state(true);
  let error = $state('');
  let now = $state(Date.now());
  let collections = $state<CollectionRow[]>([]);
  let gen = 0;

  let filtered = $derived(hasAuctionFilters(applied));
  let sorted = $derived(sortAuctionRows(rows, applied.segment === 'ending' ? 'ending' : applied.sort));

  const collName = (addr: string) =>
    collections.find((c) => c.address.toLowerCase() === addr.toLowerCase())?.name || shortAddr(addr);

  function apiParams(f: AuctionsFilterState): URLSearchParams {
    const p = new URLSearchParams({ limit: String(Math.min(200, PAGE * f.page)) });
    p.set('status', f.segment === 'ended' ? 'ended' : 'active');
    if (f.collection) p.set('collection', f.collection);
    if (f.seller) p.set('seller', f.seller);
    for (const [k, api] of [['min', 'min_price'], ['max', 'max_price']] as const) {
      const v = f[k];
      if (!v) continue;
      try { p.set(api, toWei(v).toString()); } catch { /* validated upstream */ }
    }
    return p;
  }

  async function load() {
    const g = ++gen;
    loading = true;
    error = '';
    try {
      const got = await json<AuctionRow[]>(`/api/v1/auctions?${apiParams(applied)}`);
      if (g !== gen) return;
      rows = Array.isArray(got) ? got : [];
      void loadBidCounts(g, rows);
    } catch {
      if (g !== gen) return;
      error = 'The marketplace did not respond. It may be busy — try again in a moment.';
      rows = [];
    } finally {
      if (g === gen) loading = false;
    }
  }

  // The list rows carry no bid count; fetch it only for auctions that have a
  // bid at all (highest_bid_wei > 0) — everything else is "0 bids" for free.
  async function loadBidCounts(g: number, list: AuctionRow[]) {
    const withBids = list.filter((r) => { try { return BigInt(r.highest_bid_wei || '0') > 0n; } catch { return false; } }).slice(0, 24);
    const pairs = await Promise.all(withBids.map(async (r) => {
      const bids = await jsonOrNull<unknown[]>(`/api/v1/auctions/${r.auction_id}/bids`);
      return [r.auction_id, Array.isArray(bids) ? bids.length : 1] as const;
    }));
    if (g !== gen) return;
    const next: Record<number, number> = {};
    for (const [id, n] of pairs) next[id] = n;
    bidCounts = next;
  }

  function bidsLabel(r: AuctionRow): string {
    let hasBid = false;
    try { hasBid = BigInt(r.highest_bid_wei || '0') > 0n; } catch { /* treat as none */ }
    if (!hasBid) return '0 bids';
    const n = bidCounts[r.auction_id];
    if (typeof n !== 'number') return 'has bids';
    return n === 1 ? '1 bid' : `${n} bids`;
  }

  function draftFromApplied() {
    draftCollection = applied.collection;
    draftMin = applied.min;
    draftMax = applied.max;
    draftSort = applied.sort;
    draftSeller = applied.seller;
    sellerErr = '';
  }

  function commit(next: AuctionsFilterState) {
    applied = next;
    history.replaceState(null, '', location.pathname + auctionsSearch(next));
    void load();
  }

  function apply() {
    sellerErr = sellerError(draftSeller);
    if (sellerErr) return;
    commit({
      segment: applied.segment,
      collection: A_RE.test(draftCollection) ? draftCollection.toLowerCase() : '',
      min: /^\d+(\.\d+)?$/.test(draftMin.trim()) ? draftMin.trim() : '',
      max: /^\d+(\.\d+)?$/.test(draftMax.trim()) ? draftMax.trim() : '',
      sort: applied.segment === 'ending' ? 'ending' : draftSort,
      seller: draftSeller.trim() ? draftSeller.trim().toLowerCase() : '',
      page: 1,
    });
  }
  const A_RE = /^0x[0-9a-fA-F]{40}$/;

  function clearAll() {
    draftCollection = ''; draftMin = ''; draftMax = ''; draftSort = 'recent'; draftSeller = ''; sellerErr = '';
    commit({ ...EMPTY_AUCTION_FILTERS, segment: applied.segment, sort: applied.segment === 'ending' ? 'ending' : 'recent' });
  }

  function setSegment(seg: AuctionsSegment) {
    if (seg === applied.segment) return;
    const next = withSegment(applied, seg);
    draftSort = next.sort;
    commit(next);
  }

  type Chip = { key: string; label: string; remove: () => void };
  let chips = $derived.by<Chip[]>(() => {
    const out: Chip[] = [];
    if (applied.collection) out.push({ key: 'collection', label: collName(applied.collection), remove: () => { draftCollection = ''; apply(); } });
    if (applied.min) out.push({ key: 'min', label: `Min ${applied.min} ${sym}`, remove: () => { draftMin = ''; apply(); } });
    if (applied.max) out.push({ key: 'max', label: `Max ${applied.max} ${sym}`, remove: () => { draftMax = ''; apply(); } });
    if (applied.seller) out.push({ key: 'seller', label: `Seller ${shortAddr(applied.seller)}`, remove: () => { draftSeller = ''; apply(); } });
    return out;
  });

  function priceLine(r: AuctionRow): { label: string; value: string; gold: boolean } {
    let hasBid = false;
    try { hasBid = BigInt(r.highest_bid_wei || '0') > 0n; } catch { /* none */ }
    return hasBid
      ? { label: 'Current bid', value: `${fmtPrice(r.highest_bid_wei)} ${sym}`, gold: true }
      : { label: 'Starting at', value: `${fmtPrice(r.reserve_price_wei)} ${sym}`, gold: false };
  }

  function endsSec(r: AuctionRow): number { return new Date(r.ends_at).getTime() / 1000; }
  function isOver(r: AuctionRow): boolean { return r.status !== 'active' || endsSec(r) * 1000 <= now; }
  function statusChip(r: AuctionRow): string {
    if (r.status === 'settled') return 'Sold';
    if (r.status === 'cancelled') return 'Cancelled';
    if (r.status === 'active' && endsSec(r) * 1000 <= now) return 'Ended · settling';
    return '';
  }

  onMount(() => {
    const { filters, invalid } = parseAuctionsParams(location.search);
    applied = filters;
    draftFromApplied();
    if (invalid.length) {
      toast('Some filters were invalid and were cleared');
      history.replaceState(null, '', location.pathname + auctionsSearch(filters));
    }
    void load();
    void (async () => { collections = (await jsonOrNull<CollectionRow[]>('/api/v1/collections?limit=200')) ?? []; })();
    const tick = setInterval(() => (now = Date.now()), 1000);
    return () => clearInterval(tick);
  });
</script>

<header class="al-head">
  <h1>Auctions</h1>
  <div class="al-seg" role="group" aria-label="Auction status">
    <button type="button" class="al-seg-btn" class:is-on={applied.segment === 'live'} aria-pressed={applied.segment === 'live'} data-testid="seg-live" onclick={() => setSegment('live')}>Live</button>
    <button type="button" class="al-seg-btn" class:is-on={applied.segment === 'ending'} aria-pressed={applied.segment === 'ending'} data-testid="seg-ending" onclick={() => setSegment('ending')}>Ending soon</button>
    <button type="button" class="al-seg-btn" class:is-on={applied.segment === 'ended'} aria-pressed={applied.segment === 'ended'} data-testid="seg-ended" onclick={() => setSegment('ended')}>Ended</button>
  </div>
</header>

<form class="al-bar" onsubmit={(e) => { e.preventDefault(); apply(); }} aria-label="Filter auctions">
  <div class="field al-coll">
    <label for="al-collection">Collection</label>
    <select id="al-collection" bind:value={draftCollection}>
      <option value="">All collections</option>
      {#each collections as c (c.address)}
        <option value={c.address}>{c.name || shortAddr(c.address)}</option>
      {/each}
    </select>
  </div>
  <div class="field al-price">
    <label for="al-min">Min price ({sym})</label>
    <input id="al-min" inputmode="decimal" placeholder="0" bind:value={draftMin} />
  </div>
  <div class="field al-price">
    <label for="al-max">Max price ({sym})</label>
    <input id="al-max" inputmode="decimal" placeholder="Any" bind:value={draftMax} />
  </div>
  <div class="field">
    <label for="al-sort">Sort</label>
    <select id="al-sort" bind:value={draftSort} disabled={applied.segment === 'ending'} onchange={apply}>
      <option value="recent">Newest</option>
      <option value="ending">Ending soon</option>
      <option value="price_asc">Price low→high</option>
      <option value="price_desc">Price high→low</option>
    </select>
  </div>
  <div class="field al-seller">
    <label for="al-seller">Seller</label>
    <input id="al-seller" placeholder="0x…" bind:value={draftSeller} aria-invalid={sellerErr ? 'true' : undefined}
           aria-describedby={sellerErr ? 'al-seller-err' : undefined}
           oninput={() => { if (sellerErr) sellerErr = sellerError(draftSeller); }} />
    {#if sellerErr}<span id="al-seller-err" class="al-err" role="alert" data-testid="seller-error">{sellerErr}</span>{/if}
  </div>
  <div class="al-actions">
    <button type="submit" class="btn btn-primary">Apply</button>
    <button type="button" class="btn btn-ghost" onclick={clearAll}>Clear</button>
  </div>
</form>

{#if chips.length}
  <div class="al-chips" aria-label="Applied filters">
    {#each chips as c (c.key)}
      <span class="al-chip" data-testid="filter-chip">
        {c.label}
        <button type="button" class="al-chip-x" aria-label={`Remove filter ${c.label}`} onclick={c.remove}><Icon name="x" size={14} /></button>
      </span>
    {/each}
  </div>
{/if}

<section aria-label="Auctions">
  {#if loading}
    <div class="al-grid" aria-hidden="true">
      {#each Array(8) as _, i (i)}
        <div class="al-sk" class:al-sk-extra={i >= 4}><Skeleton card /></div>
      {/each}
    </div>
  {:else if error}
    <ErrorState title="Failed to load auctions" message={error} retry={() => void load()} />
  {:else if sorted.length === 0}
    {#if filtered}
      <EmptyState title="No auctions match" icon="search" cta={{ label: 'Clear filters', onclick: clearAll }} />
    {:else if applied.segment === 'ended'}
      <EmptyState title="No ended auctions yet" body="Auctions that have ended or settled appear here." icon="gavel" />
    {:else}
      <EmptyState title="No auctions running" body="Start one from any NFT you own — it's free." icon="gavel"
                  cta={{ label: 'Browse listings', href: '/listings' }} />
    {/if}
  {:else}
    <div class="al-grid">
      {#each sorted as r (r.auction_id)}
        {@const p = priceLine(r)}
        {@const img = resolveImageUri(r.image_uri, r.token_id, 256)}
        <a class="al-card" href={`/auction/${r.auction_id}`}>
          <div class="al-img">
            {#if img}<img src={img} alt={r.name || `#${r.token_id}`} loading="lazy" decoding="async" />{:else}<span class="al-noimg"><Icon name="image" size={32} /></span>{/if}
            <span class="al-badges">
              <VerifiedBadge verified={r.collection_verified} tracked={r.collection_tracked} creatorAddr={r.collection_creator ?? ''} collectionName={r.collection_name ?? ''} link={false} hint={false} />
            </span>
            {#if statusChip(r)}<span class="al-status">{statusChip(r)}</span>{/if}
          </div>
          <div class="al-body">
            <span class="al-name">{r.name || `#${r.token_id}`}</span>
            <span class="al-priceline">
              <span class="al-pricelabel">{p.label}</span>
              <span class="al-price mono" class:is-gold={p.gold}>{p.value}</span>
            </span>
            <span class="al-metaline">
              {#if isOver(r)}
                <span class="al-ends">Ended</span>
              {:else}
                <span class="al-ends" class:is-urgent={countdownUrgent(endsSec(r), now)}>Ends in <span class="mono">{fmtCountdownShort(endsSec(r), now)}</span></span>
              {/if}
              <span class="al-dim">{bidsLabel(r)}</span>
            </span>
            <span class="al-dim mono">seller {shortAddr(r.seller)}</span>
          </div>
        </a>
      {/each}
    </div>
  {/if}
</section>

<style>
  .al-head { display: flex; align-items: center; justify-content: space-between; gap: var(--sp-3); flex-wrap: wrap; margin-bottom: var(--sp-4); }
  .al-head h1 { font-size: var(--fs-h1); line-height: var(--lh-h1); font-weight: 800; margin: 0; letter-spacing: -0.02em; }
  .al-seg { display: inline-flex; border: 1px solid var(--line-strong); border-radius: var(--r-pill); padding: 3px; background: var(--surface); }
  .al-seg-btn { min-height: 38px; padding: 0 var(--sp-4); border: 0; border-radius: var(--r-pill); background: transparent; color: var(--text-2); font-weight: 700; font-size: var(--fs-small); font-family: inherit; cursor: pointer; }
  .al-seg-btn.is-on { background: var(--violet-12); color: var(--violet-300); }
  .al-bar { display: flex; flex-wrap: wrap; align-items: start; gap: var(--sp-3); padding: var(--sp-4); border-radius: var(--r-card); background: var(--surface); border: 1px solid var(--line); margin-bottom: var(--sp-3); }
  .al-bar .field { display: flex; flex-direction: column; gap: var(--sp-1); }
  .al-bar input, .al-bar select { min-height: var(--hit); padding: 0 var(--sp-3); border-radius: var(--r-control); background: rgba(255,255,255,.05); border: 1px solid var(--line-strong); color: var(--text); font-size: 16px; font-family: inherit; width: 100%; }
  .al-bar label { font-size: var(--fs-small); color: var(--text-2); font-weight: 600; }
  .al-coll { min-width: 200px; flex: 1 1 200px; }
  .al-price { width: 130px; }
  .al-seller { min-width: 180px; }
  .al-err { color: var(--red); font-size: var(--fs-small); }
  .al-actions { display: flex; gap: var(--sp-2); align-items: end; padding-top: 24px; }
  .al-chips { display: flex; flex-wrap: wrap; gap: var(--sp-2); margin-bottom: var(--sp-3); }
  .al-chip { display: inline-flex; align-items: center; gap: 6px; padding: 6px 12px; border-radius: var(--r-pill); background: var(--violet-12); border: 1px solid var(--violet-35); color: var(--violet-300); font-size: var(--fs-small); font-weight: 700; }
  .al-chip-x { display: inline-flex; align-items: center; justify-content: center; width: 28px; height: 28px; margin: -6px -8px -6px 0; border: 0; border-radius: var(--r-pill); background: transparent; color: inherit; cursor: pointer; }
  .al-chip-x:hover { background: rgba(255,255,255,.1); }
  .al-grid { display: grid; grid-template-columns: repeat(1, 1fr); gap: var(--sp-4); }
  @media (min-width: 640px) { .al-grid { grid-template-columns: repeat(2, 1fr); } }
  @media (min-width: 960px) { .al-grid { grid-template-columns: repeat(3, 1fr); } }
  @media (min-width: 1280px) { .al-grid { grid-template-columns: repeat(4, 1fr); } }
  @media (max-width: 639px) { .al-sk-extra { display: none; } }
  .al-card { display: block; background: var(--surface); border: 1px solid var(--line); border-radius: var(--r-card); overflow: hidden; text-decoration: none; color: inherit; transition: transform var(--dur) var(--ease), border-color var(--dur) var(--ease); }
  .al-card:hover { transform: translateY(-2px); border-color: var(--violet-35); text-decoration: none; }
  .al-img { position: relative; aspect-ratio: 1; background: var(--bg); overflow: hidden; }
  .al-img img { width: 100%; height: 100%; object-fit: cover; display: block; }
  .al-noimg { display: flex; align-items: center; justify-content: center; height: 100%; color: var(--text-3); opacity: .4; }
  .al-badges { position: absolute; top: var(--sp-2); left: var(--sp-2); display: flex; gap: var(--sp-1); }
  .al-status { position: absolute; top: var(--sp-2); right: var(--sp-2); padding: 3px 10px; border-radius: var(--r-pill); background: rgba(9,9,11,.75); border: 1px solid var(--line-strong); color: var(--text-2); font-size: var(--fs-caption); font-weight: 700; }
  .al-body { display: flex; flex-direction: column; gap: var(--sp-1); padding: var(--sp-3); }
  .al-name { font-size: var(--fs-body); font-weight: 700; color: var(--text); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .al-priceline { display: flex; align-items: baseline; justify-content: space-between; gap: var(--sp-2); }
  .al-pricelabel { font-size: var(--fs-caption); line-height: var(--lh-caption); letter-spacing: var(--ls-caption); text-transform: uppercase; color: var(--text-3); font-weight: 700; }
  .al-price { font-size: var(--fs-h3); font-weight: 600; color: var(--text); }
  .al-price.is-gold { color: var(--gold-300); }
  .al-metaline { display: flex; align-items: center; justify-content: space-between; gap: var(--sp-2); font-size: var(--fs-small); }
  .al-ends { color: var(--text-2); }
  .al-ends.is-urgent { color: var(--red); font-weight: 700; }
  .al-dim { color: var(--text-3); font-size: var(--fs-small); }
  .mono { font-family: var(--font-mono); }
</style>
