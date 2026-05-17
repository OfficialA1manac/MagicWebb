/**
 * Map common contract revert / wagmi error text to short, actionable copy for MagicWebb users.
 * Raw messages still vary by wallet; we match substrings case-insensitively.
 */
export function humanizeTxError(raw: string): string | null {
  const s = raw.toLowerCase();

  if (s.includes("user rejected") || s.includes("user denied") || s.includes("rejected the request")) {
    return "Transaction was rejected in the wallet. Approve the prompt to continue, or try again.";
  }
  if (s.includes("notapproved") || s.includes("not approved")) {
    return (
      "The NFT contract has not approved MagicWebb yet. On the collection contract, run " +
      "`setApprovalForAll(<Marketplace | AuctionHouse | OfferBook>, true)` for the operator shown in the UI " +
      "(green list flow uses Marketplace; auctions use AuctionHouse; accepting offers uses OfferBook), then retry."
    );
  }
  if (s.includes("expired") && (s.includes("listing") || s.includes("marketplace") || s.includes("buy"))) {
    return "This listing’s `expiresAt` is in the past. The seller must cancel (if still listed) and create a new listing with a future expiry.";
  }
  if (s.includes("expired") && s.includes("offer")) {
    return "The signed offer’s `expiresAt` has passed. Ask the bidder to sign a new offer with a later expiry.";
  }
  if (s.includes("offerused") || s.includes("offer used")) {
    return "This offer nonce was already used or cancelled (`OfferUsed`). The bidder should pick a fresh nonce and sign again.";
  }
  if (s.includes("alreadylisted") || s.includes("already listed")) {
    return "This token already has an active listing slot. Cancel the existing listing or use a different flow.";
  }
  if (s.includes("insufficient funds") || s.includes("insufficient balance")) {
    return "Wallet does not have enough native token for this call plus gas. Fund the wallet from the faucet.";
  }
  if (s.includes("wrong chain") || s.includes("chain mismatch")) {
    return "Wallet is on the wrong chain. Use the yellow banner or your wallet’s network menu to switch.";
  }

  return null;
}
