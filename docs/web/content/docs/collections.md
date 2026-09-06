# Collections

`STRUCT`, `ARRAY`, and `MAP` are first-class column types (Collections track C1–C3, landed). They share the clustered B+Tree, WAL, MVCC, and encryption path with every other type. They are **not** JSON — JSON stays binary `NSJB` with path extract. All seven official drivers encode them.

They are not `ENCRYPTED CLIENT`-eligible and not foreign-key columns. Nesting is bounded at depth 8.

## STRUCT

Fixed, named, heterogeneous fields. Orders field-by-field; `NULL` fields sort first. Usable as `PRIMARY KEY` / `ORDER BY`.

```sql
CREATE TABLE people (
    id      UUID PRIMARY KEY DEFAULT UUID(),
    address STRUCT<city STRING, zip STRING>
);

INSERT INTO people (address)
VALUES (STRUCT('Paris' AS city, '75001' AS zip));

SELECT address.city FROM people WHERE address.zip = '75001';
```

## ARRAY

Variable-length homogeneous list. `T` may be any type, including another collection, up to `2²⁰` elements. Orders element-by-element; a shorter prefix sorts first.

```sql
CREATE TABLE docs (
    id    UUID PRIMARY KEY DEFAULT UUID(),
    tags  ARRAY<STRING>
);

INSERT INTO docs (tags) VALUES (ARRAY('sql', 'search'));

SELECT ELEMENT_AT(tags, 1), CARDINALITY(tags)
FROM docs
WHERE ARRAY_CONTAINS(tags, 'sql');
```

`ELEMENT_AT(arr, i)` is 1-based. `ARRAY_LENGTH` is an alias of `CARDINALITY`.

## MAP

Orderable scalar keys, any value type. Entries are stored in canonical key order, so two maps with the same entries compare equal regardless of insertion order. Duplicate keys are rejected.

```sql
CREATE TABLE settings (
    id    UUID PRIMARY KEY DEFAULT UUID(),
    flags MAP<STRING, BOOL>
);

INSERT INTO settings (flags)
VALUES (MAP('dark', TRUE, 'compact', FALSE));

SELECT ELEMENT_AT(flags, 'dark'), MAP_KEYS(flags), MAP_SIZE(flags)
FROM settings
WHERE MAP_CONTAINS_KEY(flags, 'dark');
```

## Not in this version

Implicit subscript sugar (`arr[1]`, `map['k']`), `ARRAY_AGG` / `MAP_AGG`, and `UNNEST` are deferred.

Engine note: [`docs/design-collections.md`](https://github.com/bzync/nextsql/blob/main/docs/design-collections.md).
