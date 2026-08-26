# Production operations (Phase 14)

Backup, restore, PITR, and logical export/import are documented in
`docs/backup.md` and `docs/export.md`. This note covers the remaining
single-node ops surface: format compatibility, metrics, diagnostics,
admission control, and `nextsql-bench`.

Official numbers keep encryption, WAL, fsync, checksums, MVCC, and
authentication enabled. Hardware, filesystem, row width, query, indexes,
cache condition, encryption, durability, and concurrency are part of the
measurement, not optional footnotes.

## Upgrade / format compatibility

Every persisted family has a version and a compatibility window in
`internal/upgrade.Catalog`. This binary reads only versions in
`[MinReadable, MaxReadable]`. There is no silent rewrite of an unknown
version. A newer file fails closed; an older-than-min file fails closed.

Current families are all **v1**. Opening a v1 data directory with this
binary is the supported path. A future format bump must either widen
`MaxReadable` or add an explicit rewrite increment — not an in-place
guess.

```text
nextsql diagnose --data-dir DIR
nextsql status --local --data-dir DIR --key-file FILE
```

`diagnose` reads plaintext headers only (superblock, WAL control, UNDO
control, isolated-page sidecar). It does not need the root unlock key and never prints secrets.
`status --local` does the same check, then opens the database with `--key-file`
and prints LSNs, table count, isolated pages, counters, and admission stats.
Default `nextsql status` (no `--local`) is a server ping: it dials `nextsqld`
and prints `mode server`.

`--key-file` is the external root unlock key. It is never stored in the
data directory. Keys are never accepted in connection URLs.

Embedded diagnostics can call `executor.DB.OrphanPages()`. The read-only check
serializes with transactions and Raft apply, then compares every allocated page
against the primary/catalog tree, all catalog-owned detached trees, durable free
IDs, and freelist metadata. It returns only page IDs that are allocated but
unreachable; it never automatically frees a suspected orphan.

## Metrics

`internal/metrics` is a process/registry of counters and a latency ring
(last 2048 query samples):

- queries, errors, commits, rollbacks, admitted, rejected, canceled, rows
- p50 / p95 / p99 / p99.9
- page AEAD seal/open time and bytes (encryption overhead)
- WAL bytes flushed this process
- isolated / repaired page counts (detect → isolate → recover)
- `fk_checks`, `fk_violations`, `fk_cascade_rows`, `fk_cascade_reject` (no keys or payloads)
- index rebuild attempts, failures, rows scanned, entries produced, and total duration
- maintenance runs, failures, physical tombstones removed, and total duration
- heap, total alloc, goroutines, CPU

Page encrypt/decrypt in `crypto.SealPage` / `OpenPage` observe the
process registry. Per-database query counters live on `executor.DB`.
Metrics never contain passwords, keys, tokens, or secrets.

Blocking index rebuild progress is available from
`executor.DB.IndexRebuildProgress()`. Each active entry reports only table and
index names, phase (`building` or `committing`), rows scanned, entries produced,
and start time. The entry is removed on success or failure; cumulative outcome
and duration remain in the metrics snapshot.

`executor.DB.MaintenanceStatus()` reports whether maintenance is paused, the
active kind/scope/start time, and the last run's completion, affected count,
and failure state. A central synchronous coordinator permits one maintenance
pass per database and never starts background goroutines or queues work.
Concurrent requests fail with `unavailable`. `PauseMaintenance` prevents new
runs; `ResumeMaintenance` re-enables them. An already-active bounded pass is
allowed to finish. SQL `MAINTAIN` statements first acquire the shared query
admission slot, so maintenance cannot bypass overload rejection or queue limits.
The default per-run budget is 30 seconds of elapsed CPU-work time, 8 MiB of
buffered tombstone keys, and 500,000 conservative logical page-I/O units. A leaf
scan costs one unit; each physical delete reserves a tree-height-based path and
merge allowance before its transaction begins. These units bound engine work,
not kernel syscalls or physical-device cache misses. Checks occur during leaf scans and between physical
deletes. Exhaustion returns `exhausted` with bounded partial progress; unprocessed
tombstones remain durable for a later pass. Embedders may set positive limits
with `DB.SetMaintenanceLimits`.

