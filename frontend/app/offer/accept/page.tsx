"use client";
import {useState} from "react";
import {useReadContract} from "wagmi";
import {type Address, type Hex} from "viem";
import {useAcceptOffer} from "@/hooks/useAcceptOffer";
import {useApproveNFT} from "@/hooks/useApproveNFT";
import {useTx} from "@/hooks/useTx";
import {TxBanner} from "@/components/TxBanner";
import {ADDR} from "@/lib/addresses";
import {ERC721Abi} from "@/lib/abi";
import {useAccount} from "wagmi";
import type {Offer} from "@/lib/eip712";

export default function AcceptOfferPage() {
  const {address} = useAccount();
  const [raw, setRaw] = useState("");
  const [tokenIdActual, setTokenIdActual] = useState("");
  const [parsed, setParsed] = useState<{offer: Offer; sig: Hex} | null>(null);
  const [parseError, setParseError] = useState<string | null>(null);

  const {data: approved} = useReadContract({
    address: parsed?.offer.collection,
    abi: ERC721Abi, functionName: "isApprovedForAll",
    args: address && parsed ? [address, ADDR.offer] : undefined,
    query: {enabled: !!parsed && !!address}
  });

  const {accept, isPending, error} = useAcceptOffer();
  const {approveAll, isPending: appPending, error: appErr} = useApproveNFT();
  const acceptTx = useTx();
  const approvalTx = useTx();

  const tryParse = (s: string) => {
    setParseError(null);
    try {
      const j = JSON.parse(s);
      const o = j.offer;
      const offer: Offer = {
        bidder: o.bidder,
        collection: o.collection,
        tokenId: BigInt(o.tokenId),
        amount: BigInt(o.amount),
        expiresAt: BigInt(o.expiresAt),
        nonce: BigInt(o.nonce)
      };
      setParsed({offer, sig: j.sig as Hex});
      setTokenIdActual(offer.tokenId.toString());
    } catch (e) {
      setParsed(null);
      setParseError((e as Error).message);
    }
  };

  return (
    <div className="space-y-4 max-w-xl">
      <h1 className="text-2xl md:text-3xl font-bold">Accept signed offer</h1>
      <p className="text-sm text-neutral-400">
        Paste the offer JSON (the bidder's clipboard payload). Then approve OfferBook and accept.
      </p>
      <textarea
        className="w-full h-40 bg-neutral-950 border border-neutral-700 rounded p-3 font-mono text-xs"
        value={raw}
        onChange={e => { setRaw(e.target.value); tryParse(e.target.value); }}
        placeholder='{"offer": {...}, "sig": "0x..."}'
      />
      {parseError && <div className="text-sm text-red-400">Invalid JSON: {parseError}</div>}

      {parsed && (
        <div className="border border-neutral-800 rounded p-4 space-y-2 text-sm">
          <div>Bidder: <span className="break-all">{parsed.offer.bidder}</span></div>
          <div>Collection: <span className="break-all">{parsed.offer.collection}</span></div>
          <div>Offer tokenId: {parsed.offer.tokenId.toString()} {parsed.offer.tokenId === 0n && <span className="text-yellow-400">(collection-wide)</span>}</div>
          <div>Amount: {(Number(parsed.offer.amount) / 1e18).toString()} C2FLR</div>
          <div>Expires: {new Date(Number(parsed.offer.expiresAt) * 1000).toLocaleString()}</div>
          <label className="block">
            Token ID you want to deliver
            <input className="mt-1 w-full bg-neutral-950 border border-neutral-700 rounded px-3 py-2"
              value={tokenIdActual} onChange={e => setTokenIdActual(e.target.value)} />
          </label>

          {!approved ? (
            <div className="space-y-2">
              <button
                className="w-full sm:w-auto px-4 py-2 rounded border border-yellow-700 hover:bg-yellow-900/30 text-sm disabled:opacity-50"
                disabled={appPending || approvalTx.isConfirming}
                onClick={async () => {
                  const h = await approveAll(parsed.offer.collection, ADDR.offer);
                  approvalTx.setHash(h as Hex);
                }}
              >{appPending ? "Confirm…" : approvalTx.isConfirming ? "Approving…" : "Approve OfferBook"}</button>
              <TxBanner hash={approvalTx.hash} isConfirming={approvalTx.isConfirming} isConfirmed={approvalTx.isConfirmed} error={appErr} label="Approval" />
            </div>
          ) : (
            <div className="space-y-2">
              <button
                className="w-full sm:w-auto px-4 py-2 rounded bg-emerald-600 hover:bg-emerald-500 disabled:opacity-50"
                disabled={isPending || acceptTx.isConfirming || !tokenIdActual}
                onClick={async () => {
                  const h = await accept(parsed.offer, parsed.sig, BigInt(tokenIdActual));
                  acceptTx.setHash(h as Hex);
                }}
              >{isPending ? "Confirm…" : acceptTx.isConfirming ? "Accepting…" : "Accept offer"}</button>
              <TxBanner hash={acceptTx.hash} isConfirming={acceptTx.isConfirming} isConfirmed={acceptTx.isConfirmed} error={error} label="Accept" />
            </div>
          )}
        </div>
      )}
    </div>
  );
}
