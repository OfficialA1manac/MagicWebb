// SPDX-License-Identifier: MIT
pragma solidity 0.8.24;

import {Script, console2} from "forge-std/Script.sol";
import {Marketplace} from "../src/Marketplace.sol";
import {AuctionHouse} from "../src/AuctionHouse.sol";
import {OfferBook} from "../src/OfferBook.sol";

/// @notice Chain-agnostic production deploy. Targets Flare Coston2 (114) by default and
///         Flare mainnet (14) when --rpc-url points there. No FeeVault contract — the
///         creator EOA receives platform fees directly (one CALL hop saved per trade).
///         `feeVault` is an IMMUTABLE constructor argument: once deployed it CANNOT be changed.
contract DeployCoston2 is Script {
    function run() external {
        uint256 pk      = vm.envUint("PRIVATE_KEY");
        address creator = vm.envAddress("CREATOR_ADDR");
        uint16  fee     = uint16(vm.envOr("FEE_BPS", uint256(250)));

        require(creator != address(0), "CREATOR_ADDR required (set in .env.local; never commit keys)");

        vm.startBroadcast(pk);
        Marketplace  marketplace = new Marketplace (creator, fee);
        AuctionHouse auction     = new AuctionHouse(creator, fee);
        OfferBook    offer       = new OfferBook   (creator, fee);
        vm.stopBroadcast();

        console2.log("CHAIN_ID=",         block.chainid);
        console2.log("CREATOR_ADDR=",     creator);
        console2.log("FEE_BPS=",          uint256(fee));
        console2.log("FEE_VAULT_IMMUTABLE=", creator);
        console2.log("MARKETPLACE_ADDR=", address(marketplace));
        console2.log("AUCTION_ADDR=",     address(auction));
        console2.log("OFFER_ADDR=",       address(offer));
    }
}
