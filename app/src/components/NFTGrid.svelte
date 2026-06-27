<script>
  import { onMount } from 'svelte';
  import NFTCard from './NFTCard.svelte';

  let items = [];
  let loading = true;
  let error = null;
  let count = 0;
  let sortBy = 'recent';
  let _fetchGen = 0;

  async function fetchListings() {
    const gen = ++_fetchGen;
    loading = true;
    error = null;

    try {
      const params = new URLSearchParams({ limit: '48', sort: sortBy });
      const res = await fetch(`/api/v1/listings?${params}`);
      if (!res.ok) throw new Error(`HTTP ${res.status}`);
      if (gen !== _fetchGen) return; // stale response

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
</script>

<div class="grid-section">
  <div class="grid-header">
    <h2>Listings</h2>
    <div class="controls">
      {#if !loading}
        <span class="count-badge">{count} loaded</span>
      {/if}
      <select class="sort-select" bind:value={sortBy} on:change={handleSortChange}>
        <option value="recent">Most Recent</option>
        <option value="price_asc">Price: Low to High</option>
        <option value="price_desc">Price: High to Low</option>
      </select>
    </div>
  </div>

  {#if loading}
    <div class="loading-grid">
      {#each Array(8) as _, i}
        <div class="card-skeleton" style="animation-delay: {i * 0.05}s">
          <div class="skeleton-image" />
          <div class="skeleton-body">
            <div class="skeleton-line" style="width:75%" />
            <div class="skeleton-line" style="width:50%" />
            <div class="skeleton-line" style="width:66%" />
          </div>
        </div>
      {/each}
    </div>
  {:else if error}
    <div class="error-card">
      <p>Failed to load listings</p>
      <p class="error-detail">{error}</p>
      <button class="retry-btn" on:click={fetchListings}>Retry</button>
    </div>
  {:else if items.length === 0}
    <div class="empty-card">
      <p>No active listings</p>
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
    padding: 1.5rem;
  }
  .grid-header {
    display: flex;
    align-items: center;
    justify-content: space-between;
    margin-bottom: 1.5rem;
  }
  .grid-header h2 {
    font-size: 1.5rem;
    font-weight: 700;
    color: #e2e8f0;
    margin: 0;
  }
  .controls { display: flex; align-items: center; gap: 0.75rem; }
  .count-badge {
    padding: 0.25rem 0.625rem;
    background: rgba(139, 92, 246, 0.15);
    border: 1px solid rgba(139, 92, 246, 0.2);
    border-radius: 2rem;
    color: #a78bfa;
    font-size: 0.8125rem;
    font-weight: 500;
  }
  .sort-select {
    padding: 0.375rem 0.75rem;
    border: 1px solid rgba(148, 163, 184, 0.2);
    border-radius: 0.5rem;
    background: rgba(30, 41, 59, 0.6);
    color: #e2e8f0;
    font-size: 0.8125rem;
    cursor: pointer;
    outline: none;
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
    background: rgba(30, 41, 59, 0.4);
    border-radius: 0.875rem;
    overflow: hidden;
    animation: shimmer 1.5s infinite;
  }
  @keyframes shimmer {
    0% { opacity: 0.6; }
    50% { opacity: 1; }
    100% { opacity: 0.6; }
  }
  .skeleton-image { aspect-ratio: 1; background: rgba(148, 163, 184, 0.1); }
  .skeleton-body { padding: 0.875rem; display: flex; flex-direction: column; gap: 0.5rem; }
  .skeleton-line {
    height: 0.75rem;
    background: rgba(148, 163, 184, 0.1);
    border-radius: 0.25rem;
  }
  .error-card, .empty-card {
    display: flex;
    flex-direction: column;
    align-items: center;
    gap: 0.75rem;
    padding: 4rem 1.5rem;
    text-align: center;
    color: #94a3b8;
  }
  .error-detail { font-size: 0.8125rem; color: #64748b; }
  .retry-btn {
    padding: 0.5rem 1.25rem;
    border: 1px solid rgba(139, 92, 246, 0.3);
    border-radius: 0.5rem;
    background: rgba(139, 92, 246, 0.1);
    color: #a78bfa;
    font-size: 0.875rem;
    cursor: pointer;
  }
  .retry-btn:hover {
    background: rgba(139, 92, 246, 0.2);
  }
</style>
