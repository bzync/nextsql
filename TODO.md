# NextSQL Development Tracker

Living end-to-end checklist. Source of requirements: `PROJECT.md`. This file is authoritative for implementation status, sequencing, dependencies, and phase gates.

Update this file when a box is completed, blocked, or split. Do not mark a phase done until its exit gate is checked.

| Field            | Value                                                                                                                                                             |
| ---------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Current phase    | **P16 Correctness / SLO closure**                                                                                                                                 |
| Active increment | P21 native table partitioning development while the remaining P16 100M B+Tree measurement is paused by explicit direction; corrected 1M HNSW is green. P19 final repository-wide functional gate remains open; P17/P18/P20 implementable scope is closed. |
| Status           | P16 is `OPEN`: corrected HNSW v10 passed with p95 8.061 ms, recall@10 1.000, and recall@100 0.998 under the 131,072-page no-steal pool. After v9 exited 137 without retained terminal evidence, bounded B+Tree v10 was started and then stopped before its first checkpoint on 2026-08-26 by explicit direction; it receives no gate credit. P19 workflow, trigger, schedule, and bounded durable task implementation is complete but its final clean repository-wide functional gate remains open. P20 CDC is complete: committed versioned changes, native table/tenant SQL/NSQL streaming, safe operation filtering, resume/history expiry, WAL-retention pins, bounded backpressure, runtime RBAC/audit/cancellation, prepared-driver support, diagnostics, bounded bulk capture, opt-in bounded row images, restart resume, and three-voter leader-failover resume are implemented and verified. |
| Last updated     | 2026-08-26                                                                                                                                                      |
| Priority order   | Correctness → durability → security → integrity → availability → latency → throughput → efficiency → DX → features                                                |

**Progress:** Phases 0–15 complete. P16's remaining B+Tree measurement is open and explicitly paused. P17, P18, and P20 implementable scope is closed. P19 implementation is complete with its final cross-cutting functional gate open. P21 has a tested bounded single-column RANGE/HASH/LIST/TENANT slice; lifecycle breadth remains open.

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
- [ ] P16 Correctness / SLO closure
- [x] P17 Schema lifecycle + storage maintenance
- [x] P18 SQL completeness
- [ ] P19 WORKFLOW / TRIGGER / SCHEDULE / TASK
- [x] P20 CDC / change streams
- [ ] P21 Native table partitioning — bounded single-column RANGE/HASH/LIST/TENANT slice implemented; full phase remains open
- [ ] P22 Follower reads / read scaling
- [ ] P23 Vector Engine 2.0
- [ ] P24 Full-text Search 2.0
- [ ] P25 Security 2.0
- [ ] P26 System catalog / introspection 2.0
- [ ] P27 Operational maturity + workload governance
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
- [x] mTLS / service identities / short-lived credentials / external IdP (as follow-ons)
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
- [x] Replica repair
- [x] Rolling maintenance
- [x] Reject writes if a leader cannot be safely identified
- [x] No split brain
- [x] Synchronous quorum commit: do not ACK until durability policy is met
- [x] RPO = 0 for acknowledged quorum-synchronous commits under covered failures
- [x] Failure detection in seconds
- [x] Leader election target `< 3 s`
- [x] Service recovery target `< 5 s`
- [x] Kill-leader integration test
- [x] Partition / quorum-loss test (writes rejected)
- [x] Replica-repair test
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
- [x] Cross-tenant leakage tolerance = 0 — `SET TENANT` / `RESET TENANT`; implicit `tenant_id` predicate; ACL fail-closed unless ADMIN (`docs/security.md`)
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
- [ ] Randomized 100M insert/delete B+Tree invariant run (`./scripts/run-btree-soak.sh`) — v9 was killed with exit 137 on 2026-08-25 after running for ~11h; its retained log is unavailable, so no terminal verification can be credited. v4 reached 24M operations (`live=11,435,641`) and v8 reached 44M clean operations (`live=17,557,686`). Bounded v10 was stopped before its first checkpoint on 2026-08-26 by explicit direction and receives no gate credit; the measurement is paused.
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

