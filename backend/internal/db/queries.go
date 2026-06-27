package db

import (
	"context"
	"fmt"
	"math/big"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/rs/zerolog/log"
)

// Q wraps a connection pool and exposes typed query methods.
type Q struct{ pool PgxPool }

// New builds a Q over any PgxPool (a *pgxpool.Pool in production, a mock in tests).
func New(pool PgxPool) *Q { return &Q{pool} }

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
	Verified    bool // curation badge (admin-set)
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
		`SELECT address, name, symbol, standard::text, deploy_block, verified
		 FROM collections WHERE address=$1`, address).
		Scan(&c.Address, &c.Name, &c.Symbol, &c.Standard, &c.DeployBlock, &c.Verified)
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
	return ParseWeiOrZero(priceStr), nil
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
	return ParseWeiOrZero(volStr), nil
}

func (q *Q) GetListedCount(ctx context.Context, collection string) (int, error) {
	var count int
	err := q.pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM listings WHERE collection=$1 AND active=true AND expires_at > now()`,
		collection).Scan(&count)
	return count, err
}

// CountActiveListings returns the total number of active, non-orphaned,
// non-expired listings across every collection. Drives the home-page
// counter. Note: SELECT COUNT(*) always returns exactly one row, so the
// pgx.ErrNoRows branch is unreachable; we propagate any actual error and
// let the handler decide whether to fail-soft or fail-hard.
func (q *Q) CountActiveListings(ctx context.Context) (int64, error) {
	var n int64
	err := q.pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM listings WHERE active=true AND NOT orphaned AND expires_at > now()`,
	).Scan(&n)
	return n, err
}

// CountActiveAuctions returns active auction count, excludes settled and
// cancelled. Drives the home-page counter.
func (q *Q) CountActiveAuctions(ctx context.Context) (int64, error) {
	var n int64
	err := q.pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM auctions WHERE status='active' AND ends_at > now()`,
	).Scan(&n)
	return n, err
}

// CountCollections returns the number of tracked collections. The "tracked"
// filter excludes one-off contract rows that never completed ingest.
func (q *Q) CountCollections(ctx context.Context) (int64, error) {
	var n int64
	err := q.pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM collections WHERE tracked=true`,
	).Scan(&n)
	return n, err
}

// TotalVolume24hWei returns the sum of sale price_wei in the last 24 hours
// across every collection. Returned as a decimal-string wei value so the
// template's wei2flr helper handles scale; COALESCE returns "0" on empty
// result so the page still renders "0.00 FLR" rather than an empty string.
func (q *Q) TotalVolume24hWei(ctx context.Context) (string, error) {
	var v string
	err := q.pool.QueryRow(ctx,
		`SELECT COALESCE(SUM(price_wei)::text,'0') FROM sales
		 WHERE occurred_at > now()-interval '24 hours'`,
	).Scan(&v)
	return v, err
}

// CollectionStats is the surface the /collection/:addr page renders.
type CollectionStats struct {
	FloorPriceWei string // lowest active listing price (wei); "0" when empty
	Volume24hWei  string // sums of sales price_wei in last 24h (wei); "0" empty
	ListedCount   int64  // count of active listings
}

// GetCollectionStats fills all three counters in a single round-trip-ish via
// three small queries that share the connection. Each could be a CTE merged
// into one statement; splitting keeps the SQL legible and the index pool is
// cheap (3 round-trips ~= 10ms under typical Coston load).
func (q *Q) GetCollectionStats(ctx context.Context, collection string) (CollectionStats, error) {
	var s CollectionStats
	floor, ferr := q.GetFloorPrice(ctx, collection)
	if ferr == nil && floor != nil {
		s.FloorPriceWei = floor.String()
	}
	vol, verr := q.Get24hVolume(ctx, collection)
	if verr == nil && vol != nil {
		s.Volume24hWei = vol.String()
	}
	listed, lerr := q.GetListedCount(ctx, collection)
	if lerr == nil {
		s.ListedCount = int64(listed)
	}
	return s, nil
}

// ── wei-string helpers (centralised — Priority Stack `parseWeiHelper`) ────

