# Vectorized and parallel execution (Phase 7)

Batch execution over the Phase 5–6 SQL/optimizer stack. Remote sessions stream these batches over the Phase 8 native protocol (`docs/protocol.md`).

## Pipeline

```text
logical / physical plan
  → bounded query budget (workers, memory, disk, I/O, time)
  → batch scan (decode into columnar Batch)
  → vector filter / project
  → hash or merge join
  → hash aggregation
  → streamed batches (no unbounded materialization)
```

Packages: `internal/scheduler`, `internal/executor/vector`, `internal/executor/aggregate`, `internal/executor/join`, plus the existing `internal/executor` session.

## Batch

`vector.Batch` is columnar. Supported capacities are 1024, 2048, and 4096 (default 1024). Rows decode into a batch; filters compact a selection vector; projections evaluate expressions into a new batch.

## Scheduler and budgets

Every `Session.Exec` / `Query` / `Stream` gets a `scheduler.Budget`:

| Resource | Default | On exceed |
|---|---|---|
| CPU workers | `min(GOMAXPROCS, 8)` | work is queued, never unbounded goroutines |
| Memory | 64 MiB | `exhausted` (cancel, do not OOM) |
| Disk spill | 256 MiB | `exhausted` |
| I/O | 1 GiB | `exhausted` |
| Time | 30 s | `exhausted` |

`Session.SetLimits` overrides defaults. The process `scheduler.Pool` caps concurrent worker goroutines.

Spill files (`NSPL`) are AES-256-GCM encrypted with a per-query DEK that exists only in RAM. Official production spills are still subject to the Phase 13 temp/spill key domain.

## Operators

| Operator | Notes |
|---|---|
| Sequential / index scan | Batch decode; parallel heap scan splits on interior separators; page decrypt is concurrent. `IndexScan … covering` reconstructs the row from the index key, primary key, `INCLUDE` payload, and partial-index equality constants and skips the heap |
| Filter / project | Selection + expression eval over batches |
| Hash join | Build on the right, probe the left; equality keys from `ON` |
| Hash semi/anti-join | Build on the right, probe the left, emit left rows only; first match wins. NULL keys do not match. Spills the build side when the memory budget is exceeded |
| Merge join | Chosen when both sides are index-ordered on the join keys |
| Hash aggregation | `COUNT`, `SUM`, `AVG`, `MIN`, `MAX` with optional `GROUP BY` |
| Parallel agg / join | Partition, local work, merge under the scheduler |
| Partition-wise aggregation | An aggregate directly over a natively partitioned table aggregates each surviving partition heap in parallel and merges the partial hash tables; cross-partition groups fold during the merge. `EXPLAIN` tags it `Aggregate … partition-wise`. See `docs/partitioning.md`. |
| Partition-wise join | An equi-join between two identically partitioned tables on their full partition key runs one bounded hash join per aligned partition pair, in parallel across pairs; pruned pairs are skipped. `INNER`/`LEFT`/`FULL`/`SEMI`/`ANTI`. `EXPLAIN` tags the join node `… partition-wise`. See `docs/partitioning.md`. |
| Parallel `CREATE INDEX` | Workers encode keys; inserts stay on the writer transaction |
| CTE materialize / scan | A materialized CTE is computed once, charged against the query memory budget, and reused by `CTEScan`. Inlined CTEs execute as ordinary nested plans. |
| Recursive CTE | Anchor, then working-table iterations of the recursive term. Depth, row, memory, and time budgets apply; overflow is `exhausted`. |
| Window | Sort by `PARTITION BY` then window `ORDER BY`, then compute ranking, offset, or framed aggregate values per partition. Memory-budgeted; partition buckets may spill. |
| UPSERT | Exclusive lock on the unique target, then insert or update. `RETURNING` projects the final row. |

## SQL surface added in this phase

```sql
SELECT COUNT(*) FROM orders;
SELECT k, COUNT(*), SUM(qty) FROM orders GROUP BY k;
SELECT orders.k, items.name
FROM orders JOIN items ON orders.k = items.k;

SELECT o.k, i.name, l.sku
FROM orders o
JOIN items i ON o.k = i.k
JOIN lines l ON l.k = o.k;

SELECT c.name, o.id
FROM customers c
LEFT JOIN orders o ON o.customer_id = c.id;
```

