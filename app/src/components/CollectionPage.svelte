<script lang="ts">
  // Collection page island (spec B4 "Collection"). collection.astro is a thin
  // shell; this owns header, stats, tabs (Items · Listings · Activity), the
  // owner-only offers toggle (in place, no reload) and the seller banner.
  import { onMount } from 'svelte';
  import VerifiedBadge from './VerifiedBadge.svelte';
  import Hint from './Hint.svelte';
  import EmptyState from './EmptyState.svelte';
  import ErrorState from './ErrorState.svelte';
  import Skeleton from './Skeleton.svelte';
  import NFTCard from './NFTCard.svelte';
  import Icon from './Icon.svelte';
  import { jsonOrNull, ApiError, json } from '../lib/api';
  import { fmtPrice, fmtAmount, shortAddr, timeAgo } from '../lib/format';
  import { resolveImageUri } from '../lib/image-uri';
  import { currentChain, explorerAddress, explorerTx, tradingLive } from '../lib/chains';
  import { toastSuccess, toastError } from '../lib/toast.svelte';

  type VerifiedReason = { standard_ok: boolean; metadata_ok: boolean; creator_known: boolean };
  type Collection = {
    address: string; name: string; symbol: string; standard: string;
    verified: boolean; creator_addr: string;
    floor_price_wei?: string; volume_24h_wei?: string; listed_count?: number;
    verified_reason?: VerifiedReason;
  };
  type TokenRow = { token_id: string; owner: string; name: string; image: string; listed: boolean; price_wei?: string; last_sale_wei?: string };
  type TokensPage = { collection: Collection; tokens: TokenRow[]; page: number; limit: number; total: number };
  type ListingRow = Record<string, unknown> & { collection: string; token_id: string; seller: string };
  type ActivityRow = { type: string; amountWei: string; timestamp: string; txHash: string };
  type OwnedNFT = { collection?: string; contract?: string };

  const ADDR_RE = /^0x[0-9a-fA-F]{40}$/;
  const chain = currentChain();
  const sym = chain.currency;
  const canTrade = tradingLive();
  const PAGE = 48;

  // `addr` is a prop so tests can mount without touching location.
  let { addr = '' }: { addr?: string } = $props();

  let loading = $state(true);
  let notFound = $state(false);
  let invalid = $state(false);
  let error = $state('');
  let col = $state<Collection | null>(null);
  let tab = $state<'items' | 'listings' | 'activity'>('items');

  // Items tab
  let tokens = $state<TokenRow[]>([]);
  let tokensTotal = $state(0);
  let tokensPage = $state(1);
  let tokensLoading = $state(false);

  // Listings / Activity tabs (fetched on first open)
  let listings = $state<ListingRow[] | null>(null);
  let activity = $state<ActivityRow[] | null>(null);

  // Wallet-dependent bits
  let me = $state<string | null>(null);
  let ownedHere = $state(0);
  let offersEligible = $state<boolean | null>(null);
  let toggling = $state(false);

  let isCollOwner = $derived(!!me && !!col?.creator_addr && me === col.creator_addr.toLowerCase());
  let initials = $derived((col?.name || addr).replace(/^0x/, '').slice(0, 2).toUpperCase());

  /** Non-zero stat segments only; all-zero → "No sales yet" (spec). */
  let statSegments = $derived.by<string[]>(() => {
    if (!col) return [];
    const out: string[] = [];
    let floor = 0n;
    try { floor = BigInt(col.floor_price_wei || '0'); } catch { floor = 0n; }
    if (floor > 0n) out.push(`Floor ${fmtAmount(floor, sym)}`);
    if ((col.listed_count ?? 0) > 0) out.push(`${col.listed_count} listed`);
    if (tokensTotal > 0) out.push(`${tokensTotal} item${tokensTotal === 1 ? '' : 's'}`);
    return out;
  });

  let reasonText = $derived.by(() => {
    const r = col?.verified_reason;
    if (!r) return 'Verification details are not available for this collection yet.';
    const part = (ok: boolean, label: string) => `${label}: ${ok ? 'confirmed' : 'not confirmed'}`;
    return [part(r.standard_ok, 'Standard NFT contract'), part(r.metadata_ok, 'Metadata'), part(r.creator_known, 'Creator known')].join(' · ');
  });

  function readWallet() {
    try {
      const a = window.MW?.address?.() ?? localStorage.getItem('mw_addr');
      me = a && ADDR_RE.test(a) ? a.toLowerCase() : null;
    } catch { me = null; }
    if (me) void loadOwned(); else ownedHere = 0;
  }

  async function loadOwned() {
    if (!me) return;
    const rows = await jsonOrNull<OwnedNFT[]>(`/api/v1/wallet/${me}/nfts`);
    ownedHere = (rows ?? []).filter((n) => String(n.collection ?? n.contract ?? '').toLowerCase() === addr.toLowerCase()).length;
  }

  async function loadTokens(page: number, append = false) {
    tokensLoading = true;
    try {
      const d = await json<TokensPage>(`/api/v1/collections/${addr}/tokens?page=${page}&limit=${PAGE}`);
      tokensTotal = d.total;
      tokensPage = page;
      tokens = append ? [...tokens, ...d.tokens] : d.tokens;
    } catch { /* Items grid keeps what it has; header stats stay */ }
    tokensLoading = false;
  }

  async function load() {
    loading = true;
    notFound = false;
    error = '';
    try {
      col = await json<Collection>(`/api/v1/collections/${addr}`);
    } catch (e) {
      if (e instanceof ApiError && e.status === 404) notFound = true;
      else error = 'Could not load this collection. Check your connection and try again.';
      loading = false;
      return;
    }
    await loadTokens(1);
    loading = false;
    if (canTrade && window.MW) {
      window.MW.isOfferEligible(addr).then((v: boolean) => (offersEligible = v)).catch(() => (offersEligible = null));
    }
  }

  async function openTab(t: typeof tab) {
    tab = t;
    if (t === 'listings' && listings === null) {
      listings = (await jsonOrNull<ListingRow[]>(`/api/v1/listings?collection=${addr}&limit=48`)) ?? [];
    }
    if (t === 'activity' && activity === null) {
      activity = (await jsonOrNull<ActivityRow[]>(`/api/v1/activity?collection=${addr}&limit=50`)) ?? [];
    }
  }

  /** In-place toggle with toast — never a reload (spec). */
  async function toggleOffers() {
    if (toggling || offersEligible === null || !window.MW) return;
    const next = !offersEligible;
    toggling = true;
    try {
      await window.MW.setOfferEligible({ nft: addr, eligible: next, name: col?.name });
      offersEligible = next;
      toastSuccess(next ? 'Offers are now enabled for this collection' : 'Offers are now disabled for this collection');
    } catch {
      toastError('The offers setting was not changed');
    }
    toggling = false;
  }

  function actWords(a: ActivityRow): string {
    const amt = a.amountWei && a.amountWei !== '0' ? fmtAmount(a.amountWei, sym) : '';
    const t = a.type.toLowerCase();
    if (t.includes('sale') || t.includes('sold') || t.includes('buy')) return amt ? `Sold for ${amt}` : 'Sold';
    if (t.includes('cancel')) return 'Listing cancelled';
    if (t.includes('list')) return amt ? `Listed for ${amt}` : 'Listed';
    if (t.includes('bid')) return amt ? `Bid of ${amt}` : 'Bid placed';
    if (t.includes('offer')) return amt ? `Offer of ${amt}` : 'Offer made';
    if (t.includes('settle')) return amt ? `Auction settled for ${amt}` : 'Auction settled';
    if (t.includes('auction')) return 'Auction started';
    return amt ? `${a.type} · ${amt}` : a.type;
  }

  onMount(() => {
    if (!addr) {
      addr = decodeURIComponent(location.pathname.replace(/^\/collection\/?/, '')).toLowerCase();
    }
    if (!ADDR_RE.test(addr)) { invalid = true; loading = false; return; }
    addr = addr.toLowerCase();
    void load();
    readWallet();
    const onWallet = () => readWallet();
    window.addEventListener('mw-wallet-changed', onWallet);
    window.addEventListener('mw-ready', onWallet);
    return () => {
      window.removeEventListener('mw-wallet-changed', onWallet);
      window.removeEventListener('mw-ready', onWallet);
    };
  });
