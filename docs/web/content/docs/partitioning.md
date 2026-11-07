# Native table partitioning

NextSQL supports native physical table partitioning using **`RANGE`**, **`HASH`**, and **`LIST`** methods.

Partitioning organizes large tables into independent, partition-local encrypted heaps and index trees. It improves write throughput, accelerates analytical scans through **partition pruning**, and enables instant data lifecycle operations (archiving or loading) via zero-copy `ATTACH` and `DETACH`.

```arch
Partitioned Table: "events" (PARTITION BY RANGE (created_at))
┌─────────────────────────────────┬─────────────────────────────────┬─────────────────────────────────┐
│     Partition: p2024            │     Partition: p2025            │     Partition: p_future         │
│     < '2025-01-01'              │     < '2026-01-01'              │     VALUES LESS THAN MAXVALUE   │
├─────────────────────────────────┼─────────────────────────────────┼─────────────────────────────────┤
│ • Local Encrypted Heap          │ • Local Encrypted Heap          │ • Local Encrypted Heap          │
│ • Local Primary B+Tree          │ • Local Primary B+Tree          │ • Local Primary B+Tree          │
│ • Local Vector & Fulltext Graph │ • Local Vector & Fulltext Graph │ • Local Vector & Fulltext Graph │
└─────────────────────────────────┴─────────────────────────────────┴─────────────────────────────────┘
```

> [!NOTE]
> NextSQL partitioning is **local physical table partitioning** within a single database deployment. It is not automatic distributed sharding, and it never replaces database authentication or RBAC isolation.

---

## Partitioning methods & DDL

Partition keys may span **one to eight columns**.

### 1. RANGE Partitioning

Divides data into ordered, non-overlapping intervals. Range keys support multi-column tuple bounds and an optional `MAXVALUE` catch-all.

```sql
CREATE TABLE metric_samples (
    device_id STRING NOT NULL,
    recorded_at TIMESTAMPTZ NOT NULL,
    temperature DECIMAL(5,2),
    PRIMARY KEY (recorded_at, device_id)
) PARTITION BY RANGE (recorded_at) (
    PARTITION p2024 VALUES LESS THAN ('2025-01-01T00:00:00Z'),
    PARTITION p2025 VALUES LESS THAN ('2026-01-01T00:00:00Z'),
    PARTITION p_future VALUES LESS THAN MAXVALUE
);
```

Multi-column tuple bounds:
```sql
CREATE TABLE audit_records (
    org_id INT32 NOT NULL,
    id UUID NOT NULL,
    event_data JSON,
    PRIMARY KEY (org_id, id)
) PARTITION BY RANGE (org_id, id) (
    PARTITION p_low VALUES LESS THAN (1000, '00000000-0000-0000-0000-000000000000'),
    PARTITION p_mid VALUES LESS THAN (5000, 'ffffffff-ffff-ffff-ffff-ffffffffffff'),
    PARTITION p_high VALUES LESS THAN MAXVALUE
);
```

### 2. HASH Partitioning

Distributes rows evenly across a fixed set of partitions using cryptographic hashing over the partition key tuple. The partition count must form a complete modulus set (`0` through `N-1`).

```sql
CREATE TABLE user_sessions (
    user_id UUID NOT NULL,
    session_id STRING NOT NULL,
    metadata JSON,
    PRIMARY KEY (user_id, session_id)
) PARTITION BY HASH (user_id) (
    PARTITION h0 MODULUS 4 REMAINDER 0,
    PARTITION h1 MODULUS 4 REMAINDER 1,
    PARTITION h2 MODULUS 4 REMAINDER 2,
    PARTITION h3 MODULUS 4 REMAINDER 3
);
```

### 3. LIST Partitioning

Maps rows with specific discrete values to explicit partitions:

```sql
CREATE TABLE orders (
    id UUID NOT NULL,
    region STRING NOT NULL,
    total DECIMAL(12,2),
    PRIMARY KEY (region, id)
) PARTITION BY LIST (region) (
    PARTITION americas VALUES IN ('us', 'ca', 'mx'),
    PARTITION europe   VALUES IN ('gb', 'de', 'fr'),
    PARTITION apac     VALUES IN ('jp', 'sg', 'au')
);
```

---

## Partition lifecycle management

NextSQL allows mutating partitions online without taking table-level locks that block readers:

### Adding and dropping partitions

```sql
-- Add a new region partition to a LIST table
ALTER TABLE orders ADD PARTITION latam VALUES IN ('br', 'ar', 'cl');

-- Drop an obsolete partition and reclaim its physical storage immediately
ALTER TABLE metric_samples DROP PARTITION p2024;
```

### Zero-copy ATTACH and DETACH

You can instantly attach an existing standalone unpartitioned table as a partition, or detach a partition into an independent table. **No rows are copied and no disk re-writes occur:**

```sql
-- 1. Create a standalone staging table with the identical schema and indexes
CREATE TABLE orders_2024_staging (...);
-- Populate or bulk-load data into orders_2024_staging ...

-- 2. Zero-copy attach: takes ownership of physical pages and metadata instantly
ALTER TABLE orders ATTACH PARTITION orders_2024_staging VALUES IN ('archive');

-- 3. Zero-copy detach: publishes the partition as a standalone unpartitioned table
ALTER TABLE orders DETACH PARTITION americas;
```

---

## Indexes & constraints

### 1. Partition-local indexes
Secondary B+Tree, FULLTEXT, and Vector (`HNSW`, `IVF`, `IVFPQ`, `SPARSE`) indexes are created **partition-locally**:
* Each partition maintains its own isolated index root and storage pages.
* Vector builds and index maintenance (`REBUILD INDEX`) occur partition by partition with bounded memory consumption.

### 2. Cross-partition UNIQUE constraints
Plain-column secondary `UNIQUE` indexes are enforced table-wide:
```sql
CREATE TABLE users (
    id UUID NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    email STRING NOT NULL,
    PRIMARY KEY (created_at, id)
);

-- Enforced across ALL partitions with shared key locks
CREATE UNIQUE INDEX users_email_uq ON users (email);
```
Inserts and updates acquire an exclusive key lock and probe the local roots of other partitions to guarantee global uniqueness.

---

## Partition pruning & query optimization

The cost-based optimizer automatically detects filters on the partition key and **prunes non-matching partitions** before reading pages from disk.

### 1. Relational scans
```sql
-- Scans ONLY partition p2025; p2024 and p_future are completely skipped
SELECT * FROM metric_samples
WHERE recorded_at >= '2025-06-01T00:00:00Z'
  AND recorded_at < '2025-07-01T00:00:00Z';
```
In `EXPLAIN`, the physical plan displays the surviving partition list:
```text
Scan metric_samples [partitions: p2025] -> Filter -> Project
```

### 2. Pruning-aware vector search
When hybrid queries or `NEAREST` searches include a filter on the partition key, the vector executor only searches graphs in the matching partitions:
```sql
SELECT id, title
FROM products
WHERE region = 'us'
NEAREST embedding TO $query_vec
LIMIT 10;
```
Instead of searching across all partitions, the engine searches only the `americas` partition's HNSW graph, delivering significantly lower latency and higher QPS.

---

## Statistics & maintenance

Run `ANALYZE` to update row counts and per-partition distribution sketches:

```sql
ANALYZE TABLE metric_samples;
```

Inspect partition metadata and row estimates through the virtual system catalog:

```sql
SELECT partition_name, rows, free_pages
FROM system.partitions
WHERE table_name = 'metric_samples';
```
