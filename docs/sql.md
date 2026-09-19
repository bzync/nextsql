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
workflow, trigger, schedule, durable-task, failover, PITR, driver, and clean
repository-wide functional gates are complete as recorded in `TODO.md`.
`CREATE SCHEDULE` accepts `EVERY '<duration>'`, `AT '<rfc3339>'`, or
`CRON '<minute hour day-of-month month day-of-week>'` (standard five-field,
UTC, numeric-only); see `docs/workflows.md` for the cron grammar.

## Statements

`CREATE TABLE` (including `FOREIGN KEY` / column `REFERENCES`), `CREATE TABLE name PRIMARY KEY (col, ...) AS <query>`, `DROP TABLE` [`IF EXISTS`], `ALTER TABLE` (`ADD`/`DROP` `[COLUMN]`, `ALTER [COLUMN] c SET`/`DROP NOT NULL`, `ALTER [COLUMN] c SET`/`DROP DEFAULT`, bounded `ADD`/`DROP`/`ATTACH`/`DETACH PARTITION`, `RENAME` `[COLUMN]`/`TO`, `ADD`/`DROP CONSTRAINT` — foreign key or `CHECK`), `CREATE INDEX` / `CREATE UNIQUE INDEX` (including JSON paths such as `metadata.category`, `INCLUDE`, `WHERE`, and expression keys), `CREATE SPATIAL INDEX`, `CREATE FULLTEXT INDEX` [`WITH (ANALYZER = 'simple' | 'english' | 'french' | 'german' | 'spanish')`] (one to eight `STRING`/`TEXT` columns), `CREATE VECTOR INDEX … USING HNSW | IVF | IVFPQ | SPARSE`, `DROP INDEX` [`IF EXISTS`], `REBUILD INDEX`, `CREATE [OR REPLACE] VIEW` [`(col, ...)`] `AS <query>`, `DROP VIEW` [`IF EXISTS`], `CREATE` / `ALTER` / `DROP WORKFLOW`, `RUN WORKFLOW`, `CREATE` / `ALTER` / `DROP TRIGGER`, `CREATE` / `ALTER` / `DROP SCHEDULE`, `CREATE` / `ALTER` / `DROP RESOURCE GROUP`, `SET RESOURCE GROUP name`, `RESET RESOURCE GROUP`, `SET CONFIG key = value`, `BACKUP DATABASE`, `VERIFY BACKUP 'name'`, `SHOW TASKS`, `SHOW DATABASES` / `TABLES` / `INDEXES` / `CONNECTIONS` / `QUERIES` / `TRANSACTIONS` / `LOCKS` / `CLUSTER` / `STORAGE`, `CANCEL TASK`, `CANCEL QUERY`, `MAINTAIN DATABASE` / `MAINTAIN TABLE` / `MAINTAIN INDEX`, `CLUSTER TRANSFER LEADER`, `CLUSTER DRAIN` [`WITH (TIMEOUT_MS = n)`], `CLUSTER MAINTENANCE ENABLE` / `DISABLE`, `CLUSTER RECONCILE CONFIRM`, `INSERT` (`VALUES` or a query) [`RETURNING`], `UPSERT` [`ON UNIQUE`] [`SET`] [`RETURNING`], `SELECT` (including `WITH` / `WITH RECURSIVE`, `DISTINCT`, `JOIN` / `GROUP BY` / `ORDER BY` / `LIMIT` / `OFFSET` / `COUNT` `SUM` `AVG` `MIN` `MAX`, window functions with `OVER`, JSON path extract, `SEARCH col [WEIGHT n] [, col [WEIGHT n] …] FOR '…'`, `FACET col [, col …]`, and `NEAREST col TO …`), `UPDATE` [`RETURNING`], `DELETE` [`RETURNING`], `BEGIN` [`READ COMMITTED` | `SNAPSHOT` | `SERIALIZABLE`], `COMMIT`, `ROLLBACK`, `SAVEPOINT name`, `ROLLBACK TO` [`SAVEPOINT`] `name`, `RELEASE` [`SAVEPOINT`] `name`, `ANALYZE` [`table`], `EXPLAIN` [`ANALYZE`] `<statement>`, `CREATE USER` / `DROP USER`, `CREATE ROLE` / `DROP ROLE`, `GRANT` / `REVOKE` (`docs/security.md`, including `GRANT USAGE ON RESOURCE GROUP name TO grantee`). Every other `SET`/`RESET` spelling — including `SET TENANT`, `RESET TENANT` — and `PARTITION BY TENANT` are rejected. Shared row tenancy was removed: a deployment serves exactly one database, and isolation is a whole deployment.

`SELECT <expr-list>` with no `FROM` at all (e.g. `SELECT 1`, `SELECT NOW()`, `SELECT 1 + 1 AS n`) evaluates the select list exactly once against no row/table context and bypasses the normal binder/planner entirely — the same architectural precedent as the `system.*` virtual-table bypass. `SELECT *` still requires `FROM` (there is no table to expand). An optional `WHERE`/`ORDER BY`/`LIMIT`/`OFFSET` still applies to that single synthetic row; `GROUP BY`/`HAVING`/`SEARCH`/`NEAREST`/`FACET`/`JOIN` need a table or index and are rejected at parse time. A bare column reference has nothing to resolve against and fails closed (`invalid_argument`), not silently.

Unquoted identifiers fold to lowercase. Quoted `"ident"` is preserved.

Table names that start with `nsql_` (case-folded) are reserved. The only exception is `CREATE TABLE nsql_schema_migrations` with the exact history DDL when that table is absent. Any other `nsql_*` name, or a different column list for `nsql_schema_migrations`, is `invalid_argument`. After that reserved DDL is accepted, the executor grants `SELECT`/`INSERT`/`UPDATE`/`DELETE` on the table to the session user (no `GRANT` SQL, no `PrivGrant`).

Reserved words include `FOREIGN`, `REFERENCES`, `CONSTRAINT`, `CASCADE`, `RESTRICT`, `ACTION`, `MATCH`, `ALTER`, `ADD`, `RENAME`, `ORDER`, `ASC`, `DESC`, `IF`, `EXISTS`, `WITH`, `OVER`, `UPSERT`, `LIKE`, and `RETURNING` (same rule as `USER`, `KEY`, `TO`): unquoted they are keywords; quoted `"foreign"` is an identifier. `RECURSIVE`, `MATERIALIZED`, `PARTITION`, `ROWS`, `RANGE`, `UNBOUNDED`, `PRECEDING`, `FOLLOWING`, `CURRENT`, `ROW`, `EXCLUDED`, `INCLUDE`, `CAST`, `ESCAPE`, and `FACET` are contextual identifiers, not reserved words — `CAST` and `ESCAPE` are recognised only in `CAST(x AS type)` and after a `LIKE` pattern, so a column named `cast` or `escape` keeps working unquoted.

## Altering a column

`ALTER TABLE t ALTER [COLUMN] c SET NOT NULL` / `DROP NOT NULL` and
`ALTER TABLE t ALTER [COLUMN] c SET DEFAULT <expr>` / `DROP DEFAULT` change a
column's nullability and default in place. One action per statement.

`SET NOT NULL` scans the table first and fails without changing anything if the
column holds NULL in any existing row — nothing re-checks stored rows
afterwards. `DROP NOT NULL` is refused on a primary-key column, which is NOT
NULL by definition.

