# NextSQL Admin

`nextsql-admin` is one binary with three modes. It is a protocol client: it never reads database files, never holds the root unlock key, and never bypasses RBAC.

```arch
Setup        install / upgrade / repair / uninstall
Operations   server, cluster, security, backup, activity
Studio       SQL workspace, explorers, EXPLAIN, catalog
```

At start it runs `nextsql lifecycle detect --json` and picks a mode:

- **No initialized installation → Setup.** Token-authenticated loopback wizard. Drives `nextsql setup` as a subprocess and opens the system browser.
- **Initialized installation → Operations**, with Studio on the same session. Operator logs in with real NSQL credentials. Default listen is `127.0.0.1:7220`.

`--mode setup|operate` overrides detection. Ambiguous detection fails closed.

```bash
go build -o nextsql-admin ./cmd/nextsql-admin
nextsql-admin
```

Keep Admin on loopback. It is not a remote management plane.

### HTTP routing

Open the URL printed by `nextsql-admin` (Operations mode defaults to
`http://127.0.0.1:7220/`). Its frontend and JSON API deliberately share one
origin: every `/api/v1/*` request must reach the same `nextsql-admin` process.
If a local reverse proxy is used, proxy the whole `/api/v1/` subtree; never let
unmatched API paths fall through to a website or single-page-app rewrite.

This check should return JSON, not HTML:

```bash
curl -i http://127.0.0.1:7220/api/v1/mode
```

The expected media type is `application/json` and the body identifies `setup`
or `operate`. An HTML response (for example a Next.js document) means the
browser is pointed at the wrong origin or the proxy route is incomplete.
Admin's JSON client and Studio's query stream both refuse that document:
the error names the `/api/v1/` route and does not display the HTML.

## Setup mode

Covers welcome, paths and dry-run validation, resource preset, administrator, summary, install, and completion. The GUI defaults to **Production** (`--profile production`): skip-init is disabled and an administrator is required. The CLI default remains `developer` so `--skip-init` scripts keep working. On a first install, recovery-key export is enabled by default for both keystores; Finish remains disabled until the operator confirms both exported files were copied offline. The wizard passes paths to `nextsql setup` and never handles key material itself. Linux `.tar.gz` / `.run` / `.deb` / `.rpm` and silent/offline/upgrade/repair paths are live-verified. macOS packaged execution remains environment-blocked. Native Windows is not supported; use WSL 2.

The same work can be done without the GUI:

```bash
nextsql setup --data-dir /var/lib/nextsql --key-file /etc/nextsql/root.key \
  --profile production --preset balanced --user app --password-file /tmp/nextsql.pw \
  --database app --recovery-key-out /etc/nextsql/recovery.key
nextsql lifecycle detect --data-dir /var/lib/nextsql --json
nextsql lifecycle upgrade --data-dir /var/lib/nextsql --key-file /etc/nextsql/root.key
```

See [Install](/docs/install) and [Command line](/docs/cli).

## Operations mode

It talks to `nextsqld` over NSQL with the operator's credentials. Server-enforced RBAC still applies: a user without `ADMIN` does not get a security dashboard by visiting Admin. Surfaces include overview, storage, connections and activity, cluster, backup, configuration, and audit — all from `system.*` and the official CLI, never by opening data-directory files. The Databases view expands the deployment's database into its tables. Backup and verification controls keep their in-flight state visible and disable conflicting actions. A signed-in session and its connection to `nextsqld` stay up until the operator logs out, switches server, or Admin stops. A request that is still running is not cut off by an HTTP write deadline. `--idle-timeout` and `--session-lifetime` are optional bounds; `0`, the default, sets neither. A request in flight still does not count as idle when a bound is set, and a positive `--session-lifetime` is not extended by that request.

### Connection profiles

One Admin can sign in to, and switch between, several `nextsqld` servers — a deployment serves exactly one database, so this is how you work with several. The `--server-addr` target is the `default` profile (`--server-name` / `--server-environment` label it); a `--profiles FILE` names more:

```json
{
  "version": 1,
  "profiles": [
    {"id": "staging", "name": "Staging", "environment": "staging",
     "address": "db.staging.internal:7210", "tls_ca": "certs/staging-ca.pem", "user": "app"},
    {"id": "prod", "name": "Production", "environment": "production",
     "address": "db.prod.internal:7210", "tls_ca": "/etc/nextsql/prod-ca.pem"}
  ]
}
```

The file, not the browser, decides which servers Admin may reach: the sign-in page and the **Switch server** dialog can only pick a profile by ID, and the sign-in page never shows where a profile points. The file holds no secret, is read strictly (an unknown key is an error), and must not be writable by group or others. Each profile follows the same TLS rules as the flags (`tls_ca` required unless `insecure`, which is loopback-only). A switch is a fresh sign-in that replaces your session only once it succeeds. A `production` profile shows a banner on every view and starts Studio in read-only mode. On a switch you may save the password in the operating system's credential store; it is usable only when switching again from the same server and user that saved it.

## Studio mode

Studio is on the same Operations session. Surfaces include:

- authenticated SQL editor with bounded streaming typed results, copy/export, session-scoped lock-wait cancel, tabs/scripts/history/find, crash recovery for unsaved tabs, and layout persistence without credentials
- catalog-aware table/column IntelliSense (no keyword completion), misspelled `FROM`/`JOIN` table-name suggestions, indexed JSON-path completion, and vector-aware `NEAREST` / `USING` completion
- positional `$1..$N` prepared parameters
- five dedicated native explorers (JSON, full-text, vector, hybrid, geo), opened from the editor **More** menu
- Users & roles, live Transactions & locks, verified Audit, Workflows/tasks/change streams, schema-migration history (also under **More**)
- table inspector: statistics, foreign keys, inbound “Referenced by”, constraints, DDL, dependencies
- schema-relationship diagram, global object search, command palette (Ctrl/Cmd+K)
- bounded per-tab EXPLAIN comparison and an ANALYZE-only profiler
- server switching between connection profiles, and a read-consistency control
- review-only builders that load SQL into the editor and never execute: data generator, CSV/JSON/NDJSON import, parameterized DML, table/index designer with live native DDL preview

Studio is not a generic SQL client. It uses official NextSQL interfaces only. NextSQL Intelligence / RAG is **not in the product**.

Engine notes: [`docs/install.md`](https://github.com/bzync/nextsql/blob/master/docs/install.md).
