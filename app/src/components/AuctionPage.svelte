<script module lang="ts">
  // Pure bid-panel matrix (spec B4 "Auctions" detail): phase × role → cells.
  // The template and the mobile sticky bar follow this table; the component
  // tests assert every cell without mounting the page (same pattern as
  // TokenPage.actionZone). Every cell is a visible control or a disabled
  // control with a Hint `reason`.
  export type AuctionPhase = 'live' | 'ended' | 'settled' | 'cancelled';
  export type AuctionRole = 'viewer' | 'buyer' | 'seller';
  export interface BidPanelCell { kind: string; label: string; disabled?: boolean; reason?: string; hint?: string }

  export function bidPanel(i: {
    phase: AuctionPhase; role: AuctionRole; browseOnly?: boolean;
    amLeader?: boolean; hasBids?: boolean; held?: boolean;
    canForceCancel?: boolean; isWinner?: boolean;
    /** e.g. "13 C2FLR" — the minimum bid shown to viewers/buyers. */
    minLabel?: string;
    /** e.g. "13 C2FLR" — the caller's cumulative escrow, for Withdraw/leader copy. */
    heldLabel?: string;
  }): BidPanelCell[] {
    if (i.browseOnly) return [{ kind: 'browse-only', label: 'Browse only' }];
    const withdraw: BidPanelCell = { kind: 'withdraw-bid', label: `Withdraw my ${i.heldLabel ?? 'bid'}` };
    switch (i.phase) {
      case 'live': {
        if (i.role === 'viewer') return [{
          kind: 'bid-connect', label: 'Connect wallet to bid',
          hint: i.minLabel ? `Minimum bid ${i.minLabel}` : undefined,
        }];
        if (i.role === 'seller') return [
          i.hasBids
            ? { kind: 'cancel-auction', label: 'Cancel auction', disabled: true, reason: 'Has bids — it will settle automatically at the end' }
            : { kind: 'cancel-auction', label: 'Cancel auction' },
        ];
        if (i.amLeader) return [{ kind: 'leading', label: `You're the highest bidder${i.heldLabel ? ` with ${i.heldLabel}` : ''}` }];
        const cells: BidPanelCell[] = [{ kind: 'bid', label: 'Place bid' }];
        if (i.held) cells.push(withdraw);
        return cells;
      }
      case 'ended': {
        const cells: BidPanelCell[] = [];
        if (i.role === 'seller' || i.isWinner) {
          cells.push({ kind: 'settle', label: 'Settle now' });
          if (i.canForceCancel) cells.push({ kind: 'force-cancel', label: 'Cancel and refund everyone' });
        } else {
          cells.push({ kind: 'ended-info', label: 'Settling automatically', disabled: true, reason: 'The NFT goes to the winner, the seller is paid minus 2%.' });
        }
        if (i.held && !i.amLeader) cells.push(withdraw);
        return cells;
      }
      case 'settled':
      case 'cancelled': {
        const cells: BidPanelCell[] = [{
          kind: 'closed', label: i.phase === 'settled' ? 'Sold' : 'Cancelled', disabled: true,
          reason: i.phase === 'settled' ? 'This auction settled — the NFT went to the winner.' : 'This auction was cancelled — every bid is refundable.',
        }];
        if (i.held && !(i.phase === 'settled' && i.amLeader)) cells.push(withdraw);
        return cells;
      }
    }
  }
</script>

