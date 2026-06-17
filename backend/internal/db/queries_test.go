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
