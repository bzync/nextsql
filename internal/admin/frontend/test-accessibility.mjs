// Accessibility audit for the merged NextSQL Admin bundle: Setup mode (the
// former Installer) and Operations mode (the former Manager plus Studio),
// each served from its own fixture HTTP server against
// the one real built bundle in ../web/. Chrome itself is external (see
// ../../webtest/cdp.mjs); axe-core stays a package-local dev dependency.
import axe from "axe-core";
import assert from "node:assert/strict";
import { createServer } from "node:http";
import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { launchChrome, runAxe } from "../../webtest/cdp.mjs";

const here = dirname(fileURLToPath(import.meta.url));
const web = resolve(here, "../web");
const assets = {
  "/": ["index.html", "text/html; charset=utf-8"],
  "/assets/app.js": ["app.js", "text/javascript; charset=utf-8"],
  "/assets/app.css": ["app.css", "text/css; charset=utf-8"],
};
const csp = "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; font-src 'self' data:; connect-src 'self'; base-uri 'none'; frame-ancestors 'none'; object-src 'none'; form-action 'self'";

function json(response, status, body) {
  response.writeHead(status, { "Content-Type": "application/json" });
  response.end(JSON.stringify(body));
}

function ndjson(response, frames) {
  response.writeHead(200, { "Content-Type": "application/x-ndjson", "Cache-Control": "no-store" });
  for (const frame of frames) response.write(`${JSON.stringify(frame)}\n`);
  response.end();
}

function serveAssets(request, response, extraHeaders = {}) {
  const url = new URL(request.url, "http://127.0.0.1");
  const asset = assets[url.pathname];
  if (!asset) return false;
  response.writeHead(200, { "Content-Type": asset[1], "Content-Security-Policy": csp, ...extraHeaders });
  response.end(readFileSync(resolve(web, asset[0])));
  return true;
}

async function withServer(handler, run) {
  const server = createServer(handler);
  await new Promise((resolveListen) => server.listen(0, "127.0.0.1", resolveListen));
  const { port } = server.address();
  const browser = await launchChrome(`http://127.0.0.1:${port}/`);
  try {
    await run(browser);
  } finally {
    await browser.close();
    await new Promise((resolveClose) => server.close(resolveClose));
  }
}

// The theme control (shared/ThemeSelect.tsx) is @bzync/rui's Select, which
// renders as a button[role=combobox] + li[role=option] listbox, not a
// native <select> — id="nsa-color-theme" is the stable hook (a <label for>
// supplies the accessible name; there's no aria-label on the button).
const THEME_TRIGGER = "document.getElementById('nsa-color-theme')";

// Matches shared/ThemeSelect.tsx's THEME_OPTIONS order (system, light, dark)
// -> the listbox's opt-0/opt-1/opt-2 ids.
const THEME_OPTION_INDEX = { system: 0, light: 1, dark: 2 };

async function themeCheck(browser, label) {
  assert.equal(await browser.evaluate(`${THEME_TRIGGER}?.getAttribute("role")`), "combobox");
  await browser.evaluate(`${THEME_TRIGGER}.click()`);
  assert.equal(await browser.evaluate("document.querySelectorAll('[role=option]').length"), 3);
  await browser.evaluate(`${THEME_TRIGGER}.click()`);
  await browser.waitFor(`${THEME_TRIGGER}?.getAttribute("aria-expanded") === "false"`, `${label} theme menu closed`);
  // End back on "system" (not "dark"/"light") so a later axe check in this
  // same session isn't run against a manually-forced dark theme — rui's
  // --color-accent-500 token is known not to meet AA against a dark
  // background (tracked separately; out of scope for this merge).
  for (const theme of ["dark", "light", "system"]) {
    await browser.evaluate(`${THEME_TRIGGER}.click()`);
    await browser.waitFor(`${THEME_TRIGGER}?.getAttribute("aria-expanded") === "true"`, `${label} theme menu open`);
    await browser.evaluate(`document.getElementById("nsa-color-theme-list-opt-${THEME_OPTION_INDEX[theme]}")?.click()`);
    await browser.waitFor(
      `${THEME_TRIGGER}?.getAttribute("aria-expanded") === "false"`,
      `${label} theme menu closed after choosing ${theme}`,
    );
  }
}

async function contrastAndMotionCheck(browser, label) {
  await browser.emulateMedia([
    { name: "prefers-reduced-motion", value: "reduce" },
    { name: "prefers-contrast", value: "more" },
  ]);
  assert.equal(await browser.evaluate("matchMedia('(prefers-reduced-motion: reduce)').matches"), true);
  assert.equal(await browser.evaluate("matchMedia('(prefers-contrast: more)').matches"), true);
  await browser.waitFor("getComputedStyle(document.querySelector('.nsa-theme-trigger')).borderTopWidth === '2px'", `${label} increased-contrast styles`);
  assert.equal(await browser.evaluate("parseFloat(getComputedStyle(document.querySelector('button')).transitionDuration) <= 0.001"), true);
  await runAxe(browser, axe.source, `${label} (increased contrast/reduced motion)`);
  await browser.emulateMedia([]);
}

// A 720-CSS-pixel viewport at DPR 2 represents a common 1440-physical-pixel
// high-density window while also exercising the compact responsive layout.
// Browser text, CSS borders, and SVG remain resolution-independent; any
// raster image has to provide enough source pixels for its rendered size.
async function highDensityCheck(browser, label, responsiveExpression) {
  await browser.emulateDeviceMetrics({ width: 720, height: 600, deviceScaleFactor: 2 });
  await browser.waitFor(
    "window.devicePixelRatio === 2 && window.innerWidth === 720",
    `${label} 2x device metrics`,
  );
  await browser.waitFor(responsiveExpression, `${label} compact responsive layout`);
  const rendering = await browser.evaluate(`(() => {
    const root = document.documentElement;
    const dpr = window.devicePixelRatio;
    const undersizedRasterImages = [...document.images]
      .filter((image) => image.getBoundingClientRect().width > 0)
      .filter((image) => image.naturalWidth < Math.ceil(image.getBoundingClientRect().width * dpr))
      .map((image) => image.currentSrc || image.src);
    return {
      pageOverflows: root.scrollWidth > root.clientWidth,
      undersizedRasterImages,
      scalableFontsReady: document.fonts.status === "loaded",
    };
  })()`);
  assert.equal(rendering.pageOverflows, false, `${label} must not overflow the page horizontally at DPR 2`);
  assert.deepEqual(rendering.undersizedRasterImages, [], `${label} raster images must have enough pixels for DPR 2`);
  assert.equal(rendering.scalableFontsReady, true, `${label} web fonts must be ready at DPR 2`);
  await runAxe(browser, axe.source, `${label} (2x density)`);
  await browser.clearDeviceMetrics();
  await browser.waitFor("window.devicePixelRatio === 1 && window.innerWidth === 1280", `${label} device metrics reset`);
}

// --- Phase 1: Setup mode (the former Installer's welcome screen) ---
async function testSetupMode() {
  await withServer((request, response) => {
    const url = new URL(request.url, "http://127.0.0.1");
    if (serveAssets(request, response)) return;
    if (url.pathname === "/api/v1/mode") return json(response, 200, { mode: "setup" });
    if (url.pathname === "/api/v1/hello") {
      return json(response, 200, {
        nextsql_version: "test",
        phase: 28,
        defaults: { dataDir: "/var/lib/nextsql", keyFile: "/etc/nextsql/root.key", configOut: "/etc/nextsql/nextsql.conf", elevated: false, os: "linux" },
      });
    }
    if (url.pathname === "/api/v1/service") {
      return json(response, 200, { supported: false, scope: "", unitFound: false, configPath: "", enabled: false, active: false });
    }
    json(response, 404, { error: "not found" });
  }, async (browser) => {
    await browser.waitFor("document.getElementById('installer-step-title')?.textContent === 'NextSQL Setup'", "the Setup welcome view");
    await browser.evaluate("document.fonts.ready");
    assert.equal(await browser.evaluate("[...document.fonts].some((face) => face.family.includes('Inter Variable') && face.status === 'loaded')"), true, "the bundled Inter font should load under the real CSP");
    await runAxe(browser, axe.source, "Setup welcome");
    assert.equal(await browser.evaluate("document.querySelector('.nsa-skip')?.getAttribute('href')"), "#installer-main");
    await themeCheck(browser, "Setup");
    await contrastAndMotionCheck(browser, "Setup");
    await highDensityCheck(browser, "Setup welcome", "getComputedStyle(document.querySelector('.nsi-shell')).flexDirection === 'column'");
    console.log("setup-mode accessibility audit passed (welcome view + axe WCAG 2.2 AA tags)");
  });
}

