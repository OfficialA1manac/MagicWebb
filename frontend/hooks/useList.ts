"use client";
import {useWriteContract} from "wagmi";
import type {Address} from "viem";
import {ADDR} from "@/lib/addresses";
import {MarketplaceAbi} from "@/lib/abi";

export function useList() {
  const {writeContractAsync, isPending, error} = useWriteContract();
  const list = (coll: Address, id: bigint, price: bigint, expiresAt: bigint) =>
    writeContractAsync({
      address: ADDR.marketplace,
      abi: MarketplaceAbi,
      functionName: "list",
      args: [coll, id, price, expiresAt]
    });
  return {list, isPending, error};
}
