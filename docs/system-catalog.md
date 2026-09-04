# System catalog / introspection (Phase 26)

NextSQL exposes a read-only virtual `system` schema: ordinary `SELECT`
against tables that are computed on the fly from live server/catalog state,
not stored rows. It is the one supported way for Studio/Manager, drivers, or
operators to introspect a database without reading data-directory files
directly.

```sql
SELECT * FROM system.tables;
SELECT * FROM system.capabilities WHERE status = 'supported';
SELECT name, remote FROM system.sessions WHERE state = 'active';
```

`system.*` tables support `WHERE`, `ORDER BY`, `LIMIT`, `DISTINCT`, and typed
parameters, evaluated over the generated rows — the same as any other SELECT
— but not `JOIN`, subqueries in the `FROM` clause, or `GROUP BY`/`HAVING`.
Every session needs at least `CONNECT` on the database to query any
`system.*` table; individual tables layer additional filtering on top (below).

`system.capabilities` carries the current `internal/system.SchemaVersion`
(currently 3) as the supported capability name `system_schema_v3`, so machine
consumers can detect column-contract changes without parsing prose. The
`system_show_aliases` row advertises the convenience syntax separately. Bump
`SchemaVersion` only when a `system.*` table's columns change shape.

## Catalog and storage tables

These reflect durable, replicated state (the catalog, the data file, the
Raft/replication layer) and are visible to any connected user unless noted.

| Table | Columns | Notes |
|---|---|---|
| `system.capabilities` | `name, status, description, since_version` | Feature flags and their support level. Always visible. |
| `system.tables` | `name, id, column_count, pk, legacy_tenant_column` | Filtered to tables the caller has `SELECT` on (table- or database-scoped), or all tables for an admin. |
| `system.columns` | `table_name, column_name, ordinal, type, not_null, is_primary, default_value` | Same table-visibility filter as `system.tables`. |
| `system.indexes` | `table_name, index_name, kind, is_unique, columns, include_columns, predicate, status` | Same table-visibility filter. |
| `system.table_stats` | `table_name, row_count, updated_at` | Same table-visibility filter. |
| `system.index_stats` | `table_name, index_name, row_count` | Same table-visibility filter. |
| `system.partitions` | see `docs/partitioning.md` | Same table-visibility filter. |
| `system.storage` | `database, engine, page_size, page_count, file_size, wal_lsn, encryption` | Always visible. `database` is the logical served name (`default` for unnamed embedded use), never a filesystem path. Never exposes key material; `wal_lsn` is redacted to 0. |
| `system.replication` / `system.raft` | `node_id, state, leader_id, leader_addr, voters, applied_lsn, has_leader, maintenance_mode` | Always visible. `leader_addr` is always `[redacted]` — network addresses are never exposed over SQL. `system.raft` is an alias for `system.replication`. `maintenance_mode` reflects this node's own local `CLUSTER MAINTENANCE ENABLE`/`DISABLE` state — see `docs/ops.md` "Maintenance mode"; it is not Raft-replicated, so it can legitimately differ between nodes. |
| `system.replica_health` | `node_id, role, has_leader, applied_lsn, commit_index, applied_index, apply_backlog, last_contact_ms, healthy` | Always visible. See `docs/ha.md` "Replica lag and follower health". |
| `system.workflows` | `name, owner, param_count, statement_count` | See `docs/workflows.md`. |
| `system.tasks` | `id, schedule, workflow, state, attempts` | A non-admin sees only tasks they own; an admin sees every task. |

## Live session/query/transaction/CDC tables

These reflect process-local, node-local, in-memory state — not the catalog,
not replicated, not persisted. Querying a different node's `nextsqld` shows
that node's own live state, not a cluster-wide view. A non-admin (anyone
without cluster `ADMIN`) sees only rows for their own user; an admin sees
every row. This matches the existing `system.tasks` owner-filtering pattern.

