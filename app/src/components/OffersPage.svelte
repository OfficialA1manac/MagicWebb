<script lang="ts">
  // Offers: "Received" (on NFTs you own: accept / decline) and "Made"
  // (your escrowed offers: cancel before expiry, refund after).
  import { onMount } from 'svelte';
  import EmptyState from './EmptyState.svelte';
  import ErrorState from './ErrorState.svelte';
  import Skeleton from './Skeleton.svelte';
  import { MW } from '../lib/mw';
  import { tradingLive, readOnlyCopy } from '../lib/chains';

  const live = tradingLive();
  const ro = readOnlyCopy();
  import { ws } from '../lib/ws/client';
  import { userChannel } from '../lib/ws/channels';
  import { currentChain } from '../lib/chains';
  import { fmtPrice, shortAddr, fmtCountdown, timeAgo } from '../lib/format';
  import { onAccountChange } from '../lib/tx/client';

  type Offer = { offer_id: string; bidder: string; collection: string; token_id: string; amount_wei: string; units: number; standard: string; expires_at: string; status: string; created_at: string; name?: string; image_uri?: string };

  let me = $state<string | null>(null);
  let tab = $state<'received' | 'made'>('received');
  let loading = $state(false);
  let error = $state('');
  let made = $state<Offer[]>([]);
  let received = $state<Offer[]>([]);
  let now = $state(Date.now());
  let syncing = $state('');
  const sym = currentChain().currency;

  async function j<T>(u: string): Promise<T | null> { try { const r = await fetch(u); return r.ok ? (await r.json()) as T : null; } catch { return null; } }
  async function load() {
    if (!me) return;
    loading = made.length === 0 && received.length === 0; error = '';
    const [m, r] = await Promise.all([j<Offer[]>(`/api/v1/offers?bidder=${me}&limit=100`), j<Offer[]>(`/api/v1/offers?owner=${me}&limit=100`)]);
    if (!m && !r) error = 'Could not load offers.';
    made = m ?? []; received = r ?? []; loading = false; syncing = '';
  }
  onMount(() => {
    const offAcct = onAccountChange((a) => { const changed = a.address !== me; me = a.address; if (me) { ws.subscribe(userChannel(me)); if (changed) void load(); } });
    const offWs = ws.on('offer-updated', () => void load());
    const tick = setInterval(() => (now = Date.now()), 1000);
    if (location.hash === '#made') tab = 'made';
    return () => { offAcct(); offWs(); clearInterval(tick); };
  });
  const expired = (o: Offer) => new Date(o.expires_at).getTime() <= now;
  const active = (o: Offer) => o.status === 'active';
  async function act(run: () => Promise<unknown>, label: string) { try { await run(); syncing = label; setTimeout(load, 1500); setTimeout(load, 6000); } catch { /* modal */ } }
  const label = (o: Offer) => o.name || `#${o.token_id}`;
  let list = $derived((tab === 'received' ? received : made).filter(active));
</script>

