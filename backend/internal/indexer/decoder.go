package indexer

import (
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

// Topic0 hashes for the events the indexer cares about.
var (
	TopicListed          = crypto.Keccak256Hash([]byte("Listed(address,uint256,address,uint128,uint64)"))
	TopicCancelled       = crypto.Keccak256Hash([]byte("Cancelled(address,uint256,address)"))
	TopicBought          = crypto.Keccak256Hash([]byte("Bought(address,uint256,address,address,uint128,uint256)"))
	TopicAuctionCreated  = crypto.Keccak256Hash([]byte("AuctionCreated(uint256,address,uint256,address,uint128,uint64,uint64)"))
	TopicBidPlaced       = crypto.Keccak256Hash([]byte("BidPlaced(uint256,address,uint128)"))
	TopicAuctionSettled  = crypto.Keccak256Hash([]byte("AuctionSettled(uint256,address,address,uint128,uint256)"))
	TopicOfferAccepted   = crypto.Keccak256Hash([]byte("OfferAccepted(address,uint256,address,address,uint128,uint256,uint64)"))
)

// EventName returns a stable string for a topic0 hash, or "" if unknown.
func EventName(topic0 common.Hash) string {
	switch topic0 {
	case TopicListed:
		return "Listed"
	case TopicCancelled:
		return "Cancelled"
	case TopicBought:
		return "Bought"
	case TopicAuctionCreated:
		return "AuctionCreated"
	case TopicBidPlaced:
		return "BidPlaced"
	case TopicAuctionSettled:
		return "AuctionSettled"
	case TopicOfferAccepted:
		return "OfferAccepted"
	}
	return ""
}
