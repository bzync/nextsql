# Transactions and MVCC (Phase 4)

Undo-oriented MVCC on the Phase 3 WAL. Concurrent transactions take row and range locks. Readers reconstruct older versions from the encrypted UNDO log.

```
Current row → undo record → previous version → older version
```

## Isolation

| Level | Snapshot | Locks |
|---|---|---|
| READ COMMITTED | Refreshed on each statement | Writers take exclusive key locks until end of transaction; a live writer's row is never overwritten |
| SNAPSHOT | Taken at `Begin` | Writers take exclusive key locks; first-committer-wins on write-write; a live writer's row is never overwritten |
| SERIALIZABLE | Taken at `Begin` | Snapshot plus shared key locks on point reads and shared range locks on scans (strict 2PL) |

Serializable is lock-based, not SSI. Anomaly tests cover dirty read, non-repeatable read, phantoms, write skew, and deadlock. Do not describe it as snapshot isolation.

### Dirty writes are refused at every level

A write whose target row's current version was written by a **different
transaction that is still running** fails with `serialization` ("write-write
conflict") — at every isolation level, including READ COMMITTED. Overwriting
such a row would be a dirty write: the other transaction may still abort, in
which case the value never existed, or commit, in which case its write is lost
with nothing reported to either side.

This check does not depend on key locks, and cannot: `lockWrite` skips locking
below SERIALIZABLE when only one transaction is live *at that moment*, so the
first writer of a row often leaves no lock behind for a later writer to block
on. It is also separate from the snapshot first-committer-wins check below it,
which fires only once the other writer has **committed** — an in-progress
writer is invisible to that test. Under SERIALIZABLE the key lock still takes
effect first, so the second writer waits rather than failing fast.

`TM.Status` reports `StatusInProgress` only for a transaction the manager still
holds as active; an id it no longer knows reads as committed, so an old version
can never be mistaken for a live writer. A transaction writing the same row
twice never conflicts with itself.

A deadlock in the wait-for graph aborts the requester (`nerr.Deadlock`). The aborted transaction must roll back so waiters can proceed.

### Foreign-key locks (even under SNAPSHOT / READ COMMITTED)

Heap writers exclusive-lock only the keys they write, and `btree.Txn.lockWrite` skips when `Iso < Serializable` and this is the only live writer. Child `INSERT` and parent `DELETE` therefore do not conflict on their own PKs. FK enforcement takes extra locks on the **referenced parent key** by calling `txn.Manager.LockKey` directly (never `lockWrite` / `lockRead`):

| Statement | Lock | Key identity |
|---|---|---|
| Child `INSERT` / `UPDATE` of a fully non-null `MATCH SIMPLE` key | Shared | Parent PK (`types.EncodeKey(parent.PKValues)`) or the unique-index key `indexKV` would insert |
| Parent `DELETE` / `UPDATE` of referenced columns | Exclusive | Same bytes as above |

After the lock is held, existence and inbound-child probes use a **probe-local** snapshot `TM.Capture(h.ID)` via `btree.Txn.LookupAt` / `RangeAt`. They do **not** read `h.Snap` and must not `Refresh(h)` — that would turn the rest of a user SNAPSHOT transaction into READ COMMITTED. `Capture` still sees this transaction’s own writes and later-committed rows, so `BEGIN SNAPSHOT` then a concurrent child `INSERT` `COMMIT` cannot let the snapshot `DELETE` the parent and orphan the child.

`CASCADE` / `SET NULL` / `SET DEFAULT` run on the leader as recursive `removeRow` / `replaceRow` under the same probe-local snapshot and referenced-key locks. Followers never interpret FK actions. Depth (`MaxFKDepth` = 8) and fan-out (`MaxFKTouchedRows` = 100 000) caps fail closed with `exhausted`.

## Row header (`NSRV`)

Clustered leaf values are wrapped:

| Offset | Size | Field |
|---|---|---|
| 0 | 4 | Magic `NSRV` |
| 4 | 1 | Version (`1`) |
| 5 | 1 | Flags |
| 6 | 8 | `xmin` (creator transaction) |
| 14 | 8 | `xmax` (deleter / replacer; `0` if live) |
| 22 | 8 | Undo id of the previous version (`0` = none) |
| 30 | … | User payload |

Unprefixed values are pre-Phase-4 rows and are treated as always-visible committed data.

Visibility: if `xmin` is not seen, walk UNDO (a `KindInsert` predecessor means the row does not exist). If `xmin` is seen and `xmax` is unseen or zero, the version is visible. A seen `xmax` is a committed delete or replace for this snapshot.

## UNDO log

A data file `foo.db` owns `foo.db.undo/`, encrypted with an UNDO DEK wrapped under the page-key provider (`crypto.DomainUNDO` = `'U'`). Stolen undo files are unreadable without that key material.

| Name | Role |
|---|---|
| `control` | Next undo id, nonce high water, wrapped UNDO DEK |
| `undo.log` | Append-only encrypted records |

Each undo record stores the previous row version for one key (`insert` / `update` / `delete`) and the previous undo id of the same transaction. WAL type `RecUndo` (11) records the undo id so recovery can find the chain.

Deletes write a tombstone (`xmax` set). Immediate purge is eligible only after
commit and when there are no other writer **or read-only** snapshots. Otherwise
the durable tombstone remains available for old-version reconstruction.

`DB.CleanupDeadVersions(limit)` performs bounded deferred cleanup across the
catalog, heaps, and indexes. It takes the database apply barrier, refuses to run
if any unguarded transaction remains, discovers committed tombstones from the
tree itself (so restart cannot lose pending work), and forgets the now-ineligible
transaction's in-memory UNDO chain. The encrypted `undo.log` remains append-only;
database-wide maintenance atomically rewrites it with only retained chains.
Record IDs/links remain stable while ciphertext uses fresh reserved nonces; the
temporary file and directory are synced around replacement. Rewrite bytes are
preflighted against the maintenance I/O budget and temporary ciphertext buffers
are memory-budgeted. Table/index-scoped maintenance never rewrites global UNDO.
Physical deletion uses the normal B+Tree underflow path, including sibling merge,
root collapse, and durable page return to the allocator. Full-text posting,
document-length, and statistics records use the same eligibility and cleanup path.

## Recovery

1. REDO committed `PageImage` / tree / allocator records (Phase 3).
2. Apply UNDO for transactions that never committed: those still open at the crash and those with an `Abort` record (`recovery.NotCommittedUntil`, `docs/wal.md`).

### UNDO durability

Undo state is **not** re-derivable from redo. A logged page image -- a
commit's, or the system transaction that logs a split -- carries every row
version on its page, including other transactions' uncommitted ones, and only
their undo records hold the values those versions replaced. If redo installs
such an image and the undo record is gone, recovery cannot put the old value
back.

Two rules keep that from happening:

1. **Written before the image is logged.** Btree appends a version's undo
   record before it changes the page, and `Engine.appendPageStatesLocked`
   writes the undo buffer after the images are copied and before they are
   appended to the WAL (`tests/crash`
   `TestPageImageDoesNotOutrunTheUndoItCarries`).
2. **Durable before the WAL writes.** The WAL runs `undo.Log.Sync` before it
   writes any record to a segment (`wal.Log.SetBeforeWrite`). It must precede
   the write, not merely the WAL `fsync`: recovery replays a committed image
   found on disk whether or not its `fsync` returned, and writeback can put WAL
   bytes on disk at any time. `Sync` fsyncs only when unsynced undo bytes
   exist, and runs without the log's mutex so appends continue
   (`TestPageImageDoesNotOutrunTheUndoItCarriesAcrossPowerLoss`).

A failed undo write or `fsync` latches, as the WAL's barriers do: every later
`Sync`, and so every WAL write, fails until restart. `undo.Open` cuts a torn
tail — a partial record left by power loss — before appending; previously new
records landed past the point replay stops and were lost on the next open.

**Cost.** Roughly one extra `fsync` per commit group: measured p50 on ext4,
single-connection update 2.45 → 3.6 ms and insert 1.26 → 2.4 ms, 16
connections 2.4–2.5 → 4.6 ms (`TODO.md` log #301). Carrying the replaced
version in the WAL's `Undo` record would let WAL order alone provide this,
and would also give followers the undo they currently lack
(`docs/production/GAPS.md`).

In-process rollback applies the transaction's undo chain, then restores pages that only that transaction dirtied.

## API

```text
tr.BeginTxn(iso) → *btree.Txn
tx.Insert / Update / Delete / Lookup / Range
tx.LookupAt / RangeAt / InsertAt / UpdateAt / DeleteAt (caller snapshot; no RC refresh)
tx.Commit / Rollback
```

`Tree.Insert` / `Update` / `Delete` and `Tree.Begin` are snapshot-isolation auto-commit and multi-statement wrappers around the same path.

## Packages

| Package | Role |
|---|---|
| `internal/txn` | Isolation, snapshots, lock table, deadlock detection |
| `internal/undo` | Encrypted undo log + recovery apply |
| `internal/storage/row` | `NSRV` encode / decode |
