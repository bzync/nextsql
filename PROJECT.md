# NextSQL — Canonical Project Specification

> **Purpose:** This document defines the complete expected NextSQL product after the development roadmap in `TODO.md` is implemented and production-gated.
>
> `TODO.md` is the execution tracker and source of implementation status.
>
> `TODO.md` also defines sequencing, dependencies, and phase gates.
>
> `SKILLS.md` defines agent engineering behavior.
> **`PROJECT.md` defines the finished product contract.**
>
> A feature described here is part of the intended final product unless explicitly marked **Deferred**, **Optional**, **Research**, or **Experimental**. Its presence in this document does not mean it is already implemented; implementation truth remains in `TODO.md`.

---

# 1. Product Identity

**NextSQL** is a high-performance, secure-by-default, encrypted-by-default, durable, native multimodel database platform written primarily in **Go**.

NextSQL is **not** a compatibility layer for:

- PostgreSQL
- MySQL
- MariaDB
- MongoDB
- Elasticsearch
- Redis
- external vector databases

NextSQL has its own:

- storage format
- SQL dialect
- wire protocol
- drivers
- optimizer
- transaction engine
- encryption architecture
- catalog
- multimodel execution engine
- administration interfaces
- development tooling

The final product combines:

```text
Relational SQL
+ Native Binary JSON
+ Full-Text Search
+ Vector Search
+ Geospatial
+ Hybrid Retrieval
+ WORKFLOW / TRIGGER / SCHEDULE / TASK
+ CDC / Change Streams
+ Native Partitioning
+ Read Scaling
```

under:

```text
one optimizer
one transaction model
one WAL/recovery model
one security model
one native protocol
```

The core architectural principle is:

> **One engine, one optimizer, one transaction model, one durability model, and one security model across every data modality.**

---

# 2. Product Family

The completed NextSQL product family consists of:

```text
NextSQL Engine
  Native database runtime:
  SQL + JSON + FTS + Vector + Hybrid + Geo + Workflow + CDC

NextSQL CLI
  Headless administration, automation, migrations, backup,
  restore, maintenance, diagnostics, cluster and security operations

NextSQL Bench
  Correctness-aware official benchmark and SLO measurement suite

NextSQL Drivers
  Go / Node.js / TypeScript / Bun / Deno / PHP
  plus future officially supported SDKs

NextSQL Installer
  Install / initialize / upgrade / repair / uninstall

NextSQL Manager
  Server / cluster / security / backup / maintenance / operations UI

NextSQL Studio
  Native NextSQL database development IDE

NextSQL Intelligence
  Version-aware, permission-aware, RAG-grounded assistant inside Studio
```

All products must use official NextSQL interfaces and server truth.

No GUI or AI layer may bypass:

- authentication
- RBAC
- tenant isolation
- catalog authority
- transaction rules
- server validation
- encryption rules

---

# 3. Non-Negotiable Engineering Priority

Always optimize in this order:

```text
1. Correctness
2. Durability
3. Security
4. Data integrity
5. Availability
6. Predictable latency
7. Throughput
8. Resource efficiency
9. Developer experience
10. Additional features
```

Reject any optimization that weakens:

- ACID correctness
- WAL durability
- fsync guarantees
- encryption
- authentication
- authorization
- checksums/integrity authentication
- replication safety
- crash recovery
- tenant isolation
- vector recall without disclosure

NextSQL must be fast **with production safety enabled**.

Official benchmarks keep enabled:

```text
fsync
WAL
encryption
checksums/authentication
MVCC
authentication
authorization
durability
```

unless explicitly labeled experimental and excluded from official SLO claims.

---

# 4. Complete Architecture

```text
                                NextSQL Clients
                                      │
                ┌─────────────────────┼─────────────────────┐
                │                     │                     │
             Drivers                 CLI              Studio/Manager
                │                     │                     │
                └─────────────────────┼─────────────────────┘
                                      │
                           Native NSQL Wire Protocol
                                      │
                                  TLS / mTLS
                                      │
                         Authentication / Identity
                                      │
                              RBAC / Tenant Policy
                                      │
                                  SQL Parser
                                      │
                              Binder / Catalog
                                      │
                           Logical Query Planner
                                      │
                        Adaptive Cost-Based Optimizer
                                      │
                         Vectorized / Parallel Executor
                                      │
        ┌───────────────┬─────────────┼──────────────┬───────────────┐
        │               │             │              │               │
        ▼               ▼             ▼              ▼               ▼
   Relational         JSON       Full-Text         Vector          Geo
     Engine          Engine        Engine           Engine          Engine
        │               │             │              │               │
     B+Tree        Binary JSON      Inverted        Flat /         Spatial
   / indexes       Path indexes     BM25/FTS        HNSW/IVF       Indexes
        │               │             │              │               │
        └───────────────┴─────────────┴──────────────┴───────┬───────┘
                                                             │
                                                     Hybrid Optimizer
                                                             │
                                                  Unified Transactions
                                                     MVCC + Locks
                                                             │
                                           ┌─────────────────┴─────────────┐
                                           │                               │
                                          UNDO                            WAL
                                           │                               │
                                           └─────────────────┬─────────────┘
                                                             │
                                                     Buffer / Storage
                                                             │
                                                  Authenticated Encryption
                                                             │
                                                         SSD / NVMe
```

Automation and change processing integrate with the same engine:

```text
WORKFLOW
├── manual RUN
├── TRIGGER
└── SCHEDULE
      │
      ▼
     TASK

Committed WAL
   │
   ▼
CDC / Change Streams
```

Distributed architecture:

```text
                    NextSQL Endpoint
                           │
               ┌───────────┴───────────┐
               │                       │
             Leader                Followers
               │                       │
               └───────────┬───────────┘
                           │
                        Raft Quorum
                           │
                 Synchronous Durability
```

Final intended distributed direction:

```text
single Raft leader for writes
+ synchronous quorum durability
+ followers
+ optional follower reads
+ native local partitioning
```

**Automatic distributed sharding and multi-primary writes are not part of the P30 core contract.**

---

# 5. Repository and Implementation Model

NextSQL is primarily implemented in Go.

Representative repository structure:

```text
nextsql/
├── cmd/
│   ├── nextsqld/
│   ├── nextsql/
│   └── nextsql-bench/
│
├── internal/
│   ├── protocol/
│   ├── auth/
│   ├── security/
│   ├── crypto/
│   ├── sql/
│   │   ├── lexer/
│   │   ├── parser/
│   │   ├── ast/
│   │   ├── binder/
│   │   ├── planner/
│   │   └── optimizer/
│   ├── catalog/
│   ├── executor/
│   ├── storage/
│   ├── txn/
│   ├── wal/
│   ├── recovery/
│   ├── json/
│   ├── fulltext/
│   ├── vector/
│   ├── geo/
│   ├── scheduler/
│   ├── workflow/
│   ├── task/
│   ├── cdc/
│   ├── partition/
│   ├── backup/
│   ├── replication/
│   ├── maintenance/
│   ├── metrics/
│   └── config/
│
├── drivers/
│   ├── go/
│   ├── node/
│   ├── bun/
│   ├── deno/
│   └── php/
│
├── installer/
├── manager/
├── studio/
├── intelligence/
├── tests/
├── docs/
└── go.mod
```

