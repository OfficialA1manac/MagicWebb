package indexer

import (
	"context"
	"errors"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/pashagolub/pgxmock/v4"

	"github.com/OfficialA1manac/MagicWebb/backend/internal/config"
	"github.com/OfficialA1manac/MagicWebb/backend/internal/db"
	"github.com/OfficialA1manac/MagicWebb/backend/internal/sse"
)

// Every governance selector must sit in the core filter, or the chain would
// emit the admin/keeper/upgrade trail into the void — the exact gap this wave
// closes. Pin the canonical signatures too: a typo silently drops the event.
func TestCoreTopicsIncludeGovernance(t *testing.T) {
	topics := coreTopics()[0]
	has := func(h common.Hash) bool {
		for _, x := range topics {
			if x == h {
				return true
			}
		}
		return false
	}
	want := map[string]common.Hash{
		"KeeperSet(address,address)":                TopicKeeperSet,
		"AdminRenounced(address)":                   TopicAdminRenounced,
		"AdminTransferStarted(address,address)":     TopicAdminTransferStarted,
		"AdminTransferCancelled(address)":           TopicAdminTransferCancelled,
		"AdminTransferred(address,address)":         TopicAdminTransferred,
		"AuditLog(bytes32,address,address,bytes32)": TopicAuditLog,
		"UpgradeQueued(address,uint64)":             TopicUpgradeQueued,
		"UpgradeCancelled(address)":                 TopicUpgradeCancelled,
		"Upgraded(address)":                         TopicUpgraded,
	}
	for sig, topic := range want {
		if topic != crypto.Keccak256Hash([]byte(sig)) {
			t.Fatalf("%s: signature drift", sig)
		}
		if !has(topic) {
			t.Fatalf("%s missing from coreTopics filter", sig)
		}
	}
	if len(governanceTopics()[0]) != len(want) {
		t.Fatalf("governanceTopics has %d entries, want %d", len(governanceTopics()[0]), len(want))
	}
}

func addrTopic(hex string) common.Hash {
	return common.BytesToHash(common.HexToAddress(hex).Bytes())
}

func TestDecodeGovernance(t *testing.T) {
	prev := "0x00000000000000000000000000000000000000aa"
	next := "0x00000000000000000000000000000000000000bb"
	mgr := common.HexToAddress("0x14C3b1Bae9d9224E5456FA78e527ADEA48e3CDA3")
	tx := common.HexToHash("0x01")

	cases := []struct {
		name         string
		log          types.Log
		event, actor string
		subject      string
		extra        string
	}{
		{"KeeperSet", types.Log{Address: mgr, TxHash: tx, Topics: []common.Hash{TopicKeeperSet, addrTopic(prev), addrTopic(next)}}, "KeeperSet", prev, next, ""},
		{"AdminTransferStarted", types.Log{Address: mgr, TxHash: tx, Topics: []common.Hash{TopicAdminTransferStarted, addrTopic(prev), addrTopic(next)}}, "AdminTransferStarted", prev, next, ""},
		{"AdminTransferCancelled", types.Log{Address: mgr, TxHash: tx, Topics: []common.Hash{TopicAdminTransferCancelled, addrTopic(next)}}, "AdminTransferCancelled", "", next, ""},
		{"AdminTransferred", types.Log{Address: mgr, TxHash: tx, Topics: []common.Hash{TopicAdminTransferred, addrTopic(prev), addrTopic(next)}}, "AdminTransferred", prev, next, ""},
		{"AdminRenounced", types.Log{Address: mgr, TxHash: tx, Topics: []common.Hash{TopicAdminRenounced, addrTopic(prev)}}, "AdminRenounced", prev, "", ""},
		{"UpgradeQueued", types.Log{Address: mgr, TxHash: tx, Topics: []common.Hash{TopicUpgradeQueued, addrTopic(next)}, Data: common.BigToHash(big.NewInt(1700000000)).Bytes()}, "UpgradeQueued", "", next, "eta=1700000000"},
		{"UpgradeCancelled", types.Log{Address: mgr, TxHash: tx, Topics: []common.Hash{TopicUpgradeCancelled, addrTopic(next)}}, "UpgradeCancelled", "", next, ""},
		{"Upgraded", types.Log{Address: mgr, TxHash: tx, Topics: []common.Hash{TopicUpgraded, addrTopic(next)}}, "Upgraded", "", next, ""},
	}
	for _, c := range cases {
		ev, err := decodeGovernance(c.log, 1_700_000_000)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if ev == nil {
			t.Fatalf("%s: nil event", c.name)
		}
		if ev.Event != c.event || ev.Actor != c.actor || ev.Subject != c.subject || ev.Extra != c.extra {
			t.Fatalf("%s: got %+v", c.name, ev)
		}
		// Storage convention: lowercase (039).
		if ev.Contract != "0x14c3b1bae9d9224e5456fa78e527adea48e3cda3" {
			t.Fatalf("%s: contract not lowercased: %s", c.name, ev.Contract)
		}
	}
}

func TestDecodeGovernanceAuditLogLabel(t *testing.T) {
	var action common.Hash
	copy(action[:], []byte("SET_KEEPER"))
	l := types.Log{Topics: []common.Hash{TopicAuditLog, action, addrTopic("0x00000000000000000000000000000000000000aa"), addrTopic("0x00000000000000000000000000000000000000bb")}}
	ev, err := decodeGovernance(l, 0)
	if err != nil || ev == nil {
		t.Fatalf("decode: %v %v", ev, err)
	}
	if ev.Event != "AuditLog" || ev.Extra != "SET_KEEPER" {
		t.Fatalf("got %+v", ev)
	}
}