1\. HNSW is green: v10 published p95 **8.061 ms** with recall. The 100M B+Tree measurement is paused by explicit direction after bounded v10 was stopped before its first checkpoint; require a future terminal structural check, full scan count, and randomized-keyspace verification before marking P16 complete.

2\. Continue P19 WORKFLOW / TRIGGER / SCHEDULE / TASK verification in parallel without competing with the P16 soak for unbounded memory or I/O. Remaining P18 partition-wise aggregation/join hooks wait on P21 physical partitioning. P17 `REBUILD INDEX … ONLINE` stays deferred behind proven concurrent-write handling.

3\. 10M DELETE variance explained: same-process trees use the maintained exact
live-row cache before the heap swap; reopened trees reconstruct the affected-row
count with an O(rows) leaf scan. This accounts for the **25 ms / 1.57 s** warm
results versus the **24 s** cold-open run; methodology is published in
`docs/ops.md`.

# Forward Product Roadmap — Phase 17+

> **Rule:** Everything below this point is planned unless explicitly checked after implementation, tests, documentation, and the phase exit gate pass. Design notes, hooks, and planned follow-ons do **not** count as implemented.

## Roadmap summary

```text
[x] P17 Schema lifecycle + storage maintenance
[x] P18 SQL completeness
[ ] P19 WORKFLOW / TRIGGER / SCHEDULE / TASK
[x] P20 CDC / change streams
[ ] P21 Native table partitioning
[ ] P22 Follower reads / read scaling
[ ] P23 Vector Engine 2.0
[ ] P24 Full-text Search 2.0
[ ] P25 Security 2.0
[ ] P26 System catalog / introspection 2.0
[ ] P27 Operational maturity + workload governance
[ ] P28 Professional Installer + NextSQL Manager
[ ] P29 NextSQL Studio
[ ] P30 NextSQL Intelligence + built-in RAG
```

Sequencing remains:

```text
Correctness → durability → security → integrity → availability → latency → throughput → efficiency → DX → features
```

Do not begin a later phase in a way that destabilizes an earlier open gate. UI foundations may be developed in parallel once underlying interfaces are stable, but P16 correctness/SLO closure remains the immediate release gate.

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
- [x] Add blocking rebuild first if online correctness is not proven — `ONLINE` is rejected
- [ ] Design and implement `REBUILD INDEX name ONLINE` only after proven-safe concurrent-write handling — deferred follow-on; blocking `REBUILD INDEX` is the shipped path (`ONLINE` is rejected)
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
- [x] Set operations: `UNION ALL` — left-associated two-query AST/plan, per-arm optimization, duplicate preservation, left-column naming, RBAC/tenant traversal, and column-count validation
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
- [x] Tenant isolation preserved through every subquery form

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
- [ ] Partition-wise aggregation hooks where physical partitioning exists — wait on P21
- [ ] Partition-wise joins hooks where physical partitioning exists — wait on P21

### Phase 18 exit gate

- [x] CTEs, subqueries, HAVING, CASE, set operations, windows, UPSERT, and RETURNING pass parser/binder/planner/executor tests
- [x] New SQL is correct across NULL, transactions, restart/recovery, prepared statements, RBAC, and tenants — `TestP18SQLGate*` plus Go driver `TestDriverP18SQLOverTLS`; inner derived `DISTINCT`/`ORDER BY`/`LIMIT` execute in `collectPlan`
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
- [x] Tenant semantics documented — every body statement inherits and applies the invoker's bound tenant
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
- [ ] Cron syntax deferred until core scheduler is proven

## TASK runtime

