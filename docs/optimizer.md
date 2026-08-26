# Query optimizer (Phase 6)

Deterministic cost-based optimizer. Same catalog generation and statistics snapshot always produce the same plan. No LLM planner.

## Pipeline

```text
logical plan
  → constant folding
  → predicate / projection / LIMIT pushdown
  → join simplification
  → column prune
  → physical alternatives (seq, PK, each matching index)
  → cost-based left-deep inner-join reorder
  → integer cost model
  → chosen physical plan + EXPLAIN tree
```

Package: `internal/sql/optimizer`. The executor calls `Optimize` after `planner.Plan`.

## Rewrites

| Rule | Effect |
|---|---|
| Constant folding | Literal arithmetic, comparisons, `AND`/`OR`/`NOT`, `BETWEEN`, `IS NULL`. `UUID()` / `NOW()` / `AI()` stay unevaluated. |
| False / NULL filter | Plan becomes `Empty`. `WHERE TRUE` drops the filter. |
| Predicate pushdown | Filter moves below Project; conjuncts that touch one join side push into that side. `LEFT JOIN`: left-only conjuncts push into the left input; right-only and mid-join WHERE stay above the join (pushing a right filter through `LEFT` would drop unmatched left rows or turn it into INNER). `FULL OUTER JOIN`: neither side's WHERE is pushed into the inputs. |
| Projection / column prune | `Scan.Needed` records referenced ordinals. |
| LIMIT pushdown | Limit moves below Project; nested limits take `min`; `LIMIT 0` is `Empty`. `OFFSET` stays on the Limit node (skip after `ORDER BY`). Not pushed below Filter. `NEAREST` / `Rerank` `K` is `LIMIT + OFFSET`. |
| Join simplification | Empty left, or (INNER) empty right / false `ON`, becomes `Empty`. True `ON` on INNER becomes a cross join. `LEFT JOIN` does not collapse an empty right or a false `ON` (those emit unmatched left + NULLs). `FULL` keeps empty-side / false-`ON` joins so unmatched rows on the other side can be emitted. `RIGHT JOIN` is rewritten to `LEFT JOIN` with swapped children and a reorder `Project`. Simple `EXISTS`/`IN` in `WHERE` become `HashSemiJoin`; `NOT EXISTS` and null-safe `NOT IN` become `HashAntiJoin`. Empty right or false predicate on a semi-join is `Empty`; on an anti-join they keep the left input. Filters still push through projections that do not reference output aliases, including derived-table inputs. |
| Join reordering | Connected `INNER` / `CROSS` components (up to eight tables) are planned as cost-based left-deep trees. Hash join builds the right input, so a smaller build side is cheaper; equal costs keep written table order. Single-relation `ON` / `WHERE` conjuncts become per-scan filters. `LEFT` / `FULL` / `SEMI` / `ANTI` are reorder barriers. `SEARCH` / `NEAREST` / hybrid keep the rank table as the first join input. A column-order `Project` restores the original schema when the physical order changed. Partition-wise aggregation and join wait on physical partitioning. |
| CTE inline / materialize | After the first rewrite pass, each CTE is inlined or materialized. Inline when referenced once, or when a cheap scan-shaped body is referenced more than once. Materialize when referenced more than once and not cheap, when the body uses `UUID()` / `NOW()` / `AI()`, for `WITH RECURSIVE`, or when `AS MATERIALIZED` is written. `AS NOT MATERIALIZED` forces inlining unless the body is volatile. Filters do not push into a materialized `CTEScan`. After inlining, a second rewrite pass can push predicates into the substituted body. |
| Segment prune | After `ANALYZE`, heap segments whose min/max cannot satisfy a sargable predicate are dropped. All gone → `Empty`. |

Rewrites run to a string-stable fixpoint (max 8 rounds).

## Access paths

Sargable forms: `col = const`, inequalities, `BETWEEN`, `IS NULL`, plus `DWITHIN(col, point, meters)`, `DISTANCE(col, point) < r`, and `WITHIN(col, box)` against a `CREATE SPATIAL INDEX`. Expression indexes match the same comparison forms on the indexed expression (`LOWER(name) = 'x'`). AND-conjuncts build per-column intervals. An index (including the clustered PK) matches a leading equality prefix plus at most one range on the next column. Unused conjuncts become `IndexScan.Residual`. Spatial scans use a Morton-code prefix range and always keep the original geo predicate as residual.

Partial indexes (`CREATE INDEX … WHERE …`) are used only when the query predicate implies the index predicate. Covering indexes (`INCLUDE`) and indexes that already hold every needed column plus the primary key can become `IndexScan … covering` (index-only, no heap fetch). A covering partial scan of an entire index is considered when the query implies the predicate, the index covers the needed columns, and there is no sargable leading key.

`SEARCH col FOR '…'` is its own access path. A `CREATE FULLTEXT INDEX` on that column becomes `Search … fulltext` (inverted-index lookup, BM25 order, `WHERE` as residual). Without an index the plan is `Search … seq` over a sequential or sargable scan. `EXPLAIN` shows `Search`.

`NEAREST col TO …` is its own access path. A `CREATE VECTOR INDEX … USING HNSW` on that column becomes `Nearest … hnsw`. Without an index the plan is `Nearest … flat` (exact). `WHERE` is residual.

