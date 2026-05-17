import type {Address} from "viem";

const norm = (s: string) => s.trim().toLowerCase();
const toAddress = (label: string, v: string): Address => {
  if (!/^0x[a-fA-F0-9]{40}$/.test(v.trim())) {
    throw new Error(`Invalid address format for ${label}: ${v}`);
  }
  return norm(v) as Address;
};

/** Next.js only inlines NEXT_PUBLIC_* when accessed as static `process.env.NAME` — not `process.env[k]`. */
const marketplaceRaw = process.env.NEXT_PUBLIC_MARKETPLACE_ADDR;
const auctionRaw = process.env.NEXT_PUBLIC_AUCTION_ADDR;
const offerRaw = process.env.NEXT_PUBLIC_OFFER_ADDR;
const chainIdRaw = process.env.NEXT_PUBLIC_CHAIN_ID;
const rpcRaw = process.env.NEXT_PUBLIC_RPC_URL;
const explorerRaw = process.env.NEXT_PUBLIC_EXPLORER_URL;
const currencySymbolRaw = process.env.NEXT_PUBLIC_CURRENCY_SYMBOL;
const chainNameRaw = process.env.NEXT_PUBLIC_CHAIN_NAME;

const reqAddress = (label: string, v: string | undefined): Address => {
  if (!v || v.trim() === "") {
    throw new Error(`Missing env ${label}. Refusing to launch.`);
  }
  return toAddress(label, v);
};

const reqNumber = (label: string, v: string | undefined): number => {
  if (!v || v.trim() === "") {
    throw new Error(`Missing env ${label}. Refusing to launch.`);
  }
  const parsed = Number(v);
  if (!Number.isInteger(parsed) || parsed <= 0) {
    throw new Error(`Invalid numeric env ${label}: ${v}`);
  }
  return parsed;
};

const reqNonEmpty = (label: string, v: string | undefined): string => {
  if (!v || v.trim() === "") {
    throw new Error(`Missing env ${label}. Refusing to launch.`);
  }
  return v;
};

export const ADDR = {
  marketplace: reqAddress("NEXT_PUBLIC_MARKETPLACE_ADDR", marketplaceRaw),
  auction: reqAddress("NEXT_PUBLIC_AUCTION_ADDR", auctionRaw),
  offer: reqAddress("NEXT_PUBLIC_OFFER_ADDR", offerRaw)
} as const;

export const CHAIN_ID = reqNumber("NEXT_PUBLIC_CHAIN_ID", chainIdRaw);
export const RPC_URL = reqNonEmpty("NEXT_PUBLIC_RPC_URL", rpcRaw);
export const EXPLORER_URL = reqNonEmpty("NEXT_PUBLIC_EXPLORER_URL", explorerRaw);
export const CURRENCY_SYMBOL = reqNonEmpty("NEXT_PUBLIC_CURRENCY_SYMBOL", currencySymbolRaw);
export const CHAIN_NAME = reqNonEmpty("NEXT_PUBLIC_CHAIN_NAME", chainNameRaw);
