// SPDX-License-Identifier: MIT
pragma solidity ^0.8.24;

import {Script, console2} from "forge-std/Script.sol";
import {Marketplace} from "../src/Marketplace.sol";
import {AuctionHouse} from "../src/AuctionHouse.sol";
import {OfferBook} from "../src/OfferBook.sol";

/// @notice Coston2 production deploy. FeeVault is intentionally NOT deployed —
///         the creator EOA is set as `feeVault` directly on each trade contract to skip a CALL hop.
///         Use `script/DeployFeeVault.s.sol` later if a contract sink is desired (admin can switch via `setFeeVault`).
contract DeployCoston2 is Script {
    address constant DEFAULT_CREATOR = 0x78993B71051de91C2D2595BC3475F07748927dc0;

    function run() external {
        uint256 pk      = vm.envUint("PRIVATE_KEY");
        address creator = vm.envOr("CREATOR_ADDR", DEFAULT_CREATOR);
        address admin   = vm.envOr("ADMIN_ADDR",   creator);
        uint16  fee     = uint16(vm.envOr("FEE_BPS", uint256(250)));

        require(creator != address(0), "CREATOR_ADDR=0");
        require(admin   != address(0), "ADMIN_ADDR=0");

        vm.startBroadcast(pk);
        Marketplace  marketplace = new Marketplace (admin, creator, fee);
        AuctionHouse auction     = new AuctionHouse(admin, creator, fee);
        OfferBook    offer       = new OfferBook   (admin, creator, fee);
        vm.stopBroadcast();

        console2.log("CHAIN_ID=",         block.chainid);
        console2.log("CREATOR_ADDR=",     creator);
        console2.log("ADMIN_ADDR=",       admin);
        console2.log("FEE_BPS=",          uint256(fee));
        console2.log("MARKETPLACE_ADDR=", address(marketplace));
        console2.log("AUCTION_ADDR=",     address(auction));
        console2.log("OFFER_ADDR=",       address(offer));
    }
}