## Admission control

A process-wide gate (`scheduler.Admission`) sits in front of every
`Session.ExecContext`:

1. Take an in-flight slot if one is free.
2. Otherwise queue up to `max_query_queue`.
3. If the queue is full, or the wait exceeds `query_queue_wait_ms`,
   reject with `unavailable`. Cancel of the parent context unblocks a
   waiter with `canceled`.

Defaults: 32 in-flight, 128 queued, 5 s wait. Config keys:

```text
max_inflight_queries=32
max_query_queue=128
query_queue_wait_ms=5000
max_result_rows=1000000
```

Per-query budgets still apply: workers, memory, disk spill, I/O,
execution time, result rows, and result bytes. Exceeding a budget
cancels or fails closed (`exhausted`) instead of growing without bound.

`nextsqld` installs the gate from config after open (including
`--require-client-key` unlock). Protocol `MaxSessions` still bounds
accepted connections.

## `nextsql-bench`

```text
nextsql-bench [--quick] [--slo] [--slo-max-rows 25000] [--slo-vectors 256]
              [--slo-vector-queries 64]
              [--slo-buffer-pages 4096]
              [--workload all|page|point|range|insert|update|delete|txn|join|agg|json|fulltext|vector|hybrid]
              [--duration 1s] [--rows 128] [--concurrency 1]
```

SQL workloads run against a throwaway encrypted database with WAL and
fsync on. Each report includes QPS, TPS (write/txn workloads), p50 / p95
/ p99 / p99.9, allocs, heap, disk delta, WAL bytes, and encryption
overhead (`enc%` = page AEAD time / elapsed). Page microbenches remain
for encode/encrypt/buffer I/O.

`--slo` runs the published-number suite: cached PK lookup, secondary-index
equality, durable single-row INSERT/UPDATE, bulk `INSERT` plus `COUNT(*)` /
`GROUP BY` / PK range / hash join at 25K (and larger if `--slo-max-rows`
allows), hybrid `WHERE`+`SEARCH`+`NEAREST`, and HNSW `recall@10` /
`recall@100` with QPS, heap, and db size. Bulk load uses 4096-row `InsertEncoded` batches and `COMMIT` every 524288
rows. Every row includes CPU,
RAM, filesystem, row width, query, indexes, cache condition, encryption,
durability, and concurrency.

These are engineering measurements on the host that ran the tool, not
universal product guarantees. The 100M analytical run and corrected 1M-vector
HNSW v10 run are published below. Large atomic index builds use a no-steal
transaction and may require a larger explicitly reported buffer pool; use
`--slo-buffer-pages` rather than weakening WAL or durability.
10M DELETE soak: `NEXTSQL_SOAK_ROWS=10000000 go test ./internal/executor/ -run TestBulkDeleteSoak`.
`BulkDeleteAll` returns the affected-row count before atomically replacing an
eligible unindexed heap. A tree populated in the current process has a maintained
exact `liveRows` cache, so the count and heap swap are effectively O(1). A tree
opened after restart deliberately begins with `liveKnown=false`; its first count
walks the leaf chain to reconstruct the answer, making that run O(rows) before
the same constant-time heap swap. This methodology difference explains the
observed **25 ms / 1.57 s** warm-process results versus the **24 s** cold-open
10M result. All include encryption, WAL, fsync, MVCC, and the affected-row count;
they are not directly comparable DELETE-throughput measurements.
100M B+Tree invariants: `./scripts/run-btree-soak.sh`. The wrapper uses
workspace-backed temporary storage (override with `NEXTSQL_SOAK_TMPDIR`),
retains timestamped output under `.bench-results` (override with
`NEXTSQL_SOAK_LOG`), defaults to `GOMEMLIMIT=3GiB`, `GOGC=25`, and
`GODEBUG=madvdontneed=1`, and disables Go's default 10-minute test timeout.
At one million operations and
above, the invariant workload commits bounded 4,096-operation write
transactions. This retains randomized insert/delete coverage and periodic
full-tree checks without turning the correctness soak into a 100-million-fsync
benchmark. The batched path completed 1M operations plus final scan and point
verification in **809.15 s** on 2026-08-21. A first replacement reached 2M
clean operations, but retained 25 GiB of WAL for a 402 MiB database; its projected
~1.25 TiB WAL footprint could not finish on the labeled filesystem. The harness
now installs a durable checkpoint after each successful full-tree invariant
check, exercising page flush and explicitly discarding checkpoint-obsolete WAL
segments for this disposable non-PITR workload to bound disk use. A 100K
checkpointed validation passed. The v4 run reached 24M clean operations
(`live=11,435,641`) before it was stopped on 2026-08-22. Its replacement,
`nextsql-btree-100m-p16-v8.service`, reached 44M clean operations
(`live=17,557,686`) under an 8 GiB memory cap before it was stopped on
2026-08-25; retained output is `.bench-results/btree-100m-p16-v8.log`.
Completion still requires the terminal structural check, full scan count, and
full randomized-keyspace point verification. Override the operation count with
`NEXTSQL_BTREE_OPS` for a smaller validation run.

