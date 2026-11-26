# Query optimizer

NextSQL uses a **deterministic, cost-based query optimizer** (`internal/sql/optimizer`). Given the same catalog generation and statistics snapshot, the optimizer always produces the exact same physical execution plan. No LLM or non-deterministic heuristic participates in query planning.

```arch
Logical Plan (planner.Plan)
  ├── 1. Constant folding & false/NULL filter elimination
  ├── 2. Predicate, projection, & LIMIT pushdown
  ├── 3. Join simplification & EXISTS / IN → Semi / Anti-Join flattening
  ├── 4. CTE inline vs. materialize decision + second rewrite pass
  ├── 5. Partition pruning & heap segment min/max pruning
  ├── 6. Sargable access-path generation (PK, secondary, covering, spatial, full-text, ANN)
  ├── 7. Cost-based left-deep inner-join reordering & partition-wise join tagging
  └── 8. Integer micro-cost evaluation → Physical Plan + EXPLAIN tree
```

---

## Logical rewrites

Logical rewrite rules iterate to a string-stable fixpoint (up to 8 passes):

| Rule | Transformation |
|---|---|
| **Constant folding** | Evaluates literal arithmetic, comparisons, boolean logic (`AND`/`OR`/`NOT`), `BETWEEN`, and `IS NULL` at plan time. Volatile functions (`UUID()`, `NOW()`, `AI()`) remain unevaluated. |
| **Contradiction & tautology pruning** | `WHERE FALSE` / `WHERE NULL` collapses the subtree to `Empty`. `WHERE TRUE` drops the filter node. |
| **Predicate pushdown** | Pushes filters below projections and into join children. For `LEFT JOIN`, left-only conjuncts push into the left input while right-only conjuncts stay above the join. For `FULL OUTER JOIN`, `WHERE` filters stay above the join. |
| **Column pruning** | Populates `Scan.Needed` with only the column ordinals referenced upstream. |
| **LIMIT pushdown** | Pushes `LIMIT` below projections, merges nested limits (`min`), turns `LIMIT 0` into `Empty`, and sets `NEAREST` / `Rerank` candidate `K = LIMIT + OFFSET`. |
| **Subquery flattening** | Rewrites correlated and uncorrelated `EXISTS` / `IN` subqueries in `WHERE` into `HashSemiJoin`, and `NOT EXISTS` / null-safe `NOT IN` into `HashAntiJoin`. |
| **Join simplification** | Rewrites `RIGHT JOIN` into `LEFT JOIN` with swapped inputs and a projection. Collapses `INNER JOIN` with an empty input or false `ON` condition to `Empty`. |
| **Join reordering** | Connected `INNER` and `CROSS` join components (up to 8 tables) are ordered into cost-based left-deep hash/merge trees where the smaller cardinality relation is chosen as the hash build (right) side. Equi-joins between identically partitioned tables on their full partition key are tagged `partition-wise`. |
| **CTE inlining vs. materialization** | Inlines single-reference or cheap scan-shaped CTEs and runs a second pushdown pass. Materializes multi-reference expensive CTEs, volatile CTEs (`UUID()`/`NOW()`/`AI()`), `WITH RECURSIVE`, and explicit `AS MATERIALIZED` bodies. |
| **Segment & partition pruning** | Drops heap segments whose `ANALYZE` min/max intervals cannot satisfy sargable predicates, and prunes `RANGE`, `HASH`, and `LIST` partitions (`Scan … [partitions: …]`). |

---

## Access paths & index selection

The optimizer evaluates all applicable access paths for each relation and picks the lowest integer micro-cost:

