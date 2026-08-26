# Changelog

All notable changes to **NextSQL** are documented in this file.

NextSQL is currently under active development as `0.1.0-dev`.

This changelog follows the project source-of-truth model:

```text
TODO.md    = current implementation/status truth
PROJECT.md = intended finished product
TODO.md    = implementation status, sequencing, dependencies, and phase gates
SKILLS.md  = engineering/agent contract
AGENTS.md  = repository agent instructions
USAGE.md   = current user/operator manual
README.md  = project overview
CHANGELOG.md = notable shipped/verified changes
```

A roadmap item is not recorded as completed here until its implementation, tests, documentation, and applicable exit gate are complete.

---

## [Unreleased]

### Current release gate

P16 correctness/SLO closure remains open.

Required before P16 can be marked complete:

- corrected 1M-vector HNSW benchmark;
- tracked p95 target must be satisfied;
- ANN recall must be reported with latency;
- randomized 100M B+Tree insert/delete invariant soak must complete successfully;
- any correctness regressions found by those gates must be fixed before closure.

Known corrected validation already includes:

- 100K distinct-vector HNSW p95: **3.317 ms**;
- recall@10: **1.000**;
- recall@100: **0.999**.

These measurements do not replace the still-required corrected 1M-vector exit-gate run.

### Deferred

- `REBUILD INDEX ... ONLINE`
  - blocking `REBUILD INDEX` is shipped;
  - `ONLINE` remains rejected until concurrent-write correctness is proven.

- partition-wise aggregation and partition-wise joins
  - waits for physical partitioning in P21.

### Planned roadmap

The following phases remain planned/open and are **not** current shipped functionality:

- P19 — WORKFLOW / TRIGGER / SCHEDULE / TASK
- P20 — CDC / Change Streams
- P21 — Native Table Partitioning
- P22 — Follower Reads / Read Scaling
- P23 — Vector Engine 2.0
- P24 — Full-text Search 2.0
- P25 — Security 2.0
- P26 — System Catalog / Introspection 2.0
- P27 — Operational Maturity / Workload Governance
- P28 — Professional Installer + NextSQL Manager
- P29 — Web-based NextSQL Studio
- P30 — NextSQL Intelligence + Built-in RAG

---

## [0.1.0-dev]

### Added

#### Native database foundation

- Native NextSQL storage engine.
- Native NextSQL SQL dialect.
- Native NSQL wire protocol.
- Official driver implementations.
- 16 KiB logical page format.
- Versioned persistent formats.
- Versioned wire formats.
- Explicit page validation and corruption handling.
- Clustered B+Tree primary storage.
- Secondary indexes.
- Range scans.
- Buffer manager.
- Crash-safe persistence.

#### Transactions and durability

- ACID transaction model.
- MVCC version chains.
- READ COMMITTED isolation.
- SNAPSHOT isolation.
- SERIALIZABLE isolation with lock-based semantics.
- Transaction rollback.
- Deadlock detection.
- UNDO integration.
- LSN-based WAL.
- WAL segmentation and rotation.
- Group commit.
- fsync before commit acknowledgement.
- Checkpoints.
- REDO recovery.
- Partial-WAL-tail handling.
- Partial-data-write handling.
- Crash-injection coverage.

#### Encryption and security

- Encryption-by-default production storage model.
- AES-256-GCM authenticated page encryption.
- Encrypted WAL.
- Encrypted UNDO.
- Encrypted backup structures.
- Encrypted vector structures.
- Encrypted full-text structures.
- Encrypted temp/spill domains where applicable.
- Root unlock key kept outside the data volume.
- KEK → database master → domain-specific DEK hierarchy.
- Key rotation support.
- Key revocation support.
- Crypto-shredding support.
- TLS 1.3 requirements for remote production connections.
- Password authentication.
- RBAC.
- Tenant-aware access controls.
- Session auditing.
- Fail-closed handling for malformed or unauthorized operations.

#### SQL engine

- Lexer.
- Parser.
- AST.
- Catalog.
- Binder.
- Logical planner.
- Physical planner.
- Deterministic cost optimizer.
- Vectorized executor.
- Parallel execution.
- Statistics.
- Plan cache.
- `EXPLAIN`.
- `EXPLAIN ANALYZE`.

#### Relational SQL

- `CREATE TABLE`.
- `CREATE INDEX`.
- `CREATE UNIQUE INDEX`.
- `CREATE DATABASE`.
- `ALTER TABLE`.
- `DROP TABLE`.
- `INSERT`.
- `SELECT`.
- `UPDATE`.
- `DELETE`.
- `BEGIN`.
- `COMMIT`.
- `ROLLBACK`.
- `ANALYZE`.
- Foreign keys.
- `RESTRICT`.
- `NO ACTION`.
- `CASCADE`.
- `SET NULL`.
- `SET DEFAULT`.
- Inner joins.
- Left joins.
- Right joins.
- Full outer joins.
- Cross joins.
- Aggregation.
- Grouping.
- Ordering.
- `LIMIT`.
- `OFFSET`.

