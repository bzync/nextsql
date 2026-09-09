# Limits and current gaps

This is still **0.0.1**. Treat it as an engine under measurement, not a drop-in production replacement, until you have run `nextsql-bench --slo` and the crash/HA suites on your hardware.

A live install uses `nextsql setup --profile production` (Setup-mode GUI default) so `nextsqld` fail-closes instead of shipping developer defaults. NextSQL Admin Setup and Operations are usable; Studio is in progress. NextSQL Intelligence / built-in RAG is not in the product.

## Hard limits

| Limit | Value |
|---|---|
| Logical page | 16 KiB |
| Packet / SQL text | 64 MiB / 16 MiB (configurable within these ceilings) |
| Parameters | 65,535 (configurable ceiling) |
| Prepared statements / session | 64 default; 4,096 ceiling |
| JSON depth / size | 32 / 1 MiB |
| Vector dimension | 8192 dense / bit; 65535 `SPARSEVECTOR<N>` (finite elements) |
| LINESTRING / POLYGON vertices | 256 |
| GEOMETRY / GEOGRAPHY vertices / nesting / parts | 65,536 / 8 / 4,096 |
| Collection nesting | 8; `ARRAY` elements `2²⁰` |
| JOIN tables | 8 (`FROM` + up to seven `JOIN`s) |
| Foreign keys per table | 16 |
| Columns per foreign key | 8 |
| FK cascade depth | 8 |
| FK cascade touched rows | 100 000 |
| Workflow body statements | 256 |
| Nested workflow / trigger depth | 8 |
| Wire result | 64 MiB |
| Default result rows | 1 000 000 |
| FTS fuzzy vocabulary scan | 4096 distinct terms/query |

## Not in this version

- General searchable field-level encryption (randomized `NSCE1.` and HKDF-separated RFC 5297 AES-SIV deterministic-equality `NSCE2.` are implemented across Go, JS/TS, Bun, and PHP drivers with key rotation, fuzz, PITR, and HA coverage)
- Multi-primary writes (deployments strictly adhere to single-leader Raft consensus)

Windows/macOS packaged Admin execution remains environment-blocked. Linux `.tar.gz` / `.run` / `.deb` / `.rpm` and silent/offline/upgrade/repair paths are live-verified.

## Known measurement notes (0.0.1)

- Large sequential SQL `DELETE` is correct after the leaf-merge fix. Official 10M warm-process and cold-open timings are published with their affected-row count methodology in `docs/ops.md`.
- 100M-row analytics are published. The 1M-vector HNSW baseline is the corrected distinct-vector v10 run (p95 **8.061 ms**, recall@10 **1.000**, recall@100 **0.998**). The terminal 100M-operation randomized B+Tree invariant soak is a deferred standalone measurement, not a release gate (best retained evidence: 44M clean operations).
- `nextsql-bench --slo` on your hardware is the source of latency numbers, not this site.

Never treat marketing language as a guarantee. NextSQL does not claim to be unhackable, fastest, or zero-downtime. Use engineering target, design objective, measured benchmark, SLO, and supported failure model.
