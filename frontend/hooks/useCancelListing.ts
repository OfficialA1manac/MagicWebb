"use client";
import {useWriteContract} from "wagmi";
import type {Address} from "viem";
import {ADDR} from "@/lib/addresses";
import {MarketplaceAbi} from "@/lib/abi";

export function useCancelListing() {
  const {writeContractAsync, isPending, error} = useWriteContract();
  const cancel = (coll: Address, id: bigint) =>
    writeContractAsync({
      address: ADDR.marketplace,
      abi: MarketplaceAbi,
      functionName: "cancel",
      args: [coll, id]
    });
  return {cancel, isPending, error};
}