- [x] Durable task ID — deterministic `s/<schedule-id>/<due-ns>` for scheduled boundaries
- [x] States: `PENDING/RUNNING/SUCCEEDED/FAILED/CANCELLED/RETRYING/FINAL_FAILED`
- [x] Attempt count — incremented atomically with each durable lease claim
- [x] Error metadata — bounded/redacted code and message; no workflow arguments or row values
- [x] Trigger metadata — versioned descriptor fields are defined; v1 row triggers remain synchronous and therefore do not create TASK rows
- [x] Tenant identity — stored at schedule creation and re-applied/rechecked for every attempt
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
- [x] Triggers are bounded and cycle-safe — parser/catalog fuzz, atomic failure, crash recovery, backup/restore, LSN PITR, three-node Raft failover, adversarial RBAC/tenant, audit redaction, TLS prepared-driver, targeted race, and full functional tests pass (2026-08-24)
- [x] Scheduled execution survives restart/failover — expired-lease crash/reopen recovery plus three-node dispatch/execute/state replication and post-leader-kill next-boundary dispatch pass (2026-08-24)
- [x] Task runtime is observable, cancellable, and resource-bounded — SQL/TLS `SHOW TASKS` and `CANCEL TASK`, fixed workers, bounded indexes/scans, retries, retention, and failure-state tests pass (2026-08-24)
- [x] RBAC/tenant isolation verified adversarially — owner pagination/cancellation isolation, tenant-binding checks, and execution-time privilege revocation fail closed (2026-08-24)
- [x] `docs/workflows.md` complete for the implemented native v1 surface
- [ ] Clean repository-wide functional suite — compile-only, targeted functional/race/fuzz, PITR, three-node Raft, and TLS-driver gates pass. A 2026-08-25 durability-enabled serial rerun on the repository volume passed backup/PITR, executor, replication, security/TLS, HA, and integration; only `internal/storage/btree` exceeded Go's 10-minute package timeout during `TestSequentialDeleteHeight3Empty` while blocked in `fdatasync`. Keep this gate open until one repository-wide invocation exits cleanly.

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
- [x] Tenant-aware subscriptions — effective tenant is bound server-side and filtered fail closed
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
- [x] Tenant/RBAC rules cannot be bypassed — unbound tenant streams fail closed and runtime revoke tests terminate open streams
- [x] Failover/restart CDC tests pass — storage restart resume and three-voter Raft leader-failover resume preserve commit-token ordering
- [x] `docs/cdc.md` complete for the native v1 surface, image policy, security, retention, operations, and recovery behavior

---

# Phase 21 — Native table partitioning

Convert existing pruning infrastructure into a user-facing physical partitioning feature.

## Partition types

- [x] `PARTITION BY RANGE` — bounded single-column slice
- [x] `PARTITION BY HASH` — bounded single-column slice with deterministic NSCT v4 routing
- [x] `PARTITION BY LIST` — bounded single-column typed-value slice
- [x] `PARTITION BY TENANT(tenant_id)` — bounded single-column slice
- [x] Partition catalog metadata — versioned NSCT v4 descriptors are wired for the shipped RANGE/HASH/LIST/TENANT slice
  - [x] Versioned bounded `NSCT` v4 descriptor foundation — stable partition IDs/names, RANGE/HASH/LIST/TENANT rules, heap/vector/index roots, fail-closed validation, legacy v1-v3 reads, truncation tests, decoder fuzz seed, and storage/partitioning docs
  - [x] Wire descriptors into native DDL, physical tree open/ownership/reclamation, and routing for the bounded RANGE/HASH/LIST/TENANT slice; lifecycle breadth remains open
- [ ] Create/attach/detach/drop partition semantics
- [x] Partition pruning from predicates — RANGE/HASH/LIST/TENANT candidate IDs are carried into physical scans and visible in `EXPLAIN`; mixed unanalyzable OR branches retain all partitions
- [ ] Partition-aware indexes
- [ ] Partition-aware statistics
- [ ] Partition-aware backup/restore
- [ ] Partition-aware maintenance
- [x] Cross-partition constraints defined — every primary key must include every partition column; cross-partition foreign keys and secondary UNIQUE indexes remain rejected
- [x] Cross-partition unique/FK semantics documented — primary keys include the partition key; partitioned-table FKs and secondary indexes remain fail-closed in the shipped slice

## Tenant partitioning

- [x] Native tenant routing into tenant partitions — bounded single-column TENANT slice
- [ ] Tenant-local B+Tree indexes
- [ ] Tenant-local JSON indexes
- [ ] Tenant-local full-text indexes where beneficial
- [ ] Tenant-local HNSW where beneficial
- [ ] Future tenant movement/shard-placement hooks only after local correctness

### Phase 21 exit gate

