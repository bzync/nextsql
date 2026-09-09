# Production hardening after report

**Increment completed:** production evidence and release-gate plumbing.
No storage format or durability mode was changed. The native protocol defaults
were raised only after confirming the existing `uint16` parameter representation
and the frame decoder's before-allocation bound.

| Area | Before | After | Implementation | Tests / result | Remaining limitation |
| --- | --- | --- | --- | --- | --- |
| Production baseline | Evidence was distributed among package tests and docs | Source-backed baseline and limits inventory exist | `docs/production/BEFORE.md` | Reviewed source/test locations | Must update as every engine limit changes |
| Gap disclosure | No dedicated severity-ranked report | Explicit CRITICAL/HIGH/MEDIUM/LOW list | `docs/production/GAPS.md` | Reviewable release input | Does not itself close gaps |
| Practical gate | No named combined production command | `make test-production` runs core, server/CLI and race-sensitive safety suites | `Makefile`, `scripts/test-production.sh`, `.github/workflows/production-gate.yml` | Chaos profile passed; full profile now passes end to end (exit 0, 15m43s) | First hosted CI artifact is now retained (`evidence/2026-09-10-test-production-hosted.md`, run 34407898651, exit 0, 48 packages, `filesystem: ext2/ext3`); it intentionally excludes multi-hour/days jobs |
| Long-test separation | B-tree soak script existed alone | PR, production, nightly fuzz, chaos, and soak profiles are named and their CI logs retain for 30 days | `Makefile`, `scripts/test-production.sh`, `.github/workflows/production-gate.yml` | Commands fail fast and preserve Go test output | CI run history became evidence with the first successful hosted run, retained 2026-09-10 |
| I/O failure behavior | The fault seam existed but had only ever been pointed at the WAL barrier (4 call sites, all in `wal`/`storage`) | Disk-full, short-write, failed-fsync, failed directory fsync and EIO are a named release profile across the engine, backup and restore | `internal/storage/io` (fault descriptor with op/path/offset, `ShortWrite`, sequential `Write`), `tests/fault`, `make test-fault` | 11 engine-level cases + 4 seam unit tests; `make test-fault` green in 50s on ext4 | Replication and the hosted multi-database paths are not yet driven through it |
| Durable-filesystem evidence | The runner redirected `TMPDIR` to the repository volume but never said what that volume was | Every profile prints `test scratch: <path> (filesystem: <type>)`, warns on `tmpfs`/`ramfs`, and refuses to run there under `NEXTSQL_REQUIRE_DURABLE_FS` | `scripts/test-production.sh` | Verified on this host: prints `ext2/ext3` for the repository volume and the warning path for `/tmp` | CI must set the variable and retain the line |
| Limit model | Frame, SQL and parameter limits were coupled; many operational limits were scattered constants | One validated catalog owns defaults and absolute ceilings for wire, memory, concurrency, result, time and storage controls; config/server wiring is regression-tested | `internal/limits`, `internal/config/config.go`, `cmd/nextsqld/main.go`, `docs/limits.md` | catalog/config/protocol/server race tests, build and vet pass | Structural/format limits remain with their owning encodings and are inventoried in `docs/limits.md` |

## Before/after limits

| Limit | Before | After |
| --- | ---: | ---: |
| Wire frame | 1 MiB | 64 MiB configurable; 64 MiB ceiling |
| SQL statement | 1 MiB | 16 MiB configurable; 64 MiB ceiling |
| Parameters | 256 | 65,535 configurable; 65,535 ceiling |
| Prepared statements / session | 64 fixed | 64 configurable; 4,096 ceiling |

## Bugs discovered

