# WebbPlace

Non-custodial NFT marketplace on **Flare Coston2** testnet.

- **Buy / sell** at fixed price (`Marketplace`)
- **English auctions** with reserve, increment, and **pull-pattern refunds** (`AuctionHouse`)
- **Off-chain signed offers** on listed *or unlisted* tokens (`OfferBook`, EIP-712)

Sellers keep custody until trade settles. Platform fee (default 2.5%) routes directly to the creator wallet.

## Stack

| Layer | Tech |
|---|---|
| Contracts | Solidity 0.8.24, Foundry, OpenZeppelin |
| Frontend  | Next.js 15 (App Router), React 19, wagmi v2, viem v2, RainbowKit v2, TailwindCSS |
| Backend (optional indexer / API) | Go + Fiber + GraphQL (gqlgen) + gRPC, Postgres + sqlc + Goose, Zig (CGo) for log decode + EIP-712 batch verify |
| Network | Flare Coston2 (chain id 114), C2FLR |

## Architecture

```
                 ┌──── Marketplace ─────┐
   wallet ──RPC──┤                      ├── creator EOA (fee)
                 ├──── AuctionHouse ────┤        │
                 │                      │        └── seller EOA (proceeds)
                 └──── OfferBook ───────┘
                              ▲
                       EIP-712 signed offer
                       (off-chain, redeemable on-chain)
```

`MarketplaceCore` is the shared base — fee config, role-based admin, pause, NFT transfer, fee splitter. Trade contracts inherit it. `feeVault` storage points directly at the creator wallet to skip a CALL hop per trade. Use the optional `DeployFeeVault.s.sol` + `setFeeVault(...)` to switch to a contract sink later.

## Coston2 setup

| Field | Value |
|---|---|
| Chain ID | 114 |
| Native | C2FLR |
| RPC | `https://coston2-api.flare.network/ext/C/rpc` |
| WS | `wss://coston2-api.flare.network/ext/bc/C/ws` |
| Explorer | `https://coston2-explorer.flare.network` |
| Faucet | `https://faucet.flare.network` (select Coston2) |

## Prereqs

- `foundry` (forge)
- `node` ≥ 20 + `npm`
- `jq` (for `tools/load_contract_addrs.sh`)
- (optional, for backend indexer) `go`, `pnpm`, `goose`, `sqlc`, `protoc`, `zig`, `psql`

## Deploy contracts

```bash
cd contracts
forge install                           # first time only
forge build
forge test -vvv --gas-report
```

Then deploy to Coston2 (operator funds `PRIVATE_KEY` at the faucet first):

```bash
export PRIVATE_KEY=0x...
export CREATOR_ADDR=0x78993B71051de91C2D2595BC3475F07748927dc0
export ADMIN_ADDR=$CREATOR_ADDR
export FEE_BPS=250
export ROUTESCAN_API_KEY=...

forge script script/DeployCoston2.s.sol --rpc-url coston2 --broadcast --verify
```

If `--verify` fails, re-verify each contract explicitly:

```bash
forge verify-contract <ADDR> Marketplace  --chain coston2 \
  --constructor-args $(cast abi-encode "constructor(address,address,uint16)" "$ADMIN_ADDR" "$CREATOR_ADDR" "$FEE_BPS")
forge verify-contract <ADDR> AuctionHouse --chain coston2 --constructor-args ...
forge verify-contract <ADDR> OfferBook    --chain coston2 --constructor-args ...
```

Pull addresses into `.env` + `frontend/.env.local`:

```bash
bash tools/load_contract_addrs.sh
```

## Run frontend

```bash
cd frontend
cp .env.local.example .env.local
# Fill NEXT_PUBLIC_*_ADDR (or run load_contract_addrs.sh first) and NEXT_PUBLIC_WC_PROJECT_ID
# Get a WalletConnect project id at https://cloud.walletconnect.com
npm install
npm run dev      # http://localhost:3000
```

## User flows

### Buy listed token
1. Connect wallet → switch to Coston2 if prompted.
2. Search by collection address → token detail page.
3. Click **Buy now**. Tx submits with `msg.value == price`.
4. Seller receives `price * (1 - feeBps/10000)`; creator wallet gets the fee.

### Auction
1. Seller `setApprovalForAll(AuctionHouse, true)` and calls `create(...)`.
2. Bidders call `bid` on `/auction/:id`. Each outbid credits the prior bidder via `pendingReturns`.
3. Outbid bidders claim their refund via **Profile → Withdraw refund** (`withdrawRefund()`).
4. After `endsAt`, anyone calls **Settle**. NFT moves; fee routes to creator.

### Signed offer (works on unlisted tokens)
1. Bidder funds deposit (`OfferBook.deposit` payable).
2. Bidder fills the offer modal → wallet signs EIP-712 typed data → `{offer, sig}` JSON copied to clipboard.
3. Bidder sends JSON to the token owner (DM, off-chain channel).
4. Owner approves `OfferBook` for the token, then submits `acceptOffer(offer, sig, tokenIdActual)`.
5. NFT transfers; deposit is debited; fee routes to creator. Bidder can pre-emptively `cancelOffer(nonce)` at any time.

## Gas (forge test --gas-report)

Filled in after `forge test`. Notable hot paths:
- `Marketplace.buy` — fee push direct to EOA, no FeeVault hop.
- `AuctionHouse.bid` outbid — pull refund (no external call), DOS-resistant.
- `OfferBook.acceptOffer` — single `_splitAndPay` call, deposit decrement.

## Backend (optional)

A Go indexer + GraphQL/gRPC API exists under `backend/`. It is not required for trade flows — every UI action calls contracts directly. To run:

```bash
make setup     # tool check, db, migrations, codegen, zig build
make dev       # matcher → indexer → api → web (logs in ./logs)
```

See `Makefile` and `backend/README` for details.

## Troubleshooting

- **"Wrong network" banner** — switch to chain 114 in your wallet (the banner has a button).
- **`NotApproved` on list / accept** — call `setApprovalForAll(<contract>, true)` on the NFT first.
- **`Expired` on buy** — the listing's `expiresAt` passed; ask the seller to re-list.
- **`OfferUsed`** — the bidder cancelled this nonce, or it was already accepted.
- **Auction outbid succeeded but no refund visible** — claim it on the profile page; refunds use the pull pattern (`withdrawRefund`).
- **Routescan verify fails** — set `ROUTESCAN_API_KEY` and re-run `forge verify-contract` per the explicit form above.

## License

MIT.
