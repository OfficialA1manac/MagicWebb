<script lang="ts">
  // D10: wallet on chain X while this site serves chain Y. One sentence, one
  // tap to fix, and an honest cross-link only when that network is deployed.
  import { onMount } from 'svelte';
  import { onAccountChange, waitForWagmi } from '../lib/tx/client';
  import { switchChain } from '@wagmi/core';
  import { chainName, currentChain, networkOrigins } from '../lib/chains';

  let walletChain = $state<number | null>(null);
  let connected = $state(false);
  let switching = $state(false);
  let err = $state('');
  const site = currentChain();

  let show = $derived(connected && walletChain !== null && walletChain !== site.id);
  let otherOrigin = $derived(walletChain !== null ? networkOrigins().get(walletChain) ?? null : null);

  onMount(() => onAccountChange((a) => { connected = !!a.address; walletChain = a.chainId; }));

  async function doSwitch() {
    switching = true; err = '';
    try {
      const cfg = await waitForWagmi();
      await switchChain(cfg, { chainId: site.id });
    } catch {
      err = `Could not switch automatically. Open your wallet and choose ${site.name}.`;
    } finally { switching = false; }
  }
</script>

{#if show}
  <div class="mw-netbanner" role="status">
    <span>Your wallet is on <b>{chainName(walletChain ?? 0)}</b>. This is the <b>{site.name}</b> marketplace.</span>
    <span class="mw-netbanner-actions">
      <button class="mw-netbtn" onclick={doSwitch} disabled={switching}>{switching ? 'Switching…' : `Switch wallet to ${site.name}`}</button>
      {#if otherOrigin}<a class="mw-netbtn ghost" href={otherOrigin}>Go to {chainName(walletChain ?? 0)} marketplace</a>{/if}
    </span>
    {#if err}<span class="mw-neterr">{err}</span>{/if}
  </div>
{/if}

<style>
  .mw-netbanner { display: flex; flex-wrap: wrap; gap: 8px 14px; align-items: center; justify-content: center; padding: 10px 16px; background: rgba(252,211,77,.12); border-bottom: 1px solid rgba(252,211,77,.35); color: #fde68a; font-size: 14px; }
  .mw-netbanner-actions { display: flex; gap: 8px; flex-wrap: wrap; }
  .mw-netbtn { min-height: 40px; padding: 0 14px; border-radius: 10px; background: linear-gradient(135deg,#fcd34d,#f59e0b); color: #09090b; font-weight: 700; font-size: 13px; border: 0; cursor: pointer; font-family: inherit; display: inline-flex; align-items: center; text-decoration: none; }
  .mw-netbtn.ghost { background: transparent; color: #fde68a; border: 1px solid rgba(252,211,77,.4); }
  .mw-netbtn:disabled { opacity: .6; cursor: wait; }
  .mw-neterr { width: 100%; text-align: center; font-size: 12px; color: #fca5a5; }
</style>
