# Native table partitioning

Status: P21 active; `NSCT` v4 descriptor shipped. Single-column
`PARTITION BY RANGE`, `PARTITION BY HASH`, `PARTITION BY LIST`, and
`PARTITION BY TENANT(tenant_id)` are available with partition-local heaps;
secondary indexes and lifecycle breadth remain deferred.

NextSQL partitioning is local physical table partitioning. It is not automatic
distributed sharding, and tenant locality never replaces RBAC or the mandatory
`tenant_id` row filter.

## Native DDL

```sql
-- RANGE (single column; ordered intervals, gaps allowed)
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

-- TENANT (must be the valid tenant_id column; STRING/UUID/TEXT)
CREATE TABLE orders (
  tenant_id UUID NOT NULL,
  id        UUID NOT NULL DEFAULT UUID(),
  note      STRING NOT NULL,
  PRIMARY KEY (tenant_id, id)
) PARTITION BY TENANT (tenant_id) (
  PARTITION t_a VALUES IN ('tenant-a'),
  PARTITION t_b VALUES IN ('tenant-b')
);

-- HASH (single column; complete, fixed modulus/remainder set)
CREATE TABLE sessions (
  account_id STRING NOT NULL,
  id         STRING NOT NULL,
  payload    STRING,
  PRIMARY KEY (account_id, id)
) PARTITION BY HASH (account_id) (
  PARTITION h0 MODULUS 4 REMAINDER 0,
  PARTITION h1 MODULUS 4 REMAINDER 1,
  PARTITION h2 MODULUS 4 REMAINDER 2,
  PARTITION h3 MODULUS 4 REMAINDER 3
);

-- LIST (single column; typed values cannot overlap)
CREATE TABLE regional_events (
  region STRING NOT NULL,
  id     STRING NOT NULL,
  note   STRING,
  PRIMARY KEY (region, id)
) PARTITION BY LIST (region) (
  PARTITION americas VALUES IN ('us', 'ca'),
  PARTITION elsewhere VALUES IN ('eu', 'ap')
);
```

Multi-column partition keys are rejected (`multi-column partition
values not supported in this slice`) until the routing tuple layer is gated.
`TENANT` requires exactly one `IN` literal per partition. `RANGE` uses
`VALUES LESS THAN (lit)` or `MAXVALUE`; the binder derives the lower edge from
the previous upper and validates non-overlapping ordered intervals. Lower is
inclusive when present, upper is exclusive.

## Durable descriptor

`NSCT` v4 stores a bounded partition descriptor in the same encrypted catalog
transaction as the table definition. It records:

- one to eight typed partition-key column ordinals;
- at most 1024 stable partition identities and names;
- detached heap and optional vector-store metadata roots;
- partition-local physical roots for every logical secondary index;
- ordered lower/upper tuples for RANGE, modulus/remainder for HASH, and typed
  admitted tuples for LIST/TENANT;
- inclusive RANGE edge flags.

The decoder caps the total routing tuples at 4096 and fails closed on unknown
enums or flags, truncation, duplicate identities/names/rules, overlapping
ranges, incomplete hash maps, NULL/mistyped values, invalid tenant keys, and
incomplete partition-local index metadata. Raw Go structs are never written.

Because the descriptor is an ordinary encrypted catalog value, catalog
changes inherit existing WAL/commit durability, crash recovery, backup and
restore, PITR, and deterministic Raft page-image replication. Physical
partition trees participate in ownership walks, reopen, and table-drop
reclamation; partition-specific backup/PITR fault-injection coverage remains
an open P21 gate.

## Native semantics (shipped slice)

- RANGE uses ordered, non-overlapping typed single-column intervals. Gaps are
  allowed; `INSERT`/`UPDATE` outside every interval fails with `not_found` and
  the transaction is failed closed. The executor routes each row to its
  partition heap via `catalog.PartitionForRow`; cross-partition `UPDATE` moves
  the row between partition heaps, reclaims the old vector payload, and
  maintains the same WAL, MVCC, and CDC row-change contract as non-partitioned
  tables.
- TENANT is LIST-like on the valid `tenant_id` column (`TenantCol()`). A
  partitioned write is fail-closed if `SET TENANT` is missing for an ACL-bound
  session, if the row `tenant_id` does not match the bound tenant, or if no
  partition admits that tenant value. `SELECT`/`UPDATE`/`DELETE` are tenant
  rewritten before planning and the executor additionally checks
  `checkTenantRow` on every written row, so physical locality never replaces
  authorization.
- HASH uses a complete fixed modulus/remainder set. Routing is deterministic
  across restart and replicas: SHA-256 of the canonical typed key, first 64
  digest bits big-endian, reduced modulo the declared modulus. Missing or mixed
  remainders fail catalog validation.
- LIST admits one or more typed values per partition. Duplicate values across
  partitions fail catalog validation, and writes with no matching value fail
  closed.
- Every primary key must include every partition column, preventing duplicate
  primary keys in separate heaps. Foreign keys on partitioned
  tables are rejected in this slice (`partitioned tables cannot have foreign
  keys`).
- Secondary indexes on partitioned tables are rejected in this slice
  (`partitioned tables do not support secondary indexes`); partition-local
  index roots remain in the descriptor for future use, and `EXPLAIN` already
  carries per-partition pruning.

## Physical ownership

Each partition owns a detached heap B+Tree and, when the table has a `VECTOR`
column, a detached vector store. The `CREATE TABLE` transaction allocates
those trees in the same engine `Storage` as the table heap, stores their
`PageID` metas in the `NSCT` v4 descriptor, and registers them in
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
`col > lit`, `col >= lit`, and `BETWEEN`; HASH, LIST, and TENANT prune on
typed equality. Candidate stable partition IDs are carried into SeqScan/PK IndexScan,
COUNT, and vectorized scan paths. An `OR` is pruned only when every branch is
analyzable; otherwise all partitions are retained conservatively.

## Remaining gate

P21 still needs multi-column RANGE/HASH/LIST, partition-local secondary
indexes and statistics, partition-aware maintenance and backup/restore/PITR
coverage, expanded fuzz/property tests, and benchmarks. The slice above is
intentionally bounded and versioned (no new catalog version).
