# Clustered B+Tree (Phase 2)

The primary storage structure is a clustered B+Tree on the Phase 1 page engine. Leaves hold the row representation. Internal nodes hold separators and child page IDs.

Phase 3 wraps every mutating operation in a write-ahead transaction. Phase 4 stores an MVCC header on clustered rows (`docs/mvcc.md`) and reconstructs older versions from the UNDO log. Persistence of committed state after a crash is WAL redo plus UNDO of in-flight transactions (`docs/wal.md`). A clean `Close` still checkpoints and flushes pages.

## Catalog

One primary tree per data file (the catalog in Phase 5). The superblock stores:

| Offset | Size | Field |
|---|---|---|
| 236 | 8 | Primary root page ID (`0` = none) |
| 244 | 2 | Height (`0` if none, `1` = single leaf) |
| 246 | 2 | Flags (zero) |

These fields occupy previously reserved superblock space just before the CRC32C. They do not change the format version. User tables and secondary indexes are detached trees: each has a slotted meta page (`NSTM` + version + root + height) whose page ID is stored in the catalog. Splits update the meta page, not the superblock.

## Page types

| Value | Type |
|---|---|
| 5 | B+Tree leaf |
| 6 | B+Tree internal |

Existing types 0–4 are unchanged.

## Node header (slot 0)

Every tree page inserts a 28-byte header as slot 0 and never deletes it.

| Offset | Size | Field |
|---|---|---|
| 0 | 1 | Header version (`1`) |
| 1 | 1 | Flags (zero) |
| 2 | 2 | Reserved |
| 4 | 8 | Previous sibling page ID (`0` = none) |
| 12 | 8 | Next sibling page ID (`0` = none) |
| 20 | 8 | Leftmost child page ID (internal only; `0` on leaves) |

Leaves are doubly linked in key order for range scans. Internal sibling pointers are unused.

## Records

Keys are opaque bytes compared lexicographically (`bytes.Compare`). Empty keys are rejected. Maximum key size is 2048 bytes. A leaf record is capped at half the usable page so a full leaf plus one insert can always split.

**Leaf** (clustered row):

```
u16 key_len
u16 value_len
key
value
```

`value` is the row representation. SQL column layout is a later phase.

**Internal:**

```
u16 key_len
key
u64 right_child
```

An internal node with leftmost child `C0` and separators `(k1,C1)…(kn,Cn)` routes `key` to the rightmost child whose separator is `<= key`, or to `C0` if `key < k1`. Separator `ki` is the first key of subtree `Ci` at the time of the split. Deletes do not tighten separators.

## Operations

- `Insert` — unique keys. Duplicate key → `already_exists`.
- `Lookup` — exact match, returns a copy of the value.
- `Delete` — missing key → `not_found`. Empty non-root leaves are unlinked and freed. A root that is left with a single child collapses. Underfull leaves merge into a sibling when the combined records fit and the leaf is at most one quarter full.
- `Range(start, end)` — half-open `[start, end)`. Nil bounds are unbounded.

Tree mutations take an exclusive lock. Lookups and scans share a read lock.

## Invariants

`Tree.Check` verifies:

- Superblock root and height match the in-memory tree
- All leaves sit at the same height
- Keys and separators are strictly increasing on each page
- Every key is inside the separator bounds of its ancestors
- The leaf sibling chain matches left-to-right leaf order
- No empty non-root node
- No page cycle

## Secondary indexes (deferred)

Secondary indexes are not implemented in Phase 2. When they are, each secondary index is its own B+Tree:

```
secondary key || primary key  →  empty value
```

or, equivalently, a unique index key of `(secondary key, primary key)` with no separate payload.

- Duplicate secondary values are distinguished by the primary key suffix.
- A secondary lookup yields primary keys; the clustered tree supplies the row.
- Covering later optimizations may store a projected payload in the secondary leaf. That is optional and must not replace the primary-key reference.
- Hash and bitmap indexes remain out of scope until they have their own phase.
- Spatial indexes (`CREATE SPATIAL INDEX`) store a 64-bit Morton geohash plus the primary key; see `docs/geo.md`.
- Vector payloads and HNSW graphs are detached encrypted B+Trees; see `docs/vector.md`.

Do not store a heap RID. The clustered primary key is the row locator.
