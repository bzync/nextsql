# Storage format

NextSQL uses a native, versioned, little-endian binary storage format (**v1**). Go structs are never serialized directly to disk, and unknown format or descriptor versions fail closed.

```arch
Physical Page (16,448 bytes on disk)
┌───────────────────────────┬───────────────────────────┬──────────────────┬──────────────┐
│ Envelope Header (32 B)    │ Ciphertext (16,384 B)     │ GCM Tag (16 B)   │ Pad (16 B)   │
│ ver · suite · key · nonce │ Logical 16 KiB Slotted Pg │ Auth Tag         │ Zero         │
└───────────────────────────┴───────────────────────────┴──────────────────┴──────────────┘
```

## Page geometry & constants

| Name | Value | Notes |
|---|---|---|
| Logical page size | `16,384` bytes (16 KiB) | Plaintext slotted page after decryption |
| Physical page size | `16,448` bytes | `16384 + 32 (header) + 16 (GCM tag) + 16 (pad)` |
| Page header | `48` bytes | Logical slotted page header |
| Slot entry | `4` bytes | `offset` (`u16`) + `length` (`u16`) |
| Superblock header | `256` bytes | Page ID `0` (plaintext metadata + HMAC key check) |
| Magic | `NSQL` | 4-byte ASCII magic |

Page ID `0` is the superblock. User data, catalog, index, and freelist pages start at page ID `1`. The byte offset of page `id` inside the data file is `id * 16448`.

---

## Superblock (page 0)

Page `0` stores file identity, allocator pointers, checkpoint boundaries, and an HMAC-SHA256-128 key verification tag. It never holds user rows.

| Offset | Size | Field |
|---|---|---|
| `0` | 4 | Magic `NSQL` |
| `4` | 2 | Format version (`1`) |
| `6` | 2 | Flags |
| `8` | 16 | Database UUID |
| `24` | 16 | File UUID |
| `40` | 4 | Logical page size (`16384`) |
| `44` | 4 | Physical page size (`16448`) |
| `48` | 2 | Cipher suite (`1` = AES-256-GCM) |
| `50` | 2 | Envelope version (`1`) |
| `52` | 4 | Key version |
| `56` | 8 | Next page ID (allocator high-water mark) |
| `64` | 8 | Freelist head page ID (`0` = none) |
| `72` | 8 | Freelist count |
| `80` | 8 | Next nonce generation (exclusive high-water mark) |
| `88` | 8 | Created timestamp (Unix nanoseconds) |
| `96` | 4 | Wrapped-DEK hook length |
| `100` | 16 | File auth tag (`HMAC-SHA256-128` over identity \|\| key version) |
| `116` | 8 | Checkpoint LSN (`0` = none) |
| `124` | 8 | Redo LSN (`0` = none) |
| `236` | 8 | Primary B+Tree root page ID (`0` = none) |
| `244` | 2 | Primary B+Tree height (`0` = none) |
| `252` | 4 | CRC32C of bytes `[0:252]` |

The file authentication tag uses the page DEK. A stolen data file without the key material cannot be opened.

---

## Encrypted page envelope (pages ≥ 1)

Every user page, catalog page, index node, and freelist page is sealed in an authenticated AES-256-GCM envelope:

| Offset | Size | Field |
|---|---|---|
| `0` | 2 | Envelope version (`1`) |
| `2` | 2 | Cipher suite (`1` = AES-256-GCM) |
| `4` | 4 | Key version |
| `8` | 8 | Page ID |
| `16` | 12 | Nonce (`generation u64` \|\| `key_version u32`) |
| `28` | 4 | Reserved flags (`0`) |
| `32` | 16,384 | Ciphertext of the 16 KiB logical page |
| `16416` | 16 | AES-GCM authentication tag |
| `16432` | 16 | Zero padding |

* **AAD (Additional Authenticated Data):** envelope version, cipher suite, key version, and page ID. Relocating a valid page ciphertext to a different page ID fails authentication immediately.
* **Crash-safe nonces:** Generation `0` is reserved. The superblock reserves nonce generations in batches of `4,096` and flushes the high-water mark to disk **before** any generation in the batch is used. A crash may skip unused generations, but never reuses a nonce.

---

## Logical slotted page (16 KiB)

After AES-256-GCM decryption, the 16,384-byte logical page has a 48-byte header:

| Offset | Size | Field |
|---|---|---|
| `0` | 4 | Magic `NSQL` |
| `4` | 2 | Format version (`1`) |
| `6` | 2 | Page type (`1` superblock, `2` slotted data, `4` freelist, `5` B+Tree leaf, `6` B+Tree internal) |
| `8` | 8 | Page ID |
| `16` | 8 | LSN of the WAL record that last modified the page |
| `24` | 8 | Transaction metadata (freelist uses this as next-page link) |
| `32` | 2 | Slot count |
| `34` | 2 | `Lower` (end of slot directory) |
| `36` | 2 | `Upper` (start of record payloads) |
| `38` | 2 | Flags |
| `40` | 4 | CRC32C of the logical page (with bytes `[40:44]` zeroed) |
| `44` | 4 | Reserved |

