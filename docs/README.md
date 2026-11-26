# NextSQL Documentation Index

This directory contains the authoritative technical specifications, operational manuals, release notes, and web documentation site ([`docs/web`](web)) for **NextSQL**.

For the interactive documentation site, visit **[nextsql.bzync.com/docs/introduction](https://nextsql.bzync.com/docs/introduction)** or run `make dev-docs` locally.

---

## 1. Installation & Containers

| Document | Description |
|---|---|
| [`install.md`](install.md) | Linux package installers (`.deb`, `.rpm`, `.tar.gz`, `.run`), WSL 2 setup, `nextsql setup`, `nextsql lifecycle`, and systemd service configuration |
| [`docker.md`](docker.md) | Official `bzynchub/nextsql` container image, first-start database initialization, volume layout, TLS, and running offline CLI commands |
| [`docker-compose.md`](docker-compose.md) | Single-node Compose, application stack integration, secrets management, and 3-node Raft HA Compose cluster |

---

## 2. SQL Dialect & Multimodel Capabilities

| Document | Description |
|---|---|
| [`sql.md`](sql.md) | Complete NextSQL dialect reference: scalar & collection types, DDL, DML, joins, CTEs, window functions, `EXPLAIN`, and resource groups |
| [`partitioning.md`](partitioning.md) | Native physical table partitioning (`RANGE`, `HASH`, `LIST` on 1–8 columns), tuple pruning, partition-local indexes, and zero-copy `ATTACH`/`DETACH` |
| [`json.md`](json.md) | Binary `NSJB` JSON storage format, path extraction (`col.a.b`), sargable path indexes, and document bounds |
| [`fulltext.md`](fulltext.md) | Inverted full-text indexes (`CREATE FULLTEXT INDEX`), BM25 ranking, language analyzers (`simple`, `english`, `french`, `german`, `spanish`), prefix/fuzzy/typo matching, `HIGHLIGHT`/`SNIPPET`, field `WEIGHT`, and `FACET` histograms |
| [`vector.md`](vector.md) | Dense (`VECTOR<F32|F16|I8,N>`), binary (`BITVECTOR<N>`), and sparse (`SPARSEVECTOR<N>`) vectors; `HNSW` (with optional F16/I8 traversal quantization), `IVF`, `IVF-PQ`, and `SPARSE` indexes; dense + sparse + BM25 RRF fusion |
| [`geo.md`](geo.md) | WGS84 `POINT`, `BOX`, `LINESTRING`, `POLYGON` plus `GEOMETRY` and `GEOGRAPHY` with SRID, WKT/EWKB/GeoJSON, haversine/Vincenty distances, and Morton Z-order spatial indexes |
| [`workflows.md`](workflows.md) | Native `WORKFLOW` procedures, synchronous row-level `TRIGGER` invocations, `SCHEDULE` (`EVERY`, `AT`, `CRON`), and durable `TASK` execution |
| [`cdc.md`](cdc.md) | Committed-only WAL change streams (`SUBSCRIBE TO table AFTER lsn`), `NSCD` v1 key/full images, `FlowAck` backpressure, and RBAC enforcement |

---

## 3. Storage, WAL, MVCC & Query Engine Internals

| Document | Description |
|---|---|
| [`storage-format.md`](storage-format.md) | 16 KiB logical / 16,448-byte physical pages, superblock (page 0), AES-256-GCM envelope, allocator & `FreelistConflicts` guard, `NSQI` page isolation, `NSCT` v1–v14 catalog descriptors, and online catalog compatibility |
| [`btree.md`](btree.md) | Primary superblock-rooted catalog B+Tree vs. detached `NSTM` user/index trees, 28-byte slot-0 node header, doubly-linked leaves, and `nextsql check-pages` structural invariants |
| [`wal.md`](wal.md) | `DomainWAL` encryption, `NSWL` framing and 13 record types, `PageDelta` v1 diffs, lock-free group commit, nested-top-action structural logging, durability latching, fuzzy checkpoints, and WAL retention |
| [`mvcc.md`](mvcc.md) | 30-byte `NSRV` row version headers (`xmin`, `xmax`, `undo_id`), `READ COMMITTED`/`SNAPSHOT`/`SERIALIZABLE` (strict 2PL), dirty-write prevention, FK probe-local snapshots, encrypted `undo.log`, and UNDO-before-WAL sync ordering |
| [`optimizer.md`](optimizer.md) | Deterministic cost-based optimizer, algebraic fixpoint rewrites, sargable access paths, bound-parameter substitution, `ANALYZE` statistics compaction, plan cache, and `EXPLAIN (FORMAT JSON)` |
| [`execution.md`](execution.md) | Columnar `vector.Batch` execution, bounded per-query `scheduler.Budget`, AES-256-GCM `NSPL` spill files, parallel & partition-wise operators, WAL-invalidated `SELECT` result cache, and durable mutation idempotency (`NSID`) |

---

## 4. Security, Encryption & Wire Protocol

| Document | Description |
|---|---|
| [`security.md`](security.md) | Key hierarchy (`root.key` → KEK → master → domain DEKs), Argon2id authentication, RBAC, TLS 1.3 & mTLS (CRL/OCSP), short-lived `NSSC1.` tokens, OIDC `nextsql-auth-broker`, `NSAC`/`NSAK` signed audit chains, and P25 security sign-off |
| [`client-encryption.md`](client-encryption.md) | Field-level client encryption: randomized AES-256-GCM (`NSCE1.`) and deterministic AES-256-SIV (`NSCE2.`) with `FileFieldKeyring` key rotation and revocation |
| [`protocol.md`](protocol.md) | Native **NSQL v1** binary wire protocol framing, handshake, authentication, streaming `DataBatch`/`FlowAck`, cancellation, and capability flags |
| [`error-codes.md`](error-codes.md) | Public `ERR_*` error code taxonomy, retry safety classification, and driver error surfaces |

---

## 5. Operations, High Availability, Backup & Admin

| Document | Description |
|---|---|
| [`ops.md`](ops.md) | Production deployment profile, `status`/`diagnose`/`check-pages`, admission control, timeouts, disk watermarks, `MAINTAIN`, `REBUILD INDEX ... ONLINE`, `nextsql-bench --slo`, and published hardware measurements |
| [`ha.md`](ha.md) | 3-node Raft clustering, quorum-synchronous commits, `STRONG`/`BOUNDED`/`STALE` follower reads, replica lag health, leader transfer, rolling upgrades, and linearizability sign-off |
| [`backup.md`](backup.md) | Encrypted physical backups (`NSBK` v1), restore verification, WAL archiving (`NSWA`), point-in-time recovery (`--until-lsn` / `--until`), and retention pruning |
| [`export.md`](export.md) | Logical schema and committed-row snapshots (`nextsql export` / `nextsql import`) |
| [`system-catalog.md`](system-catalog.md) | Read-only virtual `system.*` introspection schema and `SHOW` convenience aliases |
| [`limits.md`](limits.md) | Hard resource ceilings, wire limits, SQL bounds, and intentional scope exclusions |
| [`standards.md`](standards.md) | NextSQL-native design standards and non-goals |
| [`design-admin-ui.md`](design-admin-ui.md) | `nextsql-admin` unified architecture across Setup, Operations, and Studio modes |
| [`studio-sql-assistance.md`](studio-sql-assistance.md) | NextSQL Studio catalog-aware IntelliSense, inline parser/binder diagnostics, and quick-fix rewrites |
| [`release-notes/`](release-notes) | Per-version release notes (`0.0.1` through `0.0.10`) |
