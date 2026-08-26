# NextSQL storage format (v1)

This document describes the Phase 1–3 on-disk layout. Every field is little-endian unless noted. Do not serialize Go structs directly. WAL records live in `docs/wal.md`.

Treat these encodings as long-lived. Bump `CurrentFormatVersion` / `CurrentEnvelopeVersion` for incompatible changes.

## Constants

| Name | Value |
|---|---|
| Logical page size | 16384 bytes |
| Physical page size | 16448 bytes |
| Page header | 48 bytes |
| Slot entry | 4 bytes |
| Envelope header | 32 bytes |
| AES-GCM tag | 16 bytes |
| Envelope pad | 16 bytes |
| Superblock header | 256 bytes |
| Magic | ASCII `NSQL` |

```
PhysicalPageSize = 16384 + 32 + 16 + 16 = 16448
```

Page ID `0` is the superblock. User data pages start at ID `1`.

File offset of page `id` is `id * 16448`.

## Superblock (page 0, not user data)

The superblock is plaintext metadata plus an HMAC-SHA256 key-check. It does not contain user rows.

| Offset | Size | Field |
|---|---|---|
| 0 | 4 | Magic `NSQL` |
| 4 | 2 | Format version (`1`) |
| 6 | 2 | Flags |
| 8 | 16 | Database UUID |
| 24 | 16 | File UUID |
| 40 | 4 | Logical page size (must be 16384) |
| 44 | 4 | Physical page size (must be 16448) |
| 48 | 2 | Cipher suite (`1` = AES-256-GCM) |
| 50 | 2 | Envelope version (`1`) |
| 52 | 4 | Key version |
| 56 | 8 | Next page ID (allocator high water) |
| 64 | 8 | Freelist head page ID (`0` = none) |
| 72 | 8 | Freelist count |
| 80 | 8 | Next nonce generation (exclusive high water) |
| 88 | 8 | Created timestamp (unix nano) |
| 96 | 4 | Wrapped-DEK hook length (0 in Phase 1) |
| 100 | 16 | File auth tag (HMAC-SHA256-128 of identity \|\| key version) |
| 116 | 8 | Checkpoint LSN (`0` = none) |
| 124 | 8 | Redo LSN (`0` = none) |
| 132 | 104 | Reserved (zero), including wrapped-DEK hook |
| 236 | 8 | Primary B+Tree root page ID (`0` = none) |
| 244 | 2 | Primary B+Tree height (`0` = none) |
| 246 | 2 | Primary B+Tree flags (zero) |
| 248 | 4 | Reserved (zero) |
| 252 | 4 | CRC32C of bytes `[0:252]` |

The remainder of the 16448-byte physical slot is zero.

HMAC uses the page DEK. A stolen data file without the key file cannot be opened. This is not the page AEAD.

## Encrypted page envelope (pages ≥ 1)

User pages, indexes, freelist pages, and later WAL-backed structures use this envelope. Production files never store those pages as readable plaintext.

| Offset | Size | Field |
|---|---|---|
| 0 | 2 | Envelope version (`1`) |
| 2 | 2 | Cipher suite (`1` = AES-256-GCM) |
| 4 | 4 | Key version |
| 8 | 8 | Page ID |
| 16 | 12 | Nonce |
| 28 | 4 | Reserved flags (zero) |
| 32 | 16384 | Ciphertext of the logical page |
| 16416 | 16 | GCM authentication tag |
| 16432 | 16 | Pad (zero) |

**AAD** (authenticated, not encrypted): envelope version, cipher suite, key version, page ID.

**Nonce** (12 bytes): `generation` (uint64) || `key version` (uint32).

Generation `0` is reserved. The superblock reserves generations in batches of 4096 and persists the high water **before** any generation in the batch is used. A crash may skip unused generations; it must not reuse one.

## Logical slotted page (16 KiB plaintext after decrypt)

| Offset | Size | Field |
|---|---|---|
| 0 | 4 | Magic `NSQL` |
| 4 | 2 | Format version (`1`) |
| 6 | 2 | Page type |
| 8 | 8 | Page ID |
| 16 | 8 | LSN of the WAL record that last modified the page (`0` if never logged) |
| 24 | 8 | Transaction metadata (hook; freelist uses this as next-page link) |
| 32 | 2 | Slot count |
| 34 | 2 | Lower (end of slot directory) |
| 36 | 2 | Upper (start of records) |
| 38 | 2 | Flags |
| 40 | 4 | CRC32C of the page with this field zeroed |
| 44 | 4 | Reserved |

