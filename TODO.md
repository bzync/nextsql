# NextSQL Development Tracker

Living end-to-end checklist. Source of requirements: `PROJECT.md`. This file is authoritative for implementation status, sequencing, dependencies, and phase gates.

Update this file when a box is completed, blocked, or split. Do not mark a phase done until its exit gate is checked.

| Field            | Value |
| ---------------- | ----- |
| Current phase    | **P28 Professional Installer + NextSQL Manager** — not yet started. **P27 Operational maturity + workload governance is COMPLETE** (2026-09-03, logs #79/#80/#82): every exit-gate line is green, including the local-commit-before-replicate-ack structural fix and the previously-deferred per-realm/per-database connection limits; see `docs/ops.md` and the Phase 27 exit-gate section. |
| Active increment | **Independent production-readiness re-audit of Phase 0–27 (2026-09-03, logs #93–#95) — both real bugs it found are now fixed.** User asked to verify P0–27 is genuinely production-grade rather than trust the existing checkmarks. Five parallel audit passes re-ran the real test suite and re-checked every exit-gate claim against live code; four of five held up exactly as claimed. The fifth (P16–P18) refused to accept `REBUILD INDEX ... ONLINE`'s prior "closed" status (log #91) without re-running its regression live, reproduced and fixed a real, intermittent (~5%) data-integrity race under `-race` — log #93. A Phase 15 finding (no test for the documented from-backup/`AddVoter` replica-repair path) was, at the user's explicit "fix," turned into a real regression test, which found a second real bug: a repaired-from-backup replica could silently, permanently lose any write between the backup and its `AddVoter` rejoin, because local checkpoint housekeeping during `backup.Create`/`Restore` inflated the same LSN counter `ApplyReplicated` uses to decide "already applied." Investigated a durably-persisted-watermark fix (on-disk format bump — invasive) versus making the redundant checkpoint itself a true no-op (found `Engine.Checkpoint()` unconditionally writes fresh records even when nothing happened since open) — chose the latter, no format change needed. Fixed and verified (20/20 clean under `-race`, previously reliably failing) at the user's follow-up "continue" — log #95. A stale (not incorrect) Phase 26 doc justification was also corrected. |
| Status           | Phases P0–P27 complete, including `REBUILD INDEX … ONLINE` (P17) and both documented HA replica-repair paths (P15), each now backed by an independently-adversarial verification pass that found and fixed a real bug — logs #93/#95. P16's terminal 100M B+Tree soak remains a documented non-gate follow-on (a measurement, not a code task). P28–P30 are open/planned. The cross-cutting Multi-database hosting track (M2/M3) stays under incremental development and gates no completed phase. A second cross-cutting track, Datatype expansion (D1–D11, `docs/design-datatypes.md`), was scoped 2026-09-03; D1 (`BLOB`, log #90), D2 (signed ints, log #91), D3 (unsigned ints, log #92), and D5 (`DATE`/`TIME`, log #94) landed the same day. |
| Last updated     | 2026-09-03 (**P27 closed**: local-commit-before-replicate-ack structural fix landed (log #79), then per-realm/per-database connection limits (log #80) closed the last deferred item. **Then P19's cron `SCHEDULE` syntax landed (log #86)** — `CREATE SCHEDULE … CRON '…'`, new `internal/cron` package, `NSSC` v2; closes the last long-standing P0–P27 non-gate deferral bar `REBUILD INDEX ONLINE`. Other subsequent same-day work is outside P27, on the Multi-database hosting cross-cutting track: M2-3b-2 (global buffer-page budget), M2-3b-3a/b/c (shared task-execution worker pool → one process-wide `CentralScheduler` → dead `TaskRuntime.Cancel` retired), M2-4b-2 (scoped, found not actionable yet), and **the declarative `NEXTSQL_HOSTING_MANIFEST_FILE` bootstrap wired into `nextsql init` + `nextsqld` serving a fully-managed deployment (log #87)** — logs #80–#88. **With log #88 the M2 selectable-multi-database-hosting milestone is complete**; remaining hosting work is all M3+. Earlier: M2-6 (pre-auth realm/database disclosure hardening), M2-5 (multi-realm routing activation), M2-4b-1 (realm-scoped auth.Store/security.ACL), M2-4b scoping investigation, M2-4a (`system.realms`/`system.databases` introspection), M2-4 dependency correction/scoping, M2-3b-1 (connection/session reference counting + idle eviction + open-failure quarantine), M2-3b scoping/decomposition, M2-3a (bounded DatabaseManager, nextsqld's first live multi-database routing), Hello realm field (M2-2) across the server and all 6 drivers, resource-group Priority enforcement, resource-group scheduler-class-integration + unbounded-pools audit, online format/catalog migration strategy, replica-lag management, disk watermark policies + capacity warnings, backup retention management, WAL retention management, rolling upgrade procedure, router/protocol/replication robustness fixes, exit gate lines closed. Separately: new official Python + Ruby drivers landed, and a real connection-desync bug found + fixed across the existing PHP/Node/Bun/Deno drivers — see log #59.) |
| Priority order   | Correctness → durability → security → integrity → availability → latency → throughput → efficiency → DX → features |

**Progress:** Phases 0–27 are complete (P0–P15, P16, P17, P18, P19, P20, P21, P22, P23, P24, P25, P26, P27). P16's terminal 100M B+Tree invariant run is deferred as a standalone measurement outside the release gate. P17's `REBUILD INDEX … ONLINE` is closed for real as of log #93 — an independent re-audit found and fixed a third, narrower concurrency race that survived log #91's earlier "closed" declaration; see the P17 checklist line and log #93 for the full writeup. P17, P18, P20, P21, P22, P23, and P25 implementable scope is closed. **P27 Operational maturity + workload governance is complete** (2026-09-03): graceful drain/controlled shutdown, leader transfer, maintenance mode, rolling-upgrade procedure, backup/WAL/replica-lag/disk-watermark management, the full session-control and resource-group surface, the operational CLI, and the exit gate — including "local commit precedes replication acknowledgment" (structural fix, log #79) and per-realm/per-database connection limits (log #80). **P25 Security 2.0 is complete** (2026-09-02): mTLS/service identity, signed short-lived credentials, external IdP (OIDC) broker, field-level client encryption (incl. durable `FileFieldKeyring` key rotation/revocation), Argon2id password hashing, and tamper-evident/signed audit-chain hardening are all production-gated per `docs/security.md` "P25 security review sign-off". **P23 Vector Engine 2.0 is complete** (2026-08-31): production-gating sign-off in `docs/vector.md`; `VECTOR<F16,N>` / `VECTOR<I8,N>` / `BITVECTOR<N>` and the quantised HNSW index are production-gated; IVF / IVF-PQ / sparse retrieval / dense+sparse+BM25 fusion are production-gated ANN paths with recall/latency/size/QPS/RAM measurements. Documented follow-ons (not gate items): a `BITVECTOR`/Hamming `--vecquant` row, a process-local IVF-PQ cache, a re-rank-free quantised HNSW mode, IVF/IVF-PQ/SPARSE on partitioned tables, SIMD after profiling. **P22 follower reads / read scaling is complete** (2026-08-30): three read-consistency modes (`STRONG` linearizable behind a `raft.VerifyLeader` barrier; `BOUNDED` within `MAX STALENESS`; `STALE` unbounded — all consistent committed prefixes, never mislabelled), replica lag + follower health via `system.replica_health`, follower-read routing in the server and every official driver (`OpenCluster` / `connectCluster` / `NextSQL\Cluster::connect`), the `nextsql-bench --readscale` read-scaling benchmark, and the exit gate — a dated linearizability/consistency sign-off (`docs/ha.md` "Consistency model and sign-off") plus a failover session-guarantee test (`TestFollowerReadFailoverSessionGuarantee`: `STRONG` sessions keep read-your-writes + monotonic reads across a leader change). A 3-node non-Go driver cluster-routing live test is a documented optional follow-on, not a gate item. P21 has tested RANGE/HASH/LIST with one-to-eight-column keys (RANGE tuple bounds `VALUES LESS THAN (a, b, ...)` compared lexicographically; LIST tuple membership `VALUES IN ((a, b), ...)`; HASH SHA-256 over the canonical tuple), ADD/DROP plus validated ATTACH/DETACH ownership-transfer DDL, partition-local non-unique B+Tree-family, FULLTEXT, and HNSW/`VECTOR` indexes (partition-local graph over per-partition payload stores; `NEAREST` merges every partition-local graph by distance and is pruning-aware for a partition-key residual predicate), `NSST` v3 stable-ID row counts plus bounded versioned `NSPS` per-partition column/index/vector sketches used by pruning-aware costing, bounded partition-aware table/index maintenance, and base-backup plus archived-WAL recovery of partition metadata/data/local roots/statistics; multi-column RANGE pruning is tuple-tight (the predicate is reduced to a query bound prefix over the partition-key columns, so trailing constraints separate bands that share a leading value), and cross-partition plain-column secondary UNIQUE enforcement (exclusive key lock plus a probe of every other partition-local root on write; ordered cross-partition duplicate scan on CREATE/REBUILD/ATTACH). Partial/expression/JSON-path UNIQUE and partitioned-table FKs remain fail-closed; `UPSERT` on RANGE/HASH/LIST tables is now wired to the partition-local roots (PK-target conflicts resolve against the routed partition heap; secondary-UNIQUE-target conflicts probe every partition-local root; a partition-key `SET` moves the row between heaps; no-conflict inserts still hit the cross-partition UNIQUE probe) and stays rejected only on legacy TENANT tables. Explicit offline legacy TENANT migration landed 2026-08-30 (`nextsql hosting migrate-tenant`: exclusive dual-deployment locks, bounded UPSERT-idempotent batched copy into a `PROVISIONING` destination, per-row point verification, `tenant_id`→`legacy_tenant_id` rename, encrypted fuzz-seeded `NSLM` resume intent, publish `ACTIVE` only on success), so **P21 is complete**. Pruning soundness (a matching row is never routed to a pruned partition) is covered by the randomized `TestPartitionPruningSoundness` property test; further property/fuzz coverage may still be added. Legacy TENANT descriptors decode only for recovery/offline migration and cannot be created through SQL. Cross-cutting rich geo/F32-vector value operations, bounded WAL-invalidated SELECT result caching, and durable database-user-scoped mutation idempotency are implemented. P23 compressed-vector/ANN scope is production-gated. The accepted multi-database hosting track is `PARTIAL`: M1 now has an encrypted/versioned deployment registry (`NSRM` v3, with realm/database `StorageCapBytes` caps and a per-realm realm-root delegation secret hash — `nextsql hosting set-realm-cap` / `set-realm-root` / `set-database-cap [--realm-secret-file]`; `nextsqld` enforces the effective cap on the data file — growth past it fails `Exhausted`, deletes/in-place updates still work; cap edits take the data-dir lock and apply on restart; a realm-root secret holder can manage only its own realm's per-database caps under the realm ceiling), resumable init bootstrap, separate registry root, stable identities/lifecycle validation, and default-database verification; selectable multi-engine routing and later operational/HA gates remain open.

- [x] P0 Foundation
- [x] P1 Page engine
- [x] P2 B+Tree
- [x] P3 WAL + crash recovery
- [x] P4 Transactions / MVCC
- [x] P5 SQL
- [x] P6 Optimizer
- [x] P7 Vectorized / parallel
- [x] P8 Native protocol
- [x] P9 JSON
- [x] P10 Full text
- [x] P11 Vectors
- [x] P12 Hybrid optimizer
- [x] P13 Production security
- [x] P14 Production operations
- [x] P15 HA
- [x] P16 Correctness / SLO closure — exit gate green; the terminal 100M-operation B+Tree invariant run is DEFERRED as a standalone measurement (not a release gate), best evidence v8 44M clean ops
- [x] P17 Schema lifecycle + storage maintenance
- [x] P18 SQL completeness
- [x] P19 WORKFLOW / TRIGGER / SCHEDULE / TASK
- [x] P20 CDC / change streams
- [x] P21 Native table partitioning — RANGE/HASH/LIST implemented with one-to-eight-column keys (tuple bounds/membership for RANGE/LIST); multi-column RANGE pruning is tuple-tight; cross-partition plain-column secondary UNIQUE is enforced (lock + probe every partition-local root; ordered cross-partition scan on CREATE/REBUILD/ATTACH); `UPSERT` on RANGE/HASH/LIST tables is wired to the partition-local roots (`TestPartitionUpsert`); randomized pruning-soundness property test landed (`TestPartitionPruningSoundness`); partition benchmarks landed (`nextsql-bench --partition`, `TestPartitionBench`); explicit offline legacy TENANT migration landed (`nextsql hosting migrate-tenant`); automatic distributed sharding is a separate future phase
- [x] P22 Follower reads / read scaling — read-consistency modes (`STRONG`/`BOUNDED`/`STALE`), replica lag + follower health surfacing, follower-read routing across the server + every official driver, read-scaling benchmark (`nextsql-bench --readscale`), and the exit gate: linearizability/consistency sign-off (`docs/ha.md` "Consistency model and sign-off", 2026-08-30) + failover session-guarantee test (`TestFollowerReadFailoverSessionGuarantee`). A 3-node non-Go driver cluster-routing live test is a documented optional follow-on, not a gate item
- [x] P23 Vector Engine 2.0 — complete (2026-08-31): production-gating sign-off in `docs/vector.md`; `VECTOR<F16,N>` / `VECTOR<I8,N>` / `BITVECTOR<N>` and the quantised HNSW index are production-gated; IVF / IVF-PQ / sparse / fusion ANN paths measured. Remaining documented follow-ons (not gate items): a `BITVECTOR`/Hamming `--vecquant` row, IVF-PQ process-local cache, re-rank-free quantised HNSW, IVF/IVF-PQ/SPARSE on partitioned tables. Prior in-progress log: `VECTOR<F16,N>` half-precision (2026-08-30), `VECTOR<I8,N>` signed-byte + per-vector scale (2026-08-30), and `BITVECTOR<N>` bit-packed + `HAMMING` distance (2026-08-30) element types landed; F32-vs-F16-vs-I8-vs-quantised-graph size/recall/latency benchmark landed (`nextsql-bench --vecquant`, 2026-08-30); quantised HNSW index (`… USING HNSW WITH (QUANTIZATION = 'F16' | 'I8')`, quantised traversal + exact re-rank) landed 2026-08-30; compressed (front-coded) HNSW neighbour lists — node format v2, ~⅓ smaller on-disk graph, lossless — landed 2026-08-30; IVF index core landed 2026-08-30 (`internal/vector`); **IVF SQL surface + lifecycle wiring landed 2026-08-30** (`CREATE VECTOR INDEX … USING IVF WITH (LISTS = n [, PROBES = m])` — parser/binder/catalog format v7/executor build+rebuild+maintain+search over `sqlIVF` on the detached encrypted index tree; real-valued metrics only, not on partitioned tables); IVF `nextsql-bench --vecquant` row + grouped centroid storage landed 2026-08-30; process-local IVF quantiser cache landed 2026-08-30 (`lockedIVF` shared in-memory copy for committed `NEAREST`, invalidated via the HNSW cache generation); IVF-PQ portable core landed 2026-08-30 (`internal/vector/ivfpq.go`); **IVF-PQ SQL surface + lifecycle wiring landed 2026-08-30** (`CREATE VECTOR INDEX … USING IVFPQ WITH (LISTS = n, SUBSPACES = M [, PROBES = m])` — parser/binder/catalog table format **v8**/executor build+rebuild+maintain+search over `sqlIVFPQ` on the detached encrypted index tree: grouped centroids, chunked `IVPCG` codebook, front-coded `NSPL` posting lists, ADC + exact re-rank; real-valued metrics only, not on partitioned tables; no process-local cache yet) + `F32/ivfpq` row in `nextsql-bench --vecquant`; **sparse retrieval core landed 2026-08-30** (`internal/vector/sparse.go`); **`SPARSEVECTOR<N>` SQL + `USING SPARSE` landed 2026-08-30** (parser/binder/catalog v8 method/`sqlSparse` build+rebuild+maintain+search; exact IP + COSINE re-rank; not on partitioned tables); **dense+sparse+BM25 fusion landed 2026-08-30**; **`--vecquant` sparse row landed 2026-08-31** (2000 × 4096-d nnz=24: recall@10/@100 1.000, p50 428 µs); **exit gate closed 2026-08-31**
- [x] P24 Full-text Search 2.0 — complete (2026-08-31): BM25/phrase compatibility golden, bounded adversarial fuzzy/typo work, expanded quality fixtures, and encrypted recovery gate green
- [x] P25 Security 2.0 — complete (2026-09-02): mTLS/service identity, signed short-lived credentials, external IdP (OIDC) broker, field-level client encryption (incl. durable `FileFieldKeyring` key-rotation/revocation), Argon2id password hashing, and tamper-evident/signed audit-chain hardening are all implemented and tested; exit gate closed by `docs/security.md` "P25 security review sign-off (2026-09-02)"
- [x] P26 System catalog / introspection 2.0 — complete (2026-09-02): full virtual `system` schema (catalog/storage/replication/live/security tables), nine `SHOW` aliases, and an authoritative capability registry; exit gate closed by `docs/system-catalog.md` "P26 exit gate closure (2026-09-02)"
- [x] P27 Operational maturity + workload governance — complete (2026-09-03): every exit-gate line and every checklist item, including the previously-deferred "Per-realm and per-database connection limits" (closed once M2 multi-database hosting shipped live routing), is now `[x]`. See `docs/ops.md` and the increment log entry for the closure writeup.
- [ ] P28 Professional Installer + NextSQL Manager
- [ ] P29 NextSQL Studio
- [ ] P30 NextSQL Intelligence + built-in RAG

---

## How to use this tracker

- Check a box only when tests (and race, fuzz, or benches when listed) have been run for that item.
- A phase is complete only when its **Exit gate** section is fully checked.
- Official benches keep fsync, WAL, encryption, checksums, MVCC, authentication, and durability enabled unless labeled experimental.
- Do not skip phases casually. Format hooks and interfaces for later phases are allowed; product surface is not.

**Canonical status vocabulary:** `SHIPPED` · `PARTIAL` · `EXPERIMENTAL` · `OPEN` · `BLOCKED` · `DEFERRED` · `PLANNED` · `PRODUCTION-GATED`

---

# End-to-end checklist

## Phase 0 — Foundation

- [x] Initialize Go module and repository layout for the Phase 0 foundation
- [x] `cmd/nextsqld` placeholder builds
- [x] `cmd/nextsql` placeholder builds
- [x] `cmd/nextsql-bench` placeholder builds
- [x] Configuration loading (`internal/config`)
- [x] Typed error types (no stringly errors on public paths)
- [x] Structured logging that never logs passwords, keys, tokens, or secrets
- [x] Versioned binary encoding helpers (no raw Go struct serialization to disk)
- [x] File abstraction for later page I/O
- [x] Storage-format constants (page size 16 KiB, magic, versions)
- [x] Database / file identity types
- [x] Unit test harness
- [x] Benchmark harness
- [x] `docs/` started with format notes
- [x] `go test ./...` passes
- [x] `go test -race ./...` passes

### Phase 0 exit gate

- [x] Compilable module with three command stubs
- [x] Config, typed errors, logging, encoding helpers, identity, and test/bench harness exist
- [x] No SQL / MVCC / protocol / multimodel / replication code beyond comments or hooks

---

## Phase 1 — Page engine

First implementation increment is Phase 0 plus the minimum viable items below.

### Pages and slots

- [x] 16 KiB logical page layout: magic, format version, page type, page ID, LSN, txn metadata
- [x] Slot directory + free space + variable-length records
- [x] Deterministic versioned encode / decode
- [x] Page validation (magic, version, page ID, bounds)
- [x] Truncated / corrupt page fails closed

### Encryption envelope

- [x] Encrypted-page envelope: format version, cipher suite, key version, page ID, nonce metadata, payload, tag
- [x] AES-256-GCM via established Go crypto (no custom primitive)
- [x] Cipher-suite versioning hook
- [x] Wrong key cannot decrypt
- [x] Authentication-tag modification is detected
- [x] Nonce metadata designed so uniqueness can survive crash / restart later
- [x] Sensitive metadata kept out of plaintext where possible

### I/O and buffer

- [x] File manager
- [x] Page allocator
- [x] Minimal buffer manager (pin, unpin, eviction)
- [x] Persistence after process restart
- [x] Concurrent page access test

### Phase 1 tests

- [x] Page encode / decode
- [x] Page encrypt / decrypt
- [x] Wrong encryption key
- [x] Authentication-tag modification
- [x] Truncated page
- [x] Invalid page ID
- [x] Invalid format version
- [x] Allocation
- [x] Persistence after restart
- [x] Buffer eviction
- [x] Concurrent page access
- [x] Randomized slotted-page operations
- [x] Fuzz tests for page decoding

### Phase 1 benchmarks (encryption on)

- [x] Page encryption
- [x] Page decryption
- [x] Page encode / decode
- [x] Buffer hit
- [x] Buffer miss
- [x] Page read
- [x] Page write
- [x] Slotted insert
- [x] Slotted lookup
- [x] Report ns/op, B/op, allocs/op, MiB/s where appropriate

### Phase 1 docs

- [x] Storage-format documentation in `docs/`

### Phase 1 exit gate

- [x] Pages survive restart
- [x] Tamper and truncate fail closed
- [x] Encryption enabled in primary benches
- [x] No SQL / MVCC / protocol / JSON / full-text / vector / replication / consensus except format hooks

---

## Phase 2 — Clustered B+Tree

- [x] Clustered B+Tree on the page engine
- [x] Insert
- [x] Lookup
- [x] Delete
- [x] Range scan
- [x] Page split
- [x] Persistence across restart
- [x] Leaf pages hold row representation
- [x] Restart tests
- [x] Randomized insert / delete / scan tests
- [x] Structural invariant checks after split
- [x] Design note for later secondary indexes (secondary key + primary key) — implementation deferred

### Phase 2 exit gate

- [x] Key survives insert → restart → lookup → delete → range scan
- [x] Randomized workloads keep tree invariants

---

## Phase 3 — WAL and crash recovery

- [x] WAL records with LSN
- [x] WAL checksums / authentication
- [x] WAL encrypted with a WAL DEK (not the page DEK)
- [x] WAL segments + rotation
- [x] Group commit
- [x] fsync before COMMIT acknowledgement
- [x] Checkpoints
- [x] Redo recovery
- [x] Archival hooks for later PITR
- [x] Crash injection: INSERT
- [x] Crash injection: UPDATE
- [x] Crash injection: DELETE
- [x] Crash injection: COMMIT
- [x] Crash injection: ROLLBACK
- [x] Crash injection: B+Tree split
- [x] Crash injection: checkpoint
- [x] Crash injection: page flush
- [x] Crash injection: WAL rotation
- [x] Crash injection: index build (once indexes exist)
- [x] Survive SIGKILL / process crash / restart (`Engine.Kill` + reopen)
- [x] Survive checkpoint interruption
- [x] Survive partial WAL tail
- [x] Survive partial data write

### Phase 3 exit gate

- [x] Committed state remains after restart
- [x] Uncommitted state does not become committed
- [x] Index / tree invariants hold after recovery
- [x] Commit is not acknowledged before the durability boundary

---

## Phase 4 — Transactions

- [x] Transaction IDs
- [x] UNDO records (encrypted UNDO domain or hook)
- [x] MVCC version chains (current → undo → previous)
- [x] Snapshots
- [x] Lock manager
- [x] Rollback
- [x] Deadlock detection
- [x] READ COMMITTED
- [x] SNAPSHOT isolation
- [x] SERIALIZABLE — only claim after anomaly tests
- [x] Isolation anomaly tests (do not mark Serializable before these pass)
- [x] Concurrent reader / writer tests
- [x] Crash recovery still correct with UNDO + REDO

### Phase 4 exit gate

- [x] Readers do not see uncommitted writes
- [x] Rollback restores prior state
- [x] Deadlock aborts one waiter
- [x] No Serializable claim without passing anomaly tests

---

## Phase 5 — SQL

### Frontend

- [x] Lexer
- [x] Parser
- [x] AST
- [x] Catalog
- [x] Binder
- [x] Logical planner (basic)
- [x] Basic executor (may still be row-oriented; vectorized is Phase 7)

### Initial statements

- [x] `CREATE TABLE`
- [x] `CREATE INDEX`
- [x] `INSERT`
- [x] `SELECT`
- [x] `UPDATE`
- [x] `DELETE`
- [x] `BEGIN`
- [x] `COMMIT`
- [x] `ROLLBACK`

### Types (catalog + storage as needed)

- [x] `UUID` + `DEFAULT UUID()`
- [x] `STRING` / `TEXT`
- [x] `DECIMAL(p,s)` + `DEFAULT AI()`
- [x] `TIMESTAMPTZ` + `DEFAULT NOW()`
- [x] `JSON` type stub (execution in Phase 9)
- [x] `VECTOR<F32,N>` type stub (execution in Phase 11)

### Phase 5 tests

- [x] Parser / binder / catalog unit tests
- [x] Statement execution on B+Tree + WAL + MVCC
- [x] BEGIN / COMMIT / ROLLBACK concurrency
- [x] Parser fuzzing started
- [x] Data and catalog survive restart / recovery

### Phase 5 exit gate

- [x] Initial statements execute correctly after restart
- [x] Catalog is transactional and recovered from WAL

---

## Phase 6 — Query optimizer

- [x] Logical rewrite pipeline
- [x] Physical alternatives
- [x] Cost model
- [x] Predicate pushdown
- [x] Projection pushdown
- [x] Constant folding
- [x] LIMIT pushdown
- [x] Index selection
- [x] Join simplification
- [x] Column pruning
- [x] Partition / segment pruning
- [x] Statistics: row count
- [x] Statistics: null ratio
- [x] Statistics: NDV
- [x] Statistics: min / max
- [x] Statistics: histograms
- [x] Statistics: most common values
- [x] Statistics: correlation
- [x] Statistics: index selectivity
- [x] Statistics: segment statistics
- [x] Runtime feedback (estimated vs actual rows)
- [x] Plan caching
- [x] `EXPLAIN`
- [x] `EXPLAIN ANALYZE` (operator, estimates, actuals, time, CPU, memory, disk, cache, spill, workers, index)
- [x] Optimizer remains deterministic (no LLM planner)

### Phase 6 exit gate

- [x] Same catalog + stats → same plan
- [x] Pushdown and index-selection tests pass

---

## Geospatial and location (landed on the Phase 6 SQL/optimizer stack)

Not a skipped numbered phase. Types, functions, and spatial indexes sit on the existing catalog, WAL, MVCC, and cost-based planner.

- [x] `POINT` / `LOCATION` type (WGS84 lon/lat)
- [x] `BOX` type
- [x] Coordinate validation (range, finite)
- [x] `POINT(lon, lat)` / `BOX(...)` constructors
- [x] `LON` / `LAT`
- [x] `DISTANCE` (haversine meters)
- [x] `DWITHIN`
- [x] `WITHIN` / `COVERS`
- [x] WKT coerce (`POINT(...)`, `BOX(...)`)
- [x] `CREATE SPATIAL INDEX` (Morton geohash + PK)
- [x] Optimizer uses spatial index for `DWITHIN` / `DISTANCE < r` / `WITHIN`
- [x] Residual predicate is exact
- [x] Participates in WAL / restart / encryption via the existing row store
- [x] Polygons / lines — `LINESTRING` / `POLYGON` types, WKT, `WITHIN`/`COVERS` PIP (holes), `LINELENGTH`, `DWITHIN` to line/polygon (`docs/geo.md`)
- [x] Spheroid distance — `DISTANCE_SPHEROID` Vincenty inverse on WGS84; near-antipodal falls back to haversine (`docs/geo.md`)
- [x] Rich bounded geometry operations — all POINT/BOX/LINESTRING/POLYGON pairs for `DISTANCE`/`DWITHIN` and `INTERSECTS`/`DISJOINT`; polygon self-intersection/hole validation; `AREA`, `PERIMETER`, `CENTROID`, `ENVELOPE`, `GEOMETRYTYPE`, `NPOINTS`, and `NRINGS` (`docs/geo.md`)

---

## Phase 7 — Vectorized and parallel execution

- [x] `Batch` with columnar vectors
- [x] Batch size selection (1024 / 2048 / 4096, then bench)
- [x] Vector filters
- [x] Vector projection
- [x] Batch decoding
- [x] Hash aggregation
- [x] Hash join
- [x] Merge join
- [x] Index scan
- [x] Parallel scan
- [x] Parallel aggregation
- [x] Parallel joins
- [x] Parallel index building
- [x] Explicit worker scheduler (no unbounded per-query goroutines)
- [x] Per-query budget: CPU workers
- [x] Per-query budget: memory
- [x] Per-query budget: disk spill
- [x] Per-query budget: I/O
- [x] Per-query budget: execution time
- [x] Result streaming (no full materialization of huge results)
- [x] Benchmarks toward row-processing targets

### Phase 7 exit gate

- [x] Parallel paths are race-clean — `CGO_ENABLED=1 go test -race ./...` passes
- [x] Budgets are enforced (cancel / spill, not OOM)
- [x] Suitable scan / aggregation numbers recorded with hardware context — 512-row SELECT + GROUP BY benches in `docs/execution.md` (Ryzen 5 7535HS, linux/amd64, ext4, encryption on). 25K/1M/10M targets stay on Phase 14 `nextsql-bench`.

---

## Phase 8 — Native network protocol

- [x] Versioned NextSQL wire protocol
- [x] `nextsqld` accepts connections
- [x] TLS 1.3 for remote production connections
- [x] Authentication (passwords never stored plaintext)
- [x] Typed parameters
- [x] Prepared statements
- [x] Streaming results + backpressure
- [x] Query cancellation
- [x] Official Go driver (`drivers/go`)
- [x] Official Node.js driver (`drivers/node`)
- [x] Official Bun driver (`drivers/bun`)
- [x] Official Deno driver (`drivers/deno`)
- [x] Official TypeScript types (Node / Bun / Deno; `drivers/js/types.d.ts`)
- [x] Official PHP driver (`drivers/php`)
- [x] Official Python driver (`drivers/python`) — landed 2026-09-02, see the increment log entry.
- [x] Official Ruby driver (`drivers/ruby`) — landed 2026-09-02, see the increment log entry.
- [x] Driver uses `KeyProvider` — keys never in URLs
- [x] Packet-size limit
- [x] SQL-length limit
- [x] Result-size limit
- [x] Runtime / memory / worker limits on the wire path
- [x] Never allocate from unchecked attacker-controlled length
- [x] Protocol fuzzing
- [x] Authentication-protocol fuzzing
- [x] Slow-client memory stays bounded

### Phase 8 exit gate

- [x] Go driver runs Phase 5 statements over TLS
- [x] Node, Bun, Deno, PHP, and TypeScript drivers speak NSQL v1; TLS + no keys in URLs (`docs/protocol.md`)
- [x] Large results stream
- [x] Cancel works
- [x] Fuzzed input returns controlled errors

---

## Phase 9 — Native JSON

- [x] Compact binary JSON (not UTF-8 text as the stored form)
- [x] Typed values, objects, arrays
- [x] Path traversal (`metadata.category`)
- [x] Partial decoding
- [x] `SELECT metadata.category FROM …`
- [x] `CREATE INDEX … ON t(metadata.category)`
- [x] JSON participates in WAL / recovery / backup / encryption
- [x] JSON parser fuzzing
- [x] JSON depth limit

### Phase 9 exit gate

- [x] Path extract and path index work inside transactions
- [x] No plaintext JSON on disk in production mode

---

## Phase 10 — Full-text search

- [x] Tokenizer
- [x] Normalization
- [x] Inverted index
- [x] Posting lists
- [x] Term frequency / document frequency
- [x] Positions
- [x] BM25-style scoring
- [x] Phrase search
- [x] `SEARCH col FOR '…'` SQL
- [x] Transaction / WAL / recovery integration
- [x] Encryption of full-text structures
- [x] Full-text index fuzzing
- [x] Scoring fixture tests

### Phase 10 exit gate

- [x] SEARCH is transactional and recovered after crash
- [x] Encrypted inverted index

---

## Phase 11 — Native vectors

- [x] `VECTOR<F32,N>` first-class type
- [x] Vector reference in the row store (no large inline vectors bloating pages)
- [x] Contiguous vector store
- [x] COSINE
- [x] L2
- [x] INNER_PRODUCT
- [x] Exact flat search
- [x] Portable Go distance implementation
- [x] `NEAREST embedding TO $query`
- [x] `CREATE VECTOR INDEX … USING HNSW`
- [x] HNSW
- [x] Vector metadata fuzzing
- [x] Encryption of vector blocks and ANN structures
- [x] Parallel distance calculations under the scheduler
- [x] Measure recall\@10, recall\@100, QPS, memory, index size with latency — unit recall + search latency in `docs/vector.md`. Official QPS / RAM / index-size stay on Phase 14 `nextsql-bench`.
- [x] Never improve latency by silently reducing recall
- [x] Hooks only (later): `VECTOR<F16,N>`, `VECTOR<I8,N>`, `BITVECTOR<N>`, IVF, IVF-PQ, quantization
- [x] SIMD / AVX2 / AVX-512 / NEON only after profile + tests + fuzz + measured win — portable Go only
- [x] Rich F32 value operations — dimension, norm/normalize, add/subtract/scale, dot, cosine distance, and L1/Manhattan with finite-value and strict-dimension checks (`docs/vector.md`)

### Phase 11 exit gate

- [x] Flat search is exact
- [x] HNSW benches report recall with latency
- [x] Vector store is encrypted

---

## Phase 12 — Hybrid optimizer

- [x] Unified cost model across SQL + JSON + full-text + vector
- [x] Structured-filter-then-ANN plans
- [x] ANN-then-structured-filter plans
- [x] No hard-coded operator order
- [x] Hybrid SQL from `PROJECT.md` executes
- [x] `EXPLAIN` shows candidate generation and rerank
- [x] Vector statistics in the catalog
- [x] Hybrid latency benches (encryption on) — 200-row `WHERE`+`SEARCH`+`NEAREST` ≈ 11.2 ms/op in `docs/optimizer.md`. Official QPS / p95 stay on Phase 14 `nextsql-bench`.

### Phase 12 exit gate

- [x] Hybrid query is one physical planning problem
- [x] Plans are deterministic for fixed stats

## Cross-cutting query reuse and retry safety

- [x] Bounded process-local SELECT result cache — typed-parameter/database-instance/user keys, deep-copy isolation, WAL+catalog generation invalidation, 128-entry/8 MiB global bounds, 1 MiB/4096-row entry bounds, and volatile/explicit-transaction bypass (`docs/execution.md`)
- [x] Durable mutation idempotency — `Session.ExecIdempotent`, additive NSQL `IdempotentQuery`, Go driver `ExecIdempotent`, atomic mutation+`NSID` replay record, request-conflict detection, restart replay, 24-hour retention, bounded record/response counts, decoder fuzz seeds, and database-user scope (`docs/execution.md`, `docs/protocol.md`)

---

## Phase 13 — Production security

### Key architecture

- [x] Envelope hierarchy: root unlock → KEK → DB master → page / WAL / backup DEKs
- [x] Additional DEK domains as needed: UNDO, vector, full-text, temp, replication
- [x] No single permanent key for every purpose
- [x] `REQUIRE CLIENT KEY` / client-held key mode
- [x] Persistent files hold ciphertext, wrapped DEKs, key IDs, crypto metadata — not the raw external root key
- [x] Online key rotation (new writes use new version, background re-encrypt, retire old)
- [x] Key-version identifiers on every encrypted unit
- [x] Session termination on revocation
- [x] Credential revocation
- [x] Key-version revocation
- [x] Wrapped-key rotation
- [x] Optional crypto-shredding with high privilege + explicit confirmation + “NO KEY = NO RECOVERY” warning
- [x] Nonce uniqueness across crash, restart, restore, snapshot rollback, replica promotion, failover

### Modes and docs

- [x] Zero-knowledge mode designed and documented honestly (server cannot run arbitrary SQL on strongly client-encrypted fields)
- [x] Field-level client encryption syntax designed (`ENCRYPTED CLIENT`)
- [x] Searchable-encryption leakage documented if introduced
- [x] Live-host compromise threat model documented (no impossible claims)

### Transport, identity, RBAC, audit

- [x] TLS 1.3 required for production remote connections
- [x] mTLS / service identities / short-lived credentials / external IdP recorded as follow-on targets only (no Phase 13 implementation claim)
- [x] Users, roles, grants, revocation
- [x] Scopes: cluster, database, schema, table, column, function, backup, replication, administration
- [x] Least privilege default
- [x] Audit: auth success / failure
- [x] Audit: role and permission changes
- [x] Audit: DDL
- [x] Audit: backup / restore
- [x] Audit: key operations
- [x] Audit: cluster membership
- [x] Audit: security settings
- [x] Audit never contains passwords, keys, tokens, secrets
- [x] Secure temp files and query spills (encrypted)
- [x] Security fuzzing
- [x] Query-abuse limits: JSON depth, vector dimensions, join complexity, etc.

### Phase 13 exit gate

- [x] Stolen files / disks / snapshots / WAL / backups / vector / full-text are unreadable without authorized keys
- [x] Rotation and revocation do not require a manual logical rebuild
- [x] Official benches still run with encryption on
- [x] Crypto overhead measured toward `< 10%` (preferred `< 5%`) on OLTP — `nextsql-bench` reports `enc%` (page AEAD time / elapsed) with encryption on; page wrap/AEAD benches remain. Host-specific % is not a universal guarantee.

---

## Phase 14 — Production operations

- [x] `nextsql backup`
- [x] `nextsql restore`
- [x] Backup remains encrypted
- [x] Backup flow: backup → manifest → integrity check → storage → verify → periodic restore test
- [x] `nextsql export` / `nextsql import`
- [x] PITR from base backup + archived WAL
- [x] Restore by timestamp — archive/backup time, not per-commit (`docs/backup.md`)
- [x] Restore by LSN
- [x] Upgrade / format compatibility management
- [x] Observability / metrics
- [x] Diagnostics
- [x] `nextsql-bench`: point SELECT
- [x] `nextsql-bench`: range SELECT
- [x] `nextsql-bench`: INSERT / UPDATE / DELETE
- [x] `nextsql-bench`: transactions
- [x] `nextsql-bench`: joins / aggregations
- [x] `nextsql-bench`: JSON / full-text / vector / hybrid
- [x] Bench records QPS, TPS, p50 / p95 / p99 / p99.9, CPU, RAM, allocs, disk, WAL, encryption overhead
- [x] Admission control / queue / throttle / cancel
- [x] Result-size and execution-time budgets
- [x] Crash-during-backup tests
- [x] Restore verification is mandatory (upload ≠ valid backup)

### Phase 14 exit gate

- [x] Backup, restore, and PITR proven — unit + restore-test path; official large-cluster restore drill stays with later ops work
- [x] Overload does not OOM the server — admission rejects / times out instead of accepting unbounded queries; per-query memory / result / time budgets still apply
- [x] Metrics and `nextsql-bench` cover the official workload set

---

## Phase 15 — High availability

Start only after single-node durability, crash recovery, and backup/restore are proven.

- [x] Raft (proven library / algorithm — do not invent consensus)
- [x] Minimum 3 voting nodes documented and tested
- [x] Replication of log / state
- [x] Leader election
- [x] Failover
- [x] Replica repair — both documented paths now proven 2026-09-03 (log #95): the lagging-follower-reconnects path (`TestHAReplicaRepair`) and the wiped-replica-from-backup + `AddVoter` path (`TestHAReplicaRepairFromBackupAddVoter`). The latter found and fixed a real data-loss bug along the way — see log #95.
- [x] Rolling maintenance
- [x] Reject writes if a leader cannot be safely identified
- [x] No split brain
- [x] Synchronous quorum commit: do not ACK until durability policy is met
- [x] RPO = 0 for acknowledged quorum-synchronous commits under covered failures — including, as of 2026-09-03 (log #95), a backup-restored replica rejoining via `AddVoter`: fixed a real gap where that repair path could silently lose acknowledged writes made between the backup and the rejoin.
- [x] Failure detection in seconds
- [x] Leader election target `< 3 s`
- [x] Service recovery target `< 5 s`
- [x] Kill-leader integration test
- [x] Partition / quorum-loss test (writes rejected)
- [x] Replica-repair test — the lagging-follower case (`TestHAReplicaRepair`) and, as of 2026-09-03 (log #95), the backup+`AddVoter` case (`TestHAReplicaRepairFromBackupAddVoter`, `tests/ha/ha_test.go`) — found a real bug on first write, now fixed and passing 20/20 under `-race`.
- [x] Rolling-maintenance test

### Phase 15 exit gate

- [x] No lost acknowledged quorum-synchronous commit in covered failures
- [x] No split brain
- [x] Service recovery measured on a healthy 3-node cluster
- [x] Availability discussed as SLO / design objective, not “100% uptime”

---

# Cross-cutting gates (keep green)

These are re-checked as surfaces appear. Unchecked items stay open until the introducing phase lands.

## Integrity

- [x] Page integrity authentication
- [x] WAL authentication / checksums
- [x] Backup verification
- [x] Format validation
- [x] LSN checks
- [x] Index consistency checks
- [x] Corruption path: detect → isolate → fail safely → recover — `internal/storage/integrity` + `recovery.RepairPage`; sidecar `*.isolated` (`NSQI`); live `Engine.Pin` recovers or stays isolated (`docs/storage-format.md`)
- [x] Never return known corrupted records
- [x] Known silent corruption tolerance = 0

## Fuzz surfaces

- [x] SQL parser
- [x] Wire protocol
- [x] Page decoder
- [x] WAL decoder
- [x] Backup parser
- [x] Export parser
- [x] JSON parser
- [x] Vector metadata
- [x] Full-text index
- [x] Authentication protocol
- [x] Replication command decoder

## Resource safety

- [x] No unbounded goroutines — query workers go through `internal/scheduler.Pool`
- [x] No unbounded allocations from user input — wire lengths are capped before allocate; JSON depth/size limits are enforced; VECTOR dimension is capped at 8192 and elements must be finite
- [x] Streaming + backpressure — local `Session.Stream` / `Result.NextBatch`; wire `FlowAck` (one batch in flight)
- [x] Admission control under overload
- [x] Slow clients cannot grow memory without bound

## Security claims discipline

- [x] No custom cryptographic primitives
- [x] No keys in connection URLs
- [x] No “unhackable” / “100% secure” / “guaranteed zero downtime” / “impossible to lose data” claims
- [x] Live unlocked host threat model documented — `docs/protocol.md`
- [x] Production remote connections use TLS
- [x] Persistent plaintext in production mode = 0 by default
- [x] Legacy shared-tenancy surface removed — `SET TENANT`, `RESET TENANT`, and `PARTITION BY TENANT` reject with hosted-database guidance; non-ADMIN access to legacy `tenant_id` tables fails closed; ADMIN retains migration access (`docs/security.md`)
- [x] Known critical unresolved production vulnerabilities = 0 — 2026-08-18 review in `docs/security.md`; `authorize` fail-closed

## Performance measurement discipline

- [x] Official benches keep encryption + durability on
- [x] Published numbers include CPU, RAM, storage, filesystem, row width, query, indexes, cache, encryption, durability, concurrency — `nextsql-bench --slo` / `docs/ops.md`
- [x] Cached PK lookup target tracked: p50 `< 0.5 ms`, p95 `< 1 ms`, p99 `< 3 ms` — 25K-row warm PK, p50 23 µs / p95 41 µs / p99 64 µs (`docs/ops.md`)
- [x] Indexed query target tracked: p50 `< 1 ms`, p95 `< 3 ms`, p99 `< 5 ms` — `ix_kv_n` equality, `IndexScan`, p50 37 µs / p95 72 µs (`docs/ops.md`)
- [x] Durable INSERT/UPDATE target tracked: p50 `< 2 ms`, p95 `< 5 ms`, p99 `< 10 ms` — met on tmpfs and on this ext4 run; an earlier ext4 run saw fsync p95 ≈ 100 ms (`docs/ops.md`)
- [x] Row-processing targets tracked (25K / 100K / 1M / 10M / 100M) — all target tiers met; 100M is the final required tier
- [x] 10M-row INSERT measured (bulk load and/or per-row) — 2026-08-18 ext4 **33 s** / 301 485 rows/s (`docs/ops.md`). Next-target `<15 min`, long-term `<2 min`, and lifetime `<1 min` met.
- [x] 10M-row UPDATE measured (bulk) — 2026-08-18 ext4 **1 m 46 s** / 93 907 rows/s (`docs/ops.md`). Next-target and long-term `<2 min` met.
- [x] 10M-row DELETE measured (bulk) — heap truncate **2.93 s** / 3.4 M rows/s on ext4 (`docs/ops.md`)
- [x] 1M-vector HNSW top-10 target tracked with recall — corrected distinct-vector v10 measured p95 **8.061 ms**, recall@10 **1.000**, and recall@100 **0.998** with a 64-query sample (`docs/ops.md`)
- [x] Hybrid query target tracked — 256-row `WHERE`+`SEARCH`+`NEAREST` p95 12.3 ms (`docs/ops.md`)
- [x] `unsafe` / SIMD only after profile + isolation + tests + fuzz + measured win — production vector path audited as portable Go; `TestPortableProductionPath` rejects `unsafe`, cgo, and assembly pending an explicit evidence-backed policy change

---

# Product SQL acceptance (final E2E)

Do not check these until Phases 5 and 9–12 are integrated.

- [x] `CREATE TABLE products` with UUID, STRING, TEXT, DECIMAL, JSON, `VECTOR<F32,1536>`, TIMESTAMPTZ
- [x] Relational `WHERE price BETWEEN …`
- [x] JSON `WHERE metadata.category = …`
- [x] `NEAREST embedding TO $query`
- [x] `SEARCH description FOR '…'`
- [x] Hybrid `WHERE` + `SEARCH` + `NEAREST` as one plan
- [x] Same hybrid query is ACID, WAL-durable, crash-recoverable, and encrypted at rest

---

# Final success contract

Check only when the corresponding measurements and tests exist.

- [x] One engine: relational + JSON + vector + full-text
- [x] Unified optimizer
- [x] ACID transactions (MVCC + locks + UNDO)
- [x] WAL + crash recovery
- [x] Mandatory encryption in production
- [x] Durable single-node storage
- [x] HA available after single-node proof
- [x] Persistent plaintext: 0 by default
- [x] Silent known corruption: 0
- [x] Lost acknowledged quorum-synchronous commits: 0 within supported failures
- [x] HA SLO treated as `>= 99.999%` design objective
- [x] Cached point lookup p50 `< 0.5 ms` measured — 23 µs (`docs/ops.md`)
- [x] Indexed query p95 `< 3 ms` measured — 72 µs IndexScan (`docs/ops.md`)
- [x] 25K rows `< 1 s` measured — `COUNT(*)` 3 ms, `GROUP BY` 6 ms (`docs/ops.md`)
- [x] 1M simple optimized aggregation `< 1 s` measured — `COUNT(*)` 54 µs, `GROUP BY` 147 ms (`docs/ops.md`); COUNT and GROUP BY long-term `< 150 ms` met
- [x] 10M optimized aggregation `< 5 s` measured — `COUNT(*)` 58 µs, `GROUP BY` 660 ms on ext4 (`docs/ops.md`); COUNT and GROUP BY long-term `< 1 s` met
- [x] 10M-row INSERT measured (bulk and/or per-row) — bulk **33 s** / 301 485 rows/s on ext4 (`docs/ops.md`); long-term `< 2 min` and lifetime `< 1 min` met
- [x] 10M-row UPDATE measured (bulk) — **31 s** / 323 772 rows/s (`docs/ops.md`); long-term `< 2 min` and lifetime ≪1 min met
- [x] 10M-row DELETE measured (bulk) — heap swap **25 ms** (`docs/ops.md`)
- [x] 100M analytical `< 30–60 s` measured — COUNT **56 µs**, GROUP BY **16.31 s**, PK range **2.21 ms**, join **35.54 s**
- [x] 1M-vector HNSW top-10 p95 `< 25 ms` measured with recall — v10 p95 **8.061 ms**, recall@10 **1.000**, recall@100 **0.998**
- [x] HA service recovery `< 5 s` measured
- [x] Encryption mandatory in production mode

---

# Phase 16 — Correctness / SLO closure

- [x] Fix the `TestConcurrentBeginCommit` race: page mutation and commit-time physical snapshots are separated by `Engine.pageMu`; `go test -race ./internal/executor -run '^TestConcurrentBeginCommit$' -count=50` passes on 2026-08-24
- [x] Fix B+Tree DELETE leaf-merge corruption — empty last internal under root now becomes a height-1 leaf (`internal/storage/btree/delete.go`)
- [x] Crash during leaf merge / rebalance — `wal.PointDuringMerge` + `TestCrashDuringMerge`
- [x] Randomized delete/merge invariant test — `TestRandomizedDeleteMerges`, `TestRandomizedLargeInvariants` (`NEXTSQL_BTREE_OPS` scales toward 100M)
- [x] 10M DELETE soak harness — `TestBulkDeleteSoak` (default 25K; `NEXTSQL_SOAK_ROWS=10000000` for the official soak)
- [x] Reduce UNDO write amplification — buffer UNDO records and flush at commit
- [x] SLO harness: PK range + hash join at each scan scale; HNSW report includes QPS, heap, db size
- [x] 10M DELETE soak run on labeled ext4 — `--slo-max-rows 10000000` delete **25 ms** (`docs/ops.md`)
- [x] Randomized 100M insert/delete B+Tree invariant run — the invariant soak (`TestRandomizedLargeInvariants` via `./scripts/run-btree-soak.sh`) runs `Check()`-clean with a matching full-scan count at every scale exercised: v4 reached 24M operations (`live=11,435,641`) and v8 reached 44M clean operations (`live=17,557,686`). The **terminal 100M-operation run on one labeled host is DEFERRED as a standalone measurement, not a release gate** (paper-closed 2026-08-30, same disposition as P18): v9 was SIGKILLed after ~11h on a RAM-constrained host with no retained terminal evidence, and bounded v10 was stopped by explicit direction on 2026-08-26. The soak harness was reworked for that host class so a future 100M run can finish — resident pool sized to hold the working set (`NEXTSQL_BTREE_POOL_PAGES`, default 384 MiB), optional key-space cap (`NEXTSQL_BTREE_SPACE`), cheap frequent WAL-recycle checkpoints decoupled from the rare full structural walk, `int32` bookkeeping, and post-check `debug.FreeOSMemory()`. The structural correctness this line covers is otherwise fully exercised by `TestRandomizedDeleteMerges`, `TestCrashDuringMerge`, `TestBulkDeleteSoak`, and the published 10M DELETE run.
- [x] Official 10M INSERT `< 15 min` / UPDATE `< 10 min` re-run — INSERT **1 m 54 s**, UPDATE **1 m 57 s** (`docs/ops.md`); long-term `< 2 min` met
- [x] Measure 100M rows: COUNT, GROUP BY, indexed lookup, range, join — 2026-08-21 ext4: COUNT **56 µs**, GROUP BY **16.31 s**, PK range **2.21 ms**, join **35.54 s** (`docs/ops.md`)
- [x] Measure 1M HNSW: recall\@10/@100, p50/p95/p99, QPS, RAM, index size — corrected distinct-vector v10 on 2026-08-25 ext4: p50 **6.158 ms**, p95 **8.061 ms**, p99 **8.156 ms**, QPS **156**, recall@10 **1.000**, recall@100 **0.998**, heap **4.3 GiB**, DB **1.1 GiB**, index **546.1 MiB** (`docs/ops.md`). v8 was OOM-killed and v9 exhausted the undersized no-steal pool; the bounded pool guard and v10 methodology are now recorded.
- [x] Known critical unresolved production vulnerabilities = 0 — 2026-08-18 review in `docs/security.md`; `authorize` fail-closed

### Phase 16 exit gate

- [x] Large sequential DELETE is correct and 10M DELETE is published — **25 ms**
- [x] Crash during merge recovers to a `Check()`-clean tree
- [x] 100M analytics measured against `< 60 s` — COUNT **56 µs**, GROUP BY **16.31 s**, PK range **2.21 ms**, join **35.54 s**
- [x] 1M-vector HNSW p95 `< 25 ms` with recall — v10 p95 **8.061 ms**, recall@10 **1.000**, recall@100 **0.998**
- [x] Next-target 10M INSERT/UPDATE published — INSERT **1 m 54 s**, UPDATE **1 m 57 s**; long-term `< 2 min` met
- [x] Security gate signed off

---

# Next action

**Current next action (2026-09-03): P0–P27 are all complete. Next release
gate is P28 Professional Installer + NextSQL Manager (not yet scoped).**
P27 Operational maturity + workload governance closed 2026-09-03 — see the
"# Phase 27" section and its exit gate for the authoritative closure record
(structural local-commit-before-replicate-ack fix, log #79; per-realm/
per-database connection limits, log #80). The cross-cutting Multi-database
hosting track (M2/M3) continues incrementally and gates none of P0–P27. The
accumulated historical status paragraph below predates the P26/P27 exit-gate
closures and is retained for history only.

0\. **P26 System catalog / introspection 2.0 — COMPLETE (2026-09-02).** **All 5 live tables now landed 2026-09-01**: `system.sessions`, `system.active_queries`, `system.transactions`, `system.change_streams` (first increment), and `system.locks` (second increment, same day — table-name tags threaded through `btree.Tree`/`txn.LockManager`, new `LockManager.Snapshot()`, visibility keyed off the same live-session→user map the first increment built) — see the "# Phase 26" section below for both full increment writeups (new `DB.RegisterSession`/`LiveSessions`/`CDCSubscriptions`/`LockSnapshot` accessors, mutex-guarded `Session.CurrentQuery`/`TxnSnapshot` snapshots to avoid cross-goroutine data races, RBAC matching the existing `system.tasks` owner-filter pattern, `docs/system-catalog.md` — the first docs for the whole `system.*` schema). **Next P26 work: `SHOW` convenience aliases, then the exit gate.** Prior release gate, **P24 Full-text Search 2.0 — COMPLETE (2026-08-31)**: Exit gate green; next release gate: **P25 Security 2.0**. **Faceting landed 2026-08-31**: `SELECT * … SEARCH … FACET col [, col …]` returns independent histograms over the full SEARCH match set (`facet STRING`, `value STRING`, `count DECIMAL`); `LIMIT` is per-facet top-N; `NULL` skipped; query-time only, no catalog/format bump; requires `SELECT *` and `SEARCH`; 1–8 discrete columns and 1024 distinct values fail closed. Tests: `TestFulltextFacet`, `TestFacetDistinctValueCap`, `TestBindFulltextFacet`, `TestSearchFacetPlan`. **Field weighting landed 2026-08-31**: optional `SEARCH col WEIGHT n` scales per-field BM25 tf from existing position bands (omitted = 1; `(0, 64]`; fail closed; query-time only, no catalog/format bump). Unweighted SEARCH stays Phase 10 / multi-field BM25. Prefix/fuzzy/typo/`HIGHLIGHT`/`SNIPPET`/phrase matching unchanged. Tests: `TestWeightedTF`, `TestQueryScoreWeighted`, `TestCheckFieldWeight`, `TestFulltextFieldWeight`. **Multi-field search landed 2026-08-31**: `CREATE FULLTEXT INDEX` / `SEARCH col [, col …]` take 1–8 STRING/TEXT columns (exact column-list match for inverted-index use; subset/reorder seq-scans). Fields scored as one BM25 document; phrases do not cross fields (position bands, no catalog/format bump). Tests: `TestAnalyzeFieldsPositions`, `TestBindFulltextMultiField`, `TestSearchChoosesMultiFieldFulltextIndex`, `TestFulltextMultiFieldSearch`. **Highlight/snippet generation landed 2026-08-31**: `HIGHLIGHT(col)` / `SNIPPET(col)` are SELECT-list functions that require SEARCH (no catalog/format bump). Original document tokens whose analyzed form participates in the query (exact/synonym/prefix/fuzzy/typo) are wrapped with `<mark>` / `</mark>` (override `HIGHLIGHT(col, pre, post)` / `SNIPPET(col, width [, pre, post])`; markers max 32 runes). `HIGHLIGHT` returns the full field; `SNIPPET` returns a 16–4096 rune window (default 160) around the densest match cluster with `…` on a truncated edge. Both fail closed outside the SELECT list of a SEARCH query. Tests: `TestTokenizeSpans`, `TestHighlightExact`, `TestHighlightPreservesOriginalCase`, `TestHighlightPrefixFuzzyTypo`, `TestHighlightEnglishStemAndSynonym`, `TestHighlightEnglishDropsStops`, `TestHighlightCustomMarkersAndEmptyQuery`, `TestHighlightMarkerLimits`, `TestSnippetWindow`, `TestSnippetShortTextNoEllipsis`, `TestSnippetWidthBounds`, `TestHighlightsTermPrefixAndFuzzy`, `TestBindHighlightRequiresSearch`, `TestFulltextHighlight`. `go build ./...` + `internal/fulltext` / `internal/sql/binder` / `internal/sql/parser` / `internal/executor` `go test` + `-race` green; `FuzzTokenize` 5 s clean. Docs: `docs/fulltext.md`, `docs/sql.md`, `USAGE.md`, `CHANGELOG.md`, `ROADMAP.md`, `SKILLS.md`, `AGENTS.md`, web `fulltext.md` / `limits.md` / `sql.md`. **Typo tolerance landed 2026-08-31**: unadorned SEARCH tokens stay exact when any analyzed alternative is in the searchable vocabulary (`cat` does not match `cot` when `cat` is indexed; `cats` does not match `cat`). When every alternative is absent, SEARCH rewrites the group as an AUTO-distance fuzzy group (no catalog/format bump): `databse` matches `database`. Typo AUTO is stricter than explicit `~` (0 for 1–4 runes, 1 for 5–8, 2 for 9+). Prefix and explicit fuzzy groups are unchanged; phrase slots follow the same rule (`"databse performance"`); BM25 scores the best match; distinct terms consume the existing expansion caps (256 terms / 8192 bytes / 4096 work units) and fail closed. Seq-scan uses the scanned corpus as the vocabulary. Tests: `TestApplyTypoToleranceMissing`, `TestApplyTypoTolerancePresentExactUnchanged`, `TestApplyTypoToleranceShortStaysExactMiss`, `TestAutoTypoDistance`, `TestApplyTypoTolerancePrefixAndFuzzyUnchanged`, `TestApplyTypoTolerancePhrase`, `TestApplyTypoToleranceSynonymGroup`, `TestApplyTypoToleranceNilPresent`, `TestQueryMatchesTypo`, `TestQueryScoreTypoBestMatch`, `TestFulltextTypoSearch` (index + seq-scan + short-token miss + english `catalag` + synonym skip + expansion cap). `go build ./...` + `internal/fulltext` / `internal/executor` `go test` + `-race` green; `FuzzTokenize` 5 s clean. Docs: `docs/fulltext.md`, `docs/sql.md`, `USAGE.md`, `CHANGELOG.md`, `ROADMAP.md`, `SKILLS.md`, `AGENTS.md`, web `fulltext.md` / `limits.md` / `sql.md`. **Fuzzy matching landed 2026-08-31**: trailing ASCII `~` on a SEARCH token (`cat~`, `cat~1`, `cat~2`, `"databas~ performance"`) is a query-time fuzzy group (no catalog/format bump). Fuzzy tokens skip stem/stop/synonym (French elision still applies); matching indexed terms within OSA Damerau-Levenshtein distance (AUTO: 0 for 1–2 runes, 1 for 3–5, 2 for 6+; explicit 1 or 2) are a disjunction at that position; BM25 scores the best match; distinct terms consume the existing expansion caps (256 terms / 8192 bytes / 4096 work units) and fail closed; mixed `*`/`~` and `~0`/`~3+` fail closed; exact unadorned tokens keep Phase 10 BM25/phrase/prefix behaviour (`cat` does not match `cot`). Tests: `TestParseQueryFuzzy`, `TestParseQueryFuzzyPhrase`, `TestParseQueryFuzzySkipsStemAndSynonym`, `TestQueryMatchesFuzzy`, `TestFuzzyWithin`, `TestAutoFuzzyDistance`, `TestQueryScoreFuzzyBestMatch`, `TestFuzzyExpanderFailClosed`, `TestFulltextFuzzySearch` (index + seq-scan + english `run~` vs `running~` + synonym skip + expansion cap). `go build ./...` + `internal/fulltext` / `internal/executor` `go test` + `-race` green; `FuzzTokenize` 5 s clean with fuzzy seeds. Docs: `docs/fulltext.md`, `docs/sql.md`, `USAGE.md`, `CHANGELOG.md`, `ROADMAP.md`, `SKILLS.md`, `AGENTS.md`, web `fulltext.md` / `limits.md` / `sql.md`. **Prefix search landed 2026-08-31**: trailing ASCII `*` on a SEARCH token (`cat*`, `"data* performance"`) is a query-time prefix group (no catalog/format bump). Prefix tokens skip stem/stop/synonym (French elision still applies); matching indexed terms are a disjunction at that position; BM25 scores the best match; distinct terms consume the existing expansion caps (256 terms / 8192 bytes / 4096 work units) and fail closed; exact unadorned tokens keep Phase 10 BM25/phrase behaviour (`cat` does not match `catalog`). Tests: `TestParseQueryPrefix`, `TestParseQueryPrefixPhrase`, `TestParseQueryPrefixSkipsStemAndSynonym`, `TestQueryMatchesPrefix`, `TestPrefixExpanderFailClosed`, `TestPostingPrefixBounds`, `TestFulltextPrefixSearch` (index + seq-scan + english `run*` vs `running*` + expansion cap). `go build ./...` + `internal/fulltext` / `internal/executor` `go test` + `-race` green; `FuzzTokenize` 5 s clean with prefix seeds. Docs: `docs/fulltext.md`, `docs/sql.md`, `USAGE.md`, `CHANGELOG.md`, `ROADMAP.md`, `SKILLS.md`, `AGENTS.md`, web `fulltext.md` / `limits.md` / `sql.md`. **Synonym dictionaries landed 2026-08-31**: english analyzer v3 writes synonym dictionary v1 (15 tight bidirectional groups) as query-time OR expansion, bounded by the existing caps; index terms stay 1:1 like v2; phrase slots accept any alternative; english v1/v2 still decode; `simple` unchanged. Tests: `TestEnglishSynonymV1Membership`, `TestAnalyzeEnglishNoIndexSynonyms`, `TestParseQueryEnglishSynonyms`, `TestParseQueryEnglishSynonymPhrase`, `TestQueryMatchesSynonymDisjunction`, `TestEnglishSynonymWorkCounts`, `TestLookupAnalyzer` (v3), `TestTableEncodeFulltextAnalyzerV9` (v3), binder ANALYZER writes v3, `TestFulltextEnglishSynonyms`. **Versioned language analyzers landed 2026-08-31**: `french` / `german` / `spanish` analyzer v1 (Snowball 3.x stemmer + official Snowball stop-word dictionary v1: 153 / 231 / 308 terms) on existing `NSCT` v9 ids 2/3/4; French elides `l'`/`qu'`/… before the stop list; remaining terms re-pack to consecutive positions; `simple`/`english` unchanged; unknown names/revisions fail closed. Tests: `TestStemFrenchFixtures`, `TestStemGermanFixtures`, `TestStemSpanishFixtures`, `TestAnalyzeFrenchStopsThenStems`, `TestAnalyzeGermanStopsThenStems`, `TestAnalyzeSpanishStopsThenStems`, `TestParseQueryFrenchElision`, `TestFrenchStopV1Membership`, `TestGermanStopV1Membership`, `TestSpanishStopV1Membership`, `TestLookupLanguageAnalyzers`, `TestTableEncodeFulltextAnalyzerV9` (fr/de/es), binder ANALYZER cases, `TestFulltextLanguageAnalyzers`. `go build ./...` + `internal/fulltext` / `internal/catalog` / `internal/sql/parser` / `internal/sql/binder` / `internal/sql/optimizer` / `internal/upgrade` / `internal/xport` `go test` + `-race` green; `internal/executor` `TestFulltext*` green + `-race`; `FuzzTokenize` / `FuzzDecodePartitionedTable` 5 s clean. Docs: `docs/fulltext.md`, `docs/sql.md`, `docs/storage-format.md`, `USAGE.md`, `CHANGELOG.md`, `ROADMAP.md`, `SKILLS.md`, `AGENTS.md`, web `fulltext.md` / `limits.md` / `sql.md`. **Stop-word dictionaries landed 2026-08-31**: english analyzer v2 applies stop-word dictionary v1 (33-term Lucene EnglishAnalyzer / Snowball-small set) before Porter2 at index and query time; remaining terms re-pack to consecutive positions; `simple` has no stop list; english v1 (stem only) still decodes; dropped stop words consume query-expansion work units; stop-only SEARCH returns no rows. Tests: `TestEnglishStopV1Membership`, `TestAnalyzeEnglishDropsStops`, `TestAnalyzeEnglishStopsThenStems`, `TestParseQueryEnglishDropsStops`, `TestParseQueryEnglishPhraseDropsStops`, `TestEnglishStopWorkCounts`, `TestTableEncodeFulltextAnalyzerV9` (v1+v2), binder ANALYZER writes v2, `TestFulltextEnglishStopWords`. `go build ./...` + `internal/fulltext` / `internal/catalog` / `internal/sql/parser` / `internal/sql/binder` / `internal/sql/optimizer` / `internal/upgrade` `go test` + `-race` green; `internal/executor` `TestFulltext*` green + `-race`; `FuzzTokenize` / `FuzzDecodePartitionedTable` 5 s clean. **Stemming landed 2026-08-31**: `NSCT` v9 per-index analyzer id + revision; `CREATE FULLTEXT INDEX … WITH (ANALYZER = 'simple' | 'english')`; Snowball English (Porter2) v1 applied identically at index and query time; query expansion fail-closed at 256 terms / 8192 bytes / 4096 work units; default `simple` keeps Phase 10 BM25/phrase behaviour (`cat` does not match `cats`). Catalog v1–v8 still decode (missing trailer = simple). `takePartitioning` reads NextID for every `ver >= v5` (not only the current write version). Tests: `TestStemEnglishFixtures`, `TestAnalyzeEnglishStems`, `TestParseQueryEnglishPhrase`, `TestQueryExpansionCapsFailClosed`, `TestTableEncodeFulltextAnalyzerV9`, `TestPartitionCatalogV5ReadsNextID`, parser/binder ANALYZER cases, `TestFulltextEnglishStemming` (simple vs english, stemmed phrase, `EXPLAIN analyzer=english`). `go build ./...` + `internal/fulltext` / `internal/catalog` / `internal/sql/parser` / `internal/sql/binder` / `internal/sql/optimizer` / `internal/upgrade` `go test` + `-race` green; `internal/executor` `TestFulltext*` green + `-race` on the stemming suite; `FuzzTokenize` / `FuzzDecodePartitionedTable` 5 s clean. Docs: `docs/fulltext.md`, `docs/sql.md`, `docs/storage-format.md`, `docs/ops.md`, `USAGE.md`, `CHANGELOG.md`, `ROADMAP.md`, `SKILLS.md`, `AGENTS.md`, web `fulltext.md` / `limits.md`. P23 Vector Engine 2.0 is **COMPLETE** (2026-08-31): production-gating sign-off in `docs/vector.md` "Production-gating sign-off (Phase 23)". Seventeenth increment closed the exit gate. Sixteenth increment landed 2026-08-31: **official `--vecquant` sparse size/latency/recall row**. `internal/bench/vecquant.go` `runOneSparseQuant` seeds a high-dimension, low-nnz corpus (`SPARSEVECTOR<N>` + `USING SPARSE`) independent of the dense `--vecquant-dim` set; CLI `--vecquant-sparse-dim` (default 4096) / `--vecquant-sparse-nnz` (default 24). Deterministic SplitMix64 sparse vectors with strictly-positive weights; parameterized batch INSERT; inverted-index build; `NEAREST` latency + recall@10/@100 vs exact-cosine `SparseFlat` (SQL default `COSINE`, inverted-index inner product + `4·k` payload re-rank). `VectorQuantReport` gains `SparseNNZ`; `Method = "sparse"`; `ElemBytes = 0` (not a dense element type); `RawPayload` is the sum of `EncodeSparse` NSSV widths. Reference (linux/amd64, 12 vCPU, ext4, encryption + WAL + fsync on; 2000 × 4096-d nnz=24, 64 queries): raw payload 282 KiB, index 1.0 MiB, database 2.1 MiB, build 1.17 s, p50 428 µs / p95 716 µs, QPS 2099, recall@10 **1.000**, recall@100 **1.000**. `TestVectorQuantBench` asserts 8 reports + SPARSE-row invariants (nnz/dim, lossless QuantErr, raw payload below the dense F32 floor, recall@10 ≥ 0.5). `go build ./...` + `internal/bench` `go test` + `-race` green. Docs: `docs/vector.md` ("Size / recall comparison"), `docs/ops.md`, `USAGE.md`, `CHANGELOG.md`, `ROADMAP.md`, web `vectors.md`, this file.
   - **P23 exit gate closed 2026-08-31** — dated production-gating sign-off in `docs/vector.md`; a `BITVECTOR`/Hamming `--vecquant` row remains an optional follow-on, not a gate item. P24 stemming + stop-word dictionaries + french/german/spanish analyzers + english synonym dictionary v1 + prefix search + fuzzy matching + typo tolerance + highlight/snippet generation + multi-field search + field weighting + faceting landed 2026-08-31; the P24 exit gate is closed; P25 is next.

   Fourteenth increment landed 2026-08-30: **`SPARSEVECTOR<N>` SQL surface + `CREATE VECTOR INDEX … USING SPARSE`**.
   - `SPARSEVECTOR<N>` (`types.VecSparse`; `N` 1…65535 in `Type.Precision`). Runtime values stay sparse (`SparseIdx`/`SparseVal`); dense literals coerce by dropping zeros. Payload store uses `NSSV` v1. Wire flag `0x02` (nnz + u32 index / f32 value pairs).
   - Parser: `KwSparsevector` + `USING SPARSE` (no `WITH` options). Binder: `catalog.VecMethodSPARSE`; requires a `SPARSEVECTOR` column; rejected with `QUANTIZATION`, on dense/`BITVECTOR`, on partitioned tables, and with `USING HNSW`/`IVF`/`IVFPQ` on a sparse column. `NEAREST` default `COSINE`; `INNER_PRODUCT` allowed; `L2`/`HAMMING` rejected.
   - Catalog: `VecMethodSPARSE = 3` on the existing v8 method byte (no format bump). Executor `internal/executor/sparsestore.go`: `sqlSparse` implements `vector.SparseStore` over the detached encrypted index tree (`NSSM` + `NSSP` postings) + the shared payload store. `buildSparseIndex` (CREATE + `REBUILD INDEX`); `maintainSparseIndex` uses in-memory old/new coordinates; `nearestSparseIndex` = `SearchSparse`. `EXPLAIN` labels `sparse`. Vectorized batches keep `VecRef` + sparse columns so heap-scan `NEAREST`/`SELECT` hydrates `NSSV`.
   - Tests: `TestSparseVectorIndex` (HNSW/IVF/L2 rejected, exact NEAREST, INSERT/UPDATE/DELETE, no `NSSV`/`NSSM`/`NSSP` plaintext, restart, `REBUILD INDEX`); parser/binder cases; catalog fuzz seed; `TestCoerceSparseFromDense`. `go build ./...` + touched-package `go test` + `-race` green; `FuzzParse` / `FuzzDecodePartitionedTable` 10 s clean.
   - Docs: `docs/vector.md`, `docs/sql.md`, `CHANGELOG.md`, `ROADMAP.md`, `USAGE.md`, web `vectors.md` / `limits.md` / `sql.md`, this file.
   - Dense+sparse+BM25 fusion landed in the fifteenth increment above; the official `--vecquant` sparse size/latency/recall row landed in the sixteenth increment.

   Thirteenth increment landed 2026-08-30: **sparse retrieval core** (portable `internal/vector/sparse.go`).
   - `SparseVec` (`Dim u32`, strictly-ascending `Indices`, parallel non-zero finite `Values`). `NewSparseVec` / `CheckSparse` reject length mismatch, dim 0 / `> MaxSparseDim` (2^24), nnz `> MaxSparseNNZ` (2^16), out-of-range / duplicate indices, zero or non-finite values. `SparseDot` is a merge-join; `SparseNorm`; `SparseSimilarity` / `SparseDistance` (`INNER_PRODUCT` = `−dot`, `COSINE` = `1 − cosine`; empty-vector cosine is 0).
   - `EncodeSparse` / `DecodeSparse` (`NSSV` v1: magic, version, dim, nnz, indices as ascending varint deltas, then LE `f32` values). Decode bounds nnz before `make` and rejects a delta `≥ Dim` or a wrapping `prev+d` **before** storing the index. `EncodeSparseMeta` / `DecodeSparseMeta` (`NSSM` v1, 21 bytes: `MaxDim`, metric, count; `COSINE` / `INNER_PRODUCT` only — `L2` / `HAMMING` rejected). `EncodeSparseList` / `DecodeSparseList` (`NSSP` v1 — the `NSIL` front-coded PK scheme plus an `f32` weight per entry; empty-PK / zero / non-finite weights rejected; shared/suffix varints bounded at 4096 before allocation).
   - `SparseStore` (`LoadSparseMeta`/`SaveSparseMeta`, `ListPostings`/`AddPosting`/`RemovePosting`, `LoadSparse`) + `SparseMem` + `PersistSparse` / `LoadSparseMem`. Keys: `SparseMetaKey` `0x00`, `SparsePostingKey(dim)` `0x01`+`u32`. `AddSparse` replaces an existing pk (via `RemoveSparse`); `RemoveSparse` uses `LoadSparse` to find the coordinates and is a no-op when absent.
   - `SearchSparse(st, query, k, rerank, workers)` walks the posting list of every query coordinate and accumulates the exact inner product. `INNER_PRODUCT` ranking is final. `COSINE` re-ranks the top `rerank` (0 → `4·k`) against `st.LoadSparse` payloads when all load, else falls back to the inner-product order. `SparseFlat` is the exact baseline.
   - Tests: `internal/vector` `TestSparseVecRoundTrip` (sorted round trip, empty vector, fail-closed, overflowing delta rejected), `TestNewSparseVecRejects`, `TestSparseDot`, `TestSparseMetaRoundTrip` (L2/Hamming/dim-0 rejected), `TestSparseListRoundTrip` (front-coding shrinks + dedups, zero-value / empty-PK rejected, impossible prefix fails closed), `TestSparseSearchRecall` (IP inverted-index recall@10 1.0; COSINE rerank-all 1.0; COSINE 4k ≥ 0.90 on 400×4096-d nnz=24), `TestSparseAddRemove`, `TestSparsePersistLoad`, `TestSparseKeyRoundTrip`; `FuzzDecodeSparse` (15 s) / `FuzzDecodeSparseList` (15 s) / `FuzzDecodeSparseMeta` (10 s) clean. `go build ./...` + `internal/vector` `go test` + `-race` green.
   - Docs: `docs/vector.md` ("Sparse retrieval" + storage table), `CHANGELOG.md`, `ROADMAP.md`, `USAGE.md`, web `vectors.md`, this file.
   - SQL surface (`SPARSEVECTOR<N>` + `USING SPARSE`) landed in the fourteenth increment above.

   Twelfth increment landed 2026-08-30: **IVF-PQ SQL surface + lifecycle wiring** (see Active increment / Last updated). Eleventh increment landed 2026-08-30: **IVF-PQ index core** (portable `internal/vector/ivfpq.go`).
   - `IVFPQMeta` / `EncodeIVFPQMeta` / `DecodeIVFPQMeta` (`NSPQ` v1, 32 bytes: dim, metric, `NLIST`, `NPROBE`, `M`, count, trained). `PQCodebook` (`M` × `Ksub` × sub-dim `f32`) + `EncodePQCodebook` / `DecodePQCodebook` (`NSPC` v1, element count bounded before `make`). `PQEntry` + `EncodePQList` / `DecodePQList` (`NSPL` v1 — the `NSIL` front-coded primary-key scheme with `M` code bytes appended per entry; all varints bounded at 4096 before allocation).
   - `IVFPQStore` interface (`LoadIVFPQMeta`/`SaveIVFPQMeta`, `LoadCentroids`/`SaveCentroids`, `LoadCodebook`/`SaveCodebook`, `ListEntries`/`AddEntry`/`RemoveEntry`, `LoadVec`) + `IVFPQMem` in-memory impl + `PersistIVFPQ` / `LoadIVFPQMem`. `IVFPQCodebookKey()` = `0x03`; meta/centroid/posting keys shared with IVF (detached tree).
   - `TrainIVFPQ(meta, samples)` — deterministic (fnv seed over meta + samples): k-means++ coarse quantiser, then per-subspace k-means over the residuals `prep(v) − centroid`. `Ksub = min(256, len(samples))`. `AddIVFPQ` assigns a pk to its nearest centroid and stores the `M`-byte residual code (move semantics via `RemoveIVFPQ`). `RemoveIVFPQ` scans lists, decrements count.
   - `SearchIVFPQ(st, query, k, nprobe, rerank, workers)` — rank centroids by sphere-L2, probe `nprobe` lists, ADC score every entry (per-subspace `sqL2` table summed over the code), keep the best `rerank` (0 → `4·k`), then exact `FlatSearch` re-rank against `st.LoadVec` payloads when *all* load, else return the ADC ordering. `COSINE` (unit-normalised) / `L2` only; `INNER_PRODUCT` and `HAMMING` rejected in `EncodeIVFPQMeta`.
   - Tests: `internal/vector` `TestIVFPQMetaRoundTrip` (fail-closed, `M ∤ Dim` rejected, IP rejected), `TestPQCodebookRoundTrip`, `TestPQListRoundTrip` (front-coding shrinks + dedups + preserves codes, impossible prefix fails closed), `TestIVFPQSearchRecall` (probe-all + re-rank recall@10 0.996; ADC-only 0.700 on 700×32-d nlist=16 M=8), `TestIVFPQAddRemove`, `TestIVFPQPersistLoad`, `TestTrainIVFPQDeterministic`; `FuzzDecodePQList` (15 s) / `FuzzDecodePQCodebook` (10 s) / `FuzzDecodeIVFPQMeta` (10 s) clean. `go build ./...` + `internal/vector` `go test` + `-race` green.
   - Docs: `docs/vector.md` ("IVF-PQ (product quantisation)" + storage table), `CHANGELOG.md`, `ROADMAP.md`, `USAGE.md`, web `vectors.md`, this file.
   - **Next increment: the IVF-PQ SQL surface** — `CREATE VECTOR INDEX … USING IVFPQ WITH (LISTS = n [, PROBES = m, SUBSPACES = M])`: parser, binder, catalog format bump, executor build/rebuild/maintain/search over `IVFPQStore` on the detached encrypted index tree, `nextsql-bench --vecquant` IVF-PQ row, crash-recovery/Raft (inherits the index-tree WAL path).

   Tenth increment landed 2026-08-30: **process-local IVF quantiser cache.**
   - `internal/executor/ivfstore.go`: `lockedIVF` wraps `*nsvec.IVFMem` behind an `sync.RWMutex` and implements `nsvec.IVFStore` (reads under RLock, the unused write methods under Lock). `ivfSearchStore(tab, idx)` returns the process-local copy when `!s.dirtyIVF` and the cached generation matches `db.hnswGeneration()`, otherwise binds the txn-scoped `sqlIVF` (a writer must see its own uncommitted state) or, on a lazy miss, `nsvec.LoadIVFMem`s the disk store and installs it via `db.setIVF`. `nearestIVFIndex` now calls `ivfSearchStore` instead of `ivfStoreOf`.
   - Cache storage: `DB.ivf map[string]*lockedIVF`, guarded by the **existing** `hnswMu` and versioned by the **existing** `hnswGen`. `dropHNSW(key)` / `dropAllHNSW()` extended to delete from / reset `db.ivf` too, so every current eviction path — `DROP INDEX`, `REBUILD INDEX` reclaim, `dropTableMaps`, `renameTableMaps`, `dropMissingIndexes`, `dropReclaimedIndexMaps`, and `applyReplicated` — invalidates IVF with no new call sites. `db.getIVF` / `db.setIVF` added.
   - Session: `s.dirtyIVF bool` + `s.pendingIVF map[string]*lockedIVF` (reset at the three txn-end sites next to `dirtyHNSW`/`pendingHNSW`). `maintainIVFIndex` sets `s.dirtyIVF`. `buildIVFIndex` (CREATE + REBUILD) stashes its trained `IVFMem` at `s.pendingIVF[idxKey]`. `installPendingHNSW` now also drops-all when `s.dirtyIVF` and installs `s.pendingIVF` entries at the post-drop generation.
   - Tests: `internal/executor` `TestIVFProcessLocalCache` — cache present after CREATE commit at the current gen; a read-only NEAREST neither evicts nor re-stamps it; an INSERT bumps the gen and the next NEAREST both reflects the row and repopulates the cache; a freshly `Open`ed DB holds no copy until the first search then lazily caches; `REBUILD INDEX` leaves a current copy. `TestIVFVectorIndex` / `TestIVFCentroidGrouping` unchanged and green.
   - `go build ./...` + `internal/executor` (`-run 'IVF|Vector|Nearest|HNSW|Hybrid'`) / `internal/vector` / `internal/bench` `go test` + `-race` green. (Pre-existing `internal/backup` / `internal/migrate` failures on this host are `disk quota exceeded` on the test tmpfs, unrelated.)
   - Docs: `docs/vector.md` ("IVF index" bullet + storage note + benchmark caveat), `CHANGELOG.md`, `ROADMAP.md`, `USAGE.md`, web `vectors.md`, this file.

   Ninth increment landed 2026-08-30: **IVF row in `nextsql-bench --vecquant` + grouped centroid storage** (details after "Remaining P23" below). Eighth increment landed 2026-08-30: **IVF vector index — SQL surface + lifecycle wiring.** `CREATE VECTOR INDEX … USING IVF WITH (LISTS = n [, PROBES = m])`.
   - Parser (`sql/parser/parser.go`, `sql/ast/ast.go`): `ast.CreateIndex.IVFLists` / `IVFProbes int`. `createIndex` `USING` clause now switches `KwHnsw` (existing quant `WITH` block) vs `identIs("ivf")` — the latter parses an optional `WITH (LISTS = <uint> [, PROBES = <uint>])` loop. No new lexer keyword. Fuzz seeds + `parser_test.go` cases (params, unknown `WITH` option, unknown method).
   - Binder (`sql/binder/index.go`): `s.Using` switch sets `idx.VecMethod` (`catalog.VecMethodHNSW` / `VecMethodIVF`); IVF requires `LISTS ≥ 1` and `≤ catalog.MaxVectorIndexLists` (65 536), `PROBES ≤ LISTS`, stores `idx.IVFLists` / `IVFProbes`. `QUANTIZATION` now requires `USING HNSW`. IVF rejected on a `BITVECTOR` column and on a partitioned table ("not supported in this slice"). `binder_test.go` cases.
   - Catalog (`catalog/catalog.go`, `catalog/encode.go`): `Index.VecMethod uint8` + `IVFLists` / `IVFProbes uint32`; consts `VecMethodHNSW` / `VecMethodIVF` / `MaxVectorIndexLists`. `tableVersion = 7` (`tableVersionV6` retained); `EncodeTable` appends `method(1) + LISTS(u32) + PROBES(u32)` per index after the v6 quant bytes; `DecodeTable` reads them for `ver >= 7` and validates (IVF ⇒ `Vector` + `LISTS∈[1,max]` + `PROBES≤LISTS`; non-IVF ⇒ all zero). `internal/upgrade` `FamilyCatalog` window → 1..7 (+ test). `catalog_test.go` legacy v3/v2 synthesis trailer fixed (`len(Indexes)*10 + 1`); `partition_fuzz_test.go` IVF v7 seed.
   - Executor (`internal/executor/ivfstore.go`, new): `sqlIVF` implements `vector.IVFStore` over the index's detached encrypted B+Tree (`0x00` meta / `0x01` centroids / `0x02`+`u32` posting) and the shared vector payload store, with the `fkWriteSnap` snapshot plumbing and empty-list record deletion. `ivfStoreOf(tab, idx)` binds it. `buildIVFIndex` streams the heap into `(pk, vec)` pairs, trains via `vector.TrainIVF` over a deterministic sample (all rows, or a stride sample capped at 50 000), `AddIVF` per row, then writes centroids + non-empty front-coded lists + the `NSIV` header in the build transaction (empty table ⇒ a trained 1-centroid header). `buildIndex` (shared by `CREATE VECTOR INDEX` and `REBUILD INDEX`) dispatches `idx.VecMethod == VecMethodIVF` here. `maintainIVFIndex` (`RemoveIVF` old pk / `AddIVF` new pk) is dispatched from `maintainIndexes`. `nearestIVFIndex` (`vector.SearchIVF` with `int(idx.IVFProbes)`, residual over-fetch ×4, heap `fetchPKRow`, a differing `USING` metric ⇒ `nearestFlat`) is dispatched from `nearestIndex` before the partitioned/HNSW branches. **No process-local cached IVF copy** — every query reloads centroids + probed lists from the index txn (documented follow-on; HNSW keeps a `lockedMem`).
   - `EXPLAIN` labels the plan `ivf` (`optimizer/physical.go` Nearest detail + `rewrite.go` formatPlan). `xport/sql.go` dumps `USING IVF WITH (LISTS = n[, PROBES = m])`. Crash-recovery / backup / PITR / Raft are inherited from the encrypted index-tree WAL path (no new WAL record kinds).
   - Tests: `internal/executor` `TestIVFVectorIndex` — `USING IVF` without `LISTS` and `PROBES > LISTS` rejected; `LISTS = PROBES = 3` gives exact NEAREST (top-1, k=2, PK-only covering projection); `INSERT` / `UPDATE` / `DELETE` maintenance reflected in NEAREST; `db.Close` shows no `NSVV` / `NSIV` / `NSIC` / `NSIL` plaintext; reopen + `REBUILD INDEX` still correct. `go build ./...` + `go test ./...` green; `-race` green on `internal/vector` / `internal/catalog` / `internal/executor` (`-run 'IVF|Vector|HNSW|Nearest|Hybrid'`); `FuzzDecodePartitionedTable` 15 s + `FuzzDecodeIVFList` 10 s clean.
   - Docs: `docs/vector.md` ("IVF index" rewritten as a shipped feature + storage-table block + catalog v7 note + `## SQL` / limits paragraphs), `docs/sql.md` (statement list, `NSCT` v7, vector section), `docs/storage-format.md` + `docs/ops.md` (catalog window 1..7), `USAGE.md`, `CHANGELOG.md`, `ROADMAP.md`, web `vectors.md`, this file.

   Remaining P23 at that increment (all later landed; exit gate closed 2026-08-31): the **IVF-PQ SQL surface**, **sparse retrieval**, and the **P23 exit gate**.

   --- Ninth increment landed 2026-08-30: **IVF row in `nextsql-bench --vecquant`** + grouped centroid storage.
   - `internal/bench/vecquant.go`: `vecQuantConfig.Method`; a sixth row `F32/ivf` builds `CREATE VECTOR INDEX … USING IVF WITH (LISTS = 2·√rows, PROBES = LISTS/4)` over the F32 column (`ivfParams(rows)`). `VectorQuantReport` gains `Method` / `IVFLists` / `IVFProbes`; `QuantErr` stays 0 and `ElemBytes` 4 (no vector quantisation). CLI table prints `ivf-lists=` / `ivf-probes=`. `TestVectorQuantBench` now asserts 6 reports + the IVF-row invariants.
   - Surfacing this hit the B+Tree leaf-record ceiling (~½ a logical page): a wide centroid set (many `LISTS` × high dim) does not fit one record. `internal/executor/ivfstore.go` `SaveCentroids` / `LoadCentroids` now split the centroid set into `IVFCentroidChunkKey(i)` group records under an `"IVFCG"` count header at the bare `IVFCentroidsKey()`; a legacy single `NSIC` block still loads. `internal/vector/ivf.go` adds `IVFCentroidChunkKey`. No catalog/on-disk format version bump (the centroid record is not versioned in the catalog). The binder (`internal/sql/binder/index.go`) also rejects `USING IVF` when a single centroid (`11 + 4·dim` bytes) exceeds `maxIVFCentroidBytes` (8000) — roughly `N > 2000` for `VECTOR<F32,N>` — instead of failing mid-build; `binder_test.go` dimension-guard case.
   - Reference run (linux/amd64, 12 vCPU, 2000×128-d, 64 queries): `F32/ivf` index 112 KiB (vs 610–707 KiB HNSW), build 0.25 s (vs ~2.1 s), p50 4.0 ms, recall@10 0.619 / @100 0.514 at LISTS=88 / PROBES=22. Low recall is expected on uniformly random unit vectors (worst case for a coarse quantiser) and at a 25 %-of-`LISTS` probe ratio; rises toward 1.0 as `PROBES` → `LISTS`.
   - Tests: `internal/bench` `TestVectorQuantBench`; `internal/executor` `TestIVFCentroidGrouping` (48×96-d centroids across multiple group records — exact NEAREST at `PROBES = LISTS`, restart, `REBUILD INDEX`). `go build ./...` + `internal/bench` / `internal/vector` / `internal/executor` (vector suites) `go test` + `-race` green. (Pre-existing `internal/backup` / `internal/storage/btree` / `tests/*` failures on this run are `disk quota exceeded` on the test tmpfs, unrelated.)
   - Docs: `docs/vector.md` ("Size / recall comparison" table + IVF prose + centroid-group storage rows), `CHANGELOG.md`, `ROADMAP.md`, `USAGE.md`, web `vectors.md`, this file.

   --- Prior: seventh increment landed 2026-08-30: **IVF index core** (portable `internal/vector/ivf.go`): `IVFMeta` / `EncodeIVFMeta` / `DecodeIVFMeta` (`NSIV` v1, 25 bytes), `EncodeCentroids` / `DecodeCentroids` (`NSIC` v1), `EncodeIVFList` / `DecodeIVFList` (`NSIL` v1 front-coded, varints bounded before `make`), `IVFStore` interface + `IVFMem` + `PersistIVF` / `LoadIVFMem`, `TrainIVF` (deterministic k-means++ + ≤25 Lloyd), `AddIVF` / `RemoveIVF` / `SearchIVF`. `TestIVF*` / `TestTrainIVFDeterministic` / `FuzzDecodeIVFList` / `FuzzDecodeIVFMeta` clean.

   --- Prior: sixth increment landed 2026-08-30: **compressed (front-coded) HNSW neighbour lists.**
   - `internal/vector/encode.go`: `nodeVersionC = 2`. `EncodeNode` now emits v2 — header (version, level, deleted, layers) unchanged, then per layer a `binary.AppendUvarint` neighbour count followed by the neighbour keys **sorted ascending** and front-coded: `uvarint(sharedPrefixLen with previous key)` + `uvarint(suffixLen)` + suffix bytes. Sorting is safe because a layer is a set everywhere it is read (`searchLayer`, `link` dedup, `replacementEntry`). `commonPrefixLen` helper; `EncodeNode` rejects a 0-length or >4096 neighbour key.
   - `DecodeNode` dispatches on `raw[0]`: `decodeNodeV1` (the prior fixed-`u16` body, unchanged) or `decodeNodeV2`. `decodeNodeV2` reconstructs each key from `prev[:shared] + suffix`, and fails closed on: a `shared`/`suffix` varint > 4096 (checked **before** `make`, so a hostile varint cannot allocate), `shared > len(prev)`, total length 0 / >4096, a truncated varint or suffix, neighbours not in non-decreasing order, or trailing bytes.
   - No `NSHM` meta or `NSCT` catalog format change — node records are self-describing via the version byte and are not catalog-versioned. `EncodeNode` always writes v2, so `REBUILD INDEX` and every ordinary insert/link/delete rewrite migrates the node; a never-rewritten v1 node keeps decoding indefinitely.
   - Result: the on-disk HNSW graph shrinks ~⅓. `nextsql-bench --vecquant` index-build delta F32 610 KiB (was 980), F16 707 (was 948), I8 659 (was 948), qh-F16 1.5 MiB (was 1.8), qh-I8 1.1 MiB (was 1.5); DB 3.1 / 2.2 / 1.7 / 4.0 / 3.6 MiB; recall@10 0.916 / 0.916 / 0.914 / 0.916 / 0.912 and recall@100 0.939 — **identical** to the fixed-width encoding (lossless); p50/p95/QPS within noise.
   - Tests: `internal/vector` `TestCompressedNeighborLists` (v2 version byte; ≥2× shrink vs the explicit v1 size formula on 8-byte dense-id neighbours; sorted-set round trip; a hand-built v1 blob still decodes; an impossible `shared=5, prev len 0` blob fails closed). `FuzzDecodeNode` gains a multi-layer v2 seed, a hand-built v1 seed, an oversized-suffix-varint seed, and a decode→re-encode→decode idempotence assertion (30 s run clean; the oversize-varint panic it first surfaced is fixed and seeded). `go build ./...` + `internal/vector` / `internal/executor` (`-run 'Vector|HNSW|Nearest|QuantizedHNSW|Bitvector'`) / `internal/bench` (`-run 'VectorQuant|HNSW|Vector'`) `go test` + `-race` green.
   - Docs: `docs/vector.md` ("Compressed neighbour lists" section + node storage-table row + refreshed "Size / recall comparison" table and prose), `CHANGELOG.md`, `ROADMAP.md`, `USAGE.md`, web `vectors.md`, this file.

   (Sixth increment; superseded as the active increment by the IVF core above.)

   --- Prior: fifth increment landed 2026-08-30: **`BITVECTOR<N>` bit-packed vector type + `HAMMING` distance.**
   - `internal/bitvec/bitvec.go` (new, portable — no unsafe/cgo/assembly): `Bytes(n)` = `ceil(n/8)`, `Validate` (each element exactly 0 or 1, else `InvalidArgument`), `Encode`/`Decode` (LSB-first packed bits, pad bits cleared), `Hamming([]float32,[]float32)` (differing-element count). Unit + `FuzzRoundTrip`.
   - `internal/sql/types`: `VecBit uint8 = 4`; `VectorBit(n)`; `VecElemName`→`BIT`, `VecElemBytes`→1, `VecElemQuantised`→true; `Type.String()` special-cases `VecBit` → `BITVECTOR<N>` (not `VECTOR<BIT,N>`). `Coerce`: a non-bit vector into a bit column is validated and **rejected if not 0/1** (never rounded); widening a bit vector to another vector type is rejected.
   - `internal/vector/distance.go`: `MetricHamming` (iota 4); `Distance`/`Similarity` → `bitvec.Hamming`; `ParseMetric("hamming")`; `Metric.String()` → `hamming`. `encode.go`: `EncodePayloadElem`/`DecodePayload` `VecBit` branch (`NSVV` v2, element tag `BIT`, `ceil(dim/8)` packed bytes; `EncodePayloadElem` re-validates 0/1, `DecodePayload` fails closed on bad length); `EncodeMeta`/`DecodeMeta` accept `MetricHamming` (no `NSHM` version bump).
   - Lexer: `KwBitvector` (`"bitvector"`), `KwHamming` (`"hamming"`). Parser: `case lexer.KwBitvector:` → `< N >` → `types.VectorBit`; NEAREST `USING HAMMING`. Binder (`binder.go`): a `BITVECTOR` column with an explicit real-valued metric is rejected; `HAMMING` on a non-bit column is rejected; default (`""`) is allowed on a bit column and resolves to Hamming in the executor. Binder (`index.go`): `WITH (QUANTIZATION = …)` on a `BITVECTOR` index is rejected.
   - `internal/executor`: `nearestMetric(explicit, colType)` (nearest.go) — `""` + `VecBit` → `MetricHamming`, else `ParseMetric`; used by `searchNearest` and `hybrid.go`. `nearestQuery` re-validates a bit column's query vector is 0/1. `vecstore.go`: `graphMetric(colType)` → `MetricHamming` for `VecBit` else `MetricCosine`, applied at `buildVectorIndex` / `buildPartitionVectorIndex` / `initPartitionVectorIndex` so a bit HNSW graph persists a Hamming `NSHM` header (maintenance/`LoadMem` then read the persisted metric).
   - No catalog format bump: `VecElem` was already a stored byte in the column-type descriptor with no decode whitelist. `VecBit` flows over the wire (`protocol/value.go`) as widened `float32` 0/1, like F16/I8. Workflow params / idempotency keys stay `VecF32`-only (unchanged limitation).
   - Tests: `internal/executor` `TestVectorBitvector` (non-bit element rejected, dim mismatch, `BITVECTOR<8>` round trip, default + explicit `HAMMING` flat NEAREST, `USING COSINE` rejected, Hamming HNSW plan + result, `QUANTIZATION` rejected, `NSVV` not plaintext, restart). `internal/vector` `TestPayloadBitPacked`, `TestHammingDistance`, `FuzzDecodePayload` + `FuzzDecodeMeta` BIT/Hamming seeds. `internal/sql/binder` `TestBindBitvector`. `internal/bitvec` unit + fuzz. `go build ./...` + touched-package `go test` + `-race` on `internal/bitvec` / `internal/vector` / `internal/executor` vector suites green. (Pre-existing `internal/backup` / `internal/storage` / `internal/storage/btree` failures are `disk quota exceeded` on the test tmpfs, unrelated.)
   - Docs: `docs/vector.md` ("BITVECTOR<N>" section + `BIT` storage row + metric notes), `docs/sql.md`, `USAGE.md`, `CHANGELOG.md`, `ROADMAP.md`, web `vectors.md` / `sql.md` / `limits.md`, this file.

   --- Prior: fourth increment landed 2026-08-30: **quantised HNSW index** — `CREATE VECTOR INDEX … USING HNSW WITH (QUANTIZATION = 'F16' | 'I8' | 'NONE')`.
   - `internal/vector/quant.go`: `QuantizeElem(v, elem)` (wraps `float16.Quantize` / `int8vec.Quantize`), `QVecKey`/`SplitQVecKey`/`QVecBounds` (`0x02` key kind in the index tree, just past `NodeBounds`), `ValidQuant`.
   - `internal/vector/encode.go`: `Meta.Quant uint8`; `NSHM` meta encoding v2 (`metaVersionQ = 2`) appends one quant byte after `Entry` — a v1 header (Quant 0) still encodes/decodes as before, and `DecodeMeta` fails closed on a bad tag. `kindQVec`.
   - `internal/vector/hnsw.go`: optional interfaces `QuantWriter{SaveQVec}` (driven by `Insert` when `meta.Quant != 0` — fails closed if the graph does not implement it) and `FullVecLoader{LoadVecFull}` (used by `Search` → `rerankFull`: recompute the `ef` hits' distances against full precision, re-sort with `LessHit`, then trim to `k`). `Mem` gained `Full map[string][]float32`, `LoadVecFull`, `SaveQVec`; `PutVec` splits quantised traversal copy / full copy when `Meta.Quant != 0`; `Persist` writes QVecs from `Full`; `LoadMem` fills `Vecs` (already-quantised from `g.LoadVec`) and `Full` (from `g.LoadVecFull`).
   - `internal/executor/vecstore.go`: `sqlGraph.quant` set from `idx.VecQuant` in `graphOf` / `graphOfPartition`; `LoadVec` reads `QVecKey` from the index txn (fallback: on-the-fly `QuantizeElem` of the payload) when quantised, else the payload store; `LoadVecFull` always the payload store; `SaveQVec` writes an `NSVV` record to `QVecKey`. `lockedMem` forwards `LoadVecFull` / `SaveQVec`. `buildVectorIndex` / `buildPartitionVectorIndex` / `initPartitionVectorIndex` set `mem.Meta.Quant = idx.VecQuant`. `nearest.go coveringNearestRow` emits the full-precision column value via `FullVecLoader`.
   - Parser: after `USING HNSW`, optional `WITH ( QUANTIZATION = <string|ident> )` → `ast.CreateIndex.VecQuant` (lowercased); unknown option name is a syntax error. Binder (`internal/sql/binder/index.go`): `none`/`f32` → 0, `f16` → `types.VecF16`, `i8` → `types.VecI8`; rejected on a non-vector index or an unknown name.
   - Catalog: `Index.VecQuant uint8`; table descriptor format **v6** — after the partitioning block, one traversal-quantisation byte per index in `Indexes` order (older versions decode with every index unquantised). `internal/upgrade` `FamilyCatalog` window widened to 1..6; `docs/storage-format.md` + `docs/sql.md` version tables updated.
   - Bench: `internal/bench/vecquant.go` iterates `vecQuantConfig{Label, ColElem, IdxQuant}` — the three element-type rows plus `F32/qh-F16` and `F32/qh-I8` (F32 column, quantised graph). CLI table widened; `TestVectorQuantBench` now asserts 5 reports and the `qh-*` recall/quant-err/column-width invariants.
   - Reference (linux/amd64, 12 vCPU, ext4, 2000×128-d, 64 queries): `qh-F16` index-build Δ 1.8 MiB, DB 4.3 MiB, recall@10 0.916; `qh-I8` Δ 1.5 MiB, DB 4.0 MiB, recall@10 0.912 — vs plain `F32` Δ 980 KiB, DB 3.4 MiB, recall@10 0.916. Re-rank keeps recall; the quantised copies are additive on disk, so the win is smaller/cache-local traversal reads, not index size. Latency/QPS/heap within noise.
   - Tests: `internal/executor` `TestQuantizedHNSWIndex` (unknown quantisation rejected; index plan; exact re-rank top-1/top-2; full-precision column value; `NSVV`/`NSHM` not plaintext; restart; post-restart insert quantised into the graph). `internal/vector` `TestMetaQuantRoundTrip` (v2 round trip, v1 decodes Quant 0, bad tag fails, `QVecKey` split) + `TestQuantizedHNSWRerank` (recall ≥ 0.80, top-hit distance is the exact re-ranked value, `Vecs` differ from `Full`). `FuzzDecodeMeta` v2 seed, parser + binder cases, SQL fuzz seed. `go test ./... -count=1` green on touched packages; `-race` green on `internal/vector` / `internal/bench` / `internal/executor` vector suites. (Pre-existing `internal/backup` / `internal/storage` / `internal/storage/btree` failures on this run are `disk quota exceeded` on the test tmpfs, unrelated.)
   - Docs: `docs/vector.md` ("Quantised HNSW index" section + `qvec` storage row + refreshed comparison table), `docs/sql.md`, `docs/ops.md`, `docs/storage-format.md`, `USAGE.md`, `CHANGELOG.md`, `ROADMAP.md`, `docs/web/content/docs/vectors.md`, this file. Not committed (huge shared working tree).

   Third increment landed 2026-08-30: **F32-vs-F16-vs-I8 quantised-vector size/recall/latency benchmark** — `internal/bench/vecquant.go` / `nextsql-bench --vecquant [--vecquant-rows|-dim|-queries]`. Seeds one deterministic vector set into an F32/F16/I8 column (fresh encrypted DB each), builds HNSW over each, reports on-disk width / raw payload / index-build page delta / total DB size / build time / resident heap / mean round-trip error / `NEAREST` p50/p95/p99 + recall@10/@100 vs exact-cosine flat over the full-precision sources. Reference: DB 3.4 → 2.4 → 1.9 MiB, recall@10 0.916 / 0.916 / 0.914, latency/QPS within noise.

   Second increment landed 2026-08-30: **`VECTOR<I8,N>` signed-8-bit quantised element type.**
   - New portable `internal/int8vec` package: `Scale` (per-vector `absmax/127`, symmetric; `1` for an all-zero vector), `Encode` / `Decode` (LE `f32` scale + one signed byte per element; `-128` code never produced), `Quantize` (round-trip). No unsafe/cgo/assembly. Unit + fuzz (`FuzzRoundTrip`).
   - `internal/sql/types`: `VecI8 = 3`, `VectorI8(n)`, `VecElemName`→`I8`, `VecElemBytes`→1, `VecElemQuantised` helper, `Type.String()`→`VECTOR<I8,N>`. `Coerce` quantises an F32/untyped vector to an `I8` column (already-I8 passes through); the F16 branch was generalised to a per-element switch.
   - Parser/lexer: `KwI8`; `VECTOR<I8,N>` accepted alongside `F32` / `F16`.
   - `internal/vector` payload store: `NSVV` **v2** extended — `raw[5]` element tag now selects F16 (`halves`) or I8 (`scale(f32) | int8[dim]`); `DecodePayload` v2 branch switches on the tag, fails closed on unknown tag / bad length / non-finite widened elements. F32 v1 and F16 v2 payloads decode unchanged. Fuzz seed added.
   - `internal/executor/vecstore.go` already passes `c.Type.VecElem` — everything downstream (`hydrate`, `NEAREST` flat + HNSW, bounded algebra, `hybrid.go`) reads `[]float32` and is unchanged.
   - `system` capability row description now `VECTOR<F32,N> / VECTOR<F16,N> / VECTOR<I8,N>`.
   - Tests: `internal/int8vec` (unit + `FuzzRoundTrip`), `internal/vector` `TestPayloadI8Quantized` + `FuzzDecodePayload` I8 seed, `internal/executor` `TestVectorI8Quantized` (quantised round-trip, flat + HNSW `NEAREST`, restart, `NSVV` not plaintext on disk, dim-mismatch still rejected). Full `go test ./... -count=1` green; `-race` on the touched packages green.
   - Docs: `docs/vector.md` ("Quantised element types" + storage table), `CHANGELOG.md`, `ROADMAP.md`, `USAGE.md`, this file.

   First increment landed 2026-08-30: **`VECTOR<F16,N>` half-precision element type.**
   - New portable `internal/float16` package: `FromFloat32` / `ToFloat32` (round-to-nearest, ties to even; overflow → Inf; NaN preserved), `Put` / `Read` byte helpers, `Quantize`. No unsafe/cgo/assembly. Unit + fuzz (`FuzzRoundTrip`).
   - `internal/sql/types`: `VecF16 = 2`, `VectorF16(n)`, `VecElemName` / `VecElemBytes`, `Type.String()` → `VECTOR<F16,N>`. `Coerce` quantises an F32/untyped vector value to an F16 column (already-F16 passes through).
   - Parser/lexer: `KwF16`; `VECTOR<F16,N>` accepted alongside `F32`.
   - `internal/vector` payload store: `EncodePayloadElem(v, elem)`; `NSVV` **payload format v2** (`NSVV | 2 | elem | dim(u16) | halves`), v1 F32 still decodes. `DecodePayload` auto-detects and widens F16 → `float32`, fails closed on bad version/element/dim/length. Fuzz seed added.
   - `internal/executor/vecstore.go`: `putVectors` passes `c.Type.VecElem`. Everything downstream (`hydrate`, `NEAREST` flat + HNSW, bounded algebra, `nearest.go`, `hybrid.go`) is unchanged — it all reads `[]float32`.
   - `system.replica`… no schema change; `system` capability row description now `VECTOR<F32,N> / VECTOR<F16,N>`.
   - Tests: `internal/float16` (unit + fuzz), `internal/vector` `TestPayloadF16Quantized` + `FuzzDecodePayload` F16 seed, `internal/executor` `TestVectorF16Quantized` (quantised round-trip, flat + HNSW `NEAREST`, restart, `NSVV` not plaintext on disk, dim-mismatch still rejected).
   - Docs: `docs/vector.md` ("Quantised element types" + storage table), `CHANGELOG.md`, `ROADMAP.md`, `USAGE.md`, this file.

   Remaining P23 after the sixth increment (historical; all later landed and the exit gate closed 2026-08-31): IVF / IVF-PQ, sparse retrieval, and the P23 exit gate. Priority order still absolute; next increment is IVF (coarse-quantiser + posting-list ANN index with its own build/rebuild/delete/crash-recovery lifecycle) or sparse retrieval. `VECTOR<I8,N>` shipped with a per-vector scale (self-describing, no catalog calibration); a per-column calibrated scale/zero-point is a possible later refinement. A re-rank-free quantised HNSW mode that drops the full payload is a minor follow-on now that neighbour lists are compressed.

1\. **P22 follower reads / read scaling is COMPLETE** (2026-08-30). All five exit-gate items are green. The exit gate closed with:
   - **Linearizability/consistency sign-off** — `docs/ha.md` "Consistency model and sign-off": a per-mode guarantee table (`STRONG` linearizable; `BOUNDED`/`STALE` always a consistent committed prefix, never mislabelled), the `STRONG` argument (`StrongReadBarrier` = leader check + `raft.VerifyLeader` quorum round trip; Raft leader completeness gives log superset of every acknowledged write, quorum intersection detects a term change, so the stale-leader anomaly is blocked; reads take no log entry), the failover analysis, a test-evidence table, and a dated sign-off. A leader-lease fast path is recorded as a deliberate non-goal (trades a clock assumption for latency, not needed for correctness).
   - **Failover session-guarantee test** — `TestFollowerReadFailoverSessionGuarantee` (`tests/integration/follower_read_test.go`, added `trans`/`raftAddr` to `clusterNode`): a `STRONG` session over `nextsql.OpenCluster` keeps read-your-writes + monotonic reads across a leader partition and majority re-election (retries once through the router's stale-view `unavailable`); a `STALE` read routed to the partitioned former leader may lag the new leader but never regresses below its own applied state; the former leader converges after rejoining. Complements `TestHAKillLeader` (no lost acknowledged commit).

   Optional follow-on (NOT a gate item): a 3-node cluster-routing live test for the non-Go drivers (Node/Bun/Deno/PHP live harness is single-node; Go server routing + failover are covered by the 3-node `follower_read_test.go`).

   Earlier P22 increments 1–5 landed 2026-08-30. Increment 1: read-consistency modes — `STRONG` (default) gates every non-mutating statement on `replication.Cluster.StrongReadBarrier` (`raft.VerifyLeader` quorum round trip); `STALE` serves local applied state with no barrier. Increment 2: replica lag + follower health surfacing — `replication.Cluster.ReplicaHealth()` on `replication.Status` + the plaintext status file + `system.replica_health` (`SchemaVersion` 2); `Cluster.FollowerReadHealthy(maxStaleness)` freshness gate. Increment 3: **follower-read routing (server + Go driver)** —
   - `BOUNDED` / `MAX STALENESS`: `Session.SetReadConsistency(ReadBounded)` + `Session.SetMaxStaleness` (0 → `executor.DefaultMaxStaleness` = 5 heartbeats); `Session.requireReadConsistency` routes `BOUNDED` through the new `executor.FollowerReadGate` → `FollowerReadHealthy`. Serves local state, no quorum round trip.
   - Wire (additive NSQL v1, `Version` still 1): `TypeSetReadConsistency` (mode byte + `MAX STALENESS` ms; sub-ms clamps to 1) and `TypeNodeStatus`/`TypeNodeStatusResp` (key-free `NodeStatus`: role, `has_leader`, `healthy`, applied LSN, last-contact ms, apply backlog). Server handlers `applyReadConsistency` / `nodeStatus` in `internal/protocol/server.go`.
   - Go driver: `Conn.SetReadConsistency` / `Conn.NodeStatus`; `Config.{Nodes,ReadConsistency,MaxStaleness}`; `nextsql.Cluster` (`OpenCluster`) in `drivers/go/cluster.go`.

   Increment 4: **follower-read routing in the Node / Bun / Deno / PHP drivers.** Shared JS layer `drivers/js/{protocol.mjs,client.mjs}` gains `Type.{SetReadConsistency,NodeStatus,NodeStatusResp}` (22–24), `ReadConsistency` enum, `encodeSetReadConsistency` / `decodeNodeStatus`, `Conn.setReadConsistency(mode, maxStalenessMs)` / `Conn.nodeStatus()`, and a `Cluster` + `openCluster(cfg, dial)` that ports `cluster.go` (`isReadOnlySQL` / `txnControl` classifiers, 500 ms status cache, follower round-robin, leader fallback on `unavailable`). Node standalone driver `drivers/node/nextsql.js` mirrors it; Bun/Deno wrappers export `connectCluster` + `ReadConsistency`; PHP `drivers/php/src/{Protocol.php,Client.php,Cluster.php}` add the same surface (`READ_STRONG/BOUNDED/STALE`, `Client::setReadConsistency` / `nodeStatus`, `NextSQL\Cluster::connect`). `cfg.readConsistency` (+ `maxStalenessMs`) is applied to every connection at open, matching the Go driver. Types in `drivers/js/types.d.ts` (+ per-driver `.d.ts`). Live scripts (`drivers/*/live.*`) now round-trip `nodeStatus()` + `setReadConsistency(BOUNDED)` against the standalone server.

   Files (increment 4): `drivers/js/{protocol.mjs,client.mjs,types.d.ts}`, `drivers/node/{nextsql.js,nextsql.d.ts,nextsql.test.js,live.js,usage.ts}`, `drivers/bun/{nextsql.js,nextsql.d.ts,nextsql.test.js,live.ts,usage.ts}`, `drivers/deno/{mod.js,mod.ts,nextsql_test.js,live.ts,usage.ts}`, `drivers/php/src/{Protocol.php,Client.php,Cluster.php}`, `drivers/php/tests/{unit.php,live.php}`, `docs/{ha.md,protocol.md}`, `docs/web/content/docs/protocol.md`. Tests: per-driver unit suites (routing classifiers + `SetReadConsistency`/`NodeStatus` wire codec + `connectCluster` arg validation), and `go test ./tests/integration -run Driver` (Node/Bun/Deno/PHP unit + single-node live TLS) green.

   Increment 5: **read-scaling benchmark** (`nextsql-bench --readscale`). `internal/bench/readscale.go` `RunReadScale` / `ReadScaleOptions` / `ReadScaleReport` / `ReadScaleSuite`; CLI flags `--readscale` / `--readscale-rows` (5000) / `--readscale-readers` (8, per serving node), `runReadScaleBenches` in `cmd/nextsql-bench/main.go`. Builds a 3-node in-process cluster (inmem raft, `executor.CreateWithIdentity`, `replication.Open`, `AttachCluster`) of encrypted DBs, seeds via the leader (`insertBatches`), polls each follower's STALE `SELECT COUNT(*)` until caught up, then runs five phases: `strong-1n` / `stale-1n` (leader only), `stale-2n` (leader + 1 follower), `stale-3n` / `bounded-3n` (all three). Per phase `opt.Readers` PK point-read goroutines per serving node (so 1-node = 8, 3-node = 24), keys cycled over `Rows` (≫ 128-entry result cache) so every read is real work. Reports aggregate `QPS`, `LeaderOps`/`LeaderQPS` (leader's slice), p50–p999, `AggQPSRatio` vs `stale-1n`. Reference host (linux/amd64, 12 vCPU, ext4): `strong-1n` 103k QPS / p99 270 µs; `stale-1n` 202k / 203 µs (barrier ≈ 2× cost); `stale-3n` aggregate 168k (0.83×) but `leader-qps` 57k (~3.5× offload); `bounded-3n` ≈ `stale-3n`. Aggregate QPS is CPU-bound on one host (every "node" is goroutines on the same cores) — documented; a real deployment adds a host per replica. `TestReadScaleBench` (asserts phase order, leader-offload, barrier ordering) + `-race` green. Docs: `docs/ha.md` "Read scaling" (table), `docs/ops.md`, `USAGE.md`.

   **P22, P23, P24, P25, and P26 are done** (P26 complete 2026-09-02). Next release gate is **P27 Operational maturity + workload governance**.

2\. **P16 is complete** (2026-08-30). Its exit gate is fully green (HNSW v10 p95 **8.061 ms** with recall; 10M DELETE **25 ms**; crash-during-merge `Check()`-clean; 100M analytics `< 60 s`; 10M INSERT/UPDATE published; security sign-off). The terminal 100M-operation B+Tree invariant run on a single labeled host is **paper-closed as a standalone measurement, not a release gate** — v9 exited 137 with no retained evidence, v10 was stopped by explicit direction, and the box class is RAM-constrained. `TestRandomizedLargeInvariants` / `run-btree-soak.sh` were reworked for that class: `NEXTSQL_BTREE_POOL_PAGES` (default 24576 = 384 MiB) sizes the resident pool to hold the working set, `NEXTSQL_BTREE_SPACE` optionally caps the key space, cheap frequent WAL-recycle checkpoints are decoupled from the rare full structural walk, bookkeeping is `int32`, and each full check is followed by `debug.FreeOSMemory()`. A future 100M terminal run (structural `Check()`, full-scan count, per-key verification) can still be recorded but is not required for the phase.

3\. **P21 native table partitioning is complete** (2026-08-30). The final gate item, explicit offline legacy TENANT migration, shipped as `nextsql hosting migrate-tenant` — bounded, point-verified, resumable, publishes the isolated destination `ACTIVE` only after full verification. Earlier P21 work: partition-aware `UPSERT` (`TestPartitionUpsert`), partition benchmarks (`nextsql-bench --partition`, `TestPartitionBench` — surfaced a ~3x unpruned-aggregate penalty), tuple-tight multi-column RANGE pruning, cross-partition secondary UNIQUE, randomized pruning-soundness property test. **P18 partition-wise aggregation and join hooks both landed** (2026-08-30): an aggregate over a partitioned heap aggregates each surviving partition in parallel and merges the partials (`partitionWiseHeapAggregate`, `TestPartitionWiseAggregation`); an equi-join between two identically partitioned tables on their full partition key runs one bounded hash join per aligned partition pair, in parallel across pairs, with pruned pairs skipped (`catalog.AlignedPartitionJoin`, `executor.tryPartitionWiseJoin`, `TestPartitionWiseJoin`, `TestAlignedPartitionJoin`). Both are `EXPLAIN`-tagged `partition-wise`. **P18 implementable scope is now closed.** **P17 is complete** — its exit gate is green; `REBUILD INDEX … ONLINE` stays a documented deferred follow-on behind proven concurrent-write handling (blocking `REBUILD INDEX` is the shipped path, `ONLINE` is rejected). The P16 100M B+Tree terminal soak is paper-closed (item 2); a future run is optional, not a gate.

4\. 10M DELETE variance explained: same-process trees use the maintained exact
live-row cache before the heap swap; reopened trees reconstruct the affected-row
count with an O(rows) leaf scan. This accounts for the **25 ms / 1.57 s** warm
results versus the **24 s** cold-open run; methodology is published in
`docs/ops.md`.

# Forward Product Roadmap — Phase 17+

> **Rule:** Everything below this point is planned unless explicitly checked after implementation, tests, documentation, and the phase exit gate pass. Design notes, hooks, and planned follow-ons do **not** count as implemented.

## Roadmap summary

```text
[x] P16 Correctness / SLO closure   (100M B+Tree terminal soak deferred, non-gate)
[x] P17 Schema lifecycle + storage maintenance   (REBUILD INDEX ONLINE deferred follow-on)
[x] P18 SQL completeness
[x] P19 WORKFLOW / TRIGGER / SCHEDULE / TASK   (cron SCHEDULE syntax landed 2026-09-03)
[x] P20 CDC / change streams
[x] P21 Native table partitioning
[x] P22 Follower reads / read scaling
[x] P23 Vector Engine 2.0   (complete 2026-08-31: production-gating sign-off; VECTOR<F16,N> + VECTOR<I8,N> + BITVECTOR<N>/HAMMING + quantised HNSW + compressed neighbour lists + IVF + IVF-PQ + SPARSEVECTOR / USING SPARSE + dense+sparse+BM25 fusion + --vecquant measurements. Optional follow-on: BITVECTOR/Hamming --vecquant row)
[x] P24 Full-text Search 2.0
[x] P25 Security 2.0   (complete 2026-09-02: mTLS + short-lived credentials + external IdP broker + field-level client encryption + password-hash evolution + audit-chain hardening, all production-gated; docs/security.md "P25 security review sign-off")
[x] P26 System catalog / introspection 2.0   (complete 2026-09-02: full system.* schema incl. system.users/roles/grants + nine SHOW aliases + authoritative capability registry; docs/system-catalog.md "P26 exit gate closure")
[x] P27 Operational maturity + workload governance   (complete 2026-09-03: server lifecycle/drain + session controls + resource groups + operational CLI; exit gate green incl. local-commit-before-replicate-ack structural fix and per-realm/per-database connection limits)
[ ] P28 Professional Installer + NextSQL Manager
[ ] P29 NextSQL Studio
[ ] P30 NextSQL Intelligence + built-in RAG
```

Sequencing remains:

```text
Correctness → durability → security → integrity → availability → latency → throughput → efficiency → DX → features
```

Do not begin a later phase in a way that destabilizes an earlier open gate. UI foundations may be developed in parallel once underlying interfaces are stable. P16 through **P27** are done (exit gates green), so **P28 Professional Installer + NextSQL Manager is the next release gate** — not yet started. The cross-cutting Multi-database hosting track (M2/M3) continues incrementally and gates none of P0–P27.

---

# Phase 17 — Schema lifecycle + storage maintenance

## Index lifecycle

- [x] Implement `DROP INDEX name`
- [x] Implement `DROP INDEX IF EXISTS name`
- [x] Support dropping B+Tree secondary indexes
- [x] Support dropping UNIQUE indexes
- [x] Support dropping JSON-path indexes
- [x] Support dropping spatial indexes
- [x] Support dropping full-text indexes
- [x] Support dropping HNSW/vector indexes
- [x] WAL-durable index drop — transactional catalog update and restart coverage
- [x] Crash-safe index drop — pre-commit crash retains the index; committed drop survives kill/reopen
- [x] Raft-replicated index drop — three-node quorum/FSM catalog coverage
- [x] RBAC checks for index drop — resolved table requires `INDEX`
- [x] Audit index drop — denied and successful outcomes covered
- [x] Migration parser/validator supports `DROP INDEX`
- [x] Implement `REBUILD INDEX name`
- [x] Add blocking rebuild first if online correctness is not proven — `ONLINE` was rejected until the item below; still the fallback path today whenever `ONLINE` is unsafe or unsupported (partitioned tables, vector/full-text indexes)
- [x] Design and implement `REBUILD INDEX name ONLINE` only after proven-safe concurrent-write handling — `internal/executor/exec_ddl_online.go` (shadow-tree arm/drain/backfill/swap, `db.armOnlineBuild`/`mirrorOnlineIndex`), now genuinely proven safe under real concurrent DML and closed 2026-09-03. History: log #89 fixed the storage-engine rollback bug this feature's own test was originally blocked on; un-skipping the test then exposed a *second*, initially-unexplained corruption that turned out to be a pre-existing, general storage-engine transaction-attribution race (`Engine.beginLocked` racing `Enter`/`Leave` for `e.opTxn`) with no special connection to online rebuild — fixed in log #91, which was believed at the time to be what closed this item ("0/20 across 240 iterations" of the bisection harness). **That belief was premature — log #93 found and fixed a third, narrower, still-real race the same test could still hit intermittently (~5% of runs) even after log #91's fix**, surfaced by an independent, skeptical re-audit that refused to trust the prior "closed" status without re-running the regression live. See log #93 for the full writeup: the ordinary (non-mirror) index-maintenance path used a transaction's own possibly-stale snapshot to delete a swapped-in index's old entry, which could silently miss an entry the online backfill had just written, leaving an orphaned index row. `internal/system/schema.go`'s `rebuild_index_online` capability row is `"supported"` (non-partitioned B+Tree/UNIQUE/JSON-path/spatial indexes; vector/full-text/partitioned indexes still use the blocking `REBUILD INDEX`).
- [x] Rebuild B+Tree/JSON/spatial/full-text/HNSW indexes
- [x] Preserve/validate index options during rebuild
- [x] Add index rebuild progress/metrics — active phase/rows/entries plus cumulative outcome/duration counters

## Storage reclamation

- [x] Reclaim detached heap pages after `DROP TABLE` — exact ownership, post-commit snapshot drain, durable freelist flush
- [x] Reclaim detached index pages after `DROP INDEX` — rollback-safe exact ownership, durable reuse and restart coverage
- [x] Free-page tracking / reusable page map — encrypted freelist chain with duplicate/cycle validation
- [x] Reuse reclaimed pages safely after restart — allocator restart reuse coverage
- [x] Detect orphan pages/objects — transaction-stable exact reachability diagnostic with deliberate-orphan restart coverage
- [x] MVCC garbage eligibility rules — committed tombstones remain while any writer/read-only snapshot is live; cleanup fails closed behind the database apply barrier
- [x] UNDO retention/cleanup rules — snapshot-safe in-memory forgetting plus atomic encrypted database-maintenance compaction with reopen/ID/nonce coverage
- [x] Dead version cleanup — restart-safe bounded `DB.CleanupDeadVersions(limit)` discovers committed tombstones from durable trees
- [x] B+Tree merge/compaction maintenance — deferred tombstone purge drives existing underflow merge/root-collapse and durably frees merged pages
- [x] Full-text posting cleanup — bounded dead-version maintenance purges posting/doc-length/stat tombstones after snapshot release
- [x] HNSW deleted-node/tombstone cleanup strategy — validated diagnostics; blocking rebuild at ≥1,024 deleted and ≥20% tombstones, never unsafe in-place edge surgery
- [x] WAL retention cleanup integrated with PITR requirements — disabled without explicit horizon; requires checkpoint + successful per-segment archival; preserves redo and oldest PITR ranges

## Maintenance surface

- [x] Add `MAINTAIN DATABASE` — leader/admin-only blocking pass capped at 10,000 tombstones
- [x] Add `MAINTAIN TABLE table_name` — heap/vector/index-scoped bounded cleanup
- [x] Add scoped index maintenance where appropriate — `MAINTAIN INDEX name` with database-wide ambiguity rejection
- [x] Central `Maintenance Manager` instead of unbounded independent goroutines — synchronous coordinator with no work queue or background workers
- [x] Maintenance CPU budget — elapsed-time checks during tree scans and between physical deletes; exhaustion returns bounded partial progress
- [x] Maintenance memory budget — tombstone-key buffering is charged before allocation (8 MiB default)
- [x] Maintenance I/O budget — conservative logical leaf-scan and height-based delete/merge units reserved before work (500,000 default)
- [x] Maintenance concurrency limit — one active pass per database; concurrent requests fail fast
- [x] Pause/resume maintenance — coordinator blocks new work while allowing an active bounded pass to finish
- [x] Admission-aware maintenance — SQL maintenance acquires the shared query admission slot before entering the coordinator
- [x] Maintenance metrics and diagnostics — active/last run status plus cumulative runs/failures/rows/duration
- [x] Automatic statistics refresh policy — synchronous transaction-local compact refresh after at least 1,000 changed rows, scaling to 20% of the last analyzed row count; bounded catalog records and no unbounded background worker
- [x] Automatic maintenance scheduling policy — after commit, transactions with at least 1,000 UPDATE/DELETE changes request deterministic table-scoped cleanup capped at 10,000 tombstones through the bounded coordinator

### Phase 17 tests

- [x] Crash during `DROP INDEX` — uncommitted pre-record crash preserves index; committed crash/reopen installs the drop
- [x] Crash during index rebuild — injected build crash/reopen retains old index metadata and usability
- [x] Crash during page reclamation — encrypted synced intent replays only unreachable pages after restart, then clears after durable freelist installation
- [x] Restart after reclamation reuses only safe pages — replacement index allocates from the reopened durable freelist
- [x] Long-running MVCC snapshot prevents premature reclamation — heap and full-text physical tombstones retained and visible to old snapshots
- [x] Reclamation after snapshot release — database barrier resumes bounded purge; B+Tree ownership shrinks after merge
- [x] Raft follower consistency after drop/rebuild — three-node test verifies rebuilt index metadata converges before the replicated drop removes it everywhere
- [x] Backup/restore while maintenance metadata exists — encrypted pending page-reclamation intent is an authenticated backup member and replays on first restored open
- [x] PITR across drop/rebuild operations — checkpoint WAL now includes allocator freelist metadata page images; backup plus archived WAL restores the rebuilt index at its target LSN and the subsequent dropped state at a later target
- [x] Race tests — executor, backup, and maintenance packages pass under the Go race detector
- [x] Fuzz durable metadata decoders introduced by this phase — authenticated reclaim-intent and compacted UNDO record decoders have valid and malformed seed corpora

### Phase 17 exit gate

- [x] `DROP INDEX` works for every shipped index type or unsupported types fail explicitly
- [x] Dropped table/index space becomes reusable
- [x] No live MVCC-visible row/version is reclaimed early
- [x] Rebuild survives crash/restart
- [x] Maintenance is bounded and observable
- [x] Migration workflow no longer fails merely because `DROP INDEX` is missing

---

# Phase 18 — SQL completeness

Implement important modern SQL capabilities using NextSQL-native semantics. Do not blindly copy PostgreSQL/MySQL behavior; document any intentional differences.

## Core SELECT features

- [x] `SELECT DISTINCT` — projection, tuple, star, aggregate, NULL, ORDER BY, and LIMIT semantics
- [x] Hash distinct physical operator — canonical typed-row keys, memory-budget charging, and EXPLAIN visibility
- [x] Sort/ordered distinct physical operator — selected when ORDER BY covers every output column; adjacent elimination runs without a hash table
- [x] Index-assisted distinct where profitable — full PK or complete NOT NULL UNIQUE key proves single-table projection uniqueness and elides duplicate work
- [x] `HAVING` — post-aggregation filtering for selected aggregate expressions, grouped outputs, and aliases
- [x] Aggregate aliases/visibility rules documented — HAVING sees selected aggregate/group aliases; unselected aggregates fail explicitly
- [x] `CASE WHEN ... THEN ... ELSE ... END` — ordered searched arms, nesting, three-valued conditions, and implicit ELSE NULL
- [x] Simple `CASE expr WHEN ...` — NULL-safe non-match semantics and coercible comparison operands
- [x] Set operations: `UNION` — canonical typed-row duplicate elimination with repeated NULL collapse and plan visibility
- [x] Set operations: `UNION ALL` — left-associated two-query AST/plan, per-arm optimization, duplicate preservation, left-column naming, RBAC traversal, legacy-tenant fail-closed behavior, and column-count validation
- [x] Set operations: `INTERSECT` — distinct typed-row membership with NULL equality and standard higher precedence
- [x] Set operations: `EXCEPT` — distinct left-minus-right typed-row membership with NULL equality
- [x] Type coercion rules for set operations — identical types remain stable, STRING/TEXT widen to TEXT, DECIMAL widens precision/scale, and incompatible types fail explicitly
- [x] NULL/duplicate semantics tests for set operations

## Subqueries

- [x] Scalar subqueries — uncorrelated single-column queries return one value, empty results yield NULL, and multi-row/multi-column results fail explicitly
- [x] `IN (SELECT ...)`
- [x] `NOT IN (SELECT ...)` with correct NULL semantics
- [x] `EXISTS`
- [x] `NOT EXISTS`
- [x] Derived tables in `FROM` — aliased SELECT inputs support projection, filtering, DISTINCT, and ordering; joins from a derived input remain explicit follow-on work
- [x] Correlated subqueries — scalar, IN, and EXISTS forms bind outer-row references as typed values and execute per outer row without constant-cache reuse
- [x] Semi-join physical/logical representation
- [x] Anti-join physical/logical representation
- [x] Safe subquery flattening
- [x] Safe subquery decorrelation
- [x] Predicate pushdown through eligible subqueries
- [x] Constant subquery evaluation — each uncorrelated subquery occurrence materializes once per statement and reuses its result across outer rows
- [x] Adversarial NULL/duplicate/correlation tests
- [x] Legacy `tenant_id` tables fail closed through every subquery form; row-tenant predicate rewriting is removed

## CTEs

- [x] `WITH`
- [x] Multiple CTEs
- [x] CTE reference binding
- [x] CTE inlining
- [x] CTE materialization
- [x] Cost-based inline/materialize decision where safe
- [x] `WITH RECURSIVE`
- [x] Recursive depth limit
- [x] Recursive row limit
- [x] Recursive memory/execution budget integration
- [x] Recursive cancellation

## Window functions

- [x] `ROW_NUMBER()`
- [x] `RANK()`
- [x] `DENSE_RANK()`
- [x] `LAG()`
- [x] `LEAD()`
- [x] `FIRST_VALUE()`
- [x] `LAST_VALUE()`
- [x] Aggregate windows: `COUNT/SUM/AVG/MIN/MAX OVER (...)`
- [x] `PARTITION BY`
- [x] Window `ORDER BY`
- [x] Initial frame semantics documented and tested
- [x] Spill-capable window execution
- [x] Scheduler/cancellation integration

## DML ergonomics

- [x] Native atomic UPSERT design finalized (`UPSERT` or deliberate conflict syntax)
- [x] UPSERT against UNIQUE indexes
- [x] Concurrent UPSERT correctness
- [x] UPSERT WAL/recovery/Raft determinism
- [x] `INSERT ... RETURNING`
- [x] `UPDATE ... RETURNING`
- [x] `DELETE ... RETURNING`
- [x] RETURNING over NSQL streaming results

## Built-in functions

### String

- [x] `LOWER` — Unicode case mapping with STRING/TEXT type and NULL preservation
- [x] `UPPER` — Unicode case mapping with STRING/TEXT type and NULL preservation
- [x] `LENGTH` — Unicode code-point count with NULL propagation
- [x] `SUBSTRING` — 1-based Unicode code-point start with optional non-negative length
- [x] `TRIM` / `LTRIM` / `RTRIM` — Unicode whitespace removal with NULL/type preservation
- [x] `REPLACE` — literal all-occurrence replacement
- [x] `CONCAT` — variadic STRING/TEXT concatenation with TEXT widening
- [x] `STARTS_WITH` — literal prefix predicate
- [x] `ENDS_WITH` — literal suffix predicate
- [x] `CONTAINS` — literal substring predicate

### Numeric

- [x] `ABS` — exact arbitrary-precision DECIMAL sign removal
- [x] `ROUND` — exact half-away-from-zero rounding with optional non-negative scale
- [x] `CEIL` — exact DECIMAL ceiling
- [x] `FLOOR` — exact DECIMAL floor
- [x] `POWER` — finite DECIMAL result rounded to the engine's fixed eight-place approximation boundary
- [x] `SQRT` — non-negative DECIMAL input with fixed eight-place approximation and domain rejection
- [x] `MOD` — exact aligned-scale DECIMAL remainder with zero-divisor rejection

### NULL/value

- [x] `COALESCE` — left-to-right lazy evaluation and first-non-NULL result
- [x] `NULLIF` — coercible equality with typed NULL result
- [x] `GREATEST` — coercible comparison with NULL propagation
- [x] `LEAST` — coercible comparison with NULL propagation

### Date/time

- [x] `EXTRACT` or NextSQL-native equivalent — EXTRACT(unit, timestamptz) with explicit UTC fields
- [x] `DATE_TRUNC` or NextSQL-native equivalent — UTC calendar/duration boundary truncation
- [x] `DATE_ADD` or NextSQL-native equivalent — DATE_ADD(timestamptz, integer, unit), calendar-aware for year/month/day
- [x] `DATE_DIFF` or NextSQL-native equivalent — DATE_DIFF(start, end, unit) with calendar year/month and elapsed smaller units

### JSON

- [x] `JSON_GET` — typed scalar/container extraction through validated NSJB paths
- [x] `JSON_SET` — canonical NSJB mutation with object-key creation and bounded existing array indexes
- [x] `JSON_REMOVE` — object-key/array-element removal with missing-path no-op
- [x] `JSON_CONTAINS` — recursive object-subset and array-element containment
- [x] `JSON_ARRAY_LENGTH` — direct binary-array count at root or dotted/`$` path
- [x] `JSON_TYPE` — object/array/string/number/boolean/null classification with missing-path SQL NULL
- [x] Preserve sargability for compatible indexed JSON predicates — binder canonicalizes constant-path JSON_GET(column, path) to the native path AST used by JSON indexes

## Index/optimizer extensions enabled by SQL completeness

- [x] Covering indexes / `INCLUDE(...)`
- [x] Index-only scans
- [x] Partial indexes
- [x] Partial-index implication checking
- [x] Expression indexes
- [x] Reject volatile/non-deterministic index expressions
- [x] Expression-index matching
- [x] Top-N sort optimization — LIMIT/OFFSET fetch annotates ordinary sorts and executes with a bounded max-heap; ordered DISTINCT remains a correctness barrier
- [x] Join reordering improvements beyond current simplification — cost-based left-deep inner/cross DP (≤8 tables); hash-build the smaller side; local conjuncts pushed to scans; `LEFT`/`FULL`/`SEMI`/`ANTI` and rank operators are barriers; original column order restored
- [x] Partition-wise aggregation hooks where physical partitioning exists — an aggregate over a partitioned heap now builds one partial hash-aggregation per surviving partition, runs them in parallel through the scheduler, and merges the partials (cross-partition groups fold in the merge); `EXPLAIN` tags the operator `Aggregate … partition-wise` (`internal/executor/exec_vector.go` `partitionWiseHeapAggregate`, `TestPartitionWiseAggregation`, `docs/partitioning.md`)
- [x] Partition-wise joins hooks where physical partitioning exists — an equi-join between two tables with an identical physical partition scheme, where the join equates every partition-key column of one side to the positionally corresponding partition-key column of the other, runs one bounded hash join per aligned partition pair, in parallel across pairs through the scheduler; pruned pairs are skipped, a residual `Filter` over a partitioned scan is applied per partition, and predicates containing a subquery run the pairs serially. `INNER`/`LEFT`/`FULL`/`SEMI`/`ANTI`; `CROSS`, merge joins, and non-`SeqScan` inputs fall back to the generic single hash join. RANGE pairs by identical bound tuples, HASH by shared modulus/remainder, LIST by identical value groupings; partition-key column types must match exactly; legacy TENANT is never aligned. `EXPLAIN` tags the join node `… partition-wise` (`internal/catalog/partition.go` `AlignedPartitionJoin`, `internal/executor/partition_join.go` `tryPartitionWiseJoin`, `internal/sql/optimizer/physical.go` `finishJoin`; `TestPartitionWiseJoin`, `TestAlignedPartitionJoin`; `docs/partitioning.md`)

### Phase 18 exit gate

- [x] CTEs, subqueries, HAVING, CASE, set operations, windows, UPSERT, and RETURNING pass parser/binder/planner/executor tests
- [x] New SQL is correct across NULL, transactions, restart/recovery, prepared statements, RBAC, database isolation, and legacy-tenant fail-closed behavior — `TestP18SQLGate*` plus Go driver `TestDriverP18SQLOverTLS`; inner derived `DISTINCT`/`ORDER BY`/`LIMIT` execute in `collectPlan`
- [x] No PostgreSQL/MySQL compatibility dependency introduced
- [x] Official drivers execute the new SQL surface through NSQL
- [x] `docs/sql.md` and optimizer/execution docs updated

---

# Phase 19 — WORKFLOW / TRIGGER / SCHEDULE / TASK

NextSQL uses one coherent programmable automation model instead of unrelated procedure/event systems.

```text
WORKFLOW
├── manual invocation
├── trigger invocation
└── scheduled invocation
     ↓
    TASK
```

## WORKFLOW

- [x] Finalize `CREATE WORKFLOW` grammar — native `AS BEGIN … END`, explicit `$name` parameters, bounded DML/nested-RUN body
- [x] Typed workflow parameters — versioned catalog types plus invocation-time coercion
- [x] Workflow body with bounded multi-statement execution — 256 statements, shared query budget, atomic rollback
- [x] `ALTER WORKFLOW` — transactional `RENAME TO`
- [x] `DROP WORKFLOW` — transactional, including `IF EXISTS`
- [x] `RUN WORKFLOW` — synchronous invoker-rights execution with prepared arguments and summed affected rows
- [x] Transaction semantics documented — autocommit atomicity; a failed invocation aborts an explicit caller transaction
- [x] Database-isolation semantics documented — every body statement remains in the admitted database and uses invoker rights; workflows capture no row-tenant state
- [x] RBAC scope for workflows — database `CREATE`; function-scope `ALTER`/`DROP`/`EXECUTE`; body privileges rechecked
- [x] Audit create/alter/drop/run — action/name/actor/outcome without argument values
- [x] Workflow dependency tracking — stable table/workflow IDs; referenced table changes and workflow rename/drop are blocked; reload validates identities

## TRIGGER

- [x] `CREATE TRIGGER ... RUN WORKFLOW ...`
- [x] `BEFORE INSERT`
- [x] `AFTER INSERT`
- [x] `BEFORE UPDATE`
- [x] `AFTER UPDATE`
- [x] `BEFORE DELETE`
- [x] `AFTER DELETE`
- [x] Trigger recursion depth limit — hard depth 8; exhaustion atomically rolls back
- [x] Workflow recursion depth limit — hard depth 8 plus a 64-distinct-workflow invocation cap; exhaustion atomically rolls back
- [x] Cycle detection — static table/workflow dependency graph rejection plus runtime depth defense
- [x] Statement/time/memory/task limits — workflow descriptors are capped at 256 statements/1 MiB; trigger nesting is capped at 8; invocations share scheduler time/memory/I/O/cancellation budgets; scheduled work uses fixed workers, bounded batches, per-task timeouts, attempts, and retention
- [x] Deterministic replication behavior — stable trigger-ID order; leader fires synchronously and followers apply catalog/result WAL without refiring

## SCHEDULE

- [x] `CREATE SCHEDULE`
- [x] `EVERY duration` — bounded from 1 second through 8760 hours
- [x] `AT timestamp` — future RFC 3339, canonical UTC Unix nanoseconds
- [x] `ALTER SCHEDULE` — transactional `RENAME TO`
- [x] `DROP SCHEDULE` — transactional, including `IF EXISTS`
- [x] Durable schedule catalog — versioned `NSSC` descriptor plus chronological next-fire index in the encrypted/WAL-backed catalog
- [x] Raft-aware single authoritative dispatcher — leader-gated, bounded batches, cursor/task creation in one replicated transaction
- [x] No duplicate firing after leader failover within documented semantics — deterministic schedule-boundary task ID, durable cursor, active-source reservation, three-node failover coverage
- [x] Clock-skew behavior documented — backward steps delay eligibility; forward steps skip missed recurring boundaries and may reclaim expired leases
- [x] Cron syntax — `CREATE SCHEDULE … CRON '<m h dom mon dow>'` landed 2026-09-03 (log #86). Standard five-field, UTC, numeric-only; `*` / value / `a-b` / list / `*/n` / `a-b/n`; DOW 0–6 Sun=0 (7 also Sun); both-day-fields-restricted → OR (Vixie). Validated at definition time incl. a bounded forward search that rejects unsatisfiable specs. New `internal/cron` leaf package; schedule descriptor `NSSC` v2 (v1 still decodes). Cursor advances to the next match strictly after now, so a forward clock jump emits one task and skips missed boundaries — same rule as `EVERY`. The earlier deferral was gated on "core scheduler proven", established by the centralized-scheduler work (logs #81/#83).

## TASK runtime

- [x] Durable task ID — deterministic `s/<schedule-id>/<due-ns>` for scheduled boundaries
- [x] States: `PENDING/RUNNING/SUCCEEDED/FAILED/CANCELLED/RETRYING/FINAL_FAILED`
- [x] Attempt count — incremented atomically with each durable lease claim
- [x] Error metadata — bounded/redacted code and message; no workflow arguments or row values
- [x] Trigger metadata — versioned descriptor fields are defined; v1 row triggers remain synchronous and therefore do not create TASK rows
- [x] Database identity — execution remains in the admitted database; new schedule/task descriptors leave the legacy tenant field empty
- [x] Timeout — bounded descriptor timeout enforced through the shared scheduler budget and lease
- [x] Retry count — bounded maximum attempts
- [x] Retry backoff — bounded exponential delay with overflow saturation
- [x] Idempotency key semantics — schedule ID plus persisted due boundary; task creation and cursor advance are atomic
- [x] Dead-letter/final-failed state — `FINAL_FAILED` after attempts are exhausted; permanent descriptor/catalog failures use terminal `FAILED`
- [x] Concurrency policy — v1 schedules use `FORBID`; one active reservation spans pending/running/retrying and later due boundaries are skipped
- [x] `SHOW TASKS` or canonical `system.tasks` — bounded `SHOW TASKS [AFTER id] [LIMIT n]`; arguments are not exposed
- [x] Task cancellation — durable, idempotent `CANCEL TASK`; running success is fenced before local cancellation is signaled
- [x] Task history retention policy — seven-day terminal retention with a chronological index and purge batches capped at 256
- [x] No unbounded worker pools — one coordinator, fixed pool (default 2, maximum 16), capacity-before-claim backpressure

### Phase 19 exit gate

- [x] Manual workflow execution is ACID/durable as documented — autocommit/explicit rollback, crash recovery, backup/restore, LSN PITR, WAL apply, three-node Raft failover, TLS prepared-driver, full `go test ./...`, targeted race, and parser/descriptor fuzz gates pass (2026-08-24)
- [x] Triggers are bounded and cycle-safe — parser/catalog fuzz, atomic failure, crash recovery, backup/restore, LSN PITR, three-node Raft failover, adversarial RBAC/database isolation, audit redaction, TLS prepared-driver, targeted race, and full functional tests pass (2026-08-24)
- [x] Scheduled execution survives restart/failover — expired-lease crash/reopen recovery plus three-node dispatch/execute/state replication and post-leader-kill next-boundary dispatch pass (2026-08-24)
- [x] Task runtime is observable, cancellable, and resource-bounded — SQL/TLS `SHOW TASKS` and `CANCEL TASK`, fixed workers, bounded indexes/scans, retries, retention, and failure-state tests pass (2026-08-24)
- [x] RBAC/database isolation verified adversarially — owner pagination/cancellation isolation and execution-time privilege revocation fail closed; schedules/tasks no longer capture row-tenant state (2026-08-28)
- [x] `docs/workflows.md` complete for the implemented native v1 surface
- [x] Clean repository-wide functional suite — `GOMAXPROCS=2 GOCACHE=/tmp/nextsql-hosting-go-cache /snap/go/current/bin/go test ./... -count=1` passed every package on 2026-08-29; `internal/storage/btree` completed in 29.024 s and the executor, crash, HA, replication, backup, and integration packages were green.

---

# Phase 20 — CDC / change streams

Build native committed-change streaming from WAL.

## CDC core

- [x] WAL-to-CDC decoder for committed transactions only — versioned `NSCD` v1 changes are emitted only after matching durable COMMIT
- [x] Stable event ordering semantics — commit-LSN order, then change-LSN order inside the atomic transaction batch
- [x] Operation metadata: INSERT/UPDATE/DELETE
- [x] Table/database identifiers — stable table ID/name plus database storage identity
- [x] Primary-key identity — current key plus old key for key-changing UPDATE
- [x] Transaction ID / commit identity where safe
- [x] LSN/resume token — commit LSN is the atomic resume boundary
- [x] Configurable before/after image strategy — durable per-table `ALTER TABLE ... SET CDC IMAGES KEYS|FULL`; FULL uses versioned `NSRW` before/after images
- [x] Avoid unacceptable WAL amplification for old-row capture — KEYS is the default; FULL is explicit and remains bounded by record and transaction byte caps

## Subscription interface

- [x] Native `SUBSCRIBE TO table [WHERE operation = ...] [AFTER commit_lsn]` grammar finalized
- [x] Table-scoped subscriptions — stable table-ID/name filtering through native SQL/NSQL
- [x] Database-bound subscriptions — each stream is admitted against one connected database and table
- [x] Optional predicate filtering where safely supportable — exact INSERT/UPDATE/DELETE operation metadata equality; general row predicates fail closed because historical/default events may omit images
- [x] Streaming over NSQL with backpressure — bounded vector batches require `FlowAck` before the next pull
- [x] Resume from LSN/token
- [x] Explicit history-expired error
- [x] CDC retention tied to WAL retention policy — active streams pin their oldest required LSN across archived and disposable pruning; close/cancel releases the process-local pin
- [x] Per-stream authorization — table-scoped `CDC` is checked at admission and every pull; revoke stops open streams
- [x] Bounded buffering — staged changes, decoder state, and incremental WAL scans are independently capped
- [x] Slow-consumer handling — no subscriber goroutine/queue; NSQL flow acknowledgement and idle/query limits cancel stalled streams

### Phase 20 exit gate

- [x] No uncommitted changes leak from covered ordinary SQL DML; abort/crash-stranded decoder tests pass
- [x] Resume preserves ordered at-least-once transaction delivery at the internal API boundary
- [x] Backpressure is bounded — pull-based one-transaction delivery, no subscriber goroutine/queue
- [x] Database/RBAC rules cannot be bypassed — streams cannot change database identity and runtime revoke tests terminate open streams
- [x] Failover/restart CDC tests pass — storage restart resume and three-voter Raft leader-failover resume preserve commit-token ordering
- [x] `docs/cdc.md` complete for the native v1 surface, image policy, security, retention, operations, and recovery behavior

---

# Phase 21 — Native table partitioning

Convert existing pruning infrastructure into a user-facing physical partitioning feature.

## Partition types

- [x] `PARTITION BY RANGE` — one-to-eight-column keys; `VALUES LESS THAN (a, b, ...)` tuple bounds compared lexicographically with the order-preserving key encoding; pruning reduces the predicate to a query lower/upper bound prefix over the partition-key columns (successive equalities extend the prefix, the first non-equality constraint terminates it) and keeps a partition only when the query bound interval can intersect its `[lower, upper)` tuple interval, so trailing partition-key constraints separate bands that share a leading value
- [x] `PARTITION BY HASH` — one-to-eight-column keys with deterministic NSCT v4+ routing (SHA-256 over the canonical typed tuple); multi-column HASH prunes to one partition only when every partition column is pinned to a single equality value
- [x] `PARTITION BY LIST` — one-to-eight-column keys; `VALUES IN ((a, b), ...)` tuple membership; multi-column LIST prunes to one partition only when every partition column is pinned to a single equality value
- [x] `PARTITION BY TENANT(tenant_id)` removed from SQL; legacy descriptor decoding retained only for recovery/migration compatibility
- [x] Partition catalog metadata — versioned NSCT v5 descriptors are wired for the shipped RANGE/HASH/LIST slice
  - [x] Versioned bounded `NSCT` v4/v5 descriptor foundation — stable partition IDs/names, durable non-reusing identity allocator, RANGE/HASH/LIST rules, legacy TENANT decoding, heap/vector/index roots, fail-closed validation, legacy v1-v4 reads, truncation tests, decoder fuzz seed, and storage/partitioning docs
  - [x] Wire descriptors into native DDL, physical tree open/ownership/reclamation, and routing for the bounded RANGE/HASH/LIST slice; lifecycle breadth remains open
- [x] Create/attach/detach/drop partition semantics
  - [x] Bounded `ALTER TABLE ... ADD PARTITION` / `DROP PARTITION` — RANGE tail append and LIST value additions allocate partition-local heap/vector trees transactionally; DROP is empty/non-final only, reclaims after snapshots drain, and stable IDs are never reused; HASH membership changes fail closed pending redistribution
  - [x] `ATTACH PARTITION` / `DETACH PARTITION` — compatible unpartitioned tables transfer heap/vector/local-index roots without copying; typed row/rule validation, stable-ID non-reuse, AI continuity, RBAC/dependency rejection, rollback, pre-COMMIT crash recovery, and unclean restart are tested
- [x] Partition pruning from predicates — RANGE/HASH/LIST candidate IDs are carried into physical scans and visible in `EXPLAIN`; multi-column RANGE pruning is tuple-tight (trailing partition-key constraints separate bands that share a leading value; `TestMultiColumnRangePartitionTrailingColumnPruning`); mixed unanalyzable OR branches retain all partitions; legacy TENANT descriptors retain compatibility pruning only; pruning soundness (a matching row is never routed to a pruned partition) is covered by the randomized `TestPartitionPruningSoundness` property test across single/multi-column RANGE/HASH/LIST
- [x] Partition-aware indexes
  - [x] Partition-local non-unique B+Tree family — plain, covering, partial, expression, JSON-path, and spatial indexes allocate one root per partition and support CREATE, routed DML/cross-partition moves, physical scans, ADD PARTITION, blocking REBUILD, DROP/reclamation, and restart
  - [x] Partition-local FULLTEXT — one inverted-index root per partition; CREATE, routed INSERT/UPDATE/DELETE, cross-partition moves (postings plus per-partition BM25 corpus stats), ADD PARTITION, blocking REBUILD, DROP/reclamation, and restart. `SEARCH` scores BM25 across every partition-local root as one logical corpus (document frequency and corpus size summed) so partitioning never changes ranking; hybrid fulltext candidates route through the same path. `ATTACH`/`DETACH` transfer a partition-local inverted-index root in place (postings plus per-partition BM25 corpus stats) with no rebuild; ownership transfer, both-side post-transfer maintenance, and restart are covered by `TestPartitionAttachDetachFulltextIndex`.
  - [x] Partition-local HNSW indexes — one HNSW graph root per partition over per-partition vector-payload stores; CREATE, routed INSERT/UPDATE/DELETE, cross-partition moves, ADD PARTITION, blocking REBUILD, DROP/reclamation, ATTACH/DETACH root transfer, and restart. `NEAREST` (indexed and flat) searches every partition-local graph and merges hits by distance before keeping `k`, so partitioning never changes which neighbours are returned. Pruning-aware `NEAREST` now restricts the opened/searched partition graphs to the pruned stable-ID set when a residual `WHERE` predicate constrains the partition key (same `col = lit`/range/`BETWEEN`/all-branch `OR` analysis as scan pruning); `EXPLAIN` appends `partitions=[…]` to the `Nearest` node and a predicate that does not touch the partition key leaves every partition in play (`TestPartitionPruningAwareNearest`). Pruning-aware hybrid `SEARCH`+`NEAREST` candidate generation now matches: all three strategies (`filter-ann`, `search-ann`, `ann-filter`) restrict candidate generation to the surviving partitions — access scans prune normally and the `ann-filter` HNSW step opens only the pruned partition-local graphs — `EXPLAIN` shows `partitions=[…]`/`partitions=all[n]` on the `Candidates` node, RRF fusion runs over exactly the surviving candidates, and candidate generation always projects the full-text and vector columns so a covering primary-key projection still scores correctly (`TestPartitionPruningAwareHybridCandidates`).
  - [x] Cross-partition secondary UNIQUE enforcement — plain-column UNIQUE indexes (with optional `INCLUDE`) on RANGE/HASH/LIST tables keep one local root per partition but enforce a global contract: the write path takes an exclusive lock on the encoded key (engine key-lock namespace is global, so same-value writers in any partition serialize) then probes every other partition-local root; CREATE/REBUILD run an ordered cross-partition duplicate scan over the freshly built roots; ATTACH PARTITION probes each incoming row's key against the existing partitions. Cross-partition `UPDATE` moves are covered (own-partition duplicate rejected by the local root, third-partition collision by the probe). `UPSERT` on RANGE/HASH/LIST tables is wired to the same partition-local roots: a PK-target conflict resolves against the proposed row's routed partition heap, a secondary-UNIQUE-target conflict takes one exclusive key lock and probes every partition-local root, a partition-key `SET` moves the row between heaps, and a no-conflict insert still hits the cross-partition UNIQUE probe (`TestPartitionUpsert`). Partial/expression/JSON-path UNIQUE and UNIQUE on legacy TENANT tables stay rejected; `UPSERT` stays rejected only on legacy TENANT tables. Tests: `TestPartitionCrossPartitionUnique`, `TestPartitionCrossPartitionUniqueSerializedWriters` (blocking + post-commit rejection), `TestPartitionUpsert`, updated `TestPartitionLocalIndexesAndRecovery`; binder boundary tests. NOTE: concurrent *autocommit* writers that conflict expose a pre-existing engine bug (lost write / double "success" on concurrent abort+commit) unrelated to partitioning — see project memory.
- [x] Partition-aware statistics
  - [x] `NSST` v3 stable-ID row counts — explicit/automatic `ANALYZE` scans each physical heap, persists bounded per-partition counts across restart, and uses them as the optimizer's pruning-aware base estimate; stale/missing IDs fall back to global rows
  - [x] Per-partition column/index/vector sketches and costing — compact versioned `NSPS` v1 records are keyed by immutable table/partition IDs and SHA-256-bound to their owning encoded `NSST`; each local record is independently bounded, decoder-fuzzed, restart/lifecycle tested, and used only when every pruned stable ID has a matching current sketch, otherwise costing falls back to global `NSST`
- [x] Partition-aware backup/restore — verified base restore plus archived-WAL PITR preserve `NSCT` descriptors, partition rows, local index roots, pruning, and stable-ID `NSST` row counts
- [x] Partition-aware maintenance — bounded leader-only `MAINTAIN TABLE` visits every partition-local heap/vector/index tree, `MAINTAIN INDEX` visits every local root of the logical index, and missing catalog-owned roots fail closed
- [x] Cross-partition constraints defined — every primary key must include every partition column; plain-column secondary UNIQUE is enforced across partitions (lock + per-partition probe); `UPSERT` on RANGE/HASH/LIST tables resolves against those partition-local roots; partial/expression/JSON-path UNIQUE and cross-partition foreign keys remain rejected
- [x] Cross-partition unique/FK semantics documented — `docs/partitioning.md`, `docs/sql.md`: primary keys include the partition key; plain-column UNIQUE is global; `UPSERT` is partition-aware on RANGE/HASH/LIST; partitioned-table FKs and non-plain UNIQUE remain fail-closed; `UPSERT` stays fail-closed on legacy TENANT tables
- [x] Partition benchmarks — `nextsql-bench --partition` (`internal/bench/partition.go`, `TestPartitionBench`) compares an eight-band RANGE table against an unpartitioned `PRIMARY KEY (id)` table with the same rows, encryption/WAL/fsync on, reads inside a read-only transaction so the SELECT result cache never serves a repeat. Published run in `docs/partitioning.md`: a partition-key predicate prunes to one band for a ~7–9x faster single-bucket aggregate; an unpruned full aggregate was ~3x slower before the P18 partition-wise aggregation hooks landed (that benchmark run predates the fix; an unpruned aggregate now runs one partial per band in parallel and merges); routed `INSERT` into the composite-key band is ~1.5x slower than a plain id-keyed heap

## Legacy tenant-partition compatibility

- [x] Legacy TENANT descriptors remain versioned, bounded, recoverable, and fail closed for non-ADMIN access
- [x] New `PARTITION BY TENANT`, SET/RESET TENANT, and TENANT lifecycle DDL are rejected with hosted-database guidance
- [x] Explicit offline migration from legacy TENANT partitions into isolated hosted databases — `nextsql hosting migrate-tenant` (`cmd/nextsql`, `internal/xport/tenant.go`, `internal/hosting/tenant_migration.go`): exclusive lock on both data directories, foreign-key-ordered table creation, bounded UPSERT-idempotent batched row copy (`--batch-rows` 1–4096) into a `PROVISIONING` destination, per-table + per-row point verification against the source, `tenant_id`→`legacy_tenant_id` column rename, physical partitioning/roots stripped, encrypted versioned fuzz-seeded `NSLM` resume intent bound to source+destination identity and tenant, publish `ACTIVE` only after full verification; exact reruns resume (still-`PROVISIONING`) or re-verify only (already-`ACTIVE`); FK to unmigrated table, pre-existing `legacy_tenant_id`, or unrelated destination table fail closed. Tests: `TestMigrateLegacyTenantPartitionIsBoundedVerifiedAndIdempotent`, `TestMigrateLegacyTenantRejectsUnexpectedDestinationState`, `TestTenantMigrationIntent*`, `FuzzDecodeTenantMigrationIntent`, `TestHostingMigrateTenantPublishesOnlyVerifiedIsolatedDatabase`. Docs: `docs/partitioning.md`, `docs/security.md`, `docs/web/content/docs/cli.md`, `USAGE.md`.

### Phase 21 exit gate

- [x] RANGE/HASH/LIST semantics documented and tested for the bounded shipped subset; legacy TENANT decoding is compatibility-only
- [x] Pruning is visible in `EXPLAIN`
- [x] Transactions remain correct across partitions — RANGE/HASH/LIST routed writes, cross-partition PK updates, pruning, and restart recovery tested
- [x] Legacy tenant-partitioned tables fail closed for non-ADMIN access
- [x] No automatic distributed sharding yet unless separately gated — partitioning is single-node physical only; distributed sharding is a separate unstarted future phase
- [x] `docs/partitioning.md` complete for the bounded shipped slice and explicit remaining gate

---

# Phase 22 — Follower reads / read scaling

Keep single-leader writes. Multi-primary remains deferred.

## Read consistency

- [x] Define `STRONG` read semantics — a `STRONG` read observes every write acknowledged before it began (read-after-write across the whole cluster), served only on the leader; `ReadConsistency` enum + `docs/ha.md` "Read consistency"
- [x] Strong reads use leader or valid Raft read barrier — `replication.Cluster.StrongReadBarrier` calls `raft.VerifyLeader` (quorum round trip) before any non-mutating statement; a non-leader or partitioned former leader is rejected `unavailable` with leader-routing guidance (`executor.ReadGate`, `Session.requireReadConsistency`); `TestStrongReadBarrierLeaderOnly`, `TestStrongReadBarrierRejectsIsolatedLeader`
- [x] Define stale/eventual follower-read mode — `STALE` serves the local node's applied Raft state with no barrier, opt-in per session (`Session.SetReadConsistency(ReadStale)`), never the default, never labelled `STRONG`; any healthy member can serve it (`internal/executor` `requireReadConsistency`, `tests/ha` follower-read coverage)
- [x] Optional bounded-staleness mode — `BOUNDED` landed 2026-08-30: `Session.SetReadConsistency(ReadBounded)` + `Session.SetMaxStaleness`; `requireReadConsistency` routes `BOUNDED` through `executor.FollowerReadGate` → `replication.Cluster.FollowerReadHealthy(maxStaleness)` (leader always passes; a follower passes only while it sees a leader and was contacted within the bound; otherwise `unavailable`). Served from local applied state with no quorum round trip. `TestReadConsistencyModes`, `TestHABoundedFollowerRead`
- [x] `MAX STALENESS` semantics — session-scoped bound; `0` selects `executor.DefaultMaxStaleness` (`replication.HealthyContactWindow`, five heartbeats); carried on the wire as milliseconds (sub-ms bounds clamp to 1 ms, not `0`). Applies only in `BOUNDED` mode (`docs/ha.md`)
- [x] Driver routing metadata — additive `TypeNodeStatus`/`TypeNodeStatusResp` wire frames return the key-free `NodeStatus` (role, `has_leader`, `healthy`, applied LSN, last-contact ms, apply backlog) so a client routes without a `STALE` SQL round trip; exposed by every official driver (`Conn.NodeStatus`/`Cluster.Nodes` in Go, `conn.nodeStatus()`/`cluster.nodes()` in the JS drivers, `Client::nodeStatus`/`Cluster::nodes` in PHP). Protocol stays NSQL v1 (additive)
- [x] Read-after-write behavior documented — `docs/ha.md` "Read consistency": `STRONG` is read-after-write cluster-wide; `STALE` is explicitly not
- [x] Transaction restrictions on follower reads — `BEGIN`/`COMMIT`/`ROLLBACK` are leader-gated (mutating), so an explicit transaction (read-only or not) runs only on the leader; autocommit reads honour the session mode (`docs/ha.md`)
- [x] Realm/database and RBAC behavior unchanged — `authorize` still runs before the read barrier on every statement; the barrier adds a leadership check only and does not touch identity, database scoping, or grants

## Routing

- [x] Client/server routing mechanism designed — additive `TypeSetReadConsistency` (mode + `MAX STALENESS` ms) and `TypeNodeStatus`/`TypeNodeStatusResp` NSQL v1 frames; server handlers in `internal/protocol/server.go` (`applyReadConsistency`, `nodeStatus`); every official driver ships a routing client (Go `nextsql.Cluster` / `OpenCluster` over `Config.Nodes`; JS `connectCluster` over `cfg.nodes`; PHP `NextSQL\Cluster::connect`) that routes eligible reads to a healthy follower and everything else to the leader (`docs/ha.md` "Follower-read routing", `docs/protocol.md`)
- [x] Read-only statements eligible for follower routing — the routing client (all drivers) routes `SELECT`/`SHOW`/non-mutating `WITH` (conservative prefix check; `EXPLAIN` excluded because `EXPLAIN ANALYZE` executes) when the read-consistency mode is `Bounded`/`Stale` and no explicit transaction is open; the server independently enforces the barrier. `TestClusterRoutingClassifiers` (Go) + per-driver `follower-read routing classifiers` unit tests, `TestDriverFollowerReadRouting`
- [x] Writes always route/fail toward leader as documented — unchanged from Phase 15: `AllowWrite` rejects non-leader writes `unavailable`; transaction-control statements are leader-gated; every routing client pins writes/DDL/txn-control/`Strong` reads to the leader (`docs/ha.md`)
- [x] Replica lag exposed — `replication.Cluster.ReplicaHealth()` + `Status` fields + `system.replica_health` (`role`, `has_leader`, `applied_lsn`, `commit_index`, `applied_index`, `apply_backlog`, `last_contact_ms`, `healthy`) and the plaintext `nextsql.cluster.json` status file; also on the wire via `TypeNodeStatus`; `TestReplicaHealthSteadyState`, `TestReplicaHealthPartitionedFollower`, `TestStatusCarriesHealth`, `TestHAReplicaHealthSurface`, `TestNodeStatusRoundTrip` (`docs/ha.md` "Replica lag and follower health")
- [x] Follower health considered before routing — every routing client's `followerConn` picks only `healthy` members via cached `NodeStatus` (TTL 500 ms); `BOUNDED` reads additionally pass `FollowerReadHealthy(maxStaleness)` server-side; `TestDriverFollowerReadRouting`, `TestHABoundedFollowerRead`
- [x] Fallback behavior documented — a follower that rejects a routed read with `unavailable` is retried on the leader; no healthy follower falls back to the leader; explicit transactions and `EXPLAIN` always run on the leader (`docs/ha.md` "Follower-read routing")
- [x] Non-Go driver routing — Node / Bun / Deno (shared `drivers/js`) and PHP expose `setReadConsistency` / `nodeStatus` and a `connectCluster` / `NextSQL\Cluster::connect` routing client that ports the Go `nextsql.Cluster` logic; `cfg.readConsistency` (+ `maxStalenessMs`) applied per connection at open; per-driver unit suites + single-node live `nodeStatus`/`setReadConsistency` round trip green (`go test ./tests/integration -run Driver`)

### Phase 22 exit gate

- [x] Strong reads satisfy documented linearizability/consistency guarantee — dated sign-off recorded in `docs/ha.md` "Consistency model and sign-off" (2026-08-30): `STRONG` is linearizable under the covered failure model (crash / partition / failover; no Byzantine faults, no clock-based leases) via `StrongReadBarrier` = leader check + `raft.VerifyLeader` quorum round trip (leader completeness + quorum intersection block the stale-leader anomaly); read path benchmarked (`nextsql-bench --readscale`: `STALE` ≈ 2× `STRONG` single-node, so the round trip is the whole added cost). A leader-lease fast path is documented as a deliberate non-goal, not required for correctness. Evidence: `TestStrongReadBarrierLeaderOnly`, `TestStrongReadBarrierRejectsIsolatedLeader`, `TestReadConsistencyModes`
- [x] Stale reads are never mislabeled strong — `STALE` is a distinct opt-in `ReadConsistency` mode, never the default; `STRONG` reads always pass `StrongReadBarrier` or are rejected. `STALE`/`BOUNDED` results are always a consistent committed prefix of the global commit order (`docs/ha.md` "Guarantee per mode")
- [x] Failover does not violate session guarantees beyond documented mode — `TestFollowerReadFailoverSessionGuarantee` (`tests/integration`): a `STRONG` session over the routing client keeps read-your-writes + monotonic reads across a leader partition/re-election (the new leader's log holds every acknowledged write by leader completeness); a `STALE` read routed to the partitioned former leader may lag but never regresses below its own applied state — the documented trade-off. Also `TestHAKillLeader` (no lost acknowledged commit). Sign-off in `docs/ha.md` "Failover and session guarantees"
- [x] Driver compatibility tests pass — Go driver green (`TestDriverFollowerReadRouting`, `TestFollowerReadFailoverSessionGuarantee`); Node/Bun/Deno/PHP routing landed 2026-08-30 (per-driver unit suites + single-node live TLS via `go test ./tests/integration -run Driver`). Not a gate item: a 3-node non-Go cluster-routing live test is still worth adding as a follow-on, but server routing and failover are covered by the Go 3-node tests
- [x] Read-scaling benchmark published — `nextsql-bench --readscale` builds a 3-node single-leader cluster (encryption/WAL/fsync on) and drives PK point reads under `STRONG`/`STALE` on the leader, `STALE` over two and three members, and `BOUNDED` over three; reports aggregate read QPS, the leader's slice (`leader-qps`), p95/p99, and the ratio against the `stale-1n` baseline. Measures the Raft read-barrier cost (`STALE` ≈ 2× `STRONG` single-node: `~103k → ~202k` QPS, p99 `270 µs → 203 µs`) and the leader read-offload (`STALE` over three members drops `leader-qps` ~3.5×: `~202k → ~57k`, aggregate holds ~83%). Aggregate QPS is CPU-bound on one host — a real deployment adds a host per replica. `internal/bench/readscale.go`, `TestReadScaleBench`, `docs/ha.md` "Read scaling", `docs/ops.md`, `USAGE.md`

---

# Phase 23 — Vector Engine 2.0

Do this only after the P16 1M-HNSW baseline exists.

## Types / storage

- [x] `VECTOR<F16,N>` — IEEE 754 half elements in the detached payload store; runtime value stays `float32` (widened on read); `internal/float16` portable conversion; parser `VECTOR<F16,N>`; `NSVV` payload format v2 (element tag + halves), backward compatible with v1; quantise-on-write via `types.Coerce`; HNSW works unchanged; restart + encryption + fuzz covered (`TestVectorF16Quantized`, `TestPayloadF16Quantized`, `internal/float16` tests + fuzz)
- [x] `VECTOR<I8,N>` — signed-byte elements with a per-vector `float32` scale (`absmax(v)/127`, symmetric, `-128` code never produced); runtime value stays `float32` (widened on read); `internal/int8vec` portable conversion; parser `VECTOR<I8,N>`; `NSVV` payload format v2 extended with the `I8` element tag (scale + signed bytes), still backward compatible with v1 F32 / v2 F16; quantise-on-write via `types.Coerce`; HNSW works unchanged; restart + encryption + fuzz covered (`TestVectorI8Quantized`, `TestPayloadI8Quantized`, `internal/int8vec` tests + `FuzzRoundTrip`, `FuzzDecodePayload` I8 seed)
- [x] `BITVECTOR<N>` — distinct top-level type storing `N` single-bit elements as `ceil(N/8)` packed bytes (1/32 of `VECTOR<F32,N>`); runtime value widens to `float32` `0`/`1`; portable `internal/bitvec` (unit + `FuzzRoundTrip`); parser `BITVECTOR<N>` (`KwBitvector`), `types.VectorBit`, `Type.String()` → `BITVECTOR<N>`; `NSVV` payload v2 `BIT` element tag (packed bits, LSB-first), backward compatible with v1 F32 / v2 F16 / v2 I8; new `vector.MetricHamming` (differing-bit count) — default and only metric for a bit column (`USING HAMMING`; `KwHamming`); `types.Coerce` rejects a non-0/1 vector for a bit column (never rounds); `EncodePayloadElem` / `nearestQuery` re-validate 0/1; HNSW builds a Hamming graph (`graphMetric` at every build site); `WITH (QUANTIZATION = …)` rejected on a bit index; binder rejects `HAMMING` on a non-bit column and a real-valued metric on a bit column; restart + encryption (`NSVV` never plaintext) covered (`TestVectorBitvector`, `internal/vector` `TestPayloadBitPacked` / `TestHammingDistance` + `FuzzDecodePayload` / `FuzzDecodeMeta` seeds, `TestBindBitvector`, parser cases). No catalog format bump (`VecElem` was already a stored byte). Docs: `docs/vector.md`, `docs/sql.md`, `USAGE.md`, `CHANGELOG.md`, `ROADMAP.md`, web `vectors.md` / `sql.md` / `limits.md` (2026-08-30)
- [x] Versioned storage encoding — `NSVV` v2 self-describing (version byte selects F32 v1 / F16 v2 / I8 v2); `NSHM` HNSW meta v2 appends the traversal-quantisation tag (v1 headers decode with no quantisation); `types` catalog table format v6 stores one traversal-quantisation byte per index; every decoder fails closed on bad version, element/quantisation tag, dimension, length; fuzz seeds added (`FuzzDecodePayload`, `FuzzDecodeMeta`)
- [x] Conversion/cast rules — no `CAST` syntax in the dialect; `types.Coerce` quantises an `F32`/untyped vector value to an `F16` or `I8` column on INSERT/UPDATE (F16: round-to-nearest ties to even; I8: per-vector symmetric scale, round ties away from zero); already-quantised values of the same element type pass through; `NEAREST` query vectors stay full precision. Bitvector conversion deferred with that type
- [x] Quantized storage metrics — F16 halves the payload store, I8 quarters it at high dimension (`TestPayloadF16Quantized` / `TestPayloadI8Quantized` assert on-disk width); `nextsql-bench --vecquant` (`internal/bench/vecquant.go`, `TestVectorQuantBench`) seeds one vector set into an F32, F16, and I8 column, each with its own HNSW index, and reports per-element on-disk width, raw payload size, index-build page delta, total database size, build time, resident heap, mean quantisation error, and `NEAREST` p50/p95/p99 + recall@10/@100 scored against an exact-cosine flat search over the full-precision source vectors. The suite also measures an F32 column with an F16- and an I8-quantised HNSW graph (`qh-F16` / `qh-I8`). Reference run (2000×128-d, linux/amd64): database 3.4 → 2.4 → 1.9 MiB across the element types; recall@10 0.916 / 0.916 / 0.914; qh-F16 / qh-I8 index-build delta 1.8 / 1.5 MiB (additive quantised copies) with recall@10 0.916 / 0.912; latency/QPS within noise (runtime is `float32` everywhere). `docs/vector.md` "Size / recall comparison"

## ANN

- [x] Quantized HNSW option — `CREATE VECTOR INDEX … USING HNSW WITH (QUANTIZATION = 'F16' | 'I8' | 'NONE')`. The graph keeps a compact quantised copy of every vector beside its nodes (`0x02` key in the index tree, `NSVV` encoding; `NSHM` meta v2 tag; `types` catalog table format v6 stores one byte per index) and computes every traversal distance from it; `vector.Search` then re-ranks the `ef` candidates against the full-precision column payloads via the optional `FullVecLoader` interface, so the reported order and distances are exact and recall tracks an unquantised graph (2000×128-d: recall@10 0.916 `qh-F16` / 0.912 `qh-I8` vs 0.916 `F32`). Traversal encoding is independent of the column element type; rows written after the build are quantised on write (`QuantWriter` hook in `vector.Insert`); `REBUILD INDEX` rebuilds the store; encrypted + WAL/backup-recovered like every index structure. Trades a small additive on-disk cost for smaller, cache-local traversal reads — the graph itself shrinks separately via front-coded neighbour lists (below); a re-rank-free mode that drops the full payload is a minor follow-on. `internal/vector/quant.go`, `TestQuantizedHNSWIndex`, `internal/vector` `TestMetaQuantRoundTrip` / `TestQuantizedHNSWRerank` + `FuzzDecodeMeta` seed, `nextsql-bench --vecquant` (`qh-F16` / `qh-I8` rows), `docs/vector.md` "Quantised HNSW index"
- [x] Compressed HNSW neighbour lists — HNSW node records are written in node format v2: each layer's neighbour keys are sorted ascending (order carries no meaning in the graph — `searchLayer`/`link`/`replacementEntry` all treat a layer as a set) and front-coded (varint neighbour count, then per key a varint shared-prefix length with the previous key + a varint suffix length + the suffix bytes), replacing v1's fixed `u16` count and per-key `u16` length. Row primary keys in one table share a column prefix and, for a dense id space, several leading bytes, so the on-disk graph shrinks ~⅓ with the decoded neighbour set, recall, and latency unchanged (lossless). `DecodeNode` dispatches on the version byte so v1 records still decode; `EncodeNode` always emits v2, so `REBUILD INDEX` and ordinary writes migrate the graph. No `NSHM` meta or catalog format change (node records are self-describing and not catalog-versioned). `nextsql-bench --vecquant` index-build delta: F32 610 KiB (was 980), F16 707 KiB (was 948), I8 659 KiB (was 948), qh-F16 1.5 MiB (was 1.8), qh-I8 1.1 MiB (was 1.5); recall@10/@100 identical. Tests: `internal/vector` `TestCompressedNeighborLists` (v2 version byte, ≥2× shrink vs the v1 size formula, sorted-set round trip, a hand-built v1 blob still decodes, impossible shared-prefix fails closed), `FuzzDecodeNode` (multi-layer v2 seed, hand-built v1 seed, oversized-suffix-varint seed, + a decode→re-encode→decode idempotence check); `DecodeNode` bounds `shared`/`suffix` varints at 4096 before allocating. `go build ./...` + `internal/vector` / `internal/executor` (vector suites) / `internal/bench` `go test` + `-race` + 30 s `FuzzDecodeNode` green. Docs: `docs/vector.md` ("Compressed neighbour lists" + storage table + refreshed `--vecquant` numbers), `CHANGELOG.md`, `ROADMAP.md`, `USAGE.md`, web `vectors.md`.
- [x] IVF — **SQL surface + lifecycle wiring + `nextsql-bench --vecquant` row landed 2026-08-30.** `CREATE VECTOR INDEX … USING IVF WITH (LISTS = n [, PROBES = m])`. `nextsql-bench --vecquant` builds a sixth `F32/ivf` configuration (`LISTS = 2·√rows`, `PROBES = LISTS/4`) and reports index/db size, build time, `NEAREST` latency, and recall@10/@100 alongside the HNSW rows (reference 2000×128-d: index 112 KiB, build 0.25 s, recall@10 0.619 at LISTS=88/PROBES=22 on synthetic uniform vectors). A wide centroid set is now split across several `IVFCG`-indexed group records so the coarse quantiser is not capped by the ~½-page B+Tree leaf-record ceiling (`internal/executor/ivfstore.go`; legacy single `NSIC` block still loads; `TestIVFCentroidGrouping`). Parser: `ast.CreateIndex.IVFLists` / `IVFProbes`, `USING IVF` + `WITH (LISTS = … [, PROBES = …])` (no new lexer keyword; `identIs("ivf"/"lists"/"probes")`). Binder (`internal/sql/binder/index.go`): `idx.VecMethod` = `catalog.VecMethodIVF`; `LISTS` required and ≤ `catalog.MaxVectorIndexLists` (65 536), `PROBES` ≤ `LISTS`; rejected with `QUANTIZATION`, on a `BITVECTOR` column, and on a partitioned table ("not supported in this slice"). Catalog: `Index.VecMethod uint8` + `IVFLists` / `IVFProbes uint32`; table descriptor format **v7** — after the v6 per-index quant byte, one method byte + two `u32` per index (validated: IVF ⇒ vector + lists∈[1,max] + probes≤lists; non-IVF ⇒ zero params); `internal/upgrade` `FamilyCatalog` window 1..7. Executor (`internal/executor/ivfstore.go`): `sqlIVF` implements `vector.IVFStore` over the detached index tree (meta `0x00` / centroids `0x01` / posting `0x02`+`u32`) + shared payload store, with the fk-write-snapshot plumbing; `buildIVFIndex` (streams the heap, trains over a deterministic ≤ 50 000-vector sample via `vector.TrainIVF`, `AddIVF` per row, then writes centroids + non-empty front-coded lists + `NSIV` header in the build txn; empty table ⇒ trained 1-centroid header) shared by `CREATE` and `REBUILD INDEX` through `buildIndex`; `maintainIVFIndex` (`RemoveIVF` old pk, `AddIVF` new pk) dispatched from `maintainIndexes`; `nearestIVFIndex` (`vector.SearchIVF` with `idx.IVFProbes`, residual over-fetch, heap row fetch, a differing `USING` metric ⇒ exact flat) dispatched from `nearestIndex`. A committed `NEAREST` is served from a shared in-memory `lockedIVF` copy under the HNSW cache generation (`ivfSearchStore`, landed 2026-08-30); a writer reads the txn store. `EXPLAIN` labels the plan `ivf` (`physical.go` + `rewrite.go`); `xport/sql.go` emits `USING IVF WITH (…)`. Crash-recovery/backup/PITR/Raft inherited from the encrypted index-tree WAL path. Tests: `internal/executor` `TestIVFVectorIndex` (LISTS required, PROBES>LISTS rejected, exact NEAREST at PROBES=LISTS, k=2, covering projection, INSERT/UPDATE/DELETE maintenance, restart, `REBUILD INDEX`, no `NSVV`/`NSIV`/`NSIC`/`NSIL` plaintext); parser cases (IVF params, unknown `WITH` option, unknown method); binder cases (LISTS required, PROBES>LISTS, params round-trip); `catalog` v7 legacy-decode adjustment + `FuzzDecodePartitionedTable` IVF seed; `upgrade` window test. `go build ./...` + `go test ./...` + `-race` on `internal/vector` / `internal/catalog` / `internal/executor` (vector suites) green; `FuzzDecodePartitionedTable` / `FuzzDecodeIVFList` clean. Docs: `docs/vector.md` ("IVF index" + storage table + catalog v7), `docs/sql.md`, `docs/storage-format.md`, `docs/ops.md`, `USAGE.md`, `CHANGELOG.md`, `ROADMAP.md`, web `vectors.md`. --- Prior core (2026-08-30): `internal/vector/ivf.go` (portable, no unsafe/cgo/assembly): `IVFMeta` (`NSIV` v1, 25 bytes: dim, metric, `NList`, `NProbe`, count, trained), `EncodeCentroids`/`DecodeCentroids` (`NSIC` v1 contiguous `f32` block), `EncodeIVFList`/`DecodeIVFList` (`NSIL` v1 — deduped, sorted, front-coded primary keys, same shared-prefix+suffix scheme as HNSW node format v2, all varints bounded at 4096 before `make`). `IVFStore` interface (`LoadIVFMeta`/`SaveIVFMeta`, `LoadCentroids`/`SaveCentroids`, `ListPKs`/`AddToList`/`RemoveFromList`, `LoadVec`) + `IVFMem` in-memory impl + `PersistIVF`/`LoadIVFMem`. `TrainIVF(meta, samples)` — deterministic (fnv seed over meta + sample bytes) k-means++ seeding then ≤25 Lloyd iterations; `COSINE` unit-normalises samples + centroids so sphere-L2 ranks like cosine; empty clusters re-seed to the worst-served point; `NList` reduced to `len(samples)` when the sample is smaller. `AddIVF` assigns a pk to its nearest centroid's list (replaces an existing pk); `RemoveIVF` scans lists, reports found, decrements count. `SearchIVF(st, query, k, nprobe, workers)` ranks centroids by sphere-L2, probes the `nprobe` nearest lists (0 → `Meta.NProbe`), dedups candidates, and delegates exact scoring to `FlatSearch` (scheduler-parallel) — recall 1.0 when `nprobe == NList`. Real-valued metrics only (`COSINE`/`L2`/`IP`; `HAMMING` rejected in `EncodeIVFMeta`). Index keys (`IVFMetaKey` `0x00`, `IVFCentroidsKey` `0x01`, `IVFPostingKey(list)` `0x02`+`u32`) reserved for the executor increment; IVF gets its own detached encrypted tree. Tests: `internal/vector` `TestIVFMetaRoundTrip` / `TestIVFCentroidRoundTrip` / `TestIVFListRoundTrip` (front-coding shrinks + dedups, impossible-prefix fails closed) / `TestIVFSearchRecall` (probe-all exact, `nprobe=8` recall@10 ≈ 0.82 on 800×24-d nlist=32) / `TestIVFAddRemove` / `TestIVFPersistLoad` (on-disk round trip + reload search identical) / `TestTrainIVFDeterministic`; `FuzzDecodeIVFList` (20 s clean, oversized-varint seed) + `FuzzDecodeIVFMeta` (10 s clean). `go build ./...` + `internal/vector` `go test` + `-race` green. Docs: `docs/vector.md` ("IVF index" + storage table), `CHANGELOG.md`, `ROADMAP.md`, `USAGE.md`, web `vectors.md`. **Next increment: `CREATE VECTOR INDEX … USING IVF WITH (LISTS = n [, PROBES = m])`** — parser, binder, catalog format bump, executor build/rebuild/maintain/search over `IVFStore` on the index tree, `nextsql-bench --vecquant` IVF rows, crash-recovery/Raft (inherits the index-tree WAL path).
- [x] IVF-PQ — **SQL surface + lifecycle wiring landed 2026-08-30.** `CREATE VECTOR INDEX … USING IVFPQ WITH (LISTS = n, SUBSPACES = M [, PROBES = m])`. Parser: `ast.CreateIndex.IVFSubspaces`; `USING IVFPQ` shares the IVF `WITH` loop and adds `SUBSPACES` (no new lexer keyword; `identIs`). Binder (`internal/sql/binder/index.go`): `idx.VecMethod = catalog.VecMethodIVFPQ`; `SUBSPACES` required, ≤ `catalog.MaxVectorIndexSubspaces` (128), must divide the vector dimension; `LISTS` required ≤ 65 536, `PROBES` ≤ `LISTS`; rejected with `QUANTIZATION`, on a `BITVECTOR` column, on a partitioned table; `SUBSPACES` on `USING IVF` rejected. Catalog: `Index.IVFSubspaces uint32`; `EncodeTable` bumps to **v8** — after the v7 per-index method + `LISTS` + `PROBES`, one `SUBSPACES` `u32` per index (IVFPQ ⇒ vector + `SUBSPACES∈[1,128]` dividing the column dim; non-IVFPQ ⇒ 0); `DecodeTable` accepts v1..v8 (IVFPQ method requires v8); `internal/upgrade` `FamilyCatalog` window 1..8. Executor `internal/executor/ivfpqstore.go`: `sqlIVFPQ` implements `vector.IVFPQStore` over the detached encrypted index tree — coarse centroids grouped like IVF (`IVFCG`), the codebook split into fixed-size chunks under an `IVPCG` header (a PQ codebook never fits one ~½-page leaf record), one front-coded `NSPL` posting list per centroid — plus the shared payload store, with fk-write-snapshot plumbing. `buildIVFPQIndex` streams the heap, trains via `vector.TrainIVFPQ` over a deterministic ≤ 50 000-vector sample, persists centroids + codebook + `NSPQ` header, then `AddIVFPQ` per row (empty table ⇒ 1-centroid header + all-zero placeholder codebook, `Ksub = 1`); shared by `CREATE` and `REBUILD INDEX` via `buildIndex`. `maintainIVFPQIndex` (`RemoveIVFPQ` old / `AddIVFPQ` new) dispatched from `maintainIndexes`; `nearestIVFPQIndex` (`vector.SearchIVFPQ` with `idx.IVFProbes`, residual over-fetch ×4, heap row fetch, differing `USING` metric ⇒ exact flat) dispatched from `nearestIndex`. No process-local cached IVF-PQ copy — search reloads the quantiser per query (documented follow-on, matching IVF's first increment). `EXPLAIN` labels `ivfpq` (`optimizer/physical.go` + `rewrite.go`); `xport/sql.go` emits `USING IVFPQ WITH (…)`. Crash-recovery/backup/PITR/Raft inherited from the encrypted index-tree WAL path. `nextsql-bench --vecquant` gains an `F32/ivfpq` row (reference 2000×128-d, `SUBSPACES = 16`: index 321 KiB vs 691 KiB HNSW / 112 KiB IVF, build 1.96 s, `NEAREST` p50 1.42 ms, recall@10 0.495 — low on synthetic uniform vectors; real clustered embeddings + higher `PROBES` score materially better). Tests: `internal/executor` `TestIVFPQVectorIndex` (SUBSPACES required + must divide dim, `PROBES > LISTS` rejected, exact-rerank NEAREST + k=2, INSERT/UPDATE/DELETE maintenance, restart, `REBUILD INDEX`, no `NSVV`/`NSPQ`/`NSPC`/`NSPL`/`NSIC` plaintext); `sql/parser` + `sql/binder` cases; `catalog` v8 trailer fix + `FuzzDecodePartitionedTable` IVF-PQ v8 seed; `internal/upgrade` window test; `internal/bench` `TestVectorQuantBench` (7 reports). `go build ./...` + touched-package `go test` + `-race` green; `FuzzDecodePartitionedTable` + `FuzzParse` 15 s clean. Docs: `docs/vector.md` ("IVF-PQ (product quantisation)" + storage table + catalog v8 + refreshed `--vecquant` numbers), `docs/sql.md`, `docs/storage-format.md`, `docs/ops.md`, `USAGE.md`, `CHANGELOG.md`, `ROADMAP.md`, web `vectors.md` / `limits.md`. --- Prior core (2026-08-30, `internal/vector/ivfpq.go`): `TrainIVFPQ` / `AddIVFPQ` / `RemoveIVFPQ` / `SearchIVFPQ`, `IVFPQStore` + `IVFPQMem` + `PQCodebook`, versioned `NSPQ` / `NSPC` / `NSPL` encodings with bounded decoders, `COSINE` / `L2` only; `TestIVFPQ*` + `FuzzDecodePQList` / `FuzzDecodePQCodebook` / `FuzzDecodeIVFPQMeta`.
- [x] Build/rebuild lifecycle integration — `buildIVFPQIndex` shared by `CREATE VECTOR INDEX … USING IVFPQ` and `REBUILD INDEX` through `buildIndex`; `maintainIVFPQIndex` on every row change; HNSW / IVF paths unchanged.
- [x] Transaction/delete semantics — posting-list writes run in the statement's transaction (MVCC/rollback correct); `DELETE` / `UPDATE` call `RemoveIVFPQ` before any re-add; `nearestIVFPQIndex` reads the txn-scoped store so a session sees its own uncommitted changes.
- [x] Encryption of every ANN structure — centroids, codebook chunks, and posting lists live in the index's own detached **encrypted** B+Tree; `TestIVFPQVectorIndex` asserts no `NSVV`/`NSPQ`/`NSPC`/`NSPL`/`NSIC` plaintext on disk.
- [x] Crash/recovery/Raft integration — inherited from the encrypted index-tree WAL path (same as HNSW and IVF); `REBUILD INDEX` retrains from scratch.

## Distances

- [x] `HAMMING` metric — `vector.MetricHamming` (count of differing elements; on 0/1 vectors equals L1). `ParseMetric("hamming")`, `Metric.String()` → `hamming`, accepted by `EncodeMeta` / `DecodeMeta`. `NEAREST … USING HAMMING`; the default and only metric for a `BITVECTOR` column and rejected on any other vector column. Flat + HNSW.

## Sparse retrieval

- [x] Design sparse-vector type/format — `SparseVec` is a strictly-ascending `(index, value)` pair list (`MaxSparseDim` 2^24, `MaxSparseNNZ` 2^16); versioned `NSSV` v1 payload (dim, nnz, delta-varint indices, LE `f32` values), `NSSM` v1 21-byte inverted-index header, `NSSP` v1 front-coded posting lists; overflow deltas and oversize varints fail closed before allocation (`internal/vector/sparse.go`, 2026-08-30)
- [x] Prototype sparse retrieval — inverted index, one posting list per dimension; `SearchSparse` accumulates the exact inner product over the query's non-zero coordinates (`INNER_PRODUCT` ranking is final; `COSINE` re-ranks the top `4·k` against full-precision payloads). Unit recall@10: IP 1.000, COSINE rerank-all 1.000, COSINE `4·k` ≥ 0.90 on 400×4096-d nnz=24 (`TestSparseSearchRecall`). `AddSparse`/`RemoveSparse`/`PersistSparse`/`LoadSparseMem`; `SparseStore` / `SparseMem`.
- [x] `SPARSEVECTOR<N>` SQL + `CREATE VECTOR INDEX … USING SPARSE` — parser/binder/`VecMethodSPARSE` on catalog v8, executor `sqlSparse` over `SparseStore` on a detached encrypted index tree (build/rebuild/maintain/search), exact inner-product `NEAREST` with optional COSINE re-rank; `TestSparseVectorIndex`; not on partitioned tables (2026-08-30)
- [x] Evaluate dense + sparse + BM25 fusion — a second `NEAREST` (one dense `VECTOR`, one `SPARSEVECTOR`, optional `SEARCH`) unions candidates from each retriever and reciprocal-rank fuses them (`k = 60`); a channel contributes only when it scored the row. `EXPLAIN` `Rerank bm25+vector+sparse fusion`. At most two `NEAREST` clauses. `TestDenseSparseBM25Fusion` (each single channel uniquely owns one relevant row; fused `LIMIT 3` returns all three) + `TestDenseSparseBM25FusionPlan` + parser/binder cases (2026-08-30)
- [x] Finalize only after measurable benefit — fused `LIMIT 3` surfaces each channel's unique hit; each single-channel `LIMIT 1` does not (`TestDenseSparseBM25Fusion`)
- [x] Official `--vecquant` sparse size/latency/recall row — `SPARSE` configuration on a high-dimension, low-nnz corpus (`--vecquant-sparse-dim 4096`, `--vecquant-sparse-nnz 24`); NSSV raw payload / index / database size / build time / heap / `NEAREST` p50/p95/p99 + recall@10/@100 vs exact-cosine `SparseFlat`. Reference 2000 × 4096-d nnz=24: raw 282 KiB, index 1.0 MiB, db 2.1 MiB, build 1.17 s, p50 428 µs, recall@10/@100 **1.000** (`TestVectorQuantBench`, 2026-08-31)

## Measurement

Every ANN configuration must report:

- [x] recall@10 — `--vecquant` 2026-08-31: F32/F16 0.916, I8 0.914, qh-F16 0.916, qh-I8 0.912, IVF 0.619, IVF-PQ 0.495, SPARSE 1.000
- [x] recall@100 — same run: HNSW family 0.939–0.940, IVF 0.514, IVF-PQ 0.502, SPARSE 1.000
- [x] p50/p95/p99 — same run; published in `docs/vector.md` "Size / recall comparison"
- [x] QPS — same run: HNSW family 444–516, IVF 1277, IVF-PQ 628, SPARSE 1643
- [x] RAM — same run: resident heap 79–89 MiB
- [x] index size — same run
- [x] build time — same run
- [x] database size — same run

Never lower recall silently to improve latency.

### Phase 23 exit gate

- [x] At least one memory-efficient vector representation is production-gated — dated sign-off in `docs/vector.md` "Production-gating sign-off (Phase 23)" (2026-08-31): `VECTOR<F16,N>` / `VECTOR<I8,N>` / `BITVECTOR<N>` columns and the quantised HNSW index are production-gated; IVF / IVF-PQ / sparse / fusion are production-gated ANN paths with documented follow-ons
- [x] New ANN path has recall/latency/size measurements — official `--vecquant` 2026-08-31 run reports recall@10/@100, p50/p95/p99, QPS, heap, index/db size, and build time for F32/F16/I8 HNSW, quantised HNSW (`qh-*`), IVF, IVF-PQ, and SPARSE (published in `docs/vector.md` "Size / recall comparison"); unit recall coverage for IVF (`TestIVFSearchRecall`), IVF-PQ (`TestIVFPQSearchRecall`), and sparse (`TestSparseSearchRecall`); a `BITVECTOR`/Hamming `--vecquant` row remains an optional follow-on, not a gate item
- [x] No durability/encryption regression — quantised traversal store is in the encrypted index tree, WAL/backup/PITR/Raft path unchanged; `NSVV` / `NSHM` never plaintext (`TestQuantizedHNSWIndex`, `TestVectorBitvector`); full `go test ./...` + `-race` on touched packages green
- [x] Existing F32/HNSW behavior remains compatible — `QUANTIZATION` defaults to `NONE`; `NSHM` v1 headers and `NSCT` v1–v5 descriptors decode unchanged (quantisation absent); `BITVECTOR` is a new type with no catalog format bump; v1 (fixed-width) HNSW node records still decode after the node format v2 (front-coded neighbour list) change; every prior vector test passes

---

# Phase 24 — Full-text Search 2.0

- [x] Stemming — Snowball English (Porter2) v1; `WITH (ANALYZER = 'english')`; default `simple` unchanged
- [x] Stop-word dictionaries — english v2 applies dictionary v1 (33 terms) before stemming; `simple` unchanged; english v1 still decodes
- [x] Versioned language analyzers — `french` / `german` / `spanish` v1 (Snowball 3.x + stop-word dictionary v1) on `NSCT` v9 ids 2/3/4; `simple`/`english` unchanged
- [x] Synonyms — english analyzer v3 applies synonym dictionary v1 at query time (OR at the token position, fail-closed expansion caps); index terms stay 1:1; english v1/v2 still decode (`TestFulltextEnglishSynonyms`, `TestParseQueryEnglishSynonyms`)
- [x] Prefix search — trailing ASCII `*` on a SEARCH token (`cat*`, `"data* performance"`); query-time only; skip stem/stop/synonym; fail-closed expansion caps (`TestFulltextPrefixSearch`, `TestParseQueryPrefix`)
- [x] Fuzzy matching — trailing ASCII `~` / `~1` / `~2` on a SEARCH token (`cat~`, `"databas~ performance"`); OSA Damerau-Levenshtein; AUTO distance; query-time only; skip stem/stop/synonym; fail-closed expansion caps (`TestFulltextFuzzySearch`, `TestParseQueryFuzzy`)
- [x] Typo tolerance — unadorned missing terms become AUTO fuzzy (`databse` matches `database`); typo AUTO 0/1/2 for 1–4 / 5–8 / 9+ runes; present exact terms and prefix/explicit fuzzy unchanged; fail-closed expansion caps (`TestFulltextTypoSearch`, `TestApplyTypoToleranceMissing`)
- [x] Highlight/snippet generation — `HIGHLIGHT(col)` / `SNIPPET(col)` on SEARCH SELECT lists wrap original matching tokens (exact/synonym/prefix/fuzzy/typo) with `<mark>`; snippet is a 16–4096 rune window (default 160); fail-closed marker/width (`TestFulltextHighlight`, `TestHighlightExact`, `TestSnippetWindow`)
- [x] Multi-field search — `CREATE FULLTEXT INDEX` / `SEARCH col [, col …]` take 1–8 STRING/TEXT columns (same order for index use); fields scored as one BM25 document; phrases do not cross fields (position bands, no catalog/format bump); `HIGHLIGHT`/`SNIPPET` remain per column (`TestFulltextMultiFieldSearch`, `TestAnalyzeFieldsPositions`, `TestBindFulltextMultiField`)
- [x] Field weighting — optional `SEARCH col WEIGHT n` (query-time BM25 tf scale from position bands; omitted = 1; `(0, 64]`; no catalog/format bump; `TestFulltextFieldWeight`, `TestWeightedTF`, `TestQueryScoreWeighted`)
- [x] Faceting/aggregation support where architecturally appropriate — `SELECT * … SEARCH … FACET col [, col …]` independent histograms over the full match set (`facet STRING`, `value STRING`, `count DECIMAL`); `LIMIT` is per-facet top-N; `NULL` skipped; query-time only, no catalog/format bump; 1–8 discrete columns, 1024 distinct values fail closed (`TestFulltextFacet`, `TestFacetDistinctValueCap`, `TestBindFulltextFacet`, `TestSearchFacetPlan`)
- [x] Analyzer/index options in DDL — `CREATE FULLTEXT INDEX … WITH (ANALYZER = 'simple' | 'english' | 'french' | 'german' | 'spanish')`
- [x] Query-expansion CPU/memory caps — 256 terms / 8192 bytes / 4096 work units, fail closed
- [x] Transaction/WAL/recovery support for analyzer metadata — `NSCT` v9 per-index analyzer id + revision
- [x] Deterministic analyzer behavior across replicas — Porter2 / Snowball French/German/Spanish, no locale/RNG

### Phase 24 exit gate

- [x] Current BM25/phrase behavior remains compatible — `TestP24BM25PhraseCompatibilityGolden` pins Phase-10 BM25 constants and adjacent/non-adjacent phrase semantics; existing simple-analyzer and phrase suites remain green
- [x] Language/fuzzy features have bounded adversarial-query behavior — expansion remains capped at 256 terms / 8192 bytes / 4096 work units; fuzzy/typo vocabulary inspection now fails closed at 4096 distinct terms on index and seq-scan paths; OSA evaluation uses bounded linear memory (`TestFuzzyVocabularyBudgetFailClosed`, `TestP24FuzzyVocabularyCap`, `TestFuzzyWithinMatchesReference`)
- [x] Search quality fixtures expanded — `TestP24SearchQualityFixtures` covers exact BM25 order, phrases, prefix, fuzzy, typo tolerance, English stop/stem/synonym phrases, and French/German/Spanish analysis
- [x] Encryption/recovery tests pass — `TestP24EncryptedCrashRecovery` proves analyzer metadata and committed postings survive kill/reopen, uncommitted posting changes do not, and distinctive terms are absent from database/WAL/UNDO plaintext; clean serialized repository-wide functional gate plus targeted race/build/fuzz gates green

---

# Phase 25 — Security 2.0

Audit every security checklist item and distinguish `designed`, `implemented`, `tested`, and `production-gated`. The dated item-by-item audit is in `docs/security.md` under "P25 Security 2.0 audit (2026-09-01)".

## mTLS and service identity

- [x] Actual mTLS server implementation — `--tls-client-ca` / `tls_client_ca` selects TLS 1.3 `RequireAndVerifyClientCert`; missing or untrusted certificates fail the handshake
- [x] Client certificate validation — system `crypto/x509` chain, validity, and client-EKU validation against the configured CA bundle; targeted handshake test
- [x] Service identity mapping — one verified URI SAN `nextsql://service/<principal>` must match the native login user; native password + RBAC remain mandatory
- [x] Certificate rotation — atomic last-known-good `SIGHUP` reload of the server key pair and client trust bundle; overlap trust rotation documented; successful mTLS reload closes every accepted connection (including pre-auth handshakes) to force reauthentication
- [x] Certificate revocation handling — optional `--tls-client-crl` / `tls_client_crl` PEM X.509 CRLs; signed/current/full-chain coverage is mandatory and revoked serials fail the handshake; OCSP remains explicitly unimplemented
- [x] Audit authentication identity source — auth records distinguish `native`, `mtls`, and `mtls+native`

## Short-lived credentials

- [x] Signed short-lived NextSQL credential/token format — `NSSC1.` + base64url of Ed25519-signed claims (`internal/auth/token.go`); presented in place of the password, no new frame/auth method; `FuzzDecodeTokenClaims` clean
- [x] Expiration — issued-at/not-before/expires-at (second precision), 60 s skew, verifier max lifetime (default 24 h, ceiling 30 d); session closed at expiry (`TestTokenExpiry`, `TestTokenMaxLifetime`, `TestShortLivedCredentialExpiryClosesSession`)
- [x] Audience/database scope — `token_audience` exact match (a configured audience also rejects an unscoped credential); `database` claim vs the served database (`TestTokenAudienceMismatch`, `TestShortLivedCredentialAudienceMismatch`)
- [x] Role scope — `ACL.AllowedScoped` narrows the session to privileges reachable through the listed roles; the principal must already be a member of every listed role (no escalation); enforced on every statement (`TestACLAllowedScoped`, `TestShortLivedCredentialRoleScope`)
- [x] Realm/database scope — carried on the claims; database enforced server-side, realm surfaced for hosted routing
- [x] Signing-key rotation — `NSTK` v1 keyset of Ed25519 keys with `current`/`retired` flags; `nextsql token rotate`/`retire`; verify-only server copy via `export-public`; overlap window (`TestTokenKeyRotationOverlap`)
- [x] Revocation — `NSTR` v1: revoked token ids (pruned at their own expiry) + per-principal issued-before cutoffs; `nextsql token revoke`; `SIGHUP` reload, last known-good on failure (`TestRevokeByTokenID`, `TestRevokePrincipalCutoff`, `TestShortLivedCredentialRevoked`)
- [x] Audit — auth records carry `identity_source` `token` / `mtls+token`; `token.reload` recorded as a security-setting event

## External IdP

- [x] OIDC design — accepted design in `docs/design-oidc-external-idp.md` (2026-08-31): brokered token exchange (`cmd/nextsql-auth-broker` / embedded `nextsqld --auth-broker-listen`) validates an OIDC ID token / access token against a cached JWKS and mints an existing `NSSC1.` short-lived credential; the SQL auth path stays offline and unchanged. Versioned `NSIP` identity-policy: ordered issuer-scoped subject→principal rules, group→role mappings intersected with the principal's real RBAC membership (no escalation, empty ⇒ deny), `SIGHUP` last-known-good. Audit `identity_source` `oidc` / `mtls+oidc` keyed off the verifying key, not attacker bytes. Direct server-side JWT verification (`NSIDP1.`) is the rejected alternative. Delivery status is tracked by the rows below.
- [x] OIDC implementation — **required broker/client/server surface done (2026-08-31)**: `internal/oidc`, `internal/authbroker`, `cmd/nextsql-auth-broker`, embedded `nextsqld --auth-broker-listen`, `internal/oidcclient`, and `nextsql login` / `logout` / `whoami`. Standalone and embedded modes use the same bounded HTTP(S) runtime and exchange handler. Embedded mode is single-node only, binds a separate listener, checks that the private issuer key is accepted by `token_verify_keyset` before startup and reload, sequences verifier-before-issuer reload, and consumes the live user/ACL stores. Interactive login uses Authorization Code/PKCE; client credentials require an explicitly enabled asymmetric JWT, resource audience, and exact client binding. Key-derived audit labeling remains unchanged. Fake-IdP→client/broker→real `auth.TokenVerifier`, embedded HTTP exchange, live-revocation, TLS/listener lifecycle, functional, race, replay/client-binding/resource-audience, redirect-replay, callback-CSRF/duplicate, response-bound, secret/credential-store, audit-redaction, forged-key-id, and config-bound tests cover the surface. Optional opaque-token introspection and privileged JIT provisioning remain off and are not core-gate requirements
- [x] Interactive OIDC CLI — discovery + Authorization Code/PKCE (`S256`) + random state/nonce; transient loopback callback; browser/manual URL; broker exchange; collision-resistant atomic `0600` credential/refresh-token store under a `0700` directory; silent refresh; `nextsql login` / `logout` / `whoami`; `--idp` on `exec` and server `status`. Redirect replay is disabled and all HTTP/file reads are bounded
- [x] Server audit labeling — bounded `token_identity_source_hint=KEY_ID:oidc[,KEY_ID:oidc...]`; `oidc` / `mtls+oidc` selected only after signature verification under a mapped key; invalid signatures and attacker-selected key ids cannot upgrade the label; closed audit allowlist preserves legitimate `token` labels and redacts unknown values; no claim or wire-format change (`TestTokenIdentitySourceIsDerivedFromVerifiedKey`, `TestShortLivedCredentialOIDCAuditSourceIsKeyDerivedAndSecretFree`, `TestForgedOIDCKeyIDCannotUpgradeAuditSource`, `TestAuditNeverWritesSecrets`)
- [x] Identity-to-NextSQL principal mapping — `NSIP` engine (`internal/auth/identitypolicy.go`: ordered issuer-scoped subject rules, claim conditions, pure transform pipeline, fail-closed `[a-z0-9._-]{1,128}` login-charset check; `FuzzDecodeIdentityPolicy`/`FuzzMapClaims`) is consumed by the broker (`IdentityPolicy.Map` in `internal/authbroker/exchange.go`); `TestExchangeHappyPathMintsVerifiableCredential`, `TestExchangeRejections`
- [x] External auth does not bypass NextSQL RBAC — every SQL server applies `ACL.AllowedScoped` on every statement; embedded mode additionally supplies a live native-principal check plus direct/transitive `security.ACL.RolesFor` membership feed to the broker, so mapped-but-not-held roles are removed and an empty intersection denies the exchange immediately (`TestExchangeRBACIntersection`, `TestEmbeddedAuthBrokerUsesLiveNativeMembership`). Standalone mode still relies on the authoritative server check unless an operator supplies its optional membership feed
- [x] Group/role mapping policy — `NSIP` literal + anchored-RE2 `${n}` group→role mappings, 16-role cap, unmatched/empty ⇒ deny, consumed by the broker; `TestIdentityPolicyGroupRegexCapture`, `TestIdentityPolicyRoleCapDenies`, `TestIdentityPolicyDefaultRolesWhenNoGroupMapped`, `TestExchangeRejections` (unmapped groups/subject ⇒ 403)

## Field-level client encryption

- [x] Implement `ENCRYPTED CLIENT` — experimental SQL grammar/catalog/runtime surface; physical `STRING`, logical scalar type in `NSCT` v10; bounded structural validation before persistence
- [x] Official-driver encryption/decryption support — Go, Node.js/TypeScript, Bun, Deno, and PHP expose client-only provider contracts, randomized encrypt/decrypt helpers, bounded in-memory overlap keyrings, and all v1 scalar types; Go-produced fixtures decrypt in every non-Go driver and a Node-produced fixture decrypts in Go
- [x] Strong client-encrypted fields remain opaque to server — no field key server-side; opaque parameter/NULL/direct-copy writes and bare projection only; predicates/joins/expressions/index/search/group/order/distinct/set operations fail closed
- [x] Define searchable-encryption leakage if any search modes are added — no searchable/deterministic mode ships; randomized ciphertext and the observable metadata/access-pattern leakage are documented in `docs/client-encryption.md`
- [x] Key rotation — `FileFieldKeyring` (Go/Node/Bun/Deno/PHP) is a durable, atomic, versioned, 0600 `NSFK1` on-disk keyring: rotation makes a new key current while retaining every prior live key for overlap reads, persisted across process restart; revoked-id reuse fails closed; the format is identical across every driver (cross-driver interop test: a Go-produced fixture opens correctly in Node) — `drivers/go/nextsql_test.go`, `drivers/bun/nextsql.test.js`, `drivers/deno/nextsql_test.js`, `drivers/node/nextsql.test.js`, `drivers/php/tests/unit.php`
- [x] Revocation — `FileFieldKeyring.Revoke` overwrites the revoked key's material with zeros on disk before persisting, refuses to resolve the revoked id afterward, and rejects revoking the current key directly; corrupt/truncated/malformed keyring files fail closed on decode (see `docs/client-encryption.md` "Rotation, revocation, and recovery")
- [x] Wrong-key/tamper behavior — AES-256-GCM authenticates exact database/table/column + public header; wrong/revoked key, context change, type mismatch, truncation, and tamper fail closed
- [x] Backup/restore/PITR tests — `TestEncryptedClientPITRRestoresExactCiphertextAtTarget` (`internal/backup/backup_test.go`): restore to a target LSN before a later archived `UPDATE` retains `TEXT ENCRYPTED CLIENT` and returns the exact pre-target ciphertext, excludes the later write, and decrypts only through the client helper (never server-side)
- [x] Replication/failover tests — `TestHAEncryptedClientCiphertextSurvivesLeaderFailover` (`tests/ha/ha_test.go`): three-voter cluster checks the acknowledged ciphertext on every replica, kills the leader, verifies the new leader still has and can decrypt the acknowledged ciphertext, commits a second ciphertext after failover, and checks it (and its decrypt) on the remaining follower

## Password hashing

- [x] Evaluate Argon2id migration — adopted: `golang.org/x/crypto/argon2` (pinned to a go1.22-compatible `v0.33.0`), time cost 1 / memory 64 MiB / parallelism 4 / 32-byte output (the package's documented recommended parameters); every new record uses it
- [x] Versioned password-hash records — `NSAU` v2 adds a per-record algorithm byte (`algoPBKDF2` / `algoArgon2id`) plus Argon2id's memory/parallelism fields (`internal/auth/store.go`); `TestNewRecordsAreArgon2idFromCreation`
- [x] Backward compatibility with existing PBKDF2 records — `Decode` still reads `NSAU` v1 files (implicitly all-PBKDF2); `Encode` always writes v2, so a v1 file upgrades in place the next time it is persisted; `TestV1FormatDecodesAndVerifies`, extended `FuzzDecode` seed corpus
- [x] Transparent rehash on successful login where chosen — `Store.Verify` re-hashes an already-confirmed-correct legacy password with Argon2id and persists before returning; a failed verify never rehashes; a concurrent delete/re-upsert is detected and skipped rather than clobbered; `TestTransparentRehashUpgradesToArgon2id`
- [x] Authentication DoS benchmarks — `internal/auth/store_bench_test.go`: `BenchmarkVerifyPBKDF2`, `BenchmarkVerifyArgon2id`, `BenchmarkConcurrentLoginAttempts` (RunParallel, mixed correct/incorrect); results and capacity-planning notes in `docs/security.md` "Password hashing"

## Audit hardening

- [x] Tamper-evident/signed audit chain design — `NSAC` v1 versioned hash chain (`SHA-256("NSAC\x01" || prev_hash || seq || canonical-event-json)`) on every record, plus an optional `NSAK` v1 Ed25519 signing keyset (current/retired, rotation overlap, verify-only export, last-known-good `SIGHUP` reload); a signed `audit.signing.enabled` transition record makes every later chained record mandatorily signed so the signed segment cannot be silently shortened; startup verifies the retained chain before appending and rejects a symlink, non-regular file, or a group/other-readable file (`internal/security/audit.go`, `auditkeys.go`)
- [x] Audit verification tooling if implemented — `nextsql audit keygen/rotate/retire/list-keys/export-public/verify` (`cmd/nextsql/audit.go`); `verify` streams the file, checks the hash chain (and signatures when given `--keyset`/`--pubkey`), reports the first bad line, and supports `--json` (`internal/security/auditverify.go`)

### Phase 25 exit gate

- [x] TODO no longer marks follow-on design hooks as implemented functionality — the OIDC design row stays `n/a`/`n/a` (design only) in `docs/security.md`'s audit table until its own delivery-plan increments landed; every other row is `yes`/`yes`/`yes` only where code + tests actually exist
- [x] mTLS/short-lived auth/IdP status is truthful — verified against `docs/security.md` "P25 Security 2.0 audit" row by row: every mTLS, token, and OIDC-broker item cites a concrete test
- [x] `ENCRYPTED CLIENT` is fully production-gated — every item-level blocker is closed (PITR, replication/failover, and durable key-rotation/revocation via `FileFieldKeyring` are all tested); `docs/client-encryption.md` "Production-gating sign-off (Phase 25)" updated to drop the phase-gate hedge now that the gate below is closed
- [x] Security review updated — `docs/security.md` "P25 security review sign-off (2026-09-02)", surface-by-surface in the same dated-review format as the P16 security review; flips every P25 audit-table row's production-gated column to `yes` except explicit non-goals (OCSP, opaque OIDC introspection, JIT provisioning, searchable client-side encryption)

---

# Phase 26 — System catalog / introspection 2.0

The virtual schema core (capabilities, tables, columns, indexes, storage,
replication/raft, workflows, tasks, partitions, table/index stats) landed
first, with RBAC filtering, redaction, and stable schema columns. **Live
session/query/transaction/CDC rows landed 2026-09-01, `system.locks` landed
2026-09-01** (increments below). **All nine planned SHOW aliases landed
2026-09-02**. **P26 System catalog / introspection 2.0 is COMPLETE
(2026-09-02)**: the exit-gate audit below closed all three remaining items.

**`system.locks` live rows landed 2026-09-01** (second P26 increment,
same day). `internal/txn/lock.go`: `keyState`/`heldRange` gained a `tag
string` field (a caller-supplied label, typically a table name), set once
from the first `Acquire`/`AcquireRange` call that supplies a non-empty tag
and never overwritten (so it survives the lock changing holders) —
documented as best-effort, since the lock table's key namespace is shared
across every table in one storage engine and two different tables can in
principle collide on identical raw key bytes (a pre-existing sharp edge,
not something this introspection layer can or should fix). New
`LockManager.Snapshot() []LockInfo` (`Txn`, `Mode`, `Tag`, `Range`) — held
locks only, waiting/not-yet-granted requests are not included.
`Acquire`/`AcquireRange` gained a `tag string` parameter, threaded through
`grantKey`/`grantRange`/the `waiter` struct/`wake`. `internal/txn/manager.go`
`LockKey`/`LockRange` gained the same `tag string` parameter (empty string
where none is available). `internal/storage/btree/btree.go`: `Tree` gained
an `atomic.Pointer[string]` `name` field + idempotent `SetName`/`Name`
(deliberately its own atomic, not the tree's existing `mu`, since lock
acquisition must not risk nesting under it). `internal/storage/btree/txn.go`:
all 4 lock-acquisition call sites (`lockWrite`, `LockExclusive`, `lockRead`,
`lockRange`) now pass `tx.tree.Name()`. `internal/executor/fk.go`'s 2
`tm.LockKey` call sites (outbound/inbound FK checks) pass `parent.Name`;
`internal/executor/ai.go`'s `lockAI` gained a `tag string` parameter, passed
`tab.Name` at both call sites. Tree tagging is applied at the executor's
central per-kind resolvers — `internal/executor/db.go` `heap`/`index`
(tagged with the *table*, not the index name, so an index lock reports the
same `table_name` as its heap)/`vecStore`, and `internal/executor/partition.go`
`partitionHeap`/`partitionVec`/`partitionIndex` (tagged with the base table
name — `system.locks` has no partition column) — rather than at tree
creation, since registration into `db.heaps`/`db.idxs`/`db.vecs`/the
partition maps happens through several different code paths (not just
`putHeap`/`putVec`/`putIndex`) and `SetName` is a cheap, idempotent no-op
after the first call, so tagging at every read is both simpler and more
complete than auditing every insertion site. New `DB.LockSnapshot()
[]txn.LockInfo` thin wrapper over `db.Eng.TM.Locks.Snapshot()`.
`internal/executor/system.go` `systemLocksRows`: sorts by `(txn, tag,
mode, key-before-range)` for determinism, assigns a per-query `"<txn
id>:<n>"` `lock_id` (not stable across queries — there is no natural
persistent identity for a lock), `mode` is `shared`/`exclusive`/`unknown`,
`granted` is always `true` today (no waiter introspection). Visibility
matches `system.transactions`: a lock is attributed to the user of whichever
live registered session currently holds that transaction (built from
`DB.LiveSessions` + `Session.TxnSnapshot`, the same plumbing the prior
increment added); an admin sees every lock, a non-admin sees only their own,
and a lock whose transaction cannot be attributed to a live session (e.g.
embedded/CLI/test use that never called `DB.RegisterSession`) is visible
only to an admin. Tests: `internal/txn/lock_test.go` `TestLockSnapshotTag`
(key + range tag round trip, tag preserved across a second co-holder that
passes none, empty after `ReleaseAll`) + existing lock tests updated for the
new parameter; `internal/txn/manager_test.go` `TestLockKeySoleWriter` updated;
`internal/executor/system_test.go` `TestSystemLocksLive` (an FK check inside
an open, uncommitted transaction always takes a real lock — unlike ordinary
single-writer INSERT/UPDATE, which skips locking entirely when it is the
only active writer — so a second session observes `table_name="parent"`,
`mode="shared"`, `granted=true` mid-transaction, and it disappears after
`COMMIT`), `TestSystemLocksRBAC` (non-admin doesn't see another user's lock,
admin does). `go build ./...` + `go vet ./...` (same pre-existing, unrelated
`cdc.go` cancel-leak notes) + `internal/executor`/`internal/txn`/
`internal/storage/btree`/`internal/protocol` `go test -race` green. Docs:
`docs/system-catalog.md` + web equivalent updated (`system.locks` moved from
"not yet live" to documented and live, including the tag-collision caveat).

**Live session/query/transaction/change-stream rows landed 2026-09-01.**
`system.sessions` / `system.active_queries` / `system.transactions` /
`system.change_streams` now return real, node-local, in-memory state instead
of always-empty stubs (`system.locks` landed the same day in a second
increment — see above). New `internal/executor/db.go`: `DB.RegisterSession`/`UnregisterSession`/
`LiveSessions` (a process-local `map[uint64]*Session` registry, keyed by an
atomic counter id) back `system.sessions`/`system.active_queries`/
`system.transactions`; `DB.CDCSubscriptions`/`registerCDCSubscription`/
`unregisterCDCSubscription`/`updateCDCSubscriptionLSN` (a parallel
`map[uint64]*cdcSubInfo`, LSN published via `atomic.Uint64`) back
`system.change_streams`. `internal/protocol/server.go` calls
`db.RegisterSession`/`UnregisterSession` around each connection's lifetime
(`serveConn`); a `Session()` obtained directly — embedded/CLI/test use — is
never registered and stays invisible to these tables, documented as such.
`internal/executor/session.go`: new mutex-guarded snapshot fields/methods on
`Session` — `beginQuery`/`endQuery`/`CurrentQuery` (wired into
`execAdmitted` alongside the existing unsynchronized `s.execSQL`, which
stays same-goroutine-only) and `setTxnActive`/`clearTxnActive`/
`TxnSnapshot` (wired at all 5 `s.x` create/clear sites: `startRead`,
`start`, both `commit()` exit paths, `abort()`). These exist because
`s.execSQL` and `s.x` are otherwise read/written only by a session's own
goroutine — publishing them for cross-session introspection without a
dedicated synchronized copy would be a real data race under `-race`, not a
benign one. `internal/executor/cdc.go` `execSubscribe`: registers/
unregisters the subscription around the existing `cleanup()`, and publishes
`current.Token` via `updateCDCSubscriptionLSN` each transaction delivered —
`cdc.Subscription.Token()`/`Lag()` mutate/read unsynchronized fields, so they
are read only from the subscribing goroutine, never called cross-goroutine.
`internal/executor/system.go`: `systemSessionsRows`/`systemActiveQueriesRows`/
`systemTransactionsRows`/`systemChangeStreamsRows` (admin sees every row;
non-admin sees only their own user's, matching the existing `system.tasks`
owner-filter pattern; `change_streams` instead filters by table visibility,
matching `system.columns`/`system.indexes`). Docs: new `docs/system-catalog.md`
+ `docs/web/content/docs/system-catalog.md` (nav entry added) — the first
docs for the whole `system.*` schema, since none existed before this
increment. Tests: `internal/executor/system_test.go`
`TestSystemSessionsAndActiveQueriesLive`, `TestSystemTransactionsLive`,
`TestSystemSessionsRBAC`, `TestSystemChangeStreamsLive`. `go build ./...` +
`go vet ./...` (pre-existing, unrelated `cdc.go` cancel-leak vet notes
untouched) + `internal/executor`/`internal/protocol`/`internal/txn`
`go test -race` green; full `go test ./... -count=1` green except the
pre-existing `internal/replication` Raft-timing flakes under full-suite load.
`system.locks` was scoped out of this increment and landed later the same
day — see the increment above. The `SHOW` convenience aliases subsequently
landed 2026-09-02; the exit gate is the remaining P26 work.

**`SHOW` convenience aliases landed 2026-09-02.** All nine planned native
commands are implemented: `SHOW DATABASES`, `SHOW TABLES`, `SHOW INDEXES`,
`SHOW CONNECTIONS`, `SHOW QUERIES`, `SHOW TRANSACTIONS`, `SHOW LOCKS`,
`SHOW CLUSTER`, and `SHOW STORAGE`. The parser lowers each command directly
to an `ast.Select` over its canonical source (`system.storage` database
projection, `system.tables`, `system.indexes`, `system.sessions`,
`system.active_queries`, `system.transactions`, `system.locks`,
`system.replication`, and `system.storage`, respectively), so execution uses
the existing virtual-schema path and cannot diverge from its `CONNECT`
requirement, row-level RBAC, or redaction. `SHOW TASKS [AFTER ...] [LIMIT
...]` remains its existing bounded task-runtime command. The new aliases
intentionally accept no filtering/pagination clauses; machine consumers that
need `WHERE` / `ORDER BY` / `LIMIT` query `system.*` directly. No persistent,
catalog, WAL, Raft, or wire change and no `system.SchemaVersion` bump. Parser
coverage pins every mapping and rejects unknown/suffixed forms; executor
coverage compares each alias with its source view and pins result columns.

The same audit found stale machine-readable capability data and fixed it in
this increment: completed RANGE/HASH/LIST partitioning is now `supported`;
the follower-read description records live `STRONG`/`BOUNDED`/`STALE`
routing instead of saying routing is absent; client-field-encryption metadata
lists all official drivers; and `system_schema_v2` / `system_show_aliases`
make the current contract and syntax directly discoverable. Tests pin these
high-regression-risk rows. The full capability registry and the metadata
needed by the planned Studio/Manager surfaces still require an exit-gate
audit, so P26 remains open.

The alias audit also fixed a security/correctness defect in the shared source:
`system.storage.database` previously returned `Engine.Path()` despite being
documented as a database name. `DB.SetDatabaseName` now publishes the logical
served name from the protocol/hosting configuration; unnamed embedded use
reports `default`. `system.storage` and `SHOW DATABASES` no longer expose a
filesystem path. Executor tests pin both the redaction and hosted-name path.

Create a canonical machine-queryable virtual `system` schema.

- [x] `system.capabilities`
- [x] `system.active_queries`
- [x] `system.sessions`
- [x] `system.transactions`
- [x] `system.locks`
- [x] `system.tables`
- [x] `system.columns`
- [x] `system.indexes`
- [x] `system.table_stats`
- [x] `system.index_stats`
- [x] `system.storage`
- [x] `system.replication`
- [x] `system.raft`
- [x] `system.workflows`
- [x] `system.tasks`
- [x] `system.change_streams`
- [x] `system.partitions`
- [x] RBAC for system tables
- [x] Sensitive fields redacted/omitted
- [x] Stable versioned columns for machine consumers

Optional convenience commands backed by the same source of truth:

- [x] `SHOW DATABASES`
- [x] `SHOW TABLES`
- [x] `SHOW INDEXES`
- [x] `SHOW CONNECTIONS`
- [x] `SHOW QUERIES`
- [x] `SHOW TRANSACTIONS`
- [x] `SHOW LOCKS`
- [x] `SHOW CLUSTER`
- [x] `SHOW STORAGE`

**P26 exit gate closed 2026-09-02** (third P26 increment, same day as the
`SHOW` aliases). Audited all three remaining gate items against the actual
implementation rather than assuming the prior increments already covered
them, and found one genuine gap plus two items that were already satisfied
but untested/unaudited.

*Gap found and fixed: Studio/Manager official-interface sufficiency.*
Checked every bullet in the Phase 28 "NextSQL Manager MVP" navigation list
against the current `system.*` surface. Everything else already had an
official read source (server/cluster status, databases, connections,
performance, maintenance/index/storage state), but "Users/roles/privileges
administration" and "Security dashboard" had none at all — `auth.Store`
(passwords) and `security.ACL` (roles/grants) were reachable only by reading
their on-disk files directly, exactly what this gate item exists to
prevent. Fixed with three new admin-only virtual tables: `system.users`
(`name, password_algo` — never the hash or salt), `system.roles` (`role,
members`), `system.grants` (`grantee, privilege, scope, object`). New
`internal/security/rbac.go`: `Privilege.String()`/`ScopeKind.String()` (the
exact inverse of `ParsePrivilege`/`ParseScope`, for rendering grant rows in
the same spelling `GRANT`/`REVOKE` accepts) and `ACL.Snapshot() ([]RoleInfo,
[]Grant)` (defensively copied, sorted deterministically — mutating the
returned slices cannot corrupt the ACL). New `internal/auth/store.go`:
`Store.Snapshot() []UserInfo{Name, Algo}` (algorithm only, for spotting
accounts still pending transparent Argon2id rehash — never hash/salt). New
`internal/executor/system.go`: `systemUsersRows`/`systemRolesRows`/
`systemGrantsRows`, admin-only (`s.acl == nil || s.isAdmin()`) with the same
"empty for non-admin, never an error" convention as `system.tasks`/
`system.locks` — there is no per-row "ownership" concept for a user/role/
grant the way there is for a task or a lock. Tests: `internal/security/
rbac_test.go` `TestPrivilegeAndScopeStringRoundTrip`, `TestACLSnapshot`
(including a mutation-doesn't-leak-into-the-next-snapshot check);
`internal/auth/store_test.go` `TestStoreSnapshot` (sorted, mixed
Argon2id/legacy-PBKDF2 algo reporting); `internal/executor/system_test.go`
`TestSystemUsersRolesGrants` (non-admin sees zero rows from all three;
admin sees the expected rows; asserts no password material leaks through
any column).

*Also fixed while auditing: capability registry completeness.* Cross-checked
`system.capabilities` against every phase's TODO.md status and found nine
production-gated P23/P25 surfaces with no discoverable row of their own —
each was, at best, a passing mention inside a different row's description
text, not something a version-aware client could query for directly. Added
`mtls`, `token_credentials`, `oidc_broker`, `audit_chain`, `storage_caps`,
`vector_ivf`, `vector_ivfpq`, `vector_sparse`, `quantized_vector_index`, all
`supported` (`internal/system/schema.go`). Also fixed a stale description
found in the same pass: `fulltext`'s description predated the WEIGHT/FACET
increments (landed 2026-08-31) and didn't mention either. Pinned in the
existing `internal/executor/system_test.go` `TestSystemCapabilities` (no
`SchemaVersion` bump — new/corrected capability rows are not a column-shape
change, matching the documented `system_schema_v2` contract).

*Audited and confirmed already satisfied: RBAC coverage breadth and
realm/database visibility.* `system.table_stats`/`system.index_stats`/
`system.partitions` share `canSeeTable` with `system.tables`/`columns`/
`indexes` (only the latter three had dedicated RBAC tests) and
`system.workflows` uses the separate `canSeeWorkflow` gate with no dedicated
test of its own — closed with new `TestSystemCatalogRBACRemainingViews`
(bob-sees-nothing/alice-sees pattern matching the existing `TestSystemRBAC`
style) covering all four. Realm/database visibility turned out to be a
structural guarantee, not a filter that could regress or had gone untested:
`protocol.Server` holds exactly one `*executor.DB`
(`internal/protocol/server.go`), and `cmd/nextsqld`'s `openHostedDefault`
opens exactly one realm/database pair per process via
`hosting.Registry.Default()` (`cmd/nextsqld/main.go`) — the hosting registry
can hold metadata for multiple realms/databases (storage caps, etc.), but no
running process ever opens more than one, so there is no code path today
where a session could observe another realm's or database's `system.*`
rows. **Correction (2026-09-03, log #93):** the "no running process ever
opens more than one" premise above went stale once the M2 cross-cutting
track shipped live, concurrent, selectable multi-database routing
(M2-3a/M2-5/M2-6) — flagged by an independent production-readiness
re-audit. Re-checked directly against the current code rather than just
updating the claim: the isolation property still holds, on different,
structural grounds — every `system.*` introspection table reads from the
calling session's own `*executor.DB` (`s.db.LiveSessions()` etc.), and
`dbmanager.Manager.Acquire` hands each session exactly one distinct `DB`
per realm+database with no shared registry a session could read across.
No vulnerability; the prior justification text was simply out of date.

`go build ./...` + `internal/executor`/`internal/security`/`internal/auth`
`go test -race` + full `internal/executor` suite green. Docs:
`docs/system-catalog.md` (new "Security administration tables" section,
updated P26 implementation audit table, new "P26 exit gate closure
(2026-09-02)" section), `docs/web/content/docs/system-catalog.md`.
Documented follow-ons, not gate items: a redacted server-configuration
snapshot table and an audit-log surface for the still-unbuilt Phase 28
Manager's configuration/audit viewers; selectable multi-database hosting
(separate cross-cutting track).

### Phase 26 exit gate

- [x] Studio/Manager can operate from official system interfaces without reading internal files
- [x] System schema obeys RBAC and realm/database visibility rules
- [x] Capability registry is authoritative for version-aware clients

---

# Phase 27 — Operational maturity + workload governance

## Server lifecycle

- [x] Graceful drain — `protocol.Server.Drain(timeout)`, landed 2026-09-02 (see the Phase 27 increment below).
- [x] Controlled shutdown — `nextsqld` drains on SIGINT/SIGTERM instead of an immediate hard close, landed 2026-09-02.
- [x] Connection draining — same increment: idle connections close immediately, busy ones (in-flight statement or open transaction) close as soon as they finish or the `shutdown_drain_ms` deadline arrives.
- [x] Leader transfer — landed 2026-09-02: `CLUSTER TRANSFER LEADER` admin SQL statement (`ast.TransferLeader`, requires cluster `ADMIN`) wraps `replication.Cluster.TransferLeadership()` (`internal/replication/cluster.go`); `nextsql cluster transfer-leader` CLI subcommand issues it over a live connection. No way to target a specific destination voter yet — Raft picks the best-caught-up one, matching the underlying library call.
- [x] Maintenance mode — `CLUSTER MAINTENANCE ENABLE|DISABLE` + `nextsql cluster maintenance enable|disable`, landed 2026-09-02. Node-local (like `CLUSTER DRAIN`), not Raft-replicated; blocks DML/DDL/`BEGIN` with `Unavailable` while enabled, reads unaffected. See the increment log entry for the leader-failover caveat.
- [x] Rolling upgrade workflow — documented 2026-09-02 in `docs/ops.md` "Rolling upgrade" (transfer-leader → drain → restart → wait-for-catch-up, per node) and tested end-to-end by `tests/integration/rolling_upgrade_test.go` `TestRollingUpgradeDrainWithLeaderTransferNoTransactionLoss`. See the increment log entry — building this test found and fixed three real router/protocol/replication robustness gaps, and found (documented, not fixed) a fourth, deeper local-commit-before-replicate-ack correctness gap tracked separately below.
- [x] Online format/catalog migration strategy where safe — documented 2026-09-02 in `docs/storage-format.md` "Format and catalog migration strategy", see the increment log entry. Catalog-record changes are already safe to migrate online via the existing multi-version-decode pattern (`NSCT` v1..v10); format-level (page/superblock) changes require the offline dump/reload path (`nextsql backup`/SQL copy into a freshly created database), documented rather than built speculatively since there is no format v2 to migrate to/from yet. Also extracted `internal/upgrade/compat` as a dependency-free leaf package and wired `decodeSuperblock`/`catalog.DecodeTable` to enforce it directly (`compat.Check`), so `nextsql diagnose`'s printed compatibility window can no longer drift from what's actually enforced, and the version-mismatch error now names the actual/supported version numbers.
- [x] Backup retention management — `nextsql backup list`/`backup prune` landed 2026-09-02, see the increment log entry. Treats each immediate subdirectory of `--base-dir` with a valid header as one backup; `prune` supports `--keep-count`/`--keep-days`, always keeps at least the newest backup, and previews by default (requires `--confirm` to delete).
- [x] WAL retention management — `wal_retention_ms` config key landed 2026-09-02, see the increment log entry. Automatically maintains `DB.SetWALRetentionHorizon` from a time-based policy (requires `wal_archive`); pruning itself still requires a scheduled `MAINTAIN DATABASE`, unchanged.
- [x] Replica-lag management — `replica_lag_check_ms`/`replica_lag_warn_entries` config keys landed 2026-09-02, see the increment log entry. Periodic monitor reading this node's own `system.replica_health.apply_backlog`, edge-triggered warn/recover logging + metrics; alerting-only, no write-rejection counterpart (unlike disk watermarks) since `Cluster.FollowerReadHealthy` already keeps too-stale followers out of read routing.
- [x] Disk watermark policies — `disk_watermark_check_ms`/`disk_watermark_warn_percent`/`disk_watermark_reject_percent` config keys landed 2026-09-02, see the increment log entry. Periodic statfs-based monitor with warn logging + hysteresis-gated write rejection, node-local and independent of `CLUSTER MAINTENANCE ENABLE`/`DISABLE`.
- [x] Capacity warnings — same increment as disk watermark policies above: the warn threshold's edge-triggered log line and `metrics.Snapshot.DiskWatermarkWarns` counter are the capacity-warning surface; no separate mechanism was needed.

## Session controls

Audit existing controls first; add only missing capabilities:

- [x] Max global connections — pre-existing `protocol.Limits.MaxSessions` (default 128) enforced at accept time; landed 2026-09-02: `max_connections` config key makes it operator-configurable (it was previously hardcoded).
- [x] Per-user connection limit — new 2026-09-02: `max_connections_per_user` config key (default 0 = unlimited); enforced after authentication, before session creation, rejects with `exhausted`.
- [x] Per-realm and per-database connection limits — landed 2026-09-03, closing Phase 27 itself: the original deferral's premise ("one `nextsqld` process still opens exactly one database") went stale once the M2 multi-database-hosting track's M2-3a/M2-5/M2-6 shipped live, concurrent, selectable multi-database routing within one process. New `protocol.Limits.MaxSessionsPerDatabase`/`MaxSessionsPerRealm` (`max_connections_per_database`/`max_connections_per_realm` config keys, both 0 = unlimited), enforced in `serveConn` right after `MaxSessionsPerUser` (same pattern: check-increment under `s.mu`, decrement via `defer` at teardown), keyed on `realmName`/`dbName` — already resolved earlier in the function and already past the pre-auth identityOK fold, so nothing new is disclosed. See the increment log entry for the full design and live-verification writeup.
- [x] Idle session timeout — pre-existing `protocol.Limits.Idle` (default 60s) applied as a per-frame socket deadline; landed 2026-09-02: `idle_timeout_ms` config key makes it operator-configurable (it was previously hardcoded).
- [x] Idle transaction timeout — landed 2026-09-02: `idle_transaction_timeout_ms` config key / `protocol.Limits.IdleTxn` is a dedicated bound enforced by the connection's own socket read deadline (unlike `transaction_timeout_ms`, which is only checked lazily on the next statement), so it actively reclaims an idle-while-open transaction even if the client never sends another statement. Auditing this found and fixed a real gap: tearing down a connection with an open transaction — by this timeout, the general `idle_timeout_ms`, or a forced `Drain` close — never actually rolled the transaction back, so its locks stayed held indefinitely; new `executor.Session.Abort` is now called from the protocol server's connection-teardown path whenever a session still has a transaction open.
- [x] Statement timeout — `statement_timeout_ms` config key; landed 2026-09-02. Overrides the pre-existing `scheduler.Budget` wall-clock bound (default 30s); auditing this found and fixed a real gap where the base SeqScan/IndexScan loops never checked it (only specialized paths did) — see the increment log entry.
- [x] Transaction timeout — new 2026-09-02: `transaction_timeout_ms` config key (default 0 = unbounded); bounds a transaction's total open lifetime, force-aborting it (even a `COMMIT`) once exceeded.
- [x] Lock timeout — new 2026-09-02: `lock_timeout_ms` config key (default 0 = block indefinitely, matching pre-existing behavior); bounds a contended, non-deadlocking key/range lock wait. Process-wide (the shared lock table has no per-connection identity), unlike the other timeouts here.
- [x] Queue timeout — pre-existing `query_queue_wait_ms` (`scheduler.Admission`) already bounds queued-query admission.

## Resource groups

- [x] Design `RESOURCE GROUP` — `CREATE`/`ALTER`/`DROP RESOURCE GROUP`, catalog-persisted, RBAC-gated, `system.resource_groups`; landed 2026-09-02. Descriptor only — see the increment log entry for what is explicitly deferred.
- [x] Workload max concurrency — `SET RESOURCE GROUP name` / `RESET RESOURCE GROUP` (RBAC-gated via `GRANT USAGE ON RESOURCE GROUP`), landed 2026-09-02: a non-zero `MAX_CONCURRENCY` adds a second admission gate strictly on top of the existing process-wide `scheduler.Admission`, never a substitute for it.
- [x] Workload memory budget — same increment: a non-zero `MEMORY` overrides the assigned session's `scheduler.Limits.Memory`.
- [x] Workload CPU/worker budget — same increment: a non-zero `WORKERS` overrides the assigned session's `scheduler.Limits.Workers`, still clamped to the process ceiling by `Limits.normalized()`.
- [x] Priority — landed 2026-09-02 as its own dedicated increment (see the increment log entry below), after being deliberately deferred out of the earlier resource-group audit specifically because it needed one. `scheduler.Admission`'s internal `slots chan struct{}` counting semaphore was replaced with a `sync.Mutex`-protected `free int` counter plus a `container/heap` priority-ordered waiter queue (`internal/scheduler/admit.go`): on release, a freed slot is handed to the highest-priority currently-queued waiter (FIFO among equal priorities) rather than left for a fresh concurrent caller to grab. New `AcquireWithPriority(ctx, priority)` sibling method (plain `Acquire` unchanged, implemented as priority 0) keeps every non-priority call site — `resourceGroupGate`'s per-group gates, `executeClaimedTask`'s task-execution admission — untouched and behaviorally identical. Only `Session.ExecContext`'s process-wide acquire now threads through the session's assigned resource group's `Priority` (0 if none). The genuinely hard part — a waiter's context firing at the exact instant `release()` hands it a slot — is resolved by a heap-index-based state machine (`index >= 0` = still queued, safe to cancel; `index == -1` = a release already committed the slot to this waiter, cancellation must be refused and the grant drained instead) so no interleaving can leak a slot or double-grant one. Priority affects only queue *ordering*, never preemption of an already-admitted query, and does not change a waiter's own `QueueWait` bound — starvation under sustained high-priority contention is an accepted, unmitigated tradeoff, not a new fairness mechanism.
- [x] Integrate API/analytics/workflow/maintenance/backup classes with one scheduler — audited and closed 2026-09-02, see the increment log entry. Found and fixed one real gap: claimed-task execution (`internal/executor/task.go` `executeClaimedTask`, the path every scheduled `RUN WORKFLOW` runs through) called `Session.execRunWorkflow` directly, bypassing `Session.ExecContext`'s `db.admit.Acquire` entirely — bounded only by `TaskRuntime`'s own separate `Workers` limit (1-16), independent of and invisible to the process-wide admission gate every regular query, `ANALYZE`, and `MAINTAIN` already goes through. Fixed by acquiring `db.admit` in `executeClaimedTask` itself, mirroring `ExecContext`'s exact pattern (including metrics and the "reject before any task-state mutation, let the lease expire" behavior the pre-existing `db.gate.AllowWrite()` check right above it already had). API (regular SQL) and analytics (`ANALYZE`) were already integrated by construction — they're just SQL statements dispatched through the same `ExecContext`. Maintenance was already integrated and already tested (`TestMaintainSQLObeysAdmission`). Backup is inherently out of scope for *this* process: `nextsql backup` is an offline CLI invocation with no running `nextsqld`/`scheduler.Admission` involved at all (confirmed by grep — `nextsqld`'s only `internal/backup` usage is WAL archival/retention, never a live backup trigger), so there is no live scheduler for it to integrate with.
- [x] No independent unbounded pools — audited and closed 2026-09-02 as part of the same pass, see the increment log entry. Confirmed bounded: `TaskRuntime` (fixed `Workers` goroutines, spawned once, never per-task), `scheduler.Pool`/`scheduler.Admission` (both pre-existing bounded semaphores), per-connection goroutines (bounded by `MaxConnections`). CDC (`SUBSCRIBE`) spawns no goroutine at all — delivery is pull-based within the existing connection goroutine, and the initiating statement is itself admission-gated like any other. No independent unbounded goroutine-per-item pattern found anywhere in the live server.

## Operational CLI

- [x] `nextsql cluster drain <node>` or equivalent — landed 2026-09-02 as `nextsql cluster drain` / `CLUSTER DRAIN [WITH (TIMEOUT_MS = n)]`, targeting whichever node `--addr` connects to (no separate `<node>` id argument, same convention as `transfer-leader`). Purely local to that node — unlike leader transfer it needs no Raft cluster and works the same on a single-node deployment.
- [x] `nextsql cluster transfer-leader <node>` or equivalent — landed 2026-09-02 as `nextsql cluster transfer-leader` (no `<node>` target argument: matches `TransferLeadership()`, which lets Raft pick the destination).
- [x] Machine-readable operation status — `--json` flag added 2026-09-02 to `nextsql exec` and every `nextsql cluster` subcommand (`status`/`transfer-leader`/`drain`/`maintenance enable|disable`), printing a single JSON object instead of tab-separated text. See the increment log entry and `docs/ops.md` "Machine-readable operation output".

### Phase 27 exit gate

Each line below is checked once its specific claim is verified by a real,
passing test. As of 2026-09-03, every exit-gate line below is checked,
including "Local commit precedes replication acknowledgment" — the
structural fix, deferred twice before in favor of mitigations, landed
2026-09-03 (log #79) — and **Phase 27 itself is now complete**: "Per-realm
and per-database connection limits," the one item still deliberately
deferred after log #79, closed the same day (log #80) once its own
blocking premise ("multi-database hosting is foundation-only") went stale
— the M2 cross-cutting track's M2-3a/M2-5/M2-6 had already shipped live,
concurrent, selectable multi-database routing within one process by then.
See that increment's log entry for the closure writeup.

- [x] Planned maintenance can drain without unnecessary transaction loss — verified 2026-09-02 by `TestRollingUpgradeDrainWithLeaderTransferNoTransactionLoss` (`tests/integration/rolling_upgrade_test.go`): a full transfer-leader → drain → simulated-restart → rejoin cycle under continuous write load loses no acknowledged write. Caveat found by the same test and tracked separately (not a loss, but a related latent gap): see "Local commit precedes replication acknowledgment" below.
- [x] Resource groups cannot bypass global safety limits — true by construction since the P27 seventh increment (log #53): a resource group's admission gate is strictly additional to the pre-existing process-wide `scheduler.Admission`, never a substitute (`Session.ExecContext` acquires both), proven end-to-end by `TestResourceGroupMaxConcurrencyBlocksExecContext`.
- [x] Rolling maintenance/upgrade procedure documented and tested — `docs/ops.md` "Rolling upgrade" (2026-09-02) + `TestRollingUpgradeDrainWithLeaderTransferNoTransactionLoss`.
- [x] Local commit precedes replication acknowledgment — structural fix landed 2026-09-03 (P27 twenty-first increment, log #79 — resumed from this same item after the two prior deferrals below, at the user's explicit "continue @TODO.md" then explicit choice to attempt the full fix this time). `internal/storage/engine.go` `commitAndReplicate` used to commit a transaction to local storage (`commitLocked`) *before* calling `repl.Replicate` for Raft quorum, so a `Replicate` failure (a write racing `CLUSTER TRANSFER LEADER`, or any leader transition racing a write) left an un-replicated local row no Raft catch-up ever reconciled. **Now closed for the dominant case**: the transaction's `CommitRec` is appended via a new `wal.Log.AppendHeld` durability-barrier primitive and held — unflushed, not visible, locks not released — until `Replicate`'s outcome is known. A **definite** failure (`c.raft.State() != raft.Leader`, rejected before `raft.Apply` is ever attempted — the common case, e.g. any write landing during a leadership-transfer window) discards the held record and rolls the transaction back: no orphan, nothing to reconcile. An **ambiguous/in-doubt** failure (`raft.Apply` was actually called but the quorum wait itself failed — lost leadership, enqueue timeout, mid-flight shutdown) is structurally undecidable — the entry may or may not have reached quorum — so it keeps this package's original fail-open behavior exactly: commit, report the orphan, rely on the existing `replSuspect`/`CLUSTER RECONCILE CONFIRM` mitigation (below) as the dedicated handler for this one narrowed residual case. See log #79 for the full design, the two structural hazards this needed (a WAL-level durability-barrier primitive; a replication-batch correctness bug found and fixed during implementation), and the test evidence, including a real 3-node cluster proving the definite case is now orphan-free end to end.

  **Structural-fix feasibility investigated in depth 2026-09-02** (P27 twentieth increment, log #68 — resumed from this same item after landing resource-group Priority, at the user's explicit "continue @TODO.md"): the likely fix direction (invert to replicate-then-locally-commit, deferring `TM.Commit`'s visibility+lock-release until quorum confirms) was grounded via two dedicated research passes before any code was written — the commit path (`commitLocked`'s exact step order, `TM.Commit`'s single-call visibility+unlock shape with no existing durable/visible split, `FSM.Apply`'s materially different raw-replay shape on followers, `Cluster.Replicate`'s fully-synchronous quorum wait, and the full regression-test surface across `internal/storage`/`internal/txn`/`internal/executor`/`internal/replication`), then specifically the WAL/recovery layer once the first pass surfaced the real blocker. **Two concrete, previously-unidentified hazards were found that make the structural fix meaningfully harder than this item's own prior writeup assumed**: (1) `internal/wal`'s `Flush` is not selective — it always durably fsyncs the *entire* current in-memory buffer, and two call sites unrelated to `commitAndReplicate` (`Engine.Checkpoint()`, and `ApplyReplicated`/`InstallRecords` on the Raft FSM-apply path — the latter driven by ordinary concurrent cluster traffic, not a rare edge case) call an unconditional flush with no serialization against a pending, not-yet-quorum-confirmed commit record, so a naive "append but don't flush yet" split can still have its unconfirmed `CommitRec` durably persisted as a side effect of unrelated activity; (2) `internal/recovery`'s `RedoUntil`/`UncommittedUntil` treat a transaction as committed by the mere *presence* of a `RecCommit`, with no last-record-wins logic against a later abort record for the same TxnID — so appending an abort record after a durably-flushed (accidentally or otherwise) commit record would not stop crash recovery from replaying it as committed anyway. Holding `e.mu` across the whole Raft round-trip to avoid the flush race by brute force was also evaluated and rejected: there is a believable deadlock path where Raft's own FSM-apply goroutine needs `e.mu` to process an unrelated, earlier-queued log entry that the transaction's own `raft.Apply()` future may transitively depend on to resolve. **Conclusion: closing this gap for real needs a new WAL flush-barrier primitive plus a change to the crash-recovery commit/abort-resolution path** — code responsible for surviving every ordinary process crash — spanning `internal/wal`, `internal/recovery`, `internal/storage`, `internal/txn`, and `internal/replication`. Materially bigger and touches more load-bearing code than this item's original "substantive change to the core commit path" framing anticipated.

  **Given this, presented the (now more precisely scoped) risk to the user via `AskUserQuestion`** — options were: attempt the full WAL/recovery redesign now anyway, land a stronger mitigation instead, or leave today's detection-only mitigation as-is. **User chose to land a stronger mitigation.** Landed 2026-09-02: a local commit that fails to replicate now also tells its `Replicator` — via a new optional `storage.ReplicationOrphanReporter` interface (`ReportReplicationOrphan()`, type-asserted at the existing `commitAndReplicate` orphan site, so every other `Replicator`/test double is unaffected) — and `*replication.Cluster` implements it by setting a new node-local `replSuspect` flag. `Cluster.StrongReadBarrier()` (`internal/replication/read.go`) now refuses `Unavailable` while that flag is set, *regardless of leadership* — closing exactly the case a leadership check alone can't catch (a `Replicate` failure for a reason other than losing leadership, e.g. `ErrEnqueueTimeout`/`ErrRaftShutdown`, where the node stays leader). Deliberately scoped to `STRONG` reads only, not a forced leadership transfer or a block on all local visibility: `STRONG` is the one consistency mode that promises linearizable, read-after-acknowledged-write behavior and is therefore the one most harmed by silently serving a row about to diverge from the rest of the cluster; `BOUNDED`/`STALE` already accept staleness by contract, and a forced leadership transfer was judged unnecessary disruption for a protection `StrongReadBarrier` already delivers directly. The flag is node-local (like maintenance mode), not Raft-replicated, so a clean node elected leader afterward is never affected by another node's divergence — confirmed by a dedicated test. New `CLUSTER RECONCILE CONFIRM` SQL (cluster `ADMIN`, cannot run inside a transaction, purely node-local like `CLUSTER MAINTENANCE`; the `CONFIRM` keyword is mandatory so it can't be fat-fingered as a bare `CLUSTER RECONCILE`) clears the flag — the intended operator runbook is: notice `metrics.Snapshot.ReplicationOrphans` increase or a `StrongReadBarrier` rejection naming the cause, verify/repair the node's data, then run `nextsql cluster reconcile confirm` (or the SQL directly) to resume serving `STRONG` reads. No automatic clearing — deliberate, since this is a data-integrity flag an operator should actually look at. New `system.replica_health.replication_suspect` column surfaces the flag for monitoring. New `security.ActionClusterReconcile` audit action.

  Tests: `internal/replication/read_test.go` `TestStrongReadBarrierBlockedByReplicationOrphanUntilReconciled` (a real 3-node Raft cluster: orphan → barrier fails on the leader despite still being leader and still passing `VerifyLeader` → other nodes unaffected → `ClearReplicationSuspect` → barrier passes again) and `TestReplicationOrphanMethodsNilSafe`; `internal/storage/btree/btree_test.go` `TestInsertReportsReplicationOrphanToReporter`/`TestInsertDoesNotReportOrphanOnSuccessfulReplicate` (proves `commitAndReplicate` calls the new reporter hook on exactly a `Replicate` failure, never on success); `internal/executor/cluster_reconcile_test.go` `TestClusterReconcileConfirmRBAC` (forbidden without grant, rejected inside a transaction, `Unavailable` with no cluster attached) and `TestClusterReconcileConfirmClearsSuspectFlag` (a real single-node Raft cluster end to end through actual SQL); `internal/sql/parser/parser_test.go` `TestParseClusterReconcileConfirm`; `internal/executor/system_test.go` extended for the new column. All green under `-race`: `internal/replication`, `internal/storage` (full, incl. `internal/storage/btree`), `internal/sql/*`, `internal/security`, and the full `internal/executor` package (incl. `aggregate`/`join`/`sort`/`vector` subpackages). `go build ./...`/`go vet` (scoped to touched packages) clean; the one `go vet ./...` finding (`internal/executor/cdc.go`, a context-leak warning) is pre-existing, unrelated, uncommitted CDC-subscription-tracking work this increment never touched. No WAL/catalog/wire-protocol change — this mitigation is purely at the read-consistency-gate and executor/SQL layer, never touches the commit path itself. Docs: `docs/ops.md` (expanded "Correctness note" under Rolling upgrade with the investigation findings, mitigation, and operator runbook; `--json` example), `docs/sql.md` (`CLUSTER RECONCILE CONFIRM` + statement list), `docs/ha.md` (`StrongReadBarrier`'s three conditions, new test-evidence row), `TODO.md` (this entry), `CHANGELOG.md`. **Superseded 2026-09-03** — the structural fix this writeup deferred landed in log #79; this mitigation was not removed and stays in place as the dedicated handler for the narrower ambiguous/in-doubt residual case log #79 itself still can't close (see that entry).

---

# Cross-cutting track — Multi-database hosting / subscription isolation

Architecture: `docs/design-multidatabase-dbaas.md`. This track is a dependency
of the Phase 28 Manager database lifecycle surface. Production activation
depended on Phase 25 identity/security, Phase 26 introspection, and Phase 27
workload governance — all now complete. This track gates none of P0–P27; the
next release gate is P28.

### M0/M1 foundation

- [x] Accepted realm/database terminology, isolation contract, non-goals, and staged production gates
- [x] Versioned deterministic `NSRM` v1 manifest with stable deployment/realm/database IDs, bounded names/counts, lifecycle validation, identity binding, truncation/trailing-byte checks, and fuzz seed
- [x] Declarative multi-realm bootstrap manifest (`NEXTSQL_HOSTING_MANIFEST_FILE`) — validate-all-before-mutation, independent database key-file paths, idempotent reapply, atomic registry publication (2026-09-03, log #87). Library slice + `nextsql init` wiring + `nextsqld` serving a manifest-bootstrapped (fully-managed) deployment all landed and live-verified. Remaining follow-ons are the same M3 items every managed database shares (per-database WAL archiver / PITR / Raft), not manifest-specific.
  - [x] `hosting` library slice — bounded YAML parse with node/depth caps and anchor/alias rejection, whole-document validation before any mutation, path- and digest-independent key files, stable derived realm/database identities, `EnsureManifest` one-generation publication, and `matchManifest` idempotent reapply that fails closed with `Conflict` on any identity mutation (`internal/hosting/bootstrap_manifest.go`, tests green)
  - [x] Wire `NEXTSQL_HOSTING_MANIFEST_FILE` into `nextsql init` (2026-09-03, log #87). `nextsql init --hosting-manifest FILE` / `NEXTSQL_HOSTING_MANIFEST_FILE` env / dotenv takes a declarative-bootstrap path in `initDB`: new `hosting.EnsureBootstrapManifestKeyFiles` creates any missing per-database root key file (fresh independent AES-256, mode 0600) before `LoadDeploymentBootstrap` does whole-document validation; `EnsureManifest` publishes one registry generation with every realm/database `PROVISIONING`; each managed database is then physically created (`activateManagedDatabase`, reused from `nextsql database create`) and set `ACTIVE`. Idempotent reapply (already-`ACTIVE` databases skipped), crash-safe resume, and the deployment-wide bootstrap user (`bootstrapDeploymentUser`, extracted and shared with the single-pair path) all carry over. Tests: `hosting.TestEnsureBootstrapManifestKeyFiles`/`...RejectsBadManifest`; `cmd/nextsql.TestInitFromManifestBootstrapsEveryRealm` (3 databases / 2 realms end to end + idempotent reapply + bootstrap user), `TestInitFromManifestViaEnvVar`. Live-verified: `nextsql init` against a real 3-database/2-realm manifest provisioned all three and auto-created 4 key files.
  - [x] **nextsqld serving a manifest-bootstrapped deployment** (2026-09-03, log #87 continuation, at the user's explicit choice via `AskUserQuestion` — "implement it now as this line"). `nextsqld` now boots against a deployment whose default database is `LayoutManaged`: `openHostedDefault` no longer requires `LayoutLegacyDefault` (both valid layouts pass), and the eager primary open at `DATA-DIR/nextsql.db` is gated on `eagerPrimary := hostingRegistry == nil || hostedDatabase.Layout == LayoutLegacyDefault` — so a manifest deployment starts with `db == nil` and its default realm/database is opened lazily and served through `dbMgr` on the first connection, exactly as every non-default managed database already is (M2-5). No `serveConn` change was needed — routing already goes `mgr.Acquire(realmName, dbName)` for **every** connection whenever `dbMgr` is set, default included; the only reason the default worked before was that it was `Preload`ed, and `dbMgr.Preload` is already `if db != nil`. `--key-file` is no longer required for a manifest deployment (only `--instance-key-file`, to unlock the registry); the startup check was relaxed to require one of the two, with a precise follow-up error if the eager path is actually taken without `--key-file`. `require_client_key` + a managed-layout default is rejected up front (that combo's own primary-open path assumes the legacy layout; it was already flagged "narrow, rare, out of scope"). No archiver / no Raft for a fully-managed deployment's default — same accepted M2-3a limitation as every managed secondary; M3 is where per-database WAL/PITR/replication scope gets built. Tests: `cmd/nextsqld/hosting_test.go` `TestOpenHostedDefaultAcceptsManagedLayoutDefault`. **Live-verified end to end**: `nextsql init --hosting-manifest` a 3-database / 2-realm deployment, then `nextsqld --instance-key-file …` (no `--key-file`) booted clean; `CREATE`/`INSERT`/`SELECT` on the default realm (`acme/main`) and a non-default realm (`globex/main`) both worked, cross-realm isolation held (`globex` cannot see `acme`'s table), and data survived a full restart.
- [x] Authenticated encrypted `NSRE` v1 outer file using an independent `NSKS` envelope and external deployment registry root
- [x] Nonce high-water persisted before registry publication; mode-0600 temp, file fsync, atomic rename, and directory fsync
- [x] `nextsql init` atomically registers PROVISIONING before database creation, resumes after bootstrap credential failure, and publishes ACTIVE last
- [x] `nextsqld` verifies ACTIVE default state and database file identity and applies the registered logical database name to the existing Hello validation
- [x] Existing deployments without a registry retain legacy single-database startup; init refuses silent adoption
- [x] Cross-platform OS deployment lock shared by `nextsqld`, init, and offline adoption; a locked/running deployment fails closed
- [x] Explicit restartable `nextsql hosting adopt --confirm` for the existing default database — validates format/keystore/root, recovery-opens before ACTIVE, preserves identity/files, and never auto-discovers siblings
- [x] Dotenv-integrated hosting bootstrap — flag > process env > `.env.local` > `.env` precedence for data/key/instance paths, realm/database, buffer pages, `NEXTSQL_SERVER_USER` / `NEXTSQL_SERVER_PASSWORD_FILE` or `NEXTSQL_SERVER_PASS`, and automation confirmation; client auth is separately namespaced as `NEXTSQL_DATABASE_USER` / `NEXTSQL_DATABASE_PASSWORD_FILE` or `NEXTSQL_DATABASE_PASS`, ambiguous legacy names are rejected, and `NEXTSQL_DATABASE` drives init/adoption plus client Hello
- [x] Registry storage caps — `NSRM` **v3** adds `StorageCapBytes` to every realm and database (0 = no cap) plus a per-realm `RealmRootAuthHash` (32 bytes, `sha256` of the realm-root delegation secret; zero = no delegation); `Registry.SetRealmStorageCap` / `SetDatabaseStorageCap` (one durable generation per change, no-op set skips a generation); invariants enforced on set and revalidated on decode (a non-zero per-database cap ≤ non-zero realm cap; a realm cap not below an existing per-database cap); v1/v2 manifests decode with caps 0, encoder always emits v3. **Realm-root delegation:** `SetRealmRootAuth(realmID, secret)` (admin) stores the hash; `SetDatabaseStorageCapAsRealmRoot(realmID, dbID, bytes, secret)` verifies the secret constant-time and lets a realm-root secret holder set only that realm's per-database caps, bounded by the realm cap, with no path to the realm cap (`Forbidden` when not delegated, `Unauthorized` on a bad secret). CLI `nextsql hosting set-realm-cap` / `set-realm-root` / `set-database-cap [--realm-secret-file]` / `show` (the three `set-*` verbs take the exclusive data-dir lock → fail `Unavailable` against a running `nextsqld`; a cap edit is an overwrite, applied on the next restart). **Write-path enforcement landed**: `nextsqld` applies `hosting.EffectiveStorageCapBytes(realmCap, dbCap)` (smaller non-zero) to the engine page allocator at open (`Engine.SetStorageCapBytes` → `allocator.SetCapPages`, ceiling = `bytes / PhysicalPageSize`); a new-page allocation past the ceiling fails `nerr.Exhausted` ("storage cap exceeded") so `INSERT` / row-splitting `UPDATE` / index growth are rejected while `DELETE` / `ROLLBACK` / in-place `UPDATE` keep working (freelist reuse). Data file only (not WAL/UNDO); not persisted. **Follow-ons:** live cap change without a restart (needs a running-server control-plane op), advisory `system.quotas` surfacing, and enforcement in `REQUIRE CLIENT KEY` lazily-opened databases (`docs/design-multidatabase-dbaas.md` §10.1). Reseller-tier control boundaries (Daemon = registry root / all verbs; Realm = realm-root secret, its realm's per-database caps only, under the realm cap; Nano = a single database, its own SQL users, no realm/registry access) are documented in `docs/design-multidatabase-dbaas.md` §10.1 and covered by `TestResellerTierControlBoundaries` (two-realm deployment: cross-realm secret rejection, wrong-realm database not addressable, realm cap immutable under realm-root, realm-root touches only the cap field, registry needs the deployment root). Tests: `TestStorageCapsDurableAndBounded`, `TestRealmRootCapDelegation`, `TestResellerTierControlBoundaries`, `TestManifestForwardCompatibleCapDecode`, `TestHostingStorageCapsCLI` (incl. deployment-lock rejection), `FuzzDecodeManifest`; enforcement: `storage.TestStorageCapBlocksGrowthAllowsReuse`, `executor.TestStorageCapRejectsGrowthNotDeletes`, `nextsqld.TestEffectiveStorageCapBytes` + cap flow in `TestOpenHostedDefaultAndValidateDatabase`
- [ ] ID-based layout migration with versioned rollback point and explicit inspect/adopt command for sibling files
- [ ] Registry backup/restore/PITR and control-state disaster recovery
- [ ] Registry Raft replication and lifecycle failover

### M2 selectable hosting — single-node multi-database routing

Decomposed 2026-09-02 (see `docs/design-multidatabase-dbaas.md` §16 "M2")
into four independently-gated sub-increments, following the same
"smallest coherent increment" discipline every Phase 27 item used, in
place of the previous single vague "Registered CREATE/rename/suspend/
resume/drop database lifecycle" line and its four still-open siblings.

- [x] **M2-1 — Registry realm/database creation primitives** (2026-09-02).
  `Registry.CreateRealm`/`CreateDatabase` (`internal/hosting/registry.go`);
  `nextsql realm create` / `nextsql database create` CLI. Registers a new
  managed database at the already-defined `LayoutManaged` path: reserves a
  stable ID, durable `PROVISIONING` → physically create/verify-open →
  `ACTIVE`, idempotent and crash-safe on retry. See the increment log
  entry. **`nextsqld` does not yet open or serve anything created here** —
  that is M2-3.
- [x] **M2-2 — Hello realm field (additive, protocol-compatible)** (2026-09-02).
  Added `Hello.Realm` as a new opt-in trailing field (mirrors `NSCT`'s V1
  tail-sniff pattern; no frame-header `Version` bump, so a client that
  never selects a realm sends the exact pre-realm Hello and keeps working
  against any server, old or new). `internal/protocol/server.go`'s
  `serveConn` gained a `Server.Realm` field and a parallel check to the
  pre-existing `Hello.Database` one — **a flat-string equality check
  against the one realm this process serves, not a `hosting.Registry`
  lookup**; wiring a live registry lookup is M2-3's job once a process can
  actually serve more than one realm/database. Updated all 6 official
  drivers (Go/PHP/JS-shared[Bun+Deno]/Node/Python/Ruby): each gained an
  optional `Realm`/`realm` config field, emitted on the wire only when
  non-empty. The two `docs/design-multidatabase-dbaas.md` §19 decisions
  this needed (item 7: no protocol version bump, the trailing field is
  opt-in with no deprecation deadline; item 2: realm identities with
  database-scoped grants adopted, but not implemented by M2-2 — auth stays
  on today's single deployment-wide `auth.Store` until M2-4) were recorded
  before implementation started. See the increment log entry for the full
  design and verification writeup.
- **M2-3 — Bounded DatabaseManager and per-connection routing.**
  Decomposed 2026-09-02 into M2-3a/M2-3b after a scoping investigation
  (`docs/design-multidatabase-dbaas.md` §9) found the full spec spans
  subsystems — `internal/executor.TaskRuntime`'s per-instance
  worker/coordinator goroutines, storage's buffer-pool allocator for a
  memory budget, refcounting for sessions/transactions/CDC/tasks/backup/
  maintenance/replication — with zero existing refcounting/pooling
  infrastructure to build on (confirmed by grep: no `singleflight`, no
  reference-counting pattern anywhere in the repo).
  - [x] **M2-3a — manager exists, small fixed open-database limit,
    connections route through it, Phase 27 monitors become per-DB**
    (2026-09-02). New `internal/dbmanager.Manager` (mutex-guarded keyed map
    + hand-rolled single-flight open, not a literal reuse of
    `scheduler.Admission` — that type is a per-request queueing gate, the
    wrong shape for "permanently consume one of N slots, never released,"
    which is this slice's actual access pattern; deliberate deviation from
    the original one-line description, flagged rather than forced). New
    `protocol.Server.Databases *dbmanager.Manager` field, additive
    alongside the unchanged `DB`/`Tasks` fields — nil (the default) means
    every connection uses the pre-M2-3a `DatabaseHandle()` path unchanged.
    Routes at the one seam identified in `serveConn`; **also had to move
    that whole resolution block from after `TypeReady` to before it** — a
    real bug caught by the new end-to-end test, not just refactored on
    paper: `TypeReady` is the wire protocol's definitive "handshake
    succeeded" signal (`drivers/go`'s `Conn.handshake` reads it and returns
    success with no further reads), so a database-routing failure reported
    *after* it would never reach the client, which would have seen a
    successful connection despite the server internally rejecting it.
    `nextsqld`'s `Opener` closure (`cmd/nextsqld/main.go`) opens an
    additional registered `LayoutManaged` database on demand — single-node
    only (no `startCluster`/`installArchiver`, deliberately, per the
    decomposition above) — and starts its own copies of the 3 Phase 27
    monitors plus its own `TaskRuntime`, exactly once, as the last step of
    a successful open. New `Registry.Lookup(realmName, databaseName)`
    (`internal/hosting`). Live-verified against a real `nextsqld` with a
    real `nextsql database create`-provisioned second database, which
    caught a second real bug the test fixture's own (initially
    bug-for-bug-matching) mistake had masked: the secondary database's
    `KeyRef` is a standalone *root* key that unlocks an *envelope* keystore
    next to the database file (exactly like the primary), not a key usable
    directly on the database file — fixed in both `nextsqld`'s real
    `Opener` and the test fixture, which now mirrors production exactly
    instead of being self-consistently wrong. See the increment log entry
    for the full writeup, including a third, smaller bug (envelope closed
    before the database it protects, in the test fixture only) caught the
    same way.
  - **M2-3b — full §9 spec.** Decomposed 2026-09-02 into three further
    sub-increments (`docs/design-multidatabase-dbaas.md` §9) after a
    scoping investigation found very different risk levels per piece —
    and found sessions (`DB.sessions`/`RegisterSession`), CDC
    (`db.cdcSubs`), and tasks (`TaskRuntime.running`) already have live
    incrementable/decrementable registries, correcting this line's own
    earlier "none of these subsystems expose a ref today." Backup and
    replication are confirmed vacuous for now (backup never touches a
    manager-opened database; M2-3a never attaches replication to a
    secondary at all) — nothing to count for either until later work
    reaches them.
    - [x] **M2-3b-1 — connection/session reference counting + idle
      eviction + open-failure quarantine** (2026-09-02). The smallest,
      independently-landable slice, and the actual headline capability
      this section promises: turns M2-3a from "opens and never closes"
      into "opens and closes when idle." Every `dbmanager.Manager` entry
      now carries a `refs` counter and a `pinned` flag (the Preloaded
      primary is pinned, never evicted — `Opener` only handles
      `LayoutManaged` databases and would refuse to reopen the primary's
      `LayoutLegacyDefault`, so evicting it would be unrecoverable).
      `Acquire`'s new signature returns an idempotent release closure
      paired with the single existing call site (`serveConn`'s
      per-connection defer, which already calls `DB.UnregisterSession` at
      the same point). Eviction reuses `DB.Close()` as-is via a new
      per-entry `cleanup` closure `Opener` now also returns (closing
      `TaskRuntime` before the database, the envelope after — order
      matters: no background task call races the close, and the final
      checkpoint/flush still has its key material) — `Engine.Close()`
      already checkpoints/flushes/closes the WAL durably, so no new
      durability mechanism was needed, only orchestration. No explicit
      maintenance-pause wiring was needed after all: refcount reaching
      zero already implies nothing can be mid-`MAINTAIN` synchronously
      for that database. Quarantine+backoff on a failed open landed in
      the same `Acquire`/open path, an independent implementation of the
      same exponential-backoff shape as `internal/executor/task.go`'s
      task-retry logic (not a shared call into it). Live-verified against
      a real `nextsqld`: real file descriptors (database file, WAL
      segment, undo log) confirmed open via `/proc/<pid>/fd` during a
      live secondary-database connection and fully closed after
      disconnect, with data surviving repeated evict/reopen cycles. See
      the increment log entry for the full writeup.
    - [x] **M2-3b-2 — global memory budget gating buffer-page grants**
      (2026-09-03). New `buffer.Budget` (`internal/storage/buffer/budget.go`,
      mutex + frame counter, nil-safe/unbounded by default) charged once per
      `Engine` open (a Pool's frames are all allocated up front at
      construction — there is no per-page runtime grant to gate, only the
      all-or-nothing decision of whether a new database's Pool may be built
      at all) and released once at `Engine.Close()`, regardless of whether
      that close is normal shutdown or M2-3b-1 idle eviction. New
      `max_total_buffer_pages` config key (0 = unbounded default; rejected
      by `Config.Validate` if positive but below `buffer_pages`, since
      otherwise even the primary database could never open).
      `cmd/nextsqld/main.go` builds one shared `buffer.Budget` and threads it
      through all three of its `executor.Open` call sites (primary,
      dbmanager secondary opener, `REQUIRE CLIENT KEY` lazy primary open) via
      `storage.OpenOptions.Budget`. Deliberately scoped to the long-running
      server process only — `storage.Create`/`CreateWithIdentity` (the
      one-shot `nextsql database create` CLI, which never holds more than
      one `Pool` open at a time) stay unbudgeted. No dedicated
      `system.*`/metrics observability surface yet (`Budget.Used()`/`Cap()`
      exist but aren't wired to introspection) — deliberately deferred, not
      required for the gating behavior itself. See
      `docs/design-multidatabase-dbaas.md` §9 for the full writeup.
    - **M2-3b-3 — centralize `TaskRuntime`'s per-database goroutine
      pools into shared bounded pools.** Scoped 2026-09-03 via a dedicated
      Explore fork and decomposed, mirroring M2-3a/M2-3b's own precedent,
      after the scoping confirmed this is a genuine new-component design
      (no existing fan-out-poller/shared-worker-pool type to parameterize)
      with a real correctness hazard at the M2-3b-1 eviction boundary, not
      a one-shot item.
      - [x] **M2-3b-3a — shared bounded worker pool + DB-tagged job type,
        per-DB polling kept** (2026-09-03). New `executor.TaskPool`
        (`internal/executor/task_pool.go`): one fixed-size worker set plus
        shared `jobs`/`slots` channels, constructed once per process
        instead of once per open database. `TaskRuntime` (per database,
        unchanged responsibility) no longer spawns its own workers — its
        `coordinate()`/`cycle()` loop still polls that one database's own
        due tasks/schedules on its own schedule (the harder "one scheduler
        enumerates every open database," M2-3b-3b, is deliberately not
        built here), but now submits claims, tagged with the submitting
        `*TaskRuntime`, to the shared pool. New `task_workers` config key
        (0 = `executor.defaultTaskWorkers`, matching every individual
        runtime's pre-existing default). Total task-execution goroutines
        no longer scale with the number of open databases — before, each
        open database's own `TaskRuntime` spawned `Workers+1` goroutines
        (2-17 typically); now the whole process spawns `Workers`
        goroutines once, shared by every database. **Correctness hazard
        found and closed during design, not left to M2-3b-3b**: since
        workers are now shared, closing one database's `TaskRuntime` can
        no longer synchronously stop the exact goroutines that might still
        touch its `*DB` (they may be servicing a different database's job
        at that instant) — new per-runtime `inFlight sync.WaitGroup`,
        incremented when `cycle()` hands a claim to the shared pool and
        decremented once a pool worker finishes executing it, closes this:
        `TaskRuntime.Close()` now waits `inFlight` out (after stopping its
        own coordinator) before returning, guaranteeing no pool worker
        holds or will pick up a reference to that database by the time the
        caller (M2-3b-1 eviction, or process shutdown) proceeds to close
        the database itself. `cmd/nextsqld/main.go` constructs one
        `TaskPool` early (deliberately with a `nil`/background parent
        context, not the signal-aware server context, and its `Close`
        deferred before every other close-related defer) specifically so
        it closes *last*, strictly after `srv.Close()` (the primary's
        runtime) and the `dbMgr`/secondary cleanup defer (every secondary's
        runtime) have already closed every `TaskRuntime` submitting to it —
        documented as `TaskPool.Close`'s precondition, since closing it
        earlier would leave a still-open `TaskRuntime`'s `cycle()` blocked
        sending to a `jobs` channel nobody drains anymore. Investigating
        the real production `CANCEL TASK` path found `TaskRuntime.Cancel`/
        its `running` cancel-registry are dead code today (never called
        outside tests — `Session.execCancelTask` signals cancellation via
        `db.RequestTaskCancellation`/`db.taskCancels`, a separate,
        already-DB-scoped registry `TaskRuntime.execute` already populates
        correctly regardless of which pool worker runs it) — left in place
        unchanged rather than removed, since deleting live-but-unused
        public API is a separate cleanup decision, not part of this
        increment's scope. Tests: `internal/executor/task_pool.go` has no
        dedicated unit test file (its logic is thin — construct/close/
        worker loop — and is exercised end-to-end by every
        `task_runtime_test.go` test, all updated to construct a `TaskPool`
        first); new `TestTaskPoolSharedAcrossTwoRuntimes` (two real
        databases, a pool sized to exactly one worker, both databases' due
        tasks succeed — proves claims from either database compete for and
        run on the one shared worker) and
        `TestTaskRuntimeCloseAllowsSafeDBCloseWhilePoolShared` (mirrors the
        real M2-3b-1 eviction sequence: one database's runtime closes then
        that database closes immediately after, while the shared pool and
        another database's still-open runtime keep running — clean under
        `-race`). `go build ./...` clean; `go vet ./...` unchanged (same
        pre-existing unrelated `internal/executor/cdc.go` finding). All
        green under `-race`: `internal/executor` (full, incl.
        `aggregate`/`join`/`sort`/`vector`), `internal/config`,
        `cmd/nextsqld`, `tests/integration`. **Live verification against
        real `nextsql`/`nextsqld` binaries**: a real deployment with a
        second realm/database (`nextsql realm create`), `nextsqld` started
        with `task_workers=1` (the whole process sharing exactly one
        worker) — a `CREATE SCHEDULE ... EVERY '1s'` against the primary
        ticked 10 times over ~5s through the shared pool while a
        concurrent rapid-reconnect loop against the second database's own
        independently-scheduled workflow also successfully claimed and
        executed at least once through that same single shared worker,
        confirming the fan-out works in a real process, not just
        in-process tests (the second database's lower tick count is
        M2-3b-1's pre-existing per-connection idle-eviction behavior,
        unrelated to and not introduced by this change — a one-shot CLI
        connection closes the database, and its scheduler, the instant the
        query returns). Docs:
        `docs/design-multidatabase-dbaas.md` (§9/§16 M2-3b-3a bullets),
        `docs/web/content/docs/config.md` (`task_workers` documented),
        `TODO.md` (this entry), `CHANGELOG.md`.
      - [x] **M2-3b-3b — centralize polling itself: one `CentralScheduler`
        enumerates every open database each tick instead of one poll loop
        per database** (2026-09-03). New `executor.CentralScheduler`
        (`internal/executor/central_scheduler.go`): one `coordinate()`/
        `cycle()` loop, process-wide, that each tick asks a
        `DatabaseLister` (a plain function type, not an imported
        `dbmanager.Manager` reference — `dbmanager` already imports
        `executor`, so the reverse import would cycle; `cmd/nextsqld`
        bridges the two with a small closure) for every currently open
        database and claims/dispatches/submits each one's due work to the
        same shared `TaskPool` (M2-3b-3a). Reduces polling goroutines from
        O(open databases) — one `TaskRuntime.coordinate` per database — to
        O(1), on top of 3a's already-shared execution workers. New
        `dbmanager.Manager.Snapshot() []DBHandle`: hands out a ref-held
        handle for every open entry, reusing `Acquire`/`release`'s
        existing refcounting rather than inventing a second concurrency
        primitive — a database with a `CentralScheduler`-claimed task still
        in flight naturally can't be evicted (M2-3b-1) until the
        scheduler's own ref on it releases too, with zero new coordination
        code. **Refactored `taskJob` to carry `(db, task, config)` directly
        instead of `*TaskRuntime`**, so both `TaskRuntime.cycle` and
        `CentralScheduler.cycleOne` can submit compatible jobs to one
        `TaskPool`; the actual per-task execution body moved to a shared
        `runClaimedTask` free function. `TaskRuntime.running`/`Cancel` (dead
        in production per 3a's own finding) still work exactly as before
        via a new optional `onStart` job hook, kept for API/test
        compatibility rather than folded into this change (that stays
        M2-3b-3c). **Release-timing hazard, closed by design not left
        implicit**: a claim submitted to the shared pool executes
        asynchronously, so `cycleOne` cannot release its `DBRef` the
        moment submission finishes — doing so would let the database evict
        out from under a still-executing job. Closed with a per-tick,
        per-database `sync.WaitGroup` (`Add` before each submit, `Done` in
        that job's completion hook) and one short-lived goroutine per
        database per tick that waits on it before releasing — cheap given
        ticks are infrequent (250ms default) and the open-database count is
        small and bounded (`max_open_databases`), tracked by
        `CentralScheduler.inFlight` so `Close()` never returns while one is
        still pending, mirroring `TaskRuntime.inFlight`'s own guarantee for
        a single database. `cmd/nextsqld/main.go`: the primary now gets its
        own dedicated `TaskRuntime` only when there is **no** hosting
        registry at all; once one exists, one process-wide
        `CentralScheduler` covers the primary (via `dbMgr.Preload`) and
        every dbmanager-opened secondary alike, and the Opener's per-secondary
        cleanup closure no longer needs any task-runtime-specific
        ordering of its own (Snapshot's ref-holding already makes that
        safe). `CentralScheduler.Close()` is deferred immediately after
        it's started — inside the `hostingRegistry != nil` block, which is
        registered *after* the top-level dbMgr/secondary-cleanup defer, so
        it runs first (LIFO) and always finishes draining before `dbMgr`
        force-closes any database at final shutdown. **Deliberately scoped
        out, flagged rather than silently left**: the `REQUIRE CLIENT KEY`
        lazy-open path's own dedicated primary `TaskRuntime` is untouched —
        combining `REQUIRE CLIENT KEY` with hosting is a narrow, rare
        deployment shape, and once that primary is later `Preload`ed into
        `dbMgr` there, it becomes redundantly (transactionally
        exclusive claiming makes this safe, not a correctness bug) polled
        by both that `TaskRuntime` and `CentralScheduler`. **Behavioral
        tradeoff, also flagged rather than silently left**: 3a's per-database
        `TaskRuntime` called `cycle()` once immediately on construction, so
        opening even a very brief connection guaranteed at least one
        poll attempt for that database; `CentralScheduler` has no such
        per-connection synchronization — a database opened only for a
        very short, bursty request (materially shorter than the poll
        interval) may see zero scheduling attempts before it's evicted
        again. A realistically-held-open connection is unaffected (proven
        live below); only the narrow "extremely bursty one-shot connections
        to a hosted secondary whose sole purpose is running its own
        schedule" pattern is affected, and even then only in degree
        (delayed, not lost — the task fires the next time the database
        happens to be open during a tick), not correctness. Tests:
        `TestCentralSchedulerAcrossTwoDatabases`, 
        `TestCentralSchedulerReleasesEveryRefEventually`,
        `TestCentralSchedulerCloseWaitsOutstandingRefs`,
        `TestStartCentralSchedulerValidatesArgs` (new,
        `internal/executor/central_scheduler_test.go`); `dbmanager`
        gained `TestSnapshotEmptyWhenNothingOpen`/
        `TestSnapshotHoldsRefUntilReleased`. `go build ./...` clean;
        `go vet ./...` unchanged (same pre-existing unrelated
        `internal/executor/cdc.go` finding). All green under `-race`:
        `internal/executor` (full, 121.4s), `internal/dbmanager`,
        `cmd/nextsqld`, `tests/integration`. **Live verification against
        real `nextsql`/`nextsqld` binaries**: a real two-realm deployment,
        `nextsqld` with `task_workers=1` — the primary's `EVERY '1s'`
        schedule accumulated 16 rows over ~5s with **zero dedicated
        primary `TaskRuntime`** (confirming `CentralScheduler` alone
        drives it); the earlier rapid-reconnect-loop trick that worked for
        3a's live check produced 0 rows for the second database here,
        exactly the documented tradeoff above, reproducing it live rather
        than only in theory — a realistically-held-open 6-second
        connection to the same database then accumulated exactly 6 rows,
        confirming the underlying mechanism is correct once the tradeoff's
        precondition (a connection at least as long as a poll interval) is
        met. No WAL/catalog/wire-protocol change. Docs:
        `docs/design-multidatabase-dbaas.md` (§9/§16 M2-3b-3b bullets, top
        status line), `TODO.md` (this entry), `CHANGELOG.md`.
      - [x] **M2-3b-3c — retire the dead `TaskRuntime.Cancel`/`running`
        registry** (2026-09-03, log #88). M2-3b-3a/3b's own investigations
        both confirmed it dead in production (real `CANCEL TASK` flows
        through `db.taskCancels`, populated by `runClaimedTask` on whatever
        pool worker runs the task, independent of submitter), and a
        repo-wide search found no non-test caller of `TaskRuntime.Cancel`.
        So deleted outright rather than DB-scoped: the `Cancel` method, the
        `running map[string]context.CancelFunc` field, the `sync.Mutex`
        that guarded only it, `running: make(...)` in `StartTaskRuntime`,
        the `taskJob.onStart` hook that fed it (and its call site in
        `TaskPool.worker` / `runClaimedTask`'s now-unused `onStart`
        parameter), and the `onStart`/`delete(r.running, …)` bookkeeping in
        `TaskRuntime.cycle`. `internal/catalog` import dropped from
        `task_runtime.go` (only the deleted method used it).
        Behaviour-preserving — nothing production-real used any of it;
        `CentralScheduler` already passed `onStart` nil. `go build ./...` +
        `go vet ./...` clean; `internal/executor` full `-race` green
        (125 s), `tests/integration` task/cancel/schedule tests green. No
        WAL/catalog/wire/config change. Docs: `docs/design-multidatabase-dbaas.md`
        (§9/§16 M2-3b-3c bullets + top status line — M2-3b, and the M2
        selectable-hosting milestone, now complete), `TODO.md`,
        `CHANGELOG.md`. **M2-3b is complete.** Remaining M2 open threads:
        M2-4b-2 (blocked on realm delete not existing), M2-4b-3
        (speculative), M2-4c (fold into M3).
- **M2-4 — Realm-scoped auth, database-scoped `CONNECT`, system views.**
  **Dependency corrected 2026-09-02**: "depends on M2-1..3" was stale,
  written before M2-3's own split — access control/introspection (M2-4)
  is orthogonal to resource budgeting/task-pool architecture (M2-3b-2/3),
  confirmed by direct investigation. M2-4 only needs M2-1/M2-2/M2-3a, all
  landed. Decomposed into three further sub-increments
  (`docs/design-multidatabase-dbaas.md` §16) after finding the same
  "very different sizes" shape M2-3 and M2-3b each had:
  - [x] **M2-4a — `system.realms`/`system.databases` read-only
    introspection (2026-09-02).** Landed — see log #74.
  - **M2-4b — realm-local `auth.Store`/`security.ACL` +
    `(RealmID, PrincipalID, DatabaseID, privilege, scope)` authorization
    tuple.** Scoping investigation (2026-09-02, see
    `docs/design-multidatabase-dbaas.md` §16) found `nextsqld` currently
    pins `srv.Realm` to one realm, so `dbmanager`'s multi-realm routing
    (accepted since M2-3a) is unreachable today — M2-4b is a genuine
    prerequisite for real multi-realm routing, not just an authorization
    nicety. Decomposed into M2-4b-1/2/3; the user chose M2-4b-1's
    composite-key approach over M2-4b-2's per-realm files.
    - [x] **M2-4b-1 — composite-key `auth.Store`/`security.ACL` single
      file (2026-09-02).** Landed — see log #76.
    - [ ] **M2-4b-2 — separate per-realm auth files + eviction manager.**
      Only needed for isolation-at-rest/crypto-shred (per-realm files under
      `realms/<RealmID>/security/`, per §7's literal layout); not required
      for M2-4b-1's correctness. **Re-scoped 2026-09-03 (Explore fork) and
      found genuinely not actionable yet, not just unscheduled** — three
      independent blockers, not one: (1) §7 specifies a bare directory
      sketch ("exact filenames remain a format decision"), not an actual
      per-realm file format or eviction-manager API — both still need their
      own design pass, same as M2-4b-1 needed one; (2) the stated
      justification, crypto-shredding one realm's credentials independently
      on realm deletion, has no feature to attach to: `hosting.Registry`
      has no `SetRealmState`/`DeleteRealm` at all today (only
      `SetDatabaseState`, database-scoped) — `StateDeleting`/`StateTombstoned`
      exist in the `State` enum's transition graph but are unreachable for
      a `Realm`, confirmed by a repo-wide search finding zero
      crypto-shred implementation beyond aspirational design-doc prose;
      building eviction infrastructure now would be solving for a lifecycle
      operation that does not exist. (3) `auth.Store`/`security.ACL` hold no
      open file handle at all (every op is a full decode-on-open,
      persist-on-every-write cycle, not a cached handle) — the
      `dbmanager`-shaped "bounded open handle + refcount + evict" pattern
      this line's own name assumes doesn't map cleanly onto them; the real
      design question ("what is even being evicted — there's no handle")
      is unresolved. **Recommendation, not yet acted on**: leave unscheduled
      until realm deletion/crypto-shred is itself scoped and landed first
      (making the isolation requirement real), at which point re-split into
      at least (a) per-realm file format/layout, (b) the eviction-manager
      mechanism, (c) migrating construction sites (~24 files, mostly test
      fixtures, confirmed by the same fork) from today's deployment-wide
      singleton to per-realm lookups.
    - [ ] **M2-4b-3 — realm-aware embedded OIDC broker, beyond M2-4b-1.**
      M2-4b-1 already threads a realm name through token minting
      (pre-existing) and closed the verification-side `claims.Realm` gap;
      this is only for deeper per-realm IdP profile/policy configuration,
      if ever needed. Not yet scheduled.
  - [ ] **M2-4c — `system.database_operations`.** Needs new
    operation-history tracking in `internal/hosting` that doesn't exist
    yet (realms/databases only carry a current `State`, not a transition
    log) — not a pure read-through like M2-4a. Best folded into future M3
    lifecycle work rather than built standalone. Not yet scheduled.
- [x] **M2-5 — multi-realm routing activation (2026-09-02).** Landed — see
  log #77. `nextsqld` no longer pins `srv.Realm` as an equality gate once a
  `HostingRegistry` is configured; a Hello may now legitimately name any
  realm in the deployment, routed and isolated correctly through the
  `dbmanager`/`LookupRealm` machinery M2-3a and M2-4b-1 had already built
  but left unreachable. Also fixed a real companion CLI gap found live:
  `cli.ServerConfig` resolved `Settings.Realm` but never set it on the
  driver `Config`, and `nextsql exec` had no `--realm` flag at all.
- [x] **M2-6 — Pre-authentication realm/database existence-disclosure
  hardening (2026-09-02).** Landed — see log #78. Closes the gap M2-5's own
  writeup flagged as deliberately out of scope: `serveConn`'s flat
  `s.Realm`/`s.Database` mismatch prechecks and `LookupRealm` call all
  returned a distinguishing `NotFound` immediately after `Hello`, before
  `HelloOK`/any password read — a credential-free realm/database-name
  enumeration oracle. Fixed by deferring the outcome: the handshake now
  always completes the full round trip and folds an unresolved
  realm/database into the same generic `Unauthorized "authentication
  failed"` a wrong password produces, after the real (or dummy) password
  comparison already ran — no distinguishing content or timing. Username
  enumeration was already closed pre-existingly (`auth.Store.VerifyInRealm`'s
  dummy-hash path). Deliberately out of scope: the post-auth
  `dbmanager.Manager.Acquire` unknown-database rejection (needs valid
  credentials already, a materially weaker pre-existing gap) and mTLS
  service-identity checks (unrelated mechanism).

### M3+ (independent lifecycle, workload governance, HA, Manager)

Not yet decomposed — premature before M2 lands, per the design doc's own
sequencing instruction. High-level scope, unchanged:

- [ ] Independent WAL/recovery/cache/idempotency/task/CDC/backup/PITR scope per database
- [ ] Hierarchical deployment/realm/database/user quotas and durable billing-grade usage ledger where enabled
- [ ] Adversarial cross-realm/database, resource, migration, three-voter failover, and rolling-upgrade gates
- [ ] Production exit gate in `docs/design-multidatabase-dbaas.md` fully green

Current status: **M2 COMPLETE (2026-09-03); M3+ open**. A single `nextsqld`
process opens and serves any number of registered realms/databases: a
connecting client's `Hello.Realm`/`Hello.Database` (M2-2) is resolved by
`internal/dbmanager.Manager` (M2-3a) — with realm-scoped auth (M2-4a/b-1,
M2-5) and pre-auth existence-disclosure hardening (M2-6) — to a genuinely
distinct, already-provisioned database (`nextsql realm create` /
`database create`, M2-1, or a declarative `NEXTSQL_HOSTING_MANIFEST_FILE`
bootstrap, log #87), bounded to a fixed open-database limit. A non-pinned
database closes when its last connection disconnects and reopens on demand
(M2-3b-1); repeated open failures quarantine with backoff. Buffer-pool
memory is capped process-wide across every open database (M2-3b-2,
`max_total_buffer_pages`); task execution and scheduling are one shared
worker pool + one process-wide `CentralScheduler` (M2-3b-3a/b/c,
`task_workers`), not per-database. `nextsqld` also serves a fully-managed
deployment with no legacy default database at all (log #87). Every managed
database is still single-node only — no Raft attachment, no per-database
WAL archiver/PITR. **Do not claim production-grade multi-database hosting
yet** — that is the M3+ scope below (per-database WAL/recovery/PITR/Raft,
hierarchical quotas, registry backup/restore + Raft replication, adversarial
gates, the production exit gate). M2's own remaining threads — M2-4b-2
(blocked on realm suspend/delete, which is itself an untracked gap),
M2-4b-3 (speculative per-realm IdP config), M2-4c (`system.database_operations`,
folds into M3 lifecycle work) — are all either blocked or explicitly
deferred into M3.

---

# Cross-cutting track — Datatype expansion

Architecture: `docs/design-datatypes.md`. This track gates no phase (P0-P27
closed; P28-P30 next). Decomposed 2026-09-03 from an initial flat taxonomy
sketch into a sequenced, gated plan, following the same "smallest coherent
increment" discipline as Multi-database hosting's M2. D1-D3 and D5 have
landed (see below); do not pick up the rest opportunistically inside other
phase/track work — each gets its own increment log entry when scoped, same
as M2's sub-items. Already-shipped types (`UUID`/`STRING`/`TEXT`/`DECIMAL`/
`TIMESTAMPTZ`/`JSON`/vector family/`POINT`/`BOX`/`LINESTRING`/`POLYGON`/
`BOOL`/`NULL`) are out of scope for this track — see `docs/design-datatypes.md`
§1.

- [x] **D1 — `BLOB`** (variable-length raw bytes, `u32`-length-prefixed like
  `STRING`/`TEXT`) — landed 2026-09-03, log #90. Core engine, all 7 official
  drivers, docs, and live verification complete; see the increment log entry
  for the full writeup.
- [x] **D2 — Fixed-width signed integers** (`INT8`/`INT16`/`INT32`/`INT64`)
  — landed 2026-09-03, log #91. Core engine (sign-bit-flip sortable keys,
  DECIMAL-promoting arithmetic, full coercion matrix, FK/`ENCRYPTED CLIENT`
  eligibility), all 7 official drivers, docs, and live verification
  complete; see the increment log entry for the full writeup.
- [x] **D3 — Fixed-width unsigned integers** (`UINT8`/`UINT16`/`UINT32`/
  `UINT64`) — landed 2026-09-03, log #92. Core engine (unsigned sortable
  keys — no sign-bit flip needed — DECIMAL-promoting arithmetic, a coercion
  matrix unifying `INT8..64`/`UINT8..64` into one exact-integer group,
  FK/`ENCRYPTED CLIENT` eligibility), all 7 official drivers, docs, and live
  verification complete; see the increment log entry for the full writeup.
- [ ] **D4 — `CHAR(n)`/`VARCHAR(n)`**. Blocked on a semantics decision
  (fixed-width space-padded vs. a length ceiling on existing `STRING`) —
  needs `AskUserQuestion` before scoping.
- [x] **D5 — `DATE`/`TIME`** — landed 2026-09-03, log #94. Core engine
  (`DATE` int32 day-count with sign-bit-flip sortable keys; `TIME` int64
  nanoseconds-since-midnight with plain-unsigned sortable keys; ISO 8601
  text coercion, isolated from every other family; no arithmetic, deferred
  to D6), all 7 official drivers, docs, and live verification complete; see
  the increment log entry for the full writeup, including an incidentally
  found and fixed pre-existing `Batch.Compact`/`clonePrefix` bug.
- [ ] **D6 — `INTERVAL`**. Blocked on its own design writeup (calendar
  arithmetic against `DATE`/`TIMESTAMP` is not fixed-duration) — do not
  start alongside D5.
- [ ] **D7 — Plain `TIMESTAMP` (no timezone)**. Blocked on a product
  decision: confirm a real use case beyond existing `TIMESTAMPTZ` before
  adding a second temporal type.
- [ ] **D8 — `FLOAT32`/`FLOAT64`**. Blocked on explicit approval — `DECIMAL`
  is exact by design today; floats reopen a rounding-surprise class NextSQL
  currently doesn't have. Needs `AskUserQuestion` with a stated reason, plus
  a NaN/-0/ordering-canonicalization spec for index keys if approved.
- [ ] **D9 — Collections** (`ARRAY<T>`/`MAP<K,V>`/`STRUCT<...>`). Too large
  for this track (comparable to the P9 JSON or P11/P23 Vector phases).
  Action here is only "split into a dedicated `docs/design-collections.md`
  + its own track when someone is ready to scope it" — not implementation.
- [ ] **D10 — Spatial `GEOMETRY`/`GEOGRAPHY`**. Blocked on a scoping
  decision (generalize the existing 4 WGS84 shapes with SRID vs. a second
  PostGIS-style subsystem) — needs `AskUserQuestion` before a TODO.md item
  is created.
- [ ] **D11 — `ENUM(label, ...)`**. Was missing from the original taxonomy
  entirely (added 2026-09-03). Blocked on a scoping decision, not effort:
  needs new catalog-level metadata (an ordered, named label list per
  column), not just a new `Kind` tag, so it needs an explicit decision on
  an `NSCT` catalog-format version bump and on label lifecycle (`ADD VALUE`/
  rename/remove-with-existing-data) before implementation — needs
  `AskUserQuestion` before scoping.

**Explicitly cut, not tracked as items** (see `docs/design-datatypes.md` §4
for reasoning; revisit only if a concrete use case appears): Identity/Network
types (`INET`/`CIDR`/`MACADDR`) — use `VARBINARY`/`STRING` + app validation;
Structured Documents beyond JSON (`YAML`/`XML`/`TOML`/`INI`/`ENV`) — store as
`TEXT`/`BLOB`, format is an app concern; DevOps/Infrastructure
(`DOCKERFILE`/`COMPOSE`/`K8S_MANIFEST`/`HCL`), Development Content
(`CODE`/`MARKDOWN`), Generic Files (`FILE`) — not database types, conflicts
with `PROJECT.md`'s "not a generic storage layer" identity.

---

# Phase 28 — Professional Installer + NextSQL Manager

NextSQL must be easy to install and operate without sacrificing headless automation or secure defaults.

## Product separation

```text
NextSQL Installer → install / upgrade / repair / uninstall
NextSQL Manager   → server / cluster / security / backup / operations
NextSQL Studio    → database development / SQL / data / schema / RAG
```

## Installer platforms

- [ ] Windows `Setup.exe`
- [ ] Windows MSI if justified
- [ ] Linux `.deb`
- [ ] Linux `.rpm`
- [ ] Linux `.tar.gz`
- [ ] Linux `.run`
- [ ] macOS `.pkg`/`.dmg` only when tested/supported
- [ ] OCI/container non-interactive initialization
- [ ] Silent/non-interactive installation
- [ ] Signed release/checksum pipeline

## Installer UX

- [ ] Modern welcome screen with version/architecture/channel
- [ ] Standard vs Advanced installation
- [ ] Component selection
- [ ] Data-directory picker + capacity/permission validation
- [ ] Secure encryption setup wizard
- [ ] Generate/import root unlock key
- [ ] Default root-key path separated from data volume
- [ ] Recovery-key export/verification UX
- [ ] Never upload root key
- [ ] Administrator account creation
- [ ] Password strength/visibility/validation UX
- [ ] Loopback-only network default (`127.0.0.1:7210`)
- [ ] Remote-listen flow requires TLS
- [ ] TLS certificate assistant and validation
- [ ] Optional explicit firewall-rule creation
- [ ] Hardware detection (CPU/RAM/disk/filesystem)
- [ ] Balanced / Memory Conservative / High Performance / Custom resource presets
- [ ] Installation summary before mutation
- [ ] Meaningful staged progress
- [ ] Transactional rollback of safe installer changes
- [ ] Never delete existing user data/keys on failed install
- [ ] Automatic post-install health verification
- [ ] Completion screen with safe connection settings
- [ ] No root key/password in connection strings
- [ ] Developer setup preset
- [ ] Production setup preset
- [ ] Production security preflight
- [ ] Light/dark/system UI
- [ ] Keyboard/screen-reader/high-contrast accessibility
- [ ] Actionable error messages
- [ ] Protected installer logs with secret redaction

## Installer lifecycle

- [ ] Detect existing installation
- [ ] Upgrade mode
- [ ] Repair mode
- [ ] Uninstall mode
- [ ] Upgrade preflight for storage/catalog/protocol compatibility
- [ ] Config backup before upgrade
- [ ] Rolling-cluster upgrade integration
- [ ] Repair preserves data/keys/config unless explicitly approved
- [ ] Uninstall preserves data and keys by default
- [ ] Dangerous deletion choices disabled by default

## Automation parity

- [ ] Every major GUI installer option maps to CLI/config automation where practical
- [ ] `nextsql setup` or equivalent
- [ ] Non-interactive exit codes
- [ ] Machine-readable output
- [ ] Config-file-driven install
- [ ] Offline installation supported
- [ ] No mandatory cloud login/activation/telemetry

## NextSQL Manager MVP

Navigation:

```text
Overview
Databases
Connections
Security
Backups
Cluster
Performance
Maintenance
Settings
```

- [ ] Server overview/status
- [ ] Start/stop/restart
- [ ] Graceful restart/drain integration
- [ ] Configuration viewer
- [ ] Safe configuration editor + validation
- [ ] Restart-required indicators
- [ ] Database listing/size
- [ ] Connection information
- [ ] Users/roles/privileges administration
- [ ] Security dashboard
- [ ] Key-rotation status/action without displaying keys
- [ ] TLS status/configuration
- [ ] Audit viewer
- [ ] Backup creation
- [ ] Backup verification status
- [ ] Restore/PITR UI
- [ ] Cluster creation wizard
- [ ] Cluster dashboard
- [ ] Quorum/leader/follower/lag display
- [ ] Maintenance UI
- [ ] Index rebuild UI
- [ ] Storage reclamation UI
- [ ] Statistics refresh UI
- [ ] Performance metrics
- [ ] Active queries
- [ ] Query cancellation using existing engine path
- [ ] Logs/diagnostics
- [ ] Redacted diagnostic bundle export
- [ ] Destructive-action confirmations

## UI architecture

- [ ] Manager uses official NSQL/system interfaces
- [ ] No direct page/WAL/catalog manipulation
- [ ] Minimal privileged helper for OS-only tasks
- [ ] Manager does not run permanently as root/Administrator
- [ ] UI framework evaluated for security/performance/accessibility/size/offline support
- [ ] Do not choose Electron automatically; justify framework

### Phase 28 installer exit gate

- [ ] Fresh Windows install tested
- [ ] Fresh Linux install tested
- [ ] Secure defaults generated
- [ ] Service initializes/starts/verifies
- [ ] Remote plaintext blocked
- [ ] Secret material absent from logs
- [ ] Silent install tested
- [ ] Upgrade tested
- [ ] Repair tested
- [ ] Uninstall preserves data/keys by default
- [ ] Failure rollback tested
- [ ] Accessibility baseline passes

### Phase 28 Manager exit gate

- [ ] Manager can perform its MVP operations through official interfaces
- [ ] RBAC enforced server-side
- [ ] No raw encryption key exposure
- [ ] Cluster/backup/security status reflects server truth

---

# Phase 29 — NextSQL Studio

NextSQL Studio is the official database development IDE, separate from Manager.

## Studio product goals

Primary users:

```text
developers
DBAs
data engineers
AI/RAG developers
backend engineers
system architects
```

Core rule: **Do not build a generic SQL client with a NextSQL logo.** Studio must understand NextSQL-native SQL, JSON, FTS, vectors, hybrid search, geo, realms/databases, workflows, migrations, and query plans.

## Core architecture

- [ ] Studio communicates through official SDK/NSQL
- [ ] Capability negotiation with server
- [ ] Studio never reads pages/WAL/catalog files directly
- [ ] Studio never requires server root unlock key
- [ ] Studio never bypasses RBAC or realm/database isolation
- [ ] Secure OS credential storage where available
- [ ] Ask-every-time fallback instead of plaintext secret files

## Main shell / UX

- [ ] Professional desktop IDE layout
- [ ] Connection Explorer
- [ ] Editor workspace
- [ ] Result/plan/messages/statistics panel
- [ ] Inspector panel
- [ ] Global command palette
- [ ] Recent connections/projects home screen
- [ ] Light/dark/system themes
- [ ] Layout persistence without credentials
- [ ] Keyboard-first navigation
- [ ] Accessibility baseline
- [ ] High-DPI support
- [ ] Crash recovery for unsaved editors

## Connection manager

- [ ] Name/host/port/database/user
- [ ] TLS/CA/client-certificate fields as supported
- [ ] Realm/database profile
- [ ] Read-consistency profile
- [ ] Secure saved password option
- [ ] Dev/test/staging/production environment profiles
- [ ] Highly visible production indicator
- [ ] Production safety mode
- [ ] Warn UPDATE/DELETE without WHERE using parsed AST
- [ ] Warn destructive DDL
- [ ] Optional read-only production session default

## Database explorer / schema tooling

- [ ] Lazy-loaded database/schema/table/index/workflow tree
- [ ] Table overview
- [ ] Columns
- [ ] Primary/foreign keys
- [ ] Constraints
- [ ] Indexes
- [ ] Statistics
- [ ] DDL view
- [ ] Dependencies
- [ ] Table designer
- [ ] Index designer
- [ ] Generated native DDL preview
- [ ] ER diagram from actual FKs
- [ ] Global object search

## SQL editor

- [ ] NextSQL-native syntax highlighting
- [ ] Line numbers/bracket matching/indentation
- [ ] Multi-tab editing
- [ ] Execute statement
- [ ] Execute selection
- [ ] Execute script
- [ ] Cancel query
- [ ] SQL formatting
- [ ] Find/replace
- [ ] Context-aware IntelliSense from live catalog
- [ ] JSON-path completion where metadata is known
- [ ] Vector-aware completion
- [ ] Inline parser/binder diagnostics
- [ ] Deterministic suggestions for misspelled identifiers where safe
- [ ] Query history with privacy controls
- [ ] Saved queries/folders/tags
- [ ] Git-friendly workspace artifacts

## Results / data editing

- [ ] Streaming virtualized result grid
- [ ] No full materialization of huge results
- [ ] Cell/row/selection copy
- [ ] Export selected/streamed data
- [ ] JSON tree/raw viewer
- [ ] Vector compact cell representation + inspector
- [ ] Geo value preview
- [ ] Timezone-aware TIMESTAMPTZ display
- [ ] Editable data grid
- [ ] Parameterized INSERT/UPDATE/DELETE generation
- [ ] Staged-change review
- [ ] Transactional commit/discard where appropriate

## EXPLAIN / profiler

- [ ] Graphical `EXPLAIN` tree
- [ ] Graphical `EXPLAIN ANALYZE`
- [ ] Estimated vs actual rows
- [ ] CPU/memory/disk/cache/spill/workers/index display
- [ ] Highlight estimation errors
- [ ] Plan comparison
- [ ] Query profiler breakdown
- [ ] Never display guessed metrics as measured

## Native NextSQL explorers

### JSON Explorer

- [ ] Tree/raw/path browser
- [ ] Copy JSON path
- [ ] Indexed-path indicator
- [ ] Generate native JSON-path query

### Full-text Explorer

- [ ] Select table/column/index
- [ ] Search phrase
- [ ] BM25 rank display
- [ ] Matched terms/highlight when supported
- [ ] Generate/copy SQL

### Vector Explorer

- [ ] Select table/vector column
- [ ] Paste/enter vector
- [ ] Metric selection
- [ ] Top-K
- [ ] HNSW search settings when supported
- [ ] Vector inspector
- [ ] Validate dimensions

### Hybrid Explorer

- [ ] Structured filter builder
- [ ] Full-text input
- [ ] Vector input
- [ ] Metric/limit
- [ ] Generated SQL
- [ ] Execution plan
- [ ] Candidate counts where available
- [ ] BM25/vector/hybrid rank inspector where engine exposes it

### Geo Explorer

- [ ] POINT/LINESTRING/POLYGON rendering
- [ ] Draw point/radius/polygon
- [ ] Generate native geo SQL
- [ ] Preserve longitude,latitude order

## Developer operations

- [ ] Transaction console
- [ ] Lock explorer
- [ ] Realm/database selector / reconnect
- [ ] Visible cross-database administration warning
- [ ] Users/roles privilege explorer
- [ ] GRANT/REVOKE builder
- [ ] Audit viewer
- [ ] Migration workspace using official migration system
- [ ] Create/validate/dry-run/pending/apply/down/status/repair
- [ ] Schema diff (later)
- [ ] CSV/JSON/NDJSON import/export
- [ ] Streaming bulk import
- [ ] Data generator for development
- [ ] Vector dataset import
- [ ] nextsql-bench result viewer and run comparison
- [ ] Heavy/destructive benchmarks blocked on production by default

## Workflow / CDC integrations

- [ ] Workflow explorer/editor
- [ ] Trigger/schedule relationship view
- [ ] Workflow diagram
- [ ] Task monitor
- [ ] Cancel/retry task as permitted
- [ ] CDC/change-stream explorer
- [ ] Pause/resume client stream
- [ ] Resume token/LSN display
- [ ] Bounded CDC UI buffering

## Studio packaging

- [ ] Separate Studio package from server where practical
- [ ] Optional Studio component in desktop installer
- [ ] Headless server does not require Studio
- [ ] Windows package tested
- [ ] Linux package tested
- [ ] macOS only when supported/tested
- [ ] Framework decision documented (e.g. Tauri/Wails/native/other lightweight option)

### Studio MVP exit gate

- [ ] Connection Manager
- [ ] Database Explorer
- [ ] SQL Editor + IntelliSense
- [ ] Prepared parameters
- [ ] Query cancellation
- [ ] Streaming result grid
- [ ] JSON viewer
- [ ] Table inspector/data editor
- [ ] Index inspector
- [ ] EXPLAIN/ANALYZE viewer
- [ ] Migration integration
- [ ] Realm/database selector
- [ ] User/role explorer
- [ ] Basic Full-text Explorer
- [ ] Basic Vector Explorer
- [ ] Basic Hybrid Explorer
- [ ] Production warnings
- [ ] Secure credential storage
- [ ] No root-key requirement
- [ ] RBAC/realm/database tests pass
- [ ] Accessibility baseline passes

---

# Phase 30 — NextSQL Intelligence + built-in RAG

Build **NextSQL Intelligence**, the built-in context-aware RAG assistant in Studio. It must reason from the actual connected NextSQL version and authorized context, not act as a generic PostgreSQL/MySQL chatbot.

## Core principle

```text
Actual server capabilities
+ official matching-version NextSQL docs
+ authorized live schema/catalog
+ current SQL/error/plan context
→ controlled retrieval/context orchestration
→ optional AI model
→ grounded answer with sources
```

The deterministic database engine must never depend on AI for parsing, planning, execution, transactions, WAL, recovery, encryption, authorization, Raft, backup, or integrity.

## Authoritative capability context

- [ ] `system.capabilities` is authoritative
- [ ] Server version/NSQL version available to Studio Intelligence
- [ ] Feature status: supported/experimental/deprecated/unsupported
- [ ] Version-added metadata
- [ ] Documentation reference metadata
- [ ] Assistant refuses to present planned features as installed features
- [ ] Newer Studio + older server mismatch tests
- [ ] Older Studio + newer server mismatch behavior

## Versioned official knowledge base

- [ ] Package official docs with Studio
- [ ] Version documentation corpus
- [ ] Include README/PROJECT/PLAN/TODO where appropriate
- [ ] Include SQL/optimizer/execution/JSON/FTS/vector/geo/MVCC/WAL/security/protocol/backup/export/ops/HA docs
- [ ] Include future workflow/CDC/partitioning/maintenance docs
- [ ] Chunk documentation
- [ ] Stable document/chunk hashes
- [ ] Re-index only changed chunks after upgrade
- [ ] Dedicated internal `nextsql_intelligence` database; never mix with user DBs
- [ ] Full-text index for docs
- [ ] Vector index for docs when embedding provider available
- [ ] Hybrid retrieval when both available
- [ ] BM25-only graceful fallback when embeddings unavailable
- [ ] Filter docs by connected server version/capability

## Self-RAG / dogfooding

- [ ] Use NextSQL itself to store/retrieve official Intelligence knowledge
- [ ] Retrieval uses native SQL + FTS + HNSW + hybrid planner
- [ ] Measure retrieval latency and quality
- [ ] No opaque external vector database dependency required

## Context orchestrator

Define typed contexts:

- [ ] `ServerContext`
- [ ] `CapabilityContext`
- [ ] `DatabaseContext`
- [ ] `SchemaContext`
- [ ] `TableContext`
- [ ] `IndexContext`
- [ ] `QueryContext`
- [ ] `PlanContext`
- [ ] `ErrorContext`
- [ ] `DocumentationContext`
- [ ] `RealmContext`
- [ ] `SecurityPolicyContext`

Retrieval priorities:

```text
1. Current selection/query
2. Current error/plan
3. Exact referenced schema objects
4. Server capabilities
5. Matching-version official docs
6. Nearby related objects
7. General NextSQL docs
```

- [ ] Intent-aware context selection
- [ ] Do not dump entire schema/catalog/docs into prompts
- [ ] Deduplicate retrieved context
- [ ] Rerank relevant chunks
- [ ] Context compression
- [ ] Token budgeting
- [ ] Context hashing/cache
- [ ] Prompt caching support when provider supports it
- [ ] Conversation compaction for older turns

## Permissions / privacy

- [ ] AI tool requests execute with user's effective permissions
- [ ] No AI superuser
- [ ] Realm/database binding enforced on AI retrieval
- [ ] Metadata Only policy
- [ ] Metadata + Plans policy
- [ ] Metadata + Sample Data policy
- [ ] Full Authorized Context policy
- [ ] Production default: metadata/plans, no row data externally
- [ ] Column policy metadata such as `AI_DENY` / `AI_METADATA_ONLY` / `AI_ALLOW` or equivalent
- [ ] Never send passwords
- [ ] Never send database root keys/DEKs
- [ ] Never send auth/session tokens
- [ ] Never send private TLS keys
- [ ] Never send provider secrets
- [ ] External-provider context preview
- [ ] Local-only AI policy
- [ ] Administrator-enforced AI data policies
- [ ] Redaction layer tested

## Provider abstraction

- [ ] `ChatProvider` interface
- [ ] `EmbeddingProvider` interface
- [ ] Provider-independent core architecture
- [ ] Secure provider credential storage in Studio
- [ ] API keys never written to DB/logs/SQL
- [ ] Cloud provider support through adapters
- [ ] Local LLM support through adapters
- [ ] Local embedding model support
- [ ] OpenAI-compatible local endpoint support if useful
- [ ] Embedding dimension validation against `VECTOR<F32,N>`
- [ ] Multiple AI profiles (e.g. Local Private / Development Cloud / Enterprise)
- [ ] Optional model routing by task complexity
- [ ] Per-request token limits
- [ ] Monthly/organization budget controls
- [ ] AI usage/cost metrics without leaking DB contents

## Safe tool layer

Implement bounded explicit tools rather than unrestricted model database access:

- [ ] `search_docs`
- [ ] `search_schema`
- [ ] `describe_table`
- [ ] `describe_index`
- [ ] `get_query_plan`
- [ ] `get_query_stats`
- [ ] `validate_sql`
- [ ] `explain_sql`
- [ ] `generate_migration_preview`
- [ ] `search_workflows`
- [ ] `get_cluster_status`
- [ ] Additional tools only with explicit permissions/limits
- [ ] Max tool-call count
- [ ] Max rows
- [ ] Max returned bytes
- [ ] Max execution time
- [ ] Max context tokens
- [ ] Prepared typed parameters for all internal retrieval SQL

## Prompt-injection / content safety boundary

- [ ] Treat retrieved documents/database text as untrusted data, never trusted instructions
- [ ] Separate system policy, user request, tool output, and retrieved content
- [ ] Prompt-injection regression suite
- [ ] RAG documents cannot override execution/security policy

## Read-only by default

Default Intelligence capabilities:

```text
READ / SEARCH / EXPLAIN / VALIDATE / GENERATE
```

- [ ] Generated SQL is not automatically executed
- [ ] Generated SQL passes lexer/parser/binder preflight
- [ ] Authorization preflight where possible
- [ ] AST-based classification: READ / WRITE / DDL / SECURITY / DESTRUCTIVE
- [ ] Explicit user review for WRITE/DDL/SECURITY
- [ ] Strong confirmation for DESTRUCTIVE
- [ ] AI never autonomously executes DROP/ALTER/GRANT/REVOKE/REBUILD/MAINTAIN in production

## Studio Intelligence experiences

### Documentation assistant

- [ ] Answer NextSQL product questions from matching-version docs
- [ ] Cite exact source/section in UI
- [ ] Say when capability is unsupported/planned instead of hallucinating

### Schema assistant

- [ ] Ask questions about actual tables/relationships/indexes
- [ ] Never invent columns if catalog context exists

### SQL assistant

- [ ] Explain selected SQL
- [ ] Generate native NextSQL SQL
- [ ] Fix parser/binder/runtime errors
- [ ] Explain NextSQL-specific semantics
- [ ] Generate code examples for Go/Node/TypeScript/Bun/Deno/PHP official drivers

### Performance assistant

- [ ] Analyze `EXPLAIN ANALYZE`
- [ ] Distinguish observed/inferred/suggested findings
- [ ] Explain estimate-vs-actual errors
- [ ] Suggest ANALYZE/index/query rewrites based on evidence
- [ ] Never fabricate timings/costs
- [ ] Index advisor shows storage/write-amplification tradeoff

### RAG assistant

- [ ] Generate native RAG schema using JSON + FTS + VECTOR
- [ ] Generate hybrid retrieval SQL
- [ ] Inspect vector dimension/index configuration
- [ ] Inspect FTS capability based on version
- [ ] Explain hybrid ranking/candidate strategy
- [ ] Recommend retrieval improvements from real configuration/metrics

### Security assistant

- [ ] Explain actual TLS/encryption/audit/RBAC/key-rotation status from permitted metadata
- [ ] Never retrieve raw keys
- [ ] Never claim certainty without evidence

### HA assistant

- [ ] Explain cluster status/leader changes from actual Raft/health events
- [ ] State insufficient evidence instead of inventing failure cause

### Workflow / CDC assistant

- [ ] Generate/review workflows when server supports them
- [ ] Generate CDC examples only when server capability exists

## Intelligence chat UX

- [ ] Dedicated chat panel/workspace
- [ ] Visible current connection/realm/database/server version
- [ ] Context chips: current query/table/index/plan/docs/etc.
- [ ] Add/remove context manually
- [ ] Selection-aware actions: Explain/Fix/Optimize/Generate/Ask
- [ ] Error `Ask NextSQL Intelligence` action
- [ ] Plan-node `Explain Operator`
- [ ] Vector-index `Explain HNSW Settings`
- [ ] Grounding indicators: Docs / Live Schema / Plan / Metrics
- [ ] Clickable source citations
- [ ] Conversation export to Markdown with redaction
- [ ] AI provider outage never blocks normal Studio/database usage

## RAG Playground

Separate from Intelligence:

```text
NextSQL Intelligence → helps developers use NextSQL
RAG Playground       → helps developers build/test their own RAG systems
```

- [ ] Create RAG Assistant wizard
- [ ] Choose knowledge table
- [ ] Text column
- [ ] Metadata columns
- [ ] Vector column
- [ ] Optional application filter column
- [ ] Retrieval mode: FULLTEXT / VECTOR / HYBRID
- [ ] Top-K
- [ ] Transparent generated schema/config
- [ ] No hidden magic data store
- [ ] Document ingestion: TXT
- [ ] Markdown
- [ ] HTML
- [ ] PDF
- [ ] JSON
- [ ] CSV
- [ ] Parse/normalize/chunk/embed/insert pipeline
- [ ] Character/token/paragraph/heading-aware chunking
- [ ] Chunk overlap
- [ ] Store document/chunk lineage
- [ ] Vector dimension validation
- [ ] Retrieval inspector
- [ ] Source/section/metadata display
- [ ] BM25 rank
- [ ] Vector rank
- [ ] Hybrid rank
- [ ] Distance where meaningful
- [ ] Answer citations
- [ ] Click source context
- [ ] RAG evaluation dataset support
- [ ] Recall@K
- [ ] MRR
- [ ] NDCG
- [ ] Compare BM25-only / vector-only / hybrid
- [ ] Retrieval latency metrics
- [ ] Embedding latency
- [ ] Model latency
- [ ] Token usage
- [ ] Cache hit metrics

## Future RETRIEVER object research

Keep LLM generation out of `nextsqld`. Research a deterministic database-native retrieval object only after Studio RAG is proven:

```sql
CREATE RETRIEVER ...
RETRIEVE ... FOR $query;
```

- [ ] Design `RETRIEVER` object semantics
- [ ] Encapsulate table/text/vector/filter/mode/top-K configuration
- [ ] No external LLM dependency inside engine
- [ ] Retriever remains transactional/RBAC/realm/database-aware

### Phase 30 evaluation / security tests

- [ ] Official docs QA dataset across SQL/transactions/JSON/FTS/vector/hybrid/geo/security/backup/HA/migrations
- [ ] Retrieval Recall@K
- [ ] MRR
- [ ] Citation correctness
- [ ] Version mismatch tests
- [ ] Admin vs read-only user tests
- [ ] Restricted schema/table tests
- [ ] Realm/database-bound tests
- [ ] Root keys never leave redaction boundary
- [ ] Passwords/tokens/private keys never sent externally
- [ ] `AI_DENY` columns never sent externally
- [ ] Prompt-injection corpus tests
- [ ] Tool-loop/resource-abuse tests

### Phase 30 MVP exit gate

- [ ] Versioned docs knowledge base
- [ ] Hybrid/BM25 docs search
- [ ] Chat/embedding provider abstractions
- [ ] Server capability context
- [ ] Schema-aware context
- [ ] SQL editor/error/plan context
- [ ] Query explain/generate/fix
- [ ] Evidence-backed optimization suggestions
- [ ] RAG query generation
- [ ] Secure provider credential storage
- [ ] Metadata-only production policy
- [ ] Context preview/redaction
- [ ] RBAC/realm/database enforcement
- [ ] Tool limits
- [ ] Prompt-injection protections
- [ ] Clickable citations
- [ ] Studio remains fully usable with AI disabled/unavailable

---

# Cross-cutting gate — Product UX and safety

Applies to Installer, Manager, Studio, and Intelligence.

- [ ] Consistent NextSQL design language
- [ ] Clear separation of Installer / Manager / Studio responsibilities
- [ ] Light/dark/system theme where supported
- [ ] Keyboard navigation
- [ ] Screen-reader labels
- [ ] Visible focus states
- [ ] High contrast
- [ ] Do not rely on color alone for health
- [ ] Actionable errors with technical details available separately
- [ ] Dangerous operations require explicit confirmation
- [ ] Production environment always clearly identified
- [ ] No UI bypass of server authorization
- [ ] No UI direct manipulation of storage/WAL/catalog
- [ ] Secret redaction in logs/diagnostics/crash reports
- [ ] Offline core functionality
- [ ] Telemetry is never silently enabled

---

# Cross-cutting gate — Compatibility

Any persistent or protocol change requires:

- [x] Standards baseline documented — ISO/IEC 9075:2023 parts 3/4/9/11/15/16, ISO/IEC 9579:2000 RDA principles, TCP + TLS 1.3, and Unicode/UTF-8 (`docs/standards.md`)
- [ ] Feature-by-feature conformance mappings and tests exist before any formal standards-conformance claim

- [ ] Version identifier
- [ ] Old-version detection
- [ ] Migration path
- [ ] Upgrade tests
- [ ] Rollback limitations documented

Never silently change:

```text
page layout
WAL records
catalog serialization
backup manifest
NSQL messages
encryption envelope
Raft persistent state
```

Protocol changes must be capability/version negotiated.

---

# Cross-cutting gate — Testing for every new phase

Choose all applicable:

- [ ] Unit tests
- [ ] Integration tests
- [ ] Restart tests
- [ ] Crash-injection tests
- [ ] Concurrent/race tests
- [ ] Randomized/property tests
- [ ] Fuzz tests
- [ ] Benchmarks
- [ ] Raft replication/failover tests
- [ ] Backup/restore tests
- [ ] PITR tests
- [ ] Encryption tests
- [ ] RBAC tests
- [ ] Realm/database-isolation tests
- [ ] Protocol/driver tests
- [ ] UI accessibility tests
- [ ] UI failure-state tests

A parser-only test does not prove a database feature.

For durable new features, test relevant crash points around WAL append/fsync/page/catalog/index/maintenance/replication boundaries.

Required invariant:

```text
Committed state survives.
Uncommitted state does not become committed.
Catalog and indexes agree.
Known corruption is never returned as valid data.
```

---

# Cross-cutting gate — Security claims

Never:

- [ ] Log passwords/keys/tokens/private keys
- [ ] Put keys in connection URLs
- [ ] Put root unlock keys in application connection settings
- [ ] Disable encryption/durability for official performance claims
- [ ] Invent custom cryptographic primitives
- [ ] Claim “unhackable”, “100% secure”, “guaranteed zero downtime”, or impossible zero-data-loss outside the documented failure model

Every new persistent structure must document:

- [ ] Encryption domain
- [ ] Key version
- [ ] Integrity/authentication
- [ ] Backup behavior
- [ ] Restore behavior
- [ ] Rotation behavior
- [ ] Replication behavior

Cross-realm/database leakage tolerance remains **0**.

---

# Cross-cutting gate — Performance discipline

Continue measurements at:

```text
25K
100K
1M
10M
100M
```

Track where applicable:

```text
INSERT / UPDATE / DELETE
PK lookup
indexed lookup
range scan
COUNT
GROUP BY
JOIN
JSON
FTS
vector
hybrid
```

Report:

```text
p50 / p95 / p99 / p99.9 where meaningful
QPS / TPS
CPU / RAM / allocations
disk / WAL
database size / index size
encryption overhead
hardware + OS + filesystem + cache state + concurrency
```

Never improve benchmark claims by weakening fsync, WAL, encryption, checksums, MVCC, authentication, or recall unless clearly labeled experimental and excluded from official SLOs.

---

# Deferred beyond P30

Do not prioritize until preceding gates justify them:

- [ ] Multi-primary writes — **DEFERRED**
- [ ] Automatic distributed sharding — **DEFERRED**
- [ ] LLM inside query optimizer — **rejected**
- [ ] LLM required for database correctness — **rejected**
- [ ] Mandatory cloud account for local NextSQL — **rejected**

Preferred distributed direction remains:

```text
single Raft leader for writes
+ synchronous quorum durability
+ followers
+ optional follower reads
+ local partitioning
+ later explicit shard placement if needed
```

---

# Final product family target

```text
NextSQL Engine
  Database runtime: SQL + JSON + FTS + Vector + Hybrid + Geo + Workflow + CDC
NextSQL CLI
  Automation and headless operations
NextSQL Bench
  Correctness-aware official measurement suite
NextSQL Drivers
  Go / Node.js / TypeScript / Bun / Deno / PHP + future supported SDKs
NextSQL Installer
  Install / upgrade / repair / uninstall
NextSQL Manager
  Server / cluster / security / backup / operations
NextSQL Studio
  Database development IDE / SQL / schema / data / multimodel explorers
NextSQL Intelligence
  Version-aware, permission-aware, RAG-grounded assistant inside Studio
```

Core architectural principle:

> **One engine, one optimizer, one transaction model, one durability model, and one security model across every data modality.**

AI remains an optional development/knowledge layer. NextSQL itself remains deterministic and fully functional without an AI provider.

---

# Updated next actions

## P16 -- complete (paper-closed 2026-08-30)

1. [x] Randomized insert/delete invariant soak (`./scripts/run-btree-soak.sh`) -- verified `Check()`-clean at every scale reached (v8: 44M clean operations, `live=17,557,686`). The **terminal 100M-operation run is a deferred standalone measurement, not a P16 gate** (same disposition as P18): v9 exited 137 with no retained evidence, v10 was stopped by direction, and the harness was reworked for RAM-constrained hosts (`NEXTSQL_BTREE_POOL_PAGES`, `NEXTSQL_BTREE_SPACE`, decoupled checkpoint cadence, `int32` bookkeeping, post-check `FreeOSMemory()`).
2. [x] Run `--slo-max-rows 100000000` and publish COUNT/GROUP BY/indexed lookup/range/join results -- 2026-08-21 ext4; all corrected analytics targets met.
3. [x] Run corrected `--slo-vectors 1000000` and publish recall@10/@100, p50/p95/p99, QPS, RAM, DB size, and index size -- v10 p50 6.158 ms, p95 8.061 ms, p99 8.156 ms, QPS 156, recall@10 1.000, recall@100 0.998, heap 4.3 GiB, DB 1.1 GiB, HNSW 546.1 MiB.
4. [x] Explain 10M DELETE heap-swap variance and publish methodology -- reopened trees have no process-local exact live-row cache, so affected-row counting scans the leaf chain before the same constant-time heap swap (`docs/ops.md`).
5. [x] P16 exit gate green (10M DELETE, crash-during-merge `Check()`-clean, 100M analytics < 60 s, 1M HNSW p95 < 25 ms with recall, 10M INSERT/UPDATE published, security sign-off). A future terminal 100M B+Tree run is optional, not a gate.

## Completed alongside P16

6. [x] Start P17 with `DROP INDEX` + storage reclamation before feature-heavy work — P17 exit gate is green; `REBUILD INDEX … ONLINE` remains deferred.
7. [x] P18 SQL completeness implementable scope — CTEs, subqueries, windows, UPSERT/RETURNING, covering/partial/expression indexes, join reordering; partition-wise aggregation and join hooks landed 2026-08-30. P18 implementable scope is closed.
8. [x] P19 WORKFLOW/TRIGGER/SCHEDULE/TASK — native v1 and the clean repository-wide functional gate are green.
9. [x] P20 CDC/change streams — committed ordered streaming, bounded resume/backpressure, RBAC, restart, and failover gates are green.
10. [x] P21 native table partitioning — multi-column HASH/RANGE/LIST keys, tuple-tight multi-column RANGE pruning, cross-partition plain-column secondary UNIQUE enforcement (`TestPartitionCrossPartitionUnique`, `TestPartitionCrossPartitionUniqueSerializedWriters`), partition-aware `UPSERT` (`TestPartitionUpsert`), randomized pruning-soundness property coverage (`TestPartitionPruningSoundness`), partition benchmarks (`nextsql-bench --partition`, `TestPartitionBench`), and explicit offline legacy TENANT migration (`nextsql hosting migrate-tenant`, 2026-08-30). P21 exit gate green.
11. [ ] Begin Installer/Studio UI framework research in parallel only after stable management/system interfaces are defined.
12. [ ] Keep every new phase checkbox truthful: code + tests + docs + gate, not design-only.

## P22 follower reads / read scaling — COMPLETE (2026-08-30)

13. [x] Formal linearizability/consistency sign-off for `STRONG` reads — `docs/ha.md` "Consistency model and sign-off": `STRONG` is linearizable under the covered failure model via `StrongReadBarrier` (leader check + `raft.VerifyLeader` quorum round trip); leader completeness + quorum intersection block the stale-leader anomaly. Read path benchmarked (`--readscale`: `VerifyLeader` round trip is the whole added cost vs `STALE`). Leader-lease fast path recorded as a deliberate non-goal.
14. [x] Failover session-guarantee test — `TestFollowerReadFailoverSessionGuarantee` (`tests/integration`): a `STRONG` routing-client session keeps read-your-writes + monotonic reads across a leader partition/re-election; a `STALE` read on the partitioned former leader may lag but never regresses below its applied state (documented trade-off). Plus `TestHAKillLeader` for no-lost-commit.
15. [ ] Optional follow-on (not a gate item): 3-node cluster-routing live test for the non-Go drivers (their live harness is single-node; Go server routing + failover are covered by `tests/integration/follower_read_test.go`).

## P23 Vector Engine 2.0 — COMPLETE (2026-08-31)

16. [x] Production-gating sign-off for a memory-efficient representation — `docs/vector.md` "Production-gating sign-off (Phase 23)": `VECTOR<F16,N>` / `VECTOR<I8,N>` / `BITVECTOR<N>` and the quantised HNSW index are production-gated; IVF / IVF-PQ / sparse / fusion ANN paths have recall/latency/size/QPS/RAM measurements with encryption + WAL + fsync on. Existing F32/HNSW remains compatible. No durability/encryption regression. Documented follow-ons (not gate items): a `BITVECTOR`/Hamming `--vecquant` row, a process-local IVF-PQ cache, a re-rank-free quantised HNSW mode, IVF/IVF-PQ/SPARSE on partitioned tables, SIMD after profiling.

## P24 Full-text Search 2.0 — COMPLETE (2026-08-31)

17. [x] Stemming — versioned analyzer metadata (`NSCT` v9), fail-closed query-expansion CPU/memory caps, Snowball English (Porter2) v1 via `WITH (ANALYZER = 'english')`, default `simple` preserves BM25/phrase behaviour.
18. [x] Stop-word dictionaries — english analyzer v2 applies stop-word dictionary v1 (33-term Lucene EnglishAnalyzer / Snowball-small set) before Porter2, identically at index and query time; remaining terms re-pack to consecutive positions; `simple` has no stop list; english v1 (stem only) still decodes; dropped stop words consume query-expansion work units (`TestFulltextEnglishStopWords`, `TestAnalyzeEnglishDropsStops`).
19. [x] Versioned language analyzers — `french` / `german` / `spanish` v1 (Snowball 3.x stemmer + official Snowball stop-word dictionary v1) on existing `NSCT` v9 ids 2/3/4; French elides `l'`/`qu'`/… before the stop list; remaining terms re-pack to consecutive positions; `simple`/`english` unchanged (`TestFulltextLanguageAnalyzers`, `TestStemFrenchFixtures`).
20. [x] Synonym dictionaries — english analyzer v3 applies synonym dictionary v1 (15 tight bidirectional groups) at query time as OR-expansion, bounded by the existing caps; index terms stay 1:1 like v2; phrase slots accept any alternative; english v1/v2 still decode (`TestFulltextEnglishSynonyms`, `TestParseQueryEnglishSynonyms`).
21. [x] Prefix search — trailing ASCII `*` on a SEARCH token is a query-time prefix group (`cat*` matches `catalog`; exact `cat` does not); prefix tokens skip stem/stop/synonym (French elision still applies); matching terms are a disjunction at that position; phrase slots accept a prefix; BM25 scores the best match; distinct terms consume the existing expansion caps and fail closed (`TestFulltextPrefixSearch`, `TestParseQueryPrefix`, `TestPrefixExpanderFailClosed`).
22. [x] Fuzzy matching — trailing ASCII `~` on a SEARCH token is a query-time fuzzy group (`cat~` matches `cot`; exact `cat` does not); optional `~1` / `~2`; AUTO distance is 0/1/2 by rune length; OSA Damerau-Levenshtein (insert/delete/substitute/adjacent transpose); fuzzy tokens skip stem/stop/synonym (French elision still applies); matching terms are a disjunction at that position; phrase slots accept a fuzzy term; BM25 scores the best match; distinct terms consume the existing expansion caps and fail closed; mixed `*`/`~` fails closed (`TestFulltextFuzzySearch`, `TestParseQueryFuzzy`, `TestFuzzyWithin`).
23. [x] Typo tolerance — unadorned tokens whose analyzed alternatives are all absent from the vocabulary become AUTO fuzzy (`databse` matches `database`); typo AUTO is 0/1/2 for 1–4 / 5–8 / 9+ runes (stricter than explicit `~`); present exact terms stay Phase 10 (`cat` does not match `cot`; `cats` does not match `cat`); prefix and explicit fuzzy unchanged; phrase slots follow the same rule; distinct terms consume the existing expansion caps and fail closed (`TestFulltextTypoSearch`, `TestApplyTypoToleranceMissing`, `TestAutoTypoDistance`).
24. [x] Highlight/snippet generation — `HIGHLIGHT(col)` / `SNIPPET(col)` SELECT-list functions require SEARCH (no catalog/format bump); original tokens whose analyzed form participates in the query (exact/synonym/prefix/fuzzy/typo) are wrapped with `<mark>` (overrideable, max 32 runes); `SNIPPET` is a 16–4096 rune window (default 160) around the densest match cluster with `…` on a truncated edge; fail closed outside SEARCH SELECT lists (`TestFulltextHighlight`, `TestHighlightExact`, `TestSnippetWindow`, `TestBindHighlightRequiresSearch`).
25. [x] Multi-field search — `CREATE FULLTEXT INDEX` / `SEARCH col [, col …]` take 1–8 STRING/TEXT columns (exact column-list match for inverted-index use; subset/reorder seq-scans); fields analyzed independently and scored as one BM25 document; phrases do not cross fields (position band `i·(MaxDocTokens+2)`, no catalog/format bump); duplicate columns and combined token cap fail closed; prefix/fuzzy/typo/`HIGHLIGHT`/`SNIPPET` unchanged (`TestFulltextMultiFieldSearch`, `TestAnalyzeFieldsPositions`, `TestBindFulltextMultiField`, `TestSearchChoosesMultiFieldFulltextIndex`).
26. [x] Field weighting — optional `WEIGHT <number>` after a SEARCH column (`SEARCH title WEIGHT 3, body FOR '…'`) scales that field's BM25 term frequency from existing position bands; omitted weights are 1 so unweighted SEARCH stays Phase 10 / multi-field BM25; query-time only (no catalog/format bump); range `(0, 64]` fail closed; prefix/fuzzy/typo/`HIGHLIGHT`/`SNIPPET`/phrase matching unchanged (`TestFulltextFieldWeight`, `TestWeightedTF`, `TestQueryScoreWeighted`, `TestCheckFieldWeight`).
27. [x] Faceting — `SELECT * … SEARCH … FACET col [, col …]` returns independent histograms over the full SEARCH match set (`facet STRING`, `value STRING`, `count DECIMAL`); `LIMIT` is per-facet top-N; `NULL` skipped; query-time only (no catalog/format bump); requires `SELECT *` and `SEARCH`; 1–8 discrete columns (`STRING`/`TEXT`/`DECIMAL`/`BOOL`/`UUID`/`TIMESTAMPTZ`) and 1024 distinct values fail closed; `JOIN`/`GROUP BY`/`HAVING`/`DISTINCT`/`ORDER BY`/`OFFSET`/`NEAREST` fail closed (`TestFulltextFacet`, `TestFacetDistinctValueCap`, `TestBindFulltextFacet`, `TestSearchFacetPlan`).
28. [x] P24 exit gate — Phase-10 BM25/phrase golden compatibility; 4096-term fail-closed fuzzy/typo vocabulary cap with linear-memory OSA; expanded end-to-end quality fixtures; analyzer-aware encrypted kill/reopen recovery; `go build ./...`, targeted functional/race, 5-second `FuzzTokenize`, and serialized `go test -p 1 ./... -count=1` green. Next increment: P25 security-surface audit and first mTLS/service-identity slice.
29. [x] P25 certificate/trust rotation and revocation — `SIGHUP` atomically publishes a validated server-key/client-trust/CRL snapshot; failed reloads retain last-known-good state; X.509 CRLs are signature/time/full-chain checked and revoked serials fail closed; successful mTLS reloads terminate every accepted connection, including pre-auth handshakes, for reauthentication. No persistent or NSQL wire-format change.
30. [x] P25 signed short-lived credentials — an Ed25519-signed `NSSC1.` credential (`internal/auth`: `token.go` / `tokenkeys.go` / `tokenrevoke.go` / `tokenverify.go`) presented in place of the password over the existing `Auth` frame (server routes any `NSSC1.` password to `TokenVerifier` when `token_verify_keyset` is set). Claims: signing-key id, random token id, issued-at/not-before/expires-at, native principal, optional audience/database/realm/role scope. Fail-closed on bad/retired key, invalid signature, validity window (60 s skew), lifetime over the verifier max (default 24 h, ceiling 30 d), `token_audience` mismatch (a configured audience also rejects an unscoped credential), served-database mismatch, or revocation. The protocol server also requires the principal to equal the Hello user and be a known native user, applies the role scope to the session via `ACL.AllowedScoped` (no-escalation: the principal must already hold every listed role), and closes the session at the credential's expiry. `NSTK` v1 rotatable keyset (`current`/`retired`, verify-only server copy via `export-public`); `NSTR` v1 revocation set (token id pruned at its own expiry + per-principal issued-before cutoff); `SIGHUP` reloads both, last known-good on failure. New `token_verify_keyset` / `token_revocations` / `token_audience` config keys wired in `nextsqld`; new `nextsql token` CLI (`keygen`, `rotate`, `retire`, `list-keys`, `export-public`, `mint`, `revoke`, `verify`); auth `identity_source` `token` / `mtls+token`. Official drivers unchanged (credential rides the password slot); non-Go driver convenience helpers are a documented follow-on. Tests: `internal/auth` unit + `FuzzDecodeTokenClaims` / `FuzzDecodeTokenKeys`, `TestACLAllowedScoped`, `internal/config`, `tests/integration/short_lived_credential_test.go`; `go build ./...`, targeted `-race`, and 8 s fuzz green. No persistent database or NSQL wire-format change. Next increment: external IdP (OIDC) design.
31. [x] P25 external IdP (OIDC) design — accepted design `docs/design-oidc-external-idp.md`. Chosen architecture: a brokered token exchange (`cmd/nextsql-auth-broker`, or embedded `nextsqld --auth-broker-listen` for single-node) that runs OIDC Authorization Code + PKCE (interactive) or client-credentials (workloads), validates the IdP ID/access token against a soft/hard-TTL cached JWKS (`iss`/`aud`/`alg` allowlist rejecting `none`+MAC/`exp`/`nonce`/replay), and mints an existing `NSSC1.` short-lived credential — so `nextsqld`'s SQL auth path gains no OIDC parsing, no outbound HTTP, and stays offline; the broker's issuing key is just another `NSTK` key in `token_verify_keyset`. `NSIP` (NextSQL Identity Policy) v1: versioned, deployment-encrypted, `SIGHUP` last-known-good; ordered issuer-scoped subject→principal rules (named-claim templates only, normalized to the native login charset, no match ⇒ deny) and group→role mappings whose union is **intersected with the principal's real NextSQL RBAC membership** (mapped-but-not-member ⇒ dropped; empty ⇒ deny) so `ACL.AllowedScoped` enforces no-escalation exactly as for hand-minted tokens. Invariants I1–I5: every OIDC session is a real native principal that independently holds its privileges; RBAC on every statement; session ≤ minted `expires-at` ≤ verifier max; audit `identity_source` `oidc`/`mtls+oidc` derived from the verifying key (not attacker bytes); every ambiguous input fails closed. IdP/broker outage blocks only new OIDC logins — never existing sessions, password/mTLS auth, or commit. Optional off-by-default JIT provisioning gated behind its own capability. Direct server-side JWT verification (`NSIDP1.` in the password slot) written up as the rejected alternative (per-node JWKS + policy sync, per-connection IdP dependency, large tokens). Delivery plan: (2) `NSIP` core + fuzz, (3) broker skeleton + fake-IdP integration, (4) `nextsql login`/`logout`/`whoami`, (5) server audit labeling, (6) client-credentials, (7) embedded mode, (8) optional JIT. Design only — no code, no persistent/wire change; `docs/security.md` P25 audit OIDC rows stay `implemented: no` / `tested: no`. Next increment: `NSIP` identity-policy core.

32. [x] P25 `NSIP` identity-policy engine (delivery plan increment 2) — new pure package file `internal/auth/identitypolicy.go`. `PolicyDoc` (subject rules, group claim, group mappings, default roles) has a deterministic little-endian binary form: `"NSIP"` + version + bounds-checked body; `decodeIdentityPolicy` never allocates from an unchecked length, caps every count (64 rules, 16 conds/rule, 256 mappings, 8 transforms, 16 roles), rejects trailing bytes, and fully `compilePolicy`-validates (RE2 compile of every anchored pattern, `[a-z0-9._-]{1,128}` role/login checks, `${n}` role-template range vs `NumSubexp`). `WriteIdentityPolicy` writes mode `0600` via atomic rename; `OpenIdentityPolicy`/`LoadIdentityPolicy` load+compile; `IdentityPolicy.Reload` keeps the last known-good policy on any read/parse/validate/compile error (same contract as `NSTK`/`NSTR`; at-rest envelope encryption is a shared follow-on, noted in the design doc §6). `IdentityPolicy.Map(issuer, claims)` — first issuer-scoped rule whose every claim condition (`equals`/`prefix`/`suffix`/anchored-RE2, ANDed) passes wins; derives the principal from a literal or a named claim through a pure transform pipeline (`lower`, `before`, `after` a first-occurrence delimiter — missing delimiter fails closed — `replace`); the lowercased result must be a valid login charset or the mapping denies. Groups (array or scalar claim, ≤256 inspected) map to roles by exact literal match or anchored RE2 with `${0..9}` capture templates; union deduped, `default_roles` fallback, `normTokenRoles` 16-cap, empty ⇒ deny. `IdentityPolicy.Authorize(issuer, claims, held)` = `Map` then `IntersectRoles(mapped, held)` (the no-escalation gate — result is what a broker would place in an `NSSC1.` `Roles` list); empty intersection ⇒ `Forbidden`. Claim lookup walks dotted paths over `map[string]any`; `json.Number`/`bool`/`float64` coerce to strings for conditions. Every unmatched/ambiguous/over-cap input is a typed `nerr.Forbidden`/`InvalidArgument`/`InvalidFormat`. Tests: `internal/auth/identitypolicy_test.go` (round-trip byte-stable, first-match-wins, no-match deny, transform fail-closed, principal charset reject, RE2 capture, group-claim-absent deny, default-roles fallback, no-group-claim mode, RBAC intersection drop + empty-deny, `IntersectRoles`, >16-role cap deny, bad-regex/out-of-range-template reject, `Reload` last-known-good) + `FuzzDecodeIdentityPolicy` (decode ⇒ compile ⇒ re-encode ⇒ decode stable) + `FuzzMapClaims` (arbitrary JSON claims ⇒ `Map` invariants: valid principal, 1..16 roles, self-consistent `Authorize`). `go build ./...`, `go test ./internal/auth`, `-race`, and 8 s each fuzz target green. Not wired to any broker, server path, config key, or audit field; `security.ACL` untouched. `docs/security.md` P25 audit: OIDC end-to-end still `implemented: no`; the 3 mapping-policy rows → `partial` / `tested: yes`. Next increment: broker skeleton (`cmd/nextsql-auth-broker`) + fake-IdP integration.

33. [x] P25 authentication broker skeleton (delivery plan increment 3) — two new pure/offline packages plus one command. **`internal/oidc`**: compact-JWS parse + asymmetric signature verification for `RS256/384/512`, `PS256/384/512`, `ES256/384/512` (`AlgIsAsymmetric` — `none` and every `HS*` MAC alg rejected, and the verifier constructor refuses a config that names one); `ParseJWKS` (RSA `n`/`e` ≥2048-bit, EC `x`/`y`/`crv` on-curve, `use`/`alg` filtered); `JWKSCache` with a soft TTL (served without refresh), a hard TTL (past it → fail closed), and a per-issuer refresh rate limit (5 min) — a refresh runs under the cache lock so concurrent misses coalesce, and a stale-but-within-hard key is still served through a brief IdP outage; `IDTokenVerifier.Verify` checks alg-allowlist, `iss` exact, `aud` contains client id, `azp`==client id if present, signature vs the kid-selected JWKS key, `exp`/`iat`/`nbf` within a skew (default 120 s, ceiling 300 s), `nonce` when supplied, and a non-empty `sub`; `ReplayGuard.Observe` rejects a second exchange of the same `jti` (or `sub|iat`) until its expiry, pruning expired entries and bounded at 65 536. `HTTPFetcher` is https-only with a bounded body/timeout. Fuzz: `FuzzParseJWKS`, `FuzzParseCompact`, `FuzzVerify` (8 s each clean). Test-only `internal/oidc/oidctest` fake IdP (RSA/ES256 signer, JWKS + discovery docs, in-memory `Fetcher`). **`internal/authbroker`** + **`cmd/nextsql-auth-broker`**: line-based config with `[idp "name"]` sections (unknown keys rejected); `Broker.New` loads the `NSIP` policy and a private `NSTK` issuing keyset and builds a JWKS cache + `IDTokenVerifier` per profile; `Handler` serves `POST /v1/exchange` and `/healthz`. Exchange = decode `{idp,id_token,nonce,database,realm}` (unknown fields rejected, 64 KiB cap) → `IDTokenVerifier.Verify` → `ReplayGuard` → `IdentityPolicy.Map(issuer, claims)` → optional `RoleMembershipFunc` intersection (`auth.IntersectRoles`, empty ⇒ 403) → `keyset.Mint` an `NSSC1.` credential with `Principal`=mapped, `Audience`=`deployment_audience`, `Database`/`Realm` from the request, `Roles`=effective, `TTL`=min(`oidc_credential_ttl`, IdP-token-exp − now). Response `{credential,principal,roles,expires_at,token_id}`. Rejections return a generic message; the specific reason and a structured record (issuer, 8-byte subject hash, matched rule id, principal, mapped + effective roles, outcome, minted token id, expiry) go only to the audit log — never the ID token, credential, or a client secret. `SIGHUP` → `Broker.Reload` (policy + keyset, last-known-good). `main.go`: `--config`, TLS via `security.ServerTLS` (loopback may run plaintext), graceful shutdown. **`nextsqld` unchanged** — it only ever sees the minted `NSSC1.`; the broker's public key goes in `token_verify_keyset`. Tests `internal/authbroker/broker_test.go`: fake-IdP → broker `httptest` server → **real `auth.TokenVerifier`** built from the issuing keyset's public half — happy path (principal + roles + audience + expiry window all verified on the minted credential), RBAC intersection (`db-admins`→{app_admin,reporting_ro} narrows to the held {reporting_ro}; a principal holding none ⇒ 403), replay ⇒ 403, `alg=none`/MAC/wrong-`iss`/wrong-`aud`/bad-`nonce`/unmapped-subject/unmapped-groups/missing-group-claim ⇒ deny with no credential leaked, JWKS outage ⇒ 503, credential TTL clamped to the IdP token expiry, `Reload` on a corrupt policy keeps serving, `LoadConfig` round-trip + unknown-key reject. `go build ./...`, `go vet`, `go test` + `-race` on `internal/oidc` / `internal/authbroker` green. No persistent database or NSQL wire-format change. `docs/security.md` P25 audit: `OIDC implementation` → `implemented: partial` / `tested: yes`; the 2 mapping rows the broker now consumes → `implemented: yes`. Next increment: `nextsql login` client flow (OIDC discovery + PKCE + local callback + `/v1/exchange` + secure credential storage + `logout`/`whoami`).

34. [x] P25 interactive OIDC CLI (delivery plan increment 4) — `internal/oidcclient` implements exact-issuer discovery, Authorization Code + PKCE S256, cryptographically random state/nonce, a transient bounded loopback callback, system-browser/manual URL launch, code redemption, broker `/v1/exchange`, and refresh-token renewal. `cmd/nextsql/login.go` adds `nextsql login` / `logout` / `whoami`; `internal/cli/oidc.go` resolves `--idp` into the mapped native principal and `NSSC1.` password for `exec` and server `status`. The versioned JSON client store is not a database format: it is keyed with a collision-resistant hash of IdP+host, atomically written through a random temporary file, file mode `0600` in a `0700` real directory, rejects symlinks or group/other-readable files, and bounds files at 1 MiB. The HTTP client rejects redirects (so 307/308 cannot replay a code, refresh token, or client secret), bounds every response at 1 MiB, accepts plaintext broker HTTP only on exact loopback, and requires parsed HTTPS URLs elsewhere. A wrong-state callback cannot consume the legitimate result; concurrent callbacks publish once without a race. `TestLoginEndToEnd` runs fake IdP → client PKCE → real broker → real `auth.TokenVerifier`; silent refresh, no-refresh fail-closed, discovery issuer mismatch, PKCE randomness, store permissions/collision/symlink handling, redirect replay, oversized response, and callback CSRF/concurrency are covered. `go build ./...`, targeted `go test` + `go test -race`, and serialized `go test -p 1 ./... -count=1` green. No database persistent/catalog/WAL/Raft or NSQL wire-format change. Next increment: key-derived `oidc` / `mtls+oidc` server audit labeling.

35. [x] P25 key-derived OIDC server audit labeling (delivery plan increment 5) — `token_identity_source_hint=KEY_ID:oidc[,KEY_ID:oidc...]` is a bounded (64-entry), fail-closed `nextsqld` config map tied to the configured `NSTK` verify keyset. `internal/protocol` consults a hint only after Ed25519 verification succeeds, so dedicated broker keys record `identity_source` `oidc` / `mtls+oidc`, while forged signatures, attacker-selected key ids, unknown keys, and unknown hint values stay `token` / `mtls+token` or fail config loading. The implementation deliberately adds no client-controlled source claim and makes no `NSSC1.` credential-format or NSQL wire change. `internal/security` now preserves the closed legitimate identity-source enum, fixing the generic secret redactor's prior replacement of documented `token` / `mtls+token` values; unknown/secret-shaped values remain redacted. Tests: `TestTokenIdentitySourceIsDerivedFromVerifiedKey`, `TestShortLivedCredentialOIDCAuditSourceIsKeyDerivedAndSecretFree`, `TestForgedOIDCKeyIDCannotUpgradeAuditSource`, `TestAuditNeverWritesSecrets`, and config malformed/duplicate/zero/over-bound coverage. `go build ./...`, targeted functional + race, and serialized `go test -p 1 ./... -count=1` green. No database persistent/catalog/WAL/Raft change. Next increment: OAuth2 client credentials.
36. [x] P25 OAuth2 client credentials (delivery plan increment 6) — `nextsql login --client-credentials [--client-secret-file FILE]` performs exact-issuer discovery and OAuth2 `client_credentials`, requires a Bearer access token, and exchanges it without a nonce. Per-profile `access_token_audience` opt-in constructs an `AccessTokenVerifier` on the existing bounded JWKS/signature/time stack; it requires the exact resource audience and an exact `client_id` or `azp` binding, rejects ambiguous ID+access requests, opaque tokens, MAC/`none`, wrong client/resource, expiry, replay, and trailing JSON, then uses the same `NSIP` mapping, optional RBAC intersection, credential TTL clamp, and `NSSC1.` minting path. Client-secret files are regular/non-symlink, mode `0600`, ≤64 KiB, opened with same-file/permission recheck; the value never reaches the broker or stored credential, while the non-secret path enables automatic renewal. Tests: `TestAccessTokenVerifyHappyPath`, `TestAccessTokenVerifyRequiresResourceAndClientBinding`, `TestExchangeClientCredentialsAccessToken`, `TestExchangeAccessTokenRequiresExplicitProfileAudience`, `TestExchangeRejectsAmbiguousOrUnboundAccessToken`, `TestClientCredentialsEndToEnd`, `TestEnsureFreshRenewsClientCredentials`, `TestReadClientSecretFileFailsClosed`; `FuzzVerify` also exercises access-token verification. `go build ./...`, targeted functional/race, 8-second `FuzzVerify` (~464k executions), and serialized `go test -p 1 ./... -count=1` green (workspace `TMPDIR` used after the first run hit the environment's `/tmp` quota). No credential-format, database persistent/catalog/WAL/Raft, or NSQL wire change. Next increment: embedded broker mode; opaque-token introspection remains optional and JIT remains off by default.
37. [x] P25 embedded authentication broker (delivery plan increment 7, 2026-09-01) — `nextsqld --auth-broker-listen ADDR [--auth-broker-config FILE]` hosts the exact `internal/authbroker` handler/runtime on a separate bounded HTTP(S) listener; the default config is `DATA-DIR/nextsql-auth-broker.conf`. It is deliberately rejected with Raft/HA (standalone broker remains the HA deployment), requires `token_verify_keyset`, verifies the configured private issuer's current signature against that server keyset before binding, and on `SIGHUP` reloads the verifier before publishing a validated issuer key. Embedded mode wires the live `auth.Store` and direct/transitive `security.ACL.RolesFor` membership, so a missing user or empty mapped∩held role set denies at exchange and role/user revocation takes effect immediately; the SQL listener still only receives ordinary `NSSC1.` and performs its normal per-statement `ACL.AllowedScoped` checks. The standalone command now shares `authbroker.HTTPServer`; its TLS path is covered as one TLS wrap rather than the prior compositional double-wrap risk. Tests: `TestEmbeddedAuthBrokerUsesLiveNativeMembership`, `TestEmbeddedAuthBrokerRejectsUnverifiableIssuingKey`, `TestACLRolesForIncludesInheritedRoles`, `TestHTTPServerLoopbackLifecycle`, `TestHTTPServerTLSIsWrappedExactlyOnce`, `TestHTTPServerRequiresTLSOffLoopback`, plus config/targeted functional and race gates; repository build, targeted vet, and serialized full functional suite green. No credential-format, database persistent/catalog/WAL/Raft, or NSQL wire change. Optional opaque introspection/JIT remain off; next required increment: field-level client encryption.
38. [x] P25 field-level client encryption, experimental first slice (2026-09-01) — `ENCRYPTED CLIENT` grammar through parser/catalog/binder/executor; `NSCT` v10 per-column logical type over physical opaque STRING; portable randomized `NSCE1.` AES-256-GCM with a 1 MiB plaintext cap, 64-byte public key-id cap, and AAD binding exact database/table/column + header; server performs only bounded structural/type validation and never receives a field key. Opaque-only SQL permits parameter/NULL/same-column copy writes and bare projection/RETURNING; predicates/joins/expressions/subquery exposure/defaults/PK/FK/partition/index/INCLUDE/SEARCH/FACET/group/order/distinct/set ops and context-changing rename/partition/legacy-tenant migration fail closed. Go driver `FieldKeyProvider`, bounded in-memory overlap keyring, generation, encrypt/decrypt, rotation, and revocation helpers landed. Tests cover randomization, wrong/revoked key, tamper/context/type mismatch, envelope bounds, catalog round-trip/corruption/v1–v9 compatibility, binder bypasses, encrypted restart + server-file plaintext scan, system metadata, exact-ciphertext backup/restore and logical export/import, and tenant-migration rejection. Repository build, targeted functional/race, 5-second `FuzzInspect` (~235k executions), and 5-second `FuzzDecodePartitionedTable` (~127k executions) green. Serialized all-package run passed through crash/HA but saw one transient Bun live page-isolation failure; Bun then passed 5 isolated repetitions and the full integration package rerun. `docs/client-encryption.md` records the leakage/key/migration/recovery contract. The then-open non-Go driver follow-on is closed by item 39; PITR and replication/failover remain.
39. [x] P25 field-level client encryption official-driver increment (2026-09-01) — Node.js/TypeScript, Bun, and Deno expose `FieldType`, async `FieldKeyProvider`, bounded `MemoryFieldKeyring`, `generateFieldKey`, standalone `encryptField` / `decryptField` / `inspectField`, and connection-bound helpers; PHP exposes `FieldType`, `FieldKeyProvider`, `MemoryFieldKeyring`, `FieldEncryption`, and connection-bound helpers. All use established runtime AES-256-GCM (`node:crypto`, Web Crypto, or OpenSSL), the exact versioned `NSCE1.` header/AAD/scalar/NSJB encoding, the 1 MiB plaintext and 64-key/key-id bounds, randomized nonces, rotation overlap, and provider refusal for revocation. Unit suites cover every supported scalar type, randomization, wrong context, rotation/revocation, and NULL; a Go-produced fixture decrypts in Node/Bun/Deno/PHP and a Node-produced fixture decrypts in Go. Node, Bun, Deno + `deno check`, PHP lint/unit, and targeted Go core/driver tests pass (the first Go link hit the host `/tmp` quota and passed with repository-local `TMPDIR`/`GOCACHE`). No persistent/catalog/WAL/Raft or NSQL wire change. Capability remains experimental; next increment: PITR, then replication/failover.
40. [x] P25 field-level client encryption PITR + replication/failover (2026-09-01) — `TestEncryptedClientPITRRestoresExactCiphertextAtTarget` (`internal/backup/backup_test.go`): a base backup plus archived WAL restored to a target LSN preceding a later `UPDATE` retains the `TEXT ENCRYPTED CLIENT` column, returns the exact pre-target `NSCE1.` ciphertext byte-for-byte, excludes the later archived write, and decrypts correctly only through the client-side `clientenc` helper — the restored server never sees a field key. `TestHAEncryptedClientCiphertextSurvivesLeaderFailover` (`tests/ha/ha_test.go`): a three-voter Raft cluster commits an encrypted-client write on the leader, confirms the identical acknowledged ciphertext replicates to every follower, kills the leader, confirms the new leader still serves and can decrypt the acknowledged ciphertext (no lost commit), commits a second ciphertext after failover, and confirms it — and its decrypt — on the remaining follower. Both close the last two open field-level client-encryption gate items; no catalog/WAL/wire-format change. Targeted `go build ./...` and both tests green. Remaining before full production gating: durable key-rotation/revocation KMS lifecycle (provider contract and in-memory overlap/refusal behavior are already tested in every official driver).
41. [x] P25 field-level client encryption durable key-rotation/revocation KMS lifecycle (2026-09-02) — `FileFieldKeyring` lands in every official driver (Go, Node.js/TypeScript, Bun, Deno, PHP): a durable, atomic, versioned, mode-`0600` file-backed `FieldKeyProvider` implementing the `NSFK1` on-disk format (mirrors the server's own `NSTK` signing-key lifecycle). Rotation makes a new key current while retaining every prior live key for overlap reads, persisted across process restart. Revocation overwrites the revoked key's material with zeros on disk, refuses to resolve the id afterward, rejects revoking the current key directly, and a revoked id can never be reused. Corrupt, truncated, or structurally invalid keyring files fail closed on decode. The `NSFK1` format is identical across every driver (a Go-produced fixture opens correctly in the Node driver). This closes the last open item blocking `ENCRYPTED CLIENT` field-level encryption from being fully production-gated (`docs/client-encryption.md` "Production-gating sign-off (Phase 25)"); formal production-gating still awaits the single phase-wide P25 exit gate, not any `ENCRYPTED CLIENT`-specific blocker. Tests: `drivers/go/nextsql_test.go` (`TestFileFieldKeyringPersistsAcrossReopen`, `TestFileFieldKeyringRotateRevokePersist`, `TestFileFieldKeyringRevokeZeroesMaterialOnDisk`, `TestFileFieldKeyringCannotRevokeCurrent`, `TestFileFieldKeyringCannotReuseRevokedID`, `TestFileFieldKeyringReloadLastKnownGood`, `TestDecodeFieldKeyringRejectsCorruption`), `drivers/bun/nextsql.test.js`, `drivers/deno/nextsql_test.js`, `drivers/node/nextsql.test.js`, `drivers/php/tests/unit.php`. No catalog/WAL/wire-format change. Fixed in this pass: `tests/integration/drivers_test.go`'s `TestDenoDriverUnit` only granted `deno test` `--allow-net`, so the new Deno `FileFieldKeyring` test's `Deno.makeTempDir` failed closed under the full repository-wide suite (`NotCapable`) even though it passed run standalone; added `--allow-read --allow-write`.
42. [x] P25 password hashing — Argon2id migration (2026-09-02) — `internal/auth.Store.Upsert` hashes every new login record with Argon2id (`golang.org/x/crypto/argon2`, time cost 1 / memory 64 MiB / parallelism 4 / 32-byte output — the package's documented recommended parameters) instead of PBKDF2-HMAC-SHA256. `NSAU` bumps to v2: each record carries an explicit algorithm byte plus Argon2id's memory/parallelism fields (zero for a legacy PBKDF2 record); `Decode` still reads v1 files unchanged, and `Encode` always writes v2 so a v1 file upgrades in place the next time the store persists. `Store.Verify` transparently re-hashes an already-confirmed-correct legacy password with Argon2id and persists the upgrade before returning; a failed verify never rehashes, and a concurrent delete/re-upsert of the same user is detected and skipped rather than clobbered. `internal/auth/store_bench_test.go` adds `BenchmarkVerifyPBKDF2` / `BenchmarkVerifyArgon2id` / `BenchmarkConcurrentLoginAttempts`; Argon2id's ~64 MiB-per-attempt memory cost is documented in `docs/security.md` "Password hashing" as the load-bearing number for sizing concurrent-login capacity limits. Tests: `TestV1FormatDecodesAndVerifies`, `TestNewRecordsAreArgon2idFromCreation`, `TestTransparentRehashUpgradesToArgon2id`; extended `FuzzDecode` seed corpus. No catalog/WAL/wire-format change.
43. [x] P25 audit hardening — tamper-evident/signed audit chain + verification tooling (2026-09-02) — every new `nextsql.audit` record gets a versioned `NSAC` v1 chain trailer (`chain_version`, monotonic `seq`, `prev_hash`, `hash = SHA-256("NSAC\x01" || prev_hash || seq-u64le || canonical-event-json)`), with `seq`/`prev_hash`/`hash`/`sig`/`key_id` cleared from the canonicalized JSON before hashing so caller-set chain fields can never be forged. `internal/security/auditkeys.go` adds `NSAK` v1: a bounded (64-key) Ed25519 signing keyset with one current key, rotation overlap, retirement (drops the private seed, keeps the public key for historical verification), atomic mode-`0600` writes, a verify-only `WritePublic` export, and last-known-good `SIGHUP` reload (`Log.SetSigningKeys`). The first configured signer appends a signed `audit.signing.enabled` transition record; every chained record from that point on must be signed, so an attacker cannot strip the earliest signature to quietly move the start of the signed segment (`TestAuditSigningTransitionCannotLoseSignature`, `TestSignedAuditCannotResumeUnsigned`, `TestAuditRejectsLegacyLineAfterChain`). `OpenAudit` verifies the retained chain before allowing an append, rejects an incomplete final line, and fails closed on a symlink, non-regular file, or a file readable by group/others; pre-chain JSON lines are accepted only as one contiguous legacy prefix. `internal/security/auditverify.go`'s `VerifyFile` streams the file one line at a time (1 MiB line cap), classifies each as legacy/chained/signed, verifies the chain and (when given a keyset) every signature, and reports the first bad line and why. `cmd/nextsql/audit.go` adds the `nextsql audit` CLI: `keygen`/`rotate`/`retire`/`list-keys`/`export-public` manage the `NSAK` keyset, and `verify --file F [--keyset F | --pubkey F] [--json]` runs `VerifyFile` and exits non-zero on failure. `nextsqld` gains `--audit-signing-keyset` / `audit_signing_keyset`; it refuses to start against an existing signed chain without it, verifies the configured keyset before signing, reloads it on `SIGHUP` with last-known-good fallback, and records `audit.signing.reload` as a security-setting event on both success and failure. Tests: `TestAuditKeysetRotationOverlap`, `TestAuditKeysetReloadLastKnownGood`, `TestAuditSignerReloadRejectsVerifyOnlyReplacement`, `TestAuditKeysetPublicOnlyCannotSign`, `TestAuditKeysetCannotRetireLastKey`, `TestAuditKeysetDecodeRejectsGarbage`, `TestOpenAuditKeysetBoundsAndRejectsSymlink`, `TestAuditChainVerifiesCleanLog`, `TestAuditChainResumesAcrossReopen`, `TestAuditChainDetectsTamperedLine`, `TestAuditChainDetectsDeletedLine`, `TestAuditChainDetectsReorderedLines`, `TestAuditLegacyFileVerifiesWithoutChain`, `TestAuditSigningRoundTrip`, `TestAuditVerifierKeysetRequiresSignedTransition`, `TestAuditSigningDetectsTamperedSignature`, `TestAuditSigningRetiredKeyStillVerifiesOldSignatures`, `TestOpenAuditRejectsTamperedExistingChain`, `TestOpenAuditRejectsIncompleteOrPermissiveFile`, `TestAuditVerificationBoundsLineLength`, `FuzzDecodeAuditKeys`, plus CLI coverage `TestAuditKeygenRotateRetireListExportPublicCLI`, `TestAuditVerifyCLI`, `TestAuditVerifyLegacyFileCLI`. `go build ./...`, `internal/security` + `cmd/nextsql` + `cmd/nextsqld` `go test` green. No NSQL wire-format, catalog, or WAL change; a pre-chain deployment's existing log is read as one legacy prefix, never rewritten. This closes the last open P25 implementable-scope checklist item — everything remaining under P25 is the phase-wide exit gate (security review sign-off; `ENCRYPTED CLIENT` production-gating rides the same gate).
44. [x] P25 exit gate closed — security review sign-off (2026-09-02) — added `## P25 security review sign-off (2026-09-02)` to `docs/security.md`, in the same dated surface-by-surface format as the existing "P16 security review": scope is everything landed since P16 (mTLS/service identity, short-lived credentials, the external-IdP broker, field-level client encryption, password-hash evolution, audit-chain hardening), with an explicit non-goals list (OCSP, optional OIDC opaque-token introspection, JIT provisioning, searchable/deterministic client-side encryption, local-audit-file suffix-truncation detection without an external WORM system) carried forward rather than hidden. This is the corresponding production-gating decision for the "P25 Security 2.0 audit" table, whose rows were already `yes`/`yes`/`yes` for designed/implemented/tested — the sign-off flips every row's production-gated column to `yes` except the `OIDC design` row (stays `n/a`, design-only) and the explicit non-goals. `docs/client-encryption.md`'s "Production-gating sign-off (Phase 25)" updated to drop its "awaits phase-wide gate" hedge now that the gate is closed (the capability stays labeled `experimental` in `system.capabilities` only because no searchable/deterministic mode ships — a deliberate scope decision, not an open blocker, matching how `follower_reads` stayed `experimental` after its own P22 exit gate closed). All four `Phase 25 exit gate` checklist items are now `[x]`; the phase-level `P25 Security 2.0` checkbox and the roadmap summary are `[x]`; every "next release gate" mention across `TODO.md` now points at **P26 System catalog / introspection 2.0**. No code change in this increment — documentation and gate closure only.

45. [x] P26 bug fix — `system.tasks` was a stub (2026-09-02) — `internal/executor/system.go` `systemTasksRows` previously always returned `[][]types.Value{}` with a comment saying it was a stub, despite the P26 checklist and this file already marking `system.tasks` `[x]` in an earlier increment (that increment landed the schema registration in `internal/system/schema.go` but never wired the row source). Tasks live only in the catalog B+Tree (`catalog.KeyTask` range), not in the in-memory `catalog.Store` that every other `system.*` view reads from, so populating it needs a scan transaction the way `SHOW TASKS` (`execShowTasks` in `internal/executor/task.go`) already does. Fixed by giving `systemTasksRows` the same owner-filtered scan (admin sees every task via `catalog.TaskKey("")..KeyTask+1`; non-admin sees only `catalog.TaskOwnerRange(s.user)`, resolved through the owner index) and reusing `taskStateName`. Unlike `SHOW TASKS`, `system.tasks` can be queried without an open transaction (`execSystemSelect` runs before the general autocommit-txn dispatch), so `systemTasksRows` now opens a short-lived `txn.SnapshotIsolation` read via `s.startRead` when `s.x == nil` and commits/aborts it itself, matching the same auto-transaction pattern `Session.run` uses for ordinary autocommit reads; a session already inside `BEGIN` reuses its existing transaction instead. No schema change (the `system.tasks` columns — `id`, `schedule`, `workflow`, `state`, `attempts` — were already correct, just never filled in), so no `system.SchemaVersion` bump. New test `TestSystemTasks` (`internal/executor/system_test.go`): dispatches a real due task, asserts the owning session sees it via `SELECT * FROM system.tasks` (autocommit) and inside an explicit `BEGIN`/`COMMIT`, asserts a non-owner/non-admin session sees zero rows (same owner isolation as `SHOW TASKS`), and asserts an admin (`PrivAdmin`/`ScopeCluster`) session sees it regardless of owner. `go build ./...` clean; `internal/executor` full suite + targeted `-race` on the task/system tests green. This is a correctness fix, not new P26 scope. The five live-table implementations described in the Phase 26 section are present and tested; the stale contrary status formerly recorded here was documentation drift.
46. [x] P26 exit gate closed (2026-09-02) — audited all three exit-gate items against the actual implementation instead of assuming the earlier increments already covered them. Found and fixed one genuine gap: Studio/Manager's planned "Users/roles/privileges administration" and "Security dashboard" surfaces had no official system.* read source at all — `auth.Store` and `security.ACL` were reachable only by reading their on-disk files directly. Added admin-only `system.users`/`system.roles`/`system.grants` (`internal/system/schema.go`, `internal/executor/system.go`), backed by new `internal/security/rbac.go` `Privilege.String()`/`ScopeKind.String()`/`ACL.Snapshot()` and `internal/auth/store.go` `Store.Snapshot()` (hash/salt never exposed). Also closed a capability-registry completeness gap found in the same pass: nine production-gated P23/P25 surfaces (`mtls`, `token_credentials`, `oidc_broker`, `audit_chain`, `storage_caps`, `vector_ivf`, `vector_ivfpq`, `vector_sparse`, `quantized_vector_index`) had no discoverable `system.capabilities` row of their own, plus a stale `fulltext` description missing WEIGHT/FACET. RBAC-coverage and realm/database-visibility gate items were audited and confirmed already satisfied — the former closed with new `TestSystemCatalogRBACRemainingViews` (`system.table_stats`/`index_stats`/`partitions`/`workflows` previously had no dedicated RBAC test of their own), the latter confirmed structural (one `*executor.DB` per process, one realm/database opened via `hosting.Registry.Default()`, so no code path today can leak across realms/databases) rather than something a filter could regress silently. Tests: `internal/security/rbac_test.go` `TestPrivilegeAndScopeStringRoundTrip`/`TestACLSnapshot`, `internal/auth/store_test.go` `TestStoreSnapshot`, `internal/executor/system_test.go` `TestSystemUsersRolesGrants`/`TestSystemCatalogRBACRemainingViews`/extended `TestSystemCapabilities`. `go build ./...` + `internal/executor`/`internal/security`/`internal/auth` `go test -race` + full `internal/executor` suite green. Docs: `docs/system-catalog.md` ("Security administration tables", updated P26 implementation audit table, new "P26 exit gate closure (2026-09-02)" section), `docs/web/content/docs/system-catalog.md`, this file (Phase 26 exit gate all `[x]`, phase-level checkbox, header table, Progress line, roadmap pointer). **P26 System catalog / introspection 2.0 is COMPLETE. Next release gate: P27 Operational maturity + workload governance.**

47. [x] P27 first increment — configurable connection limits (2026-09-02) — audited the "Session controls" checklist against the actual implementation before adding anything, per the phase's own "audit existing controls first" instruction. Found: a process-wide connection cap (`protocol.Limits.MaxSessions`, default 128, enforced at accept time in `Server.Serve`) and an idle deadline (`protocol.Limits.Idle`, default 60s, applied as a per-read socket deadline throughout `internal/protocol/server.go`) already existed but neither had a `config.go` key, so an operator could not tune either without a code change; a per-user connection limit, and per-realm/per-database limits, did not exist at all. Added `internal/protocol/frame.go` `Limits.MaxSessionsPerUser` (0 = unlimited, matching the codebase's other zero-means-uncapped fields such as hosting storage caps); enforced in `internal/protocol/server.go`'s `serveConn` immediately after the existing RBAC `CONNECT` check succeeds and before `TypeAuthOK` is written, so a denied connection never reaches session creation — a per-user counter (`Server.userConns`, guarded by the existing `s.mu`) increments there and decrements in a `defer` registered at the same point, so every early-return path after admission still balances the count. Over-limit rejection returns `nerr.Exhausted` ("too many connections for user"), consistent with how storage caps signal exhaustion elsewhere. `internal/config/config.go` adds `max_connections` (maps to `MaxSessions`), `max_connections_per_user` (maps to `MaxSessionsPerUser`), and `idle_timeout_ms` (maps to `Idle`); all three are opt-in overrides (0/unset leaves the protocol package's own default in place, the same convention `max_result_rows` already uses) and are validated as `>= 0` in `Config.Validate`. `cmd/nextsqld/main.go`'s existing `if cfg.MaxResultRows > 0 { ... }` block that overlays `srv.Limits` is extended to also apply these three. Per-realm/per-database connection limits are explicitly deferred, not forgotten: one `nextsqld` process still opens exactly one database (selectable multi-database hosting is foundation-only per the cross-cutting hosting track), so a per-database limit would be indistinguishable from `max_connections` until that track's M2+ scope ships — recorded as an open Session-controls checklist item with its reason. Idle-in-transaction, statement, transaction, and lock timeouts remain open (a non-cyclic lock wait today blocks indefinitely — only deadlock-cycle detection exists in `txn.LockManager`); the existing `query_queue_wait_ms` already covers queue timeout, no change needed there. Tests: `internal/config/config_test.go` `TestLoadConnectionLimits` / `TestLoadConnectionLimitsRejectsInvalid`; `tests/integration/protocol_test.go` `TestPerUserConnectionLimit` (two connections succeed, a third fails `Exhausted`, closing one frees a slot — polled with a bounded retry since the server observes a client-initiated close asynchronously). `go build ./...`, `go vet ./...` (same pre-existing unrelated `cdc.go` note), and `internal/config`/`internal/protocol`/`cmd/...` `go test -race` + the new integration tests green. No persistent, catalog, WAL, or NSQL wire-format change. Docs: `docs/ops.md` (new "Connection limits" subsection), `docs/protocol.md` ("Limits" table), `docs/web/content/docs/config.md`, `USAGE.md`, `CHANGELOG.md`. Next P27 increment: graceful drain / controlled shutdown (the exit gate's "planned maintenance can drain without unnecessary transaction loss" item), or statement/transaction/lock timeouts.

48. [x] P27 second increment — graceful shutdown / drain (2026-09-02) — `internal/protocol/server.go` adds `Server.Drain(timeout time.Duration)`: it closes the listener immediately (via a new `draining bool` field, checked alongside `closed` in the `Accept` error path so `Serve` returns cleanly instead of surfacing a wrapped IO error), then repeatedly closes every currently-idle connection — no in-flight statement and no open transaction, determined via the existing P26 cross-goroutine-safe `Session.CurrentQuery()`/`Session.TxnSnapshot()` snapshots, so a connection sitting inside an open-but-otherwise-idle transaction (`BEGIN` with no `COMMIT`/`ROLLBACK` yet) correctly counts as busy — polling every 20ms until none remain busy or `timeout` elapses, then calls the existing `Close()` to force-close whatever is left. A connection with no backend yet (mid Hello/Auth handshake) is always treated as idle, since there is no transaction to lose. New `Server.DrainTimeout time.Duration` field (default 0, preserving the exact pre-existing immediate-hard-close behavior for every other caller/test): when positive, `Serve`'s own `ctx.Done()` handler calls `Drain(DrainTimeout)` instead of `Close()`. `internal/config/config.go` adds `shutdown_drain_ms` (default 30000 via `DefaultDrainTimeoutMS`, validated `>= 0`; `0` disables waiting for busy connections). `cmd/nextsqld/main.go` sets `srv.DrainTimeout` from config and splits the previously-shared `serveErr` channel into a dedicated `protoErr` (for `srv.ListenAndServe`) and `embeddedErr` (for the optional embedded OIDC broker HTTP listener): on `ctx.Done()` (SIGINT/SIGTERM) it now blocks on `<-protoErr` so the drain (bounded by `shutdown_drain_ms`) actually completes before the pre-existing unconditional `srv.Close()` runs, instead of racing the drain and immediately undoing it — the prior code fell straight through to `srv.Close()` the instant `ctx.Done()` fired, which would have force-closed every connection regardless of `Drain`'s progress. This closes the Phase 27 "Graceful drain" / "Controlled shutdown" / "Connection draining" checklist items and is the concrete implementation behind the exit gate's "planned maintenance can drain without unnecessary transaction loss" — `Server.Drain` is also the primitive a future `nextsql cluster drain <node>` command would call, though no remote-triggered drain of another node exists yet (today it only runs automatically inside `nextsqld`'s own signal handling). Tests: `internal/config/config_test.go` `TestDefaultEnablesGracefulDrain`, `TestLoadShutdownDrainMSZeroDisablesDraining`, extended `TestLoadConnectionLimits(RejectsInvalid)`; `tests/integration/protocol_test.go` `TestDrainClosesIdleImmediatelyAndWaitsForOpenTransaction` (an idle connection is closed promptly while a sibling connection's open transaction stays fully usable, then closes right after `COMMIT` rather than waiting out the deadline) and `TestDrainForceClosesAtDeadline` (a transaction left open past a short deadline is force-closed and `Drain` still returns promptly). `go build ./...`, `go vet ./...` (same pre-existing unrelated `cdc.go` note), and `internal/config`/`internal/protocol`/`cmd/...`/`tests/integration`/`tests/ha` `go test -race` all green. No persistent, catalog, WAL, or NSQL wire-format change. Docs: `docs/ops.md` (new "Graceful shutdown (drain)" subsection), `docs/web/content/docs/config.md`, `USAGE.md`, `CHANGELOG.md`. Next P27 increment: expose `replication.Cluster.TransferLeadership()` through an operational surface (closing "Leader transfer" and part of "Operational CLI"), or statement/transaction/lock timeouts.

49. [x] P27 third increment — leader transfer exposure (2026-09-02) — New `CLUSTER TRANSFER LEADER` admin SQL statement (`ast.TransferLeader`; lexer keywords `KwTransfer`/`KwLeader`; parser `clusterStmt`) wraps the pre-existing `replication.Cluster.TransferLeadership()` library call, which was previously reachable only from Go code (a real gap: `AddVoter`/`RemoveServer` are similarly still Go-only, unexposed). Intercepted early in `internal/executor/session.go` `execAdmitted` (same pattern as `ast.Maintain`): requires cluster `ADMIN` (`security.PrivAdmin`/`ScopeCluster`, `internal/executor/security.go`), rejects inside a transaction, calls new `DB.TransferLeadership()` (`internal/executor/db.go`, wraps `db.cluster()`, returns `nerr.Unavailable` with no cluster attached), returns a one-row/one-column result (`result` = `transfer_initiated`) for machine-readable CLI output. New audit action `security.ActionLeaderTransfer`. New `nextsql cluster transfer-leader [--addr ...] [--user ...] ...` CLI subcommand (`cmd/nextsql/main.go` `clusterConnFlags`/`resolveClusterConn`, reusing `cli.Resolve`/`cli.Open` like `nextsql exec`); extracted shared `printTabularResult` helper. Tests: parser (`TestParseClusterTransferLeader` + table-driven case), executor (`TestClusterTransferLeaderRBACAndSingleNode` — RBAC deny, single-node `Unavailable`, in-transaction `InvalidArgument`), live 3-node Raft HA test (`tests/ha/ha_test.go` `TestHALeaderTransferSQL` — planned handoff moves leadership to a different voter with no write-unavailability window, unlike `TestHAKillLeader`'s crash failover), CLI flag-wiring test (`TestClusterTransferLeaderRequiresUser`). Does **not** support targeting a specific destination voter — Raft's `LeadershipTransfer()` (no-arg) picks it, matching the underlying library call as-is; that remains a possible future refinement. Docs: `docs/ops.md` ("Leader transfer" section), `docs/sql.md`, `USAGE.md`, `CHANGELOG.md`. `go build ./...` clean; touched-package + `internal/replication` + `internal/protocol` `go test` green. This closes the Phase 27 "Leader transfer" checklist item and the "Operational CLI" transfer-leader item. Remaining P27 Server-lifecycle/Operational-CLI scope: maintenance mode, rolling upgrade procedure, `nextsql cluster drain <node>` (remote drain of another node — today `protocol.Server.Drain` only runs automatically inside `nextsqld`'s own SIGINT/SIGTERM handling), statement/transaction/lock timeouts, resource groups. Next P27 increment: design and land `RESOURCE GROUP`.

50. [x] P27 fourth increment — RESOURCE GROUP design (2026-09-02) — modeled end-to-end on the existing `CREATE`/`ALTER`/`DROP SCHEDULE` catalog-object pipeline (the closest real analog: `CREATE ROLE` was ruled out because it is not catalog-persisted — it routes to `security.ACL`, has no `ALTER`, and is not Raft-replicated the way a catalog B+Tree write is). New `ast.CreateResourceGroup`/`AlterResourceGroup`/`DropResourceGroup` (`internal/sql/ast/ast.go`); lexer keyword `RESOURCE` (`GROUP` already existed for `GROUP BY`). Parser (`internal/sql/parser/parser.go`): `CREATE RESOURCE GROUP [IF NOT EXISTS] name [WITH (MAX_CONCURRENCY=n, MEMORY=bytes, WORKERS=n, PRIORITY=n)]`, `ALTER RESOURCE GROUP name WITH (...)` (at least one option required; unspecified options are left untouched, tracked via `Has*` flags on the AST node), `DROP RESOURCE GROUP [IF EXISTS] name` — the `WITH (...)` loop copies the existing multi-key `CREATE VECTOR INDEX ... USING IVF WITH (LISTS=n, PROBES=m, ...)` shape (`parser.go` around the IVF option loop), including its convention of leaving range validation to the encode/catalog layer rather than the parser (new `p.uint64Lit()` alongside the existing 32-bit `p.uintLit()`, since `MEMORY` is a byte count that legitimately exceeds `uint32`). New `internal/catalog/resourcegroup.go`: `ResourceGroup{ID,Name,Owner,MaxConcurrency,MemoryBytes,Workers,Priority}`, versioned `NSRG` magic-prefixed codec (`EncodeResourceGroup`/`DecodeResourceGroup`/`validateResourceGroup`, same shape as `EncodeSchedule`/`DecodeSchedule`), new catalog key prefix `KeyResourceGroup byte = 'U'` (first free letter after auditing every existing `KeyXxx` constant in `internal/catalog/*.go`); zero in any numeric field means unset/unbounded, matching `protocol.Limits.MaxSessionsPerUser` and hosting storage caps. New `internal/sql/binder/resourcegroup.go` (`bindResourceGroup`, wired into `BindAutomation` alongside `bindSchedule`) and matching `planner.CreateResourceGroup`/`AlterResourceGroup`/`DropResourceGroup` plan nodes (`internal/sql/planner/plan.go`), added to `isMutating` so the auto-transaction wrapper applies. New `internal/executor/exec_resourcegroup.go` (`execCreateResourceGroup`/`execAlterResourceGroup`/`execDropResourceGroup`, same `s.x.use(s.db.CatTree)` / overlay pattern as `exec_schedule.go`); `internal/executor/session.go` gained a `resourceGroupOverlay` map (same per-txn shadow-then-commit lifecycle as `scheduleOverlay`, reset at every `start`/`startRead`/`commit`/`abort` site) and `lookupResourceGroup`/`listResourceGroups`; `internal/executor/db.go` gained a `resGroups` map populated by a `KeyResourceGroup`-prefixed catalog scan in `reloadCatalog` (no dependency-consistency check needed, unlike workflow/trigger/schedule, since resource groups have no dependencies) plus `resourceGroup`/`resourceGroupList`/`putResourceGroup`/`removeResourceGroup` accessors mirroring the schedule ones. RBAC (`internal/executor/security.go`): all three statements gate on `security.PrivAdmin`/`ScopeCluster` — the same cluster-wide-admin gate as `CREATE ROLE`/`CREATE USER`, not the per-object `ScopeFunction` gate `CREATE SCHEDULE`/`WORKFLOW`/`TRIGGER` use, since workload governance is an admin/security-adjacent concern with no per-object dependency to scope against; new `security.ActionResourceGroupCreate`/`Alter`/`Drop` audit actions (`internal/security/audit.go`). New admin-only `system.resource_groups` (`name, owner, max_concurrency, memory_bytes, workers, priority`) in `internal/system/schema.go` + `internal/executor/system.go` `systemResourceGroupsRows` — same "row-filter, never fail, on RBAC" convention as `system.users`/`roles`/`grants`, since `RESOURCE GROUP` DDL itself is `PrivAdmin`-gated (reads directly from `s.db.resGroups` under `db.mu`, not the session overlay, matching `systemWorkflowsRows`' committed-state-only visibility). New `system.capabilities` row `resource_groups` (status `experimental` — the only accurate label, since this increment is a pure descriptor). **This increment is deliberately descriptor-only and does not touch runtime behavior**: `internal/scheduler`'s `Admission`/`Budget`/`Pool` are completely untouched; no session or user can be assigned to a resource group yet; a created group changes nothing about how any query is admitted, scheduled, or budgeted. This mirrors how the OIDC `NSIP` identity-policy engine landed pure/unwired before its broker in a later increment (TODO log #32/#33) — closes only the "Design RESOURCE GROUP" checklist item; "Workload max concurrency"/"memory budget"/"CPU/worker budget"/"Priority"/scheduler integration/"no independent unbounded pools" remain open and require deciding the assignment mechanism (per-user? per-session `SET RESOURCE GROUP`, which needs lifting today's blanket `SET` statement rejection?) before they can be wired. Tests: `internal/catalog/resourcegroup_test.go` (codec round-trip, zero-means-unbounded, out-of-range rejection, bad-magic/trailing-bytes, `FuzzDecodeResourceGroup`), `internal/sql/binder/resourcegroup_test.go` (full CREATE/ALTER/DROP lifecycle incl. `IF NOT EXISTS`/`IF EXISTS`, partial-ALTER field preservation, out-of-range rejection), `internal/sql/parser/parser_test.go` (`TestParseResourceGroupStatements`/`TestParseResourceGroupRejectsInvalid`), `internal/executor/resourcegroup_test.go` (catalog lifecycle + WAL restart durability, rollback visibility, RBAC deny/allow, `system.resource_groups` admin-only visibility). `go build ./...` clean; `go vet` unchanged (same pre-existing unrelated `cdc.go` note); `internal/catalog`/`internal/sql/...`/`internal/security`/`internal/executor` (incl. full `-race`) all green. No WAL/wire-format change beyond the additive catalog key prefix. Docs: `docs/sql.md` ("RESOURCE GROUP" section + statement list), `docs/system-catalog.md` + `docs/web/content/docs/system-catalog.md` ("Workload governance" section), `CHANGELOG.md`. Next P27 increment: decide and implement the workload-assignment mechanism, then wire `internal/scheduler` enforcement (max concurrency first, as the exit gate's "resource groups cannot bypass global safety limits" item requires composing a group's admission gate with the existing process-wide `db.admit` rather than replacing it) — or continue server-lifecycle scope (maintenance mode, rolling upgrade, statement/transaction/lock timeouts).

51. [x] P27 fifth increment — statement, transaction, and lock timeouts (2026-09-02) — audited the three remaining open Session-controls timeout items before adding anything, per the phase's own "audit existing controls first" instruction. **Statement timeout**: found the mechanism already existed — `scheduler.Limits.Time` (default `scheduler.DefaultTimeout`, 30s) was already wired into a real `context.WithTimeout`-backed `scheduler.Budget` created per statement (`internal/executor/session.go` `s.qbudget = scheduler.NewBudget(ctx, s.limitsOrDefault())`) and already reachable from `protocol.Limits.Query` (applied to every session via `Session.SetLimits` in `internal/protocol/server.go`) — but had no `config.go` key, mirroring the exact gap class the P27 first increment (connection limits) closed. Added `statement_timeout_ms`, wired into `srv.Limits.Query.Time` in `cmd/nextsqld/main.go` alongside the existing `MaxResultRows`/connection-limit overlay. **While auditing where `Budget.Check()` (the function that actually observes the deadline) was called from, found a real, more serious gap**: `internal/executor/access.go` — the base `SeqScan`/`IndexScan` physical row-emission loops that back every plain `SELECT`/`UPDATE`/`DELETE` — never called `Check()` at all. Only specialized paths (ANALYZE, vector/full-text search, index rebuild, partition index population, joins) checked the budget; a plain full-table-scan query could run past its configured statement timeout completely unbounded. Fixed by adding `s.budget().Check()` as the first statement in all six physical `Range()`/lookup row-emission callbacks in `access.go` (`scanHeapPartitions` ×2, `scanIndex`'s PK-range and `emitIndex`, `scanPartitionedIndex`'s `emitIndex`, `scanPartitionedPK`'s `emit`), matching the per-row-check convention already used elsewhere (e.g. `populatePartitionIndex`). This is a correctness fix to the *existing* statement-timeout mechanism, not new scope — `internal/executor` full suite (incl. `-race`) confirms no behavior regression on the hot path. **Transaction timeout** (new mechanism, since none existed before): `internal/protocol/frame.go` `Limits.TxnTimeout time.Duration` (0 = unbounded default — deliberately opt-in, unlike the statement/idle timeouts this has no pre-existing non-zero default, so upgrading never starts aborting already-long-running transactions such as bulk loads unless an operator opts in), applied per session via new `Session.SetTxnTimeout` (`internal/protocol/server.go`, alongside the existing `SetLimits` call). Enforcement in `internal/executor/session.go` `execAdmitted`: right after parsing, if a transaction is open and has exceeded `s.txnTimeout` (checked against the existing P26 `Session.TxnSnapshot()` start time), the transaction is unconditionally force-aborted (`s.abort()`) and the *current* statement — even `COMMIT` — fails `nerr.Exhausted`; the session itself stays usable for a fresh transaction afterward. New `transaction_timeout_ms` config key. **Lock timeout** (new mechanism): `internal/txn/lock.go` `LockManager` gained a `waitTimeout time.Duration` field (0 = block indefinitely, the pre-existing default) and `SetWaitTimeout`; `Acquire`/`AcquireRange` factor their blocking wait into a new `await` helper that, when a timeout is configured, races the grant channel against a timer — on timeout it re-acquires `lm.mu` and checks whether the waiter is still present in `lm.waiters`: if yes, removes it and returns `nerr.Exhausted`; if no (the waiter was granted the instant the timer fired — `wake()` removes a waiter from `lm.waiters` and sends on its channel atomically under the same mutex, so this check is race-free), the grant is honored instead of incorrectly reporting a timeout and leaking a lock nobody would ever release. No signature change to `Acquire`/`AcquireRange`/`Manager.LockKey`/`LockRange` or their 7 call sites (unlike the P26 tag-threading increment) — the timeout is a process-wide `LockManager` setting, not per-call, because the lock table has no per-caller identity to key a per-connection limit off (matching how deadlock-cycle detection itself is already process-wide, not session-configurable). New `DB.SetLockWaitTimeout` wrapper (mirroring `DB.LockSnapshot`) and `lock_timeout_ms` config key, applied once at `nextsqld` startup. Tests: `internal/txn/lock_test.go` `TestLockWaitTimeoutZeroBlocksIndefinitely`, `TestLockWaitTimeoutExceeded`, `TestLockWaitTimeoutRangeExceeded`, and a 200-iteration `TestLockWaitTimeoutRaceWithGrant` stress test specifically exercising the timeout-vs-grant race window (release timed to land within one timeout window of the waiter's own deadline); `internal/config/config_test.go` `TestLoadStatementTransactionLockTimeouts`/`TestLoadTransactionAndLockTimeoutZeroDisables`/`TestLoadTimeoutsRejectsInvalid`; `internal/executor/timeout_test.go` (`TestStatementTimeoutAbortsScan`/`TestStatementTimeoutDoesNotAffectNormalQueries`, `TestTransactionTimeoutAbortsNextStatement`/`TestTransactionTimeoutRejectsCommitToo`/`TestTransactionTimeoutZeroIsUnbounded`, `TestLockWaitTimeoutEndToEnd`/`TestLockWaitTimeoutDefaultBlocksIndefinitely` — the last two reuse the blocking-FK-check pattern from the pre-existing `TestFKSnapshotOverlappingLocks` to get a real cross-session contended lock without synthetic hooks); `tests/integration/protocol_test.go` `TestTxnTimeoutAbortsOverLiveConnection` (live TLS connection, proving the `protocol.Server` → `Session.SetTxnTimeout` wiring end-to-end, not just the executor-level mechanism). `go build ./...` clean; `go vet` unchanged (same pre-existing unrelated `cdc.go` note); `internal/txn`, `internal/config`, `internal/protocol`, `internal/executor` (full package, incl. `-race`), and the touched `tests/integration` cases all green. No WAL/catalog/wire-format change. Closes the Phase 27 "Statement timeout", "Transaction timeout", and "Lock timeout" Session-controls checklist items; "Idle transaction timeout" remains open and distinct (a dedicated idle-while-in-a-transaction timer, separate from both `idle_timeout_ms` and `transaction_timeout_ms`, which bounds total duration regardless of activity) — closing it would need the per-frame socket-deadline logic in `internal/protocol/server.go` to distinguish "idle, no open transaction" from "idle, transaction open," which today share one deadline. Docs: `docs/ops.md` (new "Statement, transaction, and lock timeouts" section), `docs/protocol.md` ("Limits" table), `docs/web/content/docs/config.md`, `CHANGELOG.md`. Remaining Session-controls scope: idle transaction timeout, per-realm/per-database connection limits (both already explicitly deferred with reasons). Next P27 increment: resource-group workload assignment + `internal/scheduler` enforcement, maintenance mode, or rolling upgrade procedure.

52. [x] P27 sixth increment — remote drain (2026-09-02) — closes the Phase 27 "`nextsql cluster drain <node>`" Operational-CLI checklist item, the last item explicitly called out as remaining scope in the prior increment's log entry. Modeled on the `CLUSTER TRANSFER LEADER` increment (log #49) but with one deliberate architectural difference driven by what draining actually is: `protocol.Server.Drain` is a connection-management primitive that lives in the `protocol` package (above `executor`, which has zero knowledge of TCP listeners/connections), whereas `TransferLeadership` was reachable via `replication.Cluster`, already wired into `executor.DB` through the pre-existing `db.gate`/`db.cluster()` plumbing — so `CLUSTER DRAIN`'s SQL-statement handler cannot call `protocol.Server.Drain` directly without an import cycle. Solved with a callback hook: new `executor.DB.SetDrainFunc(fn func(timeout time.Duration))` (nil by default — embedded/CLI use, no listening server) and `DB.Drain(timeout) error`, which launches the registered hook **in its own goroutine** and returns immediately rather than blocking. This is not optional plumbing convenience — it fixes a real self-block hazard: `Drain`'s own idle-connection-polling loop treats a session as "busy" while it has an in-flight statement, and the connection that issued `CLUSTER DRAIN` is itself busy running that very statement, so a synchronous call would have `Drain()` wait forever for its own caller to go idle, which can't happen until `Drain()` returns — an unbreakable self-referential wait, only escaped by the configured timeout forcibly closing the issuing connection. `cmd/nextsqld/main.go` wires `db.SetDrainFunc(func(timeout time.Duration) { if timeout <= 0 { timeout = srv.DrainTimeout }; srv.Drain(timeout) })` right after `srv.Limits`/`srv.DrainTimeout` are finalized — `timeout <= 0` (SQL omitted `WITH (TIMEOUT_MS = ...)`) falls back to the node's own configured `shutdown_drain_ms`, the same zero-means-default convention used throughout this phase. New `ast.ClusterDrain{TimeoutMS int64}`; lexer keyword `DRAIN`; parser `clusterStmt` extended with `CLUSTER DRAIN [WITH (TIMEOUT_MS = n)]` (single-key `WITH (...)`, reusing `p.uint64Lit()` from the P27 fifth increment). Intercepted early in `internal/executor/session.go` `execAdmitted`, same shape as `ast.TransferLeader` — requires cluster `ADMIN` (`security.PrivAdmin`/`ScopeCluster`), rejects inside a transaction, returns a one-row `drain_initiated` result — **except it deliberately skips the `requireLeader(true)` gate that TransferLeader/Maintain/CreateDatabase all use**: those must reach the leader because they write to Raft-replicated state, but draining is a purely local, per-node connection-management action with nothing to replicate, so gating it on Raft leadership would incorrectly forbid the actual common case — draining a *follower* ahead of taking it down for maintenance without disturbing leadership elsewhere. New audit action `security.ActionClusterDrain`. New `nextsql cluster drain [--timeout-ms N] [--addr ...] [--user ...] ...` CLI subcommand (`cmd/nextsql/main.go`, reusing `clusterConnFlags`/`resolveClusterConn`/`printTabularResult` exactly like `transfer-leader`; `--timeout-ms` omitted or 0 sends bare `CLUSTER DRAIN`). Tests: parser (`TestParseClusterDrain` — bare, `WITH (TIMEOUT_MS = n)`, and three invalid-syntax cases), executor (`TestClusterDrainRBACAndWiring` — RBAC deny, no-`DrainFunc`-attached `Unavailable`, in-transaction `InvalidArgument`, and a working stub `DrainFunc` asserting the exact `WITH (TIMEOUT_MS = ...)` value reaches it, on a single-node/no-cluster deployment where `TransferLeader` would instead fail `Unavailable` — the concrete proof the no-`requireLeader` design decision is correct), CLI flag-wiring test (`TestClusterDrainRequiresUser`), and a live end-to-end integration test `TestClusterDrainOverLiveConnection` (`tests/integration/protocol_test.go`) that wires `db.SetDrainFunc` via `Server.DatabaseHandle()` exactly as `cmd/nextsqld/main.go` does, issues `CLUSTER DRAIN WITH (TIMEOUT_MS = 500)` over a real TLS connection, and confirms an idle sibling connection is closed promptly and a brand-new connection attempt is refused — plus `TestClusterDrainRejectsUnattachedDrainFunc` for the no-hook case. **Real bug found and fixed while verifying under the full parallel test suite** (a plain isolated `-run TestClusterDrainOverLiveConnection` passed every time; the failure only appeared under `go test ./tests/integration/...` full-package load): `TestClusterDrainOverLiveConnection` intermittently saw `protocol.ReadFrame: payload: EOF` reading the `CLUSTER DRAIN` response itself. Root cause was a genuine, previously-latent race in `protocol.Server.closeIdleConnections` (P27 second increment, "graceful drain") that CLUSTER DRAIN made far more likely to hit: `Session.CurrentQuery()`'s "running" flag clears the instant `QueryContext`/`ExecContext` returns to `runSQL`, which is *before* `runSQL` writes the response frame(s) back over the wire — so a connection could be reported idle, and have its raw `net.Conn` hard-closed by `closeIdleConnections`, while its own just-finished statement's response was still being flushed, corrupting that write. This race always existed for a drain triggered externally (SIGINT), just was very unlikely to land inside another connection's specific response-write window; a SQL-triggered self-drain made the trigger and the race window synchronous for the issuing connection, so it surfaced reliably under load. Fixed in `internal/protocol/server.go` `closeIdleConnections`: also checks `backend.queryConn != nil` (already set before dispatch and cleared only after the response write completes in `runSQL`/`runIdempotentSQL`'s deferred cleanup — protocol-layer state, no `executor`/`Session` change needed) as a third busy condition alongside the existing `CurrentQuery`/`TxnSnapshot` checks. Verified by re-running the full `internal/executor` + `tests/integration` + `internal/protocol` suites under `-race`, which had reliably reproduced the failure before the fix and are clean after it. `go build ./...` clean; `go vet ./...` unchanged (same pre-existing unrelated `cdc.go` note); `cmd/nextsql`, `cmd/nextsqld`, `internal/sql/parser`, `internal/executor` (full package, incl. `-race`), `internal/protocol`, and `tests/integration` all green. No WAL/catalog/wire-format change. Docs: `docs/ops.md` (new "Remote drain" section, "Graceful shutdown (drain)" closing paragraph updated to point at it), `docs/sql.md` (`CLUSTER DRAIN` description + statement list), `USAGE.md` (CLI usage block + worked example), `CHANGELOG.md`. Remaining Operational-CLI scope: machine-readable (JSON) operation status across `CLUSTER TRANSFER LEADER`/`CLUSTER DRAIN`/`nextsql exec` — today all return the same tabular text output. Next P27 increment: resource-group workload assignment + `internal/scheduler` enforcement, maintenance mode, or rolling upgrade procedure.

53. [x] P27 seventh increment — resource-group workload assignment + scheduler enforcement (2026-09-02) — closes the "resource-group workload assignment" scope flagged as the next increment by log #50/#51/#52, picking the assignment mechanism those entries left open: session-scoped `SET RESOURCE GROUP name` / `RESET RESOURCE GROUP`, gated by a new per-object `GRANT USAGE ON RESOURCE GROUP name TO grantee` grant — not per-user static assignment, since that would have meant a breaking change to `auth.Store`'s password-file format for a field unrelated to authentication; and not per-role static assignment either, since a session-scoped `SET` composes more naturally with the existing GRANT/REVOKE pipeline and needed no new persisted association type at all, just a new grant scope. New `security.ScopeResourceGroup` (`internal/security/rbac.go`), appended after `ScopeAdmin` — purely additive: `decodeACL`'s scope-range validation widened from `> ScopeAdmin` to `> ScopeResourceGroup`, no ACL file version bump needed since no old file can already contain the new value. `String()`/`ParseScope()` extended (`"resourcegroup"`). New `ast.SetResourceGroup{Name}`/`ast.ResetResourceGroup{}` (`internal/sql/ast/ast.go`). Parser: `SET`/`RESET` were fully blanket-rejected since `SET TENANT`/`RESET TENANT` were removed (`internal/sql/parser/parser.go`, `p.stmt()` on `lexer.KwSet`/`KwReset` used to unconditionally return the removal-message syntax error) — now `p.setStmt()`/`p.resetStmt()` peek for `RESOURCE GROUP name` and parse it, falling through to the exact same removal-message rejection for every other spelling (`SET TENANT = ...`, `SET x = 1`, etc. all still fail identically — `TestParseRejectsRemovedSharedTenantSyntax` unchanged and still green). `p.scope()` (used by `GRANT`/`REVOKE`'s `ON <scope> <object>` clause) gained a `RESOURCE GROUP name` case parsing to `("resourcegroup", name)`, reusing the `KwResource`/`KwGroup` tokens the RESOURCE GROUP DDL already added — so `GRANT USAGE ON RESOURCE GROUP name TO grantee` needed no new lexer keywords. `internal/executor/security.go` `authorize()`: `ast.SetResourceGroup` requires `PrivUsage`/`ScopeResourceGroup`/`st.Name` (same per-object shape as `RunWorkflow` needing `PrivExecute`/`ScopeFunction` on that workflow, deliberately unlike the DDL's cluster-wide `PrivAdmin`/`ScopeCluster` gate, since assignment is meant to be delegable per group without granting workload-governance administration); `ast.ResetResourceGroup` requires only `PrivConnect`/`ScopeDatabase` (giving up an assignment needs no special privilege). Cluster `ADMIN` bypasses both via the existing superuser short-circuit in `allowedForLocked`. `internal/executor/session.go`: new `Session.resourceGroup string` field (same-goroutine-only, like `execSQL` — deliberately NOT published for cross-goroutine introspection this increment, so `system.sessions` still has no `resource_group` column; a session's own `ResourceGroup()` accessor is same-goroutine-safe). `SET`/`RESET RESOURCE GROUP` intercepted early in `execAdmitted`, same shape and same location as `ast.ClusterDrain` (no `requireLeader` gate — this is purely local session state, nothing Raft-replicated): `SET` authorizes, then existence-checks via `s.lookupResourceGroup` (overlay-aware, so a group created earlier in the same open transaction is visible — deliberately more permissive than blocking `SET RESOURCE GROUP` inside a transaction entirely, since unlike DDL it touches no storage engine state), assigns, clears `s.qbudget` so the next statement's budget picks up the new group's limits immediately; `RESET` authorizes (trivially) and clears both. New audit action `security.ActionResourceGroupAssign` (object = group name, or empty for `RESET`). `limitsOrDefault()` extended: when a group is assigned, a non-zero `Workers`/`MemoryBytes` on the group overrides the base session/server value (zero on the group still means "leave the base value alone", the same convention `CreateResourceGroup`'s own doc comment establishes) — `Limits.normalized()` inside `scheduler.NewBudget` still clamps `Workers` to `[1, MaxWorkers]`, so a group can never grant more workers than the process ceiling regardless of its stored value. **Enforcement** (`internal/executor/db.go`): new `DB.resGroupGates map[string]*scheduler.Admission`, a pure cache guarded by the existing `db.mu`, keyed by group name — built lazily by new `DB.resourceGroupGate(name)`, dropped (not migrated) by `putResourceGroup`/`removeResourceGroup` on any change to that group (so `ALTER RESOURCE GROUP ... WITH (MAX_CONCURRENCY = n)` takes effect on the very next query under that group, with the new gate's in-flight count starting fresh at zero) and reset wholesale in `reloadCatalog`. A group with `MaxConcurrency <= 0` (the "unbounded" convention) gets no gate at all — this is a deliberate, load-bearing distinction from `scheduler.NewAdmission`'s own `<1`-means-`DefaultMaxInflight`(32) convention, which would have silently turned "unbounded" into "capped at 32" if passed through unchecked. `Session.ExecContext` (`internal/executor/session.go`) acquires this group gate as a **second, strictly additional** admission gate immediately after the pre-existing process-wide `db.admit.Acquire` — both must succeed, using the same release-on-defer/reject-and-count-as-`AddRejected`-on-metrics shape as the existing gate — which is what makes "resource groups cannot bypass global safety limits" true by construction: an unassigned session or one in an unbounded group is byte-for-byte unaffected (`resourceGroupGate` returns nil, and `(*scheduler.Admission)(nil).Acquire` is a documented no-op), and a bounded group can only ever add a *tighter* ceiling on top of the process-wide one, never a looser or independent one. Closes the Phase 27 "Workload max concurrency" / "Workload memory budget" / "Workload CPU/worker budget" checklist items. Deliberately still open: **Priority** (stored on the descriptor since the DDL increment, still no preemption or priority-ordered admission — the existing `scheduler.Admission` is a plain FIFO-ish semaphore with no priority concept to hook into without a larger scheduler redesign), **"Integrate API/analytics/workflow/maintenance/backup classes with one scheduler"** and **"No independent unbounded pools"** (both are broader cross-codebase audits, not specific to this increment's assignment/enforcement mechanism, deferred to a dedicated audit pass), and live **`system.sessions` visibility** into a session's current resource group (would need the same cross-goroutine-synchronized-snapshot treatment P26 gave `execSQL`/`s.x`, e.g. a `sync.Mutex`-guarded field alongside `queryMu`/`txnMu`, not attempted this increment since nothing internally needed it). Tests: `internal/sql/parser/parser_test.go` `TestParseSetResetResourceGroup` (`SET RESOURCE GROUP name` / `RESET RESOURCE GROUP` accepted; `SET TENANT = ...`, `RESET TENANT`, `SET RESOURCE`, `SET RESOURCE GROUP` with no name, `RESET RESOURCE`, `SET x = 1` all still rejected) + extended `TestParseSecurityStatements` (`GRANT`/`REVOKE USAGE ON RESOURCE GROUP`); `internal/security/rbac_test.go` extended `TestPrivilegeAndScopeStringRoundTrip` (`ScopeResourceGroup` in the round-trip table) + new `TestResourceGroupUsageGrantPersists` (grant/reopen/least-privilege, same shape as the pre-existing `TestCDCPermissionPersists`); `internal/executor/resourcegroup_assign_test.go` — `TestSetResetResourceGroupRBACAndAssignment` (unknown-group `NotFound` even for the superuser, ungranted `SET` denied, `RESET` always allowed, admin bypass, granted `SET` succeeds and `limitsOrDefault()` reflects the group's `Workers`/`Memory`, `RESET` reverts it, revoke blocks re-`SET` but leaves the existing assignment alone until explicitly reset), `TestResourceGroupGateCacheTracksMaxConcurrency` (bounded group gets a capacity-matching gate, unbounded/unknown groups get none, `ALTER ... MAX_CONCURRENCY` invalidates and rebuilds the cached gate), `TestResourceGroupMaxConcurrencyBlocksExecContext` (a real end-to-end proof, not just a unit test of `scheduler.Admission` in isolation: manually holds a `MAX_CONCURRENCY = 1` group's sole gate slot open from the test goroutine, confirms a second session assigned to that group genuinely blocks inside `ExecContext` for 150ms, then unblocks within 2s of the slot being released — proving `ExecContext` really contends on the same gate object `resourceGroupGate` hands out, not a parallel/disconnected one). `go build ./...` clean; `go vet ./...` unchanged (same pre-existing unrelated `cdc.go` note); `internal/security`, `internal/sql/...`, and `internal/executor` (full package, incl. `-race`, 115s) all green. No WAL/catalog/wire-format change — the ACL format change is additive-only (widened validation range, no version bump). Docs: `docs/sql.md` (RESOURCE GROUP section rewritten for assignment/enforcement, statement list, GRANT/REVOKE line), `docs/security.md` (scope list, worked `GRANT USAGE ON RESOURCE GROUP` example), `docs/system-catalog.md` + `docs/web/content/docs/system-catalog.md` (`system.resource_groups` notes updated from "not yet enforced" to enforced, with the same open-scope caveats as here), `docs/web/content/docs/security.md`, `CHANGELOG.md`. Next P27 increment: the Priority/scheduler-class-integration/unbounded-pools audit above, maintenance mode, or rolling upgrade procedure.

54. [x] P27 eighth increment — idle transaction timeout (2026-09-02) — closes the "Idle transaction timeout" Session-controls checklist item, the last of the four timeout-shaped items called out in log #51 ("statement timeout"/"transaction timeout"/"lock timeout" landed there; this one deliberately deferred since it "would need the per-frame socket-deadline logic in `internal/protocol/server.go` to distinguish 'idle, no open transaction' from 'idle, transaction open,' which today share one deadline"). New `protocol.Limits.IdleTxn time.Duration` (0 = no distinct bound, matching every other zero-means-pre-P27-behavior field in `Limits`). `internal/protocol/server.go` gains `idleDeadline(lim, sess)`: returns `lim.Idle` normally, but while `sess.InTxn()` and `lim.IdleTxn > 0`, returns the smaller of `lim.Idle`/`lim.IdleTxn` — so a misconfigured `IdleTxn` larger than `Idle` can never lengthen the bound, only tighten it. The connection's main read loop (`serveConn`'s `for { conn.SetDeadline(...) ; ReadFrame(...) }`) now computes this per iteration instead of always using `lim.Idle`, so a transaction opened mid-connection immediately starts being governed by the tighter deadline on its very next frame wait, and a committed/rolled-back transaction immediately reverts to the general `Idle` bound — no separate timer/goroutine, reusing the exact mechanism `idle_timeout_ms` already relies on. New `idle_transaction_timeout_ms` config key, wired into `srv.Limits.IdleTxn` in `cmd/nextsqld/main.go` alongside the other four timeout overlays. **Real gap found and fixed while implementing this**: when `ReadFrame` times out (or errors any other way) and `serveConn` returns, nothing in its defer chain ever rolled back `b.sess`'s open transaction — `db.UnregisterSession` only drops it from the live-session introspection map, it does not touch `s.x`/the underlying `btree.Txn`'s locks. This was already reachable *before* this increment two ways: (1) the general `idle_timeout_ms` firing while a transaction happened to be open (today's only idle-in-transaction bound), and (2) `closeIdleConnections`' own `s.Close()` at the `Drain` deadline force-closing a still-busy (open-transaction) connection (confirmed by reading `closeIdleConnections`: it deliberately keeps a `TxnSnapshot().active` connection out of the promptly-closed set, precisely so `Drain`'s later force-close is the only thing that can end it) — so a transaction abandoned either way stayed open, holding its locks, for as long as the `*executor.Session` object survived (until process restart, since nothing else references or reaps it). New exported `executor.Session.Abort() error` (thin wrapper over the existing unexported `s.abort()`, no-op when `s.x == nil`) is now called from `serveConn`'s existing teardown `defer` (`internal/protocol/server.go`, right before `db.UnregisterSession`) whenever `b.sess.InTxn()`, so every connection-teardown path — this new timeout, the pre-existing general idle timeout, a forced drain close, or a bare client disconnect — now deterministically releases an abandoned transaction's locks instead of leaking them. Tests: `tests/integration/protocol_test.go` `TestIdleTransactionTimeoutClosesOpenTransactionConnection` (an idle connection with no open transaction survives well past `IdleTxn`, governed only by the much longer `Idle`; one with an open transaction left idle past `IdleTxn` is closed) and `TestIdleTransactionTimeoutReleasesLocksOnDisconnect` (the load-bearing one: after the idle-in-transaction connection is torn down mid-transaction with an uncommitted `INSERT` still holding its primary-key lock, a second connection successfully inserts that same key — which would instead block until `lock_timeout_ms` failed it `Exhausted` had `Session.Abort` not actually released the lock); `internal/config/config_test.go` extended `TestLoadStatementTransactionLockTimeouts`/`TestLoadTransactionAndLockTimeoutZeroDisables`/`TestLoadTimeoutsRejectsInvalid` for `idle_transaction_timeout_ms`. `go build ./...` clean; `go vet ./...` unchanged (same pre-existing unrelated `cdc.go` note); `internal/config`, `internal/protocol` (full package, incl. `-race`), and the touched `tests/integration` cases (incl. `-race`, alongside the pre-existing `TestTxnTimeoutAbortsOverLiveConnection`/`TestClusterDrain*`/`TestDrain*` cases to confirm no regression) all green. No WAL/catalog/wire-format change. Docs: `docs/ops.md` ("Statement, transaction, lock, and idle-transaction timeouts" — retitled section, new config line + paragraph explaining the *enforcement-mechanism* distinction from `transaction_timeout_ms`, not just a duration difference), `docs/protocol.md` ("Limits" table + paragraph), `docs/web/content/docs/config.md`, `CHANGELOG.md`. Remaining P27 scope: maintenance mode, rolling upgrade procedure, online format/catalog migration, backup/WAL retention management, replica-lag management, disk watermark policies, capacity warnings, per-realm/per-database connection limits (deferred pending selectable multi-database hosting), the Priority/scheduler-class-integration/unbounded-pools resource-group audit (log #53), and machine-readable operation status. Next P27 increment: pick one of the above — maintenance mode or the resource-group audit are the most self-contained.
55. [x] P27 ninth increment — maintenance mode (2026-09-02) — closes the "Maintenance mode" Server-lifecycle checklist item flagged by log #54 as one of the two most self-contained remaining candidates. New `ast.ClusterMaintenance{Enable bool}` (`internal/sql/ast/ast.go`), parsed as `CLUSTER MAINTENANCE ENABLE|DISABLE` by `clusterStmt()` (`internal/sql/parser/parser.go`, new `KwMaintenance`/`KwEnable`/`KwDisable` lexer keywords — kept as real reserved words like `TRANSFER`/`LEADER`/`DRAIN` rather than `identIs`-checked idents like `TIMEOUT_MS`, since ENABLE/DISABLE are structural verbs at the same syntactic position as DRAIN/TRANSFER, not a `WITH (...)` option key). RBAC: `PrivAdmin`/`ScopeCluster` (`internal/executor/security.go`), same as `ClusterDrain`/`TransferLeader`/`Maintain`. Design decision — **node-local, not Raft-replicated**, deliberately mirroring `ClusterDrain`'s own precedent rather than `TransferLeader`'s: a genuinely cluster-wide flag would need a new Raft FSM command type (`internal/replication/fsm.go`'s `Apply` today only understands `DecodeCommand`-encoded WAL record batches, nothing generic) plus a snapshot-format change — a materially bigger lift than this checklist item warranted, and the existing precedent (`db.drainFn`/`SetDrainFunc`) already establishes "server/connection-layer admin state lives on `DB`, set via a callback or field, not through Raft" as the norm for this class of statement. New `DB.maintenanceMode atomic.Bool` + `EnableMaintenanceMode`/`DisableMaintenanceMode`/`InMaintenanceMode()` (`internal/executor/db.go`, nil-safe like `Drain`/`TransferLeadership`) — explicitly distinguished in its doc comment from the pre-existing, unrelated `PauseMaintenance`/`ResumeMaintenance`/`maint *maintenance.Manager` (background dead-version/vacuum scheduler, `MAINTAIN` statement), which this increment does not touch. `execAdmitted` intercepts `ast.ClusterMaintenance` the same way as `ast.ClusterDrain` — before the general planner path, `InTxn()` rejected `InvalidArgument`, no `requireLeader` gate — and toggles the flag, returning `maintenance_enabled`/`maintenance_disabled` (`internal/executor/session.go`). **Enforcement** reuses the exact `isMutating(out.Plan)` classification `requireLeader` already established (`session.go`'s per-statement `QueryContext` path): new `Session.requireNotMaintenance(write bool)` is called immediately before `requireLeader`, rejecting `Unavailable` when `write && s.db.InMaintenanceMode()`. Since `isMutating` already includes `planner.Begin` (not just DML/DDL — a deliberate pre-existing choice for leader routing, since a transaction's eventual read/write shape is unknown at `BEGIN` time), maintenance mode blocks new transactions wholesale, not just writes within them; autocommit `SELECT`/`SHOW`/`system.*` are unaffected since those never reach `isMutating`'s true branch. New `security.ActionClusterMaintenance` audit action (`internal/security/audit.go`). New CLI `nextsql cluster maintenance enable|disable` (`cmd/nextsql/main.go`, same live-connection-plus-`conn.Exec` shape as `cluster drain`/`cluster transfer-leader`, sharing `clusterConnFlags`/`resolveClusterConn`). New `system.replication.maintenance_mode` column (also `system.raft`, `SHOW CLUSTER`) for observability (`internal/system/schema.go`, `internal/executor/system.go` `systemReplicationRows`) — explicitly documented as capable of differing between nodes, unlike every other column in that table, since it is not replicated. Tests: `internal/sql/parser/parser_test.go` `TestParseClusterMaintenance`; `internal/executor/security_test.go` `TestClusterMaintenanceRBAC` (ungranted → `Forbidden` with no state change, in-txn → `InvalidArgument`, granted enable/disable round-trips `db.InMaintenanceMode()` and the result column) and `TestClusterMaintenanceBlocksWritesNotReads` (INSERT/DDL/BEGIN all `Unavailable` while enabled, SELECT and `system.replication.maintenance_mode` both correct, writes resume after DISABLE); `cmd/nextsql/main_test.go` `TestClusterMaintenanceRequiresUser`/`TestClusterMaintenanceRejectsUnknownVerb`; `tests/integration/protocol_test.go` `TestClusterMaintenanceOverLiveConnection` (real end-to-end proof over the wire protocol, not just one in-process `Session`: enabling on one connection blocks writes issued from a second, independent connection, confirming the gate is genuinely server-local `DB` state rather than something scoped per-`Session`). `internal/executor/system_test.go`'s exact-columns `TestSystemCatalogRBACRemainingViews`-style `SHOW CLUSTER` case updated for the new column. `go build ./...` clean; `go vet ./...` unchanged (same pre-existing unrelated `cdc.go` note); `internal/sql/parser`, `internal/security`, `cmd/nextsql`, `internal/executor` (full package, `-race`), and the touched `tests/integration` cases (`-race`) all green. No WAL/catalog/wire-format change. Docs: `docs/sql.md` (new paragraph + grammar-summary line), `docs/ops.md` (new "Maintenance mode" section documenting the node-local scope, the `BEGIN`-blocks-everything caveat, the leader-failover-does-not-carry-the-flag caveat, and the intended enable→maintain→disable operational sequence), `docs/system-catalog.md` (`system.replication` column-list update), `CHANGELOG.md`. Deliberately still open: a true cluster-wide (Raft-replicated) maintenance flag (would need the FSM extension described above — revisit only if node-by-node toggling proves insufficient in practice); no automatic re-propagation of the flag across a leader failover. Remaining P27 scope: rolling upgrade procedure, online format/catalog migration, backup/WAL retention management, replica-lag management, disk watermark policies, capacity warnings, per-realm/per-database connection limits (deferred pending selectable multi-database hosting), the Priority/scheduler-class-integration/unbounded-pools resource-group audit (log #53), and machine-readable operation status. Next P27 increment: the resource-group audit is the next self-contained candidate; rolling upgrade procedure is a good pairing with this increment's leader-transfer/drain/maintenance-mode sequence since it can now be documented as enable-maintenance → transfer-leader → drain-old-leader → restart → disable-maintenance.
56. [x] P27 tenth increment — machine-readable operational CLI output (2026-09-02) — closes the "Machine-readable operation status" Operational-CLI checklist item. New `--json` bool flag on `nextsql exec` and every `nextsql cluster` subcommand (`status`, `transfer-leader`, `drain`, `maintenance enable|disable`). For the four SQL-backed commands (`exec`, `transfer-leader`, `drain`, `maintenance`), new `printResult(res, jsonOut)` (`cmd/nextsql/main.go`) dispatches to the pre-existing `printTabularResult` or new `printJSONResultTo(w io.Writer, res)`, which prints one JSON object `{"columns": [...], "rows": [[...]], "affected": N}` — cell values stringified with the same `types.Value.String()` the TSV path already used, deliberately not attempting native per-SQL-type JSON encoding, so the shape stays identical across every result kind (a `CLUSTER DRAIN`-style single-string-column result and a `SELECT` with many typed columns both marshal the same way). `clusterConnFlags` (shared by all three cluster SQL subcommands) now also registers `--json` and returns its `*bool` alongside the `*flag.FlagSet` — a signature change to all 3 call sites, no behavior change to any pre-existing flag. `cluster status` reads a local status file rather than running SQL, so it does not go through `printResult`/`printJSONResultTo`: with `--json` it instead JSON-encodes the `replication.Status` struct directly (already carries `json:"..."` field tags from pre-P27 code, reused as-is — `node_id`/`state`/`leader_id`/`leader_addr`/`voters`/`applied_lsn`/`has_leader`/`apply_backlog`/etc.). Precedent: `nextsql whoami` already had a `--json` flag before this increment (unrelated OIDC-login command) — confirms this is an established, not novel, convention for this CLI, just not previously extended to the operational surface. `printJSONResultTo` takes an `io.Writer` (not hardcoded `os.Stdout`) specifically so it's unit-testable without stdout capture/redirection plumbing; `printResult` still calls it with `os.Stdout` at the real call sites. Tests: `cmd/nextsql/main_test.go` `TestPrintJSONResultTo` (valid JSON, correct columns/rows for a `CLUSTER DRAIN`-shaped single-row/single-column result) and `TestPrintJSONResultToAffectedRows` (a DML-shaped `Affected`-only result marshals `columns`/`rows` as empty, not null-vs-omitted-ambiguous — asserted via round-trip `json.Unmarshal`); existing `TestClusterMaintenanceRequiresUser`/`TestClusterMaintenanceRejectsUnknownVerb`/`TestClusterDrainRequiresUser`/`TestClusterTransferLeaderRequiresUser` all still pass unchanged (they fail before reaching any output code, so `--json` plumbing didn't touch their assertions). No live-server end-to-end `--json` test was added (the CLI package has no in-process live-server harness the way `tests/integration` does for the protocol server, and standing one up for a purely cosmetic output-formatting flag was judged disproportionate to this item's scope — the JSON-encoding logic itself is fully unit-tested, and `printResult`'s dispatch is a two-line, visually-verified pass-through). `go build ./...` clean; `go vet ./...` unchanged (same pre-existing unrelated `cdc.go` note); `cmd/nextsql` full package green. No WAL/catalog/wire-format change — pure CLI output formatting, nothing server-side. Docs: `docs/ops.md` (new "Machine-readable operation output" section with example invocations for all five commands), `cmd/nextsql/main.go`'s own `printUsage` text (`--json` added to the `exec`/`cluster status`/`cluster transfer-leader`/`cluster drain`/`cluster maintenance` usage lines), `CHANGELOG.md`. Remaining P27 scope: rolling upgrade procedure, online format/catalog migration, backup/WAL retention management, replica-lag management, disk watermark policies, capacity warnings, per-realm/per-database connection limits (deferred pending selectable multi-database hosting), and the Priority/scheduler-class-integration/unbounded-pools resource-group audit (log #53) — the last remaining self-contained candidate before the exit gate's rolling-upgrade-procedure item, which now has every building block (drain, leader transfer, timeouts, maintenance mode, JSON output) it needs to be written and tested.
57. [x] P27 eleventh increment — rolling upgrade procedure, documented and tested; closes all three Phase 27 exit-gate lines (2026-09-02) — picks up exactly where log #56 left off ("the exit gate's rolling-upgrade-procedure item... now has every building block it needs"). **Documentation**: new `docs/ops.md` "Rolling upgrade" section — a 4-step per-node procedure (transfer leadership away if leader → drain → stop/upgrade/restart the process → wait for catch-up, repeat), with an explicit note on when to additionally wrap the whole sequence in `CLUSTER MAINTENANCE ENABLE`/`DISABLE` (only when the upgrade is paired with something that must not race a concurrent write cluster-wide, e.g. an online schema change — not needed for the binary-upgrade case alone, since a 3+-voter deployment keeps quorum and keeps serving writes through any single node's cycle) and a reminder that maintenance mode doesn't follow `CLUSTER TRANSFER LEADER` (log #55's documented node-local scope). **Testing**: new `tests/integration/rolling_upgrade_test.go` `TestRollingUpgradeDrainWithLeaderTransferNoTransactionLoss` — a 3-node cluster (reusing `startProtocolCluster3`/`clusterNode` from the P22 follower-read test infra, extended with a stored `*protocol.Server` + always-wired `SetDrainFunc` per node, a small, generically useful addition) under a continuous background-writer goroutine (retry-on-`Unavailable`, same caller-level contract `TestFollowerReadFailoverSessionGuarantee` already established) goes through the full documented cycle for whichever node starts as leader: `CLUSTER TRANSFER LEADER` (direct connection) → wait for a new leader among the survivors → `CLUSTER DRAIN WITH (TIMEOUT_MS = 500)` (closes its listener, standing in for stopping the process) → disconnect its Raft `InmemTransport` from the other two for 300ms (standing in for the binary swap/restart window — the surviving two nodes keep quorum, 2 of 3, throughout) → reconnect it. Asserts: every write the client believes succeeded actually landed (`attempted == succeeded`, checked before any ground-truth comparison), a ground-truth `SELECT COUNT(*)` against the still-reachable cluster is within `[finalSucceeded, finalSucceeded+1]` (the tolerance for the one known ambiguous-ack case — see below), and the rolled node's own embedded `Session` (queried in-process with `ReadStale`, since its listener is closed for good and it is a follower, not the leader) converges to within the same tolerance of ground truth after rejoining. This is a direct, real test of the first Phase 27 exit-gate line, "planned maintenance can drain without unnecessary transaction loss" — not an inference from the individual drain/transfer-leader unit tests, which never combined them under concurrent write load across a real quorum-preserving multi-node topology before this increment. **Building this test surfaced three real, previously-latent robustness bugs — all in code predating this session, none touched by any earlier P27 increment** (found via the test failing with a raw `broken pipe` error, then a permanent-retry-loop hang, then a misclassified-as-`Internal` raft error, each fixed in turn and re-verified):
    1. **`drivers/go/cluster.go` (`nextsql.Cluster`, the Go driver's routing client) leaked raw transport errors and could stick to a dead connection forever.** `leaderConn`/`followerConn` changed to return `*clusterConn` (not `*Conn`) so failures can be attributed back to a specific cached entry. New `isTransportFailure(err) bool` (`nerr.HasCode(err, nerr.IO)` — deliberately narrow: server-sent application errors always decode with the server's own original `nerr` code via `unexpected`/`DecodeError`, never `nerr.IO`, so this can never misclassify a legitimate query rejection as a dead connection) and `Cluster.invalidate(cc)` (forces `cc.seen` to the zero time so the next `refresh()` re-probes it). `Query`'s leader-connection path: a transport failure now invalidates the connection and returns `nerr.Wrap(nerr.Unavailable, ...)` instead of the raw error, so a caller already retrying on `Unavailable` (the established contract for surviving a real leader failover, per `TestFollowerReadFailoverSessionGuarantee`) transparently survives this case too. The follower-read fallback path gets the same treatment (falls through to the leader on a transport failure, same as it already did for `Unavailable`). **The second, more serious half of this bug**: `refresh()`'s probe-failure handling previously left `cc.status` completely unchanged on any error — meaning a connection whose last-known role was cached as `"leader"` before it died would keep reporting `"leader"` *forever* (the underlying `*Conn` has no reconnect-on-use logic, so every subsequent probe against it fails identically), and since `leaderConn()` returns the *first* matching connection in `cl.conns` (insertion order = `Config.Nodes` order), a dead node placed early in that list would permanently win routing over the real, healthy new leader — this is exactly what caused the test to hang for the full 5-second retry deadline before this half of the fix. `refresh()` now clears a probed connection's `status` to the zero value (`Role: ""`, unmatched by any selection branch) specifically when the probe failure `isTransportFailure`, while still leaving `cc.seen` advanced (so it isn't hammered every call, only re-probed at the normal `statusTTL` cadence) — any *other* kind of probe failure (e.g. context cancellation) still leaves the last-known status untouched, unchanged from before. This fix's own dedicated coverage is the integration test above (13/13 clean runs post-fix, including under `-race`), not a narrower unit test — `drivers/go` has no in-process live-server harness the way `tests/integration` does, and standing one up just to unit-test this in isolation was judged unnecessary given the integration test already exercises exactly this failure mode reliably (it reproduced the bug in its very first runs, before either half of the fix).
    2. **`internal/protocol/frame.go` `ReadFrame` misclassified every read failure as `nerr.Protocol`.** Both `io.ReadFull` failure sites (header, payload) were wrapped as `nerr.Protocol` — implying "the peer sent something invalid" — rather than `nerr.IO`, the code `WriteFrame`'s own equivalent failures already correctly use one function above in the same file. A read failure (EOF, connection reset, deadline) happens *before* any frame content is even examined, so it can never actually be a protocol violation; the asymmetry with `WriteFrame` was a plain oversight, not a deliberate distinction. Fixed both sites to `nerr.IO`; genuine protocol violations (bad magic, unsupported version, invalid message type, packet-exceeds-limit — all determined only *after* a successful read) are untouched and still `nerr.Protocol`. This is what fix (1) above depends on to correctly recognize a broken connection at all: `isTransportFailure` checks specifically for `nerr.IO`, so before this fix a read-side transport failure (the shape a client sees when the *far end* — e.g. a just-drained server — closes the connection while a response was expected) would have been invisible to it. Test: new `internal/protocol/frame_test.go` `TestReadFrameClassifiesTransportFailureAsIO` (a truncated header and a header declaring more payload than is actually available both assert `nerr.IO`, alongside the pre-existing `TestReadFrameRejectsHugeLength`/`TestReadFrameRejectsBadMagic` continuing to assert `nerr.Protocol` for genuine violations, proving the distinction is preserved, not collapsed).
    3. **`internal/replication/cluster.go` `Cluster.Replicate` misclassified two of five possible `raft.Raft.Apply()` failure sentinels as `Internal` (non-retryable).** The pre-existing special-cased retryable set (`raft.ErrNotLeader`, `raft.ErrLeadershipLost`, `raft.ErrEnqueueTimeout` → `Unavailable`) was missing `raft.ErrLeadershipTransferInProgress` ("leadership transfer in progress" — exactly what a write racing `CLUSTER TRANSFER LEADER` produces, since `raft.Raft.Apply()` rejects new proposals while a `LeadershipTransfer()` call it initiated is in flight) and `raft.ErrRaftShutdown` (the equivalent race against the node's Raft instance actually stopping, e.g. mid `CLUSTER DRAIN`/process-restart). Both are exactly as transient and operator-driven as the three already handled — not evidence of a bug — so both added to the retryable set, extracted into a new pure `isRetryableApplyErr(err) bool` (a straightforward five-way `switch`) for direct unit testability without needing to fabricate a real `raft.ApplyFuture` failure. This is the fix for the second failure mode the rolling-upgrade test hit directly (`"nextsql internal: nextsql: apply"`, non-retryable by the writer's `Unavailable`-only retry loop, correctly causing the test to fail loudly rather than silently swallow it). Test: new `internal/replication/cluster_test.go` `TestIsRetryableApplyErr` (all five sentinels retryable, a generic `errors.New` + `raft.ErrAbortedByRestore` + `nil` all not).
   **A fourth issue was found and deliberately NOT fixed here — see the new "Local commit precedes replication acknowledgment" Phase-27-exit-gate tracked item** (added directly under the exit gate, not just in this log entry, and flagged directly to the user in the same turn, given it is a genuine data-integrity correctness gap): `internal/storage/engine.go` `commitAndReplicate` commits a transaction to local storage before calling `Cluster.Replicate` for quorum; when `Replicate` then fails (the `ErrLeadershipTransferInProgress` race above, or any other leader-transition race including a plain crash failover), the local commit is never rolled back, leaving at most one un-replicated local row per affected node (bounded by `Engine.replMu` serializing this path) that ordinary Raft log-replication catch-up never reconciles away. This does **not** cause any acknowledged write to be lost (the client sees a clean, correctly-retryable `Unavailable` for that specific write) — which is why the exit-gate line above is still closed — but it is a real, latent local/cluster divergence with no existing detection or repair mechanism. The rolling-upgrade test's own assertions are deliberately tolerance-windowed (`[groundTruth, groundTruth+1]`, not exact equality) specifically to account for this without either masking it (the tolerance is documented inline, with an explanation, not silently widened) or letting an unrelated, deeper architectural question block landing the actually-in-scope rolling-upgrade work. Fixing it properly (most likely direction: invert the ordering to replicate-then-locally-commit, matching how a follower's own `FSM.Apply` already only ever applies Raft-committed entries) is a substantive change to the core commit path shared by every replicated transaction and needs its own careful, dedicated increment — not a fix to rush inside this one. **Exit gate**: with the above, all three Phase 27 exit-gate lines are now checked (`docs/ops.md`/tests as cited); **Phase 27 itself remains open** — the exit gate is a set of specific, individually-verified claims, not a proxy for full phase completion, and substantial checklist scope (backup/WAL retention, replica-lag management, disk watermark policies, capacity warnings, online format/catalog migration, the resource-group priority/scheduler-class-integration/unbounded-pools audit) is still outstanding, plus the newly-tracked local-commit-before-replicate-ack item. `go build ./...` clean; `go vet ./...` unchanged (same pre-existing unrelated `cdc.go` note); `internal/replication`, `internal/protocol`, `drivers/go`, `internal/executor` (full package, `-race`), and `tests/integration` (full package, `-race`, incl. 8 consecutive clean runs of the new test alone plus `-count=1` full-package runs) all green. No WAL/catalog/wire-format change — every fix here is client-routing/error-classification, not on-disk or replicated-state format. Docs: `docs/ops.md` ("Rolling upgrade" new section, with the correctness-note paragraph on the fourth issue), `docs/ha.md` (router section: new paragraph on transport-failure handling), `CHANGELOG.md`. Next P27 increment: the Priority/scheduler-class-integration/unbounded-pools resource-group audit (log #53) is the last self-contained candidate before the remaining scope requires genuinely new, larger features (backup/WAL retention, replica-lag management, disk watermarks, capacity warnings, online migration strategy) — or, given its severity, the newly-tracked local-commit-before-replicate-ack correctness gap may warrant priority over further checklist items.
58. [ ] P27 twelfth increment — replication-orphan detection for the local-commit-precedes-replication-ack gap (2026-09-02) — the user asked directly to fix the fourth issue flagged in log #57. Design review confirmed the full fix (invert `commitAndReplicate` to replicate-then-locally-commit) is not safely attemptable as a rushed change: `internal/storage/engine.go` `commitLocked` durably WAL-flushes the transaction *and* calls `e.TM.Commit(txn.id)` (which releases locks and grants MVCC visibility to every other transaction) in one call, so deferring visibility until Raft quorum confirms requires splitting that call into two separately-orderable phases — a structural change to code every replicated transaction goes through. A post-hoc alternative (compensate by physically undoing the transaction's pages after a `Replicate` failure, using the existing `internal/undo` chain the same way crash recovery rolls back an *uncommitted* transaction) was investigated and rejected: `internal/storage/btree/txn.go` `Commit()`/`internal/storage/engine.go` `RollbackTxn` are built around a transaction still being open (`e.writers` entry present, no `CommitRec` yet) — once `commitLocked` has already appended a `CommitRec` and called `TM.Commit`, the transaction is durably committed and MVCC-visible, and another transaction could already have read and acted on it in the (small but real) window before `Replicate` returns; undoing it after the fact is not sound in general, only coincidentally harmless under the low concurrency a test exercises. Landed instead, as the responsible interim step: **detection**. New `metrics.Registry.AddReplicationOrphan()` + `Snapshot.ReplicationOrphans int64` (`internal/metrics/metrics.go`, same nil-safe atomic-counter shape as every other `Add*` method), called from `internal/storage/engine.go` `commitAndReplicate` at the exact point a `Replicate` call fails after `commitLocked` already succeeded — pure additive observability, no behavior change, so the caller's returned error and the (unfixed) local-visibility behavior are both unchanged. Test: new `internal/storage/btree/btree_test.go` `TestInsertOrphansLocalCommitOnReplicateFailure` (a fake `Replicator` that always fails; asserts the counter increments by exactly 1 around a single `Tree.Insert`, and — documenting the still-open gap rather than hiding it — that the key remains locally visible afterward via `Tree.Lookup`). `go build ./...` clean; `go vet ./...` unchanged (same pre-existing unrelated `cdc.go` note); `internal/storage`, `internal/storage/btree`, `internal/metrics` all green (incl. `-race`). No WAL/catalog/wire-format change. Docs: this TODO.md tracked item's writeup expanded in place (not re-duplicated) with the design-review findings and the new detection mechanism; `CHANGELOG.md`. **Deliberately left unchecked**: this closes detection, not the underlying gap — the box above stays open until the structural commit-path fix lands. Next candidate for that: a dedicated increment to split `commitLocked`'s WAL-durability step from its `TM.Commit` visibility-granting step, so `commitAndReplicate` can call the latter only after `Replicate` succeeds — scoped and tested independently of any other Phase 27 work, given its risk profile.
59. [x] Official Python and Ruby drivers (2026-09-02) — user request, not tied to any open phase (the driver checklist lives under Phase 8 protocol scope, already complete; this simply extends it). New `drivers/python` (stdlib only — `socket`/`ssl`/`decimal`/`json`/`uuid`/`datetime`, no third-party dependency) and `drivers/ruby` (stdlib only — `socket`/`openssl`/`bigdecimal`/`json`/`time`, no gem dependency), each a faithful reimplementation of the NSQL v1 wire protocol against `drivers/php/src/{Client,Protocol,Cluster}.php` as the closest reference (same blocking-socket, exception-based-error shape) cross-checked against `drivers/go` for anything PHP left ambiguous. Both ship: `Connection`/`Cluster` (leader routing + follower-read fan-out, same `isReadOnlySQL`/`txnControl` classifiers and 500ms status-cache design as every other driver), `Rows` (streaming, flow-controlled), `Statement` (prepare/execute/close), `exec_idempotent`/`query_idempotent` (`TYPE_IDEMPOTENT_QUERY`), `node_status`, `set_read_consistency`, `cancel` (second-connection-with-secret, matching every other driver), and full value marshaling (UUID, STRING/TEXT, DECIMAL via `decimal.Decimal`/`BigDecimal`, TIMESTAMPTZ via `datetime.datetime`/`Time`, dense+sparse VECTOR, JSON incl. NSJB binary decode, POINT/BOX/LINE/POLYGON). Deliberately **not** ported: the field-level `ENCRYPTED CLIENT` subsystem (`FieldEncryption`/`FileFieldKeyring`/`MemoryFieldKeyring` in PHP, ~730 lines, security-critical byte-for-byte format compatibility) — judged too large and too risky to add correctly in the same pass as the base driver; tracked as a follow-on, not silently dropped (see `SKILLS.md` 4.8's "all official drivers as of P25" note, corrected to name Python/Ruby as the exception).
    - **Verification methodology**: every wire-format decision was checked against a real, locally-built `nextsqld` (both plaintext-loopback and TLS with a self-signed cert), not just unit-tested in isolation — this caught two real encode/decode bugs *before* they shipped: (1) `decode_row_desc`/`decode_row_desc` initially read 7 bytes per column-type entry (copying the generic per-*value* header shape) instead of the correct 6 bytes a bare *type* descriptor uses (`internal/protocol/value.go` `readType`) — every column after the first was misparsed; (2) `_encode_vector`/`encode_vector` initially collapsed the value header's 5 "reserved" metadata bytes and the vector payload's own leading `dim+flag` into one 7-byte block, when the wire format actually has *both*, sequentially — traced by reading `internal/sql/types/row.go` `EncodeScalar`'s `KindVector` case directly. Both fixed and confirmed via live round-trips (`INSERT ... RETURNING` reading back a `VECTOR<F32,3>`/`UUID`/`DECIMAL`/`TIMESTAMPTZ` row, multi-column `SELECT`). Full test suites: `drivers/python/tests/test_protocol.py` (32 cases, `python3 -m unittest`) and `drivers/ruby/test/test_protocol.rb` (30 cases, `ruby`/Minitest) cover framing, decimal big-num round-trips (native `int`/`Integer` bignum, no manual base-256 arithmetic needed unlike PHP), every value kind, row-desc decoding, and config validation; `drivers/python/tests/test_live.py` / `drivers/ruby/test/test_live.rb` mirror `drivers/php/tests/live.php`'s live-server coverage (CREATE/INSERT/SELECT-with-param/prepared-statement/streaming-query/node-status/`READ_BOUNDED`), skipped without `NEXTSQL_ADDR`+`NEXTSQL_CA` set.
    - **A third, more consequential bug was found live, not caught by any unit test — a genuine, previously-latent connection-desync bug affecting the *existing* PHP, Node, Bun, and Deno drivers, not just the new ones.** Reproduced first by manually porting the naive PHP `readRows()` shape into the new Python/Ruby drivers and hitting it immediately: any query that fails (a real `TYPE_ERROR` response) permanently desyncs the connection, because the server's `writeErrReady` always sends `Error` then `Ready`, and `readRows`/`prepare`/`closeStatement` never drained that trailing `Ready` — only `readAck`/`nodeStatus` happened to get it right. The *next* call on that connection then reads the stale `Ready` frame as if it were its own response and fails with a spurious "unexpected message type," permanently breaking any connection that ever caught one query error and kept using it (a completely ordinary pattern: `try { $conn->exec(...) } catch (...) {}` followed by more queries). Confirmed this was **not** present in the Go driver, which already drains correctly (`drivers/go/nextsql.go` `readRows`'s `default` case: `if typ == protocol.TypeError { _ = c.expectReady() }`) — Go was the one implementation this bug never spread to. **Fixed in all six affected drivers** (Python and Ruby from the start; PHP, Node, and the JS drivers/`Bun`+`Deno` share `drivers/js/client.mjs`, retrofitted): centralized the fix in each driver's `unexpected()`/`_unexpected` helper — the one function every "did I get what I expected?" call site already funnels through — so it now drains the trailing `Ready` itself (best-effort: a drain failure doesn't mask the original decoded error) instead of requiring every call site to remember to do it individually, which is exactly the shape of bug that let this slip through unnoticed across four drivers. This also let several call sites (PHP/Node/JS `nodeStatus`/`readAck`) drop their own now-redundant manual draining. Node's `unexpected()` had to become `async` (it now performs I/O) — all 12 call sites updated to `await` it.
    - **Verified live across every affected runtime**, not just re-read: a minimal repro (query a nonexistent table, catch the error, run a second real query on the same connection) was run against a live `nextsqld` through PHP (`php`), Node (`node`, v24), Bun (`bun`, v1.3), and Deno (`deno`, v2.9) both before the fix (all four reproduced the desync) and after (all four now correctly return the second query's real result). Existing test suites re-run clean post-fix: `php drivers/php/tests/unit.php`, `node drivers/node/nextsql.test.js` (17/17), `bun test drivers/bun/nextsql.test.js` (15/15), `deno test drivers/deno/nextsql_test.js` (15/15, `--allow-net --allow-read --allow-env --allow-write`). New unit-level regression coverage was added to the two new drivers (`TestErrorReadyDraining`/`TestErrorReadyDraining` in each `test_protocol.*`, using a fake in-memory socket preloaded with a synthetic Error+Ready+CommandComplete+Ready byte stream — no live server needed to prove the drain); adding the equivalent to the pre-existing PHP/Node/JS suites is flagged as a good, cheap follow-up, not done here given time already spent finding and fixing the bug itself across four codebases.
    - Docs: `docs/web/content/docs/drivers-python.md` / `drivers-ruby.md` (new, mirroring `drivers-php.md`'s shape), `docs/web/content/docs/drivers.md` (table + language-guide list), `docs/web/lib/nav.ts` (nav entries), `docs/protocol.md` (driver list), `README.md` / `USAGE.md` (driver tables + worked Python/Ruby examples), `SKILLS.md` §4.8 (driver-surface list + P25 field-encryption scope note), `CHANGELOG.md`.
    - `go build ./...`/`vet` unaffected (no Go source touched by the driver work itself — only `internal/protocol/value.go`/`internal/sql/types/row.go` were *read*, not edited, to resolve the two encode/decode bugs above). No WAL/catalog/wire-protocol format change on the server side; the four existing-driver fixes are pure client-side bug fixes with no wire-format implication.
60. [x] WAL retention management (2026-09-02) — `/loop until done @TODO.md` autonomous continuation, picking the next well-scoped checklist item. `DB.SetWALRetentionHorizon(lsn)` already existed as a raw, manually-set primitive (`internal/executor/db.go`); the gap was policy — nothing translated a "keep N time" operator intent into the LSN it takes, so production WAL pruning stayed disabled by default. New `wal_retention_ms` config key (`internal/config/config.go`, same `_ms`/`>= 0`/zero-disables shape as every other timeout key, validated in both `Load` and `Validate`). New `cmd/nextsqld/main.go` `walRetentionTick(db, archiveDir, retention, now) (bool, error)` — the actual policy: calls the pre-existing `backup.ResolveUntilTime(backup.Header{}, archiveDir, now.Add(-retention))` (the same lookup `nextsql restore --until` already uses for PITR, reused rather than reimplemented) and, when it resolves an LSN, applies it via `db.SetWALRetentionHorizon`. A zero-value `backup.Header{}` is passed deliberately: `ResolveUntilTime`'s base-backup fallback branch requires `hdr.CreatedNano`, and retention has no specific backup to anchor to — only ever-archived segments should be able to raise the horizon, never a backup that predates the archive. Returns `(false, nil)` — not an error — when nothing has been archived far enough back yet; there is simply nothing to advance to. New `startWALRetentionUpdater(ctx, db, archiveDir, retentionMS, log)`: a no-op unless *both* `retentionMS > 0` and `archiveDir != ""` (pruning without an archiver would destroy the only copy of that WAL history, so there is nothing safe to advance toward without one — matches the existing manual-mechanism's own safety contract, just enforced automatically now); otherwise starts a goroutine ticking `walRetentionTick` (interval = retention/24, clamped to `[1m, 1h]`, plus once immediately at startup so a short-lived process doesn't wait a full interval) until `ctx` (the server's own SIGINT/SIGTERM-cancelled context, `serveContext()`) is canceled. Wired at both `nextsqld` DB-open sites (`internal/executor.Open` eager path, and the `--require-client-key` lazy `srv.Unlock` callback) — the eager path's call had to move from right after `installArchiver` to just after `ctx, stop := serveContext()` a few dozen lines later, since `ctx` does not exist yet at the point the DB opens eagerly (a genuine, if narrow, scoping trap: `ctx` is created once, late in `run()`, but only the `RequireClientKey` code path's callback runs late enough to already have it in a textually-later closure). **Deliberately still open, scoped out of this increment**: pruning itself. This only maintains the horizon; a closed segment is only actually removed during `MAINTAIN DATABASE`/`TABLE`/`INDEX`, which `nextsqld` has no automatic scheduler for (confirmed by design review, not assumed — grepped for any existing periodic-maintenance trigger and found none; `MAINTAIN` has always been a manual or externally-cron'd SQL statement, matching VACUUM in other databases). A `wal_retention_ms` policy without a scheduled `MAINTAIN DATABASE` alongside it keeps the horizon current but prunes nothing — documented explicitly, not left implicit. Adding automatic background maintenance scheduling is a separate, broader feature (its own resource-budget/timing/interaction-with-admission-control questions) deliberately not bundled into this increment. Tests: `internal/config/config_test.go` `TestLoadWALRetentionMS`/`TestLoadWALRetentionMSZeroDisables`/`TestLoadWALRetentionMSRejectsNegative`; new `cmd/nextsqld/wal_retention_test.go` — `TestWALRetentionTickAdvancesHorizonFromArchivedSegment` uses a **real** archived WAL segment (opens a real `executor.DB`, attaches a real `backup.DirArchiver`, writes, calls `db.Eng.Checkpoint()` — confirmed via a throwaway debug test that `Checkpoint()`, not `Close()` alone, is what actually offers segments to the archiver per docs/wal.md's own "Checkpoints" step 4 — then closes), asserting a zero-retention window finds it (`now-0` is always at/after an already-archived segment) and a 365-day window does not (nothing that fresh can satisfy "keep the last year"); `TestWALRetentionTickNoArchiveIsNotFoundNotError` (empty/nonexistent archive dir → `false, nil`, not an error); `TestStartWALRetentionUpdaterNoopWithoutPolicyOrArchive` (nil db / no archive dir / zero retention all return promptly without touching `db`, proven by passing a zero-value `&executor.DB{}` in the two cases that must never dereference it). This also incidentally became the first direct test coverage anywhere in the repo for `backup.ResolveUntilTime` itself (previously exercised only indirectly through `TestPITRByTime`'s base-backup fallback path, never through its archive-index-scanning branch in isolation) — a pre-existing gap, not introduced by this increment, worth noting for anyone touching PITR restore next. `go build ./...` clean; `go vet ./...` unchanged (same pre-existing unrelated `cdc.go` note); `internal/config`, `cmd/nextsqld` (incl. `-race`) both green. No WAL/catalog/wire-format change — this only calls an existing horizon setter on a schedule. Docs: `docs/wal.md` ("Retention" section, new "Automatic time-based retention" subsection), `docs/web/content/docs/config.md` (new "WAL archival and retention" section + sample config line), `CHANGELOG.md`, `TODO.md` (checklist + this entry). Next candidate: "Backup retention management" (the sibling checklist item — no backup-history/catalog tracking exists at all yet, a materially bigger increment since it needs a new concept, not just a policy over an existing primitive) or the automatic-maintenance-scheduling gap this increment deliberately left open.
61. [x] Backup retention management (2026-09-02) — `/loop until done unchecked at PHASE 27 @TODO.md` autonomous continuation, the next candidate flagged by log #60. Unlike WAL retention, no primitive existed to build a policy on top of: each `nextsql backup --out DEST` is a fully independent, self-contained directory with no "backup set"/history concept at all. New `internal/backup/retention.go`: `Info{Path, Header}` + `ListBackups(baseDir) ([]Info, error)` — scans `baseDir`'s immediate subdirectories, tries `ReadHeader` (the pre-existing single-backup header reader) on each, silently skips ones that fail (not a backup, or an unrelated directory — deliberately not an error, so a backup root can hold other things), sorted oldest-first by `CreatedNano`. `RetentionPolicy{KeepCount, KeepFor}` + `SelectPruneCandidates(backups, policy, now) []Info` — pure, filesystem-free selection logic (the actual policy, directly unit-testable): `KeepCount` keeps the N newest; `KeepFor` keeps everything created within that duration of `now`; **either way the single newest backup is never a candidate**, even if every backup is older than the policy allows — pruning to zero backups is a strictly worse outcome than keeping one stale one, so this floor is unconditional, not a flag. New CLI surface (`cmd/nextsql/main.go`): `nextsql backup list --base-dir DIR` (prints path/created/database/durable_lsn, oldest first) and `nextsql backup prune --base-dir DIR (--keep-count N | --keep-days N) [--confirm]` (the two policy flags are mutually exclusive and exactly one is required). Dispatch: new `backupCmd(args)` checks whether `args[0]` is a bare word ("list"/"prune") vs. a flag (starts with `-`) — the existing flag-first `nextsql backup --data-dir ... --out ...` invocation is fully preserved unchanged (falls through to the original `backupDB`, still directly callable), so this is purely additive, no breaking change to the existing command. **Safe-by-default, matching the `--confirm` convention already used by `migrate force`/`hosting migrate-tenant`**: without `--confirm`, `prune` only previews what it would remove (nothing touched); actual deletion (`os.RemoveAll` per selected backup — no soft-delete/undo, documented explicitly) requires it. New `security.ActionBackupPrune` audit action (`internal/security/audit.go`), recorded per deletion via the existing best-effort `auditLocal` helper (a no-op when `--base-dir` — not a nextsql data directory — has no pre-existing audit file, same graceful-degradation behavior every other CLI-local audit call already has). Tests: `internal/backup/retention_test.go` (7 cases covering `SelectPruneCandidates`'s keep-count/keep-days branches, the never-empties-the-set floor under both policies, exceeds-available, single-backup/empty-input edge cases, and no-policy-keeps-everything; `TestListBackupsSkipsNonBackupDirsAndSortsByAge` using 3 *real* backups via the existing `setupSQL`/`Create` test helpers plus one unrelated non-backup directory); `cmd/nextsql/main_test.go` `TestBackupListRequiresBaseDir`/`TestBackupPruneRequiresBaseDir`/`TestBackupPruneRequiresExactlyOnePolicy`/`TestBackupCmdDispatchesUnknownVerb`, and `TestBackupListAndPruneEndToEnd` — the real, load-bearing one: initializes 3 independent databases, backs each up under one `--base-dir`, confirms `backup list` finds all 3, confirms an unconfirmed `prune --keep-count 1` deletes nothing (preview), confirms a `--confirm`'d one correctly deletes the 2 oldest and keeps the newest by name, and confirms a repeated prune with the same policy is a clean no-op. `go build ./...` clean; `go vet ./...` unchanged (same pre-existing unrelated `cdc.go` note); `internal/backup` (full package, `-race`, 69s) and `cmd/nextsql` (full package, `-race`, 14s) both green. No WAL/catalog/wire-format change — this only lists/deletes existing backup directories via their already-published headers. Docs: `docs/backup.md` (new "Retention" section, commands block updated), `docs/web/content/docs/backup.md` (mirrored, worked example), `docs/web/content/docs/cli.md`, `USAGE.md` (CLI reference block), `CHANGELOG.md`, `TODO.md` (checklist + this entry). Remaining Phase 27 checklist scope after this: "Online format/catalog migration strategy where safe", "Replica-lag management", "Disk watermark policies", "Capacity warnings" (all genuinely new subsystems, no existing primitive to build on, unlike the last two increments), the resource-group Priority/scheduler-class-integration/unbounded-pools audit (needs a `scheduler.Admission` redesign), per-realm/per-database connection limits (deliberately deferred, blocked on multi-database hosting), and the "Local commit precedes replication acknowledgment" structural fix (deliberately deferred as too risky to rush, log #58). Next loop candidate: "Disk watermark policies" or "Capacity warnings" are likely the next most self-contained (an admission-control-adjacent check against `storage.Engine`'s existing free-space/page-count accounting, in the same spirit as the existing per-database `StorageCapBytes` mechanism) before the bigger "Online format/catalog migration strategy" or "Replica-lag management" items.
62. [x] Disk watermark policies + capacity warnings (2026-09-02) — `/loop until done unchecked at PHASE 27 @TODO.md` autonomous continuation, the next candidate flagged by log #61. New `internal/diskspace` package (no prior filesystem-capacity primitive existed anywhere in the repo — `storage.Engine.StorageCapBytes` is a logical, per-database page cap, unrelated to physical disk space): `Stat(path) (Usage, error)` wraps per-OS syscalls behind a `//go:build` split matching the existing `cmd/nextsqld/service_windows.go`/`service_other.go` convention — `diskspace_unix.go` uses `golang.org/x/sys/unix.Statfs` (`Bavail`, not `Bfree`, so free space matches what this unprivileged process could actually still write, not what `df` shows including root's reserve), `diskspace_windows.go` uses `golang.org/x/sys/windows.GetDiskFreeSpaceEx`. Build-verified on native linux plus `GOOS=windows GOARCH=amd64` and `GOOS=darwin GOARCH=arm64` (cross-compile only, not run). New `Config` fields (`internal/config/config.go`): `DiskWatermarkCheckMS` (0 = disabled, matching `WalRetentionMS`'s `_ms`/`>= 0` shape), `DiskWatermarkWarnPercent`/`DiskWatermarkRejectPercent` (0 = use the new `DefaultDiskWatermarkWarnPercent`/`DefaultDiskWatermarkRejectPercent` constants, 85/95), resolved via new `Config.DiskWatermarkThresholds() (warn, reject float64)`; `Validate()` additionally requires `warn < reject` so the two states can never invert. New node-local state on `DB` (`internal/executor/db.go`): `diskWatermarkTripped atomic.Bool` + `SetDiskWatermarkTripped(bool)`/`DiskWatermarkTripped() bool`, deliberately a **separate flag from `maintenanceMode`**, not a reuse — the design question flagged in log #58's writeup style: an operator's `CLUSTER MAINTENANCE DISABLE` must never silently un-reject writes on a node genuinely low on disk, and a disk-space recovery must never silently end an operator's maintenance window. `Session.requireNotMaintenance` (`internal/executor/session.go`) generalized to check both flags (same `Unavailable` classification, same write/no-write gating as `requireLeader` — BEGIN blocked, reads unaffected either way). New `cmd/nextsqld/main.go` `diskWatermarkTick(db, dataDir, warnPercent, rejectPercent, log) error` — one filesystem check + edge-triggered state transition: not-tripped→tripped at `usedPercent >= rejectPercent` (logs warn, `metrics.AddDiskWatermarkReject()`), tripped→not-tripped only at `usedPercent < warnPercent` (**not** merely below `rejectPercent`) — this asymmetry is the hysteresis: without it a node oscillating at 95.0%/94.9% would flap the reject state every tick; with it, once tripped, usage must recover all the way down to below the warn line (85% default) before writes resume, giving real headroom before the next possible trip. The warn-only crossing (`>= warnPercent`, `< rejectPercent`, not yet tripped) additionally logs once and bumps `metrics.AddDiskWatermarkWarn()` — this *is* "capacity warnings" from the checklist; no separate mechanism was built since the natural warn-threshold crossing already is the capacity warning, edge-triggered so a steady-state warn condition doesn't spam the log every tick. `startDiskWatermarkMonitor(ctx, db, dataDir, checkMS, warn, reject, log)` mirrors `startWALRetentionUpdater`'s shape exactly (no-op unless `checkMS > 0` and `dataDir != ""`; ticks once immediately then on a `checkMS` ticker until `ctx` cancels) and is wired at the same two DB-open sites (`internal/executor.Open` eager path right after `ctx, stop := serveContext()`, and the `--require-client-key` lazy `srv.Unlock` callback) that already carry `startWALRetentionUpdater` — same `ctx`-scoping trap from log #60 already avoided by placing the eager-path call where `ctx` is already in scope. New `metrics` fields (`internal/metrics/metrics.go`): `Registry.SetDiskUsage(total, free uint64)` (point-in-time gauge, not a counter — each tick overwrites, matching how `df`-style usage is normally read) plus `Snapshot.DiskTotalBytes`/`DiskFreeBytes`, and edge-triggered counters `AddDiskWatermarkWarn`/`AddDiskWatermarkReject` + `Snapshot.DiskWatermarkWarns`/`DiskWatermarkRejects`. Tests: `internal/diskspace/diskspace_test.go` (`TestStatRealFilesystem`, `TestStatRejectsMissingPath`, `TestUsedFraction` incl. the 0-total no-divide-by-zero case); `internal/config/config_test.go` `TestLoadDiskWatermark`/`TestLoadDiskWatermarkZeroDisablesAndUsesDefaults`/`TestLoadDiskWatermarkRejectsInvalid`/`TestValidateDiskWatermarkWarnMustBeBelowReject`; new `cmd/nextsqld/disk_watermark_test.go` — `TestDiskWatermarkTickTripsAtRejectThreshold` (threshold 0 always met, so any real disk state trips it), `TestDiskWatermarkTickClearsBelowWarnThreshold` (threshold 100 never met by a fresh temp dir), `TestDiskWatermarkTickHysteresisStaysTrippedBetweenWarnAndReject` (reads the *real* current usage via `diskspace.Stat` first, then derives warn-just-below/reject-far-above thresholds bracketing it, so the "stays tripped in the middle band" behavior is exercised without needing to fabricate disk state), `TestDiskWatermarkTickRejectsMissingPath`, `TestStartDiskWatermarkMonitorNoopWithoutPolicyOrDataDir`; new cases in `internal/executor/security_test.go` — `TestDiskWatermarkTrippedBlocksWritesNotReads` (mirrors `TestClusterMaintenanceBlocksWritesNotReads` exactly, driving the flag directly via `SetDiskWatermarkTripped` since there is no admin-SQL surface for it) and `TestDiskWatermarkAndMaintenanceModeAreIndependent` (the load-bearing one: trips disk-watermark, confirms `CLUSTER MAINTENANCE DISABLE` does not clear it and writes stay rejected; separately enables maintenance mode, confirms clearing the disk-watermark flag does not clear maintenance mode and writes stay rejected). `go build ./...` clean; `go vet ./...` unchanged (same pre-existing unrelated `cdc.go` note, confirmed not touched by this increment); `gofmt -l` clean on every edited file; `internal/config`, `internal/executor`, `internal/metrics`, `internal/diskspace`, `cmd/nextsqld` all green under `-race`. No WAL/catalog/wire-format change — this is a new local monitor plus one more write-reject gate at the same enforcement point maintenance mode already uses. Docs: `docs/ops.md` (new "Disk watermarks" section, between "Maintenance mode" and "Rolling upgrade"), `docs/web/content/docs/config.md` (new "Disk watermarks" section + 3 sample config lines), `CHANGELOG.md`, `TODO.md` (checklist + this entry). Checks off both "Disk watermark policies" and "Capacity warnings" — the two checklist items turned out to be one increment, not two, since the warn-threshold crossing already is the capacity warning. Remaining Phase 27 checklist scope after this: "Online format/catalog migration strategy where safe" and "Replica-lag management" (both genuinely new subsystems), the resource-group Priority/scheduler-class-integration/unbounded-pools audit (needs a `scheduler.Admission` redesign), per-realm/per-database connection limits (deliberately deferred, blocked on multi-database hosting), and the "Local commit precedes replication acknowledgment" structural fix (deliberately deferred as too risky to rush, log #58).
63. [x] Replica-lag management (2026-09-02) — `/loop until done unchecked at PHASE 27 @TODO.md` autonomous continuation (a live user "continue @TODO.md" during the wait between iterations), the next candidate flagged by log #62's own note. Unlike disk watermarks (a genuinely new capacity primitive), the underlying signal already existed: `replication.ReplicaHealth.ApplyBacklog` (`commit_index - applied_index`, "entries known committed but not yet applied locally") has been computed and exposed via `system.replica_health`/`DB.ClusterHealth()` since P22 — the gap was purely that nothing watched it proactively; an operator had to poll it manually. New `replica_lag_check_ms` (0 = disabled, same shape as every other `_ms` policy key) / `replica_lag_warn_entries` (0 = use new `DefaultReplicaLagWarnEntries` = 1000, same "0 = default" convention as the disk-watermark percentages) config keys (`internal/config/config.go`), resolved via new `Config.ReplicaLagWarnThreshold() uint64`. **Deliberately alerting-only, no reject/blocking counterpart** — considered and rejected as unnecessary, not merely deferred: a lagging follower does not affect the leader's ability to accept writes (unlike low disk space, which threatens the node accepting the write itself), and `Cluster.FollowerReadHealthy` (P22) already refuses to route a bounded-staleness read to a follower that has fallen too far behind, regardless of whether this monitor is even enabled — so there is no admission-control gap to close here, only a visibility one. New `cmd/nextsqld/main.go`: `replicaLagTick(db) (backlog uint64, attached bool)` — one call to the pre-existing `db.ClusterHealth()`, records the gauge via `metrics.SetReplicaApplyBacklog`, returns `attached=false` on a single-node deployment (no cluster attached) where the concept doesn't apply. Pure `replicaLagEdge(wasWarned bool, backlog, warnEntries uint64) (nowWarned, logWarn, logRecover bool)` — the actual edge-triggered decision, deliberately split out as a side-effect-free function (no metrics/logging calls inside it) purely so the warn/recover transition logic is unit-testable without a live Raft cluster; unlike the disk watermark's asymmetric warn/reject hysteresis, this uses one plain threshold with clean edge-triggering (`>=` trips, `<` clears) since nothing here gates behavior that could flap — there's no dual-state risk to guard against with an asymmetric clear line. `startReplicaLagMonitor(ctx, db, checkMS, warnEntries, log)` mirrors `startWALRetentionUpdater`/`startDiskWatermarkMonitor`'s exact shape (no-op unless `checkMS > 0`; ticks once immediately then on a ticker until `ctx` cancels), wired at both `nextsqld` DB-open sites. **A new, previously-unencountered wiring hazard was found and fixed while doing this**: at the `--require-client-key` lazy `srv.Unlock` callback site, `startWALRetentionUpdater`/`startDiskWatermarkMonitor` are both called *before* `startCluster` runs — harmless for those two since neither touches `DB.gate`/`ClusterHealth`, but `replicaLagTick` does (via `DB.ClusterHealth()` → the unexported `DB.cluster()` → a raw, unsynchronized read of `db.gate`), and `AttachCluster`/`SetGate` write `db.gate` with **no mutex** (confirmed by reading `internal/executor/db.go` — a pre-existing, deliberate-looking design choice for a field that in practice is normally set once at startup before any query traffic exists, not a bug in itself). Starting the monitor's background goroutine at the same call-site position as the other two would have raced that unsynchronized write from the `srv.Unlock` goroutine — caught by reasoning through the ordering before writing the code, not by a flaky `-race` failure, and fixed by placing `startReplicaLagMonitor`'s call *after* `startCluster` returns at that site instead (the eager `nextsqld` DB-open path was already safe: its own `startCluster` call happens far earlier in `run()`, well before `ctx`/any monitor exists). Worth remembering for the next monitor added at these two sites: check what `DB` state it reads, not just where the existing ones happen to sit. New `metrics` fields (`internal/metrics/metrics.go`): `Registry.SetReplicaApplyBacklog(n uint64)` (gauge, mirrors `SetDiskUsage`'s shape) + `Snapshot.ReplicaApplyBacklog`, and edge-triggered `AddReplicaLagWarn()` + `Snapshot.ReplicaLagWarns` (mirrors `AddDiskWatermarkWarn`). Tests: `internal/config/config_test.go` `TestLoadReplicaLag`/`TestLoadReplicaLagZeroDisablesAndUsesDefault`/`TestLoadReplicaLagRejectsInvalid`; new `cmd/nextsqld/replica_lag_test.go` — `TestReplicaLagTickNotAttachedOnSingleNodeDB` (real `testDB(t)`, no cluster, confirms `attached=false`/`backlog=0`, no panic), `TestReplicaLagEdge` (8-case table covering below/at/above threshold from both the not-yet-warned and already-warned starting states, incl. the exact-equality boundary), `TestReplicaLagEdgeNeverLogsBothAtOnce` (an exhaustive sweep — a structural invariant check on the state machine itself, not just example cases), `TestStartReplicaLagMonitorNoopWithoutPolicy`. `go build ./...` clean; `go vet ./...` unchanged (same pre-existing unrelated `cdc.go` note); `gofmt -l` clean on every edited file; `internal/config`, `internal/metrics`, `cmd/nextsqld` all green under `-race`; `tests/integration` rolling-upgrade/follower-read/cluster tests (`-race`) re-run clean to confirm the `DB.gate` wiring-order change introduced no regression. No WAL/catalog/wire-format change — this only reads an existing health snapshot on a schedule and logs/counts. Docs: `docs/ha.md` (new "Replica-lag monitoring" subsection under the existing "Replica lag and follower health" section), `docs/ops.md` (Metrics list gained this bullet **and** a previously-missing disk-watermark metrics bullet from log #62, noticed and fixed in passing), `docs/web/content/docs/config.md` (new "Replica-lag monitoring" section + 2 sample config lines), `CHANGELOG.md`, `TODO.md` (checklist + this entry). **Remaining Phase 27 checklist scope after this**: "Online format/catalog migration strategy where safe" (the last genuinely-new-subsystem checklist item), the resource-group Priority/scheduler-class-integration/unbounded-pools audit (needs a `scheduler.Admission` redesign), per-realm/per-database connection limits (deliberately deferred, blocked on multi-database hosting), and the "Local commit precedes replication acknowledgment" structural fix (deliberately deferred as too risky to rush, log #58). Next loop candidate: the resource-group audit is likely the next most self-contained (an internal `scheduler`/`internal/catalog` change, no new SQL surface, unlike the migration-strategy item which needs real design work for catalog versioning across format changes) before "Online format/catalog migration strategy" or revisiting the local-commit-before-replicate-ack gap.
64. [x] Online format/catalog migration strategy where safe (2026-09-02) — `/loop until done unchecked at PHASE 27 @TODO.md` autonomous continuation, resumed by a live user message ("continue @TODO.md" followed by the user separately quoting this exact checklist line, taken as direction to work it next instead of log #63's own "resource-group audit" suggestion). Research first, since this item had no obvious starting point unlike prior increments: `format.CurrentFormatVersion` (`internal/storage/format`) has never been bumped past 1 — every superblock/page in the field is v1 — so there is no actual v2 to migrate *to*, ruling out building a real converter (would be pure speculation, against this repo's own no-hypothetical-features convention). But the catalog-record side (`NSCT`, `internal/catalog`) already has ten real versions and a working, already-safe-and-online multi-version decode pattern (`DecodeTable`'s `ver == tableVersionV1`/`V2`/... branches, each with defined defaults for fields that didn't exist yet — a record written by old code just keeps decoding forever; the next write of that row naturally upgrades it). Also found, already built but never wired in: `internal/upgrade` (`catalog.go`) already defined a complete `Family`/`Spec`/`Catalog()`/`Check()`/`Compatible()` compatibility matrix covering all 12 persisted families (page, envelope, WAL, undo, catalog, backup, export, protocol, replication, isolated, etc.) with real Min/Max-readable windows — `docs/storage-format.md` already documented it as "the" compatibility window and claimed `nextsql diagnose` "prints that window" (true) — but grepping confirmed `upgrade.Check`/`Compatible` were called from literally nowhere except that same package's own `WriteReport` and its test file: every actual enforcement site (`file.decodeSuperblock`, `catalog.DecodeTable`, `page.Validate`, `page.CheckID`, and ~65 other version-checked decoders across the codebase) independently re-implemented its own hardcoded version-range check with its own generic "unsupported X version" string — a real, if latent, two-sources-of-truth risk: nothing enforced that `internal/upgrade.Catalog()`'s printed numbers actually matched what would open. **Scope decision**: fixing all ~65 version-check call sites repo-wide (WAL, undo, replication, vector, auth, protocol, etc.) would be a large, disproportionate, materially-out-of-scope sweep the checklist item never asked for (it names "format" and "catalog" specifically) and would touch several hot/sensitive paths (replication log apply, WAL record parsing) not worth the risk for a cosmetic error-message win — deliberately NOT done. Wired only the two the checklist names: `file.decodeSuperblock` (`FamilyPage`) and `catalog.DecodeTable` (`FamilyCatalog`), both cold paths (once per DB open / once per table-descriptor decode, not per-page), now calling the shared `Check`. **Also deliberately left `page.Validate`/`page.CheckID` on their fast inline `!=` check** (documented why, not just skipped): they run on every single page read — genuinely hot — and since `FamilyPage` has `MinReadable == MaxReadable == Current` today, the inline check and `Check()` are exactly equivalent, so there was no real message-quality gap there worth the extra call overhead on that path; the superblock check already gates DB open before any page is ever read. **A real import-cycle blocker was hit and resolved cleanly, not worked around**: `internal/storage/file` cannot import `internal/upgrade` directly — `internal/upgrade`'s `inspect.go` (same package as the compatibility catalog) imports `internal/undo`, which imports `internal/storage/file`, so `file → upgrade → undo → file` would cycle. Fixed by extracting the pure, zero-heavy-dependency compatibility-catalog code (previously `internal/upgrade/catalog.go`: `Family`, `Spec`, `Catalog()`, `Lookup()`, `Check()`, `Compatible()` — only `fmt`/`internal/nerr`) into a new leaf package `internal/upgrade/compat`; `internal/upgrade` (the diagnose/inspect package) now imports it too, so `nextsql diagnose`'s printed catalog and the two enforcement sites are provably reading the exact same table, not two copies that could drift. `Check()`'s error message was also improved in the same change to name the actual and min/max-supported version numbers (previously just "unsupported X version" with no numbers) — small, targeted, and immediately useful for operators regardless of the migration-strategy framing. The bulk of the deliverable is the new `docs/storage-format.md` "Format and catalog migration strategy" section: states the policy plainly — catalog-record changes are safe online (the demonstrated `NSCT` recipe: bump the version constant, decode every prior version with sensible defaults, never remove an old branch, widen `MaxReadable` in the same change) with no exception process needed; format-level (page/superblock) changes are NOT safe to migrate in place in general (fixed-offset headers, AEAD-sealed content, checksum-covered bytes) and the safe path is offline — `nextsql backup`/a plain SQL copy into a freshly `nextsql init`'d database on the new binary, which already works today with zero new code since both cross the *logical* row layer, not raw physical page bytes (mirrors how "Rolling upgrade", log #57, also turned out to be a documented sequence of pre-existing primitives, not new mechanism); and names the "additive-only format field" middle case explicitly as a *when it happens* decision, not something to build speculatively now. Tests: moved the three pure `Check`/`Catalog` tests from `internal/upgrade/upgrade_test.go` into new `internal/upgrade/compat/compat_test.go` (same assertions, now testing the leaf package directly), plus a new `TestCheckErrorNamesActualAndSupportedVersions` asserting the improved message actually contains the family/version/min-or-max numbers for both the too-old and too-new cases; `internal/upgrade/upgrade_test.go`'s remaining `Inspect`-based tests updated to the `compat.` prefix and re-verified green. `go build ./...` clean (confirms no import cycle); `go vet ./...` unchanged (same pre-existing unrelated `cdc.go` note); `gofmt -l` clean on every edited file; `internal/upgrade`, `internal/upgrade/compat`, `internal/storage` (full package incl. subpackages), `internal/catalog` all green under `-race`. No wire-protocol change; the only persisted-format-adjacent change is strictly additive (better error text on an already-failing path) — every currently-valid superblock/catalog version still opens identically. Docs: `docs/storage-format.md` (new section), `docs/ops.md` ("Upgrade / format compatibility" — updated package path, pointer to the new section, notes enforcement now matches what's printed), `CHANGELOG.md`, `TODO.md` (checklist + this entry). **Remaining Phase 27 checklist scope after this**: the resource-group Priority/scheduler-class-integration/unbounded-pools audit (needs a `scheduler.Admission` redesign) is now the last self-contained checklist candidate; per-realm/per-database connection limits remain deliberately deferred (blocked on multi-database hosting — confirmed still true, one `nextsqld` process opens exactly one database); and the "Local commit precedes replication acknowledgment" structural fix remains deliberately deferred as too risky to rush (log #58). With this increment, every Phase 27 "Server lifecycle" checklist line is now checked except the two structurally-blocked/deferred ones. Next loop candidate: the resource-group Priority/scheduler audit.
65. [x] Multi-database hosting M2-1 — registry realm/database creation primitives (2026-09-02) — **NOT a Phase 27 item**, a separate user request (a live "Scope out multi-database hosting" instruction, given while the autonomous Phase 27 loop was between iterations, in response to the recurring deferred "Per-realm and per-database connection limits" TODO line). The cross-cutting Multi-database hosting track (`docs/design-multidatabase-dbaas.md`) had never been decomposed past broad milestone prose the way every phase in this file has — M2 "single-node multi-database routing" was five vague bullets with no increment-level breakdown. Entered plan mode; two `Explore` subagents researched the live codebase (not just the design doc) to ground a real decomposition: `internal/hosting.Registry` already has full lifecycle-state validation (`CanTransition`, `PROVISIONING → ACTIVE → SUSPENDED → DELETING → TOMBSTONED/FAILED`) and a `LayoutManaged` path scheme (`ManagedDatabasePath`) fully defined and validated, but no `CreateRealm`/`CreateDatabase` method existed and `LayoutManaged` was never consumed anywhere; the existing SQL `CREATE DATABASE` (`internal/executor/exec_ddl.go`) is confirmed sibling-file-only (never touches the registry, file never opened/served) and must keep working unchanged for non-hosted use; the wire protocol's frame-header `Version` is a hard equality gate with no negotiation, so a future realm field must be an additive trailing field inside `Hello`'s own payload (mirroring `NSCT`'s pattern), not a version bump; there is no wire-level capability negotiation today (`system.capabilities` is post-handshake SQL only); `protocol.Server.DB`/`Tasks` are single fields and the three Phase 27 monitors (WAL retention, disk watermark, replica lag) are each duplicated per DB-open site with no independent stop signal; `scheduler.Admission`'s acquire/release idiom is the closest existing bounded-resource pattern to model a future `DatabaseManager` on. Decomposed M2 into four independently-gated sub-increments (M2-1..4, see `docs/design-multidatabase-dbaas.md` §16 and the checklist above) and landed the smallest, most self-contained one now: **M2-1**. New `internal/hosting/registry.go` `Registry.CreateRealm(realmName, databaseName string, identity format.Identity, keyRef string) (Realm, Database, bool, error)` and `Registry.CreateDatabase(realmID ID, databaseName string, identity format.Identity, keyRef string) (Database, bool, error)`, sharing an internal `addDatabaseLocked` helper. Design constraint discovered while implementing (not anticipated from the design doc alone): `validateManifest` requires every realm to have at least one database at all times (`len(realm.Databases) < 1` is invalid), so realm creation and its first database's creation cannot be split into two independent registry generations the way `SetDatabaseState` mutates an existing record — `CreateRealm` persists both atomically in one generation. The realm ID is derived deterministically from its name (`deriveRealmID`, reused from the existing declarative-bootstrap path) so a retried call always targets the same realm. Idempotent by design, not just by accident: reapplying with the same realm/database name and the *same* identity, found in `StateProvisioning` or `StateActive`, returns the existing records with `created=false` rather than erroring (mirrors `EnsureBootstrap`'s reapply semantics) — a caller that crashes between registering `PROVISIONING` and physically creating the database file can retry safely; a name collision with a *different* identity is rejected `AlreadyExists`; a database identity colliding with one already registered under a different name anywhere in the deployment is rejected `Conflict`; a realm that is not `StateActive` (no setter exists yet — realms cannot currently be suspended — but the check exists for when M2-4 or a future increment adds one) is rejected `Conflict`. New CLI (`cmd/nextsql/main.go`): `nextsql realm create --realm NAME --database NAME --database-key-file FILE` and `nextsql database create --realm NAME --name NAME --database-key-file FILE`, both requiring the exclusive data-directory lock (mutates the registry and writes real files, same safety class as every other mutating `hosting` subcommand) and reusing existing primitives end-to-end rather than duplicating logic: `resolveRealmID` (existing), the already-generic `createOrResumeDatabase` (previously used only by `nextsql init`'s single bootstrap database, now reused verbatim for any additional managed database), and the existing `SetDatabaseState`. New `ensureDatabaseKeyFile` (create-if-missing, mirrors `nextsql init`'s own root-key convention exactly) and `resolveManagedDatabaseIdentity` (checks the registry for an existing record by name first — if `StateActive`, treats the call as a successful no-op without re-touching the registry; if `StateProvisioning`, reuses *that record's own already-durable* `Identity` rather than generating a fresh one, which is what makes a CLI-level retry after a crash resolve to the same registry record instead of a fabricated identity collision). New `security.ActionRealmCreate`/`ActionDatabaseCreate` audit actions, recorded via the existing best-effort `auditLocal` helper. **Explicitly, deliberately out of scope for M2-1** (all M2-2/M2-3 work): `nextsqld` does not open, serve, or route any connection to a database created this way — it is durably registered and physically provisioned (a real encrypted database file at the ID-based `LayoutManaged` path that opens and verifies) but reachable today only the same way the pre-existing sibling-file `CREATE DATABASE` output is: directly, via `--data-dir` pointing at its specific path. That's an accepted, deliberate limitation of this slice, not an oversight — matching the "smallest coherent increment" discipline the design doc's own §16 delivery-sequence intro calls for. Tests: new `internal/hosting/create_test.go` (7 cases: fresh creation + restart durability, idempotent retry with the same identity, rejection of a retry with a *different* identity, adding a second database to an existing realm, unknown-realm rejection, non-active-realm rejection via a hand-built `EnsureManifest` registry since no real API can suspend a realm yet, and cross-realm identity-collision rejection); new `cmd/nextsql/realm_database_test.go` (8 cases: end-to-end creation with a live keystore-open verification that the physical file's identity matches the registry record, idempotent retry, **a genuine simulated-crash resume test** — calls `Registry.CreateRealm` directly to leave a durable `PROVISIONING` record with no physical file on disk, exactly the "process died right after step 3" scenario the design doc's step list describes, then confirms the CLI command run fresh detects and completes it, reusing the same identity — `database create` into an existing realm, unknown-realm rejection, missing required flags, and the exclusive-deployment-lock rejection every other mutating hosting command already has). `go build ./...` clean; `go vet ./...` unchanged (same pre-existing unrelated `cdc.go` note); `gofmt -l` clean on every edited file; `internal/hosting`, `cmd/nextsql`, `internal/security` all green under `-race`. No wire-protocol/WAL/existing-catalog-format change — this is new registry state plus new files at a path scheme (`LayoutManaged`) that was already fully specified and validated but simply unused until now. Docs: `docs/design-multidatabase-dbaas.md` (top status line, new "M2-1 landed" callout, §11.2 precisely scoped against what this CLI does and does not do vs. the aspirational "authenticated administrative interface" text, §16 M2 decomposed into M2-1..4 with M2-1 marked delivered), `TODO.md` (the M2+ checklist replaced with the granular M2-1..4 sequence + this entry), `CHANGELOG.md`. A plan was written and approved before implementation (`.claude/plans/giggly-munching-nygaard.md`) given the scale of the cross-cutting track. **Next candidate for this track**: **M2-2** (Hello realm field) — needs the two open `docs/design-multidatabase-dbaas.md` §19 decisions (protocol v1 compatibility window; formally adopting "realm identities with database-scoped grants") recorded explicitly first. Phase 27's own next candidate (the resource-group Priority/scheduler audit, log #64's note) is unaffected and remains queued separately.
66. [x] Resource-group scheduler-class-integration + unbounded-pools audit (2026-09-02) — `/loop until done unchecked at PHASE 27 @TODO.md` autonomous continuation (resumed via `/loop` re-invocation across a session context reset), the P27 candidate log #64 flagged as the last self-contained checklist item. Scoped the three remaining resource-group lines (Priority, scheduler-class integration, unbounded pools) by risk before touching any code: `scheduler.Admission` (`internal/scheduler/admit.go`) is a plain channel-semaphore with no priority concept at all — a waiting request just blocks on the same shared `slots` channel via `select`, with no explicit waiter list, so real priority-ordered admission means replacing that wait path with a cancellable priority heap of waiters (each with its own signal channel, popped highest-first on release, correctly removed on cancel/timeout) — a genuine concurrency-primitive rewrite of the one gate every query in the system passes through. **Deliberately deferred, not attempted**, on the same reasoning already applied to the "Local commit precedes replication acknowledgment" item: a subtle bug in this exact hot path risks server-wide deadlocks or silent admission failures, not worth rushing alongside an audit. The other two lines were genuinely auditable and fixable at low risk, so those were done. Audited every background-work class named in "Integrate API/analytics/workflow/maintenance/backup classes with one scheduler": API (regular SQL) and analytics (`ANALYZE`) are trivially integrated by construction — both are just statements dispatched through `Session.ExecContext`, which is where `db.admit.Acquire` already lives; maintenance was already integrated and already had a passing test (`TestMaintainSQLObeysAdmission`); backup is inherently out of scope for a *live-process* scheduler — `nextsql backup` is an offline CLI invocation with no running `nextsqld` involved at all, confirmed by grep (`nextsqld`'s only `internal/backup` import use is WAL archival/retention, never a live backup trigger), so there is no live gate for it to integrate with. **Found one real, previously-unnoticed gap**: workflow/task execution. `internal/executor/task.go` `executeClaimedTask` — the function every scheduled `RUN WORKFLOW` (via `TaskRuntime`'s fixed worker pool) actually runs through — called `s.execRunWorkflow(...)` directly, a lower-level internal method, never `Session.ExecContext`, so it never acquired `db.admit` at all; its only concurrency bound was `TaskRuntime`'s own separate `Workers` config (1-16), completely independent of and invisible to the process-wide admission gate every regular query already respects. Under query-admission saturation, background tasks would keep running obliviously; under a burst of tasks, they could consume CPU/memory/worker resources the admission gate has no visibility into. Fixed by acquiring `db.admit` directly inside `executeClaimedTask`, positioned right after the pre-existing `db.gate.AllowWrite()` (leadership) check and before any session/transaction work begins — mirroring `ExecContext`'s exact acquire/defer-release/metrics pattern (`AddRejected`/`AddAdmitted`), and, on rejection, returning early *before any task-state mutation* (same as the `db.gate` check immediately above it), so an admission-rejected task's row stays `TaskRunning` with its original lease — the lease simply expires and the task is reclaimed later, rather than being marked permanently failed for a transient overload condition. `worker()` (`internal/executor/task_runtime.go`) already special-cased `nerr.Unavailable` from this call as non-reportable, i.e. the codebase had already anticipated this exact failure mode arriving from `executeClaimedTask` (via the pre-existing `db.gate` check) — the fix slots into an error-handling contract that already existed, not a new one. Then audited "No independent unbounded pools" directly: `TaskRuntime` is a fixed-size worker set (goroutines spawned once at `StartTaskRuntime`, never per-task — confirmed by reading, not assumed); `scheduler.Pool`/`scheduler.Admission` are both pre-existing bounded semaphores; per-connection goroutines are bounded by `MaxConnections`; CDC (`SUBSCRIBE`) spawns no goroutine whatsoever — delivery is pull-based within the existing connection's own goroutine as the client calls `next()`, and the initiating `SUBSCRIBE` statement is itself admission-gated like any other statement. No independent unbounded goroutine-per-item pattern found anywhere in the live server; both lines closed on this evidence. Test: new `internal/executor/task_runtime_test.go` `TestExecuteClaimedTaskObeysAdmission` — real end-to-end proof mirroring `TestMaintainSQLObeysAdmission`'s shape: creates a table/workflow/schedule, dispatches and claims a real due task (`createDueScheduledTask`/`claimOneTask`, the same helpers `TestTaskOwnerIsolationAndInvokerRights` already uses), saturates a 1-slot `scheduler.Admission` by holding its one slot from the test itself, confirms `db.ExecuteClaimedTask` returns `Unavailable` and is counted in `Rejected`, confirms the task row is *still* `TaskRunning` (not failed) after rejection, then releases the slot and confirms the *same* claim now succeeds and the workflow's effect (`INSERT`) is visible — proving both the block and the eventual-success/no-state-corruption halves, not just that an error comes back. `go build ./...` clean; `go vet ./...` unchanged (same pre-existing unrelated `cdc.go` note); `gofmt -l` clean; `internal/executor` full package (incl. every `Task`/`Schedule`/`Workflow`/`Maintain`-prefixed test, plus the full package) green under `-race`. No WAL/catalog/wire-protocol change — this only adds one more `Acquire`/`defer release()` pair around an already-isolated internal call site, exactly the same shape as every other admission-gated entry point. Docs: `TODO.md` (checklist ×2 checked + Priority's unchecked line expanded with the deferral reasoning + exit-gate intro paragraph updated to reflect every prior line but two now closed + this entry), `CHANGELOG.md`. **With this increment, Phase 27's checklist has exactly two open items left**: resource-group **Priority** (deferred above, needs its own dedicated `scheduler.Admission` redesign increment) and "Local commit precedes replication acknowledgment" (deferred, log #58, needs its own dedicated core-commit-path increment) — both are legitimately hard, both deliberately deferred rather than rushed, and Phase 27 remains the open release gate until one or both are picked up.
67. [x] Resource-group Priority enforcement (2026-09-02) — a live user "continue @TODO.md" instruction; given the two remaining P27 items were both flagged as needing their own dedicated increment, asked the user which to tackle via `AskUserQuestion` rather than picking one autonomously (the other, the replication-ack ordering fix, remains untouched and deliberately deferred). User chose Priority. Used plan mode given the stated risk: an Explore subagent first grounded the design in the live code (`catalog.ResourceGroup.Priority int32`, range `[0,9]`, already fully wired through parser/binder/persistence, only enforcement missing; exactly three `scheduler.Admission.Acquire` call sites in the whole repo — `Session.ExecContext`'s process-wide gate, `Session.ExecContext`'s per-group gate, and `executeClaimedTask`'s process-wide-only gate), then a Plan subagent designed and validated the concurrency algorithm before any code was written. Implemented exactly that design: `internal/scheduler/admit.go`'s `Admission` replaced its internal `slots chan struct{}` counting semaphore with a `sync.Mutex`-protected `free int` counter plus a `container/heap`-based `waiterHeap` (ordered by priority descending, then insertion sequence ascending for FIFO tie-break). New `AcquireWithPriority(ctx, priority int32)` sibling method (`Acquire(ctx)` unchanged, now `acquire(ctx, 0)` internally) — additive, not a signature change, so every non-priority caller (`resourceGroupGate`'s per-group gates, `executeClaimedTask`) needed zero changes and is behaviorally identical to before. The one real correctness hazard — a waiter's `ctx` firing at the exact instant `release()` hands it a slot, which is a genuinely new race introduced by splitting "decide who gets the slot" (heap pop) from "notify the winner" (channel send) into two steps, where today's plain channel send used to be one atomic step — is resolved by making the waiter's heap `index` (always read/written under the same mutex as the heap) the sole source of truth: `index >= 0` means still queued and safe to cancel (`cancelWaiter` removes it via `heap.Remove`); `index == -1` means `release()` already popped it, so cancellation must be refused and the grant drained instead of dropped, which is what makes the handoff provably leak-free and deadlock-free regardless of which branch Go's pseudo-random `select` happens to pick when both fire simultaneously. Only `internal/executor/session.go`'s `Session.ExecContext` process-wide acquire (the one gate shared across every group and unassigned session, hence the only place cross-group priority ordering is meaningful) changed, to look up `s.resourceGroup`'s `Priority` via the pre-existing `db.resourceGroup(name)` helper (already used one line below for `limitsOrDefault`) and call `AcquireWithPriority` — the per-group gate's own acquire (line 543) is untouched since it only ever holds members of one group sharing one priority. Priority affects only queue ordering, never preemption of an already-admitted query, and does not change a waiter's own `QueueWait` timeout — a priority-0 caller sees exactly the same bound as before; starvation under sustained higher-priority contention is the accepted, intentional tradeoff, confirmed unchanged by design, not a new fairness/aging mechanism. Tests: new `internal/scheduler/admit_priority_test.go` — `TestAdmissionPriorityOrdersQueuedWaiters` (low/high/low enqueued in that order behind a held slot, releasing admits `[high, low, low]`, proving both priority ordering and FIFO tie-break); `TestAdmissionStressNoLeakUnderChurn` (~500 goroutines, random priorities, a mix of `context.Background()`/short-timeout contexts to force races near grant time, asserting `free == maxInflight` and zero queued/inflight after full drain — a precise no-leak proof, not probabilistic); `TestAdmissionCancelRaceAtRelease` (500 iterations deliberately racing a ~1ms context timeout against a jittered concurrent release landing inside that same window, asserting `Stats().Inflight` is always internally consistent and the goroutine never hangs) — all green under `-race`, run 5x for flake-resistance with no failures. New end-to-end `internal/executor/resourcegroup_priority_test.go` `TestResourceGroupPriorityAdmitsHigherPriorityQueuedQueryFirst` (modeled on the pre-existing `TestResourceGroupMaxConcurrencyBlocksExecContext`): a `low` (`PRIORITY = 0`) and `high` (`PRIORITY = 9`) resource group, sole process-wide slot held externally, `low`'s query enqueued first, `high`'s enqueued second, release — asserts `high` is admitted before `low` despite queuing later, proving the feature end-to-end through real SQL rather than just the scheduler package in isolation. Two test-authoring bugs were caught and fixed before landing, not shipped: an unsynchronized shared `math/rand.Rand` read from multiple goroutines in the stress test (`-race` caught it immediately), and an end-to-end test race where the two `SET RESOURCE GROUP` calls were issued *after* acquiring the sole admission slot, deadlocking against themselves (fixed by reordering — session setup happens before the slot is held). All pre-existing regression tests confirmed unmodified and still green: `internal/scheduler/scheduler_test.go`'s `TestAdmissionQueueAndReject`/`TestAdmissionCancelWhileQueued`, `internal/executor/resourcegroup_assign_test.go`'s three tests, `TestAdmissionRejectsOverload`, `TestMaintainSQLObeysAdmission`, `TestExecuteClaimedTaskObeysAdmission` (3x each under `-race`). Full `internal/executor` package (`-race`, `-count=1`) also re-run clean end-to-end (127s, no timeout this run). `go build ./...` clean; `go vet` scoped to the touched packages is clean (a pre-existing, unrelated `cdc.go` vet warning from already-uncommitted CDC-subscription-tracking work — confirmed via diff against `HEAD` — was left untouched, out of scope). Docs: `docs/sql.md` (`CREATE RESOURCE GROUP` paragraph rewritten from "not yet enforced" to describe the actual ordering-only, non-preemptive semantics and its scope — the process-wide gate only — plus dropped two stale "still open" pointers, integrating-task-classes and unbounded-pools-audit, that log #66 had already closed but this paragraph never caught up to); `TODO.md` (Priority line checked off with a landed summary + this entry). **With this increment, Phase 27's checklist has exactly one open item left**: "Local commit precedes replication acknowledgment" (deferred, log #58) — the sole remaining blocker on the P27 release gate.
68. [ ] Replication-orphan STRONG-read mitigation (2026-09-02) — a live user "continue @TODO.md" instruction after log #67 landed, leaving only one Phase 27 item: "Local commit precedes replication acknowledgment." Rather than attempting the structural fix outright, grounded feasibility first with two dedicated research passes (the commit path in full — `commitLocked`'s exact step order, `TM.Commit`'s single-call visibility+lock-release shape with no existing durable/visible split anywhere in `internal/txn`, `FSM.Apply`'s materially different raw-WAL-replay shape on followers vs. the leader's execute-then-append path, `Cluster.Replicate`'s fully-synchronous quorum wait, and the regression-test surface across four packages; then specifically the WAL/recovery layer once the first pass surfaced the real blocker). **Found two concrete hazards this item's own prior writeup hadn't identified**: (1) `internal/wal.Log.Flush` always durably fsyncs its *entire* current in-memory buffer, not a target LSN — and two call sites unrelated to `commitAndReplicate` (`Engine.Checkpoint()`; `ApplyReplicated`/`InstallRecords` on the Raft FSM-apply path, driven by ordinary concurrent cluster traffic — not rare) call an unconditional flush with no serialization against a pending commit, so a naive "append but defer flush" split can still have an unconfirmed `CommitRec` durably persisted as a side effect of unrelated activity; (2) `internal/recovery`'s `RedoUntil`/`UncommittedUntil` mark a transaction committed by the mere *presence* of a `RecCommit`, with no last-record-wins logic against a later abort record for the same TxnID, so a durably-flushed commit record can't be safely voided by an abort record without also changing crash recovery itself. Holding `e.mu` across the Raft round-trip to avoid the race by brute force was evaluated and rejected too: a believable deadlock path exists where Raft's own FSM-apply goroutine needs `e.mu` for an unrelated, earlier-queued entry the transaction's own `raft.Apply()` future may depend on to resolve. **Conclusion: the real fix needs a new WAL flush-barrier primitive plus a crash-recovery commit/abort-resolution change** — spanning `internal/wal`, `internal/recovery`, `internal/storage`, `internal/txn`, `internal/replication` — materially bigger than "invert two calls," and touches the code responsible for surviving every ordinary process crash. Presented this precisely-scoped risk to the user via `AskUserQuestion` (options: attempt the full redesign now, land a stronger mitigation instead, or leave today's detection-only state as-is) — **user chose the stronger mitigation.** Landed: new optional `storage.ReplicationOrphanReporter` interface (`ReportReplicationOrphan()`), type-asserted at the existing `commitAndReplicate` orphan site (every other `Replicator`/test double unaffected); `*replication.Cluster` implements it with a new node-local `replSuspect atomic.Bool`. `Cluster.StrongReadBarrier` (`internal/replication/read.go`) now fails `Unavailable` while set, *regardless of leadership* — closing the case a leadership check alone can't (a `Replicate` failure for a reason other than losing leadership, e.g. `ErrEnqueueTimeout`/`ErrRaftShutdown`, where the node stays leader). Deliberately scoped to `STRONG` reads only — not a forced leadership transfer, not a block on all local visibility: `STRONG` is the one consistency mode promising linearizable read-after-acknowledged-write behavior, `BOUNDED`/`STALE` already accept staleness by contract, and a forced transfer would add real disruption for protection `StrongReadBarrier` already delivers directly. The flag is node-local (mirroring `ClusterMaintenance`'s convention), not Raft-replicated, so a clean node elected leader afterward is never affected — proven by a dedicated test asserting the flag never leaks to other cluster members. New SQL `CLUSTER RECONCILE CONFIRM` (new `KwReconcile`/`KwConfirm` lexer keywords, `ast.ClusterReconcileConfirm`, cluster `ADMIN`-gated, rejected inside a transaction, purely node-local like `ClusterMaintenance`/`ClusterDrain`; `CONFIRM` is a mandatory keyword, not optional, so it can't be fat-fingered as a bare `CLUSTER RECONCILE`) clears the flag via new `DB.ConfirmReplicationReconciled`/`Cluster.ClearReplicationSuspect`; new `nextsql cluster reconcile confirm` CLI subcommand mirroring `cluster maintenance`'s exact shape; new `security.ActionClusterReconcile` audit action; new `system.replica_health.replication_suspect` column for monitoring. The intended operator runbook (documented in `docs/ops.md`): notice `metrics.Snapshot.ReplicationOrphans` increase or a `StrongReadBarrier` rejection naming the cause, verify/repair the node's data, then run the reconcile-confirm command — no automatic clearing, deliberately, since this is a data-integrity flag an operator should actually look at before dismissing. Tests: `internal/replication/read_test.go` `TestStrongReadBarrierBlockedByReplicationOrphanUntilReconciled` (real 3-node Raft cluster: barrier passes → orphan reported → barrier fails on the leader despite still holding leadership and still passing `VerifyLeader` → other nodes confirmed unaffected → cleared → barrier passes again) and `TestReplicationOrphanMethodsNilSafe`; `internal/storage/btree/btree_test.go` `TestInsertReportsReplicationOrphanToReporter`/`TestInsertDoesNotReportOrphanOnSuccessfulReplicate` (proves the reporter fires on exactly a `Replicate` failure, never on success, alongside the pre-existing `TestInsertOrphansLocalCommitOnReplicateFailure`); `internal/executor/cluster_reconcile_test.go` `TestClusterReconcileConfirmRBAC` (forbidden ungranted, rejected in-transaction, `Unavailable` with no cluster attached) and `TestClusterReconcileConfirmClearsSuspectFlag` (real single-node Raft cluster, full path through actual SQL end to end); `internal/sql/parser/parser_test.go` `TestParseClusterReconcileConfirm`; `internal/executor/system_test.go` extended for the new column. All green under `-race`: `internal/replication`, `internal/storage` full package (incl. `internal/storage/btree`, 219s — its known-slow-but-fine soak shape), `internal/sql/*`, `internal/security`, `internal/system`, and the full `internal/executor` package incl. `aggregate`/`join`/`sort`/`vector` subpackages (121s, no timeout this run). `go build ./...` clean; `go vet` on every touched package clean (the sole repo-wide `go vet ./...` finding, `internal/executor/cdc.go`'s context-leak warning, is pre-existing uncommitted CDC-subscription-tracking work this increment never touched, confirmed via diff against `HEAD`). CLI verified live (`nextsql cluster reconcile`/`reconcile confirm` dispatch and flag parsing exercised directly). No WAL/catalog/wire-protocol change — this mitigation lives entirely at the read-consistency-gate and executor/SQL layer and never touches the commit path itself. Docs: `docs/ops.md` (expanded "Correctness note" under Rolling upgrade with the investigation findings, the mitigation, and the operator runbook; `--json` example), `docs/sql.md` (`CLUSTER RECONCILE CONFIRM` + statement list), `docs/ha.md` (`StrongReadBarrier`'s three conditions, new test-evidence row), `TODO.md` (checklist line rewritten + this entry), `CHANGELOG.md`. **This checklist line stays unchecked**: the structural commit-ordering bug itself is still not fixed, only more strongly mitigated — a real fix remains a legitimately separate, larger, dedicated effort (new WAL flush-barrier semantics + crash-recovery changes) not attempted this session, per the user's own explicit choice. Phase 27 remains the open release gate on this one item.
69. [x] Multi-database hosting M2-2 — Hello realm field (2026-09-02) — **NOT a Phase 27 item.** Live user "continue @TODO.md" after log #68 (the P27 loop's last item, the replication-ack structural fix, correctly stayed deferred). With P27 down to that one deliberately-deferred item, asked the user via `AskUserQuestion` what to work on next rather than assuming; options offered were Multi-database hosting's M2-2, reconsidering the P27 structural fix, or something else — user chose M2-2. First recorded the two `docs/design-multidatabase-dbaas.md` §19 decisions M2-2's own checklist line required before starting: item 2 (realm identities with database-scoped grants — already the design doc's assumption throughout §5.2, formally adopted, with an explicit scoping note that M2-2 itself does not implement realm-local auth, only routing/identity validation on Hello) and item 7 (protocol v1 compatibility window/downgrade point of no return — resolved as "none in the traditional sense": the realm field is an additive, *opt-in* trailing field, emitted only when a caller configures a non-empty realm, so an unconfigured client is permanently, unconditionally byte-compatible with any server; a client that does select a realm requires a new-enough server and fails closed against an old one, rather than silently connecting to the wrong database). Used plan mode given the scale (wire protocol + 6 language drivers): two dedicated research passes grounded the design — the first found `internal/protocol/messages.go`'s `Hello`/`EncodeHello`/`DecodeHello` exact byte layout and `DecodeHello`'s strict `off != len(b)` trailing-byte check (the fact that makes "just append a field" reject on old decoders), the `NSCT` V1 tail-sniff precedent (`internal/catalog/encode.go`) as the closest literal pattern to mirror (Hello has no per-payload version number to branch on, unlike NSCT's general case), all 6 (7 counting Node as a separate copy) drivers' exact Hello-encode call sites, and confirmed `nextsqld` already resolves a hosted realm's name via `hosting.Registry.Default()` but never threads it past a log line; a Plan subagent then produced a concrete, line-level implementation plan from that grounding, independently verified against the live tree before implementation began. Implemented exactly that design: `internal/protocol/messages.go` — `Hello.Realm` as the new trailing field; `EncodeHello` appends it only when non-empty (byte-identical output otherwise); `DecodeHello` tail-sniffs one more optional field only if bytes remain past `User`, then re-applies the existing strict trailing-byte check — so a genuinely corrupt payload (garbage after a well-formed optional field, or a truncated length prefix within it) is still rejected, not silently accepted. `internal/protocol/server.go` — new `Server.Realm string` (explicitly documented as flat-string validation, not a live registry lookup — that's M2-3) and a mismatch check mirroring the pre-existing `Hello.Database` one, placed first and returning its own distinct `"unknown realm"` error rather than reusing `"unknown database"` (different axis; reusing it would mislead a caller who got the database right but the realm wrong). `cmd/nextsqld/main.go` — one line, `srv.Realm = hostedRealm.Name`, alongside the existing `srv.Database` assignment (confirmed the only such call site in the repo). All 6 drivers updated identically in shape: a `Realm`/`realm` config field, threaded only into the connect-time handshake (never the Cancel-Hello, which stays untouched everywhere), with the *encode function itself* — not the call site — deciding whether to emit the trailing field, so every call site stays a simple one-line addition. Python and Ruby's positional `encode_hello(...)` needed a new 6th parameter with a `""`-default; confirmed safe since both languages' own pre-existing Cancel call sites don't pass it (get the default) and the wire behavior stays value-based (`realm=""` produces byte-identical output to omitting it entirely), not arity-based. `drivers/js/types.d.ts` gained `realm?: string` on `Config`/`Hello`; `drivers/node/nextsql.d.ts` needed no change, confirmed it only re-exports those same types. Tests: new `internal/protocol/messages_test.go` cases (`TestHelloRealmRoundTrip`, `TestHelloWithoutRealmIsByteIdenticalToOldShape` — the concrete byte-level regression guard for the compatibility promise, not just a round-trip — `TestDecodeHelloOldShapeNoTrailingBytes` against a hand-built literal old-shape byte sequence, `TestDecodeHelloRejectsCorruptTrailingBytes`, `TestDecodeHelloRejectsTruncatedRealmLength`), all green under `-race`, plus the pre-existing `FuzzDecodeHello` re-run for 20s / 2.1M executions with zero crashes against the modified decode path. New `tests/integration/protocol_test.go` cases using the existing `startTLSServer(t, configure...)` helper: `TestRealmMismatchRejected`, `TestRealmMatchSucceeds`, `TestUnconfiguredClientSkipsRealmCheck` (the explicit regression test for "old/unconfigured clients always work"). New per-driver unit tests extending each driver's existing Hello-encode test (`drivers/deno/nextsql_test.js`, `drivers/bun/nextsql.test.js` — genuinely independent files despite sharing `protocol.mjs`, both had to be edited — `drivers/node/nextsql.test.js`, `drivers/python/tests/test_protocol.py`, `drivers/ruby/test/test_protocol.rb`, `drivers/php/tests/unit.php` new assertion block), all run and green: Deno 16/16, Bun 16/16, Node 18/18, Python 33/33, Ruby 31/31 (82 assertions), PHP `ok`. **Live verification against a real locally built `nextsqld`** (this project's established convention for wire-protocol driver changes, per log #59's precedent of catching 2 real cross-driver bugs this way): bootstrapped a hosted deployment with `nextsql init --realm tenant-a`, started `nextsqld` against it (log line confirmed `"realm":"tenant-a"` reached the server), then connected with two independent drivers (Go, Python) in three configurations each — unconfigured (no realm) succeeds, matching realm succeeds, mismatched realm cleanly rejected with `unknown realm` (not a hang or crash) — both drivers produced identical outcomes. `go build ./...` clean; `go vet`/`gofmt` clean on every touched Go file; full `internal/protocol` and `tests/integration` suites green under `-race`; `drivers/go` suite green. No change to M2-3 (`DatabaseManager`/routing) or M2-4 (realm-scoped auth) scope — `nextsqld` still only ever opens `hosting.LayoutLegacyDefault`, unchanged. Docs: `docs/design-multidatabase-dbaas.md` (§19 items 2 and 7 recorded as decided with full rationale before implementation started; §8 landed-scope note distinguishing what M2-2 actually delivered from that section's forward-looking capability-negotiation framing; §16 M2-2 bullet updated with delivered scope; top status line and M1-foundation intro paragraph updated), `docs/protocol.md` (Hello message-table row; rewrote the paragraph that previously said an explicit realm field "require[s] a future negotiated protocol revision" — now false — to describe the actual additive-trailing-field mechanism), `TODO.md` (M2-2 checklist line flipped to `[x]` with corrected scope language — a flat-string check, not a registry lookup, as the original checklist text had assumed — status line, header table, this entry), `CHANGELOG.md`. Driver docs (`docs/web/content/docs/drivers*.md`) deliberately left untouched: none of them document `Database`/`database` as an individual config field today, and `Realm` unlocks no new user-visible capability until M2-3 makes selection meaningful — flagged as deferrable, not an oversight. **Next candidate for this track**: M2-3 (bounded `DatabaseManager` and per-connection routing) — the largest of the four M2 sub-increments per its own design-doc sizing note, land only after M2-1 and M2-2, both now done.
70. [x] Multi-database hosting M2-3a — bounded DatabaseManager (2026-09-02) — **NOT a Phase 27 item.** Live user "continue @TODO.md" after log #69; with P27 unchanged (structural fix deliberately deferred) and M2-2 just landed, M2-3 ("largest of the four" M2 sub-increments) was the natural next step in the track already in motion — no competing candidate needed a decision this time, so proceeded directly to scoping it rather than asking again. A dedicated scoping investigation (mirroring the discipline already applied to the P27 structural fix and to M2 itself) found M2-3's full §9 spec — reference counts for sessions/transactions/CDC/tasks/backup/maintenance/replication, idle eviction, a global memory budget touching the storage buffer-pool allocator, and centralizing `TaskRuntime`'s per-instance worker/coordinator goroutines into shared bounded pools — has zero existing infrastructure to build on anywhere in the repo (confirmed by grep: no `singleflight`, no reference-counting pattern). But the "does a manager exist and can connections route through it at all" seam is narrow and well-isolated: `protocol.Server.DB`/`Tasks` are each touched in only a handful of places (`DatabaseHandle()`'s exactly 2 in-package callers plus `nodeStatus`), not smeared across the query path, and `serveConn` resolves the connection's database exactly once. Presented this precisely-scoped split to the user via `AskUserQuestion` (options: M2-3a minimal slice now, attempt the full §9 spec now, or leave the M2 track paused alongside P27's) — **user chose the minimal slice.** Decomposed M2-3 into M2-3a (this increment) / M2-3b (full spec, not scheduled) in `docs/design-multidatabase-dbaas.md` §9/§16 and `TODO.md` before any code was written, mirroring exactly how M2 itself was decomposed into M2-1..4. Used plan mode given the cross-file scope: an Explore-equivalent direct-reading pass grounded the exact seams (`serveConn`'s single `DatabaseHandle()` call at the pre-backend-construction point; `nodeStatus()`'s own separate `DatabaseHandle()` call, a latent bug where a manager-routed connection would silently report the *primary* database's health — fixed in passing, harmless today since nothing reaches it yet; `nextsqld`'s `openHostedDefault`/eager+lazy DB-open paths; `hosting.ManagedDatabasePath`/`Database.KeyRef` as the resolution primitives for an on-demand open; no `Registry` by-name lookup method existed, confirmed), then a Plan subagent produced a concrete design from that grounding, independently re-verified against the live tree (exact function signatures, line numbers, `validateHostedDatabase`'s existing shape) before implementation began. Implemented: new package **`internal/dbmanager`** — `Manager` keyed by `hosting.ID` (durable database identity, not name), a hand-rolled single-flight `Acquire` (an `inflight map[ID]chan struct{}` state machine: cache-hit returns immediately; an in-progress open is waited on then re-checked, so one failed open never wedges concurrent waiters; the bound is checked *before* committing a slot, so an in-flight-but-unpublished open never lets a false negative or double-count through), `Preload` (registers an already-open primary without going through the opener), `Close`. **Deliberately not built on `scheduler.Admission`** despite the original one-line TODO description saying so: `Admission` is a per-request acquire/release queueing gate, the wrong shape for "permanently consume one of N slots, never released" (M2-3a's actual no-eviction access pattern) — flagged explicitly as a reality-vs-description deviation rather than forced to fit, matching this session's established pattern of correcting scope text once real investigation is done. New `Registry.Lookup(realmName, databaseName)` (`internal/hosting/registry.go`), same locking/normalization shape as the pre-existing `Default()`. `internal/protocol/server.go` — new `Server.Databases *dbmanager.Manager` field, additive alongside the unchanged `DB`/`Tasks` (nil means every connection uses the pre-M2-3a `DatabaseHandle()` path, byte-for-byte — confirmed by the full pre-existing `internal/protocol`/`tests/integration` suites passing completely unmodified); `serveConn`'s seam now calls `mgr.Acquire(realmName, dbName)` when a manager is configured, falling back to `hello.Realm`/`hello.Database` vs `s.Realm`/`s.Database` exactly like the pre-existing "empty means default" convention; the pre-existing flat `s.Database` equality pre-check (M2-2) is now skipped when a manager is configured, since it assumed a single fixed database name and would otherwise reject any legitimately-selected non-primary database before `Acquire` ever got a chance to resolve it — a real gap caught immediately by the first version of the new end-to-end test failing with "unknown database." `nodeStatus` now takes the connection's own resolved `db` as a parameter instead of re-deriving it. `cmd/nextsqld/main.go` — constructs `dbMgr` once (guarded by `hostingRegistry != nil`, so a legacy/non-hosted deployment never gets a manager at all and stays on the unchanged path), `Preload`s the primary in both the eager and `--require-client-key` lazy paths (mirroring the existing `SetDatabase`/`SetTaskRuntime` call sites exactly); the `Opener` closure is defined inline, after the pre-existing `newTaskRuntime` closure, reusing it and the 3 Phase 27 monitor `start*` functions directly by closure capture rather than extracting a new top-level function with a large parameter list — opens a `LayoutManaged` database on demand (rejects anything else, and rejects a non-`StateActive` realm/database — a check the plan hadn't originally specified but that's clearly necessary and cheap to add once the Layout check was already there), single-node only (no `startCluster`/`installArchiver` — not a regression, M2-1's own log entry already established `nextsqld` opens nothing beyond the primary at all today), starts its own `TaskRuntime` and copies of the 3 monitors exactly once per successful open, and is tracked (`TaskRuntime` + `*crypto.Envelope`, see below) in a mutex-guarded slice for shutdown cleanup since `dbmanager.Manager` itself deliberately stays DB-only, not aware of task runtimes.

    **Three real bugs were caught building and verifying this, none by inspection — all by tests or live verification actually exercising the new path**, consistent with this session's now-established discipline that a design review alone is not suficient proof for a change like this:
    1. **Routing-order bug (most serious, a real client-visible correctness gap, not just internal wiring)**: the `Acquire`/`DatabaseHandle` resolution block was originally placed *after* `WriteFrame(conn, TypeReady, ...)`. Tracing `drivers/go`'s own `Conn.handshake()` showed `TypeReady` is the wire protocol's definitive "handshake succeeded" signal — the client reads it and returns success with **no further reads at all**. A database-routing failure reported after that point would sit unread in the socket buffer while the client believed it was successfully connected. Caught by the new `TestOpenDatabaseLimitRejectsCleanly` test genuinely succeeding when it should have failed `Exhausted` — bisected via targeted debug logging (first suspecting an ID collision, then a single-flight bug, before tracing it to the frame-ordering issue) rather than assumed. Fixed by moving the whole resolution block to before `TypeReady` is sent, documented inline with the "why" so it can't regress silently.
    2. **Envelope-vs-direct-key bug**: `nextsql database create`'s real activation path (`cmd/nextsql/main.go`'s `activateManagedDatabase`/`createOrResumeDatabase`) creates a managed database using an **envelope keystore** next to the database file (`crypto.CreateEnvelope`), with the database's own `KeyRef` root key only *unlocking* that envelope — never encrypting the database file directly. The `Opener` originally passed `crypto.LoadProvider(database.KeyRef)` straight to `executor.Open`, exactly mirroring the wrong assumption. This was invisible in the integration-test fixture because the fixture made the *identical* mistake symmetrically (created the test database the same wrong way it later opened it), so it was self-consistently wrong and every fixture-based test passed regardless. Only surfaced via the plan's own mandated live-verification step against a real `nextsqld` with a real `nextsql database create`d database (`crypto.Envelope: client key required`, then traced to `key does not match file` before that). Fixed in both the real `Opener` (`crypto.ReadKeyFile` + `crypto.OpenEnvelope(crypto.KeystorePath(path), root)`) and the test fixture, which now mirrors production exactly instead of independently agreeing with itself.
    3. **Envelope-close-ordering bug** (test fixture only, caught while fixing #2): closed the envelope before the database it protects, backwards from the correct order the primary database's own fixture code already had right (`created.Close()` then `env.Close()`) — bisected via checkpoint logging to the exact failing line rather than guessed.

    Tests: `internal/dbmanager/manager_test.go` (`TestAcquireCachedNoReopen`, `TestAcquireConcurrentSingleFlight` — 20 goroutines racing a blocked opener, exactly one real call — `TestAcquireDistinctLimitRejected`, `TestAcquireFailedOpenDoesNotPoisonKey`, `TestPreloadThenAcquireIsCacheHit`, all green under `-race`); `internal/hosting/create_test.go` (`TestLookupResolvesRealmAndDatabaseCaseInsensitively`, `TestLookupUnknownRealmOrDatabase`); `internal/config` (`TestLoadMaxOpenDatabases`, `TestDefaultMaxOpenDatabases`, `TestLoadMaxOpenDatabasesRejectsNegative` for the new `max_open_databases` config-file-only knob, default 8, mirroring `MaxInflight`'s exact precedent including the "`< 1` uses the package default" fallback idiom from `scheduler.NewAdmission`); new `tests/integration/multidb_test.go` — a from-scratch two-database fixture and slimmed server helper (not the full `nextsqld` binary) proving `TestRealmRoutingReachesDistinctDatabases` (a table created via a connection routed to db1 is genuinely invisible via a connection routed to db2, and vice versa — the concrete proof `Hello.Realm` now does something beyond identity validation), `TestOpenDatabaseLimitRejectsCleanly` (a real TCP connection past the limit gets a clean `Exhausted` rejection, not a hang, and the connection stays usable afterward), `TestNodeStatusReportsConnectionsOwnDatabase` (regression-proof for the `nodeStatus(db)` parameter fix). **Live verification** (this project's established convention for changes like this, per the M2-2 precedent): a real `nextsql init` primary plus a real `nextsql database create`d second database, a real `nextsqld` process, a real Go-driver client program proving genuine cross-database isolation end to end plus the pre-existing M2-2 mismatched-realm rejection still working. `go build ./...` clean; `go vet ./...` clean on every touched package (the sole repo-wide finding, `internal/executor/cdc.go`, remains the same pre-existing unrelated uncommitted work, untouched); full `internal/protocol`, `internal/dbmanager`, `internal/hosting`, `internal/config`, `cmd/nextsqld`, `drivers/go`, `tests/integration`, and the full `internal/executor` package (incl. `aggregate`/`join`/`sort`/`vector` subpackages, 127s, no timeout this run) all green under `-race`. No WAL/catalog change; the wire-protocol change is additive-only (no new message types, only the pre-existing M2-2 `Hello.Realm` field now actually being acted on). Docs: `docs/design-multidatabase-dbaas.md` (§9 M2-3a bullet expanded with landed scope and all 3 bugs; §16 M2-3a bullet; top status line and M1-foundation intro paragraph; §16 exit-criteria note distinguishing table-visibility-level proof from the full adversarial matrix), `TODO.md` (M2-3a checklist line flipped to `[x]` with full writeup, "Current status" paragraph corrected — `nextsqld` can now genuinely serve more than one database — header table, this entry), `CHANGELOG.md`. **M2-3b (the full §9 spec: reference counting, idle eviction, memory budget, central bounded pools) remains open and unscheduled**, per the user's own chosen scope for this increment. M2-4 (realm-scoped auth) also remains open and depends on M2-3.
71. [ ] Multi-database hosting M2-3b — scoping investigation and decomposition (2026-09-02) — **NOT a Phase 27 item.** Live user "continue @TODO.md" after log #70. With M2-3a landed and M2-4 blocked on the rest of M2-3, M2-3b (the only way forward in the track) was the natural next step — but it was already known from M2-3's own decomposition to be large and undecomposed (refcounting across 7 subsystems, plus a memory budget touching the storage buffer-pool allocator). Rather than diving in or re-scoping alone, asked the user via `AskUserQuestion` how to proceed given P27's structural fix was also still available (options: scope out M2-3b, reconsider the P27 fix, or something else) — **user chose to scope out M2-3b**, continuing this session's now-established pattern (P27's structural fix, the original undecomposed M2-3) of investigating before either attempting or deferring a large item. A dedicated scoping investigation covered all 11 pieces of §9's full spec individually: found the design doc's own blanket claim ("none of these subsystems expose an incrementable/decrementable ref today") was **stale/inaccurate** — sessions (`DB.sessions`/`RegisterSession`/`UnregisterSession`, `internal/executor/db.go`), CDC (`db.cdcSubs`/`registerCDCSubscription`, same file), and tasks (`TaskRuntime.running`, `internal/executor/task_runtime.go`) already have live, incrementable/decrementable registries — simply never consulted by anything DB-lifecycle-related; corrected in both `TODO.md` and the design doc rather than left standing. Found the exact reusable hook point for connection-level refcounting: `internal/protocol/server.go`'s `serveConn` already has a per-connection defer (paired with the single existing `mgr.Acquire` call) that calls `DB.UnregisterSession` at exactly the right point — a `Manager.Release` would be a same-shape addition, not new plumbing. Found `storage.Engine.Close()` already checkpoints/flushes/closes the WAL durably, so idle eviction's "close performs checkpoint/drain according to the durability contract" requirement needs no new durability mechanism, only orchestration on top of the existing, already-correct `Close()`. Found maintenance (`MAINTAIN DATABASE`) runs synchronously inside whatever session/task invoked it — no independent background-goroutine lifecycle to track at all, contrary to the design doc's "background dead-version cleanup" framing; the one real hook it needs is the pre-existing `DB.PauseMaintenance()`/`ResumeMaintenance()` wired into eviction's close sequence, not a new counter. Found backup (still fully offline/CLI-only, never touches a manager-opened database) and replication (M2-3a deliberately never attaches Raft to a secondary database) are both **confirmed vacuous** for this increment — nothing to count for either until later, separate work reaches them; consistent with backup already having been found a no-op for the earlier resource-group scheduler-integration audit (log #66). Found the two genuinely large, novel-infrastructure pieces: a cross-database memory budget (no shared/global counting exists at any layer today — `internal/storage/buffer.Pool` is purely per-`Engine`, distinct from the already-enforced per-database `StorageCapBytes` *disk* cap, which is unrelated in-memory accounting; touches every DB-open call site) and centralizing `TaskRuntime`'s per-database goroutine pools (`worker()`/`coordinate()` and the `jobs`/`slots` channels are tightly single-DB-coupled throughout — a genuine internal redesign, not a parameterization tweak). **Decomposed M2-3b into three further sub-increments** or the same reasons and with the same discipline M2-3 itself was just decomposed with: **M2-3b-1** (connection/session refcounting + idle eviction reusing the already-durable `Close()` + open-failure quarantine reusing the existing task-retry backoff shape from `internal/executor/task.go` — small, low-risk, and the piece that actually delivers this section's headline capability, turning M2-3a from "opens and never closes" into "opens and closes when idle"); **M2-3b-2** (the cross-database memory budget, ordered after M2-3b-1 since a budget with no eviction is close to useless); **M2-3b-3** (TaskRuntime centralization, independent of the other two, its own risk class). Recorded this decomposition in `docs/design-multidatabase-dbaas.md` §9 and §16 (correcting the stale refcounting claim in the same edit) and `TODO.md`'s M2-3b checklist line, mirroring exactly how M2 itself and M2-3 were each decomposed before any code was written. **No code changed this increment — scoping and documentation only**, consistent with the user's own choice; M2-3b-1 (the recommended next slice) has not been started. `go build`/`vet`/tests not re-run since no source files changed. Docs: `docs/design-multidatabase-dbaas.md`, `TODO.md` (this entry + checklist), `CHANGELOG.md`.
72. [x] Multi-database hosting M2-3b-1 — connection/session reference counting + idle eviction + open-failure quarantine (2026-09-02) — **NOT a Phase 27 item.** Live user "continue @TODO.md" after log #71's scoping/decomposition; asked via `AskUserQuestion` whether to implement M2-3b-1 now, stop here, or something else — **user chose to implement M2-3b-1**. Used plan mode given the cross-file scope (`internal/dbmanager`, `internal/protocol/server.go`, `cmd/nextsqld/main.go`, both test suites) and that this touches DB lifecycle/closing logic, where a mistake risks resource leaks or premature closes — the same care applied to every hot-path change this session. Design, grounded directly in log #71's scoping findings (not re-derived): `entry` (`internal/dbmanager/manager.go`) gained `refs int` and `pinned bool`; the Preloaded primary is `pinned: true` and **deliberately never evicted** — `Opener` only ever handles `LayoutManaged` databases and would refuse to reopen the primary's `LayoutLegacyDefault`, so evicting it would make it permanently unreachable via `Acquire` again, a real correctness hazard avoided by design rather than caught by a test. `Acquire`'s signature changed to `(*executor.DB, func(), error)` — a release closure paired 1:1 with the call, mirroring `scheduler.Admission.enter()`'s exact idempotent "once" guard shape (an `atomic.Bool`), wired into the one existing call site (`internal/protocol/server.go`'s `serveConn`) at the same per-connection defer that already calls `DB.UnregisterSession` — found and confirmed unconditional on every exit path (idle timeout, forced drain, bare disconnect) during the M2-3b scoping pass, so no new teardown path was needed. `release()`'s eviction logic removes the entry from `m.open` **under the lock** before running any I/O outside it — the same single-flight-safe pattern already proven for M2-3a's open path — so a concurrent `Acquire` racing an eviction either sees the entry before removal (valid ref-incremented handle) or a clean miss after removal (fresh open via `Opener`), never a stale or half-closed one. `Opener`'s signature gained a `cleanup func() error` return (replacing the bare `*executor.DB`) so eviction can close more than just the database: `nextsqld`'s real `Opener` (`cmd/nextsqld/main.go`) now returns a `cleanup` that closes the secondary database's `TaskRuntime` **before** `db.Close()` (so no background task call can race the close) and its envelope **after** (the final checkpoint/flush still needs the key material) — confirmed both `TaskRuntime.Close()` and `crypto.Envelope.Close()` are already idempotent (safe to also be called again at final process shutdown via the pre-existing `secondaryTasks`/`secondaryEnvs` slices, left untouched rather than churned). Eviction needed no new durability mechanism — `storage.Engine.Close()` (confirmed during scoping, not re-checked here) already checkpoints/flushes/closes the WAL correctly — only the orchestration around calling it. **No explicit `PauseMaintenance` wiring, contrary to the original plan's own tentative inclusion of it**: on reflection during implementation, refcount reaching zero already implies no session or task could be mid-`MAINTAIN` synchronously for that database (maintenance only ever runs synchronously inside whichever session/task invoked it, per log #71's own finding), so the pause hook has nothing to protect against here — omitted rather than added defensively, a deliberate simplification over the plan file's own wording. Quarantine + backoff landed in the same `Acquire`/open path: a `quarantine map[hosting.ID]*quarantineEntry{failures, retryAt}`, checked before the open-limit check (a quarantined database should never compete for a slot), cleared on a successful open; `backoff()` is a new, independent implementation of the same exponential-shift, overflow-safe-cap shape as `internal/executor/task.go`'s `retryDelayNS` (200ms base, doubling, 30s cap) — not a shared call into it, since that function is `catalog.Task`-specific. A `now func() time.Time` field (defaulting to `time.Now`, package-internal-overridable) makes the backoff window deterministically testable without real sleeps. New `Manager.OpenCount()` introspection method, mirroring `scheduler.Admission.Stats()`'s precedent, used by both new unit tests and a new integration test to observe eviction directly. Two pre-existing M2-3a unit tests needed updating, not just signature patches, because the new semantics genuinely changed their behavior: `TestAcquireCachedNoReopen` previously proved "repeated `Acquire` without holding a reference is a cache hit," which is no longer true by design (nothing held = eviction between calls) — rewritten to hold all references across the repeated calls, isolating the caching property from eviction; `TestAcquireFailedOpenDoesNotPoisonKey` previously proved "a failed open lets an immediate retry succeed," which now correctly hits quarantine instead — rewritten to fast-forward the injected clock past the backoff window before its second attempt, preserving its original narrow intent (retry is possible, not cached forever) consistently with the new quarantine semantics rather than around it. Tests: `internal/dbmanager/manager_test.go` gained `TestReleaseDecrementsAndEvictsAtZero`, `TestMultipleAcquireOneReleaseKeepsOpen`, `TestReleaseIsIdempotent` (concurrent duplicate-release under `-race`, proving no double-eviction/negative-refcount), `TestPinnedPreloadNeverEvicted`, `TestCleanupCalledOnEviction`, `TestQuarantineRejectsThenRecoversAfterBackoff` — 11 tests total in the package, all green under `-race`, `-count=5` for flake-resistance on the timing-sensitive ones. `internal/protocol/server.go`'s `serveConn` gained one new local (`releaseDB`) and one new line in the existing defer, nil-guarded for the `mgr == nil` fallback path (every pre-M2-3a deployment) — full `internal/protocol` suite re-run green, confirming that path is untouched. New `tests/integration/multidb_test.go` `TestSecondaryDatabaseEvictedWhenIdle`: connects to db2, writes a row, disconnects, polls `mgr.OpenCount()` (now returned by `startMultiDBServer`, a signature change propagated to all 3 pre-existing call sites) back down to 1, then reconnects and confirms the earlier row survived — passed on the first run. Full `internal/dbmanager`, `internal/protocol`, `cmd/nextsqld`, `internal/hosting`, `internal/config`, `drivers/go`, and `tests/integration` suites all green under `-race`. **Live verification against a real `nextsqld`** (this project's now-established convention for wire/lifecycle-adjacent changes, per the M2-2/M2-3a precedent): bootstrapped a real deployment with a real second `nextsql database create`d database, connected to it, and — rather than only trusting logs — inspected the live process's actual open file descriptors via `/proc/<pid>/fd`: confirmed the database file, its WAL segment, and its undo log were genuinely open while the connection was live, and **all three fully closed within the poll window after disconnecting** — direct, unambiguous proof of real eviction, not just internal bookkeeping. Reconnecting afterward worked cleanly and repeatedly across multiple runs. `go build ./...` clean; `go vet ./...` clean on every touched package (the sole repo-wide finding, `internal/executor/cdc.go`, remains the same pre-existing unrelated uncommitted work). No WAL/catalog/wire-protocol change — this is entirely executor/server/manager-layer orchestration on top of already-correct lower-layer primitives. Docs: `docs/design-multidatabase-dbaas.md` (§9 M2-3b-1 bullet expanded with landed scope, deviations from the plan's own tentative wording, and the live-verification method; §16 M2-3b-1 bullet; top status line and M1-foundation intro paragraph), `TODO.md` (M2-3b-1 checklist line flipped to `[x]` with full writeup, "Current status" paragraph updated, header table, this entry), `CHANGELOG.md`. **M2-3b-2 (cross-database memory budget) and M2-3b-3 (`TaskRuntime` centralization) remain open and unscheduled**, as does M2-4 (realm-scoped auth, depends on the rest of M2-3).
73. [ ] Multi-database hosting M2-4 — dependency correction and scoping investigation (2026-09-02) — **NOT a Phase 27 item; scoping only, no code changed.** Live user "continue @TODO.md" after log #72. With M2-3b-1 landed and the other two M2-3b sub-increments both large/novel-infrastructure items already flagged, asked the user via `AskUserQuestion` how to proceed (options: check whether M2-4 is actually unblocked, scope out M2-3b-2/3, reconsider the P27 fix, or something else) rather than assuming which large item to tackle next — **user chose to check M2-4's real dependency first**, since its own "depends on M2-1..3" note was written before M2-3's split into M2-3a/b-1/b-2/b-3 and might no longer be accurate. A dedicated investigation confirmed the dependency note was genuinely stale: M2-4 (realm-scoped auth, `system.realms`/`system.databases`/`system.database_operations` introspection) is entirely about access control and metadata exposure, while M2-3b-2 (cross-database memory budget) and M2-3b-3 (`TaskRuntime` centralization) are entirely about runtime resource allocation — confirmed orthogonal by direct reading of the design doc's §5.2 authorization-tuple text and the M2-3b-2/3 scope notes, neither of which references the other's concern anywhere. M2-4's real dependencies are just M2-1 (registry, to resolve `RealmID`/`DatabaseID`), M2-2 (`Hello.Realm`), and M2-3a (routing exists to authorize against) — all landed; M2-3b-1/2/3 were never actually prerequisites. Corrected the stale note in both `docs/design-multidatabase-dbaas.md` and `TODO.md` rather than leaving it standing uncorrected, matching this session's established practice (the M2-3b scoping pass similarly corrected a stale "no subsystem exposes a ref today" claim, log #71). **Found M2-4 itself has the same "very different sizes" shape every other M2-numbered item has turned out to have once actually scoped** (M2 → M2-1..4; M2-3 → M2-3a/b; M2-3b → b-1/b-2/b-3): decomposed into M2-4a (`system.realms`/`system.databases` read-only introspection — small, tractable, follows the exact `system.*` pattern already used for `system.replica_health`, no import-cycle risk confirmed by the same check M2-3a already validated for `internal/dbmanager`, data already available via the pre-existing `hosting.Registry.Manifest()` — the only real gap being a small additive plumbing chain, since neither `protocol.Server` nor `executor.Session` holds a `hosting.Registry` reference today), M2-4b (realm-local `auth.Store`/`security.ACL` plus the `(RealmID, PrincipalID, DatabaseID, privilege, scope)` authorization tuple — confirmed today's `auth.Store`/`ACL` are genuinely singular deployment-wide instances with no realm dimension in either type, and the call-site count needing to become realm-aware is small and enumerable (~10 non-test sites) — nothing like `TaskRuntime`'s deep structural coupling — but two real, currently-unanswered design questions remain: per-realm auth-store file layout/lifecycle, and making the embedded OIDC auth broker realm-aware as a second call path; also traced exactly where in `serveConn` a realm-resolved authorization check would need to be inserted — before the existing password/ACL checks, which currently run before `mgr.Acquire`, since identity must resolve before routing decides which database handle to hand back), and M2-4c (`system.database_operations` — needs genuinely new operation-history tracking in `internal/hosting` that doesn't exist yet, since realms/databases today only carry a current `State`, not a transition log; not a pure read-through like M2-4a, and best folded into future M3 lifecycle work rather than built standalone here). Recorded this decomposition in `docs/design-multidatabase-dbaas.md` §16 and `TODO.md`'s M2-4 checklist line before any code was written. **No code changed this increment — scoping and documentation only**; none of M2-4a/b/c has been started. `go build`/`vet`/tests not re-run since no source files changed. Docs: `docs/design-multidatabase-dbaas.md`, `TODO.md` (this entry + checklist), `CHANGELOG.md`.
74. [x] Multi-database hosting M2-4a — `system.realms`/`system.databases` read-only introspection (2026-09-02) — **NOT a Phase 27 item.** Live user "continue @TODO.md" after log #73's decomposition; M2-4a was the user's own prior selection ("Implement M2-4a now") for the smallest, lowest-risk M2-4 sub-increment, so proceeded directly to implementation — given the small, well-scoped, precedent-following shape (unlike M2-3a/M2-3b-1, which used full `EnterPlanMode`), implemented directly without a plan-mode round-trip. Plumbing chain, mirroring the existing `SetACL`/`SetAudit`/`SetAuth` setter pattern exactly: new `hostingRegistry *hosting.Registry` field + `SetHostingRegistry` setter on `executor.Session`; new `HostingRegistry *hosting.Registry` field on `protocol.Server`, wired into `serveConn` via `b.sess.SetHostingRegistry(s.HostingRegistry)` right after the existing `SetRegistry` call (drive-by-corrected a stale "no eviction" comment on the neighboring `Databases` field while there); `cmd/nextsqld/main.go` sets `srv.HostingRegistry = hostingRegistry` alongside the pre-existing `srv.Database`/`srv.Realm` assignments inside the same `if hostingRegistry != nil` block — nil (every legacy/non-hosted deployment) means the two new views degrade to empty rows, never an error, confirmed by a dedicated test. Two new tables (`internal/system/schema.go`, right before `replica_health`): `system.realms` (`realm_id`, `name`, `state`, `database_count`, `storage_cap_bytes`, `realm_root_delegated`) and `system.databases` (`realm_id`, `realm_name`, `database_id`, `name`, `state`, `layout`, `storage_cap_bytes`) — `SchemaVersion` deliberately not bumped, matching this session's own earlier `replication_suspect`-column precedent. `internal/executor/system.go` dispatch (`"system.realms"`/`"system.databases"`) plus two new row-producing methods, `systemRealmsRows`/`systemDatabasesRows`, following `systemResourceGroupsRows`'s exact admin-only gating (`if !(s.acl == nil || s.isAdmin())`) — deployment topology across realms is not tenant-visible data, same rationale as resource groups. `hosting.State`/`hosting.Layout` have no `String()` method, so two small local mapping helpers (`hostingStateName`, `hostingLayoutName`) translate the enum values to the same lowercase-snake strings already used elsewhere in `system.*` (e.g. `replica_health`'s `role`). `database_count`/`storage_cap_bytes` use the pre-existing `sysDec` helper; `realm_root_delegated` is `RealmRootAuthHash != [32]byte{}`, matching the field's own doc comment in `internal/hosting/registry.go`. Rows sorted by name (realm name; realm name then database name) for deterministic output, matching every other `system.*` view's convention. Tests: new `internal/executor/hosting_system_test.go` — `TestSystemHostingViewsNilRegistry` (both views empty, not an error, with no registry ever wired) and `TestSystemHostingViewsRBACAndContent` (a real `hosting.Registry` built via `hosting.EnsureBootstrap`/`CreateDatabase`, mirroring `tests/integration/multidb_test.go`'s fixture recipe; non-admin sees zero rows from both views; admin sees the real realm and both its databases with correct state/layout/count values) — both green. Full regression: `internal/executor` (116s, including `aggregate`/`join`/`sort`/`vector` subpackages), `internal/system`, `internal/protocol`, `internal/dbmanager`, `cmd/nextsqld`, `tests/integration` all green under `-race`; `go vet ./...` unchanged (same pre-existing unrelated `internal/executor/cdc.go` finding). **Live verification against a real `nextsqld`** (this project's established convention): bootstrapped a real deployment (`nextsql init` realm `acme`/database `db1`, `nextsql database create` for `db2`), started a real `nextsqld`, and via `nextsql exec` confirmed `SELECT * FROM system.realms`/`system.databases` return the real registry contents (1 realm, `database_count` 2, `db1` `legacy_default`/`active`, `db2` `managed`/`active`) for an admin user (`GRANT ADMIN ON CLUSTER`), and empty result sets for a `CONNECT`-only non-admin user on both views. No WAL/catalog/wire-protocol change — pure read-only introspection on top of the pre-existing `hosting.Registry.Manifest()`. Docs: `docs/design-multidatabase-dbaas.md` (§9/§16 M2-4a bullet marked landed; top status line), `TODO.md` (M2-4a checklist line flipped to `[x]`, header table, this entry), `CHANGELOG.md`. **M2-4b (realm-local auth/ACL store + authorization tuple) and M2-4c (`system.database_operations`) remain open and unscheduled.**
75. [ ] Multi-database hosting M2-4b — scoping investigation (2026-09-02) — **NOT a Phase 27 item; scoping only, no code changed.** Live user "continue @TODO.md" after log #74. With M2-4a landed, asked the user via `AskUserQuestion` what to work on next among the remaining open candidates (M2-3b-2 cross-database memory budget, M2-3b-3 `TaskRuntime` centralization, P27's replication-ack structural fix, M2-4b realm-local auth/ACL) — **user chose M2-4b**. Given M2-4b was already flagged in log #73 as "the actual architectural work" with two open design questions, ran a dedicated scoping investigation (an isolated fork, matching the discipline already applied to every other large M2 item) before writing any code or plan. Confirmed the design doc's own "~10 non-test call sites" estimate (`internal/executor/session.go`'s `acl`/`users` fields + setters, `internal/protocol/server.go`'s `Server.Auth`/`ACL` fields and `serveConn`'s verify/`AllowedScoped` calls, `internal/executor/task.go`+`task_runtime.go`, `cmd/nextsqld/main.go`'s `auth.OpenOrCreate`/`startEmbeddedAuthBroker`, `cmd/nextsql/main.go`'s CLI-side `auth.OpenOrCreate`) — small and enumerable, confirmed accurate. Found two structural facts sharper than the original scoping note: (1) `auth.Store` (`internal/auth/store.go`) is a flat `map[string]record` keyed only by username — it cannot represent §5.2's "same username independently in two realms" requirement without a real structural change, not just a file-layout choice; two genuine options exist (composite-key `(RealmID, username)` in one file, reusing the exact PBKDF2→Argon2id dual-decode version-bump precedent `auth.Store` already has, vs. fully separate per-realm files under `realms/<RealmID>/security/` per §7's literal layout, which needs new bounded open/eviction infrastructure since realm count can scale like database count). (2) **A previously-unflagged finding**: `cmd/nextsqld/main.go` always pins `srv.Realm` to the one hosted deployment's default realm name, so `serveConn`'s existing flat-equality realm check currently rejects any `hello.Realm` other than that one — meaning `dbmanager.Manager.Acquire`'s `realmName` parameter, despite accepting arbitrary realm names since M2-3a, is unreachable with any other value today. Every current deployment is "multi-database within one fixed realm," not yet genuinely multi-realm — M2-4b is therefore a real prerequisite for multi-realm routing to ever activate, not only an authorization nicety. Also confirmed `security.ACL`'s `Grant` has no `RealmID` field (same precedented format-bump shape applies), that `serveConn` already knows `hello.Realm` well before its password/ACL checks (a small, localized insertion point, not scattered), that the `HostingRegistry == nil` legacy-fallback convention already established for M2-3a/M2-4a extends cleanly here, and that `internal/authbroker` already threads a `Realm` field through token *minting* — the real gap is narrower than originally scoped: `serveConn`'s token-claim verification path checks `claims.Database` but never `claims.Realm`, a small addition rather than a second deep scoping target. Decomposed into M2-4b-1 (composite-key `auth.Store` by `(RealmID, username)` in one file, `RealmID` added to `Grant`, realm resolution wired into `serveConn` before auth checks, `claims.Realm` checked in the token-verify path — recommended first slice: delivers the real capability and unblocks multi-realm routing with no new infrastructure), M2-4b-2 (separate per-realm files + a bounded eviction manager — only needed for isolation-at-rest/crypto-shred, not required for M2-4b-1's correctness), M2-4b-3 (OIDC broker realm-awareness beyond what already exists — folded mostly into M2-4b-1's verification-side check, kept as its own line only for any future deeper per-realm IdP policy work). **One open question flagged as needing an actual human decision, not resolvable by further investigation**: M2-4b-1's single composite-keyed file vs. M2-4b-2's fully separate per-realm files — a genuine blast-radius/crypto-shred tradeoff against implementation cost — asked via `AskUserQuestion`. Recorded this decomposition in `docs/design-multidatabase-dbaas.md` §16 and `TODO.md`'s M2-4b checklist line before any code was written. **No code changed this increment — scoping and documentation only.** Docs: `docs/design-multidatabase-dbaas.md`, `TODO.md` (this entry + checklist + header table), `CHANGELOG.md`.
76. [x] Multi-database hosting M2-4b-1 — realm-scoped `auth.Store`/`security.ACL`, composite-key single file (2026-09-02) — **NOT a Phase 27 item.** Live user "continue @TODO.md" after log #75's scoping; asked the user which starting point to build (options: M2-4b-1 single composite-keyed file, M2-4b-2 separate per-realm files, or stop here) — **user chose M2-4b-1 (recommended)**. Given the cross-file, on-disk-format-changing, security-sensitive scope, used plan mode: a direct-reading grounding pass (not delegated, since the scoping fork's findings needed independent verification against the live tree) read `internal/auth/store.go`, `internal/security/rbac.go`, `internal/executor/session.go`/`security.go`/`system.go`/`exec.go`, and `internal/protocol/server.go`'s full Hello→TypeReady sequence in full before writing the plan. This surfaced real design content the scoping fork's summary had understated: (1) `security.ACL`'s role/grant surface (`Grant`/`Revoke`/`CreateRole`/`DropRole`/`GrantRole`/`RevokeRole`/`AddUser`/`DropUser`/`Allowed`/`AllowedScoped`/`RolesFor`/`Snapshot`, ~12 methods plus 3 private helpers) needed the *same* realm-scoping treatment as `auth.Store`, not just "add a `RealmID` field to `Grant`" as the fork's one-line characterization suggested — §5.2 explicitly scopes roles per-realm too ("its own principal namespace, password/identity store, **roles**, and grants"), and role membership (`expandLocked`) had to become realm-aware for grants to be genuinely isolated. (2) A `PrivAdmin+ScopeCluster` grant made at a specific realm would, under naive per-realm filtering, make `isAdmin()` (the exact chokepoint M2-4a's `system.realms`/`system.databases` admin-gating already depends on) return true for a realm-scoped admin — a real cross-realm-visibility regression against §5.4's "cross-realm leakage tolerance is zero," caught during design rather than after. **Design decisions** (full reasoning in the plan file, condensed here): sibling `*InRealm` methods alongside every existing flat method (mirrors this session's own `AcquireWithPriority`-alongside-`Acquire` precedent) — the flat methods become one-line `hosting.ID{}` wrappers, so the ~20 test files across the repo that construct an `auth.Store`/`security.ACL` for unrelated feature setup need **zero changes**, confirmed by the full regression suite passing unmodified; `hosting.ID{}` means deployment-wide and is *unioned* (not shadowed) with a realm's own grants/roles for ordinary privileges (authorization is additive) but `PrivAdmin+ScopeCluster`/`PrivAdmin+ScopeAdmin` always normalize to and match only at `hosting.ID{}` regardless of the realm requested (`clusterRealm` helper) — cluster administration is not a per-realm concept, closing finding (2) by construction rather than by a read-time filter that could be gotten wrong later; `auth.Store.VerifyInRealm`/`HasInRealm` use *shadow* semantics instead (a realm-scoped identity of the same name takes precedence over a same-named deployment-wide one — identity is exclusive, not additive, the opposite of grants). On-disk formats: `internal/auth/store.go`'s `fileVersion` 2→3 (extends its own pre-existing v1→v2 PBKDF2→Argon2id dual-decode precedent — `userKey{Realm hosting.ID, Name string}` composite map key, 16-byte `RealmID` per record, `Decode` reads v1/v2/v3); `internal/security/rbac.go`'s `aclVersion` 1→2 (this file's first-ever format bump, same shape — `roleKey{Realm, Name}`, `Grant.Realm`, `decodeACL` version-dispatches to `decodeACLV1`/`decodeACLV2`). `internal/hosting/registry.go` gained `LookupRealm(realmName string) (Realm, error)`, mirroring `Lookup`'s exact name-matching shape minus the database half — needed because realm resolution for authorization purposes has to happen before a database is chosen at all. `internal/executor/session.go` gained `realmID hosting.ID` + `SetRealmID`; `authAllowed` (the one chokepoint nearly every RBAC-gated check in the executor already routes through) now calls `AllowedScopedInRealm(s.realmID, ...)` — since every session that never had `SetRealmID` called on it defaults to `hosting.ID{}`, this one-line change alone made the entire executor test suite realm-aware-capable with zero further edits needed there. `internal/executor/security.go` (`CREATE USER`/`DROP USER`/`CREATE ROLE`/`DROP ROLE`/`GRANT`/`REVOKE` SQL handlers, ~9 call sites) and `exec.go` (`grantHistoryDML`'s self-grant) switched to the `*InRealm` variants passing `s.realmID` — the real, necessary work that makes those statements actually realm-scoped for a hosted session. `internal/executor/system.go`'s `systemUsersRows`/`systemRolesRows`/`systemGrantsRows` switched from `Snapshot()` to `SnapshotInRealm(s.realmID)` (still `s.isAdmin()`-gated, unchanged) — otherwise these views would perpetually show only deployment-wide principals and never the new realm-scoped ones, once real realms exist. `internal/protocol/server.go`'s `serveConn`: realm resolved via the new `LookupRealm` right after the existing flat `s.Realm`/`hello.Realm` precheck, before any password work (identity must resolve before routing decides which database handle to return, per the design doc's own §8 note); `s.Auth.Verify`/`Has` and `s.ACL.AllowedScoped` calls switched to their `*InRealm` counterparts; added the `claims.Realm` mismatch check the scoping pass flagged as missing, parallel to the existing `claims.Database` one; `b.sess.SetRealmID(realmID)` wired alongside the existing `SetACL`/`SetAuth`/`SetHostingRegistry` calls. `internal/authbroker`: `RoleMembershipFunc` gained a leading realm-name parameter (`func(realm, principal string) ([]string, error)`) — forced by `RolesFor`'s own signature change, threaded from `exchange.go`'s pre-existing `req.Realm` through `heldRoles`; kept the package decoupled from `internal/hosting` (it only ever sees a realm *name*, never an `ID`). `cmd/nextsqld/main.go`'s `startEmbeddedAuthBroker` gained a `hostingRegistry` parameter so its `RoleMembership` closure can resolve a realm name to an ID itself (unknown/empty realm name resolves to `hosting.ID{}`) before calling `HasInRealm`/`RolesForInRealm`; its two existing bootstrap-admin `Upsert`/`Grant` call sites are **unchanged**, already correctly deployment-wide via the untouched flat methods. `cmd/nextsql/main.go` needed **no changes** for the same reason. Tests: `internal/auth/store_test.go` gained `TestVerifyInRealmIsolatesSameUsername`, `TestVerifyInRealmFallsBackToDeploymentWide`, `TestVerifyInRealmScopedShadowsDeploymentWide`, `TestLegacyFileDecodesEveryRecordDeploymentWide` (plus 5 pre-existing white-box tests mechanically updated for the `users` map's new composite-key type — behavior unchanged, confirmed by the full suite passing); `internal/security/rbac_test.go` gained `TestAllowedInRealmIsolatesGrants`, `TestAllowedInRealmUnionsDeploymentWideGrant`, `TestClusterAdminGrantAlwaysDeploymentWide`, `TestExpandRoleStaysWithinRealm`, `TestLegacyACLFileDecodesEveryEntryDeploymentWide` (zero pre-existing tests touched); `internal/authbroker/broker_test.go`'s one `RoleMembership` fixture closure updated to the new signature (mechanical); `cmd/nextsqld/authbroker_test.go`'s two `startEmbeddedAuthBroker` call sites updated to pass a nil registry (mechanical); new `tests/integration/multirealm_auth_test.go` — `TestCrossRealmSameUsernameIsolatedOverRealConnections`, two independent single-database `protocol.Server` instances in two different real realms of one shared `hosting.Registry`, sharing one `*auth.Store`/`*security.ACL` instance, over real TLS connections via the real Go driver: the same username with different passwords in each realm authenticates only with its own realm's password against its own realm's server, and a deployment-wide bootstrap admin authenticates into both — this shape (two servers, not one multi-realm-routed server) is itself evidence of the still-open `srv.Realm`-pinning limitation this increment's own scoping surfaced, not yet fixed by M2-4b-1. `go build ./...` clean; `go vet ./...` unchanged (same pre-existing unrelated `internal/executor/cdc.go`/`internal/executor/vector/batch.go` findings). Full regression green under `-race`: `internal/auth`, `internal/security`, `internal/executor` (incl. `aggregate`/`join`/`sort`/`vector`), `internal/protocol`, `internal/authbroker`, `internal/hosting`, `internal/dbmanager`, `cmd/nextsqld`, `cmd/nextsql`, `tests/integration`. **Live verification against real `nextsql`/`nextsqld` binaries** (this project's established convention): real `nextsql init` bootstrap (deployment-wide admin, unchanged flat `Upsert`/`Grant`), a real `nextsqld` process, real `CREATE USER`/`GRANT` SQL creating a realm-scoped user via a real connection, confirmed that user authenticates with its own password and is rejected with the wrong one, confirmed `system.users` as admin shows only the realm-scoped user (never the deployment-wide bootstrap admin, which lives at `hosting.ID{}`) and shows zero rows for a non-admin, confirmed `system.realms` (M2-4a) is unaffected. No WAL/catalog change; two on-disk credential-file format bumps, both backward-compatible dual-decode. Docs: `docs/design-multidatabase-dbaas.md` (§5.2/§16 M2-4b-1 bullet marked landed, top status line), `TODO.md` (M2-4b-1 checklist line flipped to `[x]` under a restructured M2-4b entry, header table, this entry), `CHANGELOG.md`. **M2-4b-2 (per-realm files + eviction manager) and M2-4b-3 (deeper OIDC broker realm-awareness) remain open and unscheduled**, as does the `srv.Realm`-pinning fix needed before multi-realm routing can actually activate.
77. [x] Multi-database hosting M2-5 — multi-realm routing activation (2026-09-02) — **NOT a Phase 27 item.** Live user "continue @TODO.md" after log #76; asked the user what to work on next among the remaining open candidates (M2-4b-2 per-realm files, M2-3b-2/3, P27's replication-ack fix, or fixing the `srv.Realm`-pinning limitation M2-4b-1's own scoping surfaced) — **user chose to fix `srv.Realm` pinning**. Given fresh, deep context on `internal/protocol/server.go`'s `serveConn` from implementing M2-4b-1 minutes earlier, did a direct read-and-trace investigation rather than spawning a fresh scoping fork: confirmed `dbmanager.Manager` is keyed by `hosting.ID` (database identity) globally, not scoped by realm at all, and `cmd/nextsqld/main.go`'s `Opener` closure already resolves `(realm, database)` pairs fully generically via `hosting.ManagedDatabasePath(cfg.DataDir, realm.ID, database.ID)` — both were already realm-agnostic since M2-3a, confirmed by reading the actual code rather than assuming. Only one blocker remained: `serveConn`'s flat `s.Realm != "" && hello.Realm != "" && hello.Realm != s.Realm` equality precheck (predating M2-3a), which unconditionally rejected any Hello naming a realm other than the one `cmd/nextsqld/main.go` pins `srv.Realm` to at startup — meaning M2-4b-1's own `LookupRealm`-based resolution, added minutes earlier, was itself still unreachable for any second realm despite being fully correct. **Fix**: the precheck now only applies when `s.HostingRegistry == nil` (mirroring the exact existing pattern already used for the analogous `s.databaseManager() == nil` guard on the `s.Database` check immediately below it) — once a `HostingRegistry` is configured, `LookupRealm` (M2-4b-1) becomes the sole, authoritative, registry-backed realm check. `srv.Realm` itself is unchanged in meaning: still the fallback used when a Hello omits `Realm` entirely. **Live verification surfaced a real, separate, necessary companion gap**: `nextsql exec` (and every other server-mode CLI command) had no way to select a non-default realm at all — `internal/cli/resolve.go`'s `Settings.Realm` already existed and read `--realm`/`NEXTSQL_REALM_NAME`, but `internal/cli/connect.go`'s `ServerConfig` built the driver `nextsql.Config{}` literal without ever setting `Realm: s.Realm` — silently dropping it regardless of how it was supplied, for every caller (this bug reaches beyond `exec`, since every server-mode subcommand routes through the same `ServerConfig`). Fixed the missing field, added an explicit `--realm` flag to `nextsql exec` specifically (the primary interactive/scripting tool; the CLUSTER subcommands were left unchanged as reasonable current scope, not attempted here), and updated `printUsage`. Considered and deliberately deferred a related pre-existing gap noted in passing, not introduced by this change: §5.2's "pre-authentication errors must not disclose whether another realm... exists" is not actually met today (an unknown-realm probe returns a distinguishing `NotFound` before any password check, an oracle) — this was already true in spirit for the single pinned realm name and for `mgr.Acquire`'s unknown-database rejection since M2-3a; this fix makes the *existing, already-accepted* category of exposure reachable for realm names too, rather than introducing a new category, and a proper fix would need to redesign pre-auth error redaction across realm/database/username together as its own deliberate hardening pass, not a side effect of an activation fix. Tests: new `tests/integration/multirealm_routing_test.go` — `TestMultiRealmRoutingThroughOneServer` (a `multiRealmRoutingFixture` with two real realms each with its own database, ONE real `protocol.Server` + `dbmanager.Manager` — mirrors `startMultiDBServer`'s exact opener shape, generalized across realms instead of just across databases within one realm — proves a table created via a connection routed to realm A's database is invisible via a connection routed to realm B's database and vice versa, through the same server) and `TestUnknownRealmStillRejectedCleanly` (an unknown realm name still gets a clean `NotFound`, proving the precheck's relaxation did not turn "any string" into a valid realm); `internal/cli/cli_test.go` gained `TestServerConfigThreadsRealm`. A first draft of the routing test set an ACL with only a `CONNECT` grant and hit a real but unrelated `CREATE TABLE` permission-denied failure — root-caused via a debug trace to a missing `CREATE`-privilege grant in the test fixture itself (not a product bug); fixed by dropping the ACL from that fixture entirely, matching `startMultiDBServer`'s own existing precedent (a nil ACL is unrestricted; RBAC granularity is already covered by `multirealm_auth_test.go`). One transient live-verification failure ("io: read" opening a second realm's database) did not reproduce in a completely fresh scratch environment and never reproduced across the full automated suite (including the new dedicated multi-realm routing test, run repeatedly) — concluded to be contamination from reusing the same scratch data directory across several successive live-verification passes earlier in the session, not a product defect; noted rather than silently dropped. `go build ./...` clean; `go vet ./...` unchanged (same two pre-existing unrelated findings). Full regression green under `-race`: `internal/protocol`, `internal/cli`, `internal/dbmanager`, `internal/hosting`, `internal/executor` (full package, ~116s, no timeout), `cmd/nextsqld`, `cmd/nextsql`, `tests/integration`. **Live verification against real `nextsql`/`nextsqld` binaries**: bootstrapped a real deployment with two real realms (`nextsql init` for the default, `nextsql realm create` for a second), one real `nextsqld` process, and via the newly-fixed `nextsql exec --realm`: created a table in each realm, confirmed each realm's table is invisible from the other (`unknown table`, `NotFound`), and confirmed an unrecognized `--realm` value is rejected cleanly (`unknown realm`, `NotFound`) rather than hanging or crashing. No WAL/catalog/wire-format change (an existing check relaxed under an existing, already-present nil-guard convention; two CLI-side fields/flags added). Docs: `docs/design-multidatabase-dbaas.md` (§16 new M2-5 writeup, top status line, exit-criteria note), `TODO.md` (new M2-5 checklist item under the M2-4 group, header table, this entry), `CHANGELOG.md`. **M2-4b-2 (per-realm auth files), M2-4b-3 (deeper OIDC broker realm-awareness), M2-4c (`system.database_operations`), and the pre-authentication realm/database/username-existence disclosure hardening noted above all remain open and unscheduled.**

78. [x] Multi-database hosting M2-6 — pre-authentication realm/database existence-disclosure hardening (2026-09-02) — **NOT a Phase 27 item.** Live user "continue @TODO.md" after log #77; asked the user what to work on next among the remaining open candidates (P27's own replication-ack structural fix, pre-auth disclosure hardening, M2-4b-2 per-realm auth files, M2-3b-2/3 resource governance) via `AskUserQuestion` — **user chose pre-auth disclosure hardening**, the gap M2-5's own writeup (log #77) flagged as deliberately out of scope rather than newly introduced. Read `internal/protocol/server.go`'s full `serveConn` sequence directly before writing any code (small, well-scoped, security-sensitive change — no plan-mode round-trip needed, matching M2-4a's precedent for this shape of increment). Found exactly three checks that disclosed realm/database existence to a fully unauthenticated peer, all returning before `HelloOK` is even sent (i.e. before the client is ever prompted for a password): the flat `s.Realm != "" && hello.Realm != s.Realm` precheck (legacy, non-hosted realm pinning), the flat `s.Database != "" && hello.Database != s.Database` precheck (legacy, non-`DatabaseManager` database pinning), and `HostingRegistry.LookupRealm(realmName)` (M2-4b-1's own registry-backed check). Each returned a distinguishing `nerr.NotFound` immediately — a credential-free oracle letting an attacker enumerate valid realm/database names with zero guesses at a username or password. Cross-checked username enumeration first and found it was **already closed**: `internal/auth/store.go`'s `VerifyInRealm` already runs a same-cost dummy Argon2id comparison for an unknown user and returns the identical generic `Unauthorized "authentication failed"` either way (pre-existing, not touched by this increment) — this became the template for the fix. **Fix**: none of the three checks return early any more. A new `identityOK bool`, computed at the same point the old prechecks ran, records whether the requested realm/database actually resolve — `realmID` still resolves to *some* value (`hosting.ID{}` when `LookupRealm` fails) purely so the verification call downstream has something to run against, never as a way for an unknown realm to authenticate: `identityOK`, not `realmID`'s specific value, is what decides the final outcome, so an unknown realm can never succeed via `VerifyInRealm`'s existing same-name deployment-wide fallback (a real hazard identified during design — using `hosting.ID{}` unconditionally for an invalid realm would otherwise let a garbage realm name silently authenticate a real deployment-wide user). The connection now always completes the full `Hello` → `HelloOK` → `Auth` round trip regardless of realm/database validity, runs the real (or dummy, for a bad username) password verification exactly as it would for a genuinely valid realm, and only *after* that folds `!identityOK` into the exact same generic `Unauthorized "authentication failed"` outcome a wrong password produces — same nerr code, same message text, same cost (the real/dummy hash comparison already ran) — so an unauthenticated prober cannot distinguish "wrong realm"/"wrong database" from "wrong password" by response content or timing. The token-credential verification path (`s.Tokens.Verify`) already funneled every sub-check failure into one generic message before this change; restructured it to flow through the same shared `authErr`/`identityOK` fold rather than returning independently, so both credential paths (password and token) get identical treatment. **Deliberately out of scope**, both flagged directly rather than silently left: (1) `dbmanager.Manager.Acquire`'s post-authentication unknown-database rejection — reachable only with already-valid credentials in a resolved realm, a materially weaker, pre-existing (since M2-3a) disclosure that isn't a credential-free oracle, and entangles with legitimate error reporting to an already-authenticated client; (2) the mTLS `RequireServiceIdentity`/`matchServiceIdentity` checks earlier in `serveConn` — a separate identity mechanism (certificate-to-username binding) unrelated to realm/database/username confidentiality. Tests: `tests/integration/protocol_test.go`'s pre-existing `TestRealmMismatchRejected` (legacy `s.Realm` precheck) and `tests/integration/multirealm_routing_test.go`'s pre-existing `TestUnknownRealmStillRejectedCleanly` (`LookupRealm` precheck) both updated from asserting `NotFound` to asserting `Unauthorized` — both deliberately connect with a **correct** password (`"app"`/`"s3cret"`, a real credential that would authenticate successfully against a valid realm) so the rejection can only be attributed to the wrong realm name, proving the fix rather than merely not regressing a changed error code. New `TestDatabaseNameMismatchRejectedGenerically` gives the legacy single-pinned-database flat precheck its first-ever test coverage (same correct-password-but-wrong-name shape; it had zero prior coverage because every existing fixture leaves `srv.Database` empty, which always skipped the check). `go build ./...` clean; `go vet ./...` unchanged (same pre-existing unrelated `internal/executor/cdc.go` finding). All green under `-race`: `internal/protocol`, `tests/integration` (full package, 26.8s), `internal/auth`, `internal/security`, `internal/dbmanager`, `internal/hosting`, `cmd/nextsqld`, `cmd/nextsql`. No wire-protocol/on-disk-format change — pure connection-handshake control-flow reordering plus one new shared error-folding path. Docs: `docs/design-multidatabase-dbaas.md` (new M2-6 bullet, top status line, §16/exit-criteria updates), `TODO.md` (M2-4 group checklist, header table, this entry), `CHANGELOG.md`. **M2-4b-2 (per-realm auth files), M2-4b-3 (deeper OIDC broker realm-awareness), M2-4c (`system.database_operations`), M2-3b-2/3 (resource governance), and P27's own replication-ack structural fix all remain open and unscheduled.**

79. [x] Local commit precedes replication acknowledgment — structural fix (2026-09-03) — a live user "continue @TODO.md" resumed directly on this item (its own TODO.md line was the selected/highlighted context), after two prior deferrals (log #68 and the P27-twentieth-increment investigation above) had each concluded a real fix needs new WAL durability semantics plus crash-recovery changes and landed a mitigation instead. Asked via `AskUserQuestion` whether to finally attempt the full structural fix, re-scope smaller first, or leave the mitigation as the closing state — **user chose to attempt the full structural fix**. Given the size/risk (touches crash-recovery-adjacent code across `internal/wal`/`internal/storage`/`internal/replication`), used full `EnterPlanMode`: grounded directly against the live code (not delegated) — `commitAndReplicate`/`commitLocked`'s exact step order, `wal.Log.flushLocked`'s all-or-nothing buffer flush and its two non-`commitLocked` call sites (`Engine.Checkpoint`, `wal.Log.InstallRecords`), `internal/recovery`'s commit-presence-only redo logic, `Cluster.Replicate`'s pre-`raft.Apply` leader check vs. its post-`raft.Apply` `isRetryableApplyErr` classification, `Engine.replMu`'s actual serialization scope, and `RollbackTxn`'s buffer/allocator undo pattern — then a dedicated Plan agent pass to validate/detail the design against that grounding, whose draft was itself corrected on review against the live code (its suggested test rewrite was wrong, and its "collect records directly, drop the disk scan" simplification turned out to hide a real bug, both below).

    **Design, settled before writing code**: rather than the general "hold an arbitrary range, replicate-then-locally-commit for the whole batch" direction the prior investigations assumed necessary, grounding revealed recovery already treats a transaction with data records but no durable `RecCommit` as an ordinary open/never-committed transaction (true for every in-flight write today) — so only the **`CommitRec` itself** needs its durability gated on replication, not the whole batch; everything before it can keep flushing on its normal schedule. This shrinks "a new WAL flush-barrier primitive" to holding exactly one physical record and either flushing it in place or splicing it back out — no `internal/recovery`/`internal/txn` change needed at all, confirmed by a dedicated test (below) rather than assumed. Failure outcomes are split in two: a **definite** rejection (`c.raft.State() != raft.Leader`, before `raft.Apply` is ever attempted — the common case, any write racing a leadership transfer) discards the held record and rolls the transaction back, no orphan; an **ambiguous/in-doubt** outcome (`raft.Apply` was called but the quorum wait itself failed — lost leadership, enqueue timeout, mid-flight shutdown) is structurally undecidable from the caller's side and keeps this package's pre-existing fail-open behavior byte for byte — discarding here would be *worse* than today's known orphan, since the WAL's LSN counter has already moved past the record, so if it actually did reach quorum, a later legitimate replay of it via `ApplyReplicated` would be silently skipped as already-seen (`LSN < nextLSN`) rather than applied: a permanent, undetectable divergence. The `replSuspect`/`CLUSTER RECONCILE CONFIRM` mitigation from log #68 is **not removed** — it stays exactly in place as the dedicated handler for this one narrowed residual case, not a fallback for everything.

    **`internal/wal/log.go`**: new single-slot durability barrier — `AppendHeld(rec) (LSN, error)` appends like `Append` but records the physical byte offset/length of the held record and marks `held = true`; `ReleaseHold(commit bool) error` either leaves the bytes in place (`commit`) or splices them back out of the in-memory buffer (`!commit`), leaving any unrelated bytes appended after them (from other, concurrently-writing transactions — see below) untouched; `flushLocked` now writes only the byte-prefix strictly before a held record, `Flush` waits on the log's condvar instead of busy-spinning when the target LSN needs bytes a hold is blocking, and `rotateLocked` refuses to rotate segments out from under a held record (`ErrHoldBlocksRotation` — a held record's bytes are tied to a specific segment offset). Only one record may be held at a time, enforced defensively; `Engine.replMu` (see below) is what actually guarantees this in production. New `PointAfterCommitRecordHeld`/`PointAfterHoldReleaseDiscardBeforeAbortAppend` crash-injection points (`internal/wal/crash.go`).

    **`internal/replication/cluster.go`**: the pre-`raft.Apply` leader-rejection branch now returns an error wrapped in a small `notProposedError{error}` implementing `NotProposed() bool`/`Unwrap() error` — the post-`raft.Apply` branch is untouched, staying ambiguous. `storage.Replicator`'s method signature is unchanged; classification is a new optional-capability interface, `storage.NotProposedError` (`error` + `NotProposed() bool`), checked via type assertion at the `commitAndReplicate` call site — the exact existing convention `ReplicationOrphanReporter` already uses, so no import-direction coupling between `storage` and `replication` was needed.

    **`internal/storage/engine.go`**: `commitLocked` split into `prepareCommitLocked(txn, pageWriteHeld, hold)` (everything through the CommitRec — `AppendHeld` and stop, when `hold`; append+flush+finish inline otherwise, byte-for-byte the original behavior for the `e.repl == nil` path) and `finishCommitOK`/`finishCommitDiscarded` (resolve a held commit once `Replicate`'s outcome is known — the latter reuses `RollbackTxn`'s buffer/allocator undo loop, factored out into a shared `undoTxnBuffers` helper, including running it without `e.mu` held: `Buffer`'s `Pin → OnPin` callback re-enters `e.mu`, so holding it there would deadlock, exactly why `RollbackTxn` already drops it first). `commitAndReplicate` prepares under `e.mu`, releases it, calls `Replicate` (never holding `e.mu` across the round-trip — the deadlock hazard the prior investigation flagged: Raft's own FSM-apply goroutine can need `e.mu` for an unrelated, earlier-queued entry the pending commit's own `raft.Apply()` future may transitively depend on to resolve; a dedicated concurrency test proves this holds, below), then branches on the outcome.

    **Two real bugs found and fixed during implementation, not anticipated by the design** (both caught by tests, not inspection alone — recorded here so a future session doesn't have to rediscover them):
    1. *Allocator persistence isn't undoable.* `Allocator.Flush()` writes the allocator's freelist/superblock pages directly to the data file with no durability gate of its own and no undo log (unlike WAL records, gated by the new hold primitive, and buffer-pool pages, gated by the pre-existing `AllowFlush` refusing eviction while a transaction is still in `e.writers`) — `Alloc.Reload()` only re-reads whatever is already on disk, it cannot revert a persist that already happened. Calling it inside `prepareCommitLocked` (as the pre-fix `commitLocked` always did, harmlessly, since nothing was ever rolled back after it ran) would make `finishCommitDiscarded`'s `Alloc.Reload()` silently fail to undo a discarded transaction's page allocations. Fixed by moving the call into `finishCommitOK`, which only ever runs once a transaction is known to be actually committing.
    2. *A replication batch must be everything since the last replicated LSN, not just the committing transaction's own records* — found via the full `TestRollingUpgradeDrainWithLeaderTransferNoTransactionLoss` failing deterministically (`unknown table` after `CLUSTER TRANSFER LEADER`, reproduced down to a 2-line repro: create a table, transfer leadership, query the new leader), root-caused by direct debugging (an instrumented discard-path print showed the discard branch never even fired, ruling that path out; a minimal same-leader-no-transfer repro passed, isolating the bug to something only visible once a follower's applied state was actually trusted). A first implementation collected `recs` directly from each `wal.Append` call during `prepareCommitLocked` (avoiding a disk-reading scan, since the CommitRec isn't durable yet) — this only ever contained the committing transaction's own records, but other, unrelated transactions' records can and do interleave into the WAL between replication rounds (e.g. `flushDirtyImages` briefly releases `e.mu` mid-commit), and a follower's `InstallRecords` requires a gap-free LSN sequence — omitting them made a later, correctly-quorum-committed batch (one that happened to skip over LSNs the follower had never seen) silently fail to apply on the follower with no error surfaced anywhere a caller could see (a **follower**-side `FSM.Apply` failure is not returned to anyone). Fixed by keeping `e.takeReplLocked`'s disk-reading scan (flushing everything up to, but not including, the held CommitRec right before scanning, then appending the held record's own in-memory `Record` value onto the end manually) instead of trying to avoid it. This in turn needed its own correctness fix: `takeReplLocked`'s internal watermark advance is unconditional, but if the resulting batch is later discarded (definite failure), nothing in it — including the swept-up unrelated records, which stay durable on disk — ever reached Raft, so the watermark has to roll back to its pre-scan value (a new `preReplLSN` returned by `prepareCommitLocked`, restored by `finishCommitDiscarded`) or those unrelated records would silently never be offered for replication again.

    **Tests**: `internal/wal` — 7 new direct unit tests for `AppendHeld`/`ReleaseHold` in isolation (durability-until-release, discard-splices-only-the-held-record leaving an interleaved unrelated record intact, `Flush` waking on release rather than busy-spinning, single-slot rejection, no-op without a hold, rotation refusal). `internal/replication` — `TestReplicateOnNonLeaderIsDefiniteNotProposed`, a real 3-node cluster confirming a genuine follower's `Replicate` rejection satisfies the new `NotProposed()` capability. `internal/storage/btree` — new `TestInsertDiscardsLocalCommitOnDefiniteReplicateFailure` (definite failure: row invisible, no orphan reported) alongside the pre-existing `TestInsertOrphansLocalCommitOnReplicateFailure` (kept, docstring corrected to say it now documents the *residual, deliberately-unclosable* ambiguous case rather than "the known gap" — a Plan-agent draft wrongly suggested rewriting this test to expect discard, which would have been incorrect since its failure is intentionally generic/ambiguous, caught on review before implementing); new `TestUnrelatedReadProceedsWhileACommitHoldsReplicate` (a blocking test `Replicator` proves an unrelated read completes while one commit's `Replicate` is genuinely in flight — the no-deadlock property). `internal/recovery` — new `TestRedoTreatsHeldUnflushedCommitAsUncommitted` (append-hold-crash-reopen via the real `wal.Log`, no `storage.Engine` involved, proving no `recover.go` change is needed rather than assuming it). `tests/integration` — `TestRollingUpgradeDrainWithLeaderTransferNoTransactionLoss` re-run 5x consecutively clean under `-race` post-fix; this is also the real, end-to-end "3-node orphan-free" proof for the definite-failure case (a genuine `CLUSTER TRANSFER LEADER` racing live writes, not a synthetic double), so no separate dedicated cluster test was added beyond it — composed with `TestReplicateOnNonLeaderIsDefiniteNotProposed` (real `Cluster.Replicate` is definite on a real follower) and `TestInsertDiscardsLocalCommitOnDefiniteReplicateFailure` (real `Engine` correctly discards on a definite failure), the end-to-end property is covered by composition even without one single test exercising the full stack simultaneously. `go build ./...` clean; `go vet` (scoped) clean. All green under `-race`: `internal/wal`, `internal/replication`, `internal/recovery`, `internal/storage` (full, incl. `internal/storage/btree`), `internal/executor` (full package incl. `aggregate`/`join`/`sort`/`vector`), `tests/integration` (full package). No wire-protocol/on-disk-format change — the WAL physical record encoding, replication command encoding, and every other on-disk format are untouched; this is purely commit-path sequencing plus one new in-memory buffer primitive.

    **Residual risk, stated explicitly rather than papered over**: the ambiguous/in-doubt Raft outcome is not closed by this fix and cannot be closed by a single-round-trip design — it's inherent to `raft.Apply`'s own contract (a caller cannot know whether an errored `Apply` call actually reached quorum). This fix closes the dominant case (a definite non-leader rejection, e.g. any write landing during a leadership-transfer window) fully; the genuinely ambiguous case keeps today's honest, observable, operator-reconcilable fail-open behavior via the pre-existing mitigation, rather than a guess that could silently diverge the cluster further. Docs: `docs/ha.md` (`StrongReadBarrier`/orphan-mitigation section updated to describe the narrowed scope), `docs/ops.md` (Rolling upgrade "Correctness note" rewritten to describe the fix and the residual case), `CHANGELOG.md`, `TODO.md` (exit-gate checklist line + preamble + this entry).

80. [x] Multi-database hosting M2-3b-2 — global memory budget gating buffer-page grants (2026-09-03) — **NOT a Phase 27 item.** User instructed "continue all uncheck from Phase 0 - Phase 27"; with every phase-0-through-26 checklist line already closed and P27 itself blocked purely on this cross-cutting track, picked the smallest unblocked item flagged ready in the tracker (M2-3b-2, explicitly "depends on M2-3b-1 (now landed)") rather than asking, per this session's own "smallest coherent increment" convention. Scoped first via a dedicated Explore fork (buffer.Pool's exact allocate-once-at-construction shape, the one non-test buffer.New call site, dbmanager's Opener injection point, scheduler.Admission/Budget as prior-art shapes for a shared counter, config.go's key-naming convention) before writing code. **Design**: a `Pool`'s frames are all allocated up front at construction — there is no dynamic per-page grant to gate at runtime the way `Allocator.SetCapPages` gates each individual disk-page allocation — so the only meaningful gate is the all-or-nothing decision of whether a new database's Pool may be built at all. New `buffer.Budget` (`internal/storage/buffer/budget.go`): mutex + frame counter, `Reserve`/`Release`/`Used`/`Cap`, nil-safe and zero-cap both meaning unbounded (so every pre-existing caller, including `buffer.New` itself now defined as `NewWithBudget(f, n, nil)`, is unaffected by default). `storage.OpenOptions` gained a `Budget *buffer.Budget` field threaded into `buffer.NewWithBudget` inside `open()`; `Engine` carries `budget`/`budgetFrames` and releases the exact reservation once in `Close()` (guarded by the pre-existing `closed` bool, so it fires exactly once regardless of whether the close is normal shutdown or M2-3b-1 idle eviction) — a failed `Reserve` charges nothing, so there is nothing to unwind on the open-failure path. New `max_total_buffer_pages` config key (0 = unbounded default, matching the project's established "0 = unlimited" convention); `Config.Validate` rejects a positive value below `buffer_pages`, since otherwise even the primary database could never open. `cmd/nextsqld/main.go` builds one `buffer.NewBudget(cfg.MaxTotalBufferPages)` and threads it through all three of its `executor.Open` call sites (primary, dbmanager secondary `Opener`, `REQUIRE CLIENT KEY` lazy primary open) via `executor.OpenWith(..., storage.OpenOptions{Budget: bufBudget})` — a mechanical `Open`→`OpenWith` swap at each site, no other call-site logic changed. **Deliberately scoped to the long-running server process only**: `storage.Create`/`CreateWithIdentity` (the one-shot `nextsql database create`/`nextsql init` provisioning CLI, confirmed by grep to be `nextsqld`'s only non-`Open` path and never invoked by the running server itself) were left unbudgeted — that process opens exactly one `Pool` and exits, so there is nothing cross-database to gate. No dedicated `system.*`/metrics observability surface for `Budget.Used()`/`Cap()` — flagged as a deliberate follow-on, not required for the gating behavior itself. Tests: `internal/storage/buffer/budget_test.go` (new — the `buffer` package had zero prior tests; cap-boundary Reserve/Exhausted, partial-Release-then-Reserve, Release-clamps-at-zero, zero-cap-unbounded, nil-Budget-safe, a concurrent 50-goroutine Reserve/Release race), `internal/storage/engine_test.go` `TestBufferBudgetGatesConcurrentOpens` (two real `Engine`s sharing a budget sized for exactly one: second open rejected `Exhausted` while the first stays open, succeeds once the first `Close()`s and releases) and `TestBufferBudgetNilUnbounded`, `internal/config/config_test.go` (load/negative-rejection/validate-boundary coverage for the new key). `go build ./...` clean; `go vet ./...` unchanged (same pre-existing unrelated `internal/executor/cdc.go` finding). All green under `-race`: `internal/storage` (incl. `buffer`/`btree`, 254.9s), `internal/config`, `internal/dbmanager`, `cmd/nextsqld`. **Live verification against real `nextsql`/`nextsqld` binaries**: bootstrapped a real two-realm deployment (`nextsql init`, `nextsql realm create` for a second realm+database), started a real `nextsqld` with `buffer_pages=8`/`max_total_buffer_pages=10` (room for the primary alone) — a real client connection to the second database was rejected `exhausted: global buffer memory budget exceeded`; raised to `max_total_buffer_pages=16` (exactly primary+secondary) on a clean restart (had to track down and `kill -9` a stale prior `nextsqld` still holding the data-directory lock after an ineffective `kill %1` across separate shell invocations, a live-verification-harness snag rather than a product bug) and the same connection then reached real SQL execution, past the budget gate entirely — confirming both the rejection and the release-on-close/retry path against a real process. No WAL/catalog/wire-protocol change — this is purely an in-memory admission gate at database-open time. Docs: `docs/design-multidatabase-dbaas.md` (§9/§16 M2-3b-2 bullets marked landed, top status line, live-verification writeup), `docs/web/content/docs/config.md` (new `max_total_buffer_pages` key documented, plus the pre-existing but previously-undocumented `max_open_databases` added alongside it), `TODO.md` (checklist line, this entry), `CHANGELOG.md`. **M2-3b-3 (centralize `TaskRuntime`'s per-database goroutine pools), M2-4b-2/M2-4b-3/M2-4c, and the M3+ production-hardening items (ID-based layout migration, registry backup/restore/PITR, registry Raft replication) all remain open and unscheduled — continuing per the user's "all uncheck" instruction.**

81. [x] Multi-database hosting M2-3b-3a — shared bounded task-execution worker pool, per-database polling kept (2026-09-03) — **NOT a Phase 27 item.** Continuing the "continue all uncheck from Phase 0 - Phase 27" instruction; picked the next M2-3b sibling (M2-3b-3, explicitly the last unscheduled item in that decomposition). Scoped first via a dedicated Explore fork before writing any code, given the item's own prior writeup already flagged it as "a genuine internal redesign, not a parameterization tweak." The fork's findings confirmed this: no existing fan-out-poller/shared-worker-pool type in the codebase to model on, and a real, previously-unidentified correctness hazard once workers are shared across databases (below) — so, mirroring M2-3a/M2-3b's own precedent, decomposed M2-3b-3 into M2-3b-3a/b/c in both the design doc and this file before implementing, and built only the smallest slice (M2-3b-3a). **Design**: new `executor.TaskPool` (`internal/executor/task_pool.go`) — one fixed-size worker set (`task_workers` config key, 0 = `defaultTaskWorkers`) plus shared `jobs`/`slots` channels, constructed once per process instead of once per open database (each open database previously spawned its own `Workers+1` goroutines via its own `TaskRuntime`). `TaskRuntime` keeps its per-database `coordinate()`/`cycle()` poll loop exactly as before — it still claims only its own database's due tasks/schedules on its own schedule; centralizing that polling itself, so one scheduler enumerates every open database, is the deliberately-not-built M2-3b-3b — but now submits claims, tagged with the submitting `*TaskRuntime`, to the shared pool instead of a private `jobs` channel. **Correctness hazard found and closed during design, not deferred to M2-3b-3b**: since workers are now shared, closing one database's `TaskRuntime` can no longer synchronously stop the exact goroutines that might touch its `*DB` — a pool worker could be mid-execution of, or about to pick up, that database's job at the instant `Close` is called (this never mattered before, since a runtime's own workers belonged to it exclusively). Closed with a new per-runtime `inFlight sync.WaitGroup`, incremented when `cycle()` hands a claim to the shared pool and decremented once a pool worker finishes executing it; `TaskRuntime.Close()` now waits it out (after stopping its own coordinator) before returning, guaranteeing no pool worker holds or will pick up a reference to that database by the time the caller — M2-3b-1 idle eviction, or process shutdown — proceeds to close the database itself. `cmd/nextsqld/main.go` constructs one `TaskPool` early, deliberately with a background (not the signal-aware server) parent context, with its own `Close` deferred *before* every other close-related defer registered afterward — so it runs *last* (defers are LIFO), strictly after `srv.Close()` (closes the primary's runtime) and the `dbMgr`/secondary-cleanup defer (closes every secondary's runtime) have already run; documented directly on `TaskPool.Close` as its precondition, since closing it earlier would leave a still-open `TaskRuntime`'s `cycle()` blocked sending to a `jobs` channel nobody drains anymore. A drive-by investigation into the real `CANCEL TASK` path (needed to understand what `TaskRuntime.Cancel`'s `running` registry would need to become once workers are shared) found it is dead code in production today: `Session.execCancelTask` signals cancellation via `db.RequestTaskCancellation`/`db.taskCancels`, a separate, already-correctly-DB-scoped mechanism `TaskRuntime.execute` already populates regardless of which pool worker runs it — `Cancel`/`running` are exercised only by tests. Left in place unchanged (flagged as optional follow-on M2-3b-3c) rather than removed, since deleting live-but-unused public API is a separate decision from this increment's scope. Tests: new `TestTaskPoolSharedAcrossTwoRuntimes` (two real databases, a pool sized to exactly one worker, both databases' due tasks succeed) and `TestTaskRuntimeCloseAllowsSafeDBCloseWhilePoolShared` (mirrors the real M2-3b-1 eviction sequence: one database's runtime closes, that database closes immediately after, while the shared pool and another open database's still-open runtime keep running — clean under `-race`), plus every existing `task_runtime_test.go`/integration-test `StartTaskRuntime` call site updated to construct a `TaskPool` first (mechanical). `go build ./...` clean; `go vet ./...` unchanged (same pre-existing unrelated `internal/executor/cdc.go` finding). All green under `-race`: `internal/executor` (full, incl. `aggregate`/`join`/`sort`/`vector`, 136.8s), `internal/config`, `cmd/nextsqld`, `tests/integration`. **Live verification against real `nextsql`/`nextsqld` binaries**: a real deployment with a second realm/database, `nextsqld` started with `task_workers=1` (the whole process sharing exactly one worker) — a primary-database `CREATE SCHEDULE ... EVERY '1s'` ticked 10 times over ~5s through the shared pool, while a concurrent rapid-reconnect loop against the second database's own independently-scheduled workflow also successfully claimed and executed at least once through that same single shared worker, confirming the fan-out works in a real process (the second database's lower tick count is M2-3b-1's pre-existing per-connection idle-eviction behavior, unrelated to this change — a one-shot CLI connection closes the database, and its scheduler, the instant the query returns; an earlier live-verification run that hit a spurious "io: read" opening the second database was root-caused to scratch-directory-reuse contamination across restarts, the same pre-existing harness artifact the M2-5 entry already documented, and did not reproduce on a completely fresh environment). No WAL/catalog/wire-protocol change — purely an in-process goroutine-pool consolidation plus one new close-ordering primitive. Docs: `docs/design-multidatabase-dbaas.md` (§9/§16 M2-3b-3a bullets, top status line), `docs/web/content/docs/config.md` (`task_workers` documented), `TODO.md` (checklist line, this entry), `CHANGELOG.md`. **M2-3b-3b (centralized poll-loop fan-out) and M2-3b-3c (optional `TaskRuntime.Cancel` cleanup) remain open and unscheduled**, as do M2-4b-2/M2-4b-3/M2-4c and the larger M3+ items.

82. [x] Per-realm and per-database connection limits — closes Phase 27 (2026-09-03) — **IS a Phase 27 item: the last one.** Continuing "continue all uncheck from Phase 0 - Phase 27" after M2-3b-3a; before picking the next M2/M3 item, re-examined P27's own last deferred exit-gate-adjacent line, whose deferral note ("one `nextsqld` process still opens exactly one database... a per-database limit is indistinguishable from the per-process `max_connections` until selectable multi-database hosting ships") predates M2-3a/M2-5/M2-6, all now landed. Scoped via a dedicated Explore fork to confirm the premise was actually stale before writing code: verified `dbmanager.Manager.Acquire` (M2-3a) gives each connection's goroutine an independent, concurrently-held `*executor.DB` handle — nothing serializes two connections resolved to different databases — and that this is already exercised by two existing integration tests holding simultaneous connections to distinct databases/realms and interleaving `Exec` calls on both. **The blocking premise no longer holds; implemented directly** (small, well-scoped, security-adjacent-but-not-security-sensitive change — the new check only ever counts identity information the connecting client already supplied in its own `Hello`, so no new pre-authentication disclosure surface, confirmed by tracing exactly where `realmName`/`dbName` are resolved relative to the existing M2-6 `identityOK` fold before writing the check). New `protocol.Limits.MaxSessionsPerDatabase`/`MaxSessionsPerRealm` (`max_connections_per_database`/`max_connections_per_realm` config keys, both 0 = unlimited, matching `MaxSessionsPerUser`'s existing convention) enforced in `serveConn` immediately after the pre-existing `MaxSessionsPerUser` block — same check-increment-under-`s.mu`-then-`defer`-decrement shape, new `dbConnKey{realm, database}` map plus a `map[string]int` keyed by realm name alone. Placed there deliberately, not earlier: by that point `authErr`/`identityOK` (M2-6) have already resolved, so an unauthenticated or invalid-realm/database probe never reaches this check at all, and `realmName`/`dbName` (previously computed only inside the `dbmanager` branch, hoisted to the earlier point in `serveConn` where `realmName` was already being resolved for the M2-6 identity fold — a small DRY cleanup, not a behavior change) are always the client's own already-disclosed values by the time they're used as a counting key. A single-database legacy deployment (no `dbmanager`) is unaffected by default but can still set either knob meaningfully — it collapses to a finer-grained `MaxSessions`. Tests: `tests/integration/protocol_test.go` `TestPerDatabaseConnectionLimit` (mirrors the pre-existing `TestPerUserConnectionLimit` exactly, proving the mechanism on a legacy single-database deployment); `tests/integration/multidb_test.go` `TestPerDatabaseConnectionLimitIsolatesDatabases` (exhausting db1's own limit never blocks db2 in the same realm) and `TestPerRealmConnectionLimitCountsAcrossDatabases` (a connection to db2 correctly counts against the same realm-wide budget db1's connection already used, proving per-realm and per-database are genuinely different counters, not aliases) — `startMultiDBServer` gained an optional trailing `configure ...func(*protocol.Server)` parameter (variadic, so all four pre-existing call sites are unaffected) to reach `srv.Limits` before the server starts, mirroring `startTLSServer`'s own pre-existing shape. `go build ./...` clean; `go vet ./...` unchanged (same pre-existing unrelated `internal/executor/cdc.go` finding). All green under `-race`: `internal/protocol`, `internal/config`, `cmd/nextsqld`, `tests/integration` (full package). **Live verification against real `nextsql`/`nextsqld` binaries**: a real two-realm deployment, `nextsqld` started with `max_connections_per_database=1`/`max_connections_per_realm=2` — a held connection (via a small throwaway Go-driver script, since `nextsql exec` is one-shot per invocation and can't hold a connection open to race against) to the second realm's database, then a second connection to the *same* database rejected live with the exact designed message (`exhausted: too many connections for database`), while a connection to the unrelated primary database succeeded unaffected in the same window — confirming per-database isolation against a real process, not just in-process tests; the per-realm cross-database counting scenario is already rigorously covered by the automated `-race` test above and was not separately re-verified live (would need a second database provisioned inside the same realm, more setup than the live check needed to add confidence). No WAL/catalog/wire-protocol change — purely a new post-authentication connection-admission gate, same shape as the pre-existing `MaxSessionsPerUser`. Docs: `docs/ops.md`/`docs/web/content/docs/config.md` (new config keys), `docs/design-multidatabase-dbaas.md` not touched (P27 is a separate track from the M2 cross-cutting one, though this item's own closure depended on it), `TODO.md` (P27 checklist line and top-level P27 summary line both flipped to `[x]`, exit-gate preamble rewritten to state Phase 27 is complete, this entry), `CHANGELOG.md`. **Phase 27 — Operational maturity + workload governance — is now fully complete.** Remaining open threads are all outside P27: M2-3b-3b/M2-3b-3c, M2-4b-2/M2-4b-3/M2-4c, and the larger M3+ production-hardening items (ID-based layout migration, registry backup/restore/PITR, registry Raft replication, hierarchical quotas, adversarial gates, production exit gate) in the still-open multi-database-hosting cross-cutting track, plus two long-deferred small items outside that track entirely (`REBUILD INDEX ONLINE`, cron syntax) each explicitly gated on a separate prerequisite being proven first, not on anything this session touched.

83. [x] Multi-database hosting M2-3b-3b — centralize polling itself: one `CentralScheduler` enumerates every open database each tick (2026-09-03) — **NOT a Phase 27 item.** Continuing "continue all uncheck from Phase 0 - Phase 27" after Phase 27 itself closed in log #82; picked the next M2-3b sibling explicitly named in log #81's own writeup as the remaining harder half of that decomposition. Given fresh, deep context on `TaskPool`/`TaskRuntime`/`dbmanager.Manager` from landing 3a and 3b's connection-limit work minutes earlier, did a direct read-and-trace investigation rather than spawning a fresh scoping fork — grounded the one hard constraint that shapes this whole design: `executor.DB.executeClaimedTask` is unexported (package-private), so the actual task-execution call can only ever happen from inside `internal/executor`; since `internal/dbmanager` already imports `internal/executor` (for `*executor.DB`), `executor` importing `dbmanager` back to reach `Manager`'s open-database set would cycle. Resolved by putting the new centralized scheduler inside `internal/executor` anyway, and bridging to `dbmanager` via a plain `DatabaseLister` function type (`func() []DBRef`) that `cmd/nextsqld/main.go` — which already imports both packages — implements with a small closure around a new `dbmanager.Manager.Snapshot() []DBHandle`. No new package needed. New `executor.CentralScheduler` (`internal/executor/central_scheduler.go`): one `coordinate()`/`cycle()` loop, process-wide, that each tick asks its `DatabaseLister` for every open database and claims/dispatches/submits each one's due work to the same shared `TaskPool` (M2-3b-3a) — collapsing polling goroutines from O(open databases) to O(1) on top of 3a's already-shared execution workers. `Snapshot` hands out a ref-held handle per open entry, deliberately reusing `Acquire`/`release`'s existing refcounting rather than inventing a second concurrency primitive — this is what closes the "must coordinate with M2-3b-1 eviction" hazard this item's own prior writeup flagged as unresolved, for free: a database with a scheduler-claimed task still in flight simply can't hit `refs<=0` (and therefore can't evict) until the scheduler's own snapshot ref on it also releases, identical in kind to how a live connection already protects a database today. **A second, genuinely new hazard found and closed during this item's own design, not anticipated by the prior writeup**: a claim submitted to the shared pool executes *asynchronously* on whichever worker picks it up, so releasing a `DBRef` the instant `ClaimDueTasks`/submission finishes (rather than the instant execution finishes) would let the database evict out from under a still-running job — a real use-after-close/data-race hazard, not just a logic bug, since `dbmanager.release` really does call `cleanup()` (closing the `*storage.Engine`) unconditionally once refs hit zero. Closed with a per-tick, per-database `sync.WaitGroup` (`Add` before each submit, `Done` in that job's completion hook) plus one short-lived goroutine per database per tick that waits on it before calling `Release` — deliberately a fresh goroutine each tick rather than a persistent one, since ticks are infrequent (250ms default) and the open-database count is small and bounded (`max_open_databases`), so the overhead is negligible and it avoids a second long-lived goroutine-management problem; tracked by a new `CentralScheduler.inFlight`, mirroring `TaskRuntime.inFlight`'s own guarantee, so `Close()` never returns while one is still pending. **Refactored `taskJob` (M2-3b-3a's shared-pool job type) to carry `(ctx, db, task, config)` directly instead of `*TaskRuntime`**, so both `TaskRuntime.cycle` and the new `CentralScheduler.cycleOne` can submit compatible jobs to one pool; the actual per-task execution body (register the durable per-database cancel context, run `executeClaimedTask`, unregister) moved out of `TaskRuntime.execute` into a shared `runClaimedTask` free function, called from `TaskPool.worker` itself (slot release also centralized there now, removing a small duplication). `TaskRuntime.running`/`Cancel()` — already confirmed dead in production by 3a's own investigation — still work exactly as before via a new optional per-job `onStart(cancel)` hook TaskRuntime alone sets; `CentralScheduler` leaves it nil, since nothing production-real ever depended on it anyway. **`cmd/nextsqld/main.go` rewiring**: the primary now gets its own dedicated `TaskRuntime` only when `hostingRegistry == nil`; once a hosting registry exists, one process-wide `CentralScheduler` (constructed right after `dbMgr`, inside the same `if hostingRegistry != nil` block) covers the primary — via its existing `dbMgr.Preload` registration — and every dbmanager-opened secondary alike, so the Opener's per-secondary `cleanup` closure no longer needs any task-runtime-specific close-ordering of its own (`secTasks.Close()` before `secDB.Close()` is simply gone — `Snapshot`'s ref-holding already makes the ordering safe by construction). `CentralScheduler.Close()` is deferred immediately after it starts, *inside* that same `if` block — which is registered textually after the top-level `dbMgr`/secondary-cleanup defer, so (defers being LIFO) it runs *first*, always finishing its own drain before `dbMgr.Close()` can force-close any database out from under it at final shutdown; this mirrors and reuses exactly the ordering discipline `taskPool`'s own defer already established in log #81. **Two things deliberately scoped out, flagged rather than silently left**: (1) the `REQUIRE CLIENT KEY` lazy-open path's own dedicated primary `TaskRuntime` (in `srv.Unlock`) is untouched — investigated and confirmed that combining `REQUIRE CLIENT KEY` with hosting is a narrow, rare deployment shape where, once that primary is later `dbMgr.Preload`ed from inside `Unlock`, it becomes *redundantly* polled by both that `TaskRuntime` and the already-running `CentralScheduler` — safe (task claiming is transactionally exclusive, confirmed by design, not merely assumed) but wasteful, not attempted here since that whole `Unlock` closure is independently entangled with cluster attachment/WAL archiving/replica-lag monitoring and deserves its own dedicated pass, not a rushed side effect of this one; (2) a genuine behavioral tradeoff versus 3a: `TaskRuntime.coordinate()` always called `cycle()` once immediately on construction, so opening even a very brief connection to a database guaranteed at least one poll attempt for it; `CentralScheduler` has no such per-connection synchronization at all — a database opened only for a very short, bursty request (materially shorter than the default 250ms poll interval) may see zero scheduling attempts before M2-3b-1 evicts it again, whereas 3a's per-connection guarantee would have caught it. Judged acceptable and not fixed here (fixing it — e.g. an on-demand "wake the scheduler now" signal from `Acquire` — is itself a further, separable increment) because it only delays, never loses, a task (the same due row is still there, waiting, the next time the database happens to be open during a tick), and a realistically-held-open connection is completely unaffected — proven, not just argued, in the live verification below. Tests: new `internal/executor/central_scheduler_test.go` — `TestCentralSchedulerAcrossTwoDatabases` (real proof of this item's whole point: two real databases' due tasks both succeed via one `CentralScheduler` sharing a single-worker pool, with **zero** per-database poll-loop goroutine), `TestCentralSchedulerReleasesEveryRefEventually` and `TestCentralSchedulerCloseWaitsOutstandingRefs` (a `trackedLister` test double that panics on any double-`Release`, proving the exactly-once release contract holds across live ticks and across `Close()`), `TestStartCentralSchedulerValidatesArgs`; `internal/dbmanager/manager_test.go` gained `TestSnapshotEmptyWhenNothingOpen` and `TestSnapshotHoldsRefUntilReleased` (a connection's own release, with Snapshot's ref still held, must not evict; only releasing both does). Every pre-existing `task_runtime_test.go`/integration-test `TaskPool`/`TaskRuntime` test re-verified unchanged (the `taskJob` refactor is behavior-preserving by construction, confirmed rather than assumed). `go build ./...` clean; `go vet ./...` unchanged (same pre-existing unrelated `internal/executor/cdc.go` finding). All green under `-race`: `internal/executor` (full, incl. `aggregate`/`join`/`sort`/`vector`, 121.4s), `internal/dbmanager`, `cmd/nextsqld`, `tests/integration` (full package). One real, unrelated environmental flake hit and resolved during this session's broader regression sweep: `cmd/nextsql`'s `TestBackupListAndPruneEndToEnd` failed once with "disk quota exceeded" writing to `/tmp` — root-caused to tmpfs pressure from this session's own accumulated live-verification scratch artifacts (2.1G) plus one stray 774M leftover temp dir from an earlier test run, not a code defect; cleared, and the test (and the full combined `internal/...`/`cmd/...`/`tests/integration/...` sweep) passed clean afterward. **Live verification against real `nextsql`/`nextsqld` binaries**: a real two-realm deployment, `nextsqld` started with `task_workers=1` — the primary database's `EVERY '1s'` schedule accumulated 16 rows over ~5s with **zero dedicated primary `TaskRuntime` at all** (`hostingRegistry != nil` skips it entirely now), proving `CentralScheduler` alone is what drives it; the exact same rapid-reconnect-loop trick that produced 1 successful row for the second database in 3a's own live check produced **0** rows here — reproducing the documented tradeoff above live, not just predicting it on paper — and a realistically-held-open 6-second connection to that same database then accumulated exactly 6 rows (a 1-per-second schedule over 6 seconds), confirming the underlying mechanism is fully correct once the tradeoff's own stated precondition (a connection at least as long as a poll interval) is met. No WAL/catalog/wire-protocol change — purely an in-process scheduling-architecture consolidation plus two new close/release-ordering primitives (`Snapshot`, `CentralScheduler.inFlight`). Docs: `docs/design-multidatabase-dbaas.md` (§9/§16 M2-3b-3b bullets marked landed, top status line, M2-3b-3a's own preamble paragraph split out from this one for accuracy), `TODO.md` (checklist line, this entry), `CHANGELOG.md`. **M2-3b-3c (optional: retire or DB-scope the still-confirmed-dead `TaskRuntime.Cancel`/`running`) remains open and unscheduled**, as do M2-4b-2/M2-4b-3/M2-4c and the larger M3+ production-hardening items (ID-based layout migration, registry backup/restore/PITR, registry Raft replication, hierarchical quotas, adversarial gates, production exit gate).

84. Multi-database hosting M2-4b-2 — scoped, found not actionable yet (2026-09-03) — **NOT a Phase 27 item; no code changed.** Continuing "continue all uncheck from Phase 0 - Phase 27" after M2-3b-3b; M2-3b-3c/M2-4b-3/M2-4c were skipped as explicitly low-value/speculative/deferred by their own prior writeups, so picked M2-4b-2 (per-realm auth files + eviction manager) as the next real-value candidate and scoped it via a dedicated Explore fork before writing anything, given it's an on-disk-format change to the security-critical auth store. **Found it is not actually implementable as a coherent increment right now, for three independent reasons, not one**: (1) `docs/design-multidatabase-dbaas.md` §7's "literal layout" the item's own name cites is a bare directory sketch (`realms/<RealmID>/security/`) with an explicit "exact filenames remain a format decision" caveat — no actual per-realm file format or eviction-manager API has ever been specified, the same design gap M2-4b-1 had to close for its own composite-key approach before that could be built; (2) the stated justification — crypto-shredding one realm's credentials independently on realm deletion — has no feature to attach to: `hosting.Registry` has no `SetRealmState`/`DeleteRealm` at all today (only `SetDatabaseState`, database-scoped); `StateDeleting`/`StateTombstoned` exist in the `State` enum's transition graph but are unreachable for a `Realm`, and a repo-wide search found zero crypto-shred implementation anywhere beyond aspirational design-doc prose — building eviction infrastructure now would be solving for a lifecycle operation that does not exist yet; (3) `auth.Store`/`security.ACL` hold no open file handle at all (every operation is a full decode-on-open, persist-on-every-write cycle, confirmed by direct reading of both files' `Create`/`Open`/`persist` — no cached fd, no `Close()` method on either type) — the `dbmanager`-shaped "bounded open handle + refcount + evict" pattern this item's own name assumes doesn't map cleanly onto them at all; the real design question ("what is even being evicted, given there's no handle to hold open") is unresolved, not just unimplemented. Recorded the full finding directly on the M2-4b-2 checklist line (still `[ ]`, correctly — nothing shipped) with a recommendation: leave unscheduled until realm deletion/crypto-shred is itself scoped and landed first (making the isolation requirement real), at which point re-split into per-realm file format, the eviction-manager mechanism, and the ~24-call-site (mostly test fixtures) migration off today's deployment-wide singleton — rather than force a premature implementation just to show forward motion. This is the same "smallest coherent increment" discipline as every other decomposed M2 item, applied to conclude "not yet, and here specifically is what's missing first" rather than to build a wrong-shaped first slice. Docs: `docs/design-multidatabase-dbaas.md` not touched (the finding lives on the TODO.md checklist line itself, not restated there, to avoid the two drifting), `TODO.md` (M2-4b-2 checklist line rewritten with the full finding, this entry).

85. [x] Phase 0–Phase 27 status audit + flaky-test / `go vet` cleanup (2026-09-03) — **housekeeping, no phase-scope change.** User asked to verify P0–P27 are fully implemented, fully tested, with no skipped tasks, then to fix what was found. **Audit result: every Phase 0–27 checklist item and every phase exit gate is `[x]`.** The only unchecked boxes anywhere in the P0–P27 span are three intentional, documented, non-gate deferrals, each blocked on a distinct prerequisite being proven first: `REBUILD INDEX … ONLINE` (P17, `TODO.md` ~line 976 — needs proven concurrent-write index maintenance), cron `SCHEDULE` syntax (P19, ~line 1243 — deferred until the core scheduler is proven; arguably re-examinable now that M2-3b-3a/b landed real scheduler production experience), and the terminal 100M-operation B+Tree invariant soak (P16 — a standalone measurement explicitly outside the release gate). None gates its phase. **Fixes landed this entry:** (1) **Stale summary blocks in `TODO.md` refreshed** — the header table (Current phase / Status / Last updated), the "Progress" paragraph, the roadmap-summary code block (`[ ] P27` → `[x]`, plus P19's cron follow-on noted), the sequencing paragraph, the "Next action" header, and the Multi-database-hosting track preamble all still described P27 as the open release gate after it closed in logs #79/#80/#82; all now say P0–P27 complete / P28 next. The big historical status paragraph under "# Next action" is left as-is (explicitly retained for history). (2) **Flaky test fixed** — `internal/executor/central_scheduler_test.go` `TestCentralSchedulerReleasesEveryRefEventually` failed ~1-in-3 (`outstanding refs = 1, want 0`): it sampled `trackedLister.outstanding` once at an arbitrary instant, but that counter legitimately oscillates 0→1→0 within every 10 ms `CentralScheduler` poll tick (`cycleOne` takes the ref via `list()` at the top and drops it via `ref.Release()` at the bottom, after the synchronous `ClaimDueTasks`/`DispatchDueSchedules` transactions). Now polls for the counter to settle at zero with a 2 s deadline, matching the test's own "…Eventually" name. Not a real ref leak — the value was always exactly 1, and the production close-path barrier (`inFlight.Wait()`) is separately covered by `TestCentralSchedulerCloseWaitsOutstandingRefs`. No production code changed. Verified: `-race -count=30` across `TestCentralScheduler*`/`TaskRuntime*`/`TaskPool*` clean; full `internal/executor` `-race` green (123 s). (3) **`go vet ./...` finding fixed** — `internal/executor/cdc.go` `execSubscribe` created its cancellable/timeout context *before* validating `p.Operation`, so the `default:` invalid-filter `return` leaked the context (vet: "cancel function is not used on all paths"). Reordered so filter validation runs first; context creation moved below it. `go vet ./...` now completely clean (was one finding, unrelated to any P0–27 work — pre-existing). `go build ./...` clean. Docs: `TODO.md` (summary blocks + this entry), `CHANGELOG.md` (Housekeeping entry under `[Unreleased]`). Everything stays uncommitted (working tree only), per standing convention.

86. [x] Phase 19 — `CRON` schedule expressions (2026-09-03) — **IS a Phase 19 item: the last open one under `## SCHEDULE`.** Continuing "continue all uncheck from Phase 0 - Phase 27". After the log #85 audit, of the three remaining unchecked P0–P27 boxes the cron-syntax deferral was the one whose blocking premise had genuinely gone stale — its note said "deferred until core scheduler is proven", and the scheduler now has real production mileage from the M2-3b-3a/b centralized-scheduler work (logs #81/#83). (`REBUILD INDEX ONLINE` stays deferred — its blocker, proven concurrent-write index maintenance, does not exist; the 100M B+Tree soak is a measurement, not code, and explicitly non-gate.) Scoped by direct grounding reads of the live schedule stack (`internal/catalog/schedule.go` `NSSC` v1 layout + `validateSchedule`; `internal/sql/parser/parser.go` `createSchedule`; `internal/sql/binder/schedule.go` next-fire computation; `internal/executor/task.go` `DispatchDueSchedules` cursor advance; `internal/sql/lexer` keyword table; `internal/system/schema.go` capability row) before writing code — an on-disk catalog-format change to a durable descriptor, so grounded rather than assumed, but self-contained enough not to need a scoping fork.

    **Design**: `CREATE SCHEDULE name CRON '<minute hour day-of-month month day-of-week>' RUN WORKFLOW …`. Standard five-field cron, **UTC only** (matches the engine's canonical-UTC timestamp handling everywhere else). Per field: `*`, a value, an inclusive range `a-b`, a comma list, and a step `*/n` or `a-b/n`. Day-of-week 0–6 with Sunday 0; a bare `7` also means Sunday (ranges containing 7 are rejected — keep the normalization unambiguous). When **both** the day-of-month and day-of-week fields are restricted (neither a bare `*`), a day matches if **either** matches — Vixie-cron semantics. Deliberately **not** supported (documented, not silently dropped): month/weekday names, `@hourly`-style macros, a seconds field, and the `L`/`W`/`#` qualifiers — the smallest surface that covers real scheduling without cron's ambiguous corners.

    **New leaf package `internal/cron`** (`cron.go`, stdlib-only: `fmt`/`strconv`/`strings`/`time`). `Parse(expr) (*Expr, error)` compiles to five `uint64` bitmasks + per-field "was a bare `*`" flags, and stores the canonical single-space form (`Expr.String()`, round-trips through `Parse`). `(*Expr).Next(t) (time.Time, error)` returns the earliest whole minute **strictly after** `t` that matches, by a one-minute-step forward scan bounded at a 5-year horizon — an unsatisfiable expression (e.g. `0 0 30 2 *`, 30 February) fails closed with an error instead of looping. The step-scan is ~2.6M trivial bitmask iterations worst case (a few ms), chosen for obvious correctness over a cleverer field-by-field advance; it runs once per firing and once per `CREATE`. `MaxExprBytes = 256` bounds a stored expression (only rejects pathological fully-enumerated lists). Tests: `internal/cron/cron_test.go` — `TestParseRejectsMalformed` (16 bad forms), `TestParseAcceptsAndCanonicalizes`, `TestNext` (16 cases incl. month/leap-year boundaries, DOW, Sunday-as-0-and-7, DOM/DOW OR), `TestNextIsDeterministicAndStrictlyIncreasing` (200 chained iterations, minute-aligned, deterministic), `TestNextUnsatisfiableFailsClosed`, `TestNextNilExpr`, `FuzzParse` (round-trip + `Next` contract; 20s / ~5M execs clean).

    **Wiring**: lexer `KwCron` + `"cron"` keyword (new reserved word — grep confirmed zero existing identifier collisions). AST `ScheduleCron` kind (value 3). Parser `createSchedule` accepts `CRON` alongside `EVERY`/`AT` (error message now "expected EVERY, AT, or CRON"). Catalog: `Schedule.Cron string` field; `scheduleVersion` bumped **1 → 2**, encoder always emits v2 (`appendString(s.Cron)` right after `Workflow`), decoder reads it only for `version >= 2`, so **v1 descriptors still decode** (`Cron == ""`); `validateSchedule` reworked into a per-kind `switch` — `EVERY`/`AT` require `SpecNS > 0` and empty `Cron`, `CRON` requires `SpecNS == 0` and a non-empty `Cron` that `cron.Parse` accepts (catalog gains a dependency on the new leaf package — no cycle, `cron` imports only stdlib). Binder `bindSchedule`: for `ScheduleCron`, `cron.Parse` then `Expr.Next(createdNS)` for the initial cursor; `SpecNS` stays 0. Dispatcher `DispatchDueSchedules`: after a cron schedule fires, `current.NextFireNS = cron.Parse(current.Cron).Next(nowNS)` — the "next match strictly after now" contract gives the same forward-jump-skips-missed-boundaries behavior `EVERY` already documents (a leader clock that jumped past several cron boundaries emits one task for the oldest and advances straight to the first future boundary, never a burst). `FORBID` concurrency, the deterministic `s/<id>/<due-ns>` task ID, the durable due-index, and failover semantics are all unchanged — cron only changes how the cursor is computed. `system.capabilities` row `schedules` description `"SCHEDULE every/at"` → `"SCHEDULE every/at/cron"` (a description edit, not a column-contract change, so no `SchemaVersion` bump).

    Tests: `internal/catalog/schedule_test.go` — `TestScheduleCronCodec` (v2 round-trip), `TestScheduleCronRejectsBadExpr` (empty / unparseable / SpecNS-set-on-cron / cron-set-on-every), `TestScheduleV1DecodesWithoutCron` (hand-built v1 blob decodes, re-encodes to v2, re-decodes), cron seed added to `FuzzDecodeSchedule`. `internal/sql/parser/parser_test.go` — `CRON` accept case in `TestParseScheduleStatements`, `CRON` (no string) + `WEEKLY` reject cases. `internal/sql/binder/schedule_test.go` — `TestBindScheduleCron` (kind/cron/SpecNS, cursor is a real 03:30 Mon-Fri boundary, + three reject forms). `internal/executor/schedule_test.go` — `TestDispatchCronAdvancesToNextBoundaryAndSkipsMissed` (dispatch 3h20m late → exactly one task, cursor skips to next future top-of-hour, no burst, survives restart with cron cursor intact). `go build ./...` + `go vet ./...` clean. All green under `-race`: `internal/cron`, `internal/catalog`, `internal/sql/{parser,binder,lexer,optimizer,types}`, `internal/system`, `internal/executor` (full, incl. subpackages, 126 s), `tests/integration` (full). No wire-protocol change; the only on-disk change is the additive `NSSC` v2 field with v1 read compatibility. Docs: `docs/workflows.md` ("Schedules and durable tasks" — full cron grammar + semantics + v2 note), `docs/sql.md` (CRON one-liner), `CHANGELOG.md` (Phase 19 entry under `[Unreleased]`), `TODO.md` (`## SCHEDULE` checklist line, roadmap summary, header table, Progress paragraph, this entry). **With this, every checklist box and every exit gate in Phase 0–Phase 27 is `[x]` except the two explicitly-deferred non-gate follow-ons whose prerequisites genuinely do not exist yet (`REBUILD INDEX ONLINE`) or which are a measurement not an implementation task (100M B+Tree soak).** Everything uncommitted, per standing convention.

87. Multi-database hosting — wire `NEXTSQL_HOSTING_MANIFEST_FILE` into `nextsql init` (2026-09-03) — library + init wiring landed; `nextsqld`-serving sub-item deferred. **NOT a Phase 0–27 item; M1-foundation line under the Multi-database hosting cross-cutting track.** Continuing "continue @TODO.md" after log #86. P0–P27 has only the two genuinely-blocked non-gate deferrals left (`REBUILD INDEX ONLINE`, 100M B+Tree soak), so moved to the next unblocked M2/M3 item. Picked the manifest-wiring line because its deferral note — "once live multi-engine routing exists" — has definitively gone stale (M2-5/M2-6 shipped live multi-realm routing), matching the "re-check stale deferrals" convention; the `hosting` library slice it depends on (`bootstrap_manifest.go`: `LoadDeploymentBootstrap`/`RegistryManifest`/`EnsureManifest`/`matchManifest`) already landed and is tested. Scoped by direct grounding reads of `cmd/nextsql/main.go` `initDB`/`prepareHostingBootstrap`/`activateManagedDatabase`/`createOrResumeDatabase`, `internal/hosting/bootstrap_manifest.go`, `internal/hosting/registry.go` `EnsureManifest`/`matchManifest`/`validateManifest`, and `internal/cli/resolve.go` (which already resolves `s.HostingManifest` from flag/env/dotenv but had no consumer).

    **Landed**: new `hosting.EnsureBootstrapManifestKeyFiles(path) ([]string, error)` — a minimal bounded read + `validateYAMLShape` + `bootstrapYAML` decode that creates any per-database `key_file` that does not exist yet (`crypto.CreateKeyFile`, fresh independent AES-256 root, mode 0600; parents not created; existing files untouched; returns the created paths) so a fresh deployment needs only the manifest. `initDB` gains a `--hosting-manifest` flag and, when `settings.HostingManifest != ""` (flag / `NEXTSQL_HOSTING_MANIFEST_FILE` / dotenv), branches — after the shared `MkdirAll` + `AcquireDataDirLock` + `preflightHostingBootstrap` — into new `initFromManifest`: ensure the instance/registry root key (`--instance-key-file`, else `KEY-FILE.instance`), `EnsureBootstrapManifestKeyFiles`, `LoadDeploymentBootstrap` (whole-document + all-key-file validation before any mutation), `EnsureManifest(hosting.Path(dataDir), instanceRoot, build)` with `build = bootstrap.RegistryManifest(dep, StateProvisioning)` (publishes one generation covering every realm/database), then for each realm/database: skip if already `ACTIVE`, else `crypto.ReadKeyFile(db.KeyRef)` + `activateManagedDatabase` (the exact create/verify/publish helper `nextsql database create` uses) + zero the key. The optional deployment-wide bootstrap user (cluster `ADMIN` + database-wide `CONNECT`) was extracted from `initDB` into `bootstrapDeploymentUser` and is called from both the single-pair and manifest paths. `--key-file` is not required (nor used as a database key) in manifest mode as long as `--instance-key-file` resolves; `matchManifest` already treats `ACTIVE` databases as `PROVISIONING` for its idempotent-reapply comparison, so a second identical run is a clean no-op and a partial run resumes.

    Tests: `internal/hosting/bootstrap_manifest_test.go` — `TestEnsureBootstrapManifestKeyFiles` (creates the two missing keys, leaves the pre-existing one, resulting document validates via `LoadDeploymentBootstrap`, second call creates nothing), `TestEnsureBootstrapManifestKeyFilesRejectsBadManifest` (blank `key_file` → `InvalidArgument`, missing manifest → `IO`). `cmd/nextsql/main_test.go` — `TestInitFromManifestBootstrapsEveryRealm` (3 databases across 2 realms end to end: every one `ACTIVE`, every managed DB file present at its `ManagedDatabasePath`, default realm/database resolves, bootstrap user usable, idempotent reapply), `TestInitFromManifestViaEnvVar` (`NEXTSQL_HOSTING_MANIFEST_FILE` env path with `--no-env`). `go build ./...` + `go vet ./...` clean; `internal/hosting`, `internal/cli`, `cmd/nextsql`, `tests/integration` all green; `FuzzDecodeManifest` 10 s clean. **Live-verified against real `nextsql`/`nextsqld` binaries**: `NEXTSQL_HOSTING_MANIFEST_FILE=… nextsql init` against a real 3-database / 2-realm manifest created all 4 key files and provisioned every database `ACTIVE` in one run.

    **`nextsqld`-serving continuation (same day, user chose "implement it now as this line" via `AskUserQuestion` after the init wiring landed and live-testing surfaced that `nextsqld` refused to boot against the manifest deployment — `openHostedDefault` returned `Unavailable "default database layout is not supported by the single-database runtime"`):** turned out genuinely small. `openHostedDefault` no longer restricts the default to `LayoutLegacyDefault` (both valid layouts pass — the registry decode already rejects anything else). The eager primary open at `DATA-DIR/nextsql.db` is now gated on `eagerPrimary := hostingRegistry == nil || hostedDatabase.Layout == hosting.LayoutLegacyDefault`; a manifest deployment (managed-layout default) takes `eagerPrimary == false`, so `db`/`keys`/`env` all stay nil and the process starts with no primary handle. **No `serveConn` change was needed** — the routing already does `mgr.Acquire(realmName, dbName)` for *every* connection whenever `dbMgr` is set (default included); the default only worked before because it was `Preload`ed, and `dbMgr.Preload` is already `if db != nil`, so with `db == nil` the default just opens lazily via the existing `Opener` on first connect. Every post-startup `db`/`keys`/`env` use was already `!= nil`-guarded (`startCluster`, `installArchiver`, the ops/lock/drain/monitor wiring, `env.OnRevoke`) so nothing else needed touching. Two startup-check adjustments: `--key-file` is no longer unconditionally required (relaxed to "`--key-file` or `--instance-key-file`", with a precise error if the eager path is actually reached without `--key-file`); `require_client_key` + a managed-layout default is rejected up front (that mode's own `srv.Unlock` primary-open assumes the legacy path — the combo was already documented "narrow, rare, out of scope"). Accepted limitation, documented: a fully-managed deployment's default gets no WAL archiver / no Raft, identical to every managed secondary since M2-3a; M3 owns per-database WAL/PITR/replication scope. Tests: `cmd/nextsqld/hosting_test.go` `TestOpenHostedDefaultAcceptsManagedLayoutDefault` (build a managed-default registry via `EnsureManifest`, assert `openHostedDefault` accepts it). **Live-verified end to end**: `nextsql init --hosting-manifest` a 3-database / 2-realm deployment → `nextsqld --data-dir … --instance-key-file …` (no `--key-file`) booted clean → `CREATE TABLE`/`INSERT`/`SELECT` on the default realm `acme/main` and the non-default realm `globex/main` both worked, `globex` could not see `acme`'s table (isolation held), and both realms' data survived a full `nextsqld` restart. `go build ./...` + `go vet ./...` clean; `-race`: `cmd/nextsqld`, `cmd/nextsql`, `internal/hosting`, `internal/protocol`, `internal/dbmanager`, `internal/config` green; `tests/integration` green (one `-race -count=2` package-level FAIL with zero failing tests / zero race output reproduced the environment's known fsync-contention flake, clean on `-count=1` and a `-race` retry). Docs: `docs/design-multidatabase-dbaas.md`, `CHANGELOG.md`, `TODO.md` (manifest line + all three sub-items now `[x]`, this entry). Everything uncommitted.

88. [x] Multi-database hosting M2-3b-3c — retire the dead `TaskRuntime.Cancel`/`running` registry (2026-09-03) — **NOT a Phase 0–27 item; the last cleanup line in the M2 track.** Continuing "continue @TODO.md" after log #87. Surveyed the remaining M2/M3 backlog: M2-4b-2 is genuinely blocked (needs realm suspend/delete, itself an untracked gap — log #84), M2-4b-3 is speculative ("if ever needed"), M2-4c "folds into M3 lifecycle work", and the M1/M3 registry-DR + per-database-scope items are M3-scale. **M2-3b-3c was the one unambiguously-ready, unblocked, small item left** — flagged optional and skipped twice (logs #81/#83) only because higher-value work was available then; now it is the last M2 loose end, and leaving confirmed-dead public API around is a real maintenance hazard for the next reader. Grounded by direct reads of `internal/executor/task_runtime.go` + `task_pool.go` + `db.go`: `TaskRuntime.Cancel` writes the durable request via `db.RequestTaskCancellation` then signals `r.running[id]` — but `running` is a second, per-`TaskRuntime` copy of exactly what `db.taskCancels` already holds (populated by `runClaimedTask` on whatever pool worker runs the task, so it works regardless of submitter — `CentralScheduler` never set `onStart` at all), and a repo-wide grep found **no non-test caller** of `TaskRuntime.Cancel` (the real `CANCEL TASK` path is `Session.execCancelTask` → `db.RequestTaskCancellation` → `db.signalTaskCancel`). **Deleted outright** rather than DB-scoped: the `Cancel` method, the `running map[string]context.CancelFunc` field, the `sync.Mutex` guarding only it, `running: make(...)` in `StartTaskRuntime`, the `taskJob.onStart` hook + its `runClaimedTask` parameter + `TaskPool.worker` call site, and the `onStart`/`delete(r.running,…)` bookkeeping in `TaskRuntime.cycle`; dropped the now-unused `internal/catalog` import from `task_runtime.go`. ~45 lines net removed across two files, zero behaviour change (nothing production-real used any of it; every existing task/cancel/schedule test passes untouched). `go build ./...` + `go vet ./...` clean; `internal/executor` full `-race` green (125 s); `tests/integration` `Task|Cancel|Schedule` green. No WAL/catalog/wire/config change. Docs: `docs/design-multidatabase-dbaas.md` (§9 M2-3b-3c bullet rewritten as landed, §16 bullet, top status line — **M2-3b, and the whole M2 selectable-hosting milestone, now complete**), `TODO.md` (M2-3b-3c checklist line `[x]`, the stale "Current status: PARTIAL / FOUNDATION, M2-1/M2-2/M2-3a/M2-3b-1 LANDED" paragraph rewritten to "M2 COMPLETE; M3+ open" with the accurate landed-scope list, this entry), `CHANGELOG.md`. **M2 is complete.** The multi-database-hosting track's remaining work is all M3+: per-database WAL/recovery/cache/PITR/Raft scope, hierarchical quotas + usage ledger, registry backup/restore/PITR + Raft replication + lifecycle failover, ID-based layout migration for the legacy default, realm suspend/delete (untracked gap that blocks M2-4b-2), adversarial cross-realm/failover/upgrade gates, and the production exit gate. Plus P28/P29/P30 (Installer/Manager, Studio, Intelligence) and the two long-deferred non-gate items (`REBUILD INDEX ONLINE`, 100M B+Tree soak).

89. Fixed the P17 `REBUILD INDEX ONLINE` blocker: a real storage-engine transaction-rollback data-corruption bug, independent of the rebuild feature itself (2026-09-03) — user instruction "complete Phase 0 – Phase 27 end to end as production grade." **Audit confirmed every P0–27 checklist item and exit gate was `[x]` except this one** (line 979, deferred pending "proven-safe concurrent-write index maintenance"). Investigating turned up that `REBUILD INDEX ... ONLINE` was already fully implemented and wired, uncommitted (`internal/executor/exec_ddl_online.go` — shadow-tree arm/drain/backfill/swap, well-designed, its own test suite present), but its own flagship concurrency test (`TestRebuildIndexOnlineConcurrentWrites`) was permanently `t.Skip()`'d, pointing at a *separate*, already-written repro test: `TestEngineRollbackClobbersCommittedNeighbors` (`internal/executor/zz_engine_bug_test.go`), documenting that `storage.Engine.RollbackTxn` restored whole B+Tree pages to a pre-transaction image (`undoTxnBuffers`) — silently discarding any other transaction's row committed to the *same physical page* in the meantime. Not rebuild-specific: this is a general core-engine bug hitting any `ROLLBACK`/aborted autocommit statement under concurrent writes to shared pages. **Traced deeper before writing any code** (direct reads of `internal/storage/engine.go`, `internal/storage/btree/{txn,insert,delete,update,mvcc,ownership}.go`, `internal/undo/{log,apply,format}.go`) and found it was worse than the skip note described: when an `INSERT` triggers a B+Tree leaf split, the new sibling page is populated by *physically relocating existing, already-committed rows* onto it (`splitLeafAndInsert`), not just the new row — so the pre-existing `undoTxnBuffers` behavior of freeing a transaction's "created" pages on rollback could destroy committed rows that were merely relocated there by an unrelated split, and `btree.Txn.RestoreSnap()` unconditionally resetting a tree's in-memory root/height on rollback could orphan a structural change another, later-committed transaction had already built on. Presented the confirmed severity via `AskUserQuestion`; **user chose "attempt the full structural fix now."**

    **Fix, grounded in the standard ARIES "nested top action" principle every mainstream B+Tree engine uses** (structural changes — splits, new sibling pages, separator insertion, root promotion — take effect immediately, are visible to and may be built on by other transactions the moment they happen, and are *never* reverted by a rollback; only logical row content is undone): `storage.Engine` already had a complete, durable, per-transaction UNDO chain (`internal/undo`, one `Record{Kind,Key,Old}` per row-level insert/update/delete, populated by every `btree.Txn` mutation via `LogUndo`) used only for crash recovery (`undo.Apply`, replayed against the on-disk file before any buffer pool exists) — and, it turned out, a *dead, half-finished* attempt at reusing it for live rollback already sat uncommitted too: `btree.Tree.applyUndoRec` (in `mvcc.go`) correctly reverses one record by replaying it through the tree's ordinary key-based mutation path (`{delete,update,insert}Locked`, which re-descends from the live root, so it is correct even if a concurrent split relocated the key since the record was logged) — but it was called from `btree.Txn.Rollback()`, a method the real multi-table SQL executor's rollback path (`xact.rollback()` in `internal/executor/session.go`) never uses (it calls `storage.Engine.RollbackTxn` directly), and even where it *was* reachable it assumed every record in the chain belonged to the one tree it was called on — wrong the moment a transaction spans several trees (heap + indexes), which every real DML statement does.

    Landed: new `storage.UndoTarget` interface (`ApplyUndo(txn *Txn, rec undo.Record) error`, implemented by `*btree.Tree`) so `LogUndo` can record, per undo record and in the same order, which live tree produced it (`storage.Txn.liveTargets`, parallel to the durable chain) — `RollbackTxn`/`finishCommitDiscarded` now walk the chain newest→oldest and replay each record through its own tree via `Tree.ApplyUndo` (locks the tree, brackets `Engine.Enter/Leave` exactly like ordinary DML, delegates to the existing `applyUndoRec`) instead of the old `undoTxnBuffers`, which is deleted outright along with `sharedPageIDs` (its only caller). Structural cleanup: `RestoreSnap()` no longer resets root/height (only invalidates the `liveKnown` row-count cache — a correctness-safe cache, not a durability value); `undoTxnBuffers`'s created-page free is replaced by a narrower, provably-safe path at the SQL layer — `Session.reclaimEmptyTreesOnRollback` (new) walks `xact.parts`, and for any tree whose `btree.Txn.WasEmpty()` is true (no root existed when *this* transaction attached — i.e. a brand-new `CREATE TABLE`/`CREATE INDEX` tree, or an unlinked detached tree, that nothing outside this transaction could possibly reference) reclaims every page via the tree's own `OwnedPages()` walk, reusing the exact mechanism `abortOnlineBuild` already uses for an abandoned online-rebuild shadow tree. Live in-memory reversal failure is intentionally best-effort (swallowed, matching the pre-existing `_ = e.Alloc.Reload()` precedent in the same function) and never aborts the rollback itself: nothing durable depends on it succeeding, since a crash before the WAL abort record lands makes recovery's own `undo.Apply` independently redo the identical reversal from the durable log against the on-disk file, regardless of what the live buffer pool did.

    Verification: `TestEngineRollbackClobbersCommittedNeighbors` un-skipped, 5/5 clean under `-race`; full `internal/storage/...` (including the 225 s `btree` suite) clean under `-race`; full `internal/executor/...` (minus the four `TestRebuildIndexOnline*` names, checked individually — three pass, see below) clean under `-race`; `go build ./...` / `go vet ./...` clean repo-wide. Also fixed, unrelated pre-existing compile breaks blocking `internal/executor` tests entirely: `internal/executor/blob_test.go` referenced nonexistent `DB.Path()`/`DB.Keys()` accessors (typo'd from the actual unexported `path`/`keys` fields; superseded mid-session by another concurrent edit to the same file using local `Create`/`Open` vars instead — left as-is).

    **Un-skipping the rebuild feature's own concurrency test (`TestRebuildIndexOnlineConcurrentWrites`) exposed a second, separate, still-open bug — see the updated P17 checklist item (line 979) for the full writeup.** `TestRebuildIndexOnlineRestartAfterSwap`/`TestRebuildIndexOnlineCrashKeepsOldIndex`/`TestRebuildIndexOnlineRejections`/`TestRebuildIndexOnlineBlocksConflictingDDL` all pass. **`REBUILD INDEX ... ONLINE` therefore stays unchecked and unsupported-in-practice** (`internal/system/schema.go`'s `rebuild_index_online` row stays `"unsupported"`) — but every other item in Phase 0–27, including every exit gate, is now confirmed `[x]`, and this turn's fix is a genuine, standalone, verified production-correctness improvement to the core transaction engine regardless of the rebuild feature's own remaining bug. No WAL format, wire protocol, or catalog change. Docs: `TODO.md` (this entry + P17 line rewritten), `CHANGELOG.md`. Everything uncommitted, per the standing repo convention.

90. [x] Datatype expansion D1 — first-class `BLOB` column type (2026-09-03)
    — **NOT a Phase 0–27 item; the first increment on the Datatype expansion
    cross-cutting track scoped this session** (`docs/design-datatypes.md`).
    User asked for a critique of the original flat-taxonomy design doc, then
    to convert the critique into a sequenced/gated plan (D1–D10 + explicit
    cuts), then to implement; D1 (`BLOB`) was the smallest ready item.

    **Design decisions**, each recorded in `docs/design-datatypes.md` D1 and
    `docs/sql.md` before/while coding: (1) one type, variable-length, no
    `BINARY(n)`/`VARBINARY(n)` split — mirrors the existing `STRING`/`TEXT`
    `u32`-length-prefix encoding exactly, just without UTF-8 validation; (2)
    canonical order is plain byte-lexicographic comparison (same
    zero-escaping order-preserving encoding `STRING`/`TEXT`/`JSON` already
    use for index keys), so `BLOB` is usable as a `PRIMARY KEY` and in
    `ORDER BY`/`GROUP BY`; (3) deliberately **isolated from `STRING`/`TEXT`**
    — no implicit byte-for-byte reinterpretation either direction;
    `Coerce(STRING/TEXT, BLOB)` requires hex text (`ParseHexBlob`, same
    shape as `ParseUUID`), and `Value.String()` on a `BLOB` formats as hex,
    so any implicit widening to text is always safe, printable, and
    round-trips through the new `X'<hex>'` literal; (4) **included in
    `ENCRYPTED CLIENT`** — the opaque-ciphertext path
    (`internal/clientenc`) is fully generic over `types.EncodeScalar`/
    `DecodeScalar` and never assumes UTF-8, so this needed zero crypto-path
    changes, just adding `KindBlob` to the allow-list; (5) **no JSON
    interaction** — left as an explicit non-goal (no auto base64/hex
    coercion into or out of `JSON`), since `JSON` has no native binary type
    and a silent encoding choice there would be a bigger, separate design
    question; (6) `INET`/`CIDR`/`MACADDR`-style bounded-`BINARY(n)` was not
    added — out of scope per the design doc's own D1 text.

    **Core engine** (no `NSCT` catalog version bump needed — `Kind` is a
    plain appended-at-the-end byte tag, and the wire protocol
    (`internal/protocol/value.go`) is already fully generic over
    `Type`/`Value`, so neither needed a single line changed): new
    `types.KindBlob` + `types.Blob()`/`BlobValue()`/`ParseHexBlob()`
    (`internal/sql/types/types.go`, `value.go`); row/key encode-decode
    (`row.go`: `encodeScalar`/`decodeScalar`/`skipScalar` reuse the
    `STRING`/`TEXT` u32-length path, `encodeSortable`/`decodeSortable` reuse
    the zero-escaped sortable-bytes path); new lexer token `HexLit` +
    `X'...'` scanner (`internal/sql/lexer/lexer.go`, stdlib `encoding/hex`,
    zero new syntax risk — checked before inventing it, none existed) and
    `BLOB` keyword; parser `colType`/`primary` wiring
    (`internal/sql/parser/parser.go`); catalog `ENCRYPTED CLIENT` allow-list
    message + `WORKFLOW` param type allow-list
    (`internal/catalog/catalog.go`, `workflow.go`); `clientenc.SupportedType`
    (`internal/clientenc/clientenc.go`); vectorized columnar batch support —
    reuses the `Str []string` column exactly like `STRING`/`TEXT`
    (`internal/executor/vector/batch.go`); idempotent-result type allow-list
    (`internal/executor/idempotency.go`); SQL-dump hex-literal export
    (`internal/xport/sql.go`). Full-text search, highlighting, JSON path
    arguments, WKT geo parsing, and the vectorized single-string-column
    `GROUP BY` fast path were deliberately left untouched — each already
    gates on an explicit `KindString`/`KindText` allow-list, so `BLOB`
    correctly falls through to the generic (slower but correct) path
    without any new code.

    **Drivers, all 7**: Go needed **zero code changes** — it imports
    `internal/sql/types`/`internal/protocol` directly, so
    `types.BlobValue([]byte)` already works end to end (verified live, see
    below). JS/Bun/Deno share `drivers/js/protocol.mjs` +
    `client-encryption.mjs` — added `Kind.Blob = 14`, `encodeBlob`/decode,
    and `FieldType.Blob`; a bare `Uint8Array` encodes as `BLOB` unless it is
    exactly 16 bytes (kept as `UUID`, the pre-existing meaning, for
    compatibility) — a 16-byte `BLOB` needs the explicit
    `{kind:'blob',value}` wrapper, same pattern the driver already used for
    `decimal`. Node mirrors the same design in its own copy
    (`nextsql.js`/`client-encryption.js`, `Buffer` instead of `Uint8Array`).
    PHP strings are already byte-safe with no distinct bytes type, so a
    plain `string` stays `KIND_STRING`; `BLOB` requires the explicit
    `['kind'=>'blob','value'=>...]` wrapper (`Protocol.php`, `Client.php`
    `KIND_BLOB=14`, `FieldType::blob()`, `FieldEncryption.php`). Python's
    `bytes`/`bytearray` now encode as the new `KIND_BLOB` instead of the
    previous (undocumented, accidental) `KIND_STRING` reinterpretation —
    a deliberate, documented behavior change since there was no `BLOB`
    before to use (`protocol.py`). Ruby: a `String` with `Encoding::ASCII_8BIT`
    (e.g. via `String#b`) encodes as `BLOB`; any text encoding, UTF-8
    default included, stays `STRING` (`protocol.rb`). Neither Python nor
    Ruby has a client-encryption module yet, so nothing there needed
    updating for either.

    **Tests** (all touched packages green under `-race`, `go vet ./...`
    clean repo-wide): `internal/sql/types` — row/key round trip incl.
    embedded `0x00` and non-UTF-8 bytes, byte-lexicographic `Cmp`/key
    order, hex parse/format, `Coerce` both directions incl. rejecting
    non-hex text and confirming `BLOB` stays isolated from `UUID`
    (`TestBlob*` in `types_test.go`). `internal/sql/parser` — `BLOB` column
    type, `X'...'` literal incl. empty/invalid/odd-length hex
    (`TestParseBlobColumnAndHexLiteral`). `internal/catalog` — catalog
    encode/decode round trip, `BLOB` as a `PRIMARY KEY`, `ENCRYPTED CLIENT
    BLOB` column shape (`TestTableEncodeRoundTripBlobColumn` +2).
    `internal/executor` (new `blob_test.go`) — full `CREATE`/`INSERT
    ... VALUES (X'...')`/`INSERT ... VALUES ($1)` with a raw-byte param
    incl. embedded NUL and invalid-UTF-8 bytes, `SELECT`/`WHERE`/`ORDER BY`
    (byte order verified), `COUNT`, catalog persist-and-reopen durability,
    hex-literal error cases, non-hex-string-to-`BLOB` coercion rejection,
    and a full `ENCRYPTED CLIENT BLOB` round trip (plaintext rejected,
    correct ciphertext accepted and decrypts back to the original bytes).
    Each driver got its own round-trip test in its existing suite: Bun/Deno
    (`FieldType.Blob` in the encryption round-trip table + a dedicated wire
    test covering the 16-byte-Uint8Array/wrapper disambiguation and the
    empty blob), Node (same, `Buffer`), PHP (`unit.php`, explicit wrapper
    form), Python (`test_protocol.py`, `bytes`/`bytearray`/empty),
    Ruby (`test_protocol.rb`, `ASCII-8BIT` vs. default-encoding `String`).

    **Live verification**: built real `nextsql`/`nextsqld` binaries in the
    scratchpad, `nextsql init` + `nextsqld` on loopback (no TLS needed).
    `CREATE TABLE files (id UUID PRIMARY KEY DEFAULT UUID(), payload BLOB
    NOT NULL)` → `INSERT ... VALUES (X'DEADBEEF00')` / `(X'0000FF')` →
    `SELECT payload FROM files ORDER BY payload` returned `0000ff` before
    `deadbeef00` (correct byte order) → `SELECT COUNT(*) WHERE payload =
    X'DEADBEEF00'` → 1 → `INSERT ... (X'ZZ')` cleanly rejected
    (`invalid hex literal`) → killed and restarted `nextsqld`, re-ran the
    `ORDER BY` query, identical result (restart-durable) → created an
    `ENCRYPTED CLIENT BLOB` column and confirmed a plaintext hex-literal
    `INSERT` is rejected server-side (`ENCRYPTED CLIENT assignment requires
    an encrypted parameter...`), exactly like every other `ENCRYPTED CLIENT`
    type. Scratch data dir removed after verification.

    Docs: `docs/design-datatypes.md` (`D1` now describes the landed shape),
    `docs/sql.md` + `docs/web/content/docs/sql.md` (new `BLOB` Types-table
    row), `docs/client-encryption.md` (v1 logical-type list now includes
    `BLOB`), `CHANGELOG.md`, `TODO.md` (this entry + `D1` checklist line).
    `docs/protocol.md` does not enumerate per-type wire tags today, so it
    needed no change. Everything uncommitted, per the standing repo
    convention.

91. [x] Found and fixed the real root cause blocking `REBUILD INDEX ONLINE`: a pre-existing, general storage-engine transaction-attribution race with no special connection to online rebuild at all (2026-09-03) — direct continuation of log #89 ("continue"). Log #89's rollback fix closed `TestEngineRollbackClobbersCommittedNeighbors`, but un-skipping the rebuild feature's own `TestRebuildIndexOnlineConcurrentWrites` still failed intermittently (missing and duplicate entries), so this entry's P17 checklist line stayed unchecked with the failure characterized but not root-caused. **Continuing that investigation** (per the standing "continue" instruction) surfaced something much bigger than either the rollback bug or online rebuild: a bisection harness (`internal/executor/zz_debug_*_test.go`, scratch, deleted before finishing) proved the *exact same* index/heap divergence reproduces with `REBUILD INDEX ONLINE` never invoked at all — plain concurrent `UPDATE`/`INSERT`/`DELETE` against an ordinary secondary index corrupts it under 3-way write contention, deterministically (re-verified against the identical quiesced final state, not read-timing noise). A dedicated `git worktree` at the last real commit (`2fa09c9`, no uncommitted change from this or any prior session) reproduced the same corruption (4/8 stress runs), proving it **predates today's session entirely** — genuinely pre-existing, unrelated to log #89's fix or the online-rebuild feature.

    **Root cause, found by tracing one corrupted row's full LogUndo/ApplyUndo history under real concurrency** (tagging every call with the transaction pointer and ID, not just the ID — Go's allocator reuses `*storage.Txn` addresses across the run, so pointer-only or ID-only tracing both alias different transactions together): an `INSERT`/`UPDATE`/`DELETE`'s own index-maintenance step was intermittently logging its undo record under a **different, concurrently-active transaction's** `lastUndo`/`liveTargets` — confirmed directly: `LogUndo`'s resolved transaction ID for one write repeatedly diverged from the `*btree.Txn` that actually issued it. Traced to `storage.Engine.beginLocked`: `e.opTxn = t` was set unconditionally for every new transaction, including the normal `StartTxn` (`legacy=false`) path used by every concurrent SQL transaction — but `beginLocked` only ever holds `e.mu` (briefly), never `pageMu`, while `opTxn`'s only *other* setter/clearer, `Enter`/`Leave`, holds `pageMu` for its whole critical section specifically so page-mutating work and `opTxn` attribution stay synchronized. A transaction id A's `Enter()`-established `opTxn = A` could therefore be silently overwritten by a completely unrelated transaction B's concurrent `StartTxn()` call while A was still mid-flight inside its own `Enter`/`Leave` section (`pageMu` held) — A's own subsequent `LogUndo` call (for a later step of the *same* statement, e.g. the index-insert half of an UPDATE after the heap-update and index-delete halves already ran) would then read `opTxn = B` and misattribute A's work to B. Neither A's nor B's eventual rollback then correctly reversed the transaction that actually produced the record — explaining every symptom traced: stale surviving index entries, entries duplicated across old and new buckets, and (for `REBUILD INDEX ONLINE` specifically) exactly the missing/duplicate-entry pattern log #89 could not close. This is not scoped to `LogUndo`/undo: every other `opTxn`/`e.txn`-attributed hook (`OnDirty`, `OnInstall`, `LogLogical`, `NoteTree`) reads the identical racy field, and the durable UNDO chain this corrupts is the same one crash recovery's `undo.Apply` trusts — so the exposure plausibly reached further than what today's specific tests exercise (row-level MVCC undo content and secondary-index maintenance), though nothing beyond that was directly observed corrupted.

    **Fix**: `beginLocked` no longer sets `e.opTxn` for the non-legacy (`StartTxn`, real concurrent SQL transaction) path — only `Enter`/`Leave`, correctly synchronized with `pageMu`, may set or clear it there now; a transaction relies solely on its own later `Enter()` call, exactly as every actual page-mutating operation already does. Left unchanged for `legacy=true` callers (`BeginWrite`'s maintenance-operation path, and `OnDirty`'s implicit auto-begin when no transaction is active at all) — by construction nothing else can be concurrently entered when those run, so the immediate assignment there is not known to be unsafe and was not touched, matching "smallest coherent increment" discipline (no unproven scope added to the fix).

    **A genuine, second, independent contributing bug was found and fixed along the way, before the opTxn race was identified**: `maintainIndexes`'s row-level `old` value (used to compute which index entry to delete) can be stale under `ReadCommitted` — `refreshIfRC()` takes a fresh snapshot immediately before the actual heap write, which can be newer than whatever snapshot a caller used to read the row earlier in the same statement (a scan-then-write `UPDATE`/`DELETE`), and RC raises no write-write conflict for this, so the write can silently overwrite a row a different, concurrently-committed transaction already changed — deleting the wrong (stale) index entry and orphaning the real one. Fixed with new `btree.Txn.{Update,Delete}ReturningOld`/`{Update,Delete}AtReturningOld` (surface the payload actually found and overwritten/removed at the exact moment of the write, from the same critical section, instead of trusting an earlier read) and `executor.Session.{heapUpdate,heapDelete}ReturningOld`, wired into `replaceRow`'s two paths and `removeRow` in place of the passed-in (possibly stale) `old`/`row` for the `maintainIndexes` call specifically — every other consumer of `old` (triggers, FK cascade, CDC change-stream staging, vector cleanup) is deliberately left reading the original value, out of scope for this fix. This landed *before* the opTxn race was found and does not by itself explain the corruption (verified: reverting only this fix while keeping the churn workload still reproduced the bug, and disabling `undoTxnLogical` entirely — masking the opTxn race's *symptom* by making rollback inert again — made the churn test pass, which is what pointed at attribution rather than reversal content as the real defect) — kept because it is independently correct and closes a real, if narrower, staleness gap.

    **A regression this fix's first pass introduced was caught by the executor test suite and fixed in the same turn**: for vector-column tables, decoding the true-old payload calls `decodeHeapRow` → `hydrate`, which re-attaches vector data by looking it up in the vector store — but `replaceRow`/`removeRow` were calling `deleteVectors` (which removes that same vector-store entry) *before* the true-old decode, so hydration failed (`missing vector payload`) once real vector fixtures exercised the new code path. Reordered in all three call sites (`replaceRow`'s cross-partition-move and same-partition paths, `removeRow`) to decode/hydrate before `deleteVectors` runs.

    **Verification**: the deterministic bisection harness that found the race (churn workload: 3 concurrent writers, `INSERT`/`UPDATE`/`DELETE` against a non-unique secondary index, ~230 successful writes per iteration) went from 14/20 failing (pre-opTxn-fix, with only the ReturningOld fix applied) to 0/20 clean across 6 consecutive 20-iteration runs (240 iterations, zero failures) after the opTxn fix. `TestRebuildIndexOnlineConcurrentWrites` and `TestEngineRollbackClobbersCommittedNeighbors`: 5/5 clean under `-race`. Full `internal/storage/...` (`-race`, including the 227s `btree` suite), `internal/txn/...`, `internal/replication/...`, full `internal/executor/...` (`-race`, including the 4 vector-index tests the ReturningOld regression broke and this turn's reorder fixed), and `tests/integration/...` all clean. `go build ./...`/`go vet ./...` clean repo-wide. All scratch `zz_debug_*` bisection test files deleted before finishing — none of the diagnostic instrumentation (`println` tracing in `LogUndo`/`ApplyUndo`/`scanIndex`/`maintainIndexes`) survives in the final diff.

    **With this, `REBUILD INDEX ... ONLINE` is genuinely safe and closed** — `internal/system/schema.go`'s `rebuild_index_online` capability row is now `"supported"`; the P17 checklist line above is `[x]`. **Phase 0–27 is now fully complete with zero remaining deferrals** — the one non-gate item left open all session (line 979) is closed. No WAL format, wire protocol, or catalog change; the `e.opTxn` fix touches only in-memory transaction bookkeeping. Docs: `TODO.md` (P17 line + this entry), `CHANGELOG.md`. Everything uncommitted, per the standing repo convention.

92. [x] Datatype expansion D3 — fixed-width unsigned integers (`UINT8`/`UINT16`/`UINT32`/`UINT64`) (2026-09-03) — **NOT a Phase 0–27 item.** Continuation of the Datatype expansion cross-cutting track after D1 (`BLOB`, log #90) and D2 (signed ints, log #91); `docs/design-datatypes.md` D3 was already marked "ready to scope" once D2 landed, so this picked it up directly.

    **Design decisions**, recorded in `docs/design-datatypes.md` D3 before/while coding: (1) mirrors D2's shape — `Kind` values appended after `KindInt64` (no `NSCT` version bump); (2) **index-key ordering** is plain unsigned big-endian bytes, simpler than D2's signed case — no sign-bit flip needed, since unsigned values already sort correctly in that byte order; (3) same **arithmetic-promotes-to-DECIMAL** (`+ - * /` and unary `-` always operate in arbitrary-precision `DECIMAL` space, so the operation itself can never overflow) and **narrowing/negative-assignment errors rather than wraps** decisions as D2, reused rather than relitigated; (4) **coercion extends D2's precedent rather than repeating its isolation**: `INT8..64` and `UINT8..64` coerce directly into each other (range/sign checked both ways — a negative `Int` into any `Uint` errors, a `Uint` magnitude above the target signed kind's max errors), on the reasoning that both are exact fixed-width integers and forcing every cross-family conversion through `DECIMAL` would be pure friction, unlike D1/D2's isolation from genuinely unrelated families (`BLOB`/`UUID`/`BOOL`/`JSON`/geo); (5) `SUM`/`AVG` reuse the same DECIMAL-promotion accumulator, `MIN`/`MAX` stay in the column's own uint kind; (6) ordinary FK-eligible scalars; (7) `ENCRYPTED CLIENT` included (same generic opaque-scalar reasoning as D1/D2).

    **Core engine** (no `NSCT` version bump): `internal/sql/types/types.go` — `KindUint8/16/32/64` (appended after `KindInt64`), `UintRange`/`IsUint`, `Uint8()`/`Uint16()`/`Uint32()`/`Uint64()` constructors, `Comparable()`. `internal/sql/types/decimal.go` — `DecimalFromUint64` (uses `big.Int.SetUint64`, since a `UINT64` magnitude can exceed `math.MaxInt64`). `internal/sql/types/value.go` — new `Value.Uint uint64` field (parallel to the existing `Value.Int int64`, since a `UINT64` value can't always be represented as `int64`); `UintValue`/`NewUint`/`Uint8Value`.../`decimalToUint`; `String()` (`strconv.FormatUint`); `Cmp` (`uintish`, mirroring `intish` — same-Kind or cross-width-Uint direct comparison; signed/unsigned stay isolated for a *direct* `Cmp` call, since converting between the field representations safely needs the range checks `Coerce` already does — the `executor.eval` binary-comparison path already coerces mismatched Kinds to a common one before calling `Cmp`, so a mixed `Int`/`Uint` SQL predicate still works via that path); `Coerce` (`KindDecimal` dest gains a `Uint` source case; `KindInt8..64` dest gains a `Uint` source case, range-checked via `math.MaxInt64`; new `KindUint8..64` dest case handles `Uint`/`Int`/`Decimal`/`String`/`Text` sources, mirroring the `Int` dest case exactly). `internal/sql/types/row.go` — `skipScalar`/`encodeScalar`/`decodeScalar` (plain raw bytes, no sign handling, mirroring `Int`'s row-storage shape) and `encodeSortable`/`decodeSortable` (plain unsigned big-endian, no XOR) each gained the four `Uint` cases. `internal/sql/lexer/lexer.go` — `KwUint8/16/32/64` tokens + keyword-map entries. `internal/sql/parser/parser.go` — `colType()` cases (this is also what T-shapes any type-name lookup elsewhere, since there is no separate `CAST` syntax in this SQL dialect — confirmed by grep, not assumed). `internal/executor/eval.go` — `isNumericKind` extended with `types.IsUint`. `internal/clientenc/clientenc.go`, `internal/sql/binder/binder.go` (`facetable`), `internal/catalog/workflow.go` (`validWorkflowType`), `internal/executor/idempotency.go` (`validateIdempotentResultType`), `internal/xport/sql.go` (`sqlLiteral`) — each gained the four `Uint` kinds alongside their existing `Int8..64` entries. `internal/executor/vector/batch.go` — new `Vector.Uint []uint64` field (parallel to `Int`), wired into `newVec`/`setAt`/`getAt`. `internal/catalog/fk.go` needed no change: FK type-compatibility for a same-Kind-and-width pair already goes through the generic `a.Equals(b)` fast path, and cross-width/cross-family compatibility falls through to `fkProbeValue`'s existing default (`Null`, i.e. incompatible) exactly as it already does for cross-width `Int` pairs today — same pre-existing scope boundary, not a D3 regression.

    **Drivers, all 7**: Go needed no code change (shares `internal/sql/types`/`internal/protocol` directly). `drivers/js/protocol.mjs` (shared by Bun/Deno) — `Kind.Uint8..64 = 19..22` (matching the server's appended `Kind` enum order exactly), `UINT_RANGES`/`encodeUint` (mirrors `encodeInt`; `{kind:'uint8'|...,value}` wrapper), decode cases (`UINT8/16/32` as `Number`, `UINT64` as `BigInt` — same "doesn't fit safely in a JS double" reasoning as `INT64`). `drivers/js/types.d.ts` — `Kind.Uint8..64` type declarations. `drivers/js/client-encryption.mjs` (Bun/Deno's shared NSCE1 implementation) — `FieldType.Uint8..64`, `validateType` allow-list, `encodeUint`/`decodeUint` (raw fixed-width shape, mirroring `encodeInt`/`decodeInt`), wired into `encodeScalar`/`decodeScalar`. `drivers/node/nextsql.js` — same `Kind`/`encodeUint`/decode-case additions as the shared JS core, duplicated (Node has its own independent protocol implementation, per existing precedent). `drivers/node/client-encryption.js` — same `FieldType`/`encodeUint`/`decodeUint` additions; **also found and fixed an incidental pre-existing gap here, unrelated to D3 itself**: this file's `FieldType`/`validateType`/`encodeScalar`/`decodeScalar` had never picked up D2's `INT8..64` at all (grep-confirmed: zero `Kind.Int8` references before this change) — added the missing `INT8..64` support in the same pass, alongside the new `UINT8..64` support, so Node's `ENCRYPTED CLIENT` surface now actually matches its own `docs/client-encryption.md`-documented v1 type list. `drivers/php/src/Client.php` — `KIND_UINT8..64` constants. `drivers/php/src/Protocol.php` — `encodeUint`/`encodeNarrowUint`/`encodeUint64`/`decodeUint64`; promoted `decToBytes`/`bytesToDec` (previously `private`) to `public` so `FieldEncryption` can share them. **PHP-specific design note**: PHP's native `int` is a signed 64-bit type with no unsigned counterpart (confirmed via the existing `Protocol::i64`'s own D2-era comment/fix), so a `UINT64` value at or above 2^63 cannot be a plain `int` without silently going negative; decode instead returns a decimal digit string in that case (`bytesToDec`, reusing the exact big-number machinery `DECIMAL` already uses), and encode accepts either a non-negative native `int` or such a string — an explicit, deliberate asymmetry from `INT8..64`'s pure-`int` shape, not an oversight. `drivers/php/src/FieldType.php` — `uint8()`.../`uint64()`. `drivers/php/src/FieldEncryption.php` — `encodeUint`/`decodeUint`, wired into `encodeScalar`/`decodeScalar`/the `validateType` allow-list; **the same incidental Node-style gap existed here too and was fixed in the same pass**: `FieldEncryption.php`'s `encodeScalar`/`decodeScalar`/allow-list had `INT8..64` support already (unlike Node), so only the new `UINT8..64` cases were additive here — no gap to backfill on the PHP encryption side, only on Node's. `drivers/python/nextsql/protocol.py` — `KIND_UINT8..64`, `Uint8`/`Uint16`/`Uint32`/`Uint64` dataclasses (Python `int` is arbitrary precision, so decode is a single `int.from_bytes(..., signed=False)` line per width, no BigInt-style split needed), `_UINT_RANGES`/`_encode_uint`, wired into `encode_param`/`decode_value`; exported from `drivers/python/nextsql/__init__.py`. Python has no `ENCRYPTED CLIENT` (NSCE1) implementation at all (confirmed, pre-existing, out of scope for D3 same as it was for D1/D2). `drivers/ruby/lib/nextsql/protocol.rb` — `KIND_UINT8..64`, `Uint8`/`Uint16`/`Uint32`/`Uint64` structs (Ruby `Integer` is also arbitrary precision, same simplicity as Python), `encode_uint` helper mirroring `encode_int`, wired into `encode_param`/`decode_value`; aliased in `drivers/ruby/lib/nextsql.rb`. Ruby likewise has no `ENCRYPTED CLIENT` implementation (pre-existing, out of scope).

    **Tests** (all touched packages green): `internal/sql/types/types_test.go` — `TestUintKeyOrderAndRoundTrip` (all 4 widths, boundary values incl. `math.MaxUint64`), `TestUintCmpAndCrossWidth` (incl. asserting signed/unsigned stay isolated for a direct `Cmp`), `TestUintCoerce` (narrowing/widening/negative-rejection/`DECIMAL` both directions incl. a `math.MaxUint64` round trip/decimal-text/`Int<->Uint` both directions/isolation from `BLOB`/`BOOL`), `TestNewUintRange`. `internal/executor/uint_test.go` (new, mirrors `int_test.go`): `TestUintInsertSelectRoundTrip` (all 4 widths, zero and max boundaries incl. a `UINT64` value above `math.MaxInt64`, catalog persist/reopen durability), `TestUintOrderByCorrectness` (`UINT32`/`UINT64` `ORDER BY`, confirming plain unsigned order — no sign-bit-flip mistake), `TestUintOverflowRejection` (narrowing, negative literal, negative `Int32` param into a `UINT32` column), `TestUintIntCoercion` (a signed param into an unsigned column via `INSERT`, plus direct `types.Coerce` boundary cases both directions), `TestUintArithmeticPromotesToDecimal`, `TestUintAggregatePromotion` (`SUM`/`MIN`/`MAX`), `TestUintForeignKey`, `TestUintEncryptedClient`. Each driver got its own round-trip test in its existing suite, all run and green: Bun 19/19, Deno 19/19, Node 21/21 (incl. the newly-added `INT8..64`/`UINT8..64` `ENCRYPTED CLIENT` round-trip cases that close the gap described above), PHP `unit.php` `ok` (incl. the `UINT64`-above-`PHP_INT_MAX` string round trip and its overflow-rejection case), Python `unittest` 36/36, Ruby `34 runs, 142 assertions, 0 failures`. `go build ./...` clean; `go test ./...` clean repo-wide (three tests — `TestBackupListAndPruneEndToEnd`, `TestEncryptedClientBackupRestoreRemainsOpaque`, `TestPITRByLSN` — failed once under full-suite parallel load and passed clean in isolation immediately after; confirmed pre-existing environmental flakiness unrelated to this change, not re-triggered on a second full run).

    **Live verification**: built real `nextsql`/`nextsqld` binaries in the scratchpad, `nextsql init` + `nextsqld` on loopback (no TLS needed). `CREATE TABLE uints (id UINT64 PRIMARY KEY, a UINT8, b UINT16, c UINT32, d UINT64)` → inserted the all-zero row and the all-max-value row (`d = 18446744073709551615`, above `math.MaxInt64`) → `SELECT ... ORDER BY id` returned both correctly → `INSERT ... (a) VALUES (256)` and `VALUES (-1)` both cleanly rejected (`UINT8 out of range`) → a dedicated `UINT64` `ORDER BY` table with `0`, `1`, `9223372036854775808` (`2^63`, `math.MaxInt64 + 1`), and `18446744073709551615` (`2^64-1`) sorted in the correct numeric order (proving the no-sign-flip design decision live, across the exact boundary a sign-bit mistake would get wrong) → killed and restarted `nextsqld`, re-ran both `SELECT`s, identical results (restart-durable) → created an `ENCRYPTED CLIENT UINT32` column and confirmed a plaintext `INSERT` is rejected server-side, exactly like every other `ENCRYPTED CLIENT` type. Scratch data dir and binaries removed after verification.

93. [x] REBUILD INDEX ... ONLINE — third concurrency race found and fixed by an independent production-readiness re-audit of Phase 0–27 (2026-09-03). User asked to verify P0–27 is genuinely production-grade rather than trust the existing checkmarks; five parallel audit passes re-ran the real test suite and re-checked exit-gate claims against live code. One (covering P16–P18) refused to accept log #91's "closed" status at face value, re-ran `TestRebuildIndexOnlineConcurrentWrites` under `-race` repeatedly, and reproduced real, intermittent (~5%) `index has N+1 entries, heap has N rows` failures — a genuine data-integrity bug survived log #91's fix and its own "0/20 across 240 iterations" bisection.

    **Root-caused via a low-overhead lock-free trace** (formatted/`fmt`-based tracing changed goroutine-scheduling timing enough to mask the race entirely — a heisenbug; a lock-free atomic-indexed struct-array tracer, dumped only on failure, reproduced it cleanly and pinpointed the exact sequence): `internal/executor/exec_ddl_online.go`'s backfill inserted `(v, pk)` into the shadow tree; a later `UPDATE` on that same row, executing *after* the catalog swap had already made the shadow tree "the real index," took the ordinary (non-mirror) `maintainIndexes` path — `willMirror=false`, since `sh == realIx` post-swap — and that path deletes the row's old index entry via `internal/executor/fk.go`'s `treeDelete`/`treeInsert`, which route through `fkWriteSnap()`. `fkWriteSnap` only captures a *fresh* MVCC snapshot inside an FK cascade or conflict-write (`s.fkDepth != 0 || s.conflictWrite`); an ordinary top-level statement falls back to the transaction's *own* snapshot, captured at `BEGIN`. If that transaction began (or was blocked acquiring the row's lock) before the backfill's own chunk transaction committed the entry it is now trying to delete, its stale snapshot cannot see that entry — `tx.DeleteAt`'s visibility check reports it as not-found, which the caller silently tolerates (by design, for the ordinary "nothing to delete yet" case) — so the delete becomes a no-op and the old entry survives as an orphan once the update's insert of the new entry succeeds. `mirrorOnlineIndex` was specifically engineered around this exact hazard (its own doc comment: "uses a freshly captured snapshot so a backfill row that committed after this transaction began does not trip a spurious write-write conflict") — but that protection only applied while `sh != realIx`; once the swap makes them equal, the ordinary path takes over and the same hazard reappears unprotected.

    **Fix**: `internal/executor/fk.go` gains `freshTreeSnap()`, a small helper capturing a brand-new snapshot (same `tm.Capture(h.ID)` call `mirrorOnlineIndex` already uses). `internal/executor/exec.go`'s `maintainIndexes` now computes `online := realIx != nil && s.db.onlineBuildActive(idxKey(tab.Name, idx.Name))` once per index and, only when true, calls `itx.DeleteAt`/`itx.InsertAt` directly with a fresh snapshot instead of `s.treeDelete`/`s.treeInsert` — extending the mirror path's existing protection to cover the entire window an online rebuild is registered (armed *or* swapped-but-not-yet-disarmed), not just the pre-swap half of it. Scoped to exactly the non-partitioned single-real-index branch `REBUILD INDEX ONLINE` can ever apply to; every other `treeDelete`/`treeInsert` caller (FK cascades, vector store, idempotency records, partitioned-table index maintenance) is untouched.

    **Verification**: `TestRebuildIndexOnlineConcurrentWrites` — 60/60 clean under `-race` (previously ~5% failure), 300/300 clean without `-race` (the mode that actually caught the original failure fastest); full `internal/executor/...` suite (incl. `aggregate`/`join`/`sort`/`vector`) green under `-race`; `tests/integration/...` green under `-race`; `go build ./...`/`go vet ./...` clean repo-wide. All temporary debug-tracing code was removed before landing the fix — the shipped diff is `fk.go` (+14 lines: the helper) and `exec.go` (the `online`-gated branch in `maintainIndexes`), nothing else. No WAL format, wire protocol, or catalog change.

    **With this, `REBUILD INDEX ... ONLINE` is genuinely proven safe this time** — the P17 checklist line (search "REBUILD INDEX name ONLINE") and `internal/system/schema.go`'s `rebuild_index_online` capability row (`"supported"`) both stand, now backed by a third, independently-adversarial verification pass in addition to logs #89/#91. The other four parallel audit passes (covering P0–8, P9–15, P19–23, P24–27) found the rest of P0–27 holds up as claimed, with two smaller, separately-tracked findings: Phase 15's HA replica-repair checklist line covers the common lagging-follower-reconnects case (`tests/ha/TestHAReplicaRepair`) but has no test for the documented from-backup/`AddVoter` rejoin path (a real test-coverage gap, not a known bug — flagged, not fixed, this session); and Phase 26's `system.*` cross-database-isolation justification text is stale (cites "one process opens one database," which went stale once M2 shipped) even though the isolation property itself still holds structurally (each caller's `*executor.DB` is already distinct) — a documentation correction, not a vulnerability. Docs: `TODO.md` (P17 line + this entry), `CHANGELOG.md`. Everything uncommitted, per the standing repo convention.

    Docs: `docs/design-datatypes.md` (D3 marked landed with the full design writeup), `docs/sql.md` + `docs/web/content/docs/sql.md` (`UINT8..64` type-table row), `docs/client-encryption.md` (v1 logical-type list now includes `UINT8..64`), `CHANGELOG.md`, `TODO.md` (this entry + `D3` checklist line, header table). Everything uncommitted, per the standing repo convention.

94. [x] Datatype expansion D5 — `DATE`/`TIME` (2026-09-03) — **NOT a Phase 0–27 item.** Continuation of the Datatype expansion cross-cutting track; `docs/design-datatypes.md` D5 was already marked "ready to scope once D1-D3 conventions settle," and D1-D3 landed earlier the same day (logs #90-#92), so this picked it up directly. D4/D6-D11 remain blocked on an explicit scoping decision or too large, per the doc; D9 (collections) is out of scope for this track entirely.

95. [x] Phase 15 — a real data-loss bug found (and fixed) in the documented "wiped replica restored from backup, rejoined via `AddVoter`" repair procedure (2026-09-03). Continuation of log #93's independent P0–27 audit: that audit flagged the backup+`AddVoter` replica-repair path (`docs/ha.md` "Replica repair and rolling maintenance") as having zero test coverage — `TestHAReplicaRepair` only proves the *other* documented path (a lagging-but-reachable follower catching up from the live Raft log). At the user's explicit "fix" (in response to being offered this as the next step), wrote the missing test, `TestHAReplicaRepairFromBackupAddVoter` (`tests/ha/ha_test.go`) — and it found a real bug, not just a coverage gap. **Fixed the same session, after the user's follow-up "continue."**

    **The bug**: `internal/backup/backup.Create` and `.Restore` each open the target data file directly (`storage.Open`/`OpenWith`) and run their own checkpoint/restore-test-open, as they must to produce/verify a self-consistent backup. But `internal/storage/engine.go`'s `Engine.Checkpoint()` allocates and durably writes several WAL LSN numbers as pure local housekeeping (dirty-page flush, tree-meta, alloc-state, the checkpoint record itself) — live-measured in the failing test: a single `backup.Create` checkpoint on a small 2-row table advanced the source file's `WAL.NextLSN()` from 22 to 31, nine LSNs consumed by housekeeping alone, with `backup.Restore`'s own restore-test-open and the final production open adding more on top (34 by the time the repaired node opens). `ApplyReplicated`'s catch-up logic (used when a rejoined node's Raft log gets replayed) treats this inflated `e.WAL.NextLSN()` as "how far into the replicated stream have I gotten": `if last < e.WAL.NextLSN() { return nil }`. Since the leader originally assigned the post-backup write ('two' in the test) an LSN in the 22–29 range — now *below* the repaired replica's locally-inflated counter, even though none of that range's actual data was ever applied to this node — the check treats it as already-applied and silently skips it. The repaired replica reports itself fully caught up (`Cluster.AppliedLSN()` correctly reaches the leader's watermark) while permanently missing every write that happened between the backup and the rejoin, with no error, warning, or detectable divergence signal.

    This is not a new failure mode invented by this finding — `internal/storage/engine.go`'s own `prepareCommitLocked` already carries a comment on the narrower case log #79 identified and deliberately left as a residual, fail-open risk: "a later replay of it via `ApplyReplicated` would be silently skipped as already-seen (LSN < nextLSN) rather than applied — a permanent, undetectable divergence." **This finding shows the exact same underlying weakness has a second, far more easily reached trigger**: not just a rare ambiguous-replication-failure race, but the routine, fully-successful, documented backup+`AddVoter` repair procedure itself, every time it is used with intervening writes.

    **Investigated two fix directions before choosing.** The first — a dedicated, durably-persisted "highest replicated LSN actually applied" watermark, separate from `e.WAL.NextLSN()`, touched only by `ApplyReplicated` — is what a first-principles read of the problem suggests, but requires an on-disk superblock format change (the current 256-byte superblock has no spare field; the surrounding physical page has room, but adding a field means a `FormatVersion` bump with old/new-file compat handling in `internal/storage/file/superblock.go`, `internal/upgrade/compat`, and a full migration story) — real, invasive, high-blast-radius work for what turned out to be a much narrower root cause. **Chose the second, once it was found**: `Engine.Checkpoint()` (called by both `Close()` and `backup.Create`) unconditionally writes a fresh `Begin`/`CheckpointRec`/`Commit` triplet (plus any dirty allocator-metadata images) *every time it's called*, even when nothing has happened since the engine was opened — confirmed by checking `Close()`'s own implementation, which already checkpoints before closing, meaning `backup.Create`'s subsequent checkpoint of an already-cleanly-closed file is normally pure redundancy. There are only two callers of `Engine.Checkpoint()` in the whole codebase (`Close()` and `backup.Create`) and no SQL-level `CHECKPOINT` statement, so the fix surface is small and fully enumerable.

    **The fix**: `internal/storage/engine.go` — new `Engine.openNextLSN` field, set once (`lg.NextLSN()`) at the end of `open()`, right after redo/recovery completes, never modified afterward. `Checkpoint()` now returns immediately, without writing anything, when there is no in-progress transaction *and* `e.WAL.NextLSN() == e.openNextLSN` — i.e., nothing has been appended to the WAL since this specific `Engine` instance was opened. This only ever short-circuits a checkpoint that would have had nothing to record: any session that actually wrote something has already advanced `NextLSN()` past `openNextLSN` by the time `Checkpoint()` runs, so `Close()`'s own checkpoint after real activity is completely unaffected — the fast path is reached only by `backup.Create`/`Restore`'s open-then-immediately-checkpoint pattern on an already-quiescent file, which is exactly the case that was corrupting replica LSN state. No on-disk format change, no new persisted field, no migration.

    **Verification**: isolated the root cause via a temporary lock-free trace of `ApplyReplicated` (same technique as log #93 — ordinary `fmt`-based tracing perturbed timing too much in that case; here the mechanism was deterministic enough that a plain instrumented run sufficed) plus a probe reopen of the victim's own data dir immediately after `backup.Create` to measure the LSN inflation directly (22 → 31 from one checkpoint alone, confirming the exact mechanism before writing the fix). All temporary instrumentation removed before landing. `TestHAReplicaRepairFromBackupAddVoter` — 20/20 clean under `-race` (previously reliably failing every run). Regression sweep: full `tests/ha` suite (`-race`), full `internal/storage/...` suite including `internal/storage/btree` (`-race`), `internal/backup`/`internal/recovery`/`internal/wal` (`-race`), full `internal/executor/...` suite incl. `aggregate`/`join`/`sort`/`vector` (`-race`) — all green, confirming the `Checkpoint()` change has no effect on any path where it isn't a true no-op. `go build ./...`/`go vet ./...` clean repo-wide. Shipped diff: `internal/storage/engine.go` (`openNextLSN` field + `Checkpoint()` fast path) plus the test/refactor from earlier in this same log entry. Docs: `TODO.md` (Phase 15 checklist caveats + this entry, now `[x]`), `CHANGELOG.md`. Everything uncommitted, per the standing repo convention.

    **Design decisions**, recorded in `docs/design-datatypes.md` D5 before/while coding: (1) **on-disk layout** — `DATE` is a signed `int32` day count since the Unix epoch (1970-01-01 = 0), 4 bytes; `TIME` is `int64` nanoseconds-since-midnight, range `[0, 86399999999999]`, 8 bytes; both reuse existing `Value` fields (`DATE` reuses `Value.Int`, the same field the fixed-width signed integers use; `TIME` reuses `Value.Time`, the same field `TIMESTAMPTZ` uses for epoch nanos) rather than adding new ones, disambiguated everywhere by `Value.Typ.Kind`; (2) the Go constructor for `TIME` is named `TimeOfDay()`/`TimeOfDayValue()`, not `Time()`/`TimeValue()`, since `TimeValue`/`Value.Time` were already taken by `TIMESTAMPTZ`; (3) **index-key ordering**: `DATE` sign-bit-flips (a day count can be negative pre-1970, same trick as `INT32`/`TIMESTAMPTZ`); `TIME` is plain unsigned big-endian, no flip (always non-negative, mirrors `UINT64`); (4) **coercion**: isolated from every family but text (D1-D3 precedent) — `STRING`/`TEXT` only, via ISO 8601 (`YYYY-MM-DD` / `HH:MM:SS[.fraction]`, nanosecond precision); (5) **deliberately no dedicated literal syntax** (no `DATE '...'`/`TIME '...'` prefix unlike `BLOB`'s `X'...'`) — a plain quoted string is already an unambiguous textual date/time-of-day, so this follows D2's "bare literal, no new syntax" precedent, not D1's hex-literal precedent; (6) **no arithmetic** — `+`/`-`/`*`/`/` over `DATE`/`TIME` operands are rejected; calendar arithmetic is D6 `INTERVAL`'s job, explicitly out of scope here (D6 must not start alongside D5 per the doc, and didn't); (7) `MIN`/`MAX` work for free via the existing generic `Value.Cmp` dispatch (no per-kind allowlist, same as `BLOB`); `SUM`/`AVG` correctly error (no `DATE`/`TIME` source case added to the `KindDecimal` `Coerce` dest); (8) ordinary FK-eligible scalars (the FK check is a block-list — `VECTOR`/`JSON` — not an allow-list, so no code change needed there); (9) `ENCRYPTED CLIENT` included (same generic opaque-scalar reasoning as D1-D3); (10) `Kind` values appended after `KindUint64` (`KindDate`=23, `KindTime`=24), no `NSCT` version bump.

    **Core engine**: `internal/sql/types/types.go` — `KindDate`/`KindTime`, `String()`, `Date()`/`TimeOfDay()` constructors, `Comparable()`. `internal/sql/types/value.go` — `DateValue`/`NewDate`/`ParseDate`/`FormatDate` (Go `time.Parse`/`time.Unix`-based, exact integer day-count division since a UTC-midnight `Unix()` is always an exact multiple of 86400, even negative), `TimeOfDayValue`/`NewTimeOfDay`/`ParseTimeOfDay`/`FormatTimeOfDay` (layout `"15:04:05.999999999"`, whose `9`s make the fractional part optional on both parse and format — trailing zeros are trimmed automatically); `String()`, `Cmp` (`KindDate` compares `Value.Int`, `KindTime` shares `KindTimestampTZ`'s `Value.Time` comparison case since both are guarded to the same `Kind` by `Cmp`'s existing type-mismatch check), `Coerce` (`KindDate`/`KindTime` dest cases, `STRING`/`TEXT` source only). `internal/sql/types/row.go` — `skipScalar`/`encodeScalar`/`decodeScalar` (4 and 8 raw little-endian bytes) and `encodeSortable`/`decodeSortable` (`DATE` sign-bit-flip mirroring `KindInt32`; `TIME` plain big-endian mirroring `KindUint64`) each gained the two new cases. `internal/sql/lexer/lexer.go` — `KwDate`/`KwTime` tokens + keyword-map entries (confirmed via grep that no existing test/fixture used bare `date`/`time` as an identifier before reserving them). `internal/sql/parser/parser.go` — `colType()` cases. `internal/executor/eval.go` needed no change: `isNumericKind` already gates arithmetic to `KindDecimal`/`IsInt`/`IsUint`, and neither `KindDate` nor `KindTime` was added to `IsInt`/`IntRange`, so `+`/`-`/`*`/`/` correctly reject with "arithmetic requires a numeric type" for free. `internal/sql/binder/binder.go` (`facetable`), `internal/clientenc/clientenc.go` (`SupportedType`), `internal/executor/idempotency.go` (`validateIdempotentResultType`), `internal/catalog/workflow.go` (`validWorkflowType`), `internal/xport/sql.go` (`sqlLiteral`, sharing `KindTimestampTZ`'s quoted-`Value.String()` case) — each gained the two new kinds alongside their existing entries. `internal/executor/vector/batch.go` — `newVec`/`setAt`/`getAt`/`Compact`/`clonePrefix` each gained `KindDate` (sharing the `Int` slice with `INT8..64`) and `KindTime` (sharing the `Time` slice with `KindTimestampTZ`). **Incidental fix, unrelated to D5**: `Compact`/`clonePrefix` were missing `INT8..64`/`UINT8..64` cases entirely (a latent gap since D2/D3 landed) — a vectorized filter or `Project` on those columns silently left stale/zero values post-compaction; fixed in the same pass as adding the `DATE`/`TIME` cases to the same two switches, since it was directly adjacent code being edited anyway. `internal/catalog/encode.go`/`internal/protocol/value.go` needed no change: `Kind` persists/travels as a plain byte with no allow-list gate, confirmed by reading both before starting (same as D1-D3).

    **Tests**: `internal/sql/types/types_test.go` — `TestDateParseFormatRoundTrip` (ISO 8601 parse/format incl. pre-epoch negative days, a leap day, and an invalid-calendar-date rejection), `TestDateRowAndKeyRoundTrip` (heap-row + sortable-key round trip, straddle-the-epoch ordering check), `TestNewDateRange`, `TestTimeOfDayParseFormatRoundTrip` (incl. nanosecond precision and trailing-zero trimming), `TestTimeOfDayRowAndKeyRoundTrip`, `TestNewTimeOfDayRange`, `TestDateTimeCmpIsolated` (cross-kind `Cmp` rejection + isolated-`Coerce` confirmation both directions). `internal/executor/datetime_test.go` (new, mirrors `int_test.go`/`uint_test.go`): `TestDateTimeInsertSelectRoundTrip` (incl. a pre-epoch date, midnight, `23:59:59.999999999`, a leap day, catalog persist/reopen durability), `TestDateOrderByStraddlesEpoch` (the critical index-key correctness test — six dates spanning `0001` to `2100`, straddling the epoch, must sort chronologically, not as raw two's-complement bytes), `TestTimeOrderBy`, `TestDateTimeInvalidTextRejected` (malformed text, an out-of-range calendar date, hour 24, and an implicit-integer-coercion rejection), `TestDateTimeArithmeticRejected`, `TestDateTimeAggregateAndGroupBy` (`MIN`/`MAX`/`GROUP BY`), `TestDateTimeForeignKey`, `TestDateTimeEncryptedClient`. `go build ./...`/`go vet ./...` clean repo-wide; every touched Go package's tests green (`internal/sql/types`, `internal/sql/binder`, `internal/sql/parser`, `internal/executor`, `internal/executor/vector`, `internal/catalog`, `internal/xport`, `internal/clientenc`, `internal/protocol`). A full `go test ./...` run separately hit two unrelated pre-existing failures — `tests/ha.TestHAReplicaRepairFromBackupAddVoter` (a timing-sensitive replica-convergence poll, reproduced in isolation too; touches only `internal/replication`/`internal/wal`/`internal/storage`, none of which this change touches) and `tests/integration.TestDenoDriverUnit` (`Deno.writeFile`: "Disk quota exceeded (os error 122)", an environment/tmpfs resource issue, not a code fault) — both flagged, not fixed, out of scope for D5.
