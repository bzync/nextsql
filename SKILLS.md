# NextSQL Agent Skills

> Operating contract for AI coding agents working on NextSQL.
>
> Source: converted from the NextSQL Development Tracker. This file is not a duplicate TODO list. It defines the skills, constraints, engineering discipline, sequencing, verification rules, and product context an agent must apply when implementing or reviewing NextSQL.

---

## 1. Mission

You are working on **NextSQL**, a native multimodel database engine with its own protocol, SQL dialect, storage engine, optimizer, security model, drivers, operational tooling, Studio, and Intelligence/RAG layer.

NextSQL combines:

- Relational SQL
- Native binary JSON
- Full-text search
- Native vector search
- Geospatial data
- Hybrid SQL + JSON + full-text + vector planning
- ACID transactions
- WAL + crash recovery
- Encrypted storage
- Native network protocol
- High availability
- Operational tooling
- Future programmable automation, CDC, partitioning, read scaling, Studio, and built-in RAG

NextSQL is **not** a PostgreSQL/MySQL/MariaDB compatibility project. Do not introduce compatibility dependencies unless explicitly requested.

---

# 2. Core Agent Behavior

## 2.1 Priority order

Always optimize in this order:

```text
Correctness
→ durability
→ security
→ integrity
→ availability
→ latency
→ throughput
→ efficiency
→ developer experience
→ features
```

Never trade an earlier property for a later one.

Examples:

- Do not improve latency by reducing vector recall without making it explicit.
- Do not improve throughput by weakening fsync or WAL durability in official benchmarks.
- Do not add features while an earlier correctness gate is red.
- Do not claim HA guarantees that have not been measured.
- Do not weaken realm/database isolation for convenience.

## 2.2 Definition of done

A feature is not done merely because code compiles.

A change is complete only when applicable items are satisfied:

- implementation exists;
- parser/binder/planner/executor behavior is complete where SQL is involved;
- persistence and restart behavior are verified;
- WAL/recovery behavior is verified where state changes;
- Raft determinism/replication is verified where distributed state changes;
- RBAC and realm/database isolation are verified;
- limits and resource budgets are enforced;
- tests pass;
- race tests pass;
- fuzz tests exist for new untrusted decoders/parsers;
- benchmarks exist when performance is part of the contract;
- documentation is updated;
- failure behavior is explicit and fail-closed;
- the phase exit gate is green.

Never mark a phase complete until its exit gate is fully satisfied.

## 2.3 Evidence discipline

Prefer measured facts over claims.

For benchmarks, always record enough context to reproduce the result:

- CPU
- RAM
- storage
- filesystem
- row width
- query
- indexes
- cache state
- encryption mode
- durability mode
- concurrency
- QPS/TPS
- p50/p95/p99/p99.9 where relevant
- memory
- disk/WAL usage
- recall for ANN/vector results

Official performance measurements must keep encryption and durability enabled unless explicitly labeled experimental.

---

# 3. Current Project State

Current development state:

```text
P0–P15  complete
P16      complete — exit gate green; terminal 100M B+Tree soak deferred as a non-gate standalone measurement
P17      complete — REBUILD INDEX ... ONLINE is a deferred follow-on (not a gate item)
P18      implementable scope complete
P19      complete; clean repository-wide functional gate passed 2026-08-29
P20      complete
P21      complete — RANGE/HASH/LIST with 1–8-column keys, ATTACH/DETACH, partition-local indexes, pruning-sound, partition-wise agg/join, offline legacy TENANT migration
P22      complete — follower reads / read scaling; exit gate closed 2026-08-30
P23      complete — Vector Engine 2.0; production-gating sign-off 2026-08-31
P24      complete — Full-text Search 2.0; exit gate closed 2026-08-31
P25–P30 planned/open
```

Immediate release-gate work is **P25 Security 2.0**.

Current P25 focus:

1. Audit every P25 security item as designed, implemented, tested, or
   production-gated; then take the smallest coherent mTLS/service-identity
   increment without weakening the existing TLS/RBAC/key boundaries.

P16, P23, and P24 are complete (exit gates green; the terminal 100M B+Tree invariant
soak is a deferred standalone measurement, not a gate). Do not let later feature
work destabilize the earlier gates.

