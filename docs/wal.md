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
| `control` | Durable checkpoint / LSN / wrapped WAL DEK |
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

Redo uses `PageImage`, `TreeMeta`, `AllocState`, and `Checkpoint`. After redo, recovery applies UNDO for transactions with no commit or abort (`docs/mvcc.md`). Logical insert/update/delete records are durable physical-tree history. `Change` records are ignored by redo and consumed by CDC only after the matching durable `Commit`.

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

## Checkpoints

1. Flush committed dirty pages and `fsync` the data file.
2. Append a `Checkpoint` record and group-commit it.
3. Update the control file and the superblock checkpoint/redo LSNs.
4. Offer every segment, including the current one, to an optional `Archiver` (PITR hook). Segments are not deleted. The live segment may be re-archived after later appends.

An interrupted checkpoint leaves the previous control file in place. Recovery still starts from the last installed redo LSN.

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

Production pruning is disabled by default. `DB.SetWALRetentionHorizon(lsn)`
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

#### Automatic time-based retention (`wal_retention_ms`)

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

## Recovery

No-steal, no-force:

- Uncommitted dirty pages are never flushed.
- Committed pages may still sit only in the buffer; redo repairs them.

On `Open`:

1. Scan WAL from the redo LSN. Truncate a torn tail.
2. Analysis: transactions with a `Commit` record are committed.
3. Redo committed `PageImage` records when `page.LSN < record.LSN` (or the page is missing / torn).
4. Apply the latest committed `TreeMeta` and `AllocState` to the superblock.
5. Apply UNDO for transactions that began and never committed or aborted (`docs/mvcc.md`).
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