A `DEFAULT` follows the same rules as one written at `CREATE TABLE`: a literal
coercible to the column type, or `UUID()` / `NOW()` / `AI()` for the types each
requires, and never on an `ENCRYPTED CLIENT` column. Changing a default is a
catalog-only change: it supplies a value for writes that omit the column and
does not rewrite rows already stored.

Changing a column's *type* is not supported; it stays a table rebuild.

## Views

`CREATE [OR REPLACE] VIEW name [(col, ...)] AS <query>` stores a named query;
`DROP VIEW [IF EXISTS] name` removes it. A view and a table share one relation
namespace, so a name in `FROM` means exactly one thing.

The defining query is stored as **text**, with every identifier written in
quoted form (`SELECT "id" FROM "emp"`, which is what `system.views` shows), and
is re-resolved at each use. Quoting changes no meaning, since an unquoted name
is the quoted form of its lower-case spelling, but it keeps the stored text
parsing when a word it uses as a name later becomes a reserved keyword.
`nextsqld` logs a warning at startup naming any stored view that no longer
parses (possible only for a view created before log #287);
`CREATE OR REPLACE VIEW` repairs one. The re-resolution at each use means a view
follows the tables under it as they change — new rows, added columns, new
indexes. Using a view expands it into a common table expression holding that
query, which is why a view joins, filters, aggregates and nests exactly like
any other relation, including inside a subquery of an `UPDATE` or `DELETE`. A
`WITH` clause naming the same identifier shadows the view, as a local
definition should. Views may be built over views, up to 8 levels; a definition
that reaches itself is refused as a cycle.

**Authorization is the invoker's, never the definer's.** The expansion reads
the underlying tables by name, so using a view requires exactly the privileges
that writing its query by hand would require. A view is a convenience, not a
way to reach data the caller could not otherwise read. `system.views` shows a
view's definition to an admin or to its owner.

Boundaries, each explicit rather than partial:

- **Read-only.** `INSERT` / `UPDATE` / `DELETE` / `UPSERT` against a view are
  refused. An updatable view needs a documented row-mapping rule back to base
  rows, and writing to the wrong place silently is worse than the refusal.
- A view's query must read at least one relation: `CREATE VIEW v AS SELECT 1`
  is refused, because a CTE body has nothing to read.
- Dropping a table or view that another view is defined over is refused, so a
  view is never left pointing at something that no longer exists.
- A view is validated when created — it must parse, resolve, and be one the
  creator may run — and again at each use.
- Views live in the catalog, so a physical backup carries them. Logical export
  covers table data and table DDL only; views, like workflows and triggers, are
  not part of it.

## Cancelling a running statement

`CANCEL QUERY '<id>'` stops a statement another session is running, named by
the `query_id` that `system.active_queries` and `SHOW QUERIES` report. A user
may always cancel their own; cancelling anyone else's requires `ADMIN`, the
same boundary that decides whether the statement is visible at all. Cancelling
an id that is no longer running is not an error — the statement asked for it to
stop, and it has.

The cancel funnels through the context the target statement already runs under,
so it unwinds through the executor's ordinary cleanup, exactly as a
client-driven cancel does. `CANCEL TASK '<id>'` is unchanged and still cancels
a background task rather than a statement.

## Savepoints

`SAVEPOINT name` marks a position inside an open write transaction.
`ROLLBACK TO [SAVEPOINT] name` reverses everything written after that mark and
leaves the transaction open, holding its locks; `RELEASE [SAVEPOINT] name`
drops the mark and keeps the work. Both destroy every savepoint established
after the named one, and re-using a live name replaces that savepoint, as in
the standard. A transaction holds at most 64.

Reversal replays the transaction's own undo records through the trees that
produced them, so secondary indexes, vector stores and the heap are all
reversed together and a unique key freed by a rollback is immediately
reusable. Nothing extra is logged: redo is page-image based and a commit
writes each dirty page's final image, which already reflects the reversal.
Rolling back to the same savepoint twice is well defined — applying an undo
record again re-deletes a row already gone or restores a version already
restored — and a later whole-transaction `ROLLBACK` still reverts everything.

Two boundaries are explicit rather than partial:

- `SAVEPOINT` requires an open **write** transaction; outside one, or in a
  read-only transaction, it is refused.
- `ROLLBACK TO` is refused when a schema change ran after the savepoint was
  set. DDL is not part of the row undo chain a savepoint reverses, so crossing
  one would leave the catalog and the data disagreeing.

Staged change-stream events are truncated with the rollback, so a row that was
rolled back is never published as a CDC change.

## Column defaults on insert

A column's `DEFAULT` fills a column the `INSERT` or `UPSERT` does not name. A
value the statement supplies is used as given: an explicit `NULL`, as a literal,
a bound parameter or a value from the source query, is stored as `NULL`, and a
`NOT NULL` column refuses it even if the column has a default. `AI()` in the
value list asks for the next generated value, and an explicit value still
advances the column's `AI()` counter.

Until log #287 an explicit `NULL` was replaced by the default. A driver binding
`NULL` got the default stored, and a logical export/import replaced every stored
`NULL` in a defaulted column with the default, including fresh `NOW()` and
`UUID()` values.

## Check constraints

A `CHECK` is written beside a column (`n INT64 CHECK (n > 0)`) or at table
level (`CONSTRAINT ab CHECK (a < b)`), and added or removed later with
`ALTER TABLE ADD [CONSTRAINT name] CHECK (...)` / `DROP CONSTRAINT name`.
Foreign keys and checks share one constraint namespace, so a name identifies
exactly one constraint. An unnamed constraint is named `ck_<table>_<n>` by its
position in the statement as written. A table holds at most 16.

A row is refused only when a check evaluates to FALSE. UNKNOWN satisfies the
constraint, which is the SQL rule and the reason `CHECK (n > 0)` admits a NULL
`n` — `NOT NULL` is the constraint that rejects it.

Checks are evaluated on the leader inside the writing transaction, before the
row reaches the heap, on every path that writes a row: `INSERT` (including the
multi-row bulk path), `UPDATE`, `UPSERT`, and foreign-key `CASCADE` /
`SET NULL` / `SET DEFAULT` writes to a child row. A violation aborts the
statement with nothing written. Followers apply the resulting WAL and never
re-evaluate the predicate.

A stored predicate must therefore be deterministic and depend on nothing but
the row. These are refused at DDL time: subqueries (nothing re-validates the
constraint when the other table changes), aggregates and window functions
(they depend on other rows), `UUID()` / `NOW()` / `AI()` (a row that passed
once could fail an identical later evaluation, and a replica or a recovery
replay could disagree with the leader), parameters (no value at DDL time), and
references to an `ENCRYPTED CLIENT` column (the server holds only ciphertext).

`ALTER TABLE ADD CONSTRAINT ... CHECK` validates every existing row first and
fails without adding the constraint if any row contradicts it, so a table can
never hold rows that no write could reproduce. Only the constraint being added
is re-scanned. Checks appear in `system.checks` and in the canonical DDL that
`system.table_ddl` and logical export emit.

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
    id          UUID PRIMARY KEY DEFAULT UUID(),
    customer_id UUID NOT NULL,
    CONSTRAINT fk_orders_customer
        FOREIGN KEY (customer_id)
        REFERENCES customers (id)
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
- Cyclic `CASCADE` graphs are rejected at DDL time. Self-referential FKs and cyclic `RESTRICT` graphs are allowed.
- A child `CREATE TABLE` in the same transaction can reference a parent created earlier in that transaction (session overlay). `CREATE TABLE` bind and parent `DELETE` inbound probes both use overlay ∪ catalog lookup, so an uncommitted child table is visible to the same-txn parent delete.
- `MATCH SIMPLE`: if any foreign-key column is NULL, the existence check and parent-delete probe for that constraint are skipped.

Do not revert this binary after a catalog rewrite has written `NSCT` v13, or
v14 for a table that declares a `CHECK` constraint.
Restore a pre-v5 backup or use an explicit format-aware migration first.

## Types

| Type | Storage | Notes |
|---|---|---|
| `UUID` | 16 bytes | `DEFAULT UUID()` |
| `BOOL` | 1 byte | `TRUE` / `FALSE`; the type every comparison, `IS NULL` test and `AND`/`OR`/`NOT` produces; index keys order `FALSE` before `TRUE` (usable as `PRIMARY KEY`/`ORDER BY`); isolated from every other family — no coercion to or from text or numbers, in either direction, including `CAST` (write `CAST(flag AS STRING)` to render one as `'TRUE'`/`'FALSE'`); ordinary FK-eligible scalar; `ENCRYPTED CLIENT` supported |
| `STRING` / `TEXT` | `u32` length + UTF-8 | same encoding |
| `CHAR(n)` / `VARCHAR(n)` | same encoding as `STRING`/`TEXT`; `n` in `Type.Precision` | `n` counts **runes**, not bytes; `CHAR(n)` is true fixed-width — shorter input is right-padded with spaces to exactly `n`, and over-length input whose excess is entirely trailing spaces is trimmed back to `n` (the one ISO-standard silent trim), any other over-length input errors; `VARCHAR(n)` is a length ceiling that never truncates; both order byte-lexicographically on the stored form; not `ENCRYPTED CLIENT`|
| `BLOB` | `u32` length + raw bytes | variable-length, no UTF-8 validation; literal syntax `X'<hex>'` (e.g. `X'DEADBEEF'`, `X''` for empty); orders byte-lexicographically (usable as `PRIMARY KEY`/`ORDER BY`); isolated from `STRING`/`TEXT` — coercion either way requires hex text, never a byte-for-byte reinterpretation; `ENCRYPTED CLIENT` supported|
| `INT8` / `INT16` / `INT32` / `INT64` | 1 / 2 / 4 / 8 bytes, two's complement | exact fixed-width signed integers; index keys sign-bit-flip so `ORDER BY`/`PRIMARY KEY` sort numerically; narrowing (including a literal that doesn't fit) errors rather than wrapping; `+ - * /` and unary `-` always promote to `DECIMAL` (arbitrary precision, cannot overflow mid-operation) — only an explicit assignment back into a fixed-width column re-checks range; ordinary FK-eligible scalars; `ENCRYPTED CLIENT` supported|
| `UINT8` / `UINT16` / `UINT32` / `UINT64` | 1 / 2 / 4 / 8 bytes, plain unsigned | exact fixed-width unsigned integers; index keys use plain unsigned big-endian order (no sign-bit flip needed); narrowing and negative-to-unsigned assignment error rather than wrapping; `+ - * /` and unary `-` always promote to `DECIMAL`, same as `INT8..64`; also directly coercible to/from `INT8..64` (range/sign checked either way — the two families share one coercible "exact integer" group); ordinary FK-eligible scalars; `ENCRYPTED CLIENT` supported|
| `DECIMAL(p,s)` | `1 <= p <= 38`, `s <= p` | unscaled integer + scale; `DEFAULT AI()` when `s = 0` |
| `FLOAT32` / `FLOAT64` | 4 / 8 bytes IEEE-754 | inexact by design, for interop with external numeric data; index keys use the canonical total order `-Inf < negative < 0 < positive < +Inf < NaN`, with `-0.0` canonicalized to `+0.0` and every NaN payload collapsed to one value; arithmetic stays floating (it does not promote to `DECIMAL`); assignment into `FLOAT32` re-rounds|
| `DATE` | `int32` day count since 1970-01-01 | text form `YYYY-MM-DD`; no dedicated literal prefix — a quoted string coerces; isolated from every family but text; index keys sign-bit-flip so pre-1970 dates sort first; no arithmetic without `INTERVAL`|
| `TIME` | `int64` nanos since midnight, `< 24h` | text form `HH:MM:SS[.fraction]`; a quoted string coerces; isolated from every family but text; plain unsigned key order (always non-negative)|
| `TIMESTAMP` | `int64` nanos | a plain date-and-time with **no** time zone, read literally with no offset applied; deliberately isolated from `TIMESTAMPTZ` — converting between them needs an assumed zone, so it is text coercion only|
| `TIMESTAMPTZ` | `int64` UTC nanos | `DEFAULT NOW()` |
| `INTERVAL` | `int32` months + `int32` days + `int64` nanos | a calendar duration, literal `INTERVAL '1 month 3 days'`; month and day steps are applied calendar-correctly (Jan 31 + 1 month = Feb 28/29) before the nanosecond remainder; ordering uses the justified heuristic (1 month = 30 days = 24h), so `1 month` and `30 days` compare equal though their fields differ|
| `ENUM('a', 'b', …)` | `u16` ordinal into the column's declared label list | ordered by **declaration position**, not alphabetically (`small < medium` however the labels read); the label list is per-column catalog metadata, so two `ENUM` columns with different labels are different types; isolated from every family but text|
| `JSON` | compact binary `NSJB` | path extract and path indexes; see `docs/json.md` |
| `VECTOR<F32,N>` / `<F16,N>` / `<I8,N>` | heap reference; payload in vector store (F16 halves, I8 signed bytes + scale) | `NEAREST`, `COSINE` / `L2` / `INNER_PRODUCT`; see `docs/vector.md` |
| `BITVECTOR<N>` | heap reference; payload is `ceil(N/8)` packed bits | `NEAREST … USING HAMMING` (default and only metric); elements must be 0 or 1; see `docs/vector.md` |
| `SPARSEVECTOR<N>` | heap reference; payload is `NSSV` (non-zero index/value pairs) | `NEAREST` default `COSINE` (also `INNER_PRODUCT`); `CREATE VECTOR INDEX … USING SPARSE`; dense literals drop zeros; see `docs/vector.md` |
| `POINT` / `LOCATION` | lon, lat `float64` | WGS84; see `docs/geo.md` |
| `BOX` | west, south, east, north | axis-aligned lon/lat box |
| `LINESTRING` | `u16` count + lon/lat pairs | at least two vertices; see `docs/geo.md` |
| `POLYGON` | rings of closed lon/lat | exterior + optional holes; 256-vertex cap |
| `GEOMETRY` / `GEOMETRY(sub, srid)` | EWKB (`u32` length prefix + extended WKB) | general OGC geometry (`Point`/`LineString`/`Polygon`/`Multi*`/`GeometryCollection`), planar/Cartesian math, per-column SRID+subtype; alongside (not a generalization of) `POINT`/`BOX`/`LINESTRING`/`POLYGON`|
| `GEOGRAPHY` / `GEOGRAPHY(sub, srid)` | same EWKB encoding | same general OGC geometry, geodetic/great-circle math (SRID defaults to 4326); a column that accepts any subtype but carries an SRID spells the subtype `Geometry` — a plain `GEOGRAPHY` column is reported as `GEOGRAPHY(Geometry, 4326)`, and that spelling parses back to the same type|
| `STRUCT<name T, …>` | self-describing nested `NSRW` sub-encoding (`u32` body length + `u32` field count + null bitmap + members) | fixed, named, heterogeneous fields; construct with `STRUCT(expr AS name, …)`; read a field with `col.field[.field…]`; orders field-by-field lexicographically (usable as `PRIMARY KEY`/`ORDER BY`); NULL fields sort first; not `ENCRYPTED CLIENT`, not FK-eligible|
| `ARRAY<T>` | same nested sub-encoding; `T` is any type incl. another collection | variable-length homogeneous list; construct with `ARRAY(e1, e2, …)`; `ELEMENT_AT(arr, i)` (1-based), `CARDINALITY`/`ARRAY_LENGTH`, `ARRAY_CONTAINS(arr, x)`; orders element-by-element (a shorter prefix sorts first); nesting bounded at depth 8, `2²⁰` elements; not `ENCRYPTED CLIENT`, not FK-eligible|
| `MAP<K,V>` | same nested sub-encoding; entries stored in canonical key order | `K` must be an orderable scalar; construct with `MAP(k1, v1, k2, v2, …)`; `ELEMENT_AT(map, key)`, `MAP_CONTAINS_KEY`, `MAP_KEYS`, `MAP_VALUES`, `MAP_SIZE`; duplicate keys rejected; two MAPs with the same entries encode and compare identically regardless of insertion order; not `ENCRYPTED CLIENT`, not FK-eligible|

