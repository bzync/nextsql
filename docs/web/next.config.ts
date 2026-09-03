import path from "node:path";
import type { NextConfig } from "next";

// Pure static site: landing page, /docs, and /download (read-only release
// history). No admin console, no server actions — `next build` emits fully
// static HTML into `out/`, deployable to GitHub Pages or any static host.
// NEXTSQL_PAGES_BASE_PATH is set by the GitHub Pages workflow when the site
// is served from a subpath (e.g. a project page without a custom domain).
const nextConfig: NextConfig = {
  output: "export",
  basePath: process.env.NEXTSQL_PAGES_BASE_PATH || "",
  images: { unoptimized: true },
  turbopack: {
    root: path.join(__dirname),
  },
  allowedDevOrigins: ["127.0.0.1"],
};

export default nextConfig;
