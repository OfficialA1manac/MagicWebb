// Single source for the collection badge (spec B2). Three tiers, one markup
// contract shared by VerifiedBadge.svelte and the string-built cards on the
// static pages (through window.mwVerifiedBadge, see mwBadgeGlobal()).
//
//   tier        | when                                   | pill
//   tracked     | collection_tracked, not verified       | grey  ✓ Listed collection
//   verified    | collection_verified, no creator known  | sky   ✓ Verified
//   authentic   | verified AND collection_creator known  | gold  ✓ Authentic
//   null        | collection not tracked                 | (nothing)
//
// Styles live in src/styles/badges.css (.vb.is-tracked / .is-ok / .is-authentic).
import { esc, shortAddr } from './format';

export type BadgeTier = 'authentic' | 'verified' | 'tracked' | null;

/** The subset of an API row the badge needs. Every list endpoint carries it. */
export interface BadgeRow {
  collection_verified?: boolean | null;
  /** ERC-173 owner() of the collection; "" / null when the verifier never resolved one. */
  collection_creator?: string | null;
  /** Present on rows from tracked collections. `undefined` is treated as
   *  tracked when `collection_verified` is a boolean (only tracked rows carry it). */
  collection_tracked?: boolean | null;
}

export interface BadgeOptions {
  size?: 'sm' | 'md';
  /** Render an <a href="/docs/faq#verified"> (true) or a <span> (inside anchor cards). */
  link?: boolean;
  /** Accepted for compatibility; the B2 tooltip copy does not mention the network. */
  network?: string;
  collectionName?: string;
  /** Inline style attribute (string-built cards position the pill absolutely). */
  style?: string;
}

export const BADGE_HREF = '/docs/faq#verified';

const ADDR_RE = /^0x[0-9a-fA-F]{40}$/;
const ZERO = '0x0000000000000000000000000000000000000000';

function hasCreator(row: BadgeRow): boolean {
  const c = row.collection_creator;
  return typeof c === 'string' && ADDR_RE.test(c) && c.toLowerCase() !== ZERO;
}

export function badgeTier(row: BadgeRow | null | undefined): BadgeTier {
  if (!row) return null;
  const verified = row.collection_verified === true;
  const tracked = row.collection_tracked === undefined || row.collection_tracked === null
    ? typeof row.collection_verified === 'boolean'
    : !!row.collection_tracked;
  if (verified && hasCreator(row)) return 'authentic';
  if (verified) return 'verified';
  if (tracked) return 'tracked';
  return null;
}

export const BADGE_LABEL: Record<Exclude<BadgeTier, null>, string> = {
  tracked: 'Listed collection',
  verified: 'Verified',
  authentic: 'Authentic',
};

export const BADGE_CLASS: Record<Exclude<BadgeTier, null>, string> = {
  tracked: 'is-tracked',
  verified: 'is-ok',
  authentic: 'is-authentic',
};

/** Tooltip copy per tier (spec B2 table). */
export function badgeTip(tier: BadgeTier, row: BadgeRow | null | undefined = undefined): string {
  switch (tier) {
    case 'tracked': return 'This NFT comes from a collection MagicWebb tracks. Its details are still being checked.';
    case 'verified': return 'Standard NFT contract and metadata confirmed. Not a judgement of the art or the seller.';
    case 'authentic': return `Verified, and the creator is known: ${shortAddr(row?.collection_creator ?? '')}.`;
    default: return '';
  }
}

/**
 * Exact `.vb` markup used by badges.css:
 *   <a class="vb is-ok sm" href="/docs/faq#verified" title="…" aria-label="…">
 *     <span class="vb-dot" aria-hidden="true">✓</span>Verified</a>
 * Returns '' for untracked collections (no pill).
 */
export function badgeHtml(row: BadgeRow | null | undefined, opts: BadgeOptions = {}): string {
  const tier = badgeTier(row);
  if (!tier) return '';
  const size = opts.size ?? 'sm';
  const tip = esc(badgeTip(tier, row));
  const style = opts.style ? ` style="${esc(opts.style)}"` : '';
  const cls = `vb ${BADGE_CLASS[tier]} ${size}`;
  const inner = `<span class="vb-dot" aria-hidden="true">✓</span>${BADGE_LABEL[tier]}`;
  if (opts.link) return `<a class="${cls}" href="${BADGE_HREF}"${style} title="${tip}" aria-label="${tip}">${inner}</a>`;
  return `<span class="${cls}"${style} title="${tip}" aria-label="${tip}">${inner}</span>`;
}

declare global {
  interface Window {
    /** Shared badge renderer for inline page scripts (installed by mwBadgeGlobal). */
    mwVerifiedBadge?: (verified: boolean | null | undefined, creator?: string | null, name?: string, style?: string, tracked?: boolean | null) => string;
  }
}

/**
 * Installs window.mwVerifiedBadge so the inline scripts on index/auctions/
 * search/profile can drop their private copies and call the shared one.
 * Signature mirrors the inline copies: (verified, creator, name, style, tracked).
 */
export function mwBadgeGlobal(): void {
  if (typeof window === 'undefined') return;
  window.mwVerifiedBadge = (verified, creator, name, style, tracked) =>
    badgeHtml(
      { collection_verified: verified === true, collection_creator: creator ?? '', collection_tracked: tracked === undefined ? (typeof verified === 'boolean' ? undefined : false) : tracked },
      { size: 'sm', link: false, collectionName: name ?? '', style },
    );
}
