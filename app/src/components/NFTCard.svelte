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

  // Format price from wei to a readable value
  let priceFormatted = $derived(() => {
    const wei = BigInt(item.price_wei);
    const divisor = BigInt(10) ** BigInt(18);
    const whole = wei / divisor;
    const remainder = wei % divisor;
    const remainderStr = remainder.toString().padStart(18, '0').slice(0, 4);
    return `${whole}.${remainderStr}`;
  });

  // Truncate address for display
  let sellerShort = $derived(
    `${item.seller.slice(0, 6)}...${item.seller.slice(-4)}`
  );

  // Handle image error
  let imageError = $state(false);

  // Image proxy URL (Go backend serves proxied IPFS/images)
  let imageSrc = $derived(
    item.image_uri
      ? `/api/v1/media?uri=${encodeURIComponent(item.image_uri)}`
      : ''
  );
</script>

<div
  class="nft-card"
  transition:fly={{ y: 20, duration: 400, delay: Math.random() * 200 }}
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

    {#if item.collection_verified}
      <span class="verified-badge" title="Verified Collection">✓</span>
    {/if}

    {#if item.standard === 'erc1155' && item.amount > 1}
      <span class="amount-badge">x{item.amount}</span>
    {/if}
  </div>

  <div class="card-body">
    <h3 class="token-name">{item.name || `Token #${item.token_id}`}</h3>

    <div class="price-row">
      <span class="price-label">Price</span>
      <span class="price-value">{priceFormatted()} C2FLR</span>
    </div>

    <div class="seller-row">
      <span class="seller-label">Seller</span>
      <span class="seller-value">{sellerShort}</span>
    </div>
  </div>
</div>

<style>
  .nft-card {
    background: rgba(30, 41, 59, 0.6);
    border: 1px solid rgba(148, 163, 184, 0.1);
    border-radius: 0.875rem;
    overflow: hidden;
    transition: all 0.3s ease;
    backdrop-filter: blur(8px);
  }

  .nft-card:hover {
    border-color: rgba(139, 92, 246, 0.3);
    transform: translateY(-4px);
    box-shadow: 0 12px 40px rgba(139, 92, 246, 0.15);
  }

  .image-wrapper {
    position: relative;
    aspect-ratio: 1;
    overflow: hidden;
    background: rgba(15, 23, 42, 0.5);
  }

  .image-wrapper img {
    width: 100%;
    height: 100%;
    object-fit: cover;
    transition: transform 0.3s ease;
  }

  .nft-card:hover .image-wrapper img {
    transform: scale(1.05);
  }

  .image-placeholder {
    width: 100%;
    height: 100%;
    display: flex;
    align-items: center;
    justify-content: center;
    color: #475569;
  }

  .image-placeholder svg {
    width: 3rem;
    height: 3rem;
  }

  .verified-badge {
    position: absolute;
    top: 0.5rem;
    left: 0.5rem;
    width: 1.5rem;
    height: 1.5rem;
    background: #3b82f6;
    border-radius: 50%;
    display: flex;
    align-items: center;
    justify-content: center;
    font-size: 0.75rem;
    font-weight: 700;
    color: white;
    box-shadow: 0 2px 8px rgba(59, 130, 246, 0.4);
  }

  .amount-badge {
    position: absolute;
    bottom: 0.5rem;
    right: 0.5rem;
    padding: 0.1875rem 0.5rem;
    background: rgba(0, 0, 0, 0.6);
    border-radius: 0.375rem;
    color: #e2e8f0;
    font-size: 0.75rem;
    font-weight: 600;
    backdrop-filter: blur(4px);
  }

  .card-body {
    padding: 0.875rem;
    display: flex;
    flex-direction: column;
    gap: 0.5rem;
  }

  .token-name {
    font-size: 0.9375rem;
    font-weight: 600;
    color: #e2e8f0;
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
    margin: 0;
  }

  .price-row, .seller-row {
    display: flex;
    justify-content: space-between;
    align-items: center;
  }

  .price-label, .seller-label {
    font-size: 0.75rem;
    color: #64748b;
    text-transform: uppercase;
    letter-spacing: 0.05em;
  }

  .price-value {
    font-size: 0.875rem;
    font-weight: 600;
    color: #a78bfa;
  }

  .seller-value {
    font-size: 0.8125rem;
    color: #94a3b8;
    font-family: 'SF Mono', 'Fira Code', monospace;
  }
</style>
