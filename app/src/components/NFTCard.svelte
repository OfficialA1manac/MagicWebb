<script lang="ts">
  import { fly } from 'svelte/transition';

  interface ListingItem {
    collection: string;
    token_id: string;
    seller: string;
    price_wei: string;
    amount: number;
    standard: string;
    expires_at: string;
    listed_at: string;
    tx_hash: string;
    name: string;
    image_uri: string;
    total_supply: number;
    collection_verified: boolean;
  }

  let { item }: { item: ListingItem } = $props();

  let priceFormatted = $derived(() => {
    const wei = BigInt(item.price_wei);
    const divisor = BigInt(10) ** BigInt(18);
    const whole = wei / divisor;
    const remainder = wei % divisor;
    const remainderStr = remainder.toString().padStart(18, '0').slice(0, 4);
    return `${whole}.${remainderStr}`;
  });

  let imageError = $state(false);

  let imageSrc = $derived(
    item.image_uri
      ? `/api/v1/media?url=${encodeURIComponent(item.image_uri)}&id=${encodeURIComponent(item.token_id)}`
      : ''
  );

  // ── 3D tilt state ──
  let tiltStyle = $state('');
  let cardRef = $state<HTMLAnchorElement | null>(null);

  function handleMouseMove(e: MouseEvent) {
    if (!cardRef) return;
    const rect = cardRef.getBoundingClientRect();
    const x = e.clientX - rect.left;
    const y = e.clientY - rect.top;
    const centerX = rect.width / 2;
    const centerY = rect.height / 2;
    const rotateX = ((y - centerY) / centerY) * -6;
    const rotateY = ((x - centerX) / centerX) * 6;
    tiltStyle = `perspective(1000px) rotateX(${rotateX}deg) rotateY(${rotateY}deg) scale3d(1.02,1.02,1.02)`;
  }

  function handleMouseLeave() {
    tiltStyle = 'perspective(1000px) rotateX(0deg) rotateY(0deg) scale3d(1,1,1)';
  }
</script>

<a
  href="/token/{item.collection}/{item.token_id}"
  class="nft-card tilt-card"
  bind:this={cardRef}
  style:transform={tiltStyle}
  onmousemove={handleMouseMove}
  onmouseleave={handleMouseLeave}
  transition:fly={{ y: 20, duration: 400, delay: (function() { try { return Number(BigInt(item.token_id || '0') % 10n) * 30; } catch(_) { return 0; } })() }}
