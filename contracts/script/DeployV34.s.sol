// SPDX-License-Identifier: MIT
pragma solidity 0.8.26;

import {Script, console2} from "forge-std/Script.sol";
import {ERC1967Proxy}      from "@openzeppelin/contracts/proxy/ERC1967/ERC1967Proxy.sol";
import {Marketplace}        from "../src/Marketplace.sol";
import {AuctionHouse}       from "../src/AuctionHouse.sol";
import {OfferBook}          from "../src/OfferBook.sol";
import {MarketplaceManager} from "../src/MarketplaceManager.sol";

/// @notice Unified v3.4 deploy for EVERY network — Coston2, Songbird, Flare.
///         One script; the only per-network differences are environment inputs.
///
/// v3.4 changes over v3.2:
///   - MarketplaceManager is a PLAIN, UNPROXIED contract (no impl+proxy pair,
///     no initializer). 7 CREATEs total instead of 8.
///   - feeRecipient + manager are IMMUTABLES baked into each core
///     implementation via constructor args; the proxies initialize with NO
///     arguments. Changing either later = new impl via queueUpgrade+upgradeTo.
///   - Upgrades are INSTANT on every network (upgradeDelay()==0, owner
///     directive 2026-09-02): queueUpgrade and upgradeTo run back-to-back.
///     Custody of ADMIN_ADDR's key is the entire upgrade security model
///     until renounceAdmin().
///
/// v3.2 authority model retained:
///   - Exactly ONE keeper per network (MarketplaceManager.keeper); no grants.
///   - Exactly ONE admin until `renounceAdmin()` — which is now a LATER,
///     explicit per-network action on the owner's go-immutable order, NOT a
///     deploy-time default for mainnets.
///   - SEAL=true remains available for a deliberate sealed-from-block-one
///     deployment (renounceAdmin as the script's last action).
///
/// Required env vars:
///   PRIVATE_KEY         -- deployer key (never commit). Holds no power after
///                          this script exits: admin is ADMIN_ADDR (unsealed)
///                          or nobody (sealed).
///   ADMIN_ADDR          -- the single admin (unsealed networks only). With
///                          SEAL=true it is ignored: the deployer becomes the
///                          momentary admin purely so it can renounce in the
///                          same broadcast, and no admin survives the script.
///   FEE_RECIPIENT_ADDR  -- fee destination baked into all three core
///                          implementations as an immutable. On sealed
///                          networks this can NEVER change. MUST be a
///                          contract (Safe) when SEAL=true.
///   KEEPER_ADDR         -- the single keeper. Its private key should exist
///                          ONLY as the network's Fly secret.
///   SEAL                -- "true" to renounce admin at the end. Default
///                          false: every network deploys admin-held and
///                          instantly upgradeable.
///
/// Keeper-loss consequence on a sealed network (accepted by owner): automation
/// (instant settle, refund sweeps, expired-listing cleanup) dies with the key;
/// funds never trap — settle stays available to seller/winner, refundLosers
/// runs post-settle, withdrawLoserFunds/withdrawRefund are self-service, and
/// forceCancel (keeper/seller/winner, endsAt+3d) rescues a stuck auction.
contract DeployV34 is Script {
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
            // constructs with the deployer as the momentary admin and
            // renounces before the broadcast ends. ADMIN_ADDR is ignored.
            admin_ = deployer;
            // On sealed networks the fee destination is forever: insist on a
            // contract (Safe), never an EOA.
            require(feeRcpt.code.length > 0, "SEAL=true requires FEE_RECIPIENT_ADDR to be a Safe/contract");
        } else {
            admin_ = vm.envAddress("ADMIN_ADDR");
            require(admin_ != address(0), "ADMIN_ADDR required");
            // Two-address authority: the key that settles must never be the
            // key that upgrades (a hot Fly secret must not hold root).
            require(admin_ != keeper_, "ADMIN_ADDR must differ from KEEPER_ADDR");
        }

        vm.startBroadcast(pk);

        // ── MarketplaceManager — PLAIN contract, deliberately unproxied ────
        // Two addresses and a shim; nothing worth an upgrade surface. Cores
        // bake its address as an immutable; replacing it later = new core
        // impls via the admin-gated upgrade path.
        MarketplaceManager manager = new MarketplaceManager(admin_, keeper_);

        // ── Cores: impl bakes (feeRecipient, manager); proxy inits no-arg ──
        Marketplace marketplaceImpl = new Marketplace(feeRcpt, address(manager));
        ERC1967Proxy marketplaceProxy = new ERC1967Proxy(
            address(marketplaceImpl),
            abi.encodeWithSelector(Marketplace.initialize.selector)
        );
        Marketplace marketplace = Marketplace(address(marketplaceProxy));

        AuctionHouse auctionImpl = new AuctionHouse(feeRcpt, address(manager));
        ERC1967Proxy auctionProxy = new ERC1967Proxy(
            address(auctionImpl),
            abi.encodeWithSelector(AuctionHouse.initialize.selector)
        );
        AuctionHouse auction = AuctionHouse(address(auctionProxy));

        OfferBook offerBookImpl = new OfferBook(feeRcpt, address(manager));
        ERC1967Proxy offerBookProxy = new ERC1967Proxy(
            address(offerBookImpl),
            abi.encodeWithSelector(OfferBook.initialize.selector)
        );
        OfferBook offerBook = OfferBook(address(offerBookProxy));

        if (seal) {
            manager.renounceAdmin();
        }

        vm.stopBroadcast();

        console2.log("# Magic Webb v3.4 deploy -- record in deployments/<network>.json");
        console2.log("CHAIN_ID=",         block.chainid);
        console2.log("SEALED=",           seal);
        console2.log("MANAGER_ADDR=",     address(manager));
        console2.log("  (manager is UNPROXIED plain bytecode -- there is no separate manager impl address)");
        console2.log("MARKETPLACE_ADDR=", address(marketplace));
        console2.log("AUCTION_ADDR=",     address(auction));
        console2.log("OFFERBOOK_ADDR=",   address(offerBook));
        console2.log("MARKETPLACE_IMPL=", address(marketplaceImpl));
        console2.log("AUCTION_IMPL=",     address(auctionImpl));
        console2.log("OFFERBOOK_IMPL=",   address(offerBookImpl));
        console2.log("KEEPER_ADDR=",      keeper_);
        console2.log("FEE_RECIPIENT=",    feeRcpt);
        console2.log("FEE=",              "1.5% (150 bps, hardcoded, seller-pays on sale)");
        console2.log("UPGRADE_DELAY=",    "0 on every chain (instant; queue+upgrade back-to-back)");

        // Sanity: every core must report the same fee recipient and manager —
        // read THROUGH the proxies, which also proves the immutables resolve
        // correctly across the delegatecall (immutables live in impl code).
        require(marketplace.feeRecipient() == feeRcpt, "MARKETPLACE feeRecipient mismatch");
        require(auction.feeRecipient()     == feeRcpt, "AUCTION feeRecipient mismatch");
        require(offerBook.feeRecipient()   == feeRcpt, "OFFERBOOK feeRecipient mismatch");
        require(marketplace.manager() == address(manager), "MARKETPLACE manager mismatch");
        require(auction.manager()     == address(manager), "AUCTION manager mismatch");
        require(offerBook.manager()   == address(manager), "OFFERBOOK manager mismatch");
        // Authority invariants.
        require(address(manager).code.length > 0, "manager has no code");
        require(manager.keeper() == keeper_, "keeper mismatch");
        require(manager.hasRole(manager.KEEPER_ROLE(), keeper_), "keeper shim answers false");
        require(marketplace.upgradeDelay() == 0, "upgrade delay must be 0 (instant upgrades)");
        // Nothing may be queued at birth, and no admin hand-off in flight.
        require(marketplace.pendingImplementation() == address(0), "MARKETPLACE has a queued upgrade");
        require(auction.pendingImplementation()     == address(0), "AUCTION has a queued upgrade");
        require(offerBook.pendingImplementation()   == address(0), "OFFERBOOK has a queued upgrade");
        require(manager.pendingAdmin() == address(0), "manager has a pending admin transfer");
        // ERC-1967 implementation slot on each proxy must point at the impl
        // this script just created -- proves the proxies were wired to fresh
        // code, not something pre-existing at a colliding address.
        require(_impl(address(marketplaceProxy)) == address(marketplaceImpl), "MARKETPLACE impl slot mismatch");
        require(_impl(address(auctionProxy))     == address(auctionImpl),     "AUCTION impl slot mismatch");
        require(_impl(address(offerBookProxy))   == address(offerBookImpl),   "OFFERBOOK impl slot mismatch");
        if (seal) {
            require(manager.admin() == address(0), "sealed deploy must end with no admin");
            require(!manager.hasRole(manager.DEFAULT_ADMIN_ROLE(), deployer), "no admin may survive a sealed deploy");
            // _requireAdmin on every core consults exactly this hasRole probe,
            // so a false answer here proves queueUpgrade/upgradeTo/setKeeper
            // are dead on all four contracts (e2e additionally attempts
            // queueUpgrade and expects revert).
        } else {
            require(manager.admin() == admin_, "admin mismatch");
        }
        console2.log("feeRecipient + manager + single-keeper authority verified");
    }

    /// @dev ERC-1967 implementation slot:
    ///      bytes32(uint256(keccak256("eip1967.proxy.implementation")) - 1).
    bytes32 private constant _IMPL_SLOT =
        0x360894a13ba1a3210667c828492db98dca3e2076cc3735a920a3ca505d382bbc;

    function _impl(address proxy) internal view returns (address) {
        return address(uint160(uint256(vm.load(proxy, _IMPL_SLOT))));
    }
}