`ENCRYPTED CLIENT` defaults to randomized `NSCE1` storage. The explicit
`ENCRYPTED CLIENT DETERMINISTIC` form stores `NSCE2` and permits only
ciphertext-parameter `=` / `<>` / `!=`, NULL tests, and a direct ordinary
B-tree/`UNIQUE` index. It leaks equality and frequency within one key and
database/table/column context; ranges, joins, expressions, ordering/grouping,
and general search fail closed. See `docs/client-encryption.md`.

A table must declare a `PRIMARY KEY`. That key is the clustered B+Tree key.

## DROP / ALTER

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
and indexes; for a partitioned table this includes every partition-local heap,
vector store, and index root. Index scope touches only the resolved physical
index, or every local root of a partitioned logical index, and database scope
also covers the catalog and all tables. Missing catalog-owned physical roots
fail closed as corruption. Index names are resolved across the database and
ambiguous names are rejected. The result's
affected count is the number of physical tombstones removed. With ACLs enabled,
cluster `ADMIN` is required because maintenance crosses tenant boundaries.

`ALTER TABLE ADD COLUMN` appends a column and rewrites existing rows (NULL or the column default). A `NOT NULL` column on a non-empty table requires `DEFAULT`. `DROP COLUMN` cannot remove a primary-key column or a column used by a foreign key; secondary indexes that include the column are dropped from the catalog. `RENAME` is a catalog update (table rename also rewrites inbound `REFERENCES` names). `ADD CONSTRAINT` validates existing rows, then stores the foreign key.

