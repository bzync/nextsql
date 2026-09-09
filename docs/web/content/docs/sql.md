# SQL dialect

Pipeline: SQL → lexer → parser → binder / catalog → logical plan → rewrite → cost model → vectorized executor.

## Rules

- One statement per request. A trailing `;` is optional. Extra tokens after the statement are a syntax error.
- Unquoted identifiers fold to lowercase. Quoted `"Ident"` is preserved.
- Reserved words include `FOREIGN`, `REFERENCES`, `CONSTRAINT`, `CASCADE`, `RESTRICT`, `ACTION`, `MATCH`, `ALTER`, `ADD`, `RENAME`, `ORDER`, `ASC`, `DESC`, `IF`, `EXISTS`, `WITH`, `OVER`, `UPSERT`, and `RETURNING`. Quote them (`"foreign"`) to use them as identifiers. `PARTITION`, `ROWS`, `RANGE`, `UNBOUNDED`, `PRECEDING`, `FOLLOWING`, `CURRENT`, `ROW`, `EXCLUDED`, and `INCLUDE` are contextual.
- Parameters are `$1`, `$2`, … (1-based). The CLI `-c` flag does not bind parameters; use a driver.
- `NULL` is typed. Compare with `IS NULL` / `IS NOT NULL`.
- Table names that start with `nsql_` are reserved. The exception is `CREATE TABLE nsql_schema_migrations` with the exact history DDL used by [migrations](/docs/migrate).

## Types

| Type | Notes |
|---|---|
| `UUID` | 16 bytes. `DEFAULT UUID()` |
| `BOOL` | `TRUE` / `FALSE` |
| `STRING` / `TEXT` | UTF-8. Same encoding; `TEXT` is the long-form name |
| `CHAR(n)` / `VARCHAR(n)` | `n` is rune count. `CHAR` is space-padded; `VARCHAR` is a length ceiling (never truncates). Not `ENCRYPTED CLIENT` |
| `BLOB` | Variable-length raw bytes, no UTF-8 validation. Literal `X'<hex>'` (e.g. `X'DEADBEEF'`). Orders byte-lexicographically; usable as `PRIMARY KEY`/`ORDER BY`. Isolated from `STRING`/`TEXT` — coercion either way requires hex text |
| `INT8` / `INT16` / `INT32` / `INT64` | Exact fixed-width signed integers (1/2/4/8 bytes). Sort numerically; narrowing (incl. an out-of-range literal) errors instead of wrapping. Arithmetic (`+ - * /`, unary `-`) promotes to `DECIMAL` |
| `UINT8` / `UINT16` / `UINT32` / `UINT64` | Exact fixed-width unsigned integers (1/2/4/8 bytes). Sort numerically (plain unsigned order); narrowing and negative assignment error instead of wrapping. Coercible to/from `INT8..64` (range/sign checked). Arithmetic promotes to `DECIMAL` |
| `DECIMAL(p,s)` | `1 ≤ p ≤ 38`, `s ≤ p`. Unscaled integer + scale. `DEFAULT AI()` when `s = 0` |
| `FLOAT32` / `FLOAT64` | IEEE-754. `-0.0` canonicalizes to `+0.0` on the sortable key. Arithmetic stays floating; assignment into `FLOAT32` re-rounds |
| `DATE` | Day count since 1970-01-01. Text `YYYY-MM-DD` |
| `TIME` | Nanoseconds since midnight. Text `HH:MM:SS[.fraction]` |
| `TIMESTAMP` | Date-and-time, no time zone |
| `TIMESTAMPTZ` | UTC nanoseconds. `DEFAULT NOW()` |
| `INTERVAL` | Months, days, and nanoseconds. Literal `INTERVAL '1 month 3 days'`. Calendar arithmetic with `DATE` / `TIME` / `TIMESTAMP` / `TIMESTAMPTZ` |
| `ENUM('a', 'b', …)` | Declaration-order ordinals (`small < medium` even if that is reverse alphabetical). Isolated from other types except text |
| `JSON` | Compact binary `NSJB`. Insert a JSON text literal |
| `VECTOR<F32,N>` / `<F16,N>` / `<I8,N>` | `N` in `1…8192`. Finite floats only. Stored off-row (F16 halves, I8 signed bytes + scale) |
| `BITVECTOR<N>` | `N` single bits, `ceil(N/8)` bytes off-row. Elements are `0`/`1`. `NEAREST … USING HAMMING` |
| `SPARSEVECTOR<N>` | Non-zero index/value pairs, `N` in `1…65535`. `NEAREST` default `COSINE`. `CREATE VECTOR INDEX … USING SPARSE` |
| `POINT` / `LOCATION` | WGS84 longitude, latitude |
| `BOX` | west, south, east, north |
| `LINESTRING` | at least two vertices |
| `POLYGON` | closed exterior ring, optional holes; 256-vertex cap |
| `GEOMETRY` / `GEOGRAPHY` | General OGC geometry (including `Multi*` / `GeometryCollection`), per-column SRID + subtype. Planar vs geodetic. Alongside, not a generalization of, the WGS84 shapes |
| `STRUCT<name T, …>` | Named heterogeneous fields. `STRUCT(expr AS name, …)`, `col.field`. See [Collections](/docs/collections) |
| `ARRAY<T>` | Homogeneous list. `ARRAY(e1, e2, …)`, `ELEMENT_AT`, `CARDINALITY`, `ARRAY_CONTAINS` |
| `MAP<K,V>` | Orderable scalar keys. `MAP(k1, v1, …)`, `ELEMENT_AT`, `MAP_CONTAINS_KEY`, `MAP_KEYS` |

