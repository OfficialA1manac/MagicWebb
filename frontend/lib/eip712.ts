import type {Address} from "viem";
import {ADDR, CHAIN_ID} from "./addresses";

export const offerDomain = {
  name: "WebbPlaceOfferBook",
  version: "1",
  chainId: CHAIN_ID,
  verifyingContract: ADDR.offer
} as const;

export const offerTypes = {
  Offer: [
    {name: "bidder",     type: "address"},
    {name: "collection", type: "address"},
    {name: "tokenId",    type: "uint256"},
    {name: "amount",     type: "uint128"},
    {name: "expiresAt",  type: "uint64"},
    {name: "nonce",      type: "uint64"}
  ]
} as const;

export type Offer = {
  bidder: Address;
  collection: Address;
  tokenId: bigint;
  amount: bigint;
  expiresAt: bigint;
  nonce: bigint;
};
