# NSQL reference

Companion to `SKILL.md`. Authoritative source is `docs/sql.md` and the
per-model docs; this is a working summary for the current release (**0.0.1**;
driver packages are `0.0.1`).

## Types

| Type | Notes |
|---|---|
| `UUID` | 16 bytes. `DEFAULT UUID()` |
| `BOOL` | `TRUE` / `FALSE` |
| `STRING` / `TEXT` | UTF-8, same encoding; `TEXT` is the long-form name |
| `CHAR(n)` / `VARCHAR(n)` | `n` = rune count. `CHAR` space-pads; `VARCHAR` is a ceiling, never truncates |
| `BLOB` | Raw bytes, no UTF-8 check. Literal `X'DEADBEEF'`. Byte-lexicographic order; valid `PRIMARY KEY` |
| `INT8/16/32/64` | Exact signed. Narrowing / out-of-range errors (no wrap). Arithmetic promotes to `DECIMAL` |
| `UINT8/16/32/64` | Exact unsigned. Negative assignment errors. Arithmetic promotes to `DECIMAL` |
| `DECIMAL(p,s)` | `1 ≤ p ≤ 38`, `s ≤ p`. `DEFAULT AI()` when `s = 0` (autoincrement from 1) |
| `FLOAT32` / `FLOAT64` | IEEE-754. Arithmetic stays floating |
| `DATE` | `YYYY-MM-DD` |
| `TIME` | `HH:MM:SS[.frac]` |
| `TIMESTAMP` | date-time, no zone |
| `TIMESTAMPTZ` | UTC. `DEFAULT NOW()` |
| `INTERVAL` | `INTERVAL '1 month 3 days'`. Calendar arithmetic with date/time types |
| `ENUM('a','b',…)` | Declaration-order ordinals. Isolated from other types except text |
| `JSON` | Binary NSJB. Insert a JSON **text literal**. Path access `col.key`. Depth 32, size 1 MiB |
| `VECTOR<F32,N>` / `<F16,N>` / `<I8,N>` | `N` 1…8192, finite floats. Stored off-row |
| `BITVECTOR<N>` | `N` bits. `NEAREST … USING HAMMING` |
| `SPARSEVECTOR<N>` | index/value pairs, `N` 1…65535. `NEAREST` default `COSINE` |
| `POINT` / `LOCATION` | WGS84 **longitude, latitude** (lon first) |
| `BOX` | west, south, east, north |
| `LINESTRING` / `POLYGON` | ≥2 vertices / closed ring; 256-vertex cap |
| `GEOMETRY` / `GEOGRAPHY` | General OGC geometry, per-column SRID + subtype |
| `STRUCT<name T,…>` | `STRUCT(expr AS name, …)`, `col.field` |
| `ARRAY<T>` | `ARRAY(e1,e2,…)`, `ELEMENT_AT`, `CARDINALITY`, `ARRAY_CONTAINS` |
| `MAP<K,V>` | `MAP(k1,v1,…)`, `ELEMENT_AT`, `MAP_CONTAINS_KEY`, `MAP_KEYS` |

Every table **must** declare `PRIMARY KEY` (the clustered B+Tree key). Secondary
indexes store secondary key + primary key.

## Statements