#### Modern SQL completeness

- `SELECT DISTINCT`.
- `HAVING`.
- searched `CASE`.
- simple `CASE`.
- `UNION`.
- `UNION ALL`.
- `INTERSECT`.
- `EXCEPT`.
- scalar subqueries.
- `IN` / `NOT IN` subqueries.
- `EXISTS` / `NOT EXISTS`.
- correlated subqueries.
- derived tables.
- CTEs.
- recursive CTEs.
- window functions.
- `ROW_NUMBER`.
- `RANK`.
- `DENSE_RANK`.
- `LAG`.
- `LEAD`.
- `FIRST_VALUE`.
- `LAST_VALUE`.
- aggregate window functions.
- UPSERT.
- `INSERT ... RETURNING`.
- `UPDATE ... RETURNING`.
- `DELETE ... RETURNING`.
- covering indexes / `INCLUDE`.
- index-only scans.
- partial indexes.
- expression indexes.
- Top-N optimization.
- improved join reordering.

#### Native JSON

- Native compact binary JSON storage.
- Typed JSON values.
- Object/array/scalar support.
- JSON path traversal.
- Partial decoding.
- JSON-path indexes.
- Transaction integration.
- WAL/recovery integration.
- Encrypted JSON persistence.
- JSON depth and size limits.
- JSON parser fuzzing.

#### Full-text search

- Native inverted index.
- Tokenizer.
- Normalization.
- Posting lists.
- Term/document frequency tracking.
- Positions.
- BM25-style ranking.
- Phrase search.
- `SEARCH column FOR '...'`.
- Transaction integration.
- WAL/recovery integration.
- Encrypted full-text index structures.

#### Vector search

- `VECTOR<F32,N>`.
- Out-of-row vector storage.
- Contiguous vector store.
- COSINE distance.
- L2 distance.
- INNER_PRODUCT.
- Exact flat vector search.
- `NEAREST ... TO`.
- HNSW.
- Encrypted ANN/vector structures.
- Bounded dimensions.
- Parallel distance calculation.

#### Hybrid query planning

- Unified relational + JSON + full-text + vector planning.
- Cost-based structured-filter-first or ANN-first execution.
- Candidate generation.
- Reranking.
- Reciprocal-rank fusion for hybrid result merging.
- `EXPLAIN` visibility into hybrid planning.

#### Geospatial

- `POINT`.
- `LOCATION`.
- `BOX`.
- `LINESTRING`.
- `POLYGON`.
- Coordinate validation.
- WKT coercion.
- `LON`.
- `LAT`.
- `DISTANCE`.
- `DISTANCE_SPHEROID`.
- `DWITHIN`.
- `WITHIN`.
- `COVERS`.
- Line length support.
- Spatial indexes.
- Optimizer integration.
- Exact residual spatial predicates.

#### Schema lifecycle and storage maintenance

- `DROP INDEX` for shipped index types.
- `DROP INDEX IF EXISTS`.
- Blocking `REBUILD INDEX`.
- Crash-safe index rebuild.
- Page reclamation.
- Durable freelist.
- Safe page reuse after restart.
- Orphan detection.
- MVCC-safe garbage eligibility.
- UNDO cleanup.
- Dead-version cleanup.
- B+Tree compaction.
- Full-text tombstone cleanup.
- HNSW tombstone strategy.
- WAL retention respecting PITR.
- `MAINTAIN DATABASE`.
- `MAINTAIN TABLE`.
- `MAINTAIN INDEX`.
- Bounded maintenance coordinator.
- Maintenance CPU budgets.
- Maintenance memory budgets.
- Maintenance I/O budgets.
- One active maintenance pass per database.
- Pause/resume support.
- Admission-aware maintenance.
- Maintenance metrics.
- Automatic statistics refresh policy.
- Bounded automatic maintenance scheduling.

#### Migrations

- Timestamped migration files.
- `migrate validate`.
- `migrate create`.
- `migrate status`.
- `migrate pending`.
- `migrate version`.
- `migrate up`.
- `migrate down`.
- `migrate force`.
- `migrate repair`.
- Transactional migration application.
- Checksum validation.
- Dirty-state detection.
- Dry-run parsing.
- Server-mode migration execution over NSQL.
- `DROP INDEX` migration parsing/validation support.

#### Native protocol and drivers

- TLS-aware NSQL connections.
- Authentication handshake.
- Typed parameters.
- Prepared statements.
- Streaming results.
- Backpressure.
- Cancellation.
- Packet-size limits.
- SQL-length limits.
- Result-size limits.
- Runtime limits.
- Worker limits.
- Memory limits.
- Attacker-controlled length validation.

Official driver surfaces include:

- Go.
- Node.js.
- Bun.
- Deno.
- TypeScript types.
- PHP.

