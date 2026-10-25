import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { test } from "node:test";
import { goCaseNames } from "./sql-reference-source.mjs";

test("extracts Go string case lists with mandatory comma separators", () => {
  assert.deepEqual(goCaseNames('case "uuid", "now", "ai":\ncase "st_force2d":'), ["uuid", "now", "ai", "st_force2d"]);
  assert.deepEqual(goCaseNames('case\n "first",\n\t"second" \n:\ncase ast.Call:'), ["first", "second"]);
  assert.deepEqual(goCaseNames('showcase "fake":\ncase "one" "two":\ncase "three",:\ncase "valid":'), ["valid"]);
});

test("malformed case lists finish without pathological backtracking", () => {
  // Keep the regression isolated: a vulnerable regex must time out and fail
  // the test instead of hanging the whole frontend gate indefinitely.
  const moduleURL = new URL("./sql-reference-source.mjs", import.meta.url).href;
  const source = `
    import assert from "node:assert/strict";
    import { goCaseNames } from ${JSON.stringify(moduleURL)};
    for (const input of [
      'case "_"' + ' "_"'.repeat(100000) + '!',
      'case "_"' + ', "_"'.repeat(100000) + '!',
      'case "' + '_'.repeat(500000) + '"' + ' '.repeat(500000) + '!'
    ]) assert.deepEqual(goCaseNames(input), []);
  `;
  const result = spawnSync(process.execPath, ["--input-type=module", "-e", source], { timeout: 5000, encoding: "utf8" });
  assert.ifError(result.error);
  assert.equal(result.status, 0, result.stderr);
});
