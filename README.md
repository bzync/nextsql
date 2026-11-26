# NextSQL

[![Release](https://img.shields.io/badge/release-v0.0.10-0ea5e9)](https://github.com/bzync/nextsql/releases/tag/v0.0.10)
[![Go Version](https://img.shields.io/badge/go-1.24%2B-00ADD8)](go.mod)
[![License: MIT](https://img.shields.io/badge/license-MIT-22c55e)](LICENSE)
[![Website & Docs](https://img.shields.io/badge/docs-nextsql.bzync.com-6366f1)](https://nextsql.bzync.com/docs/introduction)

**NextSQL** is a high-performance, encrypted-by-default multimodel database engine written in Go. Relational SQL, binary JSON (`NSJB`), dense/quantized/sparse vector search (`HNSW`, `IVF`, `IVF-PQ`, `SPARSE`), full-text search (`BM25`), nested collections (`STRUCT`, `ARRAY`, `MAP`), and geospatial types (`WGS84`, `GEOMETRY`, `GEOGRAPHY`) share **one ACID storage engine, one write-ahead log, and one deterministic cost-based query optimizer**.

It is a native database from the ground up — not a wrapper, fork, or protocol facade over PostgreSQL, MySQL, MongoDB, or Elasticsearch. It ships with its own 16 KiB encrypted page engine, SQL dialect, binary wire protocol (**NSQL v1**), unified administration & IDE suite (**`nextsql-admin`**), and zero-dependency official client drivers for six languages.

---

## Table of Contents

- [Multimodel in a Single Query](#multimodel-in-a-single-query)
- [Capabilities at a Glance](#capabilities-at-a-glance)
- [Architecture Overview](#architecture-overview)
- [Quick Start](#quick-start)
- [Official Client Drivers](#official-client-drivers)
- [Repository Layout](#repository-layout)
- [Developer Workflow](#developer-workflow)
- [Documentation Index](#documentation-index)
- [Contributing & Security](#contributing--security)
- [License](#license)

---

## Multimodel in a Single Query

All data models live in the same table, share the same ACID transaction, and execute within a single cost-optimized physical plan with **Reciprocal Rank Fusion (RRF)**:

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

CREATE FULLTEXT INDEX ix_desc ON products (description) WITH (ANALYZER = 'english');
CREATE VECTOR INDEX ix_emb ON products (embedding) USING HNSW;
CREATE SPATIAL INDEX ix_loc ON products (location);

-- One physical plan: relational filter + JSON path + full-text BM25 + vector ANN
SELECT id, name, price, SNIPPET(description)
FROM products
WHERE metadata.category = 'headphones'
  AND price <= 150.00
  AND DWITHIN(location, POINT(-73.9857, 40.7484), 5000)
SEARCH description FOR 'wireless noise cancelling'
NEAREST embedding TO $query_vec
LIMIT 20;
```

---

## Capabilities at a Glance

| Subsystem | Native Capabilities |
|---|---|
| **Storage & Encryption** | 16 KiB logical / 16,448-byte physical pages; mandatory AES-256-GCM envelope encryption across pages, WAL, UNDO, spill files, backups, and Raft logs; external root key (`NSKY`) and offline recovery key (`NSRK`); online `check-pages` and `NSQI` page isolation with live WAL repair. |
| **Transactions & WAL** | Undo-oriented MVCC (`READ COMMITTED`, `SNAPSHOT`, strict-2PL `SERIALIZABLE`); lock-free group-commit WAL with `fdatasync` durability; compact `PageDelta` v1 logging; fuzzy checkpoints; size-capped or PITR WAL retention. |
| **Relational & Partitioning** | Clustered B+Tree tables; secondary, unique, covering (`INCLUDE`), partial (`WHERE`), and expression indexes; joins (`INNER`, `LEFT`, `RIGHT`, `FULL`, semi/anti); window functions; recursive CTEs; native `RANGE`, `HASH`, and `LIST` table partitioning with zero-copy `ATTACH`/`DETACH`. |
| **Vector Search** | `VECTOR<F32,N>`, `VECTOR<F16,N>`, `VECTOR<I8,N>`, `BITVECTOR<N>` (`HAMMING`), and `SPARSEVECTOR<N>`; `HNSW` (with optional F16/I8 traversal quantization), `IVF`, `IVF-PQ`, and `SPARSE` inverted indexes; dense + sparse + BM25 RRF fusion. |
| **Full-Text Search** | Multi-column `CREATE FULLTEXT INDEX` (1–8 columns); BM25 ranking; `simple`, `english` (Porter2 + stop words + synonyms), `french`, `german`, and `spanish` analyzers; phrase, prefix (`*`), fuzzy (`~`), typo tolerance, `HIGHLIGHT`, `SNIPPET`, field `WEIGHT`, and `FACET` histograms. |
| **JSON, Collections & Geo** | Binary `NSJB` JSON with sargable path indexes; typed `STRUCT`, `ARRAY`, `MAP`, and `UNNEST`; WGS84 `POINT`/`BOX`/`LINESTRING`/`POLYGON` plus `GEOMETRY`/`GEOGRAPHY` with Morton Z-order spatial indexes. |
| **Automation & CDC** | Native `WORKFLOW`, row-level `TRIGGER`, `SCHEDULE` (`EVERY`, `AT`, `CRON`), and durable `TASK` queue; committed-only WAL change streams (`SUBSCRIBE TO table AFTER lsn`) with backpressure. |
| **Security & Governance** | Argon2id password hashing; role-based access control (RBAC); TLS 1.3 & mTLS with CRL/OCSP; short-lived Ed25519 credentials (`NSSC1.`); OIDC/SSO `nextsql-auth-broker`; client-side field encryption (`NSCE1.` / `NSCE2.`); signed tamper-evident `NSAC` audit chains; workload resource groups. |
| **High Availability** | Built-in Raft consensus (3+ voting nodes); quorum-synchronous commits (`RPO = 0`); linearizable `STRONG`, `BOUNDED`, and `STALE` follower reads with cluster-aware driver routing. |
| **NextSQL Admin & Studio** | Unified `nextsql-admin` binary providing **Setup** wizard, **Operations** console (metrics, backups, cluster, security), and **Studio** IDE (catalog-aware IntelliSense, binder quick-fixes, interactive visual DAG `EXPLAIN` plans, and native multimodel explorers). |

---

## Architecture Overview

```text
Clients / CLI / Official Drivers / NextSQL Admin (Setup · Operations · Studio)
                                    │
                        Native NSQL v1 Wire Protocol
                     (TLS 1.3 · Authn · mTLS · OIDC · RBAC)
                                    │
            SQL Lexer & Parser ──► Binder & Catalog (NSCT v14)
                                    │
              Logical Planner ──► Deterministic Cost Optimizer
                                    │
                 Vectorized & Parallel Execution Engine
               (Bounded CPU/RAM/I/O Budgets · Encrypted Spill)
        ┌─────────────┬─────────────┼─────────────┬─────────────┐
        ▼             ▼             ▼             ▼             ▼
   Relational       JSON        Full-Text      Vectors     Geospatial
 (B+Tree/Parts)    (NSJB)        (BM25)     (HNSW/IVF-PQ)    (WGS84)
        └─────────────┴─────────────┼─────────────┴─────────────┘
                                    ▼
                 MVCC (NSRV) + Strict 2PL Locks + UNDO Log
                                    │
               Group-Commit REDO WAL (NSWL + PageDelta v1)
                                    │
            Buffer Manager ──► 16 KiB AES-256-GCM Sealed Pages
```

---

## Quick Start

### 1. Install

Build from source with Go 1.24+ or download pre-built Linux packages (`.deb`, `.tar.gz`, `.run`) from [GitHub Releases (v0.0.10)](https://github.com/bzync/nextsql/releases/tag/v0.0.10):

```bash
# Build all binaries (nextsqld, nextsql, nextsql-admin, nextsql-bench, nextsql-auth-broker) into ./bin/
make build
```

On Windows hosts, run NextSQL inside [WSL 2](docs/install.md#windows-wsl-2). For container deployments, see the [Docker](docs/docker.md) and [Docker Compose](docs/docker-compose.md) guides (`bzynchub/nextsql:0.0.10`).

### 2. Initialize, Serve, and Query

```bash
# 1. Create a password file and initialize an encrypted deployment
printf 's3cret-pass\n' > /tmp/nextsql.pw && chmod 600 /tmp/nextsql.pw

./bin/nextsql init \
  --data-dir /tmp/nextsql-data \
  --key-file /tmp/nextsql-root.key \
  --user app \
  --password-file /tmp/nextsql.pw \
  --database app

# 2. Start the database daemon on loopback
./bin/nextsqld \
  --data-dir /tmp/nextsql-data \
  --key-file /tmp/nextsql-root.key \
  --listen 127.0.0.1:7210 \
  --user app \
  --password-file /tmp/nextsql.pw

# 3. Run SQL queries (supports --table, --expanded/-x, --csv, --json, --time/-t)
./bin/nextsql exec \
  --addr 127.0.0.1:7210 \
  --user app \
  --password-file /tmp/nextsql.pw \
  --database app \
  --insecure \
  --table \
  -c "SELECT 'Hello, NextSQL!' AS greeting, NOW() AS server_time;"
```

### 3. Launch NextSQL Admin & Studio

```bash
./bin/nextsql-admin --server-addr 127.0.0.1:7210 --insecure
# Open http://127.0.0.1:7220 in your browser
```

---

## Official Client Drivers

All six official drivers speak the native **NSQL v1** binary protocol directly over TLS 1.3, support connection pooling with client-side LRU prepared statement caching, cluster follower-read routing (`STRONG` / `BOUNDED` / `STALE`), and client-side field encryption (`NSCE1.` / `NSCE2.`). Passwords and encryption keys are **never** placed in connection URLs.

| Language | Package / Module | Install Command | Source & Guide |
|---|---|---|---|
| **Go** | `github.com/bzync/nextsql/drivers/go` | `go get github.com/bzync/nextsql/drivers/go` | [`drivers/go`](drivers/go) · [Docs](docs/web/content/docs/drivers-go.md) |
| **Node.js / TS** | `@bzync/nextsql` | `npm i @bzync/nextsql@0.0.3` | [`drivers/node`](drivers/node) · [Docs](docs/web/content/docs/drivers-js.md) |
| **Bun** | `drivers/bun` | Import from `drivers/bun/nextsql.js` | [`drivers/bun`](drivers/bun) · [Docs](docs/web/content/docs/drivers-js.md) |
| **Python** | `bzync-nextsql` | `pip install bzync-nextsql==0.0.3` | [`drivers/python`](drivers/python) · [Docs](docs/web/content/docs/drivers-python.md) |
| **PHP** | `bzync/nextsql` | `composer require bzync/nextsql` | [`drivers/php`](drivers/php) · [Docs](docs/web/content/docs/drivers-php.md) |
| **Ruby** | `bzync-nextsql` | `gem install bzync-nextsql -v 0.0.3` | [`drivers/ruby`](drivers/ruby) · [Docs](docs/web/content/docs/drivers-ruby.md) |

---

## Repository Layout

```text
cmd/
  ├── nextsqld/             Core database server daemon
  ├── nextsql/              Operational, migration, backup, and query CLI
  ├── nextsql-admin/        Unified web UI & API (Setup, Operations, and Studio IDE)
  ├── nextsql-auth-broker/  OIDC / SSO token-exchange broker daemon
  └── nextsql-bench/        Official SLO and multimodel benchmark harness
internal/                   Core engine packages (storage, wal, recovery, txn, undo,
                            sql, executor, catalog, crypto, security, auth, protocol,
                            replication, vector, fulltext, json, cdc, scheduler, admin…)
drivers/                    Official Go, Node.js, Bun, Python, PHP, Ruby drivers + JS codec
tests/                      Cross-package integration, crash-injection, fault, HA, & upgrade suites
docs/                       Authoritative subsystem specifications and docs/web Next.js site
packaging/ & scripts/       Linux installer builders (.deb, .rpm, .tar.gz, .run) and CI gates
```

---

## Developer Workflow

Run `make help` to view all build, lint, test, and verification targets:

```bash
make doctor          # Audit local developer toolchain and driver runtimes
make build           # Build all binaries into ./bin/
make fmt             # Format all Go source files with gofmt -s
make lint            # Run gofmt check, go vet, and Admin frontend TypeScript typecheck
make test-unit       # Run fast CPU-bound unit tests
make test-race       # Run Go race detector on concurrent engine packages
make test-drivers    # Run test suites across all 6 official client drivers
make test-admin-ui   # Run Studio/Setup tests and headless Chrome axe WCAG 2.2 AA audit
make test-upgrade    # Replay retained release fixtures (v0.0.1..v0.0.10 clean & dirty)
make build-docs      # Build static documentation site (docs/web) and verify search index
make clean           # Remove build artifacts and temporary test workspaces
```

---

## Documentation Index

- **[Complete Documentation Index (`docs/README.md`)](docs/README.md)** — organized guide to all 30+ technical and operational manuals
- **[Usage Manual (`USAGE.md`)](USAGE.md)** — comprehensive CLI, SQL, configuration, and operations reference
- **[Architecture (`ARCHITECTURE.md`)](ARCHITECTURE.md)** & **[Performance (`PERFORMANCE.md`)](PERFORMANCE.md)**
- **[Environment Variables (`ENV.md`)](ENV.md)** & **[Compatibility (`COMPATIBILITY.md`)](COMPATIBILITY.md)**
- **[Changelog (`CHANGELOG.md`)](CHANGELOG.md)** — release history through **v0.0.10**
- **Product & Documentation Website:** [nextsql.bzync.com](https://nextsql.bzync.com)

---

## Contributing & Security

- **Contributing:** Read [`CONTRIBUTING.md`](CONTRIBUTING.md) and [`DEVELOPMENT.md`](DEVELOPMENT.md) before opening a pull request.
- **Security Policy:** See [`SECURITY.md`](SECURITY.md) for our threat model and private vulnerability reporting instructions.
- **Support:** See [`SUPPORT.md`](SUPPORT.md) for platform support and issue reporting guidelines.

---

## License

MIT — see [`LICENSE`](LICENSE).
