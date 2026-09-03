# Server configuration

```text
nextsqld --data-dir DIR --key-file FILE [--instance-key-file FILE]
         [--listen 127.0.0.1:7210] [--config FILE]
         [--env-file PATH | --no-env]
         [--tls-cert FILE --tls-key FILE [--tls-client-ca FILE [--tls-client-crl FILE]]]
         [--auth-broker-listen ADDR [--auth-broker-config FILE]]
         [--require-client-key]
         [--user NAME --password-file FILE]
         [--auth-file FILE] [--audit-file FILE]
         [--buffer-pages N] [--log-level debug|info|warn|error]
         [--wal-archive DIR]
         [--node-id ID --raft-bind ADDR --raft-join id=addr,... [--raft-bootstrap]]
```

`--data-dir` is required (flag or config). `--key-file` is required unless `--require-client-key` is set. For a deployment initialized with `nextsql.instance`, `--instance-key-file` defaults to `KEY-FILE.instance`; client-key mode must set it explicitly.

The hosting fields can also come from process environment, `.env.local`,
`.env`, or `--env-file`: `NEXTSQL_DATA_DIR`, `NEXTSQL_KEY_FILE`,
`NEXTSQL_INSTANCE_KEY_FILE`, `NEXTSQL_BUFFER_PAGES`, `NEXTSQL_ADDR` (listen),
`NEXTSQL_SERVER_USER`, and `NEXTSQL_SERVER_PASSWORD_FILE` (preferred) or
`NEXTSQL_SERVER_PASS`. Priority is flags > process env > dotenv > `--config` >
defaults. Server credentials are separate from client `NEXTSQL_DATABASE_USER` /
`NEXTSQL_DATABASE_PASS*`. Values for the key variables are file paths, not key
bytes; use a protected host-only env file.

## Config file (`--config`)

Simple `key=value`. Comments start with `#`. Unknown keys are rejected. Command-line flags override the file.

```text
data_dir=/var/lib/nextsql
key_file=/etc/nextsql/root.key
instance_key_file=/etc/nextsql/root.key.instance
listen_addr=127.0.0.1:7210
log_level=info
buffer_pages=1024
max_total_buffer_pages=0
max_open_databases=8
task_workers=0
tls_cert=/etc/nextsql/server.crt
tls_key=/etc/nextsql/server.key
tls_client_ca=
tls_client_crl=
token_verify_keyset=
token_revocations=
token_audience=
token_identity_source_hint=
auth_broker_config=
auth_broker_listen=
require_client_key=false
audit_file=
wal_archive=/var/lib/nextsql-wal
wal_retention_ms=0
disk_watermark_check_ms=0
disk_watermark_warn_percent=85
disk_watermark_reject_percent=95
replica_lag_check_ms=0
replica_lag_warn_entries=1000
max_inflight_queries=32
max_query_queue=128
query_queue_wait_ms=5000
max_result_rows=1000000
max_connections=128
max_connections_per_user=0
max_connections_per_database=0
max_connections_per_realm=0
idle_timeout_ms=60000
statement_timeout_ms=30000
transaction_timeout_ms=0
lock_timeout_ms=0
idle_transaction_timeout_ms=0
shutdown_drain_ms=30000
node_id=
raft_bind=
raft_join=
raft_bootstrap=false
```

## Admission and budgets

Every `Exec` takes an in-flight slot. If all slots are busy, the query queues. If the queue is full or the wait exceeds `query_queue_wait_ms`, the server returns `unavailable` instead of growing without bound.

Defaults: 32 in-flight, 128 queued, 5 s wait.

Per-query budgets (defaults): 64 MiB memory, 256 MiB spill, 1 GiB I/O, 30 s, 1 000 000 result rows / 64 MiB result bytes. Exceeding a budget fails with `exhausted`. Worker goroutines are bounded (`min(GOMAXPROCS, 8)` per query through a process pool).

