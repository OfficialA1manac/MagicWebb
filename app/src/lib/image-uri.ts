/**
 * Resolves an NFT image URI to a displayable URL.
 *
 * - `/api/v1/img/…` URIs are self-hosted blobs — render directly, no proxy needed.
 * - `data:…` URIs are inline base64 — render directly, no proxy needed.
 * - Everything else goes through the media proxy (`/api/v1/media`) for SSRF-safe
 *   outbound fetching.
 *
 * Used by all 7 Astro pages: profile, index, auctions, auction, collection,
 * search, and token.
 */
export function resolveImageUri(
  imageUri: string | null | undefined,
  tokenId?: string | null,
): string {
  if (!imageUri) return '';
  if (imageUri.startsWith('/api/v1/img/') || imageUri.startsWith('data:')) {
    return imageUri;
  }
  return (
    '/api/v1/media?url=' +
    encodeURIComponent(imageUri) +
    '&id=' +
    encodeURIComponent(tokenId || '')
  );
}
