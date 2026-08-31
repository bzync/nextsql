# Native table partitioning

Status: P21 active; `NSCT` v5 descriptor shipped. `PARTITION BY RANGE`,
`PARTITION BY HASH`, and `PARTITION BY LIST` are available with
partition-local heaps, B+Tree-family local indexes (including cross-partition
plain-column `UNIQUE`), partition-local FULLTEXT indexes, partition-local
HNSW/`VECTOR` indexes, and bounded ADD/DROP/ATTACH/DETACH lifecycle DDL. HASH keys may span one to eight
columns; RANGE and LIST keys may span one to eight columns using tuple bound
(`VALUES LESS THAN (a, b)`) and tuple membership (`VALUES IN ((a, b), ...)`)
syntax. `NEAREST` and
hybrid `SEARCH`+`NEAREST` candidate generation are pruning-aware: a residual
predicate that constrains the partition key restricts the searched partition
graphs and heaps. Plain-column secondary `UNIQUE` indexes (with optional
`INCLUDE`) are enforced across every partition; partial, expression, and
JSON-path `UNIQUE`, and `UNIQUE` on legacy TENANT tables, remain rejected.
`PARTITION BY TENANT` is rejected.

NextSQL partitioning is local physical table partitioning. It is not automatic
distributed sharding and never replaces realm/database authentication or RBAC.

## Native DDL

```sql
-- RANGE (one to eight columns; ordered intervals, gaps allowed)
CREATE TABLE events (
  id   STRING NOT NULL,
  ts   TIMESTAMPTZ NOT NULL,
  v    STRING NOT NULL,
  PRIMARY KEY (ts, id)
) PARTITION BY RANGE (ts) (
  PARTITION p_early VALUES LESS THAN ('2024-01-01T00:00:00Z'),
  PARTITION p_mid   VALUES LESS THAN ('2025-01-01T00:00:00Z'),
  PARTITION p_max   VALUES LESS THAN MAXVALUE
);

-- HASH (one to eight columns; complete, fixed modulus/remainder set)
CREATE TABLE sessions (
  account_id STRING NOT NULL,
  shard      STRING NOT NULL,
  id         STRING NOT NULL,
  payload    STRING,
  PRIMARY KEY (account_id, shard, id)
) PARTITION BY HASH (account_id, shard) (
  PARTITION h0 MODULUS 4 REMAINDER 0,
  PARTITION h1 MODULUS 4 REMAINDER 1,
  PARTITION h2 MODULUS 4 REMAINDER 2,
  PARTITION h3 MODULUS 4 REMAINDER 3
);

-- LIST (one to eight columns; typed values cannot overlap)
CREATE TABLE regional_events (
  region STRING NOT NULL,
  id     STRING NOT NULL,
  note   STRING,
  PRIMARY KEY (region, id)
) PARTITION BY LIST (region) (
  PARTITION americas VALUES IN ('us', 'ca'),
  PARTITION elsewhere VALUES IN ('eu', 'ap')
);

-- Multi-column RANGE: bounds are lexicographically ordered tuples.
CREATE TABLE ledger (
  region STRING NOT NULL,
  bucket STRING NOT NULL,
  id     STRING NOT NULL,
  PRIMARY KEY (region, bucket, id)
) PARTITION BY RANGE (region, bucket) (
  PARTITION p_lo  VALUES LESS THAN ('m', ''),
  PARTITION p_mid VALUES LESS THAN ('t', ''),
  PARTITION p_hi  VALUES LESS THAN MAXVALUE
);

-- Multi-column LIST: each admitted value is a typed tuple.
CREATE TABLE placements (
  region STRING NOT NULL,
  tier   STRING NOT NULL,
  id     STRING NOT NULL,
  PRIMARY KEY (region, tier, id)
) PARTITION BY LIST (region, tier) (
  PARTITION hot  VALUES IN (('us', 'gold'), ('eu', 'gold')),
  PARTITION cold VALUES IN (('us', 'bronze'), ('eu', 'bronze'))
);
```

