"use client";
import {useState} from "react";
import {parseEther, type Address, type Hex} from "viem";
import {useReadContract, useAccount} from "wagmi";
import {ADDR} from "@/lib/addresses";
import {ERC721Abi} from "@/lib/abi";
import {useApproveNFT} from "@/hooks/useApproveNFT";
import {useList} from "@/hooks/useList";
import {useCancelListing} from "@/hooks/useCancelListing";
import {useCreateAuction} from "@/hooks/useCreateAuction";
import {useTx} from "@/hooks/useTx";
import {TxBanner} from "./TxBanner";

export function OwnerActions({
  coll, tokenId, isListed, defaultTab = null
}: {
  coll: Address;
  tokenId: bigint;
  isListed: boolean;
  defaultTab?: "list" | "auction" | null;
}) {
  const {address} = useAccount();
  const [tab, setTab] = useState<"list" | "auction" | null>(defaultTab ?? null);

  const {data: mpApproved} = useReadContract({
    address: coll, abi: ERC721Abi, functionName: "isApprovedForAll",
    args: address ? [address, ADDR.marketplace] : undefined, query: {enabled: !!address}
  });
  const {data: ahApproved} = useReadContract({
    address: coll, abi: ERC721Abi, functionName: "isApprovedForAll",
    args: address ? [address, ADDR.auction] : undefined, query: {enabled: !!address}
  });

  return (
    <div className="border border-neutral-800 rounded-xl p-4 space-y-3 bg-neutral-900/20">
      <div className="font-semibold">You own this token</div>
      <p className="text-xs text-neutral-500">
        Approvals are per contract: <span className="font-mono text-neutral-400">Marketplace</span> for fixed-price,{" "}
        <span className="font-mono text-neutral-400">AuctionHouse</span> for auctions.
      </p>
      <div className="flex flex-wrap gap-2">
        {isListed ? (
          <CancelBtn coll={coll} tokenId={tokenId} />
        ) : (
          <button
            className="px-3 py-2 rounded bg-emerald-600 hover:bg-emerald-500 text-sm"
            onClick={() => setTab(tab === "list" ? null : "list")}
          >Sell (fixed price)</button>
        )}
        <button
          className="px-3 py-2 rounded border border-neutral-700 hover:border-neutral-500 text-sm"
          onClick={() => setTab(tab === "auction" ? null : "auction")}
        >Auction</button>
      </div>
      {tab === "list" && <ListForm coll={coll} tokenId={tokenId} approved={!!mpApproved} />}
      {tab === "auction" && <AuctionForm coll={coll} tokenId={tokenId} approved={!!ahApproved} />}
    </div>
  );
}

function CancelBtn({coll, tokenId}: {coll: Address; tokenId: bigint}) {
  const {cancel, isPending, error: writeError} = useCancelListing();
  const {hash, setHash, isConfirming, isConfirmed, txError} = useTx();
  return (
    <div className="w-full space-y-2">
      <button
        className="px-3 py-2 rounded border border-red-700 hover:bg-red-900/30 text-sm disabled:opacity-50"
        disabled={isPending || isConfirming}
        onClick={async () => {
          try {
            const h = await cancel(coll, tokenId);
            setHash(h as Hex);
          } catch { /* wagmi error state handles display */ }
        }}
      >{isPending ? "Confirm in wallet…" : isConfirming ? "Cancelling…" : "Cancel listing"}</button>
      <TxBanner hash={hash} isConfirming={isConfirming} isConfirmed={isConfirmed} error={writeError ?? txError} label="Cancel" />
    </div>
  );
}