// ParseWei parses a decimal wei-string into *big.Int, returning an explicit
// error on failure (empty, non-numeric, etc.) instead of silently leaving the
// receiver at 0. Centralizes the wei-string → *big.Int conversion so a
// schema-drift / NULL-coalesce failure surfaces loudly rather than as a
// floor/volume that suddenly reads "0 wei". Use ParseWeiOrZero for the
// paths where "missing = 0" is the right semantics (volumes, floors —
// empty collections legitimately have a 0 floor/volume).
func ParseWei(s string) (*big.Int, error) {
	if s == "" {
		return nil, fmt.Errorf("ParseWei: empty value")
	}
	n := new(big.Int)
	if _, ok := n.SetString(s, 10); !ok {
		return nil, fmt.Errorf("ParseWei: not a base-10 integer: %q", s)
	}
	return n, nil
}

// ParseWeiOrZero is the backward-compatible equivalent of the previous
// silent-zero behaviour (parses; if parse fails OR empty, returns 0 with
// a warning-level log so a fresh schema drift is traceable). All five
// prior big.Int.SetString sites were rewritten through this helper.
func ParseWeiOrZero(s string) *big.Int {
	n, err := ParseWei(s)
	if err != nil {
		// Empty is legitimately "no data"; only warn on truly malformed
		// values, otherwise the noisiness would dwarf the indexer logs.
		if s != "" && s != "0" {
			log.Warn().Err(err).Str("input", s).Msg("ParseWeiOrZero: malformed, returning 0")
		}
		return new(big.Int)
	}
	return n
}

