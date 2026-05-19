"use client";
import {useSignTypedData} from "wagmi";
import {offerDomain, offerTypes, type Offer} from "@/lib/eip712";
import {api} from "@/lib/api";
import {appendSentOffer} from "@/lib/offerInbox";

export function useSignOffer() {
  const {signTypedDataAsync, isPending, error} = useSignTypedData();

  const sign = async (offer: Offer): Promise<`0x${string}`> => {
    const signature = await signTypedDataAsync({
      domain: offerDomain,
      types: offerTypes,
      primaryType: "Offer",
      message: offer,
    });

    // Submit to Go backend; fall back to localStorage if API fails.
    try {
      await api.postOffer({
        bidder: offer.bidder,
        collection: offer.collection,
        token_id: offer.tokenId.toString(),
        amount_wei: offer.amount.toString(),
        nonce: offer.nonce.toString(),
        expires_at: Number(offer.expiresAt),
        signature,
      });
    } catch {
      // Fallback: persist locally so the seller can still receive the offer.
      const payload = JSON.stringify({
        offer: {
          ...offer,
          tokenId: offer.tokenId.toString(),
          amount: offer.amount.toString(),
          expiresAt: offer.expiresAt.toString(),
          nonce: offer.nonce.toString(),
        },
        signature,
      });
      appendSentOffer(payload);
    }

    return signature;
  };

  return {sign, isPending, error};
}
