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

## 5. Current P16 Gate

P16 remains open until:

- corrected 1M-vector HNSW run completes;
- p95 target is satisfied;
- recall is reported;
- randomized 100M B+Tree invariant soak completes;
- no unresolved correctness regression remains.

Known corrected 100K vector validation:

```text
p95       3.317 ms
recall@10 1.000
recall@100 0.999
```

This does not replace the 1M exit-gate run.

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
