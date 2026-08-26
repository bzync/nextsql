# SQL (Phase 5–7)

NextSQL dialect over the Phase 4 MVCC engine. Remote access uses the Phase 8 native protocol (`docs/protocol.md`).

The SQL design baseline is ISO/IEC 9075:2023. Part-specific references and the
rule against unsupported conformance claims are documented in
`docs/standards.md`; NextSQL-native semantics in this document remain
authoritative for the shipped version.

## Pipeline

```text
SQL → lexer → parser → AST → binder / catalog → logical plan
    → rewrite → physical alternatives → cost model → vectorized executor
```

Packages: `internal/sql/{lexer,parser,ast,binder,planner,optimizer}`, `internal/catalog`, `internal/executor`.

See `docs/optimizer.md` for rewrites, statistics, costing, and EXPLAIN.

The native P19 `WORKFLOW` / `TRIGGER` / `SCHEDULE` / `TASK` contract is
specified in `docs/workflows.md`. The native v1 implementation and its targeted
workflow, trigger, schedule, durable-task, failover, PITR, and driver gates are
complete; P19 remains open for the clean repository-wide functional gate in
`TODO.md`.

## Statements

`CREATE TABLE` (including `FOREIGN KEY` / column `REFERENCES`), `CREATE DATABASE` [`IF NOT EXISTS`], `DROP TABLE` [`IF EXISTS`], `ALTER TABLE` (`ADD`/`DROP` `[COLUMN]`, `RENAME` `[COLUMN]`/`TO`, `ADD`/`DROP CONSTRAINT`), `CREATE INDEX` / `CREATE UNIQUE INDEX` (including JSON paths such as `metadata.category`, `INCLUDE`, `WHERE`, and expression keys), `CREATE SPATIAL INDEX`, `CREATE FULLTEXT INDEX`, `CREATE VECTOR INDEX … USING HNSW`, `DROP INDEX` [`IF EXISTS`], `REBUILD INDEX`, `CREATE` / `ALTER` / `DROP WORKFLOW`, `RUN WORKFLOW`, `CREATE` / `ALTER` / `DROP TRIGGER`, `CREATE` / `ALTER` / `DROP SCHEDULE`, `SHOW TASKS`, `CANCEL TASK`, `MAINTAIN DATABASE` / `MAINTAIN TABLE` / `MAINTAIN INDEX`, `INSERT` [`RETURNING`], `UPSERT` [`ON UNIQUE`] [`SET`] [`RETURNING`], `SELECT` (including `WITH` / `WITH RECURSIVE`, `DISTINCT`, `JOIN` / `GROUP BY` / `ORDER BY` / `LIMIT` / `OFFSET` / `COUNT` `SUM` `AVG` `MIN` `MAX`, window functions with `OVER`, JSON path extract, `SEARCH col FOR '…'`, and `NEAREST col TO …`), `UPDATE` [`RETURNING`], `DELETE` [`RETURNING`], `BEGIN` [`READ COMMITTED` | `SNAPSHOT` | `SERIALIZABLE`], `COMMIT`, `ROLLBACK`, `SET TENANT` / `RESET TENANT` (`docs/security.md`), `ANALYZE` [`table`], `EXPLAIN` [`ANALYZE`] `<statement>`, `CREATE USER` / `DROP USER`, `CREATE ROLE` / `DROP ROLE`, `GRANT` / `REVOKE` (`docs/security.md`).

Unquoted identifiers fold to lowercase. Quoted `"ident"` is preserved.

Table names that start with `nsql_` (case-folded) are reserved. The only exception is `CREATE TABLE nsql_schema_migrations` with the exact history DDL (`docs/design-cli-migrate-fk-joins.md` C.2) when that table is absent. Any other `nsql_*` name, or a different column list for `nsql_schema_migrations`, is `invalid_argument`. After that reserved DDL is accepted, the executor grants `SELECT`/`INSERT`/`UPDATE`/`DELETE` on the table to the session user (no `GRANT` SQL, no `PrivGrant`).

Reserved words include `FOREIGN`, `REFERENCES`, `CONSTRAINT`, `CASCADE`, `RESTRICT`, `ACTION`, `MATCH`, `ALTER`, `ADD`, `RENAME`, `ORDER`, `ASC`, `DESC`, `IF`, `EXISTS`, `WITH`, `OVER`, `UPSERT`, and `RETURNING` (same rule as `USER`, `KEY`, `TO`): unquoted they are keywords; quoted `"foreign"` is an identifier. `RECURSIVE`, `MATERIALIZED`, `PARTITION`, `ROWS`, `RANGE`, `UNBOUNDED`, `PRECEDING`, `FOLLOWING`, `CURRENT`, `ROW`, `EXCLUDED`, and `INCLUDE` are contextual identifiers, not reserved words.