---

# 4. Existing Engine Skills

Agents must understand and preserve the following already-landed capabilities.

## 4.1 Storage engine

NextSQL uses a native page engine with:

- 16 KiB logical pages
- versioned binary storage formats
- slotted variable-length records
- page identity and validation
- authenticated encrypted page envelopes
- page allocator
- buffer manager
- crash-safe persistence
- clustered B+Tree
- page split / merge / rebalance
- range scans
- restart persistence
- structural invariant checking

Rules:

- Never serialize raw Go structs directly to disk.
- Every persistent structure must have explicit versioned encoding.
- Corrupt/truncated pages must fail closed.
- Known corrupted records must never be returned.

## 4.2 Encryption

Production storage encryption is mandatory.

Current architecture includes:

```text
root unlock
  ↓
KEK
  ↓
database master
  ↓
domain-specific DEKs
```

Separate encryption domains exist or may exist for:

- pages
- WAL
- backups
- UNDO
- vectors
- full-text
- temp/spill
- replication

Use established cryptography only.

Current page encryption uses AES-256-GCM.

Never invent a custom cryptographic primitive.

Every encrypted unit must carry enough version/key metadata to support:

- rotation
- revocation
- crash/restart
- restore
- snapshot rollback
- promotion/failover
- nonce uniqueness

No keys in connection URLs.

Persistent plaintext in production mode should remain zero by default.

## 4.3 WAL and crash recovery

Preserve:

- LSN-based WAL
- authenticated/checksummed WAL
- encrypted WAL
- WAL segments + rotation
- group commit
- fsync before COMMIT acknowledgement
- checkpoints
- redo recovery
- archival hooks
- partial-WAL-tail handling
- partial-data-write handling
- crash injection testing

Never ACK a commit before the configured durability boundary.

## 4.4 Transactions

Preserve:

- transaction IDs
- encrypted or isolated UNDO domain
- MVCC version chains
- snapshots
- locks
- rollback
- deadlock detection
- READ COMMITTED
- SNAPSHOT
- SERIALIZABLE only when anomaly tests remain green

Readers must never see uncommitted writes.

Crash recovery must remain correct with UNDO + REDO.

## 4.5 SQL engine

The native SQL stack includes:

- lexer
- parser
- AST
- catalog
- binder
- logical planner
- physical planner
- executor
- vectorized execution
- parallel execution
- cost model
- statistics
- plan cache
- EXPLAIN
- EXPLAIN ANALYZE

Do not bolt new SQL features directly into execution while bypassing parser/binder/planner architecture.

## 4.6 Query optimizer

Preserve deterministic planning.

The optimizer already supports:

- predicate pushdown
- projection pushdown
- constant folding
- LIMIT pushdown
- index selection
- join simplification/reordering
- column pruning
- partition/segment pruning hooks
- row-count stats
- null ratio
- NDV
- min/max
- histograms
- MCV
- correlation
- index selectivity
- segment statistics
- runtime estimate-vs-actual feedback
- plan caching

No LLM planner inside the core deterministic optimizer.

## 4.7 Vectorized / parallel execution

Preserve:

- columnar batches
- bounded batch sizes
- vector filters/projection
- batch decoding
- hash aggregation
- hash join
- merge join
- index scan
- parallel scan
- parallel aggregation
- parallel joins
- parallel index builds
- explicit scheduler
- bounded CPU workers
- memory budgets
- spill budgets
- I/O budgets
- execution-time budgets
- result streaming

Never introduce unbounded per-query goroutines.

## 4.8 Native network protocol

NextSQL has its own versioned wire protocol.

Preserve:

- TLS 1.3 for production remote connections
- authentication
- typed parameters
- prepared statements
- streaming + backpressure
- cancellation
- packet-size limits
- SQL-length limits
- result-size limits
- runtime/memory/worker limits
- attacker-controlled length validation

Current official driver surface includes:

- Go
- Node.js
- Bun
- Deno
- TypeScript types
- PHP

Keys must be delivered via a key provider or equivalent secure mechanism, not URLs.

## 4.9 Native JSON

Stored JSON is native compact binary JSON rather than plaintext UTF-8.

