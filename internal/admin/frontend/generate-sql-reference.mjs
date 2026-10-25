// Build-time inventory only. No engine implementation or database file is
// accessed by the browser. Fail if source seams change instead of publishing
// an empty or stale completion inventory.
import assert from "node:assert/strict";
import { readFileSync, writeFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { goCaseNames as cases } from "./sql-reference-source.mjs";

const here = dirname(fileURLToPath(import.meta.url));
const read = (name) => readFileSync(resolve(here, "../..", name), "utf8");
const between = (source, start, end) => {
  const a = source.indexOf(start);
  const b = source.indexOf(end, a + start.length);
  assert.ok(a >= 0 && b > a, `SQL inventory source seam changed: ${start}`);
  return source.slice(a, b);
};
const binder = read("sql/binder/binder.go");
const call = between(binder, "case ast.Call:", "case ast.Window:");
const geo = read("sql/types/geo.go");
const spatial = between(read("sql/types/spatial.go"), "var spatialFuncs =", "// IsSpatialFunc");
const geoNames = new Set([
  ...cases(between(geo, "func CanonGeoName", "// EvalGeo")),
  ...cases(geo.slice(geo.indexOf("func IsGeoFunc"))),
  ...[...spatial.matchAll(/"([a-z_][a-z_0-9]*)":\s*true/g)].map((item) => item[1]),
]);
// LIKE is a binder-internal call representation of the infix predicate;
// unlike CAST it has no function-call spelling in the parser.
const functions = [...new Set([...cases(call), ...geoNames])].filter((name) => name !== "like").sort();
assert.ok(functions.length > 140, "native function inventory unexpectedly small");
const schema = read("system/schema.go");
const systemTables = [...new Set([
  ...[...schema.matchAll(/\bregister\("([a-z_]+)"/g)].map((item) => `system.${item[1]}`),
  ...[...schema.matchAll(/\btables\["(system\.[a-z_]+)"\]\s*=/g)].map((item) => item[1]),
])].sort();
assert.ok(systemTables.length > 30, "system table inventory unexpectedly small");
const output = `${JSON.stringify({ functions, geoFunctions: [...geoNames].sort(), systemTables }, null, 2)}\n`;
const target = resolve(here, "src/studio/sqlReference.generated.json");
if (process.argv.includes("--check")) {
  assert.equal(readFileSync(target, "utf8"), output, "SQL inventory is stale; run npm run build");
} else {
  writeFileSync(target, output);
}
