-- +goose Up
-- +goose StatementBegin
-- Tracks keeper progress returning losing bidders' escrow after an auction
-- settles/cancels. AuctionHouse v2 escrows every bid until settlement and does
-- NOT auto-refund on outbid, so a separate keeper pass calls refundLosers(id,batch)
-- once the auction is finalized. These columns let the keeper find auctions still
-- owing refunds and throttle re-attempts (refundLosers is idempotent on-chain).
ALTER TABLE auctions
    ADD COLUMN losers_refunded  BOOLEAN     NOT NULL DEFAULT FALSE,
    ADD COLUMN refund_attempt_at TIMESTAMPTZ;

-- Hot path for the refund sweeper: only finalized auctions still owing refunds.
CREATE INDEX idx_auctions_refund_pending
    ON auctions (auction_id)
    WHERE status IN ('settled', 'cancelled') AND NOT losers_refunded;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_auctions_refund_pending;
ALTER TABLE auctions
    DROP COLUMN IF EXISTS losers_refunded,
    DROP COLUMN IF EXISTS refund_attempt_at;
-- +goose StatementEnd