</script>

{#if loading}
  <div class="cp-skel">
    <div class="cp-skel-head"><Skeleton w="72px" h="72px" r="16px" /><div class="cp-skel-lines"><Skeleton w="40%" h="24px" /><Skeleton w="60%" h="14px" /></div></div>
    <div class="cp-grid" aria-hidden="true">{#each Array(8) as _, i (i)}<Skeleton card />{/each}</div>
  </div>
{:else if invalid || notFound}
  <EmptyState
    title="We don't track this collection yet"
    body="Paste the address in Search to request indexing."
    icon="collection"
    cta={{ label: 'Open Search', href: '/search' }} />
{:else if error}
  <ErrorState message={error} retry={() => void load()} />
{:else if col}
  <header class="cp-head">
    <div class="cp-avatar" aria-hidden="true">{initials}</div>
    <div class="cp-headmain">
      <div class="cp-titlerow">
        <h1>{col.name || shortAddr(addr)}</h1>
        {#if col.symbol}<span class="cp-sym">{col.symbol}</span>{/if}
        <VerifiedBadge verified={col.verified} creatorAddr={col.creator_addr} tracked={true} collectionName={col.name} hint={false} />
        <Hint text={reasonText} label="How this badge was decided" />
      </div>
      <div class="cp-meta">
        {#if col.creator_addr}
          <a href={explorerAddress(col.creator_addr)} target="_blank" rel="noopener">Created by {shortAddr(col.creator_addr)} <Icon name="external" size={14} /></a>
          <span aria-hidden="true">·</span>
        {/if}
        <a href={explorerAddress(addr)} target="_blank" rel="noopener">Explorer <Icon name="external" size={14} /></a>
      </div>
      <p class="cp-stats" data-testid="stats">
        {#if statSegments.length}{statSegments.join(' · ')}{:else}No sales yet{/if}
      </p>
    </div>
  </header>

  {#if canTrade && isCollOwner && offersEligible !== null}
    <div class="cp-ownerbar">
      <span>You own this collection's contract. Offers are <b>{offersEligible ? 'enabled' : 'disabled'}</b>.</span>
      <button class="btn btn-secondary" disabled={toggling} onclick={() => void toggleOffers()} data-testid="offers-toggle">
        {toggling ? 'Waiting for wallet…' : offersEligible ? 'Disable offers' : 'Enable offers'}
      </button>
    </div>
  {/if}

  {#if me && ownedHere > 0}
    <div class="cp-sellerbar" data-testid="seller-banner">
      <span>You own {ownedHere} item{ownedHere === 1 ? '' : 's'} here —</span>
      <a class="btn btn-primary" href="/profile#nfts">List them</a>
    </div>
  {/if}

  <div class="cp-tabs" role="tablist" aria-label="Collection sections">
    <button role="tab" id="cp-tab-items" aria-selected={tab === 'items'} aria-controls="cp-panel-items" class:is-on={tab === 'items'} onclick={() => void openTab('items')}>Items</button>
    <button role="tab" id="cp-tab-listings" aria-selected={tab === 'listings'} aria-controls="cp-panel-listings" class:is-on={tab === 'listings'} onclick={() => void openTab('listings')}>Listings</button>
    <button role="tab" id="cp-tab-activity" aria-selected={tab === 'activity'} aria-controls="cp-panel-activity" class:is-on={tab === 'activity'} onclick={() => void openTab('activity')}>Activity</button>
  </div>

  {#if tab === 'items'}
    <section id="cp-panel-items" role="tabpanel" aria-labelledby="cp-tab-items">
      {#if tokens.length === 0 && !tokensLoading}
        <EmptyState title="No items indexed yet" body="Tokens appear here as the indexer sees them minted or transferred." icon="image" />
      {:else}
        <div class="cp-grid">
          {#each tokens as t (t.token_id)}
            <a class="cp-card" href={`/token/${addr}/${t.token_id}`}>
              <span class="cp-card-img">
                {#if t.image}<img src={resolveImageUri(t.image, t.token_id, 256)} alt={t.name || `#${t.token_id}`} loading="lazy" />{:else}<span class="cp-noimg"><Icon name="image" size={32} /></span>{/if}
                {#if t.listed}<span class="cp-listed">Listed</span>{/if}
              </span>
              <span class="cp-card-body">
                <span class="cp-card-name">{t.name || `#${t.token_id}`}</span>
                <span class="cp-card-price">
                  {#if t.listed && t.price_wei}{fmtAmount(t.price_wei, sym)}
                  {:else if t.last_sale_wei}Last sale {fmtAmount(t.last_sale_wei, sym)}
                  {:else}Not listed{/if}
                </span>
              </span>
            </a>
          {/each}
        </div>
        {#if tokensLoading}
          <div class="cp-grid" style="margin-top:var(--sp-4)" aria-hidden="true">{#each Array(4) as _, i (i)}<Skeleton card />{/each}</div>
        {/if}
        <footer class="cp-foot">
          <span class="cp-dim">Showing {tokens.length} of {tokensTotal}</span>
          {#if tokens.length < tokensTotal}
            <button class="btn btn-secondary btn-lg" disabled={tokensLoading} onclick={() => void loadTokens(tokensPage + 1, true)}>Load more</button>
          {/if}
        </footer>
      {/if}
    </section>
  {:else if tab === 'listings'}
    <section id="cp-panel-listings" role="tabpanel" aria-labelledby="cp-tab-listings">
      {#if listings === null}
        <div class="cp-grid" aria-hidden="true">{#each Array(4) as _, i (i)}<Skeleton card />{/each}</div>
      {:else if listings.length === 0}
        <EmptyState title="Nothing is listed yet" body="Listing is free — you only pay 2% when it sells." icon="tag" />
      {:else}
        <div class="cp-grid">
          {#each listings as item (item.collection + item.token_id + item.seller)}
            <NFTCard item={item as never} />
          {/each}
        </div>
      {/if}
    </section>
  {:else}
    <section id="cp-panel-activity" role="tabpanel" aria-labelledby="cp-tab-activity">
      {#if activity === null}
        <div class="cp-list" aria-hidden="true"><Skeleton h="56px" r="12px" /><Skeleton h="56px" r="12px" /><Skeleton h="56px" r="12px" /></div>
      {:else if activity.length === 0}
        <EmptyState title="No activity yet" body="Sales, listings, bids and offers in this collection will show up here the moment they happen." />
      {:else}
        <ul class="cp-list">
          {#each activity as a (a.txHash + a.type + a.timestamp)}
            <li>
              <span class="cp-act">{actWords(a)}</span>
              <span class="cp-dim">{timeAgo(a.timestamp)}</span>
              <a class="cp-dim cp-txlink" href={explorerTx(a.txHash)} target="_blank" rel="noopener" aria-label="View transaction in the explorer"><Icon name="external" size={16} /></a>
            </li>
          {/each}
        </ul>
      {/if}
    </section>
  {/if}
{/if}

<style>
  .cp-skel { display: flex; flex-direction: column; gap: var(--sp-6); }
  .cp-skel-head { display: flex; gap: var(--sp-4); align-items: center; }
  .cp-skel-lines { flex: 1; display: flex; flex-direction: column; gap: var(--sp-2); }
  .cp-head { display: flex; gap: var(--sp-4); align-items: flex-start; margin-bottom: var(--sp-6); }
  .cp-avatar { width: 72px; height: 72px; border-radius: var(--r-card); background: var(--sky-12); border: 1px solid var(--sky-35); color: var(--sky-300); display: flex; align-items: center; justify-content: center; font-weight: 800; font-size: 22px; flex: 0 0 auto; }
  .cp-headmain { min-width: 0; display: flex; flex-direction: column; gap: var(--sp-1); }
  .cp-titlerow { display: flex; align-items: center; gap: var(--sp-2); flex-wrap: wrap; }
  .cp-titlerow h1 { font-size: var(--fs-h1); line-height: var(--lh-h1); font-weight: 800; margin: 0; letter-spacing: -0.02em; overflow-wrap: anywhere; }
  .cp-sym { color: var(--text-3); font-size: var(--fs-small); font-weight: 700; }
  .cp-meta { display: flex; align-items: center; gap: var(--sp-2); flex-wrap: wrap; font-size: var(--fs-small); }
  .cp-meta a { color: var(--text-2); display: inline-flex; align-items: center; gap: 4px; min-height: var(--hit); }
  .cp-meta a:hover { color: var(--text); }
  .cp-stats { margin: 0; color: var(--text-2); font-size: var(--fs-body); }
  .cp-ownerbar, .cp-sellerbar { display: flex; align-items: center; gap: var(--sp-3); flex-wrap: wrap; padding: var(--sp-3) var(--sp-4); border-radius: var(--r-card); background: var(--gold-12); border: 1px solid var(--gold-35); color: var(--text); font-size: var(--fs-body); margin-bottom: var(--sp-4); }
  .cp-sellerbar { background: var(--sky-12); border-color: var(--sky-35); }
  .cp-tabs { display: flex; gap: var(--sp-2); margin-bottom: var(--sp-4); border-bottom: 1px solid var(--line); }
  .cp-tabs button { min-height: var(--hit); padding: 0 var(--sp-4); background: transparent; border: 0; border-bottom: 2px solid transparent; color: var(--text-2); font-family: inherit; font-size: var(--fs-body); font-weight: 700; cursor: pointer; }
  .cp-tabs button.is-on { color: var(--text); border-bottom-color: var(--sky); }
  .cp-grid { display: grid; grid-template-columns: repeat(1, 1fr); gap: var(--sp-4); }
  @media (min-width: 640px) { .cp-grid { grid-template-columns: repeat(2, 1fr); } }
  @media (min-width: 960px) { .cp-grid { grid-template-columns: repeat(3, 1fr); } }
  @media (min-width: 1280px) { .cp-grid { grid-template-columns: repeat(4, 1fr); } }
  .cp-card { display: block; border: 1px solid var(--line); border-radius: var(--r-card); overflow: hidden; background: var(--surface); text-decoration: none; color: var(--text); transition: border-color var(--dur-fast) var(--ease); }
  .cp-card:hover { border-color: var(--sky-35); }
  .cp-card-img { position: relative; display: block; aspect-ratio: 1; background: var(--ink-950); }
  .cp-card-img img { width: 100%; height: 100%; object-fit: cover; display: block; }
  .cp-noimg { position: absolute; inset: 0; display: flex; align-items: center; justify-content: center; color: var(--text-3); }
  .cp-listed { position: absolute; top: var(--sp-2); left: var(--sp-2); padding: 2px 10px; border-radius: var(--r-pill); background: var(--gold-12); border: 1px solid var(--gold-35); color: var(--gold-300); font-size: var(--fs-caption); font-weight: 800; letter-spacing: var(--ls-caption); text-transform: uppercase; backdrop-filter: blur(4px); }
  .cp-card-body { display: flex; flex-direction: column; gap: 2px; padding: var(--sp-3); }
  .cp-card-name { font-weight: 700; font-size: var(--fs-body); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .cp-card-price { color: var(--text-2); font-size: var(--fs-small); }
  .cp-foot { display: flex; flex-direction: column; align-items: center; gap: var(--sp-3); margin-top: var(--sp-6); }
  .cp-list { list-style: none; margin: 0; padding: 0; display: flex; flex-direction: column; gap: var(--sp-2); }
  .cp-list li { display: flex; align-items: center; gap: var(--sp-3); padding: var(--sp-3) var(--sp-4); min-height: 56px; border-radius: var(--r-card); background: var(--surface); border: 1px solid var(--line); font-size: var(--fs-body); }
  .cp-act { font-weight: 600; flex: 1; }
  .cp-dim { color: var(--text-3); font-size: var(--fs-small); }
  .cp-txlink { display: inline-flex; align-items: center; justify-content: center; width: var(--hit); height: var(--hit); border-radius: var(--r-control); }
  .cp-txlink:hover { color: var(--text); background: rgba(255,255,255,.06); }
</style>
