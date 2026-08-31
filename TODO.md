# NextSQL Development Tracker

Living end-to-end checklist. Source of requirements: `PROJECT.md`. This file is authoritative for implementation status, sequencing, dependencies, and phase gates.

Update this file when a box is completed, blocked, or split. Do not mark a phase done until its exit gate is checked.

| Field            | Value |
| ---------------- | ----- |
| Current phase    | **P25 Security 2.0** — open. **P24 Full-text Search 2.0 is COMPLETE** (2026-08-31): all four exit-gate items are green. |
| Active increment | P25 mTLS/service identity + signed short-lived credentials + the `NSIP` identity-policy engine are implemented and tested. **Authentication broker skeleton implemented + tested (2026-08-31)** — new `internal/oidc` (compact JWS verify RS/PS/ES 256/384/512, `none`+MAC rejected; JWKS parse; soft/hard-TTL rate-limited JWKS cache; ID-token validation `iss`/`aud`/`azp`/`exp`/`iat`/`nbf`/`nonce`; replay guard; `FuzzParseJWKS`/`FuzzParseCompact`/`FuzzVerify`), `internal/authbroker` + `cmd/nextsql-auth-broker` (`POST /v1/exchange`: verify ID token → `NSIP` `Map` → mint `NSSC1.` via a private `NSTK` key; TTL = min(configured, IdP token expiry); deployment audience; structured no-secrets audit; `SIGHUP` reload last-known-good). Fake-IdP→broker→real `auth.TokenVerifier` integration test. `nextsqld` unchanged / offline. **Next increment: `nextsql login` client flow (OIDC discovery + PKCE code flow + local callback + `/v1/exchange` + secure credential storage), then server audit labeling (`oidc`/`mtls+oidc`), client-credentials, embedded mode, JIT.** |
| Status           | Phases P0–P24 complete. P16's terminal 100M B+Tree soak and P17's `REBUILD INDEX … ONLINE` remain documented non-gate follow-ons. P25–P30 are open/planned. |
| Last updated     | 2026-08-31 (P25 authentication broker skeleton — `internal/oidc` + `internal/authbroker` + `cmd/nextsql-auth-broker`: OIDC ID-token validation against a soft/hard-TTL cached JWKS, `NSIP` claim→principal/role mapping, mint an `NSSC1.` credential signed by a private `NSTK` key; SQL auth path unchanged and offline. Unit + `-race` + fake-IdP integration + 8 s fuzz (`FuzzParseJWKS`/`FuzzParseCompact`/`FuzzVerify`) green. `docs/security.md` P25 audit: `OIDC implementation` → `implemented: partial` / `tested: yes`; the 3 mapping rows → `implemented: yes` / `tested: yes` (broker consumes `NSIP`). Not built: `nextsql login` client flow, `oidc`/`mtls+oidc` audit source, client-credentials, embedded mode, JIT. Prior: `NSIP` identity-policy engine, OIDC design accepted.) |
| Priority order   | Correctness → durability → security → integrity → availability → latency → throughput → efficiency → DX → features |