Wire defaults: 1 MiB packet, 1 MiB SQL, 256 parameters, 64 prepared statements per session, 128 concurrent sessions, 60 s idle. `max_connections` and `idle_timeout_ms` override the 128-session cap and 60 s idle deadline; `max_connections_per_user` (0 = unlimited) additionally caps concurrent authenticated connections held by one user name — an over-limit connection is rejected after authentication with `exhausted`, before a session is created. `max_connections_per_database`/`max_connections_per_realm` (both 0 = unlimited; P27's own last exit-gate item) work the same way, keyed on the resolved `(realm, database)` pair or realm name instead of the user name — a database's own counter and its realm's counter are independent, so exhausting one database's limit never blocks a connection to a different database in the same realm, while every database in a realm shares that realm's counter. All five are process-wide and node-local (not synchronized across a cluster).

`max_open_databases` (default 8) bounds how many distinct databases this process will ever have open at once when hosting more than the primary one (multi-database hosting; see `docs/design-multidatabase-dbaas.md`) — an idle database is evicted (its buffer pool and file handles closed) to make room for a newly requested one, never rejected outright while under this limit. `max_total_buffer_pages` (default 0 = unbounded) is a second, independent gate on the same multi-database scenario: each open database's buffer pool commits `buffer_pages` frames in full at open (there is no dynamic per-page grant to shrink later), so with more than one database open at once the *count* limit above says nothing about total memory. When set, every database this process opens — the primary and every secondary — reserves against this one shared ceiling; an open that would exceed it fails `exhausted` rather than growing process memory without bound, and a database's reservation is released the moment it closes (including M2-3b-1 idle eviction), making room for another. Must be 0 or `>= buffer_pages`, since otherwise even the primary database could never open. A single-database deployment (no hosting registry) is unaffected by default.

`task_workers` (default 0 = the same built-in default every individual database used before centralizing) sizes one shared worker pool that every open database's scheduled-task execution (`RUN WORKFLOW` fired by a `SCHEDULE`, or a queued `TASK`) submits claimed work to. Each database still polls its own due tasks on its own schedule independently; only the goroutines that actually execute claimed work are shared, so task-execution goroutine count no longer scales with the number of open databases the way it used to (each database spawning its own separate worker set).

`statement_timeout_ms` (default 30000) overrides the per-statement time budget above — it is the same `scheduler.Limits.Time` bound, checked throughout execution (scans, index lookups, ANALYZE, vector/full-text search, DDL, workflows/triggers), not just admission. `transaction_timeout_ms` (default 0 = unbounded) bounds a transaction's total open lifetime; once exceeded, the next statement inside it — even `COMMIT` — force-aborts and fails `exhausted`, but the connection itself stays usable afterward. `lock_timeout_ms` (default 0 = block indefinitely) bounds a contended, non-deadlocking key/range lock wait; only deadlock *cycles* are caught without it. `lock_timeout_ms` is process-wide (the shared lock table has no per-connection identity to key off); the other two are per-node like the connection limits above. `idle_transaction_timeout_ms` (default 0 = no distinct bound) bounds how long a connection may sit with an open transaction and no traffic before it is force-timed-out — unlike `transaction_timeout_ms`, which is only checked lazily when the next statement arrives, this is enforced by the connection's own socket read deadline (like `idle_timeout_ms`, but its own, typically tighter, bound while a transaction is open), so it reclaims the transaction even if the client never sends another statement. Closing a connection with an open transaction, by this timeout or any other path (including a forced drain close), now always rolls that transaction back first. See `docs/ops.md` "Statement, transaction, lock, and idle-transaction timeouts".

## Graceful shutdown

On SIGINT/SIGTERM, `nextsqld` stops accepting new connections and closes each existing one as soon as it becomes idle (no in-flight statement, no open transaction) instead of force-aborting it. `shutdown_drain_ms` (default 30000) bounds how long it waits for a busy connection before force-closing it; `0` disables waiting (immediate hard close). See `docs/ops.md` "Graceful shutdown (drain)".

## WAL archival and retention

`wal_archive` (default unset) enables the PITR archiver: recycled WAL segments are copied, encrypted, to this directory before local deletion. `wal_retention_ms` (default 0 = unmanaged) additionally makes `nextsqld` keep `DB.SetWALRetentionHorizon` current with a time-based policy — the newest archived segment's LSN at or before `now - wal_retention_ms`, recomputed roughly every 1/24th of that window (clamped to 1–60 minutes). It requires `wal_archive` to be set too and is a no-op otherwise. Neither key prunes anything by itself: pruning still only happens during a `MAINTAIN DATABASE` you run or schedule yourself. See `docs/wal.md` "Retention".

## Disk watermarks

`disk_watermark_check_ms` (default 0 = disabled) makes `nextsqld` periodically statfs the volume holding `data_dir` and act on used-space percentage: `disk_watermark_warn_percent` (default 85) logs a warning; `disk_watermark_reject_percent` (default 95) additionally rejects new mutating statements with `unavailable`, using hysteresis — once tripped, only clearing again once usage drops back below `disk_watermark_warn_percent`. `disk_watermark_warn_percent` must be less than `disk_watermark_reject_percent`. This is a last-resort backstop, independent of `CLUSTER MAINTENANCE ENABLE`/`DISABLE`; see `docs/ops.md` "Disk watermarks".

## Replica-lag monitoring

`replica_lag_check_ms` (default 0 = disabled) makes `nextsqld` periodically read this node's own `system.replica_health.apply_backlog` and log a warning (edge-triggered, plus a recovery line once it drops back down) once it reaches `replica_lag_warn_entries` (default 1000). Alerting only — no write is ever rejected because of it, unlike the disk watermark; `Cluster.FollowerReadHealthy` already keeps a too-far-behind follower out of bounded-staleness read routing regardless of whether this is enabled. See `docs/ha.md` "Replica-lag monitoring".

Structured logs never contain passwords, keys, tokens, or secrets.

The separate `nextsql-auth-broker` config supports
`access_token_audience=RESOURCE` inside an `[idp "name"]` section. Setting it
enables OAuth2 client-credentials exchange only for asymmetric JWT access
tokens with that exact resource audience and the section's exact `client_id`
binding. Opaque-token introspection is not implemented.

`auth_broker_listen` enables the embedded broker for a single-node/non-HA
server on a separate listener. `auth_broker_config` selects the same broker
configuration used by `nextsql-auth-broker`; it defaults to
`DATA-DIR/nextsql-auth-broker.conf`. Embedded mode requires
`token_verify_keyset`, rejects Raft, requires broker TLS off loopback, validates
issuer/verifier key compatibility at startup and reload, and intersects mapped
roles with the live native ACL before minting.
