// MarketplaceCore surface shared by all three cores: pull-payment refunds.
//   pendingReturns(address) view · withdrawRefund()
import type { Address } from 'viem';
import { coreAbi } from '../abi';
import { currentChain } from '../chains';
import { fmtPrice } from '../format';
import { publicClient } from './client';
import { runTx, type TxHooks, type TxRequest, type TxResult } from './runner';

export type CoreKey = 'marketplace' | 'auctionHouse' | 'offerBook';
export const CORE_LABEL: Record<CoreKey, string> = { marketplace: 'Listings', auctionHouse: 'Auctions', offerBook: 'Offers' };

export function coreAddresses(): Array<[CoreKey, Address]> {
  const c = currentChain().contracts;
  return (['marketplace', 'auctionHouse', 'offerBook'] as CoreKey[]).filter((k) => c[k]).map((k) => [k, c[k] as Address]);
}

/** Refundable balance held for `who` on each core (outbid, rejected offers, failed pushes).
 * A failed read is reported with `ok: false` rather than a fake zero, so callers
 * can distinguish "no refunds" from "could not check" (money must never hide). */
export async function pendingReturns(who: Address): Promise<Array<{ key: CoreKey; address: Address; wei: bigint; ok: boolean }>> {
  const pub = await publicClient();
  const out: Array<{ key: CoreKey; address: Address; wei: bigint; ok: boolean }> = [];
  for (const [key, address] of coreAddresses()) {
    try {
      const wei = (await pub.readContract({ address, abi: coreAbi, functionName: 'pendingReturns', args: [who] })) as bigint;
      out.push({ key, address, wei, ok: true });
    } catch { out.push({ key, address, wei: 0n, ok: false }); }
  }
  return out;
}

export const buildWithdrawRefund = (core: Address): TxRequest => ({ address: core, abi: coreAbi, functionName: 'withdrawRefund', args: [] });

export function withdrawRefundFrom(a: { core: Address; label?: string; amountWei?: bigint }, hooks?: TxHooks): Promise<TxResult> {
  return runTx({
    title: `Withdraw refund${a.label ? ` · ${a.label}` : ''}`,
    request: async () => buildWithdrawRefund(a.core),
    summary: a.amountWei ? [['Amount', `${fmtPrice(a.amountWei)} ${currentChain().currency}`], ['Cost', 'Gas only']] : [],
  }, hooks);
}
