"use client";
import {useWriteContract} from "wagmi";
import {ADDR} from "@/lib/addresses";
import {OfferBookAbi} from "@/lib/abi";

export function useWithdrawDeposit() {
  const {writeContractAsync, isPending, error} = useWriteContract();
  const withdraw = (amount: bigint) =>
    writeContractAsync({
      address: ADDR.offer,
      abi: OfferBookAbi,
      functionName: "withdraw",
      args: [amount]
    });
  return {withdraw, isPending, error};
}
