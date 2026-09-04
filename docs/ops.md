# Production operations (Phase 14)

Backup, restore, PITR, and logical export/import are documented in
`docs/backup.md` and `docs/export.md`. This note covers the remaining
single-node ops surface: format compatibility, metrics, diagnostics,
admission control, and `nextsql-bench`.

Official numbers keep encryption, WAL, fsync, checksums, MVCC, and
authentication enabled. Hardware, filesystem, row width, query, indexes,
cache condition, encryption, durability, and concurrency are part of the
measurement, not optional footnotes.

## Upgrade / format compatibility

Every persisted family has a version and a compatibility window in
`internal/upgrade/compat.Catalog`. This binary reads only versions in
`[MinReadable, MaxReadable]`. There is no silent rewrite of an unknown
version. A newer file fails closed; an older-than-min file fails closed.
The superblock (`internal/storage/file.decodeSuperblock`) and catalog
table decoder (`catalog.DecodeTable`) enforce this same catalog directly
(`compat.Check`), so what `nextsql diagnose` prints below is guaranteed to
match what actually opens — not a separately maintained number that could
drift. The error names the actual and required version numbers.

Most families are **v1**; the catalog descriptor (`NSCT`) is at **v11**
(readable 1..10). Opening a data directory this binary can read is the
supported path. A future format bump must either widen
`MaxReadable` or add an explicit rewrite increment — not an in-place
guess. See `docs/storage-format.md` "Format and catalog migration
strategy" for what's safe to migrate online (catalog-record changes, via
the existing multi-version-decode pattern) versus what requires the
offline dump/reload path (physical page/superblock layout changes).

```text
nextsql diagnose --data-dir DIR
nextsql status --local --data-dir DIR --key-file FILE
```

`diagnose` reads plaintext headers only (superblock, WAL control, UNDO
control, isolated-page sidecar). It does not need the root unlock key and never prints secrets.
`status --local` does the same check, then opens the database with `--key-file`
and prints LSNs, table count, isolated pages, counters, and admission stats.
Default `nextsql status` (no `--local`) is a server ping: it dials `nextsqld`
and prints `mode server`.

`--key-file` is the external root unlock key. It is never stored in the
data directory. Keys are never accepted in connection URLs.

Embedded diagnostics can call `executor.DB.OrphanPages()`. The read-only check
serializes with transactions and Raft apply, then compares every allocated page
against the primary/catalog tree, all catalog-owned detached trees, durable free
IDs, and freelist metadata. It returns only page IDs that are allocated but
unreachable; it never automatically frees a suspected orphan.

## Metrics

`internal/metrics` is a process/registry of counters and a latency ring
(last 2048 query samples):

- queries, errors, commits, rollbacks, admitted, rejected, canceled, rows
- p50 / p95 / p99 / p99.9
- page AEAD seal/open time and bytes (encryption overhead)
- WAL bytes flushed this process
- isolated / repaired page counts (detect → isolate → recover)
- `fk_checks`, `fk_violations`, `fk_cascade_rows`, `fk_cascade_reject` (no keys or payloads)
- index rebuild attempts, failures, rows scanned, entries produced, and total duration
- maintenance runs, failures, physical tombstones removed, and total duration
- replication orphans: a transaction that committed locally after an
  ambiguous/in-doubt `Replicate` failure — the one residual case that still
  commits locally without confirmed quorum (see "Rolling upgrade" below for
  the full writeup); 0 in normal operation, a growing count is the
  operator-visible signal of that narrow, structurally-unavoidable case
- disk total/free bytes and cumulative warn/reject counts (see "Disk
  watermarks" above)
- this node's replication apply backlog and cumulative lag-warn count (see
  "Replica-lag monitoring" in `docs/ha.md`)
- heap, total alloc, goroutines, CPU

Page encrypt/decrypt in `crypto.SealPage` / `OpenPage` observe the process
registry. `nextsqld` routes each database's own query/txn counters into that
same process registry (`executor.DB.SetMetrics`), so one snapshot is
internally coherent — `queries_per_second` / `commits_per_second` /
`crypto_time_pct` all share one uptime base. Metrics never contain
passwords, keys, tokens, or secrets.

The whole snapshot is queryable over SQL as **`system.metrics`** (admin-only,
`category`/`name`/`value`/`unit`), and the NextSQL Manager's Diagnostics view
renders it grouped by category alongside a bounded tail of the server's own
structured log (**`system.server_log`**). See `docs/system-catalog.md`
"Diagnostics (Manager M9)".

Blocking index rebuild progress is available from
`executor.DB.IndexRebuildProgress()`. Each active entry reports only table and
index names, phase (`building` or `committing`), rows scanned, entries produced,
and start time. The entry is removed on success or failure; cumulative outcome
and duration remain in the metrics snapshot.

`executor.DB.MaintenanceStatus()` reports whether maintenance is paused, the
active kind/scope/start time, and the last run's completion, affected count,
and failure state. A central synchronous coordinator permits one maintenance
pass per database and never starts background goroutines or queues work.
Concurrent requests fail with `unavailable`. `PauseMaintenance` prevents new
runs; `ResumeMaintenance` re-enables them. An already-active bounded pass is
allowed to finish. SQL `MAINTAIN` statements first acquire the shared query
admission slot, so maintenance cannot bypass overload rejection or queue limits.
The default per-run budget is 30 seconds of elapsed CPU-work time, 8 MiB of
buffered tombstone keys, and 500,000 conservative logical page-I/O units. A leaf
scan costs one unit; each physical delete reserves a tree-height-based path and
merge allowance before its transaction begins. These units bound engine work,
not kernel syscalls or physical-device cache misses. Checks occur during leaf scans and between physical
deletes. Exhaustion returns `exhausted` with bounded partial progress; unprocessed
tombstones remain durable for a later pass. Embedders may set positive limits
with `DB.SetMaintenanceLimits`.

## Admission control

A process-wide gate (`scheduler.Admission`) sits in front of every
`Session.ExecContext`:

1. Take an in-flight slot if one is free.
2. Otherwise queue up to `max_query_queue`.
3. If the queue is full, or the wait exceeds `query_queue_wait_ms`,
   reject with `unavailable`. Cancel of the parent context unblocks a
   waiter with `canceled`.

Defaults: 32 in-flight, 128 queued, 5 s wait. Config keys:

```text
max_inflight_queries=32
max_query_queue=128
query_queue_wait_ms=5000
max_result_rows=1000000
```

Per-query budgets still apply: workers, memory, disk spill, I/O,
execution time, result rows, and result bytes. Exceeding a budget
cancels or fails closed (`exhausted`) instead of growing without bound.

`nextsqld` installs the gate from config after open (including
`--require-client-key` unlock). Protocol `MaxSessions` still bounds
accepted connections.

