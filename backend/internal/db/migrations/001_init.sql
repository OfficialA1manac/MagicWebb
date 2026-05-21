-- +goose Up
-- +goose StatementBegin

-- ── Extensions ────────────────────────────────────────────────────────────
CREATE EXTENSION IF NOT EXISTS "uuid-ossp";
CREATE EXTENSION IF NOT EXISTS "pgcrypto";

-- ── Enums ─────────────────────────────────────────────────────────────────
CREATE TYPE token_standard AS ENUM ('erc721', 'erc1155');
CREATE TYPE offer_status    AS ENUM ('pending', 'accepted', 'cancelled', 'expired');
CREATE TYPE auction_status  AS ENUM ('active', 'settled', 'cancelled');

-- ── collections ───────────────────────────────────────────────────────────
CREATE TABLE collections (
    address         CHAR(42)        PRIMARY KEY,          -- checksummed EIP-55
    name            TEXT            NOT NULL DEFAULT '',
    symbol          TEXT            NOT NULL DEFAULT '',
    standard        token_standard  NOT NULL,
    deploy_block    BIGINT          NOT NULL DEFAULT 0,
    tracked         BOOLEAN         NOT NULL DEFAULT true,
    created_at      TIMESTAMPTZ     NOT NULL DEFAULT now()
);

-- ── nft_tokens ────────────────────────────────────────────────────────────
CREATE TABLE nft_tokens (
    collection      CHAR(42)        NOT NULL REFERENCES collections(address),
    token_id        NUMERIC(78,0)   NOT NULL,             -- uint256
    owner           CHAR(42),
    metadata_uri    TEXT,
    name            TEXT,
    description     TEXT,
    image_uri       TEXT,
    attributes      JSONB           NOT NULL DEFAULT '[]',
    views           BIGINT          NOT NULL DEFAULT 0,
    last_synced_at  TIMESTAMPTZ,
    PRIMARY KEY (collection, token_id)
);

-- ── listings ──────────────────────────────────────────────────────────────
CREATE TABLE listings (
    collection      CHAR(42)        NOT NULL REFERENCES collections(address),
    token_id        NUMERIC(78,0)   NOT NULL,
    seller          CHAR(42)        NOT NULL,
    price_wei       NUMERIC(78,0)   NOT NULL,
    amount          BIGINT          NOT NULL DEFAULT 1,
    standard        token_standard  NOT NULL,
    expires_at      TIMESTAMPTZ     NOT NULL,
    listed_at       TIMESTAMPTZ     NOT NULL,
    tx_hash         CHAR(66)        NOT NULL,
    active          BOOLEAN         NOT NULL DEFAULT true,
    PRIMARY KEY (collection, token_id)
);

-- ── auctions ──────────────────────────────────────────────────────────────
CREATE TABLE auctions (
    auction_id          BIGINT          PRIMARY KEY,      -- on-chain ID
    collection          CHAR(42)        NOT NULL REFERENCES collections(address),
    token_id            NUMERIC(78,0)   NOT NULL,
    seller              CHAR(42)        NOT NULL,
    standard            token_standard  NOT NULL,
    reserve_price_wei   NUMERIC(78,0)   NOT NULL,
    highest_bid_wei     NUMERIC(78,0)   NOT NULL DEFAULT 0,
    highest_bidder      CHAR(42),
    min_increment_bps   INT             NOT NULL DEFAULT 500,
    starts_at           TIMESTAMPTZ     NOT NULL,
    ends_at             TIMESTAMPTZ     NOT NULL,
    status              auction_status  NOT NULL DEFAULT 'active',
    create_tx           CHAR(66)        NOT NULL,
    settle_tx           CHAR(66),
    created_at          TIMESTAMPTZ     NOT NULL DEFAULT now()
);

-- ── bids ──────────────────────────────────────────────────────────────────
CREATE TABLE bids (
    id          BIGSERIAL       PRIMARY KEY,
    auction_id  BIGINT          NOT NULL REFERENCES auctions(auction_id),
    bidder      CHAR(42)        NOT NULL,
    amount_wei  NUMERIC(78,0)   NOT NULL,
    tx_hash     CHAR(66)        NOT NULL UNIQUE,
    placed_at   TIMESTAMPTZ     NOT NULL
);

