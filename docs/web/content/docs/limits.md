# Limits and current gaps

This is still **0.1.0-dev**. Treat it as an engine under measurement, not a drop-in production replacement, until you have run `nextsql-bench --slo` and the crash/HA suites on your hardware.

## Hard limits

| Limit | Value |
|---|---|
| Logical page | 16 KiB |
| Packet / SQL text | 1 MiB |
| Parameters | 256 |
| JSON depth / size | 32 / 1 MiB |
| Vector dimension | 8192, finite elements |
| LINESTRING / POLYGON vertices | 256 |
| JOIN tables | 8 (`FROM` + up to seven `JOIN`s) |
| Foreign keys per table | 16 |
| Columns per foreign key | 8 |
| FK cascade depth | 8 |
| FK cascade touched rows | 100 000 |
| Wire result | 64 MiB |
| Default result rows | 1 000 000 |

## Not in this version

- Heap/index page reclaim after `DROP TABLE`
- Outer `JOIN` together with `SEARCH` or `NEAREST` (inner join is allowed when the rank column is on the `FROM` table)
- Stemming / stop words
- `VECTOR<F16,N>`, `VECTOR<I8,N>`, IVF / IVF-PQ
- Field-level `ENCRYPTED CLIENT` / server-side zero-knowledge SQL
- mTLS, external IdP, short-lived credentials
- Multi-primary writes

## Known measurement notes (0.1.0-dev)

- Large sequential SQL `DELETE` is correct after the leaf-merge fix. Official 10M warm-process and cold-open timings are published with their affected-row count methodology in `docs/ops.md`.
- 100M-row analytics are published. The prior 1M-vector HNSW baseline is published, but its corrected distinct-vector rerun remains open; the 100M randomized B+Tree invariant soak follows it.
- `nextsql-bench --slo` on your hardware is the source of latency numbers, not this site.

Never treat marketing language as a guarantee. NextSQL does not claim to be unhackable, fastest, or zero-downtime. Use engineering target, design objective, measured benchmark, SLO, and supported failure model.
