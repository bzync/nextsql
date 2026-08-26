# Transactions and MVCC (Phase 4)

Undo-oriented MVCC on the Phase 3 WAL. Concurrent transactions take row and range locks. Readers reconstruct older versions from the encrypted UNDO log.

```
Current row → undo record → previous version → older version
```

## Isolation

| Level | Snapshot | Locks |
|---|---|---|
| READ COMMITTED | Refreshed on each statement | Writers take exclusive key locks until end of transaction |
| SNAPSHOT | Taken at `Begin` | Writers take exclusive key locks; first-committer-wins on write-write |
| SERIALIZABLE | Taken at `Begin` | Snapshot plus shared key locks on point reads and shared range locks on scans (strict 2PL) |

Serializable is lock-based, not SSI. Anomaly tests cover dirty read, non-repeatable read, phantoms, write skew, and deadlock. Do not describe it as snapshot isolation.

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
2. Apply UNDO for transactions that began and never committed or aborted.

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
