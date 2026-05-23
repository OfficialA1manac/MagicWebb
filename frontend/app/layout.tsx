import "./globals.css";
import type {Metadata} from "next";
import {Providers} from "./providers";
import {NetworkGuard} from "@/components/NetworkGuard";
import {ConnectButton} from "@/components/ConnectButton";
import Link from "next/link";

const APP_URL = process.env.NEXT_PUBLIC_APP_URL ?? "https://magicwebb-web.onrender.com";
const DESCRIPTION = "Non-custodial NFT marketplace on Flare — fixed price, auctions, and EIP-712 signed offers.";

export const metadata: Metadata = {
  metadataBase: new URL(APP_URL),
  title: {
    default: "MagicWebb",
    template: "%s | MagicWebb",
  },
  description: DESCRIPTION,
  openGraph: {
    type: "website",
    siteName: "MagicWebb",
    title: "MagicWebb — NFT Marketplace on Flare",
    description: DESCRIPTION,
    url: APP_URL,
    images: [{ url: "/opengraph-image", width: 1200, height: 630, alt: "MagicWebb" }],
  },
  twitter: {
    card: "summary_large_image",
    title: "MagicWebb — NFT Marketplace on Flare",
    description: DESCRIPTION,
    images: ["/opengraph-image"],
  },
  icons: {
    icon: "/favicon.svg",
    shortcut: "/favicon.svg",
  },
  robots: { index: true, follow: true },
};

export default function RootLayout({children}: {children: React.ReactNode}) {
  return (
    <html lang="en" suppressHydrationWarning>
      <body className="min-h-screen bg-neutral-950 text-neutral-100 antialiased">
        <Providers>
          <header className="flex flex-wrap items-center gap-3 border-b border-neutral-800 bg-neutral-950/95 p-4 backdrop-blur-sm">
            <Link href="/" className="text-xl font-bold tracking-tight text-white shrink-0">
              MagicWebb
            </Link>
            <nav className="flex flex-1 flex-wrap items-center gap-x-4 gap-y-2 text-sm text-neutral-300 min-w-0">
              <Link href="/#discover" className="hover:text-emerald-400 whitespace-nowrap">Browse</Link>
              <Link href="/list" className="hover:text-emerald-400 whitespace-nowrap">List NFT</Link>
              <Link href="/auctions" className="hover:text-emerald-400 whitespace-nowrap">Auctions</Link>
              <Link href="/offers" className="hover:text-emerald-400 whitespace-nowrap">Offers</Link>
              <Link href="/search" className="hover:text-emerald-400 whitespace-nowrap">Search</Link>
              <Link href="/profile/me" className="hover:text-emerald-400 whitespace-nowrap">Profile</Link>
            </nav>
            <div className="ml-auto shrink-0">
              <ConnectButton />
            </div>
          </header>
          <NetworkGuard />
          <main className="mx-auto max-w-6xl p-4 sm:p-6">{children}</main>
        </Providers>
      </body>
    </html>
  );
}
