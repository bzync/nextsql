import path from "node:path";
import type { NextConfig } from "next";

// Pure static site: landing page, /docs, and /download (read-only release
// history). No admin console, no server actions — `next build` emits fully
// static HTML into `out/`, deployable to GitHub Pages or any static host.
// NEXTSQL_PAGES_BASE_PATH is set by the GitHub Pages workflow when the site
// is served from a subpath (e.g. a project page without a custom domain).
const basePath = process.env.NEXTSQL_PAGES_BASE_PATH || "";

const nextConfig: NextConfig = {
  output: "export",
  basePath,
  // `next/image` doesn't prefix `src` with basePath when unoptimized, and a
  // handful of components reference public/ assets by raw path — expose the
  // basePath so those can prefix it themselves (see lib/asset-path.ts).
  env: {
    NEXT_PUBLIC_BASE_PATH: basePath,
  },
  images: { unoptimized: true },
  turbopack: {
    root: path.join(__dirname),
  },
  allowedDevOrigins: ["127.0.0.1"],
};

export default nextConfig;
