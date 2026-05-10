// SPDX-License-Identifier: MIT
pragma solidity ^0.8.24;

import {AccessControl} from "@openzeppelin/contracts/access/AccessControl.sol";

error VaultWithdrawFailed();
error ZeroAddress();

/// @title FeeVault
/// @notice OPTIONAL fee-collection contract. NOT deployed by default in `DeployCoston2.s.sol` —
///         the production Coston2 deploy points `feeVault` directly at the creator EOA to save gas.
/// @dev Kept on disk for ops who want a smart-contract sink. Switch via `MarketplaceCore.setFeeVault`.
contract FeeVault is AccessControl {
    bytes32 public constant WITHDRAW_ROLE = keccak256("WITHDRAW_ROLE");

    event Received(address indexed from, uint256 amount);
    event Withdrawn(address indexed to, uint256 amount);

    constructor(address admin) {
        if (admin == address(0)) revert ZeroAddress();
        _grantRole(DEFAULT_ADMIN_ROLE, admin);
        _grantRole(WITHDRAW_ROLE, admin);
    }

    /// @notice Accept fee payments.
    receive() external payable { emit Received(msg.sender, msg.value); }

    /// @notice Withdraw collected fees to an arbitrary address. Withdrawer-role only.
    function withdraw(address to, uint256 amount) external onlyRole(WITHDRAW_ROLE) {
        if (to == address(0)) revert ZeroAddress();
        (bool ok,) = to.call{value: amount}("");
        if (!ok) revert VaultWithdrawFailed();
        emit Withdrawn(to, amount);
    }
}
