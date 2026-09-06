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
	// Seller failed to deliver the NFT: settle() finalises the auction with no
	// sale and returns the winner's escrow. Emitted INSTEAD OF AuctionSettled,
	// so without this topic the auction would sit 'active' in the DB forever.
	TopicAuctionSettlementFailed = crypto.Keccak256Hash([]byte("AuctionSettlementFailed(uint256,address,uint128)"))
	// v3.4: keeper/seller/winner force-cancelled a stuck auction after
	// endsAt + 3 days (SELLER_DEFAULT_WINDOW). Only sets settled=true so
	// refundLosers unlocks — no sale, no AuctionSettled. Without this topic
	// the row would sit 'active' forever and the keeper would keep re-settling.
	TopicAuctionForceCancelled = crypto.Keccak256Hash([]byte("AuctionForceCancelled(uint256)"))
	TopicRefundPushed          = crypto.Keccak256Hash([]byte("RefundPushed(address,uint256)"))

	// OfferBook (Model A: stacked positions, fee taken at make)
	TopicOfferMade     = crypto.Keccak256Hash([]byte("OfferMade(address,uint256,address,uint256,uint128,uint64)"))
	TopicOfferAccepted = crypto.Keccak256Hash([]byte("OfferAccepted(address,uint256,address,address,uint256,uint256,uint128,uint8)"))
	TopicOfferRefunded = crypto.Keccak256Hash([]byte("OfferRefunded(address,uint256,address,uint256)"))
	// OfferEligibilitySet(address indexed coll, bool indexed eligible) — both
	// params indexed, so the log carries no data words (topics[1]=coll,
	// topics[2]=eligible).
	TopicOfferEligibilitySet = crypto.Keccak256Hash([]byte("OfferEligibilitySet(address,bool)"))

	// NFT collections (ERC-721 / ERC-1155) — watched on tracked collections to
	// maintain ownership and orphan listings whose seller no longer holds the token.
	TopicTransfer721    = crypto.Keccak256Hash([]byte("Transfer(address,address,uint256)"))
	TopicTransferSingle = crypto.Keccak256Hash([]byte("TransferSingle(address,address,address,uint256,uint256)"))
	TopicTransferBatch  = crypto.Keccak256Hash([]byte("TransferBatch(address,address,address,uint256[],uint256[])"))

	// ── Governance (v3.6 wave 1) ────────────────────────────────────────────
	// MarketplaceManager (plain, unproxied): the keeper slot and the admin
	// lifecycle. AuditLog is emitted alongside every one of them; we index it
	// too so the trail is complete even if a future manager adds an action.
	TopicKeeperSet              = crypto.Keccak256Hash([]byte("KeeperSet(address,address)"))
	TopicAdminRenounced         = crypto.Keccak256Hash([]byte("AdminRenounced(address)"))
	TopicAdminTransferStarted   = crypto.Keccak256Hash([]byte("AdminTransferStarted(address,address)"))
	TopicAdminTransferCancelled = crypto.Keccak256Hash([]byte("AdminTransferCancelled(address)"))
	TopicAdminTransferred       = crypto.Keccak256Hash([]byte("AdminTransferred(address,address)"))
	TopicAuditLog               = crypto.Keccak256Hash([]byte("AuditLog(bytes32,address,address,bytes32)"))
	// MarketplaceCore (each UUPS proxy): the upgrade queue plus the ERC-1967
	// Upgraded event OpenZeppelin emits when an implementation is installed
	// (also once at proxy construction, which is how a fresh deploy's impls
	// become known without a storage read).
	TopicUpgradeQueued    = crypto.Keccak256Hash([]byte("UpgradeQueued(address,uint64)"))
	TopicUpgradeCancelled = crypto.Keccak256Hash([]byte("UpgradeCancelled(address)"))
	TopicUpgraded         = crypto.Keccak256Hash([]byte("Upgraded(address)"))
)

// coreTopics returns every marketplace selector — used in the core getLogs topics[0] filter.
// Governance topics ride in the same filter: the core proxies emit the upgrade
// events and the manager address is appended to the address list by the
// watcher, so one getLogs call covers trading + governance.
func coreTopics() [][]common.Hash {
	return [][]common.Hash{append([]common.Hash{
		TopicListed, TopicCancelled, TopicBought,
		TopicAuctionCreated, TopicBidPlaced, TopicOutbidNotification, TopicAuctionExtended,
		TopicAuctionSettled, TopicLoserRefunded, TopicAuctionCancelled,
		TopicAuctionSettlementFailed, TopicAuctionForceCancelled,
		TopicRefundPushed,
		TopicOfferMade, TopicOfferAccepted, TopicOfferRefunded, TopicOfferEligibilitySet,
	}, governanceTopics()[0]...)}
}

// governanceTopics returns only the manager + upgrade selectors — used by
// cmd/reindexgov to backfill the governance trail without replaying trades.
func governanceTopics() [][]common.Hash {
	return [][]common.Hash{{
		TopicKeeperSet, TopicAdminRenounced, TopicAdminTransferStarted,
		TopicAdminTransferCancelled, TopicAdminTransferred, TopicAuditLog,
		TopicUpgradeQueued, TopicUpgradeCancelled, TopicUpgraded,
	}}
}

// transferTopics returns the NFT transfer selectors — used in the per-collection
// getLogs topics[0] filter to track ownership and orphan stale listings.
func transferTopics() [][]common.Hash {
	return [][]common.Hash{{
		TopicTransfer721, TopicTransferSingle, TopicTransferBatch,
	}}
}
