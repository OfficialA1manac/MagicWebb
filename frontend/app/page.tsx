import Link from "next/link";

export default function Home() {
  return (
    <div className="space-y-8">
      <section className="text-center py-12">
        <h1 className="text-4xl font-bold">Non-custodial NFT marketplace on Flare Coston2.</h1>
        <p className="mt-3 text-neutral-400">Buy, sell, auction, or make signed offers — without giving up custody.</p>
        <div className="mt-6 flex justify-center gap-3">
          <Link href="/search" className="px-4 py-2 rounded bg-emerald-600 hover:bg-emerald-500">Browse</Link>
          <Link href="/profile/me" className="px-4 py-2 rounded border border-neutral-700">My NFTs</Link>
        </div>
      </section>
      <section>
        <h2 className="text-xl font-semibold mb-3">How it works</h2>
        <ul className="grid grid-cols-1 md:grid-cols-3 gap-3 text-sm">
          <li className="p-4 border border-neutral-800 rounded">List with one approval. Buyers pay; you keep custody until purchase.</li>
          <li className="p-4 border border-neutral-800 rounded">Auctions use English bidding with pull-pattern refunds.</li>
          <li className="p-4 border border-neutral-800 rounded">Offers are signed off-chain (EIP-712) on any token, listed or not.</li>
        </ul>
      </section>
    </div>
  );
}