The slot directory starts at byte `48` and grows upward (`Lower`), while variable-length records grow downward from `16384` (`Upper`). Free space is the contiguous interval `[Lower, Upper)`. Deleted slots are marked `(offset=0, length=0)` so slot indexes remain stable across in-page compaction.

---

## Page allocator & reclamation

The allocator tracks a high-water `NextPageID` alongside a linked chain of encrypted `PageTypeFreeList` (`4`) pages.

* **Fail-closed validation:** On open, the allocator walks and validates the entire freelist chain before handing out any page ID. It rejects cycles, wrong page types, duplicate IDs, page ID `0`, IDs `>= NextPageID`, metadata pages listed as free, or count mismatches against the superblock.
* **No live rollback reload:** In-process transaction rollback never reloads the in-memory freelist from disk (which would reissue pages taken by concurrent writers or linked by committed structural splits). Startup also runs a fast internal-page ownership guard (`FreelistConflicts`) and refuses to open if a live tree page appears on the freelist. Offline inspection is available via `nextsql check-pages`.
* **Ownership-verified reclamation:** `DROP TABLE`, `DROP INDEX`, and blocking index rebuilds walk the exact tree page set via `btree.Tree.OwnedPages` and reclaim pages only after the catalog commit succeeds and older snapshots drain.
* **Preallocation runway:** By default, the allocator preallocates `16,384` pages (~256 MiB) ahead of the high-water mark using `fallocate` so bulk writes do not extend the file on every page allocation. Configure this with `prealloc_ahead_pages` in `nextsql.conf` or `--prealloc-ahead-pages` on `nextsql init` / `setup`.

---

## Corruption detection, isolation, and repair

Every page read verifies both the outer AES-GCM authentication tag and the inner logical CRC32C checksum:

```text
detect → isolate → fail safely → recover
```

1. **Detect:** AEAD authentication, CRC32C, page ID, or slotted-page bounds validation fails.
2. **Isolate:** The damaged page ID and reason code are recorded in the sidecar `nextsql.db.isolated` (`NSQI` v1). Corrupt bytes are never cached in the buffer pool or returned to callers.
3. **Fail safely:** Queries touching the damaged page receive `corruption`, while unaffected pages remain readable.
4. **Recover:** Live page repair (`recovery.RepairPage`) scans retained WAL segments from LSN 1 and rewrites the page from the latest committed `PageImage` / `PageDelta` chain.

---

## Catalog & sidecar descriptors

All schema, statistics, and automation metadata live inside the encrypted catalog B+Tree (or authenticated sidecars) using explicit versioned binary codecs:

| Magic | Current Version | Readable Window | Purpose |
|---|---|---|---|
| `NSCT` | v13 (v14 with `CHECK`) | v1..v14 | Table & index descriptors (columns, FKs, CDC policy, partitioning, HNSW/IVF/IVF-PQ, full-text analyzers, `ENCRYPTED CLIENT`, `ENUM`, `STRUCT`/`ARRAY`/`MAP`, `CHECK`) |
| `NSST` | v3 | v1..v3 | Table, column, index, vector, and partition row-count statistics |
| `NSPS` | v1 | v1 | Per-partition column, index, and vector distribution sketches |
| `NSWK` / `NSTG` / `NSSC` / `NSTK` | v2 / v1 / v1 / v1 | v1..v2 / v1 | Workflows, triggers, schedules, and durable task descriptors |
| `NSID` / `NSIR` | v1 | v1 | Durable mutation idempotency fences and replay payloads |
| `NSKY` / `NSRK` | v1 | v1 | External 46-byte root unlock key (`NSKY`) and recovery key (`NSRK`) |
| `NSKS` | v1 (v2 with recovery key) | v1..v2 | Wrapped KEK / master / domain DEK keystore (`nextsql.db.keys`) |
| `NSRE` / `NSRM` | v1 / v3 | v1 / v1..v3 | Encrypted deployment registry (`nextsql.instance`) |

---

## Online catalog compatibility & upgrade strategy

NextSQL separates **catalog evolution** from **physical page layout**:

* **Catalog descriptors migrate online automatically:** Every `NSCT` and `NSST` revision decodes all prior versions with safe defaults for newer fields. A descriptor is written at the lowest version required to express its features (for example, `NSCT` v14 is written only when a table declares a `CHECK` constraint), preserving rollback compatibility with older binaries whenever new features are unused.
* **Retained release fixtures:** Compatibility is enforced in CI by `tests/upgrade` (`make test-upgrade`), which replays real `clean` and `SIGKILL`-interrupted `dirty` data directories generated by every prior release (`v0.0.1` through `v0.0.10`).

Engine note: [`docs/storage-format.md`](https://github.com/bzync/nextsql/blob/master/docs/storage-format.md).
