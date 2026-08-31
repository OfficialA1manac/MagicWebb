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
  size?: 128 | 256 | 512,
): string {
  if (!imageUri) return '';
  if (imageUri.startsWith('data:')) return imageUri;
  // Rewrite to the unlimited /img/ path. /api/v1/img/ is matched by the
  // /api/v1 prefix rate-limit middleware (measured: x-ratelimit-limit 30), so a
  // full grid exhausts the bucket on first paint. Stored image_uri rows still
  // carry the old prefix, so normalize here rather than migrating data.
  if (imageUri.startsWith('/api/v1/img/')) {
    imageUri = '/img/' + imageUri.slice('/api/v1/img/'.length);
  }
  if (imageUri.startsWith('/img/')) {
    // Request a pre-generated thumbnail when the caller knows the display size.
    // The backend generates 128/256/512px JPEG+WebP variants at ingest and
    // serves them from /api/v1/img/<sha>?size=N, negotiating WebP off the
    // Accept header. Without this parameter the handler falls through to the
    // FULL-SIZE blob: measured 1,693,489 B for a grid tile that renders a few
    // hundred pixels wide, versus 7,904 B at size=256 — 214x smaller.
    // A blob with no thumbnail row silently falls back to full size, so this is
    // always safe to send.
    return size ? `${imageUri}?size=${size}` : imageUri;
  }
  return (
    '/api/v1/media?url=' +
    encodeURIComponent(imageUri) +
    '&id=' +
    encodeURIComponent(tokenId || '')
  );
}
