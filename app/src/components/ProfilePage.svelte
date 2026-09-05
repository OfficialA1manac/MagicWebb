<script lang="ts">
  // Profile island (spec B4 "Profile"). profile.astro is a thin shell; this
  // owns the header, the tab row (scroll-snap on mobile), per-tab content,
  // the own-profile seller tools (ERC-721 batch listing via MW.batchList),
  // the always-visible Refunds card, and the SIWE-gated edit-profile modal.
  // Data: /api/v1/profile/:addr + /api/v1/wallet/:addr/nfts +
  // /api/v1/profile-page/:addr, with a per-address sessionStorage snapshot
  // painted instantly on revisit (ProfilePage.helpers.ts).
  import { onMount, tick } from 'svelte';
  import EmptyState from './EmptyState.svelte';
  import ErrorState from './ErrorState.svelte';
  import Skeleton from './Skeleton.svelte';
  import Hint from './Hint.svelte';
  import Icon from './Icon.svelte';
  import VerifiedBadge from './VerifiedBadge.svelte';
  import RefundsPanel from './RefundsPanel.svelte';
  import { fmtAmount, fmtPrice, shortAddr, timeAgo, copyText } from '../lib/format';
  import { resolveImageUri } from '../lib/image-uri';
  import { currentChain, explorerAddress, tradingLive } from '../lib/chains';
  import { holderBadgeName, HOLDER_BADGE_TIP } from '../lib/holderBadge';
  import { DURATIONS, DEFAULT_DURATION } from '../lib/tx/durations';
  import { toastSuccess, toastError } from '../lib/toast.svelte';
  import {
    isEthAddr, resolveProfileAddr, tabsFor, emptyFor, mergeInventory, itemKey,
    isErc1155, batchEligible, batchBarLabel, validateBatch, initialsFor,
    saveSnapshot, loadSnapshot, createWalletGuard, installFirstTradeDone,
    markFirstTradeDone, HINT_1155,
    type InventoryItem, type ProfileTabId,
  } from './ProfilePage.helpers';

  type Profile = {
    display_name?: string; bio?: string; avatar_uri?: string; tag?: string;
    twitter?: string; website?: string; source_chain?: number;
  };
  type OfferRow = Record<string, unknown> & {
    collection?: string; token_id?: string; bidder?: string; status?: string;
    amount_wei?: string; principal_wei?: string;
    collection_verified?: boolean; collection_creator?: string; collection_name?: string;
  };
  type AuctionRow = InventoryItem & {
    auction_id?: string | number; status?: string;
    highest_bid_wei?: string; reserve_price_wei?: string;
  };
  type ActivityRow = {
    type?: string; status?: string; token_url?: string; collection?: string;
    tokenId?: string; amountWei?: string; timestamp?: string; ts?: string | number;
  };
  type Composite = {
    listings?: InventoryItem[]; auctions?: AuctionRow[];
    offersSent?: OfferRow[]; offersReceived?: OfferRow[];
    activity?: ActivityRow[]; createdCollections?: Array<{ address?: string; name?: string }>;
  };
  type Snapshot = { profile: Profile | null; nfts: InventoryItem[]; pp: Composite };

  const chain = currentChain();
  const sym = chain.currency;
  const canTrade = tradingLive();

  // `addr` is a prop so tests can mount without touching location.
  let { addr = '' }: { addr?: string } = $props();

  // ── State ────────────────────────────────────────────────────────────────
  let target = $state('');        // profile being shown ('' = none)
  let fromPath = $state(false);   // target came from /profile/:addr
  let connected = $state('');     // connected wallet ('' = none)
  let loading = $state(true);
  let failed = $state(false);     // hard failure with no data to show
  let haveData = $state(false);
  let stale = $state(false);      // painted from snapshot, refresh pending

  let profile = $state<Profile | null>(null);
  let nfts = $state<InventoryItem[]>([]);
  let pp = $state<Composite>({});

  let tab = $state<ProfileTabId>('items');
  let balance = $state('');       // own profile only

  // Batch listing (own profile)
  let selected = $state<Record<string, boolean>>({});
  let batchPrice = $state('');
  let batchDur = $state<number>(DEFAULT_DURATION);
  let batchErr = $state('');
  let batchBusy = $state(false);

  // Edit-profile modal
  let editOpen = $state(false);
  let editBusy = $state(false);
  let editErr = $state('');
  let modalEl = $state<HTMLElement | undefined>();
  let editForm = $state({ display_name: '', tag: '', bio: '', avatar_uri: '', twitter: '', website: '' });
  let tagMode = $state('');       // '' keep badge · preset name · '__custom__'

  let retries = 0;

  // ── Derived ──────────────────────────────────────────────────────────────
  let own = $derived(!!target && !!connected && target === connected);
  let listings = $derived(pp.listings ?? []);
  let auctions = $derived(pp.auctions ?? []);
  let offersSent = $derived(pp.offersSent ?? []);
  let offersRecv = $derived(pp.offersReceived ?? []);
  let activity = $derived(pp.activity ?? []);
  let createdColls = $derived(pp.createdCollections ?? []);
  let inventory = $derived(mergeInventory(nfts, listings, auctions as InventoryItem[]));
  let tabs = $derived(tabsFor(own));
  let displayName = $derived(profile?.display_name || shortAddr(target));
  let holderTag = $derived(profile?.tag || (target ? holderBadgeName(target) : ''));
  let selCount = $derived(Object.values(selected).filter(Boolean).length);
  let showBatchTools = $derived(own && canTrade);

  // ── Wallet / address plumbing ────────────────────────────────────────────
  function readStored(): string {
    try {
      const a = window.MW?.address?.() ?? localStorage.getItem('mw_addr');
      return isEthAddr(a) ? (a as string).toLowerCase() : '';
    } catch { return ''; }
  }

  const guard = createWalletGuard((a) => applyAddr(a));

  function applyAddr(a: string) {
    connected = readStored();
    if (fromPath) { void loadAll(target); return; } // header chips only
    target = a;
    if (!a) { haveData = false; loading = false; failed = false; return; }
    void loadAll(a);
  }

  function onWalletEvent() {
    connected = readStored();
    guard.notify(fromPath ? target : connected);
  }

  // ── Data loading ─────────────────────────────────────────────────────────
  function applySnapshot(t: string): boolean {
    const snap = loadSnapshot<Snapshot>(t);
    if (!snap) return false;
    profile = snap.profile;
    nfts = Array.isArray(snap.nfts) ? snap.nfts : [];
    pp = snap.pp ?? {};
    haveData = true;
    stale = true;
    return true;
  }

  function scheduleRetry(t: string, retryAfterSec = 0) {
    const wait = retryAfterSec || Math.min(30, 5 * 2 ** Math.min(retries, 3));
    retries += 1;
    setTimeout(() => { if (t === target) void loadAll(t); }, wait * 1000);
  }

  async function loadAll(t: string) {
    if (!t) return;
    if (!guard.beginLoad(t)) return; // overlap guard; guard.notify defers changes
    if (!haveData || stale || t !== target) {
      // Instant paint from the per-address snapshot; else the skeleton shows.
      if (t !== target || !haveData) {
        haveData = false;
        if (!applySnapshot(t)) loading = true;
      }
    }
    failed = false;
    try {
      const enc = encodeURIComponent(t);
      const [pRes, nftRes, ppRes] = await Promise.all([
        fetch(`/api/v1/profile/${enc}`).catch(() => null),
        fetch(`/api/v1/wallet/${enc}/nfts`).catch(() => null),
        fetch(`/api/v1/profile-page/${enc}`).catch(() => null),
      ]);
      const hardFailed = [pRes, nftRes, ppRes].some((r) => !r || (!r.ok && r.status !== 404));
      // Degraded wallet inventory (explorer unreachable): the backend still
      // answers 200 with a possibly-shrunken list. Never let it OVERWRITE
      // good data; on first paint render it and enrich in the background.
      const nftDegraded = !!nftRes?.headers?.get?.('X-MW-Degraded');
      if (hardFailed || (nftDegraded && haveData && !stale)) {
        let ra = 0;
        for (const r of [pRes, nftRes, ppRes]) {
          const h = r?.headers?.get?.('Retry-After');
          if (h) { ra = Math.min(30, parseInt(h, 10) || 5); break; }
        }
        if (!haveData && !applySnapshot(t)) failed = true;
        scheduleRetry(t, ra);
        return;
      }
      async function safe<T>(r: Response | null, fb: T): Promise<T> {
        if (!r || !r.ok) return fb;
        try { return (await r.json()) as T; } catch { return fb; }
      }
      profile = await safe<Profile | null>(pRes, null);
      const rawNfts = await safe<InventoryItem[]>(nftRes, []);
      nfts = Array.isArray(rawNfts) ? rawNfts : [];
      const rawPp = await safe<Composite>(ppRes, {});
      pp = rawPp && typeof rawPp === 'object' ? rawPp : {};
      haveData = true;
      stale = false;
      failed = false;
      if (nftDegraded) scheduleRetry(t); else retries = 0;
      if (!nftDegraded) saveSnapshot(t, { profile, nfts, pp } satisfies Snapshot);
      if (own) void loadBalance(t);
    } catch {
      guard.reset(); // future wallet events may retry the same address
      if (!haveData) failed = true;
    } finally {
      loading = false;
      const next = guard.endLoad();
      if (next !== null) applyAddr(next);
    }
  }

  async function loadBalance(t: string) {
    try {
      const r = await fetch(chain.rpc, {
        method: 'POST', headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ jsonrpc: '2.0', id: 1, method: 'eth_getBalance', params: [t, 'latest'] }),
      });
      const d = await r.json();
      balance = d?.result ? fmtPrice(BigInt(d.result)) : '';
    } catch { balance = ''; }
  }

  // ── Batch listing ────────────────────────────────────────────────────────
  function toggleSelect(it: InventoryItem, on: boolean) {
    selected = { ...selected, [itemKey(it)]: on };
  }

  async function batchGo() {
    batchErr = '';
    const v = validateBatch(selCount, batchPrice, sym);
    if (!v.ok) { batchErr = v.error; return; }
    const mw = window.MW;
    if (!mw) { batchErr = 'Wallet bridge not ready — reload the page.'; return; }
    const items = inventory
      .filter((it) => selected[itemKey(it)] && batchEligible(it, own))
      .map((it) => ({ nft: String(it.collection), tokenId: String(it.token_id ?? it.tokenID), priceWei: v.wei.toString(), duration: batchDur }));
    if (!items.length) return;
    batchBusy = true;
    try {
      await mw.batchList({ items });
      toastSuccess(`Listed ${items.length} NFT${items.length === 1 ? '' : 's'} — one price, one duration.`);
      markFirstTradeDone();
      selected = {};
      batchPrice = '';
      // In-place refresh (never location.reload): once quickly, once after
      // the indexer has certainly seen the events.
      setTimeout(() => void loadAll(target), 1500);
      setTimeout(() => void loadAll(target), 6000);
    } catch { /* TxModal showed the error */ }
    batchBusy = false;
  }

  // ── Header actions ───────────────────────────────────────────────────────
  async function copyAddr() {
    (await copyText(target)) ? toastSuccess('Address copied') : toastError('Could not copy the address');
  }

  // ── Edit-profile modal (kept, SIWE-gated as before) ──────────────────────
  const chainNames: Record<number, string> = { 114: 'Coston2', 19: 'Songbird', 14: 'Flare' };
  let carryOver = $derived.by(() => {
    const src = Number(profile?.source_chain || 0);
    return src && src !== chain.id ? `Profile from ${chainNames[src] ?? `chain ${src}`}` : '';
  });

  function openEdit() {
    editErr = '';
    editForm = {
      display_name: profile?.display_name || '',
      tag: profile?.tag || '',
      bio: profile?.bio || '',
      avatar_uri: profile?.avatar_uri || '',
      twitter: profile?.twitter || '',
      website: profile?.website || '',
    };
    tagMode = !profile?.tag ? '' : holderNames.includes(profile.tag) ? profile.tag : '__custom__';
    editOpen = true;
    void tick().then(() => (modalEl?.querySelector('input') as HTMLInputElement | null)?.focus());
  }

  function closeEdit() { editOpen = false; }

  function onModalKey(e: KeyboardEvent) {
    if (e.key === 'Escape') { e.stopPropagation(); closeEdit(); return; }
    if (e.key !== 'Tab' || !modalEl) return;
    // Focus trap: Tab and Shift+Tab cycle within the dialog.
    const f = modalEl.querySelectorAll<HTMLElement>('a[href], button:not([disabled]), input:not([disabled]), textarea:not([disabled]), select:not([disabled])');
    if (!f.length) return;
    const first = f[0], last = f[f.length - 1];
    if (e.shiftKey && document.activeElement === first) { e.preventDefault(); last.focus(); }
    else if (!e.shiftKey && document.activeElement === last) { e.preventDefault(); first.focus(); }
  }

  function onTagMode(v: string) {
    tagMode = v;
    if (v === '__custom__') editForm.tag = '';
    else editForm.tag = v; // '' = keep collector badge
  }

  async function saveProfile(e: SubmitEvent) {
    e.preventDefault();
    editBusy = true;
    editErr = '';
    try {
      const doFetch = window.MW?.authFetch ?? fetch;
      const res = await doFetch(`/api/v1/profile/${encodeURIComponent(target)}`, {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        credentials: 'include',
        body: JSON.stringify({
          display_name: editForm.display_name.trim(),
          tag: editForm.tag.trim(),
          bio: editForm.bio.trim(),
          avatar_uri: editForm.avatar_uri.trim(),
          twitter: editForm.twitter.trim(),
          website: editForm.website.trim(),
        }),
      });
      if (res.ok) {
        toastSuccess('Profile saved');
        closeEdit();
        void loadAll(target); // in-place, no reload
      } else {
        let msg = 'Failed to save';
        try { msg = (await res.json())?.error || msg; } catch { /* keep default */ }
        editErr = msg;
      }
    } catch (err) {
      editErr = err instanceof Error && err.message ? err.message : 'Network error — please try again';
    }
    editBusy = false;
  }

  // Preset tag options — same list as the collector badge.
  import { HOLDER_NAMES } from '../lib/holderBadge';
  const holderNames: readonly string[] = HOLDER_NAMES;

  // ── Tab helpers ──────────────────────────────────────────────────────────
  function switchTab(id: ProfileTabId) { tab = id; }
  function onTabKey(e: KeyboardEvent, i: number) {
    let next = -1;
    if (e.key === 'ArrowRight') next = (i + 1) % tabs.length;
    else if (e.key === 'ArrowLeft') next = (i - 1 + tabs.length) % tabs.length;
    else if (e.key === 'Home') next = 0;
    else if (e.key === 'End') next = tabs.length - 1;
    if (next < 0) return;
    e.preventDefault();
    tab = tabs[next].id;
    (document.getElementById(`pp-tab-${tabs[next].id}`) as HTMLElement | null)?.focus();
  }

  function emptyCta(id: ProfileTabId) {
    const em = emptyFor(id, own);
    if (!em.cta) return undefined;
    if (em.cta.href === '#items') return { label: em.cta.label, onclick: () => switchTab('items') };
    return { label: em.cta.label, href: em.cta.href };
  }

  function offerAmount(o: OfferRow): string {
    return fmtAmount(o.amount_wei || o.principal_wei || '0', sym);
  }

  function actColor(t: string): string {
    const s = String(t || '').toLowerCase();
    if (s.includes('sold') || s.includes('offer')) return 'var(--gold-300)';
    if (s.includes('bid') || s.includes('auction')) return 'var(--violet)';
    return 'var(--sky-300)';
  }

  onMount(() => {
    installFirstTradeDone();
    if (addr && isEthAddr(addr)) { target = addr.toLowerCase(); fromPath = true; }
    else {
      const r = resolveProfileAddr(location.pathname, readStored());
      target = r.target;
      fromPath = r.fromPath;
    }
    connected = readStored();
    if (target) void loadAll(target); else loading = false;
    window.addEventListener('mw-wallet-changed', onWalletEvent);
    window.addEventListener('mw-ready', onWalletEvent);
    return () => {
      window.removeEventListener('mw-wallet-changed', onWalletEvent);
      window.removeEventListener('mw-ready', onWalletEvent);
      guard.destroy();
    };
  });
