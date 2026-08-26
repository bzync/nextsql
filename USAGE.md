# NextSQL Usage Manual

End-to-end user documentation for **NextSQL 0.1.0-dev**. This manual documents the **currently usable product surface**, not every capability planned in `PROJECT.md`.

NextSQL is a new database. It is not PostgreSQL, MySQL, MongoDB, Elasticsearch, or a vector-store compatibility layer. It has its own storage format, SQL dialect, wire protocol (NSQL v1), and official drivers.

**Implementation status (2026-08-24):** phases 0–15 are complete. P16 correctness/SLO closure remains open. P17 schema lifecycle + storage maintenance is shipped except `REBUILD INDEX ... ONLINE`, which remains deliberately rejected/deferred. P18's implementable SQL-completeness scope is shipped; partition-wise aggregation/join waits for P21 physical partitioning. P19–P30 remain planned/open and must not be presented as installed functionality until their `TODO.md` exit gates are green.

Treat 0.1.0-dev as an engine under measurement, not a drop-in production replacement, until you have run `nextsql-bench --slo` plus the relevant crash, recovery, security, and HA suites on your hardware.

Documentation roles:

```text
PROJECT.md = intended finished product
TODO.md    = implementation/status truth
TODO.md    = implementation status, sequencing, dependencies, and gates
SKILLS.md  = engineering/agent contract
AGENTS.md  = repository agent entrypoint
this file  = current user/operator surface
```

Internal format and design notes live in [`docs/`](docs/). This file is the operator and application walkthrough.

---

## Contents