`SEARCH` + `NEAREST` is one physical planning problem (Phase 12). The optimizer costs structured-filter-then-ANN, ANN-then-structured-filter, and SEARCH-then-ANN, then picks the cheapest. Tie-break prefers the more exact order (`filter-ann`, then `search-ann`, then `ann-filter`). `EXPLAIN` shows `Candidates` (generation) and `Rerank bm25+vector` (reciprocal-rank fusion). Operator order is not hard-coded.

`SEARCH` / `NEAREST` / hybrid on an inner-join query ranks the `FROM` table first, then joins. `FROM`-table `WHERE` conjuncts push under the rank access path so hybrid costing is unchanged. Joined-table predicates stay above or on the join. Rank order of the search table is preserved through hash-probe inner joins when each join is 1:1; 1:N duplicates ranks. `LIMIT` after a rank+join is a result cap only — it is not copied onto `Nearest.K` / `Rerank.K`, and hybrid does not apply the single-table default `K=10` under a join, so a dropped top neighbor does not hide the next row that joins. The engine does not insert an implicit `ORDER BY`. An explicit `ORDER BY` is a `Sort` above `Project`/`Aggregate`. A search/nearest column on a joined table is rejected.

Tie-break when costs are equal: clustered PK, then index name (lexicographic), then sequential scan.

## Cost model

Integer micro-costs. Selectivity is millionths (`1_000_000 = 1.0`).

Without statistics the table is assumed to have 1000 rows. Equality defaults to 0.10, range to 0.33, `IS NULL` to 0.05, `SEARCH` to 0.10. With statistics, NDV / MCV / histograms drive estimates; JSON path equality uses the matching path-index selectivity when present. Unique or full-prefix PK equality is one row.

HNSW is costed as `ef × log2(N)` node visits (not a heap scan). Flat ANN scales with remaining rows and dimension. After a residual filter the planner over-fetches ANN candidates (`k / selectivity`, capped) so LIMIT is not met by silently dropping recall. Rerank is linear in the candidate set.

Runtime feedback (`EXPLAIN ANALYZE` actual vs estimate) is recorded on the database but **does not** change the chosen plan. Plan choice depends only on catalog + stats.

## Statistics (`ANALYZE`)

Durable catalog rows, magic `NSST`, key `S` + table name.

Collected: row count, per-column nulls / NDV / min / max / equi-height histogram / most common values / Spearman correlation vs the first PK column, per-index selectivity, PK-ordered segment min/max (up to 8 segments), and per-`VECTOR` column statistics (non-null count, dimension, HNSW index name / `M` / `efConstruction` when a vector index exists). Sampling is a fixed-seed reservoir (max 100 000 rows) so the same heap contents produce the same snapshot. Stats format `NSST` version 2 adds the vector block; version 1 snapshots still decode.

## Plan cache

Keyed by SQL text + catalog generation. DDL and `ANALYZE` bump the generation. Transaction-control, `EXPLAIN`, `ANALYZE`, and DDL are not cached. Capacity 256, FIFO eviction.

## EXPLAIN

```text
EXPLAIN SELECT …
EXPLAIN ANALYZE SELECT …
```

Result columns: `operator`, `estimates`, `actuals`, `time`, `cpu`, `memory`, `disk`, `cache`, `spill`, `workers`, `index`.

Join operators: `HashJoin`, `MergeJoin`, `CrossJoin`, `LeftJoin` (`LEFT [OUTER] JOIN`, hash or merge), `HashSemiJoin` (flattened `EXISTS`/`IN`), and `HashAntiJoin` (flattened `NOT EXISTS` / null-safe `NOT IN`). Hash join is costed as build-right plus probe-left, so reordering prefers a smaller right input. Merge is linear in both inputs when both sides are already ordered on the keys. Estimated rows for `LeftJoin` are `max(left, inner-estimate)`. Semi-join estimates are at most the left input; anti-join estimates are left minus the matching semi estimate.

CTE operators: `With` wraps remaining materialized CTEs; `Materialize` is the CTE body computed once; `CTEScan` reads that result; `RecursiveCTE` is the working-table iteration for `WITH RECURSIVE`. Inlined CTEs do not appear as separate operators.

Window operator: `Window` sits above the scan/join/aggregate input and below
the select-list `Project`. Cost includes a sort of the estimated input plus a
per-row window compute. `LIMIT` is not pushed through a window. `EXPLAIN`
detail lists the window function names.

`UPSERT` is a leaf like `Insert`. `EXPLAIN` shows `Upsert`. `RETURNING` does
not change the operator; the executor projects the written rows.

`EXPLAIN ANALYZE` executes the statement (including DML). Disk / cache / spill stay 0 until Phase 7 instrumentation. Workers is 1.

## Hybrid plans (Phase 12)

```text
Rerank bm25+vector filter-ann | search-ann | ann-filter
  Candidates … flat | hnsw | fulltext
    IndexScan / Search / SeqScan
```

Measured on this host (Ryzen 5 7535HS, linux/amd64, encryption on): 200-row `WHERE` + `SEARCH` + `NEAREST` top-10 ≈ 11.2 ms/op. Official hybrid QPS / p95 are produced by `nextsql-bench --workload hybrid` (`docs/ops.md`).