| Table | Columns | Notes |
|---|---|---|
| `system.sessions` | `session_id, user, remote, state` | One row per connection registered by the protocol server (`nextsql login`/driver connections). `state` is `active` while the session is executing a statement, else `idle`. Sessions opened by the embedded/CLI/test `Session()` API without going through the protocol server are not registered and do not appear. |
| `system.active_queries` | `query_id, user, sql, state` | One row per session presently executing a statement (i.e. currently absent for an idle session). `sql` is the exact statement text — including the querying session's own `SELECT ... FROM system.active_queries`, which always appears while it runs, same as PostgreSQL's `pg_stat_activity`. `state` is always `running` today (no queued/blocked state is tracked). |
| `system.transactions` | `txn_id, user, isolation, state` | One row per session with an open (uncommitted) transaction, explicit (`BEGIN`) or the implicit per-statement autocommit transaction while it is in flight. `isolation` is `READ COMMITTED`, `SNAPSHOT`, or `SERIALIZABLE`. `state` is always `active` (no prepared/committing intermediate state is tracked). |
| `system.locks` | `lock_id, table_name, mode, granted` | One row per currently held key or range lock. `lock_id` is a `"<txn id>:<n>"` label unique only within one query's result, not stable across queries. `mode` is `shared` or `exclusive`. `granted` is always `true` — a waiting (not-yet-granted) lock request is not surfaced. `table_name` is best-effort: it comes from a tag threaded through `btree.Tree`/`txn.LockManager` at the executor's tree-resolver call sites, and the lock table's key namespace is shared across every table in one storage engine, so it is possible (though not expected in practice) for a lock's tag to reflect a different table than the one whose row bytes happen to collide. Visibility matches `system.transactions`: a lock is attributed to the user of whichever live session currently holds that transaction; a lock whose transaction cannot be attributed to a live session (e.g. embedded/CLI/test use) is visible only to an admin. |
| `system.change_streams` | `table_name, lsn, state` | One row per open `SUBSCRIBE` on this node (see `docs/cdc.md`). `lsn` is the subscription's last-observed commit LSN, published from the subscribing session after each transaction it delivers — not a live snapshot mid-delivery. Visible to any session that can see the underlying table (same rule as `system.columns`/`system.indexes`); there is no separate `paused` state — a subscription is `active` until closed, at which point its row disappears. |

## Security administration tables

Before these landed, listing users, roles, or grants had no official SQL-level
answer at all — a Studio/Manager security dashboard would have had to read
the `auth.Store` password file or the `security.ACL` file directly, exactly
what the P26 exit gate requires it never do. `system.users`/`roles`/`grants`
reflect the durable, process-shared `auth.Store`/`security.ACL` state (not
per-session, unlike the live tables above); `system.tls` reflects the live
listener's TLS configuration instead (process-level, sourced from whatever
`nextsqld` wired via `executor.DB.SetTLSStatusSource` — see
`security.ServerTLSReloader.Status`); `system.key_versions` reflects the
attached `crypto.Envelope`'s key rotation state (sourced from
`executor.DB.SetKeyStatusSource` — see `crypto.Envelope.KeyStatus`);
`system.audit_verify`/`system.audit_log` reflect a bounded, chain-verified
re-read of the attached audit log's actual on-disk state, on every query
(sourced from `executor.DB.SetAuditSource` — see `security.TailEvents`), the
last of the three engine surfaces the Manager Security view's original M4
scope needed. All seven are **admin-only**: a non-admin gets zero rows,
never an error, matching the rest of `system.*`'s "row-filter, never fail,
on RBAC" convention. `s.acl == nil` (no ACL configured —
single-user/embedded use) is treated as admin, same as every other live
table.

