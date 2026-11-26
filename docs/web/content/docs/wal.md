# WAL and crash recovery

Write-ahead logging (WAL) is mandatory in NextSQL. No transaction commit is acknowledged to the client until its WAL records have been appended, group-committed, and flushed to stable storage via `fdatasync` / `fsync`.

```arch
Transaction Commit
  ├── 1. Sync dirty UNDO records (undo.Log.Sync)
  ├── 2. Copy dirty page states (PageDelta v1 or PageImage) under pageMu
  ├── 3. Append Change (NSCD) + Commit records to WAL buffer
  ├── 4. Group-commit batch write + fdatasync (without holding engine mutex)
  └── 5. Release locks, advance visibility, acknowledge COMMIT
```

---

## Directory layout & encryption (`DomainWAL`)

A database file `nextsql.db` owns a sibling directory `nextsql.db.wal/`:

| File | Role |
|---|---|
| `control` | Durable checkpoint LSN, redo LSN, nonce high-water mark, and wrapped WAL DEK. Version `1` (full page images only) or `2` (supports `PageDelta` v1). Replaced atomically via `control.tmp` → `fsync` → rename → directory `fsync`. |
| `wal-<16 hex>.seg` | Encrypted 128 MiB WAL segment files (`NSWS` 64-byte header + `NSWL` records). |

WAL records are encrypted with a dedicated **WAL DEK** (`crypto.DomainWAL` = `'W'`), distinct from the page DEK. Nonce generations are reserved in batches of `4,096` in the `control` file before use.

---

## Record framing (`NSWL`) & payload types

Every physical WAL record is sealed with AES-256-GCM:

| Offset | Size | Field |
|---|---|---|
| `0` | 4 | Magic `NSWL` |
| `4` | 2 | Record version (`1`) |
| `6` | 2 | Cipher suite (`1` = AES-256-GCM) |
| `8` | 4 | WAL key version |
| `12` | 8 | LSN (1-based monotonically increasing sequence number) |
| `20` | 4 | Ciphertext length (payload + 16-byte GCM tag) |
| `24` | 12 | Nonce |
| `36` | 4 | CRC32C of header bytes `[0:36]` |
| `40` | `N` | Ciphertext \|\| 16-byte GCM tag |

Inside the decrypted payload (`Type u16`, `Flags u16`, `TxnID u64`, `PrevLSN u64`, `PageID u64`), NextSQL logs 13 record types:

| Type | Name | Purpose |
|---|---|---|
| `1` | `Begin` | Marks transaction start |
| `2`–`4` | `Insert` / `Delete` / `Update` | Logical B+Tree history records |
| `5` | `PageImage` | Full 16,384-byte logical page image |
| `6` / `7` | `Commit` / `Abort` | Transaction commit or rollback boundary |
| `8` | `Checkpoint` | Fuzzy redo LSN, durable LSN, allocator state, and primary tree root |
| `9` | `TreeMeta` | Root page ID and height update |
| `10` | `AllocState` | High-water `NextPageID`, freelist head, and freelist count |
| `11` | `Undo` | Pointer (`undo_id u64`) linking to the encrypted UNDO chain |
| `12` | `Change` | Versioned logical CDC row change (`NSCD` v1), written immediately before `Commit` |
| `13` | `PageDelta` | Compact byte-range diff against a page's previous logged state (`control` v2) |

---

## Page deltas (`PageDelta` v1)

Logging a full 16 KiB `PageImage` for every modified page amplifies single-row inserts to ~18 KB of WAL I/O. Instead of compressing pages before encryption (which leaks content characteristics via ciphertext length), NextSQL logs a deterministic **`PageDelta`** whenever a verified base image exists in the engine's logged-page LRU cache:

| Size | Field |
|---|---|
| `1` | Version (`1`) |
| `8` | Base LSN (the previous WAL record for this page) |
| `32` | SHA-256 of the 16 KiB base page (with LSN and CRC fields zeroed) |
| `2` | Run count |
| `…` | Sorted, non-overlapping changed byte runs (`u16 offset`, `u16 length`, `bytes`) |

