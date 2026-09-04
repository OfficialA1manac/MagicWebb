<script lang="ts">
  // Collection-creator badge (spec B2): amber ★ Creator, shown when the
  // seller/owner is the on-chain owner (ERC-173 owner()) of the collection,
  // as detected by the backend verifier sweep.
  // link=false renders a <span> — required inside anchor cards (nested <a>
  // is invalid HTML).
  // `name` is accepted (typed) for compatibility with existing call sites; the B2 tooltip is fixed copy.
  interface Props { size?: 'sm' | 'md'; link?: boolean; name?: string }
  let { size = 'sm', link = true }: Props = $props();
  const tip = "Sold by the collection's creator";
</script>

{#if link}
  <a class="vb is-creator {size}" href="/docs/faq#creator" title={tip} aria-label={tip}><span class="vb-star" aria-hidden="true">★</span>Creator</a>
{:else}
  <span class="vb is-creator {size}" title={tip} aria-label={tip}><span class="vb-star" aria-hidden="true">★</span>Creator</span>
{/if}

<!-- Styles live in src/styles/badges.css (global) so static .astro string
     templates can emit identical markup without becoming islands. -->
