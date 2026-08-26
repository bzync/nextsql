# Hybrid queries

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

`SEARCH` and `NEAREST` may be combined with `INNER JOIN` when the rank column belongs to the `FROM` table. The `FROM` table is ranked first, then joined. A rank column on a joined table is rejected. Outer join + `SEARCH` / `NEAREST` is not supported.

Hybrid results are reciprocal-rank fused, then truncated to `LIMIT` (or re-sorted when `ORDER BY` is present).

Engine note: [`docs/optimizer.md`](https://github.com/bzync/nextsql/blob/main/docs/optimizer.md).