`CREATE DATABASE` was **removed** with multi-realm/multi-database hosting: a NextSQL deployment serves exactly one database. The parser rejects it (`syntax`) with that reason rather than a bare parse error, `IF NOT EXISTS` included. To run another database, initialize another deployment (`nextsql init` into its own data directory, with its own root key and `nextsqld` process) — which is also what gives it full isolation.

`CLUSTER TRANSFER LEADER` asks a Raft-clustered deployment's current leader
to hand off to another voter (`replication.Cluster.TransferLeadership`), for
a planned handoff ahead of a restart or maintenance window rather than
waiting for a crash to trigger failover. It cannot run inside a transaction,
requires cluster `ADMIN`, and fails `Unavailable` on a single-node
deployment. See `docs/ops.md` "Leader transfer" and the `nextsql cluster
transfer-leader` CLI wrapper.

`CLUSTER DRAIN [WITH (TIMEOUT_MS = n)]` asks the node this connection
reached to begin gracefully draining itself — stop accepting new
connections, close idle ones immediately, wait up to `TIMEOUT_MS`
milliseconds (0, the default, uses that node's configured
`shutdown_drain_ms`) for busy ones, then force-close whatever remains — the
same mechanism `nextsqld` already runs on SIGINT/SIGTERM, now reachable
without a restart or signal. It cannot run inside a transaction and requires
cluster `ADMIN`, but unlike `CLUSTER TRANSFER LEADER` it needs no Raft
cluster and is not gated on the target being the current leader — draining
is purely local to whichever node the connection reaches, so it works the
same on a single-node deployment and on any voter in a cluster. The drain
itself runs in the background; the statement returns a `drain_initiated`
acknowledgment immediately rather than blocking for the full timeout. See
`docs/ops.md` "Remote drain" and the `nextsql cluster drain` CLI wrapper.

`CLUSTER MAINTENANCE ENABLE|DISABLE` puts the node this connection reached
into (or out of) maintenance mode: while enabled, every mutating statement —
`INSERT`/`UPSERT`/`UPDATE`/`DELETE`, every DDL statement, and `BEGIN` (the
same classification `CLUSTER TRANSFER LEADER`'s leader-routing gate already
uses, since a transaction's eventual read/write shape is unknown at `BEGIN`
time) — fails `Unavailable` until disabled; reads (autocommit `SELECT`,
`SHOW`, `system.*`) keep working, so operators can still inspect state.
Toggling itself cannot run inside a transaction and requires cluster
`ADMIN`. Like `CLUSTER DRAIN` and unlike `CLUSTER TRANSFER LEADER`, it is
purely local to the node the connection reached — not Raft-replicated — so
it works the same on a single-node deployment, and a leader failover during
maintenance does not carry the flag to the new leader (re-issue the
statement against whichever node is leader afterward). The current state of
this node is visible in `SHOW CLUSTER` / `system.replication.
maintenance_mode`. Not to be confused with the unrelated `MAINTAIN`
statement or `DB.PauseMaintenance`/`ResumeMaintenance`, which pause the
background dead-version cleanup scheduler, not client query traffic. See
`docs/ops.md` "Maintenance mode" and the `nextsql cluster maintenance
enable|disable` CLI wrapper.

`CLUSTER RECONCILE CONFIRM` clears the node this connection reached's
local "replication-suspect" flag, which is set automatically (never by a
client) when this node's own local commit could not be replicated to Raft
quorum — see `docs/ops.md` "Correctness note" under Rolling upgrade for the
full mechanism. While set, `StrongReadBarrier` refuses STRONG reads from
this node regardless of leadership, since its local history may contain a
row no other cluster member has. The `CONFIRM` keyword is mandatory (a bare
`CLUSTER RECONCILE` is a syntax error) so the statement can't be
fat-fingered; an operator should run it only after verifying or repairing
this node's data. Requires cluster `ADMIN`, cannot run inside a transaction,
and — like `CLUSTER MAINTENANCE`/`CLUSTER DRAIN` — is purely node-local, not
Raft-replicated. Current state is visible in
`system.replica_health.replication_suspect`. See the `nextsql cluster
reconcile confirm` CLI wrapper.

