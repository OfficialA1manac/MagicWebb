"use client";
import {useEffect, useState} from "react";
import {parseEther, formatEther, type Address, type Hex} from "viem";
import {useReadContract, useAccount} from "wagmi";
import Link from "next/link";
import {ADDR} from "@/lib/addresses";
import {ERC721Abi} from "@/lib/abi";
import {useApproveNFT} from "@/hooks/useApproveNFT";
import {useList} from "@/hooks/useList";
import {useCancelListing} from "@/hooks/useCancelListing";
import {useTx} from "@/hooks/useTx";
import {useTokenImage} from "@/hooks/useTokenImage";
import {TxBanner} from "./TxBanner";
import type {ActiveListing} from "@/lib/marketIndex";

function NftImage({src, id, alt}: {src?: string; id: bigint; alt: string}) {
  const [err, setErr] = useState(false);
  if (src && !err) {
    return (
      <img
        src={src}
        alt={alt}
        className="w-full aspect-square object-cover"
        onError={() => setErr(true)}
      />
    );
  }
  return (
    <div className="w-full aspect-square flex items-center justify-center bg-neutral-800 text-3xl font-mono text-neutral-600 select-none">
      #{id.toString()}
    </div>
  );
}

function ListForm({
  coll, tokenId, onClose
}: {
  coll: Address;
  tokenId: bigint;
  onClose: () => void;
}) {
  const {address} = useAccount();
  const {approveAll, isPending: appPending} = useApproveNFT();
  const {list, isPending: listPending} = useList();
  const {hash, setHash, isConfirming, isConfirmed, txError, reset} = useTx();
  const [price, setPrice] = useState("");
  const [days, setDays] = useState("7");

  const {data: isApproved, refetch: refetchApproval} = useReadContract({
    address: coll, abi: ERC721Abi, functionName: "isApprovedForAll",
    args: address ? [address, ADDR.marketplace] : undefined,
    query: {enabled: !!address}
  });

  useEffect(() => {
    if (isConfirmed && !isApproved) refetchApproval();
  }, [isConfirmed, isApproved, refetchApproval]);

  const parsedPrice = (() => { try { return price ? parseEther(price) : null; } catch { return null; } })();
  const priceOk = parsedPrice !== null && parsedPrice > 0n;
  const daysNum = parseInt(days, 10);
  const daysOk = !isNaN(daysNum) && daysNum > 0 && daysNum <= 365;

  const submit = async () => {
    try {
      if (!isApproved) {
        const h = await approveAll(coll, ADDR.marketplace);
        setHash(h as Hex);
        return;
      }
      if (!priceOk || !daysOk) return;
      const expiresAt = BigInt(Math.floor(Date.now() / 1000) + daysNum * 86400);
      const h = await list(coll, tokenId, parsedPrice!, expiresAt);
      setHash(h as Hex);
    } catch {}
  };

  const busy = appPending || listPending || isConfirming;

  return (
    <div className="space-y-2 pt-2 border-t border-neutral-800">
      <input
        className="w-full rounded border border-neutral-700 bg-neutral-950 px-2 py-1.5 text-xs"
        placeholder="Price in C2FLR"
        value={price}
        onChange={e => setPrice(e.target.value)}
      />
      <div className="flex gap-2">
        <select
          className="flex-1 rounded border border-neutral-700 bg-neutral-950 px-2 py-1.5 text-xs"
          value={days}
          onChange={e => setDays(e.target.value)}
        >
          {[["1","1 day"],["3","3 days"],["7","7 days"],["30","30 days"],["90","90 days"]].map(([v,l]) => (
            <option key={v} value={v}>{l}</option>
          ))}
        </select>
        <button
          className="flex-1 rounded bg-emerald-600 hover:bg-emerald-500 px-2 py-1.5 text-xs font-medium disabled:opacity-50"
          disabled={busy || (!!isApproved && (!priceOk || !daysOk))}
          onClick={submit}
        >
          {!isApproved
            ? (appPending ? "Approving…" : "Approve")
            : (listPending ? "Confirm…" : isConfirming ? "Listing…" : "List")}
        </button>
      </div>
      <TxBanner hash={hash} isConfirming={isConfirming} isConfirmed={isConfirmed} error={txError} label={isApproved ? "List" : "Approve"} />
      <div className="flex gap-2">
        {isConfirmed && (
          <button className="flex-1 text-xs text-emerald-400 hover:text-emerald-300" onClick={() => { reset(); onClose(); }}>
            Done
          </button>
        )}
        <button className="flex-1 text-xs text-neutral-600 hover:text-neutral-400" onClick={onClose}>
          Cancel
        </button>
      </div>
    </div>
  );
}