- [x] RANGE/HASH/LIST/TENANT semantics documented and tested for shipped subset — bounded single-column forms are shipped
- [x] Pruning is visible in `EXPLAIN`
- [x] Transactions remain correct across partitions — RANGE/HASH/LIST/TENANT routed writes, cross-partition PK updates, pruning, and restart recovery tested
- [x] Tenant partitioning cannot leak cross-tenant rows
- [ ] No automatic distributed sharding yet unless separately gated
- [x] `docs/partitioning.md` complete for the bounded shipped slice and explicit remaining gate

---

# Phase 22 — Follower reads / read scaling

Keep single-leader writes. Multi-primary remains deferred.

## Read consistency

- [ ] Define `STRONG` read semantics
- [ ] Strong reads use leader or valid Raft read barrier
- [ ] Define stale/eventual follower-read mode
- [ ] Optional bounded-staleness mode
- [ ] `MAX STALENESS` semantics if implemented
- [ ] Driver routing metadata/versioning
- [ ] Read-after-write behavior documented
- [ ] Transaction restrictions on follower reads
- [ ] Tenant/RBAC behavior unchanged

## Routing

- [ ] Client/server routing mechanism designed
- [ ] Read-only statements eligible for follower routing
- [ ] Writes always route/fail toward leader as documented
- [ ] Replica lag exposed
- [ ] Follower health considered before routing
- [ ] Fallback behavior documented

### Phase 22 exit gate

- [ ] Strong reads satisfy documented linearizability/consistency guarantee
- [ ] Stale reads are never mislabeled strong
- [ ] Failover does not violate session guarantees beyond documented mode
- [ ] Driver compatibility tests pass
- [ ] Read-scaling benchmark published

---

# Phase 23 — Vector Engine 2.0

Do this only after the P16 1M-HNSW baseline exists.

## Types / storage

- [ ] `VECTOR<F16,N>`
- [ ] `VECTOR<I8,N>`
- [ ] `BITVECTOR<N>` if justified
- [ ] Versioned storage encoding
- [ ] Conversion/cast rules
- [ ] Quantized storage metrics

## ANN

- [ ] Quantized HNSW option
- [ ] IVF
- [ ] IVF-PQ
- [ ] Build/rebuild lifecycle integration
- [ ] Transaction/delete semantics
- [ ] Encryption of every ANN structure
- [ ] Crash/recovery/Raft integration

## Sparse retrieval

- [ ] Design sparse-vector type/format
- [ ] Prototype sparse retrieval
- [ ] Evaluate dense + sparse + BM25 fusion
- [ ] Finalize only after measurable benefit

## Measurement

Every ANN configuration must report:

- [ ] recall@10
- [ ] recall@100
- [ ] p50/p95/p99
- [ ] QPS
- [ ] RAM
- [ ] index size
- [ ] build time
- [ ] database size

Never lower recall silently to improve latency.

### Phase 23 exit gate

- [ ] At least one memory-efficient vector representation is production-gated
- [ ] New ANN path has recall/latency/size measurements
- [ ] No durability/encryption regression
- [ ] Existing F32/HNSW behavior remains compatible

---

# Phase 24 — Full-text Search 2.0

- [ ] Stemming
- [ ] Stop-word dictionaries
- [ ] Versioned language analyzers
- [ ] Synonyms
- [ ] Prefix search
- [ ] Fuzzy matching
- [ ] Typo tolerance
- [ ] Highlight/snippet generation
- [ ] Multi-field search
- [ ] Field weighting
- [ ] Faceting/aggregation support where architecturally appropriate
- [ ] Analyzer/index options in DDL
- [ ] Query-expansion CPU/memory caps
- [ ] Transaction/WAL/recovery support for analyzer metadata
- [ ] Deterministic analyzer behavior across replicas

### Phase 24 exit gate

- [ ] Current BM25/phrase behavior remains compatible
- [ ] Language/fuzzy features have bounded adversarial-query behavior
- [ ] Search quality fixtures expanded
- [ ] Encryption/recovery tests pass

---

# Phase 25 — Security 2.0

Audit every security checklist item and distinguish `designed`, `implemented`, `tested`, and `production-gated`.

## mTLS and service identity

- [ ] Actual mTLS server implementation
- [ ] Client certificate validation
- [ ] Service identity mapping
- [ ] Certificate rotation
- [ ] Certificate revocation handling
- [ ] Audit authentication identity source