function ListForm({coll, tokenId, approved}: {coll: Address; tokenId: bigint; approved: boolean}) {
  const [price, setPrice] = useState("");
  const [days, setDays] = useState("7");
  const {approveAll, isPending: appPending, error: appErr} = useApproveNFT();
  const {list, isPending: listPending, error: listErr} = useList();
  const {hash, setHash, isConfirming, isConfirmed, txError} = useTx();

  const parsedPrice = (() => {
    try { return price ? parseEther(price) : null; } catch { return null; }
  })();
  const parsedDays = (() => {
    const n = parseInt(days, 10);
    return !isNaN(n) && n > 0 && n <= 365 ? n : null;
  })();
  const priceValid = parsedPrice !== null && parsedPrice > 0n;
  const daysValid = parsedDays !== null;

  const submit = async () => {
    try {
      if (!approved) {
        const h = await approveAll(coll, ADDR.marketplace);
        setHash(h as Hex);
        return;
      }
      if (!priceValid || !daysValid) return;
      const expiresAt = BigInt(Math.floor(Date.now() / 1000) + parsedDays! * 86400);
      const h = await list(coll, tokenId, parsedPrice!, expiresAt);
      setHash(h as Hex);
    } catch { /* wagmi error state handles display */ }
  };

  return (
    <div className="space-y-2 text-sm">
      <label className="block">
        Price (C2FLR)
        <input className="mt-1 w-full bg-neutral-950 border border-neutral-700 rounded px-2 py-1"
          value={price} onChange={e => setPrice(e.target.value)} placeholder="1.5" />
      </label>
      {price && !priceValid && <p className="text-xs text-red-400">Enter a valid price greater than 0.</p>}
      <label className="block">
        Expires in (days)
        <input className="mt-1 w-full bg-neutral-950 border border-neutral-700 rounded px-2 py-1"
          value={days} onChange={e => setDays(e.target.value)} />
      </label>
      {days && !daysValid && <p className="text-xs text-red-400">Enter a valid number of days (1-365).</p>}
      <p className="text-xs text-neutral-500">
        After this time the listing ends — you keep the NFT and can list again.
      </p>
      <button
        className="px-3 py-2 rounded bg-emerald-600 hover:bg-emerald-500 text-sm disabled:opacity-50"
        disabled={appPending || listPending || isConfirming || (approved && (!priceValid || !daysValid))}
        onClick={submit}
      >{!approved
          ? (appPending ? "Approving…" : "Approve Marketplace")
          : (listPending ? "Confirm in wallet…" : isConfirming ? "Listing…" : "List")}</button>
      <TxBanner hash={hash} isConfirming={isConfirming} isConfirmed={isConfirmed}
        error={(appErr ?? listErr) ?? txError} label={!approved ? "Approval" : "List"} />
    </div>
  );
}

function AuctionForm({coll, tokenId, approved}: {coll: Address; tokenId: bigint; approved: boolean}) {
  const [reserve, setReserve] = useState("0");
  const [days, setDays] = useState("3");
  const [incBps, setIncBps] = useState("500");
  const {approveAll, isPending: appPending, error: appErr} = useApproveNFT();
  const {create, isPending: createPending, error: createErr} = useCreateAuction();
  const {hash, setHash, isConfirming, isConfirmed, txError} = useTx();

  const parsedReserve = (() => {
    try { return parseEther(reserve || "0"); } catch { return null; }
  })();
  const parsedDays = (() => {
    const n = parseInt(days, 10);
    return !isNaN(n) && n > 0 && n <= 30 ? n : null;
  })();
  const parsedBps = (() => {
    const n = parseInt(incBps, 10);
    return !isNaN(n) && n >= 0 && n <= 5000 ? n : null;
  })();
  const reserveValid = parsedReserve !== null;
  const daysValid = parsedDays !== null;
  const bpsValid = parsedBps !== null;

  const submit = async () => {
    try {
      if (!approved) {
        const h = await approveAll(coll, ADDR.auction);
        setHash(h as Hex);
        return;
      }
      if (!reserveValid || !daysValid || !bpsValid) return;
      const now = BigInt(Math.floor(Date.now() / 1000));
      const endsAt = now + BigInt(parsedDays! * 86400);
      const h = await create(coll, tokenId, parsedReserve!, now, endsAt, parsedBps!);
      setHash(h as Hex);
    } catch { /* wagmi error state handles display */ }
  };

  return (
    <div className="space-y-2 text-sm">
      <label className="block">
        Reserve (C2FLR)
        <input className="mt-1 w-full bg-neutral-950 border border-neutral-700 rounded px-2 py-1"
          value={reserve} onChange={e => setReserve(e.target.value)} />
      </label>
      <div className="grid grid-cols-2 gap-2">
        <label className="block">
          Duration (days)
          <input className="mt-1 w-full bg-neutral-950 border border-neutral-700 rounded px-2 py-1"
            value={days} onChange={e => setDays(e.target.value)} />
          {days && !daysValid && <p className="text-xs text-red-400">1-30 days.</p>}
        </label>
        <label className="block">
          Min increment (bps)
          <input className="mt-1 w-full bg-neutral-950 border border-neutral-700 rounded px-2 py-1"
            value={incBps} onChange={e => setIncBps(e.target.value)} />
          {incBps && !bpsValid && <p className="text-xs text-red-400">0-5000 bps.</p>}
        </label>
      </div>
      <p className="text-xs text-neutral-500">
        After end time, a winning bid can be settled. No bids: cancel and keep the NFT.
      </p>
      <button
        className="px-3 py-2 rounded bg-emerald-600 hover:bg-emerald-500 text-sm disabled:opacity-50"
        disabled={appPending || createPending || isConfirming || (approved && (!reserveValid || !daysValid || !bpsValid))}
        onClick={submit}
      >{!approved
          ? (appPending ? "Approving…" : "Approve AuctionHouse")
          : (createPending ? "Confirm in wallet…" : isConfirming ? "Creating…" : "Create auction")}</button>
      <TxBanner hash={hash} isConfirming={isConfirming} isConfirmed={isConfirmed}
        error={(appErr ?? createErr) ?? txError} label={!approved ? "Approval" : "Auction"} />
    </div>
  );
}
