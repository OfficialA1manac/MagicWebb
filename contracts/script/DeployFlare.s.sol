// SPDX-License-Identifier: MIT
pragma solidity 0.8.26;

import {Script, console2}  from "forge-std/Script.sol";
import {Marketplace}       from "../src/Marketplace.sol";
import {AuctionHouse}      from "../src/AuctionHouse.sol";
import {OfferBook}         from "../src/OfferBook.sol";
import {RoyaltyRegistry}   from "../src/RoyaltyRegistry.sol";
import {TreasuryVault}     from "../src/TreasuryVault.sol";

/// @notice Flare mainnet (chainId 14) deploy. Hard-asserts chain ID so ops cannot
///         accidentally run against a testnet RPC.
contract DeployFlare is Script {
    uint256 constant FLARE_MAINNET_CHAINID = 14;

    function run() external {
        require(block.chainid == FLARE_MAINNET_CHAINID, "WRONG_CHAIN_NOT_FLARE_MAINNET");

        uint256 pk       = vm.envUint("PRIVATE_KEY");
        address creator  = vm.envAddress("CREATOR_ADDR");
        uint16  fee      = uint16(vm.envOr("FEE_BPS", uint256(150)));
        bool    useVault = vm.envOr("USE_VAULT", false);

        require(creator != address(0), "CREATOR_ADDR=0");
        require(fee <= 1_000,          "FEE_BPS_OVER_CAP");

        vm.startBroadcast(pk);

        RoyaltyRegistry registry = new RoyaltyRegistry(creator);
        TreasuryVault   vault    = new TreasuryVault(creator);

        address feeVault = useVault ? address(vault) : creator;

        Marketplace  marketplace = new Marketplace (feeVault, fee, creator);
        AuctionHouse auction     = new AuctionHouse(feeVault, fee, creator);
        OfferBook    offerBook   = new OfferBook   (feeVault, fee, creator);

        marketplace.setRoyaltyRegistry(address(registry));
        auction.setRoyaltyRegistry(address(registry));
        offerBook.setRoyaltyRegistry(address(registry));

        vm.stopBroadcast();

        console2.log("# Magic Webb Flare mainnet deploy");
        console2.log("# --- paste into backend/.env ---");
        console2.log("CHAIN_ID=",            block.chainid);
        console2.log("MARKETPLACE_ADDR=",    address(marketplace));
        console2.log("AUCTION_ADDR=",        address(auction));
        console2.log("OFFERBOOK_ADDR=",      address(offerBook));
        console2.log("ROYALTY_ADDR=",        address(registry));
        console2.log("# --- paste into frontend/.env.local ---");
        console2.log("NEXT_PUBLIC_CHAIN_ID=",             block.chainid);
        console2.log("NEXT_PUBLIC_MARKETPLACE_ADDR=",     address(marketplace));
        console2.log("NEXT_PUBLIC_AUCTION_ADDR=",         address(auction));
        console2.log("NEXT_PUBLIC_OFFER_ADDR=",           address(offerBook));
        console2.log("NEXT_PUBLIC_ROYALTY_ADDR=",         address(registry));
        console2.log("# --- informational ---");
        console2.log("CREATOR_ADDR=",        creator);
        console2.log("FEE_BPS=",             uint256(fee));
        console2.log("USE_TREASURY_VAULT=",  useVault);
        console2.log("TREASURY_VAULT_ADDR=", address(vault));
    }
}