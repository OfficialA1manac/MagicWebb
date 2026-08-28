<script lang="ts">
  // Auction detail: live countdown, cumulative-bid aware bidding, settle,
  // cancel (seller, no bids), withdraw (outbid). WS-driven refresh.
  import { onMount } from 'svelte';
  import VerifiedBadge from './VerifiedBadge.svelte';
  import EmptyState from './EmptyState.svelte';
  import ErrorState from './ErrorState.svelte';
  import Skeleton from './Skeleton.svelte';
  import { MW } from '../lib/mw';
  import { ws } from '../lib/ws/client';
  import { tokenChannel } from '../lib/ws/channels';
  import { currentChain, tradingLive, readOnlyCopy } from '../lib/chains';
  import { fmtPrice, shortAddr, timeAgo, fmtCountdown, toWei } from '../lib/format';
  import { resolveImageUri } from '../lib/image-uri';
  import { onAccountChange } from '../lib/tx/client';
  import { minimumTopUp } from '../lib/tx/auction';

  type Auction = { auction_id: number; collection: string; token_id: string; seller: string; standard: string; reserve_price_wei: string; highest_bid_wei: string; highest_bidder: string; min_increment_bps: number; starts_at: string; ends_at: string; status: string; create_tx: string; name: string; image_uri: string; collection_verified: boolean };
  type Bid = { bidder: string; amount_wei: string; tx_hash: string; placed_at: string };

  let id = $state('');
  let loading = $state(true);
  let error = $state('');
  let a = $state<Auction | null>(null);
  let bids = $state<Bid[]>([]);
  let me = $state<string | null>(null);
  let live = $state(false);
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
  let isSeller = $derived(!!me && !!a && a.seller.toLowerCase() === me.toLowerCase());
  let highest = $derived(BigInt(a?.highest_bid_wei || '0'));
  let myCumulative = $derived(me ? bids.filter((b) => b.bidder.toLowerCase() === me!.toLowerCase()).reduce((s, b) => s + BigInt(b.amount_wei), 0n) : 0n);
  let amLeader = $derived(!!me && !!a && a.highest_bidder?.toLowerCase() === me.toLowerCase());
  let minTopUp = $derived(a ? minimumTopUp({ currentHighestWei: highest, reserveWei: BigInt(a.reserve_price_wei || '0'), myCumulativeWei: myCumulative }) : 0n);
  let name = $derived(a?.name || `#${a?.token_id ?? ''}`);
  let img = $derived(resolveImageUri(a?.image_uri, a?.token_id));
  let antiSnipe = $derived(isLive && endsMs - now < 3 * 60 * 1000);

  async function j<T>(u: string): Promise<T | null> { try { const r = await fetch(u); return r.ok ? (await r.json()) as T : null; } catch { return null; } }

  async function load(initial = false) {
    if (initial) loading = true;
    const [au, bi] = await Promise.all([j<Auction>(`/api/v1/auctions/${id}`), j<Bid[]>(`/api/v1/auctions/${id}/bids`)]);
    if (!au) { error = 'Auction not found.'; loading = false; return; }
    a = au; bids = (bi ?? []).slice().sort((x, y) => new Date(y.placed_at).getTime() - new Date(x.placed_at).getTime());
    loading = false; syncing = '';
  }

  onMount(() => {
    id = location.pathname.replace(/^\/auction\/?/, '').replace(/\/$/, '');
    if (!/^\d+$/.test(id)) { error = 'Invalid auction URL.'; loading = false; return; }
    void load(true);
    const offAcct = onAccountChange((x) => (me = x.address));
    let ch = '';
    const sub = setInterval(() => { if (a && !ch) { ch = tokenChannel(a.collection, a.token_id); ws.subscribe(ch); } }, 500);
    const offWs = ws.on('*', (_d, meta) => { if (meta.type === 'auction-updated' || meta.type === 'activity' || meta.type === 'tx-indexed') void load(); });
    const offSt = ws.onStatus((s) => (live = s === 'open'));
    const tick = setInterval(() => (now = Date.now()), 1000);
    return () => { offAcct(); offWs(); offSt(); clearInterval(tick); clearInterval(sub); if (ch) ws.unsubscribe(ch); };
  });

  function after(label: string) { syncing = label; formErr = ''; bidIn = ''; setTimeout(() => void load(), 1500); setTimeout(() => void load(), 6000); }
  async function act(run: () => Promise<unknown>, label: string) { try { await run(); after(label); } catch { /* modal showed it */ } }
  const doBid = () => {
    if (!a) return;
    let w: bigint; try { w = toWei(bidIn); } catch { formErr = 'Enter a number like 12.5'; return; }
    if (w < minTopUp) { formErr = `You need to add at least ${fmtPrice(minTopUp)} ${sym} to take the lead.`; return; }
    act(() => MW.bid({ auctionId: String(a!.auction_id), amountWei: w.toString(), name, myCumulativeWei: myCumulative.toString() }), 'Bid placed · syncing');
  };
  const doSettle = () => a && act(() => MW.settle({ auctionId: String(a!.auction_id), name }), 'Settled · syncing');
  const doCancel = () => a && act(() => MW.cancelAuction({ auctionId: String(a!.auction_id), name }), 'Auction cancelled · syncing');
  const doWithdraw = () => a && act(() => MW.withdrawLoserFunds({ auctionId: String(a!.auction_id), amountWei: myCumulative.toString() }), 'Withdrawn · syncing');