## Foreign keys

Declared at `CREATE TABLE` or `ALTER TABLE ADD CONSTRAINT` / column `REFERENCES`. `ALTER TABLE DROP CONSTRAINT` removes a stored foreign key by name.

DML enforces referential actions on the leader as ordinary row writes (followers apply WAL only). `INSERT` / `UPDATE` of a fully non-null `MATCH SIMPLE` key fails with `foreign_key` if the referenced parent row is missing.

- `RESTRICT` / `NO ACTION` (synonyms; there is no deferred check): `DELETE` / `UPDATE` of a referenced parent key fails with `foreign_key` if any child row still points at the old key.
- `CASCADE`: delete matching children (recursive) or rewrite their FK columns to the new parent key.
- `SET NULL`: set those FK columns to typed NULL.
- `SET DEFAULT`: evaluate each FK column as `ApplyDefault(i, Null(type))` — not the live value — then write the child. `UUID()` / `NOW()` / `AI()` run once on the leader. If the result is still NULL on a `NOT NULL` column, or still names the old parent key, the statement fails with `foreign_key`.

A cascade that would exceed depth 8 or 100 000 touched child rows fails with `exhausted` and rolls back the statement. In an explicit transaction the session aborts so `COMMIT` cannot persist a partial cascade. Self-referential cycles use a per-statement visited-key set; a self-row `ON UPDATE` is retargeted onto the already-moved parent identity.

```sql
CREATE TABLE orders (
    tenant_id   UUID NOT NULL,
    id          UUID NOT NULL DEFAULT UUID(),
    customer_id UUID NOT NULL,
    PRIMARY KEY (tenant_id, id),
    CONSTRAINT fk_orders_customer
        FOREIGN KEY (tenant_id, customer_id)
        REFERENCES customers (tenant_id, id)
        ON DELETE RESTRICT
        ON UPDATE RESTRICT
);

CREATE TABLE lines (
    id       UUID PRIMARY KEY,
    order_id UUID NOT NULL REFERENCES orders (id) ON DELETE CASCADE
);
```

- `MATCH SIMPLE` only (default, optional). `MATCH FULL` is rejected.
- Actions: `RESTRICT` (default), `NO ACTION` (stored as `RESTRICT`), `CASCADE`, `SET NULL`, `SET DEFAULT`.
- Referenced columns must be exactly the parent `PRIMARY KEY` or a `UNIQUE` btree index as a set (order may differ; no superkeys). `VECTOR` and `JSON` cannot be FK columns. `DECIMAL` precision and scale must match.
- Unnamed constraints are named `fk_<child>_<cols>`, truncated to 63 characters, uniqued with a numeric suffix.
- At most 16 foreign keys per table and 8 columns per key.
- If both tables are tenant-keyed, the FK must include `tenant_id` on both sides at the **same position**. A tenant-keyed parent cannot be referenced by a global child. Cascades call `checkTenantRow` on every child; a bound session cannot cascade into another tenant.
- Cyclic `CASCADE` graphs are rejected at DDL time. Self-referential FKs and cyclic `RESTRICT` graphs are allowed.
- A child `CREATE TABLE` in the same transaction can reference a parent created earlier in that transaction (session overlay). `CREATE TABLE` bind and parent `DELETE` inbound probes both use overlay ∪ catalog lookup, so an uncommitted child table is visible to the same-txn parent delete.
- `MATCH SIMPLE`: if any foreign-key column is NULL, the existence check and parent-delete probe for that constraint are skipped.

Do not revert this binary after a catalog rewrite has written `NSCT` v4.
Restore a pre-v4 backup or use an explicit format-aware migration first.

## Types

| Type | Storage | Notes |
|---|---|---|
| `UUID` | 16 bytes | `DEFAULT UUID()` |
| `STRING` / `TEXT` | `u32` length + UTF-8 | same encoding |
| `DECIMAL(p,s)` | `1 <= p <= 38`, `s <= p` | unscaled integer + scale; `DEFAULT AI()` when `s = 0` |
| `TIMESTAMPTZ` | `int64` UTC nanos | `DEFAULT NOW()` |
| `JSON` | compact binary `NSJB` | path extract and path indexes; see `docs/json.md` |
| `VECTOR<F32,N>` | heap reference; payload in vector store | `NEAREST`, `COSINE` / `L2` / `INNER_PRODUCT`; see `docs/vector.md` |
| `POINT` / `LOCATION` | lon, lat `float64` | WGS84; see `docs/geo.md` |
| `BOX` | west, south, east, north | axis-aligned lon/lat box |
| `LINESTRING` | `u16` count + lon/lat pairs | at least two vertices; see `docs/geo.md` |
| `POLYGON` | rings of closed lon/lat | exterior + optional holes; 256-vertex cap |