-- ── offers ────────────────────────────────────────────────────────────────
CREATE TABLE offers (
    offer_id        UUID            PRIMARY KEY DEFAULT uuid_generate_v4(),
    bidder          CHAR(42)        NOT NULL,
    collection      CHAR(42)        NOT NULL REFERENCES collections(address),
    -- NULL token_id = collection-wide offer (ERC721 only)
    token_id        NUMERIC(78,0),
    amount_wei      NUMERIC(78,0)   NOT NULL,
    nonce           NUMERIC(78,0)   NOT NULL,
    expires_at      TIMESTAMPTZ     NOT NULL,
    signature       TEXT            NOT NULL,             -- 65-byte EIP-712 sig, hex
    status          offer_status    NOT NULL DEFAULT 'pending',
    accept_tx       CHAR(66),
    created_at      TIMESTAMPTZ     NOT NULL DEFAULT now()
);

-- ── users ─────────────────────────────────────────────────────────────────
CREATE TABLE users (
    address         CHAR(42)        PRIMARY KEY,
    siwe_nonce      TEXT,
    nonce_expires   TIMESTAMPTZ,
    display_name    TEXT,
    avatar_uri      TEXT,
    created_at      TIMESTAMPTZ     NOT NULL DEFAULT now(),
    last_seen_at    TIMESTAMPTZ     NOT NULL DEFAULT now()
);

-- ── sales ─────────────────────────────────────────────────────────────────
CREATE TABLE sales (
    id              BIGSERIAL       PRIMARY KEY,
    collection      CHAR(42)        NOT NULL REFERENCES collections(address),
    token_id        NUMERIC(78,0)   NOT NULL,
    seller          CHAR(42)        NOT NULL,
    buyer           CHAR(42)        NOT NULL,
    price_wei       NUMERIC(78,0)   NOT NULL,
    fee_wei         NUMERIC(78,0)   NOT NULL,
    royalty_wei     NUMERIC(78,0)   NOT NULL DEFAULT 0,
    tx_hash         CHAR(66)        NOT NULL UNIQUE,
    block_number    BIGINT          NOT NULL,
    occurred_at     TIMESTAMPTZ     NOT NULL
);

-- ── royalties ─────────────────────────────────────────────────────────────
-- Mirrors on-chain RoyaltyRegistry state for fast reads.
CREATE TABLE royalties (
    collection      CHAR(42)        NOT NULL REFERENCES collections(address),
    token_id        NUMERIC(78,0),  -- NULL = collection-level default
    receiver        CHAR(42)        NOT NULL,
    fee_bps         INT             NOT NULL CHECK (fee_bps BETWEEN 0 AND 10000),
    PRIMARY KEY (collection, token_id)
);

-- ── trending_scores ───────────────────────────────────────────────────────
-- Materialized by the score worker (Zig hot-path) every minute.
CREATE TABLE trending_scores (
    collection          CHAR(42)        NOT NULL REFERENCES collections(address),
    "window"            TEXT            NOT NULL CHECK ("window" IN ('1h','24h','7d')),
    score               DOUBLE PRECISION NOT NULL DEFAULT 0,
    views               BIGINT          NOT NULL DEFAULT 0,
    bids                BIGINT          NOT NULL DEFAULT 0,
    volume_wei          NUMERIC(78,0)   NOT NULL DEFAULT 0,
    computed_at         TIMESTAMPTZ     NOT NULL DEFAULT now(),
    PRIMARY KEY (collection, "window")
);

-- ── indexer_state ─────────────────────────────────────────────────────────
CREATE TABLE indexer_state (
    chain_id        INT             PRIMARY KEY,
    indexed_block   BIGINT          NOT NULL DEFAULT 0,
    updated_at      TIMESTAMPTZ     NOT NULL DEFAULT now()
);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TABLE IF EXISTS indexer_state;
DROP TABLE IF EXISTS trending_scores;
DROP TABLE IF EXISTS royalties;
DROP TABLE IF EXISTS sales;
DROP TABLE IF EXISTS users;
DROP TABLE IF EXISTS offers;
DROP TABLE IF EXISTS bids;
DROP TABLE IF EXISTS auctions;
DROP TABLE IF EXISTS listings;
DROP TABLE IF EXISTS nft_tokens;
DROP TABLE IF EXISTS collections;
DROP TYPE  IF EXISTS auction_status;
DROP TYPE  IF EXISTS offer_status;
DROP TYPE  IF EXISTS token_standard;
DROP EXTENSION IF EXISTS "pgcrypto";
DROP EXTENSION IF EXISTS "uuid-ossp";

-- +goose StatementEnd
