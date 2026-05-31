package db

import (
	"context"
	"fmt"
	"math/big"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Q wraps a pgxpool.Pool and exposes typed query methods.
type Q struct{ pool *pgxpool.Pool }

func New(pool *pgxpool.Pool) *Q { return &Q{pool} }

// Ping verifies the DB connection is alive.
func (q *Q) Ping(ctx context.Context) error { return q.pool.Ping(ctx) }

// ── Indexer state ─────────────────────────────────────────────────────────

func (q *Q) GetIndexedBlock(ctx context.Context, chainID int) (uint64, error) {
	var block uint64
	err := q.pool.QueryRow(ctx,
		`SELECT indexed_block FROM indexer_state WHERE chain_id = $1`, chainID).
		Scan(&block)
	if err == pgx.ErrNoRows {
		return 0, nil
	}
	return block, err
}

func (q *Q) SetIndexedBlock(ctx context.Context, chainID int, block uint64) error {
	_, err := q.pool.Exec(ctx,
		`INSERT INTO indexer_state(chain_id, indexed_block, updated_at)
		 VALUES($1,$2,now())
		 ON CONFLICT (chain_id) DO UPDATE
		 SET indexed_block=EXCLUDED.indexed_block, updated_at=now()`,
		chainID, block)
	return err
}

// ── Collections ───────────────────────────────────────────────────────────

type CollectionRow struct {
	Address     string
	Name        string
	Symbol      string
	Standard    string // "erc721" | "erc1155"
	DeployBlock uint64
}

func (q *Q) UpsertCollection(ctx context.Context, addr, name, symbol, standard string, deployBlock uint64) error {
	_, err := q.pool.Exec(ctx,
		`INSERT INTO collections(address, name, symbol, standard, deploy_block)
		 VALUES($1,$2,$3,$4,$5)
		 ON CONFLICT(address) DO UPDATE
		 SET name=EXCLUDED.name, symbol=EXCLUDED.symbol`,
		addr, name, symbol, standard, deployBlock)
	return err
}

func (q *Q) GetCollection(ctx context.Context, address string) (*CollectionRow, error) {
	var c CollectionRow
	err := q.pool.QueryRow(ctx,
		`SELECT address, name, symbol, standard::text, deploy_block
		 FROM collections WHERE address=$1`, address).
		Scan(&c.Address, &c.Name, &c.Symbol, &c.Standard, &c.DeployBlock)
	if err == pgx.ErrNoRows {
		return nil, fmt.Errorf("collection not found: %s", address)
	}
	return &c, err
}

func (q *Q) ListCollections(ctx context.Context, limit int) ([]CollectionRow, error) {
	if limit == 0 || limit > 200 {
		limit = 50
	}
	rows, err := q.pool.Query(ctx,
		`SELECT address, name, symbol, standard::text, deploy_block
		 FROM collections WHERE tracked=true ORDER BY created_at DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CollectionRow
	for rows.Next() {
		var c CollectionRow
		if err := rows.Scan(&c.Address, &c.Name, &c.Symbol, &c.Standard, &c.DeployBlock); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (q *Q) GetFloorPrice(ctx context.Context, collection string) (*big.Int, error) {
	var priceStr string
	err := q.pool.QueryRow(ctx,
		`SELECT COALESCE(MIN(price_wei)::text,'0') FROM listings
		 WHERE collection=$1 AND active=true AND expires_at > now()`, collection).
		Scan(&priceStr)
	if err != nil {
		return big.NewInt(0), nil
	}
	n := new(big.Int)
	n.SetString(priceStr, 10)
	return n, nil
}

func (q *Q) Get24hVolume(ctx context.Context, collection string) (*big.Int, error) {
	var volStr string
	err := q.pool.QueryRow(ctx,
		`SELECT COALESCE(SUM(price_wei)::text,'0') FROM sales
		 WHERE collection=$1 AND occurred_at > now()-interval '24 hours'`, collection).
		Scan(&volStr)
	if err != nil {
		return big.NewInt(0), nil
	}
	n := new(big.Int)
	n.SetString(volStr, 10)
	return n, nil
}

func (q *Q) GetListedCount(ctx context.Context, collection string) (int, error) {
	var count int
	err := q.pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM listings WHERE collection=$1 AND active=true AND expires_at > now()`,
		collection).Scan(&count)
	return count, err
}

// ── Listings ──────────────────────────────────────────────────────────────

type ListingRow struct {
	Collection string
	TokenID    string // decimal uint256
	Seller     string
	PriceWei   string
	Amount     int64
	Standard   string
	ExpiresAt  time.Time
	ListedAt   time.Time
	TxHash     string
	// Denormalised from nft_tokens (may be empty)
	Name     string
	ImageURI string
}

func (q *Q) UpsertListing(ctx context.Context, r ListingRow) error {
	_, err := q.pool.Exec(ctx,
		`INSERT INTO listings(collection, token_id, seller, price_wei, amount, standard, expires_at, listed_at, tx_hash, active, orphaned)
		 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,true,false)
		 ON CONFLICT(collection, token_id, seller) DO UPDATE
		 SET price_wei=EXCLUDED.price_wei, amount=EXCLUDED.amount,
		     standard=EXCLUDED.standard, expires_at=EXCLUDED.expires_at, listed_at=EXCLUDED.listed_at,
		     tx_hash=EXCLUDED.tx_hash, active=true, orphaned=false`,
		r.Collection, r.TokenID, r.Seller, r.PriceWei, r.Amount, r.Standard, r.ExpiresAt, r.ListedAt, r.TxHash)
	return err
}

// DeactivateListing marks a single (collection, token, seller) listing inactive.
func (q *Q) DeactivateListing(ctx context.Context, collection, tokenID, seller string) error {
	_, err := q.pool.Exec(ctx,
		`UPDATE listings SET active=false WHERE collection=$1 AND token_id=$2 AND seller=$3`,
		collection, tokenID, seller)
	return err
}

// MarkOrphaned flags any active listing for (collection, token) where the seller is
// no longer the recorded owner. Indexer calls this on Transfer.
func (q *Q) MarkOrphaned(ctx context.Context, collection, tokenID, currentOwner string) error {
	_, err := q.pool.Exec(ctx,
		`UPDATE listings SET orphaned=true
		 WHERE collection=$1 AND token_id=$2 AND seller<>$3 AND active=true AND orphaned=false`,
		collection, tokenID, currentOwner)
	return err
}

// GetListing returns the cheapest active, non-orphaned listing for (collection, token).
// For ERC-721 that's the only listing. For ERC-1155 callers should prefer
// GetListingsForToken to see every seller's stack.
func (q *Q) GetListing(ctx context.Context, collection, tokenID string) (*ListingRow, error) {
	var r ListingRow
	err := q.pool.QueryRow(ctx,
		`SELECT l.collection, l.token_id::text, l.seller, l.price_wei::text, l.amount,
		        l.standard::text, l.expires_at, l.listed_at, l.tx_hash,
		        COALESCE(t.name,''), COALESCE(t.image_uri,'')
		 FROM listings l
		 LEFT JOIN nft_tokens t ON t.collection=l.collection AND t.token_id=l.token_id
		 WHERE l.collection=$1 AND l.token_id=$2 AND l.active=true AND l.orphaned=false
		   AND l.expires_at > now()
		 ORDER BY CAST(l.price_wei AS numeric) ASC LIMIT 1`,
		collection, tokenID).
		Scan(&r.Collection, &r.TokenID, &r.Seller, &r.PriceWei, &r.Amount,
			&r.Standard, &r.ExpiresAt, &r.ListedAt, &r.TxHash, &r.Name, &r.ImageURI)
	if err == pgx.ErrNoRows {
		return nil, fmt.Errorf("listing not found")
	}
	return &r, err
}

// GetListingsForToken returns every active, non-orphaned listing for a token —
// one row per seller (ERC-1155 stacks).
func (q *Q) GetListingsForToken(ctx context.Context, collection, tokenID string) ([]ListingRow, error) {
	rows, err := q.pool.Query(ctx,
		`SELECT l.collection, l.token_id::text, l.seller, l.price_wei::text, l.amount,
		        l.standard::text, l.expires_at, l.listed_at, l.tx_hash,
		        COALESCE(t.name,''), COALESCE(t.image_uri,'')
		 FROM listings l
		 LEFT JOIN nft_tokens t ON t.collection=l.collection AND t.token_id=l.token_id
		 WHERE l.collection=$1 AND l.token_id=$2 AND l.active=true AND l.orphaned=false
		   AND l.expires_at > now()
		 ORDER BY CAST(l.price_wei AS numeric) ASC`,
		collection, tokenID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ListingRow
	for rows.Next() {
		var r ListingRow
		if err := rows.Scan(&r.Collection, &r.TokenID, &r.Seller, &r.PriceWei, &r.Amount,
			&r.Standard, &r.ExpiresAt, &r.ListedAt, &r.TxHash, &r.Name, &r.ImageURI); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

type ListingsFilter struct {
	Collection string
	Seller     string
	Sort       string // "recent" | "price_asc" | "price_desc"
	Limit      int
	Cursor     string
}

func (q *Q) ListActiveListings(ctx context.Context, f ListingsFilter) ([]ListingRow, error) {
	if f.Limit == 0 || f.Limit > 100 {
		f.Limit = 50
	}
	args := []any{f.Limit}
	where := "WHERE l.active=true AND l.orphaned=false AND l.expires_at > now()"
	if f.Collection != "" {
		args = append(args, f.Collection)
		where += fmt.Sprintf(" AND l.collection=$%d", len(args))
	}
	if f.Seller != "" {
		args = append(args, f.Seller)
		where += fmt.Sprintf(" AND l.seller=$%d", len(args))
	}
	orderBy := "l.listed_at DESC"
	switch f.Sort {
	case "price_asc":
		orderBy = "CAST(l.price_wei AS numeric) ASC"
	case "price_desc":
		orderBy = "CAST(l.price_wei AS numeric) DESC"
	}

	rows, err := q.pool.Query(ctx,
		`SELECT l.collection, l.token_id::text, l.seller, l.price_wei::text, l.amount,
		        l.standard::text, l.expires_at, l.listed_at, l.tx_hash,
		        COALESCE(t.name,''), COALESCE(t.image_uri,'')
		 FROM listings l
		 LEFT JOIN nft_tokens t ON t.collection=l.collection AND t.token_id=l.token_id
		 `+where+`
		 ORDER BY `+orderBy+` LIMIT $1`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []ListingRow
	for rows.Next() {
		var r ListingRow
		if err := rows.Scan(&r.Collection, &r.TokenID, &r.Seller, &r.PriceWei, &r.Amount,
			&r.Standard, &r.ExpiresAt, &r.ListedAt, &r.TxHash, &r.Name, &r.ImageURI); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ── Auctions ──────────────────────────────────────────────────────────────

type AuctionRow struct {
	AuctionID       int64
	Collection      string
	TokenID         string
	Seller          string
	Standard        string
	ReservePriceWei string
	HighestBidWei   string
	HighestBidder   string
	MinIncrementBps int
	StartsAt        time.Time
	EndsAt          time.Time
	Status          string
	CreateTx        string
}

func (q *Q) UpsertAuction(ctx context.Context, r AuctionRow) error {
	_, err := q.pool.Exec(ctx,
		`INSERT INTO auctions(auction_id, collection, token_id, seller, standard,
		    reserve_price_wei, highest_bid_wei, highest_bidder, min_increment_bps,
		    starts_at, ends_at, status, create_tx)
		 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)
		 ON CONFLICT(auction_id) DO UPDATE
		 SET highest_bid_wei=EXCLUDED.highest_bid_wei,
		     highest_bidder=EXCLUDED.highest_bidder,
		     ends_at=EXCLUDED.ends_at,
		     status=EXCLUDED.status`,
		r.AuctionID, r.Collection, r.TokenID, r.Seller, r.Standard,
		r.ReservePriceWei, r.HighestBidWei, r.HighestBidder, r.MinIncrementBps,
		r.StartsAt, r.EndsAt, r.Status, r.CreateTx)
	return err
}

func (q *Q) SetAuctionStatus(ctx context.Context, auctionID int64, status string) error {
	_, err := q.pool.Exec(ctx,
		`UPDATE auctions SET status=$1 WHERE auction_id=$2`, status, auctionID)
	return err
}

// SetAuctionEndsAt updates an auction's end timestamp (anti-snipe extension).
func (q *Q) SetAuctionEndsAt(ctx context.Context, auctionID int64, newEnd time.Time) error {
	_, err := q.pool.Exec(ctx,
		`UPDATE auctions SET ends_at=$1 WHERE auction_id=$2 AND status='active'`,
		newEnd, auctionID)
	return err
}

func (q *Q) UpdateAuctionBid(ctx context.Context, r AuctionRow) error {
	_, err := q.pool.Exec(ctx,
		`UPDATE auctions SET highest_bid_wei=$1, highest_bidder=$2 WHERE auction_id=$3`,
		r.HighestBidWei, r.HighestBidder, r.AuctionID)
	return err
}

func (q *Q) GetAuction(ctx context.Context, auctionID int64) (*AuctionRow, error) {
	var r AuctionRow
	err := q.pool.QueryRow(ctx,
		`SELECT auction_id, collection, token_id::text, seller, standard::text,
		        reserve_price_wei::text, highest_bid_wei::text, COALESCE(highest_bidder,''),
		        min_increment_bps, starts_at, ends_at, status::text, create_tx
		 FROM auctions WHERE auction_id=$1`, auctionID).
		Scan(&r.AuctionID, &r.Collection, &r.TokenID, &r.Seller, &r.Standard,
			&r.ReservePriceWei, &r.HighestBidWei, &r.HighestBidder,
			&r.MinIncrementBps, &r.StartsAt, &r.EndsAt, &r.Status, &r.CreateTx)
	if err == pgx.ErrNoRows {
		return nil, fmt.Errorf("auction not found: %d", auctionID)
	}
	return &r, err
}

type AuctionsFilter struct {
	Collection string
	Status     string // "active" | "settled" | "cancelled" | "" = all
	Limit      int
}

func (q *Q) ListAuctions(ctx context.Context, f AuctionsFilter) ([]AuctionRow, error) {
	if f.Limit == 0 || f.Limit > 100 {
		f.Limit = 50
	}
	args := []any{f.Limit}
	where := "WHERE 1=1"
	if f.Collection != "" {
		args = append(args, f.Collection)
		where += fmt.Sprintf(" AND collection=$%d", len(args))
	}
	if f.Status != "" {
		args = append(args, f.Status)
		where += fmt.Sprintf(" AND status=$%d", len(args))
	}
	rows, err := q.pool.Query(ctx,
		`SELECT auction_id, collection, token_id::text, seller, standard::text,
		        reserve_price_wei::text, highest_bid_wei::text, COALESCE(highest_bidder,''),
		        min_increment_bps, starts_at, ends_at, status::text, create_tx
		 FROM auctions `+where+` ORDER BY ends_at ASC LIMIT $1`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AuctionRow
	for rows.Next() {
		var r AuctionRow
		if err := rows.Scan(&r.AuctionID, &r.Collection, &r.TokenID, &r.Seller, &r.Standard,
			&r.ReservePriceWei, &r.HighestBidWei, &r.HighestBidder, &r.MinIncrementBps,
			&r.StartsAt, &r.EndsAt, &r.Status, &r.CreateTx); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (q *Q) ListActiveAuctions(ctx context.Context, limit int) ([]AuctionRow, error) {
	return q.ListAuctions(ctx, AuctionsFilter{Status: "active", Limit: limit})
}

func (q *Q) GetExpiredActiveAuctions(ctx context.Context) ([]AuctionRow, error) {
	rows, err := q.pool.Query(ctx,
		`SELECT auction_id, collection, token_id::text, seller, standard::text,
		        reserve_price_wei::text, highest_bid_wei::text, COALESCE(highest_bidder,''),
		        min_increment_bps, starts_at, ends_at, status::text, create_tx
		 FROM auctions WHERE status='active' AND ends_at < now()
		 ORDER BY ends_at ASC LIMIT 100`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AuctionRow
	for rows.Next() {
		var r AuctionRow
		if err := rows.Scan(&r.AuctionID, &r.Collection, &r.TokenID, &r.Seller, &r.Standard,
			&r.ReservePriceWei, &r.HighestBidWei, &r.HighestBidder, &r.MinIncrementBps,
			&r.StartsAt, &r.EndsAt, &r.Status, &r.CreateTx); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (q *Q) GetInactiveAuctions(ctx context.Context) ([]AuctionRow, error) {
	rows, err := q.pool.Query(ctx,
		`SELECT auction_id, collection, token_id::text, seller, standard::text,
		        reserve_price_wei::text, highest_bid_wei::text, COALESCE(highest_bidder,''),
		        min_increment_bps, starts_at, ends_at, status::text, create_tx
		 FROM auctions
		 WHERE status='active'
		   AND highest_bidder IS NULL
		   AND starts_at + interval '30 minutes' < now()
		 ORDER BY starts_at ASC LIMIT 100`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AuctionRow
	for rows.Next() {
		var r AuctionRow
		if err := rows.Scan(&r.AuctionID, &r.Collection, &r.TokenID, &r.Seller, &r.Standard,
			&r.ReservePriceWei, &r.HighestBidWei, &r.HighestBidder, &r.MinIncrementBps,
			&r.StartsAt, &r.EndsAt, &r.Status, &r.CreateTx); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ── Bids ──────────────────────────────────────────────────────────────────

func (q *Q) InsertBid(ctx context.Context, auctionID int64, bidder, amtWei, txHash string, placedAt time.Time) error {
	_, err := q.pool.Exec(ctx,
		`INSERT INTO bids(auction_id, bidder, amount_wei, tx_hash, placed_at)
		 VALUES($1,$2,$3,$4,$5)
		 ON CONFLICT(tx_hash) DO NOTHING`,
		auctionID, bidder, amtWei, txHash, placedAt)
	return err
}

// BidRow is one row from the bids table.
type BidRow struct {
	Bidder    string    `json:"bidder"`
	AmountWei string    `json:"amount_wei"`
	TxHash    string    `json:"tx_hash"`
	PlacedAt  time.Time `json:"placed_at"`
}

// GetBidsForAuction returns all bids for an auction ordered newest-first.
func (q *Q) GetBidsForAuction(ctx context.Context, auctionID int64) ([]BidRow, error) {
	rows, err := q.pool.Query(ctx,
		`SELECT bidder, amount_wei::text, tx_hash, placed_at
		   FROM bids
		  WHERE auction_id = $1
		  ORDER BY placed_at DESC
		  LIMIT 200`,
		auctionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []BidRow
	for rows.Next() {
		var r BidRow
		if err := rows.Scan(&r.Bidder, &r.AmountWei, &r.TxHash, &r.PlacedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ── Sales ─────────────────────────────────────────────────────────────────

func (q *Q) InsertSale(ctx context.Context,
	collection, tokenID, seller, buyer, priceWei, feeWei, royaltyWei, txHash string,
	blockNumber uint64, occurredAt time.Time,
) error {
	_, err := q.pool.Exec(ctx,
		`INSERT INTO sales(collection, token_id, seller, buyer, price_wei, fee_wei, royalty_wei, tx_hash, block_number, occurred_at)
		 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
		 ON CONFLICT(tx_hash) DO NOTHING`,
		collection, tokenID, seller, buyer, priceWei, feeWei, royaltyWei, txHash, blockNumber, occurredAt)
	return err
}

func (q *Q) GetCollectionVolume(ctx context.Context, collection string, since time.Time) (*big.Int, error) {
	var volStr string
	err := q.pool.QueryRow(ctx,
		`SELECT COALESCE(SUM(price_wei)::text,'0') FROM sales
		 WHERE collection=$1 AND occurred_at >= $2`, collection, since).Scan(&volStr)
	if err != nil {
		return big.NewInt(0), nil
	}
	n := new(big.Int)
	n.SetString(volStr, 10)
	return n, nil
}

func (q *Q) GetCollectionBidCount(ctx context.Context, collection string, since time.Time) (int64, error) {
	var count int64
	err := q.pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM bids b
		 JOIN auctions a ON a.auction_id=b.auction_id
		 WHERE a.collection=$1 AND b.placed_at >= $2`, collection, since).Scan(&count)
	return count, err
}

func (q *Q) GetCollectionViews(ctx context.Context, collection string) (int64, error) {
	var views int64
	err := q.pool.QueryRow(ctx,
		`SELECT COALESCE(SUM(views),0) FROM nft_tokens WHERE collection=$1`, collection).Scan(&views)
	return views, err
}

// ── Trending scores ───────────────────────────────────────────────────────

type TrendingScore struct {
	Collection string
	Window     string
	Score      float64
	Views      int64
	Bids       int64
	VolumeWei  *big.Int
}

func (q *Q) UpsertTrendingScore(ctx context.Context, s TrendingScore) error {
	_, err := q.pool.Exec(ctx,
		`INSERT INTO trending_scores(collection, "window", score, views, bids, volume_wei, computed_at)
		 VALUES($1,$2,$3,$4,$5,$6,now())
		 ON CONFLICT(collection, "window") DO UPDATE
		 SET score=EXCLUDED.score, views=EXCLUDED.views, bids=EXCLUDED.bids,
		     volume_wei=EXCLUDED.volume_wei, computed_at=now()`,
		s.Collection, s.Window, s.Score, s.Views, s.Bids, s.VolumeWei.String())
	return err
}

func (q *Q) GetTrendingCollections(ctx context.Context, window string, limit int) ([]TrendingScore, error) {
	rows, err := q.pool.Query(ctx,
		`SELECT collection, "window", score, views, bids, volume_wei::text
		 FROM trending_scores WHERE "window"=$1
		 ORDER BY score DESC LIMIT $2`, window, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []TrendingScore
	for rows.Next() {
		var s TrendingScore
		var volStr string
		if err := rows.Scan(&s.Collection, &s.Window, &s.Score, &s.Views, &s.Bids, &volStr); err != nil {
			return nil, err
		}
		s.VolumeWei = new(big.Int)
		s.VolumeWei.SetString(volStr, 10)
		out = append(out, s)
	}
	return out, rows.Err()
}

// ── NFT token owner & metadata ────────────────────────────────────────────

func (q *Q) SetTokenOwner(ctx context.Context, collection, tokenID, owner string) error {
	_, err := q.pool.Exec(ctx,
		`INSERT INTO nft_tokens(collection, token_id, owner)
		 VALUES($1,$2,$3)
		 ON CONFLICT(collection, token_id) DO UPDATE SET owner=EXCLUDED.owner`,
		collection, tokenID, owner)
	return err
}

func (q *Q) GetTokenMeta(ctx context.Context, collection, tokenID string) (name, imageURI string, err error) {
	err = q.pool.QueryRow(ctx,
		`SELECT COALESCE(name,''), COALESCE(image_uri,'') FROM nft_tokens
		 WHERE collection=$1 AND token_id=$2`, collection, tokenID).Scan(&name, &imageURI)
	if err == pgx.ErrNoRows {
		return "", "", nil
	}
	return name, imageURI, err
}

func (q *Q) IncrementTokenViews(ctx context.Context, collection, tokenID string) error {
	_, err := q.pool.Exec(ctx,
		`UPDATE nft_tokens SET views=views+1 WHERE collection=$1 AND token_id=$2`,
		collection, tokenID)
	return err
}

// ── Offers ────────────────────────────────────────────────────────────────

type OfferRow struct {
	OfferID    string
	Bidder     string
	Collection string
	TokenID    string // empty = collection-wide offer
	AmountWei  string
	Nonce      string
	ExpiresAt  time.Time
	Signature  string
	Status     string
	CreatedAt  time.Time
}

type OffersFilter struct {
	Collection string
	TokenID    string
	Bidder     string
	Owner      string // join with nft_tokens to find offers on tokens owned by this address
	Status     string
	Limit      int
}

func (q *Q) InsertOffer(ctx context.Context, r OfferRow) (string, error) {
	var id string
	tokenIDParam := interface{}(nil)
	if r.TokenID != "" {
		tokenIDParam = r.TokenID
	}
	err := q.pool.QueryRow(ctx,
		`INSERT INTO offers(bidder, collection, token_id, amount_wei, nonce, expires_at, signature, status)
		 VALUES($1,$2,$3,$4,$5,$6,$7,$8)
		 RETURNING offer_id::text`,
		r.Bidder, r.Collection, tokenIDParam, r.AmountWei, r.Nonce, r.ExpiresAt, r.Signature, r.Status).
		Scan(&id)
	return id, err
}

func (q *Q) GetOffer(ctx context.Context, offerID string) (*OfferRow, error) {
	var r OfferRow
	var tokenID *string
	err := q.pool.QueryRow(ctx,
		`SELECT offer_id::text, bidder, collection, token_id::text, amount_wei::text,
		        nonce::text, expires_at, signature, status::text, created_at
		 FROM offers WHERE offer_id=$1`, offerID).
		Scan(&r.OfferID, &r.Bidder, &r.Collection, &tokenID,
			&r.AmountWei, &r.Nonce, &r.ExpiresAt, &r.Signature, &r.Status, &r.CreatedAt)
	if err == pgx.ErrNoRows {
		return nil, fmt.Errorf("offer not found: %s", offerID)
	}
	if tokenID != nil {
		r.TokenID = *tokenID
	}
	return &r, err
}

func (q *Q) ListOffers(ctx context.Context, f OffersFilter) ([]OfferRow, error) {
	if f.Limit == 0 || f.Limit > 100 {
		f.Limit = 50
	}
	args := []any{f.Limit}
	where := "WHERE o.expires_at > now()"
	if f.Collection != "" {
		args = append(args, f.Collection)
		where += fmt.Sprintf(" AND o.collection=$%d", len(args))
	}
	if f.TokenID != "" {
		args = append(args, f.TokenID)
		where += fmt.Sprintf(" AND o.token_id=$%d", len(args))
	}
	if f.Bidder != "" {
		args = append(args, f.Bidder)
		where += fmt.Sprintf(" AND o.bidder=$%d", len(args))
	}
	if f.Status != "" {
		args = append(args, f.Status)
		where += fmt.Sprintf(" AND o.status=$%d", len(args))
	}
	if f.Owner != "" {
		args = append(args, f.Owner)
		where += fmt.Sprintf(` AND EXISTS (
			SELECT 1 FROM nft_tokens t
			WHERE t.collection=o.collection AND t.token_id=o.token_id AND t.owner=$%d
		)`, len(args))
	}
	rows, err := q.pool.Query(ctx,
		`SELECT o.offer_id::text, o.bidder, o.collection, o.token_id::text,
		        o.amount_wei::text, o.nonce::text, o.expires_at, o.signature,
		        o.status::text, o.created_at
		 FROM offers o `+where+` ORDER BY o.created_at DESC LIMIT $1`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []OfferRow
	for rows.Next() {
		var r OfferRow
		var tokenID *string
		if err := rows.Scan(&r.OfferID, &r.Bidder, &r.Collection, &tokenID,
			&r.AmountWei, &r.Nonce, &r.ExpiresAt, &r.Signature, &r.Status, &r.CreatedAt); err != nil {
			return nil, err
		}
		if tokenID != nil {
			r.TokenID = *tokenID
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (q *Q) ExpireOffers(ctx context.Context) (int64, error) {
	tag, err := q.pool.Exec(ctx,
		`UPDATE offers SET status='expired' WHERE expires_at < now() AND status='pending'`)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

func (q *Q) CancelOffer(ctx context.Context, offerID, bidder string) error {
	_, err := q.pool.Exec(ctx,
		`UPDATE offers SET status='cancelled'
		 WHERE offer_id=$1 AND bidder=$2 AND status='pending'`,
		offerID, bidder)
	return err
}

// ── Users ─────────────────────────────────────────────────────────────────

func (q *Q) UpsertUser(ctx context.Context, address string) error {
	_, err := q.pool.Exec(ctx,
		`INSERT INTO users(address, last_seen_at)
		 VALUES($1, now())
		 ON CONFLICT(address) DO UPDATE SET last_seen_at=now()`,
		address)
	return err
}

// ── Event counting (for IndexerService.GetStatus) ─────────────────────────

func (q *Q) GetEventCounts(ctx context.Context) (total, last1h uint64, err error) {
	err = q.pool.QueryRow(ctx,
		`SELECT
		    (SELECT COUNT(*) FROM sales) +
		    (SELECT COUNT(*) FROM bids) +
		    (SELECT COUNT(*) FROM listings) AS total,
		    (SELECT COUNT(*) FROM sales WHERE occurred_at > now()-interval '1 hour') +
		    (SELECT COUNT(*) FROM bids WHERE placed_at > now()-interval '1 hour') AS last1h`,
	).Scan(&total, &last1h)
	return total, last1h, err
}

// ── Search ────────────────────────────────────────────────────────────────

type SearchResult struct {
	Kind       string `json:"kind"`              // "nft" | "collection"
	Collection string `json:"collection"`
	TokenID    string `json:"token_id,omitempty"`
	Name       string `json:"name"`
	ImageURI   string `json:"image_uri,omitempty"`
}

// Search finds NFTs and collections matching query using Postgres full-text search.
// Returns up to limit results per kind (nft + collection combined).
func (q *Q) Search(ctx context.Context, query string, limit int) ([]SearchResult, error) {
	if limit <= 0 || limit > 50 {
		limit = 20
	}
	rows, err := q.pool.Query(ctx, `
		(
			SELECT 'nft'::text,
			       t.collection::text,
			       t.token_id::text,
			       coalesce(t.name, '') AS name,
			       coalesce(t.image_uri, '') AS image_uri
			FROM nft_tokens t
			WHERE t.search_vec @@ plainto_tsquery('english', $1)
			LIMIT $2
		)
		UNION ALL
		(
			SELECT 'collection'::text,
			       c.address::text,
			       ''::text,
			       c.name,
			       ''::text
			FROM collections c
			WHERE c.search_vec @@ plainto_tsquery('english', $1)
			LIMIT $2
		)
	`, query, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SearchResult
	for rows.Next() {
		var r SearchResult
		if err := rows.Scan(&r.Kind, &r.Collection, &r.TokenID, &r.Name, &r.ImageURI); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ── Atomic combined writes ────────────────────────────────────────────────

// DeactivateAndSale atomically deactivates a listing and records the sale.
// Replaces the non-transactional DeactivateListing + InsertSale pair in the indexer.
func (q *Q) DeactivateAndSale(ctx context.Context,
	collection, tokenID, seller, buyer, priceWei, feeWei, royaltyWei, txHash string,
	blockNumber uint64, occurredAt time.Time,
) error {
	tx, err := q.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx,
		`UPDATE listings SET active=false WHERE collection=$1 AND token_id=$2 AND seller=$3`,
		collection, tokenID, seller); err != nil {
		_ = tx.Rollback(ctx)
		return fmt.Errorf("deactivate: %w", err)
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO sales(collection,token_id,seller,buyer,price_wei,fee_wei,royalty_wei,tx_hash,block_number,occurred_at)
		 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10) ON CONFLICT(tx_hash) DO NOTHING`,
		collection, tokenID, seller, buyer, priceWei, feeWei, royaltyWei, txHash, blockNumber, occurredAt); err != nil {
		_ = tx.Rollback(ctx)
		return fmt.Errorf("insert sale: %w", err)
	}
	return tx.Commit(ctx)
}

// InsertBidAndUpdateAuction atomically records a bid and updates the auction's highest bid.
// Replaces the non-transactional InsertBid + UpdateAuctionBid pair in the indexer.
func (q *Q) InsertBidAndUpdateAuction(ctx context.Context, auctionID int64, bidder, amtWei, txHash string, placedAt time.Time) error {
	tx, err := q.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO bids(auction_id,bidder,amount_wei,tx_hash,placed_at)
		 VALUES($1,$2,$3,$4,$5) ON CONFLICT(tx_hash) DO NOTHING`,
		auctionID, bidder, amtWei, txHash, placedAt); err != nil {
		_ = tx.Rollback(ctx)
		return fmt.Errorf("insert bid: %w", err)
	}
	if _, err := tx.Exec(ctx,
		`UPDATE auctions SET highest_bid_wei=$1, highest_bidder=$2 WHERE auction_id=$3`,
		amtWei, bidder, auctionID); err != nil {
		_ = tx.Rollback(ctx)
		return fmt.Errorf("update auction bid: %w", err)
	}
	return tx.Commit(ctx)
}

// ── Metrics ───────────────────────────────────────────────────────────────

// MarketMetrics holds aggregate stats for the /api/v1/metrics endpoint.
type MarketMetrics struct {
	TotalActiveListings int64  `json:"totalActiveListings"`
	TotalSales          int64  `json:"totalSales"`
	GrossVolumeWei      string `json:"grossVolumeWei"`
	TotalAuctions       int64  `json:"totalAuctions"`
}

func (q *Q) GetMarketMetrics(ctx context.Context) (*MarketMetrics, error) {
	var m MarketMetrics
	err := q.pool.QueryRow(ctx, `
		SELECT
			(SELECT COUNT(*)          FROM listings WHERE active = true)::bigint,
			(SELECT COUNT(*)          FROM sales)::bigint,
			COALESCE((SELECT SUM(price_wei)::text FROM sales), '0'),
			(SELECT COUNT(*)          FROM auctions)::bigint
	`).Scan(&m.TotalActiveListings, &m.TotalSales, &m.GrossVolumeWei, &m.TotalAuctions)
	return &m, err
}

// ActivityRow is a single entry in the marketplace activity feed.
type ActivityRow struct {
	Type       string    `json:"type"`
	Collection string    `json:"collection"`
	TokenID    string    `json:"tokenId"`
	AmountWei  string    `json:"amountWei"`
	Timestamp  time.Time `json:"timestamp"`
	TxHash     string    `json:"txHash"`
}

// ── nft_ownership ─────────────────────────────────────────────────────────

type OwnershipRow struct {
	Collection   string    `json:"collection"`
	TokenID      string    `json:"token_id"`
	Owner        string    `json:"owner"`
	Balance      string    `json:"balance"`
	UpdatedBlock uint64    `json:"updated_block"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// UpsertNFTOwnership replaces the (coll, id, owner) row with a new balance,
// or inserts if absent. ERC-721 transfers should also remove the previous
// owner's row via SetNFTOwnership721.
func (q *Q) UpsertNFTOwnership(ctx context.Context, collection, tokenID, owner, balance string, blockNumber uint64) error {
	_, err := q.pool.Exec(ctx,
		`INSERT INTO nft_ownership(collection, token_id, owner, balance, updated_block)
		 VALUES($1,$2,$3,$4,$5)
		 ON CONFLICT(collection, token_id, owner) DO UPDATE
		 SET balance=EXCLUDED.balance, updated_block=EXCLUDED.updated_block, updated_at=now()`,
		collection, tokenID, owner, balance, blockNumber)
	return err
}

// SetNFTOwnership721 moves a unique token from one owner to another atomically.
func (q *Q) SetNFTOwnership721(ctx context.Context, collection, tokenID, from, to string, blockNumber uint64) error {
	tx, err := q.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	if from != "" && from != "0x0000000000000000000000000000000000000000" {
		if _, err := tx.Exec(ctx,
			`DELETE FROM nft_ownership WHERE collection=$1 AND token_id=$2 AND owner=$3`,
			collection, tokenID, from); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("delete old owner: %w", err)
		}
	}
	if to != "" && to != "0x0000000000000000000000000000000000000000" {
		if _, err := tx.Exec(ctx,
			`INSERT INTO nft_ownership(collection, token_id, owner, balance, updated_block)
			 VALUES($1,$2,$3,'1',$4)
			 ON CONFLICT(collection, token_id, owner) DO UPDATE
			 SET balance='1', updated_block=EXCLUDED.updated_block, updated_at=now()`,
			collection, tokenID, to, blockNumber); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("insert new owner: %w", err)
		}
	}
	return tx.Commit(ctx)
}

// AdjustNFTOwnership1155 changes a holder's balance by `delta`. Negative deletes when ≤ 0.
func (q *Q) AdjustNFTOwnership1155(ctx context.Context, collection, tokenID, owner string, delta *big.Int, blockNumber uint64) error {
	if owner == "" || owner == "0x0000000000000000000000000000000000000000" {
		return nil
	}
	_, err := q.pool.Exec(ctx,
		`INSERT INTO nft_ownership(collection, token_id, owner, balance, updated_block)
		 VALUES($1,$2,$3,GREATEST($4::numeric,0)::text,$5)
		 ON CONFLICT(collection, token_id, owner) DO UPDATE
		 SET balance=GREATEST((CAST(nft_ownership.balance AS numeric) + $4::numeric), 0)::text,
		     updated_block=EXCLUDED.updated_block, updated_at=now()`,
		collection, tokenID, owner, delta.String(), blockNumber)
	if err != nil {
		return err
	}
	// Drop the row if balance hit zero
	_, err = q.pool.Exec(ctx,
		`DELETE FROM nft_ownership WHERE collection=$1 AND token_id=$2 AND owner=$3 AND CAST(balance AS numeric)<=0`,
		collection, tokenID, owner)
	return err
}

// WalletNFT is one NFT held by an address, with market-context flags.
type WalletNFT struct {
	Collection string `json:"collection"`
	TokenID    string `json:"token_id"`
	Balance    string `json:"balance"`
	Standard   string `json:"standard"`
	Name       string `json:"name"`
	ImageURI   string `json:"image_uri"`
	IsListed   bool   `json:"is_listed"`
	HasAuction bool   `json:"has_auction"`
	HasOffer   bool   `json:"has_offer"`
}

// GetWalletNFTs returns every NFT currently held by an address, joined with
// metadata and flagged for active listing / auction / offer presence.
func (q *Q) GetWalletNFTs(ctx context.Context, owner string) ([]WalletNFT, error) {
	rows, err := q.pool.Query(ctx,
		`SELECT o.collection, o.token_id::text, o.balance::text,
		        COALESCE(c.standard::text, 'erc721'),
		        COALESCE(m.name, ''), COALESCE(m.image_uri, ''),
		        EXISTS(SELECT 1 FROM listings l
		               WHERE l.collection=o.collection AND l.token_id=o.token_id
		                 AND l.seller=o.owner AND l.active=true AND l.orphaned=false
		                 AND l.expires_at > now()) AS is_listed,
		        EXISTS(SELECT 1 FROM auctions a
		               WHERE a.collection=o.collection AND a.token_id=o.token_id
		                 AND a.seller=o.owner AND a.status='active') AS has_auction,
		        EXISTS(SELECT 1 FROM offer_positions p
		               WHERE p.collection=o.collection AND p.token_id=o.token_id
		                 AND p.status='pending') AS has_offer
		 FROM nft_ownership o
		 LEFT JOIN collections   c ON c.address=o.collection
		 LEFT JOIN nft_metadata  m ON m.collection=o.collection AND m.token_id=o.token_id
		 WHERE o.owner=$1 AND CAST(o.balance AS numeric) > 0
		 ORDER BY o.updated_at DESC LIMIT 500`,
		owner)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []WalletNFT
	for rows.Next() {
		var w WalletNFT
		if err := rows.Scan(&w.Collection, &w.TokenID, &w.Balance, &w.Standard,
			&w.Name, &w.ImageURI, &w.IsListed, &w.HasAuction, &w.HasOffer); err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

// ── tracked_collections ───────────────────────────────────────────────────

// EnsureTrackedCollection inserts a collection into both `collections` and
// `tracked_collections` if missing. Indexer calls this on first sight.
func (q *Q) EnsureTrackedCollection(ctx context.Context, address, standard string, firstSeenBlock uint64) error {
	tx, err := q.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO collections(address, standard, deploy_block)
		 VALUES($1,$2,$3)
		 ON CONFLICT(address) DO NOTHING`,
		address, standard, firstSeenBlock); err != nil {
		_ = tx.Rollback(ctx)
		return fmt.Errorf("collections insert: %w", err)
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO tracked_collections(address, first_seen_block)
		 VALUES($1,$2)
		 ON CONFLICT(address) DO NOTHING`,
		address, firstSeenBlock); err != nil {
		_ = tx.Rollback(ctx)
		return fmt.Errorf("tracked_collections insert: %w", err)
	}
	return tx.Commit(ctx)
}

func (q *Q) MarkCollectionBackfilled(ctx context.Context, address string) error {
	_, err := q.pool.Exec(ctx,
		`UPDATE tracked_collections SET backfilled=true WHERE address=$1`, address)
	return err
}

func (q *Q) SetCollectionVerified(ctx context.Context, address string, verified bool) error {
	_, err := q.pool.Exec(ctx,
		`UPDATE tracked_collections SET verified=$2 WHERE address=$1`, address, verified)
	return err
}

func (q *Q) IsCollectionTracked(ctx context.Context, address string) (bool, error) {
	var exists bool
	err := q.pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM tracked_collections WHERE address=$1)`,
		address).Scan(&exists)
	return exists, err
}

// ── nft_metadata + nft_attributes ─────────────────────────────────────────

func (q *Q) UpsertNFTMetadata(ctx context.Context, collection, tokenID, uri, name, description, imageURI string) error {
	_, err := q.pool.Exec(ctx,
		`INSERT INTO nft_metadata(collection, token_id, uri, name, description, image_uri, fetched_at)
		 VALUES($1,$2,$3,$4,$5,$6,now())
		 ON CONFLICT(collection, token_id) DO UPDATE
		 SET uri=EXCLUDED.uri, name=EXCLUDED.name, description=EXCLUDED.description,
		     image_uri=EXCLUDED.image_uri, fetched_at=now()`,
		collection, tokenID, uri, name, description, imageURI)
	return err
}

type NFTAttribute struct {
	TraitType string `json:"trait_type"`
	Value     string `json:"value"`
}

// ReplaceNFTAttributes wipes and reinserts the attribute set for a token.
func (q *Q) ReplaceNFTAttributes(ctx context.Context, collection, tokenID string, attrs []NFTAttribute) error {
	tx, err := q.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx,
		`DELETE FROM nft_attributes WHERE collection=$1 AND token_id=$2`,
		collection, tokenID); err != nil {
		_ = tx.Rollback(ctx)
		return err
	}
	for _, a := range attrs {
		if a.TraitType == "" || a.Value == "" {
			continue
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO nft_attributes(collection, token_id, trait_type, value)
			 VALUES($1,$2,$3,$4)
			 ON CONFLICT(collection, token_id, trait_type) DO UPDATE SET value=EXCLUDED.value`,
			collection, tokenID, a.TraitType, a.Value); err != nil {
			_ = tx.Rollback(ctx)
			return err
		}
	}
	return tx.Commit(ctx)
}

func (q *Q) GetNFTAttributes(ctx context.Context, collection, tokenID string) ([]NFTAttribute, error) {
	rows, err := q.pool.Query(ctx,
		`SELECT trait_type, value FROM nft_attributes
		 WHERE collection=$1 AND token_id=$2 ORDER BY trait_type`,
		collection, tokenID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []NFTAttribute
	for rows.Next() {
		var a NFTAttribute
		if err := rows.Scan(&a.TraitType, &a.Value); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// ── notifications ──────────────────────────────────────────────────────────

type NotificationRow struct {
	ID        int64     `json:"id"`
	Recipient string    `json:"recipient"`
	Kind      string    `json:"kind"`
	Payload   string    `json:"payload"` // JSON string
	ReadAt    *time.Time `json:"read_at,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

func (q *Q) InsertNotification(ctx context.Context, recipient, kind, payloadJSON string) (int64, error) {
	var id int64
	err := q.pool.QueryRow(ctx,
		`INSERT INTO notifications(recipient, kind, payload)
		 VALUES($1,$2,$3::jsonb) RETURNING id`,
		recipient, kind, payloadJSON).Scan(&id)
	return id, err
}

func (q *Q) ListNotifications(ctx context.Context, recipient string, unreadOnly bool, limit int) ([]NotificationRow, error) {
	if limit == 0 || limit > 200 {
		limit = 50
	}
	where := "WHERE recipient=$1"
	if unreadOnly {
		where += " AND read_at IS NULL"
	}
	rows, err := q.pool.Query(ctx,
		`SELECT id, recipient, kind, payload::text, read_at, created_at
		 FROM notifications `+where+` ORDER BY created_at DESC LIMIT $2`,
		recipient, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []NotificationRow
	for rows.Next() {
		var n NotificationRow
		if err := rows.Scan(&n.ID, &n.Recipient, &n.Kind, &n.Payload, &n.ReadAt, &n.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

func (q *Q) CountUnreadNotifications(ctx context.Context, recipient string) (int64, error) {
	var n int64
	err := q.pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM notifications WHERE recipient=$1 AND read_at IS NULL`,
		recipient).Scan(&n)
	return n, err
}

func (q *Q) MarkNotificationsRead(ctx context.Context, recipient string) error {
	_, err := q.pool.Exec(ctx,
		`UPDATE notifications SET read_at=now() WHERE recipient=$1 AND read_at IS NULL`,
		recipient)
	return err
}

// ── profiles ───────────────────────────────────────────────────────────────

type ProfileRow struct {
	Address     string    `json:"address"`
	DisplayName string    `json:"display_name"`
	Bio         string    `json:"bio"`
	AvatarURI   string    `json:"avatar_uri"`
	BannerURI   string    `json:"banner_uri"`
	Twitter     string    `json:"twitter"`
	Website     string    `json:"website"`
	UpdatedAt   time.Time `json:"updated_at"`
}

func (q *Q) UpsertProfile(ctx context.Context, p ProfileRow) error {
	if err := q.UpsertUser(ctx, p.Address); err != nil {
		return err
	}
	_, err := q.pool.Exec(ctx,
		`INSERT INTO profiles(address, display_name, bio, avatar_uri, banner_uri, twitter, website, updated_at)
		 VALUES($1,$2,$3,$4,$5,$6,$7,now())
		 ON CONFLICT(address) DO UPDATE
		 SET display_name=EXCLUDED.display_name, bio=EXCLUDED.bio,
		     avatar_uri=EXCLUDED.avatar_uri, banner_uri=EXCLUDED.banner_uri,
		     twitter=EXCLUDED.twitter, website=EXCLUDED.website, updated_at=now()`,
		p.Address, p.DisplayName, p.Bio, p.AvatarURI, p.BannerURI, p.Twitter, p.Website)
	return err
}

func (q *Q) GetProfile(ctx context.Context, address string) (*ProfileRow, error) {
	var p ProfileRow
	var displayName, bio, avatarURI, bannerURI, twitter, website *string
	err := q.pool.QueryRow(ctx,
		`SELECT address, display_name, bio, avatar_uri, banner_uri, twitter, website, updated_at
		 FROM profiles WHERE address=$1`, address).
		Scan(&p.Address, &displayName, &bio, &avatarURI, &bannerURI, &twitter, &website, &p.UpdatedAt)
	if err == pgx.ErrNoRows {
		return nil, fmt.Errorf("profile not found")
	}
	if err != nil {
		return nil, err
	}
	if displayName != nil {
		p.DisplayName = *displayName
	}
	if bio != nil {
		p.Bio = *bio
	}
	if avatarURI != nil {
		p.AvatarURI = *avatarURI
	}
	if bannerURI != nil {
		p.BannerURI = *bannerURI
	}
	if twitter != nil {
		p.Twitter = *twitter
	}
	if website != nil {
		p.Website = *website
	}
	return &p, nil
}

// ── reports ────────────────────────────────────────────────────────────────

type ReportRow struct {
	ID         int64     `json:"id"`
	Reporter   string    `json:"reporter"`
	TargetKind string    `json:"target_kind"`
	TargetRef  string    `json:"target_ref"`
	Reason     string    `json:"reason"`
	Notes      string    `json:"notes"`
	Status     string    `json:"status"`
	CreatedAt  time.Time `json:"created_at"`
}

func (q *Q) InsertReport(ctx context.Context, r ReportRow) (int64, error) {
	var id int64
	err := q.pool.QueryRow(ctx,
		`INSERT INTO reports(reporter, target_kind, target_ref, reason, notes)
		 VALUES($1,$2,$3,$4,$5) RETURNING id`,
		r.Reporter, r.TargetKind, r.TargetRef, r.Reason, r.Notes).Scan(&id)
	return id, err
}

func (q *Q) ListReports(ctx context.Context, status string, limit int) ([]ReportRow, error) {
	if limit == 0 || limit > 200 {
		limit = 50
	}
	where := ""
	args := []any{limit}
	if status != "" {
		args = append(args, status)
		where = "WHERE status=$2"
	}
	rows, err := q.pool.Query(ctx,
		`SELECT id, reporter, target_kind, target_ref, reason, COALESCE(notes,''), status, created_at
		 FROM reports `+where+` ORDER BY created_at DESC LIMIT $1`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ReportRow
	for rows.Next() {
		var r ReportRow
		if err := rows.Scan(&r.ID, &r.Reporter, &r.TargetKind, &r.TargetRef,
			&r.Reason, &r.Notes, &r.Status, &r.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ── offer_positions ────────────────────────────────────────────────────────

type OfferPositionRow struct {
	Collection    string    `json:"collection"`
	TokenID       string    `json:"token_id"`
	Bidder        string    `json:"bidder"`
	Standard      string    `json:"standard"`
	Units         string    `json:"units"`
	TotalOfferWei string    `json:"total_offer_wei"`
	TotalFeeWei   string    `json:"total_fee_wei"`
	FirstAt       time.Time `json:"first_at"`
	ExpiresAt     time.Time `json:"expires_at"`
	Status        string    `json:"status"`
}

func (q *Q) UpsertOfferPosition(ctx context.Context, r OfferPositionRow) error {
	// Status field is intentionally not in the upsert SET — chain events drive transitions.
	_, err := q.pool.Exec(ctx,
		`INSERT INTO offer_positions(collection, token_id, bidder, standard, units,
		    total_offer_wei, total_fee_wei, first_at, expires_at, status)
		 VALUES($1,$2,$3,$4::token_standard,$5,$6,$7,$8,$9,'pending')
		 ON CONFLICT(collection, token_id, bidder) DO UPDATE
		 SET total_offer_wei=EXCLUDED.total_offer_wei,
		     total_fee_wei=EXCLUDED.total_fee_wei,
		     expires_at=EXCLUDED.expires_at`,
		r.Collection, r.TokenID, r.Bidder, r.Standard, r.Units,
		r.TotalOfferWei, r.TotalFeeWei, r.FirstAt, r.ExpiresAt)
	return err
}

func (q *Q) SetOfferPositionStatus(ctx context.Context, collection, tokenID, bidder, status string) error {
	_, err := q.pool.Exec(ctx,
		`UPDATE offer_positions SET status=$4::offer_status
		 WHERE collection=$1 AND token_id=$2 AND bidder=$3`,
		collection, tokenID, bidder, status)
	return err
}

func (q *Q) DeleteOfferPosition(ctx context.Context, collection, tokenID, bidder string) error {
	_, err := q.pool.Exec(ctx,
		`DELETE FROM offer_positions WHERE collection=$1 AND token_id=$2 AND bidder=$3`,
		collection, tokenID, bidder)
	return err
}

func (q *Q) GetOfferPosition(ctx context.Context, collection, tokenID, bidder string) (*OfferPositionRow, error) {
	var r OfferPositionRow
	err := q.pool.QueryRow(ctx,
		`SELECT collection, token_id::text, bidder, standard::text, units::text,
		        total_offer_wei::text, total_fee_wei::text, first_at, expires_at, status::text
		 FROM offer_positions WHERE collection=$1 AND token_id=$2 AND bidder=$3`,
		collection, tokenID, bidder).
		Scan(&r.Collection, &r.TokenID, &r.Bidder, &r.Standard, &r.Units,
			&r.TotalOfferWei, &r.TotalFeeWei, &r.FirstAt, &r.ExpiresAt, &r.Status)
	if err == pgx.ErrNoRows {
		return nil, fmt.Errorf("offer position not found")
	}
	return &r, err
}

func (q *Q) GetOfferPositionsForToken(ctx context.Context, collection, tokenID string) ([]OfferPositionRow, error) {
	rows, err := q.pool.Query(ctx,
		`SELECT collection, token_id::text, bidder, standard::text, units::text,
		        total_offer_wei::text, total_fee_wei::text, first_at, expires_at, status::text
		 FROM offer_positions WHERE collection=$1 AND token_id=$2 AND status='pending'
		 ORDER BY CAST(total_offer_wei AS numeric) DESC LIMIT 200`,
		collection, tokenID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []OfferPositionRow
	for rows.Next() {
		var r OfferPositionRow
		if err := rows.Scan(&r.Collection, &r.TokenID, &r.Bidder, &r.Standard, &r.Units,
			&r.TotalOfferWei, &r.TotalFeeWei, &r.FirstAt, &r.ExpiresAt, &r.Status); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (q *Q) GetOfferPositionsByBidder(ctx context.Context, bidder string) ([]OfferPositionRow, error) {
	rows, err := q.pool.Query(ctx,
		`SELECT collection, token_id::text, bidder, standard::text, units::text,
		        total_offer_wei::text, total_fee_wei::text, first_at, expires_at, status::text
		 FROM offer_positions WHERE bidder=$1 AND status='pending'
		 ORDER BY expires_at ASC LIMIT 200`,
		bidder)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []OfferPositionRow
	for rows.Next() {
		var r OfferPositionRow
		if err := rows.Scan(&r.Collection, &r.TokenID, &r.Bidder, &r.Standard, &r.Units,
			&r.TotalOfferWei, &r.TotalFeeWei, &r.FirstAt, &r.ExpiresAt, &r.Status); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// GetReceivedOfferPositions returns pending positions on tokens currently held by `owner`.
func (q *Q) GetReceivedOfferPositions(ctx context.Context, owner string) ([]OfferPositionRow, error) {
	rows, err := q.pool.Query(ctx,
		`SELECT p.collection, p.token_id::text, p.bidder, p.standard::text, p.units::text,
		        p.total_offer_wei::text, p.total_fee_wei::text, p.first_at, p.expires_at, p.status::text
		 FROM offer_positions p
		 JOIN nft_ownership o ON o.collection=p.collection AND o.token_id=p.token_id
		 WHERE o.owner=$1 AND p.status='pending'
		 ORDER BY CAST(p.total_offer_wei AS numeric) DESC LIMIT 200`,
		owner)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []OfferPositionRow
	for rows.Next() {
		var r OfferPositionRow
		if err := rows.Scan(&r.Collection, &r.TokenID, &r.Bidder, &r.Standard, &r.Units,
			&r.TotalOfferWei, &r.TotalFeeWei, &r.FirstAt, &r.ExpiresAt, &r.Status); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// GetExpiredOfferPositions returns pending positions whose expiresAt is past now.
// Used by the keeper to drive on-chain refundExpired calls.
func (q *Q) GetExpiredOfferPositions(ctx context.Context, limit int) ([]OfferPositionRow, error) {
	if limit == 0 || limit > 200 {
		limit = 50
	}
	rows, err := q.pool.Query(ctx,
		`SELECT collection, token_id::text, bidder, standard::text, units::text,
		        total_offer_wei::text, total_fee_wei::text, first_at, expires_at, status::text
		 FROM offer_positions WHERE status='pending' AND expires_at < now()
		 ORDER BY expires_at ASC LIMIT $1`,
		limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []OfferPositionRow
	for rows.Next() {
		var r OfferPositionRow
		if err := rows.Scan(&r.Collection, &r.TokenID, &r.Bidder, &r.Standard, &r.Units,
			&r.TotalOfferWei, &r.TotalFeeWei, &r.FirstAt, &r.ExpiresAt, &r.Status); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// GetRecentTransactions returns the last `limit` marketplace events across all tables,
// ordered newest first. Used by the /api/v1/activity endpoint.
func (q *Q) GetRecentTransactions(ctx context.Context, limit int) ([]ActivityRow, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := q.pool.Query(ctx, `
		SELECT type, collection, token_id::text, amount_wei::text, at, tx_hash FROM (
			SELECT 'Listed'          AS type, collection, token_id, price_wei      AS amount_wei, listed_at    AS at, tx_hash   FROM listings
			UNION ALL
			SELECT 'Sold',                    collection, token_id, price_wei,                    occurred_at,        tx_hash   FROM sales
			UNION ALL
			SELECT 'AuctionCreated',          collection, token_id, reserve_price_wei,            starts_at,          create_tx FROM auctions
			UNION ALL
			SELECT 'BidPlaced', a.collection, a.token_id, b.amount_wei, b.placed_at, b.tx_hash
			FROM bids b JOIN auctions a ON a.auction_id = b.auction_id
		) AS activity
		ORDER BY at DESC
		LIMIT $1
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ActivityRow
	for rows.Next() {
		var r ActivityRow
		if err := rows.Scan(&r.Type, &r.Collection, &r.TokenID, &r.AmountWei, &r.Timestamp, &r.TxHash); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