Package boundaries must remain narrow and explicit.

Avoid cyclic dependencies.

Do not serialize raw Go structs directly to disk.

All persistent formats and wire formats must be deterministic and versioned.

---

# 6. Native Multimodel Data Model

A single table may contain relational, JSON, vector, temporal, and geospatial data.

Example:

```sql
CREATE TABLE products (
    id          UUID PRIMARY KEY DEFAULT UUID(),
    tenant_id   UUID NOT NULL,
    name        STRING NOT NULL,
    description TEXT,
    price       DECIMAL(12,2),
    metadata    JSON,
    embedding   VECTOR<F32,1536>,
    location    POINT,
    created_at  TIMESTAMPTZ DEFAULT NOW()
);
```

Indexes can span different modalities:

```sql
CREATE INDEX ix_category
ON products(metadata.category);

CREATE FULLTEXT INDEX ix_description
ON products(description);

CREATE VECTOR INDEX ix_embedding
ON products(embedding)
USING HNSW;

CREATE SPATIAL INDEX ix_location
ON products(location);
```

Example relational query:

```sql
SELECT id, name, price
FROM products
WHERE price BETWEEN 1000 AND 5000;
```

Example JSON query:

```sql
SELECT *
FROM products
WHERE metadata.category = 'electronics';
```

Example full-text query:

```sql
SELECT *
FROM products
SEARCH description FOR 'wireless noise cancelling'
LIMIT 20;
```

Example vector query:

```sql
SELECT id, name
FROM products
NEAREST embedding TO $query
LIMIT 20;
```

Example hybrid query:

```sql
SELECT id, name, price
FROM products
WHERE metadata.category = 'headphones'
  AND price <= 15000
SEARCH description FOR 'wireless noise cancelling'
NEAREST embedding TO $query
LIMIT 20;
```

The optimizer must treat the hybrid query as **one physical planning problem**, not independent database calls.

---

# 7. Native SQL

NextSQL uses its own SQL dialect.

Its standards baseline is ISO/IEC 9075:2023, including SQL/CLI, SQL/PSM,
SQL/MED, SQL/Schemata, SQL/MDA, and SQL/PGQ as applicable design references.
ISO/IEC 9579:2000 RDA guides remote database protocol principles; remote
production transport uses TCP with TLS 1.3, and text uses Unicode/UTF-8. This
baseline does not itself claim formal conformance or replace the native NSQL
protocol. Planned standard areas are not shipped until `TODO.md`, capability
metadata, tests, and matching-version documentation say so. See
`docs/standards.md`.

The final SQL surface includes conventional relational operations while remaining NextSQL-native rather than compatibility-driven.

## 7.1 Core statements

```text
CREATE TABLE
ALTER TABLE
DROP TABLE
CREATE INDEX
DROP INDEX
REBUILD INDEX
INSERT
UPDATE
DELETE
SELECT
BEGIN
COMMIT
ROLLBACK
UPSERT
```

DDL and maintenance evolve according to the native catalog/storage model.

`REBUILD INDEX ... ONLINE` is only considered part of the final production surface when concurrent-write correctness is fully proven.

## 7.2 SELECT capabilities

Final SQL includes:

- `DISTINCT`
- `HAVING`
- `CASE`
- joins
- aggregations
- ordering
- limit/offset
- subqueries
- correlated subqueries
- derived tables
- CTEs
- recursive CTEs
- set operations
- window functions
- `RETURNING`

Set operations:

```text
UNION
UNION ALL
INTERSECT
EXCEPT
```

Window functions include:

```text
ROW_NUMBER
RANK
DENSE_RANK
LAG
LEAD
FIRST_VALUE
LAST_VALUE
COUNT OVER
SUM OVER
AVG OVER
MIN OVER
MAX OVER
```

## 7.3 DML ergonomics

Native atomic UPSERT must support unique-index conflict semantics.

Support:

```sql
INSERT ... RETURNING ...
UPDATE ... RETURNING ...
DELETE ... RETURNING ...
```

Results stream over NSQL rather than requiring full materialization.

## 7.4 Built-in function families

Final standard function surface includes:

### String

- `LOWER`
- `UPPER`
- `LENGTH`
- `SUBSTRING`
- `TRIM`
- `LTRIM`
- `RTRIM`
- `REPLACE`
- `CONCAT`
- `STARTS_WITH`
- `ENDS_WITH`
- `CONTAINS`

### Numeric

- `ABS`
- `ROUND`
- `CEIL`
- `FLOOR`
- `POWER`
- `SQRT`
- `MOD`

### NULL/value

- `COALESCE`
- `NULLIF`
- `GREATEST`
- `LEAST`

### Date/time

- `EXTRACT` or canonical NextSQL equivalent
- `DATE_TRUNC` or canonical equivalent
- `DATE_ADD`
- `DATE_DIFF`

### JSON

- `JSON_GET`
- `JSON_SET`
- `JSON_REMOVE`
- `JSON_CONTAINS`
- `JSON_ARRAY_LENGTH`
- `JSON_TYPE`

## 7.5 Advanced indexes

Final index capabilities include:

- clustered primary B+Tree
- secondary B+Tree
- UNIQUE
- covering indexes / `INCLUDE`
- partial indexes
- expression indexes
- JSON path indexes
- full-text indexes
- vector indexes
- spatial indexes

Index-only scans should be used where valid.

---

# 8. Query Optimizer

The optimizer is deterministic and cost-based.

Pipeline:

```text
SQL
 ↓
AST
 ↓
Binding
 ↓
Logical Plan
 ↓
Rewrite
 ↓
Physical Alternatives
 ↓
Cost Model
 ↓
Physical Plan
 ↓
Vectorized / Parallel Execution
```

It includes:

- predicate pushdown
- projection pushdown
- constant folding
- limit pushdown
- index selection
- join simplification
- join reordering
- column pruning
- partition pruning
- segment pruning
- top-N optimization
- covering/index-only selection
- partial-index implication
- expression-index matching
- subquery flattening
- subquery decorrelation
- CTE inline/materialize decisions
- hybrid structured/FTS/vector plan selection

Statistics include:

- row count
- NULL ratio
- NDV
- min/max
- histograms
- most common values
- correlation
- index selectivity
- segment statistics
- vector statistics
- runtime estimated-vs-actual feedback

The optimizer must not depend on an LLM.

---

# 9. Vectorized and Parallel Execution

Primary execution is batch/vector-oriented for suitable operations.

Representative batch sizes:

```text
1024
2048
4096
```

with benchmarks deciding actual defaults.

Support:

- vector filters
- vector projection
- batch decoding
- hash aggregation
- hash join
- merge join
- index scan
- parallel scan
- parallel aggregation
- parallel joins
- parallel index construction
- parallel vector distance computation

All work goes through explicit bounded schedulers.

Never spawn unbounded goroutines per query.

Every query has bounded:

- CPU workers
- memory
- disk spill
- I/O
- execution time
- result size

---

# 10. Streaming and Backpressure

Large query results must stream.

Never:

```text
materialize multi-GB result entirely in memory
→ then send
```

Instead:

```text
executor batch
→ protocol
→ client ACK/backpressure
→ next batch
```

