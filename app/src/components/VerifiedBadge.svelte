<script lang="ts">
  // D12: "Verified NFT" = the contract answers ERC-165 for ERC-721/1155 AND its
  // metadata resolved at least once (backend/internal/verifier). Not curation.
  let { verified, size = 'sm', showUnverified = false, network = '' }: { verified: boolean; size?: 'sm' | 'md'; showUnverified?: boolean; network?: string } = $props();
  const tip = $derived(verified
    ? `Verified NFT: standard NFT contract, metadata confirmed${network ? ' on ' + network : ''}. Not a judgement of the art or the seller.`
    : 'Unverified: the contract has not passed the ERC-721/1155 check yet, or its metadata has not loaded.');
</script>

{#if verified}
  <a class="vb is-ok {size}" href="/docs/faq#verified" title={tip} aria-label={tip}><span class="vb-dot" aria-hidden="true">✓</span>Verified NFT</a>
{:else if showUnverified}
  <a class="vb is-no {size}" href="/docs/faq#verified" title={tip} aria-label={tip}>Unverified</a>
{/if}

<style>
  .vb { display: inline-flex; align-items: center; gap: .3rem; border-radius: 999px; font-weight: 700; text-decoration: none; line-height: 1; white-space: nowrap; min-height: 24px; padding: 0 .55rem; font-size: .6875rem; }
  .vb.md { min-height: 28px; padding: 0 .7rem; font-size: .75rem; }
  .is-ok { color: #bae6fd; background: rgba(125,211,252,.12); border: 1px solid rgba(125,211,252,.35); }
  .is-no { color: rgba(255,255,255,.55); background: rgba(255,255,255,.05); border: 1px solid rgba(255,255,255,.12); font-weight: 600; }
  .vb-dot { width: 14px; height: 14px; border-radius: 50%; background: #7dd3fc; color: #09090b; font-size: 10px; font-weight: 900; display: inline-flex; align-items: center; justify-content: center; }
  .vb:focus-visible { outline: 2px solid #7dd3fc; outline-offset: 2px; }
</style>
