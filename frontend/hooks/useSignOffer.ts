"use client";
import {useSignTypedData} from "wagmi";
import {offerDomain, offerTypes, type Offer} from "@/lib/eip712";
import {api} from "@/lib/api";

export function useSignOffer() {
  const {signTypedDataAsync, isPending, error} = useSignTypedData();

  const sign = async (offer: Offer): Promise<`0x${string}`> => {
    const signature = await signTypedDataAsync({
      domain: offerDomain,
      types: offerTypes,
      primaryType: "Offer",
      message: offer,
    });

    await api.postOffer({
      bidder: offer.bidder,
      collection: offer.collection,
      token_id: offer.tokenId.toString(),
      amount_wei: offer.amount.toString(),
      nonce: offer.nonce.toString(),
      expires_at: Number(offer.expiresAt),
      signature,
    });

    return signature;
  };

  return {sign, isPending, error};
}
