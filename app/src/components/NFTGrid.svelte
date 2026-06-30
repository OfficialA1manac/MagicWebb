<script>
  import { onMount } from 'svelte';
  import NFTCard from './NFTCard.svelte';

  let items = [];
  let loading = true;
  let error = null;
  let count = 0;
  let sortBy = 'recent';
  let _fetchGen = 0;

  export let collection = '';
  export let seller = '';
  export let minPrice = '';
  export let maxPrice = '';
  export let traitFilters = '';

  async function fetchListings() {
    const gen = ++_fetchGen;
    loading = true;
    error = null;

    try {
      const params = new URLSearchParams({ limit: '48', sort: sortBy });
      if (collection) params.set('collection', collection);
      if (seller) params.set('seller', seller);
      if (minPrice) params.set('min_price', minPrice);
      if (maxPrice) params.set('max_price', maxPrice);
      if (traitFilters) params.set('traits', traitFilters);
      const res = await fetch(`/api/v1/listings?${params}`);
      if (!res.ok) throw new Error(`HTTP ${res.status}`);
      if (gen !== _fetchGen) return;

      const data = await res.json();
      if (gen !== _fetchGen) return;
      items = data;
      count = data.length;
    } catch (e) {
      if (gen !== _fetchGen) return;
      error = e.message || 'Failed to load listings';
      items = [];
    } finally {
      if (gen === _fetchGen) loading = false;
    }
  }

  onMount(() => {
    fetchListings();
  });

  function handleSortChange(e) {
    sortBy = e.target.value;
    fetchListings();
  }

  function openAppKit() {
    if (typeof window !== 'undefined' && window.__MW_APPKIT_OPEN__) {
      window.__MW_APPKIT_OPEN__();
    }
  }
</script>

