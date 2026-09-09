# Production readiness

## Current status: NOT READY

NextSQL must not be declared generally production-ready from this repository
state. P28 is the current release gate and remains open; the full P0/P1 release
matrix has not been executed and retained for a candidate on supported hardware.

## Tested evidence available in the repository

- Storage integrity, page/WAL decoding, B-tree invariants, crash recovery,
  backup/restore/PITR, transaction, protocol, auth/RBAC/TLS package tests.
- Linux packaging paths and container initialization are documented as tested.
- TLS 1.3 is required for remote production connections; production profile
  preflight fails closed for unsafe key location and remote TLS configuration.

## Guarantees and non-guarantees

- Durability is documented as WAL fsync before acknowledged durable commit.
  Failed fsync must return failure; crash paths have focused regression tests.
- I/O failure behavior is exercised, not asserted: `tests/fault` drives
  disk-full, short writes, failed fsync, failed directory fsync and EIO
  through the engine, backup and restore, and each case fails if its fault
  never reached the code under test.
- Supported transaction isolation is only what `docs/mvcc.md` and the engine
  tests demonstrate; this document does not upgrade that contract.
- Backup success includes verification/restore-test paths in the backup code,
  but a candidate release still needs an automated restore artifact.
- Persistent formats are versioned and compatibility-checked, and
  `tests/upgrade` replays retained fixtures written by shipped binaries —
  clean and `SIGKILL`ed — asserting recorded results, preserved grants, new
  writes, restart, backup/restore, and that no on-disk family moves past what
  the generating release can read. A release must cut its own fixture pair
  (`RELEASING.md`).

## Release commands

```bash
make test-pr
make test-production
make test-fault
make test-upgrade
make test-chaos
NEXTSQL_FUZZ_TIME=5m make test-nightly
NEXTSQL_BTREE_OPS=100000000 make test-soak
make test-soak-constrained
```

`test-soak` is intentionally not a normal PR gate. Production and release CI
must capture command output, Go version, host, filesystem, durability settings,
resource limits, benchmark inputs, and results. The runner prints the scratch
filesystem on every profile; release CI must set `NEXTSQL_REQUIRE_DURABLE_FS`
so a run on a RAM-backed filesystem fails instead of reporting a durability
result it cannot support. A failed or skipped mandatory
test is a failed release gate, not a waiver.

`test-soak-constrained` is the fixed `constrained-memory-v1` profile: 10M
operations, a 128 MiB B+Tree pool, a 250K-key working set, and a 512 MiB Go
memory limit (all may be explicitly overridden when recording a separate
profile). Each soak writes its normal log plus a versioned `*.manifest`
sidecar containing exact inputs, source revision/dirty state, host/kernel, CPU
count, scratch filesystem, Go version, timestamps, and exit status. Like the
other durable profiles, a soak warns on `tmpfs`/`ramfs` and refuses it when
`NEXTSQL_REQUIRE_DURABLE_FS=1`. A raw `*.timev` report plus normalized
`*.metrics` sidecar records elapsed/user/system time, peak RSS, filesystem I/O,
page faults, and context switches. Keep all four artifacts with release
evidence. The live log also reports bounded operation heartbeats (default:
1,000 records at most) with percentage, live-row count, elapsed time, observed
rate, and ETA; set `NEXTSQL_BTREE_PROGRESS_EVERY` to explicitly choose a
cadence. Running either command is evidence collection, not a claim that the
required multi-day soak has completed.