A table must declare a `PRIMARY KEY`. That key is the clustered B+Tree key.

## DROP / ALTER / CREATE DATABASE

```sql
DROP TABLE [IF EXISTS] items;
DROP INDEX [IF EXISTS] index_name;
REBUILD INDEX index_name;

ALTER TABLE items ADD note STRING;
ALTER TABLE items ADD extra STRING NOT NULL DEFAULT 'z';
ALTER TABLE items DROP COLUMN extra;
ALTER TABLE items RENAME COLUMN note TO body;
ALTER TABLE items RENAME TO products;
ALTER TABLE orders ADD CONSTRAINT fk_orders_customer
    FOREIGN KEY (customer_id) REFERENCES customers (id);
ALTER TABLE orders DROP CONSTRAINT fk_orders_customer;

CREATE DATABASE [IF NOT EXISTS] analytics;
```

`DROP TABLE` deletes the catalog descriptor (and stats). A parent still referenced by a child foreign key is rejected (`foreign_key`). After commit and after older snapshots drain, detached heap, vector-store, and index pages are returned to the durable allocator freelist.

`DROP INDEX` removes B+Tree, UNIQUE, JSON-path, spatial, full-text, and HNSW
indexes transactionally. Index names are resolved across the database; if the
same name exists on multiple tables, the statement is rejected as ambiguous.
Dropping the last UNIQUE index that supports an inbound foreign key is also
rejected. The operation requires `INDEX` privilege on the resolved table, is
written to the DDL audit stream, and is quorum-replicated through Raft; follower
catalogs install the drop through the replicated WAL batch. The operation is
rejected on non-leader nodes. After commit and after older snapshots drain, the
detached index pages are returned to the durable allocator freelist.

`REBUILD INDEX` performs a blocking rebuild from the transaction snapshot. It
creates a fresh detached structure, preserves the index kind, columns, JSON
path, uniqueness, and vector/full-text/spatial options, then swaps the catalog
metadata at commit. Rollback or a pre-commit crash leaves the old index active.
After older snapshots drain, the replaced physical index is reclaimed.
`REBUILD INDEX ... ONLINE` is not accepted; online rebuild remains deferred
until concurrent-write handling is proven safe.

`MAINTAIN INDEX index_name`, `MAINTAIN TABLE table_name`, and
`MAINTAIN DATABASE` perform a leader-only,
blocking maintenance pass capped at 10,000 physical tombstones per statement.
They cannot run inside a transaction. Table scope covers its heap, vector store,
and indexes; index scope touches only the resolved physical index, and database
scope also covers the catalog and all tables. Index names are resolved across
the database and ambiguous names are rejected. The result's
affected count is the number of physical tombstones removed. With ACLs enabled,
cluster `ADMIN` is required because maintenance crosses tenant boundaries.

`ALTER TABLE ADD COLUMN` appends a column and rewrites existing rows (NULL or the column default). A `NOT NULL` column on a non-empty table requires `DEFAULT`. `DROP COLUMN` cannot remove a primary-key column or a column used by a foreign key; secondary indexes that include the column are dropped from the catalog. `RENAME` is a catalog update (table rename also rewrites inbound `REFERENCES` names). `ADD CONSTRAINT` validates existing rows, then stores the foreign key.

`CREATE DATABASE name` creates a new database file named `name` next to the current file, using the same key provider. It is not a catalog object inside the current file, is not WAL-replicated with the current database, and cannot run inside a transaction.

## Catalog

The superblock primary tree holds catalog rows. Key `T` + table name. Value is a versioned `NSCT` descriptor (columns, PK, index list, heap meta page, foreign keys, CDC image policy, and the bounded P21 physical-partition metadata foundation). `EncodeTable` writes version 4. `DecodeTable` accepts v1 (empty FK list), v2 (key-only CDC), v3 (CDC image policy), and v4; any other version fails closed. Key `S` + table name holds a versioned `NSST` statistics snapshot from `ANALYZE`. Key `A` + table ID + column name holds the next `AI()` value for that column (same transaction as the insert). Each user table and secondary index is a detached B+Tree whose root/height live on a slotted meta page (`NSTM`) so splits do not rewrite the catalog row. Any catalog rewrite upgrades an older descriptor to v4. `PARTITION BY RANGE`, `PARTITION BY HASH`, `PARTITION BY LIST`, and `PARTITION BY TENANT(tenant_id)` are available as a bounded slice (single column, partition-local heaps, secondary indexes deferred); see `docs/partitioning.md` for the shipped syntax and physical/`EXPLAIN` pruning. Multi-column partition keys remain reserved and fail closed.

