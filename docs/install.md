# Installation and first-run setup (Phase 28)

Phase 28 splits the operator-facing surface into three products:

```text
NextSQL Installer → install / upgrade / repair / uninstall
NextSQL Manager   → server / cluster / security / backup / operations
NextSQL Studio    → database development / SQL / data / schema / RAG   (Phase 29)
```

This note covers the **automation backbone** shared by all of them:
`nextsql setup`. The OS-native installers in `packaging/` (see
`packaging/README.md`) and, later, the Manager GUI all drive the same code
path — nothing an installer does to bring a server up is unavailable to a
script.

## `nextsql setup`

`nextsql setup` is one non-interactive command that:

1. Detects the host's CPU count, physical RAM, and the free/total space and
   filesystem type of the volume that will hold the data directory
   (`internal/sysinfo`).
2. Sizes the buffer pool from a **resource preset** (below).
3. Writes a validated `nextsql.conf` with secure defaults — loopback-only
   listener, TLS required for any non-loopback address, keys never written
   to the config.
4. Initializes the database through the exact same path as `nextsql init`
   (root key file, deployment-registry key file, encrypted store, optional
   bootstrap administrator).
5. Verifies the result — on-disk header compatibility plus a clean engine
   open — before reporting success.

```bash
printf 'a-strong-passphrase\n' > /tmp/nextsql.pw && chmod 600 /tmp/nextsql.pw

nextsql setup \
  --data-dir /var/lib/nextsql \
  --key-file /etc/nextsql/root.key \
  --preset balanced \
  --user app --password-file /tmp/nextsql.pw
```

The generated config is written to `DATA-DIR/nextsql.conf` unless
`--config-out` overrides it. Start the server with it:

```bash
nextsqld --config /var/lib/nextsql/nextsql.conf
```

### Resource presets

`--preset` sizes the buffer pool as a fraction of detected physical RAM:

| Preset | Buffer pool | Use |
|---|---|---|
| `conservative` | 10% of RAM | shared hosts, containers with tight limits |
| `balanced` (default) | 25% of RAM | general-purpose deployments |
| `high-performance` | 50% of RAM | dedicated database hosts |
| `custom` | `--buffer-pages` verbatim | explicit control |

The derived pool is never smaller than the built-in default
(`config.DefaultBufferPages`, 16 MiB) and never larger than 75% of RAM
regardless of preset — NextSQL also needs the OS page cache for WAL,
checkpoints, and sort/hash spill. When physical RAM cannot be detected (any
non-Linux host today), setup falls back to the built-in default and says so
in a warning; set `--buffer-pages` explicitly there. `--buffer-pages` always
overrides the preset.

### Secure defaults

- `--listen` defaults to `127.0.0.1:7210`. Any non-loopback address
  (including `0.0.0.0` / `::`) is **rejected** unless `--tls-cert` and
  `--tls-key` are both given — setup exits `6` (validation) and writes
  nothing.
- Root unlock key and deployment-registry key are created at the
  `--key-file` path and `--key-file`.instance, mode `0600`; keep this path
  off the data volume in production.
- The generated `nextsql.conf` is mode `0640` and contains no key, password,
  or token material — only file paths.
- Passing `--user` without `--password-file` (or vice versa) is rejected.
  Omitting both initializes the database with no administrator and emits a
  warning.

### Machine-readable output

`--json` prints a single JSON object on stdout instead of the text summary:
detected hardware, the sizing decision and its rationale, the resolved
paths, whether the database was initialized, the health-check result, and
any warnings. Combined with the exit codes below it is the intended
interface for the OS installers and container init.

### Exit codes

`nextsql setup` uses the shared `nextsql` exit-code scheme (`internal/cli`):

| Code | Meaning |
|---|---|
| 0 | success |
| 1 | malformed flag / unknown preset |
| 5 | post-install health check failed, or the data directory is already initialized |
| 6 | listen address or configuration failed validation |
| 7 | required `--data-dir` / `--key-file` missing |