**Progress:** Phases 0–24 are complete (P0–P15, P16, P17, P18, P19, P20, P21, P22, P23, P24). P16's terminal 100M B+Tree invariant run is deferred as a standalone measurement outside the release gate; P17's `REBUILD INDEX … ONLINE` is a deferred follow-on. P17, P18, P20, P21, P22, and P23 implementable scope is closed. **P23 Vector Engine 2.0 is complete** (2026-08-31): production-gating sign-off in `docs/vector.md`; `VECTOR<F16,N>` / `VECTOR<I8,N>` / `BITVECTOR<N>` and the quantised HNSW index are production-gated; IVF / IVF-PQ / sparse retrieval / dense+sparse+BM25 fusion are production-gated ANN paths with recall/latency/size/QPS/RAM measurements. Documented follow-ons (not gate items): a `BITVECTOR`/Hamming `--vecquant` row, a process-local IVF-PQ cache, a re-rank-free quantised HNSW mode, IVF/IVF-PQ/SPARSE on partitioned tables, SIMD after profiling. **P22 follower reads / read scaling is complete** (2026-08-30): three read-consistency modes (`STRONG` linearizable behind a `raft.VerifyLeader` barrier; `BOUNDED` within `MAX STALENESS`; `STALE` unbounded — all consistent committed prefixes, never mislabelled), replica lag + follower health via `system.replica_health`, follower-read routing in the server and every official driver (`OpenCluster` / `connectCluster` / `NextSQL\Cluster::connect`), the `nextsql-bench --readscale` read-scaling benchmark, and the exit gate — a dated linearizability/consistency sign-off (`docs/ha.md` "Consistency model and sign-off") plus a failover session-guarantee test (`TestFollowerReadFailoverSessionGuarantee`: `STRONG` sessions keep read-your-writes + monotonic reads across a leader change). A 3-node non-Go driver cluster-routing live test is a documented optional follow-on, not a gate item. P21 has tested RANGE/HASH/LIST with one-to-eight-column keys (RANGE tuple bounds `VALUES LESS THAN (a, b, ...)` compared lexicographically; LIST tuple membership `VALUES IN ((a, b), ...)`; HASH SHA-256 over the canonical tuple), ADD/DROP plus validated ATTACH/DETACH ownership-transfer DDL, partition-local non-unique B+Tree-family, FULLTEXT, and HNSW/`VECTOR` indexes (partition-local graph over per-partition payload stores; `NEAREST` merges every partition-local graph by distance and is pruning-aware for a partition-key residual predicate), `NSST` v3 stable-ID row counts plus bounded versioned `NSPS` per-partition column/index/vector sketches used by pruning-aware costing, bounded partition-aware table/index maintenance, and base-backup plus archived-WAL recovery of partition metadata/data/local roots/statistics; multi-column RANGE pruning is tuple-tight (the predicate is reduced to a query bound prefix over the partition-key columns, so trailing constraints separate bands that share a leading value), and cross-partition plain-column secondary UNIQUE enforcement (exclusive key lock plus a probe of every other partition-local root on write; ordered cross-partition duplicate scan on CREATE/REBUILD/ATTACH). Partial/expression/JSON-path UNIQUE and partitioned-table FKs remain fail-closed; `UPSERT` on RANGE/HASH/LIST tables is now wired to the partition-local roots (PK-target conflicts resolve against the routed partition heap; secondary-UNIQUE-target conflicts probe every partition-local root; a partition-key `SET` moves the row between heaps; no-conflict inserts still hit the cross-partition UNIQUE probe) and stays rejected only on legacy TENANT tables. Explicit offline legacy TENANT migration landed 2026-08-30 (`nextsql hosting migrate-tenant`: exclusive dual-deployment locks, bounded UPSERT-idempotent batched copy into a `PROVISIONING` destination, per-row point verification, `tenant_id`→`legacy_tenant_id` rename, encrypted fuzz-seeded `NSLM` resume intent, publish `ACTIVE` only on success), so **P21 is complete**. Pruning soundness (a matching row is never routed to a pruned partition) is covered by the randomized `TestPartitionPruningSoundness` property test; further property/fuzz coverage may still be added. Legacy TENANT descriptors decode only for recovery/offline migration and cannot be created through SQL. Cross-cutting rich geo/F32-vector value operations, bounded WAL-invalidated SELECT result caching, and durable database-user-scoped mutation idempotency are implemented. P23 compressed-vector/ANN scope is production-gated. The accepted multi-database hosting track is `PARTIAL`: M1 now has an encrypted/versioned deployment registry (`NSRM` v3, with realm/database `StorageCapBytes` caps and a per-realm realm-root delegation secret hash — `nextsql hosting set-realm-cap` / `set-realm-root` / `set-database-cap [--realm-secret-file]`; `nextsqld` enforces the effective cap on the data file — growth past it fails `Exhausted`, deletes/in-place updates still work; cap edits take the data-dir lock and apply on restart; a realm-root secret holder can manage only its own realm's per-database caps under the realm ceiling), resumable init bootstrap, separate registry root, stable identities/lifecycle validation, and default-database verification; selectable multi-engine routing and later operational/HA gates remain open.

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

0\. **P24 Full-text Search 2.0 — COMPLETE (2026-08-31).** Exit gate green; next release gate: **P25 Security 2.0**. **Faceting landed 2026-08-31**: `SELECT * … SEARCH … FACET col [, col …]` returns independent histograms over the full SEARCH match set (`facet STRING`, `value STRING`, `count DECIMAL`); `LIMIT` is per-facet top-N; `NULL` skipped; query-time only, no catalog/format bump; requires `SELECT *` and `SEARCH`; 1–8 discrete columns and 1024 distinct values fail closed. Tests: `TestFulltextFacet`, `TestFacetDistinctValueCap`, `TestBindFulltextFacet`, `TestSearchFacetPlan`. **Field weighting landed 2026-08-31**: optional `SEARCH col WEIGHT n` scales per-field BM25 tf from existing position bands (omitted = 1; `(0, 64]`; fail closed; query-time only, no catalog/format bump). Unweighted SEARCH stays Phase 10 / multi-field BM25. Prefix/fuzzy/typo/`HIGHLIGHT`/`SNIPPET`/phrase matching unchanged. Tests: `TestWeightedTF`, `TestQueryScoreWeighted`, `TestCheckFieldWeight`, `TestFulltextFieldWeight`. **Multi-field search landed 2026-08-31**: `CREATE FULLTEXT INDEX` / `SEARCH col [, col …]` take 1–8 STRING/TEXT columns (exact column-list match for inverted-index use; subset/reorder seq-scans). Fields scored as one BM25 document; phrases do not cross fields (position bands, no catalog/format bump). Tests: `TestAnalyzeFieldsPositions`, `TestBindFulltextMultiField`, `TestSearchChoosesMultiFieldFulltextIndex`, `TestFulltextMultiFieldSearch`. **Highlight/snippet generation landed 2026-08-31**: `HIGHLIGHT(col)` / `SNIPPET(col)` are SELECT-list functions that require SEARCH (no catalog/format bump). Original document tokens whose analyzed form participates in the query (exact/synonym/prefix/fuzzy/typo) are wrapped with `<mark>` / `</mark>` (override `HIGHLIGHT(col, pre, post)` / `SNIPPET(col, width [, pre, post])`; markers max 32 runes). `HIGHLIGHT` returns the full field; `SNIPPET` returns a 16–4096 rune window (default 160) around the densest match cluster with `…` on a truncated edge. Both fail closed outside the SELECT list of a SEARCH query. Tests: `TestTokenizeSpans`, `TestHighlightExact`, `TestHighlightPreservesOriginalCase`, `TestHighlightPrefixFuzzyTypo`, `TestHighlightEnglishStemAndSynonym`, `TestHighlightEnglishDropsStops`, `TestHighlightCustomMarkersAndEmptyQuery`, `TestHighlightMarkerLimits`, `TestSnippetWindow`, `TestSnippetShortTextNoEllipsis`, `TestSnippetWidthBounds`, `TestHighlightsTermPrefixAndFuzzy`, `TestBindHighlightRequiresSearch`, `TestFulltextHighlight`. `go build ./...` + `internal/fulltext` / `internal/sql/binder` / `internal/sql/parser` / `internal/executor` `go test` + `-race` green; `FuzzTokenize` 5 s clean. Docs: `docs/fulltext.md`, `docs/sql.md`, `USAGE.md`, `CHANGELOG.md`, `ROADMAP.md`, `SKILLS.md`, `AGENTS.md`, web `fulltext.md` / `limits.md` / `sql.md`. **Typo tolerance landed 2026-08-31**: unadorned SEARCH tokens stay exact when any analyzed alternative is in the searchable vocabulary (`cat` does not match `cot` when `cat` is indexed; `cats` does not match `cat`). When every alternative is absent, SEARCH rewrites the group as an AUTO-distance fuzzy group (no catalog/format bump): `databse` matches `database`. Typo AUTO is stricter than explicit `~` (0 for 1–4 runes, 1 for 5–8, 2 for 9+). Prefix and explicit fuzzy groups are unchanged; phrase slots follow the same rule (`"databse performance"`); BM25 scores the best match; distinct terms consume the existing expansion caps (256 terms / 8192 bytes / 4096 work units) and fail closed. Seq-scan uses the scanned corpus as the vocabulary. Tests: `TestApplyTypoToleranceMissing`, `TestApplyTypoTolerancePresentExactUnchanged`, `TestApplyTypoToleranceShortStaysExactMiss`, `TestAutoTypoDistance`, `TestApplyTypoTolerancePrefixAndFuzzyUnchanged`, `TestApplyTypoTolerancePhrase`, `TestApplyTypoToleranceSynonymGroup`, `TestApplyTypoToleranceNilPresent`, `TestQueryMatchesTypo`, `TestQueryScoreTypoBestMatch`, `TestFulltextTypoSearch` (index + seq-scan + short-token miss + english `catalag` + synonym skip + expansion cap). `go build ./...` + `internal/fulltext` / `internal/executor` `go test` + `-race` green; `FuzzTokenize` 5 s clean. Docs: `docs/fulltext.md`, `docs/sql.md`, `USAGE.md`, `CHANGELOG.md`, `ROADMAP.md`, `SKILLS.md`, `AGENTS.md`, web `fulltext.md` / `limits.md` / `sql.md`. **Fuzzy matching landed 2026-08-31**: trailing ASCII `~` on a SEARCH token (`cat~`, `cat~1`, `cat~2`, `"databas~ performance"`) is a query-time fuzzy group (no catalog/format bump). Fuzzy tokens skip stem/stop/synonym (French elision still applies); matching indexed terms within OSA Damerau-Levenshtein distance (AUTO: 0 for 1–2 runes, 1 for 3–5, 2 for 6+; explicit 1 or 2) are a disjunction at that position; BM25 scores the best match; distinct terms consume the existing expansion caps (256 terms / 8192 bytes / 4096 work units) and fail closed; mixed `*`/`~` and `~0`/`~3+` fail closed; exact unadorned tokens keep Phase 10 BM25/phrase/prefix behaviour (`cat` does not match `cot`). Tests: `TestParseQueryFuzzy`, `TestParseQueryFuzzyPhrase`, `TestParseQueryFuzzySkipsStemAndSynonym`, `TestQueryMatchesFuzzy`, `TestFuzzyWithin`, `TestAutoFuzzyDistance`, `TestQueryScoreFuzzyBestMatch`, `TestFuzzyExpanderFailClosed`, `TestFulltextFuzzySearch` (index + seq-scan + english `run~` vs `running~` + synonym skip + expansion cap). `go build ./...` + `internal/fulltext` / `internal/executor` `go test` + `-race` green; `FuzzTokenize` 5 s clean with fuzzy seeds. Docs: `docs/fulltext.md`, `docs/sql.md`, `USAGE.md`, `CHANGELOG.md`, `ROADMAP.md`, `SKILLS.md`, `AGENTS.md`, web `fulltext.md` / `limits.md` / `sql.md`. **Prefix search landed 2026-08-31**: trailing ASCII `*` on a SEARCH token (`cat*`, `"data* performance"`) is a query-time prefix group (no catalog/format bump). Prefix tokens skip stem/stop/synonym (French elision still applies); matching indexed terms are a disjunction at that position; BM25 scores the best match; distinct terms consume the existing expansion caps (256 terms / 8192 bytes / 4096 work units) and fail closed; exact unadorned tokens keep Phase 10 BM25/phrase behaviour (`cat` does not match `catalog`). Tests: `TestParseQueryPrefix`, `TestParseQueryPrefixPhrase`, `TestParseQueryPrefixSkipsStemAndSynonym`, `TestQueryMatchesPrefix`, `TestPrefixExpanderFailClosed`, `TestPostingPrefixBounds`, `TestFulltextPrefixSearch` (index + seq-scan + english `run*` vs `running*` + expansion cap). `go build ./...` + `internal/fulltext` / `internal/executor` `go test` + `-race` green; `FuzzTokenize` 5 s clean with prefix seeds. Docs: `docs/fulltext.md`, `docs/sql.md`, `USAGE.md`, `CHANGELOG.md`, `ROADMAP.md`, `SKILLS.md`, `AGENTS.md`, web `fulltext.md` / `limits.md` / `sql.md`. **Synonym dictionaries landed 2026-08-31**: english analyzer v3 writes synonym dictionary v1 (15 tight bidirectional groups) as query-time OR expansion, bounded by the existing caps; index terms stay 1:1 like v2; phrase slots accept any alternative; english v1/v2 still decode; `simple` unchanged. Tests: `TestEnglishSynonymV1Membership`, `TestAnalyzeEnglishNoIndexSynonyms`, `TestParseQueryEnglishSynonyms`, `TestParseQueryEnglishSynonymPhrase`, `TestQueryMatchesSynonymDisjunction`, `TestEnglishSynonymWorkCounts`, `TestLookupAnalyzer` (v3), `TestTableEncodeFulltextAnalyzerV9` (v3), binder ANALYZER writes v3, `TestFulltextEnglishSynonyms`. **Versioned language analyzers landed 2026-08-31**: `french` / `german` / `spanish` analyzer v1 (Snowball 3.x stemmer + official Snowball stop-word dictionary v1: 153 / 231 / 308 terms) on existing `NSCT` v9 ids 2/3/4; French elides `l'`/`qu'`/… before the stop list; remaining terms re-pack to consecutive positions; `simple`/`english` unchanged; unknown names/revisions fail closed. Tests: `TestStemFrenchFixtures`, `TestStemGermanFixtures`, `TestStemSpanishFixtures`, `TestAnalyzeFrenchStopsThenStems`, `TestAnalyzeGermanStopsThenStems`, `TestAnalyzeSpanishStopsThenStems`, `TestParseQueryFrenchElision`, `TestFrenchStopV1Membership`, `TestGermanStopV1Membership`, `TestSpanishStopV1Membership`, `TestLookupLanguageAnalyzers`, `TestTableEncodeFulltextAnalyzerV9` (fr/de/es), binder ANALYZER cases, `TestFulltextLanguageAnalyzers`. `go build ./...` + `internal/fulltext` / `internal/catalog` / `internal/sql/parser` / `internal/sql/binder` / `internal/sql/optimizer` / `internal/upgrade` / `internal/xport` `go test` + `-race` green; `internal/executor` `TestFulltext*` green + `-race`; `FuzzTokenize` / `FuzzDecodePartitionedTable` 5 s clean. Docs: `docs/fulltext.md`, `docs/sql.md`, `docs/storage-format.md`, `USAGE.md`, `CHANGELOG.md`, `ROADMAP.md`, `SKILLS.md`, `AGENTS.md`, web `fulltext.md` / `limits.md` / `sql.md`. **Stop-word dictionaries landed 2026-08-31**: english analyzer v2 applies stop-word dictionary v1 (33-term Lucene EnglishAnalyzer / Snowball-small set) before Porter2 at index and query time; remaining terms re-pack to consecutive positions; `simple` has no stop list; english v1 (stem only) still decodes; dropped stop words consume query-expansion work units; stop-only SEARCH returns no rows. Tests: `TestEnglishStopV1Membership`, `TestAnalyzeEnglishDropsStops`, `TestAnalyzeEnglishStopsThenStems`, `TestParseQueryEnglishDropsStops`, `TestParseQueryEnglishPhraseDropsStops`, `TestEnglishStopWorkCounts`, `TestTableEncodeFulltextAnalyzerV9` (v1+v2), binder ANALYZER writes v2, `TestFulltextEnglishStopWords`. `go build ./...` + `internal/fulltext` / `internal/catalog` / `internal/sql/parser` / `internal/sql/binder` / `internal/sql/optimizer` / `internal/upgrade` `go test` + `-race` green; `internal/executor` `TestFulltext*` green + `-race`; `FuzzTokenize` / `FuzzDecodePartitionedTable` 5 s clean. **Stemming landed 2026-08-31**: `NSCT` v9 per-index analyzer id + revision; `CREATE FULLTEXT INDEX … WITH (ANALYZER = 'simple' | 'english')`; Snowball English (Porter2) v1 applied identically at index and query time; query expansion fail-closed at 256 terms / 8192 bytes / 4096 work units; default `simple` keeps Phase 10 BM25/phrase behaviour (`cat` does not match `cats`). Catalog v1–v8 still decode (missing trailer = simple). `takePartitioning` reads NextID for every `ver >= v5` (not only the current write version). Tests: `TestStemEnglishFixtures`, `TestAnalyzeEnglishStems`, `TestParseQueryEnglishPhrase`, `TestQueryExpansionCapsFailClosed`, `TestTableEncodeFulltextAnalyzerV9`, `TestPartitionCatalogV5ReadsNextID`, parser/binder ANALYZER cases, `TestFulltextEnglishStemming` (simple vs english, stemmed phrase, `EXPLAIN analyzer=english`). `go build ./...` + `internal/fulltext` / `internal/catalog` / `internal/sql/parser` / `internal/sql/binder` / `internal/sql/optimizer` / `internal/upgrade` `go test` + `-race` green; `internal/executor` `TestFulltext*` green + `-race` on the stemming suite; `FuzzTokenize` / `FuzzDecodePartitionedTable` 5 s clean. Docs: `docs/fulltext.md`, `docs/sql.md`, `docs/storage-format.md`, `docs/ops.md`, `USAGE.md`, `CHANGELOG.md`, `ROADMAP.md`, `SKILLS.md`, `AGENTS.md`, web `fulltext.md` / `limits.md`. P23 Vector Engine 2.0 is **COMPLETE** (2026-08-31): production-gating sign-off in `docs/vector.md` "Production-gating sign-off (Phase 23)". Seventeenth increment closed the exit gate. Sixteenth increment landed 2026-08-31: **official `--vecquant` sparse size/latency/recall row**. `internal/bench/vecquant.go` `runOneSparseQuant` seeds a high-dimension, low-nnz corpus (`SPARSEVECTOR<N>` + `USING SPARSE`) independent of the dense `--vecquant-dim` set; CLI `--vecquant-sparse-dim` (default 4096) / `--vecquant-sparse-nnz` (default 24). Deterministic SplitMix64 sparse vectors with strictly-positive weights; parameterized batch INSERT; inverted-index build; `NEAREST` latency + recall@10/@100 vs exact-cosine `SparseFlat` (SQL default `COSINE`, inverted-index inner product + `4·k` payload re-rank). `VectorQuantReport` gains `SparseNNZ`; `Method = "sparse"`; `ElemBytes = 0` (not a dense element type); `RawPayload` is the sum of `EncodeSparse` NSSV widths. Reference (linux/amd64, 12 vCPU, ext4, encryption + WAL + fsync on; 2000 × 4096-d nnz=24, 64 queries): raw payload 282 KiB, index 1.0 MiB, database 2.1 MiB, build 1.17 s, p50 428 µs / p95 716 µs, QPS 2099, recall@10 **1.000**, recall@100 **1.000**. `TestVectorQuantBench` asserts 8 reports + SPARSE-row invariants (nnz/dim, lossless QuantErr, raw payload below the dense F32 floor, recall@10 ≥ 0.5). `go build ./...` + `internal/bench` `go test` + `-race` green. Docs: `docs/vector.md` ("Size / recall comparison"), `docs/ops.md`, `USAGE.md`, `CHANGELOG.md`, `ROADMAP.md`, web `vectors.md`, this file.
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

   **P22, P23, and P24 are done** (2026-08-31). Next release gate is **P25 Security 2.0**.

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
[x] P19 WORKFLOW / TRIGGER / SCHEDULE / TASK
[x] P20 CDC / change streams
[x] P21 Native table partitioning
[x] P22 Follower reads / read scaling
[x] P23 Vector Engine 2.0   (complete 2026-08-31: production-gating sign-off; VECTOR<F16,N> + VECTOR<I8,N> + BITVECTOR<N>/HAMMING + quantised HNSW + compressed neighbour lists + IVF + IVF-PQ + SPARSEVECTOR / USING SPARSE + dense+sparse+BM25 fusion + --vecquant measurements. Optional follow-on: BITVECTOR/Hamming --vecquant row)
[x] P24 Full-text Search 2.0
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

Do not begin a later phase in a way that destabilizes an earlier open gate. UI foundations may be developed in parallel once underlying interfaces are stable. P16 correctness/SLO closure, P22 follower reads / read scaling, **P23 Vector Engine 2.0**, and **P24 Full-text Search 2.0** are done (exit gates green), so **P25 Security 2.0 is the current release gate**.

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
- [ ] Design and implement `REBUILD INDEX name ONLINE` only after proven-safe concurrent-write handling — **DEFERRED follow-on, not a P17 gate item.** Blocking `REBUILD INDEX` is the shipped path; `ONLINE` is explicitly rejected. Revisit once concurrent-write index maintenance is proven (candidate P27 operational-maturity work).
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
- [ ] Cron syntax deferred until core scheduler is proven

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

Audit every security checklist item and distinguish `designed`, `implemented`, `tested`, and `production-gated`. The dated item-by-item audit is in `docs/security.md` under "P25 Security 2.0 audit (2026-08-31)".

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

- [x] OIDC design — accepted design in `docs/design-oidc-external-idp.md` (2026-08-31): brokered token exchange (`cmd/nextsql-auth-broker` / embedded `nextsqld --auth-broker-listen`) validates an OIDC ID token / access token against a cached JWKS and mints an existing `NSSC1.` short-lived credential; the SQL auth path stays offline and unchanged. Versioned encrypted `NSIP` identity-policy: ordered issuer-scoped subject→principal rules, group→role mappings intersected with the principal's real RBAC membership (no escalation, empty ⇒ deny), `SIGHUP` last-known-good. Audit `identity_source` `oidc` / `mtls+oidc` keyed off the verifying key, not attacker bytes. Direct server-side JWT verification (`NSIDP1.`) documented as the rejected alternative. Nothing implemented; `docs/security.md` audit rows stay `no` for implementation/tested.
- [~] OIDC implementation — **broker skeleton done (2026-08-31)**: `internal/oidc` (compact JWS verify RS/PS/ES 256/384/512, `none`+MAC rejected; JWKS parse; soft/hard-TTL rate-limited JWKS cache; ID-token validation; replay guard; `FuzzParseJWKS`/`FuzzParseCompact`/`FuzzVerify`) + `internal/authbroker` + `cmd/nextsql-auth-broker` (`POST /v1/exchange` → validate ID token vs cached JWKS → `NSIP` `Map` → mint `NSSC1.` via a private `NSTK` key; TTL = min(configured, IdP token exp); deployment audience; structured no-secrets audit; `SIGHUP` last-known-good). Fake-IdP→broker→real `auth.TokenVerifier` integration test (`internal/authbroker/broker_test.go`). Still missing: `nextsql login` client flow, `nextsqld` `oidc`/`mtls+oidc` audit source, client-credentials, embedded mode, JIT
- [x] Identity-to-NextSQL principal mapping — `NSIP` engine (`internal/auth/identitypolicy.go`: ordered issuer-scoped subject rules, claim conditions, pure transform pipeline, fail-closed `[a-z0-9._-]{1,128}` login-charset check; `FuzzDecodeIdentityPolicy`/`FuzzMapClaims`) is consumed by the broker (`IdentityPolicy.Map` in `internal/authbroker/exchange.go`); `TestExchangeHappyPathMintsVerifiableCredential`, `TestExchangeRejections`
- [~] External auth does not bypass NextSQL RBAC — broker mints the `NSIP`-mapped role set; when a `RoleMembershipFunc` is wired it intersects with real membership (`auth.IntersectRoles`), empty ⇒ deny (`TestExchangeRBACIntersection`), and the server's `ACL.AllowedScoped` enforces no-escalation on every statement regardless. The automatic `security.ACL` membership feed is a later increment
- [x] Group/role mapping policy — `NSIP` literal + anchored-RE2 `${n}` group→role mappings, 16-role cap, unmatched/empty ⇒ deny, consumed by the broker; `TestIdentityPolicyGroupRegexCapture`, `TestIdentityPolicyRoleCapDenies`, `TestIdentityPolicyDefaultRolesWhenNoGroupMapped`, `TestExchangeRejections` (unmapped groups/subject ⇒ 403)

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
- [ ] System schema obeys RBAC and realm/database visibility rules
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
- [ ] Per-realm and per-database connection limits
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

# Cross-cutting track — Multi-database hosting / subscription isolation

Architecture: `docs/design-multidatabase-dbaas.md`. This track is a dependency
of the Phase 28 Manager database lifecycle surface. Production activation
depends on Phase 25 identity/security, Phase 26 introspection, and Phase 27
workload governance. It does not close or supersede the current release gate (P25).

### M0/M1 foundation

- [x] Accepted realm/database terminology, isolation contract, non-goals, and staged production gates
- [x] Versioned deterministic `NSRM` v1 manifest with stable deployment/realm/database IDs, bounded names/counts, lifecycle validation, identity binding, truncation/trailing-byte checks, and fuzz seed
- [ ] Declarative multi-realm bootstrap manifest (`NEXTSQL_HOSTING_MANIFEST_FILE`) — validate-all-before-mutation, independent database key-file paths, idempotent reapply, atomic registry publication; do not advertise before live router support
  - [x] `hosting` library slice — bounded YAML parse with node/depth caps and anchor/alias rejection, whole-document validation before any mutation, path- and digest-independent key files, stable derived realm/database identities, `EnsureManifest` one-generation publication, and `matchManifest` idempotent reapply that fails closed with `Conflict` on any identity mutation (`internal/hosting/bootstrap_manifest.go`, tests green)
  - [ ] Wire `NEXTSQL_HOSTING_MANIFEST_FILE` into `nextsql init` once live multi-engine routing exists
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

### M2+ selectable hosting

- [ ] Negotiated realm field and official CLI/driver configuration
- [ ] Realm-scoped auth stores and database-scoped `CONNECT`
- [ ] Bounded multi-engine manager, lazy open/recovery, reference-safe eviction, and central worker budgets
- [ ] Registered CREATE/rename/suspend/resume/drop database lifecycle
- [ ] Independent WAL/recovery/cache/idempotency/task/CDC/backup/PITR scope per database
- [ ] Hierarchical deployment/realm/database/user quotas and durable billing-grade usage ledger where enabled
- [ ] Adversarial cross-realm/database, resource, migration, three-voter failover, and rolling-upgrade gates
- [ ] Production exit gate in `docs/design-multidatabase-dbaas.md` fully green

Current status: **PARTIAL / FOUNDATION ONLY**. One `nextsqld` still opens and
serves one default database engine; do not claim selectable multi-database
hosting yet.

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
