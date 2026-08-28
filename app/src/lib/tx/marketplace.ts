// Fixed-price listings — Marketplace.sol.
//   list(coll, id, uint128 price, uint64 duration)
//   list1155(coll, id, uint128 amount, uint128 price, uint64 duration)
//   batchList(BatchItem[])   cancel(coll,id)   editPrice(coll,id,uint128)
//   buy(coll, id, seller) payable — msg.value must equal the listing price
import type { Address } from 'viem';
import { marketplaceAbi } from '../abi';
import { currentChain } from '../chains';
import { fmtPrice } from '../format';
import { ensureOperatorApproval, type TokenStandard } from './approve';
import { isValidDuration, type DurationSeconds } from './durations';
import { TxError } from './errors';
import { runTx, type TxHooks, type TxPlan, type TxRequest, type TxResult } from './runner';

export const MIN_PRICE_WEI = 10n ** 18n; // MarketplaceCore.MIN_PRICE = 1 ether
const U128 = (1n << 128n) - 1n;

export function marketplaceAddress(): Address {
  const a = currentChain().contracts.marketplace;
  if (!a) throw new TxError('Invalid', `Trading is not live on ${currentChain().name} yet — browsing, your wallet, and your profile still work. Switch to Coston2 to trade.`);
  return a;
}

export function assertPrice(priceWei: bigint): void {
  if (priceWei < MIN_PRICE_WEI) throw new TxError('PriceBelowMin', `Price must be at least 1 ${currentChain().currency}.`);
  if (priceWei > U128) throw new TxError('Invalid', 'Price is too large.');
}

export function assertDuration(d: number): asserts d is DurationSeconds {
  if (!isValidDuration(d)) throw new TxError('Invalid', 'Pick one of the allowed durations (1m–24h).');
}

// ── pure builders (unit-tested; no wallet) ─────────────────────────────────
export function buildList(nft: Address, tokenId: bigint, priceWei: bigint, duration: number, std: TokenStandard = 'erc721', amount = 1n): TxRequest {
  assertPrice(priceWei); assertDuration(duration);
  if (std === 'erc1155') {
    if (amount < 1n) throw new TxError('Invalid', 'Amount must be at least 1.');
    return { address: marketplaceAddress(), abi: marketplaceAbi, functionName: 'list1155', args: [nft, tokenId, amount, priceWei, BigInt(duration)] };
  }
  return { address: marketplaceAddress(), abi: marketplaceAbi, functionName: 'list', args: [nft, tokenId, priceWei, BigInt(duration)] };
}

export function buildBuy(nft: Address, tokenId: bigint, seller: Address, priceWei: bigint): TxRequest {
  return { address: marketplaceAddress(), abi: marketplaceAbi, functionName: 'buy', args: [nft, tokenId, seller], value: priceWei };
}

export function buildCancel(nft: Address, tokenId: bigint): TxRequest {
  return { address: marketplaceAddress(), abi: marketplaceAbi, functionName: 'cancel', args: [nft, tokenId] };
}

export function buildEditPrice(nft: Address, tokenId: bigint, newPriceWei: bigint): TxRequest {
  assertPrice(newPriceWei);
  return { address: marketplaceAddress(), abi: marketplaceAbi, functionName: 'editPrice', args: [nft, tokenId, newPriceWei] };
}

export interface BatchItem { coll: Address; id: bigint; price: bigint; duration: number }
export function buildBatchList(items: BatchItem[]): TxRequest {
  if (items.length === 0 || items.length > 50) throw new TxError('Invalid', 'Batch must contain 1–50 items.');
  for (const it of items) { assertPrice(it.price); assertDuration(it.duration); }
  return { address: marketplaceAddress(), abi: marketplaceAbi, functionName: 'batchList', args: [items.map((i) => ({ coll: i.coll, id: i.id, price: i.price, duration: BigInt(i.duration) }))] };
}

// ── flows ──────────────────────────────────────────────────────────────────
export interface ListArgs { nft: Address; tokenId: bigint; priceWei: bigint; duration: number; std?: TokenStandard; amount?: bigint; name?: string }

