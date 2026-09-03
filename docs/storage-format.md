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
| 4 | Readable. v3 payload, then a bounded physical-partition descriptor. The next partition identity is derived as max ID + 1. |
| 5 | Readable. v4 payload, then the durable next partition identity. |
| 6 | Readable. v5 payload, then one `u8` HNSW traversal-quantisation tag per index (`0` none, `2` F16, `3` I8), in `Indexes` order. |
| 7 | Readable. v6 payload, then per-index vector-ANN method + IVF `LISTS` / `PROBES`. |
| 8 | Readable. v7 payload, then per-index IVF-PQ `SUBSPACES`. |
| 9 | Readable. v8 payload, then per-index full-text analyzer id (`u8`) + revision (`u16`). `0/0` is simple v1; `1/1` is english stem-only; `1/2` is english stem plus stop-word dictionary v1; `1/3` is english v2 plus synonym dictionary v1 (query-time OR expansion); `2/1` french, `3/1` german, `4/1` spanish (Snowball stemmer + stop-word dictionary v1). Unknown id/revision pairs fail closed. |
| 10 | Current write format. v9 payload, then one `u8` flag per column in column order. `0` means ordinary. `1` means `ENCRYPTED CLIENT` and is followed by its logical plaintext type (`kind u8`, `vector tag u8`, `precision u16le`, `scale u16le`); the stored physical type is `STRING`. Unknown flags/types, inconsistent metadata, or leftover bytes fail closed. |
| other | Fail closed (`unsupported catalog version`). |

Compatibility window (`internal/upgrade` `FamilyCatalog`): current 10, readable 1..10. `nextsql diagnose` prints that window. Old binaries cannot open v10 rows. Any catalog rewrite upgrades a readable older descriptor to v10 (older descriptors decode with every index unquantised, every vector index as HNSW unless a later trailer says otherwise, every full-text index as the simple analyzer, and no client-encrypted columns). v6 adds a per-index HNSW traversal-quantisation byte; v7 adds a per-index vector-ANN-method byte plus the IVF `LISTS` / `PROBES` counts; v8 adds a per-index IVF-PQ `SUBSPACES` count; v9 adds a per-index full-text analyzer id + revision; v10 adds per-column client-encryption metadata.

The v4/v5 partition section begins with a kind byte (`0` none, `1` RANGE, `2`
HASH, `3` LIST, `4` legacy TENANT). A nonzero kind carries at most 8 column ordinals
and 1024 partition members. Every member has a stable nonzero identity, a
bounded name, detached heap/vector metadata roots, range flags, hash
modulus/remainder, partition-local index roots, and typed routing tuples. The
whole descriptor accepts at most 4096 routing tuples. Decoding rejects unknown
kinds/flags, truncation, duplicate identities/names/rules, incomplete hash
remainder sets, overlapping ordered ranges, NULL or mistyped values, invalid
legacy tenant descriptors, and missing partition-local index roots.

For a nonzero partition kind, v5 appends a `u32` next-identity high-water
value after the member list. It must be greater than every live member ID.
ADD consumes and advances it transactionally; DROP never decreases it, so a
stable identity cannot resolve to a different physical tree after removal.
Reading v4 derives this value as max live ID + 1 before the next catalog write.

This metadata is stored inside the encrypted catalog B+Tree and therefore uses
the existing page AEAD, WAL transaction, backup/restore, PITR, and Raft page
replication mechanisms. P21 user-facing SQL DDL and physical row routing are
enabled only for the bounded single-column RANGE/HASH/LIST slice; kind 4 is
decoder/runtime compatibility for recovery and offline migration only;
bounded empty ADD/DROP lifecycle semantics are enabled; attach/detach and
broader lifecycle semantics remain explicitly gated. NSCT v4+ HASH routing is
SHA-256 over the canonical typed tuple key; the first 64 digest bits are read
big-endian and reduced modulo the descriptor's complete remainder set.

Each index record is `name`, flags `u8`, meta page, column ordinals, and optional JSON path. Flag bits 1/2/4/8/16 are unique / spatial / path / full-text / vector. Bits 32/64/128 add `INCLUDE` ordinals, a binary `WHERE` expression, and parallel expression keys with stored result types. Indexes without those bits keep the previous layout so older v2 rows still decode. A descriptor that sets the new bits is unreadable by binaries that do not know them (misaligned index records, then `trailing catalog bytes` or `invalid_format`).

## Statistics descriptors (`NSST`)

Statistics rows live in the encrypted catalog B+Tree under key `S` + table
name. Version 1 stores global table/column/index/segment sketches. Version 2
adds bounded vector statistics. Version 3 is the current write format and adds
at most 1024 `(stable partition ID u32, row count u64)` entries. IDs must be
nonzero and unique; malformed counts, truncation, duplicate IDs, unsupported
versions, and trailing bytes fail closed. Versions 1 and 2 remain readable and
yield no partition block. The descriptor inherits catalog page encryption,
WAL/recovery, backup/restore, PITR, and Raft replication.

