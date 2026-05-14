"use client";
import Link from "next/link";
import {useParams} from "next/navigation";
import {useAccount, useReadContract} from "wagmi";
import {formatEther, type Address, type Hex} from "viem";
import {useEffect, useState} from "react";
import {ADDR} from "@/lib/addresses";
import {OfferBookAbi} from "@/lib/abi";
import {useWithdrawRefund} from "@/hooks/useWithdrawRefund";
import {useWithdrawDeposit} from "@/hooks/useWithdrawDeposit";
import {useTx} from "@/hooks/useTx";
import {TxBanner} from "@/components/TxBanner";

function shortAddr(a: string) {
  return `${a.slice(0, 6)}…${a.slice(-4)}`;
}

export default function Profile() {
  const {addr} = useParams<{addr: string}>();
  const {address} = useAccount();
  const target = (addr === "me" ? address : (addr as Address)) as Address | undefined;
  const {pending, withdraw, refetch, isPending, error} = useWithdrawRefund(target);
  const {withdraw: withdrawDep, isPending: depPending, error: depErr} = useWithdrawDeposit();
  const [amount, setAmount] = useState("");

  const {data: offerBalance, refetch: refetchOffer} = useReadContract({
    address: ADDR.offer,
    abi: OfferBookAbi,
    functionName: "deposits",
    args: target ? [target] : undefined,
    query: {enabled: !!target}
  });

  const refundTx = useTx();
  const depositTx = useTx();
  useEffect(() => {
    if (refundTx.isConfirmed) refetch();
  }, [refundTx.isConfirmed, refetch]);
  useEffect(() => {
    if (depositTx.isConfirmed) refetchOffer();
  }, [depositTx.isConfirmed, refetchOffer]);

  if (!target) {
    return (
      <div className="mx-auto max-w-lg rounded-xl border border-neutral-800 bg-neutral-900/40 p-8 text-center">
        <p className="text-neutral-300">Connect your wallet to view your MagicWebb profile.</p>
        <p className="mt-2 text-sm text-neutral-500">Use the Connect button in the header.</p>
      </div>
    );
  }

  const depBal = (offerBalance as bigint | undefined) ?? 0n;

  return (
    <div className="space-y-8">
      <section className="overflow-hidden rounded-2xl border border-neutral-800 bg-gradient-to-br from-neutral-900/90 to-neutral-950 p-6 sm:p-8">
        <div className="flex flex-col gap-4 sm:flex-row sm:items-center sm:justify-between">
          <div>
            <p className="text-xs font-medium uppercase tracking-wider text-emerald-400/90">Profile</p>
            <h1 className="mt-1 text-2xl font-bold sm:text-3xl">Your hub on Coston2</h1>
            <p className="mt-2 max-w-xl text-sm text-neutral-400">
              Withdraw auction refunds, manage OfferBook deposits, and jump to listing or discovery — everything here
              talks to the chain through your wallet.
            </p>
          </div>
          <div className="rounded-lg border border-neutral-700 bg-neutral-950/80 px-4 py-3 font-mono text-sm text-neutral-200">
            {shortAddr(target)}
          </div>
        </div>

        <div className="mt-6 grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
          <Link
            href="/list"
            className="group rounded-xl border border-emerald-900/40 bg-emerald-950/20 p-4 transition hover:border-emerald-500/50 hover:bg-emerald-950/35"
          >
            <div className="text-sm font-semibold text-emerald-300 group-hover:text-emerald-200">List NFT</div>
            <p className="mt-1 text-xs text-neutral-500">Fixed price or auction for a token you own.</p>
          </Link>
          <Link
            href="/search"
            className="rounded-xl border border-neutral-800 bg-neutral-950/50 p-4 transition hover:border-neutral-600"
          >
            <div className="text-sm font-semibold text-neutral-200">Search</div>
            <p className="mt-1 text-xs text-neutral-500">Open a collection and token by address.</p>
          </Link>
          <Link
            href="/auctions"
            className="rounded-xl border border-neutral-800 bg-neutral-950/50 p-4 transition hover:border-neutral-600"
          >
            <div className="text-sm font-semibold text-neutral-200">Auctions</div>
            <p className="mt-1 text-xs text-neutral-500">Browse live English auctions.</p>
          </Link>
          <Link
            href="/offer/accept"
            className="rounded-xl border border-neutral-800 bg-neutral-950/50 p-4 transition hover:border-neutral-600"
          >
            <div className="text-sm font-semibold text-neutral-200">Accept offer</div>
            <p className="mt-1 text-xs text-neutral-500">Redeem a signed EIP-712 offer on-chain.</p>
          </Link>
        </div>
      </section>

      <div className="grid gap-6 lg:grid-cols-2">
        <section className="flex flex-col rounded-xl border border-neutral-800 bg-neutral-900/30 p-5">
          <h2 className="text-lg font-semibold text-neutral-100">Auction refunds</h2>
          <p className="mt-1 text-xs text-neutral-500">Outbid amounts use a pull pattern — claim here when you have a balance.</p>
          <div className="mt-4 flex items-baseline gap-2">
            <span className="text-3xl font-mono font-semibold tracking-tight">{formatEther(pending)}</span>
            <span className="text-sm text-neutral-500">C2FLR</span>
          </div>
          <button
            className="mt-4 w-full rounded-lg bg-emerald-600 py-2.5 text-sm font-medium text-neutral-950 hover:bg-emerald-500 disabled:opacity-40 sm:w-auto sm:px-6"
            disabled={isPending || refundTx.isConfirming || pending === 0n}
            onClick={async () => {
              const h = await withdraw();
              refundTx.setHash(h as Hex);
            }}
          >
            {isPending ? "Confirm in wallet…" : refundTx.isConfirming ? "Withdrawing…" : "Withdraw refund"}
          </button>
          <div className="mt-3">
            <TxBanner
              hash={refundTx.hash}
              isConfirming={refundTx.isConfirming}
              isConfirmed={refundTx.isConfirmed}
              error={error}
              label="Refund withdrawal"
            />
          </div>
        </section>

        <section className="flex flex-col rounded-xl border border-neutral-800 bg-neutral-900/30 p-5">
          <h2 className="text-lg font-semibold text-neutral-100">OfferBook deposit</h2>
          <p className="mt-1 text-xs text-neutral-500">Escrow for signed offers. Withdraw unused balance anytime.</p>
          <div className="mt-4 flex items-baseline gap-2">
            <span className="text-3xl font-mono font-semibold tracking-tight">{formatEther(depBal)}</span>
            <span className="text-sm text-neutral-500">C2FLR</span>
          </div>
          <div className="mt-4 flex flex-col gap-2 sm:flex-row">
            <input
              className="min-w-0 flex-1 rounded-lg border border-neutral-700 bg-neutral-950 px-3 py-2 text-sm"
              placeholder="Amount to withdraw (C2FLR)"
              value={amount}
              onChange={e => setAmount(e.target.value)}
            />
            <button
              className="rounded-lg border border-neutral-600 px-4 py-2 text-sm font-medium hover:border-emerald-500/50 hover:bg-neutral-800 disabled:opacity-40"
              disabled={!amount || depPending || depositTx.isConfirming}
              onClick={async () => {
                const v = BigInt(Math.floor(Number(amount) * 1e18));
                const h = await withdrawDep(v);
                depositTx.setHash(h as Hex);
              }}
            >
              {depPending ? "Confirm…" : depositTx.isConfirming ? "Withdrawing…" : "Withdraw from deposit"}
            </button>
          </div>
          <div className="mt-3">
            <TxBanner
              hash={depositTx.hash}
              isConfirming={depositTx.isConfirming}
              isConfirmed={depositTx.isConfirmed}
              error={depErr}
              label="Deposit withdrawal"
            />
          </div>
        </section>
      </div>

      <section className="rounded-xl border border-dashed border-neutral-700 bg-neutral-950/40 p-5 text-sm text-neutral-400">
        <strong className="text-neutral-300">Tip:</strong> To make an offer on someone else&apos;s NFT, open the token page from Search and use{" "}
        <span className="text-neutral-200">Make offer</span>. Your profile is for balances and quick navigation — listings
        and buys happen on collection/token routes.
      </section>
    </div>
  );
}