Catalog mutations use the same WAL + MVCC transaction as user data. Recovery replays WAL, applies UNDO, then the executor reloads the catalog from the primary tree.

## Rows

User payloads are `NSRW` records: version, null bitmap, typed values. MVCC still wraps that payload in `NSRV` on the leaf (`docs/mvcc.md`).

Secondary indexes store secondary key + primary key (non-unique) or secondary key with primary key as the value (unique). `INCLUDE` columns are appended to that payload. Expression keys store the evaluated result, not the source column.

## B-tree index extensions

```sql
CREATE INDEX ix_cover ON items (name) INCLUDE (note, qty);
CREATE INDEX ix_active ON items (name) WHERE status = 'active';
CREATE INDEX ix_lower ON items (LOWER(name));
CREATE UNIQUE INDEX ux_email ON users (email) INCLUDE (name);
```

- `INCLUDE (col, …)` stores extra columns in the leaf payload. They are not part of the sort key or uniqueness. At most 16. They cannot repeat a non-expression key column or a `VECTOR` column. Spatial, full-text, and vector indexes reject `INCLUDE`.
- `WHERE predicate` is a partial index. Only rows for which the predicate is true are stored. `NULL` and false do not match. The optimizer uses the index only when the query `WHERE` implies the predicate (equality, range subset, `AND` of implied conjuncts; fail closed on anything unproven). `UUID()`, `NOW()`, `AI()`, subqueries, windows, aggregates, and parameters are rejected.
- Expression keys such as `LOWER(name)` or `(LOWER(name))` are matched only against the same expression in the query (`WHERE LOWER(name) = 'x'`), not against the source column. Volatile, mutating, geo, and vector functions are rejected. Uniqueness is on the expression result.
- When every column needed by the residual predicate and the output can be reconstructed from the index key, primary key, `INCLUDE` payload, or an equality constant implied by a partial predicate, `EXPLAIN` shows `IndexScan … covering` and the executor skips the heap fetch.

Old catalog rows without these fields still decode. Descriptors that store `INCLUDE`, `WHERE`, or expression keys set extra index flag bits; older binaries fail closed on those bytes.

## Executor

Vectorized. Each session statement without `BEGIN` is auto-commit. `BEGIN` starts one engine transaction shared across the catalog tree, table heaps, and indexes. Readers do not see uncommitted writes. Official storage still encrypts pages, WAL, and UNDO.

`SELECT` executes in columnar batches (1024 / 2048 / 4096). `Exec` materializes under a per-query memory / time / I/O / result-row / result-byte budget (default 1 000 000 rows, 64 MiB). Overload is queued or rejected (`docs/ops.md`). `Query` / `Stream` expose batches so callers need not retain a huge result. See `docs/execution.md`.

Access paths: sequential heap scan (optionally parallel), clustered PK lookup/range, secondary index lookup/range (heap fetch by primary key). Residual predicates stay as filters. Joins are `INNER JOIN` (bare `JOIN` is the same), `LEFT` / `RIGHT` / `FULL` `[OUTER] JOIN`, or `CROSS JOIN`, up to eight tables (`FROM` + seven joins). Inner joins are cost-based left-deep (hash-build the smaller side; equal costs keep written order). Outer joins are not reordered and require `ON`; `CROSS JOIN … ON` is a syntax error. `SEARCH` / `NEAREST` (including hybrid) may run on the `FROM` table of an inner-join query: the engine ranks that table first, then joins. A search/nearest column on a joined table, or `SEARCH`/`NEAREST` with an outer join, is rejected. Hash is the default; merge is used when both sides are already index-ordered on the equality keys (INNER or LEFT). `FULL` is hash-only and memory-capped (v1 does not spill; exceeding the budget is `exhausted`). `RIGHT` is rewritten to `LEFT` with swapped inputs and a column-order `Project`. A `NULL` join key never matches (`NULL = NULL` is unknown). Unmatched outer-join rows are emitted with typed NULLs. Columns from a null-extended side are nullable in the bound schema. Aggregates are hash-based. `ORDER BY` sorts the projected result (NULL values sort last in `ASC` and first in `DESC`) and may list output aliases, ordinals, or source columns; a sort sits above `Project`/`Aggregate` and below `LIMIT` / `OFFSET`. `SEARCH` / `NEAREST` rank order is replaced when `ORDER BY` is present.

