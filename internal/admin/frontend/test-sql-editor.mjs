import assert from "node:assert/strict";
import { readFileSync, rmSync } from "node:fs";
import { pathToFileURL } from "node:url";
import * as esbuild from "esbuild";

const outfile = new URL("./.test-sql-editor-bundle.mjs", import.meta.url).pathname;

try {
  await esbuild.build({
    entryPoints: [new URL("./src/studio/SqlCodeEditor.tsx", import.meta.url).pathname],
    bundle: true,
    platform: "node",
    format: "esm",
    target: ["node20"],
    jsx: "automatic",
    outfile,
    external: ["react", "react-dom"],
  });
  const editor = await import(`${pathToFileURL(outfile).href}?v=${Date.now()}`);

  const source = [
    "SELECT id, name, lower(name), $1, 12.5",
    "FROM users",
    "WHERE note = 'it''s safe' AND payload = X'CAFE'; -- comment",
    'SELECT "quoted""name" FROM users /* block */',
    "CREATE TABLE samples (body TEXT, flag BOOL);",
    "SELECT café FROM samples;",
  ].join("\n");
  const tokens = editor.tokenizeSql(source);
  assert.equal(tokens.map((token) => token.value).join(""), source, "highlighting must preserve every source byte");
  assert.ok(tokens.some((token) => token.type === "keyword" && token.value === "SELECT"));
  assert.ok(tokens.some((token) => token.type === "type" && token.value === "TEXT"));
  assert.ok(tokens.some((token) => token.type === "type" && token.value === "BOOL"));
  assert.ok(tokens.some((token) => token.type === "function" && token.value === "lower"));
  assert.ok(tokens.some((token) => token.type === "parameter" && token.value === "$1"));
  assert.ok(tokens.some((token) => token.type === "number" && token.value === "12.5"));
  assert.ok(tokens.some((token) => token.type === "string" && token.value === "'it''s safe'"));
  assert.ok(tokens.some((token) => token.type === "string" && token.value === "X'CAFE'"));
  assert.ok(tokens.some((token) => token.type === "comment" && token.value === "-- comment"));
  assert.ok(tokens.some((token) => token.type === "comment" && token.value === "/* block */"));
  assert.ok(tokens.some((token) => token.type === "identifier" && token.value === '"quoted""name"'));
  assert.ok(tokens.some((token) => token.type === "identifier" && token.value === "café"));

  // Match native lexical semantics instead of generic-SQL assumptions: no
  // exponent number, # comment, backtick identifier, or '?' parameter.
  const nativeBoundary = editor.tokenizeSql("1e2 # nope `name` ?");
  assert.deepEqual(
    nativeBoundary.map((token) => [token.type, token.value]),
    [
      ["number", "1"],
      ["identifier", "e2"],
      ["plain", " "],
      ["plain", "#"],
      ["plain", " "],
      ["identifier", "nope"],
      ["plain", " "],
      ["plain", "`"],
      ["identifier", "name"],
      ["plain", "`"],
      ["plain", " "],
      ["plain", "?"],
    ],
  );

  // Keep the presentational keyword copy mechanically locked to the engine's
  // authoritative lexer map. This catches both missing and invented words.
  const lexerSource = readFileSync(new URL("../../sql/lexer/lexer.go", import.meta.url), "utf8");
  const keywordMap = lexerSource.match(/var keywords = map\[string\]Kind\{([\s\S]*?)\n\}/)?.[1];
  assert.ok(keywordMap, "could not locate the native lexer keyword map");
  const nativeKeywords = [...keywordMap.matchAll(/"([a-z0-9_]+)"\s*:\s*Kw[A-Za-z0-9_]+/g)].map((match) => match[1]).sort();
  assert.deepEqual([...editor.NEXTSQL_KEYWORDS].sort(), nativeKeywords);

  assert.equal(editor.MAX_HIGHLIGHT_CHARS, 256 * 1024);
  assert.equal(editor.MAX_HIGHLIGHT_LINES, 10_000);
  console.log("SqlCodeEditor native tokenizer and bounds tests passed");
} finally {
  rmSync(outfile, { force: true });
}