Slot directory starts at offset 48 and grows forward. Each slot is `offset` (uint16) + `length` (uint16). Records grow backward from 16384. Free space is `[lower, upper)`.

A deleted slot is `(offset=0, length=0)`. Slot indexes are stable across compact.

### Page types (v1)

| Value | Type |
|---|---|
| 0 | Invalid |
| 1 | Superblock (not used inside an envelope) |
| 2 | Slotted / data |
| 3 | Free (reserved) |
| 4 | Freelist |
| 5 | B+Tree leaf |
| 6 | B+Tree internal |

B+Tree node and record encodings are in `docs/btree.md`. Do not recycle these values.

## Cipher suite

| ID | Algorithm |
|---|---|
| 1 | AES-256-GCM (Go `crypto/aes` + `cipher.NewGCM`) |

No custom ciphers. Suite IDs are versioned so approved alternatives can be added later.

## Key file (`NSKY`)

Phase 1 stand-in for a `KeyProvider`. Not a connection URL. Mode `0600`. Keep it off the data volume.

| Offset | Size | Field |
|---|---|---|
| 0 | 4 | Magic `NSKY` |
| 4 | 2 | Version (`1`) |
| 6 | 4 | Key version |
| 10 | 32 | AES-256 DEK |
| 42 | 4 | CRC32C |

Phase 13: `--key-file` is the external **root unlock key**. The sidecar
`nextsql.db.keys` (`NSKS` v1) stores KEK / master / domain DEKs wrapped under
the hierarchy in `docs/security.md`. The raw root is never in the data
directory. Legacy databases without a keystore still treat `NSKY` as the page DEK.

## Allocator

High-water `NextPageID` plus a chain of `PageTypeFreeList` pages. Each freelist page stores 8-byte page IDs as slotted records. `TxnMeta` is the next freelist page ID (`0` if none).
Allocated freelist metadata pages remain linked even when the free-ID count
shrinks, preventing the unused metadata tail from becoming unreachable.

`btree.Tree.OwnedPages` performs the fail-closed ownership walk used by storage
reclamation. It returns the detached metadata page plus every internal and leaf
page, rejects cycles/duplicate children and wrong page types, and is stable
across restart. Reclamation must use this exact ownership set; it must not infer
ownership merely from a page type.

Committed `DROP INDEX`, blocking index replacement, and `DROP TABLE` queue
these exact ownership sets while the catalog transaction is active. After the
catalog commit, reclamation takes the exclusive transaction/apply guard, waits
for older snapshots to drain, evicts the pages from the buffer, adds them to
the allocator freelist, flushes the encrypted freelist, and syncs the file.
Rollback discards the queue. A reclamation failure leaves the logical DDL
committed and is exposed by `executor.DB.LastReclaimError`; it never makes a
page eligible by guessing.

## Checksums and failure

- Superblock: CRC32C. Mismatch fails closed.
- Logical page: CRC32C after decrypt. Mismatch fails closed.
- Envelope: AES-GCM tag. Tamper, truncate, wrong key, or page-ID mismatch fails closed.
- Never return a known-corrupt record.

On a user-page integrity failure (`Engine.Pin`):

```text
detect → isolate → fail safely → recover
```

1. **Detect.** AEAD, checksum, page-ID, or slotted-page validation fails.
2. **Isolate.** The page ID is recorded in the sidecar `nextsql.db.isolated` (`NSQI` v1). The bad bytes are never cached or returned.
3. **Fail safely.** Callers receive `corruption`. Other pages stay readable. Superblock and durable-WAL-prefix failures still fail closed for the whole file.
4. **Recover.** The latest committed WAL `PageImage` for that ID is rewritten (scan from LSN 1, so a smash after checkpoint can still be rebuilt from retained segments). Uncommitted images are ignored. If no valid image exists, the page stays isolated until restore.

`NSQI` holds page IDs and reason codes only. Mode `0600`. A damaged sidecar is ignored and rebuilt on the next detect. `nextsql diagnose` / `status` report `isolated_pages`.

## Catalog descriptors (`NSCT`)

Table descriptors live in the primary tree (key `T` + name). Magic `NSCT`.

| Version | This binary |
|---|---|
| 1 | Readable. Foreign-key list is empty. Extra bytes after optional `VecMeta` are ignored. |
| 2 | Readable. v1 payload, then bounded foreign-key descriptors. CDC image policy defaults to keys. |
| 3 | Readable. v2 payload, then validated `u8` CDC image policy (`0` keys, `1` full). |
| 4 | Current write format. v3 payload, then a bounded physical-partition descriptor. Leftover bytes are `invalid_format`. |
| other | Fail closed (`unsupported catalog version`). |