#### Backups and recovery

- Encrypted physical backup.
- Restore.
- Backup verification.
- Restore verification.
- WAL archive integration.
- PITR.
- Restore by LSN.
- Restore by timestamp.
- Logical export.
- Logical import.

#### High availability

- Raft-based HA.
- Minimum 3-voter cluster model.
- Leader election.
- Replicated state/log.
- Synchronous quorum durability.
- Leader failover.
- Replica repair.
- Rolling maintenance support.
- Safe write rejection under quorum loss.
- Split-brain prevention.
- Deterministic follower application.
- Engineering target: leader election under 3 seconds.
- Engineering target: service recovery under 5 seconds.
- Availability target expressed as an SLO, not a zero-downtime guarantee.

#### Operational tooling

- `nextsql` CLI.
- `nextsqld` server.
- `nextsql-bench`.
- `nextsql init`.
- `nextsql exec`.
- `nextsql backup`.
- `nextsql restore`.
- `nextsql verify`.
- `nextsql export`.
- `nextsql import`.
- `nextsql diagnose`.
- `nextsql status`.
- cluster status tooling.
- Official benchmark workloads.
- Admission control.
- Bounded query queues.
- Query cancellation.
- Result limits.
- Operational diagnostics.

#### Packaging

- Linux `.deb` packaging.
- Linux `.run` packaging.
- Linux `.tar.gz` packaging.
- Windows `.zip` packaging.
- Windows installer support.
- Installer build scripts.

### Changed

- Expanded SQL from the original P0–P15 surface through the P18 implementable SQL-completeness scope.
- Expanded schema lifecycle from create-only index behavior to full shipped `DROP INDEX` plus blocking rebuild.
- Added durable storage reclamation and reuse instead of leaving detached pages permanently unreclaimed.
- Added bounded maintenance as a first-class engine responsibility.
- Migration validation now understands shipped `DROP INDEX` behavior.
- Project documentation now separates:
  - final product intent;
  - implementation/status truth;
  - sequencing;
  - agent engineering rules;
  - user/operator documentation.

### Fixed

- Corrected large sequential `DELETE` behavior after the B+Tree leaf-merge issue.
- Preserved B+Tree structural correctness through restart/recovery testing.
- Corrected vector benchmark methodology to use distinct-vector validation and report recall with latency.
- Improved consistency between README, usage documentation, project specification, and engineering-agent documentation.

### Security

- Documented the live-unlocked-host threat-model limitation explicitly.
- Reinforced the rule that keys and passwords must never be carried in connection URLs.
- Kept encryption and durability enabled in official benchmark methodology.
- Reinforced fail-closed behavior for malformed, unauthorized, or unsupported operations.

### Performance

Tracked engineering targets include:

- cached primary-key lookup p50 < 0.5 ms;
- indexed query p95 < 3 ms;
- 25K-row workload < 1 s;
- optimized 1M-row aggregation < 1 s;
- optimized 10M-row aggregation < 5 s;
- 100M analytical workload < 30–60 s;
- 1M HNSW top-10 p95 < 25 ms with recall reported.

Performance figures are hardware/context-specific engineering targets or measurements, not universal guarantees.

### Known limitations

- `0.1.0-dev` remains under measurement.
- P16 is not yet closed.
- `REBUILD INDEX ... ONLINE` is not implemented.
- Partition-wise aggregation/join waits for native physical partitioning.
- P19–P30 are not shipped.
- Multi-primary writes are not part of the current core roadmap.
- Studio, Manager, and Intelligence are not current production surfaces until their roadmap phases complete.

---

## Changelog policy

Use the following categories when recording changes:

```text
Added
Changed
Deprecated
Removed
Fixed
Security
Performance
```

Rules:

1. Record **shipped or verified behavior**, not aspirations.
2. Put active development under `[Unreleased]`.
3. Do not mark roadmap items completed until `TODO.md` says the owning gate is green.
4. Include correctness-impacting fixes even if they are internal.
5. Include persistent-format or wire-format changes prominently.
6. Include security-relevant behavior under `Security`.
7. Include benchmark methodology changes under `Performance`.
8. Do not convert targets into measured claims.
9. Do not describe blocking operations as online.
10. Never make unsupported claims such as:
    - “unhackable”;
    - “100% secure”;
    - “zero downtime guaranteed”;
    - “impossible to lose data”.

---

## Links

- [README.md](README.md) — project overview and quick start
- [USAGE.md](USAGE.md) — current operator/application manual
- [PROJECT.md](PROJECT.md) — intended finished product
- [TODO.md](TODO.md) — current implementation/status truth
- [ROADMAP.md](ROADMAP.md) — simplified, non-authoritative roadmap derived from `TODO.md`
- [SKILLS.md](SKILLS.md) — engineering/agent contract
- [AGENTS.md](AGENTS.md) — repository agent instructions
