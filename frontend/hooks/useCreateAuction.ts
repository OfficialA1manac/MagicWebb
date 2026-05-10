"use client";
import {useWriteContract} from "wagmi";
import type {Address} from "viem";
import {ADDR} from "@/lib/addresses";
import {AuctionHouseAbi} from "@/lib/abi";

export function useCreateAuction() {
  const {writeContractAsync, isPending, error} = useWriteContract();
  const create = (
    coll: Address, tokenId: bigint, reserve: bigint,
    startsAt: bigint, endsAt: bigint, minIncBps: number
  ) =>
    writeContractAsync({
      address: ADDR.auction,
      abi: AuctionHouseAbi,
      functionName: "create",
      args: [coll, tokenId, reserve, startsAt, endsAt, minIncBps]
    });
  return {create, isPending, error};
}
