"use client";
import {useEffect, useState} from "react";
import {formatEther, type Hex} from "viem";
import {useAccount, useReadContract} from "wagmi";
import {ADDR} from "@/lib/addresses";
import {ERC721Abi, OfferBookAbi} from "@/lib/abi";
import {useAcceptOffer} from "@/hooks/useAcceptOffer";
import {useApproveNFT} from "@/hooks/useApproveNFT";
import {useTx} from "@/hooks/useTx";
import {TxBanner} from "@/components/TxBanner";
import type {ReceivedOfferEntry} from "@/lib/offerInbox";
import {parseOfferPayload, removeReceivedOffer} from "@/lib/offerInbox";

export function ReceivedOfferCard({
  entry,
  onChanged
}: {
  entry: ReceivedOfferEntry;
  onChanged: () => void;
}) {
  const {address} = useAccount();
  const [parseErr, setParseErr] = useState<string | null>(null);
  const parsed = (() => {
    try { return parseOfferPayload(entry.raw); } catch { return null; }
  })();

  useEffect(() => {
    setParseErr(parsed ? null : "Invalid JSON");
  }, [parsed, entry.raw]);

  let tokenIdActual: bigint;
  try {
    tokenIdActual = BigInt(entry.deliverTokenId);
  } catch {
    tokenIdActual = 0n;
  }

  const {data: owner} = useReadContract({
    address: parsed?.offer.collection,
    abi: ERC721Abi,
    functionName: "ownerOf",
    args: parsed ? [tokenIdActual] : undefined,
    query: {enabled: !!parsed && tokenIdActual > 0n}
  });

  const {data: approved} = useReadContract({
    address: parsed?.offer.collection,
    abi: ERC721Abi,
    functionName: "isApprovedForAll",
    args: address && parsed ? [address, ADDR.offer] : undefined,
    query: {enabled: !!parsed && !!address}
  });

  const {data: nonceUsed} = useReadContract({
    address: ADDR.offer,
    abi: OfferBookAbi,
    functionName: "usedNonce",
    args: parsed ? [parsed.offer.bidder, parsed.offer.nonce] : undefined,
    query: {enabled: !!parsed}
  });

  const {accept, isPending, error: acceptErr} = useAcceptOffer();
  const {approveAll, isPending: appPending, error: appErr} = useApproveNFT();
  const acceptTx = useTx();
  const approvalTx = useTx();

  useEffect(() => {
    if (acceptTx.isConfirmed) {
      removeReceivedOffer(entry.id);
      onChanged();
      window.dispatchEvent(new Event("magicwebb-offers-changed"));
    }
  }, [acceptTx.isConfirmed, entry.id, onChanged]);

  if (!parsed) {
    return (
      <div className="rounded-xl border border-red-900/40 bg-red-950/20 p-4 text-sm text-red-300">
        {parseErr ?? "Invalid offer"}
        <button
          type="button"
          className="mt-2 block text-xs underline"
          onClick={() => { removeReceivedOffer(entry.id); onChanged(); }}
        >
          Remove
        </button>
      </div>
    );
  }

  const {offer, sig} = parsed;
  const now = BigInt(Math.floor(Date.now() / 1000));
  const expired = now > offer.expiresAt;
  const isOwner =
    !!address && !!owner && typeof owner === "string" &&
    address.toLowerCase() === owner.toLowerCase();
  const wrongToken = offer.tokenId !== 0n && offer.tokenId !== tokenIdActual;
  const invalidDeliver = tokenIdActual === 0n;

  return (
    <div className="rounded-xl border border-neutral-800 bg-neutral-900/40 p-4 text-sm space-y-3">
      {invalidDeliver && (
        <p className="text-xs text-red-400">Invalid stored token ID. Remove and re-import with a valid ID.</p>
      )}
      <div className="font-mono text-xs text-neutral-500 break-all">{offer.collection}</div>
      <div className="text-lg font-semibold text-emerald-400">{formatEther(offer.amount)} C2FLR</div>
      <div className="text-xs text-neutral-400">
        Bidder <span className="break-all font-mono text-neutral-300">{offer.bidder}</span>
      </div>
      <div className="text-xs text-neutral-500">
        Deliver token #{tokenIdActual.toString()}
        {offer.tokenId === 0n && <span className="text-amber-400"> (collection-wide offer)</span>}
      </div>
      <div className="text-xs text-neutral-500">
        Expires {new Date(Number(offer.expiresAt) * 1000).toLocaleString()}
      </div>

      {wrongToken && <p className="text-xs text-red-400">Stored token ID does not match this offer.</p>}
      {expired && <p className="text-xs text-amber-400">Offer expiry has passed.</p>}
      {nonceUsed && <p className="text-xs text-amber-400">This nonce was already used or cancelled.</p>}
      {!address && <p className="text-xs text-yellow-600">Connect the seller wallet to accept.</p>}
      {address && !isOwner && !wrongToken && !invalidDeliver && (
        <p className="text-xs text-red-400">Connected wallet is not the owner of token #{tokenIdActual.toString()}.</p>
      )}

      <div className="flex flex-wrap gap-2 pt-2">
        <button
          type="button"
          className="rounded-lg border border-neutral-600 px-3 py-1.5 text-xs hover:border-rose-500/50"
          onClick={() => { removeReceivedOffer(entry.id); onChanged(); window.dispatchEvent(new Event("magicwebb-offers-changed")); }}
        >
          Dismiss
        </button>

        {isOwner && !wrongToken && !expired && !nonceUsed && !invalidDeliver && (
          <>
            {!approved ? (
              <button
                type="button"
                className="rounded-lg border border-yellow-700 px-3 py-1.5 text-xs text-yellow-200 hover:bg-yellow-950/30 disabled:opacity-40"
                disabled={appPending || approvalTx.isConfirming}
                onClick={async () => {
                  try {
                    const h = await approveAll(offer.collection, ADDR.offer);
                    approvalTx.setHash(h as Hex);
                  } catch { /* wagmi handles display */ }
                }}
              >
                {appPending ? "Wallet…" : approvalTx.isConfirming ? "Approving…" : "Approve OfferBook"}
              </button>
            ) : (
              <button
                type="button"
                className="rounded-lg bg-emerald-600 px-3 py-1.5 text-xs font-medium text-neutral-950 hover:bg-emerald-500 disabled:opacity-40"
                disabled={isPending || acceptTx.isConfirming}
                onClick={async () => {
                  try {
                    const h = await accept(offer, sig, tokenIdActual);
                    acceptTx.setHash(h as Hex);
                  } catch { /* wagmi handles display */ }
                }}
              >
                {isPending ? "Wallet…" : acceptTx.isConfirming ? "Accepting…" : "Accept offer"}
              </button>
            )}
          </>
        )}
      </div>

      <TxBanner
        hash={approvalTx.hash}
        isConfirming={approvalTx.isConfirming}
        isConfirmed={approvalTx.isConfirmed}
        error={appErr ?? approvalTx.txError}
        label="Approval"
      />
      <TxBanner
        hash={acceptTx.hash}
        isConfirming={acceptTx.isConfirming}
        isConfirmed={acceptTx.isConfirmed}
        error={acceptErr ?? acceptTx.txError}
        label="Accept"
      />
    </div>
  );
}