`BACKUP DATABASE` writes a verified, encrypted backup of the node this
connection reached into its configured backup directory (config key
`backup_dir`), in a fresh timestamped subdirectory. The client never names a
filesystem path. It uses the server's already-open engine — the engine is
checkpointed, then the file set is copied while the server keeps serving, and
restore reconciles the fuzzy copy by replaying WAL from the checkpoint (the
standard hot-backup model); the copy is never a second engine open, so there
is no risk of one recovery pass truncating a WAL tail the other is writing.
The backup is published only after a restore test passes. `VERIFY BACKUP
'name'` re-runs hash verification plus a restore test on one existing backup
(by subdirectory name); it touches only the backup's own files and a
temporary restore directory, never the live database. Both require the
`BACKUP` privilege (`GRANT BACKUP ON DATABASE …`) or cluster `ADMIN`;
`BACKUP DATABASE` additionally cannot run inside a transaction. Both are
node-local. If `backup_dir` is unset, both fail `Unavailable`. The backups
are visible in `system.backups`. **Restore and point-in-time recovery are
offline-only** — a running server cannot restore into itself — and stay with
the `nextsql restore` CLI (`docs/backup.md`); NextSQL Admin's Operations mode
surfaces the exact command rather than a button. Backs Operations mode's
Backups view.

`SET CONFIG key = value` persists one server setting to the node this
connection reached's on-disk `nextsql.conf`. `value` is a string literal, a
number, `TRUE`/`FALSE`, or `DEFAULT` (remove the key so it falls back to its
built-in default). The server owns the file — the client never names or
touches a path. Requires cluster `ADMIN`, cannot run inside a transaction,
and — like `CLUSTER MAINTENANCE`/`CLUSTER DRAIN` — is purely node-local
(each node has its own `nextsql.conf`), not Raft-replicated. **The write is
persist-only: nothing is hot-reloaded, so a change takes effect only on the
next `nextsqld` restart.** `system.config`'s `file_value` / `restart_required`
columns show the pending difference between the running process and the file
(also surfacing a startup flag that overrode the file). The server must have
been started from a config file (`nextsqld --config`) or `SET CONFIG` fails
`Unavailable` — there is nothing to persist to. The file is rewritten in
canonical `key=value` form: any comments are not preserved, and settings the
built-in `Default()` populates (e.g. `max_inflight_queries`) are written
explicitly even if you did not set them. `key` must be one of the settings
`nextsql.conf` accepts (the same list `config.SettableKeys()` /
`nextsql setup --config-out` use); anything else is rejected. Backs NextSQL
Admin's Operations-mode Configuration editor.

`CREATE RESOURCE GROUP name [IF NOT EXISTS] [WITH (MAX_CONCURRENCY = n, MEMORY
= bytes, WORKERS = n, PRIORITY = n)]` declares a durable, RBAC-gated (cluster
`ADMIN`, like `CREATE ROLE`/`CREATE USER`) workload-governance descriptor: a
named record of options, catalog-persisted and WAL-recovered like any other
table, visible via `system.resource_groups` (admin-only). Every option is
optional and independently zero-defaulted ("unbounded"/"unset", the same
convention as `max_connections_per_user` and hosting storage caps); `MEMORY`
is bytes. `ALTER RESOURCE GROUP name WITH (...)` replaces only the options
given, leaving the rest at their current stored value; at least one option is
required. `DROP RESOURCE GROUP name [IF EXISTS]` removes it.

A session joins a resource group with `SET RESOURCE GROUP name` — the one
surviving spelling of `SET` after `SET TENANT` was removed, everything else
under `SET`/`RESET` still fails with the same removal message — and leaves it
with `RESET RESOURCE GROUP`, both of which work inside or outside an open
transaction. Switching into a group requires `USAGE` on it
(`GRANT USAGE ON RESOURCE GROUP name TO grantee` / `REVOKE USAGE ON RESOURCE
GROUP name FROM grantee`, cluster `ADMIN` bypasses via the same superuser rule
as every other privilege check); a name that doesn't exist fails `NotFound`
regardless of privilege. `RESET RESOURCE GROUP` needs no privilege beyond
`CONNECT`. Once assigned, the group composes with — never replaces — the
process-wide safety limits: a non-zero `MAX_CONCURRENCY` adds a second,
strictly additional admission gate on top of the existing process-wide
`scheduler.Admission` (a query must clear both; an unassigned session, or one
in an unbounded group, is unaffected), and non-zero `WORKERS`/`MEMORY`
override the session's per-query `scheduler.Limits` for as long as the
assignment lasts (`WORKERS` is still clamped to the process ceiling by
`Limits.normalized()`, so a group can never request more workers than the
process allows). `PRIORITY` (0 = normal, higher = more favored, up to 9) is
enforced on the process-wide `scheduler.Admission` gate only — the one gate
shared across every group and unassigned session, so it's the only place
cross-group ordering is meaningful: when that gate's slots are contended, a
higher-priority session's queued query is admitted ahead of an
earlier-queued lower-priority one (FIFO among equal priorities). This is
ordering only, never preemption — an already-admitted lower-priority query
is never interrupted — and it never bypasses `MAX_CONCURRENCY`/`MEMORY`/
`WORKERS` or lets a caller skip the queue-wait bound; a query still times
out on the same `QueueWait` it always would, just with a higher chance of
doing so under sustained higher-priority contention (an accepted tradeoff,
not a fairness/starvation-prevention mechanism). The per-group gate
(`MAX_CONCURRENCY`) is unaffected — it only ever holds members of one group
sharing one priority, so there is nothing to reorder there. There is still
no visibility into which resource group a live session is assigned to
(`system.sessions` has no `resource_group` column). See the Phase 27
"Resource groups" checklist in `TODO.md`. `system.capabilities` row
`resource_groups` is `supported`.

## Catalog

The superblock primary tree holds catalog rows. Key `T` + table name. Value is a versioned `NSCT` descriptor (columns, PK, index list, heap meta page, foreign keys, CDC image policy, and bounded P21 physical-partition metadata). `EncodeTable` writes version 13. `DecodeTable` accepts v1 (empty FK list), v2 (key-only CDC), v3 (CDC image policy), v4 (partition metadata), v5 (non-reusing partition identity allocator), v6 (per-index HNSW traversal-quantisation tag), v7 (per-index vector-ANN method + IVF `LISTS` / `PROBES`), v8 (per-index IVF-PQ `SUBSPACES`), v9 (per-index full-text analyzer id + revision), v10 (per-column `ENCRYPTED CLIENT` logical-type metadata), v11 (per-column `ENUM` label list), v12 (per-column recursive `STRUCT`/`ARRAY`/`MAP` descriptor), and v13 (per-column client-encryption mode); any other version fails closed. Key `S` + table name holds a versioned `NSST` statistics snapshot from `ANALYZE`; key `J` + stable table ID + stable partition ID holds its bounded `NSPS` local sketch. Key `A` + table ID + column name holds the next `AI()` value for that column (same transaction as the insert). Each user table and secondary index is a detached B+Tree whose root/height live on a slotted meta page (`NSTM`) so splits do not rewrite the catalog row. Any catalog rewrite upgrades an older descriptor to v13. `PARTITION BY RANGE`, `PARTITION BY HASH`, and `PARTITION BY LIST` are available with one-to-eight-column keys: RANGE uses lexicographically ordered tuple bounds (`VALUES LESS THAN (a, b, ...)`), LIST uses tuple membership (`VALUES IN ((a, b), ...)`), and HASH routes on the SHA-256 digest of the canonical typed tuple. Legacy TENANT descriptors remain decodable but cannot be created or extended through SQL. Plain/covering/partial/expression/JSON-path/spatial/FULLTEXT/HNSW/IVF/IVFPQ/SPARSE indexes use partition-local roots. Every secondary `UNIQUE` index (including partial, expression, and JSON-path forms) is enforced across every partition by an exclusive key lock plus a probe of every other partition-local root on write, and an ordered cross-partition scan on CREATE/REBUILD/ATTACH; UNIQUE on legacy TENANT tables remains rejected. Foreign keys may reference or be declared by partitioned tables; parent lookup and cascades traverse the affected local heaps under normal FK bounds. `UPSERT` on a RANGE/HASH/LIST table resolves its conflict against the partition-local heap (PK target) or every partition-local root (secondary `UNIQUE` target) and stays rejected only on legacy TENANT tables. Bounded ADD/DROP plus validated ownership-transfer ATTACH/DETACH lifecycle DDL is described in `docs/partitioning.md`.

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