| Cause | Impact | Fix in this increment | Regression evidence |
| --- | --- | --- | --- |
| A failed WAL `fdatasync`/`fsync` was not latched. The platform reports a writeback error once and then clears it, so the next flush — covering only the following bytes — succeeded and advanced `durableLSN` across the un-synced region | An acknowledged commit could be lost: it sat after a gap a crash would expose as a torn or missing record, so recovery would stop short of it. A durability violation, ahead of everything below it in the priority order | The log latches a failed durability barrier and refuses every later `Append`, `Flush` and `InstallCheckpoint`; restart is the only way forward. A write consuming no bytes stays retryable so a cleared `ENOSPC` can still progress | `wal.TestFailedSyncLatchesAndRefusesLaterCommits`, `wal.TestFailedWriteConsumingNothingStaysRetryable`, `storage.TestCommitFailsClosedAfterDurabilityFault`; the first and third were verified to fail with the latch removed |
| General spatial implementation and web documentation disagreed (65,536 vs 256 vertices) | Operators could plan against the wrong bound | Closed: public limits now distinguish fixed `LINESTRING`/`POLYGON` (256) from general `GEOMETRY`/`GEOGRAPHY` (65,536 vertices, depth 8, 4,096 parts) | `internal/sql/types/{types,spatial}.go`; documentation inventory sweep |
| A create that failed *after* `file.Create` had exclusively created the data file left the file on disk. `file.Create` removed it when the key or superblock write failed, but not when the directory `fsync` failed, and `storage.open` had no unwind at all for the WAL, undo, allocator, buffer or integrity steps that follow | A full or failing device during `nextsql init`, a hosted database create, or a restore's engine open poisoned the path permanently: every retry returned `already_exists` for a file no usable database was ever built on. Availability, and an operator recovery step that is not documented anywhere | `file.Create` removes the file when its directory entry cannot be made durable. `storage.open` unwinds exactly what a failed create produced — data file, integrity sidecar, and only those WAL/undo directories that did not already exist, so a pre-existing directory is never deleted | `fault.TestCreateUnderDirectorySyncFaultLeavesNoBlockingFile` and `fault.TestUndoBarrierFaultFailsCreateAndUnwindsIt`, both verified to fail without the fix |

## Benchmark status

No before/after performance benchmark is claimed: the gate plumbing has no
intended engine-performance effect. Existing benchmarks and the 100M B-tree
measurement remain separate evidence, not substitutes for a release benchmark.

## Durable test profile: measured I/O cost

`go test` runs one package per CPU by default. The durable packages are
dominated by real `fsync` latency, not CPU, so the untuned profile ran a dozen
of them against one volume at once and inflated fsync latency until packages
blew the **default 10 minute per-package timeout while still making progress**.
Both failures were parked in a real `syscall.Fsync`, not hung.

| Package | tmpfs (`/tmp`) | ext4, alone | ext4, 12 packages in parallel |
| --- | ---: | ---: | ---: |
| `internal/backup` | 27.5 s | 201 s (pass) | killed at 606 s, 8 s into a test |
| `internal/storage/btree` | — | 104 s (pass) | killed at 606 s |

`internal/backup` spent ~82% of its wall time blocked on I/O (201 s wall for
37 s of user+sys). `internal/storage/btree` alone is *faster* than the 227 s on
record in `TODO.md`; contention, not the package, produced the 606 s timeout.

Almost all of `internal/backup`'s cost was the preallocation runway, not
backup: every temporary database reserved the production ~256 MiB with
`fallocate` in real blocks, and one listing test builds three sources plus
three backups. Shrinking the runway in that suite's `TestMain` takes it from
**201 s to 3.3 s**. The decision is per suite and measured, not blanket —
`internal/storage/btree` does bulk inserts, which is precisely what the runway
exists for, and is worse without it (104 s at the default, 115 s at 64 pages),
so it keeps the production value. The runway had no test coverage at all
before this; `internal/storage/file` now asserts it at the production default,
including that `fallocate` reserves real blocks rather than a sparse file.