Slow clients must not grow server memory without bound.

Cancellation must propagate through execution.

---

# 11. Storage Engine

Default logical page size:

```text
16 KiB
```

Persistent structures use deterministic versioned binary encoding.

Primary row storage uses clustered B+Tree organization.

Features include:

- slotted pages
- variable-length rows
- page validation
- page allocation
- buffer management
- B+Tree insert
- lookup
- delete
- range scan
- split
- merge
- rebalance
- root collapse
- page reclamation
- durable freelist
- orphan detection
- restart-safe reuse
- storage integrity checking

Known corrupted records must never be silently returned.

---

# 12. Storage Maintenance and Schema Lifecycle

Final engine includes native maintenance rather than permanent storage leakage.

Support:

```sql
DROP INDEX ...
REBUILD INDEX ...
MAINTAIN DATABASE;
MAINTAIN TABLE table_name;
MAINTAIN INDEX index_name;
```

Maintenance covers:

- dead row/version cleanup
- MVCC garbage eligibility
- UNDO cleanup/compaction
- B+Tree tombstone cleanup
- page reclamation
- freelist reuse
- full-text posting cleanup
- HNSW tombstone diagnostics/rebuild policy
- WAL retention respecting PITR
- statistics refresh

Maintenance is bounded by:

- CPU
- memory
- I/O
- concurrency
- admission control

At most bounded background/coordinated work may execute; no independent unbounded maintenance goroutine model.

---

# 13. Transactions and MVCC

NextSQL uses ACID transactions with undo-oriented MVCC.

Concept:

```text
Current Row
    │
    ▼
Undo Record
    │
    ▼
Previous Version
    │
    ▼
Older Version
```

Support:

- transaction IDs
- snapshots
- MVCC version chains
- locks
- rollback
- deadlock detection
- `READ COMMITTED`
- `SNAPSHOT`
- `SERIALIZABLE`

Serializable may only be advertised while anomaly tests prove the implemented guarantee.

Readers must not see uncommitted writes.

Rollback must restore prior state.

---

# 14. WAL and Crash Recovery

Write-ahead logging is mandatory.

Commit invariant:

> WAL representing a durable committed modification must reach the configured durability boundary before COMMIT is acknowledged.

Commit path:

```text
transaction
   ↓
WAL
   ↓
group commit
   ↓
fsync / quorum durability
   ↓
COMMIT acknowledgement
```

WAL includes:

- LSNs
- authenticated checksums
- encryption
- segments
- rotation
- group commit
- checkpoints
- archival hooks
- redo recovery

NextSQL must survive covered failures including:

- process crash
- SIGKILL
- machine restart
- checkpoint interruption
- partial WAL tail
- partial data write
- crash during B+Tree changes
- crash during index lifecycle
- crash during backup/maintenance metadata operations

After recovery:

- committed state remains;
- uncommitted state does not become committed;
- storage/index invariants remain valid.

---

# 15. Mandatory Encryption Architecture

Encryption is mandatory by default in production mode.

Persistent user data must not be stored in readable plaintext under normal production configuration.

Protect:

- table pages
- B+Tree structures
- secondary indexes
- JSON
- JSON indexes
- vectors
- ANN structures
- full-text indexes
- UNDO
- WAL
- temp files
- query spills
- snapshots
- backups
- archived WAL
- partition metadata/data
- workflow/task durable state
- CDC durable state where persisted

Security property:

> Stolen database files, disks, snapshots, WAL archives, backups, vector files, or full-text structures remain unreadable without separately authorized cryptographic key material.

---

# 16. Cryptography Rules

NextSQL may have a native encryption **architecture**, but must not invent proprietary cryptographic primitives.

Do not invent:

- cipher
- hash
- MAC
- KDF
- AEAD

Use reviewed algorithms and established libraries.

Initial authenticated encryption:

```text
AES-256-GCM
```

Cipher suites and persistent envelopes are versioned.

---

# 17. Key Hierarchy

Use envelope encryption.

```text
External / Client Root Authority
             │
             ▼
       Root Unlock Key
             │
             ▼
      Key Encryption Key
             │
             ▼
     Database Master Key
             │
   ┌─────────┼──────────┬──────────┬───────────┐
   ▼         ▼          ▼          ▼           ▼
 Page DEK   WAL DEK  Backup DEK  Vector DEK  FTS/Other DEKs
```

Additional separated domains can include:

- UNDO
- temp/spill
- replication
- task/workflow metadata
- Intelligence local secure state where necessary

Do not use one permanent key for all purposes.

---

# 18. Client-Held Key Mode

Support:

```text
REQUIRE CLIENT KEY
```

The critical root/unlock key need not live permanently on the server.

Conceptual flow:

```text
Application
   │
KeyProvider
   │
NextSQL Driver
   │
TLS
   │
nextsqld
   │
Authenticated unlock exchange
   │
temporary cryptographic context
   │
query execution
```

Persistent host files may contain:

- ciphertext
- wrapped DEKs
- key IDs
- crypto metadata

but not the raw external root key.

Never put encryption keys in connection URLs.

---

# 19. Key Rotation, Revocation, and Crypto-Shredding

Support online key rotation.

```text
old key version
→ generate new version
→ new writes use new key
→ bounded/background re-encryption
→ retire old key
```

Encrypted objects identify key version.

Support:

- session termination
- credential revocation
- key-version revocation
- wrapped-key rotation
- audit
- optional high-privilege crypto-shredding

Crypto-shredding warning:

```text
NO KEY = NO RECOVERY
```

---

# 20. Zero-Knowledge and Client-Side Field Encryption

Provide an optional stronger client-encryption mode.

Future/advanced syntax:

```sql
CREATE TABLE customers (
    id UUID PRIMARY KEY,
    email STRING ENCRYPTED CLIENT,
    phone STRING ENCRYPTED CLIENT,
    profile JSON ENCRYPTED CLIENT
);
```

For strongly client-encrypted fields:

- plaintext remains client-side;
- official drivers handle encryption/decryption;
- server-side arbitrary SQL over plaintext is unavailable unless explicitly supported by a documented leakage model;
- searchable encryption, if introduced, must document leakage.

Do not claim a live unlocked server can never expose plaintext from memory.

---

# 21. Authentication, Identity, and Security 2.0

Production remote connections require secure transport.

Core:

```text
TLS 1.3
```

Final Security 2.0 target includes:

## 21.1 mTLS / service identity

- server-side mTLS
- client certificate validation
- certificate-to-service identity mapping
- rotation
- revocation
- audit identity source

## 21.2 Short-lived credentials

Signed short-lived credentials/tokens support:

- expiration
- audience/database scope
- role scope
- tenant scope
- signing-key rotation
- revocation
- audit

## 21.3 External identity provider

OIDC integration may provide:

- external login
- identity mapping
- group/role mapping

External identity must never bypass NextSQL RBAC.

## 21.4 Password hashing

Password hash records are versioned.

Migration to stronger algorithms such as Argon2id may occur while preserving compatibility and safe rehash behavior.

## 21.5 Audit hardening

Support tamper-evident/signed audit mechanisms where production-gated.

---

# 22. RBAC and Tenant Isolation

Support:

- users
- roles
- grants
- revocation

