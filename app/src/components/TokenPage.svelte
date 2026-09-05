<script module lang="ts">
  // Pure action-zone matrix (spec B4 "Token"): status × role → cells. The
  // template's controls and the mobile sticky bar follow this table, and the
  // component tests assert every cell without mounting the page. Every cell
  // is a visible control or a disabled control with a Hint `reason`.
  export type TokenStatus = 'not-listed' | 'listed' | 'auction-live' | 'auction-ended';
  export type TokenRole = 'viewer' | 'buyer' | 'seller';
  export interface ActionCell { kind: string; label: string; disabled?: boolean; reason?: string; hint?: string }

  export function actionZone(i: {
    status: TokenStatus; role: TokenRole; browseOnly?: boolean;
    offersEligible?: boolean | null; priceLabel?: string; hasBids?: boolean;
    canForceCancel?: boolean; outbid?: boolean; isWinner?: boolean; hasOwnOffer?: boolean;
  }): ActionCell[] {
    if (i.browseOnly) return [{ kind: 'browse-only', label: 'Browse only' }];
    const offersOff = i.offersEligible === false;
    const offerCell = (connect: boolean): ActionCell => {
      if (offersOff) return { kind: connect ? 'offer-connect' : 'make-offer', label: connect ? 'Connect wallet to make an offer' : 'Make offer', disabled: true, reason: 'Offers are off for this collection' };
      if (connect) return { kind: 'offer-connect', label: 'Connect wallet to make an offer' };
      return i.hasOwnOffer ? { kind: 'raise-offer', label: 'Raise offer' } : { kind: 'make-offer', label: 'Make offer' };
    };
    switch (i.status) {
      case 'not-listed':
        if (i.role === 'seller') return [
          { kind: 'list', label: 'List for sale · free' },
          { kind: 'auction', label: 'Start auction · free' },
        ];
        return [offerCell(i.role === 'viewer')];
      case 'listed':
        if (i.role === 'viewer') return [{ kind: 'buy-connect', label: `Connect to buy${i.priceLabel ? ' · ' + i.priceLabel : ''}`, hint: 'You pay exactly this price. Seller pays the 2% fee.' }];
        if (i.role === 'seller') return [
          { kind: 'edit-price', label: 'Change price' },
          { kind: 'cancel-listing', label: 'Cancel listing' },
        ];
        return [{ kind: 'buy', label: `Buy now${i.priceLabel ? ' · ' + i.priceLabel : ''}` }, offerCell(false)];
      case 'auction-live': {
        if (i.role === 'seller') return [
          i.hasBids
            ? { kind: 'cancel-auction', label: 'Cancel auction', disabled: true, reason: 'An auction with bids cannot be cancelled — it settles when it ends.' }
            : { kind: 'cancel-auction', label: 'Cancel auction' },
        ];
        const cells: ActionCell[] = i.role === 'viewer'
          ? [{ kind: 'bid-connect', label: 'Connect to bid' }]
          : [{ kind: 'bid', label: 'Place bid' }];
        if (i.role === 'buyer' && i.outbid) cells.push({ kind: 'withdraw-bid', label: 'Withdraw your bid' });
        return cells;
      }
      case 'auction-ended': {
        if (i.role === 'viewer') return [{ kind: 'ended-info', label: 'Auction ended — settling', disabled: true, reason: 'The auction is being settled automatically.' }];
        const cells: ActionCell[] = [];
        if (i.role === 'seller' || i.isWinner) {
          cells.push({ kind: 'settle', label: 'Settle now' });
          if (i.canForceCancel) cells.push({ kind: 'force-cancel', label: 'Force-cancel & refund' });
        } else {
          cells.push({ kind: 'ended-info', label: 'Auction ended — settling', disabled: true, reason: 'The auction is being settled automatically. NFT to the winner, seller paid minus 2%.' });
          if (i.outbid) cells.push({ kind: 'withdraw-bid', label: 'Withdraw your bid' });
        }
        return cells;
      }
    }
  }
