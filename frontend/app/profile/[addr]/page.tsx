"use client";
import {useParams} from "next/navigation";
import {useAccount} from "wagmi";
import {formatEther, type Address} from "viem";
import {useWithdrawRefund} from "@/hooks/useWithdrawRefund";

export default function Profile() {
  const {addr} = useParams<{addr: string}>();
  const {address} = useAccount();
  const target = (addr === "me" ? address : (addr as Address)) as Address | undefined;
  const {pending, withdraw, isPending, error} = useWithdrawRefund(target);

  if (!target) return <div className="text-sm text-neutral-400">Connect a wallet to see your profile.</div>;

  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-2xl font-bold">Profile</h1>
        <div className="text-sm text-neutral-400 break-all">{target}</div>
      </div>

      <section className="border border-neutral-800 rounded p-4 space-y-2">
        <h2 className="font-semibold">Pending refunds (auction outbids)</h2>
        <div className="text-sm">{formatEther(pending)} C2FLR</div>
        <button
          className="px-4 py-2 rounded bg-emerald-600 hover:bg-emerald-500 disabled:opacity-50"
          disabled={isPending || pending === 0n} onClick={() => withdraw()}
        >{isPending ? "Withdrawing..." : "Withdraw refund"}</button>
        {error && <div className="text-sm text-red-400">{error.message}</div>}
      </section>

      <section className="border border-neutral-800 rounded p-4 text-sm text-neutral-400">
        Listings, owned tokens, and active offers will appear once the indexer is wired up.
      </section>
    </div>
  );
}