>
  <div class="image-wrapper">
    {#if imageSrc && !imageError}
      <img
        src={imageSrc}
        alt={item.name || `Token #${item.token_id}`}
        onerror={() => (imageError = true)}
        loading="lazy"
      />
    {:else}
      <div class="image-placeholder">
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5">
          <rect x="3" y="3" width="18" height="18" rx="2" />
          <circle cx="8.5" cy="8.5" r="1.5" />
          <path d="m21 15-5-5L5 21" />
        </svg>
      </div>
    {/if}

    <!-- Top-left badges -->
    <div class="top-left-badges">
      {#if item.collection_verified}
        <div class="verified-badge" title="Verified Collection">
          <svg width="10" height="10" viewBox="0 0 20 20" fill="currentColor"><path fill-rule="evenodd" d="M6.267 3.455a3.066 3.066 0 001.745-.723 3.066 3.066 0 013.976 0 3.066 3.066 0 001.745.723 3.066 3.066 0 012.812 2.812c.051.643.304 1.254.723 1.745a3.066 3.066 0 010 3.976 3.066 3.066 0 00-.723 1.745 3.066 3.066 0 01-2.812 2.812 3.066 3.066 0 00-1.745.723 3.066 3.066 0 01-3.976 0 3.066 3.066 0 00-1.745-.723 3.066 3.066 0 01-2.812-2.812 3.066 3.066 0 00-.723-1.745 3.066 3.066 0 010-3.976 3.066 3.066 0 00.723-1.745 3.066 3.066 0 012.812-2.812zm7.44 5.252a1 1 0 00-1.414-1.414L9 10.586 7.707 9.293a1 1 0 00-1.414 1.414l2 2a1 1 0 001.414 0l4-4z" clip-rule="evenodd"/></svg>
        </div>
      {/if}
      {#if item.standard}
        <span class="standard-badge" title="Token standard">{item.standard}</span>
      {/if}
    </div>

    <!-- Top-right: price -->
    <div class="price-badge">
      <span class="price-value">{priceFormatted()}</span>
      <span class="price-symbol">C2FLR</span>
    </div>

    <!-- Bottom-right: amount for ERC1155 -->
    {#if item.standard === 'erc1155' && item.amount > 1}
      <span class="amount-badge">x{item.amount}</span>
    {/if}
  </div>

  <div class="card-body">
    <p class="collection-addr">{item.collection.slice(0, 6)}...{item.collection.slice(-4)}</p>
    <h3 class="token-name">{item.name || `#${item.token_id}`}</h3>
    <div class="card-footer">
      <span class="supply-text">{item.total_supply ? `${item.total_supply} minted` : ''}</span>
      <span class="buy-btn">Buy</span>
    </div>
  </div>
</a>

<style>
  .nft-card {
    display: block;
    background: rgba(15, 15, 19, 0.6);
    border: 1px solid rgba(255, 255, 255, 0.07);
    border-radius: 1rem;
    overflow: hidden;
    transition: box-shadow 0.3s ease, border-color 0.3s ease;
    transform-style: preserve-3d;
    will-change: transform;
  }

  .nft-card:hover {
    border-color: rgba(167, 139, 250, 0.4);
    box-shadow: 0 20px 60px rgba(139, 92, 246, 0.18);
  }

  .image-wrapper {
    position: relative;
    aspect-ratio: 1;
    overflow: hidden;
    background: rgba(9, 9, 11, 0.5);
  }

  .image-wrapper img {
    width: 100%;
    height: 100%;
    object-fit: cover;
    transition: transform 0.5s ease;
  }

  .nft-card:hover .image-wrapper img {
    transform: scale(1.06);
  }

  .image-placeholder {
    width: 100%;
    height: 100%;
    display: flex;
    align-items: center;
    justify-content: center;
    color: rgba(255, 255, 255, 0.08);
  }

  .image-placeholder svg {
    width: 3rem;
    height: 3rem;
  }

  .top-left-badges {
    position: absolute;
    top: 0.5rem;
    left: 0.5rem;
    display: flex;
    flex-direction: column;
    gap: 0.375rem;
  }

  .verified-badge {
    width: 1.5rem;
    height: 1.5rem;
    border-radius: 50%;
    background: rgba(124, 58, 237, 0.25);
    border: 1px solid rgba(167, 139, 250, 0.35);
    display: flex;
    align-items: center;
    justify-content: center;
    color: #c4b5fd;
    backdrop-filter: blur(4px);
    box-shadow: 0 0 14px -4px rgba(167, 139, 250, 0.4);
  }

  .standard-badge {
    padding: 0.125rem 0.375rem;
    border-radius: 0.375rem;
    background: rgba(255, 255, 255, 0.08);
    border: 1px solid rgba(255, 255, 255, 0.12);
    font-size: 0.5625rem;
    font-weight: 800;
    text-transform: uppercase;
    letter-spacing: 0.05em;
    color: rgba(255, 255, 255, 0.6);
    backdrop-filter: blur(4px);
  }

  .price-badge {
    position: absolute;
    top: 0.5rem;
    right: 0.5rem;
    padding: 0.25rem 0.625rem;
    border-radius: 0.625rem;
    background: rgba(9, 9, 11, 0.75);
    backdrop-filter: blur(8px);
    border: 1px solid rgba(252, 211, 77, 0.3);
    box-shadow: 0 0 18px -4px rgba(251, 191, 36, 0.45);
  }

  .price-value {
    font-size: 0.75rem;
    font-weight: 900;
    color: #fde68a;
  }

  .price-symbol {
    font-size: 0.5625rem;
    color: rgba(253, 224, 138, 0.7);
    font-weight: 700;
    text-transform: uppercase;
    margin-left: 0.125rem;
  }

  .amount-badge {
    position: absolute;
    bottom: 0.5rem;
    right: 0.5rem;
    padding: 0.125rem 0.5rem;
    background: rgba(9, 9, 11, 0.7);
    border-radius: 0.375rem;
    color: rgba(255, 255, 255, 0.7);
    font-size: 0.6875rem;
    font-weight: 700;
    backdrop-filter: blur(4px);
  }

  .card-body {
    padding: 0.75rem;
    border-top: 1px solid rgba(255, 255, 255, 0.04);
    background: rgba(15, 15, 19, 0.6);
  }

  .collection-addr {
    font-size: 0.5625rem;
    color: rgba(255, 255, 255, 0.25);
    font-family: 'JetBrains Mono', 'Fira Code', monospace;
    text-transform: uppercase;
    letter-spacing: 0.05em;
    margin-bottom: 0.1875rem;
  }

  .token-name {
    font-size: 0.875rem;
    font-weight: 700;
    color: #fafafa;
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
    margin: 0;
  }

  .card-footer {
    display: flex;
    align-items: center;
    justify-content: space-between;
    margin-top: 0.625rem;
  }

  .supply-text {
    font-size: 0.625rem;
    color: rgba(255, 255, 255, 0.25);
    font-weight: 600;
  }

  .buy-btn {
    padding: 0.25rem 0.875rem;
    border-radius: 0.5rem;
    background: linear-gradient(135deg, #7dd3fc, #0ea5e9);
    color: #09090b;
    font-size: 0.6875rem;
    font-weight: 800;
    box-shadow: 0 0 16px -3px rgba(56, 189, 248, 0.4), 0 4px 8px -2px rgba(14, 165, 233, 0.25);
    transition: transform 0.15s, box-shadow 0.15s;
  }

  .nft-card:hover .buy-btn {
    box-shadow: 0 0 24px -2px rgba(56, 189, 248, 0.55), 0 6px 14px -2px rgba(14, 165, 233, 0.35);
  }
</style>
