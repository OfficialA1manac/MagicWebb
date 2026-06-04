-- +goose Up
-- +goose StatementBegin

-- ── notification + report enums ───────────────────────────────────────────
CREATE TYPE notification_kind AS ENUM (
    'outbid', 'offer_received', 'offer_accepted', 'offer_rejected',
    'auction_won', 'auction_lost', 'sold', 'listing_orphaned', 'system'
);
CREATE TYPE report_status AS ENUM ('open', 'reviewing', 'resolved', 'dismissed');

-- ── listings: multi-listing key (collection, token_id, seller) + orphaned ──
-- ERC-721 yields one effective listing per holder; ERC-1155 stacks per holder.
-- A listing is orphaned when the seller no longer holds the token (Transfer out).
ALTER TABLE listings ADD COLUMN orphaned BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE listings DROP CONSTRAINT listings_pkey;
ALTER TABLE listings ADD PRIMARY KEY (collection, token_id, seller);

-- ── offers: on-chain stacked-position model (replaces EIP-712 off-chain) ───
-- Bidder pays principal + 1.5% at make time; fee non-refundable. Positions
-- compound per (collection, token_id, bidder). No more signatures/nonces.
ALTER TABLE offers DROP COLUMN IF EXISTS signature;
ALTER TABLE offers DROP COLUMN IF EXISTS nonce;
ALTER TABLE offers RENAME COLUMN amount_wei TO principal_wei;
ALTER TABLE offers ADD COLUMN fee_wei   NUMERIC(78,0)  NOT NULL DEFAULT 0;
ALTER TABLE offers ADD COLUMN units     BIGINT         NOT NULL DEFAULT 1;
ALTER TABLE offers ADD COLUMN standard  token_standard NOT NULL DEFAULT 'erc721';
ALTER TABLE offers ADD COLUMN make_tx   CHAR(66);
ALTER TABLE offers ALTER COLUMN token_id SET NOT NULL;
-- One compounded position per bidder per token.
CREATE UNIQUE INDEX offers_position_uq ON offers (collection, token_id, bidder)
    WHERE status = 'pending';

-- ── nft_ownership ─────────────────────────────────────────────────────────
-- Authoritative owner map maintained from Transfer events. ERC-1155 rows hold
-- per-owner unit balances; ERC-721 rows have units = 1.
CREATE TABLE nft_ownership (
    collection      CHAR(42)        NOT NULL REFERENCES collections(address),
    token_id        NUMERIC(78,0)   NOT NULL,
    owner           CHAR(42)        NOT NULL,
    units           NUMERIC(78,0)   NOT NULL DEFAULT 1,
    standard        token_standard  NOT NULL,
    updated_at      TIMESTAMPTZ     NOT NULL DEFAULT now(),
    PRIMARY KEY (collection, token_id, owner)
);
CREATE INDEX nft_ownership_owner_idx ON nft_ownership (owner) WHERE units > 0;

-- ── tracked_collections ───────────────────────────────────────────────────
-- Auto-index registry: a collection seen for the first time (via Listed,
-- AuctionCreated, OfferMade, or Transfer) is registered here, backfilled, and
-- then watched forward.
CREATE TABLE tracked_collections (
    address             CHAR(42)        PRIMARY KEY REFERENCES collections(address),
    standard            token_standard  NOT NULL,
    first_seen_block    BIGINT          NOT NULL DEFAULT 0,
    last_indexed_block  BIGINT          NOT NULL DEFAULT 0,
    backfill_done       BOOLEAN         NOT NULL DEFAULT false,
    created_at          TIMESTAMPTZ     NOT NULL DEFAULT now()
);

-- ── nft_metadata ──────────────────────────────────────────────────────────
-- Resolved off-chain metadata, fetched lazily on first sight.
CREATE TABLE nft_metadata (
    collection      CHAR(42)        NOT NULL REFERENCES collections(address),
    token_id        NUMERIC(78,0)   NOT NULL,
    name            TEXT,
    description     TEXT,
    image_uri       TEXT,
    animation_uri   TEXT,
    metadata_uri    TEXT,
    raw             JSONB           NOT NULL DEFAULT '{}',
    fetched_at      TIMESTAMPTZ     NOT NULL DEFAULT now(),
    PRIMARY KEY (collection, token_id)
);

