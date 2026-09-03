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
(currently 2) as the supported capability name `system_schema_v2`, so machine
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
what the P26 exit gate requires it never do. All three reflect the durable,
process-shared `auth.Store`/`security.ACL` state (not per-session, unlike the
live tables above) and are **admin-only**: a non-admin gets zero rows, never
an error, matching the rest of `system.*`'s "row-filter, never fail, on RBAC"
convention. `s.acl == nil` (no ACL configured — single-user/embedded use)
is treated as admin, same as every other live table.

| Table | Columns | Notes |
|---|---|---|
| `system.users` | `name, password_algo` | One row per stored user. `password_algo` is `argon2id` or `pbkdf2` (useful for spotting accounts still pending transparent rehash — see `docs/security.md` "Password hashing"). Never exposes the password hash or salt. |
| `system.roles` | `role, members` | One row per created role. `members` is a comma-joined, sorted list of the users or nested roles granted that role (empty string for a role with no members). |
| `system.grants` | `grantee, privilege, scope, object` | One row per persisted grant, to a user or a role. `privilege` and `scope` are rendered in the same spelling the `GRANT`/`REVOKE` statement grammar accepts (`security.Privilege.String()` / `security.ScopeKind.String()`, the exact inverse of `ParsePrivilege`/`ParseScope`). |

## Workload governance (Phase 27)

| Table | Columns | Notes |
|---|---|---|
| `system.resource_groups` | `name, owner, max_concurrency, memory_bytes, workers, priority` | One row per `CREATE RESOURCE GROUP` descriptor (catalog-persisted, WAL-recovered). Admin-only, same convention as the security administration tables above, since `RESOURCE GROUP` DDL itself is gated on cluster `ADMIN`. Zero in any numeric column means that option is unset/unbounded. **Enforced since the Phase 27 seventh increment**: a session joins with `SET RESOURCE GROUP name` (requires `USAGE`, granted via `GRANT USAGE ON RESOURCE GROUP name TO grantee`; cluster `ADMIN` bypasses) and leaves with `RESET RESOURCE GROUP`. A non-zero `MAX_CONCURRENCY` adds a second admission gate strictly on top of the process-wide `scheduler.Admission` (never a substitute for it); non-zero `WORKERS`/`MEMORY` override the session's per-query `scheduler.Limits` while assigned. `PRIORITY` is still stored but not enforced, and there is no `system.sessions` column yet showing a live session's current assignment. See `docs/sql.md` "RESOURCE GROUP" and `system.capabilities` row `resource_groups` (status `experimental`). |

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
