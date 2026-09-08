import assert from "node:assert/strict";
import { mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { pathToFileURL } from "node:url";
import * as esbuild from "esbuild";

const scratch = mkdtempSync(join(tmpdir(), "nextsql-setup-errors-"));
const outfile = join(scratch, "util.mjs");

try {
  await esbuild.build({
    entryPoints: [new URL("./src/setup/util.ts", import.meta.url).pathname],
    bundle: true,
    platform: "node",
    format: "esm",
    target: ["node20"],
    outfile,
  });
  const { explainSetupError } = await import(`${pathToFileURL(outfile).href}?v=${Date.now()}`);

  // Each known error surface `nextsql setup` / the Params validator can emit,
  // as it actually reaches the browser (wrapped in the CLI's "nextsql: ..."
  // stderr framing), maps to a first-run rewrite while keeping the raw text.
  const cases = [
    ["nextsql: nextsql invalid_argument: nextsql setup: a non-loopback listen address requires --tls-cert and --tls-key", /needs TLS/],
    ["nextsql invalid_argument: setup.Params: tlsCert and tlsKey must be given together", /needs TLS/],
    ["nextsql: nextsql already_exists: nextsql setup: config file /etc/nextsql/nextsql.conf already exists; pass --force to overwrite", /configuration already exists/i],
    ["nextsql: nextsql already_exists: nextsql setup: data directory already contains an initialized database; pass --skip-init to only regenerate the config", /already has a database/],
    ["nextsql: nextsql invalid_argument: nextsql setup: production profile requires --user and --password-file", /administrator account/],
    ["nextsql: nextsql invalid_argument: config.CheckProduction: production profile requires the unlock key file to be kept off the data volume", /Move the unlock key/],
    ["nextsql: nextsql invalid_argument: config.CheckProduction: production profile requires --key-file (or --instance-key-file), or --require-client-key", /needs an unlock key/],
    ["nextsql: nextsql invalid_argument: config.CheckProduction: production profile requires statement_timeout_ms > 0", /missing a required safety setting/],
    ["nextsql invalid_argument: setup.Params: production profile cannot skip initialization", /defer database creation/],
    ["nextsql: nextsql io: nextsql setup: mkdir data-dir: mkdir /var/lib/nextsql: permission denied", /couldn't write to that location/],
    ["nextsql: write config: open /etc/nextsql/nextsql.conf: operation not permitted", /couldn't write to that location/],
    ["nextsql: nextsql io: nextsql init: write page: write /data/nextsql.db: no space left on device", /disk space/i],
    ["nextsql: nextsql invalid_format: nextsql setup: post-install health check failed", /failed its self-check/],
    ["nextsql setup produced no valid JSON output", /run the NextSQL program/],
    ["exec: \"nextsql\": executable file not found in $PATH", /run the NextSQL program/],
  ];

  for (const [raw, expect] of cases) {
    const e = explainSetupError(raw);
    assert.equal(e.known, true, `expected a known rewrite for: ${raw}`);
    assert.match(`${e.title} ${e.action}`, expect, `wrong rewrite for: ${raw}`);
    assert.equal(e.detail, raw.trim(), "detail must be the verbatim message");
    assert.equal(e.detailOpen, false, "a known rewrite keeps the raw text collapsed");
    assert.ok(e.title && e.title !== "Setup couldn't finish", "a known rewrite has its own title");
  }

  // Unknown text falls back without losing information, and asks the UI to
  // show the raw message expanded.
  const unknown = explainSetupError("nextsql: nextsql internal: nextsql setup: something nobody has a rule for yet");
  assert.equal(unknown.known, false);
  assert.equal(unknown.detailOpen, true);
  assert.match(unknown.detail, /something nobody has a rule for yet/);
  assert.equal(unknown.title, "Setup couldn't finish");

  // Empty / whitespace input still renders something sane.
  const empty = explainSetupError("   ");
  assert.equal(empty.known, false);
  assert.equal(empty.detail, "No further detail was reported.");

  console.log("Setup error explainer tests passed");
} finally {
  rmSync(scratch, { recursive: true, force: true });
}
