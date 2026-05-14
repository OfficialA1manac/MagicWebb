"use client";
import {Suspense, useEffect, useMemo, useState} from "react";
import {useSearchParams} from "next/navigation";
import {SentOfferCard} from "@/components/SentOfferCard";
import {ReceivedOfferCard} from "@/components/ReceivedOfferCard";
import {
  appendReceivedOffer,
  parseOfferPayload,
  readReceivedOffers,
  readSentOffers
} from "@/lib/offerInbox";

function OffersContent() {
  const searchParams = useSearchParams();
  const [tab, setTab] = useState<"sent" | "received">(() =>
    searchParams.get("tab") === "received" ? "received" : "sent"
  );
  const [gen, setGen] = useState(0);
  const [importRaw, setImportRaw] = useState("");
  const [deliverId, setDeliverId] = useState("");
  const [importErr, setImportErr] = useState<string | null>(null);

  useEffect(() => {
    const bump = () => setGen(g => g + 1);
    window.addEventListener("magicwebb-offers-changed", bump);
    window.addEventListener("storage", bump);
    return () => {
      window.removeEventListener("magicwebb-offers-changed", bump);
      window.removeEventListener("storage", bump);
    };
  }, []);

  const sent = useMemo(() => {
    void gen;
    return readSentOffers();
  }, [gen]);

  const received = useMemo(() => {
    void gen;
    return readReceivedOffers();
  }, [gen]);

  const tryAddReceived = () => {
    setImportErr(null);
    try {
      const {offer} = parseOfferPayload(importRaw.trim());
      let deliver: string;
      if (offer.tokenId === 0n) {
        const t = deliverId.trim();
        if (!t || !/^\d+$/.test(t)) {
          setImportErr("Collection-wide offer: enter the token ID you are selling.");
          return;
        }
        if (t === "0") {
          setImportErr("Choose a token ID greater than 0 (token 0 is reserved for collection-wide semantics).");
          return;
        }
        deliver = t;
      } else {
        deliver = offer.tokenId.toString();
      }
      appendReceivedOffer(importRaw.trim(), deliver);
      setImportRaw("");
      setDeliverId("");
      setGen(g => g + 1);
      window.dispatchEvent(new Event("magicwebb-offers-changed"));
      setTab("received");
    } catch (e) {
      setImportErr((e as Error).message);
    }
  };

  return (
    <div className="space-y-8">
      <div>
        <h1 className="text-2xl font-bold md:text-3xl">Offers</h1>
        <p className="mt-2 max-w-3xl text-sm text-neutral-400">
          MagicWebb offers are EIP-712 signatures plus JSON you share off-chain. There is no on-chain inbox:{" "}
          <strong className="text-neutral-300">Sent</strong> lists offers you signed in this browser;{" "}
          <strong className="text-neutral-300">Received</strong> lists JSON you paste when a bidder sends you a payload.
          <span className="block mt-2 text-xs text-neutral-500">
            Dismiss removes a received entry from this device only. Cancel on-chain (bidder) burns the nonce so the offer
            cannot be accepted.
          </span>
        </p>
      </div>

      <div className="flex flex-wrap gap-2 border-b border-neutral-800 pb-1">
        <button
          type="button"
          className={`rounded-t-lg px-4 py-2 text-sm font-medium ${
            tab === "sent" ? "bg-emerald-950/50 text-emerald-200" : "text-neutral-400 hover:text-neutral-200"
          }`}
          onClick={() => setTab("sent")}
        >
          Offers sent
        </button>
        <button
          type="button"
          className={`rounded-t-lg px-4 py-2 text-sm font-medium ${
            tab === "received" ? "bg-emerald-950/50 text-emerald-200" : "text-neutral-400 hover:text-neutral-200"
          }`}
          onClick={() => setTab("received")}
        >
          Offers received
        </button>
      </div>

      {tab === "sent" && (
        <section className="space-y-4">
          {sent.length === 0 ? (
            <p className="text-sm text-neutral-500">
              No signed offers stored yet. Use <span className="text-neutral-300">Make offer</span> on a token page —
              each signed offer is saved here automatically.
            </p>
          ) : (
            <div className="grid gap-4 md:grid-cols-2">
              {sent.map(e => (
                <SentOfferCard key={e.id} entry={e} onChanged={() => setGen(g => g + 1)} />
              ))}
            </div>
          )}
        </section>
      )}

      {tab === "received" && (
        <section className="space-y-6">
          <div className="rounded-xl border border-neutral-800 bg-neutral-900/30 p-4 space-y-3">
            <h2 className="text-sm font-semibold text-neutral-200">Import an offer</h2>
            <p className="text-xs text-neutral-500">
              Paste the full JSON payload the bidder sent you. For collection-wide offers (tokenId 0), specify which token
              you are selling.
            </p>
            <textarea
              className="min-h-32 w-full rounded-lg border border-neutral-700 bg-neutral-950 p-3 font-mono text-xs"
              placeholder='{"offer": {...}, "sig": "0x..."}'
              value={importRaw}
              onChange={e => setImportRaw(e.target.value)}
            />
            <label className="block text-xs text-neutral-400">
              Token ID you will deliver (only if offer is collection-wide / tokenId 0)
              <input
                className="mt-1 w-full max-w-xs rounded border border-neutral-700 bg-neutral-950 px-3 py-2 font-mono text-sm"
                value={deliverId}
                onChange={e => setDeliverId(e.target.value)}
                placeholder="e.g. 42"
              />
            </label>
            {importErr && <div className="text-sm text-red-400">{importErr}</div>}
            <button
              type="button"
              className="rounded-lg bg-emerald-600 px-4 py-2 text-sm font-medium text-neutral-950 hover:bg-emerald-500"
              onClick={tryAddReceived}
            >
              Add to received
            </button>
          </div>

          {received.length === 0 ? (
            <p className="text-sm text-neutral-500">No imported offers yet.</p>
          ) : (
            <div className="grid gap-4 md:grid-cols-2">
              {received.map(e => (
                <ReceivedOfferCard key={e.id} entry={e} onChanged={() => setGen(g => g + 1)} />
              ))}
            </div>
          )}
        </section>
      )}
    </div>
  );
}

export default function OffersPage() {
  return (
    <Suspense fallback={<div className="text-sm text-neutral-500">Loading offers…</div>}>
      <OffersContent />
    </Suspense>
  );
}
