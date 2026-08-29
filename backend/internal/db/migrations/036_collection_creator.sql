-- 036_collection_creator.sql
-- The verifier sweep (internal/verifier) already batches eth_calls per
-- collection for supportsInterface/name/symbol; an ERC-173 owner() probe
-- rides along for free and gives every collection a creator to display.
--
--   creator_addr  the contract's ERC-173 owner() at last probe, stored as a
--                 lowercase 0x-prefixed hex address. NULL = never resolved
--                 (contract has no owner(), owner is the zero address, or the
--                 probe has not run yet). The sweeper never overwrites a
--                 non-NULL value with an empty result, so a chain blip cannot
--                 blank a known creator.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE collections
  ADD COLUMN IF NOT EXISTS creator_addr CHAR(42);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE collections DROP COLUMN IF EXISTS creator_addr;
-- +goose StatementEnd