Partition-local sketches use separate encrypted catalog rows under key `J` +
stable table ID `u32` + stable partition ID `u32`. Their value is `NSPS` v1:
the same table/partition identities, exact row count, the SHA-256 digest of the
owning encoded `NSST` snapshot, then compact bounded
column, index, and vector blocks. A record holds at most 64 entries of each
class and at most 15 KiB total. Column entries contain NULL count, sampled NDV, exact min/max where the
type is comparable, and sampled correlation; histograms and MCVs remain in the
global `NSST` row. Index entries contain NDV/selectivity/unique metadata and
vector entries contain population/dimension plus applicable index metadata.

`NSPS` decoding rejects zero or mismatched identities, duplicate ordinals or
index names, non-compact histogram/MCV payloads, excessive counts, unknown
versions, truncation, and trailing bytes. Reload matches the authenticated
value identities to the immutable key and its parent `NSST` partition list.
An unmatched snapshot digest is stale (for example after an older writer
refreshes only `NSST`) and is ignored in favor of global statistics; orphan or
identity-mismatched records fail closed. `ANALYZE`, table/partition removal,
WAL/recovery, Raft page images, backup/restore, and PITR mutate or preserve the
records through the ordinary catalog transaction.

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

## Durable idempotency records (`NSID`)

Retry fences use catalog key `I` plus a 32-byte SHA-256 scope digest; the raw
client key is never stored. Value magic is `NSID`, current/readable version is
1, followed by the 32-byte typed-request digest, creation/expiry Unix
nanoseconds, and a bounded versioned replay result (`NSIR` v1). Records are in
the encrypted catalog B+Tree and commit atomically with their mutation, so WAL,
recovery, backup/PITR, and Raft page replication preserve the fence.

Catalog reload rejects malformed keys, unsupported versions, invalid retention
times, oversized/truncated/trailing response bytes, and more than 1,024 live or
expired physical records. The executor replay decoder separately validates
row/column/value lengths, type tags, vector dimensions/finiteness, and its
256 KiB / 4,096-row limits. Decoder fuzz seeds cover both descriptor layers.

## Deployment registry (`NSRE` / `NSRM`)

New `nextsql init` deployments contain `nextsql.instance`, an encrypted
deployment registry independent of the user database catalog. Its wrapped-key
sidecar is `nextsql.instance.keys` (`NSKS` v1). The raw deployment registry
root stays in `--instance-key-file` (default `--key-file` plus `.instance`) off
the data volume.

The outer `NSRE` v1 file contains a bounded authenticated header: magic,
version, AES-256-GCM suite, key version, nonce generation, and plaintext
length; then the 12-byte nonce and ciphertext/tag. The header is AEAD AAD.
Before publishing a generation, the envelope durably advances its nonce
high-water, so a crash may skip but cannot reuse that generation. Publication
uses a mode-`0600` same-directory temporary file, file fsync, atomic rename,
and directory fsync.

The decrypted `NSRM` v1 manifest contains the deployment ID, generation,
default realm/database IDs, and bounded realm/database records. Each record has
a stable 128-bit ID, normalized bounded name, lifecycle state, storage-layout
tag, and database file identity. DatabaseID must equal the storage identity's
database UUID. Limits are 1,024 realms, 4,096 databases per realm, 8,192 total
databases, 63-byte ASCII names, and a 4 MiB manifest. Decode rejects zero or
duplicate IDs, duplicate names, invalid states/layouts, unresolved defaults,
identity mismatch, truncation, trailing bytes, unsupported versions, and
limit violations. The decoder has a fuzz seed.

Current scope is the M1 foundation: the default database uses the legacy
`DATA-DIR/nextsql.db` layout and `nextsqld` verifies that its identity and
ACTIVE state match the registry. `nextsql hosting adopt --confirm` explicitly
registers an existing default database without changing its file identity or
discovering sibling files. Its durable `PROVISIONING` registry record is the
restart intent and `ACTIVE` is published only after recovery-open succeeds.

`nextsql.lock` contains no authoritative state. `nextsqld`, `nextsql init`, and
offline adoption hold an OS-enforced exclusive lock on it; the file remains
after unlock and mode is forced to `0600`. Lock ownership, not file existence,
controls exclusion. Multi-database routing, ID-layout migration, registry
backup/PITR, Raft replication, realm auth stores, and managed database
directories are not implemented yet and receive no shipped claim.

## Short-lived credential material (`NSTK` / `NSTR`)

Sidecar files for signed short-lived credentials (`docs/security.md`), outside
the database and its encryption envelope. `NSTK` v1 (mode `0600`, atomic
rename) is the Ed25519 signing keyset: magic, version, key count, then per key
an id, flag byte (`retired` / `current` / `has-private`), creation Unix
seconds, 32-byte public key, and — only on an issuer keyset — the 32-byte
private seed (validated against the public key on load). A server keeps a
verify-only copy with every seed stripped. `NSTR` v1 is the revocation set:
magic, version, revoked-id count, then `(16-byte token id, expiry Unix
seconds)` pairs, followed by a cutoff count and `(u16-length principal, Unix
seconds)` pairs. Both decoders bound every count and length and reject
duplicates, trailing bytes, and more than one current key. The credential wire
blob itself (`NSSC` v1, claims + 64-byte Ed25519 signature) is never persisted
server-side.