Permission scopes include:

- cluster
- database
- schema
- table
- column
- function/workflow
- backup
- replication
- maintenance
- CDC
- administration
- Intelligence metadata/tool permissions where applicable

Least privilege is the default.

Tenant isolation is enforced server-side.

Cross-tenant leakage tolerance:

```text
0 known leakage
```

Physical tenant partitioning never replaces authorization.

---

# 23. Audit Logging

Audit at minimum:

- authentication success/failure
- role changes
- permission changes
- DDL
- backup/restore
- key operations
- cluster membership
- security settings
- workflow lifecycle/run
- schedule lifecycle
- task control
- CDC subscription/security actions
- maintenance
- high-risk Manager/Studio actions

Never log:

- passwords
- encryption keys
- tokens
- private keys
- secrets

---

# 24. Native JSON

JSON is stored in compact binary form, not merely raw UTF-8 text.

Support:

- typed scalars
- objects
- arrays
- path traversal
- partial decoding
- indexed JSON paths
- mutation functions
- containment
- type inspection

JSON participates fully in:

- transactions
- WAL
- recovery
- backup
- encryption
- replication
- partitioning
- hybrid optimization

---

# 25. Full-Text Search

Core FTS includes:

- tokenizer
- normalization
- inverted index
- posting lists
- term/document frequency
- positions
- BM25-style scoring
- phrase search

SQL:

```sql
SELECT *
FROM articles
SEARCH body FOR 'database performance'
LIMIT 20;
```

FTS is:

- transactional
- WAL-durable
- recoverable
- encrypted
- replicated

---

# 26. Full-Text Search 2.0

Final extended search target includes:

- stemming
- stop-word dictionaries
- versioned language analyzers
- synonyms
- prefix search
- fuzzy matching
- typo tolerance
- highlight/snippet generation
- multi-field search
- field weighting
- faceting/aggregation where architecturally appropriate
- analyzer/index options in DDL

Query expansion must have CPU/memory limits.

Analyzer metadata must participate in:

- transaction/WAL
- recovery
- replication
- encryption
- backup/restore

Analyzer behavior across replicas must remain deterministic.

---

# 27. Native Vector Engine

Vectors are first-class values.

Core type:

```text
VECTOR<F32,N>
```

Distances:

- COSINE
- L2
- INNER_PRODUCT

Core search:

- exact flat search
- HNSW

Example:

```sql
CREATE VECTOR INDEX ix_embedding
ON documents(embedding)
USING HNSW;
```

Large vectors are stored outside ordinary row pages by reference to avoid page bloat.

---

# 28. Vector Engine 2.0

Final advanced vector target includes production-gated support for appropriate subsets of:

- `VECTOR<F16,N>`
- `VECTOR<I8,N>`
- `BITVECTOR<N>`
- quantized HNSW
- IVF
- IVF-PQ
- quantization
- sparse retrieval
- dense+sparse+BM25 fusion

Every new vector representation has versioned encoding.

Every ANN structure is:

- encrypted
- crash recoverable
- transaction-aware
- delete-aware
- rebuildable
- Raft compatible

Every ANN configuration must report:

- recall@10
- recall@100
- p50/p95/p99
- QPS
- RAM
- index size
- build time
- database size

Never silently lower recall to improve latency.

Portable Go remains the correctness baseline.

SIMD/unsafe/architecture-specific code is introduced only after profiling, tests, fuzzing, and measured improvement.

---

# 29. Geospatial

Native geospatial support includes:

- `POINT` / `LOCATION`
- `BOX`
- `LINESTRING`
- `POLYGON`
- WKT coercion
- coordinate validation
- `LON`
- `LAT`
- `DISTANCE`
- `DISTANCE_SPHEROID`
- `DWITHIN`
- `WITHIN`
- `COVERS`
- line-length operations
- spatial indexes

Residual predicates remain exact.

Geo participates in:

- WAL/recovery
- MVCC
- encryption
- optimizer costing
- replication
- schema lifecycle

---

# 30. Hybrid Search

The optimizer understands combinations of:

```text
relational predicates
+ JSON paths
+ full-text
+ vectors
+ geospatial predicates
```

Possible plan:

```text
100M rows
   │
structured indexes
   ▼
3M
   │
additional filters
   ▼
250K
   │
ANN candidate generation
   ▼
1K
   │
BM25/vector rerank
   ▼
20
```

But the optimizer must also consider:

```text
ANN first
→ structured filtering
```

when cheaper.

Operator order is cost-based, not hard-coded.

---

# 31. WORKFLOW / TRIGGER / SCHEDULE / TASK

NextSQL uses a coherent programmable automation model instead of unrelated stored procedure/event subsystems.

```text
WORKFLOW
├── manual invocation
├── trigger invocation
└── scheduled invocation
      ↓
     TASK
```

## 31.1 WORKFLOW

Native workflow surface includes:

```sql
CREATE WORKFLOW ...
ALTER WORKFLOW ...
DROP WORKFLOW ...
RUN WORKFLOW ...
```

Workflows support:

- typed parameters
- bounded multi-statement bodies
- explicit transaction semantics
- tenant semantics
- RBAC
- audit
- dependency tracking
- recursion/resource limits

A workflow can run manually without a trigger.

## 31.2 TRIGGER

Native triggers execute workflows.

Events include:

```text
BEFORE INSERT
AFTER INSERT
BEFORE UPDATE
AFTER UPDATE
BEFORE DELETE
AFTER DELETE
```

Example conceptual syntax:

```sql
CREATE TRIGGER ...
AFTER INSERT
ON orders
RUN WORKFLOW process_order(...);
```

Trigger/workflow execution must enforce:

- trigger recursion depth
- workflow recursion depth
- cycle detection
- statement count
- time
- memory
- generated task count
- deterministic replication behavior

## 31.3 SCHEDULE

Native scheduler includes:

```sql
CREATE SCHEDULE ...
ALTER SCHEDULE ...
DROP SCHEDULE ...
```

Initial scheduling primitives:

```text
EVERY duration
AT timestamp
```

Schedules are stored durably.

In HA mode:

- one authoritative Raft-aware dispatcher exists;
- leader failover behavior is documented;
- duplicate execution is prevented within the documented guarantee;
- clock-skew behavior is explicit.

Cron-style syntax may be added only after the core scheduler is proven.

## 31.4 TASK Runtime

Scheduled/asynchronous execution is represented by durable tasks.

Task states:

```text
PENDING
RUNNING
SUCCEEDED
FAILED
CANCELLED
RETRYING
```

Task metadata includes:

- task ID
- workflow/source
- trigger source
- tenant
- attempts
- error details
- timeout
- retry count
- retry backoff
- idempotency key
- final/dead-letter semantics
- concurrency policy
- retention

Expose through a canonical machine-readable surface such as:

```sql
SELECT * FROM system.tasks;
```

and/or `SHOW TASKS`.

Tasks are cancellable and run in bounded worker pools.

---

# 32. CDC / Change Streams

NextSQL provides native committed change streaming sourced from WAL.

CDC emits **committed transactions only**.

Events include:

- INSERT
- UPDATE
- DELETE
- database/table identity
- primary-key identity
- transaction/commit identity where safe
- LSN/resume token
- relevant before/after metadata according to configured mode

