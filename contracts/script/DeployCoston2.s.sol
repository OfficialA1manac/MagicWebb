// SPDX-License-Identifier: MIT
pragma solidity 0.8.26;

import {Script, console2} from "forge-std/Script.sol";
import {Marketplace}  from "../src/Marketplace.sol";
import {AuctionHouse} from "../src/AuctionHouse.sol";
import {OfferBook}    from "../src/OfferBook.sol";

/// @notice Deploy Magic Webb to Flare Coston2 (chain 114).
///         Single 1.5% platform fee -- no royalties.
///
/// Required env vars:
///   PRIVATE_KEY   -- deployer private key (never commit)
///   CREATOR_ADDR  -- fee recipient and admin (your wallet address)
///
/// Optional env vars:
///   FEE_BPS       -- platform fee in basis points (default 150 = 1.5%)
contract DeployCoston2 is Script {
    function run() external {
        require(block.chainid == 114, "WRONG_CHAIN: use DeployFlare.s.sol for mainnet");
        uint256 pk      = vm.envUint("PRIVATE_KEY");
        address creator = vm.envAddress("CREATOR_ADDR");
        uint16  fee     = uint16(vm.envOr("FEE_BPS", uint256(150)));

        require(creator != address(0), "CREATOR_ADDR required");
        require(fee <= 1_000,          "FEE_BPS must be <= 1000 (10%)");

        vm.startBroadcast(pk);

        Marketplace  marketplace = new Marketplace (creator, fee, creator);
        AuctionHouse auction     = new AuctionHouse(creator, fee, creator);
        OfferBook    offerBook   = new OfferBook   (creator, fee, creator);

        vm.stopBroadcast();

        console2.log("# Magic Webb Coston2 deploy -- paste into backend/.env");
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
        console2.log("FEE_BPS=",       uint256(fee));
    }
}