Access paths: sequential heap scan (optionally parallel), clustered PK lookup/range, secondary index lookup/range (heap fetch by primary key). Residual predicates stay as filters. Joins are `INNER JOIN` (bare `JOIN` is the same), `LEFT` / `RIGHT` / `FULL` `[OUTER] JOIN`, or `CROSS JOIN`, up to eight tables (`FROM` + seven joins). Inner joins are cost-based left-deep (hash-build the smaller side; equal costs keep written order). Outer joins are not reordered and require `ON`; `CROSS JOIN … ON` is a syntax error. `SEARCH` / `NEAREST` (including hybrid) may run on the `FROM` table of an inner or `LEFT JOIN` query: the engine ranks that table first, then joins, retaining unmatched ranked rows with typed NULLs on the right. A search/nearest column on a joined table, or `SEARCH`/`NEAREST` with a `RIGHT`/`FULL` join, is rejected because those joins introduce unmatched rows with no rank source. Hash is the default; merge is used when both sides are already index-ordered on the equality keys (INNER or LEFT). `FULL` is hash-only and memory-capped (v1 does not spill; exceeding the budget is `exhausted`). `RIGHT` is rewritten to `LEFT` with swapped inputs and a column-order `Project`. A `NULL` join key never matches (`NULL = NULL` is unknown). Unmatched outer-join rows are emitted with typed NULLs. Columns from a null-extended side are nullable in the bound schema. Aggregates are hash-based. `ORDER BY` sorts the projected result (NULL values sort last in `ASC` and first in `DESC`) and may list output aliases, ordinals, or source columns; a sort sits above `Project`/`Aggregate` and below `LIMIT` / `OFFSET`. `SEARCH` / `NEAREST` rank order is replaced when `ORDER BY` is present.

`SELECT DISTINCT` uses a memory-budgeted hash operator after projection or
aggregation and before `ORDER BY`, `LIMIT`, and `OFFSET`. NULLs compare equal
for duplicate elimination. With `DISTINCT`, every `ORDER BY` expression must
also appear in the select output. When the ordering keys cover every output
column, the planner uses `OrderedDistinct` and removes adjacent duplicates from
the sorted stream without building a hash table. For a single-table projection
containing a complete primary key or complete `NOT NULL` UNIQUE-index key,
`IndexDistinct` proves the rows are already unique and elides duplicate work.

An aggregate with **no `GROUP BY`** is defined over one implicit group covering
the whole input, so it returns exactly one row even when nothing matched:
`COUNT` is `0` and `SUM` / `AVG` / `MIN` / `MAX` are `NULL`. This holds however
the input became empty — an empty table, a filter that matches no row, or a
constant-false filter folded away by the planner. With a `GROUP BY` there is no
group to report, so empty input returns zero rows.

An aggregate **may appear inside a larger select expression**. Each aggregate is
computed once and the surrounding expression is then evaluated against the
aggregated row, so `SELECT COUNT(*) + 1`, `SELECT SUM(b) / 2`,
`SELECT MAX(a) + MIN(a)`, `SELECT -COUNT(*)` and
`SELECT CASE WHEN COUNT(*) > 2 THEN 'many' ELSE 'few' END` all work, with or
without `GROUP BY`.

An aggregate's **argument** may be a computed expression — `SUM(a + b)`,
`COUNT(a + b)`, `MIN(a * 2)`. The expression is materialised into a column
before aggregating, so `COUNT(a + b)` counts the non-NULL values of `a + b`
rather than the rows. Identical expressions are computed once and shared.

Window functions are a separate path and compose as before: `SUM(b) OVER () * 2`
and `ROW_NUMBER() OVER (...) + 1` are fine.

`GROUP BY` takes a column reference or a computed expression — `GROUP BY a`,
`GROUP BY a + b`, `GROUP BY UPPER(name)`. A computed grouping expression is
materialised into a column before aggregating, exactly like a computed
aggregate argument, and the same expression written in the select list refers to
that group.

A select item that is not an aggregate must read only grouping columns, so
`SELECT a + 1, COUNT(*) … GROUP BY a` is accepted (one value per group) while
`SELECT b … GROUP BY a` is not.

**Positional** grouping (`GROUP BY 1`, meaning the first select item) is *not*
implemented and is rejected rather than taken literally as a constant, which
would quietly produce a single group under the same spelling.

`HAVING` runs after aggregation and before DISTINCT, ordering, and limits. It
may reference grouped expressions that appear in the output, selected aggregate
expressions, or their output aliases. Aggregate aliases are visible in `HAVING`
and `ORDER BY`; an aggregate used only in `HAVING` must first be selected in
this version. A selected aggregate may be written in `HAVING` exactly as it
appears in the select list — including the star form, `HAVING COUNT(*) > 1` —
or by its output name or alias, and a qualified reference matches an unqualified
one.

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

`HIGHLIGHT(value)` and `SNIPPET(value)` require a `SEARCH` clause on the same
`SELECT`. They wrap original document tokens whose analyzed form participates
in the SEARCH query. Optional `HIGHLIGHT(value, pre, post)` and
`SNIPPET(value, width [, pre, post])` override the default `<mark>` markers;
snippet width is 16–4096 Unicode code points (default 160). Both fail closed
in `WHERE` / `JOIN` / `GROUP BY` / `HAVING` / DML.

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

`expr IN (v1, v2, ...)` and `expr NOT IN (v1, v2, ...)` take a value list of
up to 4,096 expressions. The predicate is exactly the disjunction ISO/IEC 9075
defines it to be — `x IN (a, b)` is `x = a OR x = b` — so three-valued logic
follows from the expansion rather than from a separate rule: an unmatched `IN`
over a list containing NULL is UNKNOWN, and so is its `NOT IN`. Because each
value is compared against its own evaluation of the left operand, a left
operand calling `UUID()`, `NOW()` or `AI()` is rejected rather than given an
undefined meaning. A value list is not an index access path: it is evaluated
per row like any other predicate, exactly as the equivalent `OR` chain is.

