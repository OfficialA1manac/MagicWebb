-- +goose Up
-- +goose StatementBegin

-- ── listings: re-key on (collection, token_id, seller) + orphaned status ────
ALTER TABLE listings DROP CONSTRAINT listings_pkey;
ALTER TABLE listings ADD CONSTRAINT listings_pkey PRIMARY KEY (collection, token_id, seller);
ALTER TABLE listings ADD COLUMN orphaned BOOLEAN NOT NULL DEFAULT false;
CREATE INDEX idx_listings_orphaned ON listings (collection, token_id) WHERE orphaned;

-- ── nft_ownership: latest known holder, per (coll, id, owner) for ERC-1155 ──
CREATE TABLE nft_ownership (
    collection      CHAR(42)        NOT NULL,
    token_id        NUMERIC(78,0)   NOT NULL,
    owner           CHAR(42)        NOT NULL,
    balance         NUMERIC(78,0)   NOT NULL DEFAULT 1,
    updated_block   BIGINT          NOT NULL,
    updated_at      TIMESTAMPTZ     NOT NULL DEFAULT now(),
    PRIMARY KEY (collection, token_id, owner)
);
CREATE INDEX idx_nft_ownership_owner ON nft_ownership (owner);
CREATE INDEX idx_nft_ownership_coll  ON nft_ownership (collection);

-- ── tracked_collections: indexer auto-adds on first sight ──────────────────
CREATE TABLE tracked_collections (
    address           CHAR(42)    PRIMARY KEY REFERENCES collections(address) ON DELETE CASCADE,
    first_seen_block  BIGINT      NOT NULL,
    backfilled        BOOLEAN     NOT NULL DEFAULT false,
    verified          BOOLEAN     NOT NULL DEFAULT false,
    added_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- ── nft_metadata: split out so partial fetches don't bloat nft_tokens ──────
CREATE TABLE nft_metadata (
    collection      CHAR(42)        NOT NULL,
    token_id        NUMERIC(78,0)   NOT NULL,
    uri             TEXT,
    name            TEXT,
    description     TEXT,
    image_uri       TEXT,
    fetched_at      TIMESTAMPTZ,
    PRIMARY KEY (collection, token_id)
);

-- ── nft_attributes: queryable trait filters ────────────────────────────────
CREATE TABLE nft_attributes (
    collection      CHAR(42)        NOT NULL,
    token_id        NUMERIC(78,0)   NOT NULL,
    trait_type      TEXT            NOT NULL,
    value           TEXT            NOT NULL,
    PRIMARY KEY (collection, token_id, trait_type)
);
CREATE INDEX idx_nft_attributes_trait ON nft_attributes (collection, trait_type, value);

-- ── notifications: in-app bell ─────────────────────────────────────────────
CREATE TABLE notifications (
    id          BIGSERIAL       PRIMARY KEY,
    recipient   CHAR(42)        NOT NULL,
    kind        TEXT            NOT NULL,
    payload     JSONB           NOT NULL,
    read_at     TIMESTAMPTZ,
    created_at  TIMESTAMPTZ     NOT NULL DEFAULT now()
);
CREATE INDEX idx_notifications_unread ON notifications (recipient, created_at DESC) WHERE read_at IS NULL;
CREATE INDEX idx_notifications_recipient ON notifications (recipient, created_at DESC);

-- ── profiles: editable profile data ────────────────────────────────────────
CREATE TABLE profiles (
    address         CHAR(42)        PRIMARY KEY REFERENCES users(address) ON DELETE CASCADE,
    display_name    TEXT,
    bio             TEXT,
    avatar_uri      TEXT,
    banner_uri      TEXT,
    twitter         TEXT,
    website         TEXT,
    updated_at      TIMESTAMPTZ     NOT NULL DEFAULT now()
);

-- ── reports: user-submitted abuse / fraud ──────────────────────────────────
CREATE TABLE reports (
    id              BIGSERIAL       PRIMARY KEY,
    reporter        CHAR(42)        NOT NULL,
    target_kind     TEXT            NOT NULL,
    target_ref      TEXT            NOT NULL,
    reason          TEXT            NOT NULL,
    notes           TEXT,
    status          TEXT            NOT NULL DEFAULT 'open',
    created_at      TIMESTAMPTZ     NOT NULL DEFAULT now()
);
CREATE INDEX idx_reports_status ON reports (status, created_at DESC);

-- ── offer_positions: mirrors on-chain OfferPosition ────────────────────────
CREATE TABLE offer_positions (
    collection          CHAR(42)        NOT NULL,
    token_id            NUMERIC(78,0)   NOT NULL,
    bidder              CHAR(42)        NOT NULL,
    standard            token_standard  NOT NULL DEFAULT 'erc721',
    units               NUMERIC(78,0)   NOT NULL DEFAULT 1,
    total_offer_wei     NUMERIC(78,0)   NOT NULL,
    total_fee_wei       NUMERIC(78,0)   NOT NULL,
    first_at            TIMESTAMPTZ     NOT NULL,
    expires_at          TIMESTAMPTZ     NOT NULL,
    status              offer_status    NOT NULL DEFAULT 'pending',
    PRIMARY KEY (collection, token_id, bidder)
);
CREATE INDEX idx_offer_positions_token   ON offer_positions (collection, token_id) WHERE status='pending';
CREATE INDEX idx_offer_positions_bidder  ON offer_positions (bidder)               WHERE status='pending';
CREATE INDEX idx_offer_positions_expires ON offer_positions (expires_at)           WHERE status='pending';

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TABLE IF EXISTS offer_positions;
DROP TABLE IF EXISTS reports;
DROP TABLE IF EXISTS profiles;
DROP TABLE IF EXISTS notifications;
DROP TABLE IF EXISTS nft_attributes;
DROP TABLE IF EXISTS nft_metadata;
DROP TABLE IF EXISTS tracked_collections;
DROP TABLE IF EXISTS nft_ownership;

DROP INDEX IF EXISTS idx_listings_orphaned;
ALTER TABLE listings DROP COLUMN IF EXISTS orphaned;
ALTER TABLE listings DROP CONSTRAINT listings_pkey;
ALTER TABLE listings ADD CONSTRAINT listings_pkey PRIMARY KEY (collection, token_id);

-- +goose StatementEnd