`SELECT DISTINCT` uses a memory-budgeted hash operator after projection or
aggregation and before `ORDER BY`, `LIMIT`, and `OFFSET`. NULLs compare equal
for duplicate elimination. With `DISTINCT`, every `ORDER BY` expression must
also appear in the select output. When the ordering keys cover every output
column, the planner uses `OrderedDistinct` and removes adjacent duplicates from
the sorted stream without building a hash table. For a single-table projection
containing a complete primary key or complete `NOT NULL` UNIQUE-index key,
`IndexDistinct` proves the rows are already unique and elides duplicate work.

`HAVING` runs after aggregation and before DISTINCT, ordering, and limits. It
may reference grouped expressions that appear in the output, selected aggregate
expressions, or their output aliases. Aggregate aliases are visible in `HAVING`
and `ORDER BY`; an aggregate used only in `HAVING` must first be selected in
this version.

Both searched `CASE WHEN condition THEN value ... ELSE value END` and simple
`CASE expression WHEN value THEN result ... END` are supported and may be
nested. Arms are evaluated in order and only the selected result expression is
evaluated. A missing `ELSE` returns NULL; NULL conditions and NULL simple-case
comparisons do not match.

String built-ins `LOWER(value)` and `UPPER(value)` apply Unicode case mapping
and preserve STRING versus TEXT. `LENGTH(value)` returns the number of Unicode
code points, not encoded UTF-8 bytes. All three propagate NULL and reject
non-string values.

`SUBSTRING(value, start [, length])` uses 1-based Unicode code-point indexes.
`TRIM`, `LTRIM`, and `RTRIM` remove Unicode whitespace. `REPLACE` performs
literal all-occurrence replacement; `CONCAT` accepts one or more strings and
widens to TEXT when any input is TEXT. `STARTS_WITH`, `ENDS_WITH`, and
`CONTAINS` are case-sensitive literal predicates. These functions propagate
NULL.

`COALESCE` evaluates arguments left-to-right and stops at the first non-NULL
value. `NULLIF(a, b)` returns a typed NULL when the coercible values compare
equal. `GREATEST` and `LEAST` compare one or more coercible values and propagate
NULL if any input is NULL.

Exact DECIMAL functions include `ABS`, `CEIL`, `FLOOR`, `MOD`, and
`ROUND(value [, scale])`. ROUND uses half-away-from-zero ties and requires a
non-negative result scale. These operations do not convert through binary
floating point; MOD rejects a zero divisor and all propagate NULL.

`POWER(base, exponent)` and `SQRT(value)` return DECIMAL approximations rounded
to eight fractional digits, matching the engine's numeric approximation
boundary. Non-finite POWER results and square roots of negative values fail
explicitly; NULL propagates.

Read-only JSON functions operate directly on validated binary NSJB documents.
`JSON_GET(doc, path)` returns a typed SQL scalar or a JSON container;
`JSON_ARRAY_LENGTH(doc [, path])` reads an array count without text decoding;
and `JSON_TYPE(doc [, path])` returns `object`, `array`, `string`, `number`,
`boolean`, or `null`. Paths accept `a.b.0` and `$.a.b.0`. Missing paths and SQL
NULL documents return SQL NULL.

`JSON_SET(doc, path, value)` replaces an existing value, creates missing object
keys, and requires array indexes to exist. `JSON_REMOVE(doc, path)` removes an
object key or array element and treats a missing path as a no-op.
`JSON_CONTAINS(doc, target)` uses recursive object-subset containment and
requires every target array element to be contained by a source element.
Mutations are revalidated and canonicalized as NSJB before returning.

A constant-path predicate such as `JSON_GET(doc, '$.category') = 'books'` is
canonicalized during binding to the same native path expression as
`doc.category`, preserving JSON-path index matching. Dynamic paths remain
runtime function calls and are not considered sargable.

Date/time functions use UTC and accept units `year`, `month`, `day`, `hour`,
`minute`, and `second`. `EXTRACT(unit, ts)` returns the UTC field;
`DATE_TRUNC(unit, ts)` returns the containing boundary;
`DATE_ADD(ts, integer, unit)` uses calendar addition for year/month/day and
elapsed durations for smaller units; and `DATE_DIFF(start, end, unit)` returns
calendar boundary differences for year/month and truncated elapsed differences
for smaller units. Unknown units and non-integral DATE_ADD amounts fail.