`RANGE` uses `VALUES LESS THAN (lit)` / `VALUES LESS THAN (lit, lit, ...)` or
`MAXVALUE`; the binder derives the lower edge from the previous upper and
validates non-overlapping ordered intervals. Lower is inclusive when present,
upper is exclusive. Multi-column `RANGE` bounds are compared as whole tuples
using the order-preserving key encoding, so `('m', '')` sorts before
`('m', 'a')`. Per-column `MAXVALUE` inside a tuple is not accepted; use a
trailing minimum literal (for example `''` for `STRING`) plus a final
`VALUES LESS THAN MAXVALUE` partition.

`LIST` uses `VALUES IN (v, ...)` for a single-column key and
`VALUES IN ((v, v), ...)` for a multi-column key. Every membership tuple must
match the partition-key arity and no tuple may be claimed by two partitions.

Multi-column `HASH` routes on the SHA-256 digest of the canonical typed tuple
and needs no per-partition value literals. A partition-key column count of one
to eight is enforced for every kind. Every primary key must still include every
partition-key column.

## Bounded lifecycle DDL

```sql
-- LIST adds new, non-overlapping admitted values.
ALTER TABLE regional_events
  ADD PARTITION anz VALUES IN ('au', 'nz');

-- RANGE can append only after a bounded current tail.
ALTER TABLE events
  ADD PARTITION p_future VALUES LESS THAN MAXVALUE;

-- DROP is intentionally empty-only and must leave another partition.
ALTER TABLE regional_events DROP PARTITION elsewhere;

-- ATTACH consumes an existing compatible unpartitioned table. Its table name
-- becomes the stable logical partition name.
ALTER TABLE regional_events
  ATTACH PARTITION anz VALUES IN ('au', 'nz');

-- DETACH publishes the owned roots as an unpartitioned table named anz.
ALTER TABLE regional_events DETACH PARTITION anz;
```

ADD allocates the partition-local heap and optional vector tree in the same
transaction as the catalog rewrite. LIST additions must not overlap existing values. RANGE additions append a new tail whose lower edge is the
current tail's bounded upper edge; adding after `MAXVALUE` is rejected. HASH
membership changes are rejected because changing its complete remainder set
requires an explicit, crash-safe redistribution operation.

DROP rejects a non-empty or final partition. After commit, its physical trees
remain registered until older snapshots drain, then their exact ownership sets
are reclaimed and the handles removed under the exclusive apply guard. A
rollback publishes neither new handles nor reclamation. Stable partition IDs
come from the durable v5 high-water allocator and are never reused.

ATTACH accepts an existing unpartitioned table only when its columns, primary
key, defaults, and ordered logical index definitions exactly match the parent.
The source cannot have foreign-key, trigger, or workflow dependencies. NextSQL
streams every source heap row through typed decoding and the proposed routing
descriptor before changing ownership; one out-of-rule or corrupt row aborts
the whole operation. When the parent has a cross-partition plain-column
`UNIQUE` index, each incoming row's encoded key is also probed against the
existing partitions (under an exclusive key lock) and a collision aborts the
attach. The caller needs `ALTER` on the parent and `DROP` on the source. No row or index entry is copied: the source heap, vector store, and
matching index roots move into the new stable partition identity in the same
catalog/WAL transaction that removes the source table.

DETACH removes a non-final RANGE or LIST member from routing and atomically
publishes its physical roots as an unpartitioned table whose name is the
partition name. It rejects a table-name collision and requires database
`CREATE` in addition to parent `ALTER`. HASH attach/detach remains rejected
because changing a complete remainder set needs redistribution. Detached
tables inherit the parent schema, defaults, CDC image mode, and logical index
definitions. `AI()` high-water state is reconstructed by the bounded streaming
validation scan. The parent's partition-ID high water is unchanged, so a
detached stable ID is never reused.

Rollback publishes neither side of a transfer. Pre-COMMIT crash tests verify
that ATTACH leaves the standalone source intact and DETACH leaves the member
owned by the parent; committed transfers reopen from the catalog after an
unclean engine restart. Existing base-backup, archived-WAL, and deterministic
Raft catalog/page-image paths apply because transfer adds no sidecar or new
persistent format. Run `ANALYZE` after a large transfer to refresh costing;
missing attached stable-ID statistics retain the conservative global fallback.

## Durable descriptor

