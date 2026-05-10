"use client";
import {useWriteContract} from "wagmi";
import type {Address} from "viem";
import {ERC721Abi} from "@/lib/abi";

export function useApproveNFT() {
  const {writeContractAsync, isPending, error} = useWriteContract();
  const approveAll = (coll: Address, operator: Address) =>
    writeContractAsync({
      address: coll,
      abi: ERC721Abi,
      functionName: "setApprovalForAll",
      args: [operator, true]
    });
  return {approveAll, isPending, error};
}
