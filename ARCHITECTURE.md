# NextSQL Architecture

> Canonical high-level architecture for the implemented and roadmap-aligned NextSQL system.
>
> `PROJECT.md` defines the intended product end-state.  
> `TODO.md` defines implementation/status truth.  
> This file defines how major components fit together structurally.

---

## 1. Architecture Principles

NextSQL is a native multimodel database with:

- one storage engine;
- one transaction model;
- one WAL/recovery model;
- one optimizer;
- one security model;
- one native protocol.

NextSQL is not a compatibility wrapper around PostgreSQL, MySQL, MongoDB, Elasticsearch, or an external vector database.

Priority order:

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

---

## 2. High-Level System

```text
Clients / CLI / Studio / Manager
            │
            ▼
     Native NSQL Protocol
            │
         TLS/Auth
            │
       RBAC/Tenant
            │
          Parser
            │
          Binder
            │
          Catalog
            │
     Logical Planner
            │
    Cost-Based Optimizer
            │
     Physical Planner
            │
  Vectorized/Parallel Executor
            │
 ┌──────────┼──────────┬───────────┬────────────┐
 │          │          │           │            │
Relational JSON     Full-Text    Vector       Geo
 │          │          │           │            │
 └──────────┴──────────┴───────────┴────────────┘
            │
         MVCC/Locks
            │
       UNDO + REDO WAL
            │
       Buffer Manager
            │
       Page Storage
            │
    Encrypted Persistence
```

---

## 3. Storage Engine

Core properties:

- 16 KiB logical pages;
- explicit versioned on-disk formats;
- page identity and validation;
- slotted variable-length records;
- clustered B+Tree;
- secondary indexes;
- safe page split/merge/rebalance;
- durable freelist;
- restart-safe page reuse;
- corruption fail-closed behavior.

Persistent Go structs must never be serialized by raw memory layout.

Every persistent format must define:

- version;
- validation;
- encryption envelope;
- upgrade/migration behavior;
- recovery semantics.

---

## 4. Transactions and MVCC

Transaction architecture includes:

- transaction IDs;
- snapshots;
- MVCC version chains;
- UNDO;
- row/key/range locking;
- deadlock detection;
- rollback;
- READ COMMITTED;
- SNAPSHOT;
- SERIALIZABLE where anomaly testing remains green.

Readers must never observe uncommitted writes.

---

## 5. WAL and Recovery

The WAL subsystem owns durability and crash recovery.

Key properties:

- LSN-based records;
- encrypted/authenticated WAL;
- segments;
- rotation;
- group commit;
- fsync before commit acknowledgement;
- checkpoints;
- REDO;
- partial-tail handling;
- crash injection;
- archive hooks for PITR.

A commit cannot be acknowledged before the configured durability boundary.

---

## 6. Encryption Architecture

```text
root unlock
    ↓
KEK
    ↓
database master
    ↓
domain-specific DEKs
```

Encryption domains may include:

- pages;
- WAL;
- UNDO;
- backups;
- vectors;
- full-text;
- temp/spill;
- replication.

Current page encryption uses AES-256-GCM.

Rules:

- use established cryptography only;
- no custom cryptographic primitive;
- no raw keys in URLs;
- no secrets in logs;
- persistent plaintext should remain zero by default in production mode.

---

## 7. SQL Pipeline

```text
SQL text
→ lexer
→ parser
→ AST
→ binder/type checking
→ catalog
→ logical plan
→ rewrite
→ cost optimizer
→ physical plan
→ executor
→ WAL/MVCC
```

New SQL features must integrate through the full applicable pipeline.

---

## 8. Query Optimizer

The optimizer remains deterministic.

Core responsibilities:

- predicate pushdown;
- projection pushdown;
- constant folding;
- LIMIT pushdown;
- join reordering;
- index selection;
- column pruning;
- statistics;
- selectivity estimates;
- plan caching;
- estimate-vs-actual diagnostics.

No LLM is part of the deterministic optimizer.

---

## 9. Execution Engine

The execution layer supports:

