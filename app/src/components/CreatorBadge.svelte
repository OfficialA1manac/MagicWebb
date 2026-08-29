<script lang="ts">
  // Collection-creator ("founder") badge. Shown next to a collection or
  // profile when the address is the on-chain owner (ERC-173 owner()) of the
  // collection, as detected by the backend verifier sweep. Cosmetic +
  // informational: tells buyers who created/controls the collection.
  // link=false renders a <span> — required inside anchor cards (nested <a>
  // is invalid HTML).
  let { size = 'sm', link = true, name = '' }: { size?: 'sm' | 'md'; link?: boolean; name?: string } = $props();
  const tip = $derived(`Collection creator${name ? ' of ' + name : ''}: this address is the on-chain owner of the collection contract.`);
</script>

{#if link}
  <a class="vb is-creator {size}" href="/docs/faq#creator" title={tip} aria-label={tip}><span class="vb-star" aria-hidden="true">★</span>Creator</a>
{:else}
  <span class="vb is-creator {size}" title={tip} aria-label={tip}><span class="vb-star" aria-hidden="true">★</span>Creator</span>
{/if}

<!-- Styles live in src/styles/badges.css (global) so static .astro string
     templates can emit identical markup without becoming islands. -->
