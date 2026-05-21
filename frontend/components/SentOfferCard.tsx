"use client";
import {useState} from "react";
import {formatEther, type Hex, type Address} from "viem";
import {useAccount, useReadContract} from "wagmi";
import {ADDR, CURRENCY_SYMBOL} from "@/lib/addresses";
import {OfferBookAbi} from "@/lib/abi";
import {useCancelOffer} from "@/hooks/useCancelOffer";
import {useTx} from "@/hooks/useTx";
import {TxBanner} from "@/components/TxBanner";
import type {BackendOffer} from "@/lib/api";

export function SentOfferCard({offer, onChanged}: {offer: BackendOffer; onChanged: () => void}) {
  const {address} = useAccount();
  const [err, setErr] = useState<string | null>(null);

  const nonceBig = (() => { try { return BigInt(offer.nonce); } catch { return null; } })();
  const bidderAddr = offer.bidder as Address;
  const amount = (() => { try { return formatEther(BigInt(offer.amount_wei)); } catch { return "?"; } })();
  const exp = new Date(offer.expires_at).toLocaleString();
  const tokenDisplay = offer.token_id || "0";

  const {data: nonceUsed} = useReadContract({
    address: ADDR.offer,
    abi: OfferBookAbi,
    functionName: "usedNonce",
    args: nonceBig !== null ? [bidderAddr, nonceBig] : undefined,
    query: {enabled: nonceBig !== null}
  });

  const {cancelOffer, isPending, error: writeError} = useCancelOffer();
  const cancelTx = useTx();

  const isBidder = !!address && address.toLowerCase() === bidderAddr.toLowerCase();

  return (
    <div className="rounded-xl border border-neutral-800 bg-neutral-900/40 p-4 text-sm">
      <div className="font-mono text-xs text-neutral-500 break-all">{offer.collection}</div>
      <div className="mt-2 text-lg font-semibold text-neutral-100">
        {amount} {CURRENCY_SYMBOL} {"·"} token {tokenDisplay}
        {tokenDisplay === "0" && <span className="text-xs text-amber-400"> (collection-wide)</span>}
      </div>
      <div className="mt-1 text-xs text-neutral-500">Expires {exp}</div>
      <div className="mt-1 text-xs text-neutral-500">Nonce {offer.nonce}</div>
      <div className="mt-1 text-xs text-neutral-500 capitalize">{offer.status}</div>
      {nonceUsed && <div className="mt-2 text-xs text-amber-400">Nonce used or cancelled on-chain.</div>}
      {!isBidder && (
        <div className="mt-2 text-xs text-yellow-600">Connect the bidder wallet to cancel on-chain.</div>
      )}
      <div className="mt-4 flex flex-wrap gap-2">
        {isBidder && nonceBig !== null && (
          <button
            type="button"
            className="rounded-lg border border-amber-800 px-3 py-1.5 text-xs text-amber-200 hover:bg-amber-950/40 disabled:opacity-40"
            disabled={isPending || cancelTx.isConfirming || !!nonceUsed}
            onClick={async () => {
              setErr(null);
              try {
                const h = await cancelOffer(nonceBig);
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
        <button
          type="button"
          className="rounded-lg border border-neutral-600 px-3 py-1.5 text-xs hover:border-rose-500/50"
          onClick={onChanged}
        >
          Dismiss
        </button>
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