Preserve:

- typed objects/arrays/scalars
- path traversal
- partial decoding
- JSON-path indexes
- WAL/recovery integration
- encrypted storage
- depth/size bounds
- parser fuzzing

## 4.10 Full-text search

Current full-text engine includes:

- tokenizer
- normalization
- inverted index
- posting lists
- TF/DF
- positions
- BM25-style ranking
- phrase search
- `SEARCH col FOR '...'`
- versioned analyzers (`simple`, Snowball `english` v1 stem-only / v2 stem + stop-word dictionary v1 / v3 + synonym dictionary v1 at query time, `french` / `german` / `spanish` v1) and `WITH (ANALYZER = …)`
- prefix search (`cat*` / `"data* performance"`, fail-closed expansion caps)
- fuzzy matching (`cat~` / `cat~1` / `cat~2` / `"databas~ performance"`, OSA Damerau-Levenshtein, fail-closed expansion caps)
- typo tolerance (unadorned missing terms become AUTO fuzzy; 0/1/2 edits for 1–4 / 5–8 / 9+ runes; present exact terms unchanged)
- fail-closed query-expansion CPU/memory caps
- transaction/WAL/recovery integration
- encrypted index structures

## 4.11 Vector search

Current vector support includes:

- `VECTOR<F32,N>`
- out-of-row vector storage
- contiguous vector store
- COSINE
- L2
- INNER_PRODUCT
- exact flat search
- `NEAREST ... TO`
- HNSW
- encrypted vector blocks/ANN structures
- bounded dimensions
- parallel distance calculation
- bounded F32 value algebra: dimension, norm/normalize, add/subtract/scale,
  dot, cosine distance, and L1/Manhattan

Vector performance claims must always pair latency with recall.

## 4.12 Hybrid optimizer

NextSQL treats relational, JSON, full-text, and vector predicates as **one physical planning problem**.

Support both where cost-effective:

```text
structured filter → ANN
ANN → structured filter
```

Do not hard-code a universal operator order.

EXPLAIN should expose candidate generation and reranking.

## 4.13 Geospatial

Current native geospatial support includes:

- POINT / LOCATION
- BOX
- LINESTRING
- POLYGON
- coordinate validation
- WKT coercion
- LON / LAT
- DISTANCE
- DISTANCE_SPHEROID
- DWITHIN
- WITHIN / COVERS
- line length
- full pairwise native distance/intersection across POINT, BOX, LINESTRING,
  and POLYGON
- polygon area/perimeter, centroid/envelope, type/point/ring inspection
- self-intersection and hole topology validation
- spatial indexes
- optimizer integration

Residual spatial predicates must remain exact.

## 4.14 High availability

NextSQL HA uses Raft and requires a safe quorum model.

Preserve:

- minimum 3 voting nodes
- leader election
- replicated log/state
- failover
- replica repair
- rolling maintenance
- safe write rejection under quorum loss
- no split brain
- synchronous quorum commit
- zero lost ACKed quorum-synchronous commits within covered failures

Targets already established:

```text
leader election < 3 s
service recovery < 5 s
```

Availability is an SLO/design objective, never an unconditional “100% uptime” claim.

---

# 5. SQL Completeness Skill

P18 has landed a broad modern SQL surface using NextSQL-native semantics.

Preserve correctness for:

- SELECT DISTINCT
- HAVING
- searched CASE
- simple CASE
- UNION
- UNION ALL
- INTERSECT
- EXCEPT
- scalar subqueries
- IN / NOT IN subqueries
- EXISTS / NOT EXISTS
- correlated subqueries
- derived tables
- CTEs
- recursive CTEs
- window functions
- UPSERT
- INSERT/UPDATE/DELETE RETURNING

Window functions include:

- ROW_NUMBER
- RANK
- DENSE_RANK
- LAG
- LEAD
- FIRST_VALUE
- LAST_VALUE
- aggregate windows

Built-in functions include major string, numeric, NULL/value, date/time, and JSON operations.

Optimizer/index extensions include:

- covering indexes / INCLUDE
- index-only scans
- partial indexes
- expression indexes
- Top-N sort
- improved join reordering