## Short-lived credentials

- [ ] Signed short-lived NextSQL credential/token format
- [ ] Expiration
- [ ] Audience/database scope
- [ ] Role scope
- [ ] Tenant scope
- [ ] Signing-key rotation
- [ ] Revocation
- [ ] Audit

## External IdP

- [ ] OIDC design
- [ ] OIDC implementation
- [ ] Identity-to-NextSQL principal mapping
- [ ] External auth does not bypass NextSQL RBAC
- [ ] Group/role mapping policy

## Field-level client encryption

- [ ] Implement `ENCRYPTED CLIENT`
- [ ] Official-driver encryption/decryption support
- [ ] Strong client-encrypted fields remain opaque to server
- [ ] Define searchable-encryption leakage if any search modes are added
- [ ] Key rotation
- [ ] Revocation
- [ ] Wrong-key/tamper behavior
- [ ] Backup/restore/PITR tests
- [ ] Replication/failover tests

## Password hashing

- [ ] Evaluate Argon2id migration
- [ ] Versioned password-hash records
- [ ] Backward compatibility with existing PBKDF2 records
- [ ] Transparent rehash on successful login where chosen
- [ ] Authentication DoS benchmarks

## Audit hardening

- [ ] Tamper-evident/signed audit chain design
- [ ] Audit verification tooling if implemented

### Phase 25 exit gate

- [ ] TODO no longer marks follow-on design hooks as implemented functionality
- [ ] mTLS/short-lived auth/IdP status is truthful
- [ ] `ENCRYPTED CLIENT` is either fully production-gated or explicitly remains designed-only
- [ ] Security review updated

---

# Phase 26 — System catalog / introspection 2.0

The current increment ships the virtual schema core (capabilities, tables,
columns, indexes, storage, replication/raft, workflows, tasks, partitions,
table/index stats) with RBAC filtering, redaction, and stable schema columns.
Live query/session/transaction/lock/CDC rows and SHOW aliases remain open.

Create a canonical machine-queryable virtual `system` schema.

- [x] `system.capabilities`
- [ ] `system.active_queries`
- [ ] `system.sessions`
- [ ] `system.transactions`
- [ ] `system.locks`
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
- [ ] `system.change_streams`
- [x] `system.partitions`
- [x] RBAC for system tables
- [x] Sensitive fields redacted/omitted
- [x] Stable versioned columns for machine consumers

Optional convenience commands backed by the same source of truth:

- [ ] `SHOW DATABASES`
- [ ] `SHOW TABLES`
- [ ] `SHOW INDEXES`
- [ ] `SHOW CONNECTIONS`
- [ ] `SHOW QUERIES`
- [ ] `SHOW TRANSACTIONS`
- [ ] `SHOW LOCKS`
- [ ] `SHOW CLUSTER`
- [ ] `SHOW STORAGE`

### Phase 26 exit gate

- [ ] Studio/Manager can operate from official system interfaces without reading internal files
- [ ] System schema obeys RBAC and tenant visibility rules
- [ ] Capability registry is authoritative for version-aware clients

---

# Phase 27 — Operational maturity + workload governance

## Server lifecycle

- [ ] Graceful drain
- [ ] Controlled shutdown
- [ ] Connection draining
- [ ] Leader transfer
- [ ] Maintenance mode
- [ ] Rolling upgrade workflow
- [ ] Online format/catalog migration strategy where safe
- [ ] Backup retention management
- [ ] WAL retention management
- [ ] Replica-lag management
- [ ] Disk watermark policies
- [ ] Capacity warnings

## Session controls

Audit existing controls first; add only missing capabilities:

- [ ] Max global connections
- [ ] Per-user connection limit
- [ ] Per-tenant connection limit
- [ ] Idle session timeout
- [ ] Idle transaction timeout
- [ ] Statement timeout
- [ ] Transaction timeout
- [ ] Lock timeout
- [ ] Queue timeout

## Resource groups

- [ ] Design `RESOURCE GROUP`
- [ ] Workload max concurrency
- [ ] Workload memory budget
- [ ] Workload CPU/worker budget
- [ ] Priority
- [ ] Integrate API/analytics/workflow/maintenance/backup classes with one scheduler
- [ ] No independent unbounded pools

