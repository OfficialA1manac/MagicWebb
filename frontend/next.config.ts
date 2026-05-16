import type {NextConfig} from "next";
import path from "path";

const walletConnectSrc = [
  "wss://relay.walletconnect.com",
  "https://relay.walletconnect.com",
  "wss://relay.walletconnect.org",
  "https://relay.walletconnect.org",
  "https://rpc.walletconnect.com",
  "https://rpc.walletconnect.org",
  "https://explorer-api.walletconnect.com",
  "https://*.reown.com",
  "wss://*.reown.com",
].join(" ");

const csp = [
  "default-src 'self'",
  "script-src 'self' 'unsafe-eval' 'unsafe-inline'",
  "style-src 'self' 'unsafe-inline'",
  "img-src 'self' data: blob: https:",
  "font-src 'self' data:",
  "connect-src 'self' https://coston2-api.flare.network https://coston2-explorer.flare.network " + walletConnectSrc,
  "frame-src 'none'",
  "object-src 'none'",
  "base-uri 'self'",
  "form-action 'self'",
].join("; ");

const securityHeaders = [
  {key: "X-DNS-Prefetch-Control", value: "on"},
  {key: "X-Frame-Options", value: "SAMEORIGIN"},
  {key: "X-Content-Type-Options", value: "nosniff"},
  {key: "Referrer-Policy", value: "strict-origin-when-cross-origin"},
  {
    key: "Permissions-Policy",
    value: "camera=(), microphone=(), geolocation=()"
  },
  {key: "Content-Security-Policy", value: csp}
];

const config: NextConfig = {
  reactStrictMode: true,
  poweredByHeader: false,
  output: "standalone",
  outputFileTracingRoot: path.join(__dirname, ".."),
  transpilePackages: [
    "@walletconnect/ethereum-provider",
    "@walletconnect/universal-provider",
    "@walletconnect/sign-client"
  ],
  headers: async () => [{source: "/:path*", headers: securityHeaders}],
  webpack: (cfg, {isServer}) => {
    cfg.externals.push("pino-pretty", "lokijs", "encoding");
    cfg.resolve = cfg.resolve ?? {};
    cfg.resolve.fallback = {
      ...(cfg.resolve.fallback ?? {}),
      "@react-native-async-storage/async-storage": false,
      fs: false,
      net: false,
      tls: false
    };
    if (!isServer) {
      cfg.resolve.alias = {
        ...(cfg.resolve.alias ?? {}),
        "@react-native-async-storage/async-storage": false
      };
    }
    return cfg;
  }
};

export default config;