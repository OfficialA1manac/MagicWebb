"use client";
import {useWriteContract} from "wagmi";
import type {Address} from "viem";
import {ADDR} from "@/lib/addresses";
import {MarketplaceAbi} from "@/lib/abi";

export const BATCH_MAX = 50;

export type BatchListItem = {
  coll: Address;
  id: bigint;
  price: bigint;
  expiresAt: bigint;
};

export function useBatchList() {
  const {writeContractAsync, isPending, error} = useWriteContract();

  const batchList = (items: BatchListItem[]) => {
    if (items.length === 0 || items.length > BATCH_MAX) {
      throw new Error(`batch size must be 1–${BATCH_MAX}`);
    }
    return writeContractAsync({
      address: ADDR.marketplace,
      abi: MarketplaceAbi,
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
      functionName: "batchList" as any,
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
      args: [items.map(i => ({coll: i.coll, id: i.id, price: i.price, expiresAt: i.expiresAt}))] as any,
    });
  };

  return {batchList, isPending, error};
}
