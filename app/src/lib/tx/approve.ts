import type { Address } from 'viem';
import { erc721Abi, erc1155Abi } from '../abi';
import type { WalletCtx } from './client';
import type { TxRequest } from './runner';

export type TokenStandard = 'erc721' | 'erc1155';

/**
 * Returns the setApprovalForAll request when the operator is not yet approved,
 * or null when nothing needs signing. Used as `TxPlan.approval`.
 */
export async function ensureOperatorApproval(ctx: WalletCtx, nft: Address, operator: Address, std: TokenStandard = 'erc721'): Promise<TxRequest | null> {
  const abi = std === 'erc1155' ? erc1155Abi : erc721Abi;
  const approved = await ctx.pub.readContract({ address: nft, abi, functionName: 'isApprovedForAll', args: [ctx.account, operator] });
  if (approved) return null;
  return { address: nft, abi, functionName: 'setApprovalForAll', args: [operator, true] };
}