<div class="grid-section">
  <div class="grid-header">
    <div class="header-left">
      <h2>{#if collection}Collection Listings{:else}Current Listings{/if}</h2>
      {#if !loading}
        <span class="count-badge">{count} item{count !== 1 ? 's' : ''}</span>
      {/if}
    </div>
    <div class="controls">
      <select class="sort-select" bind:value={sortBy} on:change={handleSortChange}>
        <option value="recent">Most Recent</option>
        <option value="price_asc">Price: Low to High</option>
        <option value="price_desc">Price: High to Low</option>
      </select>
      <button on:click={openAppKit} class="list-btn">
        ＋ List NFT
      </button>
    </div>
  </div>

  {#if loading}
    <div class="loading-grid">
      {#each Array(8) as _, i}
        <div class="card-skeleton" style="animation-delay: {i * 0.05}s">
          <div class="skeleton-image"></div>
          <div class="skeleton-body">
            <div class="skeleton-line" style="width:75%"></div>
            <div class="skeleton-line" style="width:50%"></div>
            <div class="skeleton-line" style="width:66%"></div>
          </div>
        </div>
      {/each}
    </div>
  {:else if error}
    <div class="error-card">
      <div style="font-size:2rem;margin-bottom:0.5rem;">⚠</div>
      <p style="font-size:1rem;font-weight:700;color:#fca5a5;">Failed to load listings</p>
      <p class="error-detail">{error}</p>
      <div style="display:flex;gap:0.5rem;margin-top:0.5rem;">
        <button class="retry-btn" on:click={fetchListings}>Retry</button>
        <button class="retry-btn secondary" on:click={() => { error = null; loading = true; fetchListings(); }}>Retry</button>
      </div>
    </div>
  {:else if items.length === 0}
    <div class="empty-card">
      <div style="font-size:3rem;margin-bottom:1rem;opacity:0.2;">✦</div>
      <p style="font-size:1.125rem;font-weight:700;color:rgba(255,255,255,0.4);">No active listings</p>
      <p style="font-size:0.8125rem;color:rgba(255,255,255,0.2);margin-top:0.25rem;">Be the first to list an NFT on the marketplace!</p>
      <button on:click={openAppKit} class="retry-btn" style="margin-top:0.75rem;">＋ List an NFT</button>
    </div>
  {:else}
    <div class="nft-grid">
      {#each items as item (item.collection + item.token_id + item.seller)}
        <NFTCard {item} />
      {/each}
    </div>
  {/if}
</div>

<style>
  .grid-section {
    max-width: 80rem;
    margin: 0 auto;
  }
  .grid-header {
    display: flex;
    align-items: center;
    justify-content: space-between;
    margin-bottom: 1.5rem;
    flex-wrap: wrap;
    gap: 0.75rem;
  }
  .header-left {
    display: flex;
    align-items: center;
    gap: 0.75rem;
  }
  .grid-header h2 {
    font-size: 1.25rem;
    font-weight: 800;
    color: #fafafa;
    margin: 0;
    letter-spacing: -0.02em;
  }
  .controls { display: flex; align-items: center; gap: 0.75rem; flex-wrap: wrap; }
  .count-badge {
    padding: 0.25rem 0.75rem;
    background: rgba(125, 211, 252, 0.1);
    border: 1px solid rgba(125, 211, 252, 0.2);
    border-radius: 2rem;
    color: #7dd3fc;
    font-size: 0.75rem;
    font-weight: 700;
  }
  .sort-select {
    padding: 0.5rem 0.75rem;
    border: 1px solid rgba(255, 255, 255, 0.1);
    border-radius: 0.5rem;
    background: rgba(15, 15, 19, 0.6);
    color: #fafafa;
    font-size: 0.8125rem;
    cursor: pointer;
    outline: none;
    font-family: inherit;
    transition: border-color 0.2s;
  }
  .sort-select:hover, .sort-select:focus {
    border-color: rgba(167, 139, 250, 0.4);
  }
  .list-btn {
    padding: 0.5rem 1rem;
    border-radius: 0.75rem;
    background: linear-gradient(135deg, #7dd3fc, #0ea5e9);
    color: #09090b;
    font-weight: 800;
    font-size: 0.8125rem;
    border: none;
    cursor: pointer;
    transition: all 0.2s;
    box-shadow: 0 0 22px -6px rgba(56,189,248,0.45), 0 4px 12px -4px rgba(14,165,233,0.3);
    font-family: inherit;
    display: inline-flex;
    align-items: center;
    gap: 0.375rem;
  }
  .list-btn:hover {
    opacity: 0.92;
    transform: scale(1.02);
  }
  .nft-grid {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(260px, 1fr));
    gap: 1.25rem;
  }
  .loading-grid {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(260px, 1fr));
    gap: 1.25rem;
  }
  .card-skeleton {
    background: rgba(15, 15, 19, 0.5);
    border-radius: 0.875rem;
    overflow: hidden;
    border: 1px solid rgba(255, 255, 255, 0.05);
    animation: shimmer 1.5s ease-in-out infinite;
  }
  @keyframes shimmer {
    0% { opacity: 0.3; }
    50% { opacity: 0.7; }
    100% { opacity: 0.3; }
  }
  .skeleton-image { aspect-ratio: 1; background: rgba(255, 255, 255, 0.03); }
  .skeleton-body { padding: 0.875rem; display: flex; flex-direction: column; gap: 0.5rem; }
  .skeleton-line {
    height: 0.75rem;
    background: rgba(255, 255, 255, 0.04);
    border-radius: 0.25rem;
  }
  .error-card, .empty-card {
    display: flex;
    flex-direction: column;
    align-items: center;
    gap: 0.5rem;
    padding: 4rem 1.5rem;
    text-align: center;
    border-radius: 1rem;
    background: rgba(15, 15, 19, 0.5);
    border: 1px solid rgba(255, 255, 255, 0.05);
  }
  .error-detail { font-size: 0.8125rem; color: rgba(255, 255, 255, 0.3); }
  .retry-btn {
    padding: 0.5rem 1.25rem;
    border-radius: 0.625rem;
    background: rgba(167, 139, 250, 0.1);
    border: 1px solid rgba(167, 139, 250, 0.25);
    color: #a78bfa;
    font-size: 0.8125rem;
    font-weight: 700;
    cursor: pointer;
    font-family: inherit;
    transition: all 0.2s;
  }
  .retry-btn:hover {
    background: rgba(167, 139, 250, 0.2);
  }
  .retry-btn.secondary {
    background: rgba(255, 255, 255, 0.04);
    border-color: rgba(255, 255, 255, 0.1);
    color: rgba(255, 255, 255, 0.5);
  }
</style>