// --- Phase 2: Operations mode (the former Manager) + Studio workspace ---
async function testOperateMode() {
  const result = { columns: ["name", "value"], rows: [["fixture", "ok"]] };
  const studioCapabilities = {
    columns: ["name", "status", "description", "since_version"],
    column_types: ["STRING", "STRING", "STRING", "STRING"],
    rows: [
      ["transactions", "production", "Transactional SQL", "1.0"],
      ["fulltext", "supported", "Native SEARCH/HIGHLIGHT/SNIPPET", "0.1.0"],
      ["vector", "supported", "VECTOR<F32,N>/BITVECTOR<N>/SPARSEVECTOR<N>, NEAREST", "0.1.0"],
      ["geo", "supported", "rich POINT/BOX/LINESTRING/POLYGON operations and spatial index", "0.1.0"],
    ],
    truncated: false,
    elapsed_ms: 1,
  };
  const studioTables = {
    columns: ["name", "column_count", "pk"],
    column_types: ["STRING", "INT64", "STRING"],
    rows: [["articles", "5", "id"]],
    truncated: false,
    elapsed_ms: 1,
  };
  const studioColumns = {
    columns: ["table_name", "column_name", "ordinal", "type", "not_null", "is_primary", "default_value"],
    column_types: ["STRING", "STRING", "INT64", "STRING", "BOOL", "BOOL", "STRING"],
    rows: [
      ["articles", "id", "0", "INT64", "true", "true", ""],
      ["articles", "title", "1", "STRING", "false", "false", ""],
      ["articles", "body", "2", "TEXT", "false", "false", ""],
      ["articles", "metadata", "3", "JSON", "false", "false", ""],
      ["articles", "embedding", "4", "VECTOR<F32,3>", "false", "false", ""],
      ["articles", "loc", "5", "POINT", "false", "false", ""],
    ],
    truncated: false,
    elapsed_ms: 1,
  };
  const studioIndexes = {
    columns: ["table_name", "index_name", "kind", "is_unique", "columns", "include_columns", "predicate", "status"],
    column_types: ["STRING", "STRING", "STRING", "BOOL", "STRING", "STRING", "STRING", "STRING"],
    rows: [
      ["articles", "PRIMARY", "btree", "true", "id", "", "", "valid"],
      ["articles", "uq_articles_title", "btree", "true", "title", "", "", "valid"],
      ["articles", "ix_articles_text", "fulltext", "false", "title,body", "", "", "valid"],
      ["articles", "ix_metadata_first_tag", "btree", "false", "metadata.tags.0", "", "", "valid"],
      ["articles", "ix_embedding_hnsw", "vector", "false", "embedding", "", "", "valid"],
      ["articles", "ix_loc_spatial", "spatial", "false", "loc", "", "", "valid"],
    ],
    truncated: false,
    elapsed_ms: 1,
  };
  const fkColumns = ["table_name", "constraint_name", "ordinal", "column_name", "ref_table", "ref_column", "on_delete", "on_update"];
  const fkColumnTypes = ["STRING", "STRING", "INT64", "STRING", "STRING", "STRING", "STRING", "STRING"];
  const studioForeignKeys = {
    columns: fkColumns,
    column_types: fkColumnTypes,
    rows: [
      ["articles", "fk_articles_author", "1", "author_id", "authors", "id", "CASCADE", "RESTRICT"],
    ],
    truncated: false,
    elapsed_ms: 1,
  };
  const studioReferencingKeys = {
    columns: fkColumns,
    column_types: fkColumnTypes,
    rows: [
      ["comments", "fk_comments_article", "1", "article_id", "articles", "id", "CASCADE", "RESTRICT"],
    ],
    truncated: false,
    elapsed_ms: 1,
  };
  const studioTriggers = {
    columns: ["name", "owner", "timing", "event", "table_name", "workflow", "arg_count"],
    column_types: ["STRING", "STRING", "STRING", "STRING", "STRING", "STRING", "DECIMAL"],
    rows: [["articles_after_insert", "operator", "AFTER", "INSERT", "articles", "touch_articles", "1"]],
    truncated: false,
    elapsed_ms: 1,
  };
  const studioTableStats = {
    columns: ["table_name", "row_count", "updated_at"],
    column_types: ["STRING", "INT64", "TIMESTAMPTZ"],
    rows: [["articles", "1284", "2026-09-05T09:00:00Z"]],
    truncated: false,
    elapsed_ms: 1,
  };
  const studioIndexStats = {
    columns: ["table_name", "index_name", "row_count"],
    column_types: ["STRING", "STRING", "INT64"],
    rows: [
      ["articles", "PRIMARY", "1284"],
      ["articles", "ix_articles_text", "1284"],
    ],
    truncated: false,
    elapsed_ms: 1,
  };
  const studioTableDDL = {
    columns: ["object_type", "object_name", "ddl"],
    column_types: ["STRING", "STRING", "STRING"],
    rows: [
      ["TABLE", "articles", `CREATE TABLE "articles" ("id" INT64 PRIMARY KEY, "author_id" INT64 NOT NULL, "body" TEXT, CONSTRAINT "fk_articles_author" FOREIGN KEY ("author_id") REFERENCES "authors" ("id") ON DELETE CASCADE ON UPDATE RESTRICT)`],
      ["INDEX", "ix_articles_text", `CREATE FULLTEXT INDEX "ix_articles_text" ON "articles" ("body")`],
    ],
    truncated: false,
    elapsed_ms: 1,
  };
  const studioResultRows = Array.from({ length: 250 }, (_, index) => [
    String(index + 1),
    index === 0 ? "Hello" : index === 1 ? null : `Article ${index + 1}`,
    JSON.stringify({ published: index % 2 === 0, tags: ["nextsql", `row-${index + 1}`] }),
    "[0.25,-0.5,1]",
    "POINT(-73.98 40.75)",
    "2026-09-05T12:34:56.123Z",
  ]);
  const fullTextRows = [
    ["1", "<mark>Database</mark> performance", "A <mark>database</mark> performance guide"],
    ["2", "Tuning a <mark>database</mark>", "Practical <mark>performance</mark> notes"],
  ];
  const explainColumns = ["operator", "estimates", "actuals", "time", "cpu", "memory", "disk", "cache", "spill", "workers", "index"];
  const explainColumnTypes = explainColumns.map(() => "STRING");
  // Mirrors real `EXPLAIN ANALYZE SELECT * FROM t WHERE n > 5 ORDER BY n
  // LIMIT 2` output captured against a live nextsqld: 2-space-per-depth
  // indentation baked into the operator cell, actuals/time/cpu/workers
  // populated. TopNSort's actual (0) vs. estimated (2) rows is real and
  // unremarkable; SeqScan's is a synthetic 100x-off case for the
  // estimation-error highlight.
  const explainAnalyzeRows = [
    ["Limit 2", "rows=2 cost=25720", "rows=2", "145µs", "145µs", "64", "0", "0", "0", "1", ""],
    ["  TopNSort fetch=2 1", "rows=2 cost=25720", "rows=0", "0ns", "0ns", "0", "0", "0", "0", "1", ""],
    ["    Project id, n", "rows=330 cost=15160", "rows=3", "0ns", "0ns", "0", "0", "0", "0", "1", ""],
    ["      Filter (n > 5)", "rows=330 cost=14500", "rows=3", "0ns", "0ns", "0", "0", "0", "0", "1", ""],
    ["        SeqScan t", "rows=3 cost=14500", "rows=300", "0ns", "0ns", "0", "0", "0", "0", "1", ""],
  ];
  const explainAlternativeRows = [
    ["Limit 2", "rows=2 cost=800", "rows=2", "80µs", "75µs", "32", "0", "4", "0", "1", ""],
    ["  IndexScan t", "rows=2 cost=700", "rows=2", "70µs", "65µs", "16", "0", "4", "0", "1", "idx_t_fast"],
    ["    Filter (n > 5)", "rows=2 cost=100", "rows=2", "5µs", "5µs", "0", "0", "0", "0", "1", ""],
    ["      BitmapIndex idx_t_fast", "rows=2 cost=50", "rows=2", "3µs", "3µs", "0", "0", "0", "0", "1", "idx_t_fast"],
  ];
  let authenticated = false;
  let streamCalls = 0;
  let lastAnalyzeSQL = null;
  let lastStreamSQL = null;
  let lastStreamParams = null;
  let queryCalls = 0;
  let securityCalls = 0;
  let auditTampered = false;
  let activityCalls = 0;
  let alternativeExplain = false;
  let studioTableCalls = 0;
  let studioWorkflowsCalls = 0;
  let studioMigrationsCalls = 0;

  await withServer((request, response) => {
    const url = new URL(request.url, "http://127.0.0.1");
    if (serveAssets(request, response)) return;
    if (url.pathname === "/api/v1/mode") return json(response, 200, { mode: "operate" });
    if (url.pathname === "/api/v1/session" && request.method === "GET") {
      if (authenticated) return json(response, 200, { authenticated: true, user: "operator", database: "default", realm: "default", csrf_token: "browser-test" });
      return json(response, 401, { error: "not signed in" });
    }
    if (url.pathname === "/api/v1/session" && request.method === "POST") {
      authenticated = true;
      return json(response, 200, { authenticated: true, user: "operator", database: "default", realm: "default", csrf_token: "browser-test" });
    }
    if (url.pathname === "/api/v1/session" && request.method === "DELETE") {
      authenticated = false;
      return json(response, 200, {});
    }
    if (url.pathname === "/api/v1/security") {
      securityCalls++;
      return json(response, 200, {
        generated_at: new Date(securityCalls * 1_000).toISOString(),
        users: { columns: ["name", "password_algo"], rows: [["app", "argon2id"], ["auditor", "argon2id"]] },
        roles: { columns: ["role", "members"], rows: [["analyst", "app"]] },
        grants: { columns: ["grantee", "privilege", "scope", "object"], rows: [["app", "select", "table", "articles"]] },
        tls: { columns: ["enabled"], rows: [["FALSE"]] },
        key_versions: { columns: ["key_name"], rows: [] },
        audit_verify: {
          columns: ["lines", "legacy_count", "chained_count", "signed_count", "signing_started", "signatures_checked", "verified", "first_bad_line", "problem"],
          rows: [["7", "0", "7", "7", "TRUE", "TRUE", auditTampered ? "FALSE" : "TRUE", auditTampered ? "7" : "0", auditTampered ? "hash chain mismatch" : ""]],
        },
        audit_log: {
          columns: ["seq", "event_time", "actor", "action_name", "object", "outcome", "remote", "identity_source", "signed"],
          rows: [["7", "2026-09-05T10:00:00Z", "operator", "query", "articles", "success", "127.0.0.1:40000", "token", "TRUE"]],
        },
      });
    }
    if (url.pathname === "/api/v1/activity") {
      activityCalls++;
      return json(response, 200, {
        generated_at: new Date(activityCalls * 1_000).toISOString(),
        sessions: {
          columns: ["session_id", "user", "remote", "state"],
          rows: [["42", "operator", "127.0.0.1:40000", "idle"]],
        },
        active_queries: {
          columns: ["query_id", "user", "sql", "state"],
          rows: [["query-7", "operator", "SELECT * FROM system.active_queries", "running"]],
        },
        transactions: {
          columns: ["txn_id", "user", "isolation", "state"],
          rows: [["9001", "operator", "SNAPSHOT", "active"]],
        },
        locks: {
          columns: ["lock_id", "table_name", "mode", "granted"],
          rows: [["9001:1", "articles", "exclusive", "true"]],
        },
      });
    }
    if (url.pathname === "/api/v1/overview") {
      return json(response, 200, {
        generated_at: new Date(0).toISOString(),
        storage: result, replication: result, capabilities: result,
        sessions: 1, active_queries: 0, clustered: false,
      });
    }
    if (url.pathname === "/api/v1/studio/bootstrap") {
      return json(response, 200, {
        generated_at: new Date(0).toISOString(),
        server_addr: "127.0.0.1:7210",
        read_consistency: "strong",
        max_staleness_ms: 0,
        capabilities: studioCapabilities,
        tables: studioTables,
        tables_truncated: false,
      });
    }
    if (url.pathname === "/api/v1/studio/reconnect" && request.method === "POST") {
      let data = "";
      request.on("data", (chunk) => { data += chunk; });
      request.on("end", () => {
        let body = {};
        try { body = JSON.parse(data); } catch { body = {}; }
        json(response, 200, { user: "operator", realm: body.realm ?? "", database: body.database ?? "" });
      });
      return;
    }
    if (url.pathname === "/api/v1/studio/read-consistency" && request.method === "POST") {
      let data = "";
      request.on("data", (chunk) => { data += chunk; });
      request.on("end", () => {
        let body = {};
        try { body = JSON.parse(data); } catch { body = {}; }
        json(response, 200, {
          mode: body.mode ?? "strong",
          max_staleness_ms: body.mode === "bounded" ? (body.max_staleness_ms ?? 0) : 0,
        });
      });
      return;
    }
    if (url.pathname === "/api/v1/studio/table") {
      studioTableCalls++;
      return json(response, 200, {
        generated_at: new Date(0).toISOString(),
        name: "articles",
        table: studioTables,
        columns: studioColumns,
        indexes: studioIndexes,
        foreign_keys: studioForeignKeys,
        referencing_keys: studioReferencingKeys,
        triggers: studioTriggers,
        table_stats: studioTableStats,
        index_stats: studioIndexStats,
        ddl: studioTableDDL,
      });
    }
    if (url.pathname === "/api/v1/studio/workflows") {
      studioWorkflowsCalls++;
      return json(response, 200, {
        generated_at: new Date(studioWorkflowsCalls * 1_000).toISOString(),
        workflows: {
          columns: ["name", "owner", "param_count", "statement_count"],
          rows: [
            ["rollup_daily", "operator", "1", "2"],
            ["touch_articles", "operator", "1", "1"],
          ],
        },
        triggers: {
          columns: ["name", "owner", "timing", "event", "table_name", "workflow", "arg_count"],
          rows: [["articles_after_insert", "operator", "AFTER", "INSERT", "articles", "touch_articles", "1"]],
          truncated: false,
        },
        schedules: {
          columns: ["name", "owner", "kind", "spec", "workflow", "enabled", "next_fire", "last_fire"],
          rows: [["nightly", "operator", "EVERY", "24h0m0s", "rollup_daily", "true", "2026-09-07T00:00:00Z", "2026-09-06T00:00:00Z"]],
          truncated: false,
        },
        tasks: {
          columns: ["id", "schedule", "workflow", "state", "attempts"],
          rows: [
            ["s/00000007/1787616000000000000", "nightly", "rollup_daily", "succeeded", "1"],
            ["s/00000009/1787702400000000000", "nightly", "rollup_daily", "pending", "0"],
            ["s/00000011/1787788800000000000", "hourly", "touch_articles", "pending", "0"],
          ],
        },
        change_streams: {
          columns: ["table_name", "lsn", "state"],
          rows: [["articles", "914203", "active"]],
        },
      });
    }
    if (url.pathname === "/api/v1/studio/schema-graph") {
      return json(response, 200, {
        generated_at: new Date(0).toISOString(),
        truncated: false,
        foreign_keys: {
          columns: fkColumns,
          column_types: fkColumnTypes,
          rows: [
            ["comments", "fk_comments_article", "1", "article_id", "articles", "id", "CASCADE", "RESTRICT"],
            ["articles", "fk_articles_author", "1", "author_id", "authors", "id", "CASCADE", "RESTRICT"],
          ],
        },
      });
    }
    if (url.pathname === "/api/v1/studio/migrations") {
      studioMigrationsCalls++;
      return json(response, 200, {
        generated_at: new Date(studioMigrationsCalls * 1_000).toISOString(),
        present: true,
        truncated: false,
        history: {
          columns: ["version", "name", "applied_at", "execution_ms", "dirty", "direction", "checksum"],
          column_types: ["STRING", "STRING", "TIMESTAMPTZ", "DECIMAL", "DECIMAL", "STRING", "STRING"],
          rows: [
            ["0001", "init_schema", "2026-01-01T00:00:00Z", "42", "0", "up", "abc123"],
            ["0002", "add_articles", "2026-01-02T00:00:00Z", "17", "1", "up", "def456"],
          ],
        },
      });
    }
    if (url.pathname === "/api/v1/studio/query/analyze" && request.method === "POST") {
      let data = "";
      request.on("data", (chunk) => { data += chunk; });
      request.on("end", () => {
        let sql = "";
        try { sql = JSON.parse(data).sql ?? ""; } catch { sql = ""; }
        lastAnalyzeSQL = sql;
        const upper = sql.toUpperCase();
        const write = /^\s*(INSERT|UPSERT|UPDATE|DELETE|CREATE|DROP|ALTER|GRANT|REVOKE|BEGIN|COMMIT|ROLLBACK|RUN\s+WORKFLOW|BACKUP|SET\s+CONFIG)\b/.test(upper);
        if (/^\s*DELETE\b/.test(upper) && !/\bWHERE\b/.test(upper)) {
          return json(response, 200, { kind: "DELETE", destructive: true, write: true, reasons: ["No WHERE clause: this DELETE removes every row from articles."] });
        }
        if (/^\s*UPDATE\b/.test(upper) && !/\bWHERE\b/.test(upper)) {
          return json(response, 200, { kind: "UPDATE", destructive: true, write: true, reasons: ["No WHERE clause: this UPDATE modifies every row in articles."] });
        }
        if (/^\s*DROP\s+TABLE\b/.test(upper)) {
          return json(response, 200, { kind: "DROP TABLE", destructive: true, write: true, reasons: ["Permanently drops table articles and all of its data."] });
        }
        if (/^\s*INSERT\b/.test(upper)) {
          return json(response, 200, { kind: "Insert", destructive: false, write: true });
        }
        if (/^\s*CREATE\s+USER\b/.test(upper)) {
          return json(response, 200, { kind: "CreateUser", destructive: false, write: true, realm_scoped: true });
        }
        if (/^\s*DROP\s+USER\b/.test(upper)) {
          return json(response, 200, { kind: "DropUser", destructive: true, write: true, realm_scoped: true, reasons: ["Permanently removes login contractor."] });
        }
        return json(response, 200, { kind: write ? "Write" : "SELECT", destructive: false, write });
      });
      return;
    }
    if (url.pathname === "/api/v1/studio/query/stream" && request.method === "POST") {
      let data = "";
      request.on("data", (chunk) => { data += chunk; });
      request.on("end", async () => {
        try {
          const parsed = JSON.parse(data);
          lastStreamSQL = parsed.sql ?? "";
          lastStreamParams = parsed.params ?? null;
        } catch { lastStreamSQL = ""; lastStreamParams = null; }
        streamCalls++;
        if (lastStreamSQL.includes("STUDIO_TEST_SLOW")) {
          await new Promise((resolve) => setTimeout(resolve, 300));
        }
        if (/^\s*EXPLAIN\b/i.test(lastStreamSQL)) {
          const rows = alternativeExplain ? explainAlternativeRows : explainAnalyzeRows;
          return ndjson(response, [
            { type: "meta", columns: explainColumns, column_types: explainColumnTypes },
            { type: "rows", rows },
            { type: "complete", row_count: rows.length, elapsed_ms: 1 },
          ]);
        }
        if (/\bSEARCH\b/i.test(lastStreamSQL)) {
          return ndjson(response, [
            { type: "meta", columns: ["id", "title_snippet", "body_snippet"], column_types: ["INT64", "STRING", "TEXT"] },
            { type: "rows", rows: fullTextRows },
            { type: "complete", row_count: fullTextRows.length, elapsed_ms: 1 },
          ]);
        }
        if (/^\s*UPDATE\b/i.test(lastStreamSQL) && /\bWHERE\b/i.test(lastStreamSQL)) {
          return ndjson(response, [
            { type: "meta", columns: [], column_types: [] },
            { type: "complete", row_count: 0, affected: 3, elapsed_ms: 1 },
          ]);
        }
        ndjson(response, [
          {
            type: "meta",
            columns: ["id", "title", "metadata", "embedding", "location", "created_at"],
            column_types: ["INT64", "STRING", "JSON", "VECTOR<F32,3>", "POINT", "TIMESTAMPTZ"],
          },
          { type: "rows", rows: studioResultRows.slice(0, 128) },
          { type: "rows", rows: studioResultRows.slice(128) },
          { type: "complete", row_count: studioResultRows.length, elapsed_ms: 2 },
        ]);
      });
      return;
    }
    if (url.pathname === "/api/v1/studio/query/cancel" && request.method === "POST") {
      return json(response, 202, { canceled: true, query_id: "fixture" });
    }
    if (url.pathname === "/api/v1/studio/query/split" && request.method === "POST") {
      let data = "";
      request.on("data", (chunk) => { data += chunk; });
      request.on("end", () => {
        let sql = "";
        try { sql = JSON.parse(data).sql ?? ""; } catch { sql = ""; }
        const statements = sql.split(";").map((s) => s.trim()).filter(Boolean);
        return json(response, 200, { statements });
      });
      return;
    }
    if (url.pathname === "/api/v1/studio/query" && request.method === "POST") {
      let data = "";
      request.on("data", (chunk) => { data += chunk; });
      request.on("end", () => {
        let sql = "";
        try { sql = JSON.parse(data).sql ?? ""; } catch { sql = ""; }
        queryCalls++;
        // A DDL/DML statement (no result set) reports null column metadata —
        // this fixture deliberately mirrors that real-server shape so the
        // Studio frontend's null-safe normalization stays under test.
        if (/^\s*SELECT\b/i.test(sql)) {
          return json(response, 200, { columns: ["n"], column_types: ["INT64"], rows: [["1"]], truncated: false, elapsed_ms: 1 });
        }
        return json(response, 200, { columns: null, column_types: [], rows: null, affected: 1, truncated: false, elapsed_ms: 1 });
      });
      return;
    }
    json(response, 404, { error: "not found" });
  }, async (browser) => {
    await browser.waitFor("document.querySelector('h1')?.textContent === 'NextSQL Admin'", "the Operations login view");
    await runAxe(browser, axe.source, "Operations login");
    await themeCheck(browser, "Operations");
    await contrastAndMotionCheck(browser, "Operations");

    await browser.evaluate("document.getElementById('f-user').focus()");
    await browser.insertText("operator");
    await browser.evaluate("document.getElementById('f-pw').focus()");
    await browser.insertText("password1");
    const focused = await browser.evaluate(`(() => {
      const button = [...document.querySelectorAll("button")].find((item) => item.textContent.trim() === "Sign in");
      button?.focus();
      return Boolean(button);
    })()`);
    assert.equal(focused, true);
    await browser.press("Enter");

    await browser.waitFor("document.querySelector('h1')?.textContent === 'Overview'", "the Operations overview");
    await browser.waitFor("document.querySelectorAll('tbody tr').length > 0", "the overview data");
    await runAxe(browser, axe.source, "Operations overview");
    assert.equal(await browser.evaluate("document.querySelector('.nsa-skip')?.getAttribute('href')"), "#admin-main");
    assert.equal(await browser.evaluate("document.querySelector('.nsm-sidebar nav')?.getAttribute('aria-label')"), "Operations");
    await highDensityCheck(
      browser,
      "Operations overview",
      "getComputedStyle(document.querySelector('.nsm-sidebar')).display === 'none' && getComputedStyle(document.querySelector('.nsm-mobile-nav')).display !== 'none'",
    );

    // Studio: authenticated development workspace, reachable through the
    // same URL-addressable shell route.
    const studioTab = await browser.evaluate(`(() => {
      const tab = [...document.querySelectorAll('[role=tab]')].find((item) => item.textContent.trim() === "Studio");
      tab?.click();
      return Boolean(tab);
    })()`);
    assert.equal(studioTab, true, "a Studio nav entry should exist");
    await browser.waitFor("document.querySelector('h1')?.textContent === 'Studio'", "the Studio workspace");
    await browser.waitFor("document.body.textContent.includes('articles')", "the authorized Studio catalog");
    assert.equal(await browser.evaluate("window.location.hash"), "#!/studio", "selecting Studio should update the URL route");
    assert.equal(await browser.evaluate("document.querySelector('[aria-label=\"SQL editor\"]') !== null"), true, "the SQL editor should have an accessible name");
    assert.equal(await browser.evaluate("document.body.textContent.includes('127.0.0.1:7210')"), true, "the toolbar should show the nextsqld the session targets");
    await runAxe(browser, axe.source, "Studio workspace");
    await highDensityCheck(
      browser,
      "Studio workspace",
      "getComputedStyle(document.querySelector('.nss-layout')).gridTemplateColumns.split(' ').length === 1",
    );

    // The connection switcher: open the modal, axe-check it, perform a real
    // realm switch, then confirm it is offered as a recent connection.
    const openSwitch = `(() => {
      const button = [...document.querySelectorAll("button")].find((item) => item.textContent.trim() === "Switch connection…");
      button?.click();
      return Boolean(button);
    })()`;
    assert.equal(await browser.evaluate(openSwitch), true, "the toolbar should offer a Switch connection control");
    await browser.waitFor("document.querySelector('[role=dialog]')?.textContent.includes('Switch connection')", "the switch-connection modal");
    await runAxe(browser, axe.source, "Studio switch-connection modal");
    await browser.evaluate(`(() => {
      const setValue = (el, value) => {
        const proto = Object.getPrototypeOf(el);
        Object.getOwnPropertyDescriptor(proto, "value").set.call(el, value);
        el.dispatchEvent(new Event("input", { bubbles: true }));
      };
      const dialog = document.querySelector("[role=dialog]");
      const text = [...dialog.querySelectorAll('input:not([type="password"])')];
      setValue(text[0], "reporting");
      setValue(dialog.querySelector('input[type="password"]'), "s3cret");
      [...dialog.querySelectorAll("button")].find((b) => b.textContent.trim() === "Switch")?.click();
    })()`);
    await browser.waitFor("document.querySelector('[role=dialog]') === null", "the switch-connection modal to close after a switch");
    await browser.waitFor("document.body.textContent.includes('realm:reporting')", "the toolbar to reflect the switched realm");
    await browser.evaluate(openSwitch);
    await browser.waitFor("document.querySelector('[role=dialog]')?.textContent.includes('Recent on this server')", "the recent-connections section");
    assert.equal(
      await browser.evaluate(`[...document.querySelectorAll('[role=dialog] button')].some((b) => b.textContent.includes('reporting'))`),
      true,
      "the just-used connection should be offered as a recent quick-switch",
    );
    // Switch back to the default connection so later tests run against it.
    await browser.evaluate(`(() => {
      const setValue = (el, value) => {
        Object.getOwnPropertyDescriptor(Object.getPrototypeOf(el), "value").set.call(el, value);
        el.dispatchEvent(new Event("input", { bubbles: true }));
      };
      const dialog = document.querySelector("[role=dialog]");
      setValue(dialog.querySelector('input:not([type="password"])'), "");
      setValue(dialog.querySelector('input[type="password"]'), "s3cret");
      [...dialog.querySelectorAll("button")].find((b) => b.textContent.trim() === "Switch")?.click();
    })()`);
    await browser.waitFor("document.querySelector('[role=dialog]') === null", "the switch-connection modal to close");
    await browser.waitFor("!document.body.textContent.includes('realm:reporting')", "the toolbar to return to the default connection");

    // Read-consistency control: switching to Bounded reveals a staleness
    // input and shows the stale-reads badge; switching back to Strong hides
    // both. Applied live against POST /studio/read-consistency.
    assert.equal(await browser.evaluate("document.getElementById('studio-read-consistency') !== null"), true, "the toolbar should offer a read-consistency selector");
    await browser.evaluate("document.getElementById('studio-read-consistency')?.click()");
    await browser.waitFor("document.getElementById('studio-read-consistency')?.getAttribute('aria-expanded') === 'true'", "the read-consistency menu to open");
    await browser.evaluate("document.getElementById('studio-read-consistency-list-opt-1')?.click()");
    await browser.waitFor("document.getElementById('studio-read-staleness') !== null", "the bounded-staleness input");
    assert.equal(await browser.evaluate("document.body.textContent.includes('bounded reads')"), true, "a non-strong read mode should show the stale-reads badge");
    await runAxe(browser, axe.source, "Studio workspace with bounded reads");
    await browser.evaluate("document.getElementById('studio-read-consistency')?.click()");
    await browser.waitFor("document.getElementById('studio-read-consistency')?.getAttribute('aria-expanded') === 'true'", "the read-consistency menu to reopen");
    await browser.evaluate("document.getElementById('studio-read-consistency-list-opt-0')?.click()");
    await browser.waitFor("document.getElementById('studio-read-staleness') === null", "the staleness input to disappear for strong reads");

    // Command palette: Ctrl+K opens it anywhere; it is a combobox over
    // Studio's own actions, and Enter on a filtered command runs it.
    await browser.evaluate(`window.dispatchEvent(new KeyboardEvent("keydown", { key: "k", ctrlKey: true, bubbles: true }))`);
    await browser.waitFor("document.querySelector('[role=dialog]')?.textContent.includes('Command palette')", "the command palette");
    assert.equal(await browser.evaluate("document.getElementById('studio-command-palette')?.getAttribute('role')"), "combobox", "the palette input is a combobox");
    await runAxe(browser, axe.source, "Studio command palette");
    await browser.evaluate(`(() => {
      const input = document.getElementById("studio-command-palette");
      Object.getOwnPropertyDescriptor(Object.getPrototypeOf(input), "value").set.call(input, "geo explorer");
      input.dispatchEvent(new Event("input", { bubbles: true }));
    })()`);
    await browser.waitFor(`document.querySelector('#studio-command-palette-results [role=option]')?.textContent.includes('Geo explorer')`, "the palette to filter to the Geo command");
    await browser.evaluate(`document.getElementById("studio-command-palette").dispatchEvent(new KeyboardEvent("keydown", { key: "Enter", bubbles: true }))`);
    await browser.waitFor(`document.querySelector('[role=dialog]')?.textContent.includes('Geo Explorer')`, "the Geo explorer opened from the command palette");
    await browser.evaluate(`document.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true }))`);
    await browser.waitFor("document.querySelector('[role=dialog]') === null", "the Geo explorer to close");

    const tableOpened = await browser.evaluate(`(() => {
      const button = [...document.querySelectorAll("button")].find((item) => item.textContent.trim().endsWith("articles"));
      button?.click();
      return Boolean(button);
    })()`);
    assert.equal(tableOpened, true, "the table explorer should be interactive");
    await browser.waitFor("document.getElementById('studio-columns-title') !== null", "the lazy table detail");
    await browser.waitFor("document.getElementById('studio-constraints-title') !== null", "the unified table constraints section");
    await browser.waitFor("document.getElementById('studio-foreign-keys-title') !== null", "the table foreign-keys section");
    await browser.waitFor("document.getElementById('studio-statistics-title') !== null", "the table statistics section");
    assert.equal(await browser.evaluate("document.body.textContent.includes('1284')"), true, "recorded row counts from system.table_stats/system.index_stats should render");
    assert.equal(await browser.evaluate("document.body.textContent.includes('fk_articles_author')"), true, "the authorized system.foreign_keys row should render in the inspector");
    assert.equal(
      await browser.evaluate(`(() => {
        const table = document.querySelector('table[aria-label="articles constraints"]');
        const text = table?.textContent ?? '';
        return text.includes('PRIMARY KEY') && text.includes('UNIQUE INDEX') && text.includes('uq_articles_title') && text.includes('NOT NULL') && text.includes('FOREIGN KEY');
      })()`),
      true,
      "the unified constraints grid should render PK, UNIQUE-index, NOT NULL, and FK metadata",
    );
    await browser.waitFor("document.getElementById('studio-dependencies-title') !== null", "the table dependencies section");
    assert.equal(await browser.evaluate("document.body.textContent.includes('Referenced by') && document.body.textContent.includes('fk_comments_article')"), true, "inbound foreign-key references should render under 'Referenced by'");
    assert.equal(
      await browser.evaluate(`(() => {
        const section = document.getElementById('studio-dependencies-title')?.closest('section');
        const text = section?.textContent ?? '';
        return text.includes('Triggers') && text.includes('articles_after_insert') && text.includes('touch_articles');
      })()`),
      true,
      "row triggers on the table from system.triggers should render under Dependencies",
    );
    await browser.waitFor("document.getElementById('studio-ddl-title') !== null", "the table DDL section");
    assert.equal(
      await browser.evaluate("document.body.textContent.includes('CREATE TABLE \"articles\"') && document.body.textContent.includes('CREATE FULLTEXT INDEX')"),
      true,
      "the canonical CREATE statements from system.table_ddl should render",
    );
    assert.equal(
      await browser.evaluate(`[...document.querySelectorAll('button')].some((b) => b.textContent.trim() === 'Copy DDL')`),
      true,
      "the DDL section offers a copy control",
    );

    // Selecting the table also expands its lazy tree node; its Columns and
    // Indexes sub-branches come from the same authorized system.* read.
    await browser.waitFor(
      `[...document.querySelectorAll('.nss-tree-branch-label')].some((el) => el.textContent.trim() === 'Columns')`,
      "the lazy schema tree's Columns sub-branch",
    );
    const columnsExpanded = await browser.evaluate(`(() => {
      const button = [...document.querySelectorAll('.nss-tree-branch')].find((el) => el.textContent.includes('Columns'));
      button?.click();
      return Boolean(button);
    })()`);
    assert.equal(columnsExpanded, true, "the tree Columns sub-branch should be expandable");
    await browser.waitFor("document.body.textContent.includes('title \\u00b7 STRING')", "a lazily-rendered column leaf");

    const fkExpanded = await browser.evaluate(`(() => {
      const button = [...document.querySelectorAll('.nss-tree-branch')].find((el) => el.textContent.includes('Foreign keys'));
      button?.click();
      return Boolean(button);
    })()`);
    assert.equal(fkExpanded, true, "the tree Foreign keys sub-branch should be expandable");
    await browser.waitFor(
      "document.body.textContent.includes('(author_id) \\u2192 authors (id)')",
      "a lazily-rendered foreign-key leaf from system.foreign_keys",
    );

    const triggersExpanded = await browser.evaluate(`(() => {
      const button = [...document.querySelectorAll('.nss-tree-branch')].find((el) => el.textContent.includes('Triggers'));
      button?.click();
      return Boolean(button);
    })()`);
    assert.equal(triggersExpanded, true, "the tree Triggers sub-branch should be expandable");
    await browser.waitFor(
      "document.body.textContent.includes('articles_after_insert')",
      "a lazily-rendered trigger leaf from system.triggers",
    );

    const workflowsExpanded = await browser.evaluate(`(() => {
      const button = [...document.querySelectorAll('.nss-tree-branch')].find((el) => el.textContent.includes('Workflows'));
      button?.click();
      return Boolean(button);
    })()`);
    assert.equal(workflowsExpanded, true, "the tree Workflows branch should be expandable");
    await browser.waitFor("document.body.textContent.includes('rollup_daily')", "the lazily-loaded workflow branch");
    assert.ok(studioWorkflowsCalls >= 1, "expanding Workflows should trigger exactly one authorized workflows read");
    await runAxe(browser, axe.source, "Studio schema tree");

    const queryRun = await browser.evaluate(`(() => {
      const button = [...document.querySelectorAll("button")].find((item) => item.textContent.trim() === "Run query");
      button?.click();
      return Boolean(button);
    })()`);
    assert.equal(queryRun, true, "the query action should be available");
    await browser.waitFor("document.body.textContent.includes('Hello')", "the streamed Studio query result");
    assert.equal(await browser.evaluate("document.body.textContent.includes('250 rows · 6 columns · 2 ms')"), true);
    assert.equal(await browser.evaluate("document.querySelector('.nss-virtual-table')?.getAttribute('aria-rowcount')"), "251");
    assert.equal(await browser.evaluate("document.querySelectorAll('.nss-virtual-table tbody tr[aria-rowindex]').length < 100"), true, "the result grid should mount only a visible row window");
    assert.equal(await browser.evaluate("document.body.textContent.includes('3-d vector')"), true, "vectors should use a compact cell representation");
    assert.equal(await browser.evaluate(`(() => {
      const results = document.querySelector('section[aria-labelledby="studio-results-title"]');
      return ["Copy cell", "Copy all rows", "Inspect native value", "Export CSV", "Export JSON"]
        .every((label) => [...results.querySelectorAll("button")].some((button) => button.textContent.trim() === label));
    })()`), true, "bounded result actions should be present");

    await browser.evaluate(`document.querySelector('section[aria-labelledby="studio-results-title"] input[aria-label="Select result row 1"]')?.click()`);
    await browser.waitFor("document.body.textContent.includes('1 selected')", "a selected result row");

    const inspectCell = async (title, expected) => {
      const selected = await browser.evaluate(`(() => {
        const results = document.querySelector('section[aria-labelledby="studio-results-title"]');
        const cell = [...results.querySelectorAll('.nss-cell-button')].find((button) => button.title === ${JSON.stringify(title)});
        cell?.click();
        return Boolean(cell);
      })()`);
      assert.equal(selected, true, `the ${title} cell should be inspectable`);
      await browser.waitFor(`(() => {
        const results = document.querySelector('section[aria-labelledby="studio-results-title"]');
        const inspect = [...results.querySelectorAll("button")].find((button) => button.textContent.trim() === "Inspect native value");
        return Boolean(inspect && !inspect.disabled);
      })()`, `${title} inspector action enabled`);
      await browser.evaluate(`(() => {
        const results = document.querySelector('section[aria-labelledby="studio-results-title"]');
        [...results.querySelectorAll("button")].find((button) => button.textContent.trim() === "Inspect native value")?.click();
      })()`);
      await browser.waitFor(`document.querySelector('[role="dialog"]')?.textContent.includes(${JSON.stringify(expected)})`, `${expected} inspector`);
    };

    const closeInspector = async () => {
      await browser.evaluate(`document.querySelector('button[aria-label="Close cell inspector"]')?.click()`);
      await browser.waitFor("document.querySelector('[role=dialog]') === null", "cell inspector closed");
    };

    await inspectCell(studioResultRows[0][2], "Tree");
    assert.equal(await browser.evaluate("document.querySelector('[role=dialog] .nss-json-tree') !== null"), true, "JSON tree should render");
    assert.equal(await browser.evaluate("document.querySelector('[role=dialog] h2')?.textContent.includes('JSON Explorer')"), true, "the JSON inspector should identify the dedicated explorer");
    const firstTagPath = '"metadata"."tags".0';
    assert.equal(await browser.evaluate(`(() => {
      const button = [...document.querySelectorAll('[role=dialog] .nss-json-node-button')]
        .find((item) => item.getAttribute('aria-label') === ${JSON.stringify(`Select JSON path ${firstTagPath}`)});
      button?.click();
      return Boolean(button);
    })()`), true, "an array element should be selectable by its native JSON path");
    await browser.waitFor(
      `document.querySelector('[role=dialog] .nss-json-path-code')?.textContent.includes(${JSON.stringify(firstTagPath)})`,
      "the selected native JSON path",
    );
    assert.equal(await browser.evaluate("document.querySelector('[role=dialog]')?.textContent.includes('Indexed')"), true, "the selected path should show its live system.indexes status");
    assert.equal(await browser.evaluate("document.querySelector('[role=dialog]')?.textContent.includes('ix_metadata_first_tag')"), true, "the matching index should be named");
    assert.equal(await browser.evaluate(`[...document.querySelectorAll('[role=dialog] button')].some((button) => button.textContent.trim().includes('Copy JSON path'))`), true, "the native JSON path should be copyable");
    await runAxe(browser, axe.source, "Studio JSON path explorer");
    await browser.evaluate(`[...document.querySelectorAll('[role=dialog] [role=tab]')].find((tab) => tab.textContent.trim() === "Raw")?.click()`);
    await browser.waitFor("[...document.querySelectorAll('[role=dialog] [role=tab]')].some((tab) => tab.textContent.trim() === 'Raw' && tab.getAttribute('aria-selected') === 'true')", "raw JSON tab selected");
    assert.equal(await browser.evaluate(`document.querySelector('[role=dialog] .nss-inspector-code')?.textContent.includes(${JSON.stringify(studioResultRows[0][2])})`), true, "raw JSON should be preserved");
    await runAxe(browser, axe.source, "Studio JSON inspector");
    const callsBeforeJSONInsert = streamCalls;
    assert.equal(await browser.evaluate(`(() => {
      const button = [...document.querySelectorAll('[role=dialog] button')].find((item) => item.textContent.trim() === 'Insert path query');
      button?.click();
      return Boolean(button);
    })()`), true, "the selected JSON path query should be insertable");
    await browser.waitFor("document.querySelector('[role=dialog]') === null", "the JSON Explorer to close after inserting");
    assert.equal(
      await browser.evaluate(`document.querySelector('[aria-label="SQL editor"]')?.value`),
      'SELECT "metadata"."tags".0 FROM "articles" LIMIT 100',
      "the JSON Explorer should generate the exact native array-index path and only update the editor",
    );
    assert.equal(streamCalls, callsBeforeJSONInsert, "inserting a generated JSON-path query must not execute it");

    await inspectCell(studioResultRows[0][3], "Declared dimensions");
    await closeInspector();
    await inspectCell(studioResultRows[0][4], "longitude, latitude");
    assert.equal(await browser.evaluate("document.querySelector('[role=dialog] svg[aria-label=\"POINT coordinate preview\"]') !== null"), true);
    await closeInspector();
    await inspectCell(studioResultRows[0][5], "Browser timezone");
    await closeInspector();
    await runAxe(browser, axe.source, "Studio table detail and result");

    // Confirm-before-run: a statement analyzed as destructive must show a
    // warning naming why, and canceling it must not execute the query.
    const setSQL = async (sql) => {
      const applied = await browser.evaluate(`(() => {
        const editor = document.querySelector('[aria-label="SQL editor"]');
        if (!editor) return false;
        const setter = Object.getOwnPropertyDescriptor(window.HTMLTextAreaElement.prototype, "value").set;
        setter.call(editor, ${JSON.stringify(sql)});
        editor.dispatchEvent(new Event("input", { bubbles: true }));
        return true;
      })()`);
      assert.equal(applied, true, "the SQL editor should accept scripted input");
    };
    const setInputById = (id, value) => browser.evaluate(`(() => {
      const el = document.getElementById(${JSON.stringify(id)});
      if (!el) return false;
      const setter = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, "value").set;
      setter.call(el, ${JSON.stringify(value)});
      el.dispatchEvent(new Event("input", { bubbles: true }));
      return true;
    })()`);
    const clickButton = (label) => browser.evaluate(`(() => {
      const button = [...document.querySelectorAll("button")].find((item) => item.textContent.trim() === ${JSON.stringify(label)});
      button?.click();
      return Boolean(button);
    })()`);
    const clickDialogButton = (label) => browser.evaluate(`(() => {
      const button = [...document.querySelectorAll("[role=dialog] button")].find((item) => item.textContent.trim() === ${JSON.stringify(label)});
      button?.click();
      return Boolean(button);
    })()`);
    const clickButtonStartingWith = (prefix) => browser.evaluate(`(() => {
      const button = [...document.querySelectorAll("button")].find((item) => item.textContent.trim().startsWith(${JSON.stringify(prefix)}));
      button?.click();
      return Boolean(button);
    })()`);
    const clickByAriaLabel = (label) => browser.evaluate(`(() => {
      const button = [...document.querySelectorAll("button")].find((item) => item.getAttribute("aria-label") === ${JSON.stringify(label)});
      button?.click();
      return Boolean(button);
    })()`);
    const waitForStreamCalls = async (min, message) => {
      const until = Date.now() + 4_000;
      while (Date.now() < until) {
        if (streamCalls >= min) return;
        await new Promise((resolve) => setTimeout(resolve, 20));
      }
      throw new Error(message);
    };
    const waitForActivityCalls = async (min, message) => {
      const until = Date.now() + 4_000;
      while (Date.now() < until) {
        if (activityCalls >= min) return;
        await new Promise((resolve) => setTimeout(resolve, 20));
      }
      throw new Error(message);
    };
    const waitForSecurityCalls = async (min, message) => {
      const until = Date.now() + 4_000;
      while (Date.now() < until) {
        if (securityCalls >= min) return;
        await new Promise((resolve) => setTimeout(resolve, 20));
      }
      throw new Error(message);
    };
    const waitForWorkflowsCalls = async (min, message) => {
      const until = Date.now() + 4_000;
      while (Date.now() < until) {
        if (studioWorkflowsCalls >= min) return;
        await new Promise((resolve) => setTimeout(resolve, 20));
      }
      throw new Error(message);
    };
    const waitForMigrationsCalls = async (min, message) => {
      const until = Date.now() + 4_000;
      while (Date.now() < until) {
        if (studioMigrationsCalls >= min) return;
        await new Promise((resolve) => setTimeout(resolve, 20));
      }
      throw new Error(message);
    };

    const callsBeforeDestructive = streamCalls;
    await setSQL("DELETE FROM articles");
    assert.equal(await clickButton("Run query"), true, "Run query should be clickable for a destructive statement");
    await browser.waitFor("document.querySelector('[role=dialog] h2')?.textContent === 'Confirm DELETE'", "the destructive-statement confirmation");
    assert.equal(await browser.evaluate("document.querySelector('[role=dialog]')?.textContent.includes('articles')"), true, "the confirmation should name the affected table");
    await runAxe(browser, axe.source, "Studio destructive-statement confirmation");

    assert.equal(await clickDialogButton("Cancel"), true, "the confirmation's Cancel should be available");
    await browser.waitFor("document.querySelector('[role=dialog]') === null", "the confirmation to close on cancel");
    assert.equal(streamCalls, callsBeforeDestructive, "canceling the confirmation must not execute the query");

    assert.equal(await clickButton("Run query"), true, "Run query should reopen the confirmation");
    await browser.waitFor("document.querySelector('[role=dialog] h2')?.textContent === 'Confirm DELETE'", "the confirmation to reopen");
    assert.equal(await clickDialogButton("Run anyway"), true, "the confirmation's Run anyway should be available");
    await browser.waitFor("document.querySelector('[role=dialog]') === null", "the confirmation to close after confirming");
    await waitForStreamCalls(callsBeforeDestructive + 1, "confirming should execute the query");

    await setSQL("SELECT * FROM system.capabilities ORDER BY name");
    const callsBeforeSafe = streamCalls;
    assert.equal(await clickButton("Run query"), true, "Run query should be clickable for a safe statement");
    await waitForStreamCalls(callsBeforeSafe + 1, "a safe statement should execute without a confirmation");
    assert.equal(await browser.evaluate("document.querySelector('[role=dialog]') === null"), true, "no confirmation should appear for a safe statement");

    // Cross-database administration warning: CREATE/DROP USER and
    // CREATE/DROP ROLE change the realm-wide principal namespace, so the
    // confirm-before-run dialog names the realm and current database even
    // though the statement is not "destructive".
    const callsBeforeRealm = streamCalls;
    await setSQL("CREATE USER contractor IDENTIFIED BY 'pw'");
    assert.equal(await clickButton("Run query"), true, "Run query should be clickable for a realm-scoped statement");
    await browser.waitFor("document.querySelector('[role=dialog] h2')?.textContent === 'Realm-wide change — run this CreateUser?'", "the realm-wide confirmation");
    assert.equal(await browser.evaluate("document.querySelector('[role=dialog]')?.textContent.includes('every database in the default realm')"), true, "the confirmation should explain the realm-wide reach");
    await runAxe(browser, axe.source, "Studio realm-wide administration confirmation");
    assert.equal(await clickDialogButton("Cancel"), true, "the realm-wide confirmation's Cancel should be available");
    await browser.waitFor("document.querySelector('[role=dialog]') === null", "the realm-wide confirmation to close on cancel");
    assert.equal(streamCalls, callsBeforeRealm, "canceling the realm-wide confirmation must not execute the statement");

    // Production safety mode: tagging the connection "production" shows a
    // standing banner and turns on read-only mode, which asks for
    // confirmation before any write (even a non-destructive one). Turning
    // read-only mode off lets writes run without the extra prompt. The tag
    // is a per-viewer browser preference — no credential, nothing sent.
    await browser.evaluate("document.getElementById('studio-environment')?.click()");
    await browser.waitFor("document.getElementById('studio-environment')?.getAttribute('aria-expanded') === 'true'", "the environment menu to open");
    await browser.evaluate("document.getElementById('studio-environment-list-opt-4')?.click()");
    await browser.waitFor(
      "[...document.querySelectorAll('[role=alert]')].some((el) => el.textContent.includes('Production environment'))",
      "the production banner to appear once the connection is tagged production",
    );
    assert.equal(
      await browser.evaluate("[...document.querySelectorAll('.nss-toolbar *')].some((el) => el.textContent.trim() === 'read-only')"),
      true,
      "read-only mode should be on by default for a production connection",
    );
    await runAxe(browser, axe.source, "Studio production connection (read-only)");

    await setSQL("INSERT INTO articles (id, title) VALUES (99, 'x')");
    const callsBeforeReadOnlyWrite = streamCalls;
    assert.equal(await clickButton("Run query"), true, "Run query should be clickable for a write in read-only mode");
    await browser.waitFor(
      "document.querySelector('[role=dialog] h2')?.textContent?.includes('Read-only mode') === true",
      "the read-only confirmation for a write",
    );
    assert.equal(
      await browser.evaluate("document.querySelector('[role=dialog]')?.textContent.includes('writes data')"),
      true,
      "the read-only confirmation should explain why the statement was held",
    );
    await runAxe(browser, axe.source, "Studio read-only mode write confirmation");
    assert.equal(await clickDialogButton("Cancel"), true, "the read-only confirmation's Cancel should be available");
    await browser.waitFor("document.querySelector('[role=dialog]') === null", "the read-only confirmation to close on cancel");
    assert.equal(streamCalls, callsBeforeReadOnlyWrite, "canceling the read-only confirmation must not execute the write");

    assert.equal(await clickButton("Turn off read-only mode"), true, "the banner's read-only toggle should be available");
    await browser.waitFor(
      "![...document.querySelectorAll('.nss-toolbar *')].some((el) => el.textContent.trim() === 'read-only')",
      "the read-only badge to disappear once the mode is turned off",
    );
    assert.equal(await clickButton("Run query"), true, "Run query should be clickable with read-only mode off");
    await waitForStreamCalls(callsBeforeReadOnlyWrite + 1, "a write should run without a prompt once read-only mode is off");
    assert.equal(await browser.evaluate("document.querySelector('[role=dialog]') === null"), true, "no confirmation for a write once read-only mode is off");

    // Untag the connection so later steps run against the default state.
    await browser.evaluate("document.getElementById('studio-environment')?.click()");
    await browser.waitFor("document.getElementById('studio-environment')?.getAttribute('aria-expanded') === 'true'", "the environment menu to reopen");
    await browser.evaluate("document.getElementById('studio-environment-list-opt-0')?.click()");
    await browser.waitFor(
      "![...document.querySelectorAll('[role=alert]')].some((el) => el.textContent.includes('Production environment'))",
      "the production banner to disappear once the tag is cleared",
    );

    // Execute selection: highlighting one statement inside a multi-line
    // buffer must analyze/run only the highlighted text, not the whole
    // editor, and the Run action should relabel itself accordingly.
    const twoLineBuffer = "SELECT * FROM articles\nDELETE FROM articles";
    const selectionText = "DELETE FROM articles";
    const selectionStart = twoLineBuffer.indexOf(selectionText);
    const selectionEnd = selectionStart + selectionText.length;
    await setSQL(twoLineBuffer);
    const selected = await browser.evaluate(`(() => {
      const editor = document.querySelector('[aria-label="SQL editor"]');
      if (!editor) return false;
      editor.focus();
      editor.setSelectionRange(${selectionStart}, ${selectionEnd});
      editor.dispatchEvent(new Event("select", { bubbles: true }));
      return true;
    })()`);
    assert.equal(selected, true, "the SQL editor should accept a scripted selection");
    await browser.waitFor(
      "[...document.querySelectorAll('button')].some((b) => b.textContent.trim() === 'Run selection')",
      "the Run action to relabel itself for the active selection",
    );

    assert.equal(await clickButton("Run selection"), true, "Run selection should be clickable");
    await browser.waitFor("document.querySelector('[role=dialog] h2')?.textContent === 'Confirm DELETE'", "the selection-scoped confirmation");
    assert.equal(lastAnalyzeSQL, selectionText, "only the selected text should have been analyzed, not the whole buffer");
    assert.equal(await browser.evaluate("document.querySelector('[role=dialog]')?.textContent.includes('This runs only the selected text.')"), true, "the confirmation should note it is scoped to the selection");
    await runAxe(browser, axe.source, "Studio selection-scoped confirmation");

    const callsBeforeSelection = streamCalls;
    assert.equal(await clickDialogButton("Run anyway"), true, "the confirmation's Run anyway should be available");
    await browser.waitFor("document.querySelector('[role=dialog]') === null", "the confirmation to close after confirming the selection");
    await waitForStreamCalls(callsBeforeSelection + 1, "confirming the selection should execute the query");
    assert.equal(lastStreamSQL, selectionText, "only the selected text should have been executed, not the whole buffer");
    await browser.waitFor("document.body.textContent.includes('Selection · 250 rows')", "the result status to note the run was scoped to the selection");

    // Query history: every successful run above should have been recorded
    // (canceled confirmations must not appear), most recent first; loading
    // an entry should populate the editor without re-running it; Clear
    // should empty the in-memory, session-only list.
    assert.equal(await clickButtonStartingWith("History"), true, "the History control should be available");
    await browser.waitFor("document.querySelector('[role=dialog][aria-label=\"Query history\"]') !== null", "the query history panel to open");
    const historyEntries = await browser.evaluate("[...document.querySelectorAll('.nss-history-item')].map((el) => el.textContent)");
    assert.equal(historyEntries.length, 5, `five successful statements should have been recorded, got ${historyEntries.length}`);
    assert.equal(historyEntries[0].includes("DELETE FROM articles") && historyEntries[0].includes("success"), true, "the most recent run should be first and show its outcome");
    assert.equal(historyEntries.some((e) => e.includes("INSERT INTO articles")), true, "the write run after turning off read-only mode should be recorded");
    await runAxe(browser, axe.source, "Studio query history panel");

    const capabilitiesHistoryIndex = await browser.evaluate(
      "[...document.querySelectorAll('.nss-history-item')].findIndex((el) => el.textContent.includes('SELECT * FROM system.capabilities ORDER BY name'))",
    );
    assert.ok(capabilitiesHistoryIndex >= 0, "the capabilities SELECT run should be in history");
    const loadedSecondEntry = await browser.evaluate(`(() => {
      const item = document.querySelectorAll(".nss-history-item")[${capabilitiesHistoryIndex}];
      item?.click();
      return Boolean(item);
    })()`);
    assert.equal(loadedSecondEntry, true, "a history entry should be clickable");
    await browser.waitFor("document.querySelector('[role=dialog][aria-label=\"Query history\"]') === null", "the history panel to close after loading an entry");
    assert.equal(
      await browser.evaluate(`document.querySelector('[aria-label="SQL editor"]')?.value`),
      "SELECT * FROM system.capabilities ORDER BY name",
      "loading a history entry should populate the editor without executing it",
    );
    assert.equal(streamCalls, callsBeforeSelection + 1, "loading a history entry must not execute a new query");

    assert.equal(await clickButtonStartingWith("History"), true, "the History control should reopen");
    await browser.waitFor("document.querySelector('[role=dialog][aria-label=\"Query history\"]') !== null", "the query history panel to reopen");
    assert.equal(await clickButton("Clear"), true, "Clear should be available");
    await browser.waitFor("document.body.textContent.includes('No queries run yet this session.')", "the history list to empty after Clear");
    assert.equal(await clickButtonStartingWith("History"), true, "the History control should close the panel when clicked again");
    await browser.waitFor("document.querySelector('[role=dialog][aria-label=\"Query history\"]') === null", "the history panel to close");

    // A column-less write result reports its affected-row count in the status
    // line, not a misleading "0 rows".
    await setSQL("UPDATE articles SET title = 'x' WHERE id = 1");
    assert.equal(await clickButton("Run query"), true, "Run query should be clickable for the write");
    await browser.waitFor(
      "[...document.querySelectorAll('.nss-query-status')].some((el) => el.textContent.includes('3 rows affected'))",
      "the status line to report the affected-row count for a column-less write",
    );

    // Prepared parameters: a $1/$2 buffer surfaces a bind panel; a filled
    // value and an explicit NULL travel with the Run request as a positional
    // params array. The panel appears only while placeholders are present.
    const paramCallsBefore = streamCalls;
    await setSQL("SELECT * FROM t WHERE a = $1 AND b = $2");
    await browser.waitFor("document.getElementById('studio-param-1') !== null", "the parameters panel to appear for a $1/$2 buffer");
    assert.equal(
      await browser.evaluate("document.getElementById('studio-param-2') !== null"),
      true,
      "one input per distinct placeholder",
    );
    await runAxe(browser, axe.source, "Studio query parameters panel");
    assert.equal(await setInputById("studio-param-1", "42"), true, "the $1 bind field should accept input");
    assert.equal(
      await browser.evaluate(`(() => {
        const box = [...document.querySelectorAll('.nss-params-item')][1]?.querySelector('input[type=checkbox]');
        box?.click();
        return Boolean(box);
      })()`),
      true,
      "the $2 NULL toggle should be available",
    );
    assert.equal(await clickButton("Run query"), true, "Run query should be clickable with bound parameters");
    await waitForStreamCalls(paramCallsBefore + 1, "running a parameterized query should execute it");
    assert.deepEqual(
      lastStreamParams,
      [{ value: "42" }, { value: null }],
      "the Run request should carry positional params, with the toggled slot as null",
    );
    await setSQL("SELECT 1");
    await browser.waitFor("document.getElementById('studio-param-1') === null", "the parameters panel to disappear once no placeholder remains");

    // Graphical EXPLAIN: an EXPLAIN ANALYZE result renders as a tree by
    // default, showing estimated vs. actual rows and highlighting the
    // synthetic 100x-off SeqScan row; toggling to Table shows the same
    // rows as an ordinary grid, and nothing is ever labeled "actual"
    // without EXPLAIN ANALYZE data behind it.
    const explainCallsBefore = streamCalls;
    await setSQL("EXPLAIN ANALYZE SELECT * FROM t WHERE n > 5 ORDER BY n LIMIT 2");
    assert.equal(await clickButton("Run query"), true, "Run query should execute an EXPLAIN statement directly (not destructive)");
    await waitForStreamCalls(explainCallsBefore + 1, "the EXPLAIN statement should execute");
    await browser.waitFor("document.body.textContent.includes('SeqScan t')", "the EXPLAIN plan tree to render");
    assert.equal(await browser.evaluate("document.querySelector('.nss-explain-tree') !== null"), true, "the Plan view should be the default for an EXPLAIN result");
    assert.equal(await browser.evaluate("document.body.textContent.includes('actual 300 rows')"), true, "measured actual rows should render");
    assert.equal(await browser.evaluate("document.body.textContent.includes('100.0x est.')"), true, "a 100x estimation miss should be called out");
    assert.equal(await browser.evaluate("document.body.textContent.includes('Estimates only')"), false, "the EXPLAIN ANALYZE result must not claim to be estimates-only");
    await runAxe(browser, axe.source, "Studio EXPLAIN plan view");

    assert.equal(await clickButton("Profile"), true, "EXPLAIN ANALYZE should expose the bounded profiler breakdown");
    await browser.waitFor("document.querySelector('.nss-explain-profiler') !== null", "the query profiler breakdown to render");
    assert.equal(await browser.evaluate("document.body.textContent.includes('Root reported time145µs')"), true, "the profiler should retain the server's root timing");
    assert.equal(await browser.evaluate("document.body.textContent.includes('100.0× higher')"), true, "the profiler should expose the largest estimate miss");
    assert.equal(await browser.evaluate("document.body.textContent.includes('Peak reported memory64 bytes')"), true, "the profiler should expose a reported maximum without summing operators");
    assert.equal(await browser.evaluate("document.body.textContent.includes('never sums operators or invents percentages')"), true, "the inclusive-metric boundary should be explicit");
    assert.equal(await browser.evaluate("document.querySelector('[aria-label=\"EXPLAIN ANALYZE operator profile\"]') !== null"), true, "the per-operator profile table should be labeled");
    await runAxe(browser, axe.source, "Studio EXPLAIN profiler view");

    assert.equal(await clickButton("Table"), true, "the Table toggle should be available for an EXPLAIN result");
    await browser.waitFor("document.querySelector('.nss-explain-tree') === null", "the Plan view to hide when Table is selected");
    assert.equal(await browser.evaluate("document.body.textContent.includes('operator')"), true, "the Table view should show the raw EXPLAIN columns");

    assert.equal(await clickButton("Plan"), true, "the Plan toggle should restore the tree view");
    await browser.waitFor("document.querySelector('.nss-explain-tree') !== null", "the Plan view to reappear");

    // Plan comparison: pin one bounded parsed tree in this tab, execute a
    // second server-authored EXPLAIN ANALYZE shape, then align nodes only by
    // deterministic structural path. The comparison must distinguish
    // operator/topology changes from metric-only changes and keep every
    // measured value scoped to plans that were actually analyzed.
    assert.equal(await clickButton("Pin as baseline"), true, "the current plan should be pinnable as this tab's baseline");
    await browser.waitFor("document.body.textContent.includes('Pinned 5-operator plan')", "the bounded baseline confirmation");
    alternativeExplain = true;
    const comparisonCallsBefore = streamCalls;
    await setSQL("EXPLAIN ANALYZE SELECT * FROM t WHERE n > 5 ORDER BY n LIMIT 2 -- comparison");
    assert.equal(await clickButton("Run query"), true, "a second EXPLAIN ANALYZE should run for comparison");
    await waitForStreamCalls(comparisonCallsBefore + 1, "the comparison plan to execute");
    await browser.waitFor("document.body.textContent.includes('IndexScan t')", "the alternative plan tree to render");
    assert.equal(await clickButton("Compare"), true, "the pinned baseline should enable the Compare view");
    await browser.waitFor("document.querySelector('.nss-plan-comparison') !== null", "the plan comparison to render");
    assert.equal(await browser.evaluate("document.body.textContent.includes('5 → 4')"), true, "the operator counts should compare baseline to current");
    assert.equal(await browser.evaluate("document.body.textContent.includes('operator changed')"), true, "structural operator changes should be explicit");
    assert.equal(await browser.evaluate("document.body.textContent.includes('metrics changed')"), true, "metric-only changes should stay distinct from topology changes");
    assert.equal(await browser.evaluate("document.body.textContent.includes('TopNSort fetch=2 1 → IndexScan t')"), true, "structural-path-aligned operators should render side by side");
    assert.equal(await browser.evaluate("document.body.textContent.includes('idx_t_fast')"), true, "changed index use should render from the server plan");
    assert.equal(await browser.evaluate("document.body.textContent.includes('Both analyzed')"), true, "measured comparison must identify that both plans were ANALYZE results");
    await runAxe(browser, axe.source, "Studio plan comparison");
    alternativeExplain = false;

    // Multi-tab editing: each tab is an independent SQL buffer/result, but
    // the server allows only one active query at a time, so a second tab
    // must show "Busy…" and a cross-tab notice while the first is running,
    // never let it start its own query, and recover cleanly afterward.
    assert.equal(await browser.evaluate("document.querySelectorAll('.nss-tab').length"), 1, "only one tab should exist so far");
    assert.equal(await clickButton("+"), true, "the new-tab control should be available");
    await browser.waitFor("document.querySelectorAll('.nss-tab').length === 2", "a second tab to appear");
    assert.equal(await browser.evaluate(`document.querySelector('[aria-label="SQL editor"]')?.value`), "SELECT * FROM system.capabilities ORDER BY name", "a new tab should start from the default SQL, not the previous tab's buffer");
    assert.equal(await browser.evaluate(`[...document.querySelectorAll("button")].some((b) => b.textContent.trim() === "Compare")`), false, "a new tab must not inherit another tab's plan baseline");
    await runAxe(browser, axe.source, "Studio with a second query tab");

    await setSQL("SELECT 2 AS marker");
    assert.equal(await clickButton("Query 1"), true, "switching to the first tab should be possible");
    await browser.waitFor(`document.querySelector('[aria-label="SQL editor"]')?.value.includes("EXPLAIN ANALYZE")`, "the first tab to keep its own buffer");
    assert.equal(await browser.evaluate(`[...document.querySelectorAll("button")].some((b) => b.textContent.trim() === "Compare")`), true, "the pinned plan baseline should remain scoped to its original tab");
    assert.equal(await clickButton("Query 2"), true, "switching back to the second tab should be possible");
    await browser.waitFor(`document.querySelector('[aria-label="SQL editor"]')?.value === "SELECT 2 AS marker"`, "the second tab to keep its own buffer across a tab switch");

    await setSQL("SELECT 1 -- STUDIO_TEST_SLOW");
    const callsBeforeSlow = streamCalls;
    assert.equal(await clickButton("Run query"), true, "the slow query should be runnable from the second tab");
    await waitForStreamCalls(callsBeforeSlow + 1, "the slow query should reach the server");
    assert.equal(await clickButton("Query 1"), true, "switching tabs while a query runs elsewhere should still work");
    await browser.waitFor("document.body.textContent.includes('is running a query')", "the cross-tab running notice to appear on the other tab");
    assert.equal(await browser.evaluate(`[...document.querySelectorAll("button")].some((b) => b.textContent.trim() === "Busy…")`), true, "the other tab's Run action should show Busy, not offer to run");
    assert.equal(await browser.evaluate(`document.querySelector('[aria-label="Close Query 2"]')?.disabled`), true, "a running tab's close control should be disabled");
    await clickByAriaLabel("Close Query 2");
    assert.equal(await browser.evaluate("document.querySelectorAll('.nss-tab').length"), 2, "a running tab must not actually be closable");
    await runAxe(browser, axe.source, "Studio cross-tab busy notice");

    await browser.waitFor("document.body.textContent.includes('is running a query') === false", "the cross-tab notice to clear once the slow query finishes", 4_000);
    assert.equal(await clickButton("Query 2"), true, "switching back to the finished tab should work");
    await browser.waitFor("document.body.textContent.includes('250 rows')", "the second tab's own result to have arrived");

    assert.equal(await clickByAriaLabel("Close Query 2"), true, "an idle tab should be closable");
    await browser.waitFor("document.querySelectorAll('.nss-tab').length === 1", "closing the second tab to leave just the first");
    assert.equal(await browser.evaluate(`document.querySelector('[aria-label="SQL editor"]')?.value.includes("EXPLAIN ANALYZE")`), true, "closing a tab should fall back to a remaining tab, not clear the buffer");
    assert.equal(await browser.evaluate("document.querySelector('.nss-tab-close') === null"), true, "the sole remaining tab must not offer to close itself");
    assert.equal(await clickButton("Clear baseline"), true, "the tab's pinned plan should be clearable");
    assert.equal(await browser.evaluate(`[...document.querySelectorAll("button")].some((b) => b.textContent.trim() === "Compare")`), false, "clearing the baseline should remove Compare without changing the result");

    for (let i = 0; i < 10; i++) await clickButton("+");
    assert.equal(await browser.evaluate("document.querySelectorAll('.nss-tab').length"), 8, "tab creation must stop at the bounded maximum");
    assert.equal(await browser.evaluate(`document.querySelector('[aria-label="New query tab"]')?.disabled`), true, "the new-tab control should disable once the bound is reached");

    // Execute Script: splits the buffer with the server's tokenizer, warns
    // once for the whole script when any statement is destructive, then runs
    // every statement sequentially and renders each one's own result —
    // including a DDL/DML statement whose result carries null column
    // metadata (real-server shape), which must not crash the result view.
    await clickButton("Query 1");
    await setSQL("SELECT 1;\nDELETE FROM articles;\nSELECT 2;");
    const queryCallsBeforeScript = queryCalls;
    assert.equal(await clickButton("Run script"), true, "Run script should be clickable for a multi-statement buffer");
    await browser.waitFor("document.querySelector('[role=dialog] h2')?.textContent === 'Confirm script (3 statements)'", "the script confirmation to name the statement count");
    assert.equal(await browser.evaluate("document.querySelector('[role=dialog]')?.textContent.includes('1 of 3 statements are flagged')"), true, "the script confirmation should count flagged statements");
    assert.equal(await browser.evaluate("document.querySelector('[role=dialog]')?.textContent.includes('articles')"), true, "the script confirmation should name the destructive statement");
    await runAxe(browser, axe.source, "Studio script confirmation");

    assert.equal(await clickDialogButton("Run anyway"), true, "confirming the script should be possible");
    await browser.waitFor("document.querySelector('[role=dialog]') === null", "the script confirmation to close");
    await browser.waitFor("document.body.textContent.includes('3 of 3 statements ran')", "the script progress summary to complete", 4_000);
    assert.equal(queryCalls, queryCallsBeforeScript + 3, "the script should run exactly one server request per statement, sequentially");
    assert.equal(await browser.evaluate("document.querySelectorAll('.nss-script-item').length"), 3, "the script results list should have one row per statement");
    assert.equal(await browser.evaluate(`[...document.querySelectorAll(".nss-script-item")].every((item) => item.textContent.includes("success"))`), true, "every script statement should report success");
    await runAxe(browser, axe.source, "Studio script results");

    // A destructive realm-wide statement must retain both warnings in the
    // consolidated script dialog. Otherwise DROP USER/ROLE could hide its
    // cross-database reach behind the destructive reason.
    await setSQL("SELECT 1;\nDROP USER contractor;");
    const queryCallsBeforeRealmScript = queryCalls;
    assert.equal(await clickButton("Run script"), true, "Run script should be clickable for realm-wide DDL");
    await browser.waitFor("document.querySelector('[role=dialog] h2')?.textContent === 'Confirm script (2 statements)'", "the realm-wide script confirmation");
    assert.equal(await browser.evaluate("document.querySelector('[role=dialog]')?.textContent.includes('Permanently removes login contractor')"), true, "the script confirmation should retain the destructive reason");
    assert.equal(await browser.evaluate("document.querySelector('[role=dialog]')?.textContent.includes('every database in the default realm')"), true, "the script confirmation should retain the realm-wide warning");
    await runAxe(browser, axe.source, "Studio realm-wide script confirmation");
    assert.equal(await clickDialogButton("Cancel"), true, "the realm-wide script confirmation should be cancelable");
    await browser.waitFor("document.querySelector('[role=dialog]') === null", "the realm-wide script confirmation to close");
    assert.equal(queryCalls, queryCallsBeforeRealmScript, "canceling the realm-wide script must execute no statement");

    // Selecting the DDL/DML statement's row must render its null-columns
    // result without throwing — this is the exact shape that crashed before
    // the client normalized a null columns/rows array to empty.
    assert.equal(await browser.evaluate(`document.querySelectorAll(".nss-script-item")[1]?.click(); true`), true, "the destructive statement's row should be selectable");
    await browser.waitFor("document.body.textContent.includes('No query results yet') === false", "selecting a script row to update the result area");
    assert.equal(await browser.evaluate("document.querySelector('.nss-workspace') !== null"), true, "the Studio workspace should still be mounted after selecting a null-column result");

    // GRANT/REVOKE builder: opens as a modal, loads grantee suggestions from
    // the same admin-only system.users/system.roles read Operations mode's
    // Security view already uses, generates SQL client-side with no
    // execution of its own, and inserts the result into the active editor
    // tab on confirmation only.
    const setDialogInputByPlaceholder = (placeholder, value) => browser.evaluate(`(() => {
      const el = document.querySelector('[role=dialog] input[placeholder=${JSON.stringify(placeholder)}]');
      if (!el) return false;
      const setter = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, "value").set;
      setter.call(el, ${JSON.stringify(value)});
      el.dispatchEvent(new Event("input", { bubbles: true }));
      return true;
    })()`);

    // Full-text Explorer: derives the table, eligible fields, and candidate
    // index from the authorized Studio catalog; generates/copies native SQL
    // without executing on Insert; and runs through the ordinary bounded
    // stream when explicitly asked. The result adds a UI-only ordinal BM25
    // rank while preserving literal server HIGHLIGHT/SNIPPET markers.
    const fullTextSQL = `SELECT "id", SNIPPET("title") AS "title_snippet", SNIPPET("body") AS "body_snippet" FROM "articles" SEARCH "title", "body" FOR 'database performance' LIMIT 20`;
    const setFullTextQuery = (value) => browser.evaluate(`(() => {
      const el = document.getElementById('fulltext-query');
      if (!el) return false;
      const setter = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, "value").set;
      setter.call(el, ${JSON.stringify(value)});
      el.dispatchEvent(new Event("input", { bubbles: true }));
      return true;
    })()`);
    const callsBeforeFullTextInsert = streamCalls;
    assert.equal(await clickButton("Full-text…"), true, "the capability-gated Full-text Explorer control should be available");
    await browser.waitFor("document.querySelector('[role=dialog] h2')?.textContent === 'Full-text Explorer'", "the Full-text Explorer to open");
    await browser.waitFor("document.querySelector('[role=dialog]')?.textContent.includes('ix_articles_text')", "the authorized full-text index metadata");
    assert.equal(await browser.evaluate("document.getElementById('fulltext-table')?.textContent.includes('articles')"), true, "the explorer should select the visible table");
    assert.equal(await browser.evaluate("document.getElementById('fulltext-index')?.textContent.includes('ix_articles_text')"), true, "the explorer should select the valid catalog index");
    assert.equal(await browser.evaluate("document.getElementById('fulltext-columns')?.textContent.includes('title') && document.getElementById('fulltext-columns')?.textContent.includes('body')"), true, "the selected index should lock its ordered SEARCH fields");
    assert.equal(await browser.evaluate("document.querySelector('[role=dialog]')?.textContent.includes('Search phrase is required.')"), true, "an empty phrase must not produce malformed SQL");
    await runAxe(browser, axe.source, "Studio Full-text Explorer (empty)");

    assert.equal(await setFullTextQuery("database performance"), true, "the native search phrase should accept input");
    await browser.waitFor(`document.querySelector('[role=dialog]')?.textContent.includes(${JSON.stringify(fullTextSQL)})`, "the exact native full-text SQL preview");
    assert.equal(await browser.evaluate(`[...document.querySelectorAll('[role=dialog] button')].some((button) => button.textContent.trim().includes('Copy SQL'))`), true, "the generated SQL should be copyable");
    await runAxe(browser, axe.source, "Studio Full-text Explorer (ready)");
    assert.equal(await clickDialogButton("Insert into editor"), true, "the full-text query should be insertable without execution");
    await browser.waitFor("document.querySelector('[role=dialog]') === null", "the Full-text Explorer to close after inserting");
    assert.equal(await browser.evaluate(`document.querySelector('[aria-label="SQL editor"]')?.value`), fullTextSQL, "Insert should put the exact generated SEARCH statement in the active tab");
    assert.equal(streamCalls, callsBeforeFullTextInsert, "inserting a full-text query must not execute it");

    assert.equal(await clickButton("Full-text…"), true, "the Full-text Explorer should reopen from a clean form");
    await browser.waitFor("document.querySelector('[role=dialog]')?.textContent.includes('ix_articles_text')", "the full-text catalog to reload");
    assert.equal(await setFullTextQuery("database performance"), true, "the reopened search phrase should accept input");
    await browser.waitFor(`document.querySelector('[role=dialog]')?.textContent.includes(${JSON.stringify(fullTextSQL)})`, "the runnable full-text SQL preview");
    const callsBeforeFullTextRun = streamCalls;
    assert.equal(await clickDialogButton("Run search"), true, "Run search should explicitly execute the generated query");
    await browser.waitFor("document.querySelector('[role=dialog]') === null", "the Full-text Explorer to close after running");
    await waitForStreamCalls(callsBeforeFullTextRun + 1, "the full-text query should use the bounded Studio stream");
    assert.equal(lastStreamSQL, fullTextSQL, "the server should receive exactly the SQL shown in the explorer preview");
    await browser.waitFor("document.body.textContent.includes('BM25 ranked')", "the truthful BM25 ordinal-rank notice");
    assert.equal(await browser.evaluate("document.querySelector('section[aria-labelledby=studio-results-title] .nss-rank-column')?.textContent.includes('BM25 rank')"), true, "the full-text result should display an ordinal rank column");
    assert.equal(await browser.evaluate("document.querySelector('section[aria-labelledby=studio-results-title] tbody .nss-rank-column')?.textContent"), "#1", "the highest server-ranked row should display rank #1");
    assert.equal(await browser.evaluate("document.body.textContent.includes('<mark>Database</mark>')"), true, "server match markers should remain visible literal text");
    await runAxe(browser, axe.source, "Studio Full-text Explorer results");

    // Vector Explorer: derives the table, eligible VECTOR/BITVECTOR/
    // SPARSEVECTOR column, and candidate index from the authorized Studio
    // catalog; the "Vector inspector" panel live-validates the pasted
    // literal's dimension count against the column's declared N; generates/
    // copies native NEAREST SQL without executing on Insert; and runs
    // through the ordinary bounded stream when explicitly asked.
    const vectorSQL = `SELECT * FROM "articles" NEAREST "embedding" TO (1, 0, 0.5) USING COSINE LIMIT 10`;
    const setVectorInput = (value) => browser.evaluate(`(() => {
      const el = document.getElementById('vector-input');
      if (!el) return false;
      const setter = Object.getOwnPropertyDescriptor(window.HTMLTextAreaElement.prototype, "value").set;
      setter.call(el, ${JSON.stringify(value)});
      el.dispatchEvent(new Event("input", { bubbles: true }));
      return true;
    })()`);
    const callsBeforeVectorInsert = streamCalls;
    assert.equal(await clickButton("Vector…"), true, "the capability-gated Vector Explorer control should be available");
    await browser.waitFor("document.querySelector('[role=dialog] h2')?.textContent === 'Vector Explorer'", "the Vector Explorer to open");
    await browser.waitFor("document.querySelector('[role=dialog]')?.textContent.includes('ix_embedding_hnsw')", "the authorized vector index metadata");
    assert.equal(await browser.evaluate("document.getElementById('vector-table')?.textContent.includes('articles')"), true, "the explorer should select the visible table");
    assert.equal(await browser.evaluate("document.getElementById('vector-column')?.textContent.includes('embedding')"), true, "the explorer should select the only visible vector column");
    assert.equal(await browser.evaluate("document.getElementById('vector-metric')?.textContent.includes('COSINE')"), true, "COSINE is the default metric for a dense VECTOR column");
    assert.equal(await browser.evaluate("document.querySelector('[role=dialog]')?.textContent.includes('Enter a valid vector to search for.')"), true, "an empty vector must not produce malformed SQL");
    await runAxe(browser, axe.source, "Studio Vector Explorer (empty)");

    assert.equal(await setVectorInput("1, 0, 0.5"), true, "the vector textarea should accept comma-separated input");
    await browser.waitFor("document.querySelector('[role=dialog]')?.textContent.includes('Vector inspector')", "the live vector inspector to render");
    assert.equal(await browser.evaluate("document.querySelector('[role=dialog]')?.textContent.includes('3 values of 3 declared')"), true, "the inspector should confirm the vector matches the column's declared dimensions");
    await browser.waitFor(`document.querySelector('[role=dialog]')?.textContent.includes(${JSON.stringify(vectorSQL)})`, "the exact native NEAREST SQL preview");
    assert.equal(await browser.evaluate(`[...document.querySelectorAll('[role=dialog] button')].some((button) => button.textContent.trim().includes('Copy SQL'))`), true, "the generated SQL should be copyable");
    await runAxe(browser, axe.source, "Studio Vector Explorer (ready)");
    assert.equal(await clickDialogButton("Insert into editor"), true, "the vector query should be insertable without execution");
    await browser.waitFor("document.querySelector('[role=dialog]') === null", "the Vector Explorer to close after inserting");
    assert.equal(await browser.evaluate(`document.querySelector('[aria-label="SQL editor"]')?.value`), vectorSQL, "Insert should put the exact generated NEAREST statement in the active tab");
    assert.equal(streamCalls, callsBeforeVectorInsert, "inserting a vector query must not execute it");

    assert.equal(await clickButton("Vector…"), true, "the Vector Explorer should reopen from a clean form");
    await browser.waitFor("document.querySelector('[role=dialog]')?.textContent.includes('ix_embedding_hnsw')", "the vector catalog to reload");
    assert.equal(await setVectorInput("1, 0, 0.5"), true, "the reopened vector textarea should accept input");
    await browser.waitFor(`document.querySelector('[role=dialog]')?.textContent.includes(${JSON.stringify(vectorSQL)})`, "the runnable vector SQL preview");
    const callsBeforeVectorRun = streamCalls;
    assert.equal(await clickDialogButton("Run search"), true, "Run search should explicitly execute the generated query");
    await browser.waitFor("document.querySelector('[role=dialog]') === null", "the Vector Explorer to close after running");
    await waitForStreamCalls(callsBeforeVectorRun + 1, "the vector query should use the bounded Studio stream");
    assert.equal(lastStreamSQL, vectorSQL, "the server should receive exactly the SQL shown in the explorer preview");
    await runAxe(browser, axe.source, "Studio Vector Explorer results");

    // Hybrid Explorer: composes an optional structured filter with the same
    // Full-text and Vector catalog derivation as the two standalone
    // explorers into one native `[WHERE ...] SEARCH ... NEAREST ...` plan.
    // "Explain" runs EXPLAIN ANALYZE of the exact generated SQL through the
    // ordinary bounded stream, reusing the existing graphical EXPLAIN tree
    // with no new rendering code; "Run search" labels its result "Hybrid
    // rank" (not "BM25 rank") since the fused rank is not pure BM25.
    const hybridSQL = `SELECT * FROM "articles" SEARCH "title", "body" FOR 'database performance' NEAREST "embedding" TO (1, 0, 0.5) USING COSINE LIMIT 10`;
    const setHybridQuery = (value) => browser.evaluate(`(() => {
      const el = document.getElementById('hybrid-query');
      if (!el) return false;
      const setter = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, "value").set;
      setter.call(el, ${JSON.stringify(value)});
      el.dispatchEvent(new Event("input", { bubbles: true }));
      return true;
    })()`);
    const setHybridVector = (value) => browser.evaluate(`(() => {
      const el = document.getElementById('hybrid-vector-input');
      if (!el) return false;
      const setter = Object.getOwnPropertyDescriptor(window.HTMLTextAreaElement.prototype, "value").set;
      setter.call(el, ${JSON.stringify(value)});
      el.dispatchEvent(new Event("input", { bubbles: true }));
      return true;
    })()`);
    const fillHybridForm = async () => {
      assert.equal(await setHybridQuery("database performance"), true, "the search-phrase field should accept input");
      assert.equal(await setHybridVector("1, 0, 0.5"), true, "the vector textarea should accept input");
      await browser.waitFor(`document.querySelector('[role=dialog]')?.textContent.includes(${JSON.stringify(hybridSQL)})`, "the exact native hybrid SQL preview");
    };

    const callsBeforeHybridInsert = streamCalls;
    assert.equal(await clickButton("Hybrid…"), true, "the capability-gated Hybrid Explorer control should be available (fulltext and vector both supported)");
    await browser.waitFor("document.querySelector('[role=dialog] h2')?.textContent === 'Hybrid Explorer'", "the Hybrid Explorer to open");
    await browser.waitFor("document.querySelector('[role=dialog]')?.textContent.includes('ix_articles_text')", "the authorized full-text index metadata");
    assert.equal(await browser.evaluate("document.getElementById('hybrid-table')?.textContent.includes('articles')"), true, "the explorer should select the visible table");
    assert.equal(await browser.evaluate("document.getElementById('hybrid-fulltext-index')?.textContent.includes('ix_articles_text')"), true, "the explorer should select the valid catalog full-text index");
    assert.equal(await browser.evaluate("document.getElementById('hybrid-vector-column')?.textContent.includes('embedding')"), true, "the explorer should select the only visible vector column");
    assert.equal(await browser.evaluate("document.querySelector('[role=dialog]')?.textContent.includes('Search phrase is required.')"), true, "an incomplete form must not produce malformed SQL");
    await runAxe(browser, axe.source, "Studio Hybrid Explorer (empty)");

    await fillHybridForm();
    assert.equal(await browser.evaluate(`[...document.querySelectorAll('[role=dialog] button')].some((button) => button.textContent.trim().includes('Copy SQL'))`), true, "the generated SQL should be copyable");
    await runAxe(browser, axe.source, "Studio Hybrid Explorer (ready)");
    assert.equal(await clickDialogButton("Insert into editor"), true, "the hybrid query should be insertable without execution");
    await browser.waitFor("document.querySelector('[role=dialog]') === null", "the Hybrid Explorer to close after inserting");
    assert.equal(await browser.evaluate(`document.querySelector('[aria-label="SQL editor"]')?.value`), hybridSQL, "Insert should put the exact generated hybrid statement in the active tab");
    assert.equal(streamCalls, callsBeforeHybridInsert, "inserting a hybrid query must not execute it");

    assert.equal(await clickButton("Hybrid…"), true, "the Hybrid Explorer should reopen from a clean form");
    await browser.waitFor("document.querySelector('[role=dialog]')?.textContent.includes('ix_articles_text')", "the hybrid catalog to reload");
    await fillHybridForm();
    const callsBeforeHybridExplain = streamCalls;
    assert.equal(await clickDialogButton("Explain"), true, "Explain should run EXPLAIN ANALYZE of the exact generated SQL");
    await browser.waitFor("document.querySelector('[role=dialog]') === null", "the Hybrid Explorer to close after explaining");
    await waitForStreamCalls(callsBeforeHybridExplain + 1, "the explain request should use the bounded Studio stream");
    assert.equal(lastStreamSQL, `EXPLAIN ANALYZE ${hybridSQL}`, "Explain should send EXPLAIN ANALYZE of exactly the previewed SQL");
    await browser.waitFor("document.querySelector('[aria-label=\"EXPLAIN view\"]') !== null", "the existing graphical EXPLAIN tree should render with no new rendering code");
    await runAxe(browser, axe.source, "Studio Hybrid Explorer plan");

    assert.equal(await clickButton("Hybrid…"), true, "the Hybrid Explorer should reopen a second time");
    await browser.waitFor("document.querySelector('[role=dialog]')?.textContent.includes('ix_articles_text')", "the hybrid catalog to reload again");
    await fillHybridForm();
    const callsBeforeHybridRun = streamCalls;
    assert.equal(await clickDialogButton("Run search"), true, "Run search should explicitly execute the generated query");
    await browser.waitFor("document.querySelector('[role=dialog]') === null", "the Hybrid Explorer to close after running");
    await waitForStreamCalls(callsBeforeHybridRun + 1, "the hybrid query should use the bounded Studio stream");
    assert.equal(lastStreamSQL, hybridSQL, "the server should receive exactly the SQL shown in the explorer preview, with no EXPLAIN prefix");
    await browser.waitFor("document.body.textContent.includes('Hybrid ranked')", "the truthful hybrid-fusion rank notice, not a BM25-only label");
    assert.equal(await browser.evaluate("document.querySelector('section[aria-labelledby=studio-results-title] .nss-rank-column')?.textContent.includes('Hybrid rank')"), true, "the hybrid result should label its rank column Hybrid rank, not BM25 rank");
    await runAxe(browser, axe.source, "Studio Hybrid Explorer results");

    // Geo Explorer: derives the table's only visible POINT column and its
    // candidate spatial index from the authorized catalog; the click-to-draw
    // world canvas is aria-hidden decoration only, so every coordinate is set
    // here through the numeric fields, the same path a keyboard/screen-reader
    // user has. Point+radius generates DWITHIN; polygon generates WITHIN over
    // an auto-closed ring; both reuse the same CellInspector GeoView renderer
    // for a zoomed confirmation preview once the shape is valid.
    const geoPointSQL = `SELECT * FROM "articles" WHERE DWITHIN("loc", POINT(-73.9857, 40.7484), 1000) LIMIT 10`;
    const setDialogNumberInput = (id, value) => browser.evaluate(`(() => {
      const el = document.getElementById(${JSON.stringify(id)});
      if (!el) return false;
      const setter = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, "value").set;
      setter.call(el, ${JSON.stringify(String(value))});
      el.dispatchEvent(new Event("input", { bubbles: true }));
      return true;
    })()`);

    const callsBeforeGeoInsert = streamCalls;
    assert.equal(await clickButton("Geo…"), true, "the capability-gated Geo Explorer control should be available");
    await browser.waitFor("document.querySelector('[role=dialog] h2')?.textContent === 'Geo Explorer'", "the Geo Explorer to open");
    await browser.waitFor("document.querySelector('[role=dialog]')?.textContent.includes('ix_loc_spatial')", "the authorized spatial index metadata");
    assert.equal(await browser.evaluate("document.getElementById('geo-table')?.textContent.includes('articles')"), true, "the explorer should select the visible table");
    assert.equal(await browser.evaluate("document.getElementById('geo-column')?.textContent.includes('loc')"), true, "the explorer should select the only visible POINT column");
    assert.equal(await browser.evaluate("document.querySelector('[role=dialog]')?.textContent.includes('Click the map or enter longitude/latitude to place a point.')"), true, "an empty point must not produce malformed SQL");
    await runAxe(browser, axe.source, "Studio Geo Explorer (empty)");

    assert.equal(await setDialogNumberInput("geo-lon", -73.9857), true, "the longitude field should accept input");
    assert.equal(await setDialogNumberInput("geo-lat", 40.7484), true, "the latitude field should accept input");
    await browser.waitFor(`document.querySelector('[role=dialog]')?.textContent.includes(${JSON.stringify(geoPointSQL)})`, "the exact native DWITHIN SQL preview");
    assert.equal(await browser.evaluate("document.querySelector('[role=dialog] svg[aria-label=\"POINT coordinate preview\"]') !== null"), true, "the reused GeoView renderer should confirm the drawn point");
    assert.equal(await browser.evaluate(`[...document.querySelectorAll('[role=dialog] button')].some((button) => button.textContent.trim().includes('Copy SQL'))`), true, "the generated SQL should be copyable");
    await runAxe(browser, axe.source, "Studio Geo Explorer point+radius (ready)");
    assert.equal(await clickDialogButton("Insert into editor"), true, "the geo query should be insertable without execution");
    await browser.waitFor("document.querySelector('[role=dialog]') === null", "the Geo Explorer to close after inserting");
    assert.equal(await browser.evaluate(`document.querySelector('[aria-label="SQL editor"]')?.value`), geoPointSQL, "Insert should put the exact generated DWITHIN statement in the active tab");
    assert.equal(streamCalls, callsBeforeGeoInsert, "inserting a geo query must not execute it");

    assert.equal(await clickButton("Geo…"), true, "the Geo Explorer should reopen from a clean form");
    await browser.waitFor("document.querySelector('[role=dialog]')?.textContent.includes('ix_loc_spatial')", "the geo catalog to reload");
    assert.equal(await setDialogNumberInput("geo-lon", -73.9857), true, "the reopened longitude field should accept input");
    assert.equal(await setDialogNumberInput("geo-lat", 40.7484), true, "the reopened latitude field should accept input");
    await browser.waitFor(`document.querySelector('[role=dialog]')?.textContent.includes(${JSON.stringify(geoPointSQL)})`, "the runnable DWITHIN SQL preview");
    const callsBeforeGeoRun = streamCalls;
    assert.equal(await clickDialogButton("Run search"), true, "Run search should explicitly execute the generated query");
    await browser.waitFor("document.querySelector('[role=dialog]') === null", "the Geo Explorer to close after running");
    await waitForStreamCalls(callsBeforeGeoRun + 1, "the geo query should use the bounded Studio stream");
    assert.equal(lastStreamSQL, geoPointSQL, "the server should receive exactly the SQL shown in the explorer preview");

    const geoPolygonSQL = `SELECT * FROM "articles" WHERE WITHIN("loc", POLYGON('((-74.1 40.6, -73.8 40.6, -73.8 40.9, -74.1 40.6))')) LIMIT 10`;
    assert.equal(await clickButton("Geo…"), true, "the Geo Explorer should reopen for the polygon mode");
    await browser.waitFor("document.querySelector('[role=dialog]')?.textContent.includes('ix_loc_spatial')", "the geo catalog to reload a third time");
    await browser.evaluate("document.getElementById('geo-mode')?.click()");
    await browser.waitFor("document.getElementById('geo-mode')?.getAttribute('aria-expanded') === 'true'", "the query-shape menu to open");
    await browser.evaluate("document.getElementById('geo-mode-list-opt-1')?.click()");
    await browser.waitFor("document.getElementById('geo-mode')?.getAttribute('aria-expanded') === 'false'", "the query-shape menu to close after choosing Polygon");
    await browser.waitFor("document.querySelector('[role=dialog]')?.textContent.includes('A polygon needs at least three vertices')", "the polygon-mode empty state");
    for (const [lon, lat] of [[-74.1, 40.6], [-73.8, 40.6], [-73.8, 40.9]]) {
      assert.equal(await setDialogNumberInput("geo-vertex-lon", lon), true, "the vertex longitude field should accept input");
      assert.equal(await setDialogNumberInput("geo-vertex-lat", lat), true, "the vertex latitude field should accept input");
      assert.equal(await clickDialogButton("Add vertex"), true, "Add vertex should accept a valid coordinate");
    }
    await browser.waitFor(`document.querySelector('[role=dialog]')?.textContent.includes(${JSON.stringify(geoPolygonSQL)})`, "the exact native WITHIN SQL preview over the auto-closed ring");
    assert.equal(await browser.evaluate("document.querySelector('[role=dialog] svg[aria-label=\"POLYGON coordinate preview\"]') !== null"), true, "the reused GeoView renderer should confirm the drawn polygon");
    await runAxe(browser, axe.source, "Studio Geo Explorer polygon (ready)");
    assert.equal(await clickDialogButton("Insert into editor"), true, "the polygon query should be insertable without execution");
    await browser.waitFor("document.querySelector('[role=dialog]') === null", "the Geo Explorer to close after inserting the polygon query");
    assert.equal(await browser.evaluate(`document.querySelector('[aria-label="SQL editor"]')?.value`), geoPolygonSQL, "Insert should put the exact generated WITHIN statement in the active tab");

    assert.equal(await clickButton("Grant / Revoke…"), true, "the Grant/Revoke builder control should be available");
    await browser.waitFor("document.querySelector('[role=dialog]') !== null", "the GRANT/REVOKE builder to open");
    assert.equal(await browser.evaluate(`document.querySelector('[role=dialog]')?.textContent.includes("Grantee is required.")`), true, "an empty builder should show its validation error, not a malformed statement");
    assert.equal(await browser.evaluate(`[...document.querySelectorAll('[role=dialog] button')].find((b) => b.textContent.trim() === "Insert into editor")?.disabled`), true, "Insert should be disabled until the statement is valid");
    await runAxe(browser, axe.source, "Studio GRANT/REVOKE builder (empty)");

    await browser.waitFor(`document.querySelectorAll('#grant-builder-grantees option').length > 0`, "grantee suggestions to load from system.users/system.roles");
    assert.equal(await browser.evaluate(`[...document.querySelectorAll('#grant-builder-grantees option')].map((o) => o.value).includes("app")`), true, "a real system.users row should suggest as a grantee");
    assert.equal(await browser.evaluate(`[...document.querySelectorAll('#grant-builder-grantees option')].map((o) => o.value).includes("analyst")`), true, "a real system.roles row should also suggest as a grantee");

    assert.equal(await browser.evaluate(`document.querySelector('[role=dialog] input[type=checkbox]')?.click(); true`), true, "the ALL PRIVILEGES checkbox should be clickable");
    assert.equal(await setDialogInputByPlaceholder("orders", "articles"), true, "the table-name field should accept input");
    assert.equal(await setDialogInputByPlaceholder("app", "app"), true, "the grantee field should accept input");
    await browser.waitFor(
      `document.querySelector('[role=dialog]')?.textContent.includes('GRANT ALL PRIVILEGES ON TABLE "articles" TO "app"')`,
      "the live SQL preview to reflect the form",
    );
    assert.equal(await browser.evaluate(`[...document.querySelectorAll('[role=dialog] button')].find((b) => b.textContent.trim() === "Insert into editor")?.disabled`), false, "Insert should enable once the statement is valid");
    await runAxe(browser, axe.source, "Studio GRANT/REVOKE builder (filled)");

    assert.equal(await clickDialogButton("Insert into editor"), true, "Insert into editor should be clickable once valid");
    await browser.waitFor("document.querySelector('[role=dialog]') === null", "the builder to close after inserting");
    assert.equal(
      await browser.evaluate(`document.querySelector('[aria-label="SQL editor"]')?.value`),
      'GRANT ALL PRIVILEGES ON TABLE "articles" TO "app"',
      "confirming the builder should replace the active tab's SQL buffer, not execute anything itself",
    );

    assert.equal(await clickButton("Grant / Revoke…"), true, "the builder should be reopenable");
    await browser.waitFor("document.querySelector('[role=dialog]') !== null", "the builder to reopen");
    assert.equal(await browser.evaluate(`document.querySelector('[role=dialog] input[placeholder="orders"]')?.value`), "", "reopening the builder should start from a clean form, not the previous statement");
    assert.equal(await clickDialogButton("Cancel"), true, "Cancel should be available");
    await browser.waitFor("document.querySelector('[role=dialog]') === null", "the builder to close on cancel");
    assert.equal(
      await browser.evaluate(`document.querySelector('[aria-label="SQL editor"]')?.value`),
      'GRANT ALL PRIVILEGES ON TABLE "articles" TO "app"',
      "canceling the builder must not touch the editor buffer",
    );

    // Users & roles explorer: a read view over the same admin-only
    // system.users/system.roles/system.grants catalog Operations mode's
    // Security view already exposes, reused as-is (no new server route).
    // Each grant row's "Revoke" action must hand its exact grantee/
    // privilege/scope/object back into the existing GRANT/REVOKE builder,
    // prefilled, rather than a fresh blank form.
    assert.equal(await clickButton("Users & roles…"), true, "the Users & roles explorer control should be available");
    await browser.waitFor("document.querySelector('[role=dialog] h2')?.textContent === 'Users & roles'", "the Users & roles explorer to open");
    assert.equal(await browser.evaluate("document.querySelector('[role=dialog]')?.textContent.includes('app')"), true, "a real system.users row should render");
    assert.equal(await browser.evaluate("document.querySelector('[role=dialog]')?.textContent.includes('analyst')"), true, "a real system.roles row should render");
    assert.equal(await browser.evaluate("document.querySelector('[role=dialog]')?.textContent.includes('articles')"), true, "a real system.grants row should render");
    await runAxe(browser, axe.source, "Studio Users & roles explorer");

    assert.equal(await clickDialogButton("Revoke…"), true, "a grant row's Revoke action should be clickable");
    await browser.waitFor(
      `document.querySelector('[role=dialog]')?.textContent.includes('REVOKE SELECT ON TABLE "articles" FROM "app"')`,
      "the GRANT/REVOKE builder to open prefilled with the exact clicked grant row, not a blank form",
    );
    await runAxe(browser, axe.source, "Studio GRANT/REVOKE builder (prefilled from a grant row)");
    assert.equal(await clickDialogButton("Insert into editor"), true, "the prefilled revoke statement should be insertable");
    await browser.waitFor("document.querySelector('[role=dialog]') === null", "the builder to close after inserting the prefilled statement");
    assert.equal(
      await browser.evaluate(`document.querySelector('[aria-label="SQL editor"]')?.value`),
      'REVOKE SELECT ON TABLE "articles" FROM "app"',
      "inserting the prefilled revoke should replace the active tab's SQL buffer with exactly that statement",
    );

    assert.equal(await clickButton("Users & roles…"), true, "the Users & roles explorer should reopen");
    await browser.waitFor("document.querySelector('[role=dialog] h2')?.textContent === 'Users & roles'", "the explorer to reopen");
    assert.equal(await clickDialogButton("Grant / Revoke…"), true, "the explorer footer's Grant/Revoke button should open a fresh, unprefilled builder");
    await browser.waitFor(`document.querySelector('[role=dialog]')?.textContent.includes('Grantee is required.')`, "a fresh builder opened from the explorer footer must start blank, not reuse the last revoke prefill");
    assert.equal(await clickDialogButton("Cancel"), true, "Cancel should close the fresh builder");
    await browser.waitFor("document.querySelector('[role=dialog]') === null", "the builder to close on cancel");

    // Transaction console / Lock explorer: live, read-only snapshots over
    // the same system.sessions/system.active_queries/system.transactions/
    // system.locks bundle Operations mode already exposes. Opening and
    // Refresh must each fetch a new snapshot; no cross-session kill action
    // exists in the server, so this view must not invent one.
    const activityBeforeOpen = activityCalls;
    assert.equal(await clickButton("Transactions & locks…"), true, "the Transactions & locks explorer control should be available");
    await browser.waitFor("document.querySelector('[role=dialog] h2')?.textContent === 'Transactions & locks'", "the Transactions & locks explorer to open");
    await waitForActivityCalls(activityBeforeOpen + 1, "opening the explorer should fetch a live activity snapshot");
    await browser.waitFor("document.querySelector('[role=dialog]')?.textContent.includes('9001:1')", "the live activity snapshot to render");
    assert.equal(await browser.evaluate("document.querySelector('[role=dialog]')?.textContent.includes('9001')"), true, "the real transaction row should render");
    assert.equal(await browser.evaluate("document.querySelector('[role=dialog]')?.textContent.includes('9001:1')"), true, "the real lock row should render");
    assert.equal(await browser.evaluate("document.querySelector('[role=dialog]')?.textContent.includes('articles')"), true, "the lock's table name should render");
    assert.equal(await browser.evaluate("document.querySelector('[role=dialog]')?.textContent.includes('query-7')"), true, "the active query row should render");
    assert.equal(await browser.evaluate(`[...document.querySelectorAll('[role=dialog] button')].some((b) => /kill|terminate|rollback/i.test(b.textContent))`), false, "the read-only explorer must not invent a cross-session kill/rollback action");
    await runAxe(browser, axe.source, "Studio Transactions & locks explorer");

    const activityBeforeRefresh = activityCalls;
    assert.equal(await clickDialogButton("Refresh"), true, "Refresh should be available for live activity state");
    await waitForActivityCalls(activityBeforeRefresh + 1, "Refresh should fetch a new activity snapshot");
    assert.equal(await clickDialogButton("Close"), true, "Close should dismiss the activity explorer");
    await browser.waitFor("document.querySelector('[role=dialog]') === null", "the activity explorer to close");

    const activityBeforeReopen = activityCalls;
    assert.equal(await clickButton("Transactions & locks…"), true, "the activity explorer should reopen");
    await browser.waitFor("document.querySelector('[role=dialog] h2')?.textContent === 'Transactions & locks'", "the activity explorer to reopen");
    await waitForActivityCalls(activityBeforeReopen + 1, "reopening should not reuse a stale activity snapshot without refetching");
    assert.equal(await clickDialogButton("Close"), true, "Close should dismiss the reopened activity explorer");
    await browser.waitFor("document.querySelector('[role=dialog]') === null", "the reopened activity explorer to close");

    // Audit viewer: a live, read-only projection of the same bounded,
    // chain-verified audit tail Operations Security already reads. It must
    // refresh rather than reuse the Users & roles cache and must expose no
    // mutation control for the append-only server audit log.
    const securityBeforeAuditOpen = securityCalls;
    assert.equal(await clickButton("Audit…"), true, "the Audit viewer control should be available");
    await browser.waitFor("document.querySelector('[role=dialog] h2')?.textContent === 'Audit viewer'", "the Audit viewer to open");
    await waitForSecurityCalls(securityBeforeAuditOpen + 1, "opening Audit should re-read the live verified tail");
    await browser.waitFor("document.querySelector('[role=dialog]')?.textContent.includes('Chain verified')", "the audit-chain verification state to render");
    assert.equal(await browser.evaluate("document.querySelector('[role=dialog]')?.textContent.includes('operator')"), true, "the audit actor should render");
    assert.equal(await browser.evaluate("document.querySelector('[role=dialog]')?.textContent.includes('articles')"), true, "the audited object should render");
    assert.equal(await browser.evaluate("document.querySelector('[role=dialog]')?.textContent.includes('token')"), true, "the audit identity source should render");
    assert.equal(await browser.evaluate(`[...document.querySelectorAll('[role=dialog] button')].some((b) => /delete|clear|repair/i.test(b.textContent))`), false, "the read-only Audit viewer must not invent a log mutation action");
    await runAxe(browser, axe.source, "Studio Audit viewer");

    const securityBeforeAuditRefresh = securityCalls;
    auditTampered = true;
    assert.equal(await clickDialogButton("Refresh"), true, "Audit Refresh should be available");
    await waitForSecurityCalls(securityBeforeAuditRefresh + 1, "Audit Refresh should re-read the verified tail");
    await browser.waitFor("document.querySelector('[role=dialog]')?.textContent.includes('Chain verification FAILED')", "a newly detected audit-chain failure to render");
    assert.equal(await browser.evaluate("document.querySelector('[role=dialog]')?.textContent.includes('Line 7: hash chain mismatch')"), true, "the first bad line and verification problem should render");
    assert.equal(await browser.evaluate("document.querySelector('[role=dialog]')?.textContent.includes('articles')"), true, "the suspect audit record must remain visible after verification fails");
    await runAxe(browser, axe.source, "Studio Audit viewer chain failure");
    assert.equal(await clickDialogButton("Close"), true, "Close should dismiss the Audit viewer");
    await browser.waitFor("document.querySelector('[role=dialog]') === null", "the Audit viewer to close");

    const securityBeforeAuditReopen = securityCalls;
    assert.equal(await clickButton("Audit…"), true, "the Audit viewer should reopen");
    await browser.waitFor("document.querySelector('[role=dialog] h2')?.textContent === 'Audit viewer'", "the Audit viewer to reopen");
    await waitForSecurityCalls(securityBeforeAuditReopen + 1, "reopening Audit should not reuse a stale verified tail");
    assert.equal(await clickDialogButton("Close"), true, "Close should dismiss the reopened Audit viewer");
    await browser.waitFor("document.querySelector('[role=dialog]') === null", "the reopened Audit viewer to close");

    // Workflows, trigger/schedule relationships, tasks & change streams: a
    // read-only projection of the authorized system catalog. Live task/
    // subscription state means a refetch on every open; definitions stay
    // inspect-only and expose no cancel/retry/pause mutation control.
    const workflowsBeforeOpen = studioWorkflowsCalls;
    assert.equal(await clickButton("Workflows & CDC…"), true, "the Workflows & CDC explorer control should be available");
    await browser.waitFor("document.querySelector('[role=dialog] h2')?.textContent?.includes('Workflows') === true", "the Workflows & CDC explorer to open");
    await waitForWorkflowsCalls(workflowsBeforeOpen + 1, "opening Workflows & CDC should read the live catalog");
    assert.equal(await browser.evaluate("document.querySelector('[role=dialog]')?.textContent.includes('rollup_daily')"), true, "a visible workflow should render");
    await browser.waitFor("document.querySelector('[role=dialog] svg[role=img]') !== null", "the trigger/schedule workflow diagram to render");
    assert.match(
      await browser.evaluate("document.querySelector('[role=dialog] svg[role=img]')?.getAttribute('aria-label') ?? ''"),
      /2 workflows, 1 trigger, 1 schedule/,
      "the workflow SVG should have a concise semantic label",
    );
    assert.equal(await browser.evaluate("document.querySelector('[role=dialog]')?.textContent.includes('articles → articles_after_insert')"), true, "the trigger path should have an always-visible text alternative");
    assert.equal(await browser.evaluate("document.querySelector('[role=dialog]')?.textContent.includes('nightly (EVERY 24h0m0s, enabled) → rollup_daily')"), true, "the schedule path and state should have an always-visible text alternative");
    assert.equal(await browser.evaluate("document.querySelector('[role=dialog]')?.textContent.includes('s/00000007/1787616000000000000')"), true, "a scheduled task id should render");
    assert.equal(await browser.evaluate("document.querySelector('[role=dialog]')?.textContent.includes('914203')"), true, "an open change-stream subscription and its resume lsn should render");
    assert.equal(await browser.evaluate(`[...document.querySelectorAll('[role=dialog] button')].some((b) => /cancel|retry|kill|delete|pause|resume/i.test(b.textContent))`), false, "the read-only Workflows & CDC explorer must not invent a task/stream mutation action");
    // Filter the task table down to one workflow, entirely client-side.
    await browser.evaluate("document.getElementById('workflow-task-filter')?.click()");
    await browser.waitFor("document.getElementById('workflow-task-filter')?.getAttribute('aria-expanded') === 'true'", "the workflow filter menu to open");
    await browser.evaluate("document.getElementById('workflow-task-filter-list-opt-2')?.click()");
    await browser.waitFor("document.querySelector('[role=dialog]')?.textContent.includes('s/00000011/1787788800000000000') && !document.querySelector('[role=dialog]')?.textContent.includes('s/00000007/1787616000000000000')", "the task table to filter to the chosen workflow");
    await runAxe(browser, axe.source, "Studio Workflows & CDC explorer");
    const workflowsBeforeRefresh = studioWorkflowsCalls;
    assert.equal(await clickDialogButton("Refresh"), true, "Workflows & CDC Refresh should be available");
    await waitForWorkflowsCalls(workflowsBeforeRefresh + 1, "Refresh should re-read the live catalog");
    assert.equal(await clickDialogButton("Close"), true, "Close should dismiss the Workflows & CDC explorer");
    await browser.waitFor("document.querySelector('[role=dialog]') === null", "the Workflows & CDC explorer to close");

    // Schema migration history: a read-only view of nsql_schema_migrations.
    // Live server state (the CLI can apply a migration any time) means a
    // refetch on every open and Refresh; no apply/down/repair mutation control.
    const migrationsBeforeOpen = studioMigrationsCalls;
    assert.equal(await clickButton("Migrations…"), true, "the Migrations explorer control should be available");
    await browser.waitFor("document.querySelector('[role=dialog] h2')?.textContent?.includes('Schema migration history') === true", "the Migrations explorer to open");
    await waitForMigrationsCalls(migrationsBeforeOpen + 1, "opening Migrations should read the live history");
    await browser.waitFor("document.querySelector('[role=dialog]')?.textContent.includes('add_articles') === true", "an applied migration row should render");
    assert.equal(await browser.evaluate("document.querySelector('[role=dialog]')?.textContent.includes('Dirty migration state')"), true, "the dirty-state alert should surface for a dirty row");
    assert.equal(await browser.evaluate(`[...document.querySelectorAll('[role=dialog] button')].some((b) => /apply|migrate up|migrate down|repair|force/i.test(b.textContent))`), false, "the read-only Migrations explorer must not invent an apply/repair action");
    await runAxe(browser, axe.source, "Studio schema migration history explorer");
    const migrationsBeforeRefresh = studioMigrationsCalls;
    assert.equal(await clickDialogButton("Refresh"), true, "Migrations Refresh should be available");
    await waitForMigrationsCalls(migrationsBeforeRefresh + 1, "Refresh should re-read the live history");
    assert.equal(await clickDialogButton("Close"), true, "Close should dismiss the Migrations explorer");
    await browser.waitFor("document.querySelector('[role=dialog]') === null", "the Migrations explorer to close");

    // Schema relationships: a read-only ER view over the whole
    // system.foreign_keys catalog. The SVG carries a text label and the
    // grouped relationship list is its always-visible text alternative.
    assert.equal(await clickButton("Schema diagram…"), true, "the Schema diagram control should be available");
    await browser.waitFor("document.querySelector('[role=dialog] h2')?.textContent?.includes('Schema relationships') === true", "the Schema relationships explorer to open");
    await browser.waitFor("document.querySelector('[role=dialog] svg[role=img]') !== null", "the foreign-key diagram to render as a labelled image");
    assert.match(
      await browser.evaluate("document.querySelector('[role=dialog] svg[role=img]')?.getAttribute('aria-label') ?? ''"),
      /3 tables, 2 references/,
      "the diagram's accessible name states its size",
    );
    assert.equal(
      await browser.evaluate("document.querySelector('[role=dialog]')?.textContent.includes('comments (article_id)') === true"),
      true,
      "the grouped relationship list (the diagram's text alternative) enumerates each edge",
    );
    await runAxe(browser, axe.source, "Studio schema relationships explorer");
    assert.equal(await clickDialogButton("Close"), true, "Close should dismiss the Schema relationships explorer");
    await browser.waitFor("document.querySelector('[role=dialog]') === null", "the Schema relationships explorer to close");

    // Data generator: builds INSERT statements of synthetic rows from the
    // authorized column metadata and loads them into the editor. It never
    // executes anything; a NOT NULL column with no default cannot be skipped;
    // a type it cannot produce is badged and left out when nullable.
    const callsBeforeDataGen = streamCalls;
    await setSQL("SELECT 1");
    assert.equal(await clickButton("Generate data…"), true, "the Generate data control should be available");
    await browser.waitFor("document.querySelector('[role=dialog] h2')?.textContent === 'Generate development data'", "the data generator to open");
    await browser.waitFor("document.querySelector('[role=dialog]')?.textContent.includes('INSERT INTO \"articles\"')", "the generated INSERT preview to render");
    assert.equal(
      await browser.evaluate("document.querySelector('[role=dialog]')?.textContent.includes('type not generatable')"),
      true,
      "the VECTOR/POINT columns should be badged as not generatable",
    );
    await runAxe(browser, axe.source, "Studio data generator (ready)");
    assert.equal(await clickDialogButton("Insert into editor"), true, "the generated rows should be insertable without execution");
    await browser.waitFor("document.querySelector('[role=dialog]') === null", "the data generator to close after inserting");
    assert.match(
      await browser.evaluate(`document.querySelector('[aria-label="SQL editor"]')?.value ?? ''`),
      /^INSERT INTO "articles" \("id", "title", "body", "metadata"\) VALUES/,
      "Insert should replace the active tab with the generated INSERT script",
    );
    assert.equal(streamCalls, callsBeforeDataGen, "generating rows must not execute anything");

    // CSV / JSON import: parses a pasted document, maps its fields to the
    // authorized columns, and builds an INSERT script into the editor. It
    // never executes; unsupported column types are not offered as targets.
    const callsBeforeImport = streamCalls;
    await setSQL("SELECT 1");
    assert.equal(await clickButton("Import data…"), true, "the Import data control should be available");
    await browser.waitFor("document.querySelector('[role=dialog] h2')?.textContent === 'Import CSV / JSON data'", "the import explorer to open");
    await browser.evaluate(`(() => {
      const el = document.getElementById('import-source');
      const setter = Object.getOwnPropertyDescriptor(window.HTMLTextAreaElement.prototype, 'value').set;
      setter.call(el, 'id,title,body\\n1,First post,Hello world\\n2,Second post,More text');
      el.dispatchEvent(new Event('input', { bubbles: true }));
    })()`);
    await browser.waitFor("document.querySelector('[role=dialog]')?.textContent.includes('INSERT INTO \"articles\"')", "the generated INSERT preview to render from the pasted CSV");
    assert.equal(
      await browser.evaluate("document.querySelector('[role=dialog]')?.textContent.includes('embedding, loc')"),
      true,
      "the VECTOR/POINT columns should be named as non-importable, not offered as targets",
    );
    await runAxe(browser, axe.source, "Studio CSV import (ready)");
    assert.equal(await clickDialogButton("Insert into editor"), true, "the built INSERT should be insertable without execution");
    await browser.waitFor("document.querySelector('[role=dialog]') === null", "the import explorer to close after inserting");
    assert.match(
      await browser.evaluate(`document.querySelector('[aria-label="SQL editor"]')?.value ?? ''`),
      /^INSERT INTO "articles" \("id", "title", "body"\) VALUES\n {2}\(1, 'First post', 'Hello world'\),\n {2}\(2, 'Second post', 'More text'\);$/,
      "Insert should replace the active tab with the INSERT script built from the CSV",
    );
    assert.equal(streamCalls, callsBeforeImport, "importing must not execute anything");

    // Parameterized DML: builds a positional-parameter ($1..$N) INSERT /
    // UPDATE / DELETE template for the table from authorized column metadata
    // and loads it into the editor. It never executes; the placeholders are
    // bound in the editor's Parameters panel.
    const callsBeforeDml = streamCalls;
    await setSQL("SELECT 1");
    assert.equal(await clickButton("Parameterized DML…"), true, "the Parameterized DML control should be available");
    await browser.waitFor("document.querySelector('[role=dialog] h2')?.textContent === 'Parameterized INSERT / UPDATE / DELETE'", "the DML builder to open");
    await browser.waitFor("document.querySelector('[role=dialog]')?.textContent.includes('INSERT INTO \"articles\"')", "the generated INSERT template to render");
    await browser.waitFor("document.querySelector('[role=dialog]')?.textContent.includes('($1, $2, $3, $4, $5, $6)')", "every column becomes a positional placeholder");
    await runAxe(browser, axe.source, "Studio parameterized DML builder (INSERT)");
    assert.equal(
      await browser.evaluate(`(() => {
        const radio = [...document.querySelectorAll('[role=dialog] input[type=radio]')].find((r) => r.value === 'delete');
        radio?.click();
        return Boolean(radio);
      })()`),
      true,
      "the DELETE statement kind should be selectable",
    );
    await browser.waitFor("document.querySelector('[role=dialog]')?.textContent.includes('DELETE FROM \"articles\"')", "switching to DELETE rebuilds the template");
    await runAxe(browser, axe.source, "Studio parameterized DML builder (DELETE)");
    assert.equal(await clickDialogButton("Insert into editor"), true, "the DML template should be insertable without execution");
    await browser.waitFor("document.querySelector('[role=dialog]') === null", "the DML builder to close after inserting");
    assert.match(
      await browser.evaluate(`document.querySelector('[aria-label="SQL editor"]')?.value ?? ''`),
      /^DELETE FROM "articles"\n {2}WHERE "id" = \$1;$/,
      "Insert should replace the active tab with the parameterized DELETE template",
    );
    assert.equal(streamCalls, callsBeforeDml, "building a DML template must not execute anything");

    // Global object search: a keyboard-driven finder over table + workflow
    // names, no fetch per object. Typing filters; Enter opens the table.
    assert.equal(await clickButton("Search objects…"), true, "the object search control should be available");
    await browser.waitFor("document.querySelector('[role=dialog] h2')?.textContent?.includes('Search objects') === true", "the object finder to open");
    await browser.waitFor("document.getElementById('studio-object-search') !== null", "the object search input");
    await browser.evaluate(`(() => {
      const el = document.getElementById('studio-object-search');
      const setter = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value').set;
      setter.call(el, 'artic');
      el.dispatchEvent(new Event('input', { bubbles: true }));
    })()`);
    await browser.waitFor("[...document.querySelectorAll('[role=dialog] [role=option]')].some((o) => o.textContent.includes('articles'))", "a matching table to be listed");
    await runAxe(browser, axe.source, "Studio object finder");
    await browser.evaluate(`(() => {
      const el = document.getElementById('studio-object-search');
      el.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true }));
    })()`);
    await browser.waitFor("document.querySelector('[role=dialog]') === null", "selecting a result to close the finder");
    await browser.waitFor("document.getElementById('studio-columns-title') !== null", "the chosen table to open in the inspector");

    // Find/replace: literal-substring search over the active tab's own SQL
    // buffer only — no execution, no server round-trip. Ctrl/Cmd+F inside
    // the editor should open the same panel as the toolbar button.
    const selectedEditorText = () => browser.evaluate(`(() => {
      const el = document.querySelector('[aria-label="SQL editor"]');
      if (!el) return null;
      return el.value.substring(el.selectionStart, el.selectionEnd);
    })()`);
    const setDialogInputByLabel = (label, placeholder, value) => browser.evaluate(`(() => {
      const panel = document.querySelector('[role=dialog][aria-label="Find and replace"]');
      const el = panel?.querySelector('input[placeholder=${JSON.stringify(placeholder)}]');
      if (!el) return false;
      const setter = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, "value").set;
      setter.call(el, ${JSON.stringify(value)});
      el.dispatchEvent(new Event("input", { bubbles: true }));
      return true;
    })()`);

    await setSQL("SELECT id FROM t WHERE id = 1 OR id = 2");

    const shortcutOpened = await browser.evaluate(`(() => {
      const editor = document.querySelector('[aria-label="SQL editor"]');
      if (!editor) return false;
      editor.focus();
      editor.dispatchEvent(new KeyboardEvent("keydown", { key: "f", ctrlKey: true, bubbles: true, cancelable: true }));
      return true;
    })()`);
    assert.equal(shortcutOpened, true, "Ctrl/Cmd+F inside the editor should be dispatchable");
    await browser.waitFor('document.querySelector(\'[role=dialog][aria-label="Find and replace"]\') !== null', "Ctrl/Cmd+F to open the find/replace panel");
    await runAxe(browser, axe.source, "Studio find/replace panel (empty)");

    assert.equal(await setDialogInputByLabel("Find", "Text to find", "id"), true, "the Find field should accept input");
    await browser.waitFor(
      'document.querySelector(\'[role=dialog][aria-label="Find and replace"]\')?.textContent.includes("3 matches")',
      "the match count to reflect all three occurrences before any navigation",
    );

    const starts = [];
    for (let i = 0; i < 3; i++) {
      assert.equal(await clickButton("Next"), true, "Next should be clickable while matches exist");
      assert.equal(await selectedEditorText(), "id", `Next should select an "id" occurrence (iteration ${i})`);
      starts.push(await browser.evaluate('document.querySelector(\'[aria-label="SQL editor"]\')?.selectionStart'));
    }
    assert.equal(new Set(starts).size, 3, "three clicks of Next should visit three distinct occurrences");
    assert.equal(await clickButton("Next"), true, "a fourth Next click should wrap around");
    assert.equal(await browser.evaluate('document.querySelector(\'[aria-label="SQL editor"]\')?.selectionStart'), starts[0], "Next must wrap back to the first occurrence");
    await runAxe(browser, axe.source, "Studio find/replace panel (navigated)");

    assert.equal(await setDialogInputByLabel("Replace with", "Replacement text", "pk"), true, "the Replace field should accept input");
    assert.equal(await clickButton("Replace"), true, "Replace should be clickable while the selection is on a match");
    assert.equal(
      await browser.evaluate('document.querySelector(\'[aria-label="SQL editor"]\')?.value'),
      "SELECT pk FROM t WHERE id = 1 OR id = 2",
      "Replace should substitute only the currently selected occurrence and advance",
    );
    await browser.waitFor(
      'document.querySelector(\'[role=dialog][aria-label="Find and replace"]\')?.textContent.includes("Match 1 of 2")',
      "the match count to drop by one after a single Replace",
    );

    assert.equal(await clickButton("Replace all"), true, "Replace all should be clickable");
    assert.equal(
      await browser.evaluate('document.querySelector(\'[aria-label="SQL editor"]\')?.value'),
      "SELECT pk FROM t WHERE pk = 1 OR pk = 2",
      "Replace all should substitute every remaining occurrence in one pass",
    );
    await browser.waitFor(
      'document.querySelector(\'[role=dialog][aria-label="Find and replace"]\')?.textContent.includes("Replaced 2 occurrences.")',
      "Replace all should report how many occurrences it changed",
    );
    assert.equal(await browser.evaluate('document.querySelector(\'[role=dialog][aria-label="Find and replace"]\')?.textContent.includes("No matches")'), true, "no \"id\" occurrences should remain to find");
    await runAxe(browser, axe.source, "Studio find/replace panel (after replace all)");

    assert.equal(await clickButtonStartingWith("Find"), true, "the Find control should close the panel when clicked again");
    await browser.waitFor('document.querySelector(\'[role=dialog][aria-label="Find and replace"]\') === null', "the find/replace panel to close");

    // Catalog-aware IntelliSense: table names are free (already in
    // bootstrap); column names for a FROM/JOIN-referenced table are
    // fetched lazily, only while the suggestion panel is open, and cached.
    const clickSuggestOption = (labelSubstring) => browser.evaluate(`(() => {
      const option = [...document.querySelectorAll('[role=dialog][aria-label="SQL suggestions"] [role=option]')]
        .find((item) => item.textContent.includes(${JSON.stringify(labelSubstring)}));
      option?.click();
      return Boolean(option);
    })()`);
    const editorKeydown = (options) => browser.evaluate(`(() => {
      const editor = document.querySelector('[aria-label="SQL editor"]');
      if (!editor) return false;
      editor.focus();
      editor.dispatchEvent(new KeyboardEvent("keydown", ${JSON.stringify({ bubbles: true, cancelable: true, ...options })}));
      return true;
    })()`);
    const setCursor = (pos) => browser.evaluate(`(() => {
      const editor = document.querySelector('[aria-label="SQL editor"]');
      if (!editor) return false;
      editor.focus();
      editor.setSelectionRange(${pos}, ${pos});
      editor.dispatchEvent(new Event("select", { bubbles: true }));
      return true;
    })()`);

    await setSQL("");
    assert.equal(await clickButton("Suggest"), true, "the Suggest control should open the panel");
    await browser.waitFor('document.querySelector(\'[role=dialog][aria-label="SQL suggestions"]\') !== null', "the SQL suggestions panel to open");
    await browser.waitFor(
      'document.querySelector(\'[role=dialog][aria-label="SQL suggestions"] [role=option]\')?.textContent.includes("articles")',
      "the only catalog table to appear with no prefix typed",
    );
    assert.equal(
      await browser.evaluate('document.querySelector(\'[aria-label="SQL editor"]\')?.getAttribute("aria-expanded")'),
      "true",
      "the editor should report combobox-expanded state while suggesting",
    );
    await runAxe(browser, axe.source, "Studio SQL suggestions (table names)");

    assert.equal(await clickSuggestOption("articles"), true, "clicking a table suggestion should be possible");
    await browser.waitFor(
      'document.querySelector(\'[aria-label="SQL editor"]\')?.value === "articles"',
      "accepting a table suggestion into an empty buffer",
    );
    await browser.waitFor('document.querySelector(\'[role=dialog][aria-label="SQL suggestions"]\') === null', "accepting a suggestion should close the panel");

    // A FROM-referenced table's columns are fetched only once the panel is
    // opened, then cached across a close/reopen cycle.
    await setSQL("SELECT * FROM articles WHERE i");
    await setCursor("SELECT * FROM articles WHERE i".length);
    const tableCallsBeforeSuggest = studioTableCalls;
    assert.equal(await clickButton("Suggest"), true, "Suggest should reopen for the new buffer");
    await browser.waitFor(
      'document.querySelector(\'[role=dialog][aria-label="SQL suggestions"] [role=option]\')?.textContent.includes("id — articles")',
      "a column suggestion once the referenced table's catalog resolves",
    );
    assert.equal(studioTableCalls, tableCallsBeforeSuggest + 1, "opening Suggest should fetch the referenced table's columns exactly once");
    assert.equal(
      await browser.evaluate('document.querySelectorAll(\'[role=dialog][aria-label="SQL suggestions"] [role=option]\').length'),
      1,
      "the \"i\" prefix should match only the id column, not the table name articles",
    );
    await runAxe(browser, axe.source, "Studio SQL suggestions (column names)");

    assert.equal(await clickSuggestOption("id — articles"), true, "clicking the column suggestion should be possible");
    await browser.waitFor(
      'document.querySelector(\'[aria-label="SQL editor"]\')?.value === "SELECT * FROM articles WHERE id"',
      "accepting a column suggestion replaces only the partial word being typed",
    );

    const tableCallsBeforeReopen = studioTableCalls;
    assert.equal(await clickButton("Suggest"), true, "Suggest should reopen a second time for the same buffer");
    await browser.waitFor('document.querySelector(\'[role=dialog][aria-label="SQL suggestions"]\') !== null', "the panel to reopen");
    assert.equal(studioTableCalls, tableCallsBeforeReopen, "a table already cached must not be re-fetched on reopen");

    // Keyboard path: Escape closes without touching the buffer; Ctrl+Space
    // reopens; Up/Down move the active option and Enter accepts it —
    // proving the whole flow is keyboard-operable without a mouse.
    assert.equal(await editorKeydown({ key: "Escape" }), true, "Escape should be dispatchable");
    await browser.waitFor('document.querySelector(\'[role=dialog][aria-label="SQL suggestions"]\') === null', "Escape to close the suggestions panel");
    assert.equal(
      await browser.evaluate('document.querySelector(\'[aria-label="SQL editor"]\')?.value'),
      "SELECT * FROM articles WHERE id",
      "Escape must not modify the buffer",
    );

    await setSQL("SELECT * FROM articles WHERE id = 1 ORDER BY ");
    await setCursor("SELECT * FROM articles WHERE id = 1 ORDER BY ".length);
    assert.equal(await editorKeydown({ key: " ", ctrlKey: true }), true, "Ctrl+Space should be dispatchable inside the editor");
    await browser.waitFor('document.querySelector(\'[role=dialog][aria-label="SQL suggestions"]\') !== null', "Ctrl+Space to reopen the suggestions panel");
    await browser.waitFor(
      'document.querySelectorAll(\'[role=dialog][aria-label="SQL suggestions"] [role=option]\').length > 1',
      "an empty word after ORDER BY should list every catalog table and cached column",
    );
    assert.equal(await editorKeydown({ key: "ArrowDown" }), true, "ArrowDown should be dispatchable while suggesting");
    assert.equal(await editorKeydown({ key: "ArrowDown" }), true, "a second ArrowDown should move further down the list");
    const activeOptionText = await browser.evaluate(
      'document.querySelector(\'[role=dialog][aria-label="SQL suggestions"] [aria-selected="true"]\')?.textContent',
    );
    assert.ok(activeOptionText, "ArrowDown must move a real aria-selected option, matching the editor's aria-activedescendant");
    assert.equal(
      await browser.evaluate('document.querySelector(\'[aria-label="SQL editor"]\')?.getAttribute("aria-activedescendant")'),
      await browser.evaluate('document.querySelector(\'[role=dialog][aria-label="SQL suggestions"] [aria-selected="true"]\')?.id'),
      "aria-activedescendant must name the same option the visible highlight marks",
    );
    assert.equal(await editorKeydown({ key: "Enter" }), true, "Enter should be dispatchable to accept the active suggestion");
    await browser.waitFor('document.querySelector(\'[role=dialog][aria-label="SQL suggestions"]\') === null', "Enter to accept and close the panel");
    assert.equal(
      await browser.evaluate('document.querySelector(\'[aria-label="SQL editor"]\')?.value.startsWith("SELECT * FROM articles WHERE id = 1 ORDER BY ")'),
      true,
      "Enter must insert the active suggestion at the cursor rather than doing nothing",
    );
    await runAxe(browser, axe.source, "Studio SQL suggestions (keyboard-navigated)");

    // JSON-path completion: with the caret inside a dotted path, the panel
    // switches to the JSON paths the referenced table is actually indexed on
    // (ix_metadata_first_tag on metadata.tags.0 in this fixture) — the only
    // JSON structure the server exposes metadata for.
    await setSQL("SELECT metadata. FROM articles");
    await setCursor("SELECT metadata.".length);
    assert.equal(await editorKeydown({ key: " ", ctrlKey: true }), true, "Ctrl+Space should open completion inside the dotted path");
    await browser.waitFor(
      'document.querySelector(\'[role=dialog][aria-label="SQL suggestions"]\')?.textContent.includes("Indexed JSON paths")',
      "the panel to switch to JSON-path mode",
    );
    await browser.waitFor(
      'document.querySelector(\'[role=dialog][aria-label="SQL suggestions"] [role=option]\')?.textContent.includes("metadata.tags.0 — articles")',
      "the indexed JSON path to be offered",
    );
    await runAxe(browser, axe.source, "Studio SQL suggestions (indexed JSON paths)");
    assert.equal(await clickSuggestOption("metadata.tags.0 — articles"), true, "clicking the JSON-path suggestion should be possible");
    await browser.waitFor(
      'document.querySelector(\'[aria-label="SQL editor"]\')?.value === "SELECT metadata.tags.0 FROM articles"',
      "accepting a JSON-path suggestion replaces the whole dotted path",
    );

    // Deterministic misspelled table-name suggestions: a bare FROM/JOIN
    // target that matches no real table, but is one edit away from the
    // only real table in this fixture ("articles"), gets a live, polite
    // (not interrupting) "Use ... instead" notice with no fetch involved.
    await setSQL("SELECT * FROM artikles WHERE id = 1");
    await browser.waitFor(
      "document.body.textContent.includes('\"artikles\" doesn\\'t match any table you can see.')",
      "the misspelled-table notice to appear",
    );
    await runAxe(browser, axe.source, "Studio misspelled table-name notice");

    assert.equal(await clickButton('Use "articles" instead'), true, "the fix button should be clickable");
    await browser.waitFor(
      'document.querySelector(\'[aria-label="SQL editor"]\')?.value === "SELECT * FROM articles WHERE id = 1"',
      "accepting the fix should rewrite only the misspelled table name",
    );
    assert.equal(
      await browser.evaluate("document.body.textContent.includes('doesn\\'t match any table you can see')"),
      false,
      "the notice must disappear once the buffer no longer references an unknown table",
    );

    // A real table name is never flagged, and a schema-qualified target
    // (no ground-truth list to check it against) is never flagged either.
    await setSQL("SELECT * FROM articles");
    assert.equal(
      await browser.evaluate("document.body.textContent.includes('doesn\\'t match any table you can see')"),
      false,
      "a real table name must never be flagged",
    );
    await setSQL("SELECT * FROM system.capabilities");
    assert.equal(
      await browser.evaluate("document.body.textContent.includes('doesn\\'t match any table you can see')"),
      false,
      "a schema-qualified target must never be flagged",
    );

    // Saved queries: name + tag the current buffer, filter, load it back,
    // then delete. Persisted to localStorage per connection; text only.
    await setSQL("SELECT saved_query_marker FROM t");
    assert.equal(await clickButtonStartingWith("Saved"), true, "the Saved queries control should be available");
    await browser.waitFor("document.getElementById('studio-saved-name') !== null", "the saved-queries panel");
    assert.equal(await setInputById("studio-saved-name", "Marker report"), true, "the saved-query name field should accept input");
    await setInputById("studio-saved-tags", "reporting");
    assert.equal(await clickButton("Save current query"), true, "Save current query should be clickable");
    await browser.waitFor("document.body.textContent.includes('Marker report')", "the saved query to be listed");
    await browser.waitFor(
      `(() => { try { return Object.keys(localStorage).some((k) => k.startsWith('nextsql-studio-saved:') && localStorage.getItem(k).includes('saved_query_marker')); } catch { return false; } })()`,
      "the saved query to be mirrored to localStorage",
    );
    assert.equal(
      await browser.evaluate(`(() => {
        const labels = [...document.querySelectorAll('.nss-saved-transfer button')].map((b) => b.textContent.trim());
        return labels.includes('Export…') && labels.includes('Import…');
      })()`),
      true,
      "the saved-queries panel offers file export and import",
    );
    assert.equal(
      await browser.evaluate(`document.querySelector('input[type="file"][accept*="json"]') !== null`),
      true,
      "a hidden JSON file input backs the import control",
    );
    await runAxe(browser, axe.source, "Studio saved queries panel");
    await setSQL("SELECT something_else");
    assert.equal(
      await browser.evaluate(`(() => {
        const btn = [...document.querySelectorAll('.nss-saved-load')].find((b) => b.textContent.includes('Marker report'));
        btn?.click();
        return Boolean(btn);
      })()`),
      true,
      "clicking a saved query should load it",
    );
    await browser.waitFor(
      `document.querySelector('[aria-label="SQL editor"]')?.value.includes('saved_query_marker') === true`,
      "loading a saved query to replace the editor buffer",
    );
    // Loading closes the panel; reopen it to delete.
    assert.equal(await clickButtonStartingWith("Saved"), true, "the Saved queries control should reopen");
    await browser.waitFor("document.body.textContent.includes('Marker report')", "the saved query still listed on reopen");
    assert.equal(
      await browser.evaluate(`(() => {
        const btn = [...document.querySelectorAll('.nss-saved-item button')].find((b) => b.textContent.trim() === 'Delete');
        btn?.click();
        return Boolean(btn);
      })()`),
      true,
      "a saved query should be deletable",
    );
    await browser.waitFor("document.body.textContent.includes('No saved queries yet.')", "the saved query list to empty after delete");
    await browser.press("Escape");

    // Crash recovery: the editor buffer is mirrored to localStorage and
    // rehydrated on reload, with a dismissable "restored" notice.
    await setSQL("SELECT crash_recovery_marker FROM t");
    await browser.waitFor(
      `(() => { try { return Object.keys(localStorage).some((k) => k.startsWith('nextsql-studio-drafts:') && localStorage.getItem(k).includes('crash_recovery_marker')); } catch { return false; } })()`,
      "the editor buffer to be mirrored to localStorage",
    );
    await browser.reload();
    await browser.waitFor("document.querySelector('h1')?.textContent === 'Studio'", "Studio to re-mount after reload");
    await browser.waitFor(
      `document.querySelector('[aria-label="SQL editor"]')?.value.includes('crash_recovery_marker') === true`,
      "the unsaved buffer to be restored after reload",
    );
    await browser.waitFor(
      "document.body.textContent.includes('Unsaved editor tabs restored')",
      "the restored-drafts notice to appear",
    );
    await runAxe(browser, axe.source, "Studio restored unsaved editor tabs");
    assert.equal(await clickButton("Start fresh"), true, "the Start fresh control should be available");
    await browser.waitFor(
      `document.querySelector('[aria-label="SQL editor"]')?.value.includes('crash_recovery_marker') === false`,
      "Start fresh to clear the restored buffer",
    );
    assert.equal(
      await browser.evaluate("document.body.textContent.includes('Unsaved editor tabs restored')"),
      false,
      "the notice should dismiss after Start fresh",
    );

    console.log("operate-mode accessibility audit passed (login/overview/Studio workspace + axe WCAG 2.2 AA tags)");
  });
}

await testSetupMode();
await testOperateMode();
console.log("nextsql-admin accessibility audit passed");