func TestDecodeGovernanceShortLogIsMalformed(t *testing.T) {
	for _, topic := range governanceTopics()[0] {
		_, err := decodeGovernance(types.Log{Topics: []common.Hash{topic}}, 0)
		if !errors.Is(err, errMalformedLog) {
			t.Fatalf("topic %s: short log error %v does not wrap errMalformedLog", topic.Hex(), err)
		}
	}
}

func TestDecodeGovernanceIgnoresTradingTopics(t *testing.T) {
	ev, err := decodeGovernance(types.Log{Topics: []common.Hash{TopicListed}}, 0)
	if err != nil || ev != nil {
		t.Fatalf("trading topic must decode to nil,nil; got %v %v", ev, err)
	}
}

// A fresh KeeperSet is persisted, published, and fanned out to every opted-in
// wallet as a 'system' notification pointing at /status.
func TestOnGovernanceInsertsAndNotifies(t *testing.T) {
	mock, _ := pgxmock.NewPool()
	defer mock.Close()
	h := &handlers{q: db.New(mock), bcast: sse.New(), chainID: 114}

	mock.ExpectExec(`INSERT INTO governance_events`).
		WithArgs(int64(114), int64(500), "0x0000000000000000000000000000000000000000000000000000000000000001", int32(3),
			"0x14c3b1bae9d9224e5456fa78e527adea48e3cda3", "KeeperSet",
			"0x00000000000000000000000000000000000000aa", "0x00000000000000000000000000000000000000bb", "", pgxmock.AnyArg()).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))
	mock.ExpectQuery(`SELECT u.address FROM users u`).
		WithArgs(2000).
		WillReturnRows(pgxmock.NewRows([]string{"address"}).AddRow("0x00000000000000000000000000000000000000cc"))
	mock.ExpectExec(`INSERT INTO notifications`).
		WithArgs("0x00000000000000000000000000000000000000cc", "system", "Keeper replaced", pgxmock.AnyArg(), "/status").
		WillReturnResult(pgxmock.NewResult("INSERT", 1))

	l := types.Log{
		Address: common.HexToAddress("0x14C3b1Bae9d9224E5456FA78e527ADEA48e3CDA3"),
		TxHash:  common.HexToHash("0x01"), BlockNumber: 500, Index: 3,
		Topics: []common.Hash{TopicKeeperSet, addrTopic("0x00000000000000000000000000000000000000aa"), addrTopic("0x00000000000000000000000000000000000000bb")},
	}
	before := countOf("KeeperSet")
	if err := h.dispatch(context.Background(), l, 1_700_000_000); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
	if countOf("KeeperSet") != before+1 {
		t.Fatal("governance counter did not increment")
	}
}

// A replayed row (ON CONFLICT DO NOTHING → 0 rows) must not notify again.
func TestOnGovernanceDuplicateIsSilent(t *testing.T) {
	mock, _ := pgxmock.NewPool()
	defer mock.Close()
	h := &handlers{q: db.New(mock), bcast: sse.New(), chainID: 114}
	mock.ExpectExec(`INSERT INTO governance_events`).
		WithArgs(pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(), "Upgraded",
			pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg()).
		WillReturnResult(pgxmock.NewResult("INSERT", 0))
	l := types.Log{Topics: []common.Hash{TopicUpgraded, addrTopic("0x00000000000000000000000000000000000000bb")}}
	if err := h.dispatch(context.Background(), l, 1); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err) // no recipient query, no notification insert
	}
}

// Cancels are recorded but never alert anyone.
func TestOnGovernanceCancelDoesNotNotify(t *testing.T) {
	mock, _ := pgxmock.NewPool()
	defer mock.Close()
	h := &handlers{q: db.New(mock), bcast: sse.New(), chainID: 114}
	mock.ExpectExec(`INSERT INTO governance_events`).
		WithArgs(pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(), "UpgradeCancelled",
			pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg()).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))
	l := types.Log{Topics: []common.Hash{TopicUpgradeCancelled, addrTopic("0x00000000000000000000000000000000000000bb")}}
	if err := h.dispatch(context.Background(), l, 1); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

// Read-only networks configure no manager: the watcher must not add the zero
// address to the getLogs filter.
func TestWatchedContractsSkipsEmptyManager(t *testing.T) {
	cfg := &config.Config{
		MarketplaceAddr: "0xa6fbad082Bc73515A48BFc933014757461Cf3f8e",
		AuctionAddr:     "0x0ADe5F5A4d4C836AF5a5625B678846f66bE3Dd7D",
		OfferBookAddr:   "0x358d4504f4d1fA76e26d142d59f0369c5FD81F9f",
	}
	r := &Runner{cfg: cfg}
	if n := len(r.watchedContracts()); n != 3 {
		t.Fatalf("empty manager: %d addresses, want 3", n)
	}
	cfg.MarketplaceManagerAddr = "0x14C3b1Bae9d9224E5456FA78e527ADEA48e3CDA3"
	if n := len(r.watchedContracts()); n != 4 {
		t.Fatalf("with manager: %d addresses, want 4", n)
	}
}

func countOf(event string) int64 {
	for _, c := range GovernanceCounts() {
		if c.Event == event {
			return c.Count
		}
	}
	return 0
}
