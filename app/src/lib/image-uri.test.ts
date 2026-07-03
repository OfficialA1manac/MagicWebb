import { describe, it, expect } from 'vitest';
import { resolveImageUri } from './image-uri';

describe('resolveImageUri', () => {
  // ── Falsy / null / undefined ──────────────────────────────────────────
  it('returns empty string for null', () => {
    expect(resolveImageUri(null)).toBe('');
  });

  it('returns empty string for undefined', () => {
    expect(resolveImageUri(undefined)).toBe('');
  });

  it('returns empty string for empty string', () => {
    expect(resolveImageUri('')).toBe('');
  });

  // ── /api/v1/img/ URIs (self-hosted blobs) bypass proxy ───────────────
  it('returns /api/v1/img/ URI as-is (no proxy)', () => {
    const sha = 'abc123def4567890abcdef1234567890abcdef1234567890abcdef1234567890';
    const uri = '/api/v1/img/' + sha;
    expect(resolveImageUri(uri)).toBe(uri);
  });

  it('returns /api/v1/img/ URI as-is with token ID present', () => {
    const uri = '/api/v1/img/somehash';
    expect(resolveImageUri(uri, '42')).toBe(uri);
  });

  // ── data: URIs (inline base64) bypass proxy ──────────────────────────
  it('returns data:image/png URI as-is', () => {
    const uri = 'data:image/png;base64,iVBORw0KGgo=';
    expect(resolveImageUri(uri)).toBe(uri);
  });

  it('returns data:image/svg+xml URI as-is', () => {
    const uri = 'data:image/svg+xml;utf8,<svg></svg>';
    expect(resolveImageUri(uri)).toBe(uri);
  });

  it('returns data:image/gif URI as-is', () => {
    const uri = 'data:image/gif;base64,R0lGODlhAQABAAAAACw=';
    expect(resolveImageUri(uri)).toBe(uri);
  });

  it('returns data: URI with tokenId — tokenId is ignored for data URIs', () => {
    const uri = 'data:image/png;base64,iVBOR=';
    expect(resolveImageUri(uri, '123')).toBe(uri);
  });

  // ── External/remote URIs go through proxy ─────────────────────────────
  it('proxies https:// URL via /api/v1/media', () => {
    const uri = 'https://example.com/nft.png';
    const result = resolveImageUri(uri);
    expect(result).toContain('/api/v1/media?url=');
    expect(result).toContain(encodeURIComponent(uri));
  });

  it('proxies ipfs:// URL via /api/v1/media', () => {
    const uri = 'ipfs://bafybeihash/image.png';
    const result = resolveImageUri(uri);
    expect(result).toContain('/api/v1/media?url=');
    expect(result).toContain(encodeURIComponent(uri));
  });

  it('proxies ar:// URL via /api/v1/media', () => {
    const uri = 'ar://some-arweave-hash';
    const result = resolveImageUri(uri);
    expect(result).toContain('/api/v1/media?url=');
    expect(result).toContain(encodeURIComponent(uri));
  });

  it('proxies http:// URL via /api/v1/media', () => {
    const uri = 'http://some-nft-cdn.com/token/42.png';
    const result = resolveImageUri(uri);
    expect(result).toContain('/api/v1/media?url=');
    expect(result).toContain(encodeURIComponent(uri));
  });

  // ── Token ID encoding in proxy URL ────────────────────────────────────
  it('includes token_id in proxy URL when provided', () => {
    const uri = 'https://example.com/nft.png';
    const result = resolveImageUri(uri, '42');
    expect(result).toBe(
      '/api/v1/media?url=' +
        encodeURIComponent(uri) +
        '&id=' +
        encodeURIComponent('42'),
    );
  });

  it('includes empty token_id parameter when tokenId is empty', () => {
    const uri = 'https://example.com/nft.png';
    const result = resolveImageUri(uri, '');
    expect(result).toBe(
      '/api/v1/media?url=' +
        encodeURIComponent(uri) +
        '&id=' +
        encodeURIComponent(''),
    );
  });

  it('includes empty token_id parameter when tokenId not provided', () => {
    const uri = 'https://example.com/nft.png';
    const result = resolveImageUri(uri);
    expect(result).toBe(
      '/api/v1/media?url=' +
        encodeURIComponent(uri) +
        '&id=' +
        encodeURIComponent(''),
    );
  });

  it('encodes special characters in tokenId correctly', () => {
    const uri = 'https://example.com/nft.png';
    const result = resolveImageUri(uri, 'token #42/special');
    expect(result).toBe(
      '/api/v1/media?url=' +
        encodeURIComponent(uri) +
        '&id=' +
        encodeURIComponent('token #42/special'),
    );
  });

  // ── Edge cases ────────────────────────────────────────────────────────
  it('proxies relative path (no leading /api/v1/img/) via media proxy', () => {
    const uri = '/static/images/nft.png';
    const result = resolveImageUri(uri);
    expect(result).toContain('/api/v1/media?url=');
    expect(result).toContain(encodeURIComponent(uri));
  });

  it('proxies a URL that happens to contain /api/v1/img/ in its path', () => {
    // Only URIs that START with /api/v1/img/ bypass the proxy
    const uri = 'https://cdn.example.com/api/v1/img/abc.png';
    const result = resolveImageUri(uri);
    expect(result).toContain('/api/v1/media?url=');
    expect(result).not.toBe(uri);
  });

  it('proxies a URL that happens to contain "data:" in its path (not prefix)', () => {
    // Only URIs that START with data: bypass the proxy
    const uri = 'https://example.com/data:image.png';
    const result = resolveImageUri(uri);
    expect(result).toContain('/api/v1/media?url=');
    expect(result).not.toBe(uri);
  });

  // ── Cross-page consistency: verify all 7 pages would produce the same result ──
  const pages = [
    'profile.astro',
    'index.astro',
    'auctions.astro',
    'auction.astro',
    'collection.astro',
    'search.astro',
    'token.astro',
  ];

  it.each(pages)('%s: data: URI bypasses proxy consistently', () => {
    const uri = 'data:image/png;base64,abc=';
    expect(resolveImageUri(uri)).toBe(uri);
  });

  it.each(pages)('%s: /api/v1/img/ URI bypasses proxy consistently', () => {
    const uri = '/api/v1/img/somehash';
    expect(resolveImageUri(uri)).toBe(uri);
  });

  it.each(pages)('%s: external URL goes through proxy consistently', () => {
    const uri = 'https://nft.storage/token/1.png';
    const result = resolveImageUri(uri, '1');
    expect(result).toContain('/api/v1/media?url=');
    expect(result).not.toBe(uri);
  });

  it.each(pages)('%s: ipfs:// URL goes through proxy consistently', () => {
    const uri = 'ipfs://QmHash/image.png';
    const result = resolveImageUri(uri);
    expect(result).toContain('/api/v1/media?url=');
    expect(result).toContain(encodeURIComponent(uri));
  });

  it.each(pages)('%s: null image_uri returns empty string consistently', () => {
    expect(resolveImageUri(null)).toBe('');
  });
});