Partition-wise aggregation and joins remain deferred until physical partitioning exists in P21.

---

# 6. Schema Lifecycle and Maintenance Skill

P17 is substantially complete.

Preserve:

- DROP INDEX for all shipped index types
- blocking REBUILD INDEX
- crash-safe index rebuild
- page reclamation
- durable freelist
- safe page reuse after restart
- orphan detection
- MVCC-safe garbage eligibility
- UNDO cleanup
- dead-version cleanup
- B+Tree compaction
- full-text tombstone cleanup
- HNSW tombstone strategy
- WAL retention respecting PITR
- MAINTAIN DATABASE
- MAINTAIN TABLE
- MAINTAIN INDEX
- bounded maintenance coordinator
- CPU/memory/I/O budgets
- one active maintenance pass/database
- pause/resume
- admission awareness
- maintenance metrics
- automatic statistics refresh policy
- bounded automatic maintenance scheduling

`REBUILD INDEX ... ONLINE` remains deferred until concurrent-write correctness is proven.

Never silently reinterpret the blocking implementation as online.

---

# 7. P19 Skill — WORKFLOW / TRIGGER / SCHEDULE / TASK

NextSQL must use one coherent automation model:

```text
WORKFLOW
├── manual invocation
├── trigger invocation
└── scheduled invocation
      ↓
     TASK
```

## 7.1 WORKFLOW

Implement a native workflow object with:

- `CREATE WORKFLOW`
- typed parameters
- bounded multi-statement body
- `ALTER WORKFLOW`
- `DROP WORKFLOW`
- `RUN WORKFLOW`
- documented transaction semantics
- documented database-isolation semantics
- workflow RBAC
- audit events
- dependency tracking

A WORKFLOW must be runnable directly without a trigger.

## 7.2 TRIGGER

Triggers should invoke workflows instead of creating a second procedure language.

Required trigger events:

- BEFORE INSERT
- AFTER INSERT
- BEFORE UPDATE
- AFTER UPDATE
- BEFORE DELETE
- AFTER DELETE

Required protections:

- trigger recursion limit
- workflow recursion limit
- cycle detection
- statement limit
- time limit
- memory limit
- task limit
- deterministic replication behavior

## 7.3 SCHEDULE

Implement native scheduled workflow invocation.

Core surface:

- `CREATE SCHEDULE`
- `EVERY duration`
- `AT timestamp`
- `ALTER SCHEDULE`
- `DROP SCHEDULE`

Requirements:

- durable catalog
- Raft-aware authoritative dispatcher
- documented failover semantics
- no unintended duplicate firing within documented guarantees
- documented clock-skew behavior

Cron syntax is deferred until the core scheduler is proven.

## 7.4 TASK runtime

Every asynchronous/scheduled execution should become a durable TASK.

Task states:

```text
PENDING
RUNNING
SUCCEEDED
FAILED
CANCELLED
RETRYING
```

Track:

- task ID
- workflow/source metadata
- trigger metadata
- admitted database identity (legacy tenant fields remain empty for new tasks)
- attempts
- error metadata
- timeout
- retry count
- backoff
- idempotency key
- final/dead-letter semantics
- concurrency policy
- history retention

Expose tasks via `SHOW TASKS`, `system.tasks`, or the final canonical system surface.

Support cancellation.

Never use unbounded task worker pools.

### P19 exit requirements

Do not close P19 until:

- manual workflow execution is durable/ACID per documented semantics;
- triggers are bounded and cycle-safe;
- schedules survive restart and failover;
- tasks are observable and cancellable;
- resource budgets are enforced;
- RBAC and database isolation are adversarially tested;
- `docs/workflows.md` is complete.

---

# 8. P20 Skill — CDC / Change Streams

Build native CDC from committed WAL.

Required capabilities:

- emit committed transactions only
- stable ordering semantics
- INSERT/UPDATE/DELETE metadata
- table/database identity
- primary-key identity
- transaction/commit identity where safe
- LSN/resume token
- restart/resume behavior
- filtering/subscription interface
- bounded buffering
- backpressure
- lag metrics
- RBAC
- database isolation
- TLS/network integration

Never expose uncommitted changes.

Never let a slow subscriber grow memory without bound.

