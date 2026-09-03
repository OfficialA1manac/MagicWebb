// SPDX-License-Identifier: MIT
pragma solidity 0.8.26;

/// @dev A function marked nonReentrant was re-entered.
error ReentrantCall();

/// @title TransientReentrancyGuard (v3.4)
/// @notice Reentrancy guard on EIP-1153 transient storage (TSTORE/TLOAD)
///         instead of a persistent storage slot — saves ~2,000 gas per
///         guarded external call versus OZ's ReentrancyGuardUpgradeable
///         (no warm SSTORE 1→2→1 round-trip; transient writes cost 100).
///
/// Semantics are identical to the OZ guard it replaces:
///   - ONE flag per proxy address, shared by every `nonReentrant` function
///     in the contract — cross-function reentrancy (e.g. buy() re-entering
///     withdrawRefund()) is blocked exactly as before.
///   - The flag is cleared on exit (and transient storage additionally
///     clears at transaction end), so sequential calls inside one
///     transaction work — e.g. a router calling buy() twice.
///
/// Deployment gate: TSTORE/TLOAD are Cancun opcodes. This guard ships ONLY
/// after every target chain (Coston2 114, Songbird 19, Flare 14) passes the
/// runtime probe in docs/UPGRADE_RUNBOOK.md — an eth_call probe AND a real
/// transaction executing tstore/tload. If any chain fails, fall back to OZ
/// ReentrancyGuardUpgradeable with three edits in MarketplaceCore.sol: the
/// import (line 14), the inheritance list (line 68), and re-adding
/// `__ReentrancyGuard_init()` in `__MarketplaceCore_init` (line 150) — one
/// bytecode policy across all networks, never mixed guards.
///
/// No storage, no initializer, no gap: transient storage does not touch the
/// proxy's persistent layout.
abstract contract TransientReentrancyGuard {
    /// @dev keccak256("mw.v34.reentrancy.guard") - 1. The -1 breaks any
    ///      relationship to a computable keccak preimage, mirroring the
    ///      ERC-1967 slot convention.
    bytes32 private constant _GUARD_SLOT =
        0xc101b9e06bbee73467394c30687a48178f127c15818c90a746b3618cb79d629a;

    modifier nonReentrant() {
        assembly ("memory-safe") {
            if tload(_GUARD_SLOT) {
                // revert ReentrantCall() — selector 0x37ed32e8
                mstore(0x00, 0x37ed32e800000000000000000000000000000000000000000000000000000000)
                revert(0x00, 0x04)
            }
            tstore(_GUARD_SLOT, 1)
        }
        _;
        assembly ("memory-safe") {
            tstore(_GUARD_SLOT, 0)
        }
    }
}
