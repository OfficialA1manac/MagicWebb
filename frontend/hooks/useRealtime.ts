"use client";
import {useSubscription} from "urql";
import {useQueryClient} from "@tanstack/react-query";

const LISTING_SUB = `subscription { listingEvents { type collection tokenId txHash timestamp } }`;
const AUCTION_SUB = `subscription { auctionEvents { type auctionId collection tokenId txHash timestamp } }`;
const OFFER_SUB = `subscription { offerEvents { type collection tokenId bidder amountWei nonce timestamp } }`;

export function useListingSubscription() {
  const qc = useQueryClient();
  useSubscription({query: LISTING_SUB}, (_, data: {listingEvents: {type: string}}) => {
    const t = data?.listingEvents?.type ?? "";
    if (t === "Listed" || t === "Bought" || t === "Cancelled") {
      qc.invalidateQueries({queryKey: ["chain-listings"]});
      qc.invalidateQueries({queryKey: ["trending"]});
    }
    return data;
  });
}

export function useAuctionSubscription() {
  const qc = useQueryClient();
  useSubscription({query: AUCTION_SUB}, (_, data: {auctionEvents: {type: string; auctionId?: string}}) => {
    const ev = data?.auctionEvents;
    if (!ev) return data;
    if (ev.type === "AuctionCreated" || ev.type === "BidPlaced" || ev.type === "AuctionSettled") {
      qc.invalidateQueries({queryKey: ["chain-auctions"]});
      if (ev.auctionId) qc.invalidateQueries({queryKey: ["auction", ev.auctionId]});
    }
    return data;
  });
}

export function useOfferSubscription() {
  const qc = useQueryClient();
  useSubscription({query: OFFER_SUB}, (_, data: {offerEvents: {type: string}}) => {
    const t = data?.offerEvents?.type ?? "";
    if (t === "OfferCreated" || t === "OfferAccepted" || t === "OfferCancelled") {
      qc.invalidateQueries({queryKey: ["offers"]});
    }
    return data;
  });
}

/** @deprecated Use useListingSubscription / useAuctionSubscription / useOfferSubscription */
export function useRealtime(_topics: string[]) {
  useListingSubscription();
  useAuctionSubscription();
  useOfferSubscription();
}
