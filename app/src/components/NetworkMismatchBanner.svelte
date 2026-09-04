<script lang="ts">
  // Wallet on chain X while this site serves chain Y. One sentence, one tap
  // to fix, and an honest cross-link only when that network is deployed.
  import { onMount } from 'svelte';
  import { onAccountChange, switchToSiteChain, waitForWagmi } from '../lib/tx/client';
  import { chainName, currentChain, networkOrigins } from '../lib/chains';
  import { toastError } from '../lib/toast.svelte';

  let walletChain = $state<number | null>(null);
  let connected = $state(false);
  let switching = $state(false);
  const site = currentChain();

  let show = $derived(connected && walletChain !== null && walletChain !== site.id);
  let otherOrigin = $derived(walletChain !== null ? networkOrigins().get(walletChain) ?? null : null);

  onMount(() => onAccountChange((a) => { connected = !!a.address; walletChain = a.chainId; }));

  async function doSwitch() {
    switching = true;
    try {
      const cfg = await waitForWagmi();
      await switchToSiteChain(cfg); // adds the chain (wallet_addEthereumChain) on 4902
    } catch {
      toastError(`Could not switch automatically. Open your wallet and choose ${site.name}.`);
    } finally { switching = false; }
  }
</script>

{#if show}
  <div class="mw-banner mw-netbanner" role="status">
    <span>Your wallet is on <b>{chainName(walletChain ?? 0)}</b>. This is the <b>{site.name}</b> marketplace.</span>
    <span class="mw-netbanner-actions">
      <button class="btn btn-primary" onclick={doSwitch} disabled={switching}>{switching ? 'Switching…' : `Switch wallet to ${site.name}`}</button>
      {#if otherOrigin}<a class="btn btn-secondary" href={otherOrigin + location.pathname + location.search}>Go to {chainName(walletChain ?? 0)}</a>{/if}
    </span>
  </div>
{/if}

<style>
  .mw-netbanner { background: var(--gold-12); border-bottom-color: var(--gold-35); color: var(--text); padding-right: var(--sp-4); }
  .mw-netbanner-actions { display: flex; gap: var(--sp-2); flex-wrap: wrap; }
</style>
