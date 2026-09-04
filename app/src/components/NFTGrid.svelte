<script lang="ts">
  // Listings grid (spec B4). Receives filter state from ListingsFilters via
  // the FILTERS_EVENT window event (or a `filters` prop in tests), fetches in
  // place (never navigates), pages with "Load more" (48/page), and renders the
  // full state table: skeleton 8/4, empty vs no-match, error + Retry.
  import { onMount } from 'svelte';
  import NFTCard from './NFTCard.svelte';
  import EmptyState from './EmptyState.svelte';
  import ErrorState from './ErrorState.svelte';
  import Skeleton from './Skeleton.svelte';
  import { ws } from '../lib/ws/client';
  import { ACTIVITY_CHANNEL } from '../lib/ws/channels';
  import { tradingLive, readOnlyCopy } from '../lib/chains';
  import {
    json, jsonOrNull, listingsApiParams, parseListingsParams, hasActiveFilters,
    FILTERS_EVENT, CLEAR_FILTERS_EVENT, EMPTY_FILTERS, type ListingsFilterState,
  } from '../lib/api';

  interface ListingItem {
    collection: string; token_id: string; seller: string; price_wei: string;
    amount: number; standard: string; expires_at: string; listed_at: string;
    tx_hash: string; name: string; image_uri: string; total_supply: number;
    collection_verified: boolean; collection_creator?: string;
    collection_name?: string; collection_tracked?: boolean;
  }

  let { filters = null, pageSize = 48 }: { filters?: ListingsFilterState | null; pageSize?: number } = $props();

  const live = tradingLive();
  const ro = readOnlyCopy();

  let state = $state<ListingsFilterState>({ ...EMPTY_FILTERS });
  let items = $state<ListingItem[]>([]);
  let loading = $state(true);
  let error = $state('');
  let page = $state(1);
  let lastBatch = $state(0);
  let loadingMore = $state(false);
  /** Total live listings — only known while unfiltered (from /api/v1/metrics). */
  let total = $state<number | null>(null);
  let gen = 0;

  let filtered = $derived(hasActiveFilters(state));
  let hasMore = $derived(lastBatch === pageSize);

  async function fetchPage(p: number): Promise<ListingItem[]> {
    return await json<ListingItem[]>(`/api/v1/listings?${listingsApiParams(state, p, pageSize)}`);
  }

  /** Full (re)load: pages 1..state.page so a deep-linked ?page=3 shows all rows up to it. */
  async function load() {
    const g = ++gen;
    loading = true;
    error = '';
    try {
      const pages = Math.max(1, Math.min(state.page, 10));
      const batches = await Promise.all(Array.from({ length: pages }, (_, i) => fetchPage(i + 1)));
      if (g !== gen) return;
      items = batches.flat();
      page = pages;
      lastBatch = batches[batches.length - 1]?.length ?? 0;
    } catch {
      if (g !== gen) return;
      error = 'The marketplace did not respond. It may be busy — try again in a moment.';
      items = [];
    } finally {
      if (g === gen) loading = false;
    }
    if (!filtered) {
      const m = await jsonOrNull<{ totalActiveListings?: number }>('/api/v1/metrics');
      if (g === gen) total = typeof m?.totalActiveListings === 'number' ? m.totalActiveListings : null;
    } else if (g === gen) {
      total = null;
    }
  }

  async function loadMore() {
    if (loadingMore) return;
    const g = gen;
    loadingMore = true;
    try {
      const next = await fetchPage(page + 1);
      if (g !== gen) return;
      page += 1;
      lastBatch = next.length;
      items = [...items, ...next];
      state.page = page;
      history.replaceState(null, '', location.pathname + toSearch());
    } catch { /* keep what we have; the button stays for another try */ }
    finally { if (g === gen) loadingMore = false; }
  }

  function toSearch(): string {
    // NFTGrid only advances `page`; everything else is ListingsFilters' job.
    const p = new URLSearchParams(location.search);
    if (page > 1) p.set('page', String(page)); else p.delete('page');
    const s = p.toString();
    return s ? `?${s}` : '';
  }

  function applyFilters(f: ListingsFilterState) {
    state = { ...f };
    void load();
  }

  function clearFilters() {
    window.dispatchEvent(new CustomEvent(CLEAR_FILTERS_EVENT));
    applyFilters({ ...EMPTY_FILTERS });
  }

  function goListNFT() {
    const mw = window.MW;
    const connected = (() => { try { return !!mw?.address?.(); } catch { return false; } })();
    if (!connected && mw) { mw.connect().then(() => { location.href = '/profile#nfts'; }).catch(() => {}); return; }
    location.href = '/profile#nfts';
  }

  onMount(() => {
    applyFilters(filters ?? parseListingsParams(location.search).filters);
    const onFilters = (e: Event) => applyFilters((e as CustomEvent<ListingsFilterState>).detail);
    window.addEventListener(FILTERS_EVENT, onFilters);
    // Live: marketplace activity refreshes the grid in place, debounced.
    let t: ReturnType<typeof setTimeout> | null = null;
    const offWs = ws.on('*', (_d, meta) => {
      if (meta.type === 'notification') return;
      if (t) clearTimeout(t);
      t = setTimeout(() => { t = null; void load(); }, 400);
    });
    ws.subscribe(ACTIVITY_CHANNEL);
    return () => {
      window.removeEventListener(FILTERS_EVENT, onFilters);
      offWs();
      ws.unsubscribe(ACTIVITY_CHANNEL);
      if (t) clearTimeout(t);
    };
  });
