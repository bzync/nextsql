import assert from "node:assert/strict";
import { mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { pathToFileURL } from "node:url";
import * as esbuild from "esbuild";

const scratch = mkdtempSync(join(tmpdir(), "nextsql-admin-api-client-"));
const outfile = join(scratch, "api-client.mjs");

try {
  await esbuild.build({
    entryPoints: [new URL("./src/shared/apiClient.ts", import.meta.url).pathname],
    bundle: true,
    platform: "node",
    format: "esm",
    target: ["node20"],
    outfile,
  });
  const { ApiError, jsonRequest } = await import(`${pathToFileURL(outfile).href}?v=${Date.now()}`);

  let requestedHeaders;
  globalThis.fetch = async (_path, init) => {
    requestedHeaders = init.headers;
    return new Response("<!DOCTYPE html><html>wrong application</html>", {
      status: 404,
      headers: { "Content-Type": "text/html; charset=utf-8" },
    });
  };
  await assert.rejects(
    jsonRequest("GET", "/api/v1/studio/table?name=items", undefined),
    (error) => {
      assert.ok(error instanceof ApiError);
      assert.equal(error.status, 404);
      assert.match(error.message, /expected JSON/);
      assert.match(error.message, /route \/api\/v1\/\*/);
      assert.doesNotMatch(error.message, /DOCTYPE/);
      return true;
    },
  );
  assert.equal(requestedHeaders.Accept, "application/json");

  globalThis.fetch = async () => new Response("<html>rewrite</html>", {
    status: 200,
    headers: { "Content-Type": "text/html" },
  });
  await assert.rejects(
    jsonRequest("GET", "/api/v1/studio/table?name=items", undefined),
    (error) => error instanceof ApiError && error.status === 502,
    "a successful HTML rewrite must fail as an invalid upstream response",
  );

  globalThis.fetch = async () => new Response('{"error":"session expired"}', {
    status: 401,
    headers: { "Content-Type": "application/json; charset=utf-8" },
  });
  await assert.rejects(
    jsonRequest("GET", "/api/v1/session", undefined),
    (error) => error instanceof ApiError && error.status === 401 && error.message === "session expired",
    "structured API errors and their status must be preserved",
  );

  globalThis.fetch = async () => new Response(null, { status: 204 });
  assert.equal(await jsonRequest("DELETE", "/api/v1/session", undefined), null);

  globalThis.fetch = async () => new Response("not-json", {
    status: 200,
    headers: { "Content-Type": "application/json" },
  });
  await assert.rejects(
    jsonRequest("GET", "/api/v1/session", undefined),
    (error) => error instanceof ApiError && error.status === 502 && error.message === "NextSQL Admin API returned invalid JSON",
  );

  const apiOutfile = join(scratch, "ops-api.mjs");
  await esbuild.build({
    entryPoints: [new URL("./src/ops/api.ts", import.meta.url).pathname],
    bundle: true,
    platform: "node",
    format: "esm",
    target: ["node20"],
    outfile: apiOutfile,
  });
  const { api, ApiError: StreamApiError } = await import(`${pathToFileURL(apiOutfile).href}?v=${Date.now()}`);
  const handlers = { onMeta() {}, onRows() {}, onComplete() {} };
  globalThis.fetch = async (_path, init) => {
    requestedHeaders = init.headers;
    return new Response("<!DOCTYPE html><html>wrong application</html>", {
      status: 502,
      headers: { "Content-Type": "text/html; charset=utf-8" },
    });
  };
  await assert.rejects(
    api.studioQueryStream({ query_id: "q1", sql: "SELECT 1" }, handlers),
    (error) => {
      assert.ok(error instanceof StreamApiError);
      assert.equal(error.status, 502);
      assert.match(error.message, /expected JSON/);
      assert.doesNotMatch(error.message, /DOCTYPE/);
      return true;
    },
  );
  assert.equal(requestedHeaders.Accept, "application/x-ndjson, application/json");

  globalThis.fetch = async () => new Response('{"error":"this connection is already running a query"}', {
    status: 409,
    headers: { "Content-Type": "application/json" },
  });
  await assert.rejects(
    api.studioQueryStream({ query_id: "q1", sql: "SELECT 1" }, handlers),
    (error) => error instanceof StreamApiError && error.status === 409 && error.message === "this connection is already running a query",
  );
} finally {
  rmSync(scratch, { recursive: true, force: true });
}

console.log("NextSQL Admin API client response-contract tests passed.");