An ordinary `ORDER BY` directly below `LIMIT`/`OFFSET` uses a bounded max-heap
`TopNSort` with capacity `LIMIT + OFFSET`, then applies the offset. Ordered
DISTINCT deliberately disables this optimization because duplicate elimination
must occur before limiting.

`UNION ALL` combines two or more SELECT results left-to-right and preserves
duplicates. Each arm is independently bound, tenant-filtered, authorized, and
optimized. All arms must return the same number of columns; output names come
from the first arm. Equal types remain stable, STRING/TEXT widens to TEXT, and
DECIMAL widens precision and scale without losing integer digits. Other
incompatible types fail explicitly.

`UNION` uses the same query-arm pipeline and removes duplicate typed rows after
combining them. Duplicate elimination treats NULLs as equal, so repeated
all-NULL rows collapse to one result.

`INTERSECT` returns distinct rows present in both inputs; `EXCEPT` returns
distinct left rows absent from the right. Both use typed-row equality with NULL
equal for set membership. `INTERSECT` binds more tightly than left-associative
`UNION` and `EXCEPT`. `INTERSECT ALL` and `EXCEPT ALL` are rejected explicitly.

A scalar subquery may appear wherever an expression is accepted. It must expose
exactly one column and return at most one row; an empty result becomes SQL NULL,
while multiple rows fail explicitly.

`expr IN (SELECT ...)` and `expr NOT IN (SELECT ...)` require one subquery
column and use three-valued SQL membership. A matching value wins even when
other rows are NULL; without a match, a NULL on either side produces UNKNOWN.
An empty input makes IN false and NOT IN true. `EXISTS` and `NOT EXISTS` inspect
only whether the nested query produced a row, independent of its values.
Each uncorrelated subquery occurrence is evaluated and materialized once per
statement, then reused across outer rows. Separate occurrences retain separate
evaluation identities, including when their query text is identical.

An aliased SELECT may be used as a derived table in `FROM`. Its visible output
names form the outer query schema, and the outer query may project, filter,
deduplicate, and order those rows. Inner `DISTINCT`, `ORDER BY` (including
hidden sort keys), and `LIMIT`/`OFFSET` apply to the derived input before the
outer query sees it. The alias is mandatory. Joining directly from a derived
input is rejected until derived-input join planning is complete.

Scalar, IN, and EXISTS subqueries may reference columns from their immediate
outer query. Inner-scope columns win for unqualified ambiguous names; explicit
outer aliases are recommended. Correlated queries execute against each outer
row and never use the uncorrelated result cache.

Simple `EXISTS` / `IN` predicates in `WHERE` are flattened to hash semi-joins
when the inner query is a single-table `SELECT` without `DISTINCT`, `GROUP BY`,
`LIMIT`, joins, or nested subqueries. `NOT EXISTS` becomes a hash anti-join.
`NOT IN` becomes an anti-join only when the inner column is `NOT NULL`; a
nullable inner column keeps three-valued nested evaluation because a NULL on
the right makes a non-match UNKNOWN rather than TRUE. Flattened joins keep
left-row cardinality (duplicate inner matches do not duplicate the outer row).
`EXPLAIN` shows `HashSemiJoin` or `HashAntiJoin`. Nested evaluation remains
for scalar subqueries and shapes that are not proven safe to flatten.

Tenant predicates and `SELECT` privileges apply to every subquery table, whether
the subquery stays nested or is rewritten as a semi/anti-join. Filters above a
derived table still push through an ordinary projection when they do not depend
on output aliases.

`WITH` introduces named common table expressions for the following query. Later
CTEs may reference earlier ones; a CTE name shadows a catalog table of the same
name. Optional column aliases rename the CTE output. At most 32 CTEs appear in
one `WITH` list.

The optimizer inlines a CTE when that is safe and cheaper: a single reference,
or a cheap scan-shaped body used more than once (so predicates can still push
into the scan). It materializes when the body is referenced more than once and
is not cheap, when the body uses `UUID()` / `NOW()` / `AI()`, or when
`AS MATERIALIZED` is written. `AS NOT MATERIALIZED` forces inlining unless the
body is volatile. `EXPLAIN` shows `Materialize` / `CTEScan` for a materialized
CTE and omits those operators when the CTE is inlined.

