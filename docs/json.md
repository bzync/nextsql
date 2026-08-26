# Native JSON (Phase 9)

JSON is a first-class type. The stored form is compact binary (`NSJB`), not UTF-8 JSON text. Path extract and path indexes use the same WAL, MVCC, and page-encrypted row store as every other column.

## Stored form (`NSJB`)

| Offset | Size | Field |
|---|---|---|
| 0 | 4 | Magic `NSJB` |
| 4 | 1 | Version (`1`) |
| 5 | … | Value |

Value tags (little-endian):

| Tag | Meaning |
|---|---|
| `0x00` | null |
| `0x01` | false |
| `0x02` | true |
| `0x03` | i64 |
| `0x04` | string (`u32` length + UTF-8) |
| `0x05` | number token (`u32` length + ASCII, for non-int64 values) |
| `0x06` | array (`u32` body size, `u32` count, values) |
| `0x07` | object (`u32` body size, `u16` count, then `u16` key length + key + value) |

Container sizes let path extract skip unused siblings. Object keys are stored sorted; duplicate keys keep the last value.

Limits (fail closed): depth 32, document 1 MiB, string 1 MiB, array/object 1 048 576 elements. Malformed text or binary returns a typed error. The parser is fuzzed.

`INSERT` of a `STRING`/`TEXT` literal into a `JSON` column parses the text and stores `NSJB`. `EncodeRow` rejects non-`NSJB` payloads. Display (`Value.String`) writes compact JSON text.

Production pages, WAL, and UNDO stay encrypted. The binary document is never left as readable UTF-8 JSON on disk.

## Path extract

```sql
SELECT metadata.category FROM products;
SELECT name FROM products WHERE metadata.category = 'electronics';
```

`column.part.part` walks objects by key. A numeric part indexes an array (`tags.0`). A missing path is SQL `NULL`. Scalars become `STRING`, `BOOL`, or `DECIMAL`; nested objects and arrays stay `JSON`.

## Path indexes

```sql
CREATE INDEX category_index ON products (metadata.category);
```

Requires one JSON path key. The secondary B+Tree key is the extracted scalar plus the primary key (unique indexes omit the extra primary-key suffix). Missing paths index as `NULL`.

`metadata.category = …`, range comparisons, `BETWEEN`, and `IS NULL` are sargable. `EXPLAIN` shows `IndexScan … json`. Residual predicates stay exact; unlike a spatial prefix, a path index does not over-select.

Index maintenance is the same as any secondary index: insert, update, delete, `CREATE INDEX` on existing rows, and rollback.

## Examples

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
