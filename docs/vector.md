# Native vectors (Phase 11)

`VECTOR<F32,N>` is a first-class column type. Large embeddings are not inlined into heap pages. Distance functions, exact flat search, and HNSW all use the same WAL, MVCC, and page encryption as the rest of the engine.

## SQL

```sql
CREATE TABLE documents (
    id        UUID PRIMARY KEY DEFAULT UUID(),
    name      STRING NOT NULL,
    embedding VECTOR<F32,1536>
);

INSERT INTO documents (name, embedding) VALUES ('one', (1, 0, 0));

CREATE VECTOR INDEX docs_embedding ON documents (embedding) USING HNSW;

SELECT name FROM documents NEAREST embedding TO $query LIMIT 20;
SELECT name FROM documents NEAREST embedding TO (1, 0, 0) USING L2 LIMIT 5;
SELECT name, COSINE(embedding, (1, 0, 0)) FROM documents;
```

`CREATE VECTOR INDEX` requires one `VECTOR<F32,N>` column and `USING HNSW`. It cannot be `UNIQUE` and cannot use a JSON path.

`NEAREST col TO <vector>` is a `SELECT` clause (after `WHERE` / `GROUP BY` / `SEARCH`, before `LIMIT`). The query is a vector literal or a parameter. Optional `USING COSINE | L2 | INNER_PRODUCT` selects the ranking metric (default `COSINE`).

`NEAREST` may be combined with `INNER JOIN` when the nearest column belongs to the `FROM` table. The plan ranks that table first (`Nearest` / `Rerank`), then inner-joins the ranked stream. Rank order is preserved through a 1:1 join; a 1:N join duplicates ranks. A nearest column on a joined table is rejected. Outer join + `NEAREST` is not supported. The engine does not insert an implicit `ORDER BY`; an explicit `ORDER BY` re-sorts after ranking. Hybrid `WHERE`+`SEARCH`+`NEAREST` on the `FROM` table is allowed and still one hybrid plan.

`SEARCH` + `NEAREST` (with or without `WHERE`) is planned as one hybrid problem. The optimizer chooses structured-filter-then-ANN or ANN-then-structured-filter from the unified cost model. Candidate rows are fused with reciprocal rank fusion (BM25 rank + vector rank, `k = 60`) and truncated to `LIMIT`. `EXPLAIN` shows `Candidates` and `Rerank bm25+vector`. See `docs/optimizer.md`.

Without a vector index, `NEAREST` is exact flat search over the vector store (or the rows produced by `WHERE`). `EXPLAIN` shows `Nearest … hnsw` when the graph is used, or `Nearest … flat` otherwise.

Functions:

| Function | Value |
|---|---|
| `COSINE(a, b)` | cosine similarity in `[-1, 1]` |
| `L2(a, b)` | Euclidean distance |
| `INNER_PRODUCT(a, b)` | dot product |

`NEAREST` ranks by lower-is-closer distance: cosine distance `1 − similarity`, L2, and `−dot` for inner product. Results are ordered by that distance, then primary key.

## Limits

Dimension must be `1…8192`. Elements must be finite (`NaN` / `Inf` fail closed). Query dimension must match the column.

## Storage

Row store holds a compact reference (`u16` dim + detached flag). The payload lives in a detached B+Tree per table (`Table.VecMeta`):

| Kind | Key | Value |
|---|---|---|
| payload | `0x01` + column + primary key | `NSVV` version, dim, packed `f32`s |

`NULL` produces no payload. Insert, update, delete, `CREATE INDEX` on existing rows, and rollback maintain the store with the same transaction as the heap.

HNSW lives in the index B+Tree:

| Kind | Key | Value |
|---|---|---|
| meta | `0x00` | `NSHM` dim, metric, `M`, `efConstruction`, count, entry |
| node | `0x01` + primary key | level, tombstone, neighbor lists |

Default construction: `M = 16`, `efConstruction = 64`, level from a hash of the primary key (`1 / ln(M)`). Search uses `ef ≥ k` and never lowers `k` to improve latency. Deletes tombstone the vertex.

`CREATE VECTOR INDEX` builds the graph in memory, then persists vertices to the
encrypted index tree. Committed searches use a process-local copy of that
graph (same HNSW, no silent `ef`/`k` reduction). A transaction that updates
the index searches the durable tree so MVCC/rollback stay correct. Commit of
those writes bumps a generation and drops the cache.

### Tombstone maintenance

HNSW deletion intentionally tombstones nodes; in-place edge surgery is not used
because it can silently damage reachability and recall. `InspectTombstones`
counts physical live/deleted vertices and fails closed if the live count differs
from durable metadata. The default policy recommends a blocking rebuild once a
graph has at least 1,024 deleted vertices and tombstones are at least 20% of all
vertices. Operators then run `REBUILD INDEX index_name`, which reconstructs the
graph from the heap snapshot and atomically replaces the old tree. Small graphs
may be rebuilt manually; `ONLINE` rebuild remains unsupported.

## Distances

Portable Go only. SIMD / `unsafe` is not used. `TestPortableProductionPath` rejects production vector imports of `unsafe`/cgo and assembly files; architecture-specific acceleration requires profiling, isolation, tests, fuzzing, and a measured win before that policy changes.

Flat search can split candidates across `internal/scheduler.Pool`. HNSW graph updates stay on the writer transaction.

## Encryption

Payloads and HNSW nodes are ordinary table pages. Production files, WAL, and UNDO stay encrypted. Distinctive `NSVV` / `NSHM` headers must not appear as readable plaintext on disk.

## Recall

HNSW is approximate. Official benches report `recall@10` and `recall@100` against exact flat search together with latency. Latency must not be improved by silently reducing recall.

Measured on this host (Ryzen 5 7535HS, linux/amd64, portable Go, no SIMD): in-memory HNSW, 400 vectors × 16-d, 20 queries — `recall@10 = 1.000`, `recall@100 = 1.000`. Search bench: 2000 vectors × 32-d top-10 ≈ 1.89 ms/op. Official QPS / RAM / index-size stay on a longer `nextsql-bench --workload vector` run (`docs/ops.md`).

## Later (hooks only)

`VECTOR<F16,N>`, `VECTOR<I8,N>`, `BITVECTOR<N>`, IVF, IVF-PQ, quantization.
