<script lang="ts">
  // Wallet on chain X while this site serves chain Y. One sentence, one tap
  // to fix, and an honest cross-link only when that network is deployed.
  import { onMount } from 'svelte';
  import { onAccountChange, switchToSiteChain, waitForWagmi } from '../lib/tx/client';
  import { chainName, currentChain, networkOrigins } from '../lib/chains';
  import { toastError } from '../lib/toast.svelte';
  import Icon from './Icon.svelte';

  const DISMISS_KEY = 'mw_net_dismissed';
  let walletChain = $state<number | null>(null);
  let connected = $state(false);
  let switching = $state(false);
  let dismissed = $state(false);
  const site = currentChain();

  let mismatch = $derived(connected && walletChain !== null && walletChain !== site.id);
  let show = $derived(mismatch && !dismissed);
  // Banner priority: while the wrong-network banner shows, the testnet and
  // browse-only banners hide (base.css reads data-mw-banner on <html>).
  $effect(() => {
    if (typeof document === 'undefined') return;
    if (show) document.documentElement.dataset.mwBanner = 'mismatch';
    else if (document.documentElement.dataset.mwBanner === 'mismatch') delete document.documentElement.dataset.mwBanner;
  });
  function dismiss() {
    dismissed = true;
    try { sessionStorage.setItem(DISMISS_KEY, '1'); } catch { /* private mode */ }
  }
  let otherOrigin = $derived(walletChain !== null ? networkOrigins().get(walletChain) ?? null : null);

  onMount(() => {
    try { dismissed = sessionStorage.getItem(DISMISS_KEY) === '1'; } catch { /* private mode */ }
    return onAccountChange((a) => { connected = !!a.address; walletChain = a.chainId; });
  });

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
    <button type="button" class="icon-btn mw-banner-x" aria-label="Dismiss for this session" onclick={dismiss}><Icon name="x" size={18} /></button>
  </div>
{/if}

<style>
  .mw-netbanner { background: var(--gold-12); border-bottom-color: var(--gold-35); color: var(--text); padding-right: var(--sp-4); }
  .mw-netbanner-actions { display: flex; gap: var(--sp-2); flex-wrap: wrap; }
</style>