-- ── nft_attributes ────────────────────────────────────────────────────────
-- Flattened traits powering listing trait filters.
CREATE TABLE nft_attributes (
    collection      CHAR(42)        NOT NULL REFERENCES collections(address),
    token_id        NUMERIC(78,0)   NOT NULL,
    trait_type      TEXT            NOT NULL,
    value           TEXT            NOT NULL,
    PRIMARY KEY (collection, token_id, trait_type)
);
CREATE INDEX nft_attributes_filter_idx ON nft_attributes (collection, trait_type, value);

-- ── notifications ─────────────────────────────────────────────────────────
CREATE TABLE notifications (
    id          BIGSERIAL         PRIMARY KEY,
    user_addr   CHAR(42)          NOT NULL,
    kind        notification_kind NOT NULL,
    title       TEXT              NOT NULL,
    body        TEXT              NOT NULL DEFAULT '',
    link        TEXT,
    read        BOOLEAN           NOT NULL DEFAULT false,
    created_at  TIMESTAMPTZ       NOT NULL DEFAULT now()
);
CREATE INDEX notifications_user_idx ON notifications (user_addr, read, created_at DESC);

-- ── profiles ──────────────────────────────────────────────────────────────
-- Public-facing profile, distinct from the auth-bearing users row.
CREATE TABLE profiles (
    address         CHAR(42)        PRIMARY KEY,
    display_name    TEXT            NOT NULL DEFAULT '',
    bio             TEXT            NOT NULL DEFAULT '',
    avatar_uri      TEXT,
    banner_uri      TEXT,
    twitter         TEXT,
    website         TEXT,
    verified        BOOLEAN         NOT NULL DEFAULT false,
    updated_at      TIMESTAMPTZ     NOT NULL DEFAULT now()
);

-- ── reports ───────────────────────────────────────────────────────────────
CREATE TABLE reports (
    id          BIGSERIAL       PRIMARY KEY,
    reporter    CHAR(42)        NOT NULL,
    target_type TEXT            NOT NULL,             -- 'collection' | 'token' | 'listing' | 'user'
    target_id   TEXT            NOT NULL,             -- composite key as text
    reason      TEXT            NOT NULL,
    detail      TEXT            NOT NULL DEFAULT '',
    status      report_status   NOT NULL DEFAULT 'open',
    created_at  TIMESTAMPTZ     NOT NULL DEFAULT now()
);
CREATE INDEX reports_status_idx ON reports (status, created_at DESC);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TABLE IF EXISTS reports;
DROP TABLE IF EXISTS profiles;
DROP TABLE IF EXISTS notifications;
DROP TABLE IF EXISTS nft_attributes;
DROP TABLE IF EXISTS nft_metadata;
DROP TABLE IF EXISTS tracked_collections;
DROP TABLE IF EXISTS nft_ownership;

DROP INDEX IF EXISTS offers_position_uq;
ALTER TABLE offers DROP COLUMN IF EXISTS make_tx;
ALTER TABLE offers DROP COLUMN IF EXISTS standard;
ALTER TABLE offers DROP COLUMN IF EXISTS units;
ALTER TABLE offers DROP COLUMN IF EXISTS fee_wei;
ALTER TABLE offers RENAME COLUMN principal_wei TO amount_wei;
ALTER TABLE offers ADD COLUMN nonce     NUMERIC(78,0) NOT NULL DEFAULT 0;
ALTER TABLE offers ADD COLUMN signature TEXT          NOT NULL DEFAULT '';

ALTER TABLE listings DROP CONSTRAINT listings_pkey;
ALTER TABLE listings ADD PRIMARY KEY (collection, token_id);
ALTER TABLE listings DROP COLUMN IF EXISTS orphaned;

DROP TYPE IF EXISTS report_status;
DROP TYPE IF EXISTS notification_kind;

-- +goose StatementEnd
