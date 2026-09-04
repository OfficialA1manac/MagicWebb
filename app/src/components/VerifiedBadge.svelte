<script lang="ts">
  // Collection badge — three tiers from lib/badge.ts (spec B2):
  //   Listed collection (grey) → Verified (sky) → Authentic (gold).
  // Same markup contract as badgeHtml() so the string-built cards and this
  // island look identical. `showUnverified` is accepted for compatibility and
  // ignored: untracked collections render nothing.
  // link=false renders a <span> instead of an <a> — required inside anchor
  // cards (nested <a> is invalid HTML). The tap-able Hint is a <button>, so it
  // is only rendered when the badge is not inside a link card (hint defaults
  // to the value of `link`).
  import { badgeTier, badgeTip, BADGE_LABEL, BADGE_CLASS, BADGE_HREF } from '../lib/badge';
  import Hint from './Hint.svelte';
  // `showUnverified`, `network` and `collectionName` are accepted (typed) for
  // compatibility with existing call sites but intentionally not read.
  interface Props { verified: boolean; size?: 'sm' | 'md'; showUnverified?: boolean; network?: string; link?: boolean; collectionName?: string; creatorAddr?: string; tracked?: boolean; hint?: boolean }
  let { verified, size = 'sm', link = true, creatorAddr = '', tracked = undefined, hint = undefined }: Props = $props();
  const row = $derived({ collection_verified: verified, collection_creator: creatorAddr, collection_tracked: tracked });
  const tier = $derived(badgeTier(row));
  const tip = $derived(badgeTip(tier, row));
  const showHint = $derived(hint ?? link);
</script>

{#if tier}
  {#if link}
    <a class="vb {BADGE_CLASS[tier]} {size}" href={BADGE_HREF} title={tip} aria-label={tip}><span class="vb-dot" aria-hidden="true">✓</span>{BADGE_LABEL[tier]}</a>
  {:else}
    <span class="vb {BADGE_CLASS[tier]} {size}" title={tip} aria-label={tip}><span class="vb-dot" aria-hidden="true">✓</span>{BADGE_LABEL[tier]}</span>
  {/if}
  {#if showHint}<Hint text={tip} label="About this badge" />{/if}
{/if}

<!-- Styles live in src/styles/badges.css (global) so static .astro string
     templates can emit identical markup without becoming islands. -->