<script lang="ts">
  // Auction detail (spec B4): hierarchy = media + name + Ends-in + status
  // chip, then the bid panel (role matrix above), then the bids list.
  // Live countdown, cumulative-bid aware bidding, WS-driven refresh, mobile
  // sticky bid bar. Not-found → EmptyState with [See live auctions], no Retry.
  import { onMount } from 'svelte';
  import VerifiedBadge from './VerifiedBadge.svelte';
  import CreatorBadge from './CreatorBadge.svelte';
  import EmptyState from './EmptyState.svelte';
  import Skeleton from './Skeleton.svelte';
  import Hint from './Hint.svelte';
  import Icon from './Icon.svelte';
  import { MW } from '../lib/mw';
  import { ws } from '../lib/ws/client';
  import { tokenChannel } from '../lib/ws/channels';
  import { currentChain, tradingLive, readOnlyCopy } from '../lib/chains';
  import { fmtPrice, shortAddr, timeAgo, fmtCountdownShort, countdownUrgent, toWei } from '../lib/format';
  import { resolveImageUri } from '../lib/image-uri';
  import { onAccountChange, publicClient } from '../lib/tx/client';
  import { auctionHouseAbi } from '../lib/abi';
  import { minimumTopUp, forceCancelUnlocked } from '../lib/tx/auction';
  import type { Address } from 'viem';

  type Auction = { auction_id: number; collection: string; token_id: string; seller: string; standard: string; reserve_price_wei: string; highest_bid_wei: string; highest_bidder: string; min_increment_bps: number; starts_at: string; ends_at: string; status: string; create_tx: string; name: string; image_uri: string; collection_verified: boolean; collection_creator?: string; collection_name?: string; collection_tracked?: boolean };
  type Bid = { bidder: string; amount_wei: string; tx_hash: string; placed_at: string };

  let id = $state('');
  let loading = $state(true);
  let notFound = $state(false);
  let a = $state<Auction | null>(null);
  let bids = $state<Bid[]>([]);
  let me = $state<string | null>(null);
  let now = $state(Date.now());
  let bidIn = $state('');
  let formErr = $state('');
  let syncing = $state('');
  const chain = currentChain();
  const sym = chain.currency;
  const canTrade = tradingLive();
  const roCopy = readOnlyCopy();

  let endsMs = $derived(a ? new Date(a.ends_at).getTime() : 0);
  let isLive = $derived(!!a && a.status === 'active' && endsMs > now);
  let ended = $derived(!!a && a.status === 'active' && endsMs <= now);
  let phase = $derived<AuctionPhase>(!a ? 'live' : a.status === 'settled' ? 'settled' : a.status === 'cancelled' ? 'cancelled' : ended ? 'ended' : 'live');
  let isSeller = $derived(!!me && !!a && a.seller.toLowerCase() === me.toLowerCase());
  let sellerIsCreator = $derived(!!a && !!a.collection_creator && a.seller.toLowerCase() === a.collection_creator.toLowerCase());
  let canForceCancel = $derived(ended && !!a && forceCancelUnlocked(endsMs / 1000, now));
  let highest = $derived(BigInt(a?.highest_bid_wei || '0'));
  // Cumulative escrow comes from the CHAIN, never from summing the bids API —
  // withdrawLoserFunds() zeroes the on-chain cumulative but leaves historical
  // bid rows, so a client-side sum overstates the escrow and produces a
  // minimum quote that reverts BidTooLow.
  let myCumulative = $state(0n);
  let amLeader = $derived(!!me && !!a && a.highest_bidder?.toLowerCase() === me.toLowerCase());
  let minTopUp = $derived(a ? minimumTopUp({ currentHighestWei: highest, reserveWei: BigInt(a.reserve_price_wei || '0'), myCumulativeWei: myCumulative, minIncrementBps: a.min_increment_bps }) : 0n);
  let name = $derived(a?.name || `#${a?.token_id ?? ''}`);
  let img = $derived(resolveImageUri(a?.image_uri, a?.token_id, 512));
  let antiSnipe = $derived(isLive && endsMs - now < 3 * 60 * 1000);
  let role = $derived<AuctionRole>(!me ? 'viewer' : isSeller ? 'seller' : 'buyer');
  let panel = $derived(bidPanel({
    phase, role, browseOnly: !canTrade, amLeader, hasBids: highest > 0n,
    held: myCumulative > 0n, canForceCancel, isWinner: amLeader,
    minLabel: a ? `${fmtPrice(minTopUp)} ${sym}` : undefined,
    heldLabel: `${fmtPrice(myCumulative)} ${sym}`,
  }));
  let statusChip = $derived(!a ? '' :
    a.status === 'settled' ? 'Sold' :
    a.status === 'cancelled' ? 'Cancelled' :
    ended ? 'Ended · settling' :
    antiSnipe ? 'Ending soon' : 'Live');

  async function j<T>(u: string): Promise<{ status: number; body: T | null }> {
    try { const r = await fetch(u); return { status: r.status, body: r.ok ? (await r.json()) as T : null }; }
    catch { return { status: 0, body: null }; }
  }

  async function loadMyCumulative() {
    myCumulative = 0n;
    if (!me || !a) return;
    try {
      const pub = await publicClient();
      const ahAddr = chain.contracts.auctionHouse;
      if (!ahAddr) return;
      myCumulative = (await pub.readContract({ address: ahAddr as Address, abi: auctionHouseAbi, functionName: 'cumulative', args: [BigInt(a.auction_id), me as Address] })) as bigint;
    } catch { /* stays 0n — the quote overpays instead of reverting */ }
  }
  $effect(() => { if (me && a) void loadMyCumulative(); });

  async function load(initial = false) {
    if (initial) loading = true;
    const [au, bi] = await Promise.all([j<Auction>(`/api/v1/auctions/${id}`), j<Bid[]>(`/api/v1/auctions/${id}/bids`)]);
    if (!au.body) {
      // 404 (or a bad id upstream) is the spec'd "doesn't exist" empty state
      // — a transient network failure only keeps the current view.
      if (au.status === 404 || au.status === 400 || !a) notFound = true;
      loading = false;
      return;
    }
    a = au.body;
    bids = (bi.body ?? []).slice().sort((x, y) => new Date(y.placed_at).getTime() - new Date(x.placed_at).getTime());
    notFound = false;
    loading = false; syncing = '';
  }

  onMount(() => {
    id = location.pathname.replace(/^\/auction\/?/, '').replace(/\/$/, '');
    if (!/^\d+$/.test(id)) { notFound = true; loading = false; return; }
    void load(true);
    const offAcct = onAccountChange((x) => (me = x.address));
    let ch = '';
    const sub = setInterval(() => { if (a && !ch) { ch = tokenChannel(a.collection, a.token_id); ws.subscribe(ch); } }, 500);
    const offWs = ws.on('*', (_d, meta) => { if (meta.type === 'auction-updated' || meta.type === 'activity' || meta.type === 'tx-indexed') void load(); });
    const tick = setInterval(() => (now = Date.now()), 1000);
    return () => { offAcct(); offWs(); clearInterval(tick); clearInterval(sub); if (ch) ws.unsubscribe(ch); };
  });

  function after(label: string) { syncing = label; formErr = ''; bidIn = ''; setTimeout(() => void load(), 1500); setTimeout(() => void load(), 6000); }
  async function act(run: () => Promise<unknown>, label: string) { try { await run(); after(label); } catch { /* modal showed it */ } }
  const doConnect = () => { MW.connect().catch(() => {}); };
  const doBid = () => {
    if (!a) return;
    let w: bigint; try { w = toWei(bidIn); } catch { formErr = 'Enter a number like 12.5'; return; }
    if (w < minTopUp) { formErr = `At least ${fmtPrice(minTopUp)} ${sym} is needed to take the lead.`; return; }
    act(() => MW.bid({ auctionId: String(a!.auction_id), amountWei: w.toString(), name, myCumulativeWei: myCumulative.toString() }), 'Bid placed · syncing');
  };
  const doSettle = () => a && act(() => MW.settle({ auctionId: String(a!.auction_id), name }), 'Settled · syncing');
  const doCancel = () => a && act(() => MW.cancelAuction({ auctionId: String(a!.auction_id), name }), 'Auction cancelled · syncing');
  const doForceCancel = () => a && act(() => MW.forceCancel({ auctionId: String(a!.auction_id), name }), 'Cancelled · everyone refunded · syncing');
  const doWithdraw = () => a && act(() => MW.withdrawLoserFunds({ auctionId: String(a!.auction_id), amountWei: myCumulative.toString() }), 'Withdrawn · syncing');

  /** The one primary action for the mobile sticky bar. */
  let primary = $derived(panel.find((c) => !c.disabled && c.kind !== 'leading' && c.kind !== 'browse-only') ?? null);
  function runCell(kind: string) {
    switch (kind) {
      case 'bid-connect': doConnect(); break;
      case 'bid': document.getElementById('bid-in')?.focus(); break;
      case 'cancel-auction': doCancel(); break;
      case 'settle': doSettle(); break;
      case 'force-cancel': doForceCancel(); break;
      case 'withdraw-bid': doWithdraw(); break;
    }
  }
