# Changelog

All notable changes to NextSQL are recorded here.

The first public release is **0.0.1**. It is a preview: the engine is complete
enough to install, query, and operate, and it is still under measurement.
Before you rely on it, run `nextsql-bench --slo` and the crash, recovery, and
HA suites on your hardware.

Versioning follows [semver](https://semver.org/) for the engine tag (`vX.Y.Z`)
and the official driver packages. Do not reuse a published version for
different bits.

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

[Unreleased]: https://github.com/bzync/nextsql/compare/v0.0.1...HEAD
[0.0.1]: https://github.com/bzync/nextsql/releases/tag/v0.0.1
