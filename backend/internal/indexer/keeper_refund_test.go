package indexer

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/pashagolub/pgxmock/v4"

	"github.com/OfficialA1manac/MagicWebb/backend/internal/db"
)

// refundLosers(uint256,address[]) must ABI-encode as: selector ‖ id ‖ offset(0x40)
// ‖ len ‖ addr0 ‖ addr1 …  A wrong layout silently refunds nobody on-chain.
func TestEncodeRefundLosers(t *testing.T) {
	a1 := common.HexToAddress("0x1111111111111111111111111111111111111111")
	a2 := common.HexToAddress("0x2222222222222222222222222222222222222222")

	got := encodeRefundLosers(7, []common.Address{a1, a2})

	sel := crypto.Keccak256([]byte("refundLosers(uint256,address[])"))[:4]
	if !bytes.HasPrefix(got, sel) {
		t.Fatalf("selector mismatch: %x", got[:4])
	}
	// 4 selector + 5 words (id, offset, len, a1, a2)
	wantLen := 4 + 5*32
	if len(got) != wantLen {
		t.Fatalf("len = %d, want %d", len(got), wantLen)
	}
	word := func(i int) []byte { return got[4+i*32 : 4+(i+1)*32] }
	if word(0)[31] != 7 {
		t.Fatalf("id word = %x", word(0))
	}
	if word(1)[31] != 0x40 {
		t.Fatalf("offset word = %x, want 0x40", word(1))
	}
	if word(2)[31] != 2 {
		t.Fatalf("array len = %x, want 2", word(2))
	}
	if common.BytesToAddress(word(3)) != a1 {
		t.Fatalf("addr0 = %x", word(3))
	}
	if common.BytesToAddress(word(4)) != a2 {
		t.Fatalf("addr1 = %x", word(4))
	}
}

func TestEncodeRefundLosersEmpty(t *testing.T) {
	got := encodeRefundLosers(1, nil)
	// selector + id + offset + len(0)
	if len(got) != 4+3*32 {
		t.Fatalf("len = %d, want %d", len(got), 4+3*32)
	}
}


func TestCollectLosersExcludesWinnerOnSettle(t *testing.T) {
	mock, _ := pgxmock.NewPool()
	defer mock.Close()
	r := &Runner{q: db.New(mock)}

	now := time.Unix(1_700_000_000, 0)
	rows := mock.NewRows([]string{"bidder", "effective_wei", "bid_count", "last_bid_at"}).
		AddRow("0xWINNER", "5000", int64(2), now).
		AddRow("0xloser1", "3000", int64(1), now).
		AddRow("0xloser2", "1000", int64(1), now)
	mock.ExpectQuery(`FROM effective_bids`).WithArgs(int64(5)).WillReturnRows(rows)

	got, err := r.collectLosers(context.Background(),
		db.RefundableAuction{AuctionID: 5, Status: "settled", Winner: "0xwinner"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("losers = %d, want 2 (winner excluded)", len(got))
	}
	if got[0] != common.HexToAddress("0xloser1") || got[1] != common.HexToAddress("0xloser2") {
		t.Fatalf("losers = %v", got)
	}
}

func TestCollectLosersIncludesAllOnCancel(t *testing.T) {
	mock, _ := pgxmock.NewPool()
	defer mock.Close()
	r := &Runner{q: db.New(mock)}

	now := time.Unix(1_700_000_000, 0)
	rows := mock.NewRows([]string{"bidder", "effective_wei", "bid_count", "last_bid_at"}).
		AddRow("0xa", "3000", int64(1), now).
		AddRow("0xb", "1000", int64(1), now)
	mock.ExpectQuery(`FROM effective_bids`).WithArgs(int64(8)).WillReturnRows(rows)

	// Cancelled auction: no winner, every bidder is refundable.
	got, err := r.collectLosers(context.Background(),
		db.RefundableAuction{AuctionID: 8, Status: "cancelled", Winner: ""})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("losers = %d, want 2 (all bidders)", len(got))
	}
}
