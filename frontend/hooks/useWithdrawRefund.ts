"use client";
import {useReadContract, useWriteContract} from "wagmi";
import type {Address} from "viem";
import {ADDR} from "@/lib/addresses";
import {AuctionHouseAbi} from "@/lib/abi";

export function useWithdrawRefund(account?: Address) {
  const {data: pending, refetch} = useReadContract({
    address: ADDR.auction,
    abi: AuctionHouseAbi,
    functionName: "pendingReturns",
    args: account ? [account] : undefined,
    query: {enabled: !!account}
  });
  const {writeContractAsync, isPending, error} = useWriteContract();
  const withdraw = () =>
    writeContractAsync({
      address: ADDR.auction,
      abi: AuctionHouseAbi,
      functionName: "withdrawRefund",
      args: []
    });
  return {pending: (pending as bigint | undefined) ?? 0n, withdraw, refetch, isPending, error};
}
