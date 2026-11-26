# MVCC and UNDO

NextSQL implements **undo-oriented Multi-Version Concurrency Control (MVCC)** integrated with the [write-ahead log](/docs/wal) and [clustered B+Tree](/docs/btree).

When a transaction updates or deletes a row, the latest version is written in place on the clustered B+Tree leaf with an updated `NSRV` header, while the displaced row version is appended to an encrypted **UNDO log** (`nextsql.db.undo/`). Readers whose snapshot precedes the write reconstruct the visible historical version by walking the UNDO chain.

```arch
Clustered B+Tree Leaf                       Encrypted UNDO Log (nextsql.db.undo/)
┌──────────────────────────────────┐        ┌──────────────────────────────────┐
│ NSRV Header (30 bytes)           │        │ Undo Record #42                  │
│   xmin: 108  xmax: 0             │        │   kind: Update  prev_undo: #19   │
│   undo_id: 42 ───────────────────┼───────►│   prior NSRV + NSRW payload      │
│ Current NSRW Payload             │        └─────────────────┬────────────────┘
└──────────────────────────────────┘                          │
                                                              ▼
                                            ┌──────────────────────────────────┐
                                            │ Undo Record #19                  │
                                            │   kind: Insert  prev_undo: 0     │
                                            └──────────────────────────────────┘
```

---

## Row version header (`NSRV`)

Every clustered B+Tree leaf value is prefixed with a 30-byte `NSRV` header:

| Offset | Size | Field |
|---|---|---|
| `0` | 4 | Magic `NSRV` |
| `4` | 1 | Format version (`1`) |
| `5` | 1 | Flags |
| `6` | 8 | `xmin` — Transaction ID that created this version |
| `14` | 8 | `xmax` — Transaction ID that deleted or replaced this version (`0` if live) |
| `22` | 8 | `undo_id` — Pointer to the previous version in `undo.log` (`0` if none) |
| `30` | `…` | Encoded SQL row payload (`NSRW`) |

### Snapshot visibility rules

Given a reader's snapshot `S`:

1. If `xmin` is **not visible** in `S` (the creator transaction was still in flight when `S` was taken, or aborted), the reader follows `undo_id` backward through `undo.log` until it reaches a version whose `xmin` is visible in `S` (or a `KindInsert` record, which indicates the row did not exist in `S`).
2. If `xmin` **is visible** in `S` and `xmax` is `0` (or `xmax` is not visible in `S`), the current version is visible.
3. If `xmax` **is visible** in `S`, the row was deleted or replaced before `S` and is invisible.

---

## Isolation levels & concurrency control

| Isolation Level | Snapshot Timing | Locking & Conflict Semantics |
|---|---|---|
| `READ COMMITTED` | Refreshed at the start of each statement | Writers acquire exclusive key locks held until transaction end; overwriting an uncommitted live writer fails immediately. |
| `SNAPSHOT` *(default)* | Captured once at `BEGIN` | Writers acquire exclusive key locks; **first-committer-wins** aborts concurrent transactions that modify the same key with `serialization`. |
| `SERIALIZABLE` | Captured once at `BEGIN` | Strict two-phase locking (**2PL**): point lookups acquire shared key locks, range scans acquire shared range locks, and writes acquire exclusive locks. |

### Dirty-write prevention at all levels

At every isolation level—including `READ COMMITTED`—NextSQL prohibits dirty writes. If a statement attempts to update or delete a row whose current `xmin` or `xmax` belongs to another **currently active** transaction, the write fails closed with `serialization` (`write-write conflict`).

### Deadlock detection

The lock manager (`internal/txn`) maintains an explicit wait-for graph across key and range locks. Whenever a lock request would introduce a cycle, the requester is immediately aborted with `deadlock` (`ERR_DEADLOCK`) so waiting transactions can make progress. Configured `lock_timeout_ms` additionally bounds non-cyclic lock waits.

### Foreign-key locking & probe-local snapshots

Under `READ COMMITTED` and `SNAPSHOT`, ordinary reads do not take shared locks. To prevent concurrent parent `DELETE` and child `INSERT` statements from producing orphaned foreign keys across snapshot boundaries:

* **Child `INSERT` / `UPDATE`** acquires a **shared lock** on the referenced parent key in the lock table.
* **Parent `DELETE` / `UPDATE`** acquires an **exclusive lock** on the referenced parent key.
* **Probe-local snapshot:** Once the FK lock is acquired, parent existence and inbound child checks execute under a fresh probe-local snapshot (`TM.Capture`) via `LookupAt` / `RangeAt`. This observes any concurrently committed child or parent modifications without altering the user transaction's own `SNAPSHOT` isolation view.
* Cascading actions (`ON DELETE CASCADE`, `SET NULL`, `SET DEFAULT`) execute deterministically on the leader within bounded recursion caps (`MaxFKDepth = 8`, `MaxFKTouchedRows = 100,000`).

---

## Encrypted UNDO log & durability ordering

A database `nextsql.db` stores its undo history in `nextsql.db.undo/`:

| File | Role |
|---|---|
| `control` | Next `undo_id`, nonce high-water mark, and wrapped UNDO DEK (`crypto.DomainUNDO` = `'U'`) |
| `undo.log` | Append-only AES-256-GCM encrypted undo records |

### UNDO-before-WAL durability rule

Because physical WAL `PageImage` and `PageDelta` records capture pages that may contain uncommitted row versions from concurrent transactions, recovery relies on `undo.log` to reverse uncommitted modifications after a crash. NextSQL enforces a strict write ordering barrier:

1. **Append before page mutation:** A B+Tree writer appends its undo record before mutating the logical page in memory.
2. **Sync before WAL write:** The WAL calls `undo.Log.Sync` (`SetBeforeWrite`) **before** writing any WAL batch to a segment file. If an undo write or `fsync` fails, the barrier latches and prevents any dependent page image from reaching the WAL.

---

## Tombstones & bounded dead-version cleanup

Deleting a row writes a tombstone (`xmax` set) so concurrent snapshots can still walk its `undo_id` chain.

* **Immediate purge:** If no other read or write snapshot is active when the deleting transaction commits, the tombstone is removed immediately.
* **Deferred cleanup (`MAINTAIN DATABASE`):** `DB.CleanupDeadVersions` runs under the database apply barrier once older snapshots drain. It sweeps committed tombstones from catalog, heap, and index trees, returns underfull pages to the allocator, and atomically rewrites `undo.log` with fresh nonces to retain only live undo chains—all subject to the maintenance I/O and memory budgets.

Engine note: [`docs/mvcc.md`](https://github.com/bzync/nextsql/blob/master/docs/mvcc.md).
