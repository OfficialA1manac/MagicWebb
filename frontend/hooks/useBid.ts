"use client";
import {useWriteContract} from "wagmi";
import {ADDR} from "@/lib/addresses";
import {AuctionHouseAbi} from "@/lib/abi";

export function useBid() {
  const {writeContractAsync, isPending, error} = useWriteContract();
  const bid = (id: bigint, amount: bigint) =>
    writeContractAsync({
      address: ADDR.auction,
      abi: AuctionHouseAbi,
      functionName: "bid",
      args: [id],
      value: amount
    });
  return {bid, isPending, error};
}
