# NextSQL

NextSQL is a high-performance, encrypted-by-default, native multimodel database written in Go. It unifies relational SQL, binary JSON, vector search (HNSW), full-text search (BM25), and geospatial types into **one ACID engine, one write-ahead log (WAL), and one cost-based query optimizer**.

NextSQL is completely native—it is not a wrapper, fork, or compatibility layer for PostgreSQL, MySQL, MongoDB, or Elasticsearch. It has its own 16 KiB page storage engine, SQL dialect, binary wire protocol, and first-party drivers.

---

## Multimodel in a Single Query

All data models live in the same table, share the same transaction, and execute within a single physical plan:

```sql
CREATE TABLE products (
    id          UUID PRIMARY KEY DEFAULT UUID(),
    name        STRING NOT NULL,
    description TEXT,
    price       DECIMAL(12,2),
    metadata    JSON,
    embedding   VECTOR<F32,1536>,
    location    POINT,
    created_at  TIMESTAMPTZ DEFAULT NOW()
);

CREATE FULLTEXT INDEX ix_desc ON products (description);
CREATE VECTOR INDEX ix_emb ON products (embedding) USING HNSW;

-- One plan: relational filter + JSON path + full-text BM25 + vector ANN
SELECT id, name, price
FROM products
WHERE metadata.category = 'headphones'
  AND price <= 150.00
SEARCH description FOR 'wireless noise cancelling'
NEAREST embedding TO $query
LIMIT 20;
```

---

## Key Highlights

- **Unified Multimodel Engine**: Relational, JSON (`NSJB`), vectors (`HNSW`), full-text (`BM25`), and geo (`WGS84`) within a single ACID transaction and shared cost model.
- **Encrypted by Default**: Transparent AES-256-GCM envelope encryption across data pages, WAL, undo logs, secondary indexes, and backups. Root unlock keys remain strictly outside the data volume.
- **Strict Durability & ACID**: WAL with group commit and `fsync` before commit acknowledgment. Strict MVCC with snapshot and serializable isolation.
- **High Availability**: Built-in Raft consensus clustering (minimum 3 nodes) for automatic failover and quorum-replicated writes without external dependencies.
- **First-Party Native Drivers**: Official drivers speaking the native binary protocol for Go, Node.js, Bun, Python, PHP, and Ruby. Passwords and keys are never sent in connection URLs.

---

## Quick Start

### 1. Install

```bash
go install github.com/bzync/nextsql/cmd/nextsql@latest
go install github.com/bzync/nextsql/cmd/nextsqld@latest
```

*Pre-built Linux (`.deb`, `.rpm`, `.run`) and Windows installers are available via `./scripts/build-installers.sh`.*

### 2. Initialize and Run

```bash
# Initialize encrypted data directory with a root key
nextsql init --data-dir /var/lib/nextsql --key-file /etc/nextsql/root.key --user app --password-file /tmp/nextsql.pw

# Start the daemon
nextsqld --data-dir /var/lib/nextsql --key-file /etc/nextsql/root.key --listen 127.0.0.1:7210 --user app --password-file /tmp/nextsql.pw

# Execute a query
nextsql exec --addr 127.0.0.1:7210 --user app --password-file /tmp/nextsql.pw --insecure -c "SELECT 'Hello, NextSQL!';"
```

*Note: Non-loopback network connections require TLS 1.3.*

---

## Documentation

- **[User Manual (`USAGE.md`)](USAGE.md)** — Comprehensive guide to installation, SQL dialect, drivers, backups, and operations.
- **[Architecture & Scope (`PROJECT.md`)](PROJECT.md)** — Canonical project specification and end-state design.
- **[Implementation Tracker (`TODO.md`)](TODO.md)** — Development roadmap, phase gates, and benchmark measurements.
- **[Engineering Contract (`SKILLS.md`)](SKILLS.md)** — Safety invariants, architecture discipline, and verification guidelines.
- **[Technical Documentation (`docs/`)](docs/)** — In-depth guides for [Storage](docs/storage-format.md), [WAL](docs/wal.md), [MVCC](docs/mvcc.md), [Optimizer](docs/optimizer.md), [Security](docs/security.md), and [HA](docs/ha.md).

---

## License

MIT ([`LICENSE`](LICENSE))
