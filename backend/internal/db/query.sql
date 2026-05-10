-- name: GetUser :one
SELECT * FROM users WHERE address = $1;

-- name: UpsertUser :one
INSERT INTO users (address, username, bio, avatar_url)
VALUES ($1, $2, $3, $4)
ON CONFLICT (address) DO UPDATE
SET username   = COALESCE(EXCLUDED.username,   users.username),
    bio        = COALESCE(EXCLUDED.bio,        users.bio),
    avatar_url = COALESCE(EXCLUDED.avatar_url, users.avatar_url)
RETURNING *;

-- name: GetCollection :one
SELECT * FROM collections WHERE address = $1;

-- name: SearchCollections :many
SELECT * FROM collections
WHERE name ILIKE '%' || $1 || '%'
ORDER BY verified DESC, name
LIMIT $2;

-- name: GetToken :one
SELECT * FROM tokens WHERE collection = $1 AND token_id = $2;

-- name: TokensByOwner :many
SELECT * FROM tokens WHERE owner = $1 ORDER BY updated_at DESC LIMIT $2 OFFSET $3;

-- name: ActiveListing :one
SELECT * FROM listings
WHERE collection = $1 AND token_id = $2 AND status = 'active'
ORDER BY id DESC LIMIT 1;

-- name: ActiveListingsByCollection :many
SELECT * FROM listings
WHERE collection = $1 AND status = 'active'
ORDER BY id DESC LIMIT $2 OFFSET $3;

-- name: ActiveOffersForToken :many
SELECT * FROM offers
WHERE collection = $1 AND (token_id = $2 OR token_id IS NULL) AND status = 'active'
ORDER BY amount_wei DESC;

-- name: InsertOffer :one
INSERT INTO offers (collection, token_id, bidder, amount_wei, expires_at, signature, nonce)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING *;

-- name: CancelOffer :exec
UPDATE offers SET status = 'cancelled' WHERE id = $1 AND bidder = $2;

-- name: GetCursor :one
SELECT last_block FROM indexer_cursor WHERE chain_id = $1;

-- name: SetCursor :exec
UPDATE indexer_cursor SET last_block = $2 WHERE chain_id = $1;

-- name: InsertListingEvent :exec
INSERT INTO listings (collection, token_id, seller, price_wei, expires_at, tx_hash, block_number, log_index)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
ON CONFLICT (tx_hash, log_index) DO NOTHING;

-- name: MarkListingSold :exec
UPDATE listings SET status='sold' WHERE collection=$1 AND token_id=$2 AND status='active';

-- name: MarkListingCancelled :exec
UPDATE listings SET status='cancelled' WHERE collection=$1 AND token_id=$2 AND status='active';

-- name: InsertSale :exec
INSERT INTO sales (collection, token_id, seller, buyer, price_wei, fee_wei, source, tx_hash, block_number, log_index)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
ON CONFLICT (tx_hash, log_index) DO NOTHING;