A table **must** declare `PRIMARY KEY`. Secondary indexes store secondary key + primary key. B-tree indexes may add `INCLUDE (cols)`, `WHERE predicate`, and expression keys such as `LOWER(name)`. `EXPLAIN` shows `covering` when the scan reconstructs the row from the index and skips the heap.

## Statements

```text
CREATE TABLE   [FOREIGN KEY / REFERENCES …] [PARTITION BY RANGE|HASH|LIST]
CREATE DATABASE [IF NOT EXISTS]
DROP TABLE [IF EXISTS]
ALTER TABLE    ADD/DROP [COLUMN] | RENAME | ADD/DROP CONSTRAINT
               | ADD/DROP/ATTACH/DETACH PARTITION
CREATE INDEX / CREATE UNIQUE INDEX
CREATE SPATIAL INDEX   (POINT, or GEOMETRY / GEOGRAPHY)
CREATE FULLTEXT INDEX [WITH (ANALYZER = 'simple' | 'english' | 'french' | 'german' | 'spanish')]
CREATE VECTOR INDEX … USING HNSW | IVF | IVFPQ | SPARSE
DROP INDEX [IF EXISTS]
REBUILD INDEX [ONLINE]
MAINTAIN INDEX|TABLE|DATABASE
INSERT   [RETURNING]
UPSERT   [ON UNIQUE] [SET] [RETURNING]
SELECT   [WITH] [DISTINCT] [JOIN …] [WHERE] [GROUP BY] [HAVING] [ORDER BY] [SEARCH] [NEAREST] [NEAREST] [LIMIT] [OFFSET]
UPDATE   [WHERE] [LIMIT] [RETURNING]
DELETE   [WHERE] [LIMIT] [RETURNING]
BEGIN    [READ COMMITTED | SNAPSHOT | SERIALIZABLE]
COMMIT
ROLLBACK [TRANSACTION]
ANALYZE  [table]
EXPLAIN  [ANALYZE] <statement>
CREATE / ALTER / DROP WORKFLOW    RUN WORKFLOW
CREATE / ALTER / DROP TRIGGER
CREATE / ALTER / DROP SCHEDULE    SHOW TASKS    CANCEL TASK
CREATE / ALTER / DROP RESOURCE GROUP
SET RESOURCE GROUP / RESET RESOURCE GROUP
CREATE USER / DROP USER
CREATE ROLE / DROP ROLE
GRANT / REVOKE
SUBSCRIBE TO table [WHERE operation = 'INSERT|UPDATE|DELETE'] [AFTER commit_lsn]
CLUSTER DRAIN | TRANSFER LEADER | MAINTENANCE ENABLE|DISABLE
SHOW DATABASES | TABLES | INDEXES | CONNECTIONS | QUERIES | TRANSACTIONS | LOCKS | CLUSTER | STORAGE
```

`SELECT 1` and other FROM-less `SELECT` expressions are accepted (health checks, `NOW()`, constants).

`CREATE DATABASE [IF NOT EXISTS] name` creates a new database file named `name` in the same directory as the current database (same key provider). It cannot run inside a transaction and is not written to the current database WAL.

`DROP TABLE [IF EXISTS] name` removes the catalog row. A table referenced by a foreign key cannot be dropped (`foreign_key`). After commit and after older snapshots drain, detached heap, vector-store, and index pages return to the durable allocator freelist.

`REBUILD INDEX name` is a blocking rebuild from the transaction snapshot. `REBUILD INDEX name ONLINE` is supported for non-partitioned B+Tree, UNIQUE, JSON-path, and spatial indexes; vector, full-text, and partitioned indexes keep the blocking path.

`SUBSCRIBE` opens a continuous committed-change stream and cannot run inside an
explicit transaction. `AFTER` resumes after an unsigned decimal commit LSN.
See [Change streams](/docs/cdc).

`ALTER TABLE` supports `ADD [COLUMN]`, `DROP [COLUMN]`, `RENAME [COLUMN] … TO`, `RENAME TO`, `ADD CONSTRAINT` / `ADD FOREIGN KEY`, and `DROP CONSTRAINT`. Adding a `NOT NULL` column to a non-empty table requires a `DEFAULT`. A `PRIMARY KEY` column cannot be dropped.

## ORDER BY

