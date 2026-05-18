// SPDX-License-Identifier: MIT
pragma solidity 0.8.26;

import {Test}          from "forge-std/Test.sol";
import {TreasuryVault} from "../src/TreasuryVault.sol";

contract TreasuryVaultTest is Test {
    TreasuryVault vault;
    address admin     = address(this);
    address withdrawer = address(0xAB);
    address recipient  = address(0xCC);

    function setUp() public {
        vault = new TreasuryVault(admin);
        vault.grantRole(vault.WITHDRAWER_ROLE(), withdrawer);
        vm.deal(address(this), 10 ether);
    }

    function test_receiveAccumulatesFunds() public {
        (bool ok,) = address(vault).call{value: 1 ether}("");
        assertTrue(ok);
        assertEq(address(vault).balance, 1 ether);
    }

    function test_withdrawerCanWithdrawPartial() public {
        (bool ok,) = address(vault).call{value: 2 ether}("");
        assertTrue(ok);

        uint256 before_ = recipient.balance;
        vm.prank(withdrawer);
        vault.withdraw(recipient, 1 ether);
        assertEq(recipient.balance, before_ + 1 ether);
        assertEq(address(vault).balance, 1 ether);
    }

    function test_withdrawerCanWithdrawAll() public {
        (bool ok,) = address(vault).call{value: 3 ether}("");
        assertTrue(ok);

        uint256 before_ = recipient.balance;
        vm.prank(withdrawer);
        vault.withdrawAll(recipient);
        assertEq(recipient.balance, before_ + 3 ether);
        assertEq(address(vault).balance, 0);
    }

    function test_nonWithdrawerCannotWithdraw() public {
        (bool ok,) = address(vault).call{value: 1 ether}("");
        assertTrue(ok);

        vm.prank(address(0xBAD));
        vm.expectRevert();
        vault.withdraw(recipient, 1 ether);
    }

    function test_withdrawZeroReverts() public {
        (bool ok,) = address(vault).call{value: 1 ether}("");
        assertTrue(ok);

        vm.prank(withdrawer);
        vm.expectRevert();
        vault.withdraw(recipient, 0);
    }

    function test_withdrawAllOnEmptyReverts() public {
        vm.prank(withdrawer);
        vm.expectRevert();
        vault.withdrawAll(recipient);
    }

    function test_withdrawToZeroAddressReverts() public {
        (bool ok,) = address(vault).call{value: 1 ether}("");
        assertTrue(ok);

        vm.prank(withdrawer);
        vm.expectRevert();
        vault.withdraw(address(0), 1 ether);
    }

    function test_zeroAdminConstructorReverts() public {
        vm.expectRevert();
        new TreasuryVault(address(0));
    }

    function test_adminCanGrantWithdrawerRole() public {
        address newWithdrawer = address(0xBB01);
        vault.grantRole(vault.WITHDRAWER_ROLE(), newWithdrawer);
        (bool ok,) = address(vault).call{value: 1 ether}("");
        assertTrue(ok);
        vm.prank(newWithdrawer);
        vault.withdraw(recipient, 0.5 ether);
        assertEq(address(vault).balance, 0.5 ether);
    }
}
