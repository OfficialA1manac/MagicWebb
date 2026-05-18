-- +goose Up
-- +goose StatementBegin

-- ── listings ──────────────────────────────────────────────────────────────
CREATE INDEX idx_listings_active_collection ON listings (collection) WHERE active = true;
CREATE INDEX idx_listings_active_seller     ON listings (seller)     WHERE active = true;
CREATE INDEX idx_listings_expires_at        ON listings (expires_at) WHERE active = true;
CREATE INDEX idx_listings_price             ON listings (price_wei)  WHERE active = true;

-- ── auctions ──────────────────────────────────────────────────────────────
CREATE INDEX idx_auctions_active     ON auctions (ends_at) WHERE status = 'active';
CREATE INDEX idx_auctions_collection ON auctions (collection, status);
CREATE INDEX idx_auctions_seller     ON auctions (seller, status);

-- ── bids ──────────────────────────────────────────────────────────────────
CREATE INDEX idx_bids_auction ON bids (auction_id, placed_at DESC);
CREATE INDEX idx_bids_bidder  ON bids (bidder, placed_at DESC);

-- ── offers ────────────────────────────────────────────────────────────────
CREATE INDEX idx_offers_collection_token ON offers (collection, token_id) WHERE status = 'pending';
CREATE INDEX idx_offers_bidder           ON offers (bidder)               WHERE status = 'pending';
CREATE INDEX idx_offers_expires_at       ON offers (expires_at)           WHERE status = 'pending';

-- ── sales ─────────────────────────────────────────────────────────────────
CREATE INDEX idx_sales_collection ON sales (collection, occurred_at DESC);
CREATE INDEX idx_sales_seller     ON sales (seller, occurred_at DESC);
CREATE INDEX idx_sales_buyer      ON sales (buyer, occurred_at DESC);

-- ── nft_tokens ────────────────────────────────────────────────────────────
CREATE INDEX idx_nft_tokens_owner      ON nft_tokens (owner);
CREATE INDEX idx_nft_tokens_collection ON nft_tokens (collection);

-- ── trending_scores ───────────────────────────────────────────────────────
CREATE INDEX idx_trending_window_score ON trending_scores (window, score DESC);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP INDEX IF EXISTS idx_trending_window_score;
DROP INDEX IF EXISTS idx_nft_tokens_collection;
DROP INDEX IF EXISTS idx_nft_tokens_owner;
DROP INDEX IF EXISTS idx_sales_buyer;
DROP INDEX IF EXISTS idx_sales_seller;
DROP INDEX IF EXISTS idx_sales_collection;
DROP INDEX IF EXISTS idx_offers_expires_at;
DROP INDEX IF EXISTS idx_offers_bidder;
DROP INDEX IF EXISTS idx_offers_collection_token;
DROP INDEX IF EXISTS idx_bids_bidder;
DROP INDEX IF EXISTS idx_bids_auction;
DROP INDEX IF EXISTS idx_auctions_seller;
DROP INDEX IF EXISTS idx_auctions_collection;
DROP INDEX IF EXISTS idx_auctions_active;
DROP INDEX IF EXISTS idx_listings_price;
DROP INDEX IF EXISTS idx_listings_expires_at;
DROP INDEX IF EXISTS idx_listings_active_seller;
DROP INDEX IF EXISTS idx_listings_active_collection;

-- +goose StatementEnd