---

# 9. P21 Skill — Native Table Partitioning

Add physical partitioning without weakening global SQL correctness.

Expected partition types:

- RANGE
- HASH
- LIST where justified
- LIST partitioning for application-defined locality

Required engineering:

- catalog metadata
- routing
- pruning
- DDL lifecycle
- indexes
- transactions
- WAL/recovery
- Raft
- backup/restore
- maintenance
- statistics
- optimizer costing
- partition-wise aggregation
- partition-wise joins

Legacy TENANT descriptors remain decoder/runtime compatibility only. Physical
partition locality never replaces realm/database authorization.

---

# 10. P22 Skill — Follower Reads / Read Scaling

Implement read scaling with explicit consistency semantics.

Possible modes should be clearly distinguished, such as:

- leader/linearizable read
- bounded-staleness follower read
- explicitly stale follower read

Requirements:

- routing policy
- replica health awareness
- lag awareness
- consistency contract
- transaction interaction
- failover behavior
- observability
- driver exposure

Never route a request to a follower if its requested consistency cannot be satisfied.

---

# 11. P23 Skill — Vector Engine 2.0

P23 is complete (production-gating sign-off 2026-08-31, `docs/vector.md`).
Shipped:

- F16 vectors
- I8 vectors
- bit vectors + HAMMING
- IVF
- IVF-PQ
- quantized HNSW
- sparse vectors/retrieval
- hybrid sparse+dense+BM25 retrieval

Documented follow-ons (not gate items): a `BITVECTOR`/Hamming `--vecquant` row,
a process-local IVF-PQ cache, a re-rank-free quantised HNSW mode, IVF/IVF-PQ/SPARSE
on partitioned tables, and SIMD/architecture-specific kernels only after profiling.

Every ANN optimization must report:

- recall@K
- p50/p95/p99
- QPS
- memory
- index size
- build time where relevant
- hardware context

Never improve a headline latency number by silently lowering recall.

---

# 12. P24 Skill — Full-text Search 2.0

P24 is complete (exit gate closed 2026-08-31). Preserve its Phase-10
BM25/phrase golden compatibility, query-expansion and 4096-term fuzzy
vocabulary bounds, multilingual quality fixtures, and analyzer-aware encrypted
recovery coverage.

Potential areas include:

- richer analyzers
- language handling
- ranking improvements
- query syntax
- highlighting
- index/runtime optimization

Compatibility with already-shipped behavior must be tested.

---

# 13. P25 Skill — Security 2.0

Implemented in P25: mTLS / service identity / certificate + trust rotation /
X.509 CRL revocation, and signed short-lived credentials (`NSSC1.` Ed25519
credential in place of the password; expiry + audience/database/realm/role
scope; `NSTK` rotatable keyset; `NSTR` fail-closed revocation; `SIGHUP` reload;
`nextsql token` CLI; `identity_source` audit).

Remaining security work:

- external identity provider integration
- field-level client encryption
- stronger password hashing evolution
- audit hardening

Security rules:

- fail closed;
- least privilege by default;
- no secrets in logs;
- no raw keys in URLs;
- no impossible “unhackable” claims;
- document live unlocked-host limitations;
- preserve key revocation/rotation semantics;
- retain realm/database isolation through every new surface.

---

# 14. P26 Skill — System Catalog / Introspection 2.0

Create a coherent native introspection surface for:

- databases
- schemas
- tables
- columns
- indexes
- constraints
- users
- roles
- grants
- sessions
- queries
- locks
- transactions
- replication
- backups
- maintenance
- workflows
- triggers
- schedules
- tasks
- CDC
- partitions
- vector/full-text structures

Prefer stable system views/tables over ad-hoc one-off diagnostic commands where possible.

All introspection must obey permissions and realm/database boundaries.

---

# 15. P27 Skill — Operational Maturity / Workload Governance

Add production governance around the engine.

Key areas:

- server lifecycle
- graceful shutdown
- connection draining
- session controls
- query/session cancellation
- resource groups
- CPU quotas
- memory quotas
- concurrency limits
- workload prioritization
- operational CLI
- diagnostics
- safe maintenance controls

The system must degrade through queueing/rejection/cancellation rather than OOM or unbounded goroutine growth.

