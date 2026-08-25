<script lang="ts">
  // D12: "Verified NFT" = the contract answers ERC-165 for ERC-721/1155 AND its
  // metadata resolved at least once (backend/internal/verifier). Not curation.
  // link=false renders a <span> instead of an <a> — required when the badge
  // sits inside an anchor card (nested <a> is invalid HTML and browsers close
  // the outer link early).
  let { verified, size = 'sm', showUnverified = false, network = '', link = true }: { verified: boolean; size?: 'sm' | 'md'; showUnverified?: boolean; network?: string; link?: boolean } = $props();
  const tip = $derived(verified
    ? `Verified NFT: standard NFT contract, metadata confirmed${network ? ' on ' + network : ''}. Not a judgement of the art or the seller.`
    : 'Unverified: the contract has not passed the ERC-721/1155 check yet, or its metadata has not loaded.');
</script>

{#if verified}
  {#if link}
    <a class="vb is-ok {size}" href="/docs/faq#verified" title={tip} aria-label={tip}><span class="vb-dot" aria-hidden="true">✓</span>Verified NFT</a>
  {:else}
    <span class="vb is-ok {size}" title={tip} aria-label={tip}><span class="vb-dot" aria-hidden="true">✓</span>Verified NFT</span>
  {/if}
{:else if showUnverified}
  {#if link}
    <a class="vb is-no {size}" href="/docs/faq#verified" title={tip} aria-label={tip}>Unverified</a>
  {:else}
    <span class="vb is-no {size}" title={tip} aria-label={tip}>Unverified</span>
  {/if}
{/if}

<!-- Styles live in src/styles/badges.css (global) so static .astro string
     templates can emit identical markup without becoming islands. -->
