-- +goose Up
-- +goose StatementBegin
CREATE EXTENSION IF NOT EXISTS pg_trgm;
CREATE EXTENSION IF NOT EXISTS citext;

CREATE TABLE users (
  address       CITEXT PRIMARY KEY,
  username      TEXT UNIQUE,
  bio           TEXT,
  avatar_url    TEXT,
  created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE collections (
  address       CITEXT PRIMARY KEY,
  chain_id      INT  NOT NULL,
  name          TEXT NOT NULL,
  symbol        TEXT,
  verified      BOOL NOT NULL DEFAULT false,
  created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX collections_name_trgm ON collections USING GIN (name gin_trgm_ops);

CREATE TABLE tokens (
  collection    CITEXT NOT NULL REFERENCES collections(address),
  token_id      NUMERIC(78,0) NOT NULL,
  owner         CITEXT NOT NULL REFERENCES users(address),
  metadata_uri  TEXT,
  image_url     TEXT,
  name          TEXT,
  updated_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (collection, token_id)
);
CREATE INDEX tokens_owner_idx ON tokens(owner);

CREATE TYPE listing_status AS ENUM ('active','sold','cancelled','expired');
CREATE TABLE listings (
  id            BIGSERIAL PRIMARY KEY,
  collection    CITEXT NOT NULL,
  token_id      NUMERIC(78,0) NOT NULL,
  seller        CITEXT NOT NULL,
  price_wei     NUMERIC(78,0) NOT NULL,
  expires_at    TIMESTAMPTZ NOT NULL,
  status        listing_status NOT NULL DEFAULT 'active',
  tx_hash       BYTEA,
  block_number  BIGINT,
  log_index     INT,
  created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
  FOREIGN KEY (collection, token_id) REFERENCES tokens(collection, token_id),
  UNIQUE (tx_hash, log_index)
);
CREATE INDEX listings_active_idx ON listings(collection, token_id) WHERE status='active';
CREATE INDEX listings_seller_idx ON listings(seller) WHERE status='active';

CREATE TYPE auction_status AS ENUM ('active','settled','cancelled','expired');
CREATE TABLE auctions (
  id            BIGSERIAL PRIMARY KEY,
  onchain_id    NUMERIC(78,0) UNIQUE NOT NULL,
  collection    CITEXT NOT NULL,
  token_id      NUMERIC(78,0) NOT NULL,
  seller        CITEXT NOT NULL,
  reserve_wei   NUMERIC(78,0) NOT NULL,
  min_increment_bps INT NOT NULL DEFAULT 500,
  starts_at     TIMESTAMPTZ NOT NULL,
  ends_at       TIMESTAMPTZ NOT NULL,
  status        auction_status NOT NULL DEFAULT 'active',
  highest_bid_wei NUMERIC(78,0),
  highest_bidder  CITEXT,
  created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX auctions_active_endsat_idx ON auctions(ends_at) WHERE status='active';

CREATE TABLE bids (
  id            BIGSERIAL PRIMARY KEY,
  auction_id    BIGINT NOT NULL REFERENCES auctions(id),
  bidder        CITEXT NOT NULL,
  amount_wei    NUMERIC(78,0) NOT NULL,
  tx_hash       BYTEA NOT NULL,
  block_number  BIGINT NOT NULL,
  log_index     INT NOT NULL,
  created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (tx_hash, log_index)
);
CREATE INDEX bids_auction_idx ON bids(auction_id, amount_wei DESC);

CREATE TYPE offer_status AS ENUM ('active','accepted','cancelled','expired');
CREATE TABLE offers (
  id            BIGSERIAL PRIMARY KEY,
  collection    CITEXT NOT NULL,
  token_id      NUMERIC(78,0),
  bidder        CITEXT NOT NULL,
  amount_wei    NUMERIC(78,0) NOT NULL,
  expires_at    TIMESTAMPTZ NOT NULL,
  signature     BYTEA NOT NULL,
  nonce         NUMERIC(78,0) NOT NULL,
  status        offer_status NOT NULL DEFAULT 'active',
  created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (bidder, nonce)
);
CREATE INDEX offers_target_idx ON offers(collection, token_id) WHERE status='active';
CREATE INDEX offers_bidder_idx ON offers(bidder) WHERE status='active';

CREATE TABLE sales (
  id            BIGSERIAL PRIMARY KEY,
  collection    CITEXT NOT NULL,
  token_id      NUMERIC(78,0) NOT NULL,
  seller        CITEXT NOT NULL,
  buyer         CITEXT NOT NULL,
  price_wei     NUMERIC(78,0) NOT NULL,
  fee_wei       NUMERIC(78,0) NOT NULL,
  source        TEXT NOT NULL CHECK (source IN ('listing','auction','offer')),
  tx_hash       BYTEA NOT NULL,
  block_number  BIGINT NOT NULL,
  log_index     INT NOT NULL,
  created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (tx_hash, log_index)
);
CREATE INDEX sales_collection_idx ON sales(collection, created_at DESC);

CREATE TABLE indexer_cursor (
  chain_id      INT PRIMARY KEY,
  last_block    BIGINT NOT NULL
);
INSERT INTO indexer_cursor(chain_id, last_block) VALUES (114, 0);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS indexer_cursor, sales, offers, bids, auctions, listings, tokens, collections, users CASCADE;
DROP TYPE IF EXISTS offer_status, auction_status, listing_status;
-- +goose StatementEnd
