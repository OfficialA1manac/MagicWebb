package indexer

import (
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
)

func word(setLowByte byte) []byte {
	b := make([]byte, 32)
	b[31] = setLowByte
	return b
}

func TestStandardOf(t *testing.T) {
	if got := standardOf(word(0)); got != "ERC721" {
		t.Fatalf("standardOf(0) = %q, want ERC721", got)
	}
	if got := standardOf(word(1)); got != "ERC1155" {
		t.Fatalf("standardOf(1) = %q, want ERC1155", got)
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
	if len(topics) != 13 {
		t.Fatalf("core topics = %d, want 13", len(topics))
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
}
