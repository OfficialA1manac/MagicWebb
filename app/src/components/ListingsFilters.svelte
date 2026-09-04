<script lang="ts">
  // Listings header + filter bar (spec B4 "Listings"). Owns the URL state
  // (`?collection=&min=&max=&sort=&page=`): reads it on load, rewrites it with
  // history.replaceState on Apply/Clear, and broadcasts the state to NFTGrid
  // via the FILTERS_EVENT window event — the grid refetches in place, the
  // page never navigates.
  import { onMount } from 'svelte';
  import Icon from './Icon.svelte';
  import Hint from './Hint.svelte';
  import { toast, toastError, toastSuccess } from '../lib/toast.svelte';
  import { currentChain, tradingLive } from '../lib/chains';
  import { shortAddr } from '../lib/format';
  import {
    jsonOrNull, parseListingsParams, listingsSearch, emitFilters, hasActiveFilters,
    EMPTY_FILTERS, CLEAR_FILTERS_EVENT, type ListingsFilterState, type ListingsSort,
  } from '../lib/api';

  type CollectionRow = { address: string; name: string; symbol?: string; verified?: boolean };
  type ListingRow = { collection: string; seller: string };
  type SavedSearch = { id: number | string; name: string; page: string; params: string };

  const live = tradingLive();
  const sym = currentChain().currency;
  const ADDR_RE = /^0x[0-9a-fA-F]{40}$/;

  // Applied state (mirrors the URL) vs the form's draft inputs.
  let applied = $state<ListingsFilterState>({ ...EMPTY_FILTERS });
  let draftCollection = $state('');
  let draftMin = $state('');
  let draftMax = $state('');
  let draftSort = $state<ListingsSort>('recent');
  let draftTraits = $state<Record<string, string>>({});

  let liveCount = $state<number | null>(null);
  let collections = $state<Array<CollectionRow & { listed: number }>>([]);
  let comboOpen = $state(false);
  let comboRef = $state<HTMLDivElement | undefined>();
  let traits = $state<Record<string, string[]>>({});
  let me = $state<string | null>(null);
  let myListedCount = $state(0);
  let mineOn = $state(false);

  // Saved searches (SIWE) — the button is visible for EVERYONE; viewers get
  // a disabled control with the Hint reason (spec).
  let savePanelOpen = $state(false);
  let saveName = $state('');
  let saving = $state(false);
  let savedOpen = $state(false);
  let saved = $state<SavedSearch[]>([]);

  const collName = (addr: string) => collections.find((c) => c.address.toLowerCase() === addr.toLowerCase())?.name || shortAddr(addr);

  function readWallet() {
    try {
      const a = window.MW?.address?.() ?? localStorage.getItem('mw_addr');
      me = a && ADDR_RE.test(a) ? a.toLowerCase() : null;
    } catch { me = null; }
    if (me) void loadMyListed(); else { myListedCount = 0; mineOn = false; }
  }

  async function loadMyListed() {
    if (!me) return;
    const rows = await jsonOrNull<ListingRow[]>(`/api/v1/listings?seller=${me}&limit=100`);
    myListedCount = rows?.length ?? 0;
  }

  async function loadCount() {
    const m = await jsonOrNull<{ totalActiveListings?: number }>('/api/v1/metrics');
    liveCount = typeof m?.totalActiveListings === 'number' ? m.totalActiveListings : null;
  }

  async function loadCollections() {
    const [cols, listings] = await Promise.all([
      jsonOrNull<CollectionRow[]>('/api/v1/collections?limit=200'),
      jsonOrNull<ListingRow[]>('/api/v1/listings?limit=100'),
    ]);
    const counts = new Map<string, number>();
    for (const l of listings ?? []) {
      const k = l.collection?.toLowerCase();
      if (k) counts.set(k, (counts.get(k) ?? 0) + 1);
    }
    collections = (cols ?? []).map((c) => ({ ...c, listed: counts.get(c.address.toLowerCase()) ?? 0 }));
  }

  async function loadTraits(addr: string) {
    traits = {};
    if (!addr || !ADDR_RE.test(addr)) return;
    traits = (await jsonOrNull<Record<string, string[]>>(`/api/v1/collections/${encodeURIComponent(addr)}/traits`)) ?? {};
  }

  function draftFromApplied() {
    draftCollection = applied.collection;
    draftMin = applied.min;
    draftMax = applied.max;
    draftSort = applied.sort;
    draftTraits = {};
    for (const pair of applied.traits ? applied.traits.split(',') : []) {
      const [k, v] = pair.split(':');
      if (k && v) draftTraits[k] = v;
    }
  }

  function stateFromDraft(): ListingsFilterState {
    const t = Object.entries(draftTraits).map(([k, v]) => `${k}:${v}`).join(',');
    return {
      collection: ADDR_RE.test(draftCollection) ? draftCollection.toLowerCase() : '',
      min: /^\d+(\.\d+)?$/.test(draftMin.trim()) ? draftMin.trim() : '',
      max: /^\d+(\.\d+)?$/.test(draftMax.trim()) ? draftMax.trim() : '',
      sort: draftSort,
      page: 1,
      traits: draftCollection ? t : '',
      seller: mineOn && me ? me : '',
    };
  }

  /** Apply/Clear write the URL in place (replaceState — never a navigation) and tell the grid. */
  function commit(next: ListingsFilterState) {
    applied = next;
    history.replaceState(null, '', location.pathname + listingsSearch(next));
    emitFilters(next);
  }

  function apply() { commit(stateFromDraft()); }
  function clearAll() {
    draftCollection = ''; draftMin = ''; draftMax = ''; draftSort = 'recent'; draftTraits = {}; traits = {}; mineOn = false;
    commit({ ...EMPTY_FILTERS });
  }

  function pickCollection(addr: string) {
    draftCollection = addr;
    comboOpen = false;
    draftTraits = {};
    void loadTraits(addr);
    apply();
  }

  function toggleTrait(t: string, v: string) {
    if (draftTraits[t] === v) delete draftTraits[t];
    else draftTraits[t] = v;
    draftTraits = { ...draftTraits };
    apply();
  }

  function toggleMine() { mineOn = !mineOn; apply(); }

  // Applied-filter chips (each with its own ×).
  type Chip = { key: string; label: string; remove: () => void };
  let chips = $derived.by<Chip[]>(() => {
    const out: Chip[] = [];
    if (applied.collection) out.push({ key: 'collection', label: collName(applied.collection), remove: () => { draftCollection = ''; draftTraits = {}; traits = {}; apply(); } });
    if (applied.min) out.push({ key: 'min', label: `Min ${applied.min} ${sym}`, remove: () => { draftMin = ''; apply(); } });
    if (applied.max) out.push({ key: 'max', label: `Max ${applied.max} ${sym}`, remove: () => { draftMax = ''; apply(); } });
    for (const pair of applied.traits ? applied.traits.split(',') : []) {
      const [k, v] = pair.split(':');
      if (k && v) out.push({ key: `trait:${k}`, label: `${k}: ${v}`, remove: () => { delete draftTraits[k]; draftTraits = { ...draftTraits }; apply(); } });
    }
    if (applied.seller) out.push({ key: 'seller', label: 'Your listings', remove: () => { mineOn = false; apply(); } });
    return out;
  });

  // ── Saved searches (SIWE) ─────────────────────────────────────────────
  async function saveSearch() {
    const name = saveName.trim();
    if (!name) { toastError('Give the search a name first'); return; }
    saving = true;
    try {
      const res = await window.MW!.authFetch('/api/v1/saved-searches', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        credentials: 'include',
        body: JSON.stringify({ name, page: 'listings', params: listingsSearch(applied).replace(/^\?/, '') }),
      });
      if (res.ok || res.status === 201) { toastSuccess('Search saved'); savePanelOpen = false; saveName = ''; }
      else toastError('Could not save this search');
    } catch { toastError('Could not save this search'); }
    saving = false;
  }

  async function loadSaved() {
    try {
      const res = await window.MW!.authFetch('/api/v1/saved-searches?limit=20&page=listings', { credentials: 'include' });
      const rows = res.ok ? ((await res.json()) as SavedSearch[]) : [];
      saved = Array.isArray(rows) ? rows.filter((s) => s.page === 'listings') : [];
    } catch { saved = []; }
  }

  function applySaved(s: SavedSearch) {
    const { filters } = parseListingsParams('?' + s.params);
    applied = filters;
    draftFromApplied();
    if (filters.collection) void loadTraits(filters.collection);
    commit(filters);
    savedOpen = false;
  }

  async function deleteSaved(s: SavedSearch) {
    try {
      const res = await window.MW!.authFetch(`/api/v1/saved-searches/${s.id}`, { method: 'DELETE', credentials: 'include' });
      if (res.ok || res.status === 204) saved = saved.filter((x) => x.id !== s.id);
    } catch { /* leave the row */ }
  }

  // "List an NFT": connect first if needed, then the profile NFT grid.
  function listAnNFT(e: MouseEvent) {
    const mw = window.MW;
    if (mw && !me) {
      e.preventDefault();
      mw.connect().then(() => { location.href = '/profile#nfts'; }).catch(() => {});
    }
  }

  onMount(() => {
    const { filters, invalid } = parseListingsParams(location.search);
    applied = filters;
    draftFromApplied();
    if (invalid.length) {
      toast('Some filters were invalid and were cleared');
      history.replaceState(null, '', location.pathname + listingsSearch(filters));
    }
    emitFilters(filters);
    if (filters.collection) void loadTraits(filters.collection);
    void loadCount();
    void loadCollections();
    readWallet();

    const onWallet = () => readWallet();
    const onClear = () => clearAll();
    const onDoc = (e: MouseEvent) => { if (comboRef && !comboRef.contains(e.target as Node)) comboOpen = false; };
    window.addEventListener('mw-wallet-changed', onWallet);
    window.addEventListener('mw-ready', onWallet);
    window.addEventListener(CLEAR_FILTERS_EVENT, onClear);
    document.addEventListener('click', onDoc, true);
    return () => {
      window.removeEventListener('mw-wallet-changed', onWallet);
      window.removeEventListener('mw-ready', onWallet);
      window.removeEventListener(CLEAR_FILTERS_EVENT, onClear);
      document.removeEventListener('click', onDoc, true);
    };
  });
