# Immutability transition

All four core contracts are UUPS proxies (`MarketplaceCore` is `UUPSUpgradeable`).
Upgrades are **time-locked**: `queueUpgrade(impl)` → wait `upgradeDelay()` →
`upgradeTo`. `upgradeDelay()` is 6 h on test chains (114/16/31337) and **48 h** on
Songbird/Flare; a queued upgrade expires after `MAX_UPGRADE_WINDOW` (7 days) and can be
`cancelUpgrade()`-ed. Only the `MarketplaceManager` `DEFAULT_ADMIN_ROLE` holder can queue.

Sequence already performed by every deploy script (`contracts/script/Deploy*.s.sol`):
1. `manager.setCoreContracts(marketplace, auction, offerBook)`
2. `grantRole(KEEPER_ROLE, keeper)`, `grantRole(DEFAULT_ADMIN_ROLE, creator)`,
   `grantRole(OPERATOR_ROLE, creator)`
3. `renounceRole(OPERATOR_ROLE, deployer)`, `renounceRole(DEFAULT_ADMIN_ROLE, deployer)`
4. On-chain `require`s confirm the handover before the script exits.

After deploy the only privileged actors are the **creator multisig** (admin + operator:
can pause *entries*, queue upgrades) and the **keeper** (settle / refund automation).
Exits — `withdrawRefund`, `cancel`, `settle`, `cancelOffer` — are never pausable
("pausable entries, unstoppable exits").

## Going fully immutable (optional, irreversible)
From the multisig: `renounceRole(DEFAULT_ADMIN_ROLE, safe)`. After that no upgrade can
ever be queued and `OPERATOR_ROLE` can no longer be re-granted. Do this only after a
mainnet burn-in period and only with the audit's sign-off; there is no undo.
