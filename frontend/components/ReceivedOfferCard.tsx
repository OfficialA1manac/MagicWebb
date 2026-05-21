"use client";
import {useState} from "react";
import {formatEther, type Hex, type Address} from "viem";
import {useAccount, useReadContract} from "wagmi";
import {ADDR, CURRENCY_SYMBOL} from "@/lib/addresses";
import {ERC721Abi, OfferBookAbi} from "@/lib/abi";
import {useAcceptOffer} from "@/hooks/useAcceptOffer";
import {useApproveNFT} from "@/hooks/useApproveNFT";
import {useTx} from "@/hooks/useTx";
import {TxBanner} from "@/components/TxBanner";
import type {BackendOffer} from "@/lib/api";
import type {Offer} from "@/lib/eip712";

export function ReceivedOfferCard({
  offer: row,
  onChanged
}: {
  offer: BackendOffer;
  onChanged: () => void;
}) {
  const {address} = useAccount();
  const [err, setErr] = useState<string | null>(null);

  const tokenIdActual: bigint = (() => {
    try { return row.token_id ? BigInt(row.token_id) : 0n; } catch { return 0n; }
  })();

  const offer: Offer = {
    bidder: row.bidder as Address,
    collection: row.collection as Address,
    tokenId: tokenIdActual,
    amount: (() => { try { return BigInt(row.amount_wei); } catch { return 0n; } })(),
    expiresAt: BigInt(Math.floor(new Date(row.expires_at).getTime() / 1000)),
    nonce: (() => { try { return BigInt(row.nonce); } catch { return 0n; } })(),
  };
  const sig = row.signature as Hex;

  const {data: owner} = useReadContract({
    address: offer.collection,
    abi: ERC721Abi,
    functionName: "ownerOf",
    args: [tokenIdActual],
    query: {enabled: tokenIdActual > 0n}
  });

  const {data: approved} = useReadContract({
    address: offer.collection,
    abi: ERC721Abi,
    functionName: "isApprovedForAll",
    args: address ? [address as Address, ADDR.offer] : undefined,
    query: {enabled: !!address}
  });

  const {data: nonceUsed} = useReadContract({
    address: ADDR.offer,
    abi: OfferBookAbi,
    functionName: "usedNonce",
    args: [offer.bidder, offer.nonce],
  });

  const {accept, isPending, error: acceptErr} = useAcceptOffer();
  const {approveAll, isPending: appPending, error: appErr} = useApproveNFT();
  const acceptTx = useTx();
  const approvalTx = useTx();

  const now = BigInt(Math.floor(Date.now() / 1000));
  const expired = now > offer.expiresAt;
  const isOwner = !!address && !!owner && typeof owner === "string" &&
    address.toLowerCase() === (owner as string).toLowerCase();
  const invalidDeliver = tokenIdActual === 0n;

  return (
    <div className="rounded-xl border border-neutral-800 bg-neutral-900/40 p-4 text-sm space-y-3">
      <div className="font-mono text-xs text-neutral-500 break-all">{offer.collection}</div>
      <div className="text-lg font-semibold text-emerald-400">
        {formatEther(offer.amount)} {CURRENCY_SYMBOL}
      </div>
      <div className="text-xs text-neutral-400">
        Bidder <span className="break-all font-mono text-neutral-300">{offer.bidder}</span>
      </div>
      <div className="text-xs text-neutral-500">
        Token #{tokenIdActual.toString()}
        {row.token_id === "" && <span className="text-amber-400"> (collection-wide)</span>}
      </div>
      <div className="text-xs text-neutral-500">
        Expires {new Date(row.expires_at).toLocaleString()}
      </div>
      <div className="text-xs text-neutral-500 capitalize">{row.status}</div>

      {invalidDeliver && <p className="text-xs text-red-400">Collection-wide offer — token ID required to accept.</p>}
      {expired && <p className="text-xs text-amber-400">Offer expiry has passed.</p>}
      {nonceUsed && <p className="text-xs text-amber-400">This nonce was already used or cancelled.</p>}
      {!address && <p className="text-xs text-yellow-600">Connect seller wallet to accept.</p>}
      {address && !isOwner && !invalidDeliver && (
        <p className="text-xs text-red-400">Connected wallet is not the owner of token #{tokenIdActual.toString()}.</p>
      )}

      <div className="flex flex-wrap gap-2 pt-2">
        <button
          type="button"
          className="rounded-lg border border-neutral-600 px-3 py-1.5 text-xs hover:border-rose-500/50"
          onClick={onChanged}
        >
          Dismiss
        </button>

        {isOwner && !expired && !nonceUsed && !invalidDeliver && (
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
                    onChanged();
                  } catch { /* wagmi handles display */ }
                }}
              >
                {isPending ? "Wallet…" : acceptTx.isConfirming ? "Accepting…" : "Accept offer"}
              </button>
            )}
          </>
        )}
      </div>

      {err && <div className="mt-2 text-xs text-red-400">{err}</div>}
      <TxBanner hash={approvalTx.hash} isConfirming={approvalTx.isConfirming} isConfirmed={approvalTx.isConfirmed} error={appErr ?? approvalTx.txError} label="Approval" />
      <TxBanner hash={acceptTx.hash} isConfirming={acceptTx.isConfirming} isConfirmed={acceptTx.isConfirmed} error={acceptErr ?? acceptTx.txError} label="Accept" />
    </div>
  );
}
