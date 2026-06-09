package indexer

import (
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

// Event topic hashes — keccak256 of canonical ABI signatures.
// TokenStandard enum → uint8 in canonical form. These MUST match the reworked
// the deployed contracts exactly; a stale signature silently drops the event.
var (
	// Marketplace
	TopicListed    = crypto.Keccak256Hash([]byte("Listed(address,uint256,address,uint8,uint128,uint128,uint64)"))
	TopicCancelled = crypto.Keccak256Hash([]byte("Cancelled(address,uint256,address)"))
	TopicBought    = crypto.Keccak256Hash([]byte("Bought(address,uint256,address,address,uint8,uint128,uint128,uint256)"))

	// AuctionHouse (v2: cumulative bids, escrow-until-settle)
	TopicAuctionCreated     = crypto.Keccak256Hash([]byte("AuctionCreated(uint256,address,uint256,address,uint8,uint128,uint128,uint64,uint64)"))
	TopicBidPlaced          = crypto.Keccak256Hash([]byte("BidPlaced(uint256,address,uint256,uint256)"))
	TopicOutbidNotification = crypto.Keccak256Hash([]byte("OutbidNotification(uint256,address,uint256)"))
	TopicAuctionExtended    = crypto.Keccak256Hash([]byte("AuctionExtended(uint256,uint64)"))
	TopicAuctionSettled     = crypto.Keccak256Hash([]byte("AuctionSettled(uint256,address,address,uint128,uint256)"))
	TopicLoserRefunded      = crypto.Keccak256Hash([]byte("LoserRefunded(uint256,address,uint256)"))
	TopicAuctionCancelled   = crypto.Keccak256Hash([]byte("AuctionCancelled(uint256)"))

	// OfferBook (Model A: stacked positions, fee taken at make)
	TopicOfferMade     = crypto.Keccak256Hash([]byte("OfferMade(address,uint256,address,uint256,uint128,uint64)"))
	TopicOfferAccepted = crypto.Keccak256Hash([]byte("OfferAccepted(address,uint256,address,address,uint256,uint256,uint128,uint8)"))
	TopicOfferRefunded = crypto.Keccak256Hash([]byte("OfferRefunded(address,uint256,address,uint256)"))

	// NFT collections (ERC-721 / ERC-1155) — watched on tracked collections to
	// maintain ownership and orphan listings whose seller no longer holds the token.
	TopicTransfer721    = crypto.Keccak256Hash([]byte("Transfer(address,address,uint256)"))
	TopicTransferSingle = crypto.Keccak256Hash([]byte("TransferSingle(address,address,address,uint256,uint256)"))
	TopicTransferBatch  = crypto.Keccak256Hash([]byte("TransferBatch(address,address,address,uint256[],uint256[])"))
)

// coreTopics returns every marketplace selector — used in the core getLogs topics[0] filter.
func coreTopics() [][]common.Hash {
	return [][]common.Hash{{
		TopicListed, TopicCancelled, TopicBought,
		TopicAuctionCreated, TopicBidPlaced, TopicOutbidNotification, TopicAuctionExtended,
		TopicAuctionSettled, TopicLoserRefunded, TopicAuctionCancelled,
		TopicOfferMade, TopicOfferAccepted, TopicOfferRefunded,
	}}
}

// transferTopics returns the NFT transfer selectors — used in the per-collection
// getLogs topics[0] filter to track ownership and orphan stale listings.
func transferTopics() [][]common.Hash {
	return [][]common.Hash{{
		TopicTransfer721, TopicTransferSingle, TopicTransferBatch,
	}}
}
