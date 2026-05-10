"use client";
import {useWriteContract} from "wagmi";
import type {Address} from "viem";
import {ADDR} from "@/lib/addresses";
import {MarketplaceAbi} from "@/lib/abi";

export function useBuy() {
  const {writeContractAsync, isPending, error} = useWriteContract();
  const buy = (coll: Address, id: bigint, price: bigint) =>
    writeContractAsync({
      address: ADDR.marketplace,
      abi: MarketplaceAbi,
      functionName: "buy",
      args: [coll, id],
      value: price
    });
  return {buy, isPending, error};
}
