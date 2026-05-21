// SPDX-License-Identifier: MIT
pragma solidity 0.8.26;

import {Script, console2} from "forge-std/Script.sol";
import {Marketplace}    from "../src/Marketplace.sol";
import {AuctionHouse}   from "../src/AuctionHouse.sol";
import {OfferBook}      from "../src/OfferBook.sol";
import {RoyaltyRegistry} from "../src/RoyaltyRegistry.sol";
import {TreasuryVault}  from "../src/TreasuryVault.sol";

/// @notice Deploy all 5 WebbPlace contracts to Flare Coston2 (chain 114) or Flare mainnet (chain 14).
///
/// Required env vars:
///   PRIVATE_KEY   — deployer private key (never commit)
///   CREATOR_ADDR  — admin / fee recipient (Safe multi-sig on mainnet, EOA on testnet)
///
/// Optional env vars:
///   FEE_BPS       — platform fee in basis points (default 150 = 1.5%)
///   USE_VAULT     — set to "true" to route fees to deployed TreasuryVault instead of CREATOR_ADDR
///
/// Output: copy the printed env vars into frontend/.env.local and backend/.env
contract DeployCoston2 is Script {
    function run() external {
        require(block.chainid == 114, "WRONG_CHAIN: use DeployFlare.s.sol for mainnet");
        uint256 pk      = vm.envUint("PRIVATE_KEY");
        address creator = vm.envAddress("CREATOR_ADDR");
        uint16  fee     = uint16(vm.envOr("FEE_BPS", uint256(150)));
        bool    useVault = vm.envOr("USE_VAULT", false);

        require(creator != address(0), "CREATOR_ADDR required");
        require(fee <= 1_000,          "FEE_BPS must be <= 1000 (10%)");

        vm.startBroadcast(pk);

        // 1. RoyaltyRegistry — admin = creator (Safe multi-sig on mainnet)
        RoyaltyRegistry registry = new RoyaltyRegistry(creator);

        // 2. TreasuryVault — accumulates platform fees (optional)
        TreasuryVault vault = new TreasuryVault(creator);

        // Fee destination: vault contract or direct EOA
        address feeVault = useVault ? address(vault) : creator;

        // 3–5. Core market contracts — same admin and fee destination
        Marketplace  marketplace = new Marketplace (feeVault, fee, creator);
        AuctionHouse auction     = new AuctionHouse(feeVault, fee, creator);
        OfferBook    offerBook   = new OfferBook   (feeVault, fee, creator);

        // Wire registry into market contracts so royalties are applied on settlement
        marketplace.setRoyaltyRegistry(address(registry));
        auction.setRoyaltyRegistry(address(registry));
        offerBook.setRoyaltyRegistry(address(registry));

        vm.stopBroadcast();

        // ── Print env block ───────────────────────────────────────────────
        console2.log("# WebbPlace deploy output - paste into .env.local / backend/.env");
        console2.log("CHAIN_ID=",                block.chainid);
        console2.log("CREATOR_ADDR=",            creator);
        console2.log("FEE_BPS=",                 uint256(fee));
        console2.log("USE_TREASURY_VAULT=",       useVault);
        console2.log("NEXT_PUBLIC_ROYALTY_ADDR=", address(registry));
        console2.log("TREASURY_VAULT_ADDR=",      address(vault));
        console2.log("NEXT_PUBLIC_MARKETPLACE_ADDR=", address(marketplace));
        console2.log("NEXT_PUBLIC_AUCTION_ADDR=",     address(auction));
        console2.log("NEXT_PUBLIC_OFFERBOOK_ADDR=",   address(offerBook));
    }
}
