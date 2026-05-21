// SPDX-License-Identifier: MIT
pragma solidity 0.8.26;

import {AccessControl} from "@openzeppelin/contracts/access/AccessControl.sol";

error WithdrawFailed();
error ZeroAmount();
error ZeroAddress();

/// @title TreasuryVault
/// @notice Pull-payment accumulator for Magic Webb platform fees.
/// @dev Receives ETH pushed by MarketplaceCore._splitAndPay; authorised withdrawers pull to any address.
///      Intentionally no automatic distribution — fees accumulate and the DAO/admin decides allocation.
contract TreasuryVault is AccessControl {
    bytes32 public constant WITHDRAWER_ROLE = keccak256("WITHDRAWER_ROLE");

    event Received(address indexed from, uint256 amount, uint256 balance);
    event Withdrawn(address indexed by, address indexed to, uint256 amount, uint256 balance);

    constructor(address admin) {
        if (admin == address(0)) revert ZeroAddress();
        _grantRole(DEFAULT_ADMIN_ROLE, admin);
    }

    receive() external payable {
        emit Received(msg.sender, msg.value, address(this).balance);
    }

    /// @notice Withdraw `amount` wei to `to`. Only WITHDRAWER_ROLE.
    function withdraw(address to, uint256 amount) external onlyRole(WITHDRAWER_ROLE) {
        if (to == address(0)) revert ZeroAddress();
        if (amount == 0) revert ZeroAmount();
        (bool ok,) = to.call{value: amount}("");
        if (!ok) revert WithdrawFailed();
        emit Withdrawn(msg.sender, to, amount, address(this).balance);
    }

    /// @notice Withdraw entire balance to `to`. Only WITHDRAWER_ROLE.
    function withdrawAll(address to) external onlyRole(WITHDRAWER_ROLE) {
        if (to == address(0)) revert ZeroAddress();
        uint256 bal = address(this).balance;
        if (bal == 0) revert ZeroAmount();
        (bool ok,) = to.call{value: bal}("");
        if (!ok) revert WithdrawFailed();
        emit Withdrawn(msg.sender, to, bal, 0);
    }
}
