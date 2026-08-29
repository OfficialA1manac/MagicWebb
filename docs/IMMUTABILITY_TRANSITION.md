# Immutability transition

All four core contracts are UUPS proxies (`MarketplaceCore` is `UUPSUpgradeable`).
Upgrades are **time-locked**: `queueUpgrade(impl)` → wait `upgradeDelay()` →
`upgradeTo`. `upgradeDelay()` is **0 (instant)** on test chains (114/16/31337) while the
marketplace is in its testing phase, and **48 h** on Songbird/Flare; a queued upgrade
expires after `MAX_UPGRADE_WINDOW` (7 days) and can be `cancelUpgrade()`-ed. Only the
`MarketplaceManager` `DEFAULT_ADMIN_ROLE` holder can queue. The manager itself is held to
the same queue/delay/window discipline (v3 — previously a bare role check).

Sequence already performed by every deploy script (`contracts/script/Deploy*.s.sol`):
1. `manager.setCoreContracts(marketplace, auction, offerBook)`
2. `grantRole(KEEPER_ROLE, keeper)`, `grantRole(DEFAULT_ADMIN_ROLE, creator)`
3. `renounceRole(DEFAULT_ADMIN_ROLE, deployer)` — only when `creator != deployer`; the
   creator admin account should be a multisig on mainnet
4. On-chain `require`s confirm the handover before the script exits.

After deploy the only privileged actors are the **creator multisig** (admin: roles and
timelocked upgrades) and the **keeper** (instant auction settlement / refund automation).
Nothing is pausable — no role can halt any entry or exit path.

## Going fully immutable (optional, irreversible)
From the multisig: `renounceRole(DEFAULT_ADMIN_ROLE, safe)`. After that no upgrade can
ever be queued and no new roles can be granted. Do this only after a
mainnet burn-in period and only with the audit's sign-off; there is no undo.
