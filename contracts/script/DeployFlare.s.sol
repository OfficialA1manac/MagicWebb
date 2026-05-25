// SPDX-License-Identifier: MIT
pragma solidity 0.8.26;

import {Script, console2} from "forge-std/Script.sol";
import {Marketplace}  from "../src/Marketplace.sol";
import {AuctionHouse} from "../src/AuctionHouse.sol";
import {OfferBook}    from "../src/OfferBook.sol";

/// @notice Deploy Magic Webb to Flare mainnet (chain 14).
///         Single hardcoded 1.5% platform fee — sent directly to CREATOR_ADDR wallet.
///         Contracts are unstoppable: no pause, no admin. CREATOR_ADDR is fee recipient only.
///
/// Required env vars:
///   PRIVATE_KEY   -- deployer private key (never commit)
///   CREATOR_ADDR  -- fee recipient wallet address (Safe multi-sig recommended on mainnet)
contract DeployFlare is Script {
    function run() external {
        require(block.chainid == 14, "WRONG_CHAIN: use DeployCoston2.s.sol for testnet");
        uint256 pk      = vm.envUint("PRIVATE_KEY");
        address creator = vm.envAddress("CREATOR_ADDR");

        require(creator != address(0), "CREATOR_ADDR required");

        vm.startBroadcast(pk);

        Marketplace  marketplace = new Marketplace (creator);
        AuctionHouse auction     = new AuctionHouse(creator);
        OfferBook    offerBook   = new OfferBook   (creator);

        vm.stopBroadcast();

        console2.log("# Magic Webb Flare mainnet deploy -- paste into backend/.env");
        console2.log("CHAIN_ID=",         block.chainid);
        console2.log("MARKETPLACE_ADDR=", address(marketplace));
        console2.log("AUCTION_ADDR=",     address(auction));
        console2.log("OFFERBOOK_ADDR=",   address(offerBook));
        console2.log("# paste into frontend/.env.local");
        console2.log("NEXT_PUBLIC_CHAIN_ID=",         block.chainid);
        console2.log("NEXT_PUBLIC_MARKETPLACE_ADDR=", address(marketplace));
        console2.log("NEXT_PUBLIC_AUCTION_ADDR=",     address(auction));
        console2.log("NEXT_PUBLIC_OFFER_ADDR=",       address(offerBook));
        console2.log("CREATOR_ADDR=",  creator);
        console2.log("FEE=",           "1.5% (150 bps, hardcoded)");
    }
}