```text
CREATE TABLE [FOREIGN KEY / REFERENCES …] [PARTITION BY RANGE|HASH|LIST]
CREATE DATABASE [IF NOT EXISTS]            -- new file, same directory; not in a txn
DROP TABLE [IF EXISTS]
ALTER TABLE  ADD/DROP [COLUMN] | RENAME [COLUMN] … TO | RENAME TO
             | ADD/DROP CONSTRAINT | ADD FOREIGN KEY
             | ADD/DROP/ATTACH/DETACH PARTITION
CREATE [UNIQUE] INDEX          -- optional INCLUDE (cols), WHERE pred, expr keys, JSON path
CREATE SPATIAL INDEX
CREATE FULLTEXT INDEX [WITH (ANALYZER = 'simple'|'english'|'french'|'german'|'spanish')]
CREATE VECTOR INDEX … USING HNSW | IVF | IVFPQ | SPARSE
DROP INDEX [IF EXISTS] | REBUILD INDEX [ONLINE] | MAINTAIN INDEX|TABLE|DATABASE
INSERT [RETURNING]
UPSERT [ON UNIQUE (cols)] [SET …] [RETURNING]
SELECT [WITH] [DISTINCT] [JOIN …] [WHERE] [GROUP BY] [HAVING]
       [SEARCH …] [NEAREST …] [NEAREST …] [ORDER BY] [LIMIT] [OFFSET]
UPDATE [WHERE] [LIMIT] [RETURNING]         -- LIMIT only, no ORDER BY
DELETE [WHERE] [LIMIT] [RETURNING]
BEGIN [READ COMMITTED | SNAPSHOT | SERIALIZABLE] / COMMIT / ROLLBACK [TRANSACTION]
ANALYZE [table]
EXPLAIN [ANALYZE] <statement>
CREATE/ALTER/DROP WORKFLOW | RUN WORKFLOW | CREATE/ALTER/DROP TRIGGER
CREATE/ALTER/DROP SCHEDULE | SHOW TASKS | CANCEL TASK
CREATE/ALTER/DROP RESOURCE GROUP | SET/RESET RESOURCE GROUP
CREATE USER / DROP USER | CREATE ROLE / DROP ROLE | GRANT / REVOKE
SUBSCRIBE TO table [WHERE operation = 'INSERT'|'UPDATE'|'DELETE'] [AFTER commit_lsn]
CLUSTER DRAIN | TRANSFER LEADER | MAINTENANCE ENABLE|DISABLE
SHOW DATABASES | TABLES | INDEXES | CONNECTIONS | QUERIES | TRANSACTIONS | LOCKS | CLUSTER | STORAGE
```

`SELECT 1` and other FROM-less `SELECT`s are accepted (health checks, `NOW()`).

## Functions

| Area | Calls |
|---|---|
| Defaults | `UUID()`, `NOW()`, `AI()` — evaluated at execution, never folded |
| Aggregates | `COUNT(*)`, `COUNT(col)`, `SUM`, `AVG`, `MIN`, `MAX` |
| Windows | `ROW_NUMBER`, `RANK`, `DENSE_RANK`, `LAG`, `LEAD`, `FIRST_VALUE`, `LAST_VALUE`, aggregate `OVER (…)` |
| Temporal | `DATE_TRUNC`, `DATE_ADD`, `DATE_DIFF` |
| Vector | `COSINE`, `L2`, `INNER_PRODUCT`, `VECTOR_DIM`, `VECTOR_NORM`, `VECTOR_NORMALIZE` |
| Geo | `POINT`, `BOX`, `LON`/`LAT`, `DISTANCE`, `DISTANCE_SPHEROID`, `DWITHIN`, `WITHIN`, `COVERS`, `LINELENGTH` (+ `ST_*` aliases) |
| Collections | `ELEMENT_AT`, `CARDINALITY`/`ARRAY_LENGTH`, `ARRAY_CONTAINS`, `MAP_CONTAINS_KEY`, `MAP_KEYS`, `MAP_VALUES`, `MAP_SIZE` |
| Search | `HIGHLIGHT(col[,pre,post])`, `SNIPPET(col[,width[,pre,post]])` — require `SEARCH` |

## SEARCH / NEAREST

- `SEARCH col [WEIGHT n] [, col …] FOR '…'` — needs a `FULLTEXT` index whose
  column list matches in the same order (1–8 `STRING`/`TEXT` columns). Orders by
  BM25 then primary key unless `ORDER BY` is present.