### Transactional rollback

If `nextsql setup` fails after it has started creating files — a partial
`nextsql init`, a failed post-install health check — it **rolls back exactly
what this run created**: the database and its sidecars, the deployment
registry, the generated `nextsql.conf`, and any key files it generated. It
records whether each path already existed *before* the run and never removes
a pre-existing one, so an operator-supplied root key or a data directory that
already held other files is left untouched (the data directory is removed
only if it comes out empty). `--keep-failed` disables the rollback and leaves
the partial install in place for inspection — clean it up afterwards with
`nextsql lifecycle uninstall`.

### Other flags

| Flag | Effect |
|---|---|
| `--dry-run` | detect + plan + print; create, write, and initialize nothing |
| `--skip-init` | generate/refresh `nextsql.conf` only; do not touch the database |
| `--force` | overwrite an existing `nextsql.conf` that differs from the plan |
| `--keep-failed` | on failure, leave the partial install in place instead of rolling it back |
| `--config-in FILE` | load defaults from an existing key=value config before applying flags |
| `--instance-key-file` | deployment-registry key path (default `KEY-FILE.instance`) |
| `--realm` / `--database` | bootstrap realm/database names (default `default`/`default`) |
| `--log-level` | `debug` \| `info` (default) \| `warn` \| `error` |

Re-running a full `nextsql setup` against an already-initialized data
directory is refused (already-exists): use `--skip-init` to regenerate only
the config, or the `nextsql hosting` subcommands for lifecycle operations.
Idempotent re-runs are safe with `--skip-init` when the resulting config is
unchanged.

## `nextsql lifecycle` — detect, preflight, config backup

`nextsql lifecycle` is the non-interactive backbone for the *lifecycle*
half of the Installer product (detect existing install / upgrade / repair /
uninstall). Three subcommands are implemented; each takes `--json` and uses
the shared exit-code scheme.

### `nextsql lifecycle detect --data-dir DIR [--config FILE]`

Non-destructive discovery. Reports whether a `nextsql.conf` is present and
parses (and, if so, the data-dir / key-file / listen it resolves to),
whether the database is initialized (`nextsql.db` + keystore), whether the
on-disk headers are compatible with this binary, the format database id, and
whether a NextSQL process currently holds the deployment lock. It reduces
all of that to one **status**:

| Status | Meaning |
|---|---|
| `none` | no config and no initialized database |
| `config-only` | a config file exists; the database is not initialized |
| `initialized` | database present, no server running |
| `running` | a NextSQL process holds the deployment lock |

`detect` always exits `0` unless a flag is malformed or `--data-dir` is
missing (`7`). It is a query.

### `nextsql lifecycle preflight --data-dir DIR`

Upgrade preflight: may *this* binary open the data directory in place? It
reads the plaintext superblock / WAL-control / UNDO-control / envelope
headers (no root key required), checks each version against this binary's
format-compatibility catalog (`internal/upgrade/compat`), and returns a
**verdict**:

| Verdict | Exit | Fix |
|---|---|---|
| `ready` | 0 | proceed |
| `not-initialized` | 7 | nothing to upgrade |
| `server-running` | 2 | stop the server first |
| `blocked-too-new` | 6 | install a newer `nextsqld` |
| `blocked-too-old` | 6 | migrate via `nextsql export` / `import` into a fresh database |
| `blocked-damaged` | 6 | run `nextsql diagnose`, restore from a verified backup |

Preflight is advisory — it releases the deployment lock before returning, so
the actual upgrade tool must hold the lock across the whole operation. It
also only covers the keyless header surface; the catalog/protocol
compatibility inside the encrypted store is enforced again at server start.

### `nextsql lifecycle backup-config --config FILE [--out DIR]`

