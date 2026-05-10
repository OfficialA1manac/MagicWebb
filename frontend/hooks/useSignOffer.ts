"use client";
import {useSignTypedData} from "wagmi";
import {offerDomain, offerTypes, type Offer} from "@/lib/eip712";

export function useSignOffer() {
  const {signTypedDataAsync, isPending, error} = useSignTypedData();
  const sign = (offer: Offer) =>
    signTypedDataAsync({
      domain: offerDomain,
      types: offerTypes,
      primaryType: "Offer",
      message: offer
    });
  return {sign, isPending, error};
}
