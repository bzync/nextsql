# Diagnostics and benches

## Live production profile

Production installs are intended to run with this posture out of the box:

```bash
nextsql setup --data-dir DIR --key-file FILE \
  --profile production --user app --password-file /path/to/pw
nextsqld --config DIR/nextsql.conf
```

`deployment_profile=production` in `nextsql.conf` is the durable switch.
`nextsqld --production` forces it for a process that was initialized as
developer. Either path fail-closes if the unlock key sits on the data volume
or if disk-watermark / drain / statement / idle timeouts are unset.
An opt-in feature's `system.capabilities` status still controls whether it is
production-gated; the production profile does not enable application-level
field encryption automatically.

## Diagnose and status

```bash
./nextsql status
./nextsql status --local --data-dir /var/lib/nextsql --key-file /etc/nextsql/root.key
./nextsql diagnose --data-dir /var/lib/nextsql
```

`nextsql status` (default) is **server mode**. It dials a running `nextsqld`, completes the NSQL handshake, and prints `mode server`, `addr`, `user`, `database`, and `ok`. It does not open the data directory and does not print LSNs. Connection flags and dotenv match `exec`. Mixing `--data-dir` / `--key-file` onto server-mode `status` is an error.

`nextsql status --local` is the data-directory inspect: format-family versions plus opened table count, `durable_lsn` / `checkpoint_lsn` / `next_lsn`, isolated-page count, query/error/commit counters, admission inflight/queue, and cluster fields when Raft is running.

`diagnose` checks format-family versions and plaintext headers. Most families are **v1**; the catalog descriptor (`NSCT`) is at **v13** (readable 1..13). A newer or older-than-min file fails closed — there is no silent rewrite. `diagnose` does not need a key.

Isolated pages are a fail-closed corruption path (`*.isolated`). NextSQL never returns a known corrupted record.

## Metrics

The process registry tracks queries, errors, commits, rollbacks, admitted, rejected, canceled, rows, p50 / p95 / p99 / p99.9, page AEAD seal/open time, WAL bytes flushed, isolated / repaired pages, and FK check counters. Metrics never contain passwords, keys, tokens, or secrets.

## nextsql-bench

Official numbers keep encryption, WAL, `fsync`, checksums, MVCC, and authentication on. Numbers from one host are not product guarantees.

```bash
./nextsql-bench --quick
./nextsql-bench --workload all|page|point|range|insert|update|delete|txn|join|agg|json|fulltext|vector|hybrid
./nextsql-bench --duration 1s --rows 128 --concurrency 1

# labeled SLO suite (hardware, filesystem, encryption, durability printed on every row)
./nextsql-bench --slo
./nextsql-bench --slo --slo-max-rows 1000000 --slo-vectors 256 --duration 2s

# comparisons
./nextsql-bench --partition --partition-rows 40000   # RANGE-partitioned vs unpartitioned
./nextsql-bench --readscale --readscale-rows 10000   # STRONG vs STALE/BOUNDED reads across a 3-node cluster
./nextsql-bench --vecquant                           # F32/F16/I8, quantised HNSW, IVF, IVF-PQ, sparse
```

`--slo` seeds a throwaway encrypted database and measures cached PK lookup, secondary-index equality, durable single-row INSERT/UPDATE, bulk INSERT plus `COUNT(*)` / `GROUP BY` / range / join at each scale, hybrid `WHERE`+`SEARCH`+`NEAREST`, and HNSW recall@10 / recall@100.

`--readscale` builds a 3-node single-leader cluster and drives PK point reads under `STRONG` on the leader, `STALE` on the leader, `STALE` over two and three members, and `BOUNDED` over three — reporting aggregate read QPS, the leader's slice, and p95/p99. It measures the Raft read-barrier cost and the leader read-offload; see [`docs/ha.md`](https://github.com/bzync/nextsql/blob/main/docs/ha.md) "Read scaling".

100M rows and 1M-vector HNSW still need a longer run (`--slo-max-rows 100000000` / `--slo-vectors 1000000`). Published host numbers live in [`docs/ops.md`](https://github.com/bzync/nextsql/blob/main/docs/ops.md).

## Tests

```bash
go test ./...
go test -race ./...          # needs a C compiler
go test ./tests/integration ./tests/crash ./tests/ha
```