CDC requirements:

- stable ordering semantics
- resume/restart behavior
- durable resume positions where applicable
- backpressure
- bounded buffers
- lag metrics
- cancellation
- RBAC
- tenant filtering
- secure transport
- failover semantics

A slow consumer must never create unbounded server memory growth.

CDC must not expose uncommitted data.

---

# 33. Native Table Partitioning

NextSQL supports native physical table partitioning.

Partitioning modes target:

```text
RANGE
HASH
LIST
TENANT
```

for the production-gated subset.

Partitioning includes:

- catalog metadata
- partition routing
- optimizer pruning
- partition-aware statistics
- indexes
- transaction correctness
- WAL/recovery
- Raft replication
- backup/restore
- PITR
- maintenance
- schema lifecycle

Optimizer must expose pruning in `EXPLAIN`.

After physical partitioning exists, enable:

- partition-wise aggregation
- partition-wise joins

## 33.1 Tenant partitioning

Native tenant routing may provide:

- tenant-local storage locality
- tenant-local B+Tree indexes
- tenant-local JSON indexes
- tenant-local FTS indexes
- tenant-local HNSW where beneficial

Tenant partitioning is a performance/storage feature, not an authorization mechanism.

No cross-tenant result may be returned.

Automatic distributed sharding remains deferred beyond the core roadmap.

---

# 34. Follower Reads and Read Scaling

Writes remain single-leader.

NextSQL supports explicit read consistency modes.

Required concepts:

## Strong reads

Strong reads use:

- leader execution
- or a valid Raft read barrier/proven equivalent

and satisfy the documented consistency guarantee.

## Follower/stale reads

Optional follower-read modes may include:

- eventual/stale
- bounded staleness
- `MAX STALENESS` semantics if adopted

Routing considers:

- read-only eligibility
- follower health
- replica lag
- requested consistency
- transaction context
- read-after-write expectations

Writes never silently route to stale followers.

Stale reads must never be labeled strong.

Drivers expose supported routing/consistency metadata.

---

# 35. Native NSQL Protocol

NextSQL uses a versioned native wire protocol.

Support:

- protocol negotiation/versioning
- TLS 1.3
- authentication
- typed parameters
- prepared statements
- streaming results
- backpressure
- query cancellation
- packet-size limits
- SQL-size limits
- result limits
- runtime/memory/worker limits
- capability negotiation

Official drivers use the same protocol.

Network input is untrusted.

Never allocate directly from unchecked attacker-controlled lengths.

---

# 36. Official Drivers

Official drivers target:

- Go
- Node.js
- TypeScript
- Bun
- Deno
- PHP

Drivers must support applicable server features such as:

- TLS
- mTLS when enabled
- secure credential handling
- `KeyProvider`
- typed parameters
- prepared statements
- streaming
- cancellation
- transactions
- consistency profiles
- tenant context
- workflow/task APIs
- CDC subscriptions
- client-side encrypted fields when production-gated
- capability/version negotiation

Keys never belong in connection URLs.

---

# 37. High Availability

NextSQL HA uses proven Raft consensus.

Minimum recommended voting topology:

```text
3 voting nodes
```

Support:

- replication
- leader election
- failover
- replica repair
- rolling maintenance
- quorum-loss handling
- synchronous quorum commit
- leader health
- cluster membership

If a safe leader cannot be identified:

```text
reject writes
```

rather than risk split brain.

No custom consensus algorithm.

---

# 38. Availability and Data-Loss Objectives

Do not advertise guaranteed 100% uptime.

HA design objective:

```text
>= 99.999% availability SLO
```

for properly configured supported clusters.

Failover engineering targets:

```text
leader election < 3 s
service recovery < 5 s
```

For acknowledged synchronous quorum commits under supported failures:

```text
RPO = 0
```

No acknowledged commit may be reported successful before the selected durability policy is satisfied.

---

# 39. Backups and PITR

CLI surface includes:

```bash
nextsql backup
nextsql restore
nextsql export
nextsql import
```

Backups remain encrypted.

Required backup flow:

```text
backup
 ↓
manifest
 ↓
integrity verification
 ↓
storage
 ↓
verification
 ↓
periodic restore test
```

A successful upload is not proof of a valid backup.

PITR uses:

```text
base backup
+ archived WAL
= point-in-time recovery
```

Restore targets include:

- timestamp according to documented semantics
- LSN

Backup/restore must understand all persistent production-gated structures, including future workflows, tasks, partitions, security metadata, and index formats.

---

# 40. Integrity and Corruption Handling

Known silent corruption tolerance:

```text
0
```

Use:

- authenticated page integrity
- WAL authentication/checksums
- backup verification
- version/format validation
- LSN validation
- index consistency checks
- structural invariants

Corruption handling:

```text
detect
→ isolate
→ fail safely
→ recover/repair where supported
```

Never return a known-corrupted record as valid.

---

# 41. Resource Safety and Workload Governance

Every query/task/maintenance operation must be resource-bounded.

Controls include:

- admission control
- bounded queueing
- throttling
- cancellation
- memory budgets
- CPU/worker budgets
- I/O budgets
- temp/spill budgets
- execution timeout
- result-size limits
- recursion limits
- query-complexity limits

Final workload-governance target includes:

- connection/session limits
- graceful shutdown
- connection draining
- query cancellation
- session termination
- resource groups/classes
- concurrency quotas
- workload prioritization
- operational diagnostics

Overload should cause:

```text
queue
throttle
reject
cancel
spill
```

not:

```text
unbounded goroutines
unbounded allocations
OOM
```

---

# 42. Canonical `system` Schema

NextSQL provides a machine-queryable virtual `system` schema as the canonical introspection interface.

It should expose authorized information about applicable objects such as:

- server/version
- capabilities
- databases
- schemas
- tables
- columns
- constraints
- indexes
- index status
- statistics
- sessions
- active queries
- transactions
- locks
- users
- roles
- grants
- tenants
- replication
- cluster nodes
- leader/followers
- replica lag
- backups
- WAL/PITR
- maintenance
- workflows
- triggers
- schedules
- tasks
- CDC subscriptions/status
- partitions
- full-text indexes/analyzers
- vector indexes
- security configuration
- audit status
- operational metrics

Important final capability object:

```text
system.capabilities
```

It is authoritative for:

- supported features
- experimental features
- deprecated features
- unsupported features
- feature/version metadata

Studio, Manager, drivers, and Intelligence must negotiate against server capabilities rather than assuming feature availability.

All system views obey RBAC and tenant boundaries.

---

# 43. Observability and Diagnostics

NextSQL exposes metrics and diagnostics for:

- queries
- latency
- QPS/TPS
- storage
- memory
- CPU
- WAL
- checkpoints
- cache/buffer behavior
- spills
- worker utilization
- replication
- failover
- replica lag
- backup
- maintenance
- index rebuild
- workflows
- tasks
- CDC
- partition pruning
- vector index behavior
- FTS behavior
- admission control
- security/authentication events

Diagnostics must not leak secrets.

---

# 44. EXPLAIN and EXPLAIN ANALYZE

Support:

```sql
EXPLAIN ...
EXPLAIN ANALYZE ...
```