---

# 16. P28 Skill — Professional Installer + NextSQL Manager

Keep product separation clear.

## Installer

The installer is responsible for:

- installation
- initialization
- upgrade
- uninstall
- service registration
- key/bootstrap configuration
- platform checks
- diagnostics

Professional UX should be:

- user-friendly
- explicit about destructive actions
- accessible
- keyboard-friendly
- clear about progress and errors
- safe for production

Support appropriate Linux/server targets first according to the roadmap; do not pretend unsupported platforms are production-ready.

## NextSQL Manager

Manager is the operational administration product.

Expected areas include:

- instance overview
- health
- configuration
- storage
- backups
- restore
- HA/replication
- users/security
- logs/metrics
- maintenance
- upgrades

Manager must use public/native NextSQL APIs rather than secret privileged shortcuts.

---

# 17. P29 Skill — NextSQL Studio

NextSQL Studio is the web-based professional database development interface.

Core product goals:

- first-class NextSQL experience
- no PostgreSQL/MySQL emulation
- fast database exploration
- professional SQL editing
- explain/profiling
- native multimodel tooling
- operational developer workflows

Major surfaces:

## Shell / UX

- workspace navigation
- connection context
- database context
- tabs
- command palette
- keyboard shortcuts
- responsive layouts
- accessible interactions

## Connection manager

- secure connection profiles
- TLS
- key-provider integration
- no secret leakage
- connection test
- failure diagnostics

## Database explorer

- databases
- schemas
- tables
- columns
- indexes
- constraints
- views/system objects as supported
- safe DDL tooling

## SQL editor

- syntax highlighting
- autocomplete
- schema awareness
- multiple tabs
- parameter support
- execution
- cancellation
- history
- formatting
- errors with source positions

## Results

- streaming results
- pagination/virtualization
- copy/export
- typed rendering
- bounded client memory
- safe data editing where supported

## EXPLAIN / Profiler

Visualize:

- operators
- estimates
- actuals
- timing
- CPU
- memory
- disk/spill
- workers
- indexes
- candidate generation
- reranking

## Native explorers

Studio should provide specialized experiences for:

- JSON
- full-text
- vector
- hybrid search
- geospatial
- workflows/tasks
- CDC

Studio must not bypass server RBAC or realm/database controls.

---

# 18. P30 Skill — NextSQL Intelligence + Built-in RAG

NextSQL Intelligence must be grounded in authoritative NextSQL context.

## 18.1 Principle

The LLM is an assistant, not a database authority.

Never let model output override:

- parser
- binder
- optimizer
- catalog
- RBAC
- realm/database policy
- server validation

## 18.2 Knowledge sources

Use versioned authoritative sources such as:

- official docs
- SQL grammar/capability metadata
- system catalog
- diagnostics
- EXPLAIN output
- current schema
- current editor selection
- current query/error
- operational metrics where authorized

Dogfood NextSQL’s own RAG/vector/full-text/hybrid capabilities where practical.

## 18.3 Context priority

Prefer context roughly in this order:

1. current selection/query
2. current error/plan
3. active schema/catalog
4. authorized runtime diagnostics
5. official version-matched docs
6. broader NextSQL knowledge base

## 18.4 Permission and privacy

Every retrieval/tool action must respect:

- current user identity
- current realm/database
- RBAC
- database scope
- table/column restrictions
- secret handling
- audit requirements

Do not send unauthorized schema/data to an external model provider.

## 18.5 Provider abstraction

Support provider abstraction rather than coupling the product to one LLM vendor.

Keep:

- provider configuration
- model selection
- token limits
- timeout/retry policy
- redaction/privacy controls
- audit metadata

outside core database correctness.

## 18.6 Safe tool layer

LLM tools should be typed and explicitly permissioned.

Prefer tools such as:

- inspect schema
- inspect explain plan
- retrieve docs
- inspect index metadata
- inspect authorized metrics
- draft SQL

Dangerous writes/DDL must not execute merely because generated by the model.

## 18.7 Prompt injection boundary

Treat retrieved database text, comments, rows, documents, and external content as **data**, not trusted instructions.

Never allow retrieved content to:

- change system policy
- broaden permissions
- reveal secrets
- invoke unauthorized tools
- override realm/database boundaries

## 18.8 Read-only by default

Intelligence should default to explanation, drafting, and read-only inspection.

Writes, DDL, security changes, destructive operations, and operational changes require explicit safe workflows and server-side authorization.

## 18.9 Studio Intelligence experiences

Planned assistants include:

- documentation assistant
- schema assistant
- SQL assistant
- performance assistant
- RAG assistant
- security assistant
- HA assistant
- workflow/CDC assistant

Include a RAG Playground for testing:

- retrieval
- chunking
- embeddings
- hybrid ranking
- metadata filtering
- vector/full-text behavior
- citations/context
- latency
- evaluation

---

# 19. Cross-Cutting Security Skill

Always verify:

- authentication
- authorization
- least privilege
- realm/database isolation
- secret redaction
- encrypted persistence
- secure temp/spill files
- input size/depth limits
- network TLS
- auditability
- revocation
- denial behavior
- malformed-input behavior

Security claim discipline:

Never write:

- “unhackable”
- “100% secure”
- “guaranteed zero downtime”
- “impossible to lose data”

Prefer precise statements tied to threat models and tested failure classes.

---

# 20. Cross-Cutting Resource Safety Skill

Never introduce:

- unbounded goroutines
- unbounded allocations from user input
- unbounded result buffering
- unbounded task queues
- unbounded recursive SQL/workflow execution
- unbounded subscriber buffers
- unbounded maintenance work

Use:

- scheduler pools
- admission control
- memory accounting
- CPU/worker budgets
- I/O budgets
- execution timeouts
- streaming
- backpressure
- cancellation
- spill
- bounded queues

Overload should cause controlled rejection/throttling/cancellation, not OOM.

---

# 21. Cross-Cutting Testing Skill

For every new phase or major feature, consider all applicable layers:

```text
unit
integration
restart
crash injection
WAL/recovery
transaction
concurrency
race detector
fuzz
Raft/failover
backup/restore
PITR
RBAC
realm/database isolation
prepared statements
driver/wire protocol
resource-limit tests
benchmark
documentation
```

Adversarial tests are required where security, permissions, realm/database boundaries, recursion, parser input, or network input is involved.

Do not rely exclusively on happy-path tests.

---

# 22. Cross-Cutting Compatibility Skill

NextSQL is a native database.

Use `docs/standards.md` as the standards baseline: ISO/IEC 9075:2023 and its
SQL/CLI, SQL/PSM, SQL/MED, SQL/Schemata, SQL/MDA, and SQL/PGQ parts; ISO/IEC
9579:2000 RDA principles; TCP with TLS 1.3; and Unicode/UTF-8. A design
reference is not a conformance or shipped-feature claim. Record explicit
feature mappings, deviations, and tests before claiming conformance.

When adding behavior:

- define NextSQL-native semantics;
- document intentional differences;
- do not blindly copy PostgreSQL/MySQL;
- do not add hidden compatibility hacks;
- keep the native wire protocol authoritative;
- update official drivers together with server behavior when required.

Existing NextSQL behavior should remain backward-compatible unless a deliberate versioned breaking change is approved.

Persistent formats and wire formats must be versioned.

---

# 23. Go Engineering Skill

The engine is implemented primarily in Go.

Follow these rules:

- prefer standard library and proven libraries for critical primitives;
- use typed errors on public paths;
- avoid stringly-typed error contracts;
- use context/cancellation where appropriate;
- avoid hidden goroutine ownership;
- make concurrency bounded and observable;
- keep unsafe code isolated;
- use `unsafe` or SIMD only after profiling, tests, fuzzing, and measured benefit;
- make on-disk encoding explicit rather than relying on Go memory layout;
- keep secrets out of logs;
- keep deterministic code paths deterministic.

Run applicable checks such as:

```bash
go test ./...
go test -race ./...
```

and targeted fuzz/benchmark commands for modified packages.

---

# 24. SQL/DDL Implementation Workflow

When implementing a new SQL feature, generally work through:

