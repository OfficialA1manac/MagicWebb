<script lang="ts">
  // Token detail: media, verified badge, price/auction state, and EVERY action
  // (buy, list, cancel, edit price, create auction, bid, make/accept/reject
  // offer). Owner-aware: what you can do depends on who you are. Live via WS.
  import { onMount } from 'svelte';
  import VerifiedBadge from './VerifiedBadge.svelte';
  import EmptyState from './EmptyState.svelte';
  import ErrorState from './ErrorState.svelte';
  import Skeleton from './Skeleton.svelte';
  import DurationPicker from './DurationPicker.svelte';
  import { MW } from '../lib/mw';
  import { ws } from '../lib/ws/client';
  import { tokenChannel } from '../lib/ws/channels';
  import { currentChain, explorerAddress } from '../lib/chains';
  import { fmtPrice, shortAddr, timeAgo, fmtCountdown, toWei } from '../lib/format';
  import { resolveImageUri } from '../lib/image-uri';
  import { onAccountChange, publicClient } from '../lib/tx/client';
  import { erc721Abi, erc1155Abi } from '../lib/abi';
  import { DEFAULT_DURATION } from '../lib/tx/durations';
  import { minimumTopUp } from '../lib/tx/auction';
  import type { Address } from 'viem';

  type Listing = { collection: string; token_id: string; seller: string; price_wei: string; amount: number; standard: string; expires_at: string; name: string; image_uri: string; collection_verified: boolean };
  type Collection = { address: string; name: string; symbol: string; standard: string; verified: boolean };
  type Auction = { auction_id: number; collection: string; token_id: string; seller: string; reserve_price_wei: string; highest_bid_wei: string; highest_bidder: string; ends_at: string; status: string; name: string; image_uri: string; collection_verified: boolean };
  type Offer = { offer_id: string; bidder: string; amount_wei: string; units: number; standard: string; expires_at: string; status: string };
  type Activity = { type: string; amountWei: string; timestamp: string; txHash: string };

  let coll = $state('');
  let tid = $state('');
  let loading = $state(true);
  let error = $state('');
  let listing = $state<Listing | null>(null);
  let collection = $state<Collection | null>(null);
  let auction = $state<Auction | null>(null);
  let offers = $state<Offer[]>([]);
  let activity = $state<Activity[]>([]);
  let traits = $state<Record<string, string>>({});
  let owner = $state<string | null>(null);
  let myBalance1155 = $state(0n);
  let me = $state<string | null>(null);
  let offerEligible = $state<boolean | null>(null);
  let live = $state(false);
  let now = $state(Date.now());
  let syncing = $state(''); // optimistic chip text after a confirmed tx

  // forms
  let panel = $state<'none' | 'list' | 'auction' | 'offer' | 'edit'>('none');
  let priceIn = $state('');
  let duration = $state<number>(DEFAULT_DURATION);
  let bidIn = $state('');
  let formErr = $state('');

  const chain = currentChain();
  const sym = chain.currency;

  let std = $derived((listing?.standard || collection?.standard || 'erc721') as 'erc721' | 'erc1155');
  let name = $derived(listing?.name || auction?.name || (collection?.name ? `${collection.name} #${tid}` : `#${tid}`));
  let img = $derived(resolveImageUri(listing?.image_uri || auction?.image_uri || '', tid));
  let verified = $derived(!!(collection?.verified ?? listing?.collection_verified ?? auction?.collection_verified));
  let isOwner = $derived(!!me && (std === 'erc1155' ? myBalance1155 > 0n : owner?.toLowerCase() === me.toLowerCase()));
  let isSeller = $derived(!!me && !!listing && listing.seller.toLowerCase() === me.toLowerCase());
  let isAuctionSeller = $derived(!!me && !!auction && auction.seller.toLowerCase() === me.toLowerCase());
  let auctionLive = $derived(!!auction && auction.status === 'active' && new Date(auction.ends_at).getTime() > now);
  let auctionEnded = $derived(!!auction && auction.status === 'active' && new Date(auction.ends_at).getTime() <= now);
  let myOffer = $derived(me ? offers.find((o) => o.bidder.toLowerCase() === me!.toLowerCase() && o.status === 'active') ?? null : null);
  let liveOffers = $derived(offers.filter((o) => o.status === 'active' && new Date(o.expires_at).getTime() > now));
  let minBid = $derived(auction ? minimumTopUp({ currentHighestWei: BigInt(auction.highest_bid_wei || '0'), reserveWei: BigInt(auction.reserve_price_wei || '0'), myCumulativeWei: 0n }) : 0n);

  async function j<T>(url: string): Promise<T | null> {
    try { const r = await fetch(url); return r.ok ? (await r.json()) as T : null; } catch { return null; }
  }

  async function loadOwner() {
    try {
      const pub = await publicClient();
      if (std === 'erc1155') {
        if (me) myBalance1155 = (await pub.readContract({ address: coll as Address, abi: erc1155Abi, functionName: 'balanceOf', args: [me as Address, BigInt(tid)] })) as bigint;
      } else {
        owner = (await pub.readContract({ address: coll as Address, abi: erc721Abi, functionName: 'ownerOf', args: [BigInt(tid)] })) as string;
      }
    } catch { /* unknown owner: UI simply hides owner actions */ }
  }

  async function load(initial = false) {
    if (initial) loading = true;
    error = '';
    const q = `collection=${encodeURIComponent(coll)}&token_id=${encodeURIComponent(tid)}`;
    const [l, c, t, a, o, au] = await Promise.all([
      j<Listing>(`/api/v1/listings/${coll}/${tid}`),
      j<Collection>(`/api/v1/collections/${coll}`),
      j<Record<string, string>>(`/api/v1/collections/${coll}/traits`),
      j<Activity[]>(`/api/v1/activity?${q}&limit=20`),
      j<Offer[]>(`/api/v1/offers?${q}&limit=20`),
      j<Auction[]>(`/api/v1/auctions?collection=${encodeURIComponent(coll)}&status=active&limit=50`),
    ]);
    listing = l && l.price_wei ? l : null;
    collection = c; traits = t ?? {}; activity = a ?? []; offers = o ?? [];
    auction = (au ?? []).find((x) => String(x.token_id) === String(tid)) ?? null;
    if (!c && !l && !auction) error = 'This NFT is not indexed yet. If it was just minted, give the indexer a moment.';
    await loadOwner();
    if (offerEligible === null) MW.isOfferEligible(coll).then((v) => (offerEligible = v)).catch(() => (offerEligible = null));
    loading = false;
    syncing = '';
  }

  onMount(() => {
    const parts = location.pathname.replace(/^\/token\//, '').split('/');
    coll = (parts[0] || '').toLowerCase(); tid = parts[1] || '';
    if (!/^0x[0-9a-f]{40}$/.test(coll) || !tid) { error = 'Invalid token URL.'; loading = false; return; }
    fetch(`/api/v1/token/${coll}/${tid}/view`, { method: 'POST' }).catch(() => {});
    void load(true);
    const offAcct = onAccountChange((a) => { me = a.address; void loadOwner(); });
    ws.subscribe(tokenChannel(coll, tid));
    const offWs = ws.on('*', (_d, meta) => { if (meta.type !== 'notification') void load(); });
    const offSt = ws.onStatus((s) => (live = s === 'open'));
    const tick = setInterval(() => (now = Date.now()), 1000);
    return () => { offAcct(); offWs(); offSt(); clearInterval(tick); ws.unsubscribe(tokenChannel(coll, tid)); };
  });

  function afterTx(label: string) { syncing = label; panel = 'none'; formErr = ''; setTimeout(() => void load(), 1500); setTimeout(() => void load(), 6000); }
  async function act(run: () => Promise<unknown>, label: string) {
    formErr = '';
    try { await run(); afterTx(label); } catch (e) { /* TxModal already showed it */ void e; }
  }
  function parsePrice(): string | null {
    try { const w = toWei(priceIn); if (w < 10n ** 18n) { formErr = `Minimum is 1 ${sym}.`; return null; } return w.toString(); } catch { formErr = 'Enter a number like 12.5'; return null; }
  }

  const doBuy = () => listing && act(() => MW.buy({ nft: coll, tokenId: tid, seller: listing!.seller, priceWei: listing!.price_wei, name }), 'Just bought · syncing');
  const doList = () => { const p = parsePrice(); if (p) act(() => MW.list({ nft: coll, tokenId: tid, priceWei: p, duration, std, name }), 'Listed · syncing'); };
  const doEdit = () => { const p = parsePrice(); if (p) act(() => MW.editPrice({ nft: coll, tokenId: tid, newPriceWei: p, name }), 'Price updated · syncing'); };
  const doCancel = () => act(() => MW.cancelListing({ nft: coll, tokenId: tid, name }), 'Listing cancelled · syncing');
  const doAuction = () => { const p = parsePrice(); if (p) act(() => MW.createAuction({ nft: coll, tokenId: tid, reserveWei: p, duration, std, name }), 'Auction created · syncing'); };
  const doBid = () => { if (!auction) return; let w: bigint; try { w = toWei(bidIn); } catch { formErr = 'Enter a number like 12.5'; return; } if (w < minBid) { formErr = `Minimum bid is ${fmtPrice(minBid)} ${sym}.`; return; } act(() => MW.bid({ auctionId: String(auction!.auction_id), amountWei: w.toString(), name }), 'Bid placed · syncing'); };
  const doSettle = () => auction && act(() => MW.settle({ auctionId: String(auction!.auction_id), name }), 'Settled · syncing');
  const doCancelAuction = () => auction && act(() => MW.cancelAuction({ auctionId: String(auction!.auction_id), name }), 'Auction cancelled · syncing');
  const doOffer = () => { const p = parsePrice(); if (p) act(() => MW.makeOffer({ nft: coll, tokenId: tid, principalWei: p, duration, std, name }), 'Offer placed · syncing'); };
  const doAccept = (o: Offer) => act(() => MW.acceptOffer({ nft: coll, tokenId: tid, bidder: o.bidder, principalWei: o.amount_wei, std, name }), 'Offer accepted · syncing');
  const doReject = (o: Offer) => act(() => MW.rejectOffer({ nft: coll, tokenId: tid, bidder: o.bidder, name }), 'Offer declined · syncing');
  const doCancelOffer = () => act(() => MW.cancelOffer({ nft: coll, tokenId: tid, name }), 'Offer cancelled · syncing');
  const doEnableOffers = () => act(() => MW.setOfferEligible({ nft: coll, eligible: true, name: collection?.name }), 'Offers enabled · syncing');

  const openPanel = (p: typeof panel) => { panel = panel === p ? 'none' : p; formErr = ''; priceIn = ''; };
</script>

{#if loading}
  <div class="tp-grid">
    <Skeleton square r="20px" />
    <div style="display:flex;flex-direction:column;gap:12px"><Skeleton w="40%" h="14px" /><Skeleton w="70%" h="28px" /><Skeleton w="50%" h="34px" /><Skeleton h="48px" r="12px" /><Skeleton h="48px" r="12px" /></div>
  </div>
{:else if error && !collection && !listing && !auction}
  <ErrorState message={error} retry={() => load(true)} />
{:else}
  <div class="tp-grid">
    <div class="tp-media">
      {#if img}<img src={img} alt={name} loading="eager" />{:else}<div class="tp-noimg" aria-hidden="true">🖼</div>{/if}
    </div>

    <div class="tp-side">
      <div class="tp-coll">
        <a href={`/collection/${coll}`}>{collection?.name || shortAddr(coll)}</a>
        <VerifiedBadge {verified} showUnverified={true} network={chain.name} />
        {#if live}<span class="tp-live" title="Live updates connected">● live</span>{/if}
      </div>
      <h1 class="tp-title">{name}</h1>
      <div class="tp-meta mono">
        <a href={explorerAddress(coll)} target="_blank" rel="noopener">{shortAddr(coll)}</a> · #{tid} · {std.toUpperCase()}
        {#if owner} · owner <a href={`/profile/${owner}`}>{isOwner ? 'you' : shortAddr(owner)}</a>{/if}
      </div>

      {#if syncing}<div class="tp-sync" role="status"><span class="tp-spin" aria-hidden="true"></span>{syncing}</div>{/if}

      <!-- ── price / auction block ─────────────────────────────────── -->
      {#if auction && (auctionLive || auctionEnded)}
        <section class="tp-card is-violet" aria-labelledby="au-h">
          <div class="tp-card-head"><span id="au-h">{auctionLive ? 'Live auction' : 'Auction ended — awaiting settlement'}</span><span class="mono">{fmtCountdown(new Date(auction.ends_at).getTime() / 1000, now)}</span></div>
          <div class="tp-price mono">{fmtPrice(BigInt(auction.highest_bid_wei || '0') > 0n ? auction.highest_bid_wei : auction.reserve_price_wei)} <small>{sym}</small></div>
          <div class="tp-sub">{BigInt(auction.highest_bid_wei || '0') > 0n ? `Highest bid by ${shortAddr(auction.highest_bidder)}` : 'No bids yet · reserve shown'} · bids in the last 3 min extend the auction</div>
          {#if auctionLive && !isAuctionSeller}
            <div class="tp-form">
              <label class="tp-label" for="bid-in">Your bid ({sym}) · min {fmtPrice(minBid)}</label>
              <div class="tp-inrow"><input id="bid-in" class="tp-input mono" inputmode="decimal" placeholder={fmtPrice(minBid)} bind:value={bidIn} /><button class="btn p" onclick={doBid}>Place bid</button></div>
            </div>
          {:else if auctionEnded}
            <button class="btn p" onclick={doSettle}>Settle auction</button>
            <p class="tp-hint">Anyone can settle. The NFT goes to the winner and the seller is paid (minus 1.5%).</p>
          {:else if isAuctionSeller && BigInt(auction.highest_bid_wei || '0') === 0n}
            <button class="btn g" onclick={doCancelAuction}>Cancel auction</button>
          {:else if isAuctionSeller}
            <p class="tp-hint">Your auction has bids and will settle when it ends.</p>
          {/if}
        </section>
      {:else if listing}
        <section class="tp-card is-gold" aria-labelledby="ls-h">
          <div class="tp-card-head"><span id="ls-h">Listed for sale</span><span>expires {timeAgo(listing.expires_at).replace(' ago', '')} from now</span></div>
          <div class="tp-price mono">{fmtPrice(listing.price_wei)} <small>{sym}</small></div>
          <div class="tp-sub">Seller {isSeller ? 'you' : shortAddr(listing.seller)} · 1.5% fee paid by the seller</div>
          {#if isSeller}
            <div class="tp-btnrow"><button class="btn g" onclick={() => openPanel('edit')}>Change price</button><button class="btn g" onclick={doCancel}>Cancel listing</button></div>
          {:else}
            <button class="btn p" onclick={doBuy}>{me ? `Buy now · ${fmtPrice(listing.price_wei)} ${sym}` : `Connect to buy · ${fmtPrice(listing.price_wei)} ${sym}`}</button>
          {/if}
        </section>
      {:else}
        <section class="tp-card">
          <div class="tp-sub">Not listed for sale{isOwner ? ' — you own this NFT' : ''}.</div>
        </section>
      {/if}

      <!-- ── owner actions ─────────────────────────────────────────── -->
      {#if isOwner && !listing && !auctionLive}
        <div class="tp-btnrow">
          <button class="btn p" onclick={() => openPanel('list')}>List for sale</button>
          <button class="btn v" onclick={() => openPanel('auction')}>Start auction</button>
        </div>
      {/if}
      {#if !isOwner && !isSeller}
        <div class="tp-btnrow">
          {#if myOffer}
            <button class="btn g" onclick={doCancelOffer}>Cancel my offer ({fmtPrice(myOffer.amount_wei)} {sym})</button>
          {:else if offerEligible === false}
            <span class="tp-hint">Offers are off for this collection.{isOwner ? '' : ' The collection owner can enable them.'}</span>
          {:else}
            <button class="btn gold" onclick={() => openPanel('offer')}>Make offer</button>
          {/if}
        </div>
      {/if}
      {#if offerEligible === false && isOwner}
        <button class="btn g" onclick={doEnableOffers}>Enable offers for this collection</button>
        <p class="tp-hint">Only the collection contract owner can do this; the transaction is rejected otherwise.</p>
      {/if}

      <!-- ── inline forms ──────────────────────────────────────────── -->
      {#if panel !== 'none'}
        <section class="tp-panel" aria-label="Action form">
          <label class="tp-label" for="price-in">
            {panel === 'list' ? `Price (${sym})` : panel === 'edit' ? `New price (${sym})` : panel === 'auction' ? `Reserve price (${sym})` : `Your offer (${sym})`}
          </label>
          <input id="price-in" class="tp-input mono" inputmode="decimal" placeholder="min 1" bind:value={priceIn} />
          {#if panel !== 'edit'}<DurationPicker bind:value={duration} label={panel === 'auction' ? 'Auction length' : 'Valid for'} />{/if}
          {#if formErr}<div class="tp-formerr" role="alert">{formErr}</div>{/if}
          <div class="tp-btnrow">
            {#if panel === 'list'}<button class="btn p" onclick={doList}>List · free</button>
            {:else if panel === 'edit'}<button class="btn p" onclick={doEdit}>Update price</button>
            {:else if panel === 'auction'}<button class="btn v" onclick={doAuction}>Start auction · free</button>
            {:else}<button class="btn gold" onclick={doOffer}>Place offer</button>{/if}
            <button class="btn g" onclick={() => (panel = 'none')}>Close</button>
          </div>
          <p class="tp-hint">{panel === 'offer' ? 'Your offer amount is held in escrow and fully refundable until it expires.' : 'Listing is free; the 1.5% fee is taken from the sale only.'}</p>
        </section>
      {/if}

      <!-- ── offers ────────────────────────────────────────────────── -->
      <section class="tp-section">
        <h2>Offers {#if liveOffers.length}<span class="tp-count">{liveOffers.length}</span>{/if}</h2>
        {#if liveOffers.length === 0}
          <p class="tp-hint">No open offers.</p>
        {:else}
          <ul class="tp-list">
            {#each liveOffers as o (o.offer_id)}
              <li>
                <span class="mono">{fmtPrice(o.amount_wei)} {sym}</span>
                <span class="tp-dim">{me && o.bidder.toLowerCase() === me.toLowerCase() ? 'you' : shortAddr(o.bidder)} · ends {fmtCountdown(new Date(o.expires_at).getTime() / 1000, now)}</span>
                {#if isOwner}<span class="tp-btnrow"><button class="btn p sm" onclick={() => doAccept(o)}>Accept</button><button class="btn g sm" onclick={() => doReject(o)}>Decline</button></span>{/if}
              </li>
            {/each}
          </ul>
        {/if}
      </section>

      {#if Object.keys(traits).length}
        <section class="tp-section"><h2>Traits</h2><div class="tp-traits">{#each Object.entries(traits) as [k, v]}<span class="tp-trait">{k}: {v}</span>{/each}</div></section>
      {/if}
    </div>
  </div>

  <section class="tp-section tp-activity">
    <h2>Activity</h2>
    {#if activity.length === 0}
      <EmptyState title="No activity yet" body="Sales, listings, bids and offers for this NFT will show up here the moment they happen." />
    {:else}
      <ul class="tp-list">
        {#each activity as a (a.txHash + a.type + a.timestamp)}
          <li><span class="tp-tag">{a.type}</span><span class="tp-dim">{timeAgo(a.timestamp)}</span><span class="mono">{a.amountWei && a.amountWei !== '0' ? `${fmtPrice(a.amountWei)} ${sym}` : ''}</span><a class="tp-dim" href={MW.explorerTx(a.txHash)} target="_blank" rel="noopener">↗</a></li>
        {/each}
      </ul>
    {/if}
  </section>
{/if}

<style>
  .tp-grid { display: grid; grid-template-columns: repeat(auto-fit, minmax(min(100%, 360px), 1fr)); gap: 28px; align-items: start; }
  .tp-media { aspect-ratio: 1; border-radius: 20px; overflow: hidden; background: rgba(9,9,11,.8); border: 1px solid rgba(255,255,255,.08); display: flex; align-items: center; justify-content: center; }
  .tp-media img { width: 100%; height: 100%; object-fit: cover; display: block; }
  .tp-noimg { font-size: 4rem; color: rgba(255,255,255,.08); }
  .tp-side { display: flex; flex-direction: column; gap: 14px; min-width: 0; }
  .tp-coll { display: flex; gap: 10px; align-items: center; flex-wrap: wrap; font-size: 13px; color: rgba(255,255,255,.6); }
  .tp-coll a { color: #7dd3fc; text-decoration: none; font-weight: 600; }
  .tp-live { color: #4ade80; font-size: 11px; font-weight: 700; }
  .tp-title { font-size: clamp(1.5rem, 4vw, 2rem); font-weight: 800; margin: 0; line-height: 1.1; overflow-wrap: anywhere; }
  .tp-meta { font-size: 12px; color: rgba(255,255,255,.45); } .tp-meta a { color: inherit; text-decoration: underline dotted; }
  .mono { font-family: 'JetBrains Mono', ui-monospace, monospace; }
  .tp-sync { display: inline-flex; align-items: center; gap: 8px; padding: 8px 12px; border-radius: 999px; background: rgba(74,222,128,.1); border: 1px solid rgba(74,222,128,.35); color: #bbf7d0; font-size: 13px; font-weight: 600; align-self: flex-start; }
  .tp-spin { width: 12px; height: 12px; border-radius: 50%; border: 2px solid rgba(187,247,208,.4); border-top-color: #4ade80; animation: sp 1s linear infinite; } @keyframes sp { to { transform: rotate(360deg); } }
  .tp-card { padding: 18px; border-radius: 16px; background: rgba(15,15,19,.7); border: 1px solid rgba(255,255,255,.08); display: flex; flex-direction: column; gap: 10px; }
  .tp-card.is-gold { border-color: rgba(252,211,77,.3); } .tp-card.is-violet { border-color: rgba(167,139,250,.35); }
  .tp-card-head { display: flex; justify-content: space-between; gap: 8px; font-size: 11px; text-transform: uppercase; letter-spacing: .08em; font-weight: 700; color: rgba(255,255,255,.5); }
  .tp-price { font-size: clamp(1.75rem, 5vw, 2.25rem); font-weight: 600; line-height: 1; } .tp-price small { font-size: .5em; opacity: .7; }
  .tp-sub { font-size: 13px; color: rgba(255,255,255,.55); }
  .tp-hint { font-size: 12px; color: rgba(255,255,255,.5); margin: 0; line-height: 1.5; }
  .tp-btnrow { display: flex; gap: 8px; flex-wrap: wrap; }
  .btn { min-height: 44px; padding: 0 16px; border-radius: 12px; font-weight: 700; font-size: 15px; border: 1px solid transparent; cursor: pointer; font-family: inherit; display: inline-flex; align-items: center; justify-content: center; flex: 1 1 auto; }
  .btn.sm { min-height: 36px; font-size: 13px; padding: 0 12px; flex: 0 0 auto; }
  .btn.p { background: linear-gradient(135deg,#7dd3fc,#0ea5e9); color: #09090b; }
  .btn.v { background: linear-gradient(135deg,#a78bfa,#7c3aed); color: #fafafa; }
  .btn.gold { background: linear-gradient(135deg,#fcd34d,#f59e0b); color: #09090b; }
  .btn.g { background: transparent; color: #fafafa; border-color: rgba(255,255,255,.16); }
  .btn:focus-visible, .tp-input:focus-visible { outline: 2px solid #7dd3fc; outline-offset: 2px; }
  .tp-form, .tp-panel { display: flex; flex-direction: column; gap: 10px; }
  .tp-panel { padding: 16px; border-radius: 16px; background: rgba(15,15,19,.9); border: 1px solid rgba(125,211,252,.25); }
  .tp-label { font-size: 12px; color: rgba(255,255,255,.6); font-weight: 600; }
  .tp-inrow { display: flex; gap: 8px; flex-wrap: wrap; } .tp-inrow .tp-input { flex: 1 1 160px; }
  .tp-input { min-height: 44px; padding: 0 12px; border-radius: 12px; background: rgba(255,255,255,.05); border: 1px solid rgba(255,255,255,.14); color: #fafafa; font-size: 16px; width: 100%; }
  .tp-formerr { color: #fca5a5; font-size: 13px; }
  .tp-section h2 { font-size: 15px; font-weight: 800; margin: 0 0 10px; display: flex; align-items: center; gap: 8px; }
  .tp-count { font-size: 11px; background: rgba(255,255,255,.1); border-radius: 999px; padding: 2px 8px; }
  .tp-list { list-style: none; margin: 0; padding: 0; display: flex; flex-direction: column; gap: 6px; }
  .tp-list li { display: flex; align-items: center; gap: 12px; flex-wrap: wrap; padding: 10px 12px; border-radius: 12px; background: rgba(15,15,19,.5); border: 1px solid rgba(255,255,255,.05); font-size: 13px; min-height: 44px; }
  .tp-dim { color: rgba(255,255,255,.45); }
  .tp-tag { font-size: 10px; font-weight: 800; text-transform: uppercase; letter-spacing: .06em; color: #c4b5fd; background: rgba(167,139,250,.12); padding: 3px 7px; border-radius: 6px; }
  .tp-traits { display: flex; flex-wrap: wrap; gap: 6px; }
  .tp-trait { padding: 4px 10px; border-radius: 999px; background: rgba(167,139,250,.1); border: 1px solid rgba(167,139,250,.2); font-size: 12px; color: #c4b5fd; }
  .tp-activity { margin-top: 32px; }
  @media (prefers-reduced-motion: reduce) { .tp-spin { animation: none; } }
</style>