The `nextsql-btree-100m-p16-v9.service` soak started on 2026-08-25 with
`NEXTSQL_BTREE_OPS=100000000` and an 8 GiB cap applied to the Snap child scope
that contains `go test`. It was killed with exit 137 after approximately 11
hours. Its output is no longer retained, so no terminal structural check, full
scan count, or randomized point verification can be credited.

The replacement wrapper passed a 100K-operation terminal validation in
**273.72 s** on 2026-08-26 with its bounded defaults. The full
`nextsql-btree-100m-p16-v10.service` run then started with the same workload
seed, `GOMEMLIMIT=3GiB`, `GOGC=25`, `madvdontneed=1`, a 7 GiB cgroup
memory-high reclaim threshold, an 8 GiB hard memory cap, and 2 GiB bounded
swap. These cgroup bounds include file-backed page cache. The run was stopped
by explicit direction before its first 2M-operation checkpoint, so it provides
no P16 gate evidence. The 100M measurement is paused; a future run still needs
the terminal structural check, full scan count, and randomized point
verification.

### Published SLO suite (2026-08-17)

Hardware: linux/amd64, AMD Ryzen 5 7535HS, 12 logical CPUs, 14.3 GiB RAM,
encryption AES-256-GCM on, WAL + fsync on, 1 session, buffer 2048 pages
(32 MiB). Scan path: interior `SplitKeys`, concurrent page decrypt, `COUNT(*)`
counts visible slots without materializing payloads.

Reads through 1M (tmpfs-backed temp dir; EXPLAIN on the index query is
`IndexScan kv ix_kv_n`). `nextsql-bench --slo --slo-max-rows 1000000 --slo-vectors 256 --duration 2s`.