* **Clustered primary key & secondary B+Tree indexes:** Matches any leading equality prefix (`col = const`) plus at most one range interval (`<`, `<=`, `>`, `>=`, `BETWEEN`) on the next index column. Remaining predicates become `IndexScan.Residual`.
* **Covering indexes (`IndexScan … covering`):** Chosen when an index's key columns, primary key suffix, `INCLUDE` payload, and partial-index equality constants satisfy all `Scan.Needed` columns without touching the table heap.
* **Index-assisted `MIN()` and `MAX()` seeks:** Unqualified or sargable bounded `SELECT MIN(col)` / `MAX(col)` queries (including composite indexes with an equality prefix on preceding columns) execute as $O(\log N)$ bounded leaf seeks (`FirstBounded` / `LastBounded`) instead of full scans.
* **Expression & partial indexes:** Expression indexes match sargable predicates on the exact indexed expression (e.g., `LOWER(email) = 'a@b.com'`). Partial indexes (`CREATE INDEX … WHERE …`) are eligible only when the query's `WHERE` clause logically implies the index predicate.
* **Spatial indexes (`CREATE SPATIAL INDEX`):** Translates `DWITHIN`, `DISTANCE(col, point) < r`, and `WITHIN(col, box)` into 64-bit Morton geohash range scans with the exact geometric predicate preserved as residual.
* **Full-text search (`SEARCH`):** Selects a matching multi-column `CREATE FULLTEXT INDEX` (`Search … fulltext` with BM25 ranking, optional query-time `WEIGHT`, and `FACET` histograms), falling back to `Search … seq` when unindexed.
* **Vector ANN (`NEAREST`):** Selects `HNSW`, `IVF`, `IVFPQ`, or `SPARSE` vector indexes (`Nearest … hnsw | ivf | ivfpq | sparse`), over-fetching `K / selectivity` candidates when a residual filter is present so recall is not silently lost.

### Hybrid & multi-channel fusion planning

When a query combines structured `WHERE` filters, full-text `SEARCH`, and dense or sparse `NEAREST` clauses in a single statement, the optimizer costs all candidate execution strategies (`filter-ann`, `search-ann`, `ann-filter`, or `bm25+vector+sparse fusion`) under a unified cost model and fuses candidate rankings with **Reciprocal Rank Fusion (RRF)**:

```text
Rerank bm25+vector+sparse fusion
  Candidates … hnsw | ivf | ivfpq
  Candidates … sparse
  Search … fulltext
```

---

## Bound parameter sargability

Parameterized queries (`WHERE id = $1`, `WHERE created_at >= $1 AND created_at < $2`) substitute bound parameter values directly into single-table `SELECT`, `UPDATE`, and `DELETE` predicates prior to access-path selection (`internal/executor/param_sarg.go`).

* This allows prepared statements and parameterized driver queries to select primary-key, secondary-index, and partition-pruned plans identically to literal SQL.
* Substituted plans depend on parameter values and therefore bypass the shared SQL text plan cache so another execution never reuses a value-specific plan.
* Substitution is skipped when comparing `ENCRYPTED CLIENT` columns, `NULL` parameters, or multi-table joins.

---

## Statistics (`ANALYZE`) & cost model

`ANALYZE` collects deterministic statistics (using a fixed-seed reservoir sample up to 100,000 rows globally and 4,096 rows per partition) and stores them in encrypted `NSST` v3 and per-partition `NSPS` v1 catalog records:

* **Collected metrics:** Exact row count, per-column `NULL` count, NDV, min/max, equi-height histograms, Most Common Values (MCVs), Spearman correlation against the primary key, index selectivity, 8-segment PK min/max summaries, vector population/dimensions, and per-partition row counts.
* **Page-fit reduction:** Because a catalog record cannot exceed half a 16 KiB page, wide-table `ANALYZE` records automatically compact themselves in deterministic stages (dropping segment summaries first, then halving histogram buckets and MCV lists, then dropping min/max) while always preserving row, `NULL`, and distinct counts.

---

## Plan cache & `EXPLAIN`

* **Plan cache:** Caches up to `256` non-parameter-substituted plans keyed by `(SQL text, catalog generation)`. Any DDL or `ANALYZE` statement bumps the catalog generation and invalidates stale plans automatically.
* **`EXPLAIN` and `EXPLAIN ANALYZE`:**
  ```sql
  EXPLAIN SELECT * FROM orders WHERE customer_id = 42;
  EXPLAIN (FORMAT JSON) SELECT * FROM orders WHERE customer_id = 42;
  EXPLAIN ANALYZE SELECT * FROM orders WHERE customer_id = 42;
  ```
  Returns 11 diagnostic columns (`operator`, `estimates`, `actuals`, `time`, `cpu`, `memory`, `disk`, `cache`, `spill`, `workers`, `index`) or a structured JSON tree (`FORMAT JSON`) rendered as an interactive visual DAG in NextSQL Studio.

Engine note: [`docs/optimizer.md`](https://github.com/bzync/nextsql/blob/master/docs/optimizer.md).
