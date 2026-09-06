// Builds the NextSQL Admin web UI (Setup wizard + Operations + Studio)
// into ../web/ (index.html + app.js + app.css), which
// cmd/nextsql-admin embeds via //go:embed. Run `npm ci && npm run build`
// here after changing anything under src/; commit the generated ../web/
// output so `go build ./...` works without a Node toolchain.
import * as esbuild from "esbuild";
import { createHash } from "node:crypto";
import { cpSync, mkdirSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, resolve } from "node:path";

const here = dirname(fileURLToPath(import.meta.url));
const outDir = resolve(here, "../web");

rmSync(outDir, { recursive: true, force: true });
mkdirSync(outDir, { recursive: true });

// esbuild writes app.js and (from the CSS imports) app.css into ../web/.
await esbuild.build({
  entryPoints: [resolve(here, "src/main.tsx")],
  bundle: true,
  minify: true,
  sourcemap: false,
  format: "iife",
  target: ["es2020"],
  jsx: "automatic",
  legalComments: "none",
  loader: { ".woff2": "dataurl", ".svg": "dataurl", ".ttf": "dataurl", ".png": "dataurl" },
  define: { "process.env.NODE_ENV": '"production"' },
  outfile: resolve(outDir, "app.js"),
});

cpSync(resolve(here, "index.html"), resolve(outDir, "index.html"));

// PWA static assets: manifest, icons, and the service worker. Written to
// ../webpwa/ — a directory *sibling* to ../web/, not nested inside it —
// because the Go server's generic /assets/ handler embeds and exposes the
// whole of ../web/ wholesale; if these lived anywhere under ../web/ they'd
// always also be reachable under /assets/, however deeply nested. sw.js in
// particular must only exist at the top-level /sw.js path it's actually
// registered from. internal/admin/assets.go embeds ../webpwa/ separately and
// serves each file at its own top-level route. sw.js's CACHE_NAME is
// stamped with a hash of the just-built bundle, so every release gets a
// fresh cache and the service worker's own activate handler drops the
// previous one — no manual version bump needed.
const pwaDir = resolve(here, "../webpwa");
rmSync(pwaDir, { recursive: true, force: true });
cpSync(resolve(here, "public"), pwaDir, { recursive: true });
const bundleHash = createHash("sha256")
  .update(readFileSync(resolve(outDir, "app.js")))
  .update(readFileSync(resolve(outDir, "app.css")))
  .digest("hex")
  .slice(0, 12);
const swPath = resolve(pwaDir, "sw.js");
writeFileSync(swPath, readFileSync(swPath, "utf8").replace("__BUILD_ID__", bundleHash));

console.log("built NextSQL Admin UI -> internal/admin/web/");
