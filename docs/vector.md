# Native vectors (Phase 11)

`VECTOR<F32,N>` is a first-class column type. Large embeddings are not inlined into heap pages. Distance functions, exact flat search, and HNSW all use the same WAL, MVCC, and page encryption as the rest of the engine.

## Quantised element types (Phase 23)

`VECTOR<F16,N>` stores each element as an IEEE 754 half (2 bytes) instead of a
32-bit float, halving the vector payload store on disk. The in-memory value and
every distance, algebra, `NEAREST`, and HNSW path stay `float32`: a half column
is transparently widened on read.

```sql
CREATE TABLE docs (id UUID PRIMARY KEY DEFAULT UUID(), emb VECTOR<F16,1536>);
```

Writes are quantised at the boundary — the value stored and read back is the
half-precision round-trip of the inserted vector (round-to-nearest, ties to
even), so reads are consistent with what is on disk. Half precision keeps ~3
decimal significant digits and a magnitude range of ±65504; a finite element
outside that range is rejected as non-finite before it is stored. Choose
`F16` when the embedding model tolerates ~0.1% per-element error (most do);
keep `F32` when exact values matter.

`VECTOR<I8,N>` stores each element as a signed byte with a per-vector `float32`
scale (`absmax(v) / 127`, symmetric, so the `-128` code is never produced). One
quantised vector is `4 + N` bytes on disk versus `4N` for `F32` — roughly a
quarter of the payload store at high dimension.

```sql
CREATE TABLE docs (id UUID PRIMARY KEY DEFAULT UUID(), emb VECTOR<I8,1536>);
```

The scale is derived from each vector independently at write time, so there is
no catalog-side calibration or data scan. Per-element error is bounded by half a
quantisation step (`scale / 2`); expect noticeably more distortion than `F16`,
so validate recall for your embedding model before choosing `I8`. A zero vector
round-trips exactly.

`CREATE VECTOR INDEX … USING HNSW` works on an `F16` or `I8` column unchanged —
the graph reads widened `float32` payloads, so recall behaves as for `F32` minus
the quantisation noise. There is no implicit conversion between element types; a
query vector passed to `NEAREST` stays full precision and is compared against the
widened stored vectors.

### BITVECTOR&lt;N&gt; (Phase 23)

`BITVECTOR<N>` stores each of the `N` elements as a single bit — `ceil(N/8)`
bytes on disk, one thirty-second of `VECTOR<F32,N>`. It is a distinct top-level
type, not a `VECTOR<...>` element:

```sql
CREATE TABLE docs (id UUID PRIMARY KEY DEFAULT UUID(), sig BITVECTOR<256>);
INSERT INTO docs (sig) VALUES ((1, 0, 1, 1, 0, /* … 256 values … */));
```

Every element written must be exactly `0` or `1`; a real-valued vector is
rejected, never rounded. On read each element widens back to a `float32` `0` or
`1`, so distance and HNSW math stay `float32` like the other types.

The natural distance is **HAMMING** — the number of differing bits. It is the
default for a `BITVECTOR` column and the only metric allowed for one:

```sql
SELECT id FROM docs NEAREST sig TO $probe LIMIT 10;                -- HAMMING
SELECT id FROM docs NEAREST sig TO $probe USING HAMMING LIMIT 10;  -- explicit
```

`USING COSINE | L2 | INNER_PRODUCT` is rejected on a `BITVECTOR` column, and
`USING HAMMING` is rejected on any other vector column. `CREATE VECTOR INDEX …
USING HNSW` builds a Hamming graph over a `BITVECTOR` column; `WITH
(QUANTIZATION = …)` is rejected (the payload is already one bit per element).

`SPARSEVECTOR<N>` stores only the non-zero coordinates of an `N`-dimensional
vector (see "Sparse retrieval" below). `CREATE VECTOR INDEX … USING SPARSE`
builds an inverted index over those coordinates. Dense literals such as
`(1, 0, 0.5, 0)` are coerced by dropping zeros. `USING HNSW` / `IVF` / `IVFPQ`
are rejected on a sparse column; `USING SPARSE` is rejected on a dense or
`BITVECTOR` column. Default ranking is `COSINE`; `INNER_PRODUCT` is also
accepted; `L2` and `HAMMING` are rejected.

### Quantised HNSW index

`CREATE VECTOR INDEX … USING HNSW WITH (QUANTIZATION = 'F16' | 'I8' | 'NONE')`
builds an HNSW graph that keeps a compact quantised copy of every vector beside
the graph nodes and uses it for all graph-traversal distance computations. The
final `k` candidates are then re-ranked against the full-precision column
payloads, so the reported ordering and distances are exact and recall stays
close to an unquantised graph. `NONE` (the default) reads the column payloads
directly during traversal.

The traversal encoding is independent of the column's element type: an
`I8`-quantised index over a `VECTOR<F32,N>` column keeps the column full
precision (`SELECT` returns the exact stored value) while the graph traverses on
signed bytes. `REBUILD INDEX` rebuilds the quantised store; a row inserted or
updated after the build is quantised into the graph on write. The quantised
store is encrypted like every other index structure and is recovered from the
WAL and base backups with the rest of the index.

This trades a small on-disk increase (the quantised copies sit alongside the
full payloads) for smaller, more cache-local traversal reads and a bounded
re-rank cost. Dropping the full payload where re-rank is not required is future
work.

### Compressed neighbour lists

Every HNSW node record front-codes its neighbour lists (node format v2). A
layer's neighbour keys carry no meaningful order in the graph, so they are
sorted ascending and each key is stored as the shared-prefix length with the
previous key plus the differing suffix; counts and lengths are varints rather
than fixed 16-bit fields. Row primary keys in one table share a column prefix
and, in a dense id space, several leading bytes, so this shrinks the on-disk
graph by roughly a third with no effect on traversal, recall, or latency (the
lists decode to the same neighbour set). Existing v1 node records still decode;
`REBUILD INDEX` and ordinary writes re-emit v2.

