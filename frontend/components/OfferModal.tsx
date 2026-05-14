"use client";
import {useState} from "react";
import {parseEther, type Address, type Hex} from "viem";
import {useAccount, useReadContract} from "wagmi";
import {ADDR} from "@/lib/addresses";
import {OfferBookAbi} from "@/lib/abi";
import {useDeposit} from "@/hooks/useDeposit";
import {useSignOffer} from "@/hooks/useSignOffer";
import {useTx} from "@/hooks/useTx";
import {TxBanner} from "./TxBanner";
import type {Offer} from "@/lib/eip712";
import {appendSentOffer} from "@/lib/offerInbox";

export function OfferModal({coll, tokenId, onClose}: {coll: Address; tokenId: bigint; onClose: () => void}) {
  const {address} = useAccount();
  const [amount, setAmount] = useState("");
  const [days, setDays] = useState("3");
  const {deposit, isPending: depPending, error: depErr} = useDeposit();
  const {sign, isPending: sigPending, error: sigErr} = useSignOffer();
  const depositTx = useTx();
  const [signature, setSignature] = useState<string>("");
  const [copied, setCopied] = useState(false);

  const {data: existingDeposit} = useReadContract({
    address: ADDR.offer, abi: OfferBookAbi, functionName: "deposits",
    args: address ? [address] : undefined, query: {enabled: !!address}
  });
  const depositBalance = (existingDeposit as bigint | undefined) ?? 0n;

  const fund = async () => {
    if (!amount) return;
    const h = await deposit(parseEther(amount));
    depositTx.setHash(h as Hex);
  };

  const submitOffer = async () => {
    if (!address || !amount) return;
    const amt = parseEther(amount);
    const offer: Offer = {
      bidder: address as Address,
      collection: coll,
      tokenId,
      amount: amt,
      expiresAt: BigInt(Math.floor(Date.now() / 1000) + Number(days) * 86400),
      nonce: BigInt(Date.now())
    };
    const sig = await sign(offer);
    setSignature(sig);
    const payload = JSON.stringify({
      offer: {
        ...offer,
        tokenId: offer.tokenId.toString(),
        amount: offer.amount.toString(),
        expiresAt: offer.expiresAt.toString(),
        nonce: offer.nonce.toString()
      },
      sig
    }, null, 2);
    try {
      await navigator.clipboard.writeText(payload);
      setCopied(true);
    } catch {
      /* ignore */
    }
    appendSentOffer(payload);
    window.dispatchEvent(new Event("magicwebb-offers-changed"));
  };

  const needsDeposit = amount ? parseEther(amount) > depositBalance : false;

  return (
    <div className="fixed inset-0 bg-black/70 flex items-center justify-center z-50 p-4">
      <div className="bg-neutral-900 border border-neutral-700 rounded-lg p-4 sm:p-6 w-full max-w-md space-y-3 max-h-[90vh] overflow-y-auto">
        <div className="flex items-center justify-between">
          <h2 className="text-lg font-bold">Make offer</h2>
          <button className="text-sm text-neutral-400 hover:text-white" onClick={onClose}>✕</button>
        </div>
        <div>
          <label className="text-sm">Amount (C2FLR)</label>
          <input className="w-full bg-neutral-950 border border-neutral-700 rounded px-3 py-2"
            value={amount} onChange={e => setAmount(e.target.value)} placeholder="0.5" />
          <div className="text-xs text-neutral-500 mt-1">
            Your OfferBook deposit: {(Number(depositBalance) / 1e18).toString()} C2FLR
          </div>
        </div>
        <div>
          <label className="text-sm">Expires in (days)</label>
          <input className="w-full bg-neutral-950 border border-neutral-700 rounded px-3 py-2"
            value={days} onChange={e => setDays(e.target.value)} />
        </div>

        {needsDeposit && !depositTx.isConfirmed && (
          <div className="space-y-1">
            <div className="text-xs text-yellow-400">
              Deposit is below offer amount. Top up first so the owner can redeem.
            </div>
            <button
              className="w-full px-3 py-2 rounded border border-yellow-700 hover:bg-yellow-900/30 text-sm disabled:opacity-50"
              disabled={depPending || depositTx.isConfirming || !amount}
              onClick={fund}
            >{depPending ? "Confirm in wallet…" : depositTx.isConfirming ? "Funding…" : `Deposit ${amount} C2FLR`}</button>
            <TxBanner hash={depositTx.hash} isConfirming={depositTx.isConfirming} isConfirmed={depositTx.isConfirmed} error={depErr} label="Deposit" />
          </div>
        )}

        <button
          className="w-full px-3 py-2 rounded bg-emerald-600 hover:bg-emerald-500 disabled:opacity-50"
          disabled={sigPending || !amount || (needsDeposit && !depositTx.isConfirmed)}
          onClick={submitOffer}
        >{sigPending ? "Sign in wallet…" : "Sign offer"}</button>
        {sigErr && <div className="text-sm text-red-400">{sigErr.message.split("\n")[0]}</div>}
        {signature && (
          <div className="text-xs text-emerald-400 break-all">
            Offer signed{copied ? " and copied to clipboard" : ""}. Saved under{" "}
            <a href="/offers" className="underline text-emerald-300">
              Offers → Sent
            </a>
            ; share the JSON with the seller so they can import it under Offers → Received.
          </div>
        )}
      </div>
    </div>
  );
}