Compatibility window (`internal/upgrade` `FamilyCatalog`): current 4, readable 1..4. `nextsql diagnose` prints that window. Old binaries cannot open v4 rows. Any catalog rewrite upgrades a readable older descriptor to v4.

The v4 partition section begins with a kind byte (`0` none, `1` RANGE, `2`
HASH, `3` LIST, `4` TENANT). A nonzero kind carries at most 8 column ordinals
and 1024 partition members. Every member has a stable nonzero identity, a
bounded name, detached heap/vector metadata roots, range flags, hash
modulus/remainder, partition-local index roots, and typed routing tuples. The
whole descriptor accepts at most 4096 routing tuples. Decoding rejects unknown
kinds/flags, truncation, duplicate identities/names/rules, incomplete hash
remainder sets, overlapping ordered ranges, NULL or mistyped values, invalid
tenant keys, and missing partition-local index roots.

This metadata is stored inside the encrypted catalog B+Tree and therefore uses
the existing page AEAD, WAL transaction, backup/restore, PITR, and Raft page
replication mechanisms. P21 user-facing SQL DDL and physical row routing are
enabled only for the bounded single-column RANGE/HASH/LIST/TENANT slice;
broader lifecycle semantics remain explicitly gated. NSCT v4 HASH routing is
SHA-256 over the canonical typed tuple key; the first 64 digest bits are read
big-endian and reduced modulo the descriptor's complete remainder set.

Each index record is `name`, flags `u8`, meta page, column ordinals, and optional JSON path. Flag bits 1/2/4/8/16 are unique / spatial / path / full-text / vector. Bits 32/64/128 add `INCLUDE` ordinals, a binary `WHERE` expression, and parallel expression keys with stored result types. Indexes without those bits keep the previous layout so older v2 rows still decode. A descriptor that sets the new bits is unreadable by binaries that do not know them (misaligned index records, then `trailing catalog bytes` or `invalid_format`).

## P19 automation catalog

Automation metadata is stored in the same encrypted/authenticated catalog
B+Tree and participates in ordinary transaction WAL, backup/restore, PITR, and
Raft page-image replication. These are the first intended shipped versions of
the P19 descriptor families:

| Key family | Descriptor/index | Magic/version |
|---|---|---|
| `W` | workflow descriptor | `NSWK` v2 (v1 readable) |
| `G` | trigger descriptor | `NSTG` v1 |
| `Q` | schedule descriptor | `NSSC` v1 |
| `R` | schedule next-fire index | raw ordered key, descriptor validated on reload |
| `K` | task descriptor | `NSTK` v1 |
| `L` | task due/lease index | raw ordered key |
| `M` | active concurrency index | raw source/stable-ID key |
| `N` | terminal retention index | raw ordered key |
| `O` | active task workflow-dependency index | raw workflow-ID key |
| `P` | task owner pagination index | raw length-prefixed owner key |

Descriptors use explicit field encoders and bounded literal-expression codecs;
raw Go structs are never serialized. Workflow, trigger, schedule, and task
decoders reject unsupported versions, truncation, trailing bytes, invalid enum
values, excessive text/argument counts, and inconsistent identities or time
state. Catalog reload validates schedule keys, stable workflow references, and
the exact one-to-one next-fire index for every enabled schedule. Runtime index
lookups validate their primary descriptor before acting and fail closed on a
dangling or mismatched entry.

The raw secondary indexes contain identifiers and times only. They inherit the
catalog page encryption envelope, key version, authenticated page integrity,
rotation, backup, restore, and replication behavior; they are not plaintext
sidecar files. Task arguments remain solely in the encrypted `NSTK` descriptor
and are excluded from task introspection and audit/error metadata.

## What this version does not store

SQL or replication metadata as standalone files. User JSON lives inside encrypted `NSRW` row payloads as `NSJB` (`docs/json.md`). Full-text inverted indexes are detached encrypted B+Trees (`docs/fulltext.md`). Vector payloads (`NSVV`) and HNSW graphs (`NSHM`) are detached encrypted B+Trees (`docs/vector.md`). WAL records are in the sibling `*.wal/` directory (`docs/wal.md`). UNDO and MVCC headers are documented in `docs/mvcc.md`.
