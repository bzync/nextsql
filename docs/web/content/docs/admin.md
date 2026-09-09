# NextSQL Admin

`nextsql-admin` is one binary with three modes. It is a protocol client: it never reads database files, never holds the root unlock key, and never bypasses RBAC.

```text
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

## Setup mode

Covers welcome, paths and dry-run validation, resource preset, administrator, summary, install, and completion. The GUI defaults to **Production** (`--profile production`): skip-init is disabled and an administrator is required. The CLI default remains `developer` so `--skip-init` scripts keep working. On a first install, recovery-key export is enabled by default for both keystores; Finish remains disabled until the operator confirms both exported files were copied offline. The wizard passes paths to `nextsql setup` and never handles key material itself. Linux `.tar.gz` / `.run` / `.deb` / `.rpm` and silent/offline/upgrade/repair paths are live-verified. Windows/macOS packaged execution remains environment-blocked.

The same work can be done without the GUI:

```bash
nextsql setup --data-dir /var/lib/nextsql --key-file /etc/nextsql/root.key \
  --profile production --preset balanced --user app --password-file /tmp/nextsql.pw \
  --recovery-key-out /etc/nextsql/recovery.key
nextsql lifecycle detect --data-dir /var/lib/nextsql --json
nextsql lifecycle upgrade --data-dir /var/lib/nextsql --key-file /etc/nextsql/root.key
```

See [Install](/docs/install) and [Command line](/docs/cli).

## Operations mode

It talks to `nextsqld` over NSQL with the operator's credentials. Server-enforced RBAC still applies: a user without `ADMIN` does not get a security dashboard by visiting Admin. Surfaces include overview, storage, connections and activity, cluster, backup, configuration, and audit — all from `system.*` and the official CLI, never by opening data-directory files. The Databases view expands the deployment's database into its tables. Backup and verification controls keep their in-flight state visible and disable conflicting actions; a request in flight keeps an Operations session from expiring by idleness, while its absolute lifetime remains enforced.

## Studio mode

Studio is in progress on the same Operations session. Current surfaces include:

- authenticated SQL editor with bounded streaming typed results, copy/export, session-scoped lock-wait cancel, tabs/scripts/history/find, crash recovery for unsaved tabs, and layout persistence without credentials
- catalog-aware table/column IntelliSense (no keyword completion), misspelled `FROM`/`JOIN` table-name suggestions, indexed JSON-path completion, and vector-aware `NEAREST` / `USING` completion
- positional `$1..$N` prepared parameters
- five dedicated native explorers (JSON, full-text, vector, hybrid, geo), opened from the editor **More** menu
- Users & roles, live Transactions & locks, verified Audit, Workflows/tasks/change streams, schema-migration history (also under **More**)
- table inspector: statistics, foreign keys, inbound “Referenced by”, constraints, DDL, dependencies
- schema-relationship diagram, global object search, command palette (Ctrl/Cmd+K)
- bounded per-tab EXPLAIN comparison and an ANALYZE-only profiler
- Switch connection / recent connections / read-consistency control
- review-only builders that load SQL into the editor and never execute: data generator, CSV/JSON/NDJSON import, parameterized DML, table/index designer with live native DDL preview

Studio is not a generic SQL client. It uses official NextSQL interfaces only. NextSQL Intelligence / RAG is **not in the product**.

Engine notes: [`docs/design-admin.md`](https://github.com/bzync/nextsql/blob/main/docs/design-admin.md), [`docs/design-admin-setup.md`](https://github.com/bzync/nextsql/blob/main/docs/design-admin-setup.md), [`docs/design-admin-operations.md`](https://github.com/bzync/nextsql/blob/main/docs/design-admin-operations.md), [`docs/design-admin-studio.md`](https://github.com/bzync/nextsql/blob/main/docs/design-admin-studio.md).
