import path from "node:path";
import type { NextConfig } from "next";

const nextConfig: NextConfig = {
  images: { unoptimized: true },
  turbopack: {
    root: path.join(__dirname),
  },
  allowedDevOrigins: ["127.0.0.1"],
  experimental: {
    proxyClientMaxBodySize: "512mb",
    serverActions: {
      bodySizeLimit: "512mb",
    },
  },
};

export default nextConfig;
