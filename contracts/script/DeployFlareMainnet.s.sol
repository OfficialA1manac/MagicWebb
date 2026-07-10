// SPDX-License-Identifier: MIT
pragma solidity 0.8.26;

import {Script, console2} from "forge-std/Script.sol";
import {ERC1967Proxy}      from "@openzeppelin/contracts/proxy/ERC1967/ERC1967Proxy.sol";
import {Marketplace}        from "../src/Marketplace.sol";
import {AuctionHouse}       from "../src/AuctionHouse.sol";
import {OfferBook}          from "../src/OfferBook.sol";
import {MarketplaceManager} from "../src/MarketplaceManager.sol";

/// @notice Deploy Magic Webb to Flare Mainnet (chain 14) using UUPS proxies.
///
/// Required env vars:
///   PRIVATE_KEY   -- deployer private key (never commit)
///   CREATOR_ADDR  -- fee recipient wallet address (MUST be a multisig for mainnet)
/// Optional env vars:
///   KEEPER_ADDR   -- keeper identity registered under KEEPER_ROLE
///
/// Mainnet safety notes:
///   - CREATOR_ADDR MUST be a multisig (Safe or equivalent), NOT an EOA.
///     Compromise of a single EOA with DEFAULT_ADMIN_ROLE + OPERATOR_ROLE
///     can halt new listings/bids/auctions/offers indefinitely.
///   - Verify PLATFORM_FEE_BPS / MIN_PRICE against live FLR price before launch.
contract DeployFlareMainnet is Script {
    function run() external {
        require(block.chainid == 14, "WRONG_CHAIN: this script targets Flare Mainnet (chain 14)");
        uint256 pk      = vm.envUint("PRIVATE_KEY");
        address creator = vm.envAddress("CREATOR_ADDR");
        address keeper  = vm.envOr("KEEPER_ADDR", address(0));

        require(creator != address(0), "CREATOR_ADDR required");

        address deployer = vm.addr(pk);

        vm.startBroadcast(pk);

        MarketplaceManager managerImpl = new MarketplaceManager();
        ERC1967Proxy managerProxy = new ERC1967Proxy(
            address(managerImpl),
            abi.encodeWithSelector(MarketplaceManager.initialize.selector, deployer)
        );
        MarketplaceManager manager = MarketplaceManager(address(managerProxy));

        Marketplace marketplaceImpl = new Marketplace();
        ERC1967Proxy marketplaceProxy = new ERC1967Proxy(
            address(marketplaceImpl),
            abi.encodeWithSelector(Marketplace.initialize.selector, creator, address(manager))
        );
        Marketplace marketplace = Marketplace(address(marketplaceProxy));

        AuctionHouse auctionImpl = new AuctionHouse();
        ERC1967Proxy auctionProxy = new ERC1967Proxy(
            address(auctionImpl),
            abi.encodeWithSelector(AuctionHouse.initialize.selector, creator, address(manager))
        );
        AuctionHouse auction = AuctionHouse(address(auctionProxy));

        OfferBook offerBookImpl = new OfferBook();
        ERC1967Proxy offerBookProxy = new ERC1967Proxy(
            address(offerBookImpl),
            abi.encodeWithSelector(OfferBook.initialize.selector, creator, address(manager))
        );
        OfferBook offerBook = OfferBook(address(offerBookProxy));

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

        console2.log("# Magic Webb Flare Mainnet deploy -- paste into backend/.env + fly secrets");
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
