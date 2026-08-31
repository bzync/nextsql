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
SELECT VECTOR_DIM(embedding), VECTOR_NORM(embedding), VECTOR_NORMALIZE(embedding)
FROM documents;
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

## Value operations

`COSINE`, `COSINE_DISTANCE`, `L2`, `L1`/`MANHATTAN`, and
`INNER_PRODUCT`/`DOT` compare equal-dimension vectors. `VECTOR_DIM`,
`VECTOR_NORM`, and `VECTOR_NORMALIZE` inspect or normalize a value.
`VECTOR_ADD`, `VECTOR_SUBTRACT`, and `VECTOR_SCALE` provide bounded algebra.
Zero-vector normalization and non-finite arithmetic output fail closed.

## Quantised elements

`VECTOR<F16,N>` stores each element as an IEEE 754 half (2 bytes) — half the
on-disk payload — while the value, every distance/algebra/`NEAREST` path, and
HNSW stay `float32` (widened on read). Writes quantise at the boundary
(round-to-nearest, ties to even), so reads match what is stored. Use it when
~0.1% per-element error is acceptable; keep `F32` for exact values.

`VECTOR<I8,N>` stores each element as a signed byte plus a per-vector `float32`
scale (`absmax(v)/127`) — roughly a quarter of the on-disk payload at high
dimension, again widened to `float32` for every path. Quantisation error is
larger than `F16`, so validate recall for your embedding model. A zero vector
round-trips exactly.

There is no implicit conversion between element types; a `NEAREST` query vector
stays full precision.

## BITVECTOR&lt;N&gt;

`BITVECTOR<N>` is a distinct top-level type that stores `N` single-bit elements
as `ceil(N/8)` packed bytes — one thirty-second of `VECTOR<F32,N>`. Every element
written must be exactly `0` or `1` (a real-valued vector is rejected, not
rounded); on read each widens back to a `float32` `0`/`1`.

The natural distance is `HAMMING` — the number of differing bits — which is the
default and the only metric a `BITVECTOR` column accepts (`USING COSINE | L2 |
INNER_PRODUCT` is rejected on one, and `USING HAMMING` is rejected on any other
vector column). `CREATE VECTOR INDEX … USING HNSW` builds a Hamming graph;
`WITH (QUANTIZATION = …)` is rejected because the payload is already one bit per
element.

```sql
CREATE TABLE docs (id UUID PRIMARY KEY DEFAULT UUID(), sig BITVECTOR<256>);
SELECT id FROM docs NEAREST sig TO $probe LIMIT 10;  -- HAMMING
```

## Quantised HNSW index

`CREATE VECTOR INDEX … USING HNSW WITH (QUANTIZATION = 'F16' | 'I8' | 'NONE')`
builds a graph that traverses on a compact quantised copy of each vector and
re-ranks the final candidates against the full-precision column payloads, so the
result order and distances are exact and recall tracks an unquantised graph. The
traversal encoding is independent of the column type — an `I8`-quantised index
over a `VECTOR<F32,N>` column keeps the column exact. `NONE` (the default) reads
the payloads directly. The win is smaller, cache-local traversal reads; the
quantised copies are additive on disk.

`nextsql-bench --vecquant` seeds the same vectors into an `F32`, an `F16`, and an
`I8` column, an `F32` column with an `F16`/`I8`-quantised HNSW graph, an `F32`
column with an IVF and an IVF-PQ index, then a `SPARSEVECTOR` inverted index on a
high-dimension, low-nnz corpus (`--vecquant-sparse-dim` / `--vecquant-sparse-nnz`).
It reports size, index build time, `NEAREST` p50/p95/p99, QPS, heap, and
recall@10/@100. On the 2026-08-31 2000 × 128-d reference run the database
shrinks 3.2 → 2.2 → 1.7 MiB across the element types with negligible recall loss
(F32/F16 recall@10 0.916, I8 0.914); runtime is `float32` everywhere. The IVF
row builds an order of magnitude smaller index ~10× faster than HNSW, trading
recall at a partial probe ratio. The `SPARSE` row (2000 × 4096-d, 24 non-zeros)
keeps 282 KiB of NSSV payload (a dense F32 of the same ambient dim would be
32 MiB) with recall@10/@100 1.000 at 0.53 ms p50. Phase 23 is production-gated;
the dated review is [`docs/vector.md`](https://github.com/bzync/nextsql/blob/main/docs/vector.md)
"Production-gating sign-off (Phase 23)".

## Compressed neighbour lists

Each HNSW node record front-codes its neighbour lists (node format v2): the keys
in a layer are sorted and stored as a shared-prefix length plus the differing
suffix, with varint counts and lengths. Primary keys in one table share a
prefix, so the on-disk graph is roughly a third smaller than the earlier
fixed-width encoding — with no change to the neighbours returned, recall, or
latency. Existing v1 graphs still load.

## IVF index

```sql
CREATE VECTOR INDEX docs_embedding ON documents (embedding)
    USING IVF WITH (LISTS = 256, PROBES = 16);
```

The inverted-file coarse-quantiser index trains `LISTS` centroids by
deterministic k-means over a heap sample, one primary-key posting list per
centroid. A `NEAREST` query ranks the centroids, probes the `PROBES` nearest
lists (default ≈ 10 % of `LISTS`, capped at `LISTS`), and scores their vectors
exactly, so recall rises with `PROBES` and is exact at `PROBES = LISTS`.
`EXPLAIN` shows `Nearest … ivf`. `REBUILD INDEX` retrains the quantiser;
`INSERT` / `UPDATE` / `DELETE` keep the posting lists current. Centroids and
lists live in the index's own detached, encrypted B+Tree (a wide centroid set is
split across several records). `nextsql-bench --vecquant` includes an IVF row.

