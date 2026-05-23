import { ImageResponse } from "next/og";

export const runtime = "edge";
export const alt = "MagicWebb — NFT Marketplace on Flare";
export const size = { width: 1200, height: 630 };
export const contentType = "image/png";

export default function OgImage() {
  return new ImageResponse(
    (
      <div
        style={{
          background: "#0a0a0a",
          width: "100%",
          height: "100%",
          display: "flex",
          flexDirection: "column",
          alignItems: "center",
          justifyContent: "center",
          gap: 16,
        }}
      >
        <div style={{ fontSize: 80, fontWeight: 700, color: "#34d399", letterSpacing: "-2px" }}>
          MagicWebb
        </div>
        <div style={{ fontSize: 30, color: "#a3a3a3" }}>
          NFT Marketplace on Flare
        </div>
        <div style={{ fontSize: 20, color: "#525252", marginTop: 8 }}>
          Fixed price · Auctions · Off-chain offers
        </div>
      </div>
    ),
    { ...size }
  );
}
