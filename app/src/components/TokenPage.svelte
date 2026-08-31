<script lang="ts">
  // Token detail: media, verified badge, price/auction state, and EVERY action
  // (buy, list, cancel, edit price, create auction, bid, make/accept/reject
  // offer). Owner-aware: what you can do depends on who you are. Live via WS.
  import { onMount } from 'svelte';
  import VerifiedBadge from './VerifiedBadge.svelte';
  import CreatorBadge from './CreatorBadge.svelte';
  import { holderBadgeName, HOLDER_BADGE_TIP } from '../lib/holderBadge';
  import EmptyState from './EmptyState.svelte';
  import ErrorState from './ErrorState.svelte';
  import Skeleton from './Skeleton.svelte';
  import DurationPicker from './DurationPicker.svelte';
  import { MW } from '../lib/mw';
  import { ws } from '../lib/ws/client';
  import { tokenChannel } from '../lib/ws/channels';
  import { currentChain, explorerAddress, tradingLive, readOnlyCopy } from '../lib/chains';
  import { fmtPrice, shortAddr, timeAgo, fmtCountdown, toWei } from '../lib/format';
  import { resolveImageUri } from '../lib/image-uri';
  import { onAccountChange, publicClient } from '../lib/tx/client';
  import { erc721Abi, erc1155Abi, auctionHouseAbi } from '../lib/abi';
  import { DEFAULT_DURATION } from '../lib/tx/durations';
  import { minimumTopUp } from '../lib/tx/auction';
  import type { Address } from 'viem';

  type Listing = { collection: string; token_id: string; seller: string; price_wei: string; amount: number; standard: string; expires_at: string; name: string; image_uri: string; collection_verified: boolean };
  type Collection = { address: string; name: string; symbol: string; standard: string; verified: boolean; creator_addr?: string };
  type Auction = { auction_id: number; collection: string; token_id: string; seller: string; reserve_price_wei: string; highest_bid_wei: string; highest_bidder: string; min_increment_bps?: number; ends_at: string; status: string; name: string; image_uri: string; collection_verified: boolean };
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
  // On-chain fallback for tokens the DB has never indexed (every
  // explorer-sourced NFT on read-only networks): metadata read straight from
  // the contract via tokenURI/uri. When set, the normal shell renders with a
  // "not indexed" note instead of the error state.
  let onchain = $state(false);
  let ocName = $state('');
  let ocImg = $state('');
  let ocStd = $state<'erc721' | 'erc1155' | null>(null);

  // forms
  let panel = $state<'none' | 'list' | 'auction' | 'offer' | 'edit'>('none');
  let priceIn = $state('');
  let duration = $state<number>(DEFAULT_DURATION);
  let bidIn = $state('');
  let qtyIn = $state('1');   // ERC-1155 units for list/auction/offer panels
  let incPctIn = $state(''); // optional auction minimum-raise % (0-50)
  let formErr = $state('');
  let myCumWei = $state(0n); // caller's cumulative escrow on the live auction

  const canTrade = tradingLive();
  const roCopy = readOnlyCopy();

  const chain = currentChain();
  const sym = chain.currency;

  let std = $derived((listing?.standard || collection?.standard || ocStd || 'erc721') as 'erc721' | 'erc1155');
  let name = $derived(listing?.name || auction?.name || (collection?.name ? `${collection.name} #${tid}` : ocName || `#${tid}`));
  let img = $derived(resolveImageUri(listing?.image_uri || auction?.image_uri || ocImg, tid, 512));
  let verified = $derived(!!(collection?.verified ?? listing?.collection_verified ?? auction?.collection_verified));
  let creatorAddr = $derived(collection?.creator_addr || '');
  let isOwner = $derived(!!me && (std === 'erc1155' ? myBalance1155 > 0n : owner?.toLowerCase() === me.toLowerCase()));
  let isSeller = $derived(!!me && !!listing && listing.seller.toLowerCase() === me.toLowerCase());
  let isAuctionSeller = $derived(!!me && !!auction && auction.seller.toLowerCase() === me.toLowerCase());
  let auctionLive = $derived(!!auction && auction.status === 'active' && new Date(auction.ends_at).getTime() > now);
  let auctionEnded = $derived(!!auction && auction.status === 'active' && new Date(auction.ends_at).getTime() <= now);
  let myOffer = $derived(me ? offers.find((o) => o.bidder.toLowerCase() === me!.toLowerCase() && o.status === 'active') ?? null : null);
  let liveOffers = $derived(offers.filter((o) => o.status === 'active' && new Date(o.expires_at).getTime() > now));
  let minBid = $derived(auction ? minimumTopUp({ currentHighestWei: BigInt(auction.highest_bid_wei || '0'), reserveWei: BigInt(auction.reserve_price_wei || '0'), myCumulativeWei: myCumWei, minIncrementBps: auction.min_increment_bps }) : 0n);

  // Read the caller's cumulative escrow so the min top-up is the real amount
  // still owed, not the full leader total (matches AuctionPage behaviour).
  async function loadMyCumulative() {
    myCumWei = 0n;
    if (!me || !auction) return;
    try {
      const pub = await publicClient();
      const ahAddr = chain.contracts.auctionHouse;
      if (!ahAddr) return;
      myCumWei = (await pub.readContract({ address: ahAddr as Address, abi: auctionHouseAbi, functionName: 'cumulative', args: [BigInt(auction.auction_id), me as Address] })) as bigint;
    } catch { /* stays 0n; user just tops up the full amount */ }
  }
  $effect(() => { if (me && auction) void loadMyCumulative(); });

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

  /** ipfs:// → public gateway; everything else passes through. */
  function ipfsToHttp(u: string): string {
    return u.startsWith('ipfs://') ? 'https://ipfs.io/ipfs/' + u.slice(7) : u;
  }

  /**
   * DB knows nothing about this token — read metadata straight from the
   * contract before giving up: tokenURI (721) then uri (1155), metadata
   * fetched via the media proxy (SSRF-safe) with a direct fetch as backup.
   * Returns true when the token verifiably exists on-chain.
   */
  async function loadOnChainFallback(): Promise<boolean> {
    try {
      const pub = await publicClient();
      let uri = '';
      try {
        uri = (await pub.readContract({ address: coll as Address, abi: erc721Abi, functionName: 'tokenURI', args: [BigInt(tid)] })) as string;
        ocStd = 'erc721';
      } catch {
        try {
          uri = (await pub.readContract({ address: coll as Address, abi: erc1155Abi, functionName: 'uri', args: [BigInt(tid)] })) as string;
          ocStd = 'erc1155';
        } catch { return false; }
      }
      if (!uri) return false;
      // ERC-1155 metadata URIs may embed {id} as 64 lowercase hex digits.
      uri = uri.replace('{id}', BigInt(tid).toString(16).padStart(64, '0'));

      let meta: Record<string, unknown> | null = null;
      if (uri.startsWith('data:application/json;base64,')) {
        try { meta = JSON.parse(atob(uri.slice(uri.indexOf(',') + 1))); } catch { /* malformed inline JSON */ }
      } else if (uri.startsWith('data:')) {
        try { meta = JSON.parse(decodeURIComponent(uri.slice(uri.indexOf(',') + 1))); } catch { /* malformed inline JSON */ }
      } else {
        const httpUri = ipfsToHttp(uri);
        try {
          const r = await fetch('/api/v1/media?url=' + encodeURIComponent(httpUri) + '&id=' + encodeURIComponent(tid));
          if (r.ok) meta = JSON.parse(await r.text());
        } catch { /* proxy miss — try direct */ }
        if (!meta) {
          try { const r = await fetch(httpUri); if (r.ok) meta = JSON.parse(await r.text()); } catch { /* gateway/CORS miss */ }
        }
      }
      // The tokenURI call succeeding is proof enough the token exists —
      // render even when the metadata fetch failed (name falls back to #id).
      ocName = meta && typeof meta.name === 'string' && meta.name ? meta.name : '';
      const rawImg = meta && typeof meta.image === 'string' && meta.image ? meta.image
        : meta && typeof meta.image_url === 'string' && meta.image_url ? meta.image_url : '';
      ocImg = rawImg ? ipfsToHttp(rawImg) : '';
      return true;
    } catch { return false; }
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
      // Scoped to this exact token. The previous form pulled the first 50
      // active auctions in the collection and searched them client side, so a
      // collection with more than 50 auctions ordered ahead of this one made
      // the token's own auction invisible — the page then rendered as if no
      // auction existed.
      j<Auction[]>(`/api/v1/auctions?collection=${encodeURIComponent(coll)}&token_id=${encodeURIComponent(tid)}&status=active&limit=1`),
    ]);
    listing = l && l.price_wei ? l : null;
    collection = c; traits = t ?? {}; activity = a ?? []; offers = o ?? [];
    // The server filters by token_id, so this is a correctness backstop, not a
    // search: it guarantees we never attach ANOTHER token's auction to this
    // page. During a rolling deploy where this frontend is live against a
    // backend that predates the token_id parameter, the single row returned
    // may belong to a different token — this drops it and the page renders as
    // having no auction, which is the safe direction to fail.
    auction = (au ?? []).find((x) => String(x.token_id) === String(tid)) ?? null;
    if (!c && !l && !auction) {
      // Unindexed token (common on read-only networks where NFTs come from
      // the explorer, never the DB): fall back to on-chain reads before
      // showing the error. Skip the RPC round-trips on reloads that already
      // proved the token exists.
      if (!onchain) onchain = await loadOnChainFallback();
      if (!onchain) error = "We don't know this NFT yet. If it was just minted or transferred, it will appear here within a few minutes.";
    } else {
      onchain = false;
    }
    await loadOwner();
    // Deep link from profile "List →": open the list panel once ownership confirms.
    if (initial && location.hash === '#list' && isOwner && !listing && !auctionLive) panel = 'list';
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
  function parseQty(): string | null {
    if (std !== 'erc1155') return '1';
    const t = qtyIn.trim();
    if (!/^\d+$/.test(t)) { formErr = 'Quantity must be a whole number of at least 1.'; return null; }
    const n = BigInt(t); // bigint end-to-end: Number() silently rounds > 2^53
    if (n < 1n) { formErr = 'Quantity must be a whole number of at least 1.'; return null; }
    if (myBalance1155 > 0n && n > myBalance1155) { formErr = `You hold ${myBalance1155} unit${myBalance1155 === 1n ? '' : 's'}.`; return null; }
    return n.toString();
  }
  function parseIncPct(): number | undefined {
    if (!incPctIn.trim()) return undefined;
    const n = Number(incPctIn);
    if (!Number.isFinite(n) || n < 0 || n > 50) { formErr = 'Minimum raise must be 0-50%.'; return -1 as never; }
    return Math.round(n * 100); // percent -> bps
  }
  function parsePrice(): string | null {
    try { const w = toWei(priceIn); if (w < 10n ** 18n) { formErr = `Minimum is 1 ${sym}.`; return null; } return w.toString(); } catch { formErr = 'Enter a number like 12.5'; return null; }
  }

  const doBuy = () => listing && act(() => MW.buy({ nft: coll, tokenId: tid, seller: listing!.seller, priceWei: listing!.price_wei, name }), 'Just bought · syncing');
  const doList = () => { const p = parsePrice(); const q = p && parseQty(); if (p && q) act(() => MW.list({ nft: coll, tokenId: tid, priceWei: p, duration, std, amount: q, name }), 'Listed · syncing'); };
  const doEdit = () => { const p = parsePrice(); if (p) act(() => MW.editPrice({ nft: coll, tokenId: tid, newPriceWei: p, name }), 'Price updated · syncing'); };
  const doCancel = () => act(() => MW.cancelListing({ nft: coll, tokenId: tid, name }), 'Listing cancelled · syncing');
  const doAuction = () => { const p = parsePrice(); const q = p && parseQty(); const inc = parseIncPct(); if ((inc as unknown as number) === -1) return; if (p && q) act(() => MW.createAuction({ nft: coll, tokenId: tid, reserveWei: p, duration, std, amount: q, minIncBps: inc, name }), 'Auction created · syncing'); };
  const doBid = () => { if (!auction) return; let w: bigint; try { w = toWei(bidIn); } catch { formErr = 'Enter a number like 12.5'; return; } if (w < minBid) { formErr = `Minimum bid is ${fmtPrice(minBid)} ${sym}.`; return; } act(() => MW.bid({ auctionId: String(auction!.auction_id), amountWei: w.toString(), name, myCumulativeWei: myCumWei.toString() }), 'Bid placed · syncing'); };
  const doSettle = () => auction && act(() => MW.settle({ auctionId: String(auction!.auction_id), name }), 'Settled · syncing');
  const doCancelAuction = () => auction && act(() => MW.cancelAuction({ auctionId: String(auction!.auction_id), name }), 'Auction cancelled · syncing');
  const doOffer = () => { const p = parsePrice(); const q = p && parseQty(); if (p && q) act(() => MW.makeOffer({ nft: coll, tokenId: tid, principalWei: p, duration, std, units: q, name }), 'Offer placed · syncing'); };
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
        <VerifiedBadge {verified} showUnverified={true} network={chain.name} collectionName={collection?.name || ''} {creatorAddr} />
        {#if live}<span class="tp-live" title="Live updates connected">● live</span>{/if}
      </div>
      <h1 class="tp-title">{name}</h1>
      <div class="tp-meta mono">
        <a href={explorerAddress(coll)} target="_blank" rel="noopener">{shortAddr(coll)}</a> · #{tid} · {std.toUpperCase()}
        {#if owner} · owner <a href={`/profile/${owner}`}>{isOwner ? 'you' : shortAddr(owner)}</a> <span class="vb is-holder sm" title={HOLDER_BADGE_TIP}>{holderBadgeName(owner)}</span>{/if}
      </div>
      {#if creatorAddr}
        <div class="tp-meta mono">
          creator <a href={explorerAddress(creatorAddr)} target="_blank" rel="noopener">{shortAddr(creatorAddr)}</a>
          <CreatorBadge name={collection?.name || ''} />
        </div>
      {/if}

      {#if onchain}
        <p class="tp-hint tp-onchain">Not indexed by the marketplace — showing on-chain data.</p>
      {/if}

      {#if syncing}<div class="tp-sync" role="status"><span class="tp-spin" aria-hidden="true"></span>{syncing}</div>{/if}

      {#if !canTrade}
        <section class="tp-card" aria-label="Read-only network">
          <div class="tp-card-head"><span>{roCopy.heading}</span></div>
          <p class="tp-hint">{roCopy.body}</p>
          {#if roCopy.ctaHref}<a class="btn p" href={roCopy.ctaHref}>{roCopy.cta}</a>{/if}
        </section>
      {/if}

      <!-- ── price / auction block ─────────────────────────────────── -->
      {#if auction && (auctionLive || auctionEnded)}
        <section class="tp-card is-violet" aria-labelledby="au-h">
          <div class="tp-card-head"><span id="au-h">{auctionLive ? 'Live auction' : 'Auction ended — awaiting settlement'}</span><span class="mono">{fmtCountdown(new Date(auction.ends_at).getTime() / 1000, now)}</span></div>
          <div class="tp-price mono">{fmtPrice(BigInt(auction.highest_bid_wei || '0') > 0n ? auction.highest_bid_wei : auction.reserve_price_wei)} <small>{sym}</small></div>
          <div class="tp-sub">{BigInt(auction.highest_bid_wei || '0') > 0n ? `Highest bid by ${shortAddr(auction.highest_bidder)}` : 'No bids yet · reserve shown'} · bids in the last 3 min extend the auction</div>
          {#if canTrade && auctionLive && !isAuctionSeller}
            <div class="tp-form">
              <label class="tp-label" for="bid-in">Your bid ({sym}) · min {fmtPrice(minBid)}</label>
              <div class="tp-inrow"><input id="bid-in" class="tp-input mono" inputmode="decimal" placeholder={fmtPrice(minBid)} bind:value={bidIn} /><button class="btn p" onclick={doBid}>Place bid</button></div>
            </div>
          {:else if canTrade && auctionEnded}
            {#if isAuctionSeller || (me && auction && auction.highest_bidder?.toLowerCase() === me.toLowerCase())}
              <button class="btn p" onclick={doSettle}>Settle now</button>
              <p class="tp-hint">The marketplace settles this automatically within seconds; you can also settle it yourself.</p>
            {:else}
              <p class="tp-hint">Auction ended — settling automatically. NFT to the winner, seller paid minus 1.5%.</p>
            {/if}
          {:else if canTrade && isAuctionSeller && BigInt(auction.highest_bid_wei || '0') === 0n}
            <button class="btn g" onclick={doCancelAuction}>Cancel auction</button>
          {:else if canTrade && isAuctionSeller}
            <p class="tp-hint">Your auction has bids and will settle when it ends.</p>
          {/if}
        </section>
      {:else if listing}
        <section class="tp-card is-gold" aria-labelledby="ls-h">
          <div class="tp-card-head"><span id="ls-h">Listed for sale</span><span>expires {timeAgo(listing.expires_at).replace(' ago', '')} from now</span></div>
          <div class="tp-price mono">{fmtPrice(listing.price_wei)} <small>{sym}</small></div>
          <div class="tp-sub">Seller {isSeller ? 'you' : shortAddr(listing.seller)} · 1.5% fee paid by the seller</div>
          {#if isSeller}
            {#if canTrade}<div class="tp-btnrow"><button class="btn g" onclick={() => openPanel('edit')}>Change price</button><button class="btn g" onclick={doCancel}>Cancel listing</button></div>{/if}
          {:else if canTrade}
            <button class="btn p" onclick={doBuy}>{me ? `Buy now · ${fmtPrice(listing.price_wei)} ${sym}` : `Connect to buy · ${fmtPrice(listing.price_wei)} ${sym}`}</button>
          {/if}
        </section>
      {:else}
        <section class="tp-card">
          <div class="tp-sub">Not listed for sale{isOwner ? ' — you own this NFT' : ''}.</div>
        </section>
      {/if}

      <!-- ── owner actions ─────────────────────────────────────────── -->
      <!-- auctionEnded (ended, not yet settled) must gate these too: the seller
           still holds the token, so isOwner is true, but settle() will transfer
           it to the winner. Listing or re-auctioning in that window commits the
           same token twice and strands whichever action loses the race. -->
      {#if canTrade && isOwner && !listing && !auctionLive && !auctionEnded}
        <div class="tp-btnrow">
          <button class="btn p" onclick={() => openPanel('list')}>List for sale</button>
          <button class="btn v" onclick={() => openPanel('auction')}>Start auction</button>
        </div>
      {/if}
      {#if canTrade && !isOwner && !isSeller}
        <div class="tp-btnrow">
          {#if myOffer}
            <button class="btn gold" onclick={() => openPanel('offer')}>Change offer ({fmtPrice(myOffer.amount_wei)} {sym})</button>
            <button class="btn g" onclick={doCancelOffer}>Cancel my offer</button>
          {:else if offerEligible === false}
            <span class="tp-hint">Offers are off for this collection.{isOwner ? '' : ' The collection owner can enable them.'}</span>
          {:else}
            <button class="btn gold" onclick={() => openPanel('offer')}>Make offer</button>
          {/if}
        </div>
      {/if}
      {#if canTrade && offerEligible === false && isOwner}
        <button class="btn g" onclick={doEnableOffers}>Enable offers for this collection</button>
        <p class="tp-hint">Only the collection contract owner can do this; the transaction is rejected otherwise.</p>
      {/if}

      <!-- ── inline forms ──────────────────────────────────────────── -->
      {#if canTrade && panel !== 'none'}
        <section class="tp-panel" aria-label="Action form">
          <label class="tp-label" for="price-in">
            {panel === 'list' ? `Price (${sym})` : panel === 'edit' ? `New price (${sym})` : panel === 'auction' ? `Reserve price (${sym})` : `Your offer (${sym})`}
          </label>
          <input id="price-in" class="tp-input mono" inputmode="decimal" placeholder="min 1" bind:value={priceIn} />
          {#if std === 'erc1155' && panel !== 'edit'}
            <label class="tp-label" for="qty-in">{panel === 'offer' ? 'Units wanted' : 'Units to sell'}{myBalance1155 > 0n && panel !== 'offer' ? ` (you hold ${myBalance1155})` : ''}</label>
            <input id="qty-in" class="tp-input mono" inputmode="numeric" placeholder="1" bind:value={qtyIn} />
          {/if}
          {#if panel === 'auction'}
            <label class="tp-label" for="inc-in">Minimum raise % <span class="tp-dim">(optional, 0–50)</span></label>
            <input id="inc-in" class="tp-input mono" inputmode="decimal" placeholder="default: 1 {sym} flat" bind:value={incPctIn} />
          {/if}
          {#if panel !== 'edit'}<DurationPicker bind:value={duration} label={panel === 'auction' ? 'Auction length' : 'Valid for'} />{/if}
          {#if formErr}<div class="tp-formerr" role="alert">{formErr}</div>{/if}
          <div class="tp-btnrow">
            {#if panel === 'list'}<button class="btn p" onclick={doList}>List · free</button>
            {:else if panel === 'edit'}<button class="btn p" onclick={doEdit}>Update price</button>
            {:else if panel === 'auction'}<button class="btn v" onclick={doAuction}>Start auction · free</button>
            {:else}<button class="btn gold" onclick={doOffer}>Place offer</button>{/if}
            <button class="btn g" onclick={() => (panel = 'none')}>Close</button>
          </div>
          <p class="tp-hint">{panel === 'offer' ? (myOffer ? 'Changing an offer replaces the amount on-chain — the original expiry keeps counting down (it is not extended).' : 'Your offer amount is held in escrow and fully refundable until it expires.') : 'Listing is free; the 1.5% fee is taken from the sale only.'}</p>
        </section>
      {/if}

      <!-- ── offers ────────────────────────────────────────────────── -->
      <section class="tp-section">
        <h2>Offers {#if liveOffers.length}<span class="tp-count">{liveOffers.length}</span>{/if}</h2>
        {#if liveOffers.length === 0}
          <p class="tp-hint">No open offers yet. Connect a wallet to make the first one — your funds stay refundable until it's accepted.</p>
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
  .tp-onchain { color: rgba(255,255,255,.35); font-style: italic; }
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
