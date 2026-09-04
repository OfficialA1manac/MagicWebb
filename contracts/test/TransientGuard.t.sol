// SPDX-License-Identifier: MIT
pragma solidity 0.8.26;

import {Test} from "forge-std/Test.sol";
import {TransientReentrancyGuard, ReentrantCall} from "../src/TransientReentrancyGuard.sol";
import {Marketplace} from "../src/Marketplace.sol";
import {WithdrawFailed} from "../src/MarketplaceCore.sol";
import {MockERC721} from "./MockERC721.sol";
import {TestHelpers} from "./TestHelpers.sol";

/// @dev Minimal harness: two guarded functions sharing ONE transient slot —
///      the exact shape MarketplaceCore inherits.
contract GuardHarness is TransientReentrancyGuard {
    address public hook;
    uint256 public entered;

    function setHook(address h) external { hook = h; }

    function f() external nonReentrant {
        entered++;
        if (hook != address(0)) {
            // Propagate the callee's revert data verbatim so the test can
            // assert ReentrantCall surfaces from the cross-call.
            (bool ok, bytes memory data) = hook.call(abi.encodeWithSignature("poke()"));
            if (!ok) {
                assembly { revert(add(data, 32), mload(data)) }
            }
        }
    }

    function g() external nonReentrant {
        entered++;
    }
}

contract ReenterG {
    GuardHarness immutable h;
    constructor(GuardHarness h_) { h = h_; }
    function poke() external { h.g(); }
}

contract ReenterF {
    GuardHarness immutable h;
    constructor(GuardHarness h_) { h = h_; }
    function poke() external { h.f(); }
}

/// @dev Seller contract that tries to re-enter buy() from its payout hook.
contract ReentrantSeller {
    Marketplace immutable mp;
    address public coll;
    uint256 public tokenId2;
    bool public reentered;      // inner call SUCCEEDED (guard failed) — must stay false
    bool public attempted;      // receive() ran and tried

    constructor(Marketplace mp_) { mp = mp_; }
    function arm(address coll_, uint256 second) external { coll = coll_; tokenId2 = second; }
    function list(address coll_, uint256 id) external {
        MockERC721(coll_).setApprovalForAll(address(mp), true);
        mp.list(coll_, id, 1 ether, uint64(24 hours));
    }
    receive() external payable {
        attempted = true;
        // Re-enter buy() for the second listing. Under the transient guard
        // this reverts ReentrantCall inside the payout's 50k-gas subcall --
        // cheaply (a TLOAD + revert), so receive() still finishes within
        // budget and the outer push SUCCEEDS.
        try mp.buy{value: 1 ether}(coll, tokenId2, address(this)) {
            reentered = true;
        } catch {}
    }
    function onERC721Received(address, address, uint256, bytes calldata) external pure returns (bytes4) {
        return this.onERC721Received.selector;
    }
}

contract TransientGuardTest is Test, TestHelpers {
    GuardHarness h;

    function setUp() public {
        h = new GuardHarness();
    }

    function test_directReentrancy_blocked() public {
        h.setHook(address(new ReenterF(h)));
        vm.expectRevert(ReentrantCall.selector);
        h.f();
    }

    function test_crossFunctionReentrancy_blocked() public {
        // ONE slot per contract: f() re-entering g() must be blocked exactly
        // like f()→f(). This is the property the marketplace relies on
        // (buy() must not be able to re-enter withdrawRefund()).
        h.setHook(address(new ReenterG(h)));
        vm.expectRevert(ReentrantCall.selector);
        h.f();
    }

    function test_sequentialCallsInOneTx_succeed() public {
        // The epilogue tstore(slot, 0) clears the flag on exit — two guarded
        // calls in the same transaction must both run (e.g. a router calling
        // buy() twice).
        h.f();
        h.f();
        h.g();
        assertEq(h.entered(), 3);
    }

    function test_marketplaceBuy_reentrancyContained() public {
        // End-to-end: a seller whose payout hook re-enters buy() for their
        // second listing. The guard makes the inner buy revert CHEAPLY (one
        // TLOAD then revert, well inside the 50k push cap), so the outer
        // buy still completes AND its payout push succeeds -- the seller
        // receives proceeds directly, nothing lands in pendingReturns.
        Marketplace mp = _deployMarketplace(address(0xFEE), address(_deployMarketplaceManager()));
        MockERC721 nft = new MockERC721();
        ReentrantSeller seller = new ReentrantSeller(mp);

        // Give the seller two tokens, listed at 1 ether each.
        uint256 t1 = nft.mint(address(seller));
        uint256 t2 = nft.mint(address(seller));
        seller.list(address(nft), t1);
        seller.list(address(nft), t2);
        seller.arm(address(nft), t2);

        address buyer = address(0xB111);
        vm.deal(buyer, 10 ether);
        uint256 sellerBefore = address(seller).balance;
        vm.prank(buyer);
        mp.buy{value: 1 ether}(address(nft), t1, address(seller));

        assertEq(nft.ownerOf(t1), buyer, "outer buy completed");
        assertTrue(seller.attempted(), "seller hook ran");
        assertFalse(seller.reentered(), "inner re-entrant buy must NOT succeed");
        assertEq(nft.ownerOf(t2), address(seller), "second token untouched");
        // Outer payout pushed straight through: the guarded re-entry reverted
        // cheaply, so the 50k-capped push had budget to spare.
        uint256 proceeds = 1 ether - (1 ether * 200) / 10_000;
        assertEq(address(seller).balance - sellerBefore, proceeds, "seller paid directly (0.98 ether)");
        assertEq(mp.pendingReturns(address(seller)), 0, "nothing fell back to pull-withdrawal");
    }
}
