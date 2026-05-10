import type {Address} from "viem";

const req = (k: string): Address => {
  const v = process.env[k];
  if (!v) throw new Error(`missing env ${k}`);
  return v as Address;
};

export const ADDR = {
  marketplace: req("NEXT_PUBLIC_MARKETPLACE_ADDR"),
  auction:     req("NEXT_PUBLIC_AUCTION_ADDR"),
  offer:       req("NEXT_PUBLIC_OFFER_ADDR")
} as const;

export const CHAIN_ID = Number(process.env.NEXT_PUBLIC_CHAIN_ID ?? 114);
export const CREATOR  = (process.env.NEXT_PUBLIC_CREATOR_ADDR ?? "0x78993B71051de91C2D2595BC3475F07748927dc0") as Address;
