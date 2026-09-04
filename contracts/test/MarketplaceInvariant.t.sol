// SPDX-License-Identifier: MIT
pragma solidity 0.8.26;

import {Test} from "forge-std/Test.sol";
import {Marketplace} from "../src/Marketplace.sol";
import {MockERC721} from "./MockERC721.sol";
import {TokenStandard} from "../src/MarketplaceCore.sol";
import {TestHelpers} from "./TestHelpers.sol";

/// @notice Fuzz driver for the Marketplace invariants.
/// @dev Every actor is an EOA (`owner`, `buyer`, `feeRecipient`) so the
///      50k-gas push payments in MarketplaceCore always succeed — which is
///      exactly why `pendingReturns` must stay empty and the contract balance
///      must stay at zero. If either ever moves, the fee/proceeds split leaked.
contract MarketplaceHandler is Test {
    Marketplace public mp;
    MockERC721 public nft;
    address public owner = address(0xA0);
    address public buyer = address(0xB0B);
    address public feeRecipient;
    address public keeper;

    uint256 public constant N_TOKENS = 5;
    uint256[N_TOKENS] public tokenIds;

    /// @notice Gross ETH paid by buyers across every settled sale.
    uint256 public ghostGross;
    /// @notice Platform leg of the 2% fee across every sale: per sale
    ///         `fee - fee*50/200` (exactly MarketplaceCore._payFee's truncation).
    uint256 public ghostPlatform;
    /// @notice Keeper leg of the 2% fee across every sale: per sale `fee*50/200`.
    uint256 public ghostKeeper;
    /// @notice block.timestamp at which each token's current listing was written.
    mapping(uint256 => uint64) public ghostListedAt;

    constructor(Marketplace _mp, MockERC721 _nft, address _feeRecipient, address _keeper) {
        mp = _mp;
        nft = _nft;
        feeRecipient = _feeRecipient;
        keeper = _keeper;
        vm.startPrank(owner);
        for (uint256 i; i < N_TOKENS; ++i) {
            tokenIds[i] = nft.mint(owner);
        }
        nft.setApprovalForAll(address(mp), true);
        vm.stopPrank();
    }

    /// @dev The fourteen durations MarketplaceCore._expiryFor accepts.
    function _durations() internal pure returns (uint64[14] memory d) {
        d = [
            uint64(1 minutes), uint64(3 minutes), uint64(5 minutes),
            uint64(15 minutes), uint64(30 minutes),
            uint64(45 minutes), uint64(1 hours), uint64(2 hours),
            uint64(4 hours), uint64(8 hours), uint64(12 hours),
            uint64(16 hours), uint64(20 hours), uint64(24 hours)
        ];
    }

    // ── Actions ───────────────────────────────────────────────────────────

    function list(uint256 tSeed, uint128 price, uint256 dSeed) external {
        uint256 tid = tokenIds[tSeed % N_TOKENS];
        uint128 p = uint128(bound(price, mp.MIN_PRICE(), 100 ether));
        uint64 dur = _durations()[dSeed % 14];
        vm.prank(owner);
        try mp.list(address(nft), tid, p, dur) {
            ghostListedAt[tid] = uint64(block.timestamp);
        } catch {}
    }

    function batchList(uint256 tSeed, uint128 price, uint256 dSeed) external {
        uint128 p = uint128(bound(price, mp.MIN_PRICE(), 100 ether));
        uint64 dur = _durations()[dSeed % 14];
        Marketplace.BatchItem[] memory items = new Marketplace.BatchItem[](3);
        uint256[3] memory ids;
        uint256 base = tSeed % N_TOKENS; // reduce first: tSeed + i can overflow
        for (uint256 i; i < 3; ++i) {
            ids[i] = tokenIds[(base + i) % N_TOKENS];
            items[i] = Marketplace.BatchItem({coll: address(nft), id: ids[i], price: p, duration: dur});
        }
        vm.prank(owner);
        try mp.batchList(items) {
            // batchList is all-or-nothing, so on success every item landed.
            for (uint256 i; i < 3; ++i) {
                ghostListedAt[ids[i]] = uint64(block.timestamp);
            }
        } catch {}
    }

    function editPrice(uint256 tSeed, uint128 price) external {
        uint256 tid = tokenIds[tSeed % N_TOKENS];
        uint128 p = uint128(bound(price, mp.MIN_PRICE(), 100 ether));
        vm.prank(owner);
        // editPrice must NOT move expiresAt, so ghostListedAt is deliberately
        // left alone — the expiry-window invariant would catch a refresh.
        try mp.editPrice(address(nft), tid, p) {} catch {}
    }

    function buy(uint256 tSeed) external {
        uint256 tid = tokenIds[tSeed % N_TOKENS];
        (address seller,,, uint128 price,) = mp.listings(address(nft), tid, owner);
        if (seller == address(0)) return;
        vm.deal(buyer, uint256(price));
        vm.prank(buyer);
        try mp.buy{value: uint256(price)}(address(nft), tid, owner) {
            ghostGross += uint256(price);
            uint256 fee = (uint256(price) * mp.PLATFORM_FEE_BPS()) / 10_000;
            uint256 kCut = (fee * mp.KEEPER_SHARE_BPS()) / mp.PLATFORM_FEE_BPS();
            ghostKeeper += kCut;
            ghostPlatform += fee - kCut;
            ghostListedAt[tid] = 0;
            // Harness recycling: hand the NFT back so the listing paths stay
            // reachable for the rest of the run instead of draining after five
            // buys. ETH-neutral — the buyer was dealt exactly `price` and spent
            // all of it, so balance accounting is untouched.
            vm.prank(buyer);
            nft.transferFrom(buyer, owner, tid);
        } catch {}
    }

    function cancel(uint256 tSeed) external {
        uint256 tid = tokenIds[tSeed % N_TOKENS];
        vm.prank(owner);
        try mp.cancel(address(nft), tid) {
            ghostListedAt[tid] = 0;
        } catch {}
    }

    /// @dev Expiry is a first-class part of the listing lifecycle; without an
    ///      explicit warp the fuzzer never crosses `expiresAt` and the
    ///      expired-listing branches are dead.
    function warp(uint256 seed) external {
        vm.warp(block.timestamp + (seed % 6 hours) + 1);
    }

    /// @notice Keeper-gated (the manager is mandatory), so the handler acts as
    ///         the manager's keeper to keep the expired-cleanup branch reachable.
    function cleanExpired(uint256 tSeed) external {
        uint256 tid = tokenIds[tSeed % N_TOKENS];
        vm.prank(keeper);
        try mp.cleanExpired(address(nft), tid, owner) {
            ghostListedAt[tid] = 0;
        } catch {}
    }
}

