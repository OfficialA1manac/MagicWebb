<script module lang="ts">
  // Pure helpers for the home page (spec B4 "Home"), exported for the tests.
  import { fmtPrice } from '../lib/format';

  export interface HomeStatsIn { listings: number; liveAuctions: number; offers: number; soldTodayWei: string }

  /**
   * The "Right now" line — a sentence, not stat cards. Returns null while
   * everything is zero (the page shows "Nothing is listed yet. Listing is
   * free." instead — stats never render as zeros).
   */
  export function rightNowLine(s: HomeStatsIn, currency: string): string | null {
    let sold = 0n;
    try { sold = BigInt(s.soldTodayWei || '0'); } catch { sold = 0n; }
    if (s.listings === 0 && s.liveAuctions === 0 && s.offers === 0 && sold === 0n) return null;
    const plural = (n: number, w: string) => `${n} ${w}${n === 1 ? '' : 's'}`;
    const parts = [
      plural(s.listings, 'listing'),
      `${s.liveAuctions} live ${s.liveAuctions === 1 ? 'auction' : 'auctions'}`,
      plural(s.offers, 'offer'),
      `${fmtPrice(sold)} ${currency} sold today`,
    ];
    return parts.join(' · ');
  }

  export interface HeroCopy { headline: string; sub: string; primary: { label: string; kind: 'connect' | 'list' | 'browse' }; secondary: { label: string; href: string } }

  /** Hero copy per network mode + wallet state (spec B4 "Home" 1st). */
  export function heroCopy(i: { trading: boolean; networkName: string; connected: boolean }): HeroCopy {
    if (!i.trading) return {
      headline: `Browse NFTs on ${i.networkName}`,
      sub: 'Trading opens after the security audit. You can browse everything today.',
      primary: { label: 'Browse listings', kind: 'browse' },
      secondary: { label: 'Read the docs', href: '/docs' },
    };
    return {
      headline: 'Buy and sell NFTs on Flare',
      sub: 'No account. Your wallet is your login. Listing is free — you pay 2% only when something sells.',
      primary: i.connected ? { label: 'List an NFT', kind: 'list' } : { label: 'Connect wallet', kind: 'connect' },
      secondary: { label: 'Browse listings', href: '/listings' },
    };
  }

  export interface StripStep { n: 1 | 2 | 3; label: string; done: boolean; href?: string }

  /** First-run strip steps (spec B4 "Home" 2nd). Step 2 links the faucet on testnets. */
  export function firstRunSteps(i: { connected: boolean; testnet: boolean; faucetUrl?: string | null; traded?: boolean }): StripStep[] {
    return [
      { n: 1, label: 'Connect your wallet', done: i.connected },
      i.testnet
        ? { n: 2, label: 'Get free test FLR', done: false, href: i.faucetUrl ?? undefined }
        : { n: 2, label: 'Fund your wallet with FLR', done: false },
      { n: 3, label: 'Buy or list your first NFT', done: !!i.traded },
    ];
  }

  /** The strip renders until dismissed; the dismiss control appears once step 3 is done. */
  export function stripState(i: { dismissed: boolean; traded: boolean }): { visible: boolean; dismissible: boolean } {
    return { visible: !i.dismissed, dismissible: i.traded };
  }

  export const STRIP_DISMISSED_KEY = 'mw-firstrun-dismissed';
  export const FIRST_TRADE_KEY = 'mw-first-trade-done';
</script>