`NSCT` v5 stores a bounded partition descriptor in the same encrypted catalog
transaction as the table definition. It records:

- one to eight typed partition-key column ordinals;
- at most 1024 stable partition identities and names;
- detached heap and optional vector-store metadata roots;
- partition-local physical roots for every logical secondary index;
- ordered lower/upper tuples for RANGE, modulus/remainder for HASH, and typed
  admitted tuples for LIST;
- inclusive RANGE edge flags.
- a durable next-identity high-water value, so dropped stable IDs are never
  reused. v4 reads derive it as max live ID + 1 and the next rewrite upgrades
  the descriptor to v5.

Legacy `PartitionLegacyTenant` catalog descriptors remain decodable for recovery and
offline migration compatibility, but SQL cannot create or extend them. This is
not a supported authorization or hosting mechanism. `nextsql hosting
migrate-tenant` (see "Offline legacy TENANT migration" below) is the supported
way to move a former tenant off such a descriptor.

The decoder caps the total routing tuples at 4096 and fails closed on unknown
enums or flags, truncation, duplicate identities/names/rules, overlapping
ranges, incomplete hash maps, NULL/mistyped values, invalid partition rules, and
incomplete partition-local index metadata. Raw Go structs are never written.

Because the descriptor is an ordinary encrypted catalog value, catalog
changes inherit existing WAL/commit durability, crash recovery, backup and
restore, PITR, and deterministic Raft page-image replication. Physical
partition trees participate in ownership walks, reopen, and table-drop
reclamation. `TestPartitionBackupRestoreAndPITR` verifies that a base restore
and an archived-WAL recovery preserve the `NSCT` descriptor, routed rows,
partition-local index roots, pruning, and stable-ID `NSST` row counts. Backup
publication remains atomic under the backup package's existing crash-point
tests because partitions share the same authenticated data/WAL members rather
than introducing an independently published sidecar.

## Native semantics (shipped slice)

- RANGE uses ordered, non-overlapping typed intervals over the partition-key
  tuple (one to eight columns), compared with the order-preserving key encoding.
  Gaps are
  allowed; `INSERT`/`UPDATE` outside every interval fails with `not_found` and
  the transaction is failed closed. The executor routes each row to its
  partition heap via `catalog.PartitionForRow`; cross-partition `UPDATE` moves
  the row between partition heaps, reclaims the old vector payload, and
  maintains the same WAL, MVCC, and CDC row-change contract as non-partitioned
  tables.
- HASH uses a complete fixed modulus/remainder set. Routing is deterministic
  across restart and replicas: SHA-256 of the canonical typed key, first 64
  digest bits big-endian, reduced modulo the declared modulus. Missing or mixed
  remainders fail catalog validation. The key may span one to eight columns;
  the digest is taken over the canonical typed tuple in declared column order.
- LIST admits one or more typed value tuples per partition (one literal per
  partition-key column). Duplicate tuples across partitions fail catalog
  validation, and writes with no matching tuple fail closed.
- Every primary key must include every partition column, preventing duplicate
  primary keys in separate heaps. Foreign keys on partitioned
  tables are rejected in this slice (`partitioned tables cannot have foreign
  keys`).
- Plain, covering, partial, expression, JSON-path, and spatial indexes allocate
  one physical B+Tree per partition. CREATE streams each local heap without an
  input-sized result buffer; INSERT/UPDATE/DELETE maintain the owning local
  root, including cross-partition moves. Index scans honor the optimizer's
  stable partition candidates. Blocking REBUILD and DROP replace or reclaim
  every local root only after older snapshots drain. ADD PARTITION allocates
  empty roots for all existing logical indexes in the catalog transaction.
- A plain-column secondary `UNIQUE` index (optionally with `INCLUDE`) is
  enforced across every partition. Each partition still has one local root, but
  uniqueness is global: before inserting the encoded key into the row's own
  local root, the writer takes an exclusive lock on that key (the engine key
  lock namespace is global, so writers of the same value in any partition
  serialize) and probes every other partition's local root. CREATE / REBUILD
  additionally run an ordered cross-partition duplicate scan over the freshly
  built roots, and ATTACH PARTITION probes each incoming row's key against the
  existing partitions before adopting the standalone table's root. A
  cross-partition UPDATE that lands the new key in a partition that already
  holds it is rejected by that partition's own local root; a collision with a
  third partition is caught by the probe. Partial, expression, and JSON-path
  `UNIQUE` indexes, and `UNIQUE` on legacy TENANT tables, remain rejected
  (`… not supported in this slice`). NULLs are encoded into the key, so — as on
  an unpartitioned table — a `UNIQUE` index admits at most one NULL key across
  all partitions.
