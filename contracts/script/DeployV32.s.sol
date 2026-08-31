// SPDX-License-Identifier: MIT
pragma solidity 0.8.26;

import {Script, console2} from "forge-std/Script.sol";
import {ERC1967Proxy}      from "@openzeppelin/contracts/proxy/ERC1967/ERC1967Proxy.sol";
import {Marketplace}        from "../src/Marketplace.sol";
import {AuctionHouse}       from "../src/AuctionHouse.sol";
import {OfferBook}          from "../src/OfferBook.sol";
import {MarketplaceManager} from "../src/MarketplaceManager.sol";

/// @notice Unified v3.2 deploy for EVERY network — Coston2, Songbird, Flare.
///         One script; the only per-network differences are environment inputs.
///         Replaces DeployCoston2 / DeploySongbird / DeployFlareMainnet, whose
///         bodies differed only in chain id and log strings.
///
/// v3.2 authority model (owner decision 2026-08-31):
///   - Exactly ONE keeper per network, held in MarketplaceManager.keeper.
///     There is no addKeeper, no role granting, no fleet: nothing anywhere can
///     enroll a second settlement-authorized address.
///   - Exactly ONE admin (MarketplaceManager.admin) — the upgrade + keeper
///     rotation authority — until `renounceAdmin()` fires. After that the
///     deployment is sealed: no upgrades, no keeper changes, forever.
///   - SEAL=false (Coston2/dev): admin retained for iteration.
///     SEAL=true  (mainnets): renounceAdmin() is the script's LAST action —
///     the network is immutable from block one.
///
/// Required env vars:
///   PRIVATE_KEY         -- deployer key (never commit). Holds no power after
///                          this script exits: admin is ADMIN_ADDR (unsealed)
///                          or nobody (sealed).
///   ADMIN_ADDR          -- the single admin (unsealed networks only). With
///                          SEAL=true it is ignored: the deployer becomes the
///                          momentary admin purely so it can renounce in the
///                          same broadcast, and no admin survives the script.
///   FEE_RECIPIENT_ADDR  -- immutable-in-practice fee destination baked into
///                          all three cores. On sealed networks this can NEVER
///                          change. MUST be a contract (Safe) when SEAL=true.
///   KEEPER_ADDR         -- the single keeper. Its private key should exist
///                          ONLY as the network's Fly secret.
///   SEAL                -- "true" to renounce admin at the end (mainnets).
///
/// Keeper-loss consequence on a sealed network (accepted by owner): automation
/// (instant settle, refund sweeps, expired-listing cleanup) dies with the key;
/// funds never trap — settle stays available to seller/winner, refundLosers
/// runs post-settle, withdrawLoserFunds/withdrawRefund are self-service, and
/// forceCancel (keeper/seller/winner, endsAt+3d) rescues a stuck auction.
contract DeployV32 is Script {
    function run() external {
        uint256 pk       = vm.envUint("PRIVATE_KEY");
        address feeRcpt  = vm.envAddress("FEE_RECIPIENT_ADDR");
        address keeper_  = vm.envAddress("KEEPER_ADDR");
        bool    seal     = vm.envOr("SEAL", false);

        require(feeRcpt != address(0), "FEE_RECIPIENT_ADDR required");
        require(keeper_ != address(0), "KEEPER_ADDR required");

        address deployer = vm.addr(pk);

        address admin_;
        if (seal) {
            // renounceAdmin() below is sent by the deployer, and onlyAdmin
            // requires the caller to BE the admin — so a sealed deploy
            // initializes with the deployer as the momentary admin and
            // renounces before the broadcast ends. ADMIN_ADDR is ignored.
            admin_ = deployer;
            // On sealed (mainnet) networks the fee destination is forever:
            // insist on a contract (Safe), never an EOA.
            require(feeRcpt.code.length > 0, "SEAL=true requires FEE_RECIPIENT_ADDR to be a Safe/contract");
        } else {
            admin_ = vm.envAddress("ADMIN_ADDR");
            require(admin_ != address(0), "ADMIN_ADDR required");
        }

        vm.startBroadcast(pk);

        // ── MarketplaceManager (proxy) ──────────────────────────────────────
        MarketplaceManager managerImpl = new MarketplaceManager();
        ERC1967Proxy managerProxy = new ERC1967Proxy(
            address(managerImpl),
            abi.encodeWithSelector(MarketplaceManager.initialize.selector, admin_, keeper_)
        );
        MarketplaceManager manager = MarketplaceManager(address(managerProxy));

        // ── Marketplace (proxy) ─────────────────────────────────────────────
        Marketplace marketplaceImpl = new Marketplace();
        ERC1967Proxy marketplaceProxy = new ERC1967Proxy(
            address(marketplaceImpl),
            abi.encodeWithSelector(Marketplace.initialize.selector, feeRcpt, address(manager))
        );
        Marketplace marketplace = Marketplace(address(marketplaceProxy));

        // ── AuctionHouse (proxy) ────────────────────────────────────────────
        AuctionHouse auctionImpl = new AuctionHouse();
        ERC1967Proxy auctionProxy = new ERC1967Proxy(
            address(auctionImpl),
            abi.encodeWithSelector(AuctionHouse.initialize.selector, feeRcpt, address(manager))
        );
        AuctionHouse auction = AuctionHouse(address(auctionProxy));

        // ── OfferBook (proxy) ───────────────────────────────────────────────
        OfferBook offerBookImpl = new OfferBook();
        ERC1967Proxy offerBookProxy = new ERC1967Proxy(
            address(offerBookImpl),
            abi.encodeWithSelector(OfferBook.initialize.selector, feeRcpt, address(manager))
        );
        OfferBook offerBook = OfferBook(address(offerBookProxy));

        if (seal) {
            manager.renounceAdmin();
        }

        vm.stopBroadcast();

        console2.log("# Magic Webb v3.2 deploy -- record in deployments/<network>.json");
        console2.log("CHAIN_ID=",         block.chainid);
        console2.log("SEALED=",           seal);
        console2.log("MANAGER_ADDR=",     address(manager));
        console2.log("MARKETPLACE_ADDR=", address(marketplace));
        console2.log("AUCTION_ADDR=",     address(auction));
        console2.log("OFFERBOOK_ADDR=",   address(offerBook));
        console2.log("KEEPER_ADDR=",      keeper_);
        console2.log("FEE_RECIPIENT=",    feeRcpt);
        console2.log("FEE=",              "1.5% (150 bps, hardcoded, seller-pays on sale)");

        // Sanity: every contract must report the same fee recipient and manager.
        require(marketplace.feeRecipient() == feeRcpt, "MARKETPLACE feeRecipient mismatch");
        require(auction.feeRecipient()     == feeRcpt, "AUCTION feeRecipient mismatch");
        require(offerBook.feeRecipient()   == feeRcpt, "OFFERBOOK feeRecipient mismatch");
        require(marketplace.manager() == address(manager), "MARKETPLACE manager mismatch");
        require(auction.manager()     == address(manager), "AUCTION manager mismatch");
        require(offerBook.manager()   == address(manager), "OFFERBOOK manager mismatch");
        // v3.2 authority invariants.
        require(manager.keeper() == keeper_, "keeper mismatch");
        require(manager.hasRole(manager.KEEPER_ROLE(), keeper_), "keeper shim answers false");
        if (seal) {
            require(manager.admin() == address(0), "sealed deploy must end with no admin");
            require(!manager.hasRole(manager.DEFAULT_ADMIN_ROLE(), deployer), "no admin may survive a sealed deploy");
        } else {
            require(manager.admin() == admin_, "admin mismatch");
        }
        console2.log("feeRecipient + manager + single-keeper authority verified");
    }
}
