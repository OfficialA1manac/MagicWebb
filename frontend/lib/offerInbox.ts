import type {Address, Hex} from "viem";
import type {Offer} from "@/lib/eip712";

const SENT_KEY = "magicwebb:sent-offers:v1";
const RECV_KEY = "magicwebb:received-offers:v1";

export type SentOfferEntry = {
  id: string;
  raw: string;
  createdAt: number;
};

/** Imported offer the seller saved (includes which token they intend to sell for collection-wide offers). */
export type ReceivedOfferEntry = {
  id: string;
  raw: string;
  createdAt: number;
  /** Token ID seller will deliver (required when offer.tokenId is 0). */
  deliverTokenId: string;
};

function safeParse<T>(raw: string | null, fallback: T): T {
  if (!raw) return fallback;
  try {
    return JSON.parse(raw) as T;
  } catch {
    return fallback;
  }
}

export function readSentOffers(): SentOfferEntry[] {
  if (typeof window === "undefined") return [];
  return safeParse<SentOfferEntry[]>(localStorage.getItem(SENT_KEY), []).filter(
    e => typeof e.id === "string" && typeof e.raw === "string"
  );
}

export function writeSentOffers(entries: SentOfferEntry[]) {
  localStorage.setItem(SENT_KEY, JSON.stringify(entries));
}

export function appendSentOffer(raw: string): SentOfferEntry[] {
  const cur = readSentOffers();
  const id = typeof crypto !== "undefined" && crypto.randomUUID ? crypto.randomUUID() : `${Date.now()}-${Math.random()}`;
  const next = [{id, raw, createdAt: Date.now()}, ...cur];
  writeSentOffers(next);
  return next;
}

export function removeSentOffer(id: string) {
  writeSentOffers(readSentOffers().filter(e => e.id !== id));
}

export function readReceivedOffers(): ReceivedOfferEntry[] {
  if (typeof window === "undefined") return [];
  return safeParse<ReceivedOfferEntry[]>(localStorage.getItem(RECV_KEY), []).filter(
    e => typeof e.id === "string" && typeof e.raw === "string" && typeof e.deliverTokenId === "string"
  );
}

export function writeReceivedOffers(entries: ReceivedOfferEntry[]) {
  localStorage.setItem(RECV_KEY, JSON.stringify(entries));
}

export function appendReceivedOffer(raw: string, deliverTokenId: string): ReceivedOfferEntry[] {
  const cur = readReceivedOffers();
  const id = typeof crypto !== "undefined" && crypto.randomUUID ? crypto.randomUUID() : `${Date.now()}-${Math.random()}`;
  const next = [{id, raw, deliverTokenId, createdAt: Date.now()}, ...cur];
  writeReceivedOffers(next);
  return next;
}

export function removeReceivedOffer(id: string) {
  writeReceivedOffers(readReceivedOffers().filter(e => e.id !== id));
}

export function parseOfferPayload(raw: string): {offer: Offer; sig: Hex} {
  const j = JSON.parse(raw) as {
    offer: {
      bidder: string;
      collection: string;
      tokenId: string;
      amount: string;
      expiresAt: string;
      nonce: string;
    };
    sig: Hex;
  };
  const o = j.offer;
  if (!o || !j.sig) throw new Error("Missing offer or sig");
  const offer: Offer = {
    bidder: o.bidder as Address,
    collection: o.collection as Address,
    tokenId: BigInt(o.tokenId),
    amount: BigInt(o.amount),
    expiresAt: BigInt(o.expiresAt),
    nonce: BigInt(o.nonce)
  };
  return {offer, sig: j.sig};
}
