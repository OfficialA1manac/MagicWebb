import Link from "next/link";
import {MarketDiscovery} from "@/components/MarketDiscovery";
import {ADDR, EXPLORER_URL, CHAIN_NAME} from "@/lib/addresses";

export default function Home() {
  return (
    <div className="space-y-10 sm:space-y-12">
      <section className="py-8 text-center sm:py-12">
        <p className="text-sm font-medium uppercase tracking-wide text-emerald-400/90">MagicWebb</p>
        <h1 className="mt-2 text-3xl font-bold sm:text-4xl md:text-5xl">Non-custodial NFT marketplace on Flare</h1>
        <p className="mx-auto mt-3 max-w-2xl text-sm text-neutral-400 sm:text-base">
          Buy at a fixed price, run English auctions, or use EIP-712 signed offers — your NFTs stay in your wallet until
          a transaction settles on-chain. Non-custodial and chain-agnostic by design.
        </p>
        <div className="mt-6 flex flex-col justify-center gap-3 sm:flex-row sm:flex-wrap">
          <Link
            href="/list"
            className="rounded-lg bg-emerald-600 px-4 py-2.5 text-sm font-medium text-neutral-950 hover:bg-emerald-500"
          >
            List your NFT
          </Link>
          <Link
            href="/#discover"
            className="rounded-lg border border-neutral-600 px-4 py-2.5 text-sm hover:border-emerald-500/40"
          >
            Browse listings
          </Link>
          <Link
            href="/auctions"
            className="rounded-lg border border-neutral-600 px-4 py-2.5 text-sm hover:border-emerald-500/40"
          >
            Live auctions
          </Link>
          <Link
            href="/profile/me"
            className="rounded-lg border border-neutral-600 px-4 py-2.5 text-sm hover:border-emerald-500/40"
          >
            My profile
          </Link>
        </div>
      </section>

      <section id="discover" className="scroll-mt-24">
        <MarketDiscovery />
      </section>

      <section>
        <h2 className="mb-3 text-xl font-semibold">How it works</h2>
        <ul className="grid grid-cols-1 gap-3 text-sm md:grid-cols-3">
          <li className="rounded-xl border border-neutral-800 bg-neutral-900/30 p-4">
            <div className="mb-1 font-semibold text-emerald-400/90">List</div>
            Approve the marketplace once, set price and expiry. Buyers call{" "}
            <code className="text-xs text-neutral-500">buy</code> with value — custody transfers in one tx.
          </li>
          <li className="rounded-xl border border-neutral-800 bg-neutral-900/30 p-4">
            <div className="mb-1 font-semibold text-emerald-400/90">Auction</div>
            Reserve price, fixed duration, min bid step. Commit-reveal bids prevent front-running. Outbids return automatically to losers.
          </li>
          <li className="rounded-xl border border-neutral-800 bg-neutral-900/30 p-4">
            <div className="mb-1 font-semibold text-emerald-400/90">Offer</div>
            Deposit once in OfferBook, sign offers off-chain, share JSON with the owner; they accept on-chain when ready.
          </li>
        </ul>
      </section>
      <section>
        <h2 className="mb-3 text-xl font-semibold">Contracts on {CHAIN_NAME}</h2>
        <ul className="space-y-1 break-all font-mono text-xs sm:text-sm">
          <li>
            Marketplace{" "}
            <a
              className="text-emerald-400/90 underline"
              href={`${EXPLORER_URL}/address/${ADDR.marketplace}`}
              target="_blank"
              rel="noreferrer"
            >
              {ADDR.marketplace.slice(0, 6)}…{ADDR.marketplace.slice(-4)}
            </a>
          </li>
          <li>
            AuctionHouse{" "}
            <a
              className="text-emerald-400/90 underline"
              href={`${EXPLORER_URL}/address/${ADDR.auction}`}
              target="_blank"
              rel="noreferrer"
            >
              {ADDR.auction.slice(0, 6)}…{ADDR.auction.slice(-4)}
            </a>
          </li>
          <li>
            OfferBook{" "}
            <a
              className="text-emerald-400/90 underline"
              href={`${EXPLORER_URL}/address/${ADDR.offer}`}
              target="_blank"
              rel="noreferrer"
            >
              {ADDR.offer.slice(0, 6)}…{ADDR.offer.slice(-4)}
            </a>
          </li>
        </ul>
        <p className="mt-3 text-xs text-neutral-500">
          See{" "}
          <Link href="https://github.com/OfficialA1manac/MagicWebb" className="text-emerald-400/90 underline">
            docs on GitHub
          </Link>{" "}
          for architecture and contract details.
        </p>
      </section>
    </div>
  );
}
