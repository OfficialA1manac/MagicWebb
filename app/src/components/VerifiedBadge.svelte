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

<!-- Styles live in src/styles/badges.css (global) so static .astro string
     templates can emit identical markup without becoming islands. -->