### Size / recall comparison

`nextsql-bench --vecquant` seeds the same vector set into an `F32`, an `F16`, and
an `I8` column, builds an HNSW index over each, then repeats the measurement for
an `F32` column with an `F16`- and an `I8`-quantised HNSW graph, for an `F32`
column with an IVF index and with an IVF-PQ index, and for a `SPARSEVECTOR`
inverted index on a high-dimension, low-nnz corpus (independent of
`--vecquant-dim`; `--vecquant-sparse-dim` / `--vecquant-sparse-nnz`). It reports
payload/index/database size, build
time, resident heap, mean quantisation error, and `NEAREST` latency +
recall@10/@100. Dense-row recall is scored against an exact-cosine flat search
over the full-precision source vectors, so the gap between `F32` and `F16`/`I8`
is the quantisation penalty alone (the HNSW approximation is common to every
HNSW row). The `SPARSE` row is scored against exact-cosine `SparseFlat`.

Production-gating reference run (2026-08-31, linux/amd64, 12 vCPU, ext4,
encryption + WAL + fsync on; 2000 × 128-d, 64 queries). Size:

| config | col B/elem | raw payload | index build Δ | database | build | heap | quant err |
| ------ | --------: | ----------: | ------------: | -------: | ----: | ---: | --------: |
| `F32` | 4 | 1000 KiB | 707 KiB | 3.2 MiB | 3.00 s | 82 MiB | 0.00000 |
| `F16` | 2 | 500 KiB | 707 KiB | 2.2 MiB | 2.55 s | 82 MiB | 0.00022 |
| `I8` | 1 | 258 KiB | 691 KiB | 1.7 MiB | 2.30 s | 82 MiB | 0.00390 |
| `F32` + qh-`F16` | 4 | 1000 KiB | 1.6 MiB | 4.0 MiB | 2.35 s | 83 MiB | 0.00022 |
| `F32` + qh-`I8` | 4 | 1000 KiB | 1.2 MiB | 3.6 MiB | 2.36 s | 83 MiB | 0.00390 |
| `F32` + IVF | 4 | 1000 KiB | 112 KiB | 2.6 MiB | 0.26 s | 80 MiB | 0.00000 |
| `F32` + IVF-PQ | 4 | 1000 KiB | 321 KiB | 2.8 MiB | 2.04 s | 79 MiB | 0.00000 |

Query (same run; recall vs exact-cosine flat over the F32 source vectors):

| config | p50 | p95 | p99 | QPS | recall@10 | recall@100 |
| ------ | --: | --: | --: | --: | --------: | ---------: |
| `F32` | 2.22 ms | 2.79 ms | 3.36 ms | 444 | 0.916 | 0.939 |
| `F16` | 1.76 ms | 4.09 ms | 4.61 ms | 494 | 0.916 | 0.939 |
| `I8` | 1.82 ms | 2.71 ms | 3.50 ms | 516 | 0.914 | 0.940 |
| `F32` + qh-`F16` | 1.93 ms | 2.87 ms | 3.42 ms | 488 | 0.916 | 0.939 |
| `F32` + qh-`I8` | 1.95 ms | 3.21 ms | 3.72 ms | 485 | 0.912 | 0.939 |
| `F32` + IVF | 0.76 ms | 1.07 ms | 1.10 ms | 1277 | 0.619 | 0.514 |
| `F32` + IVF-PQ | 1.48 ms | 2.24 ms | 2.34 ms | 628 | 0.495 | 0.502 |

The `SPARSE` row uses a different corpus (2000 × 4096-d, 24 non-zeros, 64
queries; `--vecquant-sparse-dim` / `--vecquant-sparse-nnz`) so it is not
comparable to the 128-d dense rows above:

| config | nnz | raw payload | index build Δ | database | build | heap | p50 | p95 | p99 | QPS | recall@10 | recall@100 |
| ------ | --: | ----------: | ------------: | -------: | ----: | ---: | --: | --: | --: | --: | --------: | ---------: |
| `SPARSE` | 24 | 282 KiB | 1.0 MiB | 2.1 MiB | 1.28 s | 89 MiB | 0.53 ms | 0.94 ms | 1.09 ms | 1643 | 1.000 | 1.000 |

A dense `VECTOR<F32,4096>` of the same ambient dimension would store 32 MiB of
payload; the `NSSV` encoding keeps only the 24 non-zeros (282 KiB). The inverted
index is exact for inner product; default SQL `COSINE` re-ranks the top `4·k`
payloads, and on this corpus that recovers the exact cosine ranking
(recall@10/@100 = 1.000) at sub-millisecond p50.

The `IVF` row builds a coarse-quantiser index (`LISTS = 2·√rows`, `PROBES =
LISTS/4` — 88 / 22 at 2000 rows) over the full-precision `F32` column: no vector
quantisation, so the on-disk element width and quantisation error are the
column's. Its index is an order of magnitude smaller than an HNSW graph (only
centroids + posting lists) and it builds ~10× faster, but at this probe ratio it
scores far fewer candidates than HNSW visits, so both recall and latency are
worse. Uniformly random unit vectors — the benchmark's synthetic data — are close
to a worst case for a coarse quantiser; recall on clustered real embeddings at
the same probe ratio is materially higher, and rises toward 1.0 as `PROBES`
approaches `LISTS`.