</script>

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
  import Hint from './Hint.svelte';
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
  import { minimumTopUp, forceCancelUnlocked } from '../lib/tx/auction';
  import type { Address } from 'viem';

  type Listing = { collection: string; token_id: string; seller: string; price_wei: string; amount: number; standard: string; expires_at: string; name: string; image_uri: string; collection_verified: boolean };
  type Collection = { address: string; name: string; symbol: string; standard: string; verified: boolean; creator_addr?: string };
  type Auction = { auction_id: number; collection: string; token_id: string; seller: string; reserve_price_wei: string; highest_bid_wei: string; highest_bidder: string; min_increment_bps?: number; ends_at: string; status: string; name: string; image_uri: string; collection_verified: boolean };
  type Offer = { offer_id: string; bidder: string; amount_wei: string; units: number; standard: string; expires_at: string; status: string };
  type Activity = { type: string; amountWei: string; timestamp: string; txHash: string };
  type TokenDetail = { owner?: string; last_sale_wei?: string; indexed_at?: string };

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
  // ERC-173 owner() of the collection CONTRACT — distinct from the token's
  // owner. OfferBook.setOfferEligible reverts for anyone else, so the
  // "Enable offers" button is shown only to this address. null = unknown
  // (read failed or not yet loaded); the button stays hidden rather than
  // offering a transaction that is guaranteed to revert.
  let collOwner = $state<string | null>(null);
  // The owner's SAVED profile tag (live from /api/v1/profile). The badge chip
  // previously showed only the DETERMINISTIC collector name derived from the
  // address, so a user's custom tag (e.g. "KawaiiMint") never appeared on
  // token/listing pages and edits looked like they didn't propagate
  // (reported 2026-09-01). Refetched on every owner change — always current.
  let ownerTag = $state('');
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
  // GET /api/v1/token/:coll/:id — indexed owner + last sale; 404 = the
  // indexer has never seen this token id.
  let tokenDetail = $state<TokenDetail | null>(null);
  // Collection IS tracked but this token id isn't in it → spec 404 copy.
  let unknownToken = $state(false);
  let imageError = $state(false);
  let refreshingMeta = $state(false);

  // forms
  let panel = $state<'none' | 'list' | 'auction' | 'offer' | 'edit'>('none');
  let priceIn = $state('');
  let duration = $state<number>(DEFAULT_DURATION);
  let bidIn = $state('');
  let qtyIn = $state('1');   // ERC-1155 units for list/auction/offer panels
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
  let isCollOwner = $derived(!!me && !!collOwner && collOwner.toLowerCase() === me.toLowerCase());
  let isAuctionSeller = $derived(!!me && !!auction && auction.seller.toLowerCase() === me.toLowerCase());
  let auctionLive = $derived(!!auction && auction.status === 'active' && new Date(auction.ends_at).getTime() > now);
  let auctionEnded = $derived(!!auction && auction.status === 'active' && new Date(auction.ends_at).getTime() <= now);
  let isAuctionWinner = $derived(!!me && !!auction && auction.highest_bidder?.toLowerCase() === me.toLowerCase());
  // forceCancel() (seller/winner/keeper) unlocks 3 days after endsAt while
  // still unsettled — escrow recovery only, the NFT stays put.
  let canForceCancel = $derived(auctionEnded && !!auction && forceCancelUnlocked(new Date(auction!.ends_at).getTime() / 1000, now));
  let myOffer = $derived(me ? offers.find((o) => o.bidder.toLowerCase() === me!.toLowerCase() && o.status === 'active') ?? null : null);
  let liveOffers = $derived(offers.filter((o) => o.status === 'active' && new Date(o.expires_at).getTime() > now));
  let minBid = $derived(auction ? minimumTopUp({ currentHighestWei: BigInt(auction.highest_bid_wei || '0'), reserveWei: BigInt(auction.reserve_price_wei || '0'), myCumulativeWei: myCumWei, minIncrementBps: auction.min_increment_bps }) : 0n);
  // Outbid: escrow on this auction but not the current leader → Withdraw here (spec).
  let outbid = $derived(!!auction && myCumWei > 0n && !isAuctionWinner);
  // Expired offers stay on the page with "Get refund" — never filtered out (audit item).
  let expiredOffers = $derived(offers.filter((o) => (o.status === 'active' || o.status === 'expired') && new Date(o.expires_at).getTime() <= now));
  // Edition chip (spec): "1 of 1" or "Multi-edition · you hold n".
  let editionChip = $derived(std === 'erc1155' ? `Multi-edition${myBalance1155 > 0n ? ` · you hold ${myBalance1155}` : ''}` : '1 of 1');
  // Status × role for the matrix (sticky bar + tests share actionZone()).
  let tokenStatus: TokenStatus = $derived(auctionLive ? 'auction-live' : auctionEnded ? 'auction-ended' : listing ? 'listed' : 'not-listed');
  let tokenRole: TokenRole = $derived(!me ? 'viewer' : (isOwner || isSeller || isAuctionSeller) ? 'seller' : 'buyer');
  let primaryCell = $derived(actionZone({
    status: tokenStatus, role: tokenRole, browseOnly: !canTrade, offersEligible: offerEligible,
    priceLabel: listing ? `${fmtPrice(listing.price_wei)} ${sym}` : undefined,
    hasBids: !!auction && BigInt(auction?.highest_bid_wei || '0') > 0n,
    canForceCancel, outbid, isWinner: isAuctionWinner, hasOwnOffer: !!myOffer,
  })[0]);

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
    } catch { /* unknown owner: fall back to the indexed owner below */ }
    if (!owner && tokenDetail?.owner) owner = tokenDetail.owner;
    // Live profile tag for the owner badge — never cached client-side.
    if (owner) {
      const prof = await j<{ tag?: string }>(`/api/v1/profile/${owner.toLowerCase()}`);
      ownerTag = prof?.tag || '';
    } else {
      ownerTag = '';
    }
    // Collection contract owner (ERC-173), independent of token ownership and
    // of standard — works for ERC-1155 too since it is just a selector call.
    // Falls back to the indexer's creator_addr when the contract has no
    // owner() (or the read fails), which is the deployer in every collection
    // this marketplace has minted.
    if (collOwner === null) {
      try {
        const pub = await publicClient();
        collOwner = (await pub.readContract({ address: coll as Address, abi: erc721Abi, functionName: 'owner' })) as string;
      } catch { collOwner = creatorAddr || null; }
    }
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
  function withTimeout<T>(p: Promise<T>, ms: number, fallback: T): Promise<T> {
    // A dead or blackholed RPC must never hang the page's not-found decision
    // (found by the CI e2e run: the 404 state waited on the fallback forever).
    return Promise.race([p, new Promise<T>((res) => setTimeout(() => res(fallback), ms))]);
  }
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
    const [l, c, t, a, o, au, td] = await Promise.all([
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
      // Indexed owner + last sale; a 404 here with a KNOWN collection is the
      // "Token #N doesn't exist in this collection" state (spec).
      j<TokenDetail>(`/api/v1/token/${coll}/${tid}`),
    ]);
    tokenDetail = td;
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
      if (!onchain) onchain = await withTimeout(loadOnChainFallback().catch(() => false), 6000, false);
      if (!onchain) error = "We don't know this NFT yet. If it was just minted or transferred, it will appear here within a few minutes.";
    } else {
      onchain = false;
    }
    // The collection IS tracked but this token id has never been indexed,
    // isn't listed, isn't auctioned and can't be read on-chain → the spec's
    // "Token #N doesn't exist in this collection" 404 state.
    unknownToken = false;
    if (c && !l && !auction && !td) {
      if (!onchain) onchain = await withTimeout(loadOnChainFallback().catch(() => false), 6000, false);
      unknownToken = !onchain;
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
  function parsePrice(): string | null {
    try { const w = toWei(priceIn); if (w < 10n ** 18n) { formErr = `Minimum is 1 ${sym}.`; return null; } return w.toString(); } catch { formErr = 'Enter a number like 12.5'; return null; }
  }

  const doBuy = () => listing && act(() => MW.buy({ nft: coll, tokenId: tid, seller: listing!.seller, priceWei: listing!.price_wei, name }), 'Just bought · syncing');
  const doList = () => { const p = parsePrice(); const q = p && parseQty(); if (p && q) act(() => MW.list({ nft: coll, tokenId: tid, priceWei: p, duration, std, amount: q, name }), 'Listed · syncing'); };
  const doEdit = () => { const p = parsePrice(); if (p) act(() => MW.editPrice({ nft: coll, tokenId: tid, newPriceWei: p, name }), 'Price updated · syncing'); };
  const doCancel = () => act(() => MW.cancelListing({ nft: coll, tokenId: tid, name }), 'Listing cancelled · syncing');
  const doAuction = () => { const p = parsePrice(); const q = p && parseQty(); if (p && q) act(() => MW.createAuction({ nft: coll, tokenId: tid, reserveWei: p, duration, std, amount: q, name }), 'Auction created · syncing'); };
  const doBid = () => { if (!auction) return; let w: bigint; try { w = toWei(bidIn); } catch { formErr = 'Enter a number like 12.5'; return; } if (w < minBid) { formErr = `Minimum bid is ${fmtPrice(minBid)} ${sym}.`; return; } act(() => MW.bid({ auctionId: String(auction!.auction_id), amountWei: w.toString(), name, myCumulativeWei: myCumWei.toString() }), 'Bid placed · syncing'); };
  const doSettle = () => auction && act(() => MW.settle({ auctionId: String(auction!.auction_id), name }), 'Settled · syncing');
  const doCancelAuction = () => auction && act(() => MW.cancelAuction({ auctionId: String(auction!.auction_id), name }), 'Auction cancelled · syncing');
  const doForceCancel = () => auction && act(() => MW.forceCancel({ auctionId: String(auction!.auction_id), name }), 'Force-cancelled · refunds unlocked · syncing');
  const doOffer = () => { const p = parsePrice(); const q = p && parseQty(); if (p && q) act(() => MW.makeOffer({ nft: coll, tokenId: tid, principalWei: p, duration, std, units: q, name }), 'Offer placed · syncing'); };
  const doAccept = (o: Offer) => act(() => MW.acceptOffer({ nft: coll, tokenId: tid, bidder: o.bidder, principalWei: o.amount_wei, std, name }), 'Offer accepted · syncing');
  const doReject = (o: Offer) => act(() => MW.rejectOffer({ nft: coll, tokenId: tid, bidder: o.bidder, name }), 'Offer declined · syncing');
  const doCancelOffer = () => act(() => MW.cancelOffer({ nft: coll, tokenId: tid, name }), 'Offer cancelled · syncing');
  const doEnableOffers = () => act(() => MW.setOfferEligible({ nft: coll, eligible: true, name: collection?.name }), 'Offers enabled · syncing');
  // Withdraw a losing/outbid escrow right here on the token page (spec).
  const doWithdrawBid = () => auction && act(() => MW.withdrawLoserFunds({ auctionId: String(auction!.auction_id), amountWei: myCumWei.toString() }), 'Bid withdrawn · syncing');
  // Expired-offer refunds live on the token page too (audit item).
  const doRefundExpired = (o: Offer) => act(() => MW.refundExpiredOffer({ nft: coll, tokenId: tid, bidder: o.bidder }), 'Refunded · syncing');
  const connectWallet = () => { MW.connect().catch(() => {}); };
  // "Refresh metadata": re-read on-chain metadata + the API in place.
  async function refreshMetadata() {
    refreshingMeta = true;
    imageError = false;
    onchain = false; ocImg = ''; ocName = '';
    await load();
    if (!img) await loadOnChainFallback().then((ok) => (onchain = onchain || ok));
    refreshingMeta = false;
  }

  const openPanel = (p: typeof panel) => { panel = panel === p ? 'none' : p; formErr = ''; priceIn = ''; };

  /** Activity in words (spec): "Listed for 10", "Sold for 12.5", time + tx link. */
  function actWords(a: Activity): string {
    const amt = a.amountWei && a.amountWei !== '0' ? `${fmtPrice(a.amountWei)} ${sym}` : '';
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

  /** Mobile sticky-bar primary action, from the same matrix as everything else. */
  function stickyAction() {
    switch (primaryCell?.kind) {
      case 'buy': return doBuy();
      case 'buy-connect': case 'bid-connect': case 'offer-connect': return connectWallet();
      case 'list': return openPanel('list');
      case 'edit-price': return openPanel('edit');
      case 'make-offer': case 'raise-offer': return openPanel('offer');
      case 'bid': return document.getElementById('bid-in')?.focus();
      case 'settle': return doSettle();
      case 'cancel-auction': return doCancelAuction();
      case 'cancel-listing': return doCancel();
      default: return;
    }
  }
</script>

{#if loading}
  <div class="tp-grid">
    <Skeleton square r="20px" />
    <div style="display:flex;flex-direction:column;gap:12px"><Skeleton w="40%" h="14px" /><Skeleton w="70%" h="28px" /><Skeleton w="50%" h="34px" /><Skeleton h="48px" r="12px" /><Skeleton h="48px" r="12px" /></div>
  </div>
{:else if unknownToken}
  <EmptyState
    title={`Token #${tid} doesn't exist in this collection`}
    body="Check the token id, or browse the collection's items."
    icon="image"
    cta={{ label: 'Browse the collection', href: `/collection/${coll}` }} />
{:else if error && !collection && !listing && !auction}
  <ErrorState message={error} retry={() => load(true)} />
{:else}
  <div class="tp-grid">
    <div class="tp-media">
      {#if img && !imageError}
        <img src={img} alt={name} loading="eager" onerror={() => (imageError = true)} />
      {:else}
        <div class="tp-nometa">
          <p>No image in metadata</p>
          <button class="btn btn-secondary" disabled={refreshingMeta} onclick={() => void refreshMetadata()}>{refreshingMeta ? 'Refreshing…' : 'Refresh metadata'}</button>
        </div>
      {/if}
    </div>

    <div class="tp-side">
      <div class="tp-coll">
        <a href={`/collection/${coll}`}>{collection?.name || shortAddr(coll)}</a>
        <VerifiedBadge {verified} showUnverified={true} network={chain.name} collectionName={collection?.name || ''} {creatorAddr} />
        {#if live}<span class="tp-live" title="Live updates connected">● live</span>{/if}
      </div>
      <div class="tp-titlerow">
        <h1 class="tp-title">{name}</h1>
        <span class="tp-edition" title={std === 'erc1155' ? 'Several copies of this token exist' : 'Only one copy of this token exists'}>{editionChip}</span>
      </div>
      {#if owner}
        <div class="tp-meta">
          Owned by <a href={`/profile/${owner}`} class="mono">{shortAddr(owner)}</a>{#if isOwner}&nbsp;(you){/if}
          <span class="vb is-holder sm" title={HOLDER_BADGE_TIP}>{ownerTag || holderBadgeName(owner)}</span>
        </div>
      {/if}
      <div class="tp-meta mono">
        <a href={explorerAddress(coll)} target="_blank" rel="noopener">{shortAddr(coll)}</a> · #{tid} · {std.toUpperCase()}
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
            {#if me}
              <div class="tp-form">
                <label class="tp-label" for="bid-in">Your bid ({sym}) · min {fmtPrice(minBid)}</label>
                <div class="tp-inrow"><input id="bid-in" class="tp-input mono" inputmode="decimal" placeholder={fmtPrice(minBid)} bind:value={bidIn} /><button class="btn p" onclick={doBid}>Place bid</button></div>
              </div>
            {:else}
              <button class="btn p" onclick={connectWallet}>Connect to bid</button>
            {/if}
            {#if outbid}
              <button class="btn g" onclick={doWithdrawBid}>Withdraw your bid · {fmtPrice(myCumWei)} {sym}</button>
              <p class="tp-hint">You've been outbid. Your escrowed {fmtPrice(myCumWei)} {sym} is fully refundable right now.</p>
            {/if}
          {:else if canTrade && auctionEnded}
            {#if isAuctionSeller || isAuctionWinner}
              <button class="btn p" onclick={doSettle}>Settle now</button>
              <p class="tp-hint">The marketplace settles this automatically within seconds; you can also settle it yourself.</p>
              {#if canForceCancel}
                <button class="btn g" onclick={doForceCancel}>Force-cancel &amp; refund</button>
                <p class="tp-hint">Settlement has been stuck for 3+ days. Force-cancel closes the auction without a trade: every bid becomes refundable and the NFT stays where it is.</p>
              {:else}
                <p class="tp-hint">The keeper settles automatically; if it can't, force-cancel unlocks after 3 days.</p>
              {/if}
            {:else}
              <p class="tp-hint">Auction ended — settling automatically. NFT to the winner, seller paid minus 2%.</p>
              {#if outbid}
                <button class="btn g" onclick={doWithdrawBid}>Withdraw your bid · {fmtPrice(myCumWei)} {sym}</button>
              {/if}
            {/if}
          {:else if canTrade && isAuctionSeller && BigInt(auction.highest_bid_wei || '0') === 0n}
            <button class="btn g" onclick={doCancelAuction}>Cancel auction</button>
          {:else if canTrade && isAuctionSeller}
            <div class="tp-btnrow">
              <button class="btn g" disabled aria-disabled="true">Cancel auction</button>
              <Hint text="An auction with bids cannot be cancelled — it settles when it ends." label="Why can't I cancel?" />
            </div>
          {/if}
        </section>
      {:else if listing}
        <section class="tp-card is-gold" aria-labelledby="ls-h">
          <div class="tp-card-head"><span id="ls-h">Listed for sale</span><span>expires {timeAgo(listing.expires_at).replace(' ago', '')} from now</span></div>
          <div class="tp-price mono">{fmtPrice(listing.price_wei)} <small>{sym}</small></div>
          <div class="tp-sub">Seller {isSeller ? 'you' : shortAddr(listing.seller)} · 2% fee paid by the seller</div>
          {#if isSeller}
            {#if canTrade}<div class="tp-btnrow"><button class="btn g" onclick={() => openPanel('edit')}>Change price</button><button class="btn g" onclick={doCancel}>Cancel listing</button></div>{/if}
          {:else if canTrade}
            <button class="btn p" onclick={me ? doBuy : connectWallet}>{me ? `Buy now · ${fmtPrice(listing.price_wei)} ${sym}` : `Connect to buy · ${fmtPrice(listing.price_wei)} ${sym}`}</button>
            <p class="tp-hint">You pay exactly this price. Seller pays the 2% fee.</p>
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
          <button class="btn p" onclick={() => openPanel('list')}>List for sale · free</button>
          <button class="btn v" onclick={() => openPanel('auction')}>Start auction · free</button>
        </div>
      {/if}
      {#if canTrade && !isOwner && !isSeller}
        <div class="tp-btnrow">
          {#if !me}
            {#if offerEligible === false}
              <button class="btn gold" disabled aria-disabled="true">Connect wallet to make an offer</button>
              <Hint text="Offers are off for this collection" label="Why is this disabled?" />
            {:else}
              <button class="btn gold" onclick={connectWallet}>Connect wallet to make an offer</button>
            {/if}
          {:else if myOffer}
            <button class="btn gold" onclick={() => openPanel('offer')}>Raise offer ({fmtPrice(myOffer.amount_wei)} {sym})</button>
            <button class="btn g" onclick={doCancelOffer}>Withdraw my offer</button>
          {:else if offerEligible === false}
            <button class="btn gold" disabled aria-disabled="true">Make offer</button>
            <Hint text="Offers are off for this collection. The collection owner can enable them." label="Why is this disabled?" />
          {:else}
            <button class="btn gold" onclick={() => openPanel('offer')}>Make offer</button>
          {/if}
        </div>
      {/if}
      {#if canTrade && offerEligible === false && isCollOwner}
        <!-- Shown ONLY to the collection contract's ERC-173 owner. Gating on
             token ownership here (as this once did) put a guaranteed-revert
             "Only the owner can do that" in front of every holder. -->
        <button class="btn g" onclick={doEnableOffers}>Enable offers for this collection</button>
        <p class="tp-hint">You own this collection's contract, so you can switch offers on for everyone.</p>
      {:else if canTrade && offerEligible === false && isOwner}
        <p class="tp-hint">Offers are off for this collection. Only the collection contract's owner can enable them — listing and auctions are unaffected.</p>
      {/if}

      <!-- ── inline forms ──────────────────────────────────────────── -->
      {#if canTrade && panel !== 'none'}
        <section class="tp-panel" aria-label="Action form">
          <label class="tp-label" for="price-in">
            {panel === 'list' ? `Price (${sym})` : panel === 'edit' ? `New price (${sym})` : panel === 'auction' ? `Reserve price (${sym})` : `Your offer (${sym})`}
          </label>
          <input id="price-in" class="tp-input mono" inputmode="decimal" placeholder={`Min 1 ${sym}`} bind:value={priceIn} />
          {#if panel === 'list'}<p class="tp-hint">You receive 98% when it sells.</p>{/if}
          {#if std === 'erc1155' && panel !== 'edit'}
            <label class="tp-label" for="qty-in">{panel === 'offer' ? 'Units wanted' : 'Units to sell'}{myBalance1155 > 0n && panel !== 'offer' ? ` (you hold ${myBalance1155})` : ''}</label>
            <input id="qty-in" class="tp-input mono" inputmode="numeric" placeholder="1" bind:value={qtyIn} />
          {/if}
          {#if panel === 'auction'}
            <p class="tp-hint">Bids raise the lead by at least 1 {sym} — the same rule for every auction.</p>
          {/if}
          <!-- Raising an existing offer keeps its expiry: no DurationPicker (audit). -->
          {#if panel !== 'edit' && !(panel === 'offer' && myOffer)}<DurationPicker bind:value={duration} label={panel === 'auction' ? 'Auction length' : 'Valid for'} />{/if}
          {#if formErr}<div class="tp-formerr" role="alert">{formErr}</div>{/if}
          <div class="tp-btnrow">
            {#if panel === 'list'}<button class="btn p" onclick={doList}>List · free</button>
            {:else if panel === 'edit'}<button class="btn p" onclick={doEdit}>Update price</button>
            {:else if panel === 'auction'}<button class="btn v" onclick={doAuction}>Start auction · free</button>
            {:else}<button class="btn gold" onclick={doOffer}>Place offer</button>{/if}
            <button class="btn g" onclick={() => (panel = 'none')}>Close</button>
          </div>
          <p class="tp-hint">{panel === 'offer' ? (myOffer ? 'Changing an offer replaces the amount on-chain — the original expiry keeps counting down (it is not extended).' : 'Your offer amount is held in escrow and fully refundable until it expires.') : 'Listing is free; the 2% fee is taken from the sale only.'}</p>
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
                {#if isOwner}
                  <span class="tp-btnrow"><button class="btn p sm" onclick={() => doAccept(o)}>Accept</button><button class="btn g sm" onclick={() => doReject(o)}>Decline</button></span>
                {:else if me && o.bidder.toLowerCase() === me.toLowerCase()}
                  <span class="tp-btnrow"><button class="btn gold sm" onclick={() => openPanel('offer')}>Raise</button><button class="btn g sm" onclick={doCancelOffer}>Withdraw</button></span>
                {/if}
              </li>
            {/each}
          </ul>
        {/if}
        {#if expiredOffers.length}
          <!-- Expired offers stay visible with their refund path (audit item). -->
          <h3 class="tp-subhead">Expired offers</h3>
          <ul class="tp-list">
            {#each expiredOffers as o (o.offer_id)}
              <li class="tp-expired">
                <span class="mono">{fmtPrice(o.amount_wei)} {sym}</span>
                <span class="tp-dim">{me && o.bidder.toLowerCase() === me.toLowerCase() ? 'you' : shortAddr(o.bidder)} · expired {timeAgo(o.expires_at)}</span>
                {#if me && o.bidder.toLowerCase() === me.toLowerCase()}
                  <button class="btn p sm" onclick={() => doRefundExpired(o)}>Get refund</button>
                {:else if isOwner}
                  <button class="btn g sm" onclick={() => doRefundExpired(o)}>Return funds</button>
                {/if}
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
          <!-- Rows in words (spec): "Listed for 10" · time · tx link. -->
          <li><span class="tp-act">{actWords(a)}</span><span class="tp-dim">{timeAgo(a.timestamp)}</span><a class="tp-dim" href={MW.explorerTx(a.txHash)} target="_blank" rel="noopener" aria-label="View transaction in the explorer">↗</a></li>
        {/each}
      </ul>
    {/if}
  </section>

  <!-- Mobile sticky action bar (spec): the matrix's primary action, 48px,
       pinned above the tab bar. Hidden on desktop. -->
  {#if canTrade && primaryCell && primaryCell.kind !== 'browse-only'}
    <div class="tp-sticky" data-testid="sticky-bar">
      <button class="btn p tp-sticky-btn" disabled={primaryCell.disabled} aria-disabled={primaryCell.disabled ? 'true' : undefined}
              title={primaryCell.disabled ? primaryCell.reason : undefined} onclick={() => stickyAction()}>
        {primaryCell.label}
      </button>
    </div>
  {/if}
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
  .tp-titlerow { display: flex; align-items: center; gap: 10px; flex-wrap: wrap; }
  .tp-edition { padding: 3px 10px; border-radius: 999px; background: rgba(255,255,255,.08); border: 1px solid rgba(255,255,255,.14); font-size: 11px; font-weight: 800; color: rgba(255,255,255,.65); white-space: nowrap; }
  .tp-nometa { display: flex; flex-direction: column; align-items: center; gap: 12px; color: rgba(255,255,255,.5); font-size: 14px; padding: 24px; text-align: center; }
  .tp-nometa p { margin: 0; }
  .tp-subhead { font-size: 12px; font-weight: 800; text-transform: uppercase; letter-spacing: .06em; color: rgba(255,255,255,.4); margin: 14px 0 8px; }
  .tp-expired { opacity: .75; }
  .tp-act { font-weight: 600; }
  .tp-sticky { display: none; }
  @media (max-width: 767px) {
    .tp-sticky { display: block; position: fixed; left: 0; right: 0; bottom: calc(var(--tabbar-h, 56px) + env(safe-area-inset-bottom)); z-index: var(--z-banner, 40); padding: 8px 12px; background: rgba(9,9,11,.92); backdrop-filter: blur(8px); border-top: 1px solid rgba(255,255,255,.1); }
    .tp-sticky-btn { width: 100%; min-height: 48px; }
  }
  @media (prefers-reduced-motion: reduce) { .tp-spin { animation: none; } }
</style>
