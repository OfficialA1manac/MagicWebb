"use client";
import {useWriteContract} from "wagmi";
import {ADDR} from "@/lib/addresses";
import {OfferBookAbi} from "@/lib/abi";

export function useCancelOffer() {
  const {writeContractAsync, isPending, error} = useWriteContract();
  const cancelOffer = (nonce: bigint) =>
    writeContractAsync({
      address: ADDR.offer,
      abi: OfferBookAbi,
      functionName: "cancelOffer",
      args: [nonce]
    });
  return {cancelOffer, isPending, error};
}