The `IVF-PQ` row uses the same `LISTS` / `PROBES` plus `SUBSPACES = 16`: the
posting lists store a 16-byte product-quantisation code per vector instead of
pointing at a full vector, and search ADC-scores the codes then re-ranks the top
candidates exactly against the payload store. On this synthetic data PQ residual
codes add error on top of the coarse quantiser, so recall is below plain IVF;
the codebook build makes it slower than IVF but still faster than HNSW, and the
index stays far smaller than an HNSW graph. As with IVF, recall on real
clustered embeddings and higher `PROBES` is materially better.

`F16` and `I8` **columns** halve and quarter the payload store; the total
database shrinks accordingly. The HNSW graph itself holds only neighbour links,
front-coded on disk (node format v2) — the index build delta above is roughly a
third smaller than it was with the fixed-width v1 node encoding, with recall and
latency unchanged.

A **quantised HNSW graph** (`qh-*`) keeps a compact quantised copy of every
vector beside the graph nodes, so the index-build delta grows rather than
shrinks — the quantised copies are additive to the full payloads that re-rank
still needs. Re-rank against those full payloads keeps recall at the `F32` level
(`qh-F16`) or within a point of it (`qh-I8`). Runtime is `float32` everywhere, so
build time, latency, QPS, and heap stay within noise. The remaining lever for a
smaller quantised index is dropping the full payload where re-rank is not
required, which is future work. Validate recall against a real embedding model
before choosing `I8` in either place.

### IVF index

```sql
CREATE VECTOR INDEX docs_embedding ON documents (embedding)
    USING IVF WITH (LISTS = 256, PROBES = 16);
```

The inverted-file (IVF) coarse-quantiser index trains `LISTS` centroids by
deterministic k-means++ over a sample of the column (unit-normalised first for
`COSINE`), then assigns every vector to its nearest centroid's **posting list**.
A `NEAREST` query ranks the centroids against the query vector, probes the
`PROBES` nearest lists, and scores every vector in them exactly, so recall rises
with `PROBES` and reaches 1.0 when every list is probed (`PROBES = LISTS`).

- `LISTS` is required, capped at 65 536. `PROBES` is optional (default ≈ 10 % of
  `LISTS`, at least 1) and may not exceed `LISTS`.
- One centroid must fit a B+Tree leaf record (~½ a logical page), so IVF is
  rejected at binding time for very high-dimensional columns (roughly `N > 2000`
  for `VECTOR<F32,N>`); use `USING HNSW` there.
- The coarse quantiser is real-valued only (`COSINE` / `L2` / `INNER_PRODUCT`);
  IVF is rejected on a `BITVECTOR` column and `WITH (QUANTIZATION = …)` is not a
  valid IVF option.
- Centroids, the `NSIV` header, and the front-coded posting lists (`NSIL`, the
  same varint-count + shared-prefix + suffix scheme as HNSW neighbour lists) live
  in the index's own detached, encrypted B+Tree, so IVF inherits WAL, backup,
  PITR, and Raft durability like every other index. A B+Tree leaf record holds
  roughly half a logical page, so a wide centroid set (many `LISTS`, high
  dimension) is split across several centroid-group records transparently.
  `REBUILD INDEX` retrains and rewrites the quantiser; `INSERT` / `UPDATE` /
  `DELETE` move a row's primary key between posting lists.
- A committed `NEAREST` query is served from a **process-local copy** of the
  quantiser (centroids, posting lists, and full-precision vectors held in memory),
  built once at commit time or lazily on first search and shared by every session,
  so a query does not reload and decrypt the index tree each time. The copy is
  evicted when the index is mutated, rebuilt, dropped, or replaced by a replicated
  apply; a transaction that has modified the index reads its own uncommitted state
  directly from the index tree instead. This is the same generation-tracked cache
  the HNSW graph uses.
- `nextsql-bench --vecquant` includes an IVF row (see "Size / recall comparison"
  above). IVF is not yet supported on partitioned tables.

The portable core (`TrainIVF`, `AddIVF`, `RemoveIVF`, `SearchIVF`, the `IVFStore`
interface, the `NSIV` / `NSIC` / `NSIL` encodings) lives in `internal/vector`.

### IVF-PQ (product quantisation)

```sql
CREATE VECTOR INDEX ix_docs_emb ON documents (embedding)
    USING IVFPQ WITH (LISTS = 256, PROBES = 16, SUBSPACES = 8);
```