contract MarketplaceInvariantTest is Test, TestHelpers {
    Marketplace mp;
    MockERC721 nft;
    MarketplaceHandler handler;
    address feeRecipient = address(0xFEE);

    function setUp() public {
        mp = _deployMarketplace(feeRecipient, address(_deployMarketplaceManager()));
        nft = new MockERC721();
        handler = new MarketplaceHandler(mp, nft, feeRecipient, TEST_SENTINEL_KEEPER);
        targetContract(address(handler));
    }

    /// @notice Every listing the fuzzer can reach is either absent or
    ///         well-formed: owned by the recorded seller, priced at or above
    ///         MIN_PRICE, single-unit ERC-721, and expiring strictly after the
    ///         block it was written in but no later than the longest shared
    ///         duration (24h) past it. Replaces the previous
    ///         `ghost == 0 || ghost == 1 ether` assertion, which could not fail
    ///         because those were the only two values ever assigned.
    function invariant_listingsWellFormed() public view {
        address seller_ = handler.owner();
        for (uint256 i; i < handler.N_TOKENS(); ++i) {
            uint256 tid = handler.tokenIds(i);
            (address s, uint64 expiresAt, TokenStandard std, uint128 price, uint128 amount) =
                mp.listings(address(nft), tid, seller_);

            if (s == address(0)) {
                // Deleted / never written: the whole record must be clear.
                assertEq(expiresAt, 0, "cleared listing kept an expiry");
                assertEq(price, 0, "cleared listing kept a price");
                assertEq(amount, 0, "cleared listing kept an amount");
                continue;
            }

            assertEq(s, seller_, "listing seller field diverged from its key");
            assertGe(uint256(price), mp.MIN_PRICE(), "live listing priced below MIN_PRICE");
            assertEq(amount, 1, "ERC-721 listing with amount != 1");
            assertTrue(std == TokenStandard.ERC721, "unexpected token standard");

            uint64 listedAt = handler.ghostListedAt(tid);
            assertGt(expiresAt, listedAt, "listing expires at or before it was written");
            assertLe(
                uint256(expiresAt),
                uint256(listedAt) + 24 hours,
                "listing outlives the longest shared duration"
            );
        }
    }

    /// @notice Listings are non-custodial: the Marketplace escrows nothing and
    ///         forwards the buyer's ETH within the same transaction. Any
    ///         residual balance means fee/proceeds arithmetic leaked wei, or a
    ///         push payment silently fell back to pendingReturns.
    function invariant_holdsNoEth() public view {
        assertEq(address(mp).balance, 0, "Marketplace must never hold ETH");
        assertEq(mp.pendingReturns(handler.owner()), 0, "seller push failed");
        assertEq(mp.pendingReturns(feeRecipient), 0, "fee push failed");
        assertEq(mp.pendingReturns(TEST_SENTINEL_KEEPER), 0, "keeper push failed");
        assertEq(mp.pendingReturns(handler.buyer()), 0, "buyer credited unexpectedly");
    }

    /// @notice Value conservation across every settled sale: the fee wallet
    ///         holds exactly the accrued 1.5% platform leg, the sentinel keeper
    ///         exactly the 0.5% keeper leg (each truncated per sale exactly as
    ///         _payFee does), and the seller exactly the remainder. All three
    ///         actors start at zero balance and are never dealt ETH, so these
    ///         are absolute, not relative, checks.
    function invariant_salesConserveValue() public view {
        uint256 gross = handler.ghostGross();
        uint256 platform = handler.ghostPlatform();
        uint256 keeperShare = handler.ghostKeeper();
        assertEq(feeRecipient.balance, platform, "fee recipient balance != accrued platform leg");
        assertEq(TEST_SENTINEL_KEEPER.balance, keeperShare, "keeper balance != accrued keeper leg");
        assertEq(handler.owner().balance, gross - platform - keeperShare, "seller balance != gross minus fees");
    }
}
