// SPDX-License-Identifier: MIT
pragma solidity ^0.8.24;

import {ReentrancyGuard} from "@openzeppelin/contracts/utils/ReentrancyGuard.sol";
import {AccessControl} from "@openzeppelin/contracts/access/AccessControl.sol";
import {Pausable} from "@openzeppelin/contracts/utils/Pausable.sol";
import {IERC721} from "@openzeppelin/contracts/token/ERC721/IERC721.sol";

error TransferFailed();
error WithdrawFailed();
error InvalidFee();
error ZeroAddress();

/// @title MarketplaceCore
/// @notice Shared base for trade contracts: fee config, fee routing, NFT transfer, role + pause guards.
/// @dev `feeVault` is set to the creator EOA in the production Coston2 deploy to skip a CALL hop per trade.
abstract contract MarketplaceCore is ReentrancyGuard, AccessControl, Pausable {
    /// @notice Hard cap on platform fee (10%). Enforced in constructor and `setFeeBps`.
    uint16 public constant MAX_FEE_BPS = 1_000;

    bytes32 public constant FEE_ROLE    = keccak256("FEE_ROLE");
    bytes32 public constant PAUSER_ROLE = keccak256("PAUSER_ROLE");

    /// @notice Platform fee in basis points (1 bp = 0.01%). Cap = `MAX_FEE_BPS`.
    uint16  public feeBps;
    /// @notice Recipient of fee on every trade. EOA in prod, may be ops contract.
    address public feeVault;

    event FeeUpdated(uint16 oldBps, uint16 newBps);
    event FeeVaultUpdated(address oldVault, address newVault);

    constructor(address admin, address vault, uint16 fee) {
        if (admin == address(0) || vault == address(0)) revert ZeroAddress();
        if (fee > MAX_FEE_BPS) revert InvalidFee();
        _grantRole(DEFAULT_ADMIN_ROLE, admin);
        _grantRole(FEE_ROLE, admin);
        _grantRole(PAUSER_ROLE, admin);
        feeVault = vault;
        feeBps   = fee;
    }

    /// @notice Update fee bps. Reverts if `newBps > MAX_FEE_BPS`.
    function setFeeBps(uint16 newBps) external onlyRole(FEE_ROLE) {
        if (newBps > MAX_FEE_BPS) revert InvalidFee();
        emit FeeUpdated(feeBps, newBps);
        feeBps = newBps;
    }

    /// @notice Update fee recipient. Reverts on zero address.
    function setFeeVault(address newVault) external onlyRole(FEE_ROLE) {
        if (newVault == address(0)) revert ZeroAddress();
        emit FeeVaultUpdated(feeVault, newVault);
        feeVault = newVault;
    }

    /// @notice Pause all trade entry points.
    function pause() external onlyRole(PAUSER_ROLE) { _pause(); }
    /// @notice Unpause trade entry points.
    function unpause() external onlyRole(PAUSER_ROLE) { _unpause(); }

    /// @dev Splits `amount` into fee + seller cut and pushes both. Fee ≤ amount by construction.
    function _splitAndPay(address seller, uint256 amount) internal returns (uint256 fee, uint256 sellerAmt) {
        fee = (amount * feeBps) / 10_000;
        unchecked { sellerAmt = amount - fee; } // fee ≤ amount via /10_000
        if (fee > 0) {
            (bool ok,) = feeVault.call{value: fee}("");
            if (!ok) revert TransferFailed();
        }
        (bool ok2,) = seller.call{value: sellerAmt}("");
        if (!ok2) revert TransferFailed();
    }

    /// @dev ERC-721 safeTransfer wrapper. Reverts if `from` is not approved or no longer owns token.
    function _transferNFT(address coll, address from, address to, uint256 id) internal {
        IERC721(coll).safeTransferFrom(from, to, id);
    }
}
