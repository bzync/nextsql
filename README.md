# NextSQL

NextSQL is a high-performance, encrypted-by-default multimodel database written in Go. Relational SQL, binary JSON, vector search (HNSW), full-text search (BM25), and geospatial types share **one ACID engine, one write-ahead log, and one cost-based query optimizer**.

It is a new database — not a wrapper, fork, or compatibility layer for PostgreSQL, MySQL, MongoDB, or Elasticsearch. It has its own 16 KiB page storage engine, SQL dialect, binary wire protocol (NSQL v1), and first-party drivers.

The current release is **0.0.4** (preview). Linux packages are on [GitHub Releases](https://github.com/bzync/nextsql/releases/tag/v0.0.4). On Windows, run them inside [WSL 2](docs/install.md#windows-wsl-2).

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

## Highlights

- **One engine for every model.** Relational columns, JSON (`NSJB`), vectors (`HNSW`), full-text (`BM25`), and geo (`WGS84`) share a single ACID transaction and cost model.
- **Encrypted by default.** AES-256-GCM envelope encryption on data pages, WAL, undo, indexes, and backups. The root unlock key stays off the data volume.
- **Durable commits.** Group-commit WAL and `fsync` before a commit is acknowledged. MVCC with snapshot and serializable isolation.
- **Built-in HA.** Optional Raft cluster (three voting nodes minimum). Writes wait for a quorum. No external coordinator.
- **Official drivers.** Go, Node.js, Bun, Python, PHP, and Ruby speak NSQL v1. Passwords and keys never go in a connection URL.

---

## Quick Start

### 1. Install

```bash
go install github.com/bzync/nextsql/cmd/nextsql@latest
go install github.com/bzync/nextsql/cmd/nextsqld@latest
```

Pre-built Linux installers (`.deb`, `.tar.gz`, `.run`) are on the [v0.0.4 release](https://github.com/bzync/nextsql/releases/tag/v0.0.4). Native Windows is not supported; on Windows, run NextSQL inside [WSL 2](docs/install.md#windows-wsl-2).

### 2. Initialize and Run

```bash
# Initialize encrypted data directory with a root key
nextsql init --data-dir /var/lib/nextsql --key-file /etc/nextsql/root.key --user app --password-file /tmp/nextsql.pw --database app

# Start the daemon
nextsqld --data-dir /var/lib/nextsql --key-file /etc/nextsql/root.key --listen 127.0.0.1:7210 --user app --password-file /tmp/nextsql.pw

# Execute a query
nextsql exec --addr 127.0.0.1:7210 --user app --password-file /tmp/nextsql.pw --insecure -c "SELECT 'Hello, NextSQL!';"
```

*Note: Non-loopback network connections require TLS 1.3.*

---

## Documentation

- **[Usage manual](USAGE.md)** — install, SQL, drivers, backups, and operations
- **[Changelog](CHANGELOG.md)** — what shipped in 0.0.4
- **[Support](SUPPORT.md)** and **[Security](SECURITY.md)**
- **[docs/](docs/)** — storage, WAL, MVCC, optimizer, security, and HA
- Product site: [nextsql.bzync.com](https://nextsql.bzync.com)

---

## License

MIT ([`LICENSE`](LICENSE))
