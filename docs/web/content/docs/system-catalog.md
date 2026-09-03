# System catalog

`system.*` is a read-only virtual schema for introspection: ordinary
`SELECT`s computed from live server state, not stored rows.

```sql
SELECT * FROM system.tables;
SELECT name, remote FROM system.sessions WHERE state = 'active';
SELECT sql FROM system.active_queries;
```

`WHERE`, `ORDER BY`, `LIMIT`, `DISTINCT`, and parameters are supported;
`JOIN` and `GROUP BY` are not. Every session needs `CONNECT` on the database;
some tables layer RBAC filtering on top.

Capability consumers can query the supported `system_schema_v2` row in
`system.capabilities` to identify the current stable system-column contract.
`system_show_aliases` advertises the convenience syntax.

Catalog/storage tables (always visible, or filtered to tables you can
`SELECT`): `capabilities`, `tables`, `columns`, `indexes`, `table_stats`,
`index_stats`, `partitions`, `storage`, `replication` (alias `raft`),
`replica_health`, `workflows`, `tasks`.

`system.storage.database` is the configured logical database name (`default`
for unnamed embedded use), never the engine's filesystem path.

Live, node-local, in-memory tables (a non-admin sees only their own rows):

- `sessions` — one row per connected session (`session_id, user, remote,
  state`); `state` is `active` while executing a statement, else `idle`.
- `active_queries` — one row per session currently executing a statement
  (`query_id, user, sql, state`), including your own running query.
- `transactions` — one row per session with an open transaction (`txn_id,
  user, isolation, state`).
- `change_streams` — one row per open `SUBSCRIBE` on this node (`table_name,
  lsn, state`), visible to sessions that can see the underlying table.
- `locks` — one row per currently held key or range lock (`lock_id,
  table_name, mode, granted`); `mode` is `shared`/`exclusive`, `granted` is
  always `true` (waiting requests aren't shown). `table_name` is best-effort
  (see `docs/system-catalog.md`).

These tables reflect this node's process memory, not the cluster: they clear
on restart and are not replicated.

Security administration tables (admin-only — a non-admin sees zero rows):

- `users` — one row per stored user (`name, password_algo`); `password_algo`
  is `argon2id` or `pbkdf2`. Never exposes the hash or salt.
- `roles` — one row per created role (`role, members`); `members` is a
  comma-joined, sorted list of the users/roles granted that role.
- `grants` — one row per persisted grant (`grantee, privilege, scope,
  object`); `privilege`/`scope` are rendered in `GRANT`/`REVOKE` grammar
  spelling.

Workload governance (Phase 27, admin-only): `resource_groups` — one row per
`CREATE RESOURCE GROUP` descriptor (`name, owner, max_concurrency,
memory_bytes, workers, priority`; zero = unset/unbounded). Catalog-persisted
and durable, and **enforced**: `SET RESOURCE GROUP name` (needs `USAGE`,
granted via `GRANT USAGE ON RESOURCE GROUP name TO grantee`) / `RESET
RESOURCE GROUP` assign/clear a session's group. A non-zero `MAX_CONCURRENCY`
adds a second admission gate on top of — never instead of — the process-wide
one; non-zero `WORKERS`/`MEMORY` override the session's per-query limits.
`PRIORITY` is stored but not yet enforced. See `docs/sql.md` "RESOURCE
GROUP".

Convenience commands use the same RBAC-filtered sources:

- `SHOW DATABASES` → the `database` column of `system.storage`
- `SHOW TABLES` → `system.tables`
- `SHOW INDEXES` → `system.indexes`
- `SHOW CONNECTIONS` → `system.sessions`
- `SHOW QUERIES` → `system.active_queries`
- `SHOW TRANSACTIONS` → `system.transactions`
- `SHOW LOCKS` → `system.locks`
- `SHOW CLUSTER` → `system.replication`
- `SHOW STORAGE` → `system.storage`

Use a direct `system.*` query for `WHERE`, `ORDER BY`, or `LIMIT`; the aliases
accept no clauses. `SHOW TASKS [AFTER id] [LIMIT n]` remains the separate
bounded task-runtime command.