## Format and catalog migration strategy (Phase 27)

`CurrentFormatVersion` has never been bumped past 1 — every physical page
and superblock in the field is v1. `internal/upgrade/compat.Catalog()` is
the single source of truth for what version of each family (`page`,
`envelope`, `wal`, `undo`, `catalog`, `backup`, `export`, `protocol`,
`replication`, `isolated` — see the const block in that package) this
binary can open; `nextsql diagnose` prints it verbatim, and `decodeSuperblock`
(`internal/storage/file`) and `catalog.DecodeTable` (`internal/catalog`) call
`compat.Check` directly rather than each re-deriving their own version
window, so the printed compatibility window and actual enforcement cannot
drift apart. `compat.Check`'s error names the actual and supported version
numbers, so an operator can immediately tell a too-old file (needs the
offline migration below) from a too-new one (needs a newer `nextsqld`
binary) without cross-referencing the catalog by hand. (`page.Validate`/
`page.CheckID` — the per-page hot path, executed on every page read — keep
their own inline `!= CurrentFormatVersion` check rather than calling
`compat.Check`: `FamilyPage` has `MinReadable == MaxReadable == Current`
today, so the two checks are equivalent, and the superblock read already
gates DB open before any page is read, making the per-page check pure
defense in depth. Route it through `compat.Check` too if `FamilyPage` ever
gains a real multi-version window.)

**Catalog-level changes are safe to migrate online — and this already
works today**, no new mechanism needed. The pattern, demonstrated across
ten `NSCT` revisions above: bump the record's version constant, teach the
decoder to read every prior version (each with a defined default for
fields that didn't exist yet), and never remove an old version's decode
branch. A record written by older code keeps decoding correctly forever;
there is no eager rewrite step, no downtime, and no version-gated
maintenance window. Any subsequent *write* of that record (the next
`ALTER TABLE`, `ANALYZE`, etc.) naturally upgrades it to the current
version as a side effect of being rewritten at all. This is the sanctioned
recipe for every future catalog-record change (`NSCT`, `NSST`, and the
smaller task/schedule/workflow/resource-group/idempotency records in
`internal/catalog`) and needs no exception process — just: widen
`MaxReadable` in `compat.Catalog()` in the same change that adds the new
version, and never delete an old branch.

**Format-level (page/superblock binary layout) changes are not safe to
migrate online, in general**, and no online mechanism is planned for them
speculatively — fixed-offset headers, AEAD-sealed content, and
checksum-covered bytes make an in-place physical rewrite unsafe to attempt
against a database that may be serving traffic or mid-WAL-replay. The safe
path for a breaking format change is offline, and it already exists using
tools that ship today: `nextsql backup` (a full physical-then-logical
snapshot) or a plain `SELECT`/`INSERT` copy through the SQL layer, into a
freshly `nextsql init`'d database running the new binary. Both paths cross
the *logical* row layer (`internal/sql/types`), not the raw physical page
bytes, so neither cares what physical format version either database is
using — this is why it works today with zero new code, and will keep
working the same way after a real format bump. A future increment that
actually proposes a breaking format change should document a `nextsql
migrate` operational sequence in `docs/ops.md` built from these existing
primitives (mirroring how "Rolling upgrade", `docs/ops.md`, is itself a
documented sequence of pre-existing primitives, not new mechanism), rather
than inventing an in-place converter.

A middle case is worth naming for when it actually arises rather than
building for now: a genuinely *additive-only* format change (a new
optional trailing header field with a safe, well-defined zero-value
default) could in principle follow the same forward-compatible-read
pattern the catalog already uses — an old binary ignores unknown trailing
bytes it already skips today, a new binary supplies the default for
absent ones. This is deliberately not built speculatively; evaluate it in
good faith against the actual proposed change when a real format bump is
next on the table, the same way each `NSCT` version above was decided on
its own concrete merits, not designed in advance of having a version to
add.

## What this version does not store

SQL or replication metadata as standalone files. User JSON lives inside encrypted `NSRW` row payloads as `NSJB` (`docs/json.md`). Full-text inverted indexes are detached encrypted B+Trees (`docs/fulltext.md`). Vector payloads (`NSVV`), HNSW graphs (`NSHM`), IVF / IVF-PQ index trees (`NSIV` / `NSPQ`), and the sparse inverted-index core encodings (`NSSV` / `NSSM` / `NSSP`) are detached encrypted B+Trees (`docs/vector.md`). WAL records are in the sibling `*.wal/` directory (`docs/wal.md`). UNDO and MVCC headers are documented in `docs/mvcc.md`.
