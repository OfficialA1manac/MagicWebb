// SPDX-License-Identifier: MIT
pragma solidity ^0.8.24;

import {Script, console2} from "forge-std/Script.sol";
import {FeeVault} from "../src/FeeVault.sol";

/// @notice Optional ops script. Deploys a `FeeVault` and prints its address.
///         To switch fees over to it, call `setFeeVault(<vault>)` on each trade contract from `ADMIN_ADDR`.
contract DeployFeeVault is Script {
    function run() external {
        uint256 pk    = vm.envUint("PRIVATE_KEY");
        address admin = vm.envOr("ADMIN_ADDR", vm.addr(pk));

        vm.startBroadcast(pk);
        FeeVault vault = new FeeVault(admin);
        vm.stopBroadcast();

        console2.log("FEE_VAULT_ADDR=", address(vault));
        console2.log("ADMIN_ADDR=",     admin);
    }
}