</script>

<header class="lf-head">
  <div class="lf-title">
    <h1>Listings</h1>
    <span class="lf-pill" data-testid="live-count">{liveCount === null ? '—' : liveCount} live</span>
  </div>
  {#if live}
    <a class="btn btn-primary" href="/profile#nfts" onclick={listAnNFT}>List an NFT</a>
  {/if}
</header>

<form class="lf-bar" onsubmit={(e) => { e.preventDefault(); apply(); }} aria-label="Filter listings">
  <div class="field lf-combo" bind:this={comboRef}>
    <label for="lf-collection" id="lf-collection-label">Collection</label>
    <button
      type="button" id="lf-collection" class="lf-combo-btn" role="combobox"
      aria-expanded={comboOpen} aria-haspopup="listbox" aria-controls="lf-collection-list" aria-labelledby="lf-collection-label"
      onclick={() => (comboOpen = !comboOpen)}>
      <span class="lf-combo-val">{draftCollection ? collName(draftCollection) : 'All collections'}</span>
      <Icon name="chevron-down" size={18} />
    </button>
    {#if comboOpen}
      <ul class="lf-combo-list" id="lf-collection-list" role="listbox" aria-label="Collection">
        <li role="option" aria-selected={!draftCollection}>
          <button type="button" onclick={() => pickCollection('')}>All collections</button>
        </li>
        {#each collections as c (c.address)}
          <li role="option" aria-selected={draftCollection.toLowerCase() === c.address.toLowerCase()}>
            <button type="button" onclick={() => pickCollection(c.address)}>{c.name || shortAddr(c.address)} · {c.listed} listed</button>
          </li>
        {/each}
      </ul>
    {/if}
  </div>

  <div class="field lf-price">
    <label for="lf-min">Min price ({sym})</label>
    <input id="lf-min" inputmode="decimal" placeholder="0" bind:value={draftMin} />
  </div>
  <div class="field lf-price">
    <label for="lf-max">Max price ({sym})</label>
    <input id="lf-max" inputmode="decimal" placeholder="Any" bind:value={draftMax} />
  </div>

  <div class="field">
    <label for="lf-sort">Sort</label>
    <select id="lf-sort" bind:value={draftSort} onchange={apply}>
      <option value="recent">Newest</option>
      <option value="price_asc">Price low→high</option>
      <option value="price_desc">Price high→low</option>
      <option value="ending">Ending soon</option>
    </select>
  </div>

  <div class="lf-actions">
    <button type="submit" class="btn btn-primary lf-apply">Apply</button>
    <button type="button" class="btn btn-ghost" onclick={clearAll}>Clear</button>
    <span class="lf-save">
      <button
        type="button" class="btn btn-secondary" data-testid="save-search"
        disabled={!me} aria-disabled={!me ? 'true' : undefined}
        onclick={() => { savePanelOpen = !savePanelOpen; savedOpen = false; }}>★ Save search</button>
      {#if !me}<Hint text="Connect a wallet to save searches" label="Why is saving disabled?" align="end" />{/if}
    </span>
    {#if me}
      <button type="button" class="btn btn-secondary" onclick={() => { savedOpen = !savedOpen; savePanelOpen = false; if (savedOpen) void loadSaved(); }}>Saved ▾</button>
    {/if}
  </div>
</form>

{#if savePanelOpen && me}
  <div class="lf-savepanel">
    <label class="label" for="lf-savename">Name this search</label>
    <div class="lf-saverow">
      <input id="lf-savename" maxlength="100" placeholder="e.g. Animi under 10 {sym}" bind:value={saveName}
             onkeydown={(e) => { if (e.key === 'Enter') { e.preventDefault(); void saveSearch(); } }} />
      <button type="button" class="btn btn-primary" disabled={saving} onclick={() => void saveSearch()}>{saving ? 'Saving…' : 'Save'}</button>
      <button type="button" class="btn btn-ghost" onclick={() => (savePanelOpen = false)}>Cancel</button>
    </div>
  </div>
{/if}

{#if savedOpen && me}
  <div class="lf-savepanel" role="list" aria-label="Saved searches">
    {#if saved.length === 0}
      <p class="lf-dim">No saved searches yet.</p>
    {:else}
      {#each saved as s (s.id)}
        <div class="lf-savedrow" role="listitem">
          <button type="button" class="lf-savedname" onclick={() => applySaved(s)}>{s.name}</button>
          <button type="button" class="icon-btn" aria-label={`Delete saved search ${s.name}`} onclick={() => void deleteSaved(s)}><Icon name="x" size={16} /></button>
        </div>
      {/each}
    {/if}
  </div>
{/if}

{#if me && myListedCount > 0}
  <div class="lf-chips">
    <button type="button" class="lf-chip lf-chip-toggle" class:is-on={mineOn} aria-pressed={mineOn} onclick={toggleMine}>
      Your listings ({myListedCount})
    </button>
  </div>
{/if}

{#if chips.length}
  <div class="lf-chips" aria-label="Applied filters">
    {#each chips as c (c.key)}
      <span class="lf-chip" data-testid="filter-chip">
        {c.label}
        <button type="button" class="lf-chip-x" aria-label={`Remove filter ${c.label}`} onclick={c.remove}><Icon name="x" size={14} /></button>
      </span>
    {/each}
  </div>
{/if}

{#if applied.collection && Object.keys(traits).length}
  <div class="lf-traits">
    {#each Object.entries(traits).sort(([a], [b]) => a.localeCompare(b)) as [t, vals] (t)}
      <div class="lf-trait-group">
        <span class="lf-trait-name">{t}</span>
        <div class="lf-trait-vals">
          {#each vals.slice().sort() as v (v)}
            <button type="button" class="lf-chip lf-chip-toggle" class:is-on={draftTraits[t] === v} aria-pressed={draftTraits[t] === v} onclick={() => toggleTrait(t, v)}>{v}</button>
          {/each}
        </div>
      </div>
    {/each}
  </div>
{/if}

<style>
  .lf-head { display: flex; align-items: center; justify-content: space-between; gap: var(--sp-3); flex-wrap: wrap; margin-bottom: var(--sp-4); }
  .lf-title { display: flex; align-items: center; gap: var(--sp-3); }
  .lf-title h1 { font-size: var(--fs-h1); line-height: var(--lh-h1); font-weight: 800; margin: 0; letter-spacing: -0.02em; }
  .lf-pill { padding: 4px 12px; border-radius: var(--r-pill); background: var(--sky-12); border: 1px solid var(--sky-35); color: var(--sky-300); font-size: var(--fs-small); font-weight: 700; white-space: nowrap; }
  .lf-bar { display: flex; flex-wrap: wrap; align-items: end; gap: var(--sp-3); padding: var(--sp-4); border-radius: var(--r-card); background: var(--surface); border: 1px solid var(--line); margin-bottom: var(--sp-3); }
  .lf-bar input, .lf-bar select { min-height: var(--hit); padding: 0 var(--sp-3); border-radius: var(--r-control); background: rgba(255,255,255,.05); border: 1px solid var(--line-strong); color: var(--text); font-size: 16px; font-family: inherit; width: 100%; }
  .lf-price { width: 130px; }
  .lf-apply { min-height: var(--hit); }
  .lf-actions { display: flex; gap: var(--sp-2); flex-wrap: wrap; align-items: center; }
  .lf-save { display: inline-flex; align-items: center; gap: var(--sp-1); }
  .lf-combo { position: relative; min-width: 230px; flex: 1 1 230px; }
  .lf-combo-btn { min-height: var(--hit); padding: 0 var(--sp-3); border-radius: var(--r-control); background: rgba(255,255,255,.05); border: 1px solid var(--line-strong); color: var(--text); font-size: var(--fs-body); font-family: inherit; display: flex; align-items: center; justify-content: space-between; gap: var(--sp-2); cursor: pointer; width: 100%; }
  .lf-combo-val { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .lf-combo-list { position: absolute; top: calc(100% + 4px); left: 0; right: 0; z-index: var(--z-drawer); max-height: 280px; overflow: auto; margin: 0; padding: var(--sp-1); list-style: none; background: var(--surface-2); border: 1px solid var(--line-strong); border-radius: var(--r-control); box-shadow: var(--shadow); }
  .lf-combo-list button { display: block; width: 100%; text-align: left; min-height: var(--hit); padding: 0 var(--sp-3); border: 0; background: transparent; color: var(--text); font-family: inherit; font-size: var(--fs-body); border-radius: var(--r-control); cursor: pointer; }
  .lf-combo-list button:hover { background: rgba(255,255,255,.07); }
  .lf-combo-list [aria-selected="true"] button { color: var(--sky-300); font-weight: 700; }
  .lf-savepanel { padding: var(--sp-3) var(--sp-4); border-radius: var(--r-card); background: var(--surface); border: 1px solid var(--line); margin-bottom: var(--sp-3); display: flex; flex-direction: column; gap: var(--sp-2); }
  .lf-saverow { display: flex; gap: var(--sp-2); flex-wrap: wrap; }
  .lf-saverow input { flex: 1 1 200px; min-height: var(--hit); padding: 0 var(--sp-3); border-radius: var(--r-control); background: rgba(255,255,255,.05); border: 1px solid var(--line-strong); color: var(--text); font-size: 16px; font-family: inherit; }
  .lf-savedrow { display: flex; align-items: center; justify-content: space-between; gap: var(--sp-2); }
  .lf-savedname { background: transparent; border: 0; color: var(--text); font-family: inherit; font-size: var(--fs-body); font-weight: 600; min-height: var(--hit); cursor: pointer; text-align: left; flex: 1; padding: 0 var(--sp-2); border-radius: var(--r-control); }
  .lf-savedname:hover { background: rgba(255,255,255,.06); }
  .lf-dim { color: var(--text-3); font-size: var(--fs-small); margin: 0; }
  .lf-chips { display: flex; flex-wrap: wrap; gap: var(--sp-2); margin-bottom: var(--sp-3); }
  .lf-chip { display: inline-flex; align-items: center; gap: 6px; padding: 6px 12px; border-radius: var(--r-pill); background: var(--sky-12); border: 1px solid var(--sky-35); color: var(--sky-300); font-size: var(--fs-small); font-weight: 700; }
  .lf-chip-x { display: inline-flex; align-items: center; justify-content: center; width: 28px; height: 28px; margin: -6px -8px -6px 0; border: 0; border-radius: var(--r-pill); background: transparent; color: inherit; cursor: pointer; }
  .lf-chip-x:hover { background: rgba(255,255,255,.1); }
  .lf-chip-toggle { background: rgba(255,255,255,.05); border-color: var(--line-strong); color: var(--text-2); cursor: pointer; font-family: inherit; min-height: 36px; }
  .lf-chip-toggle.is-on { background: var(--sky-12); border-color: var(--sky); color: var(--sky-300); }
  .lf-traits { display: flex; flex-direction: column; gap: var(--sp-3); padding: var(--sp-4); border-radius: var(--r-card); background: var(--surface); border: 1px solid var(--line); margin-bottom: var(--sp-3); }
  .lf-trait-group { display: flex; flex-direction: column; gap: var(--sp-1); }
  .lf-trait-name { font-size: var(--fs-caption); line-height: var(--lh-caption); letter-spacing: var(--ls-caption); text-transform: uppercase; font-weight: 700; color: var(--text-3); }
  .lf-trait-vals { display: flex; flex-wrap: wrap; gap: var(--sp-1); }
  @media (max-width: 640px) { .lf-price { width: calc(50% - var(--sp-2)); } .lf-bar { padding: var(--sp-3); } }
</style>