- `UPSERT` is supported on RANGE/HASH/LIST tables (legacy TENANT tables stay
  rejected). Because every primary key includes every partition column, a
  PK-target `UPSERT` routes its proposed row to exactly one partition and any
  primary-key conflict is resolved against that partition's local heap. A
  secondary-`UNIQUE`-target `UPSERT` takes one exclusive lock on the encoded key
  (global lock namespace) and probes every partition-local root, so the
  conflicting row is found wherever it lives; the update then rewrites that row
  in place and, if `SET` changes a partition-key column, moves it between heaps.
  A no-conflict `UPSERT` inserts through the ordinary routed write path,
  including the cross-partition `UNIQUE` probe, so a new row whose unique key
  already lives in another partition is still rejected.
- Partition-local FULLTEXT indexes allocate one inverted-index root per
  partition, maintained by the same CREATE / routed DML / cross-partition move /
  ADD PARTITION / blocking REBUILD / DROP / ATTACH / DETACH paths as the B+Tree
  family. Each local root keeps its own posting lists, document-length records,
  and BM25 corpus stats, so a partition is self-contained: ATTACH transfers the
  standalone table's inverted-index root in place and DETACH publishes it with
  the standalone table, without rebuilding. `SEARCH` (and hybrid full-text
  candidate generation) scores every pruned partition-local root as one logical
  corpus: document frequency and corpus size are summed across roots, so a
  partitioned table ranks identically to the same rows in an unpartitioned
  table. A cross-partition UPDATE moves the document's postings and adjusts both
  partitions' corpus stats.
- Partition-local HNSW/`VECTOR` indexes allocate one HNSW graph root per
  partition, maintained by the same CREATE / routed DML / cross-partition move /
  ADD PARTITION / blocking REBUILD / DROP / ATTACH / DETACH paths as the other
  index families. Vector payloads live in the per-partition vector store (not
  the shared `tab.VecMeta` store), so a partition is self-contained for
  ATTACH/DETACH. `NEAREST` — with an index or as a flat scan — searches every
  partition-local graph that the residual predicate cannot rule out and merges
  hits by distance, then keeps the requested `k`, so a partitioned table returns
  the same neighbours as the same rows in an unpartitioned table. When the
  residual predicate constrains the partition key (the same `col = lit` /
  range / `BETWEEN` / all-branch `OR` analysis used for scan pruning) only the
  surviving partition graphs are opened and searched; `EXPLAIN` shows the pruned
  set on the `Nearest` node (`partitions=[…]`). A predicate that does not touch
  the partition key leaves every partition in play, so pruning never changes
  which neighbours are returned.
- Hybrid `SEARCH`+`NEAREST` candidate generation is pruning-aware in the same
  way. All three strategies (`filter-ann`, `search-ann`, `ann-filter`) restrict
  candidate generation to the surviving partitions: the `filter-ann` and
  `search-ann` access scans are pruned like any other scan, and the
  `ann-filter` HNSW candidate step opens and searches only the pruned
  partition-local graphs. `EXPLAIN` shows the pruned set on the `Candidates`
  node (`partitions=[…]`, or `partitions=all[n]` when nothing is ruled out).
  Fusion (RRF over BM25 and vector rank) then runs over exactly the surviving
  candidates, so a row in a pruned partition can never enter the fused result
  even when it holds both a closer vector and a stronger text match. Candidate
  generation always projects the full-text and vector columns so a covering
  primary-key projection (`SELECT pk … SEARCH … NEAREST`) still scores
  correctly.
- A partitioned table cannot declare foreign keys in this slice
  (`partitioned tables cannot have foreign keys`).
