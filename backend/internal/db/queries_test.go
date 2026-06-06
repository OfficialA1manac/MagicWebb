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
