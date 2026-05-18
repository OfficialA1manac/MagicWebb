package indexer

import (
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

// Event topic hashes — keccak256 of canonical ABI signatures.
// TokenStandard enum → uint8 in canonical form.
var (
	// Marketplace
	TopicListed    = crypto.Keccak256Hash([]byte("Listed(address,uint256,address,uint8,uint128,uint128,uint64)"))
	TopicCancelled = crypto.Keccak256Hash([]byte("Cancelled(address,uint256,address)"))
	TopicBought    = crypto.Keccak256Hash([]byte("Bought(address,uint256,address,address,uint8,uint128,uint128,uint256,uint256)"))

	// AuctionHouse
	TopicAuctionCreated   = crypto.Keccak256Hash([]byte("AuctionCreated(uint256,address,uint256,address,uint8,uint128,uint128,uint64,uint64)"))
	TopicBidPlaced        = crypto.Keccak256Hash([]byte("BidPlaced(uint256,address,uint128)"))
	TopicAuctionSettled   = crypto.Keccak256Hash([]byte("AuctionSettled(uint256,address,address,uint128,uint256,uint256)"))
	TopicAuctionCancelled = crypto.Keccak256Hash([]byte("AuctionCancelled(uint256)"))

	// OfferBook
	TopicOfferAccepted     = crypto.Keccak256Hash([]byte("OfferAccepted(address,uint256,address,address,uint128,uint256,uint256,uint64)"))
	TopicOffer1155Accepted = crypto.Keccak256Hash([]byte("Offer1155Accepted(address,uint256,address,address,uint128,uint128,uint256,uint256,uint64)"))
)

// allTopics returns every selector — used in getLogs topics[0] filter.
func allTopics() [][]common.Hash {
	return [][]common.Hash{{
		TopicListed, TopicCancelled, TopicBought,
		TopicAuctionCreated, TopicBidPlaced,
		TopicAuctionSettled, TopicAuctionCancelled,
		TopicOfferAccepted, TopicOffer1155Accepted,
	}}
}
