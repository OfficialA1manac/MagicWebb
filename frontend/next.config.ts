import type {NextConfig} from "next";

const config: NextConfig = {
  reactStrictMode: true,
  webpack: (cfg) => {
    cfg.externals.push("pino-pretty", "lokijs", "encoding");
    return cfg;
  }
};

export default config;
