import "./globals.css";
import type {Metadata} from "next";
import {Providers} from "./providers";
import {NetworkGuard} from "@/components/NetworkGuard";
import {ConnectButton} from "@/components/ConnectButton";
import Link from "next/link";

export const metadata: Metadata = {
  title: "WebbPlace",
  description: "Non-custodial NFT marketplace on Flare Coston2"
};

export default function RootLayout({children}: {children: React.ReactNode}) {
  return (
    <html lang="en">
      <body>
        <Providers>
          <header className="flex items-center justify-between p-4 border-b border-neutral-800">
            <Link href="/" className="text-xl font-bold">WebbPlace</Link>
            <nav className="flex gap-4 text-sm">
              <Link href="/search">Search</Link>
              <Link href="/profile/me">Profile</Link>
            </nav>
            <ConnectButton />
          </header>
          <NetworkGuard />
          <main className="max-w-6xl mx-auto p-6">{children}</main>
        </Providers>
      </body>
    </html>
  );
}