The durable set now runs serially (`-p 1`, `NEXTSQL_TEST_IO_PARALLEL`) with an
explicit generous per-package `-timeout` (`NEXTSQL_TEST_TIMEOUT`, default 45m),
while the CPU-bound set keeps the default fan-out. The runner also clears the
repo-local scratch directory it owns, because a killed run strands every
`t.TempDir()` — the observed timeout left 820 MiB behind.

**`/tmp` is `tmpfs` on this host.** Any run that does not redirect `TMPDIR`
fsyncs into RAM and is not durability evidence. The runner redirects it to the
repository volume; a retained release run must record the filesystem type.

## Upgrade evidence: retained release fixtures

`tests/upgrade` (`make test-upgrade`) replays data directories written by
shipped binaries. `tests/upgrade/testdata/v0.0.1-{clean,dirty}.tar.gz` were cut
from the `v0.0.1` tag with `scripts/make-upgrade-fixture.sh`; each carries the
corpus results and the compatibility catalog that release printed. See
`docs/storage-format.md` "Retained release fixtures" for what each run asserts.

Cost: 52s for both fixtures on ext4, including building the current binaries
and expanding two ~258 MiB data directories. Each archive is ~2.5 MiB
compressed, so retaining one pair per release is cheap; the size is dominated by
the encrypted WAL and pages, not the preallocation runway.

## Verification performed

- `bash -n scripts/test-production.sh` — passed.
- `make -n test-pr test-production test-nightly test-chaos test-soak` — passed.
- `make test-chaos` — passed: WAL, recovery, storage, B-tree, and backup
  crash/corruption/recovery selection.
- `make test-production` — **passes end to end: exit 0, 15m43s wall (5m48s
  user), every package green, `TMPDIR` on ext4.** Slowest packages:
  `internal/storage/btree` 155s and 326s under `-race`, `internal/backup` 230s
  and 188s under `-race` — all far inside the 45m per-package timeout.
- `make test-chaos` — passes, 36s.
- The log #232 WAL durability regressions were re-run by name against ext4
  (not the host's `tmpfs` `/tmp`): `wal.TestFailedSyncLatchesAndRefusesLaterCommits`,
  `wal.TestFailedWriteConsumingNothingStaysRetryable`,
  `storage.TestCommitFailsClosedAfterDurabilityFault` — all pass.
- `bash -n scripts/test-production.sh` and `make -n` on every profile — pass.
- Historical: a later re-run failed in `internal/storage/btree` under `-race`
  with `wal.writeControl: create: .../nextsql.db.wal/control.tmp: no such file
  or directory`. Not the engine: a second `make test-production` had been
  started by hand while the first was running, and the runner's startup
  `rm -rf` of the shared `.test-tmp-production` path deleted the first run's
  live `t.TempDir()` trees. Each run now owns `.test-tmp-production/run-$$`
  and prunes only directories whose owning pid is gone.
- Historical: the first run exposed the sandbox `/tmp` quota while
  tests performed their legitimate durable preallocation, so the runner sets
  `TMPDIR` to ignored `.test-tmp-production`. A later full run then failed with
  two 10-minute per-package timeouts (`internal/storage/btree`,
  `internal/backup`), both parked in a real `syscall.Fsync`. Cause and fix are
  in "Durable test profile: measured I/O cost" above: the profile was running a
  dozen fsync-bound packages against one volume simultaneously.
- `make test-upgrade` — passes, 52s, `TMPDIR` on ext4: both `v0.0.1` fixtures
  replay under the current build with byte-identical results, and the released
  `v0.0.1` binaries re-read the directory the current build wrote
  (`NEXTSQL_UPGRADE_OLD_BINDIR`).
- Negative control for the dirty fixture: deleting its WAL segment before the
  replay makes **17 of 17** recorded queries stop matching, so the archive's
  content genuinely arrives through redo and the comparison fires. That control
  also exposed the missing-segment fail-open now fixed in `wal.Open`
  (`docs/wal.md`, "A missing segment fails the open closed").