A session assigned to a resource group (`SET RESOURCE GROUP name`, see
"RESOURCE GROUP" in `docs/sql.md`) with a non-zero `MAX_CONCURRENCY` gets a
**second** admission gate on top of the one above — never instead of it. Both
must admit a query; a group can only add a tighter ceiling, never a looser
or independent one, so per-workload concurrency limits cannot bypass the
process-wide `max_inflight_queries`/`max_query_queue` limits. A group's
`WORKERS`/`MEMORY` similarly override the assigned session's per-query
budget (`WORKERS` still clamped to the process worker ceiling).

## Connection limits

Five node-local, process-wide `protocol.Limits` fields, configurable and
distinct from the admission gate above (which bounds concurrent query
*execution*, not connection count):

```text
max_connections=128
max_connections_per_user=0
max_connections_per_database=0
max_connections_per_realm=0
idle_timeout_ms=60000
```

`max_connections` (default 128) rejects a new TCP/TLS accept once the
process-wide connection count is reached — before any bytes are read, so it
never returns a wire-level error, the connection is simply closed.
`max_connections_per_user` (default 0 = unlimited) rejects a connection
*after* authentication succeeds but before a session is created, once that
user name already holds the configured number of concurrent connections;
the client sees `exhausted` ("too many connections for user"). Closing one
of the user's connections frees a slot for a subsequent connection.
`max_connections_per_database`/`max_connections_per_realm` (default 0 =
unlimited, P27's own last exit-gate item, closed once multi-database
hosting shipped selectable per-process databases) work the same way, keyed
on the resolved `(realm, database)` pair or realm name instead of the user
name — `too many connections for database`/`too many connections for
realm`. A database's counter and its realm's counter are independent:
exhausting one database's own limit never blocks a connection to a
different database in the same realm, but every database in a realm shares
that realm's own counter. On a single-database (non-hosted) deployment
these still work, just against the one pinned database/realm — a
finer-grained alternative to `max_connections` there.
`idle_timeout_ms` (default 60000) overrides the per-read socket deadline
applied between frames on every connection (Hello, Auth, and every
subsequent request); a connection that sends nothing within the window is
dropped. None of these three are synchronized across a Raft cluster — each
node enforces its own accepted-connection state.

## Statement, transaction, lock, and idle-transaction timeouts

```text
statement_timeout_ms=30000
transaction_timeout_ms=0
lock_timeout_ms=0
idle_transaction_timeout_ms=0
```

`statement_timeout_ms` overrides the per-statement `scheduler.Budget` wall-clock
bound (`scheduler.DefaultTimeout`, 30 s, when unset). This budget is checked
throughout query execution — scans, index lookups, ANALYZE, vector/full-text
search, DDL rebuilds, workflows/triggers — so an over-budget statement fails
`exhausted` rather than running unbounded; 0/unset leaves the 30 s default in
place. `transaction_timeout_ms` (default 0 = unbounded) bounds a transaction's
total open lifetime, from `BEGIN` (or the first statement of an implicit
autocommit transaction) to `COMMIT`/`ROLLBACK`: once exceeded, the *next*
statement dispatched inside it — even `COMMIT` — force-aborts the transaction
and fails `exhausted` instead of being allowed to complete; the connection
itself stays usable for a fresh transaction afterward. Unlike
`idle_timeout_ms`/the statement timeout, this has no historical non-zero
default, so upgrading never starts aborting already-long-running transactions
(e.g. bulk loads) unless an operator opts in. `lock_timeout_ms` (default 0 =
block indefinitely) bounds how long a contended, non-deadlocking key/range
lock wait blocks before failing `exhausted`; only deadlock *cycles* are
detected without this — a two-party non-cyclic wait (one transaction holding
a row another wants, with no wait-for cycle) blocks forever unless
`lock_timeout_ms` is set. It is process-wide, not per-connection (unlike the
other two): the lock table has no per-caller identity to key a limit off, so
one operator-configured bound applies to every contended wait engine-wide.
`idle_transaction_timeout_ms` (default 0 = no distinct bound) bounds how long
a connection may sit with an open transaction and no traffic between frames
before it is force-timed-out and the transaction released — distinct from
`transaction_timeout_ms` above in *how* it is enforced: `transaction_timeout_ms`
is only checked lazily when the next statement arrives, so a connection that
never sends another statement keeps the transaction (and its locks) open
indefinitely regardless of that setting; `idle_transaction_timeout_ms` is
instead enforced by the connection's own socket read deadline (the same
mechanism as `idle_timeout_ms`, just with its own, typically tighter, bound
that applies only while a transaction is open), so it actively reclaims the
transaction even if the client goes silent. 0 leaves an idle transaction
governed only by the general `idle_timeout_ms` deadline, matching pre-P27
behavior. Closing a connection this way — or any other way while a
transaction is still open, including a forced close at the drain deadline —
now always force-rolls-back that transaction first, so its locks are never
left held by a session nothing will ever resume.

None of these four are synchronized across a Raft cluster — each node
enforces its own configured bound against its own local wall-clock/lock
state.

## Graceful shutdown (drain)

```text
shutdown_drain_ms=30000
```

On SIGINT/SIGTERM, `nextsqld` stops accepting new connections immediately,
then closes each existing connection as soon as it is idle — no in-flight
statement and no open transaction — instead of force-aborting whatever is
mid-flight. A connection sitting inside an open, otherwise-idle transaction
(`BEGIN` with no `COMMIT`/`ROLLBACK` yet) counts as busy and is left open
until it finishes or the deadline arrives — and so does a connection whose
just-finished statement's response is still being written back to it, even
after the statement itself has completed (this matters most for a
`CLUSTER DRAIN` connection specifically, since its own trigger and the idle
check are otherwise synchronous). Any connection still busy once
`shutdown_drain_ms` (default 30000) elapses is force-closed, same as before
this existed. `shutdown_drain_ms=0` disables waiting for busy connections
(immediate hard close, matching pre-P27 behavior); the process still exits
once the (now instant) drain finishes.

This is `protocol.Server.Drain`, a Go-level primitive. It runs automatically
inside `nextsqld`'s own signal handling, and — see "Remote drain" below — can
also be triggered over a live connection without a restart or a signal.

## Remote drain

```text
nextsql cluster drain [--timeout-ms N] [--addr HOST:PORT] [--user NAME] [--password-file FILE]
```