`expr [NOT] LIKE pattern [ESCAPE c]` matches Unicode code points: `_` matches
exactly one character, `%` matches any sequence including the empty one, and
every other character matches itself. Matching is case-sensitive — NextSQL has
no collation that folds case behind the operator, so write `LOWER(col) LIKE
'a%'` when that is what you mean. `ESCAPE` declares a single character that
must be followed by `%`, `_`, or itself; any other escape sequence, a dangling
escape, or a multi-character escape is rejected. A NULL value, pattern or
escape yields UNKNOWN, not false. The matcher resumes from the last `%` on a
mismatch, so a pattern cannot cost exponential time the way a backtracking
regular-expression engine can. `LIKE` is not an index access path either;
`STARTS_WITH` is the prefix predicate the optimizer understands.

`CAST(expr AS type)` converts a value using the same conversion rules the
engine applies everywhere else (`types.Coerce`), so a cast can never reach a
conversion an `INSERT` would not accept. The target type is the written type
including `DECIMAL` precision and scale, `CHAR`/`VARCHAR` length, `ENUM` label
set and vector element type. A NULL input casts to a NULL of the target type;
a conversion the rules reject (`CAST('abc' AS INT64)`) is an error, never a
silent zero.

`NOT` binds more loosely than comparison, `IS [NOT] NULL`, `BETWEEN`, `IN` and
`LIKE`, as in the standard: `NOT a = b` is `NOT (a = b)` and `NOT a IS NULL` is
`NOT (a IS NULL)`. A `NOT` written inside an operand (`a = NOT b`) is still
accepted.

Statements nest at most 4,096 levels deep, counting parentheses, operator
chains, subqueries, set operations and workflow bodies; a deeper statement is
refused with `invalid_argument` before it is bound. Every stage after the
parser walks the tree recursively, so the bound is what keeps a single
statement from exhausting the process stack.

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

## INSERT from a query

`INSERT` takes its rows either from a `VALUES` list or from a query:

```sql
INSERT INTO archive (id, total) SELECT id, total FROM orders WHERE closed;
INSERT INTO daily WITH r AS (SELECT day, amount FROM sales) SELECT day, SUM(amount) FROM r GROUP BY day;
INSERT INTO copy SELECT * FROM orders RETURNING id;
```

- The source is an ordinary query — `SELECT`, a set operation, or a `WITH` —
  bound by the ordinary query binder and optimized like any other. Joins,
  aggregates, subqueries, views and CTEs behave exactly as they would on their
  own, and there is no second grammar for the query inside an `INSERT`.
- The query's output columns must match the columns being written, position by
  position: the named column list, or every column in declaration order when
  none is given. A count mismatch is `invalid_argument`.
- A target column the query does not supply takes its `DEFAULT`. Every
  constraint that applies to a `VALUES` insert applies here: `NOT NULL`,
  `CHECK`, primary key and unique keys, and foreign keys.
- **The source is read to completion before the first row is written.** A
  transaction sees its own writes and there is no statement-level command id,
  so a streaming implementation of `INSERT INTO t SELECT ... FROM t` would read
  back the rows it had just written and feed itself without bound. Reading
  first is what makes the source a fixed relation, whether the self-reference
  is direct or reached through a CTE, a view or a join — so
  `INSERT INTO t SELECT id + 100, v FROM t` doubles `t` exactly once.
- Because the source runs as a query, it is bounded like one:
  `max_result_rows` and `max_result_bytes` apply and the rows are charged to
  the statement's memory budget, so an oversized source is an explicit
  `exhausted` error, never a silently truncated insert (`docs/limits.md`).
- Triggers on the target fire and its foreign keys are checked, because a
  query-sourced row is written by the same path a `VALUES` row is.
- `EXPLAIN` shows the source plan under the `Insert` node (`Insert → Project →
  Filter → SeqScan`); a `VALUES` insert stays a single node.
- Privileges: `INSERT` on the target, plus `SELECT` on every relation the query
  reads — the source is read with the invoker's own rights, so an `INSERT`
  cannot copy out of a table the user may not read. `RETURNING` also requires
  `SELECT` on the target.
- `INSERT INTO t SELECT <exprs>` with no `FROM` is one row of values written a
  different way, and is treated as exactly that (which is also what captures
  `NOW()`/`UUID()` once for replication). A FROM-less source that filters,
  orders, bounds or deduplicates that single row is rejected — write `VALUES`.
- Not accepted: a query source writing an `ENCRYPTED CLIENT` column (that
  column takes only an encrypted parameter, `NULL`, or a direct ciphertext
  copy, none of which a query's output can be shown to be at bind time), a
  query source on a legacy `TENANT`-partitioned table, and a query-sourced
  `INSERT` in a workflow body (a workflow persists table/columns/rows only).
  `UPSERT` takes `VALUES` only.

## CREATE TABLE from a query

`CREATE TABLE` also builds a table from a query, taking the column names and
types from that query's output instead of a written column list:

```sql
CREATE TABLE archive PRIMARY KEY (id) AS SELECT id, name, qty FROM orders WHERE qty > 10;
CREATE TABLE totals PRIMARY KEY (day) AS SELECT day, SUM(amount) AS amount FROM sales GROUP BY day;
CREATE TABLE joined PRIMARY KEY (id) AS SELECT o.id AS id, c.name AS name FROM orders o JOIN customers c ON o.cid = c.id;
```

- **The key is named, not inferred.** Every NextSQL table is clustered on its
  primary key and a query's output carries none, so `PRIMARY KEY (col, ...)` is
  required and each column named must be one of the query's output columns.
  There is no rowid to fall back on.
- The source is an ordinary query — `SELECT`, a set operation, or a `WITH` —
  bound by the ordinary query binder and optimized like any other, exactly as
  for `INSERT` from a query. `EXPLAIN` shows the source plan under the
  `CreateTableAs` node.
- **It is a snapshot, not a view.** The rows are read once and stored, so later
  changes to the source do not reach it. Use `CREATE VIEW` for a relation that
  re-resolves at each use (see *Views*).
- **Every output column must be usable as a column.** An unaliased expression
  reports its name as `?`, which no column may be called, so
  `SELECT id, qty + 1 FROM t` is rejected and `SELECT id, qty + 1 AS twice FROM t`
  is what to write. A repeated output name is rejected for the same reason.
- **Column types come from the values the query produced**, because a NextSQL
  query has no static output type — a result column's type travels with its
  values. Two consequences worth knowing:
  - Integer arithmetic and `COUNT`/`SUM` produce an exact decimal whose
    precision and scale live in the value rather than the type, so a column
    derived from `qty * 2` is `DECIMAL(38, s)` where `s` is the scale the
    values actually needed — nothing is rounded away. A plain copy of a
    declared `DECIMAL(12,2)` column keeps `DECIMAL(12,2)`.
  - A derived type that no `CREATE TABLE` could declare is rejected rather
    than stored, so the new table is always one an operator could have written
    and whose canonical DDL (`system.table_ddl`, logical export) re-parses.
    The check is the round trip itself — render the candidate table, parse it
    back, require every column type to match — so a type added later cannot
    quietly reintroduce the problem. A comparison such as `qty > 15` yields
    `BOOL`, which is an ordinary column type, so that column is stored as
    `BOOL`; a derived type the dialect cannot spell is refused by name and
    `CAST` is the remedy.
- **The query must return at least one row**, since an empty result carries no
  values to derive types from. A `NULL` does carry its type (a `NULL` read from
  a `STRING` column is a `STRING` `NULL`, and `CAST(x AS t)` produces a typed
  `NULL`), so a column that is `NULL` in every row is still typed. Only the
  bare `NULL` literal is untyped and rejected — `CAST` it.
- Nullability is not inferred from the data: only the primary-key columns are
  `NOT NULL`. A result that happened to contain no `NULL` does not decide what
  the new table accepts afterwards. A `NULL` in a column named as the key is
  rejected.
- The statement is atomic. If the rows cannot be stored — a repeated value in
  the key column, say — the table is not left behind, and a `ROLLBACK` takes
  it with it.
- Like the source of an `INSERT`, the query is bounded as a query:
  `max_result_rows`, `max_result_bytes` and the statement memory budget apply,
  so an oversized source is an explicit `exhausted` error (`docs/limits.md`).
- Privileges: `CREATE`, plus `SELECT` on every relation the query reads — the
  source is read with the invoker's own rights, so it cannot copy out of a
  table the user may not read.
- The new table has no indexes, foreign keys or checks; add them with
  `CREATE INDEX` / `ALTER TABLE` afterwards. Partitioning is not accepted in
  this form, and neither is a query source in a workflow body.

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
- On a RANGE/HASH/LIST partitioned table, a PK-target `UPSERT` resolves its
  conflict against the proposed row's partition-local heap (the primary key
  includes every partition column), and a secondary-`UNIQUE`-target `UPSERT`
  probes every partition-local root so the occupant is found in whichever
  partition holds it. `SET` that changes a partition-key column moves the row
  between partition heaps, and a no-conflict `UPSERT` is still checked against
  the cross-partition `UNIQUE` probe. `UPSERT` on legacy TENANT tables is
  rejected.
