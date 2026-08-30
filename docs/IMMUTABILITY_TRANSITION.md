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
2. `grantRole(KEEPER_ROLE, keeper)`, `grantRole(DEFAULT_ADMIN_ROLE, creator)` — `KEEPER_ADDR`
   is **required**; the scripts `require` it is non-zero and abort the deploy otherwise
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

### Pre-renunciation checklist — the keeper fleet

`MarketplaceManager.addKeeper()` is gated on **admin OR an existing keeper**
(`MarketplaceManager.sol:189-194`). Renouncing `DEFAULT_ADMIN_ROLE` therefore hands the
last remaining ability to grow the keeper fleet to the keepers themselves. If every
`KEEPER_ROLE` key is lost, compromised, or was never granted, `KEEPER_ROLE` becomes
**permanently unreachable on that deployment** — no admin exists to re-grant it, and no
keeper exists to co-opt a replacement. There is no recovery short of redeploying the
whole marketplace and migrating users.

Nothing is *trapped* by a dead keeper fleet: every exit path stays open
(`refundExpiredOffer` accepts the bidder themselves, `refundLosers` and `forceCancel` are
permissionless, `cancel`/`cancelOffer`/`withdrawLoserFunds`/`withdrawRefund` are
self-service). But automation dies: expired listings and offers are never swept, auctions
are never instantly settled at `endsAt`, and fee sweeps stop. The marketplace degrades to
manual-only operation forever.

Run this **before** the renunciation transaction, not after:

- [ ] **At least two independent keeper keys hold `KEEPER_ROLE`.**
      Confirm on-chain for each: `manager.hasRole(manager.KEEPER_ROLE(), <addr>) == true`.
      Two is the minimum — one key is a single point of permanent failure the moment the
      admin is gone.
- [ ] **The keys are independently held and backed up.** Different machines, different
      custody. Two hot keys derived from one seed on one server is one key.
- [ ] **Each key can actually transact.** Non-zero native balance on the target chain,
      and a recent successful transaction from that address (the keeper's own
      `settle`/`cleanExpired` traffic counts).
- [ ] **Each key can actually call `addKeeper`.** Prove it, do not assume it — a key that
      holds the role on paper but cannot sign is worth nothing after renunciation.
      Either:
      - fork the target chain at head and dry-run the grant:
        ```
        anvil --fork-url $RPC_URL --fork-block-number latest &
        cast rpc anvil_impersonateAccount $KEEPER_ADDR
        cast send $MANAGER_ADDR "addKeeper(address)" $NEW_KEEPER --from $KEEPER_ADDR --unlocked --rpc-url http://127.0.0.1:8545
        cast call $MANAGER_ADDR "hasRole(bytes32,address)(bool)" $(cast keccak "KEEPER_ROLE") $NEW_KEEPER --rpc-url http://127.0.0.1:8545
        ```
      - or, better, do it for real on the live chain: have each existing keeper grant a
        throwaway address, verify, then `removeKeeper` it. That exercises the real signer,
        the real nonce, and the real gas balance.
- [ ] **A written keeper-rotation runbook exists** describing how a surviving keeper adds
      a replacement, because after renunciation that is the only mechanism left.
- [ ] **The bot fleet is observed healthy** on the target network (keeper election holding
      a leader, settlements landing) for the full burn-in window, not just at the moment
      of the check.

Deploy-time guard: all three deploy scripts `require(keeper != address(0))`, so a network
can no longer be launched with an empty fleet in the first place. That closes the
worst case (deploy with no keeper, then renounce), but it does not cover keys lost after
deploy — hence this checklist.

**Future consideration (deferred, requires a contract change + redeploy):** make
`MarketplaceManager` extend `AccessControlEnumerableUpgradeable` so the live keeper count
is queryable on-chain (`getRoleMemberCount(KEEPER_ROLE)`). The renunciation transaction
could then be gated on a real count instead of an operator checklist. Deferred this round
because it changes storage layout on already-deployed proxies and its cost/upgrade impact
has not been evaluated; the checklist above is the interim control.