```text
grammar
→ lexer/parser/AST
→ binder/type checking
→ catalog/dependency model
→ logical plan
→ optimizer rules/costing
→ physical plan
→ executor
→ transactions/WAL
→ recovery
→ replication
→ RBAC/realm/database checks
→ protocol/driver exposure
→ EXPLAIN
→ metrics
→ tests/fuzz
→ docs
```

Not every feature needs every layer, but never skip a relevant layer merely to make syntax pass.

---

# 25. Storage Feature Workflow

When adding a persistent structure:

1. Define versioned format.
2. Define corruption validation.
3. Define encryption domain/envelope.
4. Define allocation/reclamation.
5. Define WAL semantics.
6. Define crash/restart semantics.
7. Define backup/restore behavior.
8. Define PITR behavior if relevant.
9. Define replication behavior.
10. Define migration/upgrade behavior.
11. Add fuzz tests for decoders.
12. Add integrity diagnostics.
13. Add benchmarks if performance-sensitive.

Never create an unversioned persistent format.

---

# 26. Distributed Feature Workflow

When adding cluster-visible behavior:

- define leader authority;
- define deterministic replicated state;
- define quorum behavior;
- define failure behavior;
- define retry/idempotency semantics;
- define leader failover behavior;
- define duplicate prevention where applicable;
- define clock assumptions;
- test partitions;
- test leader kill;
- test follower repair;
- test rolling maintenance.

Do not rely on wall-clock timing alone for correctness.

---

# 27. Performance Targets and Known Measurements

Treat performance values as measured results tied to hardware/context, not universal guarantees.

Existing tracked targets/results include:

- cached PK lookup target p50 < 0.5 ms
- indexed query target p95 < 3 ms
- 25K rows < 1 s
- 1M optimized aggregation < 1 s
- 10M optimized aggregation < 5 s
- 100M analytical workload < 30–60 s
- 1M HNSW top-10 p95 target < 25 ms **with recall**

Known measurements in the tracker include successful 25K/1M/10M/100M analytical runs and high-throughput bulk DML runs.

The corrected 1M-vector v10 gate is green at p95 8.061 ms, recall@10 1.000,
and recall@100 0.998. Preserve that latency/recall pairing in every published
ANN result.

---

# 28. Product Family

The intended NextSQL product family is:

```text
NextSQL Engine
NextSQL CLI
Official NextSQL Drivers
NextSQL Manager
NextSQL Studio
NextSQL Intelligence
```

These products should share native APIs and semantics rather than each inventing separate behavior.

The engine remains authoritative.

---

# 29. Agent Change Checklist

Before modifying code:

- [ ] Identify the owning phase.
- [ ] Check whether an earlier release gate is open.
- [ ] Identify persisted/wire/catalog format impact.
- [ ] Identify transaction/WAL/recovery impact.
- [ ] Identify Raft/failover impact.
- [ ] Identify RBAC/realm/database impact.
- [ ] Identify resource-abuse risks.
- [ ] Identify driver/API impact.
- [ ] Identify documentation impact.
- [ ] Identify benchmark requirements.

Before claiming completion:

- [ ] Code compiles.
- [ ] Unit/integration tests pass.
- [ ] Race tests pass where applicable.
- [ ] Restart/crash tests pass where applicable.
- [ ] Fuzz coverage added for new untrusted decoders/parsers.
- [ ] RBAC/realm/database tests pass.
- [ ] Resource limits are tested.
- [ ] Distributed behavior is tested where applicable.
- [ ] Benchmarks include required correctness metrics.
- [ ] Docs are updated.
- [ ] Exit gate is actually satisfied.

---

# 30. Current Execution Directive

P0–P24 are complete. Continue with P25 Security 2.0 in dependency order:

```text
1. Audit designed vs implemented vs tested vs production-gated security state
2. Implement the smallest coherent mTLS/service-identity increment
3. Fix any correctness, durability, security, or isolation regression first
4. Close P25 only when its exit gate is actually green
```

Do not begin later feature work in a way that destabilizes completed release gates.

---

# 31. Final Rule

When uncertain, choose the implementation that is:

```text
more correct
more durable
more secure
more observable
more bounded
more testable
more explicit
```

even if it is initially slower or less feature-rich.

NextSQL should earn performance and features **after** correctness.
