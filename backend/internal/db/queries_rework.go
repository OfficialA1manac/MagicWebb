package db

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/OfficialA1manac/MagicWebb/backend/internal/imagestore"
)

const zeroAddr = "0x0000000000000000000000000000000000000000"

func lcStandard(s string) string {
	s = strings.ToLower(s)
	if s != "erc1155" {
		return "erc721"
	}
	return "erc1155"
}

// ── Collection auto-indexing ───────────────────────────────────────────────

// ListDistinctCollectionsFromTokens returns every unique collection address that
// appears in nft_tokens. Used at startup to seed tracked_collections for
// collections that were ever listed, auctioned, or transferred — even if their
// tracked_collections row was lost or the collection was never explicitly tracked.
func (q *Q) ListDistinctCollectionsFromTokens(ctx context.Context) ([]string, error) {
	rows, err := q.reader().Query(ctx, `SELECT DISTINCT collection FROM nft_tokens`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var a string
		if err := rows.Scan(&a); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// EnsureCollection registers a collection (and its tracked_collections row) the
// first time it is seen via any marketplace event. Idempotent.
func (q *Q) EnsureCollection(ctx context.Context, addr, standard string, block uint64) error {
	std := lcStandard(standard)
	if _, err := q.writer().Exec(ctx,
		`INSERT INTO collections(address, standard, deploy_block, tracked)
		 VALUES($1,$2,$3,true)
		 ON CONFLICT(address) DO NOTHING`,
		addr, std, block); err != nil {
		return err
	}
	_, err := q.writer().Exec(ctx,
		`INSERT INTO tracked_collections(address, standard, first_seen_block, last_indexed_block)
		 VALUES($1,$2,$3,$3)
		 ON CONFLICT(address) DO NOTHING`,
		addr, std, block)
	return err
}

// SeedTrackedCollections ensures every address in `addrs` has a row in
// tracked_collections by calling EnsureCollection for each one. Standard
// defaults to 'erc721' when unknown. Errors are logged but not fatal — a
// single unreachable collection won't block the rest of the seed.
// Returns the count of newly-seeded addresses.
func (q *Q) SeedTrackedCollections(ctx context.Context, addrs []string) int {
	var seeded int
	for _, addr := range addrs {
		addr = strings.ToLower(strings.TrimSpace(addr))
		if len(addr) != 42 || addr[:2] != "0x" {
			continue
		}
		if err := q.EnsureCollection(ctx, addr, "erc721", 0); err != nil {
			// Logged by the caller; a single failure doesn't block the rest.
			continue
		}
		seeded++
	}
	return seeded
}

// ListTrackedCollections returns every collection the indexer watches for transfers.
func (q *Q) ListTrackedCollections(ctx context.Context) ([]string, error) {
	rows, err := q.reader().Query(ctx, `SELECT address FROM tracked_collections`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var a string
		if err := rows.Scan(&a); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// ── IDX-2: Per-collection indexer checkpoints ────────────────────────────────

// TrackedCollection holds one tracked collection with its indexer checkpoint.
type TrackedCollection struct {
	Address          string
	LastScannedBlock uint64
}

// ListTrackedCollectionsWithCheckpoints returns every tracked collection with
// its last_scanned_block. Used by the indexer to determine per-collection
// catch-up ranges. Collections with last_scanned_block=0 need full backfill.
func (q *Q) ListTrackedCollectionsWithCheckpoints(ctx context.Context) ([]TrackedCollection, error) {
	rows, err := q.reader().Query(ctx,
		`SELECT address, COALESCE(last_scanned_block, 0)
		   FROM tracked_collections WHERE tracked = true
		 ORDER BY last_scanned_block ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TrackedCollection
	for rows.Next() {
		var tc TrackedCollection
		if err := rows.Scan(&tc.Address, &tc.LastScannedBlock); err != nil {
			return nil, err
		}
		out = append(out, tc)
	}
	return out, rows.Err()
}

// GetMinCollectionCheckpoint returns the minimum last_scanned_block across all
// tracked collections. Used on indexer restart to determine the safe starting
// block — the indexer must re-scan from the earliest collection's checkpoint
// so no Transfer events are missed.
func (q *Q) GetMinCollectionCheckpoint(ctx context.Context) (uint64, error) {
	var block uint64
	err := q.reader().QueryRow(ctx,
		`SELECT COALESCE(MIN(last_scanned_block), 0)
		   FROM tracked_collections WHERE tracked = true`).Scan(&block)
	return block, err
}

// SetCollectionCheckpoint updates the last_scanned_block for a single tracked
// collection. Called after processTransfers successfully indexes a block range
// for a specific collection.
func (q *Q) SetCollectionCheckpoint(ctx context.Context, address string, block uint64) error {
	_, err := q.writer().Exec(ctx,
		`UPDATE tracked_collections
		   SET last_scanned_block = $2, updated_at = now()
		 WHERE address = $1`, address, block)
	return err
}

// SetCollectionCheckpointsBatch updates last_scanned_block for all tracked
// collections in one query. Called after a full processTransfers range
// completes successfully — all collections advance to the same block.
func (q *Q) SetCollectionCheckpointsBatch(ctx context.Context, block uint64) error {
	_, err := q.writer().Exec(ctx,
		`UPDATE tracked_collections
		   SET last_scanned_block = $1, updated_at = now()
		 WHERE tracked = true`, block)
	return err
}

// ── Ownership + orphaning (from Transfer events) ───────────────────────────

func (q *Q) GetTokenOwner(ctx context.Context, collection, tokenID string) (string, error) {
	var owner string
	err := q.reader().QueryRow(ctx,
		`SELECT owner FROM nft_ownership
		 WHERE collection=$1 AND token_id=$2 AND units > 0
		 ORDER BY units DESC LIMIT 1`, collection, tokenID).Scan(&owner)
	if err == pgx.ErrNoRows {
		return "", nil
	}
	return owner, err
}

// ApplyTransfer721 sets the single owner of an ERC-721 token and orphans any
// active listing whose seller is no longer the holder.
func (q *Q) ApplyTransfer721(ctx context.Context, collection, tokenID, to string) error {
	tx, err := q.writer().Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if _, err = tx.Exec(ctx,
		`DELETE FROM nft_ownership WHERE collection=$1 AND token_id=$2`, collection, tokenID); err != nil {
		return err
	}
	if to != zeroAddr {
		if _, err = tx.Exec(ctx,
			`INSERT INTO nft_ownership(collection, token_id, owner, units, standard)
			 VALUES($1,$2,$3,1,'erc721')`, collection, tokenID, to); err != nil {
			return err
		}
	}
	if _, err = tx.Exec(ctx,
		`INSERT INTO nft_tokens(collection, token_id, owner) VALUES($1,$2,$3)
		 ON CONFLICT(collection, token_id) DO UPDATE SET owner=EXCLUDED.owner`,
		collection, tokenID, to); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx,
		`UPDATE listings SET orphaned=true, active=false
		 WHERE collection=$1 AND token_id=$2 AND lower(seller)<>lower($3) AND active=true`,
		collection, tokenID, to); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// ApplyTransfer1155 moves `units` of a token between holders and orphans the
// sender's listing once their balance reaches zero.
func (q *Q) ApplyTransfer1155(ctx context.Context, collection, tokenID, from, to, units string) error {
	tx, err := q.writer().Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if to != zeroAddr {
		if _, err = tx.Exec(ctx,
			`INSERT INTO nft_ownership(collection, token_id, owner, units, standard)
			 VALUES($1,$2,$3,$4,'erc1155')
			 ON CONFLICT(collection, token_id, owner)
			 DO UPDATE SET units = nft_ownership.units + EXCLUDED.units, updated_at=now()`,
			collection, tokenID, to, units); err != nil {
			return err
		}
	}
	if from != zeroAddr {
		if _, err = tx.Exec(ctx,
			`UPDATE nft_ownership SET units = GREATEST(units - $4::numeric, 0), updated_at=now()
			 WHERE collection=$1 AND token_id=$2 AND owner=$3`,
			collection, tokenID, from, units); err != nil {
			return err
		}
		// Orphan the sender's listing if they no longer hold any units.
		if _, err = tx.Exec(ctx,
			`UPDATE listings SET orphaned=true, active=false
			 WHERE collection=$1 AND token_id=$2 AND seller=$3 AND active=true
			   AND NOT EXISTS (
			       SELECT 1 FROM nft_ownership n
			       WHERE n.collection=$1 AND n.token_id=$2 AND n.owner=$3 AND n.units > 0)`,
			collection, tokenID, from); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// ── Offer positions (Model A stacked) ──────────────────────────────────────

// UpsertOfferPosition records a bidder's compounded position. The contract emits
// the cumulative principal, so we overwrite rather than add.
func (q *Q) UpsertOfferPosition(ctx context.Context, r OfferRow) error {
	_, err := q.writer().Exec(ctx,
		`INSERT INTO offers(collection, token_id, bidder, principal_wei, fee_wei, units, standard, expires_at, status, make_tx)
		 VALUES($1,$2,$3,$4,$5,$6,$7,$8,'pending',$9)
		 ON CONFLICT(collection, token_id, bidder) WHERE status='pending'
		 DO UPDATE SET principal_wei=EXCLUDED.principal_wei,
		     fee_wei = offers.fee_wei + EXCLUDED.fee_wei,
		     units=EXCLUDED.units, expires_at=EXCLUDED.expires_at, make_tx=EXCLUDED.make_tx`,
		r.Collection, r.TokenID, r.Bidder, r.AmountWei, r.FeeWei, r.Units,
		lcStandard(r.Standard), r.ExpiresAt, r.MakeTx)
	return err
}

func (q *Q) SetOfferStatus(ctx context.Context, collection, tokenID, bidder, status string) error {
	_, err := q.writer().Exec(ctx,
		`UPDATE offers SET status=$4
		 WHERE collection=$1 AND token_id=$2 AND bidder=$3 AND status='pending'`,
		collection, tokenID, bidder, status)
	return err
}

// GetActiveOffersForToken returns all pending positions on a token, high to low.
// GetActiveOffersForToken returns pending positions on a token, high to low.
// `limit` caps the returned row count at both the SQL LIMIT (efficiency)
// and the caller's expected maximum (DoS surface against a "hot" token
// with thousands of stacked offers — a single GET would otherwise pull
// multi-MB JSON). The caller is responsible for passing a sensible value;
// the API layer uses 200 to keep the worst-case response bounded while
// still surfacing the top of the leaderboard.
func (q *Q) GetActiveOffersForToken(ctx context.Context, collection, tokenID string, limit int) ([]OfferRow, error) {
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	rows, err := q.reader().Query(ctx,
		`SELECT offer_id::text, bidder, collection, token_id::text, principal_wei::text,
		        fee_wei::text, units, standard::text, expires_at, status::text,
		        COALESCE(make_tx,''), created_at
		 FROM offers
		 WHERE collection=$1 AND token_id=$2 AND status='pending' AND expires_at > now()
		 ORDER BY principal_wei::numeric DESC
		 LIMIT $3`, collection, tokenID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []OfferRow
	for rows.Next() {
		var r OfferRow
		if err := rows.Scan(&r.OfferID, &r.Bidder, &r.Collection, &r.TokenID, &r.AmountWei,
			&r.FeeWei, &r.Units, &r.Standard, &r.ExpiresAt, &r.Status, &r.MakeTx, &r.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ── Wallet NFTs (for the picker) + preflight ───────────────────────────────

type OwnedNFT struct {
	Collection string `json:"collection"`
	TokenID    string `json:"token_id"`
	Units      string `json:"units"`
	Standard   string `json:"standard"`
	Name       string `json:"name"`
	ImageURI   string `json:"image_uri"`
}

func (q *Q) WalletNFTs(ctx context.Context, owner string) ([]OwnedNFT, error) {
	rows, err := q.reader().Query(ctx,
		`SELECT n.collection, n.token_id::text, n.units::text, n.standard::text,
		        COALESCE(m.name, t.name, ''), COALESCE(m.image_uri, t.image_uri, '')
		 FROM nft_ownership n
		 LEFT JOIN nft_metadata m ON m.collection=n.collection AND m.token_id=n.token_id
		 LEFT JOIN nft_tokens   t ON t.collection=n.collection AND t.token_id=n.token_id
		 WHERE n.owner=$1 AND n.units > 0
		 ORDER BY n.updated_at DESC LIMIT 500`, owner)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []OwnedNFT
	for rows.Next() {
		var o OwnedNFT
		if err := rows.Scan(&o.Collection, &o.TokenID, &o.Units, &o.Standard, &o.Name, &o.ImageURI); err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

type Preflight struct {
	Listed     bool   `json:"listed"`
	Orphaned   bool   `json:"orphaned"`
	SellerOwns bool   `json:"seller_owns"`
	Seller     string `json:"seller"`
	PriceWei   string `json:"price_wei"`
}

// EnsureListingSellerOwnership seeds nft_ownership for a listing seller so
// preflight and the metadata worker can find the token before Transfer logs arrive.
func (q *Q) EnsureListingSellerOwnership(ctx context.Context, collection, tokenID, seller, standard string, amount int64) error {
	if standard == "erc1155" {
		_, err := q.writer().Exec(ctx,
			`INSERT INTO nft_ownership(collection, token_id, owner, units, standard)
			 VALUES($1,$2,$3,$4,'erc1155')
			 ON CONFLICT(collection, token_id, owner)
			 DO UPDATE SET units = GREATEST(nft_ownership.units, EXCLUDED.units), updated_at=now()`,
			collection, tokenID, seller, amount)
		return err
	}
	_, err := q.writer().Exec(ctx,
		`DELETE FROM nft_ownership WHERE collection=$1 AND token_id=$2`, collection, tokenID)
	if err != nil {
		return err
	}
	_, err = q.writer().Exec(ctx,
		`INSERT INTO nft_ownership(collection, token_id, owner, units, standard)
		 VALUES($1,$2,$3,1,'erc721')`, collection, tokenID, seller)
	return err
}

// OrphanListing marks a seller's listing inactive when they no longer hold the NFT.
func (q *Q) OrphanListing(ctx context.Context, collection, tokenID, seller string) error {
	_, err := q.writer().Exec(ctx,
		`UPDATE listings SET orphaned=true, active=false
		 WHERE lower(collection)=lower($1) AND token_id=$2 AND lower(seller)=lower($3)
		   AND active=true AND NOT orphaned`,
		collection, tokenID, seller)
	return err
}

// ListingPreflight reports whether a (collection, token, seller) listing can
// still be filled: active, not orphaned, unexpired, and the seller still holds
// at least `l.amount` units. The `n.units > 0` check would falsely pass for
// ERC-1155 listings whose seller transferred most of their balance away but
// still has 1 token left, only to revert on-chain when the buyer tries to take
// the full amount.
func (q *Q) ListingPreflight(ctx context.Context, collection, tokenID, seller string) (*Preflight, error) {
	p := &Preflight{Seller: seller}
	err := q.reader().QueryRow(ctx,
		`SELECT (l.active AND NOT l.orphaned AND l.expires_at > now()), l.orphaned, l.price_wei::text,
		        EXISTS(SELECT 1 FROM nft_ownership n
		               WHERE n.collection=l.collection AND n.token_id=l.token_id
		                 AND lower(n.owner)=lower(l.seller) AND n.units >= l.amount)
		 FROM listings l
		 WHERE lower(l.collection)=lower($1) AND l.token_id=$2 AND lower(l.seller)=lower($3)`,
		collection, tokenID, seller).Scan(&p.Listed, &p.Orphaned, &p.PriceWei, &p.SellerOwns)
	if err == pgx.ErrNoRows {
		return p, nil
	}
	return p, err
}

// ListActiveListingsMissingOwnership returns active listings whose seller has
// no matching row in nft_ownership, including listings where a partial-balance
// ERC-1155 seller transferred most of their tokens away. A stale DB state on
// either count blocks buy preflight.
func (q *Q) ListActiveListingsMissingOwnership(ctx context.Context, limit int) ([]struct {
	Collection, TokenID, Seller, Standard string
	Amount                                 int64
}, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := q.reader().Query(ctx,
		`SELECT l.collection, l.token_id::text, l.seller, l.standard::text, l.amount
		 FROM listings l
		 WHERE l.active=true AND NOT l.orphaned AND l.expires_at > now()
		   AND NOT EXISTS (
		     SELECT 1 FROM nft_ownership n
		     WHERE n.collection=l.collection AND n.token_id=l.token_id
		       AND lower(n.owner)=lower(l.seller) AND n.units >= l.amount)
		 LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []struct {
		Collection, TokenID, Seller, Standard string
		Amount                                 int64
	}
	for rows.Next() {
		var r struct {
			Collection, TokenID, Seller, Standard string
			Amount                                 int64
		}
		if err := rows.Scan(&r.Collection, &r.TokenID, &r.Seller, &r.Standard, &r.Amount); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ── Notifications ──────────────────────────────────────────────────────────

type NotificationRow struct {
	ID        int64     `json:"id"`
	Kind      string    `json:"kind"`
	Title     string    `json:"title"`
	Body      string    `json:"body"`
	Link      string    `json:"link"`
	Read      bool      `json:"read"`
	CreatedAt time.Time `json:"created_at"`
}

func (q *Q) InsertNotification(ctx context.Context, addr, kind, title, body, link string) error {
	_, err := q.writer().Exec(ctx,
		`INSERT INTO notifications(user_addr, kind, title, body, link)
		 VALUES($1,$2,$3,$4,NULLIF($5,''))`,
		strings.ToLower(addr), kind, title, body, link)
	return err
}

func (q *Q) ListNotifications(ctx context.Context, addr string, limit int) ([]NotificationRow, error) {
	if limit == 0 || limit > 100 {
		limit = 50
	}
	rows, err := q.reader().Query(ctx,
		`SELECT id, kind::text, title, body, COALESCE(link,''), read, created_at
		 FROM notifications WHERE user_addr=$1
		 ORDER BY created_at DESC LIMIT $2`, strings.ToLower(addr), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []NotificationRow
	for rows.Next() {
		var n NotificationRow
		if err := rows.Scan(&n.ID, &n.Kind, &n.Title, &n.Body, &n.Link, &n.Read, &n.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

func (q *Q) UnreadCount(ctx context.Context, addr string) (int, error) {
	var n int
	err := q.reader().QueryRow(ctx,
		`SELECT count(*) FROM notifications WHERE user_addr=$1 AND read=false`,
		strings.ToLower(addr)).Scan(&n)
	return n, err
}

func (q *Q) MarkNotificationsRead(ctx context.Context, addr string) error {
	_, err := q.writer().Exec(ctx,
		`UPDATE notifications SET read=true WHERE user_addr=$1 AND read=false`,
		strings.ToLower(addr))
	return err
}

// ── Profiles ───────────────────────────────────────────────────────────────

type ProfileRow struct {
	Address     string `json:"address"`
	DisplayName string `json:"display_name"`
	Bio         string `json:"bio"`
	AvatarURI   string `json:"avatar_uri"`
	BannerURI   string `json:"banner_uri"`
	Twitter     string `json:"twitter"`
	Website     string `json:"website"`
	Verified    bool   `json:"verified"`
}

func (q *Q) GetProfile(ctx context.Context, addr string) (*ProfileRow, error) {
	p := &ProfileRow{Address: strings.ToLower(addr)}
	err := q.reader().QueryRow(ctx,
		`SELECT display_name, bio, COALESCE(avatar_uri,''), COALESCE(banner_uri,''),
		        COALESCE(twitter,''), COALESCE(website,''), verified
		 FROM profiles WHERE address=$1`, strings.ToLower(addr)).
		Scan(&p.DisplayName, &p.Bio, &p.AvatarURI, &p.BannerURI, &p.Twitter, &p.Website, &p.Verified)
	if err == pgx.ErrNoRows {
		return p, nil // empty profile is valid
	}
	return p, err
}

// UpsertProfile writes user-editable fields only; `verified` is admin-controlled.
func (q *Q) UpsertProfile(ctx context.Context, p ProfileRow) error {
	_, err := q.writer().Exec(ctx,
		`INSERT INTO profiles(address, display_name, bio, avatar_uri, banner_uri, twitter, website, updated_at)
		 VALUES($1,$2,$3,NULLIF($4,''),NULLIF($5,''),NULLIF($6,''),NULLIF($7,''), now())
		 ON CONFLICT(address) DO UPDATE
		 SET display_name=EXCLUDED.display_name, bio=EXCLUDED.bio, avatar_uri=EXCLUDED.avatar_uri,
		     banner_uri=EXCLUDED.banner_uri, twitter=EXCLUDED.twitter, website=EXCLUDED.website,
		     updated_at=now()`,
		strings.ToLower(p.Address), p.DisplayName, p.Bio, p.AvatarURI, p.BannerURI, p.Twitter, p.Website)
	return err
}

func (q *Q) SetVerified(ctx context.Context, addr string, verified bool) error {
	_, err := q.writer().Exec(ctx,
		`INSERT INTO profiles(address, verified, updated_at) VALUES($1,$2, now())
		 ON CONFLICT(address) DO UPDATE SET verified=EXCLUDED.verified, updated_at=now()`,
		strings.ToLower(addr), verified)
	return err
}

// ── Reports ────────────────────────────────────────────────────────────────

func (q *Q) InsertReport(ctx context.Context, reporter, targetType, targetID, reason, detail string) error {
	_, err := q.writer().Exec(ctx,
		`INSERT INTO reports(reporter, target_type, target_id, reason, detail)
		 VALUES($1,$2,$3,$4,$5)`,
		strings.ToLower(reporter), targetType, targetID, reason, detail)
	return err
}

// ── Metadata + attributes ──────────────────────────────────────────────────

type MissingToken struct {
	Collection string
	TokenID    string
	Standard   string
}

func (q *Q) ListTokensMissingMetadata(ctx context.Context, limit int) ([]MissingToken, error) {
	if limit == 0 || limit > 200 {
		limit = 100
	}
	// NOTE: tokens whose metadata row exists but has image_uri='' (the
	// "fetched but image-less" case) are intentionally NOT in this list.
	// Re-querying them every 30s produced a tight eth_call loop on tokens
	// whose JSON genuinely has no `image` field. Once a metadata row exists
	// at all, the drop is over.
	rows, err := q.reader().Query(ctx,
		`SELECT src.collection, src.token_id::text, src.standard::text
		 FROM (
		   SELECT n.collection, n.token_id, n.standard
		   FROM nft_ownership n
		   LEFT JOIN nft_metadata m ON m.collection=n.collection AND m.token_id=n.token_id
		   WHERE m.collection IS NULL
		   UNION
		   SELECT l.collection, l.token_id, l.standard
		   FROM listings l
		   LEFT JOIN nft_metadata m ON m.collection=l.collection AND m.token_id=l.token_id
		   WHERE l.active=true AND NOT l.orphaned AND l.expires_at > now()
		     AND m.collection IS NULL
		 ) src
		 GROUP BY src.collection, src.token_id, src.standard
		 LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MissingToken
	for rows.Next() {
		var t MissingToken
		if err := rows.Scan(&t.Collection, &t.TokenID, &t.Standard); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

type Trait struct{ Type, Value string }

func (q *Q) UpsertMetadata(ctx context.Context, collection, tokenID, name, desc, image, animation, uri string, traits []Trait) error {
	tx, err := q.writer().Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if _, err = tx.Exec(ctx,
		`INSERT INTO nft_metadata(collection, token_id, name, description, image_uri, animation_uri, metadata_uri, fetched_at)
		 VALUES($1,$2,$3,$4,$5,$6,$7, now())
		 ON CONFLICT(collection, token_id) DO UPDATE
		 SET name=EXCLUDED.name, description=EXCLUDED.description, image_uri=EXCLUDED.image_uri,
		     animation_uri=EXCLUDED.animation_uri, metadata_uri=EXCLUDED.metadata_uri, fetched_at=now()`,
		collection, tokenID, name, desc, image, animation, uri); err != nil {
		return err
	}
	// Mirror onto nft_tokens for legacy reads.
	if _, err = tx.Exec(ctx,
		`INSERT INTO nft_tokens(collection, token_id, name, description, image_uri, metadata_uri)
		 VALUES($1,$2,$3,$4,$5,$6)
		 ON CONFLICT(collection, token_id) DO UPDATE
		 SET name=EXCLUDED.name, description=EXCLUDED.description,
		     image_uri=EXCLUDED.image_uri, metadata_uri=EXCLUDED.metadata_uri`,
		collection, tokenID, name, desc, image, uri); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx,
		`DELETE FROM nft_attributes WHERE collection=$1 AND token_id=$2`, collection, tokenID); err != nil {
		return err
	}
	for _, t := range traits {
		if t.Type == "" {
			continue
		}
		if _, err = tx.Exec(ctx,
			`INSERT INTO nft_attributes(collection, token_id, trait_type, value)
			 VALUES($1,$2,$3,$4) ON CONFLICT(collection, token_id, trait_type) DO UPDATE SET value=EXCLUDED.value`,
			collection, tokenID, t.Type, t.Value); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// MarkMissing writes a sentinel nft_metadata row that records "this token
// has no off-chain metadata" without clobbering an existing real row. It
// targets the (collection, token_id) unique constraint only — there's no
// `nft_tokens` mirror, so previously-fetched name/image on the legacy
// nft_tokens table is preserved.
//
// DO NOTHING means: if a real metadata row already exists (race or stale
// sentinel vs. a later successful fetch), we don't wipe its populated
// fields. The indexer simply records the attempt.
func (q *Q) MarkMissing(ctx context.Context, collection, tokenID string) error {
	_, err := q.writer().Exec(ctx,
		`INSERT INTO nft_metadata(collection, token_id, name, description, image_uri, animation_uri, metadata_uri, fetched_at)
		 VALUES($1, $2, NULL, NULL, NULL, NULL, NULL, now())
		 ON CONFLICT(collection, token_id) DO NOTHING`,
		collection, tokenID)
	return err
}

// ── Image retry candidates ─────────────────────────────────────────────────────

// ImageRetryToken is a token whose metadata was fetched but whose image self-
// hosting failed (image_uri is still an http(s) URL instead of a local blob
// path). The slow-path retry worker picks these up on a longer cadence.
type ImageRetryToken struct {
	Collection   string
	TokenID      string
	ImageURI     string // the upstream http(s) URL to retry
	RetryCount   int    // how many times we've already tried
}

// maxImageRetries is the ceiling after which the worker stops re-attempting
// self-hosting for a token. At a 60-min cadence with exponential backoff this
// covers ~53 hours of wall-clock retries (1+2+4+8+16+24 = 55h).
const maxImageRetries = 6

// ListTokensWithUpstreamImages returns tokens whose image_uri is an http(s)
// URL (i.e., self-hosting failed during ingest and the fallback upstream URI
// is stored), filtered to those whose backoff has expired. Limited to `limit`
// rows, next eligible first.
func (q *Q) ListTokensWithUpstreamImages(ctx context.Context, limit int) ([]ImageRetryToken, error) {
	if limit <= 0 {
		limit = 50
	} else if limit > 100 {
		limit = 100
	}
	rows, err := q.reader().Query(ctx,
		`SELECT collection, token_id::text, image_uri, image_retry_count
		 FROM nft_metadata
		 WHERE image_uri IS NOT NULL
		   AND (image_uri LIKE 'http://%%' OR image_uri LIKE 'https://%%')
		   AND image_retry_count < $2
		   AND (next_image_retry_at IS NULL OR next_image_retry_at <= now())
		 ORDER BY next_image_retry_at NULLS FIRST, fetched_at ASC
		 LIMIT $1`, limit, maxImageRetries)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ImageRetryToken
	for rows.Next() {
		var t ImageRetryToken
		if err := rows.Scan(&t.Collection, &t.TokenID, &t.ImageURI, &t.RetryCount); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// ── Self-hosted image/blob store ───────────────────────────────────────────────
//
// nft_image_blobs: SHA-256 keyed, BYTEA-backed content-addressed store. Every
// row is dedup-by-hash so identical image bytes from different contracts share
// one entry + a refcount, capped at imagestore.MaxBlobBytes per row. The body	// is what the frontend serves from /api/v1/img/<sha256> — no upstream
	// gateways are in the render path after ingest.
//
// Compile-time assertion pinning *Q as an imagestore.Store so future signature
// drift breaks the build immediately rather than at the first ingest request
// in production. Lives HERE (where the type is defined) rather than in the
// api package — signals drift at the source.
var _ imagestore.Store = (*Q)(nil)

// PutImage upserts body keyed by sha256. Existing rows bump refcount + last_seen
// and silently correct any mime drift (impossible by construction: identical SHA
// → identical bytes → identical mime, but if the indexer vs. handler mistypes,
// the first writer wins so we never serve a row with a mismatched header).
//
// collection is the NFT contract address that triggered this insert. On INSERT
// it is stored in the collection column for per-collection quota enforcement;
// on CONFLICT (dedup) the existing collection value is preserved.
func (q *Q) PutImage(ctx context.Context, sha256hex, mime, collection, sourceURI string, body []byte) error {
	tx, err := q.writer().Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if _, err = tx.Exec(ctx,
		`INSERT INTO nft_image_blobs(sha256, mime, byte_length, source_uri, body, collection)
		 VALUES($1,$2,$3,$4,$5,$6)
		 ON CONFLICT(sha256) DO UPDATE
		   SET refcount     = nft_image_blobs.refcount + 1,
		       last_seen_at = now()`,
		sha256hex, mime, len(body), sourceURI, body, collection); err != nil {
		return fmt.Errorf("put image: %w", err)
	}
	return tx.Commit(ctx)
}

// CountBlobsForCollection returns the number of distinct blobs first inserted
// by this collection. Rows with an empty collection (migration 018 default,
// legacy rows) are excluded from the count so grandfathered blobs do not
// consume quota. Used by imagestore.Put() to enforce MaxBlobCountPerCollection.
func (q *Q) CountBlobsForCollection(ctx context.Context, collection string) (int, error) {
	var n int
	err := q.reader().QueryRow(ctx,
		`SELECT count(*) FROM nft_image_blobs WHERE collection=$1`, collection).Scan(&n)
	return n, err
}

// TotalBlobBytes returns the cumulative byte_length across all nft_image_blobs
// rows. Used by imagestore.Put() to enforce MaxTotalBlobBytes.
func (q *Q) TotalBlobBytes(ctx context.Context) (int64, error) {
	var total int64
	err := q.reader().QueryRow(ctx,
		`SELECT COALESCE(sum(byte_length), 0) FROM nft_image_blobs`).Scan(&total)
	return total, err
}

// GetImage returns the blob row for a hash as an imagestore.Blob. Returns
// pgx.ErrNoRows if unknown. Imports imagestore so the persistent bytea
// payload stays type-safe at the package boundary (the imagestore package
// owns the Blob shape; db just fills it).
func (q *Q) GetImage(ctx context.Context, sha256hex string) (imagestore.Blob, error) {
	var b imagestore.Blob
	err := q.reader().QueryRow(ctx,
		`SELECT body, mime, source_uri FROM nft_image_blobs WHERE sha256=$1`, sha256hex).
		Scan(&b.Body, &b.Mime, &b.SourceURI)
	return b, err
}

// HasImage reports whether a hash exists. Used by mediaProxy to choose local-
// first vs. upstream fetch without pulling the bytea row just to throw it away.
func (q *Q) HasImage(ctx context.Context, sha256hex string) (bool, error) {
	var n int
	err := q.reader().QueryRow(ctx,
		`SELECT 1 FROM nft_image_blobs WHERE sha256=$1`, sha256hex).Scan(&n)
	if err == pgx.ErrNoRows {
		return false, nil
	}
	return n == 1, err
}

// UpdateImageURI replaces the image_uri in both nft_metadata and nft_tokens
// with a self-hosted blob path and resets retry tracking. Used by the
// slow-path image retry worker after a successful imagestore.Put.
func (q *Q) UpdateImageURI(ctx context.Context, collection, tokenID, imageURI string) error {
	tx, err := q.writer().Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if _, err = tx.Exec(ctx,
		`UPDATE nft_metadata SET image_uri=$3, fetched_at=now(),
		        image_retry_count=0, next_image_retry_at=NULL
		 WHERE collection=$1 AND token_id=$2`, collection, tokenID, imageURI); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx,
		`UPDATE nft_tokens SET image_uri=$3
		 WHERE collection=$1 AND token_id=$2`, collection, tokenID, imageURI); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// BumpImageRetry increments the retry count and schedules the next attempt
// using exponential backoff: 1h, 2h, 4h, 8h, 16h, 24h (capped). Once count
// reaches maxImageRetries the token is permanently skipped by the retry query.
func (q *Q) BumpImageRetry(ctx context.Context, collection, tokenID string, count int) error {
	if count >= maxImageRetries {
		return nil
	}
	// Exponential backoff via power(): PG `^` is BITWISE XOR, not exponentiation
	// — using it gave a schedule of 3h, 0h, 1h, 6h, … instead of 1h, 2h, 4h, 8h,
	// 16h, capped at 24h. power(2.0, exp)::int yields the intended geometric
	// series; GREATEST clamps count-1 to ≥ 0 so the very first bump doesn't
	// schedule the next retry in the past.
	_, err := q.writer().Exec(ctx,
		`UPDATE nft_metadata
		 SET image_retry_count = $3,
		     next_image_retry_at = now() + LEAST(power(2.0, GREATEST($3 - 1, 0))::int, 24) * interval '1 hour'
		 WHERE collection=$1 AND token_id=$2`,
		collection, tokenID, count+1)
	return err
}

// ── Saved Searches ────────────────────────────────────────────────────────────

type SavedSearchRow struct {
	ID        int64     `json:"id"`
	UserAddr  string    `json:"user_addr"`
	Name      string    `json:"name"`
	Page      string    `json:"page"`
	Params    string    `json:"params"`
	CreatedAt time.Time `json:"created_at"`
}

// ListSavedSearches returns saved searches for a user, newest first.
// When page is non-empty, filters to only that page ("listings" or "auctions").
func (q *Q) ListSavedSearches(ctx context.Context, addr string, limit int, page string) ([]SavedSearchRow, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	var rows pgx.Rows
	var err error
	if page != "" {
		rows, err = q.reader().Query(ctx,
			`SELECT id, user_addr, name, page, params, created_at
			 FROM saved_searches WHERE user_addr=$1 AND page=$2
			 ORDER BY created_at DESC LIMIT $3`,
			strings.ToLower(addr), page, limit)
	} else {
		rows, err = q.reader().Query(ctx,
			`SELECT id, user_addr, name, page, params, created_at
			 FROM saved_searches WHERE user_addr=$1
			 ORDER BY created_at DESC LIMIT $2`,
			strings.ToLower(addr), limit)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SavedSearchRow
	for rows.Next() {
		var r SavedSearchRow
		if err := rows.Scan(&r.ID, &r.UserAddr, &r.Name, &r.Page, &r.Params, &r.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// InsertSavedSearch creates a new saved search. Returns the inserted ID.
func (q *Q) InsertSavedSearch(ctx context.Context, addr, name, page, params string) (int64, error) {
	var id int64
	err := q.writer().QueryRow(ctx,
		`INSERT INTO saved_searches(user_addr, name, page, params)
		 VALUES($1,$2,$3,$4) RETURNING id`,
		strings.ToLower(addr), name, page, params).Scan(&id)
	return id, err
}

// DeleteSavedSearch removes a saved search. Only the owning user may delete.
// Returns true when a row was actually deleted.
func (q *Q) DeleteSavedSearch(ctx context.Context, id int64, addr string) (bool, error) {
	tag, err := q.writer().Exec(ctx,
		`DELETE FROM saved_searches WHERE id=$1 AND user_addr=$2`, id, strings.ToLower(addr))
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

// ── Trait filters ────────────────────────────────────────────────────────────────

// ListTraitValues returns distinct trait values for a collection, powering filters.
func (q *Q) ListTraitValues(ctx context.Context, collection string) (map[string][]string, error) {
	rows, err := q.reader().Query(ctx,
		`SELECT trait_type, value, count(*) FROM nft_attributes
		 WHERE collection=$1 GROUP BY trait_type, value ORDER BY trait_type, value`, collection)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][]string{}
	for rows.Next() {
		var tt, v string
		var c int
		if err := rows.Scan(&tt, &v, &c); err != nil {
			return nil, err
		}
		out[tt] = append(out[tt], v)
	}
	return out, rows.Err()
}