1. [What you need to know first](#1-what-you-need-to-know-first)

2. [Install and build](#2-install-and-build)

3. [End-to-end walkthrough](#3-end-to-end-walkthrough)

4. [SQL dialect](#4-sql-dialect)

5. [Relational data](#5-relational-data)

6. [JSON](#6-json)

7. [Full-text search](#7-full-text-search)

8. [Vectors](#8-vectors)

9. [Hybrid queries](#9-hybrid-queries)

10. [Geospatial](#10-geospatial)

11. [Transactions](#11-transactions)

12. [Users, roles, and tenants](#12-users-roles-and-tenants)

13. [Command-line reference](#13-command-line-reference)

14. [Schema migrations](#14-schema-migrations)

15. [Server configuration](#15-server-configuration)

16. [Drivers](#16-drivers)

17. [TLS and client-held keys](#17-tls-and-client-held-keys)

18. [Backup, restore, and PITR](#18-backup-restore-and-pitr)

19. [Logical export and import](#19-logical-export-and-import)

20. [High availability](#20-high-availability)

21. [Status, diagnostics, and benches](#21-status-diagnostics-and-benches)

22. [Limits and current gaps](#22-limits-and-current-gaps)

23. [Further reading](#23-further-reading)

---

## 1. What you need to know first

### Binaries

| Binary | Role |

|---|---|

| `nextsql` | CLI: init, exec, migrate, backup, restore, verify, export, import, diagnose, status, cluster |

| `nextsqld` | Server. Speaks the native NSQL v1 protocol on `--listen` (default `127.0.0.1:7210`) |

| `nextsql-bench` | Official measurements. Encryption, WAL, and fsync stay on |

### Files that matter

A data directory is **not** a single file. After `nextsql init` and the first server start you typically have:

```text

DATA-DIR/

  nextsql.db            encrypted pages (16 KiB logical)

  nextsql.db.keys       wrapped DEKs only — never the root unlock key

  nextsql.db.wal/       encrypted WAL control + segments

  nextsql.db.undo/      encrypted UNDO log

  nextsql.users         password hashes (PBKDF2-HMAC-SHA256)

  nextsql.acl           roles and grants

  nextsql.audit         JSON-lines audit log (mode 0600)

  raft/                 present only when Raft HA is enabled

```

The **root unlock key** is a separate `--key-file` (`NSKY`, mode `0600`). Keep it **off** the data volume. Stolen disks, snapshots, WAL, backups, vector trees, and full-text trees stay ciphertext without that file.

### Non-negotiable rules

- Keys and passwords never go in a connection URL. Drivers reject `://`, `key=`, and `password=` in the address.

- TLS 1.3 is required for any non-loopback listen address or remote client.

- `--insecure` / `insecureNoTLS` is loopback-only.

- A backup or export is not valid until `verify` (including a restore/import test) succeeds.

- Statements without `BEGIN` auto-commit. One SQL statement per `exec` / `-c`.

- Every table needs a `PRIMARY KEY`. That key is the clustered B+Tree key.

- Unquoted identifiers fold to lowercase.

### Honest threat model

A privileged attacker on a **live unlocked** `nextsqld` can see keys, pages, and rows in RAM. NextSQL decrypts to execute SQL. Encryption protects files at rest and TLS protects the wire. It does not hide plaintext from the running process.

---

## 2. Install and build

### Packages

From a NextSQL checkout, build Linux (`.deb`, `.tar.gz`, `.run`) and Windows (`.zip`, `setup.exe`) installers:

```bash

./scripts/build-installers.sh

```

Linux:

```bash

sudo dpkg -i dist/nextsql_*_amd64.deb

# or: sudo ./dist/nextsql-*-linux-amd64.run

```

Windows: run `dist/nextsql-*-windows-amd64-setup.exe` as Administrator (`/S` for silent).

The packages copy binaries and a config file. They do **not** initialize a data directory or start `nextsqld`. After install:

```bash

printf 'secret\n' > /tmp/nextsql.pw && chmod 600 /tmp/nextsql.pw

nextsql init --data-dir /var/lib/nextsql --key-file /etc/nextsql/root.key \\

  --user app --password-file /tmp/nextsql.pw

sudo systemctl enable --now nextsql          # Linux

# Windows: Start-Service NextSQL

```

Layout, silent flags, and user-local Linux installs: [`packaging/README.md`](packaging/README.md).

### Build from source

Requires **Go 1.22+**.

```bash

git clone https://github.com/bzync/nextsql.git

cd nextsql

go build -o nextsql       ./cmd/nextsql

go build -o nextsqld      ./cmd/nextsqld

go build -o nextsql-bench ./cmd/nextsql-bench

```

Confirm:

```bash

./nextsql version

# nextsql 0.1.0-dev

```

Official drivers live in the same tree (`drivers/go`, `drivers/node`, `drivers/bun`, `drivers/deno`, `drivers/php`). They are not published as public packages.

---

## 3. End-to-end walkthrough

This section is a complete first session: initialize, serve, create a multimodel table, write, query, index, and inspect.

Use two terminals. Paths below are examples. In production put the key file on a different volume from `--data-dir`.

### 3.1 Create a password file and initialize

```bash

printf 'secret\n' > /tmp/nextsql.pw

chmod 600 /tmp/nextsql.pw

./nextsql init \\

  --data-dir /var/lib/nextsql \\

  --key-file /etc/nextsql/root.key \\

  --user app --password-file /tmp/nextsql.pw

```

What that does:

1. Creates `/etc/nextsql/root.key` if it is missing (32-byte AES root, mode `0600`).

2. Creates `/var/lib/nextsql/nextsql.db` and the keystore sidecar.

3. Bootstraps user `app` with `ADMIN` on `CLUSTER` and `CONNECT` on the database.

Printed output includes the data-file path plus database and file identity UUIDs.

`--user` requires `--password-file`. The password file may end with a newline; it is stripped.

### 3.2 Start the server (loopback)

```bash

./nextsqld \\

  --data-dir /var/lib/nextsql \\

  --key-file /etc/nextsql/root.key \\

  --listen 127.0.0.1:7210 \\

  --user app --password-file /tmp/nextsql.pw

```

`--user` on `nextsqld` upserts that user if you want the server to create or refresh credentials at start. At least one user must exist or `nextsqld` refuses to start.

Loopback may run without TLS. Any bind that is not loopback requires `--tls-cert` and `--tls-key`.

### 3.3 Run SQL from the CLI

`nextsql exec` is a one-shot client. After resolve, `user`, a password, and SQL are required. SQL is `-c` or a single positional argument. Flags, `NEXTSQL_*` environment variables, and `.env` files can supply the rest (see [§13](#13-command-line-reference)).

```bash

CLI=(./nextsql exec --addr 127.0.0.1:7210 --user app --password-file /tmp/nextsql.pw --insecure)

```

Create the product table used throughout this manual:

```bash

"${CLI[@]}" -c "

CREATE TABLE products (

    id          UUID PRIMARY KEY DEFAULT UUID(),

    tenant_id   UUID NOT NULL,

    name        STRING NOT NULL,

    description TEXT,

    price       DECIMAL(12,2),

    metadata    JSON,

    embedding   VECTOR<F32,8>,

    location    POINT,

    created_at  TIMESTAMPTZ DEFAULT NOW()

)

"

```

`VECTOR<F32,1536>` is the production-shaped type. The walkthrough uses dimension **8** so you can type literals by hand. Dimension must match between the column, inserts, and `NEAREST`.

Insert two rows (JSON is a string literal that is parsed and stored as binary `NSJB`; vectors are parenthesized floats; points are `POINT(lon, lat)`):

```bash

"${CLI[@]}" -c "

INSERT INTO products (tenant_id, name, description, price, metadata, embedding, location)

VALUES

  ('11111111-1111-1111-1111-111111111111',

   'Aero 2',

   'wireless noise cancelling headphones',

   12900.00,

   '{\\"category\\":\\"headphones\\",\\"color\\":\\"black\\"}',

   (0.1, 0.2, 0.3, 0.4, 0.5, 0.6, 0.7, 0.8),

   POINT(-73.9857, 40.7484)),

  ('11111111-1111-1111-1111-111111111111',

   'Desk Lamp',

   'adjustable LED desk lamp',

   4500.00,

   '{\\"category\\":\\"lighting\\",\\"color\\":\\"white\\"}',

   (0.9, 0.1, 0.0, 0.0, 0.0, 0.0, 0.0, 0.0),

   POINT(-73.9780, 40.7580))

"

```

Successful DML prints `affected N`.

### 3.4 Query

```bash

"${CLI[@]}" -c "SELECT name, price FROM products WHERE price BETWEEN 4000 AND 15000"

"${CLI[@]}" -c "SELECT name FROM products WHERE metadata.category = 'headphones'"

```

Result columns are tab-separated.

### 3.5 Indexes

```bash

"${CLI[@]}" -c "CREATE INDEX ix_category ON products (metadata.category)"

"${CLI[@]}" -c "CREATE FULLTEXT INDEX ix_desc ON products (description)"

"${CLI[@]}" -c "CREATE VECTOR INDEX ix_emb ON products (embedding) USING HNSW"

"${CLI[@]}" -c "CREATE SPATIAL INDEX ix_loc ON products (location)"

"${CLI[@]}" -c "ANALYZE products"

```

`ANALYZE` writes statistics the optimizer uses. `EXPLAIN` shows the chosen access path:

```bash

"${CLI[@]}" -c "EXPLAIN SELECT name FROM products WHERE metadata.category = 'headphones'"

"${CLI[@]}" -c "EXPLAIN SELECT name FROM products SEARCH description FOR 'wireless noise cancelling' LIMIT 5"

"${CLI[@]}" -c "EXPLAIN SELECT name FROM products NEAREST embedding TO (0.1, 0.2, 0.3, 0.4, 0.5, 0.6, 0.7, 0.8) LIMIT 5"

```

### 3.6 Full text, vectors, hybrid, geo

```bash

"${CLI[@]}" -c "

SELECT name FROM products

SEARCH description FOR 'wireless noise cancelling'

LIMIT 5

"

"${CLI[@]}" -c "

SELECT name FROM products

NEAREST embedding TO (0.1, 0.2, 0.3, 0.4, 0.5, 0.6, 0.7, 0.8)

USING COSINE

LIMIT 5

"

"${CLI[@]}" -c "

SELECT name, price FROM products

WHERE metadata.category = 'headphones' AND price <= 15000

SEARCH description FOR 'wireless noise cancelling'

NEAREST embedding TO (0.1, 0.2, 0.3, 0.4, 0.5, 0.6, 0.7, 0.8)

LIMIT 5

"

"${CLI[@]}" -c "

SELECT name FROM products

WHERE DWITHIN(location, POINT(-73.9857, 40.7484), 2000)

"

```

That hybrid statement is one physical plan: structured filters, BM25, and ANN share the same cost model and the same WAL / MVCC / encryption path.

### 3.7 Multi-statement transaction

`nextsql exec` sends one statement per invocation, so a `BEGIN` … `COMMIT` session needs a driver (see [§16](#16-drivers)). From a Go session:

```go

conn.Exec(ctx, `BEGIN SNAPSHOT`)

conn.Exec(ctx, `UPDATE products SET price = price * 1.1 WHERE name = $1`, types.StringValue("Aero 2"))

conn.Exec(ctx, `COMMIT`)

```

Without `BEGIN`, each statement is its own committed transaction.

### 3.8 Inspect the instance

```bash

./nextsql diagnose --data-dir /var/lib/nextsql

./nextsql status --local --data-dir /var/lib/nextsql --key-file /etc/nextsql/root.key

```

`diagnose` reads plaintext headers only (no key). `status --local` also opens the database and prints table count, LSNs, isolated pages, query counters, and admission stats. Default `status` (no `--local`) dials `nextsqld` and prints `mode server`.

---

## 4. SQL dialect

Pipeline: SQL → lexer → parser → binder / catalog → logical plan → rewrite → cost model → vectorized executor.

See [`docs/sql.md`](docs/sql.md) for catalog internals.

### Rules

- One statement per request. A trailing `;` is optional. Extra tokens after the statement are a syntax error.

- Unquoted identifiers fold to lowercase. Quoted `"Ident"` is preserved.

- Reserved words include `FOREIGN`, `REFERENCES`, `CONSTRAINT`, `CASCADE`, `RESTRICT`, `ACTION`, `MATCH`, `ALTER`, `ADD`, `RENAME`, `ORDER`, `ASC`, `DESC`, `IF`, `EXISTS`, `WITH`, and `OVER`. Quote them (`"foreign"`) to use them as identifiers.

- Parameters are `$1`, `$2`, … (1-based). The CLI `-c` flag does not bind parameters; use a driver.

- `NULL` is typed. Comparisons with `IS NULL` / `IS NOT NULL`.

- `ORDER BY expr [ASC|DESC] [, …]` sorts the projected result. NULLs sort last in `ASC` and first in `DESC`. Keys may be output aliases, 1-based select-list ordinals, or source columns (kept as hidden sort columns). `SEARCH` orders by BM25 then primary key unless `ORDER BY` is present. `NEAREST` orders by distance then primary key unless `ORDER BY` is present. Hybrid results are reciprocal-rank fused, then truncated to `LIMIT` / `OFFSET` (or re-sorted when `ORDER BY` is present). `SELECT … LIMIT n OFFSET m` skips `m` rows after ordering, then returns up to `n`. `OFFSET` may appear before `LIMIT`. `OFFSET` without `LIMIT` skips and returns the rest. `UPDATE` / `DELETE` still take `LIMIT` only.

- `DROP TABLE [IF EXISTS] name` removes the catalog row. A table referenced by a foreign key cannot be dropped (`foreign_key`). Detached heap/index pages are reclaimed only when safe with respect to active MVCC snapshots, then become reusable through the durable freelist.

- `ALTER TABLE` supports `ADD [COLUMN]`, `DROP [COLUMN]`, `RENAME [COLUMN] … TO`, `RENAME TO`, `ADD CONSTRAINT` / `ADD FOREIGN KEY`, and `DROP CONSTRAINT`. Adding a `NOT NULL` column to a non-empty table requires a `DEFAULT`. A `PRIMARY KEY` column cannot be dropped. `CREATE DATABASE [IF NOT EXISTS] name` creates a new database file named `name` in the same directory as the current database (same key provider). It cannot run inside a transaction and is not written to the current database WAL.

- `FOREIGN KEY` / `REFERENCES` is validated at `CREATE TABLE` and `ALTER TABLE ADD CONSTRAINT` (referenced table, types, PK or UNIQUE target, tenant rule). Insert, update, and delete enforce the stored action. `RESTRICT` / `NO ACTION` reject live children. `CASCADE` deletes or rewrites children (recursive, depth 8 / 100 000 row caps). `SET NULL` nulls FK columns. `SET DEFAULT` evaluates `ApplyDefault(i, Null(type))` on the leader, not the live FK value. Missing parent, illegal SET DEFAULT, or `RESTRICT` children return `foreign_key`; cap hits return `exhausted`.

- Up to eight tables per `SELECT` (`FROM` + up to seven `JOIN`s). `INNER JOIN`, bare `JOIN`, `LEFT` / `RIGHT` / `FULL` `[OUTER] JOIN`, and `CROSS JOIN` are accepted. Outer joins require `ON`. `CROSS JOIN … ON` is a syntax error. `SELECT *` with `GROUP BY` is rejected. `FULL OUTER JOIN` is hash-only and memory-capped (does not spill).

- `NULL` join keys never match (`NULL = NULL` is unknown).

- `SEARCH` and `NEAREST` may be combined with `INNER JOIN` when the rank column belongs to the `FROM` table. The `FROM` table is ranked first, then joined. A rank column on a joined table is rejected. Outer join + `SEARCH` / `NEAREST` is not supported.

### Types

| Type | Notes |

|---|---|

| `UUID` | 16 bytes. `DEFAULT UUID()` |

| `STRING` / `TEXT` | UTF-8. Same encoding; `TEXT` is the long-form name |

| `DECIMAL(p,s)` | `1 ≤ p ≤ 38`, `s ≤ p`. Unscaled integer + scale. `DEFAULT AI()` when `s = 0` |

| `TIMESTAMPTZ` | UTC nanoseconds. `DEFAULT NOW()` |

| `JSON` | Compact binary `NSJB`. Insert a JSON text literal |

| `VECTOR<F32,N>` | `N` in `1…8192`. Finite floats only. Stored off-row |

| `POINT` / `LOCATION` | WGS84 longitude, latitude |

| `BOX` | west, south, east, north |

| `LINESTRING` | at least two vertices |

| `POLYGON` | closed exterior ring, optional holes; 256-vertex cap |

A table **must** declare `PRIMARY KEY`. Secondary indexes store secondary key + primary key. B-tree indexes may add `INCLUDE (cols)`, `WHERE predicate`, and expression keys such as `LOWER(name)`; `EXPLAIN` shows `covering` when the scan does not fetch the heap.

### Statements

```text

CREATE TABLE   [FOREIGN KEY / REFERENCES …]

CREATE INDEX / CREATE UNIQUE INDEX

CREATE SPATIAL INDEX

CREATE FULLTEXT INDEX

CREATE VECTOR INDEX … USING HNSW

DROP INDEX [IF EXISTS] name

REBUILD INDEX name

MAINTAIN DATABASE

MAINTAIN TABLE table_name

MAINTAIN INDEX index_name

INSERT   [RETURNING]

UPSERT   [ON UNIQUE] [SET] [RETURNING]

SELECT   [JOIN | LEFT [OUTER] JOIN] [WHERE] [GROUP BY] [SEARCH] [NEAREST] [LIMIT] [OFFSET]

UPDATE   [WHERE] [LIMIT] [RETURNING]

DELETE   [WHERE] [LIMIT] [RETURNING]

BEGIN    [READ COMMITTED | SNAPSHOT | SERIALIZABLE]

COMMIT

ROLLBACK [TRANSACTION]

ANALYZE  [table]

EXPLAIN  [ANALYZE] <statement>

SET TENANT = … / RESET TENANT

CREATE USER / DROP USER

CREATE ROLE / DROP ROLE

GRANT / REVOKE

```

### Functions (common)

| Area | Calls |

|---|---|

| Defaults | `UUID()`, `NOW()`, `AI()` |

| Aggregates | `COUNT(*)`, `COUNT(col)`, `SUM`, `AVG`, `MIN`, `MAX` |

| Windows | `ROW_NUMBER`, `RANK`, `DENSE_RANK`, `LAG`, `LEAD`, `FIRST_VALUE`, `LAST_VALUE`, and aggregate `OVER (...)` |

| Vector | `COSINE(a,b)`, `L2(a,b)`, `INNER_PRODUCT(a,b)` |

| Geo | `POINT`, `BOX`, `LON`/`LAT`, `DISTANCE`, `DISTANCE_SPHEROID`, `DWITHIN`, `WITHIN`, `COVERS`, `LINELENGTH` (and `ST_*` aliases) |

### Index lifecycle and maintenance

`DROP INDEX` is WAL-durable, crash-safe, Raft-replicated, RBAC checked, audited, and supported by the migration parser/validator for shipped index types.

```sql
DROP INDEX ix_category;
DROP INDEX IF EXISTS ix_old;

REBUILD INDEX ix_category;
```

`REBUILD INDEX name` is currently a **blocking** rebuild. `REBUILD INDEX name ONLINE` is intentionally rejected until concurrent-write correctness is proven; do not treat the blocking implementation as online.

Maintenance is bounded and observable:

```sql
MAINTAIN DATABASE;
MAINTAIN TABLE products;
MAINTAIN INDEX ix_category;
```

Maintenance may reclaim safe dead versions/pages, clean index tombstones, compact eligible UNDO state, respect PITR/WAL retention, and refresh statistics according to the implemented policy. It is subject to CPU, memory, I/O, concurrency, admission, and elapsed-time budgets. Long-running snapshots can intentionally delay physical reclamation.

`UUID()`, `NOW()`, and `AI()` are evaluated at execution, not folded by the optimizer. `AI()` is a `DECIMAL(p,0)` autoincrement starting at 1. Explicit inserts bump the sequence when the value is at least the next number. Allocation is in the statement transaction (`ROLLBACK` reuses). Concurrent inserts exclusive-lock the sequence key. SQL is not re-executed on followers, so the stored integer stays deterministic.

---

## 5. Relational data

```sql

CREATE TABLE items (

    id    UUID PRIMARY KEY DEFAULT UUID(),

    sku   STRING NOT NULL,

    qty   DECIMAL(10,0),

    price DECIMAL(12,2)

);

CREATE UNIQUE INDEX uq_sku ON items (sku);

CREATE INDEX ix_sku_cover ON items (sku) INCLUDE (qty, price);

CREATE INDEX ix_low_sku ON items (LOWER(sku));

INSERT INTO items (sku, qty, price) VALUES

    ('A-1', 3, 19.50),

    ('B-2', 9, 44.00);

SELECT sku, qty FROM items WHERE sku = 'B-2';

SELECT sku FROM items WHERE price BETWEEN 10 AND 50 LIMIT 20;

SELECT sku FROM items ORDER BY sku LIMIT 10 OFFSET 10;

UPDATE items SET qty = qty + 1 WHERE sku = 'A-1';

DELETE FROM items WHERE qty = 0;

UPSERT INTO items (id, sku, qty, price) VALUES (UUID(), 'A-1', 4, 19.50)

    ON UNIQUE (sku)

    SET qty = excluded.qty

    RETURNING sku, qty;

SELECT COUNT(*) FROM items;

SELECT sku, SUM(qty) FROM items GROUP BY sku;

```

Foreign keys may be declared on `CREATE TABLE`. The referenced columns must be exactly a `PRIMARY KEY` or `UNIQUE` btree index (same columns, any order). `DECIMAL` precision and scale must match. `NO ACTION` is stored as `RESTRICT`. Recommended tenant pattern is a composite `PRIMARY KEY (tenant_id, id)` so the FK can include `tenant_id` on both sides at the same position.

```sql

CREATE TABLE customers (

    tenant_id UUID NOT NULL,

    id        UUID NOT NULL DEFAULT UUID(),

    email     STRING NOT NULL,

    PRIMARY KEY (tenant_id, id)

);

CREATE TABLE orders (

    tenant_id   UUID NOT NULL,

    id          UUID NOT NULL DEFAULT UUID(),

    customer_id UUID NOT NULL,

    PRIMARY KEY (tenant_id, id),

    CONSTRAINT fk_orders_customer

        FOREIGN KEY (tenant_id, customer_id)

        REFERENCES customers (tenant_id, id)

        ON DELETE RESTRICT

);

```

These constraints are stored in the catalog (`NSCT` v2) and enforced on `INSERT` / `UPDATE` / `DELETE`. Cascades are ordinary leader-side row writes (WAL + UNDO); followers do not re-run the action. After any `CREATE TABLE` or `CREATE INDEX` (which rewrites descriptors as v2), do not roll the server binary back without restoring a pre-v2 backup.

Joins are cost-based left-deep inner trees (up to eight tables; outer joins are not reordered). `INNER JOIN` / bare `JOIN`, `LEFT` / `RIGHT` / `FULL` `[OUTER] JOIN`, and `CROSS JOIN` are accepted. Hash join is the default and builds the right input. Merge join is chosen for INNER and LEFT when both sides are already index-ordered on the join keys. `FULL` is hash-only and refuses to spill (`exhausted`). `RIGHT` is rewritten to `LEFT`. `NULL` keys do not match. Result order is unspecified unless `ORDER BY` is present.

```sql

SELECT orders.k, items.sku

FROM orders JOIN items ON orders.k = items.k;

SELECT o.id, c.name, l.sku

FROM orders o

JOIN customers c ON c.id = o.customer_id

JOIN lines l ON l.order_id = o.id;

SELECT customers.name, orders.id

FROM customers

LEFT JOIN orders ON orders.customer_id = customers.id;

SELECT customers.name, orders.id

FROM orders

RIGHT JOIN customers ON orders.customer_id = customers.id;

SELECT a.email, b.email

FROM accounts a

FULL OUTER JOIN accounts b ON a.email = b.email AND a.id <> b.id;

SELECT a.n, b.n

FROM t a

CROSS JOIN u b;

```

`UPDATE` / `DELETE` accept `LIMIT` so large mutations can be batched (the official bulk path commits every 8192 rows).

```sql

UPDATE scan SET n = 0 WHERE n <> 0 LIMIT 8192;

DELETE FROM scan LIMIT 8192;

```

---

## 6. JSON

JSON is a first-class column. The stored form is binary `NSJB`, not UTF-8 text. Insert a JSON string; the engine parses it.

```sql

CREATE TABLE products (

    id       UUID PRIMARY KEY DEFAULT UUID(),

    name     STRING NOT NULL,

    metadata JSON

);

INSERT INTO products (name, metadata) VALUES

    ('alpha', '{"category":"electronics","n":1}'),

    ('beta',  '{"category":"books","n":2}');

CREATE INDEX category_index ON products (metadata.category);

SELECT name FROM products WHERE metadata.category = 'electronics';

SELECT metadata.category FROM products WHERE name = 'alpha';

```

Path extract: `column.part.part`. A numeric part indexes an array (`tags.0`). A missing path is SQL `NULL`. Scalars become `STRING`, `BOOL`, or `DECIMAL`; nested objects and arrays stay `JSON`.

Equality, range, `BETWEEN`, and `IS NULL` on a path index are sargable. `EXPLAIN` shows `IndexScan … json`.

Limits (fail closed): depth 32, document 1 MiB, string 1 MiB, 1 048 576 array/object elements. Details: [`docs/json.md`](docs/json.md).

---

## 7. Full-text search

```sql

CREATE TABLE articles (

    id    UUID PRIMARY KEY DEFAULT UUID(),

    title STRING NOT NULL,

    body  TEXT

);

CREATE FULLTEXT INDEX ix_body ON articles (body);

SELECT title FROM articles SEARCH body FOR 'database performance' LIMIT 20;

SELECT title FROM articles SEARCH body FOR '"database performance"';

```

`SEARCH col FOR <string>` sits after `WHERE` / `GROUP BY` and before `LIMIT`. Unquoted tokens are required (AND) and ranked with BM25. Double-quoted groups are phrases (consecutive positions). Results are score descending, then primary key.

Tokenizer: letters and digits; Unicode lowercase; hyphens split tokens; apostrophes inside a token are kept. **No stemmer and no stop-word list** — `cat` does not match `cats`.

`CREATE FULLTEXT INDEX` takes one `STRING`/`TEXT` column. It cannot be `UNIQUE` and cannot use a JSON path.

Without an index, `SEARCH` still runs over the heap (or a sargable `WHERE` path). `EXPLAIN` shows `Search … fulltext` or `Search … seq`.

Limits: term 128 runes, document 100 000 tokens, query 64 tokens. Details: [`docs/fulltext.md`](docs/fulltext.md).

---

## 8. Vectors

```sql

CREATE TABLE documents (

    id        UUID PRIMARY KEY DEFAULT UUID(),

    name      STRING NOT NULL,

    embedding VECTOR<F32,1536>

);

INSERT INTO documents (name, embedding) VALUES ('one', (1, 0, 0 /* … dim must match */));

CREATE VECTOR INDEX docs_embedding ON documents (embedding) USING HNSW;

SELECT name FROM documents NEAREST embedding TO $1 LIMIT 20;

SELECT name FROM documents NEAREST embedding TO (1, 0, 0) USING L2 LIMIT 5;

SELECT name, COSINE(embedding, (1, 0, 0)) FROM documents;

```

`NEAREST col TO <vector>` sits after `WHERE` / `GROUP BY` / `SEARCH` and before `LIMIT`. Optional `USING COSINE | L2 | INNER_PRODUCT` (default `COSINE`).

`NEAREST` ranks by lower-is-closer distance: cosine distance `1 − similarity`, L2, and `−dot` for inner product.

Without a vector index, search is exact flat. With `USING HNSW`, `EXPLAIN` shows `Nearest … hnsw`. Default construction: `M = 16`, `efConstruction = 64`. Search never silently lowers `k` to improve latency.

Limits: dimension `1…8192`; elements must be finite (`NaN` / `Inf` fail closed). Query dimension must match the column. Details: [`docs/vector.md`](docs/vector.md).

---

## 9. Hybrid queries

Structured filters, JSON paths, BM25, and ANN are **one** planning problem. The optimizer chooses filter-then-ANN or ANN-then-filter from the cost model. Candidates are fused with reciprocal rank fusion (`k = 60`) and truncated to `LIMIT`.

```sql

SELECT id, name, price

FROM products

WHERE metadata.category = 'headphones'

  AND price <= 15000

SEARCH description FOR 'wireless noise cancelling'

NEAREST embedding TO $query

LIMIT 20;

```

`EXPLAIN` shows `Candidates` and `Rerank bm25+vector`. Operator order is not hard-coded. Run `ANALYZE` first so statistics exist.

Details: [`docs/optimizer.md`](docs/optimizer.md).

---

## 10. Geospatial

Coordinates are **(longitude, latitude)** on WGS84. This is not PostGIS.

```sql

CREATE TABLE places (

    id   UUID PRIMARY KEY DEFAULT UUID(),

    name STRING NOT NULL,

    loc  POINT NOT NULL

);

INSERT INTO places (name, loc) VALUES

    ('empire', POINT(-73.9857, 40.7484)),

    ('jfk',    'POINT(-73.7781 40.6413)');

CREATE SPATIAL INDEX ix_loc ON places (loc);

SELECT name FROM places

WHERE DWITHIN(loc, POINT(-73.9857, 40.7484), 5000);

SELECT name, DISTANCE(loc, POINT(-73.9857, 40.7484))

FROM places

WHERE WITHIN(loc, BOX(-74.1, 40.6, -73.8, 40.9));

SELECT DISTANCE_SPHEROID(POINT(-74.0060, 40.7128), POINT(-118.2437, 34.0522));

```

WKT also coerces: `POINT(lon lat)`, `BOX(w s, e n)`, `LINESTRING(...)`, `POLYGON((...))`.

`CREATE SPATIAL INDEX` requires a single `POINT` column (not `UNIQUE`). The optimizer uses a Morton geohash prefix for `DWITHIN`, `DISTANCE(col, const) < r`, `WITHIN`, and `COVERS`. The residual predicate is exact. `EXPLAIN` shows `IndexScan … spatial`.

`DISTANCE` is haversine meters. `DISTANCE_SPHEROID` is Vincenty on the WGS84 ellipsoid; near-antipodal pairs fall back to haversine.

3D, geography-vs-geometry dual types, and spheroidal distance-to-polyline are not implemented. Details: [`docs/geo.md`](docs/geo.md).

---

## 11. Transactions

```sql

BEGIN;                    -- default SNAPSHOT

BEGIN READ COMMITTED;

BEGIN SNAPSHOT;

BEGIN SERIALIZABLE;

COMMIT;

ROLLBACK;

```

| Level | Snapshot | Locks |

|---|---|---|

| `READ COMMITTED` | Refreshed each statement | Exclusive key locks until end of transaction |

| `SNAPSHOT` | Taken at `BEGIN` | Exclusive key locks; first-committer-wins on write-write |

| `SERIALIZABLE` | Taken at `BEGIN` | Snapshot plus shared key/range locks (strict 2PL) |

`SERIALIZABLE` is lock-based, not SSI. Deadlock aborts the requester (`deadlock`); that transaction must `ROLLBACK`.

Readers do not see uncommitted writes. Commit is acknowledged only after group-commit WAL + `fsync`. Details: [`docs/mvcc.md`](docs/mvcc.md).

---

## 12. Users, roles, and tenants

### Bootstrap

`nextsql init --user` / `nextsqld --user` creates a user with `ADMIN` on `CLUSTER` and `CONNECT` on the database. Passwords are hashed (PBKDF2-HMAC-SHA256, 100 000 iterations). They are never stored plaintext.

### SQL

```sql

CREATE USER reporter IDENTIFIED BY 's3cret';

CREATE ROLE analyst;

GRANT analyst TO reporter;

GRANT SELECT ON TABLE products TO analyst;

GRANT ADMIN ON CLUSTER TO dba;

REVOKE SELECT ON TABLE products FROM analyst;

DROP USER reporter;

```

A new principal has no rights until granted. Least privilege is fail-closed.

Privileges include `CONNECT`, `SELECT`, `INSERT`, `UPDATE`, `DELETE`, `CREATE`, `DROP`, `INDEX`, `EXECUTE`, `GRANT`, `BACKUP`, `REPLICATION`, `ADMIN`.

Scopes: `CLUSTER`, `DATABASE`, `SCHEMA`, `TABLE`, `COLUMN`, `FUNCTION`, `BACKUP`, `REPLICATION`, `ADMINISTRATION`. `GRANT SELECT ON products TO analyst` treats a bare name as a table.

`DROP USER` deletes the password hash and disconnects that user's sessions.

### Tenants

Tables with a `tenant_id` column (`UUID`, `STRING`, or `TEXT`) are tenant-keyed. Cross-tenant leakage tolerance is 0.

```sql

SET TENANT = '11111111-1111-1111-1111-111111111111';

SET TENANT = $1;

RESET TENANT;

```

- `SET TENANT` is session-local. It is not stored in the catalog.

- Bound `SELECT` / `UPDATE` / `DELETE` / `SEARCH` / `NEAREST` / export get an implicit `tenant_id = <bound>` predicate.

- `INSERT` injects the bound value and rejects a mismatched `tenant_id`. `UPDATE` cannot reassign `tenant_id`.

- Production sessions (ACL attached, not cluster `ADMIN`) **must** `SET TENANT` before they touch a tenant-keyed table. Unbound access is `forbidden`.

- Cluster `ADMIN` may omit `SET TENANT` and see every tenant.

This is row isolation by `tenant_id`, not a separate catalog or encryption domain. Details: [`docs/security.md`](docs/security.md).

---

## 13. Command-line reference

```text

nextsql init     --data-dir DIR --key-file FILE [--user NAME --password-file FILE]

                 [--buffer-pages N]

nextsql exec     [--addr HOST:PORT] [--user NAME] [--password-file FILE]

                 [--database NAME] [--tls-ca FILE | --insecure]

                 [--env-file PATH | --no-env] [--tenant VALUE]

                 [-c SQL | SQL]

nextsql migrate  status|pending|version|validate|create|up|down|force|repair

                 [--dir DIR] [--addr HOST:PORT] [--user NAME]

                 [--password-file FILE] [--tls-ca FILE | --insecure]

                 [--env-file PATH | --no-env] [--tenant VALUE]

nextsql backup   --data-dir DIR --key-file FILE --out DIR

nextsql restore  --from DIR --data-dir DIR --key-file FILE

                 [--wal-archive DIR] [--until-lsn N | --until RFC3339]

nextsql verify   --from DIR --key-file FILE

nextsql export   --data-dir DIR --key-file FILE --out DIR

nextsql import   --from DIR --data-dir DIR --key-file FILE

nextsql diagnose --data-dir DIR

nextsql status   [--addr HOST:PORT] [--user NAME] [--password-file FILE]

                 [--database NAME] [--tls-ca FILE | --insecure]

                 [--env-file PATH | --no-env]

nextsql status --local [--data-dir DIR] [--key-file FILE]

nextsql cluster status --data-dir DIR

nextsql version

nextsql help

```

`--out` for backup and export must not already exist. The tool writes a temporary directory, verifies, then publishes atomically.

`nextsql exec` talks to a running `nextsqld`. `--user`, `--password-file`, and `-c` are optional as flags when the environment or a dotenv file supplies them. After resolve, user, password, and SQL must be present. `-c` wins over a positional SQL argument. Mixing `--data-dir` / `--key-file` onto `exec` is an error. `NEXTSQL_KEY_FILE` in the environment or `.env` is ignored (the root key is not an exec input).

Address is `host:port` only. Values containing `://`, `key=`, or `password=` are rejected. Keys are never accepted in connection URLs.

Every server-mode connect must set TLS (`--tls-ca` / `NEXTSQL_TLS_CA`) or `--insecure` / `NEXTSQL_INSECURE=true`, including `127.0.0.1`. `--insecure` is rejected unless the address is loopback. `--tls-ca` is a PEM CA / server certificate; SNI is taken from the host in `--addr`.

### Client configuration (`exec`)

Priority, highest wins: explicit flags (including empty strings) > non-empty process environment > `.env.local` (cwd only) > `.env` (walk from the working directory toward `/`, at most 16 levels) > defaults.

`--no-env` skips dotenv files. `--env-file PATH` loads only that file (missing path is an error). Empty environment variables do not override a file value.

| Variable | Meaning | Default |

|---|---|---|

| `NEXTSQL_ADDR` | `host:port` | `127.0.0.1:7210` |

| `NEXTSQL_USER` | Auth user | none (required) |

| `NEXTSQL_PASSWORD_FILE` | Password file (newline stripped) | none |

| `NEXTSQL_PASSWORD` | Inline password (CI convenience) | none |

| `NEXTSQL_DATABASE` | Hello `database` field | empty (optional) |

| `NEXTSQL_TLS_CA` | PEM CA / server cert | none |

| `NEXTSQL_INSECURE` | `true` / `1` / `yes` → plaintext, loopback only | false |

| `NEXTSQL_TENANT` | Optional `SET TENANT` after connect | unset |

| `NEXTSQL_MIGRATIONS_DIR` | Migration file directory | `./migrations` |

If both a password file and `NEXTSQL_PASSWORD` are set, the file wins. Using the inline password prints a one-line stderr warning: prefer `NEXTSQL_PASSWORD_FILE`. Do not put `NEXTSQL_PASSWORD` in a committed file.

`.env.local` is the recommended gitignored overlay. A parent directory’s `.env.local` is not loaded.

This client loader is not `nextsqld --config`. Do not put the root unlock key in the application `.env`. Local and remote `.env` examples: [§14](#14-schema-migrations).

### Schema migrations

Full walkthrough, `.env` examples, and sample files: [§14](#14-schema-migrations).

`nextsql migrate` is always **server mode**. It never reads `--data-dir` or the root key. Default directory is `./migrations`.

```text

nextsql migrate validate | create NAME | status | pending | version

nextsql migrate up|down [--count N] [--to VERSION] [--dry-run]

nextsql migrate force VERSION --confirm

nextsql migrate repair --confirm

```

Recommended v1 workflow is still forward-only (`up`). `down` may apply compensating DML plus supported schema lifecycle statements such as `DROP TABLE`, `ALTER TABLE`, `CREATE TABLE`, and `DROP INDEX`. `DROP INDEX` is now understood by the migration parser/validator.

---

## 14. Schema migrations

Keep schema in Git. `nextsql migrate` applies timestamped SQL files to a running `nextsqld` over NSQL. It is always **server mode**: it never opens `--data-dir` and never reads the root unlock key. A laptop `nextsqld` and a remote VPS are the same session. Only TLS and latency differ.

Prefer a password file. Never put the root unlock key in the application `.env`.

Default directory is `./migrations` (`--dir` / `NEXTSQL_MIGRATIONS_DIR`). Connection flags and dotenv match `exec` ([§13](#13-command-line-reference)).

### Commands

```text

nextsql migrate validate

nextsql migrate create add_orders

nextsql migrate status

nextsql migrate pending

nextsql migrate version

nextsql migrate up   [--count N] [--to VERSION] [--dry-run]

nextsql migrate down [--count N] [--to VERSION] [--dry-run]

nextsql migrate force VERSION --confirm

nextsql migrate repair --confirm

```

| Command | Connects? | Notes |

|---|---|---|

| `validate` | no | filenames, pairing, parse; no server |

| `create NAME` | no | writes empty `.up.sql` / `.down.sql` |

| `status` | yes | version, dirty, applied/pending, checksum mismatches |

| `pending` | yes | unapplied versions |

| `version` | yes | one line: version or `none` |

| `up` | yes | apply pending in order |

| `down` | yes | newest-first; legal compensating SQL only |

| `force VERSION --confirm` | yes | rewrite history; does **not** run SQL |

| `repair --confirm` | yes | refresh stored checksums of already-applied files |

`validate` / `create` do not connect. `status` / `up` / `down` / `force` / `repair` create `nsql_schema_migrations` if it is missing. The CLI never sends `GRANT` SQL: creating that table grants `SELECT`/`INSERT`/`UPDATE`/`DELETE` on it to the handshake user.

Each up file is one transaction: `BEGIN`, dirty history insert, each statement, finalize (`dirty=0`), `COMMIT`. On error the file is rolled back. Files must not contain `BEGIN`/`COMMIT`/`ROLLBACK`, `SET TENANT`, or `GRANT`/`REVOKE`/`CREATE`/`DROP` `USER`/`ROLE` (those persist outside WAL). Pass `--tenant` on the CLI instead.

`--dry-run` connects, lists the files that would run, checksums them, and parses every statement. It does not `BEGIN` and does not execute user SQL.

### Local development (`.env`)

`.env` is safe to commit if it contains no secrets. `.env.local` is gitignored and is the place for the password-file path.

```bash

# .env  — safe to commit if it contains no secrets

NEXTSQL_ADDR=127.0.0.1:7210

NEXTSQL_USER=app

NEXTSQL_INSECURE=true

NEXTSQL_MIGRATIONS_DIR=./migrations

# NEXTSQL_DATABASE is optional; leave unset on 0.1.0-dev

```

```bash

# .env.local  — gitignored

NEXTSQL_PASSWORD_FILE=/home/dev/secrets/nextsql.pw

# Optional local-only operator vars; ignored by exec/migrate:

# NEXTSQL_DATA_DIR=/var/lib/nextsql

# NEXTSQL_KEY_FILE=/etc/nextsql/root.key

```

Do **not** put the root key path in the committed `.env`. Do **not** put `NEXTSQL_PASSWORD=...` in a committed file. The password file is preferred over an inline password.

`NEXTSQL_INSECURE=true` is loopback-only. A laptop that omits both `--insecure` and `--tls-ca` fails at resolve, including `127.0.0.1`.

### Remote VPS (`.env.production`)

Load this on the migrate runner, not on the database host. The VPS `nextsqld` already has the root key; the migrator must not.

```bash

# .env.production  — loaded with --env-file on the migrate runner, not on the DB host

NEXTSQL_ADDR=db.example.com:7210

NEXTSQL_USER=migrator

NEXTSQL_PASSWORD_FILE=/run/secrets/nextsql-migrator.pw

NEXTSQL_TLS_CA=/etc/nextsql/ca.pem

NEXTSQL_MIGRATIONS_DIR=./migrations

# no NEXTSQL_KEY_FILE — the VPS nextsqld has the key; CI must not

```

```bash

nextsql migrate up --env-file .env.production

nextsql exec --env-file .env.production -c "SELECT version FROM nsql_schema_migrations"

```

Remote connections need `--tls-ca` (or `NEXTSQL_TLS_CA`). `--insecure` is rejected off loopback.

On Raft, connect to the **leader**. Writes already fail with `unavailable` if there is no leader. History inserts and `CREATE TABLE` replicate as WAL records; followers do not re-run the migrator.

### File names

```text

migrations/

  20260818120000_create_customers.up.sql

  20260818120000_create_customers.down.sql

  20260818120100_create_orders.up.sql

  20260818120100_create_orders.down.sql

```

Pattern: `YYYYMMDDHHMMSS_slug.up.sql` (optional matching `.down.sql`). Version is a 14-digit UTC timestamp. `migrate create NAME` slugs the name (lowercase, non-alphanumerics to `_`, max 64 characters) and retries `+1s` if that timestamp already exists.

A version may have up only (forward-only). Down without up fails `validate`. Integer prefixes such as `0001_name.up.sql` are not accepted.

Preferred style: one statement per file. Multi-statement files are split on `;` (not inside strings or comments), up to 32 statements per file. Each statement is parsed before any `BEGIN`. Comments (`--` and `/* … */`) are kept in the checksum.

Checksum: SHA-256 of the file after CR LF → LF and stripping a single UTF-8 BOM if present. Comment edits change the digest; `repair --confirm` updates stored checksums of already-applied files.

### Example files

Recommended pattern: composite `PRIMARY KEY (tenant_id, id)` so an FK can include `tenant_id` on both sides. `id UUID PRIMARY KEY` plus `REFERENCES parent (tenant_id, id)` is illegal unless a UNIQUE btree index on exactly `(tenant_id, id)` exists first.

Copies of the first two files live in [`docs/examples/migrations/`](docs/examples/migrations/). That is documentation only; this module has no application `migrations/` directory.

`20260818120000_create_customers.up.sql`:

```sql

CREATE TABLE customers (

    tenant_id  UUID NOT NULL,

    id         UUID NOT NULL DEFAULT UUID(),

    email      STRING NOT NULL,

    name       STRING NOT NULL,

    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    PRIMARY KEY (tenant_id, id)

);

CREATE UNIQUE INDEX ux_customers_tenant_email ON customers (tenant_id, email);

```

A v1-legal compensating down for seed data:

```sql

DELETE FROM customers;

```

`20260818120100_create_orders.up.sql`:

```sql

CREATE TABLE orders (

    tenant_id   UUID NOT NULL,

    id          UUID NOT NULL DEFAULT UUID(),

    customer_id UUID NOT NULL,

    total       DECIMAL(12,2) NOT NULL,

    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    PRIMARY KEY (tenant_id, id),

    CONSTRAINT fk_orders_customer

        FOREIGN KEY (tenant_id, customer_id)

        REFERENCES customers (tenant_id, id)

        ON DELETE RESTRICT

        ON UPDATE RESTRICT

);

CREATE INDEX ix_orders_customer ON orders (tenant_id, customer_id);

```

`20260818120200_create_lines.up.sql`:

```sql

CREATE TABLE lines (

    tenant_id  UUID NOT NULL,

    id         UUID NOT NULL DEFAULT UUID(),

    order_id   UUID NOT NULL,

    sku        STRING NOT NULL,

    qty        DECIMAL(12,0) NOT NULL,

    PRIMARY KEY (tenant_id, id),

    CONSTRAINT fk_lines_order

        FOREIGN KEY (tenant_id, order_id)

        REFERENCES orders (tenant_id, id)

        ON DELETE CASCADE

        ON UPDATE RESTRICT

);

CREATE INDEX ix_lines_order ON lines (tenant_id, order_id);

```

`RESTRICT` rejects a parent `DELETE`/`UPDATE` that still has children. `CASCADE` deletes or rewrites matching children on the leader (depth 8 / 100 000 row caps). `SET NULL` / `SET DEFAULT` are also enforced.

After apply, multi-join and `LEFT JOIN` work (up to eight tables). Inner joins may be reordered; result order is unspecified unless `ORDER BY` is present. `SEARCH` / `NEAREST` may run on the `FROM` table of an inner join; outer join + search is rejected.

```sql

SELECT orders.id, customers.name, lines.sku

FROM orders

JOIN customers ON customers.tenant_id = orders.tenant_id

              AND customers.id = orders.customer_id

JOIN lines     ON lines.tenant_id = orders.tenant_id

              AND lines.order_id = orders.id;

```

### Apply

```bash

nextsql migrate validate

nextsql migrate up --dry-run

nextsql migrate up

nextsql migrate status

```

`validate` needs no server. `up` and `status` need a running `nextsqld` and the same user / password / TLS settings as `exec`.

### Forward-only (honest)

The recommended v1 workflow is still **forward-only** (`up`) when you want expand/contract deploys. `DROP TABLE`, `ALTER TABLE`, and `DROP INDEX` are legal in migration files, so a down migration can reverse supported schema changes when the compensating SQL is valid.

`migrate down` walks applied versions newest-first (`--count` / `--to`; `--count 0` means all). Each down file is one transaction: mark the history row dirty with `direction='down'`, run statements, `DELETE` that history row. On error the file is rolled back and the up row stays.

Legal down statements include `DELETE`, `INSERT`, `UPDATE`, supported `CREATE TABLE` / `CREATE INDEX`, `DROP TABLE`, `DROP INDEX`, `ALTER TABLE`, and `CREATE DATABASE`. A version with no `.down.sql` is refused (exit 6). Security DDL (`CREATE USER`, `GRANT`, …) remains outside the normal migration-file surface unless explicitly supported by the current implementation.

`force VERSION --confirm` is an operator action. It rewrites history without running SQL. `VERSION` `0` or `none` deletes all history rows (objects stay). Otherwise rows newer than `VERSION` are deleted and `VERSION` is upserted clean.

`repair --confirm` updates stored checksums to match the working tree. It does not re-run SQL.

A dirty history row or a checksum mismatch stops `up` (exit 3 / 4). Run **one migrator per database**: the history primary key is the lock.

### Privileges

The migrate user needs `CONNECT` + `CREATE` on the database, table DML on `nsql_schema_migrations` (auto-granted on first bootstrap **as that user**), and whatever the files themselves require. Cluster `ADMIN` is sufficient.

If an `ADMIN` created the history table first, a later least-privilege migrator has no table DML: `GRANT` out of band or re-bootstrap as that user.

`--tenant` issues `SET TENANT` after connect. Production non-`ADMIN` users must set a tenant before files touch tenant-keyed tables. History has no `tenant_id`; it is cluster-global.

---

## 15. Server configuration

```text

nextsqld --data-dir DIR --key-file FILE

         [--listen 127.0.0.1:7210] [--config FILE]

         [--tls-cert FILE --tls-key FILE]

         [--require-client-key]

         [--user NAME --password-file FILE]

         [--auth-file FILE] [--audit-file FILE]

         [--buffer-pages N] [--log-level debug|info|warn|error]

         [--wal-archive DIR]

         [--node-id ID --raft-bind ADDR --raft-join id=addr,... [--raft-bootstrap]]

```

`--data-dir` is required (flag or config). `--key-file` is required unless `--require-client-key` is set.

### Config file (`--config`)

Simple `key=value`. Comments start with `#`. Unknown keys are rejected.

```text

data_dir=/var/lib/nextsql

key_file=/etc/nextsql/root.key

listen_addr=127.0.0.1:7210

log_level=info

buffer_pages=1024

tls_cert=/etc/nextsql/server.crt

tls_key=/etc/nextsql/server.key

require_client_key=false

audit_file=

wal_archive=/var/lib/nextsql-wal

max_inflight_queries=32

max_query_queue=128

query_queue_wait_ms=5000

max_result_rows=1000000

node_id=

raft_bind=

raft_join=

raft_bootstrap=false

```

Command-line flags override the file.

### Admission and budgets

Every `Exec` takes an in-flight slot. If all slots are busy, the query queues. If the queue is full or the wait exceeds `query_queue_wait_ms`, the server returns `unavailable` instead of growing without bound.

Defaults: 32 in-flight, 128 queued, 5 s wait.

Per-query budgets (defaults): 64 MiB memory, 256 MiB spill, 1 GiB I/O, 30 s, 1 000 000 result rows / 64 MiB result bytes. Exceeding a budget fails with `exhausted`. Worker goroutines are bounded (`min(GOMAXPROCS, 8)` per query through a process pool).

Wire defaults: 1 MiB packet, 1 MiB SQL, 256 parameters, 64 prepared statements per session, 128 concurrent sessions, 60 s idle.

---

## 16. Drivers

Official drivers speak NSQL v1. **Do not put keys or passwords in a URL.** TLS 1.3 is required off loopback.

| Runtime | Path | Open |

|---|---|---|

| Go | [`drivers/go`](drivers/go) | `nextsql.Open(nextsql.Config{…})` |

| Node.js 18+ | [`drivers/node`](drivers/node) | `connect({ address, user, password, tls })` |

| Bun | [`drivers/bun`](drivers/bun) | same shape as Node |

| Deno | [`drivers/deno`](drivers/deno) | `import { connect } from "./mod.ts"` |

| PHP 8.1+ | [`drivers/php`](drivers/php) | `NextSQL\Client::connect([…])` |

Shared TypeScript types: [`drivers/js/types.d.ts`](drivers/js/types.d.ts).

Common API: `exec` (materialize), `query` (stream rows), `prepare` / execute, `cancel`, `close`. A connection is single-flight: a second query while rows are open returns `conflict`.

### Go

```go

package main

import (

  "context"

  "fmt"

  "log"

  "os"

  nextsql "github.com/bzync/nextsql/drivers/go"

  "github.com/bzync/nextsql/internal/sql/types"

)

func main() {

  conn, err := nextsql.Open(nextsql.Config{

    Address:       "127.0.0.1:7210",

    User:          "app",

    Password:      os.Getenv("NEXTSQL_PASSWORD"),

    InsecureNoTLS: true, // loopback only

  })

  if err != nil {

    log.Fatal(err)

  }

  defer conn.Close()

  dec, err := types.ParseDecimal("50.00")

  if err != nil {

    log.Fatal(err)

  }

  res, err := conn.Exec(context.Background(),

    `SELECT name FROM items WHERE price < $1`,

    types.DecimalValue(dec, types.Type{Kind: types.KindDecimal, Precision: 12, Scale: 2}),

  )

  if err != nil {

    log.Fatal(err)

  }

  for _, row := range res.Rows {

    fmt.Println(row[0].String())

  }

  stmt, err := conn.Prepare(context.Background(),

    `SELECT sku FROM items WHERE sku = $1`)

  if err != nil {

    log.Fatal(err)

  }

  defer stmt.Close()

  _, err = stmt.Exec(context.Background(), types.StringValue("A-1"))

  if err != nil {

    log.Fatal(err)

  }

}

```

`Query` returns `*Rows` (`Next`, `Values`, `Columns`, `Close`). Canceling the `Query` context opens a side connection and cancels the in-flight statement.

For `--require-client-key`, set `Config.KeyProvider` (never a URL). See [§17](#17-tls-and-client-held-keys).

### Node.js / Bun

```js
const { connect } = require("./drivers/node/nextsql"); // Bun: drivers/bun/nextsql.js

const conn = await connect({
  address: "127.0.0.1:7210",

  user: "app",

  password: process.env.NEXTSQL_PASSWORD,

  insecureNoTLS: true,
});

const res = await conn.exec("SELECT name FROM items WHERE price < $1", [
  { kind: "decimal", value: "50.00" },
]);

console.log(res.rows);

const stmt = await conn.prepare("SELECT sku FROM items WHERE sku = $1");

const rows = await stmt.query(["A-1"]);

await stmt.close();

await conn.close();
```

Typed parameters: `{ kind: "uuid" | "decimal", value: "…" }`, numbers, strings, booleans, `Date`, `number[]` (vectors), `{ lon, lat }` (points), `{ west, south, east, north }` (boxes), or a plain object (JSON).

TypeScript: `import { connect, type Config } from "./drivers/node/nextsql"`.

### Deno

```ts
import { connect } from "./drivers/deno/mod.ts";

const conn = await connect({
  address: "127.0.0.1:7210",

  user: "app",

  password: Deno.env.get("NEXTSQL_PASSWORD"),

  insecureNoTLS: true,
});

const res = await conn.exec("SELECT 1");

await conn.close();
```

### PHP 8.1+

```php

require 'drivers/php/autoload.php';

$conn = NextSQL\Client::connect([

    'address' => '127.0.0.1:7210',

    'user' => 'app',

    'password' => getenv('NEXTSQL_PASSWORD'),

    'insecureNoTLS' => true,

]);

$res = $conn->exec('SELECT name FROM items WHERE price < $1', [

    ['kind' => 'decimal', 'value' => '50.00'],

]);

$conn->close();

```

Remote TLS:

```php

$conn = NextSQL\Client::connect([

    'address' => 'db.example.com:7210',

    'user' => 'app',

    'password' => getenv('NEXTSQL_PASSWORD'),

    'tls' => ['cafile' => '/etc/nextsql/ca.pem', 'servername' => 'db.example.com'],

]);

```

---

## 17. TLS and client-held keys

### Remote server

Any listen address that is not loopback requires TLS 1.3:

```bash

# example self-signed pair for a lab — replace with a real certificate

openssl req -x509 -newkey rsa:2048 -sha256 -days 365 -nodes \\

  -keyout /etc/nextsql/server.key \\

  -out    /etc/nextsql/server.crt \\

  -subj "/CN=db.example.com"

./nextsqld \\

  --data-dir /var/lib/nextsql \\

  --key-file /etc/nextsql/root.key \\

  --listen 0.0.0.0:7210 \\

  --tls-cert /etc/nextsql/server.crt \\

  --tls-key  /etc/nextsql/server.key \\

  --user app --password-file /tmp/nextsql.pw

```

Client:

```bash

./nextsql exec \\

  --addr db.example.com:7210 \\

  --tls-ca /etc/nextsql/server.crt \\

  --user app --password-file /tmp/nextsql.pw \\

  -c "SELECT 1"

```

`--insecure` against a remote host is rejected.

### REQUIRE CLIENT KEY

`nextsqld --require-client-key` does **not** load `--key-file`. After password auth the first client sends the 32-byte root over TLS (`TypeUnlock`). The host does not keep a long-lived key file.

```bash

./nextsqld \\

  --data-dir /var/lib/nextsql \\

  --require-client-key \\

  --listen 127.0.0.1:7210 \\

  --user app --password-file /tmp/nextsql.pw

```

The root still exists in RAM for the life of the unlocked process. That is not a zero-knowledge property.

Drivers:

- Go: `Config.KeyProvider` (a `crypto.KeyProvider` that returns the root DEK).

- Node / Bun / Deno: `key: <32-byte Buffer | Uint8Array>`.

- PHP: `'key' => $clientRoot` (32-byte string).

Field-level `ENCRYPTED CLIENT` columns are designed but **not implemented**. See [`docs/security.md`](docs/security.md).

---

## 18. Backup, restore, and PITR

A successful write is not a valid backup. `nextsql backup` publishes the destination only after hash checks **and** a restore-test open.

```bash

# physical backup (pages, WAL, UNDO, users, ACL — still ciphertext)

./nextsql backup \\

  --data-dir /var/lib/nextsql \\

  --key-file /etc/nextsql/root.key \\

  --out /backups/nextsql-2026-08-18

# re-check later

./nextsql verify --from /backups/nextsql-2026-08-18 --key-file /etc/nextsql/root.key

# restore into an empty directory

./nextsql restore \\

  --from /backups/nextsql-2026-08-18 \\

  --data-dir /var/lib/nextsql-restored \\

  --key-file /etc/nextsql/root.key

```

The backup directory is not a tar of plaintext files. Layout (`NSBK` v1): `header`, `keystore` (wrapped DEKs only), `manifest`, `members/*`, `verified`. Stolen backups are unreadable without the root unlock key. Audit logs are operational and are **not** part of the backup.

### Point-in-time recovery

Enable WAL archival on the server:

```bash

./nextsqld \\

  --data-dir /var/lib/nextsql \\

  --key-file /etc/nextsql/root.key \\

  --wal-archive /var/lib/nextsql-wal \\

  --user app --password-file /tmp/nextsql.pw

```

Recycled (and checkpoint-time current) segments are copied as sealed `NSWA` archives.

```bash

# replay committed records with LSN <= N

./nextsql restore \\

  --from /backups/nextsql-2026-08-18 \\

  --data-dir /var/lib/nextsql-pitr \\

  --key-file /etc/nextsql/root.key \\

  --wal-archive /var/lib/nextsql-wal \\

  --until-lsn 12000

# stop at the latest backup/archive stamp <= this time

./nextsql restore \\

  --from /backups/nextsql-2026-08-18 \\

  --data-dir /var/lib/nextsql-pitr \\

  --key-file /etc/nextsql/root.key \\

  --wal-archive /var/lib/nextsql-wal \\

  --until 2026-08-18T15:04:05Z

```

`--until` is **backup / archive time**, not per-commit time. Do not treat it as a commit-accurate clock. Details: [`docs/backup.md`](docs/backup.md).

---

## 19. Logical export and import

Export is a **logical** snapshot (schema + committed rows). It is not a page-level backup and it is not PITR. Vector payloads are inlined; indexes are recreated on import.

```bash

./nextsql export \\

  --data-dir /var/lib/nextsql \\

  --key-file /etc/nextsql/root.key \\

  --out /exports/nextsql-2026-08-18

./nextsql import \\

  --from /exports/nextsql-2026-08-18 \\

  --data-dir /var/lib/nextsql-copy \\

  --key-file /etc/nextsql/root.key

```

The destination is created if `nextsql.db` is missing, with a **new** identity under the same root. Existing dest tables with the same name fail closed (`already_exists`). Uncommitted writes are not exported.

An export is not valid until the built-in import-test succeeds (`verified` marker). Details: [`docs/export.md`](docs/export.md).

---

## 20. High availability

Optional Raft cluster ([hashicorp/raft](https://github.com/hashicorp/raft)). Minimum **3 voting nodes**. NextSQL does not invent consensus.

A write is acknowledged only after:

1. The leader executes the statement and flushes its local WAL.

2. The sealed replication batch is committed on a Raft quorum.

If there is no leader, writes fail closed (`unavailable`). SQL is **not** re-executed on followers, so `UUID()` / `NOW()` / `AI()` stay deterministic.

Engineering targets on a healthy 3-node cluster: leader election `< 3 s`, service recovery `< 5 s`. Continuous service is a design objective (`≥ 99.999%` availability SLO), not a zero-downtime claim.

### Start three nodes

Only **one** node bootstraps. The other two use the same `--raft-join` list without `--raft-bootstrap`. All replicas share the keystore / root unlock key.

```bash

# node n1 (bootstrap)

./nextsqld --data-dir /var/lib/nextsql-n1 --key-file /etc/nextsql/root.key \\

  --tls-cert cert.pem --tls-key key.pem --listen 0.0.0.0:7210 \\

  --user app --password-file /tmp/nextsql.pw \\

  --node-id n1 --raft-bind 10.0.0.1:7211 \\

  --raft-join n1=10.0.0.1:7211,n2=10.0.0.2:7211,n3=10.0.0.3:7211 \\

  --raft-bootstrap

# node n2

./nextsqld --data-dir /var/lib/nextsql-n2 --key-file /etc/nextsql/root.key \\

  --tls-cert cert.pem --tls-key key.pem --listen 0.0.0.0:7210 \\

  --user app --password-file /tmp/nextsql.pw \\

  --node-id n2 --raft-bind 10.0.0.2:7211 \\

  --raft-join n1=10.0.0.1:7211,n2=10.0.0.2:7211,n3=10.0.0.3:7211

# node n3 — same as n2 with n3 / 10.0.0.3

```

```bash

./nextsql cluster status --data-dir /var/lib/nextsql-n1

# node n1

# state Leader

# leader n1

# voters 3

# has_leader true

```

A wiped replica is restored with `nextsql backup` / `restore` (same identity and keys), then rejoined. Raft logs are ciphertext (replication DEK). HA is not a substitute for backups.

Details: [`docs/ha.md`](docs/ha.md).

---

## 21. Status, diagnostics, and benches

### Diagnose and status

```bash

./nextsql status

./nextsql status --local --data-dir /var/lib/nextsql --key-file /etc/nextsql/root.key

./nextsql diagnose --data-dir /var/lib/nextsql

```

`nextsql status` (default) is **server mode**. It dials a running `nextsqld`, completes the NSQL handshake, and prints `mode server`, `addr`, `user`, `database`, and `ok`. It does not open the data directory and does not print LSNs. Connection flags and dotenv match `exec` (`--addr`, `--user`, `--password-file`, `--database`, `--tls-ca` / `--insecure`, `--env-file` / `--no-env`). Mixing `--data-dir` / `--key-file` onto server-mode `status` is an error.

`nextsql status --local` is the data-directory inspect: format-family versions plus opened table count, `durable_lsn` / `checkpoint_lsn` / `next_lsn`, isolated-page count, query/error/commit counters, admission inflight/queue, and cluster fields when Raft is running. It requires `--data-dir` and `--key-file` (or `NEXTSQL_DATA_DIR` / `NEXTSQL_KEY_FILE`). `--addr` and `--local` together is an error.

`diagnose` checks format-family versions (currently all **v1**) and plaintext headers. A newer or older-than-min file fails closed — there is no silent rewrite. `diagnose` does not need a key.

Isolated pages are a fail-closed corruption path (`*.isolated`). NextSQL never returns a known corrupted record.

### Exit codes

`nextsql` maps errors to process exit codes for CI:

| Code | When |

|---|---|

| 0 | Success |

| 1 | Usage, unknown command, invalid flags |

| 2 | Connection, authentication, or TLS |

| 3 | Dirty migration history (`migrate up` / `down` / `status`) |

| 4 | Migration checksum mismatch |

| 5 | SQL execution error |

| 6 | Migration validation error (bad files, unimplemented down, `--to`) |

| 7 | Local-mode missing `--data-dir` / `--key-file` |

### `nextsql-bench`

Official numbers keep encryption, WAL, `fsync`, checksums, MVCC, and authentication on. Numbers from one host are not product guarantees.

```bash

./nextsql-bench --quick

./nextsql-bench --workload all|page|point|range|insert|update|delete|txn|join|agg|json|fulltext|vector|hybrid

./nextsql-bench --duration 1s --rows 128 --concurrency 1

# labeled SLO suite (hardware, filesystem, encryption, durability printed on every row)

./nextsql-bench --slo

./nextsql-bench --slo --slo-max-rows 1000000 --slo-vectors 256 --duration 2s

```

`--slo` seeds a throwaway encrypted database and measures cached PK lookup, secondary-index equality, durable single-row INSERT/UPDATE, bulk INSERT plus `COUNT(*)` / `GROUP BY` / range / join at each scale, hybrid `WHERE`+`SEARCH`+`NEAREST`, and HNSW recall\@10 / recall\@100.

100M rows and 1M-vector HNSW still need a longer run (`--slo-max-rows 100000000` / `--slo-vectors 1000000`). Published host numbers: [`docs/ops.md`](docs/ops.md).

### Tests

```bash

go test ./...

go test -race ./...          # needs a C compiler

go test ./tests/integration ./tests/crash ./tests/ha

```

---

## 22. Limits, feature status, and current gaps

### Hard limits

| Limit | Value |

|---|---|

| Logical page | 16 KiB |

| Packet / SQL text | 1 MiB |

| Parameters | 256 |

| JSON depth / size | 32 / 1 MiB |

| Vector dimension | 8192, finite elements |

| LINESTRING / POLYGON vertices | 256 |

| JOIN tables | 8 (`FROM` + up to seven `JOIN`s) |

| Foreign keys per table | 16 |

| Columns per foreign key | 8 |

| FK cascade depth | 8 |

| FK cascade touched rows | 100 000 |

| Wire result | 64 MiB |

| Default result rows | 1 000 000 |

### Shipped after the original P0–P15 manual baseline

P17/P18 added user-visible behavior that older copies of this manual did not describe:

- `DROP INDEX [IF EXISTS] name` for shipped index types

- blocking `REBUILD INDEX name`

- safe heap/index page reclamation and durable freelist reuse

- bounded `MAINTAIN DATABASE`, `MAINTAIN TABLE`, and `MAINTAIN INDEX`

- broader modern SQL: DISTINCT, HAVING, CASE, set operations, subqueries, CTEs/recursive CTEs, windows, UPSERT/RETURNING, covering/partial/expression indexes, Top-N, and improved join reordering

- migration parser/validator support for `DROP INDEX`

### Deliberately not shipped / still open

- `REBUILD INDEX ... ONLINE` — blocking rebuild is the shipped path; `ONLINE` remains rejected until concurrent-write safety is proven

- P16 release gate remains open until the corrected 1M HNSW measurement and 100M randomized B+Tree invariant soak are green

- partition-wise aggregation/join — waits on P21 physical partitioning

- P19 WORKFLOW / TRIGGER / SCHEDULE / TASK

- P20 CDC / change streams

- P21 native table partitioning

- P22 follower reads / read scaling

- P23 Vector Engine 2.0 features such as F16/I8/bit vectors, IVF/IVF-PQ, quantization, and sparse retrieval

- P24 Full-text Search 2.0 features such as richer analyzers/language handling/highlighting

- P25 Security 2.0 features such as mTLS/service identities, short-lived credentials, external IdP, field-level client encryption, password-hash evolution, and audit hardening

- P26 system catalog / introspection 2.0

- P27 workload governance / operational maturity

- P28 Professional Installer + NextSQL Manager

- P29 web-based NextSQL Studio

- P30 NextSQL Intelligence + built-in RAG

- Multi-primary writes are not part of the current roadmap core

### Existing functional limitations

- Outer `JOIN` together with `SEARCH` or `NEAREST` remains unsupported where documented; inner join is allowed when the rank column is on the `FROM` table

- Current shipped FTS does not yet include the P24 richer analyzer/language feature set

- Current shipped vector engine remains the production-gated F32 + flat/HNSW surface until P23 extensions land

### Known measurement / correctness notes (0.1.0-dev)

- Large sequential SQL `DELETE` is correct after the leaf-merge fix. Official 10M timing methodology is published in `docs/ops.md`.

- 100M-row analytics are published: the current tracker records COUNT **56 µs**, GROUP BY **16.31 s**, PK range **2.21 ms**, and join **35.54 s** on the labeled ext4 benchmark environment.

- The corrected distinct-vector 100K validation records HNSW p95 **3.317 ms**, recall@10 **1.000**, recall@100 **0.999**. The corrected 1M-vector exit-gate run is still required before P16 closes.

- The randomized B+Tree soak must also reach its current P16 exit requirement before P16 is marked complete.

- `nextsql-bench --slo` on your hardware is the source of deployment-specific latency numbers, not this manual.

### Planned features are not current syntax

`PROJECT.md` describes the intended finished product. This manual does **not** expose planned P19–P30 grammar as usable syntax before implementation.

For example, do not assume these work merely because they are planned:

```text
CREATE WORKFLOW ...
CREATE TRIGGER ...
CREATE SCHEDULE ...
CDC subscription syntax
partitioning DDL
follower-read consistency syntax
```

Check `TODO.md`, server capability metadata when available, and the matching-version manual before using a planned feature.

---

## 23. Further reading

| Document | Topic |

|---|---|

| [PROJECT.md](PROJECT.md) | Intended final NextSQL product/end-state |

| [TODO.md](TODO.md) | Current implementation status, open gates, measurements |

| [ROADMAP.md](ROADMAP.md) | Simplified, non-authoritative roadmap derived from `TODO.md` |

| [SKILLS.md](SKILLS.md) | Engineering and agent operating contract |

| [AGENTS.md](AGENTS.md) | Repository agent entrypoint |

| [README.md](README.md) | Product overview and quick start |

| [docs/sql.md](docs/sql.md) | Dialect, types, catalog |

| [docs/optimizer.md](docs/optimizer.md) | Rewrites, costing, hybrid plans, `EXPLAIN` |

| [docs/execution.md](docs/execution.md) | Vectorized batches, budgets |

| [docs/json.md](docs/json.md) | Binary JSON and path indexes |

| [docs/fulltext.md](docs/fulltext.md) | Tokenizer, BM25, inverted index |

| [docs/vector.md](docs/vector.md) | `VECTOR<F32,N>`, HNSW, distances |

| [docs/geo.md](docs/geo.md) | WGS84 types and predicates |

| [docs/mvcc.md](docs/mvcc.md) | Isolation, UNDO |

| [docs/wal.md](docs/wal.md) | WAL, checkpoints |

| [docs/protocol.md](docs/protocol.md) | Native wire protocol |

| [docs/security.md](docs/security.md) | Keys, TLS, RBAC, tenants |

| [docs/backup.md](docs/backup.md) | Backup, restore, PITR |

| [docs/export.md](docs/export.md) | Logical export / import |

| [docs/ops.md](docs/ops.md) | Metrics, admission, SLO numbers |

| [docs/ha.md](docs/ha.md) | Raft clustering |

| [docs/examples/migrations/](docs/examples/migrations/) | Sample `customers` / `orders` migration files |

For implementation truth, always prefer the matching-version `TODO.md` and measured `docs/*` over older examples or planned product descriptions.

---

## Documentation alignment rule

This manual is intentionally narrower than `PROJECT.md`.

```text
PROJECT.md
→ what NextSQL is expected to become

TODO.md
→ what is implemented now

SKILLS.md / AGENTS.md
→ how repository agents must build and verify it

this manual
→ how users operate the currently shipped surface
```

When a planned feature lands, update this manual only after its implementation, tests, documentation, and applicable exit gate are complete.
