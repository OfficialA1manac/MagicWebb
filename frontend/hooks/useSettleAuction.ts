"use client";
import {useWriteContract} from "wagmi";
import {ADDR} from "@/lib/addresses";
import {AuctionHouseAbi} from "@/lib/abi";

export function useSettleAuction() {
  const {writeContractAsync, isPending, error} = useWriteContract();
  const settle = (id: bigint) =>
    writeContractAsync({
      address: ADDR.auction,
      abi: AuctionHouseAbi,
      functionName: "settle",
      args: [id]
    });
  return {settle, isPending, error};
}