</script>

{#if loading}
  <div class="ap-grid"><Skeleton square r="16px" /><div style="display:flex;flex-direction:column;gap:12px"><Skeleton w="40%" /><Skeleton w="70%" h="28px" /><Skeleton h="120px" r="16px" /><Skeleton h="48px" r="12px" /></div></div>
{:else if notFound || !a}
  <EmptyState title="This auction doesn't exist" body="The link may be old, or the auction may have been removed." icon="gavel"
              cta={{ label: 'See live auctions', href: '/auctions' }} />
{:else}
  <div class="ap-grid">
    <a class="ap-media" href={`/token/${a.collection}/${a.token_id}`}>
      {#if img}<img src={img} alt={name} />{:else}<span class="ap-noimg"><Icon name="image" size={48} /></span>{/if}
    </a>
    <div class="ap-side">
      <div class="ap-coll">
        <a href={`/collection/${a.collection}`}>{a.collection_name || shortAddr(a.collection)}</a>
        <VerifiedBadge verified={a.collection_verified} tracked={a.collection_tracked} creatorAddr={a.collection_creator ?? ''} collectionName={a.collection_name ?? ''} />
        <span class="ap-chip" class:is-live={statusChip === 'Live'} class:is-hot={statusChip === 'Ending soon'} data-testid="status-chip">{statusChip}</span>
      </div>
      <h1 class="ap-title">{name}</h1>
      <div class="ap-meta mono">Auction #{a.auction_id} · seller {isSeller ? 'you' : shortAddr(a.seller)}{#if sellerIsCreator} <CreatorBadge name={a.collection_name ?? ''} />{/if} · <a href={`/token/${a.collection}/${a.token_id}`}>token #{a.token_id}</a></div>
      {#if syncing}<div class="ap-sync" role="status"><span class="ap-spin" aria-hidden="true"></span>{syncing}</div>{/if}

      {#if !canTrade}
        <section class="ap-card" aria-label="Read-only network">
          <div class="ap-head"><span>{roCopy.heading}</span></div>
          <p class="ap-hint">{roCopy.body}</p>
          {#if roCopy.ctaHref}<a class="btn btn-primary" href={roCopy.ctaHref}>{roCopy.cta}</a>{/if}
        </section>
      {:else}
        <section class="ap-card" class:is-hot={antiSnipe} aria-label="Bid panel">
          <div class="ap-head">
            <h2 class="ap-ends-h">{isLive ? 'Ends in' : phase === 'ended' ? 'Ended' : statusChip}</h2>
            <span class="mono ap-count" class:is-urgent={isLive && countdownUrgent(endsMs / 1000, now)} aria-live="polite">
              {isLive ? fmtCountdownShort(endsMs / 1000, now) : timeAgo(a.ends_at)}
            </span>
          </div>
          {#if antiSnipe}
            <div class="ap-snipe" role="status">Bids in the last 3 minutes add 3 more minutes so nobody can snipe.</div>
          {/if}
          <div class="ap-price mono" class:is-gold={highest > 0n}>{fmtPrice(highest > 0n ? a.highest_bid_wei : a.reserve_price_wei)} <small>{sym}</small></div>
          <div class="ap-sub">{highest > 0n ? `Current bid · ${amLeader ? 'you are leading' : shortAddr(a.highest_bidder)}` : 'Starting price · no bids yet'}</div>

          {#each panel as cell (cell.kind)}
            {#if cell.kind === 'bid-connect'}
              <button class="btn btn-primary btn-lg" onclick={doConnect}>{cell.label}</button>
              {#if cell.hint}<p class="ap-hint">{cell.hint}</p>{/if}
            {:else if cell.kind === 'bid'}
              <div class="ap-form">
                <label class="ap-label" for="bid-in">Your bid ({sym})</label>
                <div class="ap-inrow">
                  <input id="bid-in" class="ap-input mono" inputmode="decimal" placeholder={fmtPrice(minTopUp)} bind:value={bidIn} aria-describedby="bid-help" />
                  <button class="btn btn-primary" onclick={doBid}>Place bid</button>
                </div>
                <p class="ap-hint" id="bid-help">At least {fmtPrice(minTopUp)} — 1 {sym} above the current bid</p>
                {#if formErr}<div class="ap-err" role="alert">{formErr}</div>{/if}
                <dl class="ap-numbers">
                  <div><dt>You have held</dt><dd class="mono">{fmtPrice(myCumulative)} {sym}</dd></div>
                  <div><dt>Needed to lead</dt><dd class="mono">{fmtPrice(minTopUp)} {sym}</dd></div>
                  <div><dt>Refundable if outbid</dt><dd class="ap-check"><Icon name="check" size={16} /> yes</dd></div>
                </dl>
              </div>
            {:else if cell.kind === 'leading'}
              <div class="ap-ok" data-testid="leading">{cell.label}.</div>
            {:else if cell.kind === 'withdraw-bid'}
              <button class="btn btn-secondary" onclick={doWithdraw}>{cell.label}</button>
            {:else if cell.kind === 'cancel-auction'}
              <span class="ap-cellrow">
                <button class="btn btn-danger" disabled={cell.disabled} aria-disabled={cell.disabled ? 'true' : undefined} onclick={doCancel}>{cell.label}</button>
                {#if cell.disabled && cell.reason}<Hint text={cell.reason} label="Why can't I cancel?" />{/if}
              </span>
            {:else if cell.kind === 'settle'}
              <button class="btn btn-gold btn-lg" onclick={doSettle}>Settle now</button>
              <p class="ap-hint">The marketplace settles this automatically within seconds; you can also settle it yourself.</p>
            {:else if cell.kind === 'force-cancel'}
              <span class="ap-cellrow">
                <button class="btn btn-danger" onclick={doForceCancel}>{cell.label}</button>
                <Hint text="Settlement has been stuck for 3+ days, so anyone involved can close the auction without a trade: every bid becomes refundable and the NFT stays where it is." label="About the 3-day rule" />
              </span>
            {:else if cell.kind === 'ended-info' || cell.kind === 'closed'}
              <p class="ap-hint" role="status">{cell.kind === 'ended-info' ? 'Settling automatically — the NFT goes to the winner, the seller is paid minus 2%.' : cell.reason}</p>
            {/if}
          {/each}
        </section>
      {/if}

      <section class="ap-section">
        <h2>Bids <span class="ap-n">{bids.length}</span></h2>
        {#if bids.length === 0}
          <EmptyState title="No bids yet" body="Be the first — the starting price is the minimum." icon="gavel" />
        {:else}
          <ul class="ap-list">
            {#each bids as b (b.tx_hash)}
              <li><span class="mono">{fmtPrice(b.amount_wei)} {sym}</span><span class="ap-dim">{me && b.bidder.toLowerCase() === me.toLowerCase() ? 'you' : shortAddr(b.bidder)} · {timeAgo(b.placed_at)}</span><a class="ap-dim ap-ext" href={MW.explorerTx(b.tx_hash)} target="_blank" rel="noopener" aria-label="View transaction on the explorer"><Icon name="external" size={16} /></a></li>
            {/each}
          </ul>
        {/if}
      </section>
    </div>
  </div>

  <!-- Mobile sticky bid bar: the panel's primary action, above the tab bar. -->
  {#if canTrade && primary}
    <div class="ap-sticky" data-testid="sticky-bar">
      <span class="ap-sticky-price mono">{fmtPrice(highest > 0n ? a.highest_bid_wei : a.reserve_price_wei)} {sym}</span>
      <button class="btn btn-primary btn-lg" onclick={() => runCell(primary!.kind)}>{primary.kind === 'bid' ? 'Place bid' : primary.label}</button>
    </div>
  {/if}
{/if}

<style>
  .ap-grid { display: grid; grid-template-columns: repeat(auto-fit, minmax(min(100%, 360px), 1fr)); gap: var(--sp-6); align-items: start; }
  .ap-media { aspect-ratio: 1; border-radius: var(--r-card); overflow: hidden; background: var(--bg); border: 1px solid var(--line); display: flex; align-items: center; justify-content: center; }
  .ap-media img { width: 100%; height: 100%; object-fit: cover; display: block; }
  .ap-noimg { color: var(--text-3); opacity: .4; display: inline-flex; }
  .ap-side { display: flex; flex-direction: column; gap: var(--sp-3); min-width: 0; }
  .ap-coll { display: flex; gap: var(--sp-2); align-items: center; flex-wrap: wrap; font-size: var(--fs-small); }
  .ap-coll > a { color: var(--sky-300); text-decoration: none; font-weight: 600; }
  .ap-chip { padding: 3px 10px; border-radius: var(--r-pill); border: 1px solid var(--line-strong); color: var(--text-2); font-size: var(--fs-caption); font-weight: 700; }
  .ap-chip.is-live { border-color: var(--green); color: var(--green); }
  .ap-chip.is-hot { border-color: var(--gold); color: var(--gold-300); }
  .ap-title { font-size: var(--fs-h1); line-height: var(--lh-h1); font-weight: 800; margin: 0; overflow-wrap: anywhere; }
  .ap-meta { font-size: var(--fs-caption); color: var(--text-3); }
  .ap-meta a { color: inherit; }
  .mono { font-family: var(--font-mono); }
  .ap-card { padding: var(--sp-4); border-radius: var(--r-card); background: var(--surface); border: 1px solid var(--violet-35); display: flex; flex-direction: column; gap: var(--sp-3); }
  .ap-card.is-hot { border-color: var(--gold); }
  .ap-head { display: flex; justify-content: space-between; align-items: baseline; gap: var(--sp-2); }
  .ap-ends-h { font-size: var(--fs-caption); line-height: var(--lh-caption); letter-spacing: var(--ls-caption); text-transform: uppercase; font-weight: 700; color: var(--text-3); margin: 0; }
  .ap-count { font-size: var(--fs-body); color: var(--text); }
  .ap-count.is-urgent { color: var(--red); font-weight: 700; }
  .ap-snipe { font-size: var(--fs-small); color: var(--gold-300); font-weight: 600; background: var(--gold-12); border: 1px solid var(--gold-35); border-radius: var(--r-control); padding: var(--sp-2) var(--sp-3); }
  .ap-price { font-size: clamp(1.75rem, 5vw, 2.25rem); font-weight: 600; line-height: 1; }
  .ap-price.is-gold { color: var(--gold-300); }
  .ap-price small { font-size: .5em; opacity: .7; }
  .ap-sub { font-size: var(--fs-small); color: var(--text-2); }
  .ap-ok { font-size: var(--fs-body); color: var(--green); background: var(--green-12); border: 1px solid var(--green); border-radius: var(--r-control); padding: var(--sp-3); font-weight: 600; }
  .ap-form { display: flex; flex-direction: column; gap: var(--sp-2); }
  .ap-label { font-size: var(--fs-small); color: var(--text-2); font-weight: 600; }
  .ap-inrow { display: flex; gap: var(--sp-2); flex-wrap: wrap; }
  .ap-inrow .ap-input { flex: 1 1 160px; }
  .ap-input { min-height: var(--hit); padding: 0 var(--sp-3); border-radius: var(--r-control); background: rgba(255,255,255,.05); border: 1px solid var(--line-strong); color: var(--text); font-size: 16px; width: 100%; }
  .ap-err { color: var(--red); font-size: var(--fs-small); }
  .ap-hint { font-size: var(--fs-small); color: var(--text-2); margin: 0; line-height: var(--lh-small); }
  .ap-numbers { display: grid; grid-template-columns: repeat(3, 1fr); gap: var(--sp-2); margin: var(--sp-1) 0 0; }
  .ap-numbers > div { background: rgba(255,255,255,.03); border: 1px solid var(--line); border-radius: var(--r-control); padding: var(--sp-2) var(--sp-3); display: flex; flex-direction: column; gap: 2px; min-width: 0; }
  .ap-numbers dt { font-size: var(--fs-caption); line-height: var(--lh-caption); letter-spacing: var(--ls-caption); text-transform: uppercase; color: var(--text-3); font-weight: 700; }
  .ap-numbers dd { margin: 0; font-size: var(--fs-body); font-weight: 600; color: var(--text); overflow-wrap: anywhere; }
  .ap-check { color: var(--green); display: inline-flex; align-items: center; gap: 4px; }
  .ap-cellrow { display: inline-flex; align-items: center; gap: var(--sp-2); }
  .ap-sync { display: inline-flex; align-items: center; gap: var(--sp-2); padding: var(--sp-2) var(--sp-3); border-radius: var(--r-pill); background: var(--green-12); border: 1px solid var(--green); color: var(--green); font-size: var(--fs-small); font-weight: 600; align-self: flex-start; }
  .ap-spin { width: 12px; height: 12px; border-radius: 50%; border: 2px solid rgba(74,222,128,.4); border-top-color: var(--green); animation: sp 1s linear infinite; }
  @keyframes sp { to { transform: rotate(360deg); } }
  .ap-section h2 { font-size: var(--fs-h3); font-weight: 800; margin: 0 0 var(--sp-2); display: flex; gap: var(--sp-2); align-items: center; }
  .ap-n { font-size: var(--fs-caption); background: rgba(255,255,255,.1); border-radius: var(--r-pill); padding: 2px 8px; }
  .ap-list { list-style: none; margin: 0; padding: 0; display: flex; flex-direction: column; gap: var(--sp-1); }
  .ap-list li { display: flex; align-items: center; gap: var(--sp-3); flex-wrap: wrap; padding: var(--sp-2) var(--sp-3); border-radius: var(--r-control); background: var(--surface); border: 1px solid var(--line); font-size: var(--fs-small); min-height: var(--hit); }
  .ap-dim { color: var(--text-3); }
  .ap-ext { display: inline-flex; align-items: center; margin-left: auto; min-width: var(--hit); min-height: var(--hit); justify-content: center; }
  .ap-sticky { display: none; }
  @media (max-width: 639px) {
    .ap-sticky { display: flex; position: fixed; left: 0; right: 0; bottom: calc(var(--tabbar-h, 60px) + env(safe-area-inset-bottom)); z-index: var(--z-banner); align-items: center; justify-content: space-between; gap: var(--sp-3); padding: var(--sp-2) var(--sp-4); background: var(--surface-2); border-top: 1px solid var(--line-strong); }
    .ap-sticky-price { font-weight: 700; color: var(--gold-300); font-size: var(--fs-h3); }
  }
  @media (max-width: 480px) { .ap-numbers { grid-template-columns: 1fr; } }
</style>