| Workload | Rows | Query | Indexes | Cache | p50 | p95 | p99 | Target | Met |
|---|---|---|---|---|---|---|---|---|---|
| Cached PK | 25 000 | `SELECT n FROM kv WHERE id = $1` | PRIMARY KEY (id) | warm heap + PK | 30 µs | 55 µs | 96 µs | p50&lt;0.5 ms | yes |
| Indexed equality | 25 000 | `SELECT id FROM kv WHERE n = $1` | `ix_kv_n` | warm | 44 µs | 79 µs | 152 µs | p95&lt;3 ms | yes |
| 25K `COUNT(*)` | 25 000 | `SELECT COUNT(*) FROM scan` | heap | working set fits in 32 MiB buffer | 3 ms | 3 ms | 3 ms | &lt;1 s | yes |
| 25K `GROUP BY` | 25 000 | `SELECT k, COUNT(*) FROM scan GROUP BY k` | heap + hash agg | working set fits in 32 MiB buffer | 6 ms | 6 ms | 6 ms | &lt;1 s | yes |
| 100K `COUNT(*)` | 100 000 | `SELECT COUNT(*) FROM scan` | heap | working set fits in 32 MiB buffer | 9 ms | 9 ms | 9 ms | &lt;1 s | yes |
| 100K `GROUP BY` | 100 000 | `SELECT k, COUNT(*) FROM scan GROUP BY k` | heap + hash agg | working set fits in 32 MiB buffer | 24 ms | 24 ms | 24 ms | &lt;1 s | yes |
| 1M `COUNT(*)` | 1 000 000 | `SELECT COUNT(*) FROM scan` | heap | 32 MiB buffer (working set exceeds buffer) | 215 ms | 215 ms | 215 ms | &lt;1 s | yes |
| 1M `GROUP BY` | 1 000 000 | `SELECT k, COUNT(*) FROM scan GROUP BY k` | heap + hash agg | 32 MiB buffer (working set exceeds buffer) | 191 ms | 191 ms | 191 ms | &lt;1 s | yes |
| Hybrid | 256 | `WHERE` + `SEARCH` + `NEAREST` LIMIT 10 | JSON path + full-text + HNSW | warm | 9.0 ms | 16.3 ms | 18.6 ms | p95&lt;100 ms | yes |
| HNSW top-10 | 256 × 8-d | `NEAREST … LIMIT 10` | HNSW | warm graph | 2.1 ms | 3.3 ms | 3.3 ms | p95&lt;25 ms | yes* |

\*Recall@10 = 1.000, recall@100 = 0.646. This is **not** the 1M-vector official scale.

10M row-processing, same host, **ext4** (LVM) temp dir. 4096-page / 64 MiB buffer; working set does not fit. `TMPDIR` on ext4; `nextsql-bench --slo --slo-max-rows 10000000 --slo-vectors 16 --duration 1s` (2026-08-18 insert-path pass). Encryption, WAL, fsync, checksums, and MVCC stayed on.

| Workload | Rows | FS | Elapsed | Rate | Target | Met |
|---|---|---|---|---|---|---|
| 10M bulk `INSERT` | 10 000 000 | ext4 | **33 s** | 301 485 rows/s | next &lt;15 min; long-term &lt;2 min; lifetime &lt;1 min | **yes / yes / yes** |
| 10M bulk `UPDATE` | 10 000 000 | ext4 | **10 s** | 985 275 rows/s | next &lt;10 min; long-term &lt;2 min | **yes / yes** |
| 10M bulk `DELETE` | 10 000 000 | ext4 | **24 s** | heap swap / count | long-term &lt;5 min | **yes** |
| 10M `COUNT(*)` | 10 000 000 | ext4 | **121 µs** | live counter | &lt;5 s; long-term &lt;1 s | **yes / yes** |
| 10M `GROUP BY` | 10 000 000 | ext4 | **589 ms** | — | &lt;5 s; long-term &lt;1 s | **yes / yes** |
| 10M PK range `COUNT` | 10 000 000 | ext4 | 6.34 s | — | residual string `id` range | no (&lt;5 s) |
| 10M `COUNT(*)` join | 10 000 000 | ext4 | **1.06 s** | — | streamed hash probe | yes |

1M on the same run: `INSERT` 2.04 s (489 320 rows/s), `COUNT(*)` 53 µs, `GROUP BY` **112 ms**.

100M row-processing, same host, **ext4** (LVM) workspace-backed temp dir,
4096-page / 64 MiB buffer, 2026-08-21. Command:
`TMPDIR=$PWD/.bench-tmp nextsql-bench --slo --slo-max-rows 100000000
--slo-vectors 16 --slo-no-dml --duration 1s`. Encryption, WAL, fsync,
checksums, and MVCC stayed on. After correcting the range workload and using
fixed-width scan keys, the replacement 100M bulk load took **18 m 02.22 s**
(92 403 rows/s).