<script lang="ts">
  import { onMount } from 'svelte';
  import NFTCard from './NFTCard.svelte';
  import EmptyState from './EmptyState.svelte';
  import Skeleton from './Skeleton.svelte';
  import Icon from './Icon.svelte';
  import VerifiedBadge from './VerifiedBadge.svelte';
  import { currentChain, tradingLive, isTestnet, faucetUrl } from '../lib/chains';
  import { jsonOrNull, json } from '../lib/api';
  import { shortAddr } from '../lib/format';

  type ListingItem = {
    collection: string; token_id: string; seller: string; price_wei: string;
    amount: number; standard: string; expires_at: string; listed_at: string;
    tx_hash: string; name: string; image_uri: string; total_supply: number;
    collection_verified: boolean; collection_creator?: string;
    collection_name?: string; collection_tracked?: boolean;
  };
  type CollectionRow = { address: string; name: string; symbol?: string; verified?: boolean; creator_addr?: string };
  type ActivityRow = { type: string; amountWei: string; ts: number };
  type AuctionRow = { auction_id: number; status: string; ends_at: string };

  const chain = currentChain();
  const sym = chain.currency;
  const trading = tradingLive();
  const testnet = isTestnet(chain.id);
  const faucet = faucetUrl();

  let connected = $state(false);
  let loading = $state(true);
  let error = $state(false);
  let listings = $state<ListingItem[]>([]);
  let collections = $state<Array<CollectionRow & { listed: number }>>([]);
  let stats = $state<HomeStatsIn>({ listings: 0, liveAuctions: 0, offers: 0, soldTodayWei: '0' });
  let dismissed = $state(false);
  let traded = $state(false);

  let hero = $derived(heroCopy({ trading, networkName: chain.name, connected }));
  let line = $derived(rightNowLine(stats, sym));
  let steps = $derived(firstRunSteps({ connected, testnet, faucetUrl: faucet, traded }));
  let strip = $derived(stripState({ dismissed, traded }));

  function readWallet() {
    try { connected = !!window.MW?.address?.(); } catch { connected = false; }
  }

  async function load() {
    loading = true;
    error = false;
    try {
      const rows = await json<ListingItem[]>('/api/v1/listings?limit=8&sort=recent');
      listings = Array.isArray(rows) ? rows : [];
    } catch {
      error = true;
      loading = false;
      return;
    }
    loading = false;

    // Everything after the grid is best-effort — a stat that fails to load
    // just stays 0 (and the line hides itself when everything is zero).
    const [st, auctions, activity, cols, allListings] = await Promise.all([
      jsonOrNull<Record<string, unknown>>('/api/v1/stats'),
      jsonOrNull<AuctionRow[]>('/api/v1/auctions?status=active&limit=100'),
      jsonOrNull<ActivityRow[]>('/api/v1/activity?limit=100'),
      jsonOrNull<CollectionRow[]>('/api/v1/collections?limit=12'),
      jsonOrNull<Array<{ collection: string }>>('/api/v1/listings?limit=100'),
    ]);
    const num = (v: unknown) => (typeof v === 'number' && Number.isFinite(v) ? v : 0);
    const dayAgo = Date.now() - 24 * 3600 * 1000;
    let sold = 0n;
    for (const r of activity ?? []) {
      if (r.type !== 'Sold' || !(r.ts >= dayAgo)) continue;
      try { sold += BigInt(r.amountWei || '0'); } catch { /* skip bad row */ }
    }
    const nowMs = Date.now();
    stats = {
      listings: num(st?.totalActiveListings),
      liveAuctions: (auctions ?? []).filter((a) => a.status === 'active' && new Date(a.ends_at).getTime() > nowMs).length,
      offers: num(st?.totalOffers),
      soldTodayWei: sold.toString(),
    };
    const counts = new Map<string, number>();
    for (const l of allListings ?? listings) {
      const k = l.collection?.toLowerCase();
      if (k) counts.set(k, (counts.get(k) ?? 0) + 1);
    }
    collections = (cols ?? []).map((c) => ({ ...c, listed: counts.get(c.address.toLowerCase()) ?? 0 }));
  }

  function primaryAction(e: MouseEvent) {
    if (hero.primary.kind === 'browse') { location.href = '/listings'; return; }
    if (hero.primary.kind === 'list') { location.href = '/profile#nfts'; return; }
    e.preventDefault();
    window.MW?.connect().then(() => { readWallet(); }).catch(() => {});
  }

  function goListNFT() {
    if (!connected && window.MW) { window.MW.connect().then(() => { location.href = '/profile#nfts'; }).catch(() => {}); return; }
    location.href = '/profile#nfts';
  }

  function dismissStrip() {
    dismissed = true;
    try { localStorage.setItem(STRIP_DISMISSED_KEY, '1'); } catch { /* private mode */ }
  }

  onMount(() => {
    try {
      dismissed = localStorage.getItem(STRIP_DISMISSED_KEY) === '1';
      traded = localStorage.getItem(FIRST_TRADE_KEY) === '1';
    } catch { /* private mode */ }
    readWallet();
    void load();
    const onWallet = () => readWallet();
    window.addEventListener('mw-wallet-changed', onWallet);
    window.addEventListener('mw-ready', onWallet);
    return () => {
      window.removeEventListener('mw-wallet-changed', onWallet);
      window.removeEventListener('mw-ready', onWallet);
    };
  });
</script>

