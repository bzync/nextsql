# WAL and crash recovery (Phase 3)

Write-ahead logging is mandatory. A modification is not committed until its WAL records have been group-committed and `fsync`ed.

```
transaction → WAL → group commit → fsync → COMMIT acknowledgement
```

Phase 4 adds UNDO records and MVCC (`docs/mvcc.md`). Each B+Tree `Insert` / `Update` / `Delete` is still an auto-committed write transaction unless the caller uses `BeginTxn`. Crash recovery is REDO of committed page images followed by UNDO of in-flight transactions.

## Files

A data file `foo.db` owns a sibling directory `foo.db.wal/`:

| Name | Role |
|---|---|
| `control` | Durable checkpoint / LSN / wrapped WAL DEK; version `1` (full page images only) or `2` (may contain page deltas, see below) |
| `wal-<16 hex>.seg` | Encrypted record segments |

Control is replaced atomically (`control.tmp` → `control` → directory `fsync`).

## WAL DEK

WAL records are sealed with a WAL DEK, not the page DEK. The WAL DEK is generated at WAL create time and stored in the control file wrapped under the page-key provider (`crypto.WrapDEK`, domain `'W'`). Stolen WAL files are unreadable without the page-key material that unwraps the WAL DEK.

Nonce generations for WAL records are reserved in batches of 4096, persisted in the control file before use.

## Physical record (`NSWL`)

Little-endian. AES-256-GCM.

| Offset | Size | Field |
|---|---|---|
| 0 | 4 | Magic `NSWL` |
| 4 | 2 | Record version (`1`) |
| 6 | 2 | Cipher suite |
| 8 | 4 | WAL key version |
| 12 | 8 | LSN (1-based; 0 is reserved) |
| 20 | 4 | Ciphertext length (payload + 16-byte tag) |
| 24 | 12 | Nonce |
| 36 | 4 | CRC32C of bytes `[0:36]` |
| 40 | N | Ciphertext \|\| tag |

**AAD:** bytes `[0:20]` (magic through LSN). Ciphertext length is covered by the header CRC.

A torn tail (short read, bad header CRC, or AEAD failure after the last durable LSN) is truncated. The same failure at or below `DurableLSN` is corruption and fails closed.

## Logical payload

| Offset | Size | Field |
|---|---|---|
| 0 | 2 | Type |
| 2 | 2 | Flags |
| 4 | 8 | Transaction ID |
| 12 | 8 | Previous LSN of this transaction |
| 20 | 8 | Page ID (`0` if none) |
| 28 | … | Type-specific body |

| Type | Name | Body |
|---|---|---|
| 1 | Begin | empty |
| 2 | Insert | `u16 klen`, `u16 vlen`, key, value |
| 3 | Delete | `u16 klen`, key |
| 4 | Update | `u16 klen`, `u16 vlen`, key, value |
| 5 | PageImage | 16384-byte logical page (LSN already stamped) |
| 6 | Commit | empty |
| 7 | Abort | empty |
| 8 | Checkpoint | redo LSN, durable LSN, allocator, tree root/height |
| 9 | TreeMeta | root `u64`, height `u16` |
| 10 | AllocState | next, freelist head, freelist count |
| 11 | Undo | undo id `u64`, kind `u8`, `u16 klen`, key |
| 12 | Change | versioned key-only logical SQL row change (`NSCD` v1) |
| 13 | PageDelta | changed byte ranges of a page against its previous logged state (page delta v1, below); only in a control-version-2 log |

Redo uses `PageImage`, `PageDelta`, `TreeMeta`, `AllocState`, and `Checkpoint`. After redo, recovery applies UNDO for transactions that never committed, including ones whose rollback was logged (`docs/mvcc.md`). Logical insert/update/delete records are durable physical-tree history. `Change` records are ignored by redo and consumed by CDC only after the matching durable `Commit`.

### Logical CDC change (`NSCD` v1)

The executor stages bounded SQL row identities on the storage transaction. At
commit, the engine appends all `Change` records contiguously after page and
allocator records and immediately before `Commit`. This ordering makes the
commit LSN a safe resume boundary even when the transaction began before the
consumer's previous token. A crash before `Commit` may leave authentic change
records in WAL; CDC discards them and never exposes them.