| Workload | Rows | Elapsed | Target | Met |
|---|---:|---:|---:|---:|
| `COUNT(*)` | 100 000 000 | **56 µs** | &lt;60 s | yes (live-count fast path) |
| `GROUP BY k, COUNT(*)` | 100 000 000 | **16.31 s** | &lt;60 s | **yes** |
| PK range `COUNT` (exactly 5,000 rows) | 100 000 000 | **2.21 ms** | &lt;60 s | **yes** |
| Hash join `COUNT(*)` | 100 000 000 | **35.54 s** | &lt;60 s | **yes** |

The original range predicate (`id >= 's0' AND id < 's5000'`) was not a
5,000-row interval because the unpadded decimal strings compare
lexicographically; at 100M it selected tens of millions of rows. Range costing
also treated a clustered PK range like per-row random heap lookups, causing a
`SeqScan`. Both defects were fixed on 2026-08-21: the harness now chooses an
equal-width 5,000-row interval around the scale midpoint, and the optimizer
costs it as a seek plus sequential leaf reads. A durability-on 1M verification
used `id >= 's500000' AND id < 's505000'`, chose clustered `IndexScan`, and
completed in **3.28 ms**. The corrected durability-on 100M rerun used
`id >= 's000050000000' AND id < 's000050005000'`, self-verified the clustered
`IndexScan` plan and exact 5,000-row result, and completed in **2.21 ms**. The
100M analytics exit gate is met.
The indexed-equality latency check in the same run used `IndexScan kv ix_kv_n`
and measured p50 **35 µs**, p95 **100 µs**, and p99 **147 µs** at its
25K-row latency scale.

1M-vector HNSW baseline, same host and **ext4**, 2026-08-21. Command:
`TMPDIR=$PWD/.bench-tmp nextsql-bench --slo --slo-max-rows 25000
--slo-vectors 1000000 --slo-buffer-pages 32768 --slo-no-dml
--duration 1s`. The 32,768-page buffer is 512 MiB. Encryption, WAL, fsync,
checksums, and MVCC remained enabled. The default 4,096-page pool exhausted
its no-steal dirty frames while atomically persisting this index, so the larger
pool is part of the labeled methodology rather than an unreported benchmark
change.

Measurement caveat: this baseline's original deterministic generator produced
only eight distinct directions at dimension 8, repeated across all one million
rows. Its recall values therefore mostly measure primary-key tie-breaking among
equal-distance vectors and are not representative ANN recall. The harness now
uses deterministic, normalized, distinct vectors, disjoint query seeds, and a
64-query default sample. The corrected 1M v10 result below supersedes this
degenerate baseline; the historical timings remain useful only as latency
history.

Corrected-workload scale checks on the same ext4 workspace, 2026-08-21, used
distinct normalized vectors, disjoint query seeds, and 64 queries. Both kept
encryption, WAL, fsync, checksums, and MVCC enabled:

| Vectors | Buffer | p50 | p95 | p99 | Recall@10 | Recall@100 | Heap | HNSW size | Target met |
|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| 10,000 | 4,096 pages | **1.504 ms** | **1.827 ms** | **2.485 ms** | **1.000** | **1.000** | 157.2 MiB | not recorded | yes |
| 100,000 | 8,192 pages | **2.567 ms** | **3.317 ms** | **4.763 ms** | **1.000** | **0.999** | 394.2 MiB | 48.2 MiB | yes |
| 1,000,000 | 131,072 pages | **6.158 ms** | **8.061 ms** | **8.156 ms** | **1.000** | **0.998** | 4.3 GiB | 546.1 MiB | yes |

The corrected 1M workload includes heap `searchLayer`, covering PK hits (no
heap fetch for `SELECT id`), and a discarded warm parameterized `NEAREST`.
The bounded `nextsql-hnsw-1m-p16-v8.service` run reached
HNSW graph construction but was OOM-killed at its explicit 8 GiB cgroup limit
on 2026-08-24. It produced no query-latency or recall sample and therefore does
not satisfy the P16 exit gate. The failure is retained in
`.bench-results/hnsw-1m-p16-v8.log`; raising the cap without first bounding Go
heap growth is not an accepted fix.

