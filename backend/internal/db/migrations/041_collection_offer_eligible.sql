-- 041_collection_offer_eligible.sql
--
-- Mirrors OfferBook.offerEligible[collection] (v3.4): the owner must opt a
-- collection in before makeOffer() succeeds on it. Written by the indexer from
-- OfferEligibilitySet(address indexed coll, bool indexed eligible) so the UI
-- can hide the "make offer" CTA on collections the contract would revert on.
--
-- Default false matches the contract's zero-value mapping: a collection with
-- no event yet is not eligible. Historical events are replayed by the indexer
-- from the contract's deploy block, so no backfill is needed here.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE collections
  ADD COLUMN IF NOT EXISTS offer_eligible BOOLEAN NOT NULL DEFAULT false;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE collections DROP COLUMN IF EXISTS offer_eligible;
-- +goose StatementEnd
