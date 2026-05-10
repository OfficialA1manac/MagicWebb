"use client";
import {useWriteContract} from "wagmi";
import type {Hex} from "viem";
import {ADDR} from "@/lib/addresses";
import {OfferBookAbi} from "@/lib/abi";
import type {Offer} from "@/lib/eip712";

export function useAcceptOffer() {
  const {writeContractAsync, isPending, error} = useWriteContract();
  const accept = (o: Offer, sig: Hex, tokenIdActual: bigint) =>
    writeContractAsync({
      address: ADDR.offer,
      abi: OfferBookAbi,
      functionName: "acceptOffer",
      args: [o, sig, tokenIdActual]
    });
  return {accept, isPending, error};
}
