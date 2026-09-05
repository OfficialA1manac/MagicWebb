<script module lang="ts">
  // Pure offers matrix (spec B4 "Offers") — exported for the component tests
  // (same pattern as TokenPage.actionZone): tab × expired → actions, plus the
  // sort rules ("Best offer" is the default on Received).
  export type OffersTab = 'received' | 'sent';
  export type OffersSort = 'best' | 'newest' | 'expiring';
  export interface OfferActionCell { kind: string; label: string }

  export function offerActions(tab: OffersTab, expired: boolean): OfferActionCell[] {
    if (tab === 'received') {
      return expired
        ? [{ kind: 'return-funds', label: 'Return their funds' }]
        : [{ kind: 'accept', label: 'Accept' }, { kind: 'decline', label: 'Decline' }];
    }
    return expired
      ? [{ kind: 'get-refund', label: 'Get refund' }]
      : [{ kind: 'raise', label: 'Raise offer' }, { kind: 'withdraw', label: 'Withdraw offer (full refund)' }];
  }

  export function defaultOffersSort(tab: OffersTab): OffersSort {
    return tab === 'received' ? 'best' : 'newest';
  }

  export interface OfferRowLike { amount_wei: string; created_at: string; expires_at: string }

  export function isOfferExpired(o: { expires_at: string }, nowMs: number): boolean {
    return new Date(o.expires_at).getTime() <= nowMs;
  }

  export function sortOfferRows<T extends OfferRowLike>(rows: T[], sort: OffersSort): T[] {
    const out = rows.slice();
    const amt = (o: OfferRowLike) => { try { return BigInt(o.amount_wei || '0'); } catch { return 0n; } };
    switch (sort) {
      case 'best': out.sort((a, b) => (amt(b) > amt(a) ? 1 : amt(b) < amt(a) ? -1 : 0)); break;
      case 'newest': out.sort((a, b) => new Date(b.created_at).getTime() - new Date(a.created_at).getTime()); break;
      case 'expiring': out.sort((a, b) => new Date(a.expires_at).getTime() - new Date(b.expires_at).getTime()); break;
    }
    return out;
  }
</script>

<script lang="ts">
  // Offers (spec B4): viewer sees a teach-first page (two disabled tabs, one
  // explainer row each, a single Connect wallet primary); connected sees
  // "Received (n)" / "Sent (n)" with per-row actions. Expired offers stay in
  // the list with their refund action — never filtered out.
  import { onMount } from 'svelte';
  import VerifiedBadge from './VerifiedBadge.svelte';
  import EmptyState from './EmptyState.svelte';
  import ErrorState from './ErrorState.svelte';
  import Skeleton from './Skeleton.svelte';
  import { MW } from '../lib/mw';
  import { tradingLive, readOnlyCopy, currentChain } from '../lib/chains';
  import { ws } from '../lib/ws/client';
  import { userChannel } from '../lib/ws/channels';
  import { fmtPrice, shortAddr, fmtCountdownShort, timeAgo } from '../lib/format';
  import { resolveImageUri } from '../lib/image-uri';
  import { onAccountChange } from '../lib/tx/client';

  type Offer = { offer_id: string; bidder: string; collection: string; token_id: string; amount_wei: string; units: number; standard: string; expires_at: string; status: string; created_at: string; name?: string; image_uri?: string; collection_verified?: boolean; collection_creator?: string; collection_name?: string; collection_tracked?: boolean };

  const live = tradingLive();
  const ro = readOnlyCopy();
  const sym = currentChain().currency;

  let me = $state<string | null>(null);
  let tab = $state<OffersTab>('received');
  let sort = $state<OffersSort>('best');
  let loading = $state(false);
  let error = $state('');
  let sent = $state<Offer[]>([]);
  let received = $state<Offer[]>([]);
  let now = $state(Date.now());
  let syncing = $state('');

  async function j<T>(u: string): Promise<T | null> { try { const r = await fetch(u); return r.ok ? (await r.json()) as T : null; } catch { return null; } }
  async function load() {
    if (!me) return;
    loading = sent.length === 0 && received.length === 0; error = '';
    const [m, r] = await Promise.all([j<Offer[]>(`/api/v1/offers?bidder=${me}&limit=100`), j<Offer[]>(`/api/v1/offers?owner=${me}&limit=100`)]);
    if (!m && !r) error = 'Could not load offers.';
    sent = m ?? []; received = r ?? []; loading = false; syncing = '';
  }

  onMount(() => {
    let ch = '';
    const offAcct = onAccountChange((a) => {
      const changed = a.address !== me;
      me = a.address;
      const next = me ? userChannel(me) : '';
      if (next !== ch) { if (ch) ws.unsubscribe(ch); if (next) ws.subscribe(next); ch = next; }
      if (me && changed) void load();
    });
    const offWs = ws.on('offer-updated', () => void load());
    const tick = setInterval(() => (now = Date.now()), 1000);
    if (location.hash === '#sent' || location.hash === '#made') setTab('sent');
    return () => { offAcct(); offWs(); clearInterval(tick); if (ch) ws.unsubscribe(ch); };
  });

  function setTab(t: OffersTab) { tab = t; sort = defaultOffersSort(t); }

  const expired = (o: Offer) => isOfferExpired(o, now);
  // "active" status rows past their expiry ARE shown — with the refund action.
  const visible = (o: Offer) => o.status === 'active';
  const label = (o: Offer) => o.name || `#${o.token_id}`;
  const thumb = (o: Offer) => resolveImageUri(o.image_uri, o.token_id, 128);
  let receivedN = $derived(received.filter(visible).length);
  let sentN = $derived(sent.filter(visible).length);
  let list = $derived(sortOfferRows((tab === 'received' ? received : sent).filter(visible), sort));

  async function act(run: () => Promise<unknown>, msg: string) { try { await run(); syncing = msg; setTimeout(load, 1500); setTimeout(load, 6000); } catch { /* modal showed it */ } }
  function runAction(kind: string, o: Offer) {
    switch (kind) {
      case 'accept': void act(() => MW.acceptOffer({ nft: o.collection, tokenId: o.token_id, bidder: o.bidder, principalWei: o.amount_wei, std: o.standard as 'erc721' | 'erc1155', name: label(o) }), 'Accepted · syncing'); break;
      case 'decline': void act(() => MW.rejectOffer({ nft: o.collection, tokenId: o.token_id, bidder: o.bidder, name: label(o) }), 'Declined · syncing'); break;
      case 'return-funds': void act(() => MW.refundExpiredOffer({ nft: o.collection, tokenId: o.token_id, bidder: o.bidder }), 'Funds returned · syncing'); break;
      case 'raise': location.href = `/token/${o.collection}/${o.token_id}#offer`; break;
      case 'withdraw': void act(() => MW.cancelOffer({ nft: o.collection, tokenId: o.token_id, name: label(o) }), 'Withdrawn · full refund · syncing'); break;
      case 'get-refund': void act(() => MW.refundExpiredOffer({ nft: o.collection, tokenId: o.token_id, bidder: o.bidder }), 'Refunded · syncing'); break;
    }
  }
