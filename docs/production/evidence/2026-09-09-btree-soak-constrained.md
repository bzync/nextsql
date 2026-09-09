# B+Tree invariant soak — constrained-memory-v1

Recorded 2026-09-09. 10,000,000 operations, 8,192 pool pages (128 MiB),
250,000-key space, `GOMEMLIMIT=512MiB`, `GOGC=25`, run with
`NEXTSQL_REQUIRE_DURABLE_FS=1`. `TestRandomizedLargeInvariants` PASS, exit 0.

This is one dated constrained-memory run. The normal 100M profile and the
multi-day soak `GAPS.md` also requires are still outstanding, and this does not
stand in for either.

The 100M profile was started on this host and stopped rather than completed: it
reaches 4M operations in 20 minutes, and its projection grows with the live set
(3h04m at 1M ops, 5h58m at 2M, 7h34m at 3M, 7h56m at 4M) as throughput decays
from 31,369 to 3,360 ops/s. It needs a dedicated run of 8+ hours on an idle
machine. Its partial artifacts were discarded rather than retained as if they
were a result.

`.bench-results/` is gitignored, so the sidecars are reproduced here.

## Manifest

```text
nextsql_btree_soak_manifest_version=1
profile=constrained-memory-v1
started_utc=20260909T102051Z
ended_utc=20260909T105452Z
exit=0
repository=/home/rayan/bzync-server/nextsql
git_revision=b32204ac04913c8bceb8141d1d79e2dd53bc6739
git_dirty=true
ops=10000000
pool_pages=8192
space=250000
gomemlimit=512MiB
gogc=25
godebug=madvdontneed=1
progress=1
progress_every=auto
tmpdir=/home/rayan/bzync-server/nextsql/.bench-tmp
filesystem_type=ext2/ext3
require_durable_fs=true
log=/home/rayan/bzync-server/nextsql/.bench-results/btree-constrained-memory-v1-ops10000000-pool8192-20260909T102051Z.log
metrics=/home/rayan/bzync-server/nextsql/.bench-results/btree-constrained-memory-v1-ops10000000-pool8192-20260909T102051Z.metrics
timev=/home/rayan/bzync-server/nextsql/.bench-results/btree-constrained-memory-v1-ops10000000-pool8192-20260909T102051Z.timev
hostname=bzync
kernel=Linux 7.0.0-31-generic x86_64 GNU/Linux
cpu=12
memory_bytes=15380348928
filesystem=type=ext4 device=/dev/mapper/ubuntu--vg-ubuntu--lv available_kib=172089412
go_version=go version go1.26.7 linux/amd64
```

## Metrics

```text
nextsql_btree_soak_metrics_version=1
profile=constrained-memory-v1
started_utc=20260909T102051Z
ended_utc=20260909T105452Z
timev=.bench-results/btree-constrained-memory-v1-ops10000000-pool8192-20260909T102051Z.timev
user_seconds=830.28
system_seconds=95.38
elapsed_wall=34:01.17
max_rss_kib=135704
major_page_faults=506
voluntary_context_switches=1848950
involuntary_context_switches=67202
filesystem_inputs=221540
filesystem_outputs=121919176
```
