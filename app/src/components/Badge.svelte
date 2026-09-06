<script lang="ts">
  // One badge component for every surface (v3.6, D3). Three variants:
  //   check   — NFT-level ✓ glyph (sky). Renders only when the NFT is verified.
  //             Inside anchor cards use link=false + hint=false (no nested
  //             interactive element); the card wrapper places a tap-able
  //             CardHint next to the anchor instead.
  //   pill    — collection tier pill for detail headers (Listed collection /
  //             Verified / Authentic).
  //   creator — ★ Creator (amber). The caller decides when it applies
  //             (creator_is_owner on items; verified_creator on a profile;
  //             a known creator on a collection header).
  // `title` is never the only affordance: with hint=true a touch-safe Hint
  // button follows the badge.
  import { badgeTier, badgeTip, BADGE_LABEL, BADGE_CLASS, BADGE_HREF, CREATOR_HREF, tokenCheck, CREATOR_TIP_ITEM } from '../lib/badge';
  import type { BadgeRow } from '../lib/badge';
  import Hint from './Hint.svelte';

  interface Props {
    variant?: 'check' | 'pill' | 'creator';
    row?: BadgeRow | null;
    size?: 'sm' | 'md';
    /** Render as a link to the FAQ (false inside anchor cards). */
    link?: boolean;
    /** Append a touch-safe Hint button (defaults to `link`). */
    hint?: boolean;
    /** Override tooltip (creator variant on profiles / collections). */
    tip?: string;
    /** Extra classes, e.g. positioning inside a card. */
    class?: string;
  }
  let { variant = 'check', row = null, size = 'sm', link = true, hint = undefined, tip = undefined, class: cls = '' }: Props = $props();

  const tier = $derived(badgeTier(row));
  const check = $derived(tokenCheck(row));
  const showHint = $derived(hint ?? link);
  const pillTip = $derived(badgeTip(tier, row));
  const creatorTip = $derived(tip ?? CREATOR_TIP_ITEM);
</script>

{#if variant === 'check'}
  {#if check.ok}
    {#if link}
      <a class="vb vb-check {size} {cls}" href={BADGE_HREF} title={check.tip} aria-label={check.tip}><span class="vb-dot" aria-hidden="true">✓</span><span class="vb-check-text">Verified</span></a>
    {:else}
      <span class="vb vb-check {size} {cls}" title={check.tip} aria-label={check.tip}><span class="vb-dot" aria-hidden="true">✓</span><span class="vb-check-text">Verified</span></span>
    {/if}
    {#if showHint}<Hint text={check.tip} label="About this checkmark" />{/if}
  {/if}
{:else if variant === 'pill'}
  {#if tier}
    {#if link}
      <a class="vb {BADGE_CLASS[tier]} {size} {cls}" href={BADGE_HREF} title={pillTip} aria-label={pillTip}><span class="vb-dot" aria-hidden="true">✓</span>{BADGE_LABEL[tier]}</a>
    {:else}
      <span class="vb {BADGE_CLASS[tier]} {size} {cls}" title={pillTip} aria-label={pillTip}><span class="vb-dot" aria-hidden="true">✓</span>{BADGE_LABEL[tier]}</span>
    {/if}
    {#if showHint}<Hint text={pillTip} label="About this badge" />{/if}
  {/if}
{:else}
  {#if link}
    <a class="vb is-creator {size} {cls}" href={CREATOR_HREF} title={creatorTip} aria-label={creatorTip}><span class="vb-star" aria-hidden="true">★</span>Creator</a>
  {:else}
    <span class="vb is-creator {size} {cls}" title={creatorTip} aria-label={creatorTip}><span class="vb-star" aria-hidden="true">★</span>Creator</span>
  {/if}
  {#if showHint}<Hint text={creatorTip} label="About the creator badge" />{/if}
{/if}