export function list(a: ListArgs, hooks?: TxHooks): Promise<TxResult> {
  const req = buildList(a.nft, a.tokenId, a.priceWei, a.duration, a.std, a.amount); // validate early
  const sym = currentChain().currency;
  const fee = (a.priceWei * 150n) / 10_000n;
  const plan: TxPlan = {
    title: `List ${a.name ?? `#${a.tokenId}`}`,
    approval: (ctx) => ensureOperatorApproval(ctx, a.nft, marketplaceAddress(), a.std),
    request: async () => req,
    summary: [
      ['Price', `${fmtPrice(a.priceWei)} ${sym}`],
      ['You receive on sale', `${fmtPrice(a.priceWei - fee)} ${sym} (98.5%)`],
      ['Marketplace fee', `${fmtPrice(fee)} ${sym} (1.5%) · only when it sells`],
      ['Listing cost', 'Free · gas only'],
    ],
  };
  return runTx(plan, hooks);
}

export interface BuyArgs { nft: Address; tokenId: bigint; seller: Address; priceWei: bigint; name?: string }

/** Server preflight + buy. Price is re-read from the preflight so a stale page cannot overpay. */
export function buy(a: BuyArgs, hooks?: TxHooks): Promise<TxResult> {
  const sym = currentChain().currency;
  const plan: TxPlan = {
    title: `Buy ${a.name ?? `#${a.tokenId}`}`,
    request: async () => {
      let price = a.priceWei;
      try {
        const r = await fetch(`/api/v1/listings/${a.nft}/${a.tokenId}/preflight?seller=${a.seller}`);
        if (r.ok) {
          const pf = await r.json() as { ok?: boolean; price_wei?: string };
          if (pf.ok === false) throw new TxError('Invalid', 'This listing is no longer available (sold, cancelled, or the NFT moved).');
          if (pf.price_wei) price = BigInt(pf.price_wei);
        }
      } catch (e) { if (e instanceof TxError) throw e; /* preflight is advisory */ }
      return buildBuy(a.nft, a.tokenId, a.seller, price);
    },
    summary: [
      ['Price', `${fmtPrice(a.priceWei)} ${sym}`],
      ['Marketplace fee', '1.5% · paid by the seller'],
      ['You pay', `${fmtPrice(a.priceWei)} ${sym} + gas`],
    ],
  };
  return runTx(plan, hooks);
}

export interface BatchListArgs { items: BatchItem[]; name?: string }

/** List up to 50 ERC-721s in one tx. One shared price+duration form upstream
 *  builds the items; approval is ensured per unique collection. */
export function batchList(a: BatchListArgs, hooks?: TxHooks): Promise<TxResult> {
  const req = buildBatchList(a.items); // validate early (1..50, price, duration)
  const sym = currentChain().currency;
  const total = a.items.reduce((t, i) => t + i.price, 0n);
  const uniqueColls = [...new Set(a.items.map((i) => i.coll.toLowerCase()))] as Address[];
  const plan: TxPlan = {
    title: a.name ?? `List ${a.items.length} NFTs`,
    approval: async (ctx) => {
      // The runner signs exactly one approval request; extra collections are
      // approved inline here (rare: batch items usually share one collection).
      const pending: TxRequest[] = [];
      for (const coll of uniqueColls) {
        const req = await ensureOperatorApproval(ctx, coll, marketplaceAddress(), 'erc721');
        if (req) pending.push(req);
      }
      const last = pending.pop() ?? null;
      for (const req of pending) {
        const hash = await ctx.wallet.writeContract({ ...req, account: ctx.account, chain: ctx.wallet.chain } as never);
        await ctx.pub.waitForTransactionReceipt({ hash });
      }
      return last;
    },
    request: async () => req,
    summary: [
      ['Items', `${a.items.length}`],
      ['Total asking price', `${fmtPrice(total)} ${sym}`],
      ['Marketplace fee', '1.5% per sale · paid by the seller'],
      ['Listing cost', 'Free · gas only'],
    ],
  };
  return runTx(plan, hooks);
}

export function cancel(a: { nft: Address; tokenId: bigint; name?: string }, hooks?: TxHooks): Promise<TxResult> {
  return runTx({ title: `Cancel listing ${a.name ?? `#${a.tokenId}`}`, request: async () => buildCancel(a.nft, a.tokenId), summary: [['Cost', 'Gas only · your NFT stays in your wallet']] }, hooks);
}

export function editPrice(a: { nft: Address; tokenId: bigint; newPriceWei: bigint; name?: string }, hooks?: TxHooks): Promise<TxResult> {
  const req = buildEditPrice(a.nft, a.tokenId, a.newPriceWei);
  return runTx({ title: `Change price ${a.name ?? `#${a.tokenId}`}`, request: async () => req, summary: [['New price', `${fmtPrice(a.newPriceWei)} ${currentChain().currency}`]] }, hooks);
}
