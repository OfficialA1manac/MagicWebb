"use client";
import {useWriteContract} from "wagmi";
import {ADDR} from "@/lib/addresses";
import {OfferBookAbi} from "@/lib/abi";

export function useDeposit() {
  const {writeContractAsync, isPending, error} = useWriteContract();
  const deposit = (amount: bigint) =>
    writeContractAsync({
      address: ADDR.offer,
      abi: OfferBookAbi,
      functionName: "deposit",
      args: [],
      value: amount
    });
  return {deposit, isPending, error};
}
