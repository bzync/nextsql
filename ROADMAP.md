# NextSQL Roadmap

> Human-readable roadmap summary.
>
> This file is **not authoritative for status**.
>
> `TODO.md` is the source of truth for implementation status, sequencing, dependencies, and gates.  
> This file is a simplified, non-authoritative view derived from it.

---

## Current State

```text
P0–P15  complete
P16      open — correctness / SLO closure
P17      complete except REBUILD INDEX ... ONLINE deferred
P18      implementable scope complete
P19      active — v1 implementation complete; final full-suite gate open
P20      complete — native committed CDC streaming, images, retention, RBAC, and failover verified
P21      active — bounded single-column RANGE/HASH/LIST/TENANT DDL, routing, pruning, and recovery slice landed; lifecycle breadth open
P22–P30 planned/open
```

---

## Immediate Gate — P16

Required:

1. corrected 1M HNSW validation;
2. p95 target satisfied;
3. recall reported;
4. randomized 100M B+Tree invariant soak;
5. no unresolved correctness regressions.

The memory-bounded corrected 1M-vector v10 measurement is green. The remaining
100M B+Tree soak is explicitly paused after bounded v10 was stopped before its
first checkpoint; P16 remains open.

---

## P19 — Automation

Manual synchronous `WORKFLOW`, synchronous row `TRIGGER`, native schedules,
and the bounded durable `TASK` runtime are implemented. Targeted functional,
race, fuzz, PITR, Raft failover, and TLS-driver gates pass. P19 remains open
until one repository-wide functional invocation exits cleanly; the 2026-08-25
serial rerun passed every package except the storage B+Tree package, which hit
Go's 10-minute package timeout in a durability `fdatasync` path.

```text
WORKFLOW
├── manual
├── trigger
└── schedule
      ↓
     TASK
```

---

## P20 — CDC

The committed-WAL core provides versioned changes, commit-only
ordered transaction delivery, bounded pull backpressure, native SQL/NSQL
streaming, table/tenant filtering, commit-LSN resume, explicit history expiry,
runtime RBAC revocation, audit, cancellation, and prepared-driver support.
Active streams also pin their live-WAL horizon through pruning. Key-only is the
default and durable per-table FULL images are explicitly bounded. Safe
operation predicates, restart, and three-voter leader-failover resume are
covered. Process diagnostics expose bounded
CDC activity, delivery, error, and lag counters.

Target surface:

- ordering;
- resume tokens;
- backpressure;
- tenant/RBAC enforcement.

---

## P21 — Partitioning

The bounded, versioned `NSCT` v4 catalog descriptor and a tested
single-column RANGE/HASH/LIST/TENANT DDL, routing, pruning, and recovery slice
are implemented. Attach/detach/drop lifecycle semantics, partition-local indexes, and broader
maintenance/backup coverage remain open.
Planned shipped modes:

- RANGE;
- HASH;
- LIST where justified;
- tenant-aware partitioning.

Unlocks partition-wise aggregation/join work.

---

## P22 — Follower Reads

Read scaling with explicit consistency:

- leader/linearizable;
- bounded staleness;
- explicitly stale.

---

## P23 — Vector Engine 2.0

Planned:

- F16;
- I8;
- bit vectors;
- IVF;
- IVF-PQ;
- quantization;
- sparse retrieval.

---

## P24 — Full-Text Search 2.0

Planned:

- richer analyzers;
- language support;
- ranking improvements;
- highlighting;
- runtime/index optimizations.

---

## P25 — Security 2.0

Planned:

- mTLS;
- service identity;
- short-lived credentials;
- external identity providers;
- field-level client encryption;
- password-hash evolution;
- audit hardening.

---

## P26 — System Introspection 2.0

The virtual `system` schema core is implemented with stable columns and
permission-aware redaction. Live activity views and SHOW aliases remain open.

Stable native introspection for:

- schema;
- sessions;
- locks;
- replication;
- backups;
- maintenance;
- automation;
- CDC;
- partitions;
- vector/full-text structures.

---

## P27 — Workload Governance

Planned:

- resource groups;
- CPU/memory quotas;
- concurrency limits;
- workload priorities;
- graceful draining;
- improved diagnostics.

---

## P28 — Installer + Manager

Professional lifecycle management and operational UI.

---

## P29 — NextSQL Studio

Web-based professional database development interface with:

- SQL editor;
- explorer;
- profiling;
- multimodel tools;
- workflow/task/CDC tooling.

---

## P30 — NextSQL Intelligence

Built-in, permission-aware RAG/AI assistance for:

- docs;
- schema;
- SQL;
- performance;
- security;
- HA;
- workflows;
- CDC.

AI remains optional and non-authoritative.

---

## Beyond Core P30

Not part of the committed core roadmap:

- multi-primary writes;
- automatic distributed sharding;
- autonomous shard placement.

Any future work here requires separate production gating.
