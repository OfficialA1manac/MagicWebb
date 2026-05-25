"use client";
import {useWriteContract} from "wagmi";
import {ADDR} from "@/lib/addresses";
import {AuctionHouseAbi} from "@/lib/abi";

export function useBid() {
  const {writeContractAsync, isPending, error} = useWriteContract();

  /**
   * Reveal phase of commit-reveal bid.
   * @param id         - auction ID
   * @param fullAmount - total bid amount (uint128)
   * @param salt       - same salt used in commitBid
   * @param value      - msg.value: fullAmount for new bidders; fullAmount - prevHighBid for rebidders
   */
  const bid = (id: bigint, fullAmount: bigint, salt: `0x${string}`, value: bigint) =>
    writeContractAsync({
      address: ADDR.auction,
      abi: AuctionHouseAbi,
      functionName: "bid",
      args: [id, fullAmount, salt],
      value,
    });

  return {bid, isPending, error};
}
