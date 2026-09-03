# Limits and current gaps

This is still **0.1.0-dev**. Treat it as an engine under measurement, not a drop-in production replacement, until you have run `nextsql-bench --slo` and the crash/HA suites on your hardware.

## Hard limits

| Limit | Value |
|---|---|
| Logical page | 16 KiB |
| Packet / SQL text | 1 MiB |
| Parameters | 256 |
| JSON depth / size | 32 / 1 MiB |
| Vector dimension | 8192 dense / bit; 65535 `SPARSEVECTOR<N>` (finite elements) |
| LINESTRING / POLYGON vertices | 256 |
| JOIN tables | 8 (`FROM` + up to seven `JOIN`s) |
| Foreign keys per table | 16 |
| Columns per foreign key | 8 |
| FK cascade depth | 8 |
| FK cascade touched rows | 100 000 |
| Wire result | 64 MiB |
| Default result rows | 1 000 000 |
| FTS fuzzy vocabulary scan | 4096 distinct terms/query |

## Not in this version

- Heap/index page reclaim after `DROP TABLE`
- Outer `JOIN` together with `SEARCH` or `NEAREST` (inner join is allowed when the rank column is on the `FROM` table)
- Additional language analyzers beyond `simple` / `english` / `french` / `german` / `spanish` (`HIGHLIGHT` / `SNIPPET`, prefix, fuzzy matching, typo tolerance, multi-field search, field weighting, and faceting are implemented)
- Dense+sparse+BM25 fusion (`VECTOR<F16,N>`, `VECTOR<I8,N>`, `BITVECTOR<N>`, the quantised HNSW index, compressed HNSW neighbour lists, IVF, IVF-PQ, and `SPARSEVECTOR<N>` / `USING SPARSE` are implemented)
- Production-gated field-level `ENCRYPTED CLIENT` (experimental core + Go
  helper exist; non-Go drivers, PITR, and HA/failover coverage remain open)
- OIDC opaque-token introspection/JIT and OCSP (the required external-IdP,
  short-lived credential, mTLS, live-rotation, and X.509 CRL paths are implemented)
- Multi-primary writes

## Known measurement notes (0.1.0-dev)

- Large sequential SQL `DELETE` is correct after the leaf-merge fix. Official 10M warm-process and cold-open timings are published with their affected-row count methodology in `docs/ops.md`.
- 100M-row analytics are published. The prior 1M-vector HNSW baseline is published, but its corrected distinct-vector rerun remains open; the 100M randomized B+Tree invariant soak follows it.
- `nextsql-bench --slo` on your hardware is the source of latency numbers, not this site.

Never treat marketing language as a guarantee. NextSQL does not claim to be unhackable, fastest, or zero-downtime. Use engineering target, design objective, measured benchmark, SLO, and supported failure model.
