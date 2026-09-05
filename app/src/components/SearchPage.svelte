<script module lang="ts">
  // Pure search helpers (spec B4 "Search"), exported for the tests.
  export const RECENT_SEARCHES_KEY = 'mw-recent-searches';
  export const MAX_RECENT = 5;

  /** Search runs from 2 characters (the API rejects shorter queries too). */
  export function canSearch(q: string): boolean {
    return q.trim().length >= 2;
  }

  /** `1 result for "magic"` — pluralised. */
  export function resultsHeading(n: number, q: string): string {
    return `${n} result${n === 1 ? '' : 's'} for "${q}"`;
  }

  export interface SearchRowLike { kind: string }

  export function groupResults<T extends SearchRowLike>(rows: T[]): { collections: T[]; nfts: T[] } {
    return {
      collections: rows.filter((r) => r.kind === 'collection'),
      nfts: rows.filter((r) => r.kind === 'nft'),
    };
  }

  /** Most-recent-first, de-duplicated (case-insensitive), capped at MAX_RECENT. */
  export function pushRecent(list: string[], q: string, max = MAX_RECENT): string[] {
    const t = q.trim();
    if (!t) return list.slice(0, max);
    const rest = list.filter((x) => x.toLowerCase() !== t.toLowerCase());
    return [t, ...rest].slice(0, max);
  }
</script>

<script lang="ts">
  import { onMount } from 'svelte';
  import Icon from './Icon.svelte';
  import Skeleton from './Skeleton.svelte';
  import EmptyState from './EmptyState.svelte';
  import ErrorState from './ErrorState.svelte';
  import VerifiedBadge from './VerifiedBadge.svelte';
  import { currentChain } from '../lib/chains';
  import { jsonOrNull } from '../lib/api';
  import { fmtPrice, shortAddr } from '../lib/format';
  import { resolveImageUri } from '../lib/image-uri';

  type SearchRow = {
    kind: 'nft' | 'collection'; collection: string; token_id?: string; name: string;
    image_uri?: string; collection_verified?: boolean; collection_creator?: string;
    standard?: string; collection_name?: string; collection_tracked?: boolean;
  };
  type CollectionDetail = { symbol?: string; listed_count?: number; floor_price_wei?: string };

  const sym = currentChain().currency;

  let q = $state('');
  let searchedQ = $state('');
  let phase = $state<'idle' | 'short' | 'loading' | 'done' | 'error'>('idle');
  let rows = $state<SearchRow[]>([]);
  let details = $state<Record<string, CollectionDetail>>({});
  let recent = $state<string[]>([]);
  let inputEl = $state<HTMLInputElement | undefined>();
  let gen = 0;
  let debounceT: ReturnType<typeof setTimeout> | null = null;

  let groups = $derived(groupResults(rows));

  function readRecent(): string[] {
    try {
      const raw = localStorage.getItem(RECENT_SEARCHES_KEY);
      const arr = raw ? (JSON.parse(raw) as unknown) : [];
      return Array.isArray(arr) ? arr.filter((x): x is string => typeof x === 'string').slice(0, MAX_RECENT) : [];
    } catch { return []; }
  }
  function saveRecent(list: string[]) {
    try { localStorage.setItem(RECENT_SEARCHES_KEY, JSON.stringify(list)); } catch { /* private mode */ }
  }

  async function doSearch(query = q) {
    const t = query.trim();
    if (!t) { phase = 'idle'; rows = []; return; }
    if (!canSearch(t)) { phase = 'short'; rows = []; return; }
    const g = ++gen;
    phase = 'loading';
    const got = await jsonOrNull<SearchRow[]>(`/api/v1/search?q=${encodeURIComponent(t)}`);
    if (g !== gen) return;
    if (got === null) { phase = 'error'; rows = []; return; }
    rows = Array.isArray(got) ? got : [];
    searchedQ = t;
    phase = 'done';
    recent = pushRecent(recent, t);
    saveRecent(recent);
    history.replaceState(null, '', location.pathname + (t ? `?q=${encodeURIComponent(t)}` : ''));
    void loadCollectionDetails(g, rows);
  }

  // Collection cards show "n listed · floor X" — fetched per collection
  // result (there are at most a handful per query).
  async function loadCollectionDetails(g: number, list: SearchRow[]) {
    const colls = list.filter((r) => r.kind === 'collection').slice(0, 8);
    const pairs = await Promise.all(colls.map(async (r) => {
      const d = await jsonOrNull<CollectionDetail>(`/api/v1/collections/${encodeURIComponent(r.collection)}`);
      return [r.collection.toLowerCase(), d] as const;
    }));
    if (g !== gen) return;
    const next: Record<string, CollectionDetail> = {};
    for (const [addr, d] of pairs) { if (d) next[addr] = d; }
    details = next;
  }

  function onInput() {
    if (debounceT) clearTimeout(debounceT);
    const t = q.trim();
    if (!t) { phase = 'idle'; rows = []; return; }
    if (!canSearch(t)) { phase = 'short'; rows = []; return; }
    debounceT = setTimeout(() => { debounceT = null; void doSearch(); }, 300);
  }

  function onKey(e: KeyboardEvent) {
    if (e.key === 'Enter') { e.preventDefault(); void doSearch(); }
    if (e.key === 'Escape') { q = ''; phase = 'idle'; rows = []; }
  }

  function useRecent(r: string) { q = r; void doSearch(r); }
  function collStats(addr: string): string {
    const d = details[addr.toLowerCase()];
    if (!d) return '';
    const listed = d.listed_count ?? 0;
    let floor = '';
    try { if (BigInt(d.floor_price_wei || '0') > 0n) floor = ` · floor ${fmtPrice(d.floor_price_wei)} ${sym}`; } catch { /* no floor */ }
    return `${listed} listed${floor}`;
  }

  onMount(() => {
    recent = readRecent();
    const pre = new URLSearchParams(location.search).get('q') || '';
    if (pre) { q = pre; void doSearch(pre); }
    inputEl?.focus();
    return () => { if (debounceT) clearTimeout(debounceT); };
  });
