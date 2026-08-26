# JSON

JSON is a first-class column. The stored form is binary **NSJB**, not UTF-8 text. Insert a JSON string; the engine parses it.

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

## Path extract

`column.part.part`. A numeric part indexes an array (`tags.0`). A missing path is SQL `NULL`. Scalars become `STRING`, `BOOL`, or `DECIMAL`; nested objects and arrays stay `JSON`.

Equality, range, `BETWEEN`, and `IS NULL` on a path index are sargable. `EXPLAIN` shows `IndexScan … json`.

## Limits

These fail closed:

| Limit | Value |
|---|---|
| Depth | 32 |
| Document | 1 MiB |
| String | 1 MiB |
| Array / object elements | 1 048 576 |

JSON cannot be a foreign-key column. Path indexes cannot be `UNIQUE` full-text or vector indexes.

See also the engine note [`docs/json.md`](https://github.com/bzync/nextsql/blob/main/docs/json.md) in the repository.