| Table | Columns | Notes |
|---|---|---|
| `system.users` | `name, password_algo` | One row per stored user. `password_algo` is `argon2id` or `pbkdf2` (useful for spotting accounts still pending transparent rehash — see `docs/security.md` "Password hashing"). Never exposes the password hash or salt. |
| `system.roles` | `role, members` | One row per created role. `members` is a comma-joined, sorted list of the users or nested roles granted that role (empty string for a role with no members). |
| `system.grants` | `grantee, privilege, scope, object` | One row per persisted grant, to a user or a role. `privilege` and `scope` are rendered in the same spelling the `GRANT`/`REVOKE` statement grammar accepts (`security.Privilege.String()` / `security.ScopeKind.String()`, the exact inverse of `ParsePrivilege`/`ParseScope`). |
| `system.tls` | `enabled, subject, issuer, not_before, not_after, days_until_expiry, dns_names, mtls_required, client_ca_configured, client_crl_configured` | Always exactly one row (never zero, for an admin) — `enabled=false` with every other column blank/null when no TLS listener is attached (embedded/CLI use, a loopback plaintext deployment, or `nextsqld` running without `--tls-cert`), same "always one descriptive row" convention as `system.replication` reporting `state='single'` with no cluster attached. Sourced from the active leaf certificate only: **never carries private key material and never carries a network address** (same redaction convention as `system.replication.leader_addr`) — `subject`/`issuer` are the certificate's distinguished names, `dns_names` is the leaf's SAN list (already public to anyone who completes a handshake, so this is not new exposure), and `days_until_expiry` can go negative for an already-expired certificate that a failed `Reload` left active (the last known-good snapshot is retained — see `docs/security.md`/`ServerTLSReloader`). `mtls_required`/`client_ca_configured`/`client_crl_configured` reflect `--tls-client-ca`/`--tls-client-crl`, not per-connection state. |
| `system.key_versions` | `key_name, current_version, version_count, revoked_count, retired_count` | One row per key the attached `crypto.Envelope` manages: `kek`, `master`, and each data domain (`page`, `wal`, `undo`, `backup`, `vector`, `fulltext`, `temp`, `replication` — `crypto.DomainName`/`crypto.AllDomains`). **Never carries key material** — the underlying `crypto.KeyStatus` struct has no field capable of holding any. Unlike `system.tls`, this table is list-shaped: it returns **zero rows** (not a placeholder row) when no persistent envelope is attached — embedded/CLI use with a bare `crypto.KeyProvider`, or a legacy deployment with no `.keys` keystore file — the same "empty means not applicable" convention `system.databases`/`system.realms` already use on a non-hosted deployment. `version_count` is `len(ring.keys)` — it drops immediately on `Revoke` (which deletes the version's DEK right away), while `revoked_count`/`retired_count` come from the ring's separate flags map and only drop on a later `Retire`. `kek`/`master` always report `version_count=1`: `RotateKEK`/`RotateMaster` discard the prior key outright rather than retaining a ring. `retired_count` is always `0` today — `Retire`'s current implementation deletes a version's flag entry outright rather than ever setting a "retired" flag first, so there is no code path that produces a nonzero value yet; reported anyway so this table's shape doesn't need to change if that ever does. |
| `system.audit_verify` | `lines, legacy_count, chained_count, signed_count, signing_started, signatures_checked, verified, first_bad_line, problem` | A single status fact — always exactly one row for an admin, same "always one descriptive row" shape as `system.tls`. `lines=0`/`verified=false` with the rest blank/zero when no audit log is attached (embedded/CLI use) *or* when the file could not even be opened (a real operational fault, e.g. removed out from under a running server) — the two are distinguished only by `problem` being non-empty in the second case, since both otherwise report the same zero-line shape. Sourced from `security.TailEvents` re-reading the live file **on every query** — nothing here is cached, since the whole point is to reflect what is actually durable on disk right now, not a startup snapshot. `signatures_checked` reflects whether `nextsqld` was started with `--audit-signing-keyset` (if so, every signature in the signed segment is cryptographically checked, not just the hash chain). |
| `system.audit_log` | `seq, event_time, actor, action_name, object, outcome, remote, identity_source, signed` | The most recent audit records, oldest-first-in-storage but capped server-side at 200 rows per query (`executor.systemAuditLogTailCap`) regardless of how large the audit file has grown on disk — a bounded ring buffer (`security.TailEvents`), not an unbounded read, so cost never scales with file size the way `system.audit_verify`'s underlying chain check still does. List-shaped like `system.key_versions`/`system.config`: **zero rows** (not a placeholder row) when no audit log is attached. **Deliberately includes a record even when `system.audit_verify` reports the chain broken** — an operator investigating a detected problem needs to see the suspect entry, not have it silently hidden (`security.TailEvents`'s own doc comment). Every field already passed through `security.Redact`/`prepareAuditEvent` at write time — "never put passwords, keys, tokens, or secrets" is `Event`'s own contract, not something this table adds. `remote` (a client connection address, not server/cluster topology) is deliberately **not** redacted here, unlike `system.config`'s `listen_addr`/`raft_bind`/`raft_join`: `system.sessions` already exposes the identical kind of value in its own `remote` column unredacted, and source-IP forensics is core audit-log value. `signed` is `true` when the record carries a signature (`sig`/`key_id` both present) — the raw signature bytes and key id themselves are not exposed, only this boolean. Two columns are named off their most natural spelling: `event_time` (not `time` — the `TIME` data type keyword) and `action_name` (not `action` — `FOREIGN KEY`'s `ON DELETE`/`UPDATE {CASCADE\|RESTRICT\|NO ACTION}` clause), both reserved words in this dialect with no quoted-identifier escape, the same pitfall `system.config`'s `name` (not `key`) column already hit once. |

## Deployment configuration (Manager M8)

| Table | Columns | Notes |
|---|---|---|
| `system.config` | `name, value, file_value, restart_required` | One row per setting the running process's `config.Config` or the node's on-disk `nextsql.conf` sets away from its default (`config.DiffState`, which reuses `Config.Marshal`'s exact field list and zero-value-omission rule). `value` is the running value; `file_value` is what `nextsql.conf` currently says; `restart_required` (`"yes"`/`"no"`) is set when they differ — either because `SET CONFIG` persisted a change not yet applied, or because a startup flag overrode the file. Admin-only; list-shaped like `system.key_versions`, so it returns **zero rows** (not a placeholder row) for embedded/CLI use with no process-level `config.Config`. **Every network-address-shaped value is redacted** in both `value` and `file_value` to `[redacted]` (`listen_addr`, `raft_bind`, `raft_join`, `auth_broker_listen`) — same "never expose a network address over SQL" convention as `system.replication.leader_addr` — but `restart_required` is still computed from the *unredacted* values, so it stays correct for those keys. Nothing else is redacted: `Config` itself never holds key material or passwords (only file *paths* to them). The column is named `name`, not `key` (`KEY` is a reserved word — `PRIMARY KEY`/`FOREIGN KEY` — with no quoted-identifier escape). The write side is the `SET CONFIG` statement (`docs/sql.md`) — cluster `ADMIN`, persist-only to `nextsql.conf`, effective on restart; it backs the NextSQL Manager's Configuration editor (`docs/design-manager.md` M8). |

## Diagnostics (Manager M9)

| Table | Columns | Notes |
|---|---|---|
| `system.metrics` | `category, name, value, unit` | One row per counter/gauge in the process-wide metrics registry (`internal/metrics.Snapshot`), sourced from whatever `nextsqld` wired via `executor.DB.SetMetricsSource` (normally `metrics.Default()` — the same registry the crypto/storage/replication hooks write to; `nextsqld` also routes the connected database's own query/txn counters into it via `SetMetrics`, so a single snapshot is internally coherent — `queries_per_second`/`commits_per_second`/`crypto_time_pct` all share one uptime base). Admin-only; list-shaped like `system.config`, so it returns **zero rows** (not a placeholder row) for embedded/CLI use with no process-level registry attached. `value` is always a plain decimal string; `unit` is a rendering hint — `count`, `bytes`, `nanoseconds`, `ratio_pct`, or `per_second`. `category` groups related metrics for display (`throughput`, `latency`, `encryption`, `storage`, `replication`, `constraints`, `maintenance`, `cdc`, `runtime`); it is **not** named `group` (a reserved word — `GROUP BY` — with no quoted-identifier escape, the same pitfall `system.config`'s `name`-not-`key` and `system.audit_log`'s `event_time`/`action_name` already hit). Nothing is redacted: the registry holds only counters and process resource stats — never addresses, key material, or tenant data. Same hosting-mode wiring-scope caveat as `system.tls`/`system.config`: under M2 multi-database hosting a `dbMgr`-opened database keeps its own registry and `system.metrics` reports "not attached" for it. |
| `system.server_log` | `seq, event_time, level, message, attributes` | A bounded, in-memory tail of the running process's own structured log — the newest `logging.DefaultRingCapacity` (500) records, capped again at 200 per query (`executor.systemServerLogTailCap`). Sourced from a `logging.Ring` that wraps the stderr JSON handler (`logging.NewWithRing`), wired by `nextsqld` via `executor.DB.SetServerLogSource`; re-read on every query so the tail is always current. Admin-only; list-shaped, **zero rows** when no ring is attached (embedded/CLI use with a bare `logging.New` logger). Memory cost is fixed regardless of process lifetime — this is a diagnostic tail, **not** a durable log store (the real log is still stderr / the service journal). `attributes` is the record's remaining key/value pairs rendered `k=v k=v`. `event_time` (not `time` — reserved). Unlike `system.config`, addresses are **not** redacted here: a log line is freeform text and an admin diagnosing a connectivity fault needs to see a listen address or an unreachable peer — the same "privileged reader + operational value" reasoning that keeps `system.audit_log.remote` unredacted; the table never holds anything the process didn't already print to its own stderr, and the "never log keys/passwords/tokens" contract is the logger's callers' (`logging.New`'s doc comment). Same legacy/non-hosted `db` wiring-scope caveat as `system.metrics`. |

## Backups (Manager M5)

| Table | Columns | Notes |
|---|---|---|
| `system.backups` | `name, created_at, database_id, checkpoint_lsn, durable_lsn` | One row per verified backup in the node's configured backup directory (config key `backup_dir`) — every immediate subdirectory with a valid backup header (`backup.ListBackups`), oldest first. Gated on the `BACKUP` privilege (`GRANT BACKUP ON DATABASE …`) or cluster `ADMIN` — a caller without either sees zero rows, not an error. **Zero rows when no `backup_dir` is configured** (embedded/CLI use, or the key unset), same "empty means not applicable" convention as `system.config`. `name` is the subdirectory name only — the write/verify statements (`BACKUP DATABASE` / `VERIFY BACKUP 'name'`, `docs/sql.md`) take that name, never a filesystem path. `database_id` is the backed-up database's identity (`Header.Identity.DatabaseString()`), useful for spotting a backup taken from a different database than the one now running here. Restore/PITR is offline-only (`nextsql restore`) — a running server cannot restore into itself. |

## Workload governance (Phase 27)

| Table | Columns | Notes |
|---|---|---|
| `system.resource_groups` | `name, owner, max_concurrency, memory_bytes, workers, priority` | One row per `CREATE RESOURCE GROUP` descriptor (catalog-persisted, WAL-recovered). Admin-only, same convention as the security administration tables above, since `RESOURCE GROUP` DDL itself is gated on cluster `ADMIN`. Zero in any numeric column means that option is unset/unbounded. **Enforced since the Phase 27 seventh increment**: a session joins with `SET RESOURCE GROUP name` (requires `USAGE`, granted via `GRANT USAGE ON RESOURCE GROUP name TO grantee`; cluster `ADMIN` bypasses) and leaves with `RESET RESOURCE GROUP`. A non-zero `MAX_CONCURRENCY` adds a second admission gate strictly on top of the process-wide `scheduler.Admission` (never a substitute for it); non-zero `WORKERS`/`MEMORY` override the session's per-query `scheduler.Limits` while assigned. `PRIORITY` is still stored but not enforced, and there is no `system.sessions` column yet showing a live session's current assignment. See `docs/sql.md` "RESOURCE GROUP" and `system.capabilities` row `resource_groups` (status `experimental`). |

## Multi-database hosting (cross-cutting track)

These reflect the deployment registry (`internal/hosting.Registry.Manifest`)
read-only. **Admin-only** and **empty (never an error) on a legacy/non-hosted
deployment** — no `hosting.Registry` wired — exactly like the security
administration tables. See `docs/design-multidatabase-dbaas.md`.

| Table | Columns | Notes |
|---|---|---|
| `system.realms` | `realm_id, name, state, database_count, storage_cap_bytes, realm_root_delegated` | One row per registered realm. `storage_cap_bytes` is `0` for no realm-level cap; `realm_root_delegated` is true when a realm-root delegation secret hash is set (§10.1). |
| `system.databases` | `realm_id, realm_name, database_id, name, state, layout, storage_cap_bytes` | One row per registered database across every realm. `layout` is `legacy_default` or `managed`. |
| `system.quotas` | `scope, realm_name, database_name, state, cap_bytes, effective_cap_bytes, usage_known, used_bytes, pct_of_cap, over_cap` | Advisory surfacing of the storage caps (§10.1). One row per realm (`scope='realm'`, empty `database_name`) and per database (`scope='database'`). `cap_bytes` is that scope's own configured cap; `effective_cap_bytes` is `EffectiveStorageCapBytes(realmCap, dbCap)` for a database row, the realm cap for a realm row (`0` = uncapped). The usage columns (`used_bytes`, `pct_of_cap` as an integer percent, `over_cap`) are populated **only for the row matching the session's own connected realm+database** — `usage_known` flags which one — because no other database's engine is reachable from one connection. `used_bytes` is the data-file logical high-water, the quantity the cap is enforced against. Never a hard limit: the authoritative over-cap signal is the write-path `nerr.Exhausted` rejection. `system.capabilities` row `quotas_view`. |

## Design notes

- **Ephemeral by design.** The live tables above are a snapshot of this
  process's in-memory state at query time, not a durable record. A server
  restart clears them; a multi-node deployment has one `system.sessions`
  (etc.) per node.
- **Cross-goroutine safety.** A session's query/transaction state is read
  from other sessions' goroutines while introspecting, so it is published
  through dedicated mutex-guarded snapshot fields (`Session.CurrentQuery`,
  `Session.TxnSnapshot`) rather than reading the session's live execution
  state directly — the same discipline applied to the CDC subscription LSN
  (`DB.CDCSubscriptions`, an `atomic.Uint64` updated only by the subscribing
  session's own goroutine).
- **RBAC.** Every live table's admin check is `s.acl == nil || s.isAdmin()`
  — an ACL-less (single-user/embedded) deployment sees everything, matching
  the rest of `system.*`.

## SHOW aliases

The following convenience statements are thin aliases over the same
permission-filtered `system.*` sources:

| Statement | Canonical source |
|---|---|
| `SHOW DATABASES` | the `database` projection of `system.storage` |
| `SHOW TABLES` | `system.tables` |
| `SHOW INDEXES` | `system.indexes` |
| `SHOW CONNECTIONS` | `system.sessions` |
| `SHOW QUERIES` | `system.active_queries` |
| `SHOW TRANSACTIONS` | `system.transactions` |
| `SHOW LOCKS` | `system.locks` |
| `SHOW CLUSTER` | `system.replication` |
| `SHOW STORAGE` | `system.storage` |

Each alias has the same columns and rows as `SELECT *` from its source,
except `SHOW DATABASES`, which returns only the `database` column. The current
single-engine server therefore reports its one served database; selectable
multi-database hosting remains a separate open track. Aliases accept no
`WHERE`, `ORDER BY`, `LIMIT`, or other clauses. Query the canonical view
directly when filtering or pagination is needed. The existing bounded `SHOW
TASKS [AFTER id] [LIMIT n]` task command is unchanged.

Because the parser lowers these aliases to system-table reads, they enforce
the same `CONNECT` requirement, per-row RBAC, and redaction rules. No separate
diagnostic data source exists.

## P26 implementation audit (2026-09-02)

| Surface | Designed | Implemented | Tested | Production-gated | Evidence / remaining work |
|---|---|---|---|---|---|
| Virtual schema core and stable columns | yes | yes | yes | no | `internal/system/schema.go`, `TestSystemCapabilities`, `TestSystemTablesAndColumns`; P26 exit gate remains open |
| Catalog/storage/replication/workflow/task/partition/stat rows | yes | yes | yes | no | executor system tests, including live `system.tasks` scan and RBAC |
| Live sessions/queries/transactions/CDC rows | yes | yes | yes | no | node-local synchronized registries; live and RBAC tests in `internal/executor/system_test.go` |
| Live lock rows | yes | yes | yes | no | `LockManager.Snapshot`, lock attribution/RBAC tests, documented best-effort table tag |
| Nine `SHOW` aliases | yes | yes | yes | no | parser mapping/rejection tests plus executor source-equivalence and RBAC tests |
| Logical database-name redaction | yes | yes | yes | no | `system.storage` / `SHOW DATABASES` publish configured logical name, never `Engine.Path()` |
| Capability registry accuracy | yes | yes | yes | yes | every P23/P25 surface (`mtls`, `token_credentials`, `oidc_broker`, `audit_chain`, `storage_caps`, `vector_ivf`, `vector_ivfpq`, `vector_sparse`, `quantized_vector_index`) now has its own discoverable row, not just a passing mention inside another row's description; `fulltext`'s description was also corrected to include WEIGHT/FACET (landed 2026-08-31 but missing from the row text). Pinned in `TestSystemCapabilities`. |
| Studio/Manager official-interface sufficiency | yes | yes | yes | yes | `system.users`/`system.roles`/`system.grants` close the one real gap found (Manager MVP's "Users/roles/privileges administration" and "Security dashboard" bullets had no official read source); everything else in the Manager MVP list (server/cluster/backup/performance/maintenance status) already had one. See "P26 exit gate closure" below. |
| RBAC coverage breadth | yes | yes | yes | yes | `system.table_stats`/`system.index_stats`/`system.partitions` (share `canSeeTable` with `system.tables`/`columns`/`indexes`) and `system.workflows` (`canSeeWorkflow`) now each have a dedicated pinning test (`TestSystemCatalogRBACRemainingViews`), not just their better-known siblings. |
| Realm/database visibility | yes | yes | yes | yes | Structural, not filter-based: `protocol.Server` holds exactly one `*executor.DB`, and `cmd/nextsqld`'s `openHostedDefault` opens exactly one realm/database pair per process via `hosting.Registry.Default()`. No code path today lets a session observe another realm's or database's `system.*` rows, because no process ever has more than one open at once. This is a hard prerequisite to revisit if/when a multi-database-per-process `DatabaseManager` (`docs/design-multidatabase-dbaas.md` §9) ships — that is out of scope for P26 and stays a documented future gap, not a current one. |

`Production-gated = no` above means P26 itself is still open; it does not
revoke the production-gated status of earlier phases whose state these views
report.

## P26 exit gate closure (2026-09-02)

All three exit-gate items are closed:

- **Studio/Manager can operate from official system interfaces without
  reading internal files.** Audited every bullet in the Phase 28 "NextSQL
  Manager MVP" navigation list (`TODO.md`) against the current `system.*`
  surface: server/cluster status → `system.replication`/`system.raft`/
  `system.replica_health`; databases → `system.storage`/`system.tables`;
  connections → `system.sessions`; performance/active queries →
  `system.active_queries`; maintenance/index/storage state →
  `system.index_stats`/`system.table_stats`/`system.storage`. The one gap —
  users/roles/privileges administration and the security dashboard — is
  closed by `system.users`/`system.roles`/`system.grants` (see "Security
  administration tables" above). Configuration-viewer and audit-viewer data
  sources, and OS-level start/stop/restart/drain, are explicitly out of scope
  for P26 (drain/restart is Phase 27 workload governance; a redacted
  configuration snapshot table is a documented follow-on, not a blocker,
  since no Manager exists yet to consume it and no MVP bullet requires
  editing config through SQL).
- **System schema obeys RBAC and realm/database visibility rules.** Every
  `system.*` table now has an explicit RBAC test (see "RBAC coverage
  breadth" above), and realm/database isolation is structurally guaranteed
  by the current single-database-per-process architecture, not by a filter
  that could regress (see "Realm/database visibility" above).
- **Capability registry is authoritative for version-aware clients.** Every
  major supported surface has its own row (see "Capability registry
  accuracy" above); `system_schema_v2` continues to gate the column-contract
  version separately from feature support.

Documented follow-ons (not gate items): a redacted server-configuration
snapshot table for the eventual Manager configuration viewer; an
audit-log-tail/query surface for the eventual Manager audit viewer;
selectable multi-database hosting (tracked separately, see the cross-cutting
hosting track in `TODO.md`).