- Token modifiers: `cat*` prefix, `cat~`/`cat~1`/`cat~2` fuzzy, unknown terms get
  typo tolerance (fail-closed, 4096-term vocab scan cap).
- `NEAREST col TO <vector> [USING COSINE|L2|INNER_PRODUCT|HAMMING] LIMIT k` —
  orders by distance then primary key unless `ORDER BY`.
- At most two `NEAREST` clauses: one dense `VECTOR`, one `SPARSEVECTOR`.
- Hybrid results are reciprocal-rank fused (`k = 60`) then truncated to
  `LIMIT`/`OFFSET`, or re-sorted when `ORDER BY` is present.
- `SEARCH`/`NEAREST` + `INNER JOIN` only when the rank column is on the `FROM`
  table. Outer join + `SEARCH`/`NEAREST` is unsupported.

## Transaction isolation

| Level | Snapshot | Locks |
|---|---|---|
| `READ COMMITTED` | refreshed each statement | exclusive key locks until txn end |
| `SNAPSHOT` (default) | taken at `BEGIN` | exclusive key locks; first-committer-wins on write-write |
| `SERIALIZABLE` | taken at `BEGIN` | + shared key/range locks (strict 2PL, not SSI) |

Deadlock aborts the requester (`deadlock`); it must `ROLLBACK`. Commit is acked
only after group-commit WAL + `fsync` (and a Raft quorum on a cluster).

## Error codes (`internal/nerr`)

`invalid_argument`, `invalid_format`, `corruption`, `not_found`,
`already_exists`, `page_full`, `exhausted`, `crypto`, `io`, `internal`,
`unavailable`, `conflict`, `deadlock`, `serialization`, `syntax`, `unauthorized`,
`forbidden`, `protocol`, `canceled`, `foreign_key`.

Common ones in application code:

- `conflict` — second query on a single-flight connection while rows are open;
  or `SNAPSHOT` write-write loser.
- `serialization` / `deadlock` — retry the whole transaction after `ROLLBACK`.
- `foreign_key` — missing parent, `RESTRICT` children, or illegal `SET DEFAULT`.
- `exhausted` — a cap was hit (FK cascade rows, `FULL` join spill, result size).
- `unauthorized` / `forbidden` — auth failed / RBAC denied.
- `unavailable` — no Raft leader (writes fail closed).

## Hard limits

| Limit | Value |
|---|---|
| Logical page | 16 KiB |
| Packet / SQL text | 1 MiB |
| Bind parameters | 256 |
| JSON depth / size | 32 / 1 MiB |
| Vector dimension | 8192 dense/bit; 65535 `SPARSEVECTOR<N>` |
| LINESTRING / POLYGON vertices | 256 |
| Collection nesting / `ARRAY` elements | 8 / 2²⁰ |
| JOIN tables | 8 (`FROM` + 7 `JOIN`) |
| Foreign keys per table / columns per FK / cascade depth | 16 / 8 / 8 |
| FK cascade touched rows | 100 000 |
| Workflow body statements / nested depth | 256 / 8 |
| Wire result / default result rows | 64 MiB / 1 000 000 |

## Not in 0.0.1

- Outer `JOIN` with `SEARCH` / `NEAREST` (inner join is fine when the rank
  column is on the `FROM` table).
- IVF / IVF-PQ / `SPARSE` vector indexes on partitioned tables (partition-local
  HNSW works).
- Partial / expression / JSON-path `UNIQUE` on partitioned tables;
  partitioned-table foreign keys.
- Searchable or deterministic field-level `ENCRYPTED CLIENT` (randomized
  `NSCE1.` with Go/JS/PHP helpers exists, labeled experimental).
- `ARRAY_AGG` / `MAP_AGG` / `UNNEST`, collection subscript sugar.
- Multi-primary writes; hosted HA; per-hosted-database backup/PITR addressing.

Never treat marketing language as a guarantee. `nextsql-bench --slo` on your
hardware is the source of latency numbers.
