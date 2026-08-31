# NextSQL Performance and Benchmark Policy

## Purpose

This document defines how NextSQL performance is measured and reported.

Performance must never take priority over correctness, durability, security, or integrity.

---

## 1. Priority

```text
Correctness
→ durability
→ security
→ integrity
→ availability
→ latency
→ throughput
→ efficiency
```

---

## 2. Official Benchmark Rules

Official measurements keep enabled:

- encryption;
- WAL;
- fsync;
- authentication;
- checksums/authentication;
- MVCC;
- normal durability.

If any of these are disabled, the result must be labeled experimental and must not be compared directly to production-mode figures.

---

## 3. Required Benchmark Metadata

Record:

- CPU;
- RAM;
- storage device;
- filesystem;
- OS/kernel where relevant;
- row width;
- dataset size;
- query;
- indexes;
- cache state;
- encryption mode;
- durability mode;
- concurrency;
- QPS/TPS;
- p50;
- p95;
- p99;
- p99.9 where meaningful;
- memory;
- disk usage;
- WAL usage.

For vector/ANN:

- recall@K;
- index size;
- build time;
- distance metric;
- search parameters.

---

## 4. Current Engineering Targets

Tracked targets include:

```text
cached PK lookup p50 < 0.5 ms
indexed query p95 < 3 ms
25K-row workload < 1 s
1M optimized aggregation < 1 s
10M optimized aggregation < 5 s
100M analytical workload < 30–60 s
1M HNSW top-10 p95 < 25 ms with recall reported
```

These are targets, not universal guarantees.

---

## 5. P16 Gate (closed)

P16 is complete (paper-closed 2026-08-30). Exit gate, all satisfied:

- corrected 1M-vector HNSW v10: p95 **8.061 ms**, recall@10 **1.000**,
  recall@100 **0.998**;
- 10M DELETE published, crash-during-merge `Check()`-clean;
- 100M analytics `< 60 s`; 10M INSERT/UPDATE published;
- no unresolved correctness regression.

The terminal randomized 100M-operation B+Tree invariant soak is a deferred
standalone measurement, not a release gate (same disposition as P18); v8
reached 44M clean operations. The current release gate is P22 follower reads.

---

## 6. Vector Benchmark Rule

Never improve a headline ANN latency result by silently lowering recall.

Always present latency and recall together.

---

## 7. Regression Policy

Performance changes should be compared against a reproducible baseline.

Investigate regressions in:

- p95/p99;
- throughput;
- allocations;
- memory;
- WAL volume;
- disk growth;
- index size;
- recovery time;
- recall.

---

## 8. Benchmark Tool

Use:

```bash
nextsql-bench --quick
nextsql-bench --slo
```

Workload categories may include:

- point;
- range;
- insert;
- update;
- delete;
- transaction;
- join;
- aggregation;
- JSON;
- full-text;
- vector;
- hybrid.

---

## 9. Claims

Never present one machine's result as a product-wide guarantee.

Use wording such as:

```text
Measured on:
CPU:
RAM:
Storage:
Filesystem:
Dataset:
Concurrency:
Encryption:
Durability:
Result:
```
