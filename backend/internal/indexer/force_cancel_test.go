package indexer

import (
	"bytes"
	"context"
	"errors"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/pashagolub/pgxmock/v4"

	"github.com/OfficialA1manac/MagicWebb/backend/internal/db"
	"github.com/OfficialA1manac/MagicWebb/backend/internal/sse"
)

// v3.4 AuctionForceCancelled(uint256 indexed id) and
// OfferEligibilitySet(address indexed coll, bool indexed eligible) must be in
// the core getLogs filter, or the chain would emit them into the void: a
// force-cancelled auction would sit 'active' forever and the keeper would
// keep re-settling it.
func TestCoreTopicsIncludesV34Events(t *testing.T) {
	topics := coreTopics()[0]
	has := func(h common.Hash) bool {
		for _, x := range topics {
			if x == h {
				return true
			}
		}
		return false
	}
	if !has(TopicAuctionForceCancelled) {
		t.Fatal("AuctionForceCancelled missing from coreTopics filter")
	}
	if !has(TopicOfferEligibilitySet) {
		t.Fatal("OfferEligibilitySet missing from coreTopics filter")
	}
	// Selectors are keccak of the canonical signature — a typo silently drops
	// the event, so pin the exact bytes.
	if TopicAuctionForceCancelled != crypto.Keccak256Hash([]byte("AuctionForceCancelled(uint256)")) {
		t.Fatal("TopicAuctionForceCancelled signature drift")
	}
	if TopicOfferEligibilitySet != crypto.Keccak256Hash([]byte("OfferEligibilitySet(address,bool)")) {
		t.Fatal("TopicOfferEligibilitySet signature drift")
	}
}

func TestOnAuctionForceCancelledMarksCancelled(t *testing.T) {
	mock, _ := pgxmock.NewPool()
	defer mock.Close()
	h := &handlers{q: db.New(mock), bcast: sse.New()}

	mock.ExpectExec(`UPDATE auctions SET status=\$1 WHERE auction_id=\$2`).
		WithArgs("cancelled", int64(42)).
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))

	l := types.Log{Topics: []common.Hash{
		TopicAuctionForceCancelled,
		common.BigToHash(big.NewInt(42)),
	}}
	if err := h.dispatch(context.Background(), l, 0); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestOnAuctionForceCancelledShortLogIsMalformed(t *testing.T) {
	h := &handlers{}
	err := h.onAuctionForceCancelled(context.Background(),
		types.Log{Topics: []common.Hash{TopicAuctionForceCancelled}})
	if !errors.Is(err, errMalformedLog) {
		t.Fatalf("short log: error %v does not wrap errMalformedLog", err)
	}
}

func TestOnOfferEligibilitySet(t *testing.T) {
	mock, _ := pgxmock.NewPool()
	defer mock.Close()
	h := &handlers{q: db.New(mock), bcast: sse.New()}

	coll := common.HexToAddress("0xAbCdEfabcdefabcdefabcdefabcdefabcdefabcd")
	// Both params indexed: topics[1]=coll (left-padded), topics[2]=bool word.
	mkLog := func(eligible bool) types.Log {
		flag := common.Hash{}
		if eligible {
			flag = common.BigToHash(big.NewInt(1))
		}
		return types.Log{Topics: []common.Hash{
			TopicOfferEligibilitySet,
			common.BytesToHash(coll.Bytes()),
			flag,
		}}
	}
	// Address must land lowercased (039_lowercase_addresses convention).
	mock.ExpectExec(`INSERT INTO collections\(address, name, symbol, standard, deploy_block, offer_eligible\)`).
		WithArgs("0xabcdefabcdefabcdefabcdefabcdefabcdefabcd", true).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))
	mock.ExpectExec(`INSERT INTO collections\(address, name, symbol, standard, deploy_block, offer_eligible\)`).
		WithArgs("0xabcdefabcdefabcdefabcdefabcdefabcdefabcd", false).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))

	if err := h.dispatch(context.Background(), mkLog(true), 0); err != nil {
		t.Fatalf("enable: %v", err)
	}
	if err := h.dispatch(context.Background(), mkLog(false), 0); err != nil {
		t.Fatalf("disable: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}

	err := h.onOfferEligibilitySet(context.Background(),
		types.Log{Topics: []common.Hash{TopicOfferEligibilitySet, {}}})
	if !errors.Is(err, errMalformedLog) {
		t.Fatalf("short log: error %v does not wrap errMalformedLog", err)
	}
}

// forceCancel(uint256) must encode as selector ‖ id (one 32-byte word) — the
// same shape as settle(uint256), which the keeper already sends.
func TestEncodeForceCancel(t *testing.T) {
	got := encodeForceCancel(99)
	sel := crypto.Keccak256([]byte("forceCancel(uint256)"))[:4]
	if !bytes.HasPrefix(got, sel) {
		t.Fatalf("selector mismatch: %x", got[:4])
	}
	if len(got) != 4+32 {
		t.Fatalf("len = %d, want 36", len(got))
	}
	if got[4+31] != 99 || !bytes.Equal(got[4:4+31], make([]byte, 31)) {
		t.Fatalf("id word = %x", got[4:])
	}
}

// The keeper's force-cancel sweep must query with the 72h window and encode
// a forceCancel call for each row. sendRaw needs a live eth client, so the
// tx path is exercised only up to the query here: an empty result set means
// no tx is attempted (and no eth call — r.eth is nil).
func TestForceCancelStuckAuctionsQueriesWindow(t *testing.T) {
	mock, _ := pgxmock.NewPool()
	defer mock.Close()
	r := &Runner{q: db.New(mock)}

	mock.ExpectQuery(`WHERE a\.status='active' AND a\.ends_at \+ \$1::interval <= now\(\)`).
		WithArgs(db.ForceCancelWindow).
		WillReturnRows(mock.NewRows([]string{"auction_id"}))

	r.forceCancelStuckAuctions(context.Background(), nil, common.Address{},
		common.HexToAddress("0x1111111111111111111111111111111111111111"), nil, big.NewInt(114))
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

// Read-only networks (no AuctionHouse) must skip the sweep entirely — no DB
// query, no tx.
func TestForceCancelStuckAuctionsSkipsWithoutContract(t *testing.T) {
	mock, _ := pgxmock.NewPool()
	defer mock.Close()
	r := &Runner{q: db.New(mock)}
	r.forceCancelStuckAuctions(context.Background(), nil, common.Address{}, common.Address{}, nil, big.NewInt(114))
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
