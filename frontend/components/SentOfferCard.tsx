"use client";
import {useEffect, useState} from "react";
import {formatEther, type Hex} from "viem";
import {useAccount, useReadContract} from "wagmi";
import {ADDR} from "@/lib/addresses";
import {OfferBookAbi} from "@/lib/abi";
import {useCancelOffer} from "@/hooks/useCancelOffer";
import {useTx} from "@/hooks/useTx";
import {TxBanner} from "@/components/TxBanner";
import type {SentOfferEntry} from "@/lib/offerInbox";
import {parseOfferPayload, removeSentOffer} from "@/lib/offerInbox";

export function SentOfferCard({entry, onChanged}: {entry: SentOfferEntry; onChanged: () => void}) {
  const {address} = useAccount();
  const [err, setErr] = useState<string | null>(null);

  const parsed = (() => {
    try { return parseOfferPayload(entry.raw); } catch { return null; }
  })();

  const offerSummary = parsed ? {
    amount: formatEther(parsed.offer.amount),
    coll: parsed.offer.collection,
    tid: parsed.offer.tokenId.toString(),
    exp: new Date(Number(parsed.offer.expiresAt) * 1000).toLocaleString(),
    nonce: parsed.offer.nonce.toString()
  } : null;

  const {data: nonceUsed} = useReadContract({
    address: ADDR.offer,
    abi: OfferBookAbi,
    functionName: "usedNonce",
    args: parsed ? [parsed.offer.bidder, parsed.offer.nonce] : undefined,
    query: {enabled: !!parsed}
  });

  const {cancelOffer, isPending, error: writeError} = useCancelOffer();
  const cancelTx = useTx();

  const isBidder =
    !!address && !!parsed && address.toLowerCase() === parsed.offer.bidder.toLowerCase();

  const copy = async () => {
    try {
      await navigator.clipboard.writeText(entry.raw);
    } catch {
      setErr("Could not copy to clipboard");
    }
  };

  useEffect(() => {
    if (cancelTx.isConfirmed) onChanged();
  }, [cancelTx.isConfirmed, onChanged]);

  return (
    <div className="rounded-xl border border-neutral-800 bg-neutral-900/40 p-4 text-sm">
      {!offerSummary ? (
        <div className="text-red-400">Invalid stored payload</div>
      ) : (
        <>
          <div className="font-mono text-xs text-neutral-500 break-all">{offerSummary.coll}</div>
          <div className="mt-2 text-lg font-semibold text-neutral-100">
            {offerSummary.amount} C2FLR {"·"} token {offerSummary.tid}
            {offerSummary.tid === "0" && <span className="text-xs text-amber-400"> (collection-wide)</span>}
          </div>
          <div className="mt-1 text-xs text-neutral-500">Expires {offerSummary.exp}</div>
          <div className="mt-1 text-xs text-neutral-500">Nonce {offerSummary.nonce}</div>
          {nonceUsed && <div className="mt-2 text-xs text-amber-400">Nonce used or cancelled on-chain.</div>}
          {!isBidder && (
            <div className="mt-2 text-xs text-yellow-600">Connect the bidder wallet to cancel this offer on-chain.</div>
          )}
        </>
      )}
      <div className="mt-4 flex flex-wrap gap-2">
        <button
          type="button"
          className="rounded-lg border border-neutral-600 px-3 py-1.5 text-xs hover:border-emerald-500/50"
          onClick={copy}
        >
          Copy JSON
        </button>
        <button
          type="button"
          className="rounded-lg border border-neutral-600 px-3 py-1.5 text-xs hover:border-rose-500/50"
          onClick={() => {
            removeSentOffer(entry.id);
            onChanged();
            window.dispatchEvent(new Event("magicwebb-offers-changed"));
          }}
        >
          Remove from list
        </button>
        {parsed && isBidder && (
          <button
            type="button"
            className="rounded-lg border border-amber-800 px-3 py-1.5 text-xs text-amber-200 hover:bg-amber-950/40 disabled:opacity-40"
            disabled={isPending || cancelTx.isConfirming || !!nonceUsed}
            onClick={async () => {
              setErr(null);
              try {
                const n = parsed.offer.nonce;
                const h = await cancelOffer(n);
                cancelTx.setHash(h as Hex);
              } catch (e) {
                const msg = (e as Error)?.message?.split("\n")[0];
                if (msg && !msg.toLowerCase().includes("rejected")) setErr(msg);
              }
            }}
          >
            {isPending ? "Wallet…" : cancelTx.isConfirming ? "Cancelling…" : "Cancel on-chain (burn nonce)"}
          </button>
        )}
      </div>
      {err && <div className="mt-2 text-xs text-red-400">{err}</div>}
      <div className="mt-2">
        <TxBanner
          hash={cancelTx.hash}
          isConfirming={cancelTx.isConfirming}
          isConfirmed={cancelTx.isConfirmed}
          error={writeError ?? cancelTx.txError}
          label="Cancel offer"
        />
      </div>
    </div>
  );
}
