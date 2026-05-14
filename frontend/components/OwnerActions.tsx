"use client";
import {useState} from "react";
import {parseEther, type Address, type Hex} from "viem";
import {useReadContract} from "wagmi";
import {useAccount} from "wagmi";
import {ADDR} from "@/lib/addresses";
import {ERC721Abi} from "@/lib/abi";
import {useApproveNFT} from "@/hooks/useApproveNFT";
import {useList} from "@/hooks/useList";
import {useCancelListing} from "@/hooks/useCancelListing";
import {useCreateAuction} from "@/hooks/useCreateAuction";
import {useTx} from "@/hooks/useTx";
import {TxBanner} from "./TxBanner";

export function OwnerActions({
  coll, tokenId, isListed
}: {coll: Address; tokenId: bigint; isListed: boolean}) {
  const {address} = useAccount();
  const [tab, setTab] = useState<"list" | "auction" | null>(null);

  const {data: mpApproved} = useReadContract({
    address: coll, abi: ERC721Abi, functionName: "isApprovedForAll",
    args: address ? [address, ADDR.marketplace] : undefined, query: {enabled: !!address}
  });
  const {data: ahApproved} = useReadContract({
    address: coll, abi: ERC721Abi, functionName: "isApprovedForAll",
    args: address ? [address, ADDR.auction] : undefined, query: {enabled: !!address}
  });

  return (
    <div className="border border-neutral-800 rounded p-4 space-y-3">
      <div className="font-semibold">You own this token</div>
      <div className="flex flex-wrap gap-2">
        {isListed ? (
          <CancelBtn coll={coll} tokenId={tokenId} />
        ) : (
          <button
            className="px-3 py-2 rounded bg-emerald-600 hover:bg-emerald-500 text-sm"
            onClick={() => setTab(tab === "list" ? null : "list")}
          >List for sale</button>
        )}
        <button
          className="px-3 py-2 rounded border border-neutral-700 hover:border-neutral-500 text-sm"
          onClick={() => setTab(tab === "auction" ? null : "auction")}
        >Create auction</button>
      </div>
      {tab === "list" && (
        <ListForm coll={coll} tokenId={tokenId} approved={!!mpApproved} />
      )}
      {tab === "auction" && (
        <AuctionForm coll={coll} tokenId={tokenId} approved={!!ahApproved} />
      )}
    </div>
  );
}

function CancelBtn({coll, tokenId}: {coll: Address; tokenId: bigint}) {
  const {cancel, isPending, error} = useCancelListing();
  const {hash, setHash, isConfirming, isConfirmed} = useTx();
  return (
    <div className="w-full space-y-2">
      <button
        className="px-3 py-2 rounded border border-red-700 hover:bg-red-900/30 text-sm disabled:opacity-50"
        disabled={isPending || isConfirming}
        onClick={async () => {
          const h = await cancel(coll, tokenId);
          setHash(h as Hex);
        }}
      >{isPending ? "Confirm in wallet…" : isConfirming ? "Cancelling…" : "Cancel listing"}</button>
      <TxBanner hash={hash} isConfirming={isConfirming} isConfirmed={isConfirmed} error={error} label="Cancel" />
    </div>
  );
}

function ListForm({coll, tokenId, approved}: {coll: Address; tokenId: bigint; approved: boolean}) {
  const [price, setPrice] = useState("");
  const [days, setDays] = useState("7");
  const {approveAll, isPending: appPending, error: appErr} = useApproveNFT();
  const {list, isPending: listPending, error: listErr} = useList();
  const {hash, setHash, isConfirming, isConfirmed} = useTx();

  const submit = async () => {
    if (!approved) {
      const h = await approveAll(coll, ADDR.marketplace);
      setHash(h as Hex);
      return;
    }
    const expiresAt = BigInt(Math.floor(Date.now() / 1000) + Number(days) * 86400);
    const h = await list(coll, tokenId, parseEther(price), expiresAt);
    setHash(h as Hex);
  };

  return (
    <div className="space-y-2 text-sm">
      <label className="block">
        Price (C2FLR)
        <input className="mt-1 w-full bg-neutral-950 border border-neutral-700 rounded px-2 py-1"
          value={price} onChange={e => setPrice(e.target.value)} placeholder="1.5" />
      </label>
      <label className="block">
        Expires in (days)
        <input className="mt-1 w-full bg-neutral-950 border border-neutral-700 rounded px-2 py-1"
          value={days} onChange={e => setDays(e.target.value)} />
      </label>
      <button
        className="px-3 py-2 rounded bg-emerald-600 hover:bg-emerald-500 text-sm disabled:opacity-50"
        disabled={appPending || listPending || isConfirming || (!approved ? false : !price)}
        onClick={submit}
      >{!approved
          ? (appPending ? "Approving…" : "Approve Marketplace")
          : (listPending ? "Confirm in wallet…" : isConfirming ? "Listing…" : "List")}</button>
      <TxBanner hash={hash} isConfirming={isConfirming} isConfirmed={isConfirmed}
        error={appErr || listErr} label={!approved ? "Approval" : "List"} />
    </div>
  );
}

function AuctionForm({coll, tokenId, approved}: {coll: Address; tokenId: bigint; approved: boolean}) {
  const [reserve, setReserve] = useState("0");
  const [days, setDays] = useState("3");
  const [incBps, setIncBps] = useState("500");
  const {approveAll, isPending: appPending, error: appErr} = useApproveNFT();
  const {create, isPending: createPending, error: createErr} = useCreateAuction();
  const {hash, setHash, isConfirming, isConfirmed} = useTx();

  const submit = async () => {
    if (!approved) {
      const h = await approveAll(coll, ADDR.auction);
      setHash(h as Hex);
      return;
    }
    const now = BigInt(Math.floor(Date.now() / 1000));
    const endsAt = now + BigInt(Number(days) * 86400);
    const h = await create(coll, tokenId, parseEther(reserve), now, endsAt, Number(incBps));
    setHash(h as Hex);
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
        </label>
        <label className="block">
          Min increment (bps)
          <input className="mt-1 w-full bg-neutral-950 border border-neutral-700 rounded px-2 py-1"
            value={incBps} onChange={e => setIncBps(e.target.value)} />
        </label>
      </div>
      <button
        className="px-3 py-2 rounded bg-emerald-600 hover:bg-emerald-500 text-sm disabled:opacity-50"
        disabled={appPending || createPending || isConfirming}
        onClick={submit}
      >{!approved
          ? (appPending ? "Approving…" : "Approve AuctionHouse")
          : (createPending ? "Confirm in wallet…" : isConfirming ? "Creating…" : "Create auction")}</button>
      <TxBanner hash={hash} isConfirming={isConfirming} isConfirmed={isConfirmed}
        error={appErr || createErr} label={!approved ? "Approval" : "Auction"} />
    </div>
  );
}
