# Relational data

Every table is a clustered B+Tree on its `PRIMARY KEY`. Secondary indexes store the secondary key plus that primary key.

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

`UPDATE` / `DELETE` accept `LIMIT` so large mutations can be batched (the official bulk path commits every 8192 rows).

```sql
UPDATE scan SET n = 0 WHERE n <> 0 LIMIT 8192;
DELETE FROM scan LIMIT 8192;
```

## Physical partitioning

Bounded single-column `RANGE`, `HASH`, and `LIST` partitioning routes rows to
local encrypted heaps and exposes pruning in `EXPLAIN`. Non-unique B+Tree,
covering, partial, expression, JSON-path, and spatial indexes have one physical
root per partition.

```sql
CREATE TABLE events (
    region STRING NOT NULL,
    id     UUID NOT NULL DEFAULT UUID(),
    note   STRING,
    PRIMARY KEY (region, id)
) PARTITION BY LIST (region) (
    PARTITION americas VALUES IN ('us', 'ca'),
    PARTITION europe   VALUES IN ('eu')
);

ALTER TABLE events ADD PARTITION apac VALUES IN ('ap', 'au');
-- archived is an existing unpartitioned table with the exact same schema and indexes.
ALTER TABLE events ATTACH PARTITION archived VALUES IN ('old');
ALTER TABLE events DETACH PARTITION archived;
```

`ATTACH` consumes an existing unpartitioned table named by the partition after
streaming typed validation of its matching schema, indexes, and rows; no roots
or rows are copied. `DETACH` publishes that owned member as an unpartitioned
table of the same name. Both are atomic WAL/catalog ownership transfers. HASH
membership changes, secondary `UNIQUE`, FULLTEXT, and HNSW partition indexes
remain rejected. See the repository's `docs/partitioning.md` for exact DDL,
RBAC, recovery, and lifecycle limits.

`ANALYZE events` records exact stable-partition row counts plus bounded local
column/index/vector sketches. Pruned plans use local costing only when every
selected partition has a matching versioned sketch; otherwise they fall back
to the global table distribution. Local samples cap at 4,096 rows and each
encrypted catalog record caps at 64 entries per sketch class and 15 KiB total.

## Foreign keys

Declared on `CREATE TABLE` or `ALTER TABLE ADD CONSTRAINT`. The referenced columns must be exactly a `PRIMARY KEY` or `UNIQUE` btree index (same columns, any order). `DECIMAL` precision and scale must match. `NO ACTION` is stored as `RESTRICT`.

Recommended account-scoped key pattern is a composite `PRIMARY KEY (account_id, id)` so the FK can include `account_id` on both sides at the same position.

```sql
CREATE TABLE customers (
    account_id UUID NOT NULL,
    id        UUID NOT NULL DEFAULT UUID(),
    email     STRING NOT NULL,
    PRIMARY KEY (account_id, id)
);

CREATE TABLE orders (
    account_id   UUID NOT NULL,
    id          UUID NOT NULL DEFAULT UUID(),
    customer_id UUID NOT NULL,
    PRIMARY KEY (account_id, id),
    CONSTRAINT fk_orders_customer
        FOREIGN KEY (account_id, customer_id)
        REFERENCES customers (account_id, id)
        ON DELETE RESTRICT
);
```

These constraints are stored in the catalog (`NSCT` v3; v1/v2 remain readable) and enforced on `INSERT` / `UPDATE` / `DELETE`. Cascades are ordinary leader-side row writes (WAL + UNDO); followers do not re-run the action.

| Action | Behavior |
|---|---|
| `RESTRICT` / `NO ACTION` | Reject if live children still point at the old key |
| `CASCADE` | Delete or rewrite children (recursive; depth 8 / 100 000 row caps) |
| `SET NULL` | Null FK columns |
| `SET DEFAULT` | Evaluate `ApplyDefault` on the leader, not the live FK value |

Missing parent, illegal `SET DEFAULT`, or `RESTRICT` children return `foreign_key`. Cap hits return `exhausted`. After any `CREATE TABLE` or `CREATE INDEX` (which rewrites descriptors as v2), do not roll the server binary back without restoring a pre-v2 backup.

Other FK rules:

- `MATCH SIMPLE` only. `MATCH FULL` is rejected.
- `VECTOR` and `JSON` cannot be FK columns.
- At most 16 foreign keys per table and 8 columns per key.
- If both tables are account-keyed, the FK must include `account_id` on both sides at the **same position**.
- Cyclic `CASCADE` graphs are rejected at DDL time. Self-referential FKs and cyclic `RESTRICT` graphs are allowed.

## Joins

Up to eight tables per `SELECT` (`FROM` + up to seven `JOIN`s). `INNER JOIN`, bare `JOIN`, `LEFT` / `RIGHT` / `FULL` `[OUTER] JOIN`, and `CROSS JOIN` are accepted. Outer joins require `ON`. `CROSS JOIN … ON` is a syntax error.

Hash join is the default and builds the right input. Inner joins are cost-based left-deep (a smaller build side is preferred; equal costs keep written table order). Outer joins are not reordered. Merge join is chosen for INNER and LEFT when both sides are already index-ordered on the join keys. `FULL` is hash-only and refuses to spill (`exhausted`). `RIGHT` is rewritten to `LEFT`. `NULL` keys do not match (`NULL = NULL` is unknown). Result order is unspecified unless `ORDER BY` is present.

```sql
SELECT orders.k, items.sku
FROM orders JOIN items ON orders.k = items.k;

SELECT customers.name, orders.id
FROM customers
LEFT JOIN orders ON orders.customer_id = customers.id;

SELECT a.email, b.email
FROM accounts a
FULL OUTER JOIN accounts b ON a.email = b.email AND a.id <> b.id;

SELECT a.n, b.n
FROM t a
CROSS JOIN u b;
```

`SEARCH` and `NEAREST` may be combined with `INNER JOIN` when the rank column belongs to the `FROM` table. Outer join + `SEARCH` / `NEAREST` is not supported. `SELECT *` with `GROUP BY` is rejected.
