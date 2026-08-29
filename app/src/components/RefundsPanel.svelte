<script lang="ts">
  // "Money waiting for you": pull-payment balances on each core contract
  // (outbid, declined offers, transfers that could not be pushed). Reads
  // pendingReturns() on-chain for the connected wallet.
  import { onMount } from 'svelte';
  import { MW } from '../lib/mw';
  import { onAccountChange } from '../lib/tx/client';
  import { currentChain } from '../lib/chains';
  import { fmtPrice } from '../lib/format';
  import { CORE_LABEL, type CoreKey } from '../lib/tx/core';

  let me = $state<string | null>(null);
  let rows = $state<Array<{ key: CoreKey; address: string; wei: bigint; ok: boolean }>>([]);
  let loading = $state(false);
  let loadError = $state(false);
  const sym = currentChain().currency;
  let total = $derived(rows.filter((r) => r.ok).reduce((s, r) => s + r.wei, 0n));

  // Generation counter: overlapping loads (wallet switch + WS burst) resolve
  // out of order, and a slower earlier response must not overwrite fresh rows.
  let _gen = 0;
  async function load() {
    if (!me) { rows = []; loadError = false; return; }
    const gen = ++_gen;
    loading = true;
    try {
      const next = (await MW.pendingReturns(me)).map((r) => ({ ...r, address: r.address as string }));
      if (gen !== _gen) return;
      rows = next;
      // Never hide silently: even one failed core read could be masking money
      // the user can withdraw, so any partial failure surfaces the error card.
      loadError = rows.some((r) => !r.ok);
    } catch {
      if (gen !== _gen) return;
      rows = [];
      loadError = true;
    }
    loading = false;
  }
  onMount(() => {
    const off = onAccountChange((a) => { me = a.address; void load(); });
    // Debounce the firehose '*' subscription (matches NFTGrid): each load is
    // one on-chain read per core contract, so bursts must coalesce.
    let t: ReturnType<typeof setTimeout> | null = null;
    const offWs = MW.ws.on('*', () => { if (t) clearTimeout(t); t = setTimeout(() => { t = null; void load(); }, 400); });
    return () => { off(); offWs(); if (t) clearTimeout(t); };
  });
  async function withdraw(r: { key: CoreKey; address: string; wei: bigint }) {
    try { await MW.withdrawRefundFrom({ core: r.address, label: CORE_LABEL[r.key], amountWei: r.wei.toString() }); setTimeout(load, 1500); setTimeout(load, 6000); } catch { /* modal */ }
  }
</script>

{#if me && !loading && loadError}
  <section class="rp rp-err" aria-labelledby="rp-err-h">
    <div class="rp-head"><h2 id="rp-err-h">Couldn't check your refunds</h2></div>
    <p class="rp-hint">We couldn't reach the network to see if you have funds waiting. Your money is safe on-chain.</p>
    <button class="rp-btn" onclick={() => void load()}>Try again</button>
  </section>
{/if}

{#if me && !loading && total > 0n}
  <section class="rp" aria-labelledby="rp-h">
    <div class="rp-head"><h2 id="rp-h">Refunds waiting for you</h2><span class="rp-total mono">{fmtPrice(total)} {sym}</span></div>
    <p class="rp-hint">Outbid or declined? Funds come back here, never lost. Withdraw any time — gas only.</p>
    <ul class="rp-list">
      {#each rows.filter((r) => r.ok && r.wei > 0n) as r (r.key)}
        <li><span>{CORE_LABEL[r.key]}</span><span class="mono">{fmtPrice(r.wei)} {sym}</span><button class="rp-btn" onclick={() => withdraw(r)}>Withdraw</button></li>
      {/each}
    </ul>
  </section>
{/if}

<style>
  .rp { padding: 16px; border-radius: 16px; background: rgba(74,222,128,.06); border: 1px solid rgba(74,222,128,.3); margin-bottom: 20px; }
  .rp-err { background: rgba(252,165,165,.06); border-color: rgba(252,165,165,.3); }
  .rp-head { display: flex; justify-content: space-between; align-items: center; gap: 8px; flex-wrap: wrap; } h2 { font-size: 15px; font-weight: 800; margin: 0; }
  .rp-total { font-weight: 700; color: #bbf7d0; }
  .rp-hint { font-size: 12px; color: rgba(255,255,255,.55); margin: 6px 0 10px; }
  .rp-list { list-style: none; margin: 0; padding: 0; display: flex; flex-direction: column; gap: 6px; }
  .rp-list li { display: flex; align-items: center; gap: 12px; font-size: 13px; min-height: 44px; }
  .rp-list li span:first-child { flex: 1; }
  .rp-btn { min-height: 40px; padding: 0 14px; border-radius: 10px; background: linear-gradient(135deg,#4ade80,#16a34a); color: #09090b; font-weight: 700; border: 0; cursor: pointer; font-family: inherit; }
  .mono { font-family: 'JetBrains Mono', ui-monospace, monospace; }
</style>