Expose where applicable:

- operator
- estimated rows
- actual rows
- execution time
- CPU
- memory
- disk reads
- cache hits
- spill
- workers
- index
- partition pruning
- FTS candidate generation
- vector candidates
- hybrid reranking
- join strategy

Plans must be suitable for both CLI output and Studio visualization.

---

# 45. Migrations and Schema Lifecycle

NextSQL provides native migration/version workflow.

Migration tooling must understand native NextSQL DDL rather than translate through another database dialect.

Schema lifecycle includes safe handling of:

- tables
- indexes
- constraints
- workflows
- schedules
- partitions
- future persistent database objects

Migration validation must use parser/binder/catalog truth.

---

# 46. NextSQL Installer

The official installer provides a professional installation lifecycle.

Functions include:

- platform prerequisites
- install
- initialize
- data directory selection
- key/bootstrap configuration
- service registration
- start/stop validation
- repair
- upgrade
- rollback strategy where supported
- uninstall
- diagnostics

UX principles:

- clear and user-friendly
- safe defaults
- explicit destructive-action warnings
- accessible
- keyboard-friendly
- reliable progress/error reporting
- no plaintext secret storage
- production/readiness checks

Installer must not imply unsupported OS/platform combinations are production-ready.

---

# 47. NextSQL Manager

NextSQL Manager is the official operational administration UI, separate from Studio.

Primary responsibilities:

- server overview
- health
- configuration
- databases
- storage
- backups
- restore/PITR
- maintenance
- users
- roles
- grants
- encryption/key status
- audit
- HA/replication
- node/leader status
- replica lag
- workload controls
- logs/metrics/diagnostics
- upgrades
- workflow/task operational status
- CDC operational status
- partition status

Manager uses:

- official NSQL/API interfaces
- `system` schema
- server capability negotiation

Manager must never:

- read raw database pages directly
- read WAL files as a shortcut
- require the raw root unlock key
- bypass server RBAC
- display fake cluster/security state derived only from local UI assumptions

Server truth is authoritative.

---

# 48. NextSQL Studio

NextSQL Studio is the official NextSQL database development IDE.

It is separate from Manager.

Target users:

- developers
- DBAs
- data engineers
- AI/RAG developers
- backend engineers
- system architects

Core rule:

> **Do not build a generic SQL client with a NextSQL logo.**

Studio understands native:

- NextSQL SQL
- JSON
- FTS
- vectors
- hybrid search
- geospatial
- tenants
- workflows/tasks
- CDC
- partitions
- migrations
- query plans
- capabilities

Studio communicates only through official supported interfaces.

It never directly reads pages/WAL/catalog files.

---

# 49. Studio Shell and UX

Studio includes:

- professional IDE layout
- connection explorer
- editor workspace
- results panel
- plan panel
- messages panel
- statistics panel
- inspector
- command palette
- recent connections/projects
- light/dark/system themes
- layout persistence without secrets
- keyboard-first navigation
- accessibility baseline
- high-DPI support
- unsaved editor crash recovery

---

# 50. Studio Connection Manager

Connection profiles support:

- name
- host
- port
- database
- user
- TLS
- CA
- client certificate where supported
- tenant profile
- read-consistency profile
- secure saved credentials
- environment profile

Environment categories can include:

```text
development
test
staging
production
```

Production connections must be visually obvious.

Production safety features include:

- optional read-only default
- destructive DDL warnings
- parsed-AST warning for UPDATE/DELETE without WHERE
- capability-aware validation

Never store raw credentials in plaintext configuration files.

---

# 51. Studio Database Explorer

Studio provides lazy-loaded exploration of:

- databases
- schemas
- tables
- columns
- primary keys
- foreign keys
- constraints
- indexes
- statistics
- DDL
- dependencies
- workflows
- triggers
- schedules
- tasks
- CDC
- partitions

Design tools include:

- table designer
- index designer
- native DDL preview
- ER diagram from actual foreign-key metadata
- global object search

All metadata comes from authorized server introspection.

---

# 52. Studio SQL Editor

SQL editor includes:

- NextSQL-native syntax highlighting
- line numbers
- bracket matching
- indentation
- tabs
- execute statement
- execute selection
- execute script
- cancel query
- formatting
- find/replace
- live-catalog IntelliSense
- JSON-path completion
- vector-aware completion
- inline parser/binder diagnostics
- safe identifier suggestions
- query history with privacy controls
- saved queries
- folders/tags
- Git-friendly workspace artifacts

Editor diagnostics should use actual NextSQL parser/binder semantics where possible.

---

# 53. Studio Results and Data Editing

Results must support:

- streaming
- virtualization/paging
- typed rendering
- NULL distinction
- JSON inspection
- vector-aware display
- geospatial display where useful
- copy
- export
- bounded memory

Editable data grids must preserve:

- transaction safety
- primary-key identity
- concurrency behavior
- server validation
- RBAC

Studio must never simulate a successful edit when the server rejects it.

---

# 54. Studio Plan and Performance Tooling

Studio visualizes `EXPLAIN` / `EXPLAIN ANALYZE`.

Show:

- plan tree
- estimates vs actuals
- timings
- CPU
- memory
- disk
- cache
- spills
- workers
- indexes
- partition pruning
- FTS candidates/ranking
- vector candidates
- hybrid reranking

The plan UI must reflect server output rather than reconstruct an imagined plan client-side.

---

# 55. Studio Native Multimodel Explorers

Studio includes dedicated experiences for:

## JSON

- structured viewer
- path inspection
- JSON index awareness
- JSON function tooling

## Full Text

- analyzer/index inspection
- query testing
- ranking information
- highlights/snippets where supported

## Vector

- vector dimension/index inspection
- HNSW/ANN configuration
- recall/latency test support where appropriate
- index lifecycle visibility

## Hybrid

- structured + FTS + vector query construction/testing
- candidate/ranking inspection

## Geospatial

- point/shape inspection
- distance/filter testing
- index status

## Workflow/Task

- workflow editor
- trigger/schedule inspection
- run history
- task state/error/attempt view
- cancellation controls as authorized

## CDC

- subscription configuration/inspection
- resume token/lag visibility
- event viewer for authorized streams

---

# 56. NextSQL Intelligence

NextSQL Intelligence is the built-in context-aware assistant in Studio.

It is **not** a generic PostgreSQL/MySQL chatbot.

Its answer model is:

```text
actual connected server capabilities
+ matching-version official NextSQL docs
+ authorized live schema/catalog
+ current SQL/error/plan context
+ authorized metrics
→ controlled retrieval/context orchestration
→ optional AI model
→ grounded answer with sources
```

The deterministic engine must never depend on AI for:

- parsing
- binding
- optimization
- execution
- transactions
- WAL
- recovery
- encryption
- authorization
- Raft
- backup
- integrity

Studio remains fully usable if AI is disabled or unavailable.

---

# 57. Intelligence Capability Awareness

`system.capabilities` is authoritative.

Intelligence receives:

- server version
- NSQL version
- feature state
- version-added metadata
- deprecation metadata
- documentation references

It must not present planned features as installed features.

It must correctly handle:

- newer Studio + older server
- older Studio + newer server

---

# 58. Versioned Intelligence Knowledge Base