IVF-PQ extends the IVF coarse quantiser with a compact code for every vector, so
a posting list stores an `M`-byte code instead of pointing at a full-precision
vector. A vector assigned to coarse centroid `c` is stored as the
product-quantisation code of its residual `r = v − c`: `r` is split into `M`
equal sub-vectors and each is replaced by the 1-byte index of its nearest entry
in a per-subspace codebook of up to 256 sub-centroids. A search ranks the coarse
centroids and, for each probed list, scores its entries with **asymmetric
distance computation (ADC)** — a per-subspace query-to-sub-centroid distance table
summed over the `M` code bytes — without touching the full vectors. When the
caller can still supply the full-precision payloads (as the executor can, from
the column's payload store) the final candidates are re-ranked exactly, so recall
tracks an unquantised IVF; without them the ADC ranking stands.

- `SUBSPACES` (the subspace count `M`) is required, must divide the column
  dimension, and is capped at 128. The codebook is trained by deterministic
  k-means over the residuals of the training sample.
- `LISTS` is required (capped at 65 536); `PROBES` is optional (default ≈ 10 % of
  `LISTS`, at least 1) and may not exceed `LISTS`.
- The residual formulation is Euclidean, so IVF-PQ supports `COSINE` (unit-
  normalised first) and `L2`; `INNER_PRODUCT` is rejected. `WITH (QUANTIZATION =
  …)` is not a valid IVFPQ option, and IVF-PQ is rejected on a `BITVECTOR` column
  and on partitioned tables (as plain IVF is).
- Encodings: `NSPQ` meta (dimension, metric, `LISTS`, `PROBES`, `M`, count),
  `NSPC` codebook (contiguous `f32` sub-centroids), and `NSPL` posting lists (the
  same front-coded primary-key scheme as `NSIL`, with the `M` code bytes appended
  to each entry). Every decoder bounds its varints before allocating.
- The executor stores the index in its own detached, encrypted B+Tree — coarse
  centroids grouped like IVF, the codebook split into fixed-size chunks under an
  `IVPCG` header (it never fits one leaf record), and one front-coded posting
  list per centroid — so IVF-PQ inherits WAL, backup, PITR, and Raft replication
  from the encrypted index-tree path. `INSERT` / `UPDATE` / `DELETE` maintain the
  posting lists incrementally; `REBUILD INDEX` retrains the quantiser and
  codebook from scratch. Rows inserted after a build on an empty table land in a
  single placeholder cell until the next `REBUILD INDEX`.
- `nextsql-bench --vecquant` includes an `F32/ivfpq` row (see "Size / recall
  comparison" above). There is no process-local cached IVF-PQ copy yet — a
  committed `NEAREST` reloads the quantiser from the index tree per query (a
  documented follow-on, matching plain IVF's first increment).

The catalog table descriptor is format **v9**. v8 stored one `SUBSPACES` `u32`
per index after the v7 ANN-method byte and IVF `LISTS` / `PROBES` counts
(`0` for HNSW, plain IVF, and non-vector indexes). v9 appends a per-index
full-text analyzer id + revision after that trailer.

The portable core (`TrainIVFPQ`, `AddIVFPQ`, `RemoveIVFPQ`, `SearchIVFPQ`, the
`IVFPQStore` interface and `PQCodebook`, the `NSPQ` / `NSPC` / `NSPL` encodings)
lives in `internal/vector`.

### Sparse retrieval

A sparse vector stores only its non-zero coordinates: a strictly ascending list
of dimension indices and a parallel list of finite, non-zero `float32` values.
Learned-sparse models (SPLADE-style term weights) and bag-of-words vectors are
almost entirely zero over a large vocabulary, so a dense `[]float32` would waste
both RAM and disk. The natural index is inverted: one posting list per dimension,
holding `(primary key, weight)` pairs. A search walks the posting lists of the
query's non-zero coordinates and accumulates the inner product for every document
that shares a term — exact, with no approximation and no missed vector.

- Dimension is a `u32` up to `2^24`; a single vector may hold at most `2^16`
  non-zeros. Zero values, duplicate or out-of-range indices, and non-finite
  weights are rejected rather than silently dropped.
- Distances: `INNER_PRODUCT` (`−dot`) and `COSINE` (`1 − cosine`). `L2` and
  `HAMMING` are not meaningful here and are rejected by the meta encoder.
  `COSINE` re-ranks the top `4·k` inner-product candidates against the
  full-precision sparse payloads when the store can supply them; without
  payloads the inner-product ranking stands.
- Encodings (all versioned, fail-closed): `NSSV` v1 (dimension, non-zero count,
  indices as ascending varint deltas, then little-endian `f32` values — a
  hostile overflowing delta is rejected before it can wrap to a smaller index),
  `NSSM` v1 (21-byte header: max dimension, metric, count), `NSSP` v1 (front-coded
  posting lists: varint count, then per entry a shared-prefix + suffix primary
  key plus the `f32` weight; key lengths bounded at 4096 **before** allocation).
- SQL: `SPARSEVECTOR<N>` (`N` is the ambient dimension, 1…65535 — the catalog
  type stores it in a `u16`; the portable core still allows `2^24`) plus
  `CREATE VECTOR INDEX … USING SPARSE`. Dense vector literals of length `N`
  are coerced by dropping zeros (a large-`N` insert uses a bound parameter).
  `COSINE` (default) and `INNER_PRODUCT` only; not on
  partitioned tables; `WITH (QUANTIZATION = …)` is rejected. The executor
  (`internal/executor/sparsestore.go`) implements `SparseStore` over a
  detached encrypted index tree (`NSSM` header + one `NSSP` posting list per
  dimension) and the shared `NSSV` payload store. `CREATE` / `REBUILD INDEX`
  stream the heap; `INSERT` / `UPDATE` / `DELETE` maintain posting lists from
  the in-memory old/new values; `NEAREST` is `SearchSparse` (exact inner
  product, optional COSINE re-rank). `EXPLAIN` labels the plan `sparse`.
  `nextsql export` emits `USING SPARSE`. No process-local cached copy.

The portable core (`NewSparseVec`, `SparseDot`, `AddSparse`, `RemoveSparse`,
`SearchSparse`, `SparseFlat`, the `SparseStore` interface, the `NSSV` / `NSSM` /
`NSSP` encodings) lives in `internal/vector`. Unit recall on 400 vectors ×
4096-d with 24 non-zeros: inner-product inverted-index `recall@10 = 1.000`;
`COSINE` with full re-rank `1.000`; `COSINE` with the default `4·k` re-rank
≥ 0.90.

`nextsql-bench --vecquant` includes a `SPARSE` row on a representative
high-dimension, low-nnz corpus (see "Size / recall comparison").

### Dense + sparse + BM25 fusion

A `SELECT` may name two `NEAREST` clauses — one dense `VECTOR` column and one
`SPARSEVECTOR` column — with an optional `SEARCH`. The optimizer unions
candidates from each retriever (HNSW/IVF/IVF-PQ, the sparse inverted index, and
BM25) and reciprocal-rank fuses the lists (`k = 60`). A document contributes
to a channel only when that retriever scored it, so a lexical miss does not
steal rank from a dense or sparse hit. `EXPLAIN` shows
`Rerank bm25+vector+sparse fusion` (or `vector+sparse fusion` without
`SEARCH`). At most two `NEAREST` clauses; a third is a syntax error. The pair
must be one dense vector and one sparse vector (`BITVECTOR` is rejected).
Existing `SEARCH` + single `NEAREST` hybrid plans are unchanged.

```sql
SELECT id, title FROM documents
SEARCH body FOR 'wireless headphones'
NEAREST embedding TO $dense
NEAREST sparse TO $sparse
LIMIT 20;
```

Measurable benefit (`TestDenseSparseBM25Fusion`): a four-row corpus where each
of BM25, dense ANN, and sparse retrieval uniquely owns one relevant row — each
single-channel `LIMIT 1` returns only its own hit; the fused `LIMIT 3` returns
all three.

## SQL

```sql
CREATE TABLE documents (
    id        UUID PRIMARY KEY DEFAULT UUID(),
    name      STRING NOT NULL,
    embedding VECTOR<F32,1536>,
    sparse    SPARSEVECTOR<8>
);

INSERT INTO documents (name, embedding, sparse) VALUES ('one', (1, 0, 0), (1, 0, 0.5, 0, 0, 0, 0, 0));

CREATE VECTOR INDEX docs_embedding ON documents (embedding) USING HNSW;
CREATE VECTOR INDEX docs_embedding ON documents (embedding) USING HNSW
    WITH (QUANTIZATION = 'I8');
CREATE VECTOR INDEX docs_embedding ON documents (embedding)
    USING IVF WITH (LISTS = 256, PROBES = 16);
CREATE VECTOR INDEX docs_sparse ON documents (sparse) USING SPARSE;

SELECT name FROM documents NEAREST embedding TO $query LIMIT 20;
SELECT name FROM documents NEAREST embedding TO (1, 0, 0) USING L2 LIMIT 5;
SELECT name, COSINE(embedding, (1, 0, 0)) FROM documents;
SELECT VECTOR_DIM(embedding), VECTOR_NORM(embedding), VECTOR_NORMALIZE(embedding)
FROM documents;
SELECT VECTOR_ADD(a, b), VECTOR_SUBTRACT(a, b), VECTOR_SCALE(a, 0.5)
FROM vector_pairs;
```

`CREATE VECTOR INDEX` requires one `VECTOR<F32,N>` / `<F16,N>` / `<I8,N>` / `BITVECTOR<N>` / `SPARSEVECTOR<N>` column and `USING HNSW`, `USING IVF`, `USING IVFPQ`, or `USING SPARSE`. It cannot be `UNIQUE` and cannot use a JSON path. For `USING HNSW`, optional `WITH (QUANTIZATION = 'F16' | 'I8' | 'NONE')` builds a quantised traversal graph with exact re-rank (see "Quantised HNSW index" above); `NONE` is the default and `WITH (QUANTIZATION = …)` is rejected on a `BITVECTOR` or `SPARSEVECTOR` column. For `USING IVF`, `WITH (LISTS = n [, PROBES = m])` is required; for `USING IVFPQ`, `WITH (LISTS = n, SUBSPACES = M [, PROBES = m])` is required (see "IVF index" and "IVF-PQ (product quantisation)" above). `USING SPARSE` requires a `SPARSEVECTOR` column and takes no `WITH` options.

`NEAREST col TO <vector>` is a `SELECT` clause (after `WHERE` / `GROUP BY` / `SEARCH`, before `LIMIT`). The query is a vector literal or a parameter. Optional `USING COSINE | L2 | INNER_PRODUCT | HAMMING` selects the ranking metric (default `COSINE`, or `HAMMING` for a `BITVECTOR` column). `HAMMING` is only valid on a `BITVECTOR` column, and a `BITVECTOR` column accepts only `HAMMING`. A `SPARSEVECTOR` column accepts `COSINE` (default) and `INNER_PRODUCT`; `L2` and `HAMMING` are rejected. A second `NEAREST` clause fuses a dense vector column with a `SPARSEVECTOR` column (see "Dense + sparse + BM25 fusion" above).

`NEAREST` may be combined with `INNER JOIN` when the nearest column belongs to the `FROM` table. The plan ranks that table first (`Nearest` / `Rerank`), then inner-joins the ranked stream. Rank order is preserved through a 1:1 join; a 1:N join duplicates ranks. A nearest column on a joined table is rejected. Outer join + `NEAREST` is not supported. The engine does not insert an implicit `ORDER BY`; an explicit `ORDER BY` re-sorts after ranking. Hybrid `WHERE`+`SEARCH`+`NEAREST` on the `FROM` table is allowed and still one hybrid plan.

`SEARCH` + `NEAREST` (with or without `WHERE`) is planned as one hybrid problem. The optimizer chooses structured-filter-then-ANN or ANN-then-structured-filter from the unified cost model. Candidate rows are fused with reciprocal rank fusion (BM25 rank + vector rank, `k = 60`) and truncated to `LIMIT`. `EXPLAIN` shows `Candidates` and `Rerank bm25+vector`. See `docs/optimizer.md`.

Without a vector index, `NEAREST` is exact flat search over the vector store (or the rows produced by `WHERE`). `EXPLAIN` shows `Nearest … hnsw` when the graph is used, `Nearest … ivf` /
`ivfpq` / `sparse` for those indexes, or `Nearest … flat` otherwise.

On a partitioned table, `NEAREST` (indexed or flat) searches every partition-local graph and merges hits by distance. When the residual `WHERE` predicate constrains the partition key, only the surviving partitions are searched and `EXPLAIN` appends `partitions=[…]` to the `Nearest` node; a predicate that does not touch the partition key leaves every partition in play, so ranking is unchanged. See `docs/partitioning.md`.

Functions:

| Function | Value |
|---|---|
| `COSINE(a, b)` | cosine similarity in `[-1, 1]` |
| `L2(a, b)` | Euclidean distance |
| `INNER_PRODUCT(a, b)` | dot product |
| `DOT(a, b)` / `VECTOR_DOT(a, b)` | dot-product aliases |
| `COSINE_DISTANCE(a, b)` | `1 − cosine similarity` |
| `L1(a, b)` / `MANHATTAN(a, b)` | Manhattan distance |
| `VECTOR_DIM(v)` | dimension |
| `VECTOR_NORM(v)` | Euclidean norm |
| `VECTOR_NORMALIZE(v)` | unit-length vector; zero norm is rejected |
| `VECTOR_ADD(a, b)` | element-wise sum |
| `VECTOR_SUBTRACT(a, b)` | element-wise difference |
| `VECTOR_SCALE(v, decimal)` | scalar product |

All binary operations require equal dimensions. Arithmetic output is checked
for finite `float32` values, so overflow fails closed instead of persisting
NaN/Inf. Aliases include `VECTOR_DIMS`/`DIMENSIONS`, `NORM`/`NORMALIZE`, and
`VECTOR_SUB`.

`NEAREST` ranks by lower-is-closer distance: cosine distance `1 − similarity`, L2, `−dot` for inner product, and the differing-bit count for Hamming. Results are ordered by that distance, then primary key.

## Limits

Dimension must be `1…8192`. Elements must be finite (`NaN` / `Inf` fail closed). Query dimension must match the column.

## Storage

Row store holds a compact reference (`u16` dim + detached flag). The payload lives in a detached B+Tree per table (`Table.VecMeta`):

| Kind | Key | Value |
|---|---|---|
| payload (F32) | `0x01` + column + primary key | `NSVV` v1, dim, packed `f32`s |
| payload (F16) | `0x01` + column + primary key | `NSVV` v2, element tag `F16`, dim, packed halves |
| payload (I8) | `0x01` + column + primary key | `NSVV` v2, element tag `I8`, dim, `f32` scale, packed signed bytes |
| payload (BIT) | `0x01` + column + primary key | `NSVV` v2, element tag `BIT`, dim, packed bits (`ceil(dim/8)` bytes, LSB-first) |
| payload (SPARSE) | `0x01` + column + primary key | `NSSV` v1: dim, nnz, delta-varint indices, little-endian `f32` values |

The payload is self-describing: `DecodePayload` reads the version byte, and for
`v2` the element tag, and widens half or signed-byte elements to `float32`.
Existing `v1` payloads keep decoding unchanged, so the format change is
backward compatible. `NULL` produces no
payload. Insert, update, delete, `CREATE INDEX` on existing rows, and rollback
maintain the store with the same transaction as the heap.

HNSW lives in the index B+Tree:

| Kind | Key | Value |
|---|---|---|
| meta | `0x00` | `NSHM` v1 dim, metric, `M`, `efConstruction`, count, entry; v2 appends the traversal-quantisation tag |
| node | `0x01` + primary key | level, tombstone, neighbour lists — v2 front-codes each layer (sorted keys, varint shared-prefix + suffix); v1 (fixed-width) still decodes |
| qvec | `0x02` + primary key | `NSVV` traversal vector (present only on a quantised index) |

Default construction: `M = 16`, `efConstruction = 64`, level from a hash of the primary key (`1 / ln(M)`). Search uses `ef ≥ k` and never lowers `k` to improve latency. Deletes tombstone the vertex.

An IVF index (`USING IVF`) uses its own detached, encrypted index tree:

| Kind | Key | Value |
|---|---|---|
| meta | `0x00` | `NSIV` v1: dim, metric, `LISTS`, `PROBES`, count, trained flag (25 bytes) |
| centroids | `0x01` | `IVFCG` group index (`"IVFCG"` + version + `u32` group count) when the centroid set is grouped; otherwise a single `NSIC` v1 block (legacy) |
| centroid group | `0x01` + `u32` group ordinal | `NSIC` v1: dim, group centroid count, packed `f32` centroids — each group encodes under the ~½-page leaf-record ceiling |
| posting | `0x02` + `u32` list ordinal | `NSIL` v1: front-coded primary keys (varint count, shared-prefix + suffix per key); an emptied list deletes its record |

An IVF-PQ index (`USING IVFPQ`) uses the same detached tree layout plus a chunked
codebook record:

| Kind | Key | Value |
|---|---|---|
| meta | `0x00` | `NSPQ` v1: dim, metric, `LISTS`, `PROBES`, `M`, count, trained flag (32 bytes) |
| centroids | `0x01` (+ `u32` group) | `IVFCG` group index / `NSIC` coarse centroids, grouped like IVF |
| codebook | `0x03` | `IVPCG` chunk index: `"IVPCG"` + version + `u32` chunk count + `u32` total length |
| codebook chunk | `0x03` + `u32` chunk ordinal | a slice of the encoded `NSPC` v1 block (`M`, sub-dimension, `Ksub`, then `M · Ksub` packed `f32` sub-centroids); reassembled before decode |
| posting | `0x02` + `u32` list ordinal | `NSPL` v1: front-coded primary keys, each followed by its `M` product-quantisation code bytes |

A sparse inverted index (`USING SPARSE`) uses its own detached, encrypted index
tree:

| Kind | Key | Value |
|---|---|---|
| meta | `0x00` | `NSSM` v1: max dimension, metric (`COSINE` / `INNER_PRODUCT`), count (21 bytes) |
| posting | `0x01` + `u32` dimension | `NSSP` v1: front-coded `(primary key, weight)` pairs (varint count, shared-prefix + suffix per key, then `f32` weight) |

Sparse vector payloads use `NSSV` v1 (dimension, non-zero count, delta-varint
indices, little-endian `f32` values) rather than the dense `NSVV` element tags;
the ambient dimension is a `u32` (up to `2^24`) and would not fit the `NSVV` v2
`u16` dimension field.

The catalog table descriptor is format **v8**: after the v6 per-index
traversal-quantisation byte and the v7 per-index vector-ANN-method byte plus IVF
`LISTS` / `PROBES` `u32` counts, it stores one IVF-PQ `SUBSPACES` `u32` per index
(all `0` for HNSW and non-vector indexes). `CREATE VECTOR INDEX … USING IVF` /
`USING IVFPQ` trains the quantiser (and, for IVF-PQ, the residual codebook) over
a deterministic sample of the heap (≤ 50 000 vectors), assigns every row to a
posting list, and writes the centroids, codebook, lists, and header in one
transaction; `REBUILD INDEX` retrains from scratch. A committed IVF search reads
a process-local copy of the quantiser under the same generation counter as the
HNSW graph cache below; IVF-PQ has no process-local copy yet and reloads its
quantiser from the index tree per query.

`WITH (QUANTIZATION = 'F16' | 'I8')` sets the v2 meta tag and writes one `qvec`
record per vertex — the column vector quantised to that encoding. Graph
traversal computes distances from `qvec`; `Search` then re-ranks the `ef`
candidates against the full-precision payload store and keeps the closest `k`,
so the result order and distances are exact. A tombstoned vertex keeps its stale
`qvec` (never revisited); `REBUILD INDEX` rewrites the whole store.

`CREATE VECTOR INDEX` builds the graph in memory, then persists vertices to the
encrypted index tree. Committed searches use a process-local copy of that
graph (same HNSW, no silent `ef`/`k` reduction). A transaction that updates
the index searches the durable tree so MVCC/rollback stay correct. Commit of
those writes bumps a generation and drops the cache.

On a partitioned table the index is partition-local: one HNSW graph per
partition, over that partition's own vector-payload store. `NEAREST` searches
every partition-local graph and merges hits by distance before keeping `k`, so
partitioning does not change which neighbours are returned (`docs/partitioning.md`).

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

Portable Go only. SIMD / `unsafe` is not used. `TestPortableProductionPath`
rejects `unsafe`/cgo/assembly in `internal/vector` and the element-type codecs
(`internal/float16`, `internal/int8vec`, `internal/bitvec`); architecture-specific
acceleration requires profiling, isolation, tests, fuzzing, and a measured win
before that policy changes.

Flat search can split candidates across `internal/scheduler.Pool`. HNSW graph updates stay on the writer transaction.

## Encryption

Payloads and HNSW nodes are ordinary table pages. Production files, WAL, and UNDO stay encrypted. Distinctive `NSVV` / `NSHM` headers must not appear as readable plaintext on disk.

## Recall

HNSW is approximate. Official benches report `recall@10` and `recall@100` against exact flat search together with latency. Latency must not be improved by silently reducing recall.

Measured on this host (Ryzen 5 7535HS, linux/amd64, portable Go, no SIMD): in-memory HNSW, 400 vectors × 16-d, 20 queries — `recall@10 = 1.000`, `recall@100 = 1.000`. Search bench: 2000 vectors × 32-d top-10 ≈ 1.89 ms/op. P11-scale QPS / RAM / index-size stay on `nextsql-bench --workload vector` (`docs/ops.md`). P23 ANN configurations report the full recall / latency / QPS / RAM / size set on `nextsql-bench --vecquant` (see **Size / recall comparison** and **Production-gating sign-off (Phase 23)**).

## Production-gating sign-off (Phase 23)

This is the dated production-gating review for the Phase 23 exit gate. It
states which memory-efficient representations and ANN paths are production-gated,
the measurements that justify that, what remains a documented follow-on, and the
compatibility / durability argument. It is a review, not a proof of zero defects.

### What is production-gated

| Surface | Guarantee |
|---|---|
| `VECTOR<F16,N>` | Half-precision payload store. Runtime value and every distance / HNSW / `NEAREST` path stay `float32` (widened on read). Versioned `NSVV` v2. |
| `VECTOR<I8,N>` | Signed-byte payload plus a per-vector `f32` scale. Same runtime contract as F16. Versioned `NSVV` v2. |
| `BITVECTOR<N>` + `HAMMING` | Bit-packed payload (`ceil(N/8)` bytes). Elements are exactly 0/1 (rejected, never rounded). Hamming is the default and only metric. HNSW builds a Hamming graph. |
| Quantised HNSW | `USING HNSW WITH (QUANTIZATION = 'F16' \| 'I8')` traverses on a compact copy and re-ranks against full-precision payloads, so recall tracks an unquantised graph. `NONE` is the default. |
| Compressed neighbour lists | HNSW node format v2 front-codes each layer. Lossless: v1 records still decode; recall and neighbour sets are unchanged. |
| IVF | `USING IVF WITH (LISTS = n [, PROBES = m])`. Encrypted detached index tree, process-local quantiser cache, not on partitioned tables. |
| IVF-PQ | `USING IVFPQ WITH (LISTS = n, SUBSPACES = M [, PROBES = m])`. ADC + exact re-rank. Encrypted detached index tree. Not on partitioned tables. No process-local cache yet (follow-on). |
| `SPARSEVECTOR<N>` + `USING SPARSE` | Inverted-index exact inner product, optional COSINE re-rank. Encrypted detached index tree. Not on partitioned tables. |
| Dense + sparse + BM25 fusion | A second `NEAREST` unions candidates and reciprocal-rank fuses them. |

Every new representation uses a versioned encoding. Every ANN structure is
encrypted, crash-recoverable through the index-tree WAL path, transaction- and
delete-aware, rebuildable (`REBUILD INDEX`), and Raft-compatible by inheriting
that same WAL path. Portable Go is the correctness baseline
(`TestPortableProductionPath`).

### Compatibility with F32 / HNSW

The production-gated F32 + flat/HNSW surface is unchanged:

- `QUANTIZATION` defaults to `NONE`; an omitted `WITH` clause still builds an
  unquantised graph.
- `NSVV` v1 F32 payloads, `NSHM` v1 headers, `NSCT` v1–v5 table descriptors,
  and HNSW node-format v1 records still decode.
- `BITVECTOR` is a new type (no catalog format bump for the element tag).
- Catalog table format v6/v7/v8 is additive; `DecodeTable` accepts v1..v8.
- Search never silently lowers `k` or `ef` to improve latency.

### Durability and encryption

Quantised payloads (`NSVV`), sparse payloads (`NSSV`), HNSW meta/nodes/qvecs
(`NSHM`), IVF (`NSIV`/`NSIC`/`NSIL`), IVF-PQ (`NSPQ`/`NSPC`/`NSPL`), and sparse
postings (`NSSM`/`NSSP`) live in encrypted B+Trees. Lifecycle tests assert those
magics never appear as readable plaintext
(`TestVectorF16Quantized`, `TestVectorI8Quantized`, `TestVectorBitvector`,
`TestQuantizedHNSWIndex`, `TestIVFVectorIndex`, `TestIVFPQVectorIndex`,
`TestSparseVectorIndex`). Crash recovery, backup, PITR, and Raft apply are the
same encrypted index-tree WAL path as the original HNSW index. INSERT / UPDATE /
DELETE maintain every ANN structure in the statement transaction; a writer
searches its own uncommitted tree.

### Recall discipline

Official `--vecquant` always reports recall@10 and recall@100 next to latency.
F16 columns and an F16-quantised HNSW graph match F32 recall@10 **0.916** on
the reference corpus; I8 is within a point (0.914 column / 0.912 quantised
graph). IVF and IVF-PQ recall at a 25 %-of-`LISTS` probe ratio on synthetic
uniform vectors is **explicitly lower** (0.619 / 0.495) — that is the documented
coarse-quantiser trade-off, not a silent reduction. Sparse inverted-index inner
product is exact (recall@10/@100 **1.000** on the sparse corpus). A
`BITVECTOR` / Hamming `--vecquant` row remains an optional follow-on; the type
itself is production-gated through lifecycle tests.

### Measurements

Every ANN configuration in `nextsql-bench --vecquant` reports recall@10,
recall@100, p50/p95/p99, QPS, RAM (resident heap), index size, build time, and
database size, with encryption + WAL + fsync on. See **Size / recall
comparison** above for the 2026-08-31 reference run.

### Test evidence

| Property | Test |
|---|---|
| F16 payload round-trip, restart, no plaintext | `TestVectorF16Quantized`, `TestPayloadF16Quantized` |
| I8 payload round-trip, restart, no plaintext | `TestVectorI8Quantized`, `TestPayloadI8Quantized` |
| Bitvector 0/1 fail-closed, Hamming HNSW, restart | `TestVectorBitvector`, `TestBindBitvector`, `TestPayloadBitPacked` |
| Quantised HNSW traversal + exact re-rank | `TestQuantizedHNSWIndex`, `TestQuantizedHNSWRerank` |
| Compressed neighbour lists lossless + v1 decode | `TestCompressedNeighborLists` |
| IVF lifecycle, cache, grouped centroids | `TestIVFVectorIndex`, `TestIVFProcessLocalCache`, `TestIVFCentroidGrouping` |
| IVF-PQ lifecycle, ADC + exact re-rank | `TestIVFPQVectorIndex`, `TestIVFPQSearchRecall` |
| Sparse lifecycle + inverted-index recall | `TestSparseVectorIndex`, `TestSparseSearchRecall` |
| Dense + sparse + BM25 fusion benefit | `TestDenseSparseBM25Fusion`, `TestDenseSparseBM25FusionPlan` |
| Size / recall / latency / QPS / heap | `nextsql-bench --vecquant`, `TestVectorQuantBench` |
| Portable Go (no `unsafe`/cgo/assembly) | `TestPortableProductionPath` |
| Decoder fail-closed | `FuzzDecodePayload`, `FuzzDecodeMeta`, `FuzzDecodeNode`, `FuzzDecodeIVF*`, `FuzzDecodePQ*`, `FuzzDecodeSparse*` |

### Documented follow-ons (not gate items)

- a `BITVECTOR` / Hamming row in `nextsql-bench --vecquant`;
- a process-local IVF-PQ quantiser cache (plain IVF already has one);
- a re-rank-free quantised HNSW mode that drops the full payload;
- IVF / IVF-PQ / `USING SPARSE` on partitioned tables;
- SIMD / `unsafe` acceleration, only after profiling, isolation, tests, fuzzing,
  and a measured win.

### Sign-off

At least one memory-efficient vector representation is production-gated:
`VECTOR<F16,N>`, `VECTOR<I8,N>`, `BITVECTOR<N>`, and the quantised HNSW index.
New ANN paths (quantised HNSW, compressed neighbour lists, IVF, IVF-PQ, sparse
retrieval, dense+sparse+BM25 fusion) have recall, latency, size, QPS, and RAM
measurements with encryption and durability enabled. No durability or encryption
regression is tracked as open. Existing F32 / HNSW behaviour remains compatible.
No correctness defect in the production-gated subset is tracked as open after
this review (2026-08-31).