Issues `CLUSTER DRAIN [WITH (TIMEOUT_MS = n)]` over a live connection to the
node at `--addr`, asking that node's own `protocol.Server` to begin the same
graceful drain described above — immediately, no signal or restart required.
`--timeout-ms` (0, the default, uses that node's configured
`shutdown_drain_ms`) is how long it waits for busy connections before
force-closing them. Requires cluster `ADMIN`. Unlike `CLUSTER TRANSFER
LEADER`, this needs no Raft cluster and is not gated on being issued against
the leader — draining is purely local to whichever node the connection
reaches, so a follower is exactly as drainable as a leader (the common case:
drain one node for maintenance without disturbing leadership elsewhere).
`nextsql cluster drain` is equivalent to `nextsql exec -c 'CLUSTER DRAIN'`
with the same connection flags. The drain itself runs in the background on
the target node, so the issuing connection gets an immediate
`drain_initiated` acknowledgment rather than blocking for the full timeout —
that connection will itself be closed once it goes idle, like any other.

## Leader transfer

```text
nextsql cluster transfer-leader [--addr HOST:PORT] [--user NAME] [--password-file FILE]
```

Issues the `CLUSTER TRANSFER LEADER` admin statement over a live connection
to a Raft-clustered deployment's current leader, asking it to hand off to
another voter (`replication.Cluster.TransferLeadership`, wrapping
`raft.Raft.LeadershipTransfer`). Requires `ADMIN ON CLUSTER`. It is a planned
handoff — unlike a crashed-leader failover, callers see no write
unavailability window — and is the tool to reach for before restarting or
taking a leader node down for maintenance so the new leader is already
serving before the old one stops. It fails `Unavailable` on a single-node
deployment (no cluster attached) and `InvalidArgument` if issued inside an
open transaction. `nextsql cluster transfer-leader` is equivalent to
`nextsql exec -c 'CLUSTER TRANSFER LEADER'` with the same connection flags;
either prints the result as the usual tab-separated `nextsql exec` output.
There is no way to target a specific destination voter yet — Raft picks the
best-caught-up one — matching the current `TransferLeadership()` library
call.

## Maintenance mode

```text
nextsql cluster maintenance enable|disable [--addr HOST:PORT] [--user NAME] [--password-file FILE]
```

Issues `CLUSTER MAINTENANCE ENABLE` / `DISABLE` over a live connection to the
node at `--addr`. While enabled, that node rejects every mutating statement —
`INSERT`/`UPSERT`/`UPDATE`/`DELETE`, every DDL statement, and `BEGIN` — with
`Unavailable`, using the same write/no-write classification `requireLeader`
already applies for leader routing (so a transaction is blocked at `BEGIN`
regardless of whether it would have only read; there is no way to know in
advance). Reads keep working — autocommit `SELECT`, `SHOW`, and `system.*`
queries are unaffected — so operators can still confirm state and drive the
maintenance work itself over the same connection. Requires cluster `ADMIN`
and cannot be issued inside a transaction.

Like remote drain and unlike leader transfer, this is purely local to
whichever node the connection reaches — not Raft-replicated — so it works
the same on a single-node deployment and needs no attached cluster. On a
Raft-clustered deployment this means enabling it on the current leader is
what actually blocks writes cluster-wide (since only the leader accepts
writes in the first place); enabling it on a follower is a no-op for write
traffic (followers already reject writes via the leader-routing gate) but
still blocks that follower's own read-write transaction attempts and reads
`system.replication.maintenance_mode = true` locally. **The flag does not
survive a leader failover** — if leadership moves during a maintenance
window (a crash, or a `CLUSTER TRANSFER LEADER` issued while maintenance is
still enabled), the new leader is not automatically in maintenance mode;
re-issue `CLUSTER MAINTENANCE ENABLE` against it. The intended sequence for
planned per-node maintenance that must not race a concurrent write is:
enable maintenance mode on the leader, perform the maintenance step (a
schema change, a `nextsql cluster drain` / restart of a follower, etc.),
then disable it — `CLUSTER TRANSFER LEADER` and `CLUSTER DRAIN` remain
usable while maintenance mode is enabled, since both are handled before the
mutating-statement classification is reached.

Current state is visible via `SHOW CLUSTER` / `SELECT maintenance_mode FROM
system.replication` on any node. Not to be confused with the unrelated
`MAINTAIN` statement or `DB.PauseMaintenance`/`ResumeMaintenance` documented
above — those pause the background dead-version cleanup scheduler, not
client query traffic. `nextsql cluster maintenance enable` is equivalent to
`nextsql exec -c 'CLUSTER MAINTENANCE ENABLE'` with the same connection
flags.

## Disk watermarks

```
disk_watermark_check_ms=60000
disk_watermark_warn_percent=85
disk_watermark_reject_percent=95
```

