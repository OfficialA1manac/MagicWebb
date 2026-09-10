// SPDX-License-Identifier: MIT
pragma solidity 0.8.26;

import {Script, console2} from "forge-std/Script.sol";
import {ERC1967Proxy}      from "@openzeppelin/contracts/proxy/ERC1967/ERC1967Proxy.sol";
import {MarketplaceManager} from "../src/MarketplaceManager.sol";

/// @notice Deploys ONLY a v3.7 MarketplaceManager (UUPS impl + ERC-1967
///         proxy) for a network whose cores already exist — the migration
///         path for Coston2, which went live (block 34905078) on the v3.4
///         plain-bytecode manager and must become fully upgradeable without
///         redeploying the three core proxies (their addresses, listings,
///         escrow and history stay put).
///
/// Full migration for an already-deployed network (owner, ADMIN_KEY):
///   1. PRIVATE_KEY=… ADMIN_ADDR=<current admin> KEEPER_ADDR=<current keeper>
///      forge script script/DeployManager.s.sol --rpc-url <rpc> --broadcast
///      → MANAGER_ADDR (the new proxy).
///   2. MANAGER_ADDR=<new proxy> ADMIN_KEY=… DEPLOYER_KEY=…
///      tools/upgrade-cores.sh <network>
///      → builds new core impls baking the NEW manager, installs them through
///        the OLD manager's admin gate (same admin key), verifies, and
///        rewrites deployments/<network>.json (contracts.marketplaceManager +
///        impls.marketplaceManager). Then commit + push (= deploy) and run
///        cmd/reindexgov from the upgrade block so the governance trail shows
///        the new manager's INIT.
///   The old plain manager stays on chain, unreferenced; it holds no funds.
///
/// Fresh networks do NOT use this script — DeployV34 deploys the proxied
/// manager together with the cores.
contract DeployManager is Script {
    bytes32 private constant _IMPL_SLOT =
        0x360894a13ba1a3210667c828492db98dca3e2076cc3735a920a3ca505d382bbc;

    function run() external {
        uint256 pk      = vm.envUint("PRIVATE_KEY");
        address admin_  = vm.envAddress("ADMIN_ADDR");
        address keeper_ = vm.envAddress("KEEPER_ADDR");
        require(admin_ != address(0),  "ADMIN_ADDR required");
        require(keeper_ != address(0), "KEEPER_ADDR required");
        require(admin_ != keeper_,     "ADMIN_ADDR must differ from KEEPER_ADDR");

        vm.startBroadcast(pk);
        MarketplaceManager impl = new MarketplaceManager();
        ERC1967Proxy proxy = new ERC1967Proxy(
            address(impl),
            abi.encodeWithSelector(MarketplaceManager.initialize.selector, admin_, keeper_)
        );
        vm.stopBroadcast();

        MarketplaceManager manager = MarketplaceManager(address(proxy));
        require(manager.admin()  == admin_,  "admin mismatch");
        require(manager.keeper() == keeper_, "keeper mismatch");
        require(manager.pendingAdmin() == address(0), "pending admin set at birth");
        require(address(uint160(uint256(vm.load(address(proxy), _IMPL_SLOT)))) == address(impl), "impl slot mismatch");
        require(impl.proxiableUUID() == _IMPL_SLOT, "impl is not UUPS");

        console2.log("# Magic Webb v3.7 manager -- next: MANAGER_ADDR=<proxy> tools/upgrade-cores.sh <network>");
        console2.log("CHAIN_ID=",     block.chainid);
        console2.log("MANAGER_ADDR=", address(proxy));
        console2.log("MANAGER_IMPL=", address(impl));
        console2.log("ADMIN_ADDR=",   admin_);
        console2.log("KEEPER_ADDR=",  keeper_);
    }
}
