# Changelog

All notable changes to NextSQL are recorded here.

The current release is **0.0.3**; **0.0.1** was the first public one. All are
previews: the engine is complete enough to install, query, and operate, and it
is still under measurement. Before you rely on it, run `nextsql-bench --slo`
and the crash, recovery, and HA suites on your hardware.

Versioning follows [semver](https://semver.org/) for the engine tag (`vX.Y.Z`)
and the official driver packages. Do not reuse a published version for
different bits.

---

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

[Unreleased]: https://github.com/bzync/nextsql/compare/v0.0.3...HEAD
[0.0.3]: https://github.com/bzync/nextsql/releases/tag/v0.0.3
[0.0.2]: https://github.com/bzync/nextsql/releases/tag/v0.0.2
[0.0.1]: https://github.com/bzync/nextsql/releases/tag/v0.0.1