</script>

{#if !live}
  <EmptyState title={ro.heading} body={ro.body} cta={ro.ctaHref ? { label: ro.cta, href: ro.ctaHref } : undefined} />
{:else if !me}
  <!-- Viewer: teach first (spec) — disabled tabs, one explainer row each, one primary. -->
  <div class="op-tabs" role="tablist" aria-label="Offers">
    <button role="tab" aria-selected="true" class="is-on" disabled aria-disabled="true" data-testid="tab-received">Received</button>
    <button role="tab" aria-selected="false" disabled aria-disabled="true" data-testid="tab-sent">Sent</button>
  </div>
  <ul class="op-list op-teach" data-testid="viewer-teach">
    <li class="op-row op-explain">Offers on NFTs you own appear here</li>
    <li class="op-row op-explain">Offers you've made appear here</li>
  </ul>
  <div class="op-connect">
    <button class="btn btn-primary btn-lg" onclick={() => MW.connect().catch(() => {})}>Connect wallet</button>
  </div>
{:else}
  <div class="op-bar">
    <div class="op-tabs" role="tablist" aria-label="Offers">
      <button role="tab" aria-selected={tab === 'received'} class:is-on={tab === 'received'} data-testid="tab-received" onclick={() => setTab('received')}>Received <span class="op-n">{receivedN}</span></button>
      <button role="tab" aria-selected={tab === 'sent'} class:is-on={tab === 'sent'} data-testid="tab-sent" onclick={() => setTab('sent')}>Sent <span class="op-n">{sentN}</span></button>
    </div>
    <label class="op-sort">
      <span>Sort</span>
      <select bind:value={sort} data-testid="offers-sort">
        <option value="best">Best offer</option>
        <option value="newest">Newest</option>
        <option value="expiring">Expiring soon</option>
      </select>
    </label>
  </div>
  {#if syncing}<div class="op-sync" role="status">{syncing}</div>{/if}
  {#if loading}
    <div class="op-list"><Skeleton h="72px" r="14px" /><Skeleton h="72px" r="14px" /><Skeleton h="72px" r="14px" /><Skeleton h="72px" r="14px" /></div>
  {:else if error}
    <ErrorState title="Could not load offers" message="Give it a moment and try again." retry={load} />
  {:else if list.length === 0}
    {#if tab === 'received'}
      <EmptyState title="No offers yet" body="When someone offers on your NFT it appears here." icon="inbox" />
    {:else}
      <EmptyState title="You haven't made any offers" body="Find an NFT you want and tap Make offer on its page. Your funds stay refundable until you are accepted or the offer expires." icon="tag" cta={{ label: 'Browse listings', href: '/listings' }} />
    {/if}
  {:else}
    <ul class="op-list">
      {#each list as o (o.offer_id)}
        <li class="op-row" class:is-expired={expired(o)}>
          <a class="op-thumb" href={`/token/${o.collection}/${o.token_id}`} aria-label={label(o)}>{#if thumb(o)}<img src={thumb(o)} alt={label(o)} loading="lazy" decoding="async" />{/if}</a>
          <div class="op-main">
            <div class="op-title">
              <a class="op-name" href={`/token/${o.collection}/${o.token_id}`}>{label(o)}</a>
              <VerifiedBadge verified={!!o.collection_verified} tracked={o.collection_tracked} creatorAddr={o.collection_creator ?? ''} collectionName={o.collection_name ?? ''} hint={false} />
            </div>
            <div class="op-dim">
              {tab === 'received' ? `from ${shortAddr(o.bidder)}` : `on ${o.collection_name || shortAddr(o.collection)}`}
              · {expired(o) ? `expired ${timeAgo(o.expires_at)}` : `Expires in ${fmtCountdownShort(new Date(o.expires_at).getTime() / 1000, now)}`}
            </div>
          </div>
          <div class="op-amt mono">{fmtPrice(o.amount_wei)} {sym}</div>
          <div class="op-acts">
            {#each offerActions(tab, expired(o)) as cell (cell.kind)}
              <button class="btn {cell.kind === 'accept' || cell.kind === 'get-refund' ? 'btn-primary' : 'btn-secondary'} btn-sm op-act" onclick={() => runAction(cell.kind, o)}>{cell.label}</button>
            {/each}
          </div>
        </li>
      {/each}
    </ul>
  {/if}
{/if}

<style>
  .op-bar { display: flex; align-items: center; justify-content: space-between; gap: var(--sp-3); flex-wrap: wrap; margin-bottom: var(--sp-3); }
  .op-tabs { display: flex; gap: var(--sp-1); }
  .op-tabs button { min-height: var(--hit); padding: 0 var(--sp-4); border-radius: var(--r-control); background: rgba(255,255,255,.05); border: 1px solid var(--line-strong); color: var(--text-2); font-weight: 700; font-family: inherit; font-size: var(--fs-body); cursor: pointer; display: inline-flex; gap: var(--sp-2); align-items: center; }
  .op-tabs button.is-on { background: var(--gold-12); border-color: var(--gold); color: var(--gold-300); }
  .op-tabs button:disabled { cursor: not-allowed; opacity: .6; }
  .op-n { font-size: var(--fs-caption); background: rgba(255,255,255,.1); border-radius: var(--r-pill); padding: 2px 8px; }
  .op-sort { display: inline-flex; align-items: center; gap: var(--sp-2); font-size: var(--fs-small); color: var(--text-2); font-weight: 600; }
  .op-sort select { min-height: 40px; padding: 0 var(--sp-3); border-radius: var(--r-control); background: rgba(255,255,255,.05); border: 1px solid var(--line-strong); color: var(--text); font-family: inherit; font-size: var(--fs-small); }
  .op-teach { margin-bottom: var(--sp-4); }
  .op-explain { color: var(--text-2); font-size: var(--fs-body); min-height: var(--hit); display: flex; align-items: center; }
  .op-connect { display: flex; }
  .op-sync { padding: var(--sp-2) var(--sp-3); border-radius: var(--r-pill); background: var(--green-12); border: 1px solid var(--green); color: var(--green); font-size: var(--fs-small); font-weight: 600; display: inline-block; margin-bottom: var(--sp-3); }
  .op-list { list-style: none; margin: 0; padding: 0; display: flex; flex-direction: column; gap: var(--sp-2); }
  .op-row { display: grid; grid-template-columns: 56px 1fr auto; grid-template-areas: "thumb main amt" "acts acts acts"; gap: var(--sp-2) var(--sp-3); align-items: center; padding: var(--sp-3); border-radius: var(--r-card); background: var(--surface); border: 1px solid var(--line); }
  @media (min-width: 768px) { .op-row { grid-template-columns: 56px 1fr auto auto; grid-template-areas: "thumb main amt acts"; } }
  .op-row.is-expired { opacity: .75; }
  .op-explain.op-row { grid-template-columns: 1fr; grid-template-areas: "main"; }
  .op-thumb { grid-area: thumb; width: 56px; height: 56px; border-radius: var(--r-control); background: var(--surface-2); overflow: hidden; display: block; }
  .op-thumb img { width: 100%; height: 100%; object-fit: cover; }
  .op-main { grid-area: main; min-width: 0; }
  .op-title { display: flex; align-items: center; gap: var(--sp-2); flex-wrap: wrap; }
  .op-name { color: var(--text); font-weight: 700; text-decoration: none; font-size: var(--fs-body); overflow-wrap: anywhere; }
  .op-dim { font-size: var(--fs-small); color: var(--text-3); }
  .op-amt { grid-area: amt; font-weight: 600; font-size: var(--fs-h3); white-space: nowrap; color: var(--gold-300); }
  .op-acts { grid-area: acts; display: flex; gap: var(--sp-2); flex-wrap: wrap; }
  .op-act { flex: 1 1 auto; }
  .mono { font-family: var(--font-mono); }
</style>