The encrypted type-specific body is:

| Offset | Size | Field |
|---|---:|---|
| 0 | 4 | Magic `NSCD` |
| 4 | 2 | Change format version (`1`) |
| 6 | 1 | Operation: INSERT (`1`), UPDATE (`2`), DELETE (`3`) |
| 7 | 1 | Flags: bit 0 before image, bit 1 after image |
| 8 | 4 | Stable table ID |
| 12 | 2 | Table-name byte length |
| 14 | 2 | New/current tenant byte length |
| 16 | 2 | Old tenant byte length (UPDATE only) |
| 18 | 2 | New/current encoded primary-key byte length |
| 20 | 2 | Old encoded primary-key byte length (changed-key UPDATE only) |
| 22 | 2 | Before-image byte length |
| 24 | 2 | After-image byte length |
| 26 | 2 | Reserved; zero |
| 28 | ... | Table, tenant, old tenant, key, old key, before, after bytes |

Names, tenants, keys, total record size, staged change count, and staged bytes
are bounded before allocation. Existing tables default to key-only records.
Tables explicitly configured for `CDC IMAGES FULL` add versioned `NSRW` before
images for UPDATE/DELETE and after images for INSERT/UPDATE. The total logical
record remains capped at one logical page; an oversized opt-in image fails the
transaction instead of weakening bounds or splitting atomic event identity.

A write transaction logs **one** `PageImage` per dirty page, at commit, with the page’s final bytes. Intermediate pin/release images are not written; redo only needs the last committed image. Uncommitted dirty pages still cannot flush (`AllowFlush`).

## Segments (`NSWS`)

Default size is 128 MiB. Header is 64 bytes: magic, version, segment id, start LSN, database/file UUIDs, CRC32C.

## Group commit

`Append` only buffers. `Flush(lsn)` is the durability boundary: one writer writes the buffer, `fdatasync`s the segment (Linux; full `fsync` elsewhere), then wakes waiters. New segment files and the control file still use `fsync` so the directory entry is durable. `Commit` does not return success until `Flush` of the commit record succeeds.