`ORDER BY expr [ASC|DESC] [, …]` sorts the projected result. NULLs sort last in `ASC` and first in `DESC`. Keys may be output aliases, 1-based select-list ordinals, or source columns.

`SEARCH` orders by BM25 then primary key unless `ORDER BY` is present. `SEARCH col [WEIGHT n] [, col [WEIGHT n] …] FOR '…'` uses a `FULLTEXT` index whose column list matches in the same order (1–8 `STRING`/`TEXT` columns; phrases do not cross fields; optional `WEIGHT` scales per-field BM25 tf in `(0, 64]`, default 1). Trailing ASCII `*` on a token is prefix search (`cat*` matches `catalog`; exact `cat` does not); trailing ASCII `~` is fuzzy matching (`cat~` matches `cot`; optional `~1` / `~2`); unadorned tokens apply typo tolerance when the term is absent from the vocabulary (`databse` matches `database`); prefix, fuzzy, and typo expansion is fail-closed. `HIGHLIGHT(col)` / `SNIPPET(col)` mark original matching tokens in the SELECT list of a SEARCH query. `SELECT * … SEARCH … FACET col [, col …]` returns independent histograms over the full match set (`facet`, `value`, `count`); `LIMIT` is per-facet top-N. `NEAREST` orders by distance then primary key unless `ORDER BY` is present. Hybrid results are reciprocal-rank fused, then truncated to `LIMIT` / `OFFSET` (or re-sorted when `ORDER BY` is present). A second `NEAREST` (dense `VECTOR` + `SPARSEVECTOR`) is dense+sparse+BM25 fusion. `LIMIT n OFFSET m` skips `m` ordered rows then returns up to `n`. `OFFSET` may appear before `LIMIT`. `OFFSET` without `LIMIT` skips and returns the rest. `UPDATE` / `DELETE` take `LIMIT` only.

## Functions

The complete signatures, return behavior, NULL rules, and examples are in the
[Function reference](/docs/functions).

| Area | Calls |
|---|---|
| Defaults | `UUID()`, `NOW()`, `AI()` |
| Aggregates | `COUNT(*)`, `COUNT(col)`, `SUM`, `AVG`, `MIN`, `MAX`, `ARRAY_AGG`, `MAP_AGG` |
| Windows | `ROW_NUMBER`, `RANK`, `DENSE_RANK`, `LAG`, `LEAD`, `FIRST_VALUE`, `LAST_VALUE`, and aggregate `OVER (...)` |
| String | `LOWER`, `UPPER`, `LENGTH`, `SUBSTRING`, `TRIM`, `LTRIM`, `RTRIM`, `REPLACE`, `CONCAT`, `STARTS_WITH`, `ENDS_WITH`, `CONTAINS` |
| Numeric | `ABS`, `ROUND`, `CEIL`, `FLOOR`, `POWER`, `SQRT`, `MOD` |
| NULL / value | `COALESCE`, `NULLIF`, `GREATEST`, `LEAST` |
| Temporal | `EXTRACT`, `DATE_TRUNC`, `DATE_ADD`, `DATE_DIFF` |
| JSON | `JSON_GET`, `JSON_SET`, `JSON_REMOVE`, `JSON_CONTAINS`, `JSON_ARRAY_LENGTH`, `JSON_TYPE` |
| Vector | `COSINE`, `COSINE_DISTANCE`, `L2`, `L1`, `INNER_PRODUCT`, `VECTOR_DIM`, `VECTOR_NORM`, `VECTOR_NORMALIZE`, `VECTOR_ADD`, `VECTOR_SUBTRACT`, `VECTOR_SCALE` |
| Geo | `POINT`, `BOX`, `LON`/`LAT`, `DISTANCE`, `DISTANCE_SPHEROID`, `DWITHIN`, `WITHIN`, `COVERS`, `LINELENGTH` (and `ST_*` aliases) |
| Collections | `ELEMENT_AT`, `CARDINALITY` / `ARRAY_LENGTH`, `ARRAY_CONTAINS`, `MAP_CONTAINS_KEY`, `MAP_KEYS`, `MAP_VALUES`, `MAP_SIZE` |
| Search | `HIGHLIGHT(col [, pre, post])`, `SNIPPET(col [, width [, pre, post]])` (require `SEARCH`) |

`UUID()`, `NOW()`, and `AI()` are evaluated at execution, not folded by the optimizer. `AI()` is a `DECIMAL(p,0)` autoincrement starting at 1. Explicit inserts bump the sequence when the value is at least the next number. Allocation is in the statement transaction (`ROLLBACK` reuses). Concurrent inserts exclusive-lock the sequence key.

## EXPLAIN

```sql
EXPLAIN SELECT name FROM products WHERE metadata.category = 'headphones';
EXPLAIN ANALYZE SELECT name FROM products SEARCH description FOR 'wireless' LIMIT 5;
```

`ANALYZE` (the statement) writes statistics first. `EXPLAIN ANALYZE` executes the plan. See [hybrid queries](/docs/hybrid) for `Candidates` and `Rerank`.
