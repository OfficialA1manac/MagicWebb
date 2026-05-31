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
	// Bought lost the trailing royalty arg in the rework.
	TopicBought    = crypto.Keccak256Hash([]byte("Bought(address,uint256,address,address,uint8,uint128,uint128,uint256)"))

	// AuctionHouse
	TopicAuctionCreated   = crypto.Keccak256Hash([]byte("AuctionCreated(uint256,address,uint256,address,uint8,uint128,uint128,uint128,uint64,uint64)"))
	TopicBidPlaced        = crypto.Keccak256Hash([]byte("BidPlaced(uint256,address,uint128,uint128)"))
	TopicAuctionExtended  = crypto.Keccak256Hash([]byte("AuctionExtended(uint256,uint64)"))
	TopicAuctionSettled   = crypto.Keccak256Hash([]byte("AuctionSettled(uint256,address,address,uint128,uint256)"))
	TopicAuctionCancelled = crypto.Keccak256Hash([]byte("AuctionCancelled(uint256)"))

	// OfferBook (stacked positions)
	TopicOfferPositionUpdated     = crypto.Keccak256Hash([]byte("OfferPositionUpdated(address,uint256,address,uint128,uint128,uint64)"))
	TopicOfferRefunded            = crypto.Keccak256Hash([]byte("OfferRefunded(address,uint256,address,uint128)"))
	TopicOfferAccepted            = crypto.Keccak256Hash([]byte("OfferAccepted(address,uint256,address,address,uint128,uint256)"))
	TopicOffer1155PositionUpdated = crypto.Keccak256Hash([]byte("Offer1155PositionUpdated(address,uint256,address,uint128,uint128,uint128,uint64)"))
	TopicOffer1155Refunded        = crypto.Keccak256Hash([]byte("Offer1155Refunded(address,uint256,address,uint128)"))
	TopicOffer1155Accepted        = crypto.Keccak256Hash([]byte("Offer1155Accepted(address,uint256,address,address,uint128,uint128,uint256)"))

	// Token transfers (off-contract) — used to detect orphaned listings and update nft_ownership.
	TopicTransfer721         = crypto.Keccak256Hash([]byte("Transfer(address,address,uint256)"))
	TopicTransferSingle1155  = crypto.Keccak256Hash([]byte("TransferSingle(address,address,address,uint256,uint256)"))
	TopicTransferBatch1155   = crypto.Keccak256Hash([]byte("TransferBatch(address,address,address,uint256[],uint256[])"))
)

// allMarketTopics returns selectors emitted by the three marketplace contracts.
// Used in the primary getLogs filter (addresses scoped to those three).
func allMarketTopics() [][]common.Hash {
	return [][]common.Hash{{
		TopicListed, TopicCancelled, TopicBought,
		TopicAuctionCreated, TopicBidPlaced, TopicAuctionExtended,
		TopicAuctionSettled, TopicAuctionCancelled,
		TopicOfferPositionUpdated, TopicOfferRefunded, TopicOfferAccepted,
		TopicOffer1155PositionUpdated, TopicOffer1155Refunded, TopicOffer1155Accepted,
	}}
}

// transferTopics returns selectors used for ownership tracking on NFT collections.
func transferTopics() [][]common.Hash {
	return [][]common.Hash{{
		TopicTransfer721, TopicTransferSingle1155, TopicTransferBatch1155,
	}}
}
