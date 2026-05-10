"use client";
import {useState} from "react";
import {parseEther, type Address} from "viem";
import {useAccount} from "wagmi";
import {useDeposit} from "@/hooks/useDeposit";
import {useSignOffer} from "@/hooks/useSignOffer";
import type {Offer} from "@/lib/eip712";

export function OfferModal({coll, tokenId, onClose}: {coll: Address; tokenId: bigint; onClose: () => void}) {
  const {address} = useAccount();
  const [amount, setAmount] = useState("");
  const [days, setDays] = useState("3");
  const {deposit, isPending: depPending} = useDeposit();
  const {sign, isPending: sigPending} = useSignOffer();
  const [signature, setSignature] = useState<string>("");

  const submit = async () => {
    if (!address) return;
    const amt = parseEther(amount);
    await deposit(amt);
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
    // Off-chain dispatch: indexer (or seller's UI) reads this. For now, surface to user.
    navigator.clipboard.writeText(JSON.stringify({offer: {
      ...offer,
      tokenId: offer.tokenId.toString(),
      amount: offer.amount.toString(),
      expiresAt: offer.expiresAt.toString(),
      nonce: offer.nonce.toString()
    }, sig}, null, 2));
  };

  return (
    <div className="fixed inset-0 bg-black/70 flex items-center justify-center z-50">
      <div className="bg-neutral-900 border border-neutral-700 rounded-lg p-6 w-96 space-y-3">
        <h2 className="text-lg font-bold">Make offer</h2>
        <div>
          <label className="text-sm">Amount (C2FLR)</label>
          <input className="w-full bg-neutral-950 border border-neutral-700 rounded px-3 py-2"
            value={amount} onChange={e => setAmount(e.target.value)} />
        </div>
        <div>
          <label className="text-sm">Expiry (days)</label>
          <input className="w-full bg-neutral-950 border border-neutral-700 rounded px-3 py-2"
            value={days} onChange={e => setDays(e.target.value)} />
        </div>
        {signature && (
          <div className="text-xs text-emerald-400 break-all">
            Offer signed + copied to clipboard. Send to token owner.
          </div>
        )}
        <div className="flex gap-2 justify-end">
          <button className="px-3 py-2 text-sm" onClick={onClose}>Close</button>
          <button
            className="px-3 py-2 rounded bg-emerald-600 hover:bg-emerald-500 disabled:opacity-50"
            disabled={depPending || sigPending || !amount}
            onClick={submit}
          >{depPending ? "Depositing..." : sigPending ? "Signing..." : "Deposit + sign"}</button>
        </div>
      </div>
    </div>
  );
}