```sql
WITH recent AS (
    SELECT id, value FROM items WHERE value = 'a'
),
     counted AS MATERIALIZED (
    SELECT value, COUNT(*) AS n FROM recent GROUP BY value
)
SELECT value, n FROM counted;
```

`WITH RECURSIVE` allows a CTE to reference itself through `UNION` or
`UNION ALL`. The left term must not reference the CTE; the recursive term may.
The recursive term cannot use `DISTINCT`, aggregation, `ORDER BY`, `LIMIT` /
`OFFSET`, window functions, or outer joins. Recursion is bounded: at most 100 working-table
iterations, the statement row budget, the memory budget, and the query time
budget. Exceeding any bound is `exhausted`. A self-reference without
`RECURSIVE` is rejected. Recursive CTEs always materialize. A recursive term
may `JOIN` the working table; joining from an ordinary derived table remains
unsupported.

```sql
WITH RECURSIVE walk AS (
    SELECT id, parent FROM org WHERE parent IS NULL
    UNION ALL
    SELECT o.id, o.parent FROM org o JOIN walk ON o.parent = walk.id
)
SELECT id FROM walk;
```

CTE names in nested `IN` / `EXISTS` / scalar subqueries are expanded to the CTE
query as a derived table. Tenant predicates and `SELECT` privilege checks apply
to the underlying tables inside each CTE body, not to the CTE name.

Window functions use `fn(...) OVER ( [PARTITION BY ...] [ORDER BY ...] [frame] )`
in the select list or `ORDER BY`. They run after `FROM` / `WHERE` / `JOIN` /
`GROUP BY` / `HAVING` and before `DISTINCT`, query `ORDER BY`, `LIMIT`, and
`OFFSET`. Nested window functions are rejected. Windows are not allowed in
`WHERE`, `GROUP BY`, `HAVING`, or `JOIN`.

Supported functions: `ROW_NUMBER()`, `RANK()`, `DENSE_RANK()`,
`LAG(expr [, offset [, default]])`, `LEAD(expr [, offset [, default]])`,
`FIRST_VALUE(expr)`, `LAST_VALUE(expr)`, and aggregate windows
`COUNT` / `SUM` / `AVG` / `MIN` / `MAX`. Ranking functions and `LAG` / `LEAD`
ignore frames. `LAG` / `LEAD` offset is a non-negative integer literal
(default 1). Without an explicit default, an out-of-range offset is NULL.

Frames are `ROWS` or `RANGE` with bounds `UNBOUNDED PRECEDING` /
`UNBOUNDED FOLLOWING` / `CURRENT ROW` / `n PRECEDING` / `n FOLLOWING`.
`BETWEEN` is accepted. Default frame with `ORDER BY` is
`RANGE BETWEEN UNBOUNDED PRECEDING AND CURRENT ROW`; without `ORDER BY` it is
the whole partition. `RANGE` uses peer groups for `CURRENT ROW` (equal
`ORDER BY` keys, NULLs equal). `RANGE` offsets (`n PRECEDING` / `n FOLLOWING`)
are rejected. `ROWS` offsets are physical row counts. An empty frame yields
NULL for `SUM` / `AVG` / `MIN` / `MAX` / `FIRST_VALUE` / `LAST_VALUE` and 0
for `COUNT`. `LAST_VALUE` with the default frame returns the current row (or
its peer group under `RANGE`), not the last row of the partition.

`PARTITION BY` NULLs form one partition. Window `ORDER BY` uses the same NULL
ordering as query `ORDER BY` (NULL last in `ASC`, first in `DESC`). Ranking
ties share `RANK` with gaps and `DENSE_RANK` without gaps; `ROW_NUMBER` is
unique and stable for ties. Window execution charges the query memory budget
and can spill partition buckets to encrypted temp files; a single partition
that does not fit is `exhausted`. Cancellation and the time budget are checked
during processing.

```sql
SELECT k, v,
       ROW_NUMBER() OVER (PARTITION BY k ORDER BY v) AS n,
       SUM(v) OVER (PARTITION BY k ORDER BY v
                    ROWS BETWEEN UNBOUNDED PRECEDING AND CURRENT ROW) AS running
FROM items;
```

`EXPLAIN` shows `Window`. Tenant predicates and `SELECT` privilege checks apply
to the underlying table; a window cannot see another tenant's rows.

## UPSERT and RETURNING

`UPSERT` is a native insert-or-update. It is not `INSERT ON CONFLICT` and not
`REPLACE`. The conflict target is the `PRIMARY KEY` or a `UNIQUE` btree index.

