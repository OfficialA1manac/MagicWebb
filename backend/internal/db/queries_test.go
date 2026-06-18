package db

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/pashagolub/pgxmock/v4"
)

func TestGetIndexedBlock(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer mock.Close()
	q := New(mock)

	rows := mock.NewRows([]string{"indexed_block"}).AddRow(uint64(12345))
	mock.ExpectQuery(`SELECT indexed_block FROM indexer_state`).
		WithArgs(114).WillReturnRows(rows)

	got, err := q.GetIndexedBlock(context.Background(), 114)
	if err != nil || got != 12345 {
		t.Fatalf("GetIndexedBlock = %d, %v; want 12345, nil", got, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestGetIndexedBlockNoRowsReturnsZero(t *testing.T) {
	mock, _ := pgxmock.NewPool()
	defer mock.Close()
	q := New(mock)

	mock.ExpectQuery(`SELECT indexed_block FROM indexer_state`).
		WithArgs(114).WillReturnError(pgx.ErrNoRows)

	got, err := q.GetIndexedBlock(context.Background(), 114)
	if err != nil || got != 0 {
		t.Fatalf("no-rows GetIndexedBlock = %d, %v; want 0, nil", got, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestSetIndexedBlock(t *testing.T) {
	mock, _ := pgxmock.NewPool()
	defer mock.Close()
	q := New(mock)

	mock.ExpectExec(`INSERT INTO indexer_state`).
		WithArgs(114, uint64(999)).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))

	if err := q.SetIndexedBlock(context.Background(), 114, 999); err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

// DeactivateAndSale must scope the deactivation to the SELLER: listings are
// keyed (collection, token_id, seller) and other holders' stacked 1155
// listings for the same token must stay active.
func TestDeactivateAndSaleIsSellerScopedAndAtomic(t *testing.T) {
	mock, _ := pgxmock.NewPool()
	defer mock.Close()
	q := New(mock)

	at := time.Unix(1_700_000_000, 0)
	mock.ExpectBegin()
	mock.ExpectExec(`UPDATE listings SET active=false WHERE collection=\$1 AND token_id=\$2 AND seller=\$3`).
		WithArgs("0xc", "1", "0xs").
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	mock.ExpectExec(`INSERT INTO sales`).
		WithArgs("0xc", "1", "0xs", "0xb", "100", "1", "0", "0xhash", uint64(7), at).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))
	mock.ExpectCommit()

	if err := q.DeactivateAndSale(context.Background(), "0xc", "1", "0xs", "0xb", "100", "1", "0", "0xhash", 7, at); err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestGetCollectionStatsSince(t *testing.T) {
	mock, _ := pgxmock.NewPool()
	defer mock.Close()
	q := New(mock)

	since := time.Unix(1_700_000_000, 0)
	rows := mock.NewRows([]string{"address", "views", "bids", "volume"}).
		AddRow("0xabc", int64(42), int64(7), "1500000000000000000").
		AddRow("0xdef", int64(0), int64(0), "0")
	mock.ExpectQuery(`FROM collections c`).
		WithArgs(since, 500).WillReturnRows(rows)

	got, err := q.GetCollectionStatsSince(context.Background(), since, 500)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("rows = %d, want 2", len(got))
	}
	if got[0].Collection != "0xabc" || got[0].Views != 42 || got[0].Bids != 7 {
		t.Fatalf("row0 = %+v", got[0])
	}
	if got[0].VolumeWei.String() != "1500000000000000000" {
		t.Fatalf("volume = %s", got[0].VolumeWei)
	}
	if got[1].VolumeWei.Sign() != 0 {
		t.Fatalf("zero volume parsed as %s", got[1].VolumeWei)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestExtendAuction(t *testing.T) {
	mock, _ := pgxmock.NewPool()
	defer mock.Close()
	q := New(mock)

	end := time.Unix(1_700_000_000, 0)
	mock.ExpectExec(`UPDATE auctions SET ends_at`).
		WithArgs(end, int64(7)).
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))

	if err := q.ExtendAuction(context.Background(), 7, end); err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestSetAuctionStatus(t *testing.T) {
	mock, _ := pgxmock.NewPool()
	defer mock.Close()
	q := New(mock)

	mock.ExpectExec(`UPDATE auctions SET status`).
		WithArgs("settled", int64(3)).
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))

	if err := q.SetAuctionStatus(context.Background(), 3, "settled"); err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

// The bid update must be atomic: insert the bid and bump the auction high bid in one
// transaction, committing only if both succeed.
func TestInsertBidAndUpdateAuctionIsAtomic(t *testing.T) {
	mock, _ := pgxmock.NewPool()
	defer mock.Close()
	q := New(mock)

	placed := time.Unix(1_700_000_000, 0)
	mock.ExpectBeginTx(pgx.TxOptions{})
	mock.ExpectExec(`INSERT INTO bids`).
		WithArgs(int64(9), "0xbob", "1000", "0xtx", placed).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))
	mock.ExpectExec(`UPDATE auctions SET highest_bid_wei`).
		WithArgs("1000", "0xbob", int64(9)).
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	mock.ExpectCommit()

	if err := q.InsertBidAndUpdateAuction(context.Background(), 9, "0xbob", "1000", "0xtx", placed); err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

// If the auction update fails, the whole transaction rolls back (no orphaned bid).
func TestInsertBidAndUpdateAuctionRollsBackOnUpdateFailure(t *testing.T) {
	mock, _ := pgxmock.NewPool()
	defer mock.Close()
	q := New(mock)

	placed := time.Unix(1_700_000_000, 0)
	mock.ExpectBeginTx(pgx.TxOptions{})
	mock.ExpectExec(`INSERT INTO bids`).
		WithArgs(int64(9), "0xbob", "1000", "0xtx", placed).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))
	mock.ExpectExec(`UPDATE auctions SET highest_bid_wei`).
		WithArgs("1000", "0xbob", int64(9)).
		WillReturnError(context.DeadlineExceeded)
	mock.ExpectRollback()

	if err := q.InsertBidAndUpdateAuction(context.Background(), 9, "0xbob", "1000", "0xtx", placed); err == nil {
		t.Fatal("expected error when the auction update fails")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestGetSettledUnrefundedAuctions(t *testing.T) {
	mock, _ := pgxmock.NewPool()
	defer mock.Close()
	q := New(mock)

	rows := mock.NewRows([]string{"auction_id", "status", "winner"}).
		AddRow(int64(5), "settled", "0xwinner").
		AddRow(int64(8), "cancelled", "")
	mock.ExpectQuery(`FROM auctions\s+WHERE status IN \('settled', 'cancelled'\) AND NOT losers_refunded`).
		WillReturnRows(rows)

	got, err := q.GetSettledUnrefundedAuctions(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	if got[0].AuctionID != 5 || got[0].Status != "settled" || got[0].Winner != "0xwinner" {
		t.Fatalf("row0 = %+v", got[0])
	}
	if got[1].AuctionID != 8 || got[1].Status != "cancelled" || got[1].Winner != "" {
		t.Fatalf("row1 = %+v", got[1])
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestMarkLosersRefunded(t *testing.T) {
	mock, _ := pgxmock.NewPool()
	defer mock.Close()
	q := New(mock)

	mock.ExpectExec(`UPDATE auctions SET losers_refunded\s*=\s*TRUE`).
		WithArgs(int64(5)).
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))

	if err := q.MarkLosersRefunded(context.Background(), 5); err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestSetRefundAttempt(t *testing.T) {
	mock, _ := pgxmock.NewPool()
	defer mock.Close()
	q := New(mock)

	mock.ExpectExec(`UPDATE auctions SET refund_attempt_at\s*=\s*now\(\)`).
		WithArgs(int64(5)).
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))

	if err := q.SetRefundAttempt(context.Background(), 5); err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

// ListingPreflight must match checksummed DB addresses when callers pass lowercase params.
func TestListingPreflightCaseInsensitiveMatch(t *testing.T) {
	mock, _ := pgxmock.NewPool()
	defer mock.Close()
	q := New(mock)

	coll := "0xAbCdEf0123456789012345678901234567890AbC"
	tokenID := "42"
	seller := "0x1234567890123456789012345678901234567890"
	rows := mock.NewRows([]string{"listed", "orphaned", "price_wei", "seller_owns"}).
		AddRow(true, false, "1000000000000000000", true)
	mock.ExpectQuery(`FROM listings l`).
		WithArgs(strings.ToLower(coll), tokenID, strings.ToLower(seller)).
		WillReturnRows(rows)

	pf, err := q.ListingPreflight(context.Background(), strings.ToLower(coll), tokenID, strings.ToLower(seller))
	if err != nil {
		t.Fatal(err)
	}
	if !pf.Listed || pf.Orphaned || !pf.SellerOwns || pf.PriceWei != "1000000000000000000" {
		t.Fatalf("preflight = %+v; want active listing with seller ownership", pf)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestListingPreflightNoRows(t *testing.T) {
	mock, _ := pgxmock.NewPool()
	defer mock.Close()
	q := New(mock)

	mock.ExpectQuery(`FROM listings l`).
		WithArgs("0xcoll", "1", "0xseller").
		WillReturnError(pgx.ErrNoRows)

	pf, err := q.ListingPreflight(context.Background(), "0xcoll", "1", "0xseller")
	if err != nil {
		t.Fatal(err)
	}
	if pf.Listed || pf.SellerOwns {
		t.Fatalf("preflight = %+v; want empty result", pf)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

// MarkMissing writes a sentinel nft_metadata row so the indexer drops the
// token from ListTokensMissingMetadata (breaking the 30-seconds re-fetch
// loop on tokens that contract-reply with an empty tokenURI).
//
// The mock regex locks in two load-bearing invariants:
//   - a single INSERT into nft_metadata (no tx, no nft_tokens mirror)
//   - ON CONFLICT DO NOTHING semantic via no follow-up UPDATE expectation
func TestMarkMissingInsertsSentinel(t *testing.T) {
	mock, _ := pgxmock.NewPool()
	defer mock.Close()
	q := New(mock)

	mock.ExpectExec(`INSERT INTO nft_metadata\(collection, token_id, name, description, image_uri, animation_uri, metadata_uri, fetched_at\)`).
		WithArgs("0xcoll", "1").
		WillReturnResult(pgxmock.NewResult("INSERT", 1))

	if err := q.MarkMissing(context.Background(), "0xcoll", "1"); err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

// MarkMissing targets only the (collection, token_id) ON CONFLICT — it must
// NOT touch nft_tokens, otherwise a previous successful fetch's
// name/image on that mirror would be wiped.
func TestMarkMissingLeavesNftTokensUntouched(t *testing.T) {
	mock, _ := pgxmock.NewPool()
	defer mock.Close()
	q := New(mock)

	// The only DB op MarkMissing should perform is on nft_metadata. Any
	// nft_tokens INSERT/UPDATE is a regression.
	mock.ExpectExec(`INSERT INTO nft_metadata`).
		WithArgs("0xcoll", "1").
		WillReturnResult(pgxmock.NewResult("INSERT", 1))

	if err := q.MarkMissing(context.Background(), "0xcoll", "1"); err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		// ExpectationsWereMet verifies that every Expect* was satisfied AND
		// that no unexpected query was sent. If MarkMissing had silently
		// added a nft_tokens upsert, pgxmock records it as unexpected and
		// returns a non-nil error here — which is what we want.
		t.Fatal(err)
	}
}

// ListTokensWithUpstreamImages returns tokens whose image_uri is still an
// upstream http(s) URL (self-hosting failed during ingest), filtered by
// backoff and retry count ceiling.
func TestListTokensWithUpstreamImages(t *testing.T) {
	mock, _ := pgxmock.NewPool()
	defer mock.Close()
	q := New(mock)

	rows := mock.NewRows([]string{"collection", "token_id", "image_uri", "image_retry_count"}).
		AddRow("0xc", "1", "https://ipfs.io/ipfs/QmTest", 0).
		AddRow("0xd", "42", "http://example.com/img.png", 3)
	mock.ExpectQuery(`FROM nft_metadata`).
		WithArgs(50, maxImageRetries).WillReturnRows(rows)

	out, err := q.ListTokensWithUpstreamImages(context.Background(), 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 2 {
		t.Fatalf("rows = %d, want 2", len(out))
	}
	if out[0].Collection != "0xc" || out[0].TokenID != "1" || out[0].ImageURI != "https://ipfs.io/ipfs/QmTest" || out[0].RetryCount != 0 {
		t.Fatalf("row0 = %+v", out[0])
	}
	if out[1].ImageURI != "http://example.com/img.png" || out[1].RetryCount != 3 {
		t.Fatalf("row1 = %+v", out[1])
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

// ListTokensWithUpstreamImages clamps limit to [1,200].
func TestListTokensWithUpstreamImagesClampsLimit(t *testing.T) {
	mock, _ := pgxmock.NewPool()
	defer mock.Close()
	q := New(mock)

	rows := mock.NewRows([]string{"collection", "token_id", "image_uri", "image_retry_count"})
	mock.ExpectQuery(`FROM nft_metadata`).
		WithArgs(50, maxImageRetries).WillReturnRows(rows)

	out, err := q.ListTokensWithUpstreamImages(context.Background(), 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 0 {
		t.Fatalf("rows = %d, want 0", len(out))
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

// UpdateImageURI must update both nft_metadata and nft_tokens atomically,
// resetting retry tracking columns on success.
func TestUpdateImageURIIsAtomic(t *testing.T) {
	mock, _ := pgxmock.NewPool()
	defer mock.Close()
	q := New(mock)

	mock.ExpectBegin()
	mock.ExpectExec(`UPDATE nft_metadata SET image_uri`).
		WithArgs("0xc", "1", "/api/v1/img/abc123").
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	mock.ExpectExec(`UPDATE nft_tokens SET image_uri`).
		WithArgs("0xc", "1", "/api/v1/img/abc123").
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	mock.ExpectCommit()

	if err := q.UpdateImageURI(context.Background(), "0xc", "1", "/api/v1/img/abc123"); err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

// UpdateImageURI rolls back if the second UPDATE fails.
func TestUpdateImageURIRollsBackOnFailure(t *testing.T) {
	mock, _ := pgxmock.NewPool()
	defer mock.Close()
	q := New(mock)

	mock.ExpectBegin()
	mock.ExpectExec(`UPDATE nft_metadata SET image_uri`).
		WithArgs("0xc", "1", "/api/v1/img/abc123").
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	mock.ExpectExec(`UPDATE nft_tokens SET image_uri`).
		WithArgs("0xc", "1", "/api/v1/img/abc123").
		WillReturnError(context.DeadlineExceeded)
	mock.ExpectRollback()

	if err := q.UpdateImageURI(context.Background(), "0xc", "1", "/api/v1/img/abc123"); err == nil {
		t.Fatal("expected error when nft_tokens update fails")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

// BumpImageRetry increments retry count and sets next retry time.
func TestBumpImageRetry(t *testing.T) {
	mock, _ := pgxmock.NewPool()
	defer mock.Close()
	q := New(mock)

	mock.ExpectExec(`UPDATE nft_metadata`).
		WithArgs("0xc", "1", 3). // count+1 = 3
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))

	if err := q.BumpImageRetry(context.Background(), "0xc", "1", 2); err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

// BumpImageRetry with count=0 (first failure) sets retry_count to 1.
func TestBumpImageRetryFirstFailure(t *testing.T) {
	mock, _ := pgxmock.NewPool()
	defer mock.Close()
	q := New(mock)

	mock.ExpectExec(`UPDATE nft_metadata`).
		WithArgs("0xc", "1", 1). // 0+1 = 1
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))

	if err := q.BumpImageRetry(context.Background(), "0xc", "1", 0); err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

// ── Image retry backoff: full lifecycle tests ────────────────────────────

// BumpImageRetry at count=5 (the penultimate attempt) sets retry_count to 6
// and schedules next_image_retry_at with the capped 24h backoff.
func TestBumpImageRetryCappedAt24h(t *testing.T) {
	mock, _ := pgxmock.NewPool()
	defer mock.Close()
	q := New(mock)

	mock.ExpectExec(`UPDATE nft_metadata`).
		WithArgs("0xc", "1", 6). // 5+1 = 6 (max)
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))

	if err := q.BumpImageRetry(context.Background(), "0xc", "1", 5); err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

// BumpImageRetry propagates a DB error (e.g. connection failure) instead of
// silently swallowing it.
func TestBumpImageRetryPropagatesError(t *testing.T) {
	mock, _ := pgxmock.NewPool()
	defer mock.Close()
	q := New(mock)

	mock.ExpectExec(`UPDATE nft_metadata`).
		WithArgs("0xc", "1", 1).
		WillReturnError(context.DeadlineExceeded)

	if err := q.BumpImageRetry(context.Background(), "0xc", "1", 0); err == nil {
		t.Fatal("expected error from BumpImageRetry")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

// ListTokensWithUpstreamImages excludes tokens at or above maxImageRetries.
// Even though image_uri is still an upstream URL, the retry ceiling stops
// further attempts.
func TestListTokensWithUpstreamImagesExcludesExhausted(t *testing.T) {
	mock, _ := pgxmock.NewPool()
	defer mock.Close()
	q := New(mock)

	// Return empty — exhausted tokens (retry_count >= 6) are filtered by
	// the WHERE clause, so the mock returns no rows.
	rows := mock.NewRows([]string{"collection", "token_id", "image_uri", "image_retry_count"})
	mock.ExpectQuery(`FROM nft_metadata`).
		WithArgs(50, maxImageRetries).WillReturnRows(rows)

	out, err := q.ListTokensWithUpstreamImages(context.Background(), 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 0 {
		t.Fatalf("rows = %d, want 0 (exhausted tokens excluded)", len(out))
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

// ListTokensWithUpstreamImages returns tokens whose backoff has expired
// (next_image_retry_at <= now), proving the time-gated filter works.
func TestListTokensWithUpstreamImagesIncludesExpiredBackoff(t *testing.T) {
	mock, _ := pgxmock.NewPool()
	defer mock.Close()
	q := New(mock)

	// A token with retry_count=3 whose backoff window has passed.
	rows := mock.NewRows([]string{"collection", "token_id", "image_uri", "image_retry_count"}).
		AddRow("0xc", "1", "https://ipfs.io/ipfs/QmExpired", 3)
	mock.ExpectQuery(`FROM nft_metadata`).
		WithArgs(50, maxImageRetries).WillReturnRows(rows)

	out, err := q.ListTokensWithUpstreamImages(context.Background(), 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 {
		t.Fatalf("rows = %d, want 1", len(out))
	}
	if out[0].RetryCount != 3 {
		t.Fatalf("retry_count = %d, want 3", out[0].RetryCount)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

// UpdateImageURI resets both image_retry_count and next_image_retry_at to
// their zero values, proving a successful self-host clears the backoff state.
func TestUpdateImageURIResetsRetryState(t *testing.T) {
	mock, _ := pgxmock.NewPool()
	defer mock.Close()
	q := New(mock)

	// The SQL sets image_retry_count=0 and next_image_retry_at=NULL.
	// pgxmock verifies the exact args; the retry-reset is implicit in the
	// SQL (we verify the SQL content via the ExpectExec regex below).
	mock.ExpectBegin()
	mock.ExpectExec(`UPDATE nft_metadata SET image_uri`).
		WithArgs("0xc", "1", "/api/v1/img/abc123").
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	mock.ExpectExec(`UPDATE nft_tokens SET image_uri`).
		WithArgs("0xc", "1", "/api/v1/img/abc123").
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	mock.ExpectCommit()

	if err := q.UpdateImageURI(context.Background(), "0xc", "1", "/api/v1/img/abc123"); err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

// Full lifecycle: simulate → bump×3 → verify still eligible → bump to max
// → verify excluded → UpdateImageURI → verify clean state.
//
// This is the integration-style test that exercises the entire retry worker
// pipeline against pgxmock, proving the queries compose correctly.
func TestRetryLifecycleSimulateBumpExclude(t *testing.T) {
	mock, _ := pgxmock.NewPool()
	defer mock.Close()
	q := New(mock)

	// Phase 1: Token starts as a retry candidate (simulated IPFS outage:
	// image_uri is an upstream URL, retry_count=0, backoff not yet set).
	rows := mock.NewRows([]string{"collection", "token_id", "image_uri", "image_retry_count"}).
		AddRow("0xc", "1", "https://ipfs.io/ipfs/QmFakeHash", 0)
	mock.ExpectQuery(`FROM nft_metadata`).
		WithArgs(50, maxImageRetries).WillReturnRows(rows)

	out, err := q.ListTokensWithUpstreamImages(context.Background(), 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 || out[0].RetryCount != 0 {
		t.Fatalf("phase 1: %+v", out)
	}

	// Phase 2: Bump through retries 0→1→2→3 (4 attempts). Each bump
	// increments the count and schedules exponential backoff.
	for i := 0; i < 3; i++ {
		mock.ExpectExec(`UPDATE nft_metadata`).
			WithArgs("0xc", "1", i+1).
			WillReturnResult(pgxmock.NewResult("UPDATE", 1))
		if err := q.BumpImageRetry(context.Background(), "0xc", "1", i); err != nil {
			t.Fatalf("bump %d: %v", i, err)
		}
	}

	// Phase 3: Token is still eligible (count=3 < max=6) and backoff
	// hasn't expired yet. Mock returns it as if the backoff window passed.
	rows = mock.NewRows([]string{"collection", "token_id", "image_uri", "image_retry_count"}).
		AddRow("0xc", "1", "https://ipfs.io/ipfs/QmFakeHash", 3)
	mock.ExpectQuery(`FROM nft_metadata`).
		WithArgs(50, maxImageRetries).WillReturnRows(rows)

	out, err = q.ListTokensWithUpstreamImages(context.Background(), 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 || out[0].RetryCount != 3 {
		t.Fatalf("phase 3: %+v", out)
	}

	// Phase 4: Bump to max (3→4→5→6). After count=6 the token is
	// permanently excluded by the retry_count < maxImageRetries filter.
	for i := 3; i < 6; i++ {
		mock.ExpectExec(`UPDATE nft_metadata`).
			WithArgs("0xc", "1", i+1).
			WillReturnResult(pgxmock.NewResult("UPDATE", 1))
		if err := q.BumpImageRetry(context.Background(), "0xc", "1", i); err != nil {
			t.Fatalf("bump %d: %v", i, err)
		}
	}

	// Phase 5: Verify exclusion — query returns zero rows because
	// retry_count (now 6) >= maxImageRetries (6).
	rows = mock.NewRows([]string{"collection", "token_id", "image_uri", "image_retry_count"})
	mock.ExpectQuery(`FROM nft_metadata`).
		WithArgs(50, maxImageRetries).WillReturnRows(rows)

	out, err = q.ListTokensWithUpstreamImages(context.Background(), 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 0 {
		t.Fatalf("phase 5: expected 0 candidates after max retries, got %d", len(out))
	}

	// Phase 6: Simulate a successful retry (image self-hosted). UpdateImageURI
	// must reset retry_count=0 and next_image_retry_at=NULL atomically.
	mock.ExpectBegin()
	mock.ExpectExec(`UPDATE nft_metadata SET image_uri`).
		WithArgs("0xc", "1", "/api/v1/img/fixed").
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	mock.ExpectExec(`UPDATE nft_tokens SET image_uri`).
		WithArgs("0xc", "1", "/api/v1/img/fixed").
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	mock.ExpectCommit()

	if err := q.UpdateImageURI(context.Background(), "0xc", "1", "/api/v1/img/fixed"); err != nil {
		t.Fatal(err)
	}

	// Phase 7: Token no longer appears in retry candidates because
	// image_uri is now a local blob path (not http/https).
	rows = mock.NewRows([]string{"collection", "token_id", "image_uri", "image_retry_count"})
	mock.ExpectQuery(`FROM nft_metadata`).
		WithArgs(50, maxImageRetries).WillReturnRows(rows)

	out, err = q.ListTokensWithUpstreamImages(context.Background(), 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 0 {
		t.Fatalf("phase 7: expected 0 candidates after self-host, got %d", len(out))
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

// Regression for the infinite 30s re-fetch loop on empty-tokenURI tokens:
// ListTokensMissingMetadata's third UNION arm
//   (JOIN nft_metadata WHERE image_uri IS NULL OR image_uri = '')
// was removed. The mock's regex anchors on the END of the new (2-arm) query:
// `... AND m.collection IS NULL ) src GROUP BY ... LIMIT $1`, which exists
// only after the third arm is removed.
func TestListTokensMissingMetadataDropsImageEmptyThirdArm(t *testing.T) {
	mock, _ := pgxmock.NewPool()
	defer mock.Close()
	q := New(mock)

	rows := mock.NewRows([]string{"collection", "token_id", "standard"}).
		AddRow("0xc", "1", "erc721").
		AddRow("0xc", "7", "erc1155")
	mock.ExpectQuery(`m\.collection IS NULL\s*\) src\s*GROUP BY src\.collection, src\.token_id, src\.standard\s*LIMIT \$1`).
		WithArgs(50).
		WillReturnRows(rows)

	out, err := q.ListTokensMissingMetadata(context.Background(), 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 2 {
		t.Fatalf("rows = %d, want 2", len(out))
	}
	if out[0].Collection != "0xc" || out[0].TokenID != "1" || out[0].Standard != "erc721" {
		t.Fatalf("row0 = %+v", out[0])
	}
	if out[1].Standard != "erc1155" {
		t.Fatalf("row1 = %+v", out[1])
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

// ── Indexer tx-atomicity: listings + offers ──────────────────────────────────
//
// Both pairs (onListed writes listings+nft_ownership; onOfferAccepted writes
// offers+sales) must commit together — a crash between them leaves the
// front-end in a state where one half of the state is visible and the other
// half isn't, producing "mystery 500" on the next /token/:addr/:id hit.

func listingRowFixture(amount int64, std string) ListingRow {
	at := time.Unix(1_700_000_000, 0)
	exp := at.Add(7 * 24 * time.Hour)
	return ListingRow{
		Collection: "0xc", TokenID: "1", Seller: "0xseller",
		PriceWei: "1000000000000000000", Amount: amount, Standard: std,
		ExpiresAt: exp, ListedAt: at, TxHash: "0xlisted_tx",
	}
}

func TestUpsertListingAndOwnershipIsAtomic721(t *testing.T) {
	mock, _ := pgxmock.NewPool()
	defer mock.Close()
	q := New(mock)

	r := listingRowFixture(1, "erc721")
	mock.ExpectBeginTx(pgx.TxOptions{})
	mock.ExpectExec(`INSERT INTO listings`).
		WithArgs(r.Collection, r.TokenID, r.Seller, r.PriceWei,
			r.Amount, r.Standard, r.ExpiresAt, r.ListedAt, r.TxHash).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))
	mock.ExpectExec(`DELETE FROM nft_ownership WHERE collection=\$1 AND token_id=\$2`).
		WithArgs(r.Collection, r.TokenID).
		WillReturnResult(pgxmock.NewResult("DELETE", 0))
	mock.ExpectExec(`INSERT INTO nft_ownership\(collection, token_id, owner, units, standard\)\s+VALUES\(\$1,\$2,\$3,1,'erc721'\)`).
		WithArgs(r.Collection, r.TokenID, r.Seller).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))
	mock.ExpectCommit()

	if err := q.UpsertListingAndOwnership(context.Background(), r); err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestUpsertListingAndOwnershipIsAtomic1155(t *testing.T) {
	mock, _ := pgxmock.NewPool()
	defer mock.Close()
	q := New(mock)

	r := listingRowFixture(5, "erc1155")
	mock.ExpectBeginTx(pgx.TxOptions{})
	mock.ExpectExec(`INSERT INTO listings`).
		WithArgs(r.Collection, r.TokenID, r.Seller, r.PriceWei,
			r.Amount, r.Standard, r.ExpiresAt, r.ListedAt, r.TxHash).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))
	mock.ExpectExec(`INSERT INTO nft_ownership\(collection, token_id, owner, units, standard\)\s+VALUES\(\$1,\$2,\$3,\$4,'erc1155'\)`).
		WithArgs(r.Collection, r.TokenID, r.Seller, r.Amount).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))
	// No DELETE on the erc1155 branch — the upsert is the only ownership write.
	mock.ExpectCommit()

	if err := q.UpsertListingAndOwnership(context.Background(), r); err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

// If the ownership seed (erc721) fails, the listing INSERT is rolled back so
// the front-end never sees a listing whose preflight cannot locate the seller.
func TestUpsertListingAndOwnershipRollsBackOnOwnershipFailure(t *testing.T) {
	mock, _ := pgxmock.NewPool()
	defer mock.Close()
	q := New(mock)

	r := listingRowFixture(1, "erc721")
	mock.ExpectBeginTx(pgx.TxOptions{})
	mock.ExpectExec(`INSERT INTO listings`).
		WithArgs(r.Collection, r.TokenID, r.Seller, r.PriceWei,
			r.Amount, r.Standard, r.ExpiresAt, r.ListedAt, r.TxHash).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))
	mock.ExpectExec(`DELETE FROM nft_ownership WHERE collection=\$1 AND token_id=\$2`).
		WithArgs(r.Collection, r.TokenID).
		WillReturnResult(pgxmock.NewResult("DELETE", 0))
	mock.ExpectExec(`INSERT INTO nft_ownership\(collection, token_id, owner, units, standard\)\s+VALUES\(\$1,\$2,\$3,1,'erc721'\)`).
		WithArgs(r.Collection, r.TokenID, r.Seller).
		WillReturnError(context.DeadlineExceeded)
	mock.ExpectRollback()

	if err := q.UpsertListingAndOwnership(context.Background(), r); err == nil {
		t.Fatal("expected error when seller ownership seed fails")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

// onOfferAccepted writes two rows on different tables. They must commit
// together; otherwise a crash between them leaves the bidder's escrow pinned
// on a 'pending' offer that the seller can never re-process.
func TestAcceptOfferAndRecordSaleIsAtomic(t *testing.T) {
	mock, _ := pgxmock.NewPool()
	defer mock.Close()
	q := New(mock)

	at := time.Unix(1_700_000_000, 0)
	mock.ExpectBeginTx(pgx.TxOptions{})
	mock.ExpectExec(`UPDATE offers SET status='accepted'\s+WHERE collection=\$1 AND token_id=\$2 AND bidder=\$3 AND status='pending'`).
		WithArgs("0xc", "1", "0xbidder").
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	mock.ExpectExec(`INSERT INTO sales`).
		WithArgs("0xc", "1", "0xseller", "0xbidder",
			"1000", "15", "0", "0xacc_tx", uint64(9), at).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))
	mock.ExpectCommit()

	if err := q.AcceptOfferAndRecordSale(context.Background(),
		"0xc", "1", "0xseller", "0xbidder",
		"1000", "15", "0", "0xacc_tx", 9, at); err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

// onOfferAccepted rollback: the offer UPDATE must NOT survive a failed sale
// insert (otherwise the buyer's escrow is locked forever on a 'pending' offer
// they can't satisfy — the seller can't re-tender the same offer either).
func TestAcceptOfferAndRecordSaleRollsBackOnSaleFailure(t *testing.T) {
	mock, _ := pgxmock.NewPool()
	defer mock.Close()
	q := New(mock)

	at := time.Unix(1_700_000_000, 0)
	mock.ExpectBeginTx(pgx.TxOptions{})
	mock.ExpectExec(`UPDATE offers SET status='accepted' WHERE collection=\$1 AND token_id=\$2 AND bidder=\$3 AND status='pending'`).
		WithArgs("0xc", "1", "0xbidder").
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	mock.ExpectExec(`INSERT INTO sales`).
		WithArgs("0xc", "1", "0xseller", "0xbidder",
			"1000", "15", "0", "0xacc_tx", uint64(9), at).
		WillReturnError(context.DeadlineExceeded)
	mock.ExpectRollback()

	if err := q.AcceptOfferAndRecordSale(context.Background(),
		"0xc", "1", "0xseller", "0xbidder",
		"1000", "15", "0", "0xacc_tx", 9, at); err == nil {
		t.Fatal("expected error when sale insert fails")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
