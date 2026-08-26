# Vectors

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

## NEAREST

`NEAREST col TO <vector>` sits after `WHERE` / `GROUP BY` / `SEARCH` and before `LIMIT`. Optional `USING COSINE | L2 | INNER_PRODUCT` (default `COSINE`).

`NEAREST` ranks by lower-is-closer distance:

| Metric | Distance |
|---|---|
| `COSINE` | `1 − similarity` |
| `L2` | Euclidean |
| `INNER_PRODUCT` | `−dot` |

Without a vector index, search is exact flat. With `USING HNSW`, `EXPLAIN` shows `Nearest … hnsw`. Default construction: `M = 16`, `efConstruction = 64`. Search never silently lowers `k` to improve latency.

## Limits

- Dimension `1…8192`
- Elements must be finite (`NaN` / `Inf` fail closed)
- Query dimension must match the column
- `VECTOR<F16,N>`, `VECTOR<I8,N>`, IVF / IVF-PQ are **not** implemented

Engine note: [`docs/vector.md`](https://github.com/bzync/nextsql/blob/main/docs/vector.md).
