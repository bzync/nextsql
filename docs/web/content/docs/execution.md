# Vectorized execution

NextSQL executes queries through a **columnar vectorized and parallel execution engine** (`internal/executor`, `internal/executor/vector`, `internal/scheduler`) that streams batches over the [native NSQL v1 wire protocol](/docs/protocol) under strict per-query resource budgets.

```arch
Physical Plan
  └── scheduler.Budget (CPU workers · 64 MiB RAM · 256 MiB encrypted spill · 1 GiB I/O · 30s timeout)
        └── Parallel / Partition-Wise Scan (concurrent page decrypt → columnar vector.Batch)
              └── Selection Vector Filter & Columnar Expression Projection
                    └── Hash / Merge / Semi / Anti Join (spills to AES-256-GCM NSPL on memory cap)
                          └── Vectorized Hash / Window Aggregation
                                └── Streamed Batches (NextBatch / Session.Stream)
```

---

## Columnar batches (`vector.Batch`)

Instead of materializing row-at-a-time Go structs across operators, the executor decodes storage rows into columnar `vector.Batch` slices:

* **Batch capacities:** `1,024` (default), `2,048`, or `4,096` rows per batch.
* **Selection vectors:** Filter predicates evaluate over columnar vectors and compact a selection vector without copying unreferenced column payloads.
* **Streaming delivery:** `Session.Query` and `Session.Stream` yield batches incrementally to the wire protocol encoder so large result sets do not require unbounded in-memory materialization.

---

## Per-query scheduler & resource budgets

Every query runs inside a bounded `scheduler.Budget` managed by the process-wide `scheduler.Pool`. NextSQL never spawns unbounded goroutines per query or partition:

| Resource | Default Limit | Config Override | Behavior on Exceed |
|---|---|---|---|
| **CPU workers** | `min(GOMAXPROCS, 8)` | Resource group / session limits | Tasks queue on the bounded worker pool |
| **Memory** | `64 MiB` | `max_query_memory_bytes` | Operator spills to disk (where supported) or fails closed with `exhausted` (never OOMs) |
| **Disk spill** | `256 MiB` | `max_query_spill_bytes` | Fails query with `exhausted` |
| **Logical I/O** | `1 GiB` | `max_query_io_bytes` | Fails query with `exhausted` |
| **Wall time** | `30 s` | `statement_timeout_ms` | Cancels execution and returns `exhausted` |

### Encrypted temporary spill files (`NSPL`)

When a hash join, semi/anti-join, sort, or window partition exceeds its memory budget, intermediate partitions spill to temporary disk files (`NSPL` format). Every spill file is encrypted with **AES-256-GCM** using an ephemeral per-query DEK (or the database's `DomainTemp` key domain) that exists only in RAM and is destroyed as soon as the query completes.

---

## Physical operators

| Operator | Execution Strategy |
|---|---|
| **Sequential & index scan** | Parallel heap scans split work across internal B+Tree separators so worker threads decrypt and decode pages concurrently. When a filter over a sequential scan is concurrency-safe (no subqueries or volatile functions), the predicate is evaluated inside the parallel scan workers so non-matching rows are discarded immediately. |
| **Covering index scan** | `IndexScan … covering` reconstructs rows directly from the index key, primary-key suffix, `INCLUDE` payload, and partial-index constants without touching the table heap. |
| **Bounded `MIN` / `MAX` probe** | Single-column and composite-prefix `MIN(col)` / `MAX(col)` aggregates execute via $O(\log N)$ B+Tree `FirstBounded` / `LastBounded` leaf seeks with zero intermediate batch allocations. |
| **Hash join (`INNER`, `LEFT`, `FULL`)** | Builds an in-memory hash table on the right (smaller) input and probes from the left input. Supports up to 8 joined tables per statement. |
| **Hash semi / anti-join** | Executes flattened `EXISTS` / `IN` and `NOT EXISTS` / null-safe `NOT IN` subqueries; spills build partitions to encrypted `NSPL` files when memory is tight. |
| **Merge join** | Streams both inputs in linear time when both sides are already index-ordered on the equality join keys. |
| **Partition-wise join** | Equi-joins between two identically partitioned tables on their full partition key run independent bounded hash joins per aligned partition pair in parallel (`HashJoin … partition-wise`), skipping pruned partitions. |
| **Vectorized aggregation** | Computes `COUNT`, `SUM`, `AVG`, `MIN`, `MAX`, and `GROUP BY` across batches. Over partitioned tables, `Aggregate … partition-wise` aggregates each surviving partition in parallel and merges partial hash tables. |
| **Window functions** | Sorts by `PARTITION BY` and `ORDER BY`, then computes ranking (`ROW_NUMBER`, `RANK`, `DENSE_RANK`), offset (`LAG`, `LEAD`), and framed aggregates with memory-budgeted partition spill. |
| **Materialized & recursive CTEs** | `Materialize` evaluates a CTE once against the query memory budget and serves `CTEScan` readers. `RecursiveCTE` iterates anchor and working tables under strict recursion depth, row, memory, and timeout caps. |

---

## Built-in `SELECT` result cache

Autocommit `SELECT`, CTE, and set-operation queries automatically use a process-local, **WAL-invalidated cache-aside layer**:

* **Cache key:** Cryptographic hash of `(exact SQL text, typed parameters, authenticated user, bound tenant, durable WAL LSN, catalog generation)`.
* **Zero stale reads:** Because the lookup key binds both the durable WAL LSN and catalog generation, any committed write or schema/statistics change immediately invalidates cached entries.
* **Strict bounds:** Capped at `128` entries and `8 MiB` total (`1 MiB` and `4,096` rows maximum per entry). Explicit `BEGIN` transactions, change-stream subscriptions, and queries containing `UUID()`, `NOW()`, or `AI()` bypass the cache. Deep copies are returned on hit so caller mutation cannot corrupt cached state.

---

## Durable mutation idempotency (`NSID`)

Clients can execute `INSERT`, `UPSERT`, `UPDATE`, `DELETE`, `RUN WORKFLOW`, or `CANCEL TASK` with an idempotency key (`IdempotentQuery` on the wire protocol, or `Conn.ExecIdempotent` in the Go driver):

1. The key is hashed with SHA-256 alongside the authenticated user and tenant (the raw key is never stored on disk).
2. The mutation and its bounded `NSID` v1 catalog record (`NSIR` v1 replay result, up to `256 KiB` / `4,096` rows) commit atomically in the same WAL transaction.
3. Retrying the identical typed request within the 24-hour retention window returns the recorded result with `IdempotentReplay = true` without re-executing the mutation. Reusing the same key for a different SQL statement or parameter list fails closed with `conflict`.

Engine note: [`docs/execution.md`](https://github.com/bzync/nextsql/blob/master/docs/execution.md).
