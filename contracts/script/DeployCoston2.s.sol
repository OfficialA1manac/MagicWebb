// SPDX-License-Identifier: MIT
pragma solidity 0.8.26;

import {Script, console2} from "forge-std/Script.sol";
import {Marketplace}        from "../src/Marketplace.sol";
import {AuctionHouse}       from "../src/AuctionHouse.sol";
import {OfferBook}          from "../src/OfferBook.sol";
import {MarketplaceManager} from "../src/MarketplaceManager.sol";
import {MagicWebbNFT}       from "../src/MagicWebbNFT.sol";

/// @notice Deploy Magic Webb to Flare Coston2 (chain 114).
///         Single hardcoded 1.5% platform fee — sent directly to CREATOR_ADDR wallet.
///         Cores are immutable escrow contracts; the MarketplaceManager provides the
///         role registry + entry-only circuit breaker ("pausable entries,
///         unstoppable exits") and the future token-module slots.
///
/// Required env vars:
///   PRIVATE_KEY   -- deployer private key (never commit)
///   CREATOR_ADDR  -- fee recipient wallet address (MUST be a Gnosis Safe multisig for production;
///                    run DeploySafe.s.sol first and use the resulting SAFE_ADDR)
/// Optional env vars:
///   KEEPER_ADDR   -- keeper identity registered under KEEPER_ROLE
///
/// Mainnet safety notes:
///   - CREATOR_ADDR MUST be a multisig (Safe or equivalent), NOT an EOA.
///     Compromise of a single EOA with DEFAULT_ADMIN_ROLE + OPERATOR_ROLE
///     can halt new listings/bids/auctions/offers indefinitely.
///   - After deploying the Gnosis Safe (via DeploySafe.s.sol), set up the
///     Zodiac Allowance Module on-chain to authorize the keeper to sweep fees
///     from the Safe to your personal wallet:
///       1. enableModule(allowanceModuleAddr) on the Safe
///       2. addDelegate(KEEPER_ADDR) on the Allowance Module
///       3. setAllowance(KEEPER_ADDR, address(0), <periodAmount>, <periodInSeconds>, 0)
///     Then set SAFE_ADDR and PERSONAL_WALLET_ADDR in the backend env for automatic sweeping.
contract DeployCoston2 is Script {
    function run() external {
        require(block.chainid == 114, "WRONG_CHAIN: this script targets Coston2 (chain 114)");
        uint256 pk      = vm.envUint("PRIVATE_KEY");
        address creator = vm.envAddress("CREATOR_ADDR");
        address keeper  = vm.envOr("KEEPER_ADDR", address(0));

        require(creator != address(0), "CREATOR_ADDR required");

        address deployer = vm.addr(pk);

        vm.startBroadcast(pk);

        // Deployer is temporary admin for setup; control is handed to CREATOR_ADDR
        // and every deployer role renounced before the broadcast ends.
        MarketplaceManager manager = new MarketplaceManager(deployer);

        // C-03: Production ERC-721 NFT contract. Ownable (owner=creator), sequential
        // mint, per-token URI storage. Replaces the previous out-of-repo mock at
        // 0x0E513BfE29E00E160ADE7516AD9363F070a101bF.
        MagicWebbNFT  nft         = new MagicWebbNFT("Magic Webb Animi", "ANIMI", creator);

        Marketplace  marketplace = new Marketplace (creator, address(manager));
        AuctionHouse auction     = new AuctionHouse(creator, address(manager));
        OfferBook    offerBook   = new OfferBook   (creator, address(manager));

        manager.setCoreContracts(address(marketplace), address(auction), address(offerBook));
        if (keeper != address(0)) {
            manager.grantRole(manager.KEEPER_ROLE(), keeper);
        }
        manager.grantRole(manager.DEFAULT_ADMIN_ROLE(), creator);
        manager.grantRole(manager.OPERATOR_ROLE(), creator);
        if (creator != deployer) {
            manager.renounceRole(manager.OPERATOR_ROLE(), deployer);
            manager.renounceRole(manager.DEFAULT_ADMIN_ROLE(), deployer);
        }

        vm.stopBroadcast();

        console2.log("# Magic Webb Coston2 deploy -- paste into backend/.env + frontend wallet.js");
        console2.log("CHAIN_ID=",         block.chainid);
        console2.log("MANAGER_ADDR=",     address(manager));
        console2.log("MARKETPLACE_ADDR=", address(marketplace));
        console2.log("AUCTION_ADDR=",     address(auction));
        console2.log("OFFERBOOK_ADDR=",   address(offerBook));
        console2.log("NFT_ADDR=",         address(nft));
        console2.log("CREATOR_ADDR=",     creator);
        console2.log("FEE=",              "1.5% (150 bps, hardcoded, seller-pays on sale)");
        // Sanity: every contract must report the same immutable fee recipient and manager.
        require(marketplace.feeRecipient() == creator, "MARKETPLACE feeRecipient mismatch");
        require(auction.feeRecipient()     == creator, "AUCTION feeRecipient mismatch");
        require(offerBook.feeRecipient()   == creator, "OFFERBOOK feeRecipient mismatch");
        require(marketplace.manager() == address(manager), "MARKETPLACE manager mismatch");
        require(auction.manager()     == address(manager), "AUCTION manager mismatch");
        require(offerBook.manager()   == address(manager), "OFFERBOOK manager mismatch");
        require(nft.owner()           == creator,          "NFT owner mismatch");
        require(manager.entriesAllowed(), "manager must deploy unpaused");
        require(manager.hasRole(manager.DEFAULT_ADMIN_ROLE(), creator),   "creator must hold admin");
        require(manager.hasRole(manager.OPERATOR_ROLE(), creator),        "creator must hold operator");
        if (creator != deployer) {
            require(!manager.hasRole(manager.DEFAULT_ADMIN_ROLE(), deployer), "deployer must have renounced admin");
            require(!manager.hasRole(manager.OPERATOR_ROLE(), deployer),      "deployer must have renounced operator");
        }
        if (keeper != address(0)) {
            require(manager.hasRole(manager.KEEPER_ROLE(), keeper), "keeper role missing");
        }
        console2.log("feeRecipient + manager + admin handover verified");
    }
}