- Privileges: `INSERT` and `UPDATE`. `RETURNING` also requires `SELECT`.

`INSERT`, `UPDATE`, `DELETE`, and `UPSERT` accept `RETURNING` items or
`RETURNING *` after `LIMIT` (when present). The list sees the row after the
write: inserted/updated values, or the deleted row. Expressions, aliases, and
`excluded.col` on `UPSERT` are allowed. Windows and aggregates are rejected.
Results stream over NSQL as `RowDesc` plus `DataBatch` frames, then
`CommandComplete` with `Affected`. `EXPLAIN` shows `Upsert` for `UPSERT`.

Retryable application mutations use the engine/NSQL idempotency API rather
than alternate SQL syntax: local callers use `Session.ExecIdempotent`, and the
official Go driver uses `Conn.ExecIdempotent`. The key and typed request are
fenced in the same transaction as the mutation. See `docs/execution.md` and
`docs/protocol.md`.

`ANALYZE [table]` writes `NSST` statistics into the catalog tree (key `S` + name). They survive restart. Version 3 adds bounded exact row counts keyed by stable physical-partition ID. Partitioned tables also receive bounded `NSPS` v1 column/index/vector sketches under immutable table/partition-ID keys. Pruning-aware plans merge them only when every selected stable ID is covered; missing or stale local data falls back to global `NSST`. Local sampling is capped at 4,096 rows per member and each record caps column/index/vector entries at 64 and is trimmed, lowest-priority sketches first, to fit one catalog record (about 8 KiB). `EXPLAIN` / `EXPLAIN ANALYZE` return one row per operator with estimates (and actuals / time / CPU / memory / disk / cache / spill / workers / index).

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
statement is rejected inside an explicit transaction. The stream is scoped to
the connection's selected database; there is no row-tenant selector. See
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

`CREATE FULLTEXT INDEX` and `SEARCH col [WEIGHT n] [, col [WEIGHT n] …] FOR '…'` are implemented, including `WITH (ANALYZER = 'simple' | 'english' | 'french' | 'german' | 'spanish')` and multi-column indexes (1–8 `STRING`/`TEXT` columns; `SEARCH` uses an index whose column list matches in the same order; phrases do not cross fields; optional `WEIGHT` scales per-field BM25 tf in `(0, 64]`, default 1). Default `simple` preserves Phase 10 BM25/phrase behaviour (no stemming, no stop list); `english` is Snowball English (Porter2) plus stop-word dictionary v1 plus synonym dictionary v1 at query time (catalog revision 3; revision 1 stem-only and revision 2 stem+stops indexes still decode). `french` / `german` / `spanish` are Snowball 3.x stemmers plus that language's Snowball stop-word dictionary v1 (catalog revision 1). Trailing ASCII `*` on a SEARCH token is prefix search (`cat*` matches `catalog`; exact `cat` does not); trailing ASCII `~` is fuzzy matching (`cat~` matches `cot`; optional `~1` / `~2`; AUTO distance by token length). Unadorned tokens apply typo tolerance only when the analyzed term is absent from the vocabulary (`databse` matches `database`; exact `cat` does not match `cot` when `cat` is indexed; AUTO typo is 0/1/2 for 1–4 / 5–8 / 9+ runes). Prefix, fuzzy, and typo expansion is fail-closed against the query-expansion caps. `HIGHLIGHT(col)` and `SNIPPET(col)` are SELECT-list functions that require `SEARCH`: they wrap original matching tokens (exact/synonym/prefix/fuzzy/typo, same analyzer) with `<mark>` / `</mark>` (override with `HIGHLIGHT(col, pre, post)`); `SNIPPET` is a 16–4096 rune window (default 160) around the densest match cluster. `SELECT * … SEARCH … FACET col [, col …]` returns independent histograms over the full match set (`facet`, `value`, `count`); `LIMIT` is per-facet top-N; `NULL` is skipped; 1–8 discrete columns and 1024 distinct values fail closed. See `docs/fulltext.md`.

`CREATE VECTOR INDEX … USING HNSW` and `NEAREST col TO …` are implemented, including `WITH (QUANTIZATION = 'F16' | 'I8' | 'NONE')` for a quantised-traversal HNSW graph with exact re-rank. `CREATE VECTOR INDEX … USING IVF WITH (LISTS = n [, PROBES = m])` builds an inverted-file (coarse-quantiser) index, and `CREATE VECTOR INDEX … USING IVFPQ WITH (LISTS = n, SUBSPACES = M [, PROBES = m])` an inverted-file index with product-quantised residual codes and an exact re-rank — real-valued metrics only. Partitioned tables own one HNSW, IVF, or IVFPQ index tree per partition, and `NEAREST` merges candidates from the selected partitions. `BITVECTOR<N>` columns rank by `USING HAMMING` (the default and only metric for a bit column) over an exact flat search or a Hamming HNSW graph. `SPARSEVECTOR<N>` stores only non-zero coordinates (`NSSV`); `CREATE VECTOR INDEX … USING SPARSE` builds an inverted index (`NSSM` / `NSSP`) and ranks by `COSINE` (default) or `INNER_PRODUCT` — not on dense/`BITVECTOR` columns. Partitioned sparse indexes are likewise per-partition and merged by `NEAREST`. See `docs/vector.md`.

`WHERE` + `SEARCH` + `NEAREST` is one hybrid plan (`docs/optimizer.md`). `EXPLAIN` shows `Candidates` and `Rerank`. A second `NEAREST` (dense `VECTOR` + `SPARSEVECTOR`) is dense+sparse+BM25 fusion (`docs/vector.md`); `EXPLAIN` shows `Rerank bm25+vector+sparse fusion`.