The replacement `nextsql-hnsw-1m-p16-v9.service` run used
`GOMEMLIMIT=4GiB` and `GOGC=50` beneath the unchanged 8 GiB cgroup cap but
failed during atomic graph construction when its 32,768-page no-steal pool
had no evictable frames (`buffer.evict: all frames are pinned`) at a 5.48 GiB
memory peak. This is retained in `.bench-results/hnsw-1m-p16-v9.log`.

The v10 rerun started on 2026-08-25 with the same corrected 1M-vector/64-query
data set, encryption, WAL, fsync, checksums, and MVCC, but a 131,072-page
(2 GiB) buffer pool so the atomic no-steal build can retain its dirty pages.
`GOMEMLIMIT=4GiB` and `GOGC=50` remain enabled; retained output is
`.bench-results/hnsw-1m-p16-v10.log`. It completed with p50 **6.158 ms**, p95
**8.061 ms**, p99 **8.156 ms**, QPS **156**, recall@10 **1.000**, recall@100
**0.998**, heap **4.3 GiB**, DB size **1.1 GiB**, and HNSW size **546.1 MiB**.

| Run | Vectors | Dimension | p50 | p95 | p99 | QPS | Recall@10 | Recall@100 | Heap | DB size | HNSW size | Target met |
|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| instrumented | 1,000,000 | 8 | **883 µs** | **1.071 ms** | **1.071 ms** | **1,097** | **0.125** | **0.041** | **3.1 GiB** | **915.6 MiB** | **353.4 MiB** | yes for this run |
| immediately preceding | 1,000,000 | 8 | **1.078 ms** | **36.78 ms** | **36.78 ms** | **134** | **0.125** | **0.041** | **2.4 GiB** | **915.6 MiB** | not instrumented | **no** |

The configured query target is p50 `<10 ms`, p95 `<25 ms`, p99 `<50 ms`
with non-zero recall. One run met it and the adjacent run missed p95, while
both had low recall. Treat this as the required 1M baseline, not a signed-off
exit gate. The 36.78 ms miss had p95 = p99, which is what `latencyPct` does
when one cold sample dominates a tiny query count. The harness now warms one
parameterized `NEAREST` (`$1`) before timing, reuses the plan cache, and uses
64 queries. `searchLayer` uses min/max heaps instead of re-sorting the
candidate lists on every neighbor. HNSW size is physical database-file growth
measured immediately across `CREATE VECTOR INDEX`.

10M `INSERT` is a durable **bulk load**: `InsertEncoded` → `InsertBatch` (4096-row batches, `COMMIT` every 524288). Sequential keys pin one leaf and append. When the new key is greater than every key on a full leaf, the left page is kept and only the new record goes on a new right sibling (no collect/rebuild). The rightmost parent is cached so a split does not re-descend from the root. A sole-writer snapshot skips per-row logical WAL and UNDO; commit still writes encrypted page images and `fdatasync`s. Default WAL segment is 128 MiB. AES-256-GCM AEAD objects are cached per DEK (stdlib crypto, not a new primitive). It is not 10M single-row SQL `INSERT`s. Page cache-miss I/O authenticates the envelope and page id in place (no extra 16 KiB copy).

10M `COUNT(*)` with no predicate returns a process-maintained live key count when this session is the only snapshot. After `Open` of an existing tree the count is unknown and the executor scans. Concurrent readers always scan.

10M `UPDATE` patches the decimal column in place on the leaf (`PatchVisible`) when this session is the only snapshot and the table has no secondary indexes.

10M `DELETE ALL` replaces the heap with an empty tree in one catalog/WAL transaction when this session is the only snapshot and there are no secondary indexes. Concurrent readers force the slow per-row path.

### Next-target scorecard (2026-08-18)

These are engineering next/long-term aims, not the PLAN.md §9 product gates (those stay the published “Met” column above).