Copies the live config to a timestamped sibling
`nextsql.conf.bak-YYYYMMDDThhmmssZ` (or into `--out`), mode `0640`, then
reloads the copy and confirms it parses back to an identical config before
reporting success. Refuses a source that does not exist (`7`) or does not
parse. This is the "config backup before upgrade" step.

### `nextsql lifecycle upgrade --data-dir DIR --key-file FILE [--config FILE]`

The mutating in-place upgrade runner. It performs these ordered steps and
never deletes anything:

1. **acquire the deployment lock** and hold it for the whole operation, so no
   `nextsqld` can start between the preflight and the verification open. A
   lock it cannot take means a server is already using the directory —
   `upgrade` stops with `server-running` (exit `2`).
2. **preflight** the plaintext headers exactly as `lifecycle preflight`
   does. A non-`ready` verdict stops the run before any mutation, with the
   same exit codes (`6` blocked-*, `7` not-initialized).
3. **back up the config** via the same verified copy as `backup-config`
   (skipped when no `nextsql.conf` is present). A backup that cannot be
   written or does not reload identically aborts the run — the copy that was
   written is left in place and its path is reported.
4. **open the encrypted store** with this binary. This runs WAL recovery —
   the actual in-place change — and confirms the catalog decodes under the
   new format code while the operator is still watching, rather than at the
   first client connection. `--buffer-pages` sizes this open (default
   `config.DefaultBufferPages`).
5. **re-verify** the headers still read cleanly and report the table count
   and durable LSN.

| Outcome | Exit | Meaning |
|---|---|---|
| `applied` | 0 | recovery ran and re-verification passed |
| `dry-run` | 0 | `--dry-run`: preflight was ready, nothing was backed up or opened |
| `blocked` | 2 / 6 / 7 | preflight refused, or a clustered node was not acknowledged; nothing was mutated |
| `failed-verify` | 5 | the config backup failed, or the engine did not open / verify — any backup already written is retained for rollback |

`upgrade` is idempotent: a second run against an already-current data
directory simply opens it again and reports `applied`. It does **not** swap
the `nextsqld` binary or restart a service — the OS installer or the operator
does that around this call. `--dry-run` prints the plan and preflight verdict
and mutates nothing; `--json` emits one object (plan steps, assessment,
config-backup path, engine-open result).

**Rolling-cluster upgrade integration.** When the data directory shows Raft
membership — a `raft/` state directory, the key-free `nextsql.cluster.json`
status file, or `node_id` + `raft_bind` in the config — `upgrade` will not
mutate the node until you pass **`--cluster-node`**, which asserts the node
has already been drained (and, if it was the leader, that leadership was
transferred and a new leader confirmed). Without it the run stops at
`blocked` (exit `6`) and prints the per-node procedure; `--dry-run` and
`detect` show the same procedure without the gate. The offline upgrade of one
*stopped* node is safe on its own — it runs WAL recovery only and the Raft
log replays on restart — so the flag is a sequencing acknowledgment, not a
technical barrier: what is unsafe is upgrading several nodes at once or a node
that was never drained. Run the full sequence one node at a time:

1. If this node is the current leader, `nextsql cluster transfer-leader`
   against it and wait for a new leader (`SHOW CLUSTER` on another node);
   skip for a follower.
2. `nextsql cluster drain [--timeout-ms N]` against the node.
3. Stop the process, then `nextsql lifecycle upgrade --cluster-node`.
4. Restart the upgraded node; wait for `system.replica_health.apply_backlog`
   to reach `0` before the next node.
5. Repeat for every node — a 3-voter cluster keeps quorum (2 of 3)
   throughout. The `--json` output carries this list under `rolling_upgrade`
   for an installer or the Manager to drive. See `docs/ops.md` "Rolling
   upgrade" for the operational detail and the correctness note.

### `nextsql lifecycle repair --data-dir DIR --key-file FILE`

Reconciles a damaged installation **without touching the database or the
unlock keys**. It:

