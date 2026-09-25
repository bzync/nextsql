# Changelog

All notable changes to NextSQL are recorded here.

The current release is **0.0.5**; **0.0.1** was the first public one. All are
previews: the engine is complete enough to install, query, and operate, and it
is still under measurement. Before you rely on it, run `nextsql-bench --slo`
and the crash, recovery, and HA suites on your hardware.

Versioning follows [semver](https://semver.org/) for the engine tag (`vX.Y.Z`)
and the official driver packages. Do not reuse a published version for
different bits.

---

## [Unreleased]

## [0.0.5] — 2026-09-25

### Added

- **Raft Group Proposal Batching (`internal/storage/engine.go`, `internal/wal/log.go`).**
  - **High-Throughput Cluster Proposal Batching**: batches up to 64 concurrent transaction commit requests into a single Raft proposal round. Concurrent callers queue onto a group coordinator (`replQueue`) without holding locks, while the coordinator replicates the batch in one quorum round and flushes all commit records in a single fsync. Closes GAPS.md Row 12.
  - **Contiguous Held Batches (`AppendHeldBatch`, `ReleaseHold`)**: extends WAL hold buffer to support atomic multi-record batches, allowing non-durable commit records to be proposed over Raft and then either released and flushed together in one fsync or spliced out in memory on rejection (`NotProposedError`) without touching disk.
  - Verified under `-race` with concurrent writer matrices during leader kills, network partitions, and planned leader transfers (`tests/ha/ha_test.go`).

- **Zero-Filled WAL Segment Preallocation (`internal/wal/segment.go`, `internal/wal/log.go`).**
  - **Filesystem Journal Bypass (`diskio.Preallocate`)**: segments are preallocated to full size (128 MiB) via `fallocate` mode 0 at creation time. Overwrites within the preallocated range avoid updating the inode file size (`i_size`) and extent trees, eliminating ext4/XFS `jbd2` journal flushes during `fdatasync`. Drops P50 fsync latency from **1.44 ms to 0.89 ms** (~38% speedup). Closes GAPS.md Row 10 secondary gain.
  - **Zero-Tail Recognition & Safe Active Offset Discovery**: `ScanFrom`, `wal.Open`, and `ClipTo` recognize preallocated zero headers (`isZeroHeader`) and trailers (`isAllZero`) without truncating the file back down, preserving the preallocated runway across restarts. `findSegmentWriteOffset` locates the exact valid record boundary on open. Torn tails past `durableLSN` are cleanly truncated.
  - Verified in `internal/wal/prealloc_test.go`, `tests/fault`, `tests/crash`, and `tests/upgrade`.

- **NextSQL Studio Relational Completions & Function Catalog (`StudioWorkspace.tsx`, `studio.css`).**
  - Synthesizes intelligent relational `JOIN` conditions and `ON` / `WHERE` equality matches based on primary and foreign key constraints.
  - Comprehensive built-in function signature catalog across aggregates, string manipulation, math, dates, JSON, vectors, spatial, and system introspection.
  - Active item auto-scrolling during keyboard navigation and semantic color badges (`PK`, `FK`, `Table`, `Function`).

- **Sustained Insert Throughput Characterization & Measurement Suite (`scripts/bench-sustained-insert.go`).**
  - **Alternating Benchmarking Architecture**: automated harness executing 20-second alternating runs with 64 concurrent client connections over the native wire protocol against `nextsqld` on dedicated ext4 block storage with 10-second settle gaps and filesystem `sync` barriers. Measures time-series per-second ops, latency percentiles, physical disk writes (`/proc/<pid>/io` `write_bytes`), logical writes (`wchar`), `wal_bytes_written` from `system.metrics`, and Linux Pressure Stall Information (`/proc/pressure/io`).
  - **Verified Write Amplification Reduction (6.50x)**: page-delta redo writes **3,102.9 bytes/insert** (2,024.8–2,029.1 bytes logged to WAL) vs **20,132.4–20,191.0 bytes/insert** under full images (19,045.6–19,111.9 bytes logged to WAL).
  - **Sustained Throughput & Saturation Attribution**: page-delta mode sustains peak throughput (**5,362–5,768 ops/s**, P50 10.8–11.3 ms) for 12+ seconds before kernel dirty writeback queues saturate, yielding 3.1x higher average sustained throughput (3,308.9 vs 1,067.9 ops/s) than full images, which exhaust OS dirty-page headroom within 6 seconds (~108 MiB/s write rate) and collapse to ~220–290 ops/s. Proves conclusively that write stalls under heavy insert loads are hardware write-bandwidth and kernel writeback saturation, not engine mutex or scheduling bottlenecks.

- **NextSQL Admin Setup Mode Firewall Rule Creation & Staged Progress.**
  - **Host Firewall Assistant (`nextsql setup --firewall`, `internal/admin/setup/firewall.go`)**: automatic probing of Linux host firewalls (`ufw`, `firewalld`, `nftables`, `iptables`), safe structured rule execution without shell interpolation when running with root privileges (`os.Geteuid() == 0`), and informative copyable `sudo` command guidance with 1-click clipboard copy when running unprivileged or in `--dry-run`.
  - **Setup Wizard Integration (`Resources.tsx`, `Summary.tsx`, `Completion.tsx`)**: accessible firewall configuration card when exposing NextSQL on external network addresses, summary review row, and post-install outcome alerts.
  - **Multi-Step Staged Progress Indicator (`InstallProgress`)**: accessible multi-stage progress tracking across parameter validation, database & keystores initialization, offline recovery keys sealing, configuration persistence, engine health verification, and system service / firewall configuration with ARIA progressbar semantics (`role="progressbar"`), active stage indicator, live elapsed timer, and completed checkmark badges.
  - Full WCAG 2.2 AA accessibility verification in headless Chrome with axe.

- **NextSQL Studio Catalog-Aware Binder Diagnostics & Typo Quick-Fixes.**
  - **Inline Table & Column Typo Detection**: Studio analyzes SQL buffers against cached live catalog metadata to detect misspelled table references across `FROM`, `JOIN`, `UPDATE`, and `INSERT INTO` clauses (with CTE awareness from `WITH [RECURSIVE]`) and misspelled column references on known tables (qualified and unqualified, skipping keywords, functions, aliases, and literals).
  - **Deterministic Suggestions**: computes suggestions via deterministic Levenshtein distance (distance ≤1 for length ≤4, ≤2 for length >4; ties rejected; bounded at 5 table/column fixes; suppressed if catalog enumeration is truncated).
  - **Single-Click Quick-Fix Rewrites & Jump to Error**: non-modal alert strips provide a "Go to error" button to highlight the offending span in the editor and a "Use '<suggestion>' instead" button to apply the rewrite in-place.
  - **Server-Side Binder Error Integration**: when a query fails with a server-emitted `unknown table` or `unknown column` error, the execution error alert dynamically parses the identifier, provides an immediate "Go to identifier" button, and offers a single-click quick-fix rewrite when a unique catalog match exists.
  - Full unit test coverage in `test-studio-results.mjs` and WCAG 2.2 AA accessibility verification in headless Chrome.

- **Streaming Bulk Import in NextSQL Studio.**
  - **Streaming Bulk Import Explorer (`StreamingImportExplorer.tsx`)**: accessible modal tool (`Streaming bulk import…` in Data menu, Command Palette `Streaming bulk import…`, Recent Connections workspace tools, and a 1-click switch link from the editor `ImportExplorer`) that streams chunked multi-row `INSERT` batches of CSV, semicolon CSV, TSV, JSON array, or NDJSON datasets directly into database tables over the authenticated session connection (`POST /api/v1/studio/query`).
  - **Transactional Safety & Rollback**: optional single-transaction wrapping (`BEGIN;` -> batches -> `COMMIT;`) ensures all-or-nothing atomicity with automatic `ROLLBACK;` if any batch fails or if the operator cancels. Alternatively supports per-batch commit for partial completion.
  - **Pre-execution Type Validation & Bounded Sizing**: validates integer, decimal, boolean, and JSON cells against table columns with row-named error reporting prior to database submission. Enforces NOT-NULL without default constraints and rejects unsupported data types (e.g. `VECTOR`). Input bounded to 64 MiB (`MAX_BULK_IMPORT_INPUT_BYTES`) and 100,000 rows (`MAX_BULK_IMPORT_ROWS`), with batch statements bounded to ≤512 KiB to safely stay within server `ValidateSQL` limits.
  - **Live Observability & Controls**: interactive field mapping with 5-row sample preview table, accessible progress bar (`role="progressbar"`), real-time throughput metrics (rows/sec, elapsed time, estimated time remaining), and dark monospace scrolling activity console.
  - **Operator Governance**: respects Studio read-only mode (blocks writes when active) and enforces an explicit confirmation checkbox when connected to a `production` server profile.
  - Full WCAG 2.2 AA accessibility verification in headless Chrome with axe.

- **`nextsql-bench` Result Viewer & Run Comparison in NextSQL Studio.**
  - **CLI JSON Output (`cmd/nextsql-bench`)**: added `-json` flag to `nextsql-bench` emitting versioned `nextsql-bench-report-v1` structured JSON suites across SLO, partition-pruning, read-scaling, quantised-vector, and standard SQL benchmarks. Captures hardware environment context, percentiles (P50, P95, P99, P99.9), memory allocations, WAL bytes, encryption overhead, and vector recall.
  - **Benchmark Viewer (`BenchmarkViewer.tsx`)**: accessible modal tool (`Benchmark viewer…` in Operations menu, Command Palette `Benchmark viewer & run comparison…`, and Recent Connections workspace tools) supporting both dual-run comparison and deep single-run inspection.
  - **Regression Detection & Markdown Export**: automatically detects latency (>5%), QPS (<-5%), and recall (>1%) regressions; displays side-by-side hardware context diffs; exports GitHub-formatted markdown comparison tables with summary metrics.
  - **Report Import & Fixture Management**: bundled baseline and candidate sample fixtures (`SAMPLE_BENCH_BASELINE`, `SAMPLE_BENCH_CANDIDATE`) plus file upload and paste JSON input with client-side validation and 8 MiB size bounds.
  - Full WCAG 2.2 AA accessibility verification in headless Chrome with axe.

- **NextSQL Studio Schema Diff & Migration Generator.**
  - **Schema Diff Explorer (`SchemaDiffExplorer.tsx`)**: accessible modal tool (`Schema diff…` menu item, Command Palette `Schema diff & migration…`, and Recent Connections workspace tools) that compares table schemas against another table in the database or against custom/pasted `CREATE TABLE` DDL. Inspects column additions, drops, and alterations (types, nullability, defaults, primary keys), secondary/unique/fulltext/vector/spatial index changes, and foreign key constraints.
  - **Safe Migration DDL Generation**: pure bounded helper (`resultTools.ts`) generates native NextSQL migration scripts into the active SQL editor for operator review (never auto-executing). Quoted identifiers, destructive `DROP` operations commented out by default with safety warnings, population failure warnings for `NOT NULL` without default, and manual type-conversion copy migration steps.
  - Full WCAG 2.2 AA accessibility verification in headless Chrome with axe.

- **NextSQL Studio Connection Explorer and Recent Connections & Projects home screen.**
  - **Connection Explorer (`ConnectionExplorer.tsx`)**: sidebar view accessible via an Explorer header toggle (`Tables` / `Connections`) and Command Palette (`Switch to Connection explorer`). Displays active connection details (host address, connected database, user, TLS/mTLS encryption posture, and read-consistency mode), operator-declared server profiles with quick-connect action, and recent connections history with relative timestamps and clear action.
  - **Recent Connections & Projects Modal (`RecentConnectionsModal.tsx`)**: accessible dialog (`Connections…` toolbar button, Command Palette `Connections & recent projects…`, or Connection Explorer) displaying active session overview with quick-switch and unsaved tab alerts, tabular recent connections with relative timestamps and Connect action, operator-declared server profiles grid, and project tools launchers (Saved Queries, Search Objects, Schema Designer, Data Importer, ER Diagram, Data Generator).
  - **Safe Client Persistence**: recent connections metadata (`profileId`, `name`, `address`, `database`, `user`, `environment`, `connectedAt`) is safely persisted in browser `localStorage` (`nextsql-studio-recents`), bounded to 10 entries (`MAX_RECENT_CONNECTIONS = 10`), strictly client-side with zero secret/token/password retention.
  - Full WCAG 2.2 AA accessibility verification in headless Chrome with axe.

### Fixed

- **Followers could serve a leader's uncommitted or rolled-back row versions, and a failover could make them committed.** A committed transaction's page image carries concurrent uncommitted changes on the same page into the replication stream. WAL `Undo` records now carry replaced version metadata (`wal.UndoBody` V2 format, `wal.UndoFlagV2`), replicated in batch version 3 (`replication.VersionUndo`). Engine logs undo for all write operations, and `RollbackTxn` appends and replicates `wal.RecAbort`. Followers register in-progress transactions on `wal.RecUndo`, install replicated undo records into local undo storage, track transaction lifecycles, and on `wal.RecAbort` reverse uncommitted leaf page changes directly in the buffer pool. On leader election/promotion, the new leader runs `SettlePredecessorTransactions()`, applying undo for all uncommitted predecessor transactions to buffer frames, marking them aborted in `txn.Manager`, and replicating `wal.RecAbort` to the cluster before opening for writes.

- **NextSQL Admin showed an inaccurate table-statistics row count.** `system.table_stats.row_count` and `system.index_stats.row_count` were the last `ANALYZE` snapshot, or 0 when statistics had never been collected. Both are now the number of rows visible to the statement, the same figure as `COUNT(*)`. On a single-database deployment the Databases tree also treats that database as the connected one, so its tables stay listed when the sign-in database label differs from the registry name.
- **`system.index_stats` reported its table's row count as the index's size.** A partial index was reported at its table's full row count — an index covering 3 of 8 rows read as 8 — and a full-text or vector index, whose entries are terms and graph nodes rather than rows, was reported the same way. The new `entry_count` column is what the index itself holds under the statement's snapshot, summed across partitions for a partitioned table, and is `NULL` for the index kinds whose entries are not one per row. The new `index_kind` column (`BTREE`, `UNIQUE`, `PARTIAL`, `UNIQUE PARTIAL`, `SPATIAL`, `SPATIAL PARTIAL`, `FULLTEXT`, `VECTOR`) says which case applies. `row_count` still reports the owning table.
- **Nothing showed that the planner was costing plans against stale statistics.** Making `row_count` live removed the only place the `ANALYZE` snapshot was visible, and that snapshot is what the optimizer actually plans from — on a freshly loaded table it estimated 330 rows where 1 matched. `system.table_stats` now reports `analyzed_rows` beside `row_count`, `NULL` until `ANALYZE` has ever run, so the two disagreeing is exactly the condition that warrants `ANALYZE`. NextSQL Admin's Maintenance view raises a warning naming the drifted tables and their two counts; a non-empty table that has never been analyzed counts as drifted, an empty one does not.
- **`system.storage` reported fabricated page and byte counts.** `page_count` was `len(tables) * 2 + 1` — a function of how many tables existed, not of storage — and `file_size` was that number multiplied by the page size. On a deployment holding 269,516,928 bytes it reported 148,032, understating the disk by more than 1,800x, and that figure is what NextSQL Admin's Overview and Databases views showed an operator sizing a volume. `page_count` is now the allocator's real high-water mark, the new `free_pages` column is its freelist, and `file_size` is the file's measured size on disk (`NULL` if the measurement fails, rather than a derived substitute). Both Admin views now summarize the row in readable units and distinguish the disk footprint from the live extent, which a preallocating deployment grows well beyond.
- **`wal_bytes_written` was always 0.** `metrics.Registry.AddWAL` existed and was documented as the cumulative WAL-append counter, but nothing ever called it, so the metric read 0 on every deployment no matter how much WAL had been written — including in NextSQL Admin's Diagnostics view. It is now incremented at the segment write, counting the prefix of a short or failed write too, since those bytes are on the volume either way. The Diagnostics storage section also states that the WAL gauges refresh on checkpoint and that `disk_total_bytes`/`disk_free_bytes` stay 0 until `disk_watermark_check_ms` is set, so a legitimate zero is no longer indistinguishable from a broken reading.
- **A dead nextsqld connection reported raw transport internals to the operator.** Every Admin read-model and action surfaced the underlying framing error verbatim — `protocol.WriteFrame: frame: write tcp 127.0.0.1:44496->127.0.0.1:7310: write: broken pipe` — which names an internal routine, carries both ends of the TCP connection including the ephemeral local port, and tells an operator nothing they can act on. Sign-in already collapsed this case to a clean message; every other path now does the same, and socket pairs are redacted from operator-facing error text generally, matching how addresses are handled everywhere else (`system.replication.leader_addr` is `[redacted]`). A server-side I/O fault still reports itself as one: the framing operation decides, not the error code, so a disk problem is not mislabelled as an unreachable server.
- **Studio reported the operator's own constraint violations as `502 Bad Gateway`.** `studioErrorStatus` defaults to 502 — correct for a genuinely upstream failure — but `already_exists` and `foreign_key` were never mapped, so a duplicate `INSERT` or an FK violation came back as a gateway error, pointing the operator at infrastructure over their own statement. Both are now `409 Conflict`, along with `unavailable` (which every other action handler already treated as 409) and `invalid_format` (now `400`).
- **Cancelling a Studio query reported `413 Request Entity Too Large`.** The engine reports a cancelled statement as `exhausted` — the same code a genuine memory bound uses — so pressing Cancel produced `nextsql exhausted: query cancelled` at 413. Admin creates and owns the context the Cancel route cancels, so it now relabels the result: a cancelled statement is `408` with `query canceled`, a statement that outran Studio's own limit says so and names the limit, and a real resource bound (memory budget, oversized SQL) still reports `413` unchanged. The Cancel route cancels only the statement's child context, never the handler's request context, which is why the handler could not see it and the session has to make the call.

### Changed

- **A cancelled statement now reports `ERR_CANCELED` instead of `ERR_EXHAUSTED`.** `scheduler.Budget` and `scheduler.Pool` reported cancellation with the same code as a memory or time bound, while `scheduler.Admission` and the protocol layer already reported `ERR_CANCELED` — two of four sites were the outliers, and the result was that a deliberate cancel could not be told apart from a resource limit. An exhausted *time* budget still reports `ERR_EXHAUSTED`, because a time budget is a resource. This is wire-visible: a client that matched `ERR_EXHAUSTED` to detect cancellation must match `ERR_CANCELED`. The `throughput.canceled` metric was counting `ERR_EXHAUSTED` as a cancellation to compensate, and no longer does, so it now reflects real cancellations only.
- **`system.SchemaVersion` is 5** (capability `system_schema_v5`). `system.storage` gained `free_pages`, `system.table_stats` gained `analyzed_rows`, and `system.index_stats` gained `index_kind` and `entry_count`, so `SELECT *` against any of the three returns more columns than on v4. The capability's `since_version` now tracks the constant rather than being a stale literal, which would otherwise have advertised the v5 contract as having shipped in 0.0.1.
- **Due schedules are now materialised without holding a task worker.** `TaskRuntime.cycle` acquired worker slots before doing any work and held them across both the claim and dispatch transactions, so turning a due schedule into a task row — which needs no worker, only catalog writes — was gated on worker availability. Dispatch now runs outside the slots, and only claiming (which takes a lease, and so must not outrun what can be run) still takes one. Slot acquisition also waits up to one poll interval instead of giving up immediately, so runtimes sharing a pool queue fairly on the channel rather than racing for it. `DispatchDueSchedules` already suppressed a duplicate while a non-terminal task for the same schedule was active, so dispatching earlier cannot pile tasks up.
- **`EXPLAIN ANALYZE` reported the configured worker ceiling as if it were measured parallelism, and copied wall time into the `cpu` column.** `workers` was `Limits.Workers`, so a plain `COUNT(*)` — which takes a serial fast path and never fans out — still claimed the full limit (8 on the default configuration), and a vector lookup or small join claimed it too. `cpu` was assigned `= time` at both sites that set it, so it carried no information at all and understated real CPU on a parallel step by up to the worker count. Both are now measured: `workers` is the fan-out a parallel step actually ran with (clamped to the number of tasks, `1` when nothing fanned out), and `cpu` is the summed time those tasks spent working, so it exceeds `time` exactly when the operator really ran in parallel.

---

## [0.0.4] — 2026-09-22

### Fixed

- **NextSQL Admin dropped a signed-in operator without a logout.** The session
  ended after 15 minutes without a click, or 12 hours after sign-in, and the
  connection to `nextsqld` was closed after about a minute with no traffic.
  The session and that connection now stay up until the operator logs out,
  switches server, or Admin stops. Operations mode also no longer closes the
  browser connection when a response takes longer than 30 seconds, so a
  backup or other long request can finish. `--idle-timeout` and
  `--session-lifetime` still apply when set to a positive duration; `0` (the
  default) sets neither.
- **NextSQL Admin printed an entire HTML application shell when an API request
  was misrouted.** The shared JSON client and the Studio query stream now ask
  explicitly for their media type and validate `Content-Type` before reading
  the body. An upstream Next.js or other web-app fallback is rejected with a
  short instruction to route `/api/v1/*` to `nextsql-admin`, rather than
  retaining and rendering the document as the table-inspector or query error.
  Structured JSON errors and 401 session handling are unchanged.
- **Studio's data grid could update the wrong table.** Editability followed
  the live editor buffer, so a ran selection, or a buffer edited after the
  rows arrived, wrote the displayed primary keys into whichever table the
  buffer's first `FROM` named. The grid now writes back only to the statement
  that produced the rows. A comma-join, a `FROM` inside a subquery, a second
  statement, and a quoted name cut at an embedded quote are not treated as a
  single-table result. Staged row identity no longer collides when a
  primary-key value contains `|`.

## [0.0.3] — 2026-09-20

### Fixed

- **`ANALYZE` failed on wide partitioned tables** with `record exceeds page
  capacity`. Each partition's local statistics were trimmed to a 15 KiB cap,
  but a catalog record holds only about 8 KiB, so a partitioned table with
  many long text values could not be analyzed at all. The earlier fix covered
  only the table-wide statistics. Local statistics are now trimmed to the real
  record limit; the dropped columns fall back to the table-wide statistics.
- **A crash could lose hundreds of acknowledged commits at once.** A commit
  copied its changed pages and wrote the copies to the WAL a moment later; in
  between, another transaction could change the same page and log its newer
  copy first. Recovery replays in log order, so it ended on the older copy:
  rows another transaction had committed were lost, together with any rows a
  B+Tree split had moved to new pages. It needed a flush slow enough to open
  the window, so it rarely showed on fast disks. A commit now logs its copies
  before any page can change again.
- **A crash could lose or change a committed row.** A logged page image carries
  every row version on its page, including other transactions' uncommitted
  ones, and undo records were buffered in memory. A commit could copy a page
  just after another transaction changed it, and a B+Tree split logged pages
  without writing the buffer at all, so after a crash redo installed a version
  whose undo record never reached disk: a committed row went missing, or a
  rolled-back transfer came back half applied. Undo records are now written
  before any image that carries their versions enters the WAL, and made
  durable before the WAL writes it: the WAL now fsyncs the undo log first
  whenever it holds unsynced records, so the guarantee holds across power
  loss, not only a process crash. This costs roughly one extra `fsync` per
  commit group (measured p50 on ext4: single-connection update 2.45 → 3.6 ms,
  16 connections 2.4 → 4.6 ms). A failed undo write or `fsync` now stops
  further commits until restart, as a failed WAL `fsync` already did.
- **Undo records appended after a power loss could be lost.** A partial record
  left at the end of the undo log was skipped on open but not removed, so new
  records were written past the point the next open stops reading. Open now
  cuts the partial record first.
- **A new Raft leader could deadlock its first write.** A follower elected with
  committed entries still waiting to be applied accepted writes at once; the
  write held the executor's apply guard while waiting on Raft, and Raft's apply
  of the earlier entry needed that guard exclusively. A new leader now accepts
  writes, and serves `STRONG` reads, only after a Raft barrier confirms it has
  applied everything committed before its term. A statement arriving in that
  window waits for it, bounded by the apply timeout, then fails `unavailable`
  and can be retried.

### Known issues

- **Followers can serve a leader's uncommitted or rolled-back row versions**,
  and a failover in that window can make them committed. A follower installs
  the leader's page images without the undo records for the uncommitted
  versions they carry. See `docs/production/GAPS.md`.

## [0.0.2] — 2026-09-19

### Fixed

- **`nextsqld` could not start again after an interrupted write to its own
  audit chain.** A half-written final line in `nextsql.audit` failed
  verification, and startup refused the whole file over it — under a
  container supervisor, an unbounded restart loop that took every dependent
  service down with it. A record is `fsync`ed before the chain head advances,
  so a torn final record was never acknowledged and can be dropped: startup
  now quarantines its bytes to a mode-`0600` `nextsql.audit.torn-<timestamp>`
  sibling, truncates only that record, and writes an `audit.torn_tail.repair`
  entry into the chain recording the offset, byte count and SHA-256 of what
  was removed. A final record whose newline alone was lost is repaired by
  appending the terminator, dropping nothing. Every other kind of damage — a
  sequence gap, a `prev_hash`/`hash` mismatch, a bad signature, anything on an
  earlier line — still refuses to start, and `nextsql audit verify` still
  reports a torn tail as unverified. `nextsql audit verify --json` gains an
  additive `torn_tail` boolean (every existing key is unchanged) so the two
  cases can be told apart. See `docs/security.md` "Interrupted final writes".
- **`system.capabilities.since_version` reported the running build's version**
  for 17 capabilities (mTLS, token credentials, the OIDC broker, the audit
  chain, follower reads, `REBUILD INDEX … ONLINE`, the vector index families
  and others) instead of the release they shipped in, so bumping the engine
  version would have relabelled all of them. They are pinned to `0.0.1`, where
  they shipped.

### Added

- **`wal_max_retained_mb` / `--wal-max-retained-mb`** bounds the WAL directory
  on a deployment that does not archive. Previously the only production prune
  path required `wal_archive`, so a single node that did not want PITR had
  nothing that ever reclaimed a WAL segment and the directory grew for the
  life of the instance. The cap is applied after each successful periodic
  checkpoint and during `MAINTAIN DATABASE`, removes only segments already
  below the redo LSN and every CDC retention pin — so it can be exceeded
  rather than delete history that is still needed — and is mutually exclusive
  with `wal_archive`. See `docs/wal.md` "Retention".
- **`wal_on_disk_bytes`, `wal_segments`, `wal_trimmed_segments`** in
  `system.metrics`. `wal_bytes_written` is cumulative and only rises; these
  report what the volume is actually holding, which is what shows an
  unconfigured deployment's WAL growing without bound.

---

## [0.0.1] — 2026-09-13

First public release.

NextSQL is a native, encrypted-by-default multimodel database written in Go.
Relational SQL, binary JSON, vector search, full-text search, and geospatial
types share one storage engine, one write-ahead log, one MVCC transaction
model, and one cost-based optimizer. It is not a PostgreSQL, MySQL, MongoDB,
or Elasticsearch compatibility layer.

### Engine

- Clustered B+Tree storage on 16 KiB pages, with secondary indexes, MVCC, and
  AES-256-GCM envelope encryption on by default.
- LSN-based WAL with group commit and `fsync` before a commit is acknowledged.
- Page-delta WAL redo: a commit logs only the bytes that changed on each page
  after the first full image. Measured over the wire on a default server:
  about 2.3–2.6 KB written per single-row insert, against 18–19 KB with full
  page images. New databases use deltas (WAL control file version 2). Existing
  databases keep full images unless `wal_page_deltas=on`.
- Crash recovery that replays committed work and undoes aborted work, including
  transactions whose rollback was logged but had not reached the data file.
- Buffer pool, UNDO, and checkpoints. A checkpoint redo boundary accounts for
  dirty pages and running transactions, so an acknowledged commit is not lost
  across a crash.
- Optional three-voter Raft cluster. A write is acknowledged only after the
  leader's local WAL flush and a quorum commit. SQL is not re-executed on
  followers.

### SQL

- Native dialect with required `PRIMARY KEY`, typed parameters (`$1`),
  `INSERT` / `UPDATE` / `DELETE` / `UPSERT`, `RETURNING`, `SELECT` with joins,
  `GROUP BY`, window functions, CTEs, subqueries, `UNION` / `INTERSECT` /
  `EXCEPT`, `IN`, `LIKE`, and `CAST`.
- `BOOL` is a declarable column type.
- `CHECK` constraints, `ALTER COLUMN`, views, savepoints, `INSERT INTO t <query>`,
  and `CREATE TABLE … AS <query>`.
- Native JSON (`NSJB`) with path extract and path indexes.
- Full-text `SEARCH` with BM25, language analyzers, prefix and fuzzy queries,
  `HIGHLIGHT` / `SNIPPET`, multi-field indexes, per-field `WEIGHT`, and `FACET`.
- `VECTOR<F32,N>` / `F16` / `I8`, `BITVECTOR`, `SPARSEVECTOR`, HNSW / IVF /
  IVF-PQ / sparse indexes, and `NEAREST`.
- WGS84 `POINT` / `BOX` / `LINESTRING` / `POLYGON`, plus `GEOMETRY` /
  `GEOGRAPHY`.
- Hybrid `SELECT`: filters, `SEARCH`, and `NEAREST` in one physical plan.
- `WORKFLOW` / `TRIGGER` / `SCHEDULE` / durable `TASK`, committed CDC, and
  `RANGE` / `HASH` / `LIST` partitioning.
- Virtual `system` catalog and `SHOW` aliases.

A bound parameter in `WHERE col = $1` uses the primary key or a matching
index. `ANALYZE` fits statistics to the catalog record so wide tables get
planner stats. Filtered sequential scans evaluate the predicate while
scanning, instead of loading the whole table into memory.

### Security and operations

- TLS 1.3 off loopback. Passwords and encryption keys are never accepted in a
  URL.
- Password auth (Argon2id), RBAC, optional mTLS, short-lived `NSSC1.`
  credentials, OIDC broker, and field-level client encryption (`NSCE1` /
  `NSCE2`) in the Go, Node, Bun, and PHP drivers.
- Encrypted backup, restore, PITR, and logical export/import. A backup or
  export is not valid until `verify` succeeds.
- `nextsql setup --profile production` writes fail-closed operational defaults.
- NextSQL Admin (`nextsql-admin`): Setup, Operations, and Studio on loopback.
  It is a protocol client — it never reads database files or bypasses RBAC.
  A profile switch is logged by from/to profile and user only; the log does
  not record whether the operator used a saved password.
- Published binaries do not record `golang.org/x/crypto`, so Docker Scout no
  longer reports GO-2026-5932 (unmaintained `openpgp`; no fix version exists).
  Argon2id and OCSP use copies of the v0.56.0 packages this tree already
  called. `NSCE2.` HKDF uses the standard library `crypto/hkdf`.

### Install and platforms

- Linux amd64 packages: `.deb`, `.tar.gz`, and `.run`, plus a Docker image
  (`bzynchub/nextsql:0.0.1`).
- Native Windows is not supported. On a Windows machine, run the Linux
  packages inside WSL 2. Keep the data directory on the Linux filesystem, not
  `/mnt/c`.
- macOS packages are not a supported path.

### Drivers

Official drivers speak NSQL v1. This release publishes them all at **0.0.1**,
matching the engine:

| Runtime | Package |
|---|---|
| Go | `github.com/bzync/nextsql/drivers/go` (Go module, engine tag) |
| Node.js 18+ | `@bzync/nextsql` |
| Bun | `drivers/bun` in this repository |
| PHP 8.1+ | `bzync/nextsql` |
| Python 3.10+ | `bzync-nextsql` |
| Ruby 3.0+ | `bzync-nextsql` |

### Notable correctness fixes in this cut

- Concurrent page splits are logged as their own committed system transactions,
  so acknowledged rows survive power loss under concurrent writers.
- Group commit: commits arriving during one fsync are made durable by the next,
  without weakening the durability rule.
- Deadlocks that formed after a lock changed hands are detected.
- A transaction keeps its admission slot from `BEGIN` until it ends, so lock
  holders can finish under contention.
- `INSERT` stores an explicit `NULL` instead of replacing it with the column
  default. Logical export/import compares every imported row to the dump.
- Cancelling a statement no longer breaks its TLS connection.
- Concurrent password hashes are gated, so unauthenticated login storms cannot
  drive the server out of memory.
- A nested statement large enough to crash the server is refused. `NOT` binds
  to the comparison it negates.
- Two concurrent writers can no longer both commit a write to the same row
  and have one write vanish.
- Live page repair refuses a broken delta chain instead of installing a stale
  page.

### Known limits

Hard limits and unimplemented SQL are listed in `docs/limits.md` and
[Limits](https://nextsql.bzync.com/docs/limits). Highlights:

- One database per `nextsqld` process.
- Outer `JOIN` with `SEARCH` / `NEAREST` is unsupported.
- IVF / IVF-PQ / `SPARSE` vector indexes are not available on partitioned
  tables.
- Native Windows is out of scope; use WSL 2.
- Studio mode is usable and still completing its MVP gate.

Sustained write throughput depends on the host disk. Official benches keep
encryption, WAL, fsync, checksums, MVCC, and authentication enabled.

---

## Changelog policy

A change is recorded here when it is implemented, tested, and documented — not
when it is only designed. Internal sequencing lives in `TODO.md`. Intended
product scope lives in `PROJECT.md`.

[Unreleased]: https://github.com/bzync/nextsql/compare/v0.0.5...HEAD
[0.0.5]: https://github.com/bzync/nextsql/releases/tag/v0.0.5
[0.0.4]: https://github.com/bzync/nextsql/releases/tag/v0.0.4
[0.0.3]: https://github.com/bzync/nextsql/releases/tag/v0.0.3
[0.0.2]: https://github.com/bzync/nextsql/releases/tag/v0.0.2
[0.0.1]: https://github.com/bzync/nextsql/releases/tag/v0.0.1