- `MAINTAIN TABLE` visits the base ownership trees and every partition-local
  heap, vector store, and index root. `MAINTAIN INDEX` visits every local root
  of the named logical index without crossing into heaps or other indexes. Both
  retain the existing leader-only, exclusive, bounded maintenance contract and
  fail closed if a catalog-owned physical root is missing.

## Physical ownership

Each partition owns a detached heap B+Tree and, when the table has a `VECTOR`
column, a detached vector-payload store that holds that partition's vectors
(the shared `tab.VecMeta` store is used only by unpartitioned tables). A
partition-local HNSW index adds one more detached tree per partition for the
graph nodes. The `CREATE TABLE` transaction allocates
those trees in the same engine `Storage` as the table heap, stores their
`PageID` metas in the `NSCT` v5 descriptor, and registers them in
`DB.partHeaps`/`partVecs` (and `partIdxs` when indexes are present). `DROP
TABLE` reclaims heap/vec/index trees per partition via `queueTreeReclaim`.
The catalog tree remains the single durable source of truth; recovery
(`reloadCatalog`) reopens every partition tree from its meta, and Raft
replication is deterministic because the catalog descriptor and the data pages
share the same WAL page-image batch (`ApplyReplicated` reopens the catalog and
all partition trees).

## EXPLAIN pruning

`EXPLAIN` shows the pruned partition set on `SeqScan` and primary-key
`IndexScan` access:

```text
SeqScan events partitions=[p_early]
SeqScan events partitions=all[3]
SeqScan orders partitions=[t_a]
IndexScan sessions pk partitions=[h2]
```

For RANGE the optimizer prunes on `col = lit`, `col < lit`, `col <= lit`,
`col > lit`, `col >= lit`, and `BETWEEN`. Because RANGE bounds are
lexicographically ordered `[lower, upper)` tuples, the predicate is reduced to a
query lower/upper bound prefix over the partition-key columns: successive
equality constraints (`region = 'eu' AND shard = 'p'`) extend the prefix, and
the first non-equality constraint contributes its own bound literal and ends the
walk. A partition survives only when the query bound interval can intersect its
tuple interval, so a predicate that also pins or bounds a trailing
partition-key column prunes bands that merely share a leading value
(`region = 'eu' AND shard = 'p'` selects the single `shard` band that can hold
`'p'`). A predicate that constrains only the leading column still keeps every
band that shares that value, because the trailing columns are then free.
Single-column HASH and LIST prune on typed
equality. Multi-column HASH and multi-column LIST prune to one partition only
when every partition column is pinned to a single equality value; otherwise
every partition is retained. Legacy TENANT descriptors retain equality pruning
only for recovery compatibility. Candidate stable partition IDs are carried into SeqScan/PK IndexScan,
COUNT, and vectorized scan paths. An `OR` is pruned only when every branch is
analyzable; otherwise all partitions are retained conservatively.

### Pruning soundness property

Pruning is allowed to keep extra partitions but must never drop one that could
hold a matching row. `TestPartitionPruningSoundness`
(`internal/sql/optimizer`) enforces this: it generates thousands of random
single- and multi-column RANGE, HASH, and LIST schemes with random predicates
(`=`, `<`, `<=`, `>`, `>=`, `BETWEEN`, `OR` of equalities, bounded ranges, and an
unrelated conjunct), enumerates the whole key space, routes every row through the
descriptor, and asserts that any row satisfying the predicate is owned by a
partition the authoritative pruner (`prunePartitionsForExplain`, reached from
`partitionAccessDetail`) retained. The test also fails if pruning becomes
vacuous — a healthy fraction of the generated cases must actually prune.

`ANALYZE` stores exact row counts per stable partition ID in the bounded
`NSST` v3 descriptor and writes a separate compact `NSPS` v1 record for each
physical member. `NSPS` is keyed by immutable table/partition IDs and contains
bounded local column NULL/NDV/min/max/correlation, index selectivity, and vector
population sketches. Each record caps every sketch class at 64 and the total at
15 KiB; routing, indexed, and vector columns take priority, histograms/MCVs remain global, and
local sampling is capped at 4,096 rows. The optimizer sums exact counts and
merges local sketches only when every pruned stable ID is covered. Missing or
stale identities/sketches fall back to global `NSST`, preventing partition DDL
from manufacturing an empty or overconfident plan. Each `NSPS` record carries
the SHA-256 digest of its owning encoded `NSST`, so an older writer's unpaired
global refresh makes the local record stale rather than accepted. The decoder is fuzz-seeded,
validates key/value identity on reload, and table/partition removal deletes its
side records transactionally.