</script>

{#if loading}
  <div class="ap-grid"><Skeleton square r="20px" /><div style="display:flex;flex-direction:column;gap:12px"><Skeleton w="40%" /><Skeleton w="70%" h="28px" /><Skeleton h="120px" r="16px" /><Skeleton h="48px" r="12px" /></div></div>
{:else if error || !a}
  <ErrorState message={error || 'Auction not found.'} retry={() => load(true)} />
{:else}
  <div class="ap-grid">
    <a class="ap-media" href={`/token/${a.collection}/${a.token_id}`}>
      {#if img}<img src={img} alt={name} />{:else}<div class="ap-noimg" aria-hidden="true">🖼</div>{/if}
    </a>
    <div class="ap-side">
      <div class="ap-coll"><a href={`/collection/${a.collection}`}>{shortAddr(a.collection)}</a><VerifiedBadge verified={a.collection_verified} network={chain.name} />{#if live}<span class="ap-live">● live</span>{/if}</div>
      <h1 class="ap-title">{name}</h1>
      <div class="ap-meta mono">Auction #{a.auction_id} · seller {isSeller ? 'you' : shortAddr(a.seller)} · <a href={`/token/${a.collection}/${a.token_id}`}>token #{a.token_id}</a></div>
      {#if syncing}<div class="ap-sync" role="status"><span class="ap-spin" aria-hidden="true"></span>{syncing}</div>{/if}

      {#if !canTrade}
        <section class="ap-card" aria-label="Read-only network">
          <div class="ap-head"><span>{roCopy.heading}</span></div>
          <p class="ap-hint">{roCopy.body}</p>
          {#if roCopy.ctaHref}<a class="btn v" href={roCopy.ctaHref}>{roCopy.cta}</a>{/if}
        </section>
      {/if}

      <section class="ap-card" class:is-hot={antiSnipe}>
        <div class="ap-head">
          <span>{isLive ? 'Ends in' : ended ? 'Ended' : a.status}</span>
          <span class="mono ap-count" aria-live="polite">{fmtCountdown(endsMs / 1000, now)}</span>
        </div>
        {#if antiSnipe}<div class="ap-snipe">Final 3 minutes — any new bid extends the auction (up to 30 min total)</div>{/if}
        <div class="ap-price mono">{fmtPrice(highest > 0n ? a.highest_bid_wei : a.reserve_price_wei)} <small>{sym}</small></div>
        <div class="ap-sub">{highest > 0n ? `Highest bid · ${amLeader ? 'you are leading' : shortAddr(a.highest_bidder)}` : 'No bids yet · reserve shown'}</div>

        {#if canTrade && isLive && !isSeller}
          {#if amLeader}
            <div class="ap-ok">You are the highest bidder with {fmtPrice(myCumulative)} {sym}.</div>
          {:else}
            <div class="ap-form">
              <label class="ap-label" for="bid-in">{myCumulative > 0n ? `Add to your bid (you have ${fmtPrice(myCumulative)} ${sym} in)` : `Your bid (${sym})`} · min {fmtPrice(minTopUp)}</label>
              <div class="ap-inrow"><input id="bid-in" class="ap-input mono" inputmode="decimal" placeholder={fmtPrice(minTopUp)} bind:value={bidIn} /><button class="btn v" onclick={doBid}>Place bid</button></div>
              {#if formErr}<div class="ap-err" role="alert">{formErr}</div>{/if}
              <p class="ap-hint">Bids add up: your total is what counts. If you are outbid, every {sym} you sent is refundable.</p>
            </div>
          {/if}
        {:else if canTrade && ended}
          {#if amLeader || isSeller}
            <button class="btn gold" onclick={doSettle}>Settle now</button>
            <p class="ap-hint">The marketplace settles this automatically within seconds; as {amLeader ? 'the winner' : 'the seller'} you can also settle it yourself.</p>
          {:else}
            <p class="ap-hint">Auction ended — settling automatically. NFT to the winner, proceeds (minus 1.5%) to the seller, losing bids refundable.</p>
          {/if}
        {:else if canTrade && isLive && isSeller}
          {#if highest === 0n}<button class="btn g" onclick={doCancel}>Cancel auction</button>{:else}<p class="ap-hint">Your auction has bids and will settle when it ends.</p>{/if}
        {/if}
        {#if canTrade && me && myCumulative > 0n && !amLeader && (isLive || a.status !== 'active')}
          <button class="btn g" onclick={doWithdraw}>Withdraw my {fmtPrice(myCumulative)} {sym}</button>
        {/if}
      </section>

      <section class="ap-section">
        <h2>Bids <span class="ap-n">{bids.length}</span></h2>
        {#if bids.length === 0}
          <EmptyState title="No bids yet" body="Be the first — the reserve is the minimum." />
        {:else}
          <ul class="ap-list">
            {#each bids as b (b.tx_hash)}
              <li><span class="mono">{fmtPrice(b.amount_wei)} {sym}</span><span class="ap-dim">{me && b.bidder.toLowerCase() === me.toLowerCase() ? 'you' : shortAddr(b.bidder)} · {timeAgo(b.placed_at)}</span><a class="ap-dim" href={MW.explorerTx(b.tx_hash)} target="_blank" rel="noopener">↗</a></li>
            {/each}
          </ul>
        {/if}
      </section>
    </div>
  </div>
{/if}

<style>
  .ap-grid { display: grid; grid-template-columns: repeat(auto-fit, minmax(min(100%, 360px), 1fr)); gap: 28px; align-items: start; }
  .ap-media { aspect-ratio: 1; border-radius: 20px; overflow: hidden; background: rgba(9,9,11,.8); border: 1px solid rgba(255,255,255,.08); display: flex; align-items: center; justify-content: center; }
  .ap-media img { width: 100%; height: 100%; object-fit: cover; display: block; } .ap-noimg { font-size: 4rem; color: rgba(255,255,255,.08); }
  .ap-side { display: flex; flex-direction: column; gap: 14px; min-width: 0; }
  .ap-coll { display: flex; gap: 10px; align-items: center; flex-wrap: wrap; font-size: 13px; } .ap-coll a { color: #7dd3fc; text-decoration: none; font-weight: 600; }
  .ap-live { color: #4ade80; font-size: 11px; font-weight: 700; }
  .ap-title { font-size: clamp(1.5rem, 4vw, 2rem); font-weight: 800; margin: 0; line-height: 1.1; overflow-wrap: anywhere; }
  .ap-meta { font-size: 12px; color: rgba(255,255,255,.45); } .ap-meta a { color: inherit; }
  .mono { font-family: 'JetBrains Mono', ui-monospace, monospace; }
  .ap-card { padding: 18px; border-radius: 16px; background: rgba(15,15,19,.7); border: 1px solid rgba(167,139,250,.35); display: flex; flex-direction: column; gap: 10px; }
  .ap-card.is-hot { border-color: #fcd34d; }
  .ap-head { display: flex; justify-content: space-between; gap: 8px; font-size: 11px; text-transform: uppercase; letter-spacing: .08em; font-weight: 700; color: rgba(255,255,255,.5); }
  .ap-count { font-size: 14px; color: #fafafa; text-transform: none; letter-spacing: 0; }
  .ap-snipe { font-size: 12px; color: #fde68a; font-weight: 600; }
  .ap-price { font-size: clamp(1.75rem, 5vw, 2.25rem); font-weight: 600; line-height: 1; } .ap-price small { font-size: .5em; opacity: .7; }
  .ap-sub { font-size: 13px; color: rgba(255,255,255,.55); }
  .ap-ok { font-size: 13px; color: #bbf7d0; background: rgba(74,222,128,.1); border: 1px solid rgba(74,222,128,.3); padding: 10px 12px; border-radius: 12px; }
  .ap-form { display: flex; flex-direction: column; gap: 8px; }
  .ap-label { font-size: 12px; color: rgba(255,255,255,.6); font-weight: 600; }
  .ap-inrow { display: flex; gap: 8px; flex-wrap: wrap; } .ap-inrow .ap-input { flex: 1 1 160px; }
  .ap-input { min-height: 44px; padding: 0 12px; border-radius: 12px; background: rgba(255,255,255,.05); border: 1px solid rgba(255,255,255,.14); color: #fafafa; font-size: 16px; width: 100%; }
  .ap-err { color: #fca5a5; font-size: 13px; }
  .ap-hint { font-size: 12px; color: rgba(255,255,255,.5); margin: 0; line-height: 1.5; }
  .btn { min-height: 44px; padding: 0 16px; border-radius: 12px; font-weight: 700; font-size: 15px; border: 1px solid transparent; cursor: pointer; font-family: inherit; display: inline-flex; align-items: center; justify-content: center; }
  .btn.v { background: linear-gradient(135deg,#a78bfa,#7c3aed); color: #fafafa; } .btn.gold { background: linear-gradient(135deg,#fcd34d,#f59e0b); color: #09090b; } .btn.g { background: transparent; color: #fafafa; border-color: rgba(255,255,255,.16); }
  .btn:focus-visible, .ap-input:focus-visible { outline: 2px solid #7dd3fc; outline-offset: 2px; }
  .ap-sync { display: inline-flex; align-items: center; gap: 8px; padding: 8px 12px; border-radius: 999px; background: rgba(74,222,128,.1); border: 1px solid rgba(74,222,128,.35); color: #bbf7d0; font-size: 13px; font-weight: 600; align-self: flex-start; }
  .ap-spin { width: 12px; height: 12px; border-radius: 50%; border: 2px solid rgba(187,247,208,.4); border-top-color: #4ade80; animation: sp 1s linear infinite; } @keyframes sp { to { transform: rotate(360deg); } }
  .ap-section h2 { font-size: 15px; font-weight: 800; margin: 0 0 10px; display: flex; gap: 8px; align-items: center; } .ap-n { font-size: 11px; background: rgba(255,255,255,.1); border-radius: 999px; padding: 2px 8px; }
  .ap-list { list-style: none; margin: 0; padding: 0; display: flex; flex-direction: column; gap: 6px; }
  .ap-list li { display: flex; align-items: center; gap: 12px; flex-wrap: wrap; padding: 10px 12px; border-radius: 12px; background: rgba(15,15,19,.5); border: 1px solid rgba(255,255,255,.05); font-size: 13px; min-height: 44px; }
  .ap-dim { color: rgba(255,255,255,.45); }
</style>