</script>

{#if !target}
  <!-- No wallet & no address (spec): EmptyState with h1 + connect + search link. -->
  <h1 class="pp-solo-h1">Profile</h1>
  <EmptyState
    title="Connect your wallet to see your NFTs"
    body="Your wallet is your login — no account needed."
    icon="wallet"
    cta={{ label: 'Connect wallet', onclick: () => window.__MW_APPKIT_OPEN__?.() }}
    secondary={{ label: 'Looking for someone? Paste their address in Search', href: '/search' }} />
{:else if failed && !haveData}
  <h1 class="pp-solo-h1">Profile</h1>
  <ErrorState title="Could not load this profile" message="Check your connection — we retry automatically." retry={() => void loadAll(target)} />
{:else if loading && !haveData}
  <div class="pp-skel" aria-hidden="true">
    <div class="pp-skel-head"><Skeleton w="72px" h="72px" r="16px" /><div class="pp-skel-lines"><Skeleton w="40%" h="24px" /><Skeleton w="60%" h="14px" /></div></div>
    <div class="pp-grid">{#each Array(4) as _, i (i)}<Skeleton card />{/each}</div>
  </div>
{:else}
  {#if own}
    <RefundsPanel />
  {/if}

  <header class="pp-head">
    <div class="pp-avatar" aria-hidden="true">
      {#if profile?.avatar_uri}
        <img src={profile.avatar_uri} alt="" onerror={(e) => ((e.currentTarget as HTMLImageElement).style.display = 'none')} />
      {/if}
      <span class="pp-initials">{initialsFor(profile?.display_name || target)}</span>
    </div>
    <div class="pp-headmain">
      <div class="pp-titlerow">
        <h1>{displayName}</h1>
        {#if own}<span class="pp-you">This is you</span>{/if}
        {#if holderTag}
          <span class="pp-tag">{holderTag}</span>
          <Hint text={profile?.tag ? 'Profile tag — set by this user.' : HOLDER_BADGE_TIP} label="About this tag" />
        {/if}
        {#if createdColls.length}
          <a class="pp-creator" href="/docs/faq#creator" title="Collection creator"><span aria-hidden="true">★</span> Creator</a>
        {/if}
      </div>
      <p class="pp-addr mono">
        {shortAddr(target)}
        <button type="button" class="pp-iconbtn" aria-label="Copy address" onclick={() => void copyAddr()}><Icon name="copy" size={16} /></button>
        <a class="pp-explorer" href={explorerAddress(target)} target="_blank" rel="noopener">Explorer <Icon name="external" size={14} /></a>
      </p>
      {#if profile?.bio}<p class="pp-bio">{profile.bio}</p>{/if}
      {#if carryOver}<p class="pp-carry">{carryOver}</p>{/if}
      {#if own}
        <p class="pp-balance">Balance: <span class="mono">{balance === '' ? '…' : balance}</span> {sym}</p>
      {/if}
    </div>
    {#if own}
      <div class="pp-actions">
        <button type="button" class="btn btn-secondary" onclick={openEdit}>Edit profile</button>
      </div>
    {/if}
  </header>

  {#if stale}
    <p class="pp-stale" role="status">Showing your last data while we refresh…</p>
  {/if}

  <!-- Tabs: one tab stop, arrows move inside; scroll-snap row on mobile. -->
  <div class="pp-tabswrap">
    <div class="pp-tabs" role="tablist" aria-label="Profile sections">
      {#each tabs as t, i (t.id)}
        <button role="tab" id={`pp-tab-${t.id}`} aria-selected={tab === t.id} aria-controls="pp-panel"
                tabindex={tab === t.id ? 0 : -1} class:is-on={tab === t.id}
                onclick={() => switchTab(t.id)} onkeydown={(e) => onTabKey(e, i)}>
          {t.label}
        </button>
      {/each}
    </div>
  </div>

  <section id="pp-panel" role="tabpanel" aria-labelledby={`pp-tab-${tab}`}>
    {#if tab === 'items'}
      {#if showBatchTools && inventory.some((it) => batchEligible(it, own))}
        <p class="pp-batchhint">Tick unlisted ERC-721 items to list several at once (max 50).</p>
      {/if}
      {#if inventory.length === 0}
        {@const em = emptyFor('items', own)}
        <EmptyState title={em.title} body={em.body} icon="image" cta={emptyCta('items')} />
      {:else}
        <div class="pp-grid">
          {#each inventory as it (itemKey(it))}
            {@const tid = String(it.token_id ?? it.tokenID ?? '')}
            {@const selectable = showBatchTools && batchEligible(it, own)}
            <div class="pp-card">
              <a class="pp-stretch" href={`/token/${encodeURIComponent(String(it.collection))}/${encodeURIComponent(tid)}`} aria-label={it.name || `#${tid}`}></a>
              {#if selectable}
                <input type="checkbox" class="pp-cb" checked={!!selected[itemKey(it)]}
                       aria-label={`Select ${it.name || `#${tid}`} for batch listing`}
                       onchange={(e) => toggleSelect(it, (e.currentTarget as HTMLInputElement).checked)} />
              {/if}
              {#if showBatchTools && isErc1155(it)}
                <span class="pp-hint1155"><Hint text={HINT_1155} label="Why can't I select this?" align="start" /></span>
              {/if}
              <span class="pp-img">
                {#if it.image_uri}
                  <img src={resolveImageUri(it.image_uri, tid, 256)} alt="" loading="lazy" />
                {:else}
                  <span class="pp-noimg"><Icon name="image" size={32} /></span>
                {/if}
                {#if it.price_wei}<span class="pp-price mono">{fmtAmount(it.price_wei, sym)}</span>{/if}
                {#if it._escrowed}<span class="pp-escrow">In auction</span>{/if}
              </span>
              <span class="pp-cardbody">
                <span class="pp-cardmeta">
                  <span class="pp-coll mono">{shortAddr(String(it.collection || ''))}</span>
                  <VerifiedBadge verified={it.collection_verified === true} creatorAddr={it.collection_creator || ''} tracked={it.collection_tracked ?? undefined} collectionName={it.collection_name || ''} link={false} hint={false} />
                </span>
                <span class="pp-cardname">{it.name || `#${tid}`}</span>
              </span>
            </div>
          {/each}
        </div>
      {/if}
    {:else if tab === 'sale'}
      {#if listings.length === 0}
        {@const em = emptyFor('sale', own)}
        <EmptyState title={em.title} body={em.body} icon="tag" cta={emptyCta('sale')} />
      {:else}
        <div class="pp-grid">
          {#each listings as it (itemKey(it))}
            {@const tid = String(it.token_id ?? it.tokenID ?? '')}
            <div class="pp-card">
              <a class="pp-stretch" href={`/token/${encodeURIComponent(String(it.collection))}/${encodeURIComponent(tid)}`} aria-label={it.name || `#${tid}`}></a>
              <span class="pp-img">
                {#if it.image_uri}<img src={resolveImageUri(it.image_uri, tid, 256)} alt="" loading="lazy" />{:else}<span class="pp-noimg"><Icon name="image" size={32} /></span>{/if}
                {#if it.price_wei}<span class="pp-price mono">{fmtAmount(it.price_wei, sym)}</span>{/if}
              </span>
              <span class="pp-cardbody">
                <span class="pp-cardmeta">
                  <span class="pp-coll mono">{shortAddr(String(it.collection || ''))}</span>
                  <VerifiedBadge verified={it.collection_verified === true} creatorAddr={it.collection_creator || ''} tracked={it.collection_tracked ?? undefined} collectionName={it.collection_name || ''} link={false} hint={false} />
                </span>
                <span class="pp-cardname">{it.name || `#${tid}`}</span>
              </span>
            </div>
          {/each}
        </div>
      {/if}
    {:else if tab === 'auctions'}
      {#if auctions.length === 0}
        {@const em = emptyFor('auctions', own)}
        <EmptyState title={em.title} body={em.body} icon="gavel" cta={emptyCta('auctions')} />
      {:else}
        <div class="pp-list">
          {#each auctions as a (String(a.auction_id))}
            <a class="pp-row" href={`/auction/${encodeURIComponent(String(a.auction_id))}`}>
              <span class="pp-rowmain">Auction #{a.auction_id}
                <span class="pp-coll mono">{shortAddr(String(a.collection || ''))} #{a.token_id}</span>
              </span>
              <span class="pp-chip">{a.status || 'active'}</span>
              <span class="pp-amt mono">{fmtAmount(a.highest_bid_wei || a.reserve_price_wei || '0', sym)}</span>
            </a>
          {/each}
        </div>
      {/if}
    {:else if tab === 'offers'}
      {#if offersSent.length === 0 && offersRecv.length === 0}
        {@const em = emptyFor('offers', own)}
        <EmptyState title={em.title} body={em.body} icon="inbox" cta={emptyCta('offers')} />
      {:else}
        {#if offersRecv.length}
          <h2 class="pp-subhead">Received ({offersRecv.length})</h2>
          <div class="pp-list">
            {#each offersRecv as o, i (String(o.collection) + String(o.token_id) + String(o.bidder ?? i))}
              <a class="pp-row" href={`/token/${encodeURIComponent(String(o.collection))}/${encodeURIComponent(String(o.token_id))}`}>
                <span class="pp-rowmain">From {shortAddr(String(o.bidder || ''))}
                  <span class="pp-coll mono">{shortAddr(String(o.collection || ''))} #{o.token_id}</span>
                </span>
                <span class="pp-chip">{o.status || 'pending'}</span>
                <span class="pp-amt mono">{offerAmount(o)}</span>
              </a>
            {/each}
          </div>
        {/if}
        {#if offersSent.length}
          <h2 class="pp-subhead">Sent ({offersSent.length})</h2>
          <div class="pp-list">
            {#each offersSent as o, i (String(o.collection) + String(o.token_id) + String(i))}
              <a class="pp-row" href={`/token/${encodeURIComponent(String(o.collection))}/${encodeURIComponent(String(o.token_id))}`}>
                <span class="pp-rowmain">{shortAddr(String(o.collection || ''))} #{o.token_id}</span>
                <span class="pp-chip">{o.status || 'pending'}</span>
                <span class="pp-amt mono">{offerAmount(o)}</span>
              </a>
            {/each}
          </div>
        {/if}
      {/if}
    {:else}
      {#if activity.length === 0}
        {@const em = emptyFor('activity', own)}
        <EmptyState title={em.title} body={em.body} icon="chart" />
      {:else}
        <div class="pp-list">
          {#each activity as a, i (String(a.ts ?? a.timestamp ?? i) + String(a.type))}
            <div class="pp-row">
              <span class="pp-acttype" style={`color:${actColor(String(a.type))}`}>{a.type}</span>
              {#if a.status}<span class="pp-chip">{a.status}</span>{/if}
              {#if a.collection}
                <a class="pp-coll mono pp-toklink" href={a.token_url || `/token/${encodeURIComponent(a.collection)}/${encodeURIComponent(String(a.tokenId ?? ''))}`}>
                  {shortAddr(a.collection)}{a.tokenId ? ` #${a.tokenId}` : ''}
                </a>
              {/if}
              <span class="pp-spacer"></span>
              {#if a.amountWei && a.amountWei !== '0'}<span class="pp-amt mono">{fmtAmount(a.amountWei, sym)}</span>{/if}
              {#if a.ts || a.timestamp}<span class="pp-when">{timeAgo((a.ts ?? a.timestamp) as string)}</span>{/if}
            </div>
          {/each}
        </div>
      {/if}
    {/if}
  </section>

  <!-- Sticky batch bar (own profile, ERC-721 selection > 0) -->
  {#if showBatchTools && selCount > 0}
    <div class="pp-batchbar" role="region" aria-label="Batch listing">
      <span class="pp-batchlabel">{batchBarLabel(selCount)}</span>
      <input class="pp-batchprice mono" inputmode="decimal" placeholder={`Price each (${sym}, min 1)`} bind:value={batchPrice} aria-label="Price for every selected NFT" />
      <select class="pp-batchdur" bind:value={batchDur} aria-label="Duration">
        {#each DURATIONS as d (d.seconds)}<option value={d.seconds}>{d.label}</option>{/each}
      </select>
      <button type="button" class="btn btn-primary" disabled={batchBusy} onclick={() => void batchGo()}>
        {batchBusy ? 'Waiting for wallet…' : `List ${selCount} selected · free`}
      </button>
      {#if batchErr}<span class="pp-batcherr" role="alert">{batchErr}</span>{/if}
    </div>
  {/if}

  <!-- Edit-profile modal (SIWE-gated via MW.authFetch) -->
  {#if editOpen}
    <!-- svelte-ignore a11y_no_noninteractive_element_interactions a11y_click_events_have_key_events -->
    <div class="pp-overlay" role="presentation" onclick={(e) => { if (e.target === e.currentTarget) closeEdit(); }} onkeydown={onModalKey}>
      <div class="pp-modal" role="dialog" aria-modal="true" aria-labelledby="pp-edit-title" bind:this={modalEl}>
        <div class="pp-modalhead">
          <h2 id="pp-edit-title">Edit profile</h2>
          <button type="button" class="pp-iconbtn" aria-label="Close edit profile" onclick={closeEdit}><Icon name="x" size={18} /></button>
        </div>
        <form onsubmit={saveProfile}>
          <label class="pp-field">Display name
            <input name="display_name" maxlength="64" bind:value={editForm.display_name} placeholder="Your display name" />
          </label>
          <label class="pp-field">Tag
            <select value={tagMode} onchange={(e) => onTagMode((e.currentTarget as HTMLSelectElement).value)}>
              <option value="">(keep collector badge — {holderBadgeName(target)})</option>
              {#each holderNames as n (n)}<option value={n}>{n}</option>{/each}
              <option value="__custom__">Custom…</option>
            </select>
          </label>
          {#if tagMode === '__custom__'}
            <label class="pp-field">Custom tag
              <input name="tag" maxlength="32" bind:value={editForm.tag} placeholder={holderBadgeName(target)} />
            </label>
          {/if}
          <label class="pp-field">Bio
            <textarea name="bio" maxlength="500" bind:value={editForm.bio} placeholder="Tell the community about yourself…"></textarea>
          </label>
          <label class="pp-field">Avatar URL
            <input name="avatar_uri" bind:value={editForm.avatar_uri} placeholder="https://example.com/avatar.png" />
          </label>
          <label class="pp-field">Twitter / X
            <input name="twitter" bind:value={editForm.twitter} placeholder="@username or full URL" />
          </label>
          <label class="pp-field">Website
            <input name="website" bind:value={editForm.website} placeholder="https://yoursite.com" />
          </label>
          {#if editErr}<p class="pp-editerr" role="alert">{editErr}</p>{/if}
          <div class="pp-modalfoot">
            <button type="button" class="btn btn-secondary" onclick={closeEdit}>Cancel</button>
            <button type="submit" class="btn btn-primary" disabled={editBusy}>{editBusy ? 'Saving…' : 'Save changes'}</button>
          </div>
        </form>
      </div>
    </div>
  {/if}
{/if}

<style>
  .pp-solo-h1 { font-size: var(--fs-h1); line-height: var(--lh-h1); font-weight: 800; margin: 0 0 var(--sp-4); }

  .pp-skel { display: flex; flex-direction: column; gap: var(--sp-6); }
  .pp-skel-head { display: flex; gap: var(--sp-4); align-items: center; }
  .pp-skel-lines { flex: 1; display: flex; flex-direction: column; gap: var(--sp-2); }

  /* Header */
  .pp-head { display: flex; gap: var(--sp-4); align-items: flex-start; margin-bottom: var(--sp-6); flex-wrap: wrap; }
  .pp-avatar { position: relative; width: 72px; height: 72px; border-radius: var(--r-card); background: var(--sky-12); border: 1px solid var(--sky-35); overflow: hidden; flex: 0 0 auto; display: flex; align-items: center; justify-content: center; }
  .pp-avatar img { position: absolute; inset: 0; width: 100%; height: 100%; object-fit: cover; }
  .pp-initials { color: var(--sky-300); font-weight: 800; font-size: 22px; }
  .pp-headmain { min-width: 0; flex: 1; display: flex; flex-direction: column; gap: var(--sp-1); }
  .pp-titlerow { display: flex; align-items: center; gap: var(--sp-2); flex-wrap: wrap; }
  .pp-titlerow h1 { font-size: var(--fs-h1); line-height: var(--lh-h1); font-weight: 800; margin: 0; letter-spacing: -0.02em; overflow-wrap: anywhere; }
  .pp-you { padding: 2px 10px; border-radius: var(--r-pill); background: var(--green-12); border: 1px solid rgba(74,222,128,.35); color: var(--green); font-size: var(--fs-caption); font-weight: 800; letter-spacing: var(--ls-caption); text-transform: uppercase; }
  .pp-tag { padding: 2px 10px; border-radius: var(--r-pill); background: var(--violet-12); border: 1px solid var(--violet-35); color: var(--violet); font-size: var(--fs-caption); font-weight: 700; }
  .pp-creator { padding: 2px 10px; border-radius: var(--r-pill); background: var(--gold-12); border: 1px solid var(--gold-35); color: var(--gold-300); font-size: var(--fs-caption); font-weight: 800; text-decoration: none; }
  .pp-addr { display: flex; align-items: center; gap: var(--sp-2); margin: 0; color: var(--text-2); font-size: var(--fs-small); overflow-wrap: anywhere; }
  .pp-iconbtn { width: var(--hit); height: var(--hit); margin: -12px 0; display: inline-flex; align-items: center; justify-content: center; background: transparent; border: 0; border-radius: var(--r-control); color: var(--text-3); cursor: pointer; }
  .pp-iconbtn:hover { color: var(--text); background: rgba(255,255,255,.06); }
  .pp-explorer { display: inline-flex; align-items: center; gap: 4px; color: var(--text-2); min-height: var(--hit); }
  .pp-explorer:hover { color: var(--text); }
  .pp-bio { margin: 0; color: var(--text-2); font-size: var(--fs-body); line-height: var(--lh-body); max-width: 36rem; overflow-wrap: anywhere; }
  .pp-carry { margin: 0; color: var(--text-3); font-size: var(--fs-caption); font-style: italic; }
  .pp-balance { margin: 0; color: var(--text-2); font-size: var(--fs-small); }
  .pp-actions { display: flex; align-items: center; gap: var(--sp-2); }
  .pp-stale { margin: 0 0 var(--sp-3); color: var(--text-3); font-size: var(--fs-caption); }

  /* Tabs — scroll-snap row with fade edges on mobile (spec). */
  .pp-tabswrap { position: relative; margin-bottom: var(--sp-4); }
  .pp-tabs { display: flex; gap: var(--sp-2); border-bottom: 1px solid var(--line); overflow-x: auto; scrollbar-width: none; }
  .pp-tabs::-webkit-scrollbar { display: none; }
  .pp-tabs button { min-height: var(--hit); padding: 0 var(--sp-4); background: transparent; border: 0; border-bottom: 2px solid transparent; color: var(--text-2); font-family: inherit; font-size: var(--fs-body); font-weight: 700; cursor: pointer; white-space: nowrap; }
  .pp-tabs button.is-on { color: var(--text); border-bottom-color: var(--sky); }
  @media (max-width: 767px) {
    .pp-tabs { scroll-snap-type: x proximity; }
    .pp-tabs button { scroll-snap-align: start; }
    .pp-tabswrap::before, .pp-tabswrap::after { content: ''; position: absolute; top: 0; bottom: 1px; width: 20px; pointer-events: none; z-index: 1; }
    .pp-tabswrap::before { left: 0; background: linear-gradient(90deg, var(--bg), transparent); }
    .pp-tabswrap::after { right: 0; background: linear-gradient(-90deg, var(--bg), transparent); }
  }

  /* Cards */
  .pp-batchhint { margin: 0 0 var(--sp-3); color: var(--text-3); font-size: var(--fs-caption); }
  .pp-grid { display: grid; grid-template-columns: repeat(1, 1fr); gap: var(--sp-4); }
  @media (min-width: 640px) { .pp-grid { grid-template-columns: repeat(2, 1fr); } }
  @media (min-width: 960px) { .pp-grid { grid-template-columns: repeat(3, 1fr); } }
  @media (min-width: 1280px) { .pp-grid { grid-template-columns: repeat(4, 1fr); } }
  .pp-card { position: relative; border: 1px solid var(--line); border-radius: var(--r-card); overflow: hidden; background: var(--surface); transition: border-color var(--dur-fast) var(--ease); }
  .pp-card:hover { border-color: var(--sky-35); }
  /* The card is a <div> with a stretched link: the checkbox and Hint are
     interactive and may not nest inside an <a>. */
  .pp-stretch { position: absolute; inset: 0; z-index: 1; }
  .pp-cb { position: absolute; top: var(--sp-2); left: var(--sp-2); z-index: 3; width: 20px; height: 20px; accent-color: var(--sky); cursor: pointer; }
  .pp-hint1155 { position: absolute; top: var(--sp-2); left: var(--sp-2); z-index: 3; }
  .pp-img { position: relative; display: block; aspect-ratio: 1; background: var(--ink-950, #09090b); }
  .pp-img img { width: 100%; height: 100%; object-fit: cover; display: block; }
  .pp-noimg { position: absolute; inset: 0; display: flex; align-items: center; justify-content: center; color: var(--text-3); }
  .pp-price { position: absolute; top: var(--sp-2); right: var(--sp-2); padding: 2px 10px; border-radius: var(--r-pill); background: rgba(9,9,11,.75); border: 1px solid var(--gold-35); color: var(--gold-300); font-size: var(--fs-caption); font-weight: 800; backdrop-filter: blur(4px); }
  .pp-escrow { position: absolute; bottom: var(--sp-2); left: var(--sp-2); padding: 2px 10px; border-radius: var(--r-pill); background: var(--violet-12); border: 1px solid var(--violet-35); color: var(--violet); font-size: var(--fs-caption); font-weight: 800; letter-spacing: var(--ls-caption); text-transform: uppercase; }
  .pp-cardbody { display: flex; flex-direction: column; gap: 2px; padding: var(--sp-3); position: relative; z-index: 2; pointer-events: none; }
  .pp-cardmeta { display: flex; align-items: center; gap: var(--sp-2); }
  .pp-coll { color: var(--text-3); font-size: var(--fs-caption); }
  .pp-cardname { font-weight: 700; font-size: var(--fs-body); color: var(--text); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }

  /* Rows (auctions / offers / activity) */
  .pp-subhead { font-size: var(--fs-caption); line-height: var(--lh-caption); letter-spacing: var(--ls-caption); text-transform: uppercase; font-weight: 800; color: var(--text-3); margin: var(--sp-4) 0 var(--sp-2); }
  .pp-list { list-style: none; display: flex; flex-direction: column; gap: var(--sp-2); }
  .pp-row { display: flex; align-items: center; gap: var(--sp-3); flex-wrap: wrap; padding: var(--sp-3) var(--sp-4); min-height: 52px; border-radius: var(--r-card); background: var(--surface); border: 1px solid var(--line); font-size: var(--fs-small); color: var(--text); text-decoration: none; }
  a.pp-row:hover { border-color: var(--sky-35); }
  .pp-rowmain { display: flex; flex-direction: column; gap: 2px; font-weight: 600; flex: 1; min-width: 0; }
  .pp-chip { padding: 2px 10px; border-radius: var(--r-pill); background: rgba(255,255,255,.06); color: var(--text-2); font-size: var(--fs-caption); font-weight: 700; text-transform: uppercase; letter-spacing: var(--ls-caption); }
  .pp-amt { color: var(--gold-300); font-weight: 700; }
  .pp-acttype { font-weight: 800; text-transform: uppercase; font-size: var(--fs-caption); letter-spacing: var(--ls-caption); }
  .pp-toklink { text-decoration: underline; text-underline-offset: 2px; }
  .pp-toklink:hover { color: var(--text); }
  .pp-spacer { flex: 1; }
  .pp-when { color: var(--text-3); font-size: var(--fs-caption); }

  /* Sticky batch bar */
  .pp-batchbar { position: sticky; bottom: calc(60px + env(safe-area-inset-bottom)); z-index: var(--z-banner); display: flex; align-items: center; gap: var(--sp-3); flex-wrap: wrap; margin-top: var(--sp-4); padding: var(--sp-3) var(--sp-4); border-radius: var(--r-card); background: var(--surface-2); border: 1px solid var(--sky-35); box-shadow: var(--shadow); }
  @media (min-width: 768px) { .pp-batchbar { bottom: var(--sp-4); } }
  .pp-batchlabel { font-weight: 700; color: var(--sky-300); font-size: var(--fs-small); }
  .pp-batchprice { width: 12rem; min-height: 40px; padding: 0 var(--sp-3); border-radius: var(--r-control); background: var(--bg); border: 1px solid var(--line-strong); color: var(--text); font-size: var(--fs-small); }
  .pp-batchdur { min-height: 40px; padding: 0 var(--sp-3); border-radius: var(--r-control); background: var(--bg); border: 1px solid var(--line-strong); color: var(--text); font-size: var(--fs-small); font-family: inherit; }
  .pp-batcherr { color: var(--red); font-size: var(--fs-small); font-weight: 600; }

  /* Edit modal */
  .pp-overlay { position: fixed; inset: 0; z-index: var(--z-modal); background: rgba(0,0,0,.65); backdrop-filter: blur(6px); display: flex; align-items: center; justify-content: center; padding: var(--sp-4); }
  .pp-modal { background: var(--surface-2); border: 1px solid var(--line-strong); border-radius: var(--r-card); padding: var(--sp-6); max-width: 480px; width: 100%; max-height: 85vh; overflow-y: auto; box-shadow: var(--shadow); }
  .pp-modalhead { display: flex; align-items: center; justify-content: space-between; margin-bottom: var(--sp-4); }
  .pp-modalhead h2 { font-size: var(--fs-h2); line-height: var(--lh-h2); font-weight: 800; margin: 0; }
  .pp-modal form { display: flex; flex-direction: column; gap: var(--sp-3); }
  .pp-field { display: flex; flex-direction: column; gap: 6px; font-size: var(--fs-small); font-weight: 700; color: var(--text-2); }
  .pp-field input, .pp-field textarea, .pp-field select { min-height: 44px; padding: var(--sp-2) var(--sp-3); border-radius: var(--r-control); background: var(--bg); border: 1px solid var(--line-strong); color: var(--text); font-size: var(--fs-body); font-family: inherit; }
  .pp-field textarea { min-height: 80px; resize: vertical; }
  .pp-editerr { margin: 0; color: var(--red); font-size: var(--fs-small); font-weight: 600; }
  .pp-modalfoot { display: flex; gap: var(--sp-3); margin-top: var(--sp-2); }
  .pp-modalfoot .btn { flex: 1; }

  .mono { font-family: 'JetBrains Mono', ui-monospace, monospace; }
</style>