</script>

<section class="ng" aria-label="Listings">
  {#if loading}
    <div class="ng-grid" aria-hidden="true">
      {#each Array(8) as _, i (i)}
        <div class="ng-sk" class:ng-sk-extra={i >= 4}><Skeleton card /></div>
      {/each}
    </div>
  {:else if error}
    <ErrorState title="Failed to load listings" message={error} retry={() => void load()} />
  {:else if items.length === 0}
    {#if !live}
      <EmptyState title={ro.heading} body={ro.body} cta={ro.ctaHref ? { label: ro.cta, href: ro.ctaHref } : undefined} />
    {:else if filtered}
      <EmptyState title="No listings match" body="Try widening the price range or clearing a filter." icon="search"
                  cta={{ label: 'Clear filters', onclick: clearFilters }} />
    {:else}
      <EmptyState title="Nothing is listed yet" body="Listing is free — you only pay 2% when it sells." icon="tag"
                  cta={{ label: 'List an NFT', onclick: goListNFT }} />
    {/if}
  {:else}
    <div class="ng-grid">
      {#each items as item (item.collection + item.token_id + item.seller)}
        <NFTCard {item} />
      {/each}
    </div>
    <footer class="ng-foot">
      <span class="ng-count" data-testid="showing">
        {total !== null && total >= items.length ? `Showing ${items.length} of ${total}` : `Showing ${items.length}`}
      </span>
      {#if hasMore}
        <button class="btn btn-secondary btn-lg" onclick={() => void loadMore()} disabled={loadingMore}>
          {loadingMore ? 'Loading…' : 'Load more'}
        </button>
      {/if}
    </footer>
  {/if}
</section>

<style>
  .ng-grid { display: grid; grid-template-columns: repeat(1, 1fr); gap: var(--sp-4); }
  @media (min-width: 640px) { .ng-grid { grid-template-columns: repeat(2, 1fr); } }
  @media (min-width: 960px) { .ng-grid { grid-template-columns: repeat(3, 1fr); } }
  @media (min-width: 1280px) { .ng-grid { grid-template-columns: repeat(4, 1fr); } }
  /* Skeleton 8 on desktop, 4 on mobile (spec). */
  @media (max-width: 639px) { .ng-sk-extra { display: none; } }
  .ng-foot { display: flex; flex-direction: column; align-items: center; gap: var(--sp-3); margin-top: var(--sp-6); }
  .ng-count { color: var(--text-3); font-size: var(--fs-small); }
</style>
