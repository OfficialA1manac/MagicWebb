// Single source for every badge (v3.6, owner decision D3).
//
//   ✓ check     NFT-level: "this NFT fully works here" — verified collection,
//               metadata present, image served from our store, holder known.
//               The backend computes it (`verified` + `verified_reason[]` on
//               every row); tokenCheck() reads that and falls back to the same
//               rule client-side for rows that predate the field.
//   pill        Collection-level tier for detail headers: Listed collection
//               (grey) → Verified (sky) → Authentic (gold, creator known).
//   ★ creator   Only for the creator: on their profile, on the collection
//               header, and on an NFT when its holder is the creator AND minted
//               it (`creator_is_owner` from the backend). Never for a mere owner.
//
// Styles live in src/styles/badges.css (.vb.is-* pills, .vb-check glyph).
import { shortAddr } from './format';

export type BadgeTier = 'authentic' | 'verified' | 'tracked' | null;

/** The subset of an API row the badges need. Every list endpoint carries it. */
export interface BadgeRow {
  collection_verified?: boolean | null;
  /** ERC-173 owner() of the collection; "" / null when never resolved. */
  collection_creator?: string | null;
  collection_tracked?: boolean | null;
  collection_name?: string | null;
  /** v3.6 NFT-level checkmark from the backend (db/badge.go). */
  verified?: boolean | null;
  verified_reason?: string[] | null;
  /** v3.6: holder is the creator AND minted the token. */
  creator_is_owner?: boolean | null;
  // Fallback inputs when `verified` is absent (older rows / on-chain fallback).
  name?: string | null;
  image_uri?: string | null;
  image?: string | null;
  owner?: string | null;
  seller?: string | null;
}

export const BADGE_HREF = '/docs/faq#verified';
export const CREATOR_HREF = '/docs/faq#creator';

const ADDR_RE = /^0x[0-9a-fA-F]{40}$/;
const ZERO = '0x0000000000000000000000000000000000000000';

function hasCreator(row: BadgeRow): boolean {
  const c = row.collection_creator;
  return typeof c === 'string' && ADDR_RE.test(c) && c.toLowerCase() !== ZERO;
}

// ── Collection tier (pill) ──────────────────────────────────────────────────

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

/** Tooltip copy per tier. */
export function badgeTip(tier: BadgeTier, row: BadgeRow | null | undefined = undefined): string {
  switch (tier) {
    case 'tracked': return 'This collection is tracked by MagicWebb. Its details are still being checked.';
    case 'verified': return 'Standard NFT contract and metadata confirmed. Not a judgement of the art or the seller.';
    case 'authentic': return `Verified, and the creator is known: ${shortAddr(row?.collection_creator ?? '')}.`;
    default: return '';
  }
}

// ── NFT-level checkmark ─────────────────────────────────────────────────────

export const REASON_COPY: Record<string, string> = {
  unverified_collection: 'its collection has not passed verification yet',
  no_metadata: 'its name and details could not be read',
  image_missing: 'its image is not stored on the marketplace yet',
  owner_unknown: 'its current owner is not known yet',
};

export const CHECK_TIP = 'Verified NFT: its collection, name, image and owner all check out on this marketplace.';

function imageStored(uri: string | null | undefined): boolean {
  const u = (uri ?? '').trim();
  return u.startsWith('/api/v1/img/') || u.startsWith('/img/') || u.startsWith('data:');
}

export interface TokenCheck {
  ok: boolean;
  reasons: string[];
  /** Tooltip: what was checked, or why the check is missing. */
  tip: string;
}

/**
 * Reads the backend's `verified` / `verified_reason`; when absent, applies
 * the same rule client-side so cards never disagree with the token page.
 */
export function tokenCheck(row: BadgeRow | null | undefined): TokenCheck {
  if (!row) return { ok: false, reasons: [], tip: '' };
  let ok: boolean;
  let reasons: string[];
  if (typeof row.verified === 'boolean') {
    ok = row.verified;
    reasons = Array.isArray(row.verified_reason) ? row.verified_reason : [];
  } else {
    reasons = [];
    if (row.collection_verified !== true) reasons.push('unverified_collection');
    if (!(row.name ?? '').trim()) reasons.push('no_metadata');
    if (!imageStored(row.image_uri ?? row.image)) reasons.push('image_missing');
    if (row.owner === '' ) reasons.push('owner_unknown');
    ok = reasons.length === 0;
  }
  const tip = ok
    ? CHECK_TIP
    : reasons.length
      ? `Not verified yet: ${reasons.map((r) => REASON_COPY[r] ?? r).join('; ')}.`
      : 'Not verified yet.';
  return { ok, reasons, tip };
}

// ── ★ Creator ───────────────────────────────────────────────────────────────

export const CREATOR_TIP_ITEM = 'Held and minted by the collection’s creator.';
export const CREATOR_TIP_PROFILE = 'Creator of a verified collection on this marketplace.';
export const CREATOR_TIP_COLLECTION = 'The wallet that created this collection.';

/** ★ on an NFT row: only when the backend says holder == creator AND minted by them. */
export function showCreatorOnItem(row: BadgeRow | null | undefined): boolean {
  return row?.creator_is_owner === true;
}