export function ProfileNftCard({
  coll,
  id,
  collectionName,
  listing,
  hidden,
  onToggleHide,
  onActionDone,
}: {
  coll: Address;
  id: bigint;
  collectionName?: string;
  listing?: ActiveListing;
  hidden: boolean;
  onToggleHide: () => void;
  onActionDone?: () => void;
}) {
  const [listOpen, setListOpen] = useState(false);
  const {cancel, isPending: cancelPending} = useCancelListing();
  const cancelTx = useTx();
  const {data: imageUrl} = useTokenImage(coll, id);

  const isListed = !!listing;
  const title = collectionName ?? "Unknown";

  const handleCancel = async () => {
    try {
      const h = await cancel(coll, id);
      cancelTx.setHash(h as Hex);
    } catch {}
  };

  useEffect(() => {
    if (cancelTx.isConfirmed) onActionDone?.();
  }, [cancelTx.isConfirmed, onActionDone]);

  return (
    <div className={`flex flex-col rounded-xl border border-neutral-800 bg-neutral-900/40 overflow-hidden transition hover:border-neutral-600 ${hidden ? "opacity-40 grayscale" : ""}`}>
      <Link href={`/token/${coll}/${id.toString()}`} className="block shrink-0">
        <NftImage src={imageUrl} id={id} alt={`${title} #${id}`} />
      </Link>

      <div className="flex flex-col flex-1 p-3 gap-2">
        <div>
          <div className="text-xs text-neutral-500 truncate">{title}</div>
          <div className="text-sm font-mono font-semibold text-neutral-100">#{id.toString()}</div>
          {isListed && (
            <div className="mt-0.5 text-xs font-medium text-emerald-400">
              Listed · {formatEther(listing.price)} C2FLR
            </div>
          )}
          {!isListed && (
            <div className="mt-0.5 text-xs text-neutral-600">Not listed</div>
          )}
        </div>

        {!listOpen && (
          <div className="flex gap-1.5 mt-auto">
            <button
              type="button"
              className="flex-1 rounded border border-neutral-700 px-2 py-1.5 text-xs hover:border-neutral-500 hover:bg-neutral-800/50 transition"
              onClick={onToggleHide}
            >
              {hidden ? "Show" : "Hide"}
            </button>
            {isListed ? (
              <button
                type="button"
                className="flex-1 rounded border border-red-800/50 bg-red-950/20 px-2 py-1.5 text-xs text-red-300 hover:bg-red-950/40 disabled:opacity-50 transition"
                disabled={cancelPending || cancelTx.isConfirming}
                onClick={handleCancel}
              >
                {cancelPending ? "Confirm…" : cancelTx.isConfirming ? "Unlisting…" : "Unlist"}
              </button>
            ) : (
              <button
                type="button"
                className="flex-1 rounded border border-emerald-800/50 bg-emerald-950/20 px-2 py-1.5 text-xs text-emerald-300 hover:bg-emerald-950/40 transition"
                onClick={() => setListOpen(true)}
              >
                List
              </button>
            )}
          </div>
        )}

        {cancelTx.hash && !listOpen && (
          <TxBanner
            hash={cancelTx.hash}
            isConfirming={cancelTx.isConfirming}
            isConfirmed={cancelTx.isConfirmed}
            error={cancelTx.txError}
            label="Unlist"
          />
        )}

        {listOpen && (
          <ListForm
            coll={coll}
            tokenId={id}
            onClose={() => setListOpen(false)}
          />
        )}
      </div>
    </div>
  );
}
