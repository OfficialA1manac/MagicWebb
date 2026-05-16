"use client";
import {useState, useCallback} from "react";
import {useWaitForTransactionReceipt} from "wagmi";
import type {Hex} from "viem";

export function useTx() {
  const [hash, setHash] = useState<Hex | undefined>();
  const {isLoading: isConfirming, isSuccess: isConfirmed, error: receiptError} =
    useWaitForTransactionReceipt({hash, query: {enabled: !!hash}});
  const reset = useCallback(() => setHash(undefined), []);
  // receiptError fires when tx is mined but reverted on-chain; expose as `txError` for TxBanner
  return {hash, setHash, isConfirming, isConfirmed, receiptError, txError: receiptError, reset};
}