```sql
UPSERT INTO items (id, email, name) VALUES ('1', 'a@b', 'Ann');
UPSERT INTO items (id, email, name) VALUES ('2', 'a@b', 'Bea')
    ON UNIQUE (email)
    SET name = excluded.name
    RETURNING id, name;
```

- `ON UNIQUE (cols)` names the full primary key or a `UNIQUE` btree index (same
  columns, any order). JSON-path, spatial, full-text, and vector indexes are
  rejected. If `ON UNIQUE` is omitted, the engine uses the primary key when
  every PK column is in the insert list; otherwise the sole covered `UNIQUE`
  btree index. Ambiguity is `invalid_argument` (`UPSERT requires ON UNIQUE`).
- On conflict, an explicit `SET` runs like `UPDATE`. Unqualified names are the
  existing row; `excluded.col` is the proposed insert row (including defaults).
  With no `SET`, non-key insert columns are copied from the proposed row and
  the primary key plus unique-target columns stay.
- Unique keys encode NULL, so a unique index admits at most one NULL key.
  UPSERT matches that encoded key; it does not treat NULL as distinct.
- Concurrent UPSERTs on the same unique key take an exclusive lock on that
  key. A committed occupant is updated even when this statement's snapshot
  would not see it. WAL records are ordinary insert/update; followers apply
  those records and do not re-run UPSERT.
- Privileges: `INSERT` and `UPDATE`. `RETURNING` also requires `SELECT`.
  A bound tenant session injects `tenant_id` as for `INSERT` and cannot
  `SET tenant_id`.

`INSERT`, `UPDATE`, `DELETE`, and `UPSERT` accept `RETURNING` items or
`RETURNING *` after `LIMIT` (when present). The list sees the row after the
write: inserted/updated values, or the deleted row. Expressions, aliases, and
`excluded.col` on `UPSERT` are allowed. Windows and aggregates are rejected.
Results stream over NSQL as `RowDesc` plus `DataBatch` frames, then
`CommandComplete` with `Affected`. `EXPLAIN` shows `Upsert` for `UPSERT`.

`ANALYZE [table]` writes `NSST` statistics into the catalog tree (key `S` + name). They survive restart. `EXPLAIN` / `EXPLAIN ANALYZE` return one row per operator with estimates (and actuals / time / CPU / memory / disk / cache / spill / workers / index).

Statistics also refresh automatically inside a modifying transaction once a
table accumulates at least 1,000 changed rows. For an already analyzed table,
the threshold grows to 20% of its last row count when that is larger. Refresh
is synchronous, deterministic, and commits atomically with the data changes;
there is no independent analyzer goroutine. Automatic snapshots omit bulky
segment, histogram, and MCV payloads so statistics cannot exceed a catalog page
and abort DML; explicit ANALYZE retains those detailed distributions.

## CDC subscription

```sql
GRANT CDC ON TABLE orders TO streamer;
SUBSCRIBE TO orders;
SUBSCRIBE TO orders WHERE operation = 'DELETE';
SUBSCRIBE TO orders AFTER 1842;
ALTER TABLE orders SET CDC IMAGES FULL;
```

`SUBSCRIBE` is a continuous, table-scoped result stream sourced from committed
WAL changes. `AFTER` is an unsigned decimal commit-LSN resume token. The
statement is rejected inside an explicit transaction. Tenant scope is taken
only from `SET TENANT`; it cannot be supplied by the statement. See
[`docs/cdc.md`](cdc.md) for ordering, result columns, cancellation, retention,
and the opt-in bounded image policy.

Committed transactions that update or delete at least 1,000 rows automatically
request table-scoped dead-version cleanup through the maintenance coordinator.
Each pass is capped at 10,000 tombstones and inherits the configured CPU,
memory, and I/O budgets. Cleanup runs only after commit; a paused/busy manager
or live snapshot defers it without changing the durable transaction outcome.

Checkpoint WAL includes the allocator freelist metadata page images as well as
its head/count state. This keeps PITR self-contained when index rebuild/drop
reclamation creates freelist pages after the base backup.

JSON path extract (`SELECT metadata.category`) and `CREATE INDEX … ON t(metadata.category)` are implemented. See `docs/json.md`.

`CREATE FULLTEXT INDEX` and `SEARCH col FOR '…'` are implemented. See `docs/fulltext.md`.

`CREATE VECTOR INDEX … USING HNSW` and `NEAREST col TO …` are implemented. See `docs/vector.md`.

`WHERE` + `SEARCH` + `NEAREST` is one hybrid plan (`docs/optimizer.md`). `EXPLAIN` shows `Candidates` and `Rerank`.
