// SPDX-License-Identifier: MIT
pragma solidity 0.8.26;

import {Script, console2} from "forge-std/Script.sol";
import {Marketplace}        from "../src/Marketplace.sol";
import {AuctionHouse}       from "../src/AuctionHouse.sol";
import {OfferBook}          from "../src/OfferBook.sol";
import {MarketplaceManager} from "../src/MarketplaceManager.sol";

/// @notice Deploy Magic Webb to Flare mainnet (chain 14).
///         Single hardcoded 1.5% platform fee — sent directly to CREATOR_ADDR wallet.
///         Cores are immutable escrow contracts; the MarketplaceManager provides the
///         role registry + entry-only circuit breaker ("pausable entries,
///         unstoppable exits") and the future token-module slots.
///
/// Required env vars:
///   PRIVATE_KEY   -- deployer private key (never commit)
///   CREATOR_ADDR  -- fee recipient + manager admin (Safe multi-sig REQUIRED on mainnet)
/// Optional env vars:
///   KEEPER_ADDR   -- keeper identity registered under KEEPER_ROLE
contract DeployFlare is Script {
    function run() external {
        require(block.chainid == 14, "WRONG_CHAIN: use DeployCoston2.s.sol for testnet");
        uint256 pk      = vm.envUint("PRIVATE_KEY");
        address creator = vm.envAddress("CREATOR_ADDR");
        address keeper  = vm.envOr("KEEPER_ADDR", address(0));

        require(creator != address(0), "CREATOR_ADDR required");
        // Mainnet gating: the admin/fee wallet must be a contract (Safe
        // multisig), never a single EOA key.
        require(creator.code.length > 0, "CREATOR_ADDR must be a multisig contract on mainnet");

        address deployer = vm.addr(pk);

        vm.startBroadcast(pk);

        // Deployer is temporary admin for setup; control is handed to CREATOR_ADDR
        // and every deployer role renounced before the broadcast ends.
        MarketplaceManager manager = new MarketplaceManager(deployer);

        Marketplace  marketplace = new Marketplace (creator, address(manager));
        AuctionHouse auction     = new AuctionHouse(creator, address(manager));
        OfferBook    offerBook   = new OfferBook   (creator, address(manager));

        manager.setCoreContracts(address(marketplace), address(auction), address(offerBook));
        if (keeper != address(0)) {
            manager.grantRole(manager.KEEPER_ROLE(), keeper);
        }
        manager.grantRole(manager.DEFAULT_ADMIN_ROLE(), creator);
        manager.grantRole(manager.OPERATOR_ROLE(), creator);
        manager.renounceRole(manager.OPERATOR_ROLE(), deployer);
        manager.renounceRole(manager.DEFAULT_ADMIN_ROLE(), deployer);

        vm.stopBroadcast();

        console2.log("# Magic Webb Flare mainnet deploy -- paste into .env + frontend wallet.js");
        console2.log("CHAIN_ID=",         block.chainid);
        console2.log("MANAGER_ADDR=",     address(manager));
        console2.log("MARKETPLACE_ADDR=", address(marketplace));
        console2.log("AUCTION_ADDR=",     address(auction));
        console2.log("OFFERBOOK_ADDR=",   address(offerBook));
        console2.log("CREATOR_ADDR=",     creator);
        console2.log("FEE=",              "1.5% (150 bps, hardcoded, seller-pays on sale)");
        // Sanity: every contract must report the same immutable fee recipient and manager.
        require(marketplace.feeRecipient() == creator, "MARKETPLACE feeRecipient mismatch");
        require(auction.feeRecipient()     == creator, "AUCTION feeRecipient mismatch");
        require(offerBook.feeRecipient()   == creator, "OFFERBOOK feeRecipient mismatch");
        require(marketplace.manager() == address(manager), "MARKETPLACE manager mismatch");
        require(auction.manager()     == address(manager), "AUCTION manager mismatch");
        require(offerBook.manager()   == address(manager), "OFFERBOOK manager mismatch");
        require(manager.entriesAllowed(), "manager must deploy unpaused");
        require(manager.hasRole(manager.DEFAULT_ADMIN_ROLE(), creator),   "creator must hold admin");
        require(manager.hasRole(manager.OPERATOR_ROLE(), creator),        "creator must hold operator");
        require(!manager.hasRole(manager.DEFAULT_ADMIN_ROLE(), deployer), "deployer must have renounced admin");
        require(!manager.hasRole(manager.OPERATOR_ROLE(), deployer),      "deployer must have renounced operator");
        if (keeper != address(0)) {
            require(manager.hasRole(manager.KEEPER_ROLE(), keeper), "keeper role missing");
        }
        console2.log("feeRecipient + manager + admin handover verified");
    }
}
