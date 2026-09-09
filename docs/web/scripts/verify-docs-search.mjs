import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const root = path.dirname(fileURLToPath(new URL("../package.json", import.meta.url)));
const functionsRoute = fs.readFileSync(path.join(root, "out", "docs", "functions.html"), "utf8");
const docsPayload = fs.readFileSync(path.join(root, "out", "docs.txt"), "utf8");

assert.match(
  functionsRoute,
  /<h2 id="string-functions">String functions/,
  "the static Function reference must render its String functions anchor",
);
assert.match(
  functionsRoute,
  /CONCAT\(value \[, value \.\.\.\]\)/,
  "the rendered Function reference must include the CONCAT signature",
);
assert.match(
  docsPayload,
  /section:functions:string-functions/,
  "the command-palette index must include the String functions section",
);
assert.match(
  docsPayload,
  /CONCAT\('Next', 'SQL'\)/,
  "the command-palette search payload must include CONCAT content",
);

console.log("verified rendered Function reference and CONCAT search entry");