## Partition-wise aggregation

An aggregate (`SUM`/`COUNT`/`AVG`/`MIN`/`MAX`, with or without `GROUP BY`)
directly over a partitioned table's heap runs **partition-wise**: the executor
builds one partial hash-aggregation table per surviving partition, feeds each
from its own partition heap transaction, runs the partitions in parallel through
the query scheduler (bounded by the per-query worker budget), and merges the
partial tables into the final result. Groups that span partitions — a `GROUP BY`
on a non-partition-key column, or a global aggregate — are folded during the
merge, so the answer is identical to a single aggregation over the union of the
partitions. Partition pruning still applies first: only the retained bands are
aggregated. `EXPLAIN` tags the operator `Aggregate <names> partition-wise`.

The path covers the top-level `SELECT ... FROM <partitioned table>` form and the
same shape nested under a join, set operation, CTE, or subquery. A `WHERE` clause
that the optimizer cannot fully turn into a partition-key or primary-key range
still forces the generic streaming aggregation over a `Filter` node.

## Partition-wise joins

An equi-join between two tables that use an **identical** physical partition
scheme, where the join equates every partition-key column of one side to the
positionally corresponding partition-key column of the other, runs
**partition-wise**: a row on either side can only match rows in the single
paired partition on the other side, so the executor runs one bounded hash join
per aligned partition pair — in parallel across pairs through the query
scheduler — and concatenates the results. No hash table ever spans more than one
partition pair, and a pair whose partition was pruned on either input is skipped
entirely. The union of the per-pair results is identical to a single join over
the whole relations.

Two schemes are identical when they have the same kind and partition count and:
RANGE bound tuples match position-for-position (with the same inclusivity),
HASH shares one modulus and the same remainder set, and LIST partitions have the
same value groupings. Partition-key column types must match exactly. Legacy
TENANT and unpartitioned tables are never aligned.

Supported join kinds: `INNER`, `LEFT`, `FULL`, `SEMI`, `ANTI`. `CROSS` joins,
merge joins, and inputs that are not a plain partitioned scan (for example a
partition-key predicate that the optimizer turned into a primary-key
`IndexScan`, or a join input that is itself a join) fall back to the generic
single hash join. A residual `WHERE` filter that stays as a `Filter` over the
partitioned scan is applied per partition before the join. `EXPLAIN` tags the
join node `<JoinOp> <predicate> partition-wise`.

Join predicates that contain a subquery run the per-pair joins serially (the
residual check mutates per-session state); every other case runs the pairs in
parallel.

## Benchmarks

`nextsql-bench --partition` (implemented in `internal/bench/partition.go`,
guarded by `TestPartitionBench`) compares a RANGE-partitioned table — eight
single-value bands over a `STRING` bucket, `PRIMARY KEY (bucket, id)` — against
an unpartitioned `PRIMARY KEY (id)` table holding the same rows with `bucket` as
a plain column. Encryption, WAL, and fsync stay on. Reads run inside a read-only
transaction so the bounded SELECT result cache never serves a repeat.

Representative run, 40,000 rows per table, 3 s per measurement, Ryzen (linux/amd64,
12 CPU, AES-256-GCM, WAL + fsync):

| workload            | partitions     | part p50 | flat p50 | speedup |
| ------------------- | -------------- | -------- | -------- | ------- |
| single-bucket COUNT | 1/8 (pruned)   | 2.67 ms  | 25.0 ms  | 9.4x    |
| single-bucket SUM   | 1/8 (pruned)   | 3.40 ms  | 25.3 ms  | 7.4x    |
| full SUM (no prune) | 8/8            | 27.1 ms  | 8.77 ms  | 0.32x   |
| routed INSERT       | n/a            | 107 µs   | 71 µs    | 0.66x   |

How to read it:

