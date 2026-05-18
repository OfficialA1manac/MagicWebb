// SPDX-License-Identifier: MIT
pragma solidity 0.8.26;

import {Script, console2} from "forge-std/Script.sol";
import {Marketplace} from "../src/Marketplace.sol";
import {AuctionHouse} from "../src/AuctionHouse.sol";
import {OfferBook} from "../src/OfferBook.sol";

/// @notice Flare mainnet (chainId 14) deploy. Asserts chainId so ops cannot accidentally
///         deploy mainnet code to a testnet RPC. `feeVault` is immutable per contract.
contract DeployFlare is Script {
    uint256 constant FLARE_MAINNET_CHAINID = 14;

    function run() external {
        require(block.chainid == FLARE_MAINNET_CHAINID, "WRONG_CHAIN_NOT_FLARE_MAINNET");

        uint256 pk      = vm.envUint("PRIVATE_KEY");
        address creator = vm.envAddress("CREATOR_ADDR");
        uint16  fee     = uint16(vm.envOr("FEE_BPS", uint256(150)));

        require(creator != address(0), "CREATOR_ADDR=0");
        require(fee <= 1_000, "FEE_BPS_OVER_CAP");

        vm.startBroadcast(pk);
        Marketplace  marketplace = new Marketplace (creator, fee);
        AuctionHouse auction     = new AuctionHouse(creator, fee);
        OfferBook    offer       = new OfferBook   (creator, fee);
        vm.stopBroadcast();

        console2.log("CHAIN_ID=",            block.chainid);
        console2.log("CREATOR_ADDR=",        creator);
        console2.log("FEE_BPS=",             uint256(fee));
        console2.log("FEE_VAULT_IMMUTABLE=", creator);
        console2.log("MARKETPLACE_ADDR=",    address(marketplace));
        console2.log("AUCTION_ADDR=",        address(auction));
        console2.log("OFFER_ADDR=",          address(offer));
    }
}
