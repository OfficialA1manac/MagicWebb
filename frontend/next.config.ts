import type {NextConfig} from "next";
import path from "path";

const securityHeaders = [
  {key: "X-DNS-Prefetch-Control", value: "on"},
  {key: "X-Frame-Options", value: "SAMEORIGIN"},
  {key: "X-Content-Type-Options", value: "nosniff"},
  {key: "Referrer-Policy", value: "strict-origin-when-cross-origin"},
  {
    key: "Permissions-Policy",
    value: "camera=(), microphone=(), geolocation=(), interest-cohort=()"
  }
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