* **Fail-closed replay:** During recovery or live page repair, a `PageDelta` is applied only if the target page is at the exact `BaseLSN` **and** its SHA-256 digest matches the base digest. Any mismatch fails closed with `corruption`.
* **Write reduction:** Single-row inserts average ~106-byte deltas, reducing total disk writes per insert from ~18.5 KB to ~2.4 KB.
* **Configuration:** Controlled by `wal_page_deltas` (`auto` default, `on`, `off`). Under `auto`, newly initialized databases use `control` v2 and log deltas, while existing v1 databases stay on v1 unless upgraded with `wal_page_deltas=on`.

---

## Group commit & durability latching

* **Lock-free flush window:** `wal.Log.Flush` writes buffered records and runs `fdatasync` without holding the log mutex or the storage engine mutex. Concurrent transactions continue appending into the next batch and are made durable together in a single kernel sync.
* **Nested-top-action structural logging:** When a B+Tree page split or merge allocates or frees pages, `Engine.LeaveOp` immediately logs the modified pages and allocator state as an atomic system transaction ahead of any user commit that routes through those pages.
* **Failed durability barriers latch:** On Linux, an `fsync` writeback error is reported once and cleared by the kernel. If any WAL or UNDO `fdatasync`/`fsync` fails (or partially writes), NextSQL **latches the log**: `DurableLSN` remains frozen at the last verified boundary and every subsequent `Append`, `Flush`, and `InstallCheckpoint` returns the latched error until the server restarts and recovers cleanly.

---

## Fuzzy checkpoints & recovery

NextSQL uses a **no-steal, no-force** buffer policy: uncommitted dirty pages are never flushed to the data file, and committed pages do not need to be flushed to the data file at `COMMIT` time.

### Fuzzy redo boundary

Periodic checkpoints (`checkpoint_interval_ms`, default 5 minutes) flush committed dirty pages and compute a safe `RedoLSN` as the minimum of:

1. The WAL end LSN when the page flush began;
2. The oldest logged change not yet stamped onto its buffer frame;
3. The oldest dirty buffer frame's first logged LSN (`recLSN`);
4. The `Begin` LSN of the oldest still-running transaction (so recovery never misses an in-flight transaction's `Begin` record).

### Crash recovery sequence

On `Open`:

1. **Validate segment continuity:** Verify that WAL segments cover `[RedoLSN, DurableLSN]`. A missing segment in that interval fails open closed rather than silently starting with missing commits.
2. **Scan from `RedoLSN`:** Skip sealed historical segments whose successor starts at or before `RedoLSN`. Truncate any torn tail **above** `DurableLSN` (any corruption at or below `DurableLSN` fails closed).
3. **REDO pass:** Replay committed `PageImage`, `PageDelta`, `TreeMeta`, and `AllocState` records in exact LSN order.
4. **UNDO pass:** Roll back all transactions that lack a `Commit` record (both transactions still open at the crash and transactions with an `Abort` record whose rollback had not yet flushed to the data file).

---

## WAL retention policies

By default, NextSQL retains all WAL segments indefinitely to support maximum local live page repair and point-in-time recovery. In production, configure one of two mutually exclusive retention policies in `nextsql.conf`:

| Policy | Settings | Behavior |
|---|---|---|
| **Size-capped retention** | `wal_max_retained_mb=4096` | Automatically trims closed segments older than `RedoLSN` (and any active CDC pins) after each periodic checkpoint and during `MAINTAIN DATABASE`. Minimum floor: `256` MiB. |
| **PITR time retention** | `wal_archive=/path/to/archive` + `wal_retention_ms=604800000` | Periodically advances the PITR retention horizon (`now - wal_retention_ms`). Closed segments older than both `RedoLSN` and the horizon are verified in the archive and pruned when `MAINTAIN DATABASE` runs. |

Monitor WAL footprint via `wal_on_disk_bytes`, `wal_segments`, and `wal_trimmed_segments` in `system.metrics`.

Engine note: [`docs/wal.md`](https://github.com/bzync/nextsql/blob/master/docs/wal.md).
