import { BaseError, ContractFunctionRevertedError, UserRejectedRequestError, InsufficientFundsError, type Abi, type Hex } from 'viem';
import { currentChain } from '../chains';

export type TxErrorKind =
  | 'WalletRequired' | 'WrongChain' | 'UserRejected' | 'InsufficientFunds'
  | 'ContractRevert' | 'Paused' | 'PriceBelowMin' | 'BidTooLow' | 'AuctionEnded'
  | 'NotOwner' | 'Expired' | 'OffersNotEligible' | 'NotApproved' | 'RpcError' | 'IndexTimeout' | 'Invalid';

export class TxError extends Error {
  kind: TxErrorKind;
  revertName?: string;
  hash?: Hex;
  constructor(kind: TxErrorKind, message: string, opts: { revertName?: string; hash?: Hex; cause?: unknown } = {}) {
    super(message, { cause: opts.cause });
    this.name = 'TxError';
    this.kind = kind;
    this.revertName = opts.revertName;
    this.hash = opts.hash;
  }
}

function sym() { try { return currentChain().currency; } catch { return 'FLR'; } }

/** Custom-error name → plain-language copy a non-technical user can act on. */
function revertCopy(name: string): [TxErrorKind, string] | null {
  const table: Record<string, [TxErrorKind, string]> = {
    EntriesHalted: ['Paused', 'The marketplace has paused new listings, bids and offers. Existing trades can still be settled and refunds withdrawn.'],
    BelowMinPrice: ['PriceBelowMin', `Price is below the minimum of 1 ${sym()}.`],
    BidTooLow: ['BidTooLow', `Your bid must beat the current bid by at least 1 ${sym()}.`],
    BadIncrement: ['BidTooLow', 'Bid increment is too small.'],
    AuctionEnded: ['AuctionEnded', 'This auction has already ended.'],
    AuctionLive: ['Invalid', 'This auction is still running.'],
    NotOwner: ['NotOwner', 'Only the owner can do that.'],
    NotSeller: ['NotOwner', 'Only the seller can do that.'],
    NotApproved: ['NotApproved', 'The marketplace is not approved to move this NFT yet.'],
    Expired: ['Expired', 'This listing or offer has expired.'],
    OfferExpired: ['Expired', 'This offer has expired.'],
    InvalidExpiry: ['Invalid', 'Pick an expiry in the future.'],
    InvalidDuration: ['Invalid', 'Pick one of the allowed durations.'],
    InvalidAmount: ['Invalid', 'Amount must be at least 1.'],
    NotListed: ['Invalid', 'This NFT is not listed any more.'],
    NotActive: ['Invalid', 'This item is no longer active.'],
    WrongPrice: ['Invalid', 'The price changed. Refresh and try again.'],
    WrongValue: ['Invalid', 'The amount sent does not match the price.'],
    PrincipalChanged: ['Invalid', 'The offer changed. Refresh and try again.'],
    OffersNotEligible: ['OffersNotEligible', 'This collection has not enabled offers yet.'],
    NoOffer: ['Invalid', 'There is no such offer.'],
    OfferActive: ['Invalid', 'You already have an active offer on this NFT. Cancel it first.'],
    NothingToWithdraw: ['Invalid', 'Nothing to withdraw.'],
    CannotCancel: ['Invalid', 'This auction has bids and cannot be cancelled early.'],
    NotSettled: ['Invalid', 'The auction has not been settled yet.'],
    BatchTooLarge: ['Invalid', 'Too many items in one batch (max 50).'],
    TransferFailed: ['ContractRevert', 'The token transfer failed. Is the marketplace still approved?'],
    WithdrawFailed: ['ContractRevert', 'The withdrawal transfer failed.'],
  };
  return table[name] ?? null;
}

const REJECTED = 'You rejected the request in your wallet. Nothing was sent and no funds moved.';

export function decodeRevert(e: unknown, _abis: Abi[] = []): TxError {
  if (e instanceof TxError) return e;
  if (e instanceof BaseError) {
    if (e.walk((x) => x instanceof UserRejectedRequestError)) return new TxError('UserRejected', REJECTED, { cause: e });
    if (e.walk((x) => x instanceof InsufficientFundsError)) return new TxError('InsufficientFunds', `Not enough ${sym()} to cover the amount plus gas.`, { cause: e });
    const rev = e.walk((x) => x instanceof ContractFunctionRevertedError) as ContractFunctionRevertedError | null;
    if (rev) {
      const name = rev.data?.errorName ?? rev.reason ?? '';
      const hit = revertCopy(name);
      if (hit) return new TxError(hit[0], hit[1], { revertName: name, cause: e });
      return new TxError('ContractRevert', name ? `The contract rejected this (${name}).` : 'The contract rejected this transaction.', { revertName: name || undefined, cause: e });
    }
    const short = e.shortMessage || e.message;
    if (/user rejected|denied|rejected the request/i.test(short)) return new TxError('UserRejected', REJECTED, { cause: e });
    if (/insufficient funds/i.test(short)) return new TxError('InsufficientFunds', `Not enough ${sym()} to cover the amount plus gas.`, { cause: e });
    if (/chain|network/i.test(short) && /mismatch|switch|does not match/i.test(short)) {
      return new TxError('WrongChain', `Your wallet is on a different network. Switch it to ${currentChain().name}.`, { cause: e });
    }
    return new TxError('RpcError', short, { cause: e });
  }
  const msg = e instanceof Error ? e.message : String(e);
  if (/user rejected|denied/i.test(msg)) return new TxError('UserRejected', REJECTED, { cause: e });
  return new TxError('RpcError', msg || 'Something went wrong talking to the network.', { cause: e });
}