## Operational CLI

- [ ] `nextsql cluster drain <node>` or equivalent
- [ ] `nextsql cluster transfer-leader <node>` or equivalent
- [ ] Machine-readable operation status

### Phase 27 exit gate

- [ ] Planned maintenance can drain without unnecessary transaction loss
- [ ] Resource groups cannot bypass global safety limits
- [ ] Rolling maintenance/upgrade procedure documented and tested

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

Core rule: **Do not build a generic SQL client with a NextSQL logo.** Studio must understand NextSQL-native SQL, JSON, FTS, vectors, hybrid search, geo, tenants, workflows, migrations, and query plans.

## Core architecture

- [ ] Studio communicates through official SDK/NSQL
- [ ] Capability negotiation with server
- [ ] Studio never reads pages/WAL/catalog files directly
- [ ] Studio never requires server root unlock key
- [ ] Studio never bypasses RBAC/tenant isolation
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
- [ ] Tenant profile
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
- [ ] Tenant selector / reset
- [ ] Visible ADMIN-all-tenants warning
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
- [ ] Tenant selector
- [ ] User/role explorer
- [ ] Basic Full-text Explorer
- [ ] Basic Vector Explorer
- [ ] Basic Hybrid Explorer
- [ ] Production warnings
- [ ] Secure credential storage
- [ ] No root-key requirement
- [ ] RBAC/tenant tests pass
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
- [ ] `TenantContext`
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
- [ ] Tenant binding enforced on AI retrieval
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
- [ ] Visible current connection/database/tenant/server version
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
- [ ] Tenant column
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
- [ ] Retriever remains transactional/RBAC/tenant-aware

### Phase 30 evaluation / security tests

- [ ] Official docs QA dataset across SQL/transactions/JSON/FTS/vector/hybrid/geo/security/backup/HA/migrations
- [ ] Retrieval Recall@K
- [ ] MRR
- [ ] Citation correctness
- [ ] Version mismatch tests
- [ ] Admin vs read-only user tests
- [ ] Restricted schema/table tests
- [ ] Tenant-bound tests
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
- [ ] RBAC/tenant enforcement
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
- [ ] Tenant-isolation tests
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

Cross-tenant leakage tolerance remains **0**.

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

## Immediate — P16 only

1. [ ] Run `./scripts/run-btree-soak.sh` randomized insert/delete invariant soak on labeled hardware — **OPEN/PAUSED** by explicit direction; bounded v10 was stopped before its first checkpoint and receives no gate credit.
2. [x] Run `--slo-max-rows 100000000` and publish COUNT/GROUP BY/indexed lookup/range/join results — 2026-08-21 ext4; all corrected analytics targets met.
3. [x] Run corrected `--slo-vectors 1000000` and publish recall@10/@100, p50/p95/p99, QPS, RAM, DB size, and index size — v10 p50 **6.158 ms**, p95 **8.061 ms**, p99 **8.156 ms**, QPS **156**, recall@10 **1.000**, recall@100 **0.998**, heap **4.3 GiB**, DB **1.1 GiB**, HNSW **546.1 MiB**.
4. [x] Explain 10M DELETE heap-swap variance (24 s vs prior 25 ms / 1.57 s) and publish methodology — reopened trees have no process-local exact live-row cache, so affected-row counting scans the leaf chain before the same constant-time heap swap (`docs/ops.md`).
5. [ ] Do not mark P16 complete until the randomized 100M B+Tree invariant soak is green; the analytics and corrected HNSW gates are already green.

## After P16

6. [x] Start P17 with `DROP INDEX` + storage reclamation before feature-heavy work — P17 exit gate is green; `REBUILD INDEX … ONLINE` remains deferred.
7. [x] P18 SQL completeness implementable scope — CTEs, subqueries, windows, UPSERT/RETURNING, covering/partial/expression indexes, join reordering; partition-wise agg/join wait on P21.
8. [ ] Begin Installer/Studio UI framework research in parallel only after stable management/system interfaces are defined.
9. [ ] Keep every new phase checkbox truthful: code + tests + docs + gate, not design-only.
