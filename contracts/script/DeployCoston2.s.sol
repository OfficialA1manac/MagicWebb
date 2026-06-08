// SPDX-License-Identifier: MIT
pragma solidity 0.8.26;

import {Script, console2} from "forge-std/Script.sol";
import {Marketplace}  from "../src/Marketplace.sol";
import {AuctionHouse} from "../src/AuctionHouse.sol";
import {OfferBook}    from "../src/OfferBook.sol";

/// @notice Deploy Magic Webb to Flare Coston2 (chain 114).
///         Single hardcoded 1.5% platform fee — sent directly to CREATOR_ADDR wallet.
///         Contracts are unstoppable: no pause, no admin. CREATOR_ADDR is fee recipient only.
///
/// Required env vars:
///   PRIVATE_KEY   -- deployer private key (never commit)
///   CREATOR_ADDR  -- fee recipient wallet address
contract DeployCoston2 is Script {
    function run() external {
        require(block.chainid == 114, "WRONG_CHAIN: use DeployFlare.s.sol for mainnet");
        uint256 pk      = vm.envUint("PRIVATE_KEY");
        address creator = vm.envAddress("CREATOR_ADDR");

        require(creator != address(0), "CREATOR_ADDR required");

        vm.startBroadcast(pk);

        Marketplace  marketplace = new Marketplace (creator);
        AuctionHouse auction     = new AuctionHouse(creator);
        OfferBook    offerBook   = new OfferBook   (creator);

        vm.stopBroadcast();

        console2.log("# Magic Webb Coston2 deploy -- paste into backend/.env + frontend wallet.js");
        console2.log("CHAIN_ID=",         block.chainid);
        console2.log("MARKETPLACE_ADDR=", address(marketplace));
        console2.log("AUCTION_ADDR=",     address(auction));
        console2.log("OFFERBOOK_ADDR=",   address(offerBook));
        console2.log("CREATOR_ADDR=",     creator);
        console2.log("FEE=",              "1.5% (150 bps, hardcoded, seller-pays on sale)");
        // Sanity: every contract must report the same immutable fee recipient.
        require(marketplace.feeRecipient() == creator, "MARKETPLACE feeRecipient mismatch");
        require(auction.feeRecipient()     == creator, "AUCTION feeRecipient mismatch");
        require(offerBook.feeRecipient()   == creator, "OFFERBOOK feeRecipient mismatch");
        console2.log("feeRecipient verified on all three contracts ==", creator);
    }
}
