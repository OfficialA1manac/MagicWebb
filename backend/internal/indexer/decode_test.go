package indexer

import (
	"errors"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)

func word(setLowByte byte) []byte {
	b := make([]byte, 32)
	b[31] = setLowByte
	return b
}

// standardOf must emit the Postgres token_standard enum values VERBATIM —
// lowercase. Uppercase variants are rejected by the DB (SQLSTATE 22P02),
// which silently dropped every Listed/AuctionCreated event in production.
func TestStandardOf(t *testing.T) {
	if got := standardOf(word(0)); got != "erc721" {
		t.Fatalf("standardOf(0) = %q, want erc721", got)
	}
	if got := standardOf(word(1)); got != "erc1155" {
		t.Fatalf("standardOf(1) = %q, want erc1155", got)
	}
}

func TestChunkOutOfRangeReturnsZeroWord(t *testing.T) {
	data := make([]byte, 64)
	data[31] = 0xAB
	if got := chunk(data, 0)[31]; got != 0xAB {
		t.Fatalf("chunk(0) low byte = %#x, want 0xAB", got)
	}
	z := chunk(data, 9) // beyond data
	if len(z) != 32 {
		t.Fatalf("out-of-range chunk len = %d, want 32", len(z))
	}
	for _, b := range z {
		if b != 0 {
			t.Fatal("out-of-range chunk should be zero-filled")
		}
	}
}

func TestBigStrAndTsUnix(t *testing.T) {
	b := make([]byte, 32)
	big.NewInt(1_700_000_000).FillBytes(b)
	if got := bigStr(b); got != "1700000000" {
		t.Fatalf("bigStr = %q", got)
	}
	if got := tsUnix(b).Unix(); got != 1_700_000_000 {
		t.Fatalf("tsUnix = %d", got)
	}
}

// Guards against the M1 bug class: an event the contract emits but the indexer never
// filters for (silently dropped). Every AuctionHouse v2 event the keeper/UI relies on
// must be in the core topic filter — including the cumulative-bid additions
// (OutbidNotification, LoserRefunded).
func TestCoreTopicsIncludesAuctionExtended(t *testing.T) {
	topics := coreTopics()[0]
	if len(topics) != 15 {
		t.Fatalf("core topics = %d, want 15", len(topics))
	}
	has := func(want common.Hash) bool {
		for _, h := range topics {
			if h == want {
				return true
			}
		}
		return false
	}
	if !has(TopicAuctionExtended) {
		t.Fatal("AuctionExtended missing from coreTopics filter — extensions would be dropped")
	}
	if !has(TopicOutbidNotification) {
		t.Fatal("OutbidNotification missing from coreTopics filter — outbid pushes would be dropped")
	}
	if !has(TopicLoserRefunded) {
		t.Fatal("LoserRefunded missing from coreTopics filter — refund sync would be dropped")
	}
	if !has(TopicRefundPushed) {
		t.Fatal("RefundPushed missing from coreTopics filter — withdraw-required tracking would be blind")
	}
	if !has(TopicAuctionSettlementFailed) {
		t.Fatal("AuctionSettlementFailed missing from coreTopics filter — a seller-default auction would sit 'active' forever")
	}
}

// abiWord encodes v as a 32-byte big-endian ABI word.
func abiWord(v *big.Int) []byte {
	b := make([]byte, 32)
	v.FillBytes(b)
	return b
}

// decodeABIString consumes eth_call return data from ARBITRARY collection
// contracts. big.Int.Int64() truncates >63-bit values (possibly to negative),
// so hostile offset/length words previously drove b[off:off+32] to panic
// inside an unrecovered goroutine, killing the whole server (CodeRabbit
// critical, 2026-08). Every hostile shape must yield "" — never panic.
func TestDecodeABIStringHostileWords(t *testing.T) {
	maxI64 := new(big.Int).SetInt64(1<<62 - 1)
	hostile := map[string][]byte{
		"negative offset (Int64 truncation)": append(
			abiWord(new(big.Int).Lsh(big.NewInt(1), 63)), // Int64() == MinInt64
			make([]byte, 32)...),
		"offset overflow (off+32 wraps)": append(
			abiWord(maxI64),
			make([]byte, 32)...),
		"offset past end": append(
			abiWord(big.NewInt(4096)),
			make([]byte, 32)...),
		"negative length": append(append(
			abiWord(big.NewInt(32)),
			abiWord(new(big.Int).Lsh(big.NewInt(1), 63))...),
			make([]byte, 32)...),
		"length overflow (start+n wraps)": append(append(
			abiWord(big.NewInt(32)),
			abiWord(maxI64)...),
			make([]byte, 32)...),
		"short return data": make([]byte, 63),
	}
	for name, b := range hostile {
		if got := decodeABIString(b); got != "" {
			t.Errorf("%s: got %q, want \"\"", name, got)
		}
	}

	// Sanity: a well-formed encoding still decodes.
	valid := append(abiWord(big.NewInt(32)), abiWord(big.NewInt(5))...)
	valid = append(valid, append([]byte("hello"), make([]byte, 27)...)...)
	if got := decodeABIString(valid); got != "hello" {
		t.Fatalf("valid string: got %q, want \"hello\"", got)
	}
}

// A well-formed EMPTY TransferBatch (ids=[] and values=[] — legal ERC-1155,
// canonical data = [0x40][0x60][len=0][len=0]) must be a silent no-op, not a
// "malformed" error that previously aborted (post-classification: spammed)
// the indexing range. The nil-q handlers proves the no-op returns before any
// DB access.
func TestOnTransferBatchEmptyBatchIsNoOp(t *testing.T) {
	h := &handlers{}
	data := append(abiWord(big.NewInt(0x40)), abiWord(big.NewInt(0x60))...)
	data = append(data, abiWord(big.NewInt(0))...) // ids len = 0
	data = append(data, abiWord(big.NewInt(0))...) // vals len = 0
	l := types.Log{
		Topics: []common.Hash{TopicTransferBatch, {}, {}, {}},
		Data:   data,
	}
	if err := h.onTransferBatch(nil, l); err != nil {
		t.Fatalf("empty batch: got %v, want nil", err)
	}
}

// Structural-validation failures must carry the errMalformedLog sentinel so
// the watcher log-and-skips them (a chain log is immutable — retrying can
// never succeed) instead of pinning the cursor forever.
func TestOnTransferBatchMalformedCarriesSentinel(t *testing.T) {
	h := &handlers{}
	l := types.Log{
		Topics: []common.Hash{TopicTransferBatch, {}, {}, {}},
		Data:   make([]byte, 32), // shorter than the 2-word head
	}
	err := h.onTransferBatch(nil, l)
	if err == nil {
		t.Fatal("short log: want error, got nil")
	}
	if !errors.Is(err, errMalformedLog) {
		t.Fatalf("short log: error %v does not wrap errMalformedLog", err)
	}
}