`INNER JOIN`, bare `JOIN`, `LEFT` / `RIGHT` / `FULL` `[OUTER] JOIN`, and `CROSS JOIN` are accepted. Up to eight tables per statement. Inner joins are cost-based left-deep (hash-build the smaller side). Outer joins keep written order. `NULL` join keys do not match. `LEFT JOIN` emits unmatched left rows (including when the right side is empty or spilled). `RIGHT JOIN` is rewritten to `LEFT`. `FULL OUTER JOIN` emits unmatched rows from both sides; it is hash-only and returns `exhausted` instead of spilling. `SELECT *` with `GROUP BY` is rejected. Result order is unspecified.

## Streaming

`Session.Exec` still returns materialized `Result.Rows` under the memory budget (and the 1 000 000 row cap). `Session.Query` attaches `Result.NextBatch`. `Session.Stream` delivers batches to a callback so callers need not retain the whole result.

## Built-in SELECT result cache

Eligible autocommit SELECT/CTE/set-operation results use a process-local
cache-aside path: lookup first, execute on miss, then store a deep copy. The
key hashes exact SQL, typed parameters, authenticated user, and bound tenant.
Every lookup must match both the durable WAL position and catalog generation,
so any committed engine write or schema/statistics change invalidates old
entries. Authorization and tenant rewriting still run before lookup.

The cache is bounded to 128 entries and 8 MiB total. One entry may contain at
most 4,096 rows and 1 MiB. Oversized results execute normally but are not
stored. Explicit transactions, continuous subscriptions, and volatile expressions
(`UUID()`, `NOW()`, `AI()`) bypass it. `Query`/`Stream` may reuse an eligible
materialized entry and then batch that copy. Returned rows are deep copies; caller
mutation cannot alter another hit. `Result.Cached` and
`DB.ResultCacheStats()` expose local diagnostics. The cache is an acceleration
only and is empty after restart.

## Durable mutation idempotency

`Session.ExecIdempotent(ctx, key, sql, params)` executes `INSERT`, `UPSERT`,
`UPDATE`, `DELETE`, `RUN WORKFLOW`, or `CANCEL TASK` in a transaction that also
writes its replay result. Retrying the same typed request returns that result
with `Result.IdempotentReplay = true`; using the key for another request is
`conflict`. Failed/rolled-back mutations do not consume a key. The API rejects
an already-open explicit transaction so the retry fence cannot accidentally
commit unrelated work.

Keys are SHA-256 scoped by authenticated user and tenant and are never stored
raw. `NSID` v1 catalog records are WAL-backed, encrypted, replicated with
catalog pages, survive restart, and expire after 24 hours. Retention is bounded
to 1,024 records with a 256 KiB replay response and 4,096-row decoder cap;
expired entries are reclaimed synchronously before a new mutation. Capacity or
response overflow fails before commit. The native protocol and Go driver expose
the same behavior through `IdempotentQuery` / `Conn.ExecIdempotent`.

## Measurement notes

Official benches keep encryption, WAL, fsync, checksums, and MVCC on.

Recorded on 2026-08-17, linux/amd64, AMD Ryzen 5 7535HS, 12 logical CPUs, 14.3 GiB RAM, ext4:

| Workload | ns/op | B/op | allocs/op |
|---|---|---|---|
| SELECT 512 rows, batch 1024 | 532077 | 820235 | 5140 |
| SELECT 512 rows, batch 2048 | 518287 | 820628 | 5164 |
| SELECT 512 rows, batch 4096 | 515774 | 819840 | 5158 |
| GROUP BY + COUNT, 2000 rows | 2757003 | 3905874 | 35303 |

These are engineering measurements, not product SLOs. Later optimized runs on
the same host measured 10M `COUNT(*)` at **121 µs**, `GROUP BY` at **589 ms**,
bulk `INSERT` at **33 s**, bulk `UPDATE` at **10 s**, and cold-open bulk
`DELETE` at **24 s**. The 100M run measured `COUNT(*)` at **56 µs**,
`GROUP BY` at **16.31 s**, a corrected PK range at **2.21 ms**, and the join at
**35.54 s**. See `docs/ops.md` for commands, cache conditions, row-count
methodology, and the current corrected-HNSW caveat.

Race detection for parallel paths is still blocked on this host (`cgo: gcc not found`).