Studio packages/version-controls official NextSQL knowledge.

Corpus may include:

- PROJECT
- PLAN
- TODO
- official SQL docs
- optimizer docs
- execution docs
- JSON docs
- FTS docs
- vector docs
- geo docs
- MVCC docs
- WAL/recovery docs
- security docs
- protocol docs
- backup/restore docs
- operations docs
- HA docs
- workflow docs
- CDC docs
- partitioning docs
- maintenance docs

Knowledge chunks use stable hashes so only changed chunks need re-indexing after upgrades.

A dedicated internal database may be used:

```text
nextsql_intelligence
```

It must remain logically separate from user databases.

Retrieval uses:

- full-text/BM25
- vector retrieval when embeddings exist
- hybrid retrieval when both exist
- BM25-only fallback if embeddings are unavailable

Official docs retrieval filters against the connected server's version/capabilities.

---

# 59. Self-RAG / Dogfooding

NextSQL Intelligence should use NextSQL itself for its official knowledge retrieval where practical.

```text
NextSQL docs
→ NextSQL tables
→ native full-text index
→ native vector index
→ native hybrid optimizer
→ Studio Intelligence
```

No external vector database is required for the built-in knowledge base.

Measure:

- retrieval latency
- Recall@K
- MRR
- NDCG where applicable
- citation correctness

---

# 60. Intelligence Context Orchestration

Typed context includes concepts such as:

```text
ServerContext
CapabilityContext
DatabaseContext
SchemaContext
TableContext
IndexContext
QueryContext
PlanContext
ErrorContext
DocumentationContext
TenantContext
SecurityPolicyContext
```

Context priority:

```text
1. current selection/query
2. current error/plan
3. exact referenced schema objects
4. server capabilities
5. matching-version official docs
6. nearby related objects
7. broader NextSQL docs
```

The orchestrator must:

- select context by intent
- avoid dumping entire schemas
- deduplicate
- rerank
- compress
- enforce token budgets
- cache safely
- compact old conversation context

---

# 61. Intelligence Permissions and Privacy

AI context must respect exactly the same authorization boundaries as the user.

Never send to an external model provider:

- raw root keys
- passwords
- tokens
- private keys
- secrets
- unauthorized tables/columns
- cross-tenant rows
- fields marked with an AI-deny policy

Support explicit policy such as:

```text
AI_DENY
```

or final equivalent for columns/data that must never leave the local trust boundary.

Production policy should support metadata-only AI operation.

Users must be able to preview/redact context before external transmission where appropriate.

---

# 62. Intelligence Provider Abstraction

AI is provider-optional.

Maintain abstractions for:

- chat/completion provider
- embedding provider
- model selection
- token limits
- timeout
- retries
- credential storage
- provider privacy settings
- audit metadata

No single AI vendor may become a correctness dependency.

AI provider failure must not interrupt database operation or normal Studio usage.

---

# 63. Intelligence Tool Layer

AI actions use typed, permission-aware tools.

Examples:

- inspect capabilities
- inspect schema
- inspect table/index metadata
- retrieve matching-version docs
- inspect EXPLAIN
- inspect authorized metrics
- draft SQL
- validate SQL
- generate RAG schema/query
- explain workflow/CDC definitions

Generated SQL is not automatically trusted.

Dangerous actions require:

- explicit user intent
- server authorization
- production safety checks
- confirmation where applicable

---

# 64. Intelligence Prompt-Injection Boundary

Retrieved data is **data**, not trusted instructions.

Untrusted content includes:

- user table rows
- document text
- comments
- imported RAG documents
- external HTML/PDF content

Retrieved text must never be allowed to:

- change system policy
- broaden RBAC
- reveal secrets
- invoke unauthorized tools
- switch tenant
- override production safeguards

Maintain a prompt-injection test corpus.

---

# 65. Intelligence Assistant Modes

## SQL Assistant

Can:

- explain native SQL
- generate native SQL
- fix parser/binder errors
- explain query behavior
- propose safer rewrites

## Performance Assistant

Can:

- explain real plans
- compare estimates vs actuals
- identify scans/index choices
- recommend evidence-backed improvements

Never invent metrics.

## Schema Assistant

Can:

- explain schema
- suggest indexes
- explain dependencies
- propose native DDL

## RAG Assistant

Can:

- generate native RAG schema
- generate hybrid retrieval SQL
- inspect vector dimensions
- inspect FTS availability
- explain hybrid ranking
- recommend retrieval improvements from real metrics/configuration

## Security Assistant

Can explain permitted metadata for:

- TLS
- encryption
- RBAC
- audit
- rotation

It never retrieves raw keys.

## HA Assistant

Can explain:

- cluster status
- leader/follower state
- lag
- actual failover events

It must say when evidence is insufficient.

## Workflow / CDC Assistant

Can generate/review definitions only when supported by the connected server.

---

# 66. Intelligence Chat UX

Studio provides:

- dedicated chat panel/workspace
- visible connection
- visible database
- visible tenant
- visible server version
- context chips
- manual add/remove context
- selection actions: Explain / Fix / Optimize / Generate / Ask
- error “Ask NextSQL Intelligence”
- plan-node explanation
- vector-index explanation
- grounding indicators
- clickable citations
- redacted Markdown export

Grounding indicators may distinguish:

```text
Docs
Live Schema
Plan
Metrics
Capabilities
```

---

# 67. RAG Playground

RAG Playground is distinct from Intelligence.

```text
NextSQL Intelligence
→ helps developers use NextSQL

RAG Playground
→ helps developers build/test RAG systems using NextSQL
```

Provide a wizard to configure:

- knowledge table
- text column
- metadata columns
- vector column
- tenant column
- retrieval mode: FULLTEXT / VECTOR / HYBRID
- top-K

Generated schema/configuration must be transparent.

No hidden proprietary “magic” store.

Document ingestion target formats:

- TXT
- Markdown
- HTML
- PDF
- JSON
- CSV

Pipeline:

```text
parse
→ normalize
→ chunk
→ embed
→ insert
→ index
```

Chunking options include:

- character
- token
- paragraph
- heading-aware
- overlap

Store document/chunk lineage.

Validate vector dimensions.

Retrieval inspector exposes:

- source
- section
- metadata
- BM25 rank
- vector rank
- hybrid rank
- distance where meaningful
- source context

Evaluation includes:

- Recall@K
- MRR
- NDCG
- BM25 vs vector vs hybrid comparison
- retrieval latency
- embedding latency
- model latency
- token usage
- cache hits

Answers support source citations.

---

# 68. Deterministic RETRIEVER Research

After Studio RAG proves the model, NextSQL may research a native deterministic retrieval object such as:

```sql
CREATE RETRIEVER ...
RETRIEVE ... FOR $query;
```

A RETRIEVER, if adopted, encapsulates:

- table
- text column
- vector column
- filters
- retrieval mode
- top-K

It remains:

- deterministic
- transactional
- RBAC-aware
- tenant-aware

LLM generation remains outside `nextsqld`.

This is **Research** until explicitly production-gated.

---

# 69. Official Benchmark Suite

`nextsql-bench` measures at minimum:

- point SELECT
- range SELECT
- INSERT
- UPDATE
- DELETE
- transactions
- joins
- aggregations
- JSON
- full-text
- vector
- hybrid
- partition-aware workloads
- follower-read scaling where applicable

Report:

- QPS
- TPS
- p50
- p95
- p99
- p99.9
- CPU
- RAM
- allocations
- disk
- WAL
- database size
- index size
- encryption overhead
- hardware
- OS
- filesystem
- cache condition
- concurrency

Vector benchmarks also report recall.

---

# 70. Performance Objectives

Performance numbers are engineering targets and measured results, not universal guarantees.

## Cached primary-key lookup

```text
p50 < 0.5 ms
p95 < 1 ms
p99 < 3 ms
```

## Indexed query

```text
p50 < 1 ms
p95 < 3 ms
p99 < 5 ms
```

## Durable simple INSERT/UPDATE

Target:

```text
p50 < 2 ms
p95 < 5 ms
p99 < 10 ms
```

subject to storage durability latency.

## Row processing

Target classes:

```text
25K rows   < 1 s
100K rows  < 1 s for suitable optimized processing
1M rows    < 1 s for suitable scans/aggregations
10M rows   < 5 s for suitable optimized aggregation
100M rows  < 30–60 s for appropriate analytical workloads
```

## 1M-vector HNSW

```text
Top-10
p50 < 10 ms
p95 < 25 ms
p99 < 50 ms
```

must be reported with:

- recall@10
- recall@100
- QPS
- RAM
- index size

## Hybrid search

Initial target on appropriate indexed data/hardware:

```text
p50 < 50 ms
p95 < 100 ms
p99 < 250 ms
```

---

# 71. Fuzzing and Adversarial Testing

Continuously fuzz applicable untrusted inputs:

- SQL parser
- wire protocol
- authentication protocol
- page decoder
- WAL decoder
- backup parser
- export/import parser
- JSON parser
- vector metadata
- full-text structures
- replication command decoder
- workflow syntax/runtime edges
- CDC/resume metadata
- partition metadata
- new persistent format decoders

Malformed input must produce controlled errors.

---

# 72. Testing Contract

Every feature must receive applicable coverage across:

```text
unit
integration
transaction
concurrency
restart
crash injection
WAL/recovery
Raft/failover
backup/restore
PITR
RBAC
tenant isolation
prepared statements
wire protocol
official drivers
race detector
fuzz/property tests
resource-limit tests
benchmarks
documentation
```

A phase is not complete because code merely exists.

It is complete only after implementation, tests, docs, and its exit gate are green.

---

# 73. Product UX and Safety Contract

Applies to Installer, Manager, Studio, and Intelligence.

All user-facing products should provide:

- professional consistent design
- accessibility baseline
- clear errors
- actionable diagnostics
- keyboard-friendly interaction
- confirmation for destructive operations
- production environment visibility
- secret-safe storage
- server-authoritative status
- capability negotiation
- no fake success states
- no silent privilege escalation

---

# 74. Security Claim Discipline

Never advertise:

```text
100% secure
unhackable
guaranteed zero downtime
fastest database in the world
impossible to lose data
```

Use:

- engineering target
- measured benchmark
- design objective
- SLO
- supported failure model
- documented threat model

instead.

---

# 75. Persistent-Structure Contract

Every new persistent structure must document:

- format version
- encryption domain
- key version
- integrity/authentication
- backup behavior
- restore behavior
- PITR behavior where relevant
- rotation behavior
- replication behavior
- upgrade/migration behavior
- corruption behavior

No unversioned persistent format is allowed.

---

# 76. Final Success Contract

The complete NextSQL product succeeds when the production-gated roadmap provides:

```text
Native SQL relational engine
Native binary JSON
Native full-text search
Native vector search
Native geospatial
Unified hybrid optimizer
ACID MVCC transactions
WAL + crash recovery
Mandatory production encryption
Client-held key support
RBAC + tenant isolation
Backup / restore / PITR
Raft HA
Follower read scaling
Native table partitioning
Schema lifecycle + maintenance
Modern SQL completeness
WORKFLOW / TRIGGER / SCHEDULE / TASK
CDC / change streams
Canonical system introspection
Operational workload governance
Official drivers
Professional Installer
NextSQL Manager
NextSQL Studio
NextSQL Intelligence
RAG Playground
```

with long-term quality objectives:

```text
Persistent plaintext:
0 by default in production mode

Known silent corruption tolerance:
0

Known critical unresolved production vulnerabilities:
0 at a production release gate

Cross-tenant leakage tolerance:
0

Lost acknowledged synchronous quorum commits:
0 within supported failure assumptions

HA availability design SLO:
>= 99.999%

Leader election target:
< 3 seconds

HA service recovery target:
< 5 seconds

Cached point lookup:
p50 < 0.5 ms target

Indexed query:
p95 < 3 ms target

25K processed rows:
< 1 second target

1M suitable optimized aggregation:
< 1 second target

10M suitable optimized aggregation:
< 5 seconds target

100M analytical workload:
< 30–60 seconds target

1M-vector HNSW Top-10:
p95 < 25 ms target with recall reported

Encryption:
mandatory in production mode
```

---

# 77. Explicitly Deferred Beyond the Core P30 Product

The following are **not** required to consider the P30 product family complete:

## Deferred

- multi-primary writes
- automatic distributed sharding
- autonomous cross-node shard placement/rebalancing

## Rejected as core architecture

- LLM inside the deterministic query optimizer
- LLM required for database correctness
- LLM required for transactions/WAL/recovery/security
- mandatory cloud account for local NextSQL
- hidden PostgreSQL/MySQL compatibility engine

The preferred distributed model remains:

```text
single Raft write leader
+ synchronous quorum durability
+ followers
+ optional follower reads
+ local native partitioning
+ later explicit shard-placement research if justified
```

---

# 78. Source-of-Truth Relationship

Use the project files as follows:

```text
PROJECT.md
→ What the finished NextSQL product is expected to be.

TODO.md
→ What is implemented, open, blocked, deferred, or production-gated.

ROADMAP.md
→ Simplified, non-authoritative sequence derived from TODO.md.

SKILLS.md
→ How an AI coding agent must behave while working on NextSQL.

docs/*
→ Detailed technical specifications and measured implementation truth.
```

If `PROJECT.md` and `TODO.md` differ on implementation status:

```text
TODO.md wins for status.
PROJECT.md wins for intended end-state.
```

If a feature is only designed but not implemented/tested:

- keep it unchecked in `TODO.md`;
- do not claim it is shipped;
- it may still appear in `PROJECT.md` as part of the intended final product.

---

# 79. Final Guiding Principle

> **NextSQL must be fast without weakening correctness, encrypted without pretending custom cryptography is safer, highly available without risking split brain, multimodel without becoming several loosely coupled databases, intelligent without making AI a correctness dependency, and professional without allowing tooling to bypass the engine's security model.**

The finished system is not merely a database executable.

It is a complete native database platform:

```text
Engine
+ Protocol
+ Drivers
+ CLI
+ Bench
+ Automation
+ CDC
+ Partitioning
+ HA / Read Scaling
+ Security
+ Operations
+ Installer
+ Manager
+ Studio
+ Intelligence / RAG
```

all built around one authoritative NextSQL engine.