<!-- ── Hero (parallax stays home-only via BaseLayout) ─────────────────── -->
<section class="hs-hero hero-parallax">
  <h1 class="hs-headline">{hero.headline}</h1>
  <p class="hs-sub">{hero.sub}</p>
  <div class="hs-ctas">
    {#if hero.primary.kind === 'browse'}
      <a class="btn btn-primary btn-lg" href="/listings">{hero.primary.label}</a>
    {:else}
      <button class="btn btn-primary btn-lg" onclick={primaryAction}>{hero.primary.label}</button>
    {/if}
    <a class="btn btn-secondary btn-lg" href={hero.secondary.href}>{hero.secondary.label}</a>
  </div>
</section>

<!-- ── First-run strip (trading networks only) ────────────────────────── -->
{#if trading && strip.visible}
  <section class="hs-strip" aria-label="Getting started" data-testid="first-run-strip">
    {#each steps as s (s.n)}
      <span class="hs-step" class:is-done={s.done}>
        <span class="hs-step-n" aria-hidden="true">{#if s.done}<Icon name="check" size={14} />{:else}{s.n}{/if}</span>
        {#if s.href}
          <a href={s.href} target="_blank" rel="noopener">{s.label} <Icon name="external" size={14} /></a>
        {:else}
          {s.label}
        {/if}
      </span>
    {/each}
    {#if strip.dismissible}
      <button type="button" class="hs-strip-x" aria-label="Dismiss getting started" onclick={dismissStrip}><Icon name="x" size={16} /></button>
    {/if}
  </section>
{/if}

<!-- ── Right now ──────────────────────────────────────────────────────── -->
<section class="hs-block" aria-label="Right now">
  {#if loading}
    <Skeleton w="60%" h="18px" />
  {:else if line}
    <p class="hs-line" data-testid="right-now">{line}</p>
  {:else}
    <p class="hs-line hs-line-empty" data-testid="right-now-empty">Nothing is listed yet. Listing is free.</p>
  {/if}
</section>

<!-- ── Newest listings ────────────────────────────────────────────────── -->
<section class="hs-block" aria-label="Newest listings">
  <div class="hs-rowhead">
    <h2>Newest listings</h2>
    <a class="hs-more" href="/listings">View all</a>
  </div>
  {#if loading}
    <div class="hs-grid" aria-hidden="true">
      {#each Array(8) as _, i (i)}
        <div class="hs-sk" class:hs-sk-extra={i >= 4}><Skeleton card /></div>
      {/each}
    </div>
  {:else if error}
    <EmptyState title="Can't reach the marketplace" body="It may be busy — try again in a moment." icon="alert"
                cta={{ label: 'Retry', onclick: () => void load() }} />
  {:else if listings.length === 0}
    {#if trading}
      <EmptyState title="Nothing is listed yet" body="Be the first — listing is free." icon="tag"
                  cta={{ label: 'List an NFT', onclick: goListNFT }} />
    {:else}
      <EmptyState title="Nothing is listed yet" body={`Trading isn't live on ${chain.name} yet — you can browse, connect your wallet, and view your profile.`} icon="tag" />
    {/if}
  {:else}
    <div class="hs-grid">
      {#each listings as item (item.collection + item.token_id + item.seller)}
        <NFTCard {item} />
      {/each}
    </div>
  {/if}
</section>

<!-- ── Collections ────────────────────────────────────────────────────── -->
{#if collections.length}
  <section class="hs-block" aria-label="Collections">
    <div class="hs-rowhead"><h2>Collections</h2></div>
    <div class="hs-cols">
      {#each collections as c (c.address)}
        <a class="hs-col" href={`/collection/${c.address}`}>
          <span class="hs-col-name">{c.name || shortAddr(c.address)}</span>
          <VerifiedBadge verified={!!c.verified} creatorAddr={c.creator_addr ?? ''} collectionName={c.name} link={false} hint={false} />
          <span class="hs-col-dim">{c.listed} listed</span>
        </a>
      {/each}
    </div>
  </section>
{/if}

<!-- ── How it works (hidden on browse-only networks) ──────────────────── -->
{#if trading}
  <section class="hs-block" aria-label="How it works">
    <div class="hs-rowhead"><h2>How it works</h2></div>
    <ol class="hs-how">
      <li><strong>Buy:</strong> pay the price, get the NFT instantly.</li>
      <li><strong>Sell:</strong> list free, set a price or run an auction.</li>
      <li><strong>Offers:</strong> money is held safely and refunded if not accepted.</li>
    </ol>
    <a class="hs-guide" href="/docs/user-guide">Read the 2-minute guide <Icon name="chevron-right" size={16} /></a>
  </section>
{/if}

<style>
  .hs-hero { text-align: center; padding: var(--sp-16) var(--sp-4) var(--sp-12); }
  .hs-headline { font-size: var(--fs-display); line-height: var(--lh-display); font-weight: 900; letter-spacing: -0.03em; margin: 0 0 var(--sp-4); }
  .hs-sub { max-width: 34rem; margin: 0 auto var(--sp-6); color: var(--text-2); font-size: var(--fs-h3); line-height: var(--lh-h3); }
  .hs-ctas { display: flex; gap: var(--sp-3); justify-content: center; flex-wrap: wrap; }
  @media (max-width: 480px) {
    .hs-headline { font-size: 2rem; line-height: 2.25rem; }
    .hs-ctas { flex-direction: column; align-items: stretch; padding: 0 var(--sp-4); }
  }
  .hs-strip { max-width: 72rem; margin: 0 auto var(--sp-6); padding: var(--sp-3) var(--sp-4); display: flex; align-items: center; gap: var(--sp-4); flex-wrap: wrap; border: 1px solid var(--line); border-radius: var(--r-card); background: var(--surface); position: relative; }
  .hs-step { display: inline-flex; align-items: center; gap: var(--sp-2); font-size: var(--fs-small); color: var(--text-2); font-weight: 600; }
  .hs-step.is-done { color: var(--green); }
  .hs-step a { color: var(--sky-300); display: inline-flex; align-items: center; gap: 4px; min-height: var(--hit); }
  .hs-step-n { display: inline-flex; align-items: center; justify-content: center; width: 22px; height: 22px; border-radius: var(--r-pill); border: 1px solid var(--line-strong); font-size: var(--fs-caption); font-weight: 700; flex: 0 0 auto; }
  .hs-step.is-done .hs-step-n { border-color: var(--green); color: var(--green); }
  .hs-strip-x { margin-left: auto; display: inline-flex; align-items: center; justify-content: center; width: var(--hit); height: var(--hit); border: 0; border-radius: var(--r-pill); background: transparent; color: var(--text-3); cursor: pointer; }
  .hs-strip-x:hover { color: var(--text); }
  @media (max-width: 640px) { .hs-strip { flex-direction: column; align-items: flex-start; gap: var(--sp-2); } .hs-strip-x { position: absolute; top: var(--sp-1); right: var(--sp-1); margin: 0; } }
  .hs-block { max-width: 72rem; margin: 0 auto; padding: var(--sp-4); }
  .hs-line { margin: 0; font-size: var(--fs-body); color: var(--text-2); font-weight: 600; }
  .hs-line-empty { color: var(--text-3); }
  .hs-rowhead { display: flex; align-items: baseline; justify-content: space-between; gap: var(--sp-3); margin-bottom: var(--sp-4); }
  .hs-rowhead h2 { font-size: var(--fs-h2); line-height: var(--lh-h2); font-weight: 800; margin: 0; letter-spacing: -0.02em; }
  .hs-more { font-size: var(--fs-small); color: var(--sky-300); font-weight: 700; min-height: var(--hit); display: inline-flex; align-items: center; }
  .hs-grid { display: grid; grid-template-columns: repeat(1, 1fr); gap: var(--sp-4); }
  @media (min-width: 640px) { .hs-grid { grid-template-columns: repeat(2, 1fr); } }
  @media (min-width: 960px) { .hs-grid { grid-template-columns: repeat(3, 1fr); } }
  @media (min-width: 1280px) { .hs-grid { grid-template-columns: repeat(4, 1fr); } }
  @media (max-width: 639px) { .hs-sk-extra { display: none; } }
  .hs-cols { display: flex; gap: var(--sp-3); flex-wrap: wrap; }
  .hs-col { display: inline-flex; align-items: center; gap: var(--sp-2); padding: var(--sp-2) var(--sp-4); min-height: var(--hit); border: 1px solid var(--line); border-radius: var(--r-pill); background: var(--surface); text-decoration: none; color: inherit; transition: border-color var(--dur) var(--ease); }
  .hs-col:hover { border-color: var(--sky-35); text-decoration: none; }
  .hs-col-name { font-weight: 700; font-size: var(--fs-body); color: var(--text); }
  .hs-col-dim { font-size: var(--fs-small); color: var(--text-3); }
  .hs-how { list-style: none; margin: 0 0 var(--sp-3); padding: 0; display: flex; flex-direction: column; gap: var(--sp-2); }
  .hs-how li { font-size: var(--fs-body); line-height: var(--lh-body); color: var(--text-2); }
  .hs-how strong { color: var(--text); }
  .hs-guide { display: inline-flex; align-items: center; gap: 4px; font-size: var(--fs-body); font-weight: 700; color: var(--sky-300); min-height: var(--hit); }
</style>