- columnar batches;
- vectorized filters/projections;
- hash aggregation;
- hash join;
- merge join;
- index scans;
- parallel scans;
- parallel aggregation;
- parallel joins;
- parallel index builds;
- bounded worker scheduling;
- bounded memory;
- spill;
- streaming;
- cancellation;
- backpressure.

Unbounded per-query goroutines are forbidden.

---

## 10. Native Multimodel Layer

### Relational

- clustered B+Tree;
- secondary indexes;
- joins;
- grouping;
- windows;
- subqueries;
- CTEs;
- set operations.

### JSON

- native binary representation;
- path traversal;
- partial decoding;
- path indexes.

### Full-Text

- inverted index;
- postings;
- positions;
- BM25-style ranking;
- phrase search.

### Vector

- `VECTOR<F32,N>`;
- exact flat search;
- HNSW;
- COSINE;
- L2;
- INNER_PRODUCT.

### Geospatial

- POINT / LOCATION;
- BOX;
- LINESTRING;
- POLYGON;
- distance/predicate functions;
- spatial indexes.

---

## 11. Hybrid Planning

Hybrid queries are one physical planning problem.

Examples:

```text
structured filter → ANN
ANN → structured filter
```

The optimizer chooses based on cost.

`EXPLAIN` should expose:

- candidate generation;
- filters;
- reranking;
- index choices;
- estimates/actuals.

---

## 12. Native Protocol

The native NSQL protocol supports:

- TLS 1.3;
- authentication;
- typed parameters;
- prepared statements;
- streaming;
- cancellation;
- packet limits;
- result limits;
- runtime limits;
- capability/version handling where required.

Official drivers must use this protocol rather than compatibility protocols.

---

## 13. High Availability

Current HA direction:

```text
        Leader
       /   |   \
      /    |    \
Follower Follower ...
```

Raft responsibilities:

- leader election;
- quorum commit;
- replicated state;
- failover;
- replica repair;
- rolling maintenance;
- quorum-loss write rejection;
- split-brain prevention.

Current targets:

```text
leader election < 3 s
service recovery < 5 s
```

Multi-primary writes are not part of the core roadmap.

---

## 14. Automation Architecture

Planned P19 model:

```text
WORKFLOW
├── manual invocation
├── trigger invocation
└── scheduled invocation
      ↓
     TASK
```

The design intentionally avoids introducing a second procedure language for triggers.

---

## 15. CDC Architecture

Planned CDC is derived from committed WAL only.

```text
Committed WAL
    ↓
CDC encoder
    ↓
bounded subscriber buffers
    ↓
consumer
```

CDC must preserve:

- committed-only events;
- ordering;
- resume position;
- backpressure;
- tenant/RBAC boundaries.

---

## 16. Partitioning and Read Scaling

Planned partitioning:

- RANGE;
- HASH;
- LIST where justified;
- tenant-aware partitioning.

Follower reads must expose explicit consistency semantics.

Strong reads cannot be silently served from replicas that cannot satisfy them.

---

## 17. Tooling Architecture

```text
NextSQL Engine
├── nextsqld
├── nextsql CLI
├── nextsql-bench
├── official drivers
├── Installer
├── Manager
├── Studio
└── Intelligence
```

The server remains authoritative.

Studio/Manager must use native public interfaces rather than reading raw pages/WAL directly.

---

## 18. Intelligence Boundary

NextSQL Intelligence is optional.

It may inspect authorized:

- docs;
- schema;
- EXPLAIN;
- metrics;
- index metadata;
- current query/error.

It must never override:

- parser;
- binder;
- optimizer;
- RBAC;
- tenant policy;
- server validation.

Retrieved text is data, not trusted instructions.

---

## 19. Source of Truth

```text
PROJECT.md      product end-state
TODO.md         implementation/status truth
TODO.md         implementation status, sequencing, dependencies, and gates
ROADMAP.md      simplified non-authoritative sequence
SKILLS.md       engineering contract
AGENTS.md       repository-agent instructions
ARCHITECTURE.md structural design
docs/*          subsystem implementation details
```
