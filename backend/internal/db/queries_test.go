package db

import (
	"context"
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