- A predicate that pins the partition key prunes to one band and scans ~1/N of
  the data, so a single-bucket aggregate is several times faster than the
  full-table scan the unpartitioned `PRIMARY KEY (id)` layout forces. A
  clustered `PRIMARY KEY (bucket, id)` on the unpartitioned table would instead
  give a prefix range scan and close most of this gap — partitioning's read win
  is largest when the alternative has no usable key or index on that column.
- An aggregate with **no** partition-key predicate now runs partition-wise (see
  above): each band's heap is aggregated in parallel and the partials are
  merged, which removes most of the earlier sequential per-band penalty. The
  numbers in the table above predate that change; re-run the suite for current
  figures. An equi-join on the partition key between two identically partitioned
  tables likewise runs partition-wise (see above).
- Routed `INSERT` into a partitioned table carries the wider composite key plus
  routing; the smaller per-band trees do not offset that at this scale. Pick the
  partition key for pruning and lifecycle (`DROP PARTITION`), not for write
  throughput.

## Offline legacy TENANT migration

`nextsql hosting migrate-tenant` copies one historical tenant out of a legacy
`tenant_id` / `PARTITION BY TENANT` database into a freshly provisioned isolated
deployment. It is an offline `ADMIN` tool: stop `nextsqld` for the source, and
the command exclusively locks both data directories (ordered by path, so two
runs cannot deadlock) for the whole operation.

Flow (`internal/xport/tenant.go`, driven from `cmd/nextsql`):

1. Enumerate every source table with a `tenant_id` legacy marker. A table that
   already has a `legacy_tenant_id` column, or a foreign key to a table outside
   the migration set, fails closed.
2. Build the destination schema from a clone with physical partitioning and all
   physical roots stripped, the `tenant_id` column renamed to `legacy_tenant_id`
   (ordinary application data in the isolated database), and indexes renamed to
   match. `CDC IMAGES FULL` is preserved.
3. Preflight the destination: it must be empty or an exact resumable match of
   this migration's logical schema. Any unrelated table is rejected.
4. Create the destination tables in foreign-key dependency order, then copy the
   matching rows in bounded transactions (`--batch-rows`, 1–4096, default 256)
   through `UPSERT` so a retry after a committed batch is idempotent. Indexes
   are created after the row copy.
5. Point-verify every table: destination row count equals the source tenant row
   count, no destination row belongs to another tenant, and each source row
   re-reads identically by primary key in the destination.

A durable encrypted `nextsql.tenant-migration` intent (`NSLM` v1, AES-256-GCM,
tenant value encrypted, decoder fuzz-seeded by `FuzzDecodeTenantMigrationIntent`)
binds the destination to one source identity, one destination identity, and one
tenant. The destination database stays `PROVISIONING` until copy and
verification succeed; the intent is then marked complete with the verified
table/row counts and the registry entry is published `ACTIVE`. An exact rerun
resumes: a still-`PROVISIONING` destination re-copies (UPSERT-idempotent) and
re-verifies, an already-`ACTIVE` destination runs `VerifyLegacyTenantMigration`
only, and a changed source, tenant, or destination is a `conflict`. Source and
destination roots plus the destination registry root must be three independent
key files.

Tests: `TestMigrateLegacyTenantPartitionIsBoundedVerifiedAndIdempotent`,
`TestMigrateLegacyTenantRejectsUnexpectedDestinationState` (`internal/xport`);
`TestTenantMigrationIntentEncryptedExactAndComplete`,
`TestTenantMigrationIntentTamperTruncateAndWrongKeyFailClosed`,
`FuzzDecodeTenantMigrationIntent` (`internal/hosting`);
`TestHostingMigrateTenantPublishesOnlyVerifiedIsolatedDatabase` (`cmd/nextsql`).

## Remaining gate

The P21 shipped slice above is intentionally bounded and versioned. Further
fuzz/property coverage may still be added; automatic distributed sharding is a
separate future phase and is not part of this slice. Pruning soundness is
covered by a randomized property test (see above); routing round-trips and
descriptor decoding are fuzz-seeded; `nextsql-bench --partition` covers pruning,
overhead, and write cost. `UPSERT` on RANGE/HASH/LIST tables is wired to the
partition-local roots (see "Native semantics"); it stays rejected only on legacy
TENANT tables.