1. **Regenerates a missing or unparseable `nextsql.conf`** with secure
   defaults (loopback listener, detected buffer-pool sizing). An unparseable
   config is copied to a timestamped `.bak-<UTC>` first. An existing config
   that *parses* is left untouched unless `--force-config` (which also backs
   the current one up). A regenerated config carries only what the flags say
   — `--preset` / `--buffer-pages` / `--listen` (+ `--tls-cert` / `--tls-key`)
   / `--log-level`; other custom settings must be re-applied by hand.
2. **Reports permission drift** on the config (want `0640`) and the resolved
   key files (want `0600`), and — only with `--fix-perms` — tightens them.
   Permissions are never loosened.
3. **Opens the encrypted store once** (running WAL recovery) and reports the
   table count and durable LSN.

| Outcome | Exit | Meaning |
|---|---|---|
| `repaired` | 0 | at least one fix was applied and the engine verified |
| `healthy` | 0 | nothing needed fixing; the engine verified |
| `dry-run` | 0 | `--dry-run`: the plan was printed, nothing changed |
| `blocked` | 2 / 7 | a server holds the lock, or there is no database here |
| `failed` | 5 | a step failed — most importantly, the engine would not open; run `nextsql diagnose` |

`repair` refuses to run while a `nextsqld` process holds the deployment lock.
It is not `setup`: it will not initialize a database, only mend one that
already exists.

### `nextsql lifecycle uninstall --data-dir DIR`

Removes a NextSQL installation. **The encrypted database and the external
unlock keys are preserved by default** — a plain run only removes the
installer-generated `nextsql.conf` and its timestamped backups. Deleting
anything else is opt-in:

| Flag | Also removes |
|---|---|
| *(none)* | `nextsql.conf` + `nextsql.conf.bak-*` only |
| `--purge-data` | the primary database + sidecars (keystore, WAL, UNDO, isolated-page registry), the auth / ACL / audit files, the deployment-registry database, and the deployment lock — **destroys all data** |
| `--purge-keys` | the external root and instance unlock key files — **requires `--purge-data`** (deleting the keys while the database survives leaves it permanently unreadable), and the key paths must be resolvable from a parseable config or an explicit `--key-file` |

Every run is a **dry run until `--confirm`**: without it, `uninstall` prints
exactly what it would remove and what it would preserve and exits `0`
(`outcome: planned`). It refuses to run while a `nextsqld` process holds the
deployment lock (`outcome: blocked`, exit `2`), and refuses inconsistent
purge flags (exit `6`) — in both cases nothing is deleted. Key paths are
taken from `--key-file` / `--instance-key-file` when given, otherwise from
the config. The empty `--data-dir` itself is left in place. `--json` emits
one object (the remove / preserve classification, the outcome, and the list
of paths actually removed).

Outcomes: `planned` / `removed` / `blocked` (2/6) / `partial` (5, some paths
could not be deleted — the rest were).

## Config round-tripping

`config.Config.Marshal` renders a live config back to the `key=value` format
`config.Load` reads, emitting only the keys that differ from a bare
`config.Default()` plus the core fields. `Load(Marshal(c))` reconstructs an
equal config. This is what `nextsql setup` persists (with a provenance
header) and is the basis for the "configuration viewer" / "config backup
before upgrade" items still to come in the Manager and installer-lifecycle
tracks.

## Still to come in Phase 28

The `nextsql lifecycle` backbone (`detect` / `preflight` / `backup-config` /
`upgrade` / `repair` / `uninstall`) is complete, and `upgrade` now routes a
Raft cluster node through the per-node rolling procedure (`--cluster-node`
acknowledgment; `rolling_upgrade` guidance in the `--json` output). What
remains in Phase 28: transactional rollback of installer-created files is
done for `nextsql setup`; the GUI installer UX (welcome / component selection
/ encryption wizard / staged progress / accessibility) and the entire NextSQL
Manager MVP are still open. All are tracked in `TODO.md` under Phase 28. This
note grows as they land.