| Area | Current | Next target | Long-term | Lifetime |
|---|---:|---:|---:|---:|
| PK p50 | **23 µs** | ≤30 µs | ≤20 µs | — |
| PK p99 | **50 µs** | <250 µs | **<100 µs met** | — |
| Indexed p95 | **67 µs** | <250 µs | **<100 µs met** | — |
| 1M `COUNT(*)` | **53 µs** | **<300 ms met** | **<150 ms met** | **met** |
| 1M `GROUP BY` | **112 ms** | **<300 ms met** | **<150 ms met** | **met** |
| 10M `COUNT(*)` | **121 µs** | **<3 s met** | **<1 s met** | **met** |
| 10M `GROUP BY` | **589 ms** | **<3 s met** | **<1 s met** | **met** |
| 10M INSERT | **33 s** / 301 485 rows/s | **<15 min met** | **<2 min met** | **<1 min met** |
| 10M UPDATE | **10 s** / 985 275 rows/s | **<10 min met** | **<2 min met** | **met** |
| 10M DELETE | **24 s** | **measurable** | **<5 min met** | **met** |
| 100M analytics | COUNT **56 µs**; GROUP BY **16.31 s**; range **2.21 ms**; join **35.54 s** | <60 s | <30 s | **next target met** |
| 1M HNSW | p95 **1.071–36.78 ms**, recall@10 **0.125**, recall@100 **0.041** | p95 <25 ms + recall | <10 ms | baseline measured; gate unstable |
| HA recovery | **<5 s met** | retain | <2 s | — |

1M bulk DML, same host, 2026-08-18. tmpfs: INSERT 1 m 6 s (15 205 rows/s), UPDATE 51.3 s (19 483 rows/s), `COUNT(*)` 215 ms, `GROUP BY` 191 ms. ext4 (LVM): INSERT 1 m 18 s (12 828 rows/s), UPDATE 1 m 27 s (11 488 rows/s). 10M times above are the last full-scale ext4 run; 1M rates are the post-fix re-run.

`GROUP BY k, COUNT(*)` peeks the group column as a byte view and increments interned counters (no per-row `string` alloc). 1M `GROUP BY` is **147 ms**; 10M is **660 ms**.

Write-path changes behind the 1M rates: in-place leaf insert/update/delete (no full-page rebuild on the common path), arithmetic `leafFits`, growing updates that refuse to mutate when the page cannot hold the new record, and allocator freelist persistence at commit (not on every Alloc/Free).

The corrected 100M analytics measurements are published above.

Durable writes, same host, two filesystems (WAL + fsync still on):

| Workload | FS | p50 | p95 | p99 | Target | Met |
|---|---|---|---|---|---|---|
| INSERT | tmpfs | 333 µs | 742 µs | 7.0 ms | p50&lt;2 ms p95&lt;5 ms p99&lt;10 ms | yes |
| UPDATE | tmpfs | 391 µs | 770 µs | 1.3 ms | same | yes |
| INSERT | ext4 (LVM) | 1.7 ms | 3.0 ms | 10.0 ms | same | yes |
| UPDATE | ext4 (LVM) | 1.8 ms | 2.5 ms | 6.8 ms | same | yes |

ext4 fsync p95 is device-dependent. An earlier labeled run on this host saw INSERT/UPDATE p95 ≈ 100 ms; this run met the target. Do not treat either as a universal guarantee.

Row width for `kv`: STRING PK + DECIMAL(12,2). Scan table: STRING PK + STRING + DECIMAL (~40 B encoded). Hybrid products: UUID + STRING + DECIMAL + TEXT + JSON + `VECTOR<F32,8>`.

A `BETWEEN` residual against `ix_kv_n` on 25 K rows was ~21 ms and is not the official indexed-query measurement; equality is `IndexScan`.

`enc%` is AEAD time share, not “slowdown vs unencrypted”. Official
benches do not disable encryption. ~7% on a short INSERT micro-run is inside the
`< 10%` OLTP engineering target; it is not a universal guarantee.

## Out of scope here

Race detector (`go test -race`) still needs a C compiler on this host.
HA is documented in `docs/ha.md`.