</script>

<h1 class="sp-h1">Search</h1>
<div class="sp-inrow">
  <input class="sp-input" bind:this={inputEl} bind:value={q} type="search" autocomplete="off"
         aria-label="Search collections and NFTs" placeholder="Search collections and NFTs"
         oninput={onInput} onkeydown={onKey} />
  <button class="btn btn-primary btn-lg" onclick={() => void doSearch()}>Search</button>
</div>

{#if phase === 'short'}
  <p class="sp-helper" data-testid="min-length-helper">Type at least 2 characters</p>
{:else if phase === 'idle'}
  {#if recent.length}
    <div class="sp-recent" data-testid="recent-searches">
      <span class="sp-cap">Recent searches</span>
      <div class="sp-recent-row">
        {#each recent as r (r)}
          <button type="button" class="sp-chip" onclick={() => useRecent(r)}><Icon name="search" size={14} />{r}</button>
        {/each}
      </div>
    </div>
  {:else}
    <p class="sp-helper">Try a collection name or token number</p>
  {/if}
{:else if phase === 'loading'}
  <div class="sp-skl" aria-hidden="true"><Skeleton h="64px" r="14px" /><Skeleton h="64px" r="14px" /><Skeleton h="64px" r="14px" /></div>
{:else if phase === 'error'}
  <ErrorState title="Search isn't responding" message="Give it a moment and try again." retry={() => void doSearch(searchedQ || q)} />
{:else if phase === 'done'}
  {#if rows.length === 0}
    <EmptyState title={`No results for "${searchedQ}"`} body="Try a shorter name, or browse everything." icon="search"
                cta={{ label: 'Browse listings', href: '/listings' }} />
  {:else}
    <p class="sp-heading" data-testid="results-heading">{resultsHeading(rows.length, searchedQ)}</p>
    {#if groups.collections.length}
      <h2 class="sp-group">Collections ({groups.collections.length})</h2>
      <div class="sp-colls">
        {#each groups.collections as r (r.collection)}
          <a class="sp-coll" href={`/collection/${r.collection}`}>
            <span class="sp-coll-top">
              <span class="sp-coll-name">{r.name || shortAddr(r.collection)}</span>
              {#if details[r.collection.toLowerCase()]?.symbol}<span class="sp-dim">{details[r.collection.toLowerCase()].symbol}</span>{/if}
              <VerifiedBadge verified={!!r.collection_verified} tracked={r.collection_tracked} creatorAddr={r.collection_creator ?? ''} collectionName={r.name} link={false} hint={false} />
            </span>
            {#if collStats(r.collection)}<span class="sp-dim">{collStats(r.collection)}</span>{/if}
          </a>
        {/each}
      </div>
    {/if}
    {#if groups.nfts.length}
      <h2 class="sp-group">NFTs ({groups.nfts.length})</h2>
      <div class="sp-grid">
        {#each groups.nfts as r (r.collection + (r.token_id ?? ''))}
          {@const img = resolveImageUri(r.image_uri, r.token_id, 256)}
          <a class="sp-card" href={`/token/${r.collection}/${r.token_id ?? ''}`}>
            <span class="sp-card-img">
              {#if img}<img src={img} alt={r.name || `#${r.token_id}`} loading="lazy" decoding="async" />{:else}<span class="sp-noimg"><Icon name="image" size={28} /></span>{/if}
              <span class="sp-card-badge"><VerifiedBadge verified={!!r.collection_verified} tracked={r.collection_tracked} creatorAddr={r.collection_creator ?? ''} collectionName={r.collection_name ?? ''} link={false} hint={false} /></span>
            </span>
            <span class="sp-card-body">
              <span class="sp-dim">{r.collection_name || shortAddr(r.collection)}</span>
              <span class="sp-card-name">{r.name || `#${r.token_id}`}</span>
            </span>
          </a>
        {/each}
      </div>
    {/if}
  {/if}
{/if}

<style>
  .sp-h1 { font-size: var(--fs-h1); line-height: var(--lh-h1); font-weight: 800; letter-spacing: -0.02em; margin: 0 0 var(--sp-4); }
  .sp-inrow { display: flex; gap: var(--sp-2); margin-bottom: var(--sp-4); }
  .sp-input { flex: 1; min-height: 48px; padding: 0 var(--sp-4); border-radius: var(--r-control); background: rgba(255,255,255,.05); border: 1px solid var(--line-strong); color: var(--text); font-size: 16px; font-family: inherit; }
  .sp-helper { color: var(--text-3); font-size: var(--fs-small); margin: 0 0 var(--sp-4); }
  .sp-heading { color: var(--text-2); font-size: var(--fs-small); margin: 0 0 var(--sp-4); }
  .sp-heading, .sp-helper { line-height: var(--lh-small); }
  .sp-cap { font-size: var(--fs-caption); line-height: var(--lh-caption); letter-spacing: var(--ls-caption); text-transform: uppercase; color: var(--text-3); font-weight: 700; display: block; margin-bottom: var(--sp-2); }
  .sp-recent-row { display: flex; flex-wrap: wrap; gap: var(--sp-2); }
  .sp-chip { display: inline-flex; align-items: center; gap: 6px; min-height: 40px; padding: 0 var(--sp-3); border-radius: var(--r-pill); background: rgba(255,255,255,.05); border: 1px solid var(--line-strong); color: var(--text-2); font-size: var(--fs-small); font-weight: 600; font-family: inherit; cursor: pointer; }
  .sp-chip:hover { color: var(--text); }
  .sp-skl { display: flex; flex-direction: column; gap: var(--sp-2); }
  .sp-group { font-size: var(--fs-h3); line-height: var(--lh-h3); font-weight: 800; margin: var(--sp-4) 0 var(--sp-3); }
  .sp-colls { display: flex; flex-direction: column; gap: var(--sp-2); }
  .sp-coll { display: flex; flex-direction: column; gap: 4px; padding: var(--sp-3) var(--sp-4); border: 1px solid var(--line); border-radius: var(--r-card); background: var(--surface); text-decoration: none; color: inherit; transition: border-color var(--dur) var(--ease); }
  .sp-coll:hover { border-color: var(--sky-35); text-decoration: none; }
  .sp-coll-top { display: flex; align-items: center; gap: var(--sp-2); flex-wrap: wrap; }
  .sp-coll-name { font-weight: 700; font-size: var(--fs-body); color: var(--text); }
  .sp-dim { color: var(--text-3); font-size: var(--fs-small); }
  .sp-grid { display: grid; grid-template-columns: repeat(auto-fill, minmax(160px, 1fr)); gap: var(--sp-3); }
  .sp-card { display: block; border: 1px solid var(--line); border-radius: var(--r-card); background: var(--surface); overflow: hidden; text-decoration: none; color: inherit; transition: transform var(--dur) var(--ease), border-color var(--dur) var(--ease); }
  .sp-card:hover { transform: translateY(-2px); border-color: var(--sky-35); text-decoration: none; }
  .sp-card-img { position: relative; display: block; aspect-ratio: 1; background: var(--bg); overflow: hidden; }
  .sp-card-img img { width: 100%; height: 100%; object-fit: cover; display: block; }
  .sp-noimg { display: flex; align-items: center; justify-content: center; height: 100%; color: var(--text-3); opacity: .4; }
  .sp-card-badge { position: absolute; top: var(--sp-2); left: var(--sp-2); }
  .sp-card-body { display: flex; flex-direction: column; gap: 2px; padding: var(--sp-3); }
  .sp-card-name { font-weight: 700; font-size: var(--fs-body); color: var(--text); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
</style>