The write and the `fdatasync` run **without** the log mutex, and a
non-replicated commit waits for them **without** the engine mutex (log #287).
Commits that arrive while one flush is in the kernel append their records and
are made durable together by the next flush. Before this, both mutexes were
held across the fsync, so no other transaction could even append until it
finished: N concurrent commits paid N fsyncs, and single-row insert throughput
stayed at ~700/s from 1 to 64 connections. Measured in-process on ext4 with
64 connections: 5,018 inserts/s at ~7 commits per 1.7 ms flush. That figure
depends heavily on the host's I/O load.

Exactly one flush owns the segment write at a time. Rotation, `Close` and
`ClipTo` reach the segment through `flushLocked` and wait for it; `CrashClose`
waits explicitly. A commit keeps its place in `Engine.writers` (unacknowledged,
invisible, still holding its locks, counted as active by a checkpoint) until
its commit record is durable, and becomes visible only afterwards. A rollback
attempted in that window is refused, because the commit record may already be
on stable storage. A waiter on a log that closes underneath it fails with
`unavailable` instead of waiting forever. The replicated commit path is
unchanged: it still serializes whole commits across the Raft round trip.

### Structure modifications are logged as system transactions

A B+tree split or merge takes effect as soon as it happens: other
transactions route through the new page and write into it, and rollback never
reverts it. Its redo used to be only the page images the splitting transaction
logged at its own commit. If that transaction never committed, a committed
transaction could make durable an image that pointed to, or held rows in, a
page whose own image was never logged. After a crash that page was unreadable
and committed rows were lost. The new concurrent-commit power-loss test
reproduced this in about one round in three, and it predates group commit.

Now, when a page-writing operation that allocated or dropped a page completes
successfully, `Engine.LeaveOp` logs every page it dirtied as its own committed
system transaction: `Begin`, page images, `AllocState`, `Commit`. This is the
standard nested-top-action approach. It does not fsync: WAL order puts it ahead
of any commit that can depend on it. The images may carry other transactions'
uncommitted row versions, which recovery's undo and MVCC visibility handle as
for any shared page. A failed operation logs nothing. If the append itself
fails, the pages stay pending: `AllowFlush` refuses to write them and the next
commit logs them first or refuses to commit.

`Engine.Kill` / `Log.CrashClose` discard the unsynced tail (truncate to the last synced offset) to simulate power loss.

### Failed durability barriers latch

A failed `fdatasync`/`fsync` leaves what actually reached stable storage
indeterminate, and the platform reports the failure only once — a Linux
writeback error is delivered to a single `fsync` call and then cleared, so the
next `fsync`, covering only the *following* bytes, succeeds. A log that simply
retried would therefore mark the un-synced region durable and acknowledge a
commit sitting after a gap that a crash would expose as a torn or missing
record, losing the acknowledged commit.

The log latches instead. Once a durability barrier fails, every subsequent
`Append`, `Flush` and `InstallCheckpoint` returns that same error, so no commit
can be acknowledged and the redo boundary cannot advance past the indeterminate
region (which would make a later segment discard turn the gap into permanent
loss). `DurableLSN` stays at the last known-good value, and the control file
keeps that value, so recovery on restart replays to a boundary the log actually
observed as synced. Restart is the only way forward.

A write that consumed **no** bytes does not latch: the buffer is intact at an
unchanged offset, so a transient `ENOSPC` an operator clears can still make
progress. A partial write does latch, because the segment then carries a torn
record that a later write cannot repair in place.

Page and allocator syncs do not latch — they are re-derivable by redo, and the
WAL is the durability authority.

### A missing segment fails the open closed

The control file names the boundary the pages on disk are behind: `redo_lsn`,
and the `durable_lsn` the engine already acknowledged. `wal.Open` refuses when
the segments covering that interval are not on disk — either no segment at all
while `durable_lsn > 0`, or an oldest segment whose `StartLSN` is past
`redo_lsn`. Redo cannot reach the acknowledged boundary in that state, so
opening anyway would silently present a database missing acknowledged commits,
and the next checkpoint would install over the gap and make the loss permanent.
The error names the LSNs involved so an operator can tell what is missing;
the way forward is a restore, or recovery pointed at a WAL archive.

A log that has never acknowledged anything (`durable_lsn == 0`) is an empty log,
not a hole, and still opens with a fresh segment.

### Driving these paths in tests

Every durable read, write and barrier in the engine goes through one seam,
`internal/storage/io` (`diskio`): `ReadFullAt`, `WriteFullAt`, `WriteAt`,
`Write`, `Sync`, `DataSync` and `SyncDir`. A test installs a hook with
`diskio.SetFaultForTest` and receives a `diskio.Fault` naming the operation
(`read`, `write`, `sync`, `datasync`, `syncdir`), the file or directory, and
the offset (`-1` for a sequential write), so one file can be failed while
another keeps working — a full data volume beside a healthy log, say.
Returning a `*diskio.ShortWrite` writes the prefix it names and then fails,
reproducing a genuine torn record rather than a write that consumed nothing.
The hook is process-global and never installed by production code.

`tests/fault` is the release profile built on it (`make test-fault`, and part
of `test-pr`, `test-production`, `test-chaos` and `test-nightly`). Each test
there asserts that its fault actually fired, so a path that later moves off the
seam fails the suite instead of silently passing.

## Page deltas

Redo is physical, and a commit used to log a full 16 KiB image of every page
it dirtied. A single-row insert wrote about 18 KB of WAL, and under sustained
concurrent writes that write amplification, not the CPU, bounded throughput.
Compressing the images was rejected: compression before encryption makes
ciphertext length depend on page content.

A commit now logs a `PageDelta` record instead of a full image whenever a safe
base exists: only the byte ranges that differ from the page's previous logged
state. Body, version 1:

| Size | Field |
|---|---|
| 1 | Version (`1`) |
| 8 | Base LSN: the record whose page state this delta applies to |
| 32 | SHA-256 of the base page with its LSN (bytes 16..24) and checksum (40..44) fields zeroed |
| 2 | Run count |
| … | Runs, sorted and non-overlapping: `u16 offset`, `u16 length`, bytes |

The LSN and checksum fields are excluded from the diff and the digest because
they change independently of the content. Runs separated by fewer than 8
unchanged bytes are merged. A delta larger than half a page is not worth a
base, so that page logs a full image. The decoder is strict (sorted runs,
exact length, no trailing bytes) and fuzzed (`FuzzDecodePageDelta`).

**When a base is safe.** The engine keeps an LRU cache of each page's most
recent logged state (half the buffer pool's frame count, at least 16). A delta
is encoded against an entry only if all of these hold, under the engine lock
at append time:

- the entry is the page's most recent record, and the page being logged
  carries that record's LSN;
- the record will be replayed. The entry settles only once its transaction's
  commit record is appended (for a replicated commit, once the hold is
  released as committed), or once it is a committed system transaction;
- the record is at or after the last checkpoint's redo boundary, so recovery
  scans it;
- the page has not been changed without logging since. A rollback changes
  pages without logging them, so it drops the entries for every page it
  touched.

An evicted or ineligible page logs a full image, which starts a new chain.

**Replay fails closed.** Recovery skips a delta whose page is already at or
past its LSN. Otherwise it applies the delta only if the page is at exactly the
base LSN *and* matches the base digest. Anything else is a `corruption` error
naming the page and both LSNs. A wrong base never produces a plausible wrong
page. Live page repair (`recovery.RepairPage`) rebuilds a page by chaining
committed images and deltas from the WAL; a delta whose base does not match
breaks the chain rather than extending it.

**The version gate.** A release that predates page deltas would read type 13
as a torn tail. It checks the control version and refuses a log at `2`, so a
page delta may only exist in a log whose control file is at `2`:

- `wal.Append` refuses a `PageDelta` on a version-1 log;
- a follower installing replicated records moves its own control file to `2`
  before the first delta reaches a segment;
- recovery moves a version-1 log that contains a delta to `2` before replaying
  anything. A point-in-time restore can produce that pairing from a base
  backup taken before the source moved to `2`.

`wal_page_deltas` (`auto` default, `on`, `off`) chooses the mode:

| Value | New database | Existing version-1 log | Existing version-2 log |
|---|---|---|---|
| `auto` | created at `2`, logs deltas | stays at `1`, full images; still opens with the release that created it | logs deltas |
| `on` | created at `2`, logs deltas | moved to `2` on open (one way) | logs deltas |
| `off` | — (`nextsql init` always uses `auto`) | full images | full images; stays at `2`, still replays its deltas |

Nothing moves a log back to `1`. A database that must stay openable by an
older release keeps its version-1 log under `auto`; `tests/upgrade` asserts
that replaying a retained v0.0.1 fixture does not change its on-disk version.

**Measured effect.** In the storage test (`TestPageDeltasAreWrittenAndRecovered`),
2,000 single-key commits logged about 2,000 deltas averaging 106 bytes, where
each would otherwise have been a 16,384-byte image. Over the wire, a default
`nextsqld` doing single-row inserts writes 2.3–2.6 KB to disk per insert with
deltas and 18.2–19 KB with `wal_page_deltas=off`. That counts every byte the
process writes: WAL, undo, and data-file page flushes. Details, including why
the throughput figures from that host are not a sustained-load claim, are in
`TODO.md` log #289.

## Checkpoints

1. Flush committed dirty pages and `fsync` the data file.
2. Append a `Checkpoint` record and group-commit it.
3. Update the control file and the superblock checkpoint/redo LSNs.
4. Offer every segment, including the current one, to an optional `Archiver` (PITR hook). Segments are not deleted. The live segment may be re-archived after later appends.

An interrupted checkpoint leaves the previous control file in place. Recovery still starts from the last installed redo LSN.

**The redo boundary is fuzzy.** A page dirtied by a still-running transaction
cannot be flushed (no-steal), and a committed change to that page can sit in
the WAL below the log's end. The boundary is therefore the minimum of:

- the log's end when the flush began;
- the oldest logged change not yet stamped onto its buffer frame;
- the oldest dirty frame's first logged LSN (`recLSN`);
- the `Begin` record of the oldest transaction still running.

The last term exists because recovery finds transactions to undo by scanning
for `Begin` records from the boundary, and an id it never sees defaults to
committed. A committed image of a shared page can carry a running
transaction's row versions. With a boundary past that `Begin`, a transaction
that never committed came back after a crash as committed, with no undo.
Before this fix, a checkpoint taken while a transaction held a dirty page could
lose an acknowledged commit.
`tests/crash/checkpoint_redo_test.go` pins each term; each test fails with its
term removed.

`nextsqld` installs a checkpoint every `checkpoint_interval_ms` (default
`300000`, five minutes). This bounds ordinary crash redo to the WAL suffix
since that checkpoint without deleting retained WAL or changing the fsync
commit rule. Set the value to `0` only for a controlled deployment that
performs checkpoints explicitly; an indefinitely running production node with
it disabled can otherwise accumulate an arbitrarily large redo suffix after an
unclean stop. A failed scheduled checkpoint is logged and retried on the next
interval; the database stays durable through WAL rather than acknowledging an
unsafe checkpoint.

### Retention

**Nothing prunes the WAL until retention is configured.** A freshly
initialized deployment retains every segment it ever writes, including
`wal-0000000000000001.seg`, for as long as the instance runs. That is the
default, and it is deliberate — but it means WAL growth is unbounded until an
operator chooses one of the two policies below. `wal_on_disk_bytes` and
`wal_segments` (`system.metrics`) report what the volume is actually holding;
`wal_bytes_written` counts bytes ever appended and only ever rises, so it
cannot answer this question.

Pick one:

| | `wal_archive` + `wal_retention_ms` | `wal_max_retained_mb` |
|---|---|---|
| For | PITR deployments | single nodes that do not want PITR |
| Prunes when | scheduled `MAINTAIN DATABASE` | after each periodic checkpoint, and during `MAINTAIN DATABASE` |
| Keeps | history newer than the time policy | up to the byte cap |
| Archive | required; a segment is archived immediately before deletion | must not be configured |

The two are mutually exclusive and `config.Validate` refuses the pair: where
an archiver is configured the local segment may be the only copy until the
archiver has taken it, and a size cap has no way to know that.

#### PITR retention (`wal_archive` + `wal_retention_ms`)

`DB.SetWALRetentionHorizon(lsn)`
sets the oldest PITR point local cleanup may pass; zero disables pruning. During
`MAINTAIN DATABASE`, a closed segment is removable only when its successor
starts no later than both the installed redo LSN and the configured PITR
horizon. The segment is offered to the configured archiver with its exact LSN
range immediately before deletion; archive failure preserves it. Segment bytes
are charged to the maintenance logical-I/O budget before archival, and directory
deletions are synced.

Pruning local history deliberately reduces the records available to live
page-image repair, which scans local WAL from LSN 1. If corruption needs an
older archived image, restore the archived WAL before repair. Deployments that
prioritize maximum local repair history should leave the horizon unset.

##### Automatic time-based retention (`wal_retention_ms`)

`DB.SetWALRetentionHorizon` is a raw, point-in-time LSN setter — by itself
it is a manual mechanism, not a policy. `nextsqld`'s `wal_retention_ms`
config key turns it into one: when positive **and** `wal_archive` is also
configured, `nextsqld` periodically (every 1/24th of the retention window,
clamped to `[1m, 1h]`, plus once immediately at startup) recomputes the
horizon as the newest archived segment's LSN at or before
`now - wal_retention_ms`, using the same `ResolveUntilTime` lookup
`nextsql restore --until` uses for PITR — and calls
`SetWALRetentionHorizon` with it. `wal_retention_ms` alone does not require
`wal_archive`: if no archiver is configured, updating the horizon has
nothing safe to advance to (see above — pruning without an archiver
destroys the only copy of that history), so the updater is a no-op until
both are set together. 0 (the default) leaves the horizon unmanaged,
matching prior behavior exactly.

This only maintains the horizon — it does not prune anything itself.
Pruning still happens only during `MAINTAIN DATABASE` (SQL) / `MAINTAIN
TABLE`/`INDEX`, which remains a manual or externally-scheduled operation
(e.g. cron calling `nextsql exec -c 'MAINTAIN DATABASE'`); nextsqld has no
automatic background maintenance scheduler. A `wal_retention_ms` policy
without a scheduled `MAINTAIN DATABASE` alongside it keeps the horizon
current but prunes nothing.

#### Size-capped retention (`wal_max_retained_mb`)

For a deployment that does not archive, `wal_max_retained_mb` bounds the WAL
directory directly. `nextsqld` applies it after each successful periodic
checkpoint — a checkpoint is precisely what makes older segments obsolete, so
it is the moment they can be reclaimed — and `MAINTAIN DATABASE` applies it
too. Unlike the PITR policy it therefore needs no external scheduler.

`wal.Log.TrimToCap` removes closed segments oldest first until the retained
total is at or below the cap, and stops at the first segment it may not
remove. A segment is removable only when its successor starts no later than
both the installed redo LSN and every active CDC retention pin — the same
condition `PruneArchivedBefore` applies. **The cap can therefore be exceeded,
and that is the correct outcome**: a workload that pins WAL faster than a
checkpoint releases it keeps its history, and the operator sees
`wal_on_disk_bytes` rise, rather than the engine deleting records it still
needs. `wal_trimmed_segments` counts what the cap has actually reclaimed, so
a cap that is configured but never able to reclaim anything is visible as a
flat counter beside a rising footprint.

The floor is 256 MiB (two default segments): a cap that cannot hold the
segment being written plus one closed one could never be satisfied. The
active segment is never a candidate. 0, the default, retains every segment.

The same caveat as the PITR policy applies — pruning local history reduces
what live page-image repair can scan. A deployment that prioritizes maximum
local repair history should leave the cap unset and watch the footprint
metrics instead.

```bash
nextsqld --wal-max-retained-mb 4096 ...
# or nextsql.conf:
#   wal_max_retained_mb=4096
```

## Recovery

No-steal, no-force:

- Uncommitted dirty pages are never flushed.
- Committed pages may still sit only in the buffer; redo repairs them.

On `Open`:

1. Scan WAL from the redo LSN. Truncate a torn tail.
2. Analysis: transactions with a `Commit` record are committed.
3. Redo committed `PageImage` records when `page.LSN < record.LSN` (or the page is missing / torn), and committed `PageDelta` records under the base check above.
4. Apply the latest committed `TreeMeta` and `AllocState` to the superblock.
5. Apply UNDO for transactions that never committed: those still open at the crash, and those with an `Abort` record (`recovery.NotCommittedUntil`). Both are registered as aborted with the transaction manager, which otherwise defaults an unknown id to committed. UNDO only reverses versions still carrying the transaction's id, so an aborted transaction whose rollback had already finished is a no-op. Without this, a rollback that had not yet reached the data file came back committed: half a transfer (`tests/crash/rollback_delta_test.go`).
6. Resume LSNs after the last complete record.

The scanner uses sealed segment boundaries to skip a retained segment whose
successor has a valid CRC/identity-checked header and starts at or before the
redo LSN. Such a segment cannot contribute a record to this recovery pass; it
remains on disk unchanged for PITR and on-demand page repair. This makes
restart work proportional to the redo suffix, rather than all retained
historical WAL.

A later live read of a smashed page that is past the redo LSN still calls `recovery.RepairPage`, which scans retained segments from LSN 1 for the latest committed image (`docs/storage-format.md`).

## Crash points

`wal.Injector` can fire once at: WAL write, WAL sync, commit record, split, checkpoint, page flush, rotation, rollback, insert, update, delete.

Index-build crash injection waits until secondary indexes exist.

## Superblock hooks

Previously reserved superblock bytes store `CheckpointLSN` (offset 116) and `RedoLSN` (offset 124). Zeros mean “no checkpoint”; format version is unchanged.