{#if !live}
  <EmptyState title={ro.heading} body={ro.body} cta={ro.ctaHref ? { label: ro.cta, href: ro.ctaHref } : undefined} />
{:else if !me}
  <EmptyState title="Connect your wallet to see offers" body="Offers you make are held in escrow and fully refundable. Offers on NFTs you own show up here for you to accept or decline." cta={{ label: 'Connect wallet', onclick: () => MW.connect().catch(() => {}) }} />
{:else}
  <div class="op-tabs" role="tablist">
    <button role="tab" aria-selected={tab === 'received'} class:is-on={tab === 'received'} onclick={() => (tab = 'received')}>Received <span class="op-n">{received.filter(active).length}</span></button>
    <button role="tab" aria-selected={tab === 'made'} class:is-on={tab === 'made'} onclick={() => (tab = 'made')}>Made <span class="op-n">{made.filter(active).length}</span></button>
  </div>
  {#if syncing}<div class="op-sync" role="status">{syncing}</div>{/if}
  {#if loading}
    <div class="op-list"><Skeleton h="64px" r="14px" /><Skeleton h="64px" r="14px" /><Skeleton h="64px" r="14px" /></div>
  {:else if error}
    <ErrorState message={error} retry={load} />
  {:else if list.length === 0}
    {#if tab === 'received'}
      <EmptyState title="No offers on your NFTs yet" body="When someone makes an offer on something you own, it appears here with Accept and Decline." cta={{ label: 'Browse listings', href: '/listings' }} />
    {:else}
      <EmptyState title="You have not made any offers" body="Find an NFT you want and tap Make offer on its page. Your funds stay refundable until you are accepted or the offer expires." cta={{ label: 'Find an NFT', href: '/search' }} />
    {/if}
  {:else}
    <ul class="op-list">
      {#each list as o (o.offer_id)}
        <li class="op-row" class:is-expired={expired(o)}>
          <a class="op-thumb" href={`/token/${o.collection}/${o.token_id}`} aria-label={label(o)}>{#if o.image_uri}<img src={o.image_uri.startsWith('/api') || o.image_uri.startsWith('data:') ? o.image_uri : `/api/v1/media?url=${encodeURIComponent(o.image_uri)}&id=${o.token_id}`} alt="" />{/if}</a>
          <div class="op-main">
            <a class="op-name" href={`/token/${o.collection}/${o.token_id}`}>{label(o)}</a>
            <div class="op-dim">{tab === 'received' ? `from ${shortAddr(o.bidder)}` : `on ${shortAddr(o.collection)}`} · {expired(o) ? 'expired ' + timeAgo(o.expires_at) : 'expires in ' + fmtCountdown(new Date(o.expires_at).getTime() / 1000, now)}</div>
          </div>
          <div class="op-amt mono">{fmtPrice(o.amount_wei)} {sym}</div>
          <div class="op-acts">
            {#if tab === 'received'}
              {#if !expired(o)}
                <button class="btn p" onclick={() => act(() => MW.acceptOffer({ nft: o.collection, tokenId: o.token_id, bidder: o.bidder, principalWei: o.amount_wei, std: o.standard as 'erc721' | 'erc1155', name: label(o) }), 'Accepted · syncing')}>Accept</button>
                <button class="btn g" onclick={() => act(() => MW.rejectOffer({ nft: o.collection, tokenId: o.token_id, bidder: o.bidder, name: label(o) }), 'Declined · syncing')}>Decline</button>
              {:else}
                <button class="btn g" onclick={() => act(() => MW.refundExpiredOffer({ nft: o.collection, tokenId: o.token_id, bidder: o.bidder }), 'Refunded · syncing')}>Refund bidder</button>
              {/if}
            {:else if !expired(o)}
              <button class="btn g" onclick={() => act(() => MW.cancelOffer({ nft: o.collection, tokenId: o.token_id, name: label(o) }), 'Cancelled · syncing')}>Cancel · full refund</button>
            {:else}
              <button class="btn p" onclick={() => act(() => MW.refundExpiredOffer({ nft: o.collection, tokenId: o.token_id, bidder: o.bidder }), 'Refunded · syncing')}>Get refund</button>
            {/if}
          </div>
        </li>
      {/each}
    </ul>
  {/if}
{/if}

<style>
  .op-tabs { display: flex; gap: 6px; margin-bottom: 14px; }
  .op-tabs button { min-height: 44px; padding: 0 16px; border-radius: 12px; background: rgba(255,255,255,.05); border: 1px solid rgba(255,255,255,.12); color: rgba(255,255,255,.7); font-weight: 700; font-family: inherit; cursor: pointer; display: inline-flex; gap: 8px; align-items: center; }
  .op-tabs button.is-on { background: rgba(252,211,77,.14); border-color: #fcd34d; color: #fde68a; }
  .op-n { font-size: 11px; background: rgba(255,255,255,.1); border-radius: 999px; padding: 2px 8px; }
  .op-sync { padding: 8px 12px; border-radius: 999px; background: rgba(74,222,128,.1); border: 1px solid rgba(74,222,128,.35); color: #bbf7d0; font-size: 13px; font-weight: 600; display: inline-block; margin-bottom: 12px; }
  .op-list { list-style: none; margin: 0; padding: 0; display: flex; flex-direction: column; gap: 8px; }
  .op-row { display: grid; grid-template-columns: 56px 1fr auto; grid-template-areas: "thumb main amt" "acts acts acts"; gap: 8px 12px; align-items: center; padding: 12px; border-radius: 14px; background: rgba(15,15,19,.6); border: 1px solid rgba(255,255,255,.07); }
  @media (min-width: 768px) { .op-row { grid-template-columns: 56px 1fr auto auto; grid-template-areas: "thumb main amt acts"; } }
  .op-row.is-expired { opacity: .75; }
  .op-thumb { grid-area: thumb; width: 56px; height: 56px; border-radius: 10px; background: #1a1a24; overflow: hidden; display: block; } .op-thumb img { width: 100%; height: 100%; object-fit: cover; }
  .op-main { grid-area: main; min-width: 0; } .op-name { color: #fafafa; font-weight: 700; text-decoration: none; font-size: 14px; overflow-wrap: anywhere; }
  .op-dim { font-size: 12px; color: rgba(255,255,255,.5); }
  .op-amt { grid-area: amt; font-weight: 600; font-size: 15px; white-space: nowrap; }
  .op-acts { grid-area: acts; display: flex; gap: 8px; flex-wrap: wrap; }
  .mono { font-family: 'JetBrains Mono', ui-monospace, monospace; }
  .btn { min-height: 40px; padding: 0 14px; border-radius: 10px; font-weight: 700; font-size: 13px; border: 1px solid transparent; cursor: pointer; font-family: inherit; flex: 1 1 auto; }
  .btn.p { background: linear-gradient(135deg,#7dd3fc,#0ea5e9); color: #09090b; } .btn.g { background: transparent; color: #fafafa; border-color: rgba(255,255,255,.16); }
  .btn:focus-visible { outline: 2px solid #7dd3fc; outline-offset: 2px; }
</style>
