# Clustered B+Tree

NextSQL's primary relational storage structure is a **clustered B+Tree** built on the [16 KiB encrypted page engine](/docs/storage-format). Primary tree leaves hold the full MVCC-wrapped row representation (`NSRV`), while internal nodes hold routing keys and child page IDs.

Secondary indexes do not use heap RIDs; every secondary index is a detached B+Tree that maps `(secondary_key, primary_key)` to the clustered primary key.

```arch
                     ┌──────────────────────────────┐
                     │   Internal Node (Page 6)     │
                     │  C0 │ (k1, C1) │ (k2, C2)    │
                     └──┬─────────┬──────────┬──────┘
                        │         │          │
        ┌───────────────┘         │          └───────────────┐
        ▼                         ▼                          ▼
┌───────────────┐        ┌────────────────┐        ┌────────────────┐
│ Leaf (Page 5) │◄──────►│ Leaf (Page 5)  │◄──────►│ Leaf (Page 5)  │
│ [k < k1]      │  prev  │ [k1 <= k < k2] │  next  │ [k >= k2]      │
│ NSRV Rows     │        │ NSRV Rows      │        │ NSRV Rows      │
└───────────────┘        └────────────────┘        └────────────────┘
```

---

## Primary vs. detached trees

* **Catalog tree (superblock-rooted):** The database catalog lives in the primary B+Tree rooted in the superblock (offsets `236..248`: root page ID, height, flags).
* **User tables and indexes (detached trees):** Every user table heap, partition heap, secondary B+Tree index, full-text index, and vector index is a **detached B+Tree**. Each detached tree owns a dedicated slotted metadata page (`NSTM` magic + version + root page ID + height) whose page ID is stored in the catalog (`NSCT`). Root splits and collapses update the tree's `NSTM` metadata page transactionally without touching the superblock.

---

## Node layout & records

B+Tree pages use logical page types `5` (leaf) and `6` (internal).

### Node header (slot 0)

Every B+Tree page permanently reserves slot `0` for a 28-byte node header:

| Offset | Size | Field |
|---|---|---|
| `0` | 1 | Header version (`1`) |
| `1` | 1 | Flags (`0`) |
| `2` | 2 | Reserved |
| `4` | 8 | Previous sibling page ID (`0` = none) |
| `12` | 8 | Next sibling page ID (`0` = none) |
| `20` | 8 | Leftmost child page ID (`C0` on internal nodes; `0` on leaves) |

Leaf pages are doubly linked (`prev` / `next`) in key order so forward and backward range scans can traverse leaves sequentially without revisiting internal nodes.

### Key & record encoding

Keys are opaque byte slices compared lexicographically (`bytes.Compare`). SQL composite keys encode each column in sort-preserving big-endian binary order so byte comparison matches SQL ordering.

* **Maximum key size:** `2,048` bytes (non-empty).
* **Maximum leaf record size:** Capped at half the usable 16 KiB page capacity so a full leaf plus any valid insert is guaranteed to fit across a two-way split.

**Leaf record:**
```text
u16 key_len
u16 value_len
byte[key_len]   key
byte[value_len] value   (NSRV header + NSRW SQL row payload on heaps)
```

**Internal separator record:**
```text
u16 key_len
byte[key_len] key
u64           right_child_page_id
```

An internal node with leftmost child `C0` and separators `(k1, C1) … (kn, Cn)` routes a lookup key to the rightmost child whose separator satisfies `ki <= key`, or to `C0` when `key < k1`.

---

## Tree operations & structural modifications

* **Point lookup (`Lookup`):** Descends from root to leaf in $O(\log N)$ page reads and evaluates MVCC visibility against the caller's snapshot.
* **Forward & backward range scans (`Range`, `FirstBounded`, `LastBounded`):** `Range(start, end)` evaluates the half-open interval `[start, end)`. `FirstBounded` and `LastBounded` descend directly to the lower or upper bound and walk leaf sibling pointers (`next` or `prev`) to stop on the first visible entry, powering $O(\log N)$ index-assisted `MIN()` and `MAX()` aggregates.
* **Splits and merges:**
  * Overflowing a leaf or internal node splits the page at the median slot and inserts a separator into the parent (growing tree height by 1 when the root splits).
  * Deleting records that leave a non-root leaf empty unlinks and frees the page. Underfull leaves (at most $\frac{1}{4}$ full) merge into an adjacent sibling when combined records fit; a root left with a single child collapses.
* **Nested-top-action WAL logging:** Structural modifications (page splits and merges) take effect immediately for concurrent readers and writers and are logged as atomic system transactions (`Engine.LeaveOp`) so committed rows on newly split pages survive power loss even if the splitting transaction later aborts.

---

## Secondary indexes

Every secondary index (`CREATE INDEX`, `CREATE UNIQUE INDEX`, expression indexes, partial `WHERE` indexes, JSON path indexes, and Morton-coded `SPATIAL` indexes) is stored as its own detached B+Tree:

* **Non-unique secondary index:** Encodes `secondary_key || primary_key` as the unique B+Tree key. Duplicate secondary values are disambiguated by the trailing primary key.
* **Covering index (`INCLUDE`):** Stores projected non-key columns in the leaf value so `IndexScan … covering` can satisfy queries directly from the index without fetching from the table heap.

---

## Structural invariants & `nextsql check-pages`

`btree.Tree.Check` and `executor.DB.CheckPageOwnership` verify full physical and logical integrity across all trees in a database:

1. Detached metadata (`NSTM`) or superblock root and height match the physical tree.
2. Every leaf sits at the exact same depth.
3. Keys and internal separators are strictly increasing within every page.
4. Every key on a child page falls strictly within its ancestor separator interval.
5. The doubly-linked leaf sibling chain (`prev` / `next`) matches the left-to-right internal depth-first traversal with no cycles or gaps.
6. No page is simultaneously on the allocator freelist and owned by a tree (`free_and_owned`), shared between two trees (`shared_pages`), or out of file bounds.

Run the offline verifier at any time with:

```bash
nextsql check-pages --data-dir /var/lib/nextsql --key-file /etc/nextsql/root.key
```

Engine note: [`docs/btree.md`](https://github.com/bzync/nextsql/blob/master/docs/btree.md).
