// Builds the NextSQL Manager web UI into ../web/ (index.html + app.js +
// app.css), which cmd/nextsql-manager embeds via //go:embed. Run
// `npm ci && npm run build` here after changing anything under src/; commit the
// generated ../web/ output so `go build ./...` works without a Node toolchain.
import * as esbuild from "esbuild";
import { cpSync, mkdirSync, rmSync } from "node:fs";
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
  loader: { ".woff2": "dataurl", ".svg": "dataurl", ".ttf": "dataurl" },
  define: { "process.env.NODE_ENV": '"production"' },
  outfile: resolve(outDir, "app.js"),
});

cpSync(resolve(here, "index.html"), resolve(outDir, "index.html"));

console.log("built NextSQL Manager UI -> internal/manager/web/");
