import assert from "node:assert/strict";
import { mkdtempSync, readFileSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { pathToFileURL } from "node:url";
import * as esbuild from "esbuild";

const scratch = mkdtempSync(join(tmpdir(), "nextsql-completion-"));
try {
  const outfile = join(scratch, "completion.mjs");
  await esbuild.build({ entryPoints: [new URL("./src/studio/sqlCompletion.ts", import.meta.url).pathname], bundle: true, platform: "node", format: "esm", jsx: "automatic", outfile });
  const completion = await import(pathToFileURL(outfile).href);
  const native = JSON.parse(readFileSync(new URL("./src/studio/sqlReference.generated.json", import.meta.url)));
  const commands = JSON.parse(readFileSync(new URL("./src/studio/sqlCommands.json", import.meta.url)));
  const caps = (rows) => completion.readSQLCapabilities({ columns: ["description", "status", "name"], rows: rows.map(([name, status]) => ["server description", status, name]) });
  const supported = caps([["vector", "supported"], ["fulltext", "supported"], ["geo", "supported"], ["window_functions", "supported"], ["transactions", "supported"], ["cte", "supported"]]);
  const unavailable = caps([["vector", "unsupported"], ["fulltext", "experimental"], ["geo", "deprecated"]]);

  assert.ok(native.functions.length > 140);
  assert.ok(!native.functions.includes("year"), "do not advertise functions the binder rejects");
  assert.ok(native.functions.includes("first_value"));
  assert.ok(native.functions.includes("vector_scale"));
  assert.ok(native.functions.includes("st_geomfromgeojson"));
  assert.ok(native.functions.includes("st_force2d"), "native names containing digits must not disappear");
  for (const command of commands) {
    const prefix = command.label.slice(0, -1);
    assert.ok(completion.commandSQLSuggestions(prefix, prefix.length)?.some((item) => item.label === command.label), `complete ${command.label}`);
  }
  const source = "SELECT ';' AS text; -- retained\nCREATE VEC";
  const vectorCommand = completion.commandSQLSuggestions(source, source.length).find((item) => item.label === "CREATE VECTOR INDEX");
  assert.equal(source.slice(0, vectorCommand.replaceStart) + vectorCommand.insertText + source.slice(vectorCommand.replaceEnd), "SELECT ';' AS text; -- retained\nCREATE VECTOR INDEX");
  assert.equal(completion.commandSQLSuggestions("CREATE /* retain */ TA", 22), null, "do not remove interleaved comments");
  assert.equal(completion.commandSQLSuggestions("SELECT * FROM t", 15), null);
  const context = (sql) => completion.contextSQLSuggestions(sql, sql.length);
  assert.deepEqual(context("CREATE VECTOR INDEX ix ON t(v) USING I").map((item) => item.label), ["IVF", "IVFPQ"]);
  assert.ok(context("CREATE TABLE t (name VA").some((item) => item.label === "VARCHAR(255)"));
  assert.ok(context("CREATE TABLE t (id INT64, embedding VE").some((item) => item.label === "VECTOR<F32, 1536>"));
  assert.ok(context("ALTER TABLE t ADD COLUMN c BO").some((item) => item.label === "BOOL"));
  assert.ok(context("ALTER TABLE t AD").some((item) => item.label === "ADD PARTITION"));
  assert.ok(context("SELECT CAST(1 AS DE").some((item) => item.label === "DECIMAL(12,2)"));
  assert.equal(context("CREATE TABLE t (CONSTRAINT pk PRIMARY KEY (id"), null, "a key's identifier list is not a type slot");

  for (const sql of ["SE", "SELECT co", "SELECT * FROM t WHERE na", "SELECT t."]) assert.equal(completion.canAutoCompleteSQL(sql, sql.length), true, sql);
  for (const sql of ["S", "SELECT ", "SELECT 'co", 'SELECT "co', "SELECT X'AB", "-- CREATE TAB", "/* SELECT co", "/* comment.", "SELECT $12", "SELECT 1 -- co"]) assert.equal(completion.canAutoCompleteSQL(sql, sql.length), false, sql);
  const tooLarge = "x".repeat(completion.MAX_COMPLETION_CHARS + 1);
  assert.equal(completion.canAutoCompleteSQL(tooLarge, tooLarge.length), false);
  assert.deepEqual(completion.sqlFeatureNotes(tooLarge, supported), []);

  const item = { kind: "function", label: "VECTOR_NORM(vec)", insertText: "VECTOR_NORM()" };
  assert.equal(completion.annotateSQLSuggestions([item], supported)[0].availability, "supported");
  const blocked = completion.annotateSQLSuggestions([item], unavailable)[0];
  assert.equal(blocked.availability, "unsupported");
  assert.match(blocked.note, /Unavailable on this server/);
  assert.equal(completion.annotateSQLSuggestions([{ ...item, insertText: "HIGHLIGHT()" }], unavailable)[0].availability, "experimental");
  assert.equal(completion.annotateSQLSuggestions([{ ...item, insertText: "ST_BUFFER()" }], unavailable)[0].availability, "deprecated");
  const unknown = completion.annotateSQLSuggestions([item], caps([]))[0];
  assert.equal(unknown.availability, "unknown");
  assert.match(unknown.note, /unverified/);
  assert.equal(completion.annotateSQLSuggestions([item], caps([["vector", "supported"], ["vector", "unsupported"]]))[0].availability, "unknown");
  assert.equal(completion.readSQLCapabilities({ columns: [], rows: [] }).size, 0);

  const notes = (sql, capabilities = supported) => completion.sqlFeatureNotes(sql, capabilities);
  assert.match(notes("CREATE DATABASE app")[0].message, /one database/);
  assert.match(notes("CREATE PROCEDURE p()")[0].message, /WORKFLOW/);
  assert.match(notes("CALL w()")[0].message, /RUN WORKFLOW/);
  assert.match(notes("VACUUM t")[0].message, /MAINTAIN/);
  assert.match(notes("SELECT YEAR(NOW())")[0].message, /YEAR\(\)/);
  assert.equal(notes("SELECT VECTOR_NORM(v) FROM t", unavailable)[0].severity, "warning");
  assert.equal(notes("SELECT VECTOR_NORM(v) FROM t", caps([]))[0].severity, "info");
  assert.equal(notes("SELECT HIGHLIGHT(body) FROM t SEARCH body FOR 'word'", unavailable)[0].severity, "warning");
  assert.ok(notes("CREATE VECTOR INDEX ix ON t(v) USING IVFPQ", caps([["vector", "supported"], ["vector_ivfpq", "unsupported"]])).some((note) => note.message.includes("Unavailable on this server: vector_ivfpq")));
  assert.ok(notes("CREATE TABLE t (secret STRING ENCRYPTED CLIENT)", caps([["field_encryption_client", "unsupported"]])).some((note) => note.message.includes("Unavailable on this server: field_encryption_client")));
  assert.equal(notes("-- CREATE DATABASE app\nSELECT 'VACUUM t; SELECT YEAR()' AS text").length, 0);
  assert.equal(notes('SELECT "YEAR" FROM t /* CREATE PROCEDURE p() */').length, 0);
  assert.equal(notes("CREATE TABLE t (name VARCHAR(20), price DECIMAL(12,2))").length, 0);
  assert.equal(notes("WITH c AS (SELECT 1) SELECT * FROM c").length, 0);
  assert.equal(notes("WITH RECURSIVE c(x) AS (SELECT 1), d(y) AS (SELECT 2) SELECT * FROM c").length, 0, "CTE column lists are declarations, not unknown calls");
  const unicode = "SELECT '🐘';\nCREATE DATABASE app";
  const note = notes(unicode)[0];
  assert.equal(note.line, 2);
  assert.equal(unicode.slice(note.offset, note.offset + 6), "CREATE");
  assert.ok(notes("SELECT YEAR();".repeat(100)).length <= completion.MAX_FEATURE_NOTES);
  const adversarial = "SELECT nope();".repeat(9000).slice(0, completion.MAX_COMPLETION_CHARS);
  assert.equal(notes(adversarial).length, completion.MAX_FEATURE_NOTES, "in-budget adversarial SQL retains the note bound");
  console.log(`Studio SQL completion tests passed (${commands.length} command forms, ${native.functions.length} native built-ins)`);
} finally {
  rmSync(scratch, { recursive: true, force: true });
}