// CapWeiLimit clamps a request-style limit (n <= 0 → default, n > max → max)
// so callers cannot fan out unbounded result sets.
func CapWeiLimit(n, def, max int) int {
	if n <= 0 {
		return def
	}
	if n > max {
		return max
	}
	return n
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
	Name        string
	ImageURI    string
	TotalSupply int64 // collection-level total supply (0 when unindexed)
	// Denormalised from collections
	CollectionVerified bool
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

// DeactivateListing closes one seller's listing for a token (multi-listing key).
func (q *Q) DeactivateListing(ctx context.Context, collection, tokenID, seller string) error {
	_, err := q.pool.Exec(ctx,
		`UPDATE listings SET active=false WHERE collection=$1 AND token_id=$2 AND seller=$3`,
		collection, tokenID, seller)
	return err
}

func (q *Q) GetListing(ctx context.Context, collection, tokenID string) (*ListingRow, error) {
	var r ListingRow
	err := q.pool.QueryRow(ctx,
		`SELECT l.collection, l.token_id::text, l.seller, l.price_wei::text, l.amount,
		        l.standard::text, l.expires_at, l.listed_at, l.tx_hash,
		        COALESCE(m.name, t.name, ''), COALESCE(m.image_uri, t.image_uri, '')
		 FROM listings l
		 LEFT JOIN nft_metadata m ON m.collection=l.collection AND m.token_id=l.token_id
		 LEFT JOIN nft_tokens t ON t.collection=l.collection AND l.token_id=t.token_id
		 WHERE l.collection=$1 AND l.token_id=$2 AND l.active=true
		   AND NOT l.orphaned AND l.expires_at > now()`,
		collection, tokenID).
		Scan(&r.Collection, &r.TokenID, &r.Seller, &r.PriceWei, &r.Amount,
			&r.Standard, &r.ExpiresAt, &r.ListedAt, &r.TxHash, &r.Name, &r.ImageURI)
	if err == pgx.ErrNoRows {
		return nil, fmt.Errorf("listing not found")
	}
	return &r, err
}

type ListingsFilter struct {
	Collection string
	Seller     string
	Sort       string            // "recent" | "price_asc" | "price_desc"
	Traits     map[string]string // trait_type -> value (AND across traits)
	Limit      int
	Cursor     string
}

func (q *Q) ListActiveListings(ctx context.Context, f ListingsFilter) ([]ListingRow, error) {
	if f.Limit == 0 || f.Limit > 100 {
		f.Limit = 50
	}
	args := []any{f.Limit}
	where := "WHERE l.active=true AND NOT l.orphaned AND l.expires_at > now()"
	if f.Collection != "" {
		args = append(args, f.Collection)
		where += fmt.Sprintf(" AND l.collection=$%d", len(args))
	}
	if f.Seller != "" {
		args = append(args, f.Seller)
		where += fmt.Sprintf(" AND l.seller=$%d", len(args))
	}
	for tt, v := range f.Traits {
		if tt == "" || v == "" {
			continue
		}
		args = append(args, tt)
		ttIdx := len(args)
		args = append(args, v)
		vIdx := len(args)
		where += fmt.Sprintf(` AND EXISTS (SELECT 1 FROM nft_attributes a
			WHERE a.collection=l.collection AND a.token_id=l.token_id
			  AND a.trait_type=$%d AND a.value=$%d)`, ttIdx, vIdx)
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
		        COALESCE(m.name, t.name, ''), COALESCE(m.image_uri, t.image_uri, ''),
		        COALESCE(c.verified,false),
		        0 AS total_supply
		 FROM listings l
		 LEFT JOIN nft_metadata m ON m.collection=l.collection AND m.token_id=l.token_id
		 LEFT JOIN nft_tokens t ON t.collection=l.collection AND l.token_id=t.token_id
		 LEFT JOIN collections c ON c.address=l.collection
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
			&r.Standard, &r.ExpiresAt, &r.ListedAt, &r.TxHash, &r.Name, &r.ImageURI,
			&r.CollectionVerified, &r.TotalSupply); err != nil {
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
	Name            string
	ImageURI        string
}

// auctionSelectCols is the canonical SELECT projection for an AuctionRow.
// Centralised here so GetAuction, ListAuctions, GetExpiredActiveAuctions,
// GetInactiveAuctions, and ListActiveAuctions stay byte-for-byte identical.
// Note: a.highest_bidder (NOT highest_bider — typo guarded here).
const auctionSelectCols = `a.auction_id, a.collection, a.token_id::text, a.seller, a.standard::text,
		        a.reserve_price_wei::text, a.highest_bid_wei::text, COALESCE(a.highest_bidder,''),
		        a.min_increment_bps, a.starts_at, a.ends_at, a.status::text, a.create_tx,
		        COALESCE(m.name, t.name, ''), COALESCE(m.image_uri, t.image_uri, '')`

const auctionFromJoin = ` FROM auctions a
		 LEFT JOIN nft_metadata m ON m.collection=a.collection AND m.token_id=a.token_id
		 LEFT JOIN nft_tokens t ON t.collection=a.collection AND a.token_id=t.token_id`

func scanAuctionRow(rows pgx.Rows) (AuctionRow, error) {
	var r AuctionRow
	err := rows.Scan(&r.AuctionID, &r.Collection, &r.TokenID, &r.Seller, &r.Standard,
		&r.ReservePriceWei, &r.HighestBidWei, &r.HighestBidder, &r.MinIncrementBps,
		&r.StartsAt, &r.EndsAt, &r.Status, &r.CreateTx, &r.Name, &r.ImageURI)
	return r, err
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

// ExtendAuction pushes out an active auction's end time after an on-chain anti-snipe
// extension (the AuctionExtended event). No-op on non-active auctions.
func (q *Q) ExtendAuction(ctx context.Context, auctionID int64, endsAt time.Time) error {
	_, err := q.pool.Exec(ctx,
		`UPDATE auctions SET ends_at=$1 WHERE auction_id=$2 AND status='active'`, endsAt, auctionID)
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
		`SELECT `+auctionSelectCols+auctionFromJoin+` WHERE a.auction_id=$1`, auctionID).
		Scan(&r.AuctionID, &r.Collection, &r.TokenID, &r.Seller, &r.Standard,
			&r.ReservePriceWei, &r.HighestBidWei, &r.HighestBidder,
			&r.MinIncrementBps, &r.StartsAt, &r.EndsAt, &r.Status, &r.CreateTx,
			&r.Name, &r.ImageURI)
	if err == pgx.ErrNoRows {
		return nil, fmt.Errorf("auction not found: %d", auctionID)
	}
	return &r, err
}

type AuctionsFilter struct {
	Collection string
	Seller     string
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
		where += fmt.Sprintf(" AND a.collection=$%d", len(args))
	}
	if f.Seller != "" {
		args = append(args, f.Seller)
		where += fmt.Sprintf(" AND a.seller=$%d", len(args))
	}
	if f.Status != "" {
		args = append(args, f.Status)
		where += fmt.Sprintf(" AND a.status=$%d", len(args))
	}
	rows, err := q.pool.Query(ctx,
		`SELECT `+auctionSelectCols+auctionFromJoin+` `+where+` ORDER BY a.ends_at ASC LIMIT $1`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AuctionRow
	for rows.Next() {
		r, err := scanAuctionRow(rows)
		if err != nil {
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
		`SELECT `+auctionSelectCols+auctionFromJoin+
			` WHERE a.status='active' AND a.ends_at < now()
		 ORDER BY a.ends_at ASC LIMIT 100`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AuctionRow
	for rows.Next() {
		r, err := scanAuctionRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (q *Q) GetInactiveAuctions(ctx context.Context) ([]AuctionRow, error) {
	rows, err := q.pool.Query(ctx,
		`SELECT `+auctionSelectCols+auctionFromJoin+
			` WHERE a.status='active'
		   AND a.highest_bidder IS NULL
		   AND a.starts_at + interval '30 minutes' < now()
		 ORDER BY a.starts_at ASC LIMIT 100`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AuctionRow
	for rows.Next() {
		r, err := scanAuctionRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ── Loser refunds (keeper) ────────────────────────────────────────────────

// RefundableAuction is a finalized auction whose losing bidders' escrow has not
// yet been returned on-chain. Winner is the highest_bidder ("" when the auction
// cancelled with no qualifying bid — every bidder is then a loser to refund).
type RefundableAuction struct {
	AuctionID int64
	Status    string // "settled" | "cancelled"
	Winner    string
}

// GetSettledUnrefundedAuctions returns finalized auctions still owing loser
// refunds, throttled so a just-attempted auction is skipped for 2 minutes
// (refundLosers is idempotent on-chain, so re-sends are safe but wasteful).
func (q *Q) GetSettledUnrefundedAuctions(ctx context.Context) ([]RefundableAuction, error) {
	rows, err := q.pool.Query(ctx,
		`SELECT auction_id, status::text, COALESCE(highest_bidder,'')
		   FROM auctions
		  WHERE status IN ('settled', 'cancelled') AND NOT losers_refunded
		    AND (refund_attempt_at IS NULL OR refund_attempt_at < now() - interval '2 minutes')
		  ORDER BY auction_id ASC LIMIT 100`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RefundableAuction
	for rows.Next() {
		var r RefundableAuction
		if err := rows.Scan(&r.AuctionID, &r.Status, &r.Winner); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// MarkLosersRefunded flags an auction as fully refunded so the sweeper stops
// re-sending refundLosers for it.
func (q *Q) MarkLosersRefunded(ctx context.Context, auctionID int64) error {
	_, err := q.pool.Exec(ctx,
		`UPDATE auctions SET losers_refunded = TRUE WHERE auction_id=$1`, auctionID)
	return err
}

// SetRefundAttempt records that the sweeper just broadcast refundLosers for an
// auction, throttling the next attempt.
func (q *Q) SetRefundAttempt(ctx context.Context, auctionID int64) error {
	_, err := q.pool.Exec(ctx,
		`UPDATE auctions SET refund_attempt_at = now() WHERE auction_id=$1`, auctionID)
	return err
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

// EffectiveBidRow is one bidder's cumulative position on an auction.
type EffectiveBidRow struct {
	Bidder       string    `json:"bidder"`
	EffectiveWei string    `json:"effective_wei"`
	BidCount     int64     `json:"bid_count"`
	LastBidAt    time.Time `json:"last_bid_at"`
}

// GetEffectiveBids returns per-bidder cumulative totals for an auction,
// highest effective bid first. The leader (row 0) is the current/settlement
// winner under the cumulative-bid model. Backed by the effective_bids view.
//
// Hard cap at LIMIT 200 — see Priority Stack `getEffectiveBidsLimit`.
// Pre-fix, no LIMIT and a contested auction with 10k+ tiny incremental bids
// OOM-ed rendering pages. Clamping at 200 covers the realistic active-bidder
// spectrum and keeps JSON / template payloads bounded.
func (q *Q) GetEffectiveBids(ctx context.Context, auctionID int64) ([]EffectiveBidRow, error) {
	rows, err := q.pool.Query(ctx,
		`SELECT bidder, effective_wei::text, bid_count, last_bid_at
		   FROM effective_bids
		  WHERE auction_id = $1
		  ORDER BY effective_wei DESC, last_bid_at ASC
		  LIMIT 200`,
		auctionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []EffectiveBidRow
	for rows.Next() {
		var r EffectiveBidRow
		if err := rows.Scan(&r.Bidder, &r.EffectiveWei, &r.BidCount, &r.LastBidAt); err != nil {
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
	return ParseWeiOrZero(volStr), nil
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

// CollectionStatsRow is one collection's aggregate inputs to the trending score.
type CollectionStatsRow struct {
	Collection string
	Views      int64
	Bids       int64
	VolumeWei  *big.Int
}

// GetCollectionStatsSince returns views/bids/volume for every collection in one
// grouped query, replacing the per-collection N+1 the score worker used to
// issue (3 queries × collections × windows per minute).
func (q *Q) GetCollectionStatsSince(ctx context.Context, since time.Time, limit int) ([]CollectionStatsRow, error) {
	rows, err := q.pool.Query(ctx, `
		SELECT c.address,
		       COALESCE(v.views, 0),
		       COALESCE(b.bids, 0),
		       COALESCE(s.volume, '0')
		  FROM collections c
		  LEFT JOIN (SELECT collection, SUM(views) AS views
		               FROM nft_tokens GROUP BY collection) v ON v.collection = c.address
		  LEFT JOIN (SELECT a.collection, COUNT(*) AS bids
		               FROM bids bd JOIN auctions a ON a.auction_id = bd.auction_id
		              WHERE bd.placed_at >= $1 GROUP BY a.collection) b ON b.collection = c.address
		  LEFT JOIN (SELECT collection, SUM(price_wei)::text AS volume
		               FROM sales WHERE occurred_at >= $1 GROUP BY collection) s ON s.collection = c.address
		  LIMIT $2`, since, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CollectionStatsRow
	for rows.Next() {
		var r CollectionStatsRow
		var volStr string
		if err := rows.Scan(&r.Collection, &r.Views, &r.Bids, &volStr); err != nil {
			return nil, err
		}
		r.VolumeWei = ParseWeiOrZero(volStr)
		out = append(out, r)
	}
	return out, rows.Err()
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
		s.VolumeWei = ParseWeiOrZero(volStr)
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
		`SELECT COALESCE(m.name, t.name, ''), COALESCE(m.image_uri, t.image_uri, '')
		 FROM nft_tokens t
		 LEFT JOIN nft_metadata m ON m.collection=t.collection AND m.token_id=t.token_id
		 WHERE t.collection=$1 AND t.token_id=$2`, collection, tokenID).Scan(&name, &imageURI)
	if err == pgx.ErrNoRows {
		err = q.pool.QueryRow(ctx,
			`SELECT COALESCE(name,''), COALESCE(image_uri,'') FROM nft_metadata
			 WHERE collection=$1 AND token_id=$2`, collection, tokenID).Scan(&name, &imageURI)
		if err == pgx.ErrNoRows {
			return "", "", nil
		}
	}
	return name, imageURI, err
}

func (q *Q) IncrementTokenViews(ctx context.Context, collection, tokenID string) error {
	_, err := q.pool.Exec(ctx,
		`UPDATE nft_tokens SET views=views+1 WHERE collection=$1 AND token_id=$2`,
		collection, tokenID)
	return err
}

// GetTokenAttributes returns the on-chain traits for one token. Drives the
// Attributes grid on /token/:addr/:id. Order matches storage (by trait_type
// then value) so the layout is stable across reloads.
func (q *Q) GetTokenAttributes(ctx context.Context, collection, tokenID string) ([]Trait, error) {
	rows, err := q.pool.Query(ctx,
		`SELECT trait_type, value FROM nft_attributes
		 WHERE collection=$1 AND token_id=$2
		 ORDER BY trait_type ASC, value ASC`,
		collection, tokenID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Trait
	for rows.Next() {
		var t Trait
		if err := rows.Scan(&t.Type, &t.Value); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// ── Offers ────────────────────────────────────────────────────────────────

type OfferRow struct {
	OfferID    string
	Bidder     string
	Collection string
	TokenID    string
	AmountWei  string // principal_wei: cumulative escrowed principal (fee excluded)
	FeeWei     string
	Units      int64
	Standard   string
	ExpiresAt  time.Time
	Status     string
	MakeTx     string
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

func (q *Q) GetOffer(ctx context.Context, offerID string) (*OfferRow, error) {
	var r OfferRow
	err := q.pool.QueryRow(ctx,
		`SELECT offer_id::text, bidder, collection, token_id::text, principal_wei::text,
		        fee_wei::text, units, standard::text, expires_at, status::text,
		        COALESCE(make_tx,''), created_at
		 FROM offers WHERE offer_id=$1`, offerID).
		Scan(&r.OfferID, &r.Bidder, &r.Collection, &r.TokenID, &r.AmountWei,
			&r.FeeWei, &r.Units, &r.Standard, &r.ExpiresAt, &r.Status, &r.MakeTx, &r.CreatedAt)
	if err == pgx.ErrNoRows {
		return nil, fmt.Errorf("offer not found: %s", offerID)
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
			SELECT 1 FROM nft_ownership n
			WHERE n.collection=o.collection AND n.token_id=o.token_id AND n.owner=$%d AND n.units > 0
		)`, len(args))
	}
	rows, err := q.pool.Query(ctx,
		`SELECT o.offer_id::text, o.bidder, o.collection, o.token_id::text,
		        o.principal_wei::text, o.fee_wei::text, o.units, o.standard::text,
		        o.expires_at, o.status::text, COALESCE(o.make_tx,''), o.created_at
		 FROM offers o `+where+` ORDER BY o.created_at DESC LIMIT $1`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []OfferRow
	for rows.Next() {
		var r OfferRow
		if err := rows.Scan(&r.OfferID, &r.Bidder, &r.Collection, &r.TokenID,
			&r.AmountWei, &r.FeeWei, &r.Units, &r.Standard, &r.ExpiresAt,
			&r.Status, &r.MakeTx, &r.CreatedAt); err != nil {
			return nil, err
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
	Kind       string `json:"kind"` // "nft" | "collection"
	Collection string `json:"collection"`
	TokenID    string `json:"token_id,omitempty"`
	Name       string `json:"name"`
	ImageURI   string `json:"image_uri,omitempty"`
}

// Search finds NFTs and collections matching query using Postgres full-text search.
// Returns up to limit results per kind (nft + collection combined).
//
// LIMIT pushed into each UNION ALL branch via parens so Postgres can plan it
// properly. Outer ORDER BY + LIMIT cap the merged result. See Priority Stack
// `getRecentTxnsLimit` for the post-fix story.
func (q *Q) Search(ctx context.Context, query string, limit int) ([]SearchResult, error) {
	if limit <= 0 || limit > 50 {
		limit = 20
	}
	rows, err := q.pool.Query(ctx, `
		(
			SELECT 'nft'::text,
			       t.collection::text,
			       t.token_id::text,
			       coalesce(m.name, t.name, '') AS name,
			       coalesce(m.image_uri, t.image_uri, '') AS image_uri
			FROM nft_tokens t
			LEFT JOIN nft_metadata m ON m.collection=t.collection AND m.token_id=t.token_id
			WHERE t.search_vec @@ plainto_tsquery('english', $1)
			ORDER BY t.token_id ASC
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
			ORDER BY c.name ASC
			LIMIT $2
		)
		ORDER BY 4 ASC
		LIMIT $2
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

// ── Pending withdrawals ("withdraw required" tracking) ────────────────────

// SeedPendingWithdrawal records an address whose refund MAY have fallen back
// to pull-withdrawal (LoserRefunded/RefundPushed fire on both push outcomes).
// The withdrawal sweeper verifies against on-chain pendingReturns afterwards.
func (q *Q) SeedPendingWithdrawal(ctx context.Context, address string) error {
	_, err := q.pool.Exec(ctx,
		`INSERT INTO pending_withdrawals(address, verified, updated_at)
		 VALUES($1, false, now())
		 ON CONFLICT(address) DO UPDATE SET updated_at = now()`,
		address)
	return err
}

// PendingWithdrawalRow is one address owing (or suspected of owing) a withdrawal.
type PendingWithdrawalRow struct {
	Address   string `json:"address"`
	AmountWei string `json:"amount_wei"`
	Verified  bool   `json:"verified"`
}

// ListPendingWithdrawals returns all candidate/verified rows for the sweeper.
func (q *Q) ListPendingWithdrawals(ctx context.Context, limit int) ([]PendingWithdrawalRow, error) {
	rows, err := q.pool.Query(ctx,
		`SELECT address, amount_wei::text, verified FROM pending_withdrawals
		  ORDER BY updated_at ASC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PendingWithdrawalRow
	for rows.Next() {
		var r PendingWithdrawalRow
		if err := rows.Scan(&r.Address, &r.AmountWei, &r.Verified); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// MarkPendingWithdrawalVerified stores the on-chain owed amount. Returns true
// when this flipped the row from candidate to verified (first confirmation),
// so the caller can notify the user exactly once.
func (q *Q) MarkPendingWithdrawalVerified(ctx context.Context, address, amountWei string) (bool, error) {
	var wasVerified bool
	err := q.pool.QueryRow(ctx,
		`UPDATE pending_withdrawals AS pw
		    SET amount_wei = $2, verified = true, updated_at = now()
		  FROM (SELECT verified FROM pending_withdrawals WHERE address = $1) prev
		  WHERE pw.address = $1
		  RETURNING prev.verified`,
		address, amountWei).Scan(&wasVerified)
	return !wasVerified, err
}

// DeletePendingWithdrawal removes a row once on-chain pendingReturns is zero
// (the push landed after all, or the user withdrew).
func (q *Q) DeletePendingWithdrawal(ctx context.Context, address string) error {
	_, err := q.pool.Exec(ctx, `DELETE FROM pending_withdrawals WHERE address = $1`, address)
	return err
}

// GetVerifiedPendingWithdrawal returns the owed amount for an address, or ""
// when nothing verified is owed. Drives the profile-page withdraw banner.
func (q *Q) GetVerifiedPendingWithdrawal(ctx context.Context, address string) (string, error) {
	var amt string
	err := q.pool.QueryRow(ctx,
		`SELECT amount_wei::text FROM pending_withdrawals
		  WHERE address = $1 AND verified = true`, address).Scan(&amt)
	if err == pgx.ErrNoRows {
		return "", nil
	}
	return amt, err
}

// SetCollectionVerified flips the curation badge on a collection.
func (q *Q) SetCollectionVerified(ctx context.Context, address string, verified bool) error {
	_, err := q.pool.Exec(ctx,
		`UPDATE collections SET verified = $2 WHERE address = $1`, address, verified)
	return err
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
	// Seller-keyed: listings PK is (collection, token_id, seller) — other
	// holders' stacked 1155 listings for the same token must stay active.
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

// UpsertListingAndOwnership atomically writes a listing row and seeds the
// seller's nft_ownership row in one pgx transaction. Replaces the non-
// transactional UpsertListing + EnsureListingSellerOwnership pair in the
// onListed event handler so a crash between the two writes can never leave a
// live listing with no preflight-buildable seller row (which would cause a
// buy preflight race: the listing is visible but seller ownership appears
// missing until the next transfer log lands). The erc1155 branch uses
// GREATEST(...) so a higher balance beats a transient low one; the erc721
// branch DELETEs all ownership rows for (coll,token) then INSERTs one row for
// the lister, mirroring the contract's single-owner invariant.
func (q *Q) UpsertListingAndOwnership(ctx context.Context, r ListingRow) error {
	tx, err := q.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO listings(collection, token_id, seller, price_wei, amount, standard, expires_at, listed_at, tx_hash, active, orphaned)
		 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,true,false)
		 ON CONFLICT(collection, token_id, seller) DO UPDATE
		 SET price_wei=EXCLUDED.price_wei, amount=EXCLUDED.amount,
		     standard=EXCLUDED.standard, expires_at=EXCLUDED.expires_at, listed_at=EXCLUDED.listed_at,
		     tx_hash=EXCLUDED.tx_hash, active=true, orphaned=false`,
		r.Collection, r.TokenID, r.Seller, r.PriceWei, r.Amount, r.Standard, r.ExpiresAt, r.ListedAt, r.TxHash); err != nil {
		_ = tx.Rollback(ctx)
		return fmt.Errorf("upsert listing: %w", err)
	}
	if r.Standard == "erc1155" {
		if _, err := tx.Exec(ctx,
			`INSERT INTO nft_ownership(collection, token_id, owner, units, standard)
			 VALUES($1,$2,$3,$4,'erc1155')
			 ON CONFLICT(collection, token_id, owner)
			 DO UPDATE SET units = GREATEST(nft_ownership.units, EXCLUDED.units), updated_at=now()`,
			r.Collection, r.TokenID, r.Seller, r.Amount); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("seed 1155 ownership: %w", err)
		}
	} else {
		if _, err := tx.Exec(ctx,
			`DELETE FROM nft_ownership WHERE collection=$1 AND token_id=$2`,
			r.Collection, r.TokenID); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("clear 721 ownership: %w", err)
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO nft_ownership(collection, token_id, owner, units, standard)
			 VALUES($1,$2,$3,1,'erc721')`,
			r.Collection, r.TokenID, r.Seller); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("seed 721 ownership: %w", err)
		}
	}
	return tx.Commit(ctx)
}

// AcceptOfferAndRecordSale atomically flips a pending offer's status to
// 'accepted' and records the resulting sale row in one pgx transaction.
// Replaces the non-transactional SetOfferStatus + InsertSale pair in the
// onOfferAccepted event handler so a crash between the two writes can never
// leave the offer frozen on 'pending' (bidder can't re-bid, their escrow is
// still locked on-chain) or leave a sale row referencing an offer that was
// never flipped. The WHERE status='pending' gate is preserved so a duplicate
// event from a re-org doesn't silently overwrite an outcome.
func (q *Q) AcceptOfferAndRecordSale(ctx context.Context,
	collection, tokenID, seller, bidder string,
	priceWei, feeWei, royaltyWei, txHash string,
	blockNumber uint64, occurredAt time.Time,
) error {
	tx, err := q.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx,
		`UPDATE offers SET status='accepted'
		 WHERE collection=$1 AND token_id=$2 AND bidder=$3 AND status='pending'`,
		collection, tokenID, bidder); err != nil {
		_ = tx.Rollback(ctx)
		return fmt.Errorf("accept offer: %w", err)
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO sales(collection,token_id,seller,buyer,price_wei,fee_wei,royalty_wei,tx_hash,block_number,occurred_at)
		 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10) ON CONFLICT(tx_hash) DO NOTHING`,
		collection, tokenID, seller, bidder, priceWei, feeWei, royaltyWei, txHash, blockNumber, occurredAt); err != nil {
		_ = tx.Rollback(ctx)
		return fmt.Errorf("record sale: %w", err)
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

// GetRecentTransactions returns the last `limit` marketplace events across all tables,
// ordered newest first. Used by the /api/v1/activity endpoint.
//
// LIMIT is PUSHED into each UNION ALL subquery (parenthesised for Postgres
// set-op precedence) so the planner can honour per-branch (listed_at /
// occurred_at / placed_at / starts_at) indexes. Pre-fix, LIMIT sat on the
// outer wrapper and the planner materialised full historical windows from
// every branch before the global ORDER BY — full Seq Scan + in-memory merge
// sort on every call. See Priority Stack `getRecentTxnsLimit` 🟠 P1.
func (q *Q) GetRecentTransactions(ctx context.Context, limit int) ([]ActivityRow, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := q.pool.Query(ctx, `
		SELECT type, collection, token_id::text, amount_wei::text, at, tx_hash FROM (
			(SELECT 'Listed' AS type, collection, token_id, price_wei AS amount_wei, listed_at AS at, tx_hash
			   FROM listings ORDER BY listed_at DESC LIMIT $1)
			UNION ALL
			(SELECT 'Sold',            collection, token_id, price_wei,            occurred_at, tx_hash
			   FROM sales ORDER BY occurred_at DESC LIMIT $1)
			UNION ALL
			(SELECT 'AuctionCreated',  collection, token_id, reserve_price_wei,   starts_at,   create_tx
			   FROM auctions ORDER BY starts_at DESC LIMIT $1)
			UNION ALL
			(SELECT 'BidPlaced', a.collection, a.token_id, b.amount_wei, b.placed_at, b.tx_hash
			   FROM bids b JOIN auctions a ON a.auction_id = b.auction_id
			   ORDER BY b.placed_at DESC LIMIT $1)
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