Optional, off by default (`disk_watermark_check_ms=0`). When enabled and
`--data-dir` is set, `nextsqld` runs a background goroutine that periodically
statfs's the volume holding the data directory
(`internal/diskspace.Stat` — `statfs(2)`/`GetDiskFreeSpaceEx`, so it reports
the physical filesystem, not NextSQL's own logical `storage_cap_bytes`) and
acts on used-space percentage:

- At or above `disk_watermark_warn_percent` (default 85), logs a warning.
- At or above `disk_watermark_reject_percent` (default 95), additionally
  rejects every mutating statement with `Unavailable` — the same
  classification and enforcement point as [maintenance mode](#maintenance-mode)
  above, but a **separate, independent flag**: an operator's `CLUSTER
  MAINTENANCE DISABLE` does not clear a disk-watermark trip, and a
  disk-watermark recovery does not clear an operator's maintenance window.
  Reads keep working throughout, same as maintenance mode.

The two thresholds use hysteresis to avoid flapping right at one boundary:
once tripped, the reject state only clears after usage drops back **below
the warn line**, not merely below the (higher) reject line. `Validate()`
requires `disk_watermark_warn_percent < disk_watermark_reject_percent`.

This is node-local and not Raft-replicated (like maintenance mode and
remote drain) — on a cluster, only the leader accepting writes matters for
write traffic; a tripped follower still blocks its own local write
attempts. It is a last-resort backstop against actually running out of disk
mid-write, not a substitute for capacity planning or WAL/backup retention
policies (see [WAL retention](wal.md#automatic-time-based-retention-wal_retention_ms)
and `nextsql backup prune`) — configure those first so this trips rarely if
ever. Current disk usage and cumulative warn/reject counters are exposed via
the metrics registry (`internal/metrics.Snapshot.DiskTotalBytes`/
`DiskFreeBytes`/`DiskWatermarkWarns`/`DiskWatermarkRejects`).

## Rolling upgrade

Procedure for taking one node of a Raft-clustered deployment down for a
binary upgrade (or any other maintenance that requires stopping its
process) without an availability or data-loss window, repeated once per
node:

1. If the node is the current leader, issue `nextsql cluster
   transfer-leader` against it first. This is a planned handoff — unlike a
   crashed-leader failover, callers see no write-unavailability window at
   all (see "Leader transfer" above). Wait for a new leader to be confirmed
   (`SHOW CLUSTER` / `system.replication.has_leader` on a remaining node)
   before continuing; skip this step entirely for a node that is already a
   follower.
2. Issue `nextsql cluster drain [--timeout-ms N]` against the node. This
   stops it accepting new connections, closes idle ones immediately, and
   waits up to `N` (default: its configured `shutdown_drain_ms`) for busy
   ones to finish before force-closing whatever remains and closing its
   listener — see "Remote drain" above. Choose `N` generously enough that
   ordinary statements on this deployment finish comfortably inside it; a
   connection still busy exactly at the deadline is force-closed like any
   other drain (see the correctness note below).
3. Stop the process and upgrade the binary. Run `nextsql lifecycle upgrade
   --data-dir DIR --key-file FILE --cluster-node` while the process is down:
   it runs WAL recovery and confirms the store opens and the catalog decodes
   under the new binary before the node rejoins (see `docs/install.md`). The
   `--cluster-node` flag is required for a node that shows Raft membership —
   without it the command stops at `blocked` and prints this procedure — and
   asserts steps 1–2 were done. Then restart the node. It rejoins the Raft
   cluster as a follower and replays from where it left off; wait for it to
   catch up (`system.replica_health.apply_backlog` back to 0, or
   `applied_lsn` matching the leader's) before moving on to the next node.
4. Repeat for the next node. A 3-voter deployment keeps quorum (2 of 3)
   throughout every single node's cycle, so writes never stop landing
   cluster-wide during a properly sequenced rolling upgrade — only the node
   being upgraded itself is briefly unreachable.

`CLUSTER MAINTENANCE ENABLE` (see above) is not part of this sequence by
default — draining and restarting one node at a time is already safe on its
own, since the surviving majority keeps serving writes. Reach for
maintenance mode in addition to this sequence only when the upgrade is
paired with something that specifically must not race a concurrent write
cluster-wide (an online schema change, say): enable it on the leader before
step 1 of the *first* node's cycle, leave it enabled across every node's
cycle, and disable it only after the last node rejoins — remembering to
re-issue `CLUSTER MAINTENANCE ENABLE` against whichever node ends up leader
after each `CLUSTER TRANSFER LEADER`, since the flag is node-local and does
not follow leadership (see the maintenance-mode section above).

**Tested by** `tests/integration/rolling_upgrade_test.go`
`TestRollingUpgradeDrainWithLeaderTransferNoTransactionLoss`: a 3-node
cluster under continuous write load goes through steps 1–3 for one node
(transfer leadership away, drain — which also closes its listener — take
its Raft transport down to stand in for the process restart, then bring it
back), and the test asserts every acknowledged write survives and the
rolled node converges back to the cluster's state once it rejoins. This is
the first Phase 27 exit-gate line, "planned maintenance can drain without
unnecessary transaction loss."

**Correctness note (structural fix landed 2026-09-03; see TODO.md's Phase 27
exit gate and log #79 for the full history and design):**
`storage.Engine.commitAndReplicate` used to commit a transaction to local
storage *before* calling `Cluster.Replicate` for Raft quorum. If a write
raced a leader transition (`CLUSTER TRANSFER LEADER` in step 1 above being
the in-scope case, but a crash failover has the identical race) such that
`Replicate` failed after the local commit already succeeded, that local
commit was never rolled back — the client correctly saw the write fail
(`Unavailable`, safe to retry), but the node could be left holding one
un-replicated local row that ordinary Raft log-replication catch-up never
reconciled away. This was deferred twice, each time in favor of a
mitigation, before the structural fix below finally landed.

**The fix**: a transaction's commit record is now held — appended to the
WAL but kept out of the durable prefix, not yet visible, locks not yet
released — via a new `wal.Log.AppendHeld`/`ReleaseHold` durability-barrier
primitive, until `Replicate`'s outcome is known. The outcome splits in two:

- **Definite failure** — this node was rejected before it could even
  propose the entry to Raft (`raft.State() != Leader`, checked before
  `raft.Apply` is called). This is the common, in-scope case above: any
  write landing during a leadership-transfer window. The held record is
  discarded and the transaction rolled back cleanly. No local commit ever
  happened, so there is nothing to reconcile and no orphan.
- **Ambiguous/in-doubt failure** — the entry *was* proposed (`raft.Apply`
  was called) but the quorum wait itself failed or timed out (lost
  leadership mid-flight, an enqueue timeout, a shutdown racing the call).
  Whether the entry actually reached quorum before the failure can't be
  known from here, and the WAL's LSN counter has already moved past the
  held record — discarding it would risk a *worse* outcome than today's
  known orphan: if the entry did reach quorum, a later legitimate replay of
  it would be silently skipped as already-seen rather than applied,
  producing a permanent, undetectable divergence instead of an observable
  one. So this one residual case keeps this project's original fail-open
  behavior exactly as before: the commit stays local, and the
  detection/mitigation below is unchanged and remains the answer for it.

Closing the ambiguous case for real is not a matter of more engineering
effort within this design — it's inherent to `raft.Apply`'s own contract,
which does not distinguish "definitely didn't commit" from "unknown" on
failure. A fundamentally different replication protocol would be needed to
close it, which was judged out of scope here.

**Detection and mitigation for the residual ambiguous case** (unchanged
from before this fix, and still in effect): `metrics.Snapshot.ReplicationOrphans`
(additive-only, see "Metrics" above) makes an ambiguous-failure orphan
observable. The leader also tells its `Cluster` (via the
`storage.ReplicationOrphanReporter` hook) to bar itself from serving STRONG
reads — `Cluster.StrongReadBarrier()` fails `Unavailable` regardless of
leadership until an operator explicitly runs `CLUSTER RECONCILE CONFIRM`.
This is deliberately narrower than a leadership transfer: the affected row
is only ever visible to reads under this exact orphan-triggering timing
window, and STRONG is the one consistency mode that promises linearizable,
read-after-acknowledged-write behavior — the mode most harmed by silently
serving a row about to diverge from the rest of the cluster. `BOUNDED`/
`STALE` reads are unaffected (both already accept some staleness by
contract). The flag is node-local (like maintenance mode), not
Raft-replicated: a clean node elected leader afterward is never affected by
another node's divergence. `system.replica_health.replication_suspect`
surfaces the flag for monitoring.

**Operator runbook**: after `metrics.Snapshot.ReplicationOrphans` increases
(or a client/log shows a `Replicate` failure), and STRONG reads on the
affected node start failing `Unavailable` with a "local commit history is
unreconciled" message, an operator should verify the node's data (compare
against a known-good replica, or accept that the orphaned row will simply
be overwritten or become irrelevant) and then run `nextsql cluster reconcile
confirm` (or `CLUSTER RECONCILE CONFIRM` directly) against that node to
resume serving STRONG reads. There is no automatic clearing — this is a
data-integrity flag, and only an operator who has actually looked should
clear it.

## Machine-readable operation output

```text
nextsql exec --json -c 'CLUSTER DRAIN'
nextsql cluster status --data-dir DIR --json
nextsql cluster transfer-leader --json
nextsql cluster drain --json
nextsql cluster maintenance enable --json
nextsql cluster reconcile confirm --json
```

`--json` on `nextsql exec` and every `nextsql cluster` subcommand prints a
single JSON object on stdout instead of the default tab-separated text, for
scripts that would rather decode structured output than parse TSV
positionally. `exec`, `cluster transfer-leader`, `cluster drain`, and
`cluster maintenance enable|disable` all print
`{"columns": [...], "rows": [[...]], "affected": N}` — cell values are
stringified the same way the TSV path renders them (no attempt is made to
reproduce native JSON types per SQL type, keeping the shape stable across
every result kind). `cluster status` instead prints its own status object
(`{"node_id", "state", "leader_id", "leader_addr", "voters", "applied_lsn",
"has_leader", "apply_backlog", ...}`, the same fields printed as plain text
by default), since it reads a local status file rather than running SQL.
Without `--json`, output is unchanged from before this existed.

## `nextsql-bench`

```text
nextsql-bench [--quick] [--slo] [--slo-max-rows 25000] [--slo-vectors 256]
              [--slo-vector-queries 64]
              [--slo-buffer-pages 4096]
              [--workload all|page|point|range|insert|update|delete|txn|join|agg|json|fulltext|vector|hybrid]
              [--partition] [--partition-rows 20000]
              [--readscale] [--readscale-rows 5000] [--readscale-readers 8]
              [--vecquant] [--vecquant-rows 2000] [--vecquant-dim 128]
              [--vecquant-sparse-dim 4096] [--vecquant-sparse-nnz 24]
              [--vecquant-queries 64]
              [--duration 1s] [--rows 128] [--concurrency 1]
```

SQL workloads run against a throwaway encrypted database with WAL and
fsync on. Each report includes QPS, TPS (write/txn workloads), p50 / p95
/ p99 / p99.9, allocs, heap, disk delta, WAL bytes, and encryption
overhead (`enc%` = page AEAD time / elapsed). Page microbenches remain
for encode/encrypt/buffer I/O.

`--partition` runs the partition-pruning comparison: a RANGE-partitioned table
(eight single-value bands, `PRIMARY KEY (bucket, id)`) against an unpartitioned
`PRIMARY KEY (id)` table with the same rows. It reports a pruned single-bucket
scan, a pruned single-bucket `SUM` over the heap, an unpruned full `SUM` (the
partitioning overhead check), and routed vs plain `INSERT`, each with p50/p95/p99
and `speedup` = flat p50 / partitioned p50. Reads run inside a read-only
transaction so the SELECT result cache never serves them. See
`docs/partitioning.md` for a published run and how to read it.

`--readscale` runs the follower-read scaling comparison: a 3-node single-leader
cluster (encryption, WAL, fsync on) driving PK point reads under `STRONG` on the
leader, `STALE` on the leader, `STALE` over two and three members, and `BOUNDED`
over three. It reports aggregate read QPS, the leader's slice of it
(`leader-qps`), p95/p99, and the aggregate ratio against the `stale-1n`
baseline. It measures the Raft read-barrier cost (`STALE` ≈ 2× `STRONG` on one
node) and the leader read-offload (~3.5× lower `leader-qps` across three
members). Aggregate QPS is CPU-bound on one host; a real deployment adds a host
per replica. See `docs/ha.md` "Read scaling" for a published run.

`--vecquant` runs the quantised-vector comparison: the same vector set seeded
into an `F32`, an `F16`, and an `I8` column, each with its own HNSW index, plus
an `F32` column with an `F16`- and an `I8`-quantised HNSW graph
(`WITH (QUANTIZATION = …)`), an `F32` column with an IVF and an IVF-PQ index,
and a `SPARSEVECTOR` inverted index on a high-dimension, low-nnz corpus
(`--vecquant-sparse-dim` / `--vecquant-sparse-nnz`, independent of
`--vecquant-dim`). It reports per-config on-disk width, raw payload
size, index-build page delta, total database size, build time, resident heap,
mean quantisation error, and `NEAREST` p50/p95/p99 + recall@10/@100 (dense rows
scored against an exact-cosine flat search over the full-precision source
vectors; the `SPARSE` row against exact-cosine `SparseFlat`). See
`docs/vector.md` "Size / recall comparison" for the 2026-08-31 published run
(p50/p95/p99, QPS, heap, recall@10/@100, index/db size, build time) and
"Production-gating sign-off (Phase 23)" for the dated P23 review.

`--slo` runs the published-number suite: cached PK lookup, secondary-index
equality, durable single-row INSERT/UPDATE, bulk `INSERT` plus `COUNT(*)` /
`GROUP BY` / PK range / hash join at 25K (and larger if `--slo-max-rows`
allows), hybrid `WHERE`+`SEARCH`+`NEAREST`, and HNSW `recall@10` /
`recall@100` with QPS, heap, and db size. Bulk load uses 4096-row `InsertEncoded` batches and `COMMIT` every 524288
rows. Every row includes CPU,
RAM, filesystem, row width, query, indexes, cache condition, encryption,
durability, and concurrency.

These are engineering measurements on the host that ran the tool, not
universal product guarantees. The 100M analytical run and corrected 1M-vector
HNSW v10 run are published below. Large atomic index builds use a no-steal
transaction and may require a larger explicitly reported buffer pool; use
`--slo-buffer-pages` rather than weakening WAL or durability.
10M DELETE soak: `NEXTSQL_SOAK_ROWS=10000000 go test ./internal/executor/ -run TestBulkDeleteSoak`.
`BulkDeleteAll` returns the affected-row count before atomically replacing an
eligible unindexed heap. A tree populated in the current process has a maintained
exact `liveRows` cache, so the count and heap swap are effectively O(1). A tree
opened after restart deliberately begins with `liveKnown=false`; its first count
walks the leaf chain to reconstruct the answer, making that run O(rows) before
the same constant-time heap swap. This methodology difference explains the
observed **25 ms / 1.57 s** warm-process results versus the **24 s** cold-open
10M result. All include encryption, WAL, fsync, MVCC, and the affected-row count;
they are not directly comparable DELETE-throughput measurements.
100M B+Tree invariants: `./scripts/run-btree-soak.sh`. The wrapper uses
workspace-backed temporary storage (override with `NEXTSQL_SOAK_TMPDIR`),
retains timestamped output under `.bench-results` (override with
`NEXTSQL_SOAK_LOG`), defaults to `GOMEMLIMIT=3GiB`, `GOGC=40`,
`GODEBUG=madvdontneed=1`, and `NEXTSQL_BTREE_POOL_PAGES=24576` (a 384 MiB
resident buffer pool), and disables Go's default 10-minute test timeout.
At one million operations and above, the invariant workload commits bounded
4,096-operation write transactions, keeping randomized insert/delete coverage
and periodic full-tree checks without turning the correctness soak into a
100-million-fsync benchmark. At scale the harness runs two cadences: a cheap
checkpoint plus checkpoint-obsolete WAL discard every one million operations
(bounding WAL disk footprint for this disposable non-PITR workload -- redo is
full 16 KiB page images), and the expensive full structural `Check()` plus
scan-count every tenth of the run, each followed by `debug.FreeOSMemory()`.
`NEXTSQL_BTREE_POOL_PAGES` sizes the resident buffer pool so most of the working
set stays cached (buffer-miss read/evict traffic, not CPU, dominates the run);
`NEXTSQL_BTREE_SPACE` optionally caps the key space so the resident tree fits
the pool on a RAM-constrained host. The batched path completed 1M operations
plus final scan and point verification in 809.15 s on 2026-08-21.

Disposition (2026-08-30): the terminal 100M-operation run is paper-closed as a
deferred standalone measurement, not a P16 release gate -- the same disposition
applied to P18. The structural correctness it covers is exercised by
`TestRandomizedDeleteMerges`, `TestCrashDuringMerge`, `TestBulkDeleteSoak`, and
the published 10M DELETE run, and by the soak itself at every scale it has
reached: v4 reached 24M clean operations (`live=11,435,641`);
`nextsql-btree-100m-p16-v8` reached 44M clean operations (`live=17,557,686`)
under an 8 GiB cap. `nextsql-btree-100m-p16-v9` (`NEXTSQL_BTREE_OPS=100000000`,
8 GiB cap) was SIGKILLed (exit 137) after ~11 h on a RAM-constrained host with
no retained terminal evidence; v10 was stopped by explicit direction on
2026-08-26. The harness was then reworked (resident pool, key-space cap,
decoupled checkpoint cadence, `int32` bookkeeping, post-check `FreeOSMemory()`)
so a future terminal run -- full structural check, scan count, randomized point
verification -- can complete on that host class. Override the operation count
with `NEXTSQL_BTREE_OPS` for a smaller validation run.

### Published SLO suite (2026-08-17)

Hardware: linux/amd64, AMD Ryzen 5 7535HS, 12 logical CPUs, 14.3 GiB RAM,
encryption AES-256-GCM on, WAL + fsync on, 1 session, buffer 2048 pages
(32 MiB). Scan path: interior `SplitKeys`, concurrent page decrypt, `COUNT(*)`
counts visible slots without materializing payloads.

Reads through 1M (tmpfs-backed temp dir; EXPLAIN on the index query is
`IndexScan kv ix_kv_n`). `nextsql-bench --slo --slo-max-rows 1000000 --slo-vectors 256 --duration 2s`.

| Workload | Rows | Query | Indexes | Cache | p50 | p95 | p99 | Target | Met |
|---|---|---|---|---|---|---|---|---|---|
| Cached PK | 25 000 | `SELECT n FROM kv WHERE id = $1` | PRIMARY KEY (id) | warm heap + PK | 30 µs | 55 µs | 96 µs | p50&lt;0.5 ms | yes |
| Indexed equality | 25 000 | `SELECT id FROM kv WHERE n = $1` | `ix_kv_n` | warm | 44 µs | 79 µs | 152 µs | p95&lt;3 ms | yes |
| 25K `COUNT(*)` | 25 000 | `SELECT COUNT(*) FROM scan` | heap | working set fits in 32 MiB buffer | 3 ms | 3 ms | 3 ms | &lt;1 s | yes |
| 25K `GROUP BY` | 25 000 | `SELECT k, COUNT(*) FROM scan GROUP BY k` | heap + hash agg | working set fits in 32 MiB buffer | 6 ms | 6 ms | 6 ms | &lt;1 s | yes |
| 100K `COUNT(*)` | 100 000 | `SELECT COUNT(*) FROM scan` | heap | working set fits in 32 MiB buffer | 9 ms | 9 ms | 9 ms | &lt;1 s | yes |
| 100K `GROUP BY` | 100 000 | `SELECT k, COUNT(*) FROM scan GROUP BY k` | heap + hash agg | working set fits in 32 MiB buffer | 24 ms | 24 ms | 24 ms | &lt;1 s | yes |
| 1M `COUNT(*)` | 1 000 000 | `SELECT COUNT(*) FROM scan` | heap | 32 MiB buffer (working set exceeds buffer) | 215 ms | 215 ms | 215 ms | &lt;1 s | yes |
| 1M `GROUP BY` | 1 000 000 | `SELECT k, COUNT(*) FROM scan GROUP BY k` | heap + hash agg | 32 MiB buffer (working set exceeds buffer) | 191 ms | 191 ms | 191 ms | &lt;1 s | yes |
| Hybrid | 256 | `WHERE` + `SEARCH` + `NEAREST` LIMIT 10 | JSON path + full-text + HNSW | warm | 9.0 ms | 16.3 ms | 18.6 ms | p95&lt;100 ms | yes |
| HNSW top-10 | 256 × 8-d | `NEAREST … LIMIT 10` | HNSW | warm graph | 2.1 ms | 3.3 ms | 3.3 ms | p95&lt;25 ms | yes* |

\*Recall@10 = 1.000, recall@100 = 0.646. This is **not** the 1M-vector official scale.

10M row-processing, same host, **ext4** (LVM) temp dir. 4096-page / 64 MiB buffer; working set does not fit. `TMPDIR` on ext4; `nextsql-bench --slo --slo-max-rows 10000000 --slo-vectors 16 --duration 1s` (2026-08-18 insert-path pass). Encryption, WAL, fsync, checksums, and MVCC stayed on.

| Workload | Rows | FS | Elapsed | Rate | Target | Met |
|---|---|---|---|---|---|---|
| 10M bulk `INSERT` | 10 000 000 | ext4 | **33 s** | 301 485 rows/s | next &lt;15 min; long-term &lt;2 min; lifetime &lt;1 min | **yes / yes / yes** |
| 10M bulk `UPDATE` | 10 000 000 | ext4 | **10 s** | 985 275 rows/s | next &lt;10 min; long-term &lt;2 min | **yes / yes** |
| 10M bulk `DELETE` | 10 000 000 | ext4 | **24 s** | heap swap / count | long-term &lt;5 min | **yes** |
| 10M `COUNT(*)` | 10 000 000 | ext4 | **121 µs** | live counter | &lt;5 s; long-term &lt;1 s | **yes / yes** |
| 10M `GROUP BY` | 10 000 000 | ext4 | **589 ms** | — | &lt;5 s; long-term &lt;1 s | **yes / yes** |
| 10M PK range `COUNT` | 10 000 000 | ext4 | 6.34 s | — | residual string `id` range | no (&lt;5 s) |
| 10M `COUNT(*)` join | 10 000 000 | ext4 | **1.06 s** | — | streamed hash probe | yes |

1M on the same run: `INSERT` 2.04 s (489 320 rows/s), `COUNT(*)` 53 µs, `GROUP BY` **112 ms**.

100M row-processing, same host, **ext4** (LVM) workspace-backed temp dir,
4096-page / 64 MiB buffer, 2026-08-21. Command:
`TMPDIR=$PWD/.bench-tmp nextsql-bench --slo --slo-max-rows 100000000
--slo-vectors 16 --slo-no-dml --duration 1s`. Encryption, WAL, fsync,
checksums, and MVCC stayed on. After correcting the range workload and using
fixed-width scan keys, the replacement 100M bulk load took **18 m 02.22 s**
(92 403 rows/s).

| Workload | Rows | Elapsed | Target | Met |
|---|---:|---:|---:|---:|
| `COUNT(*)` | 100 000 000 | **56 µs** | &lt;60 s | yes (live-count fast path) |
| `GROUP BY k, COUNT(*)` | 100 000 000 | **16.31 s** | &lt;60 s | **yes** |
| PK range `COUNT` (exactly 5,000 rows) | 100 000 000 | **2.21 ms** | &lt;60 s | **yes** |
| Hash join `COUNT(*)` | 100 000 000 | **35.54 s** | &lt;60 s | **yes** |

The original range predicate (`id >= 's0' AND id < 's5000'`) was not a
5,000-row interval because the unpadded decimal strings compare
lexicographically; at 100M it selected tens of millions of rows. Range costing
also treated a clustered PK range like per-row random heap lookups, causing a
`SeqScan`. Both defects were fixed on 2026-08-21: the harness now chooses an
equal-width 5,000-row interval around the scale midpoint, and the optimizer
costs it as a seek plus sequential leaf reads. A durability-on 1M verification
used `id >= 's500000' AND id < 's505000'`, chose clustered `IndexScan`, and
completed in **3.28 ms**. The corrected durability-on 100M rerun used
`id >= 's000050000000' AND id < 's000050005000'`, self-verified the clustered
`IndexScan` plan and exact 5,000-row result, and completed in **2.21 ms**. The
100M analytics exit gate is met.
The indexed-equality latency check in the same run used `IndexScan kv ix_kv_n`
and measured p50 **35 µs**, p95 **100 µs**, and p99 **147 µs** at its
25K-row latency scale.

1M-vector HNSW baseline, same host and **ext4**, 2026-08-21. Command:
`TMPDIR=$PWD/.bench-tmp nextsql-bench --slo --slo-max-rows 25000
--slo-vectors 1000000 --slo-buffer-pages 32768 --slo-no-dml
--duration 1s`. The 32,768-page buffer is 512 MiB. Encryption, WAL, fsync,
checksums, and MVCC remained enabled. The default 4,096-page pool exhausted
its no-steal dirty frames while atomically persisting this index, so the larger
pool is part of the labeled methodology rather than an unreported benchmark
change.

Measurement caveat: this baseline's original deterministic generator produced
only eight distinct directions at dimension 8, repeated across all one million
rows. Its recall values therefore mostly measure primary-key tie-breaking among
equal-distance vectors and are not representative ANN recall. The harness now
uses deterministic, normalized, distinct vectors, disjoint query seeds, and a
64-query default sample. The corrected 1M v10 result below supersedes this
degenerate baseline; the historical timings remain useful only as latency
history.

Corrected-workload scale checks on the same ext4 workspace, 2026-08-21, used
distinct normalized vectors, disjoint query seeds, and 64 queries. Both kept
encryption, WAL, fsync, checksums, and MVCC enabled:

| Vectors | Buffer | p50 | p95 | p99 | Recall@10 | Recall@100 | Heap | HNSW size | Target met |
|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| 10,000 | 4,096 pages | **1.504 ms** | **1.827 ms** | **2.485 ms** | **1.000** | **1.000** | 157.2 MiB | not recorded | yes |
| 100,000 | 8,192 pages | **2.567 ms** | **3.317 ms** | **4.763 ms** | **1.000** | **0.999** | 394.2 MiB | 48.2 MiB | yes |
| 1,000,000 | 131,072 pages | **6.158 ms** | **8.061 ms** | **8.156 ms** | **1.000** | **0.998** | 4.3 GiB | 546.1 MiB | yes |

The corrected 1M workload includes heap `searchLayer`, covering PK hits (no
heap fetch for `SELECT id`), and a discarded warm parameterized `NEAREST`.
The bounded `nextsql-hnsw-1m-p16-v8.service` run reached
HNSW graph construction but was OOM-killed at its explicit 8 GiB cgroup limit
on 2026-08-24. It produced no query-latency or recall sample and therefore does
not satisfy the P16 exit gate. The failure is retained in
`.bench-results/hnsw-1m-p16-v8.log`; raising the cap without first bounding Go
heap growth is not an accepted fix.

The replacement `nextsql-hnsw-1m-p16-v9.service` run used
`GOMEMLIMIT=4GiB` and `GOGC=50` beneath the unchanged 8 GiB cgroup cap but
failed during atomic graph construction when its 32,768-page no-steal pool
had no evictable frames (`buffer.evict: all frames are pinned`) at a 5.48 GiB
memory peak. This is retained in `.bench-results/hnsw-1m-p16-v9.log`.

The v10 rerun started on 2026-08-25 with the same corrected 1M-vector/64-query
data set, encryption, WAL, fsync, checksums, and MVCC, but a 131,072-page
(2 GiB) buffer pool so the atomic no-steal build can retain its dirty pages.
`GOMEMLIMIT=4GiB` and `GOGC=50` remain enabled; retained output is
`.bench-results/hnsw-1m-p16-v10.log`. It completed with p50 **6.158 ms**, p95
**8.061 ms**, p99 **8.156 ms**, QPS **156**, recall@10 **1.000**, recall@100
**0.998**, heap **4.3 GiB**, DB size **1.1 GiB**, and HNSW size **546.1 MiB**.

| Run | Vectors | Dimension | p50 | p95 | p99 | QPS | Recall@10 | Recall@100 | Heap | DB size | HNSW size | Target met |
|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| instrumented | 1,000,000 | 8 | **883 µs** | **1.071 ms** | **1.071 ms** | **1,097** | **0.125** | **0.041** | **3.1 GiB** | **915.6 MiB** | **353.4 MiB** | yes for this run |
| immediately preceding | 1,000,000 | 8 | **1.078 ms** | **36.78 ms** | **36.78 ms** | **134** | **0.125** | **0.041** | **2.4 GiB** | **915.6 MiB** | not instrumented | **no** |

The configured query target is p50 `<10 ms`, p95 `<25 ms`, p99 `<50 ms`
with non-zero recall. One run met it and the adjacent run missed p95, while
both had low recall. Treat this as the required 1M baseline, not a signed-off
exit gate. The 36.78 ms miss had p95 = p99, which is what `latencyPct` does
when one cold sample dominates a tiny query count. The harness now warms one
parameterized `NEAREST` (`$1`) before timing, reuses the plan cache, and uses
64 queries. `searchLayer` uses min/max heaps instead of re-sorting the
candidate lists on every neighbor. HNSW size is physical database-file growth
measured immediately across `CREATE VECTOR INDEX`.

10M `INSERT` is a durable **bulk load**: `InsertEncoded` → `InsertBatch` (4096-row batches, `COMMIT` every 524288). Sequential keys pin one leaf and append. When the new key is greater than every key on a full leaf, the left page is kept and only the new record goes on a new right sibling (no collect/rebuild). The rightmost parent is cached so a split does not re-descend from the root. A sole-writer snapshot skips per-row logical WAL and UNDO; commit still writes encrypted page images and `fdatasync`s. Default WAL segment is 128 MiB. AES-256-GCM AEAD objects are cached per DEK (stdlib crypto, not a new primitive). It is not 10M single-row SQL `INSERT`s. Page cache-miss I/O authenticates the envelope and page id in place (no extra 16 KiB copy).

10M `COUNT(*)` with no predicate returns a process-maintained live key count when this session is the only snapshot. After `Open` of an existing tree the count is unknown and the executor scans. Concurrent readers always scan.

10M `UPDATE` patches the decimal column in place on the leaf (`PatchVisible`) when this session is the only snapshot and the table has no secondary indexes.

10M `DELETE ALL` replaces the heap with an empty tree in one catalog/WAL transaction when this session is the only snapshot and there are no secondary indexes. Concurrent readers force the slow per-row path.

### Next-target scorecard (2026-08-18)

These are engineering next/long-term aims, not the PLAN.md §9 product gates (those stay the published “Met” column above).

| Area | Current | Next target | Long-term | Lifetime |
|---|---:|---:|---:|---:|
| PK p50 | **23 µs** | ≤30 µs | ≤20 µs | — |
| PK p99 | **50 µs** | <250 µs | **<100 µs met** | — |
| Indexed p95 | **67 µs** | <250 µs | **<100 µs met** | — |
| 1M `COUNT(*)` | **53 µs** | **<300 ms met** | **<150 ms met** | **met** |
| 1M `GROUP BY` | **112 ms** | **<300 ms met** | **<150 ms met** | **met** |
| 10M `COUNT(*)` | **121 µs** | **<3 s met** | **<1 s met** | **met** |
| 10M `GROUP BY` | **589 ms** | **<3 s met** | **<1 s met** | **met** |
| 10M INSERT | **33 s** / 301 485 rows/s | **<15 min met** | **<2 min met** | **<1 min met** |
| 10M UPDATE | **10 s** / 985 275 rows/s | **<10 min met** | **<2 min met** | **met** |
| 10M DELETE | **24 s** | **measurable** | **<5 min met** | **met** |
| 100M analytics | COUNT **56 µs**; GROUP BY **16.31 s**; range **2.21 ms**; join **35.54 s** | <60 s | <30 s | **next target met** |
| 1M HNSW | p95 **1.071–36.78 ms**, recall@10 **0.125**, recall@100 **0.041** | p95 <25 ms + recall | <10 ms | baseline measured; gate unstable |
| HA recovery | **<5 s met** | retain | <2 s | — |

1M bulk DML, same host, 2026-08-18. tmpfs: INSERT 1 m 6 s (15 205 rows/s), UPDATE 51.3 s (19 483 rows/s), `COUNT(*)` 215 ms, `GROUP BY` 191 ms. ext4 (LVM): INSERT 1 m 18 s (12 828 rows/s), UPDATE 1 m 27 s (11 488 rows/s). 10M times above are the last full-scale ext4 run; 1M rates are the post-fix re-run.

`GROUP BY k, COUNT(*)` peeks the group column as a byte view and increments interned counters (no per-row `string` alloc). 1M `GROUP BY` is **147 ms**; 10M is **660 ms**.

Write-path changes behind the 1M rates: in-place leaf insert/update/delete (no full-page rebuild on the common path), arithmetic `leafFits`, growing updates that refuse to mutate when the page cannot hold the new record, and allocator freelist persistence at commit (not on every Alloc/Free).

The corrected 100M analytics measurements are published above.

Durable writes, same host, two filesystems (WAL + fsync still on):

| Workload | FS | p50 | p95 | p99 | Target | Met |
|---|---|---|---|---|---|---|
| INSERT | tmpfs | 333 µs | 742 µs | 7.0 ms | p50&lt;2 ms p95&lt;5 ms p99&lt;10 ms | yes |
| UPDATE | tmpfs | 391 µs | 770 µs | 1.3 ms | same | yes |
| INSERT | ext4 (LVM) | 1.7 ms | 3.0 ms | 10.0 ms | same | yes |
| UPDATE | ext4 (LVM) | 1.8 ms | 2.5 ms | 6.8 ms | same | yes |

ext4 fsync p95 is device-dependent. An earlier labeled run on this host saw INSERT/UPDATE p95 ≈ 100 ms; this run met the target. Do not treat either as a universal guarantee.

Row width for `kv`: STRING PK + DECIMAL(12,2). Scan table: STRING PK + STRING + DECIMAL (~40 B encoded). Hybrid products: UUID + STRING + DECIMAL + TEXT + JSON + `VECTOR<F32,8>`.

A `BETWEEN` residual against `ix_kv_n` on 25 K rows was ~21 ms and is not the official indexed-query measurement; equality is `IndexScan`.

`enc%` is AEAD time share, not “slowdown vs unencrypted”. Official
benches do not disable encryption. ~7% on a short INSERT micro-run is inside the
`< 10%` OLTP engineering target; it is not a universal guarantee.

## Out of scope here

Race detector (`go test -race`) still needs a C compiler on this host.
HA is documented in `docs/ha.md`.