A committed `NEAREST` is served from a process-local in-memory copy of the
quantiser — centroids, posting lists, and vectors held in memory, shared by every
session, built at commit or lazily on first search and evicted on any write to
the index — so a query does not reload the index tree each time. This is the same
generation-tracked cache the HNSW graph uses.

IVF is real-valued only (`COSINE` / `L2` / `INNER_PRODUCT`) and is not available
on partitioned tables or `BITVECTOR` columns.

## IVF-PQ

```sql
CREATE VECTOR INDEX ix_docs_emb ON docs (embedding)
    USING IVFPQ WITH (LISTS = 256, PROBES = 16, SUBSPACES = 8);
```

IVF-PQ adds product quantisation on top of IVF: a posting list stores an
`M`-byte code for each vector (the product-quantisation code of its residual from
the coarse centroid) instead of pointing at a full vector. A search scores each
probed list with asymmetric distance computation — a per-subspace query table
summed over the code bytes — and re-ranks the final candidates against the
full-precision payloads, so recall tracks an unquantised IVF. `COSINE` and `L2`
only.

- `SUBSPACES` (the subspace count `M`) is required and must divide the vector
  dimension (≤ 128). `LISTS` is required; `PROBES` defaults to ≈ 10 % of `LISTS`.
- Not available on partitioned tables or `BITVECTOR` columns; `WITH
  (QUANTIZATION = …)` is not an IVFPQ option.
- Its own detached, encrypted index tree (coarse centroids, a chunked codebook,
  one front-coded posting list per centroid); `INSERT` / `UPDATE` / `DELETE`
  maintain it incrementally and `REBUILD INDEX` retrains it. No process-local
  cached copy yet — a committed query reloads the quantiser per query.
- Catalog table descriptor format **v8**.

The portable core (`TrainIVFPQ`, `AddIVFPQ`, `RemoveIVFPQ`, `SearchIVFPQ`) lives
in `internal/vector`.

## Sparse retrieval

```sql
CREATE TABLE docs (id UUID PRIMARY KEY DEFAULT UUID(), emb SPARSEVECTOR<8>);
INSERT INTO docs (emb) VALUES ((1, 0, 0.5, 0, 0, 0, 0, 0));
CREATE VECTOR INDEX ix_emb ON docs (emb) USING SPARSE;
SELECT id FROM docs NEAREST emb TO (1, 0, 0.5, 0, 0, 0, 0, 0) LIMIT 10;
```

`SPARSEVECTOR<N>` stores only non-zero `(index, weight)` pairs (`NSSV`).
`CREATE VECTOR INDEX … USING SPARSE` builds an inverted index (one posting list
per dimension, `NSSP`) and ranks with exact inner-product accumulation,
optional `COSINE` re-rank. SQL `N` is 1…65535; the portable core allows ambient
dimension up to `2^24` with ≤ `2^16` non-zeros. Dense literals such as
`(1, 0, 0.5, 0)` are coerced by dropping zeros. Not available on partitioned
tables or dense/`BITVECTOR` columns. `nextsql-bench --vecquant` includes a
`SPARSE` size/latency/recall row on a high-dimension, low-nnz corpus.

A second `NEAREST` clause fuses a dense `VECTOR` column with a `SPARSEVECTOR`
column, optionally with `SEARCH` for BM25. The engine unions candidates from
each retriever and reciprocal-rank fuses them (`EXPLAIN`:
`Rerank bm25+vector+sparse fusion`). At most two `NEAREST` clauses.

## Limits

- Dimension `1…8192` (dense / bit vectors); `SPARSEVECTOR<N>` SQL `N` is 1…65535 (portable core allows 2^24 with ≤ 2^16 non-zeros)
- Elements must be finite (`NaN` / `Inf` fail closed); `F16` magnitude ≤ 65504
- Query dimension must match the column
- IVF / IVF-PQ `LISTS` ≤ 65 536, IVF-PQ `SUBSPACES` ≤ 128; IVF / IVF-PQ / SPARSE are not available on partitioned tables

Engine note: [`docs/vector.md`](https://github.com/bzync/nextsql/blob/main/docs/vector.md).
