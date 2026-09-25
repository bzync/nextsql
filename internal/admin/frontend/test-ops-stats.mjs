import assert from "node:assert/strict";
import { mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { pathToFileURL } from "node:url";
import * as esbuild from "esbuild";

const scratch = mkdtempSync(join(tmpdir(), "nextsql-ops-stats-"));
const outfile = join(scratch, "stats.mjs");

const cols = ["table_name", "row_count", "analyzed_rows", "updated_at"];

try {
  await esbuild.build({
    entryPoints: [new URL("./src/ops/stats.ts", import.meta.url).pathname],
    bundle: true,
    platform: "node",
    format: "esm",
    target: ["node20"],
    outfile,
  });
  const { staleTables, describeStaleTables, summarizeStorage, describeStorage, humanBytes } = await import(
    `${pathToFileURL(outfile).href}?v=${Date.now()}`
  );

  // A table whose live count matches its ANALYZE snapshot is not stale.
  assert.deepEqual(staleTables({ columns: cols, rows: [["t", 5, 5, ""]] }), []);

  // Drift in either direction is stale, and the drift itself is reported.
  assert.deepEqual(staleTables({ columns: cols, rows: [["t", 8, 5, ""]] }), [
    { name: "t", live: 8, analyzed: 5 },
  ]);
  assert.deepEqual(staleTables({ columns: cols, rows: [["t", 2, 5, ""]] }), [
    { name: "t", live: 2, analyzed: 5 },
  ]);

  // Never analyzed: flagged only when the table actually holds rows, so a
  // freshly created empty table does not nag the operator.
  assert.deepEqual(staleTables({ columns: cols, rows: [["t", 3, null, ""]] }), [
    { name: "t", live: 3, analyzed: null },
  ]);
  assert.deepEqual(staleTables({ columns: cols, rows: [["empty", 0, null, ""]] }), []);

  // A server too old to report analyzed_rows yields no warning rather than a
  // fabricated one.
  assert.deepEqual(
    staleTables({ columns: ["table_name", "row_count", "updated_at"], rows: [["t", 9, ""]] }),
    [],
  );

  // Defensive: absent/!malformed payloads never throw.
  assert.deepEqual(staleTables(undefined), []);
  assert.deepEqual(staleTables(null), []);
  assert.deepEqual(staleTables({ columns: cols, rows: [null, "nope"] }), []);
  assert.deepEqual(staleTables({ columns: cols, rows: [["t", "abc", 5, ""]] }), []);

  // Decimal row counts arrive as JSON strings over the wire.
  assert.deepEqual(staleTables({ columns: cols, rows: [["t", "8", "5", ""]] }), [
    { name: "t", live: 8, analyzed: 5 },
  ]);

  const many = Array.from({ length: 8 }, (_, i) => [`t${i}`, i + 1, 0, ""]);
  const stale = staleTables({ columns: cols, rows: many });
  assert.equal(stale.length, 8);
  const text = describeStaleTables(stale);
  assert.match(text, /t0 \(1 live vs 0\)/);
  assert.match(text, / · …$/);
  assert.equal(text.includes("t6"), false, "should cap the list at six tables");

  assert.match(
    describeStaleTables([{ name: "t", live: 3, analyzed: null }]),
    /t \(3 live vs never analyzed\)/,
  );
  assert.equal(describeStaleTables([]), "");

  // --- storage summary ---
  const scols = ["database", "engine", "page_size", "page_count", "free_pages", "file_size", "wal_lsn", "encryption"];
  const s1 = summarizeStorage({
    columns: scols,
    rows: [["appdb", "nextsql", "16384", "37", "2", "269516928", "0", "enabled"]],
  });
  assert.equal(s1.pageCount, 37);
  assert.equal(s1.freePages, 2);
  assert.equal(s1.livePages, 35);
  assert.equal(s1.fileSize, 269516928);
  const storageText = describeStorage(s1);
  assert.match(storageText, /257\.03 MiB on disk/);
  assert.match(storageText, /35 live pages/);
  assert.match(storageText, /2 reclaimable/);

  // A stat failure leaves file_size NULL; the summary says so rather than 0 B.
  const s2 = summarizeStorage({
    columns: scols,
    rows: [["appdb", "nextsql", "16384", "10", "0", null, "0", "enabled"]],
  });
  assert.equal(s2.fileSize, null);
  assert.match(describeStorage(s2), /file size unavailable/);
  assert.equal(describeStorage(s2).includes("reclaimable"), false);

  // A server without the new columns yields no summary rather than zeroes.
  assert.equal(
    summarizeStorage({ columns: ["database", "engine"], rows: [["appdb", "nextsql"]] }),
    null,
  );
  assert.equal(summarizeStorage(undefined), null);
  assert.equal(summarizeStorage({ columns: scols, rows: [] }), null);
  assert.equal(describeStorage(null), "");

  // free_pages absent (older server) defaults to 0 rather than breaking.
  const s3 = summarizeStorage({
    columns: ["page_size", "page_count", "file_size"],
    rows: [["16384", "8", "1024"]],
  });
  assert.equal(s3.freePages, 0);
  assert.equal(s3.livePages, 8);

  assert.equal(humanBytes(0), "0 B");
  assert.equal(humanBytes(1023), "1023 B");
  assert.equal(humanBytes(1024), "1.00 KiB");

  console.log("test-ops-stats.mjs: ok");
} finally {
  rmSync(scratch, { recursive: true, force: true });
}
