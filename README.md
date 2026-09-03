# NextSQL

A high-performance, encrypted-by-default multimodel database. Relational SQL, native JSON, vector search, full-text search, and geospatial types share one ACID engine, one WAL, and one query optimizer.

NextSQL is a new database. It is not a PostgreSQL, MySQL, MongoDB, Elasticsearch, or vector-store compatibility layer. It has its own storage format, SQL dialect, wire protocol, and drivers.

Documentation source of truth:

```text
PROJECT.md = intended finished product
TODO.md    = current implementation/status truth
TODO.md    = implementation status, sequencing, dependencies, and phase gates
SKILLS.md  = engineering/agent contract
AGENTS.md  = repository agent entrypoint
USAGE.md   = current user/operator manual
README.md  = project overview and quick start
```

A feature described in `PROJECT.md` is not necessarily implemented. Check `TODO.md` and matching-version docs before treating a planned capability as shipped.

NextSQL's standards baseline is ISO/IEC 9075:2023 (including SQL/CLI,
SQL/PSM, SQL/MED, SQL/Schemata, SQL/MDA, and SQL/PGQ), ISO/IEC 9579:2000 RDA
principles, TCP with TLS 1.3, and Unicode/UTF-8. This is a design baseline, not
a blanket conformance claim; see [`docs/standards.md`](docs/standards.md).

**Version:** 0.1.0-dev · **Status:** P16 correctness/SLO closure open · **Module:** [`github.com/bzync/nextsql`](https://github.com/bzync/nextsql)

```sql

CREATE TABLE products (

    id          UUID PRIMARY KEY DEFAULT UUID(),

    account_id  UUID NOT NULL,

    name        STRING NOT NULL,

    description TEXT,

    price       DECIMAL(12,2),

    metadata    JSON,

    embedding   VECTOR<F32,1536>,

    location    POINT,

    created_at  TIMESTAMPTZ DEFAULT NOW()

);

CREATE INDEX ix_category ON products (metadata.category);

CREATE FULLTEXT INDEX ix_desc ON products (description);

CREATE VECTOR INDEX ix_emb ON products (embedding) USING HNSW;

SELECT id, name, price

FROM products

WHERE metadata.category = 'headphones'

  AND price <= 15000

SEARCH description FOR 'wireless noise cancelling'

NEAREST embedding TO $query

LIMIT 20;

```

That hybrid statement is one physical plan: structured filters, BM25, and ANN participate in the same cost model. The write path is the same WAL, MVCC, encryption, and crash recovery as a `DECIMAL` update.

---

## Why NextSQL

| Need | What NextSQL does |

|---|---|

| One system of record | Relational, JSON, vectors, full text, and geo live in the same table and the same transaction |

| Encryption by default | AES-256-GCM envelope encryption on pages, WAL, UNDO, indexes, vectors, full-text trees, backups, and spills. No custom ciphers |

| Keys stay off the data volume | External root unlock key via `--key-file`. Drivers never accept keys in a URL |

| Durable writes | Group-commit WAL + fsync before commit is acknowledged. Stolen files stay ciphertext |

| Honest operations | Official benches keep encryption, WAL, fsync, checksums, MVCC, and authentication on |

Priority order is fixed: correctness, durability, security, integrity, availability, latency, throughput, efficiency, developer experience, then features.

---

## Quick start

Requires Go 1.22+. Install the engine, then initialize a data directory.

Linux `.deb` / `.run` and Windows `setup.exe` installers:

```bash

./scripts/build-installers.sh

```

See [`packaging/README.md`](packaging/README.md). Or install with Go:

```bash

go install github.com/bzync/nextsql/cmd/nextsql\@latest

go install github.com/bzync/nextsql/cmd/nextsqld\@latest

```

For Docker or Podman installation with persistent encrypted storage, see
[`docs/docker.md`](docs/docker.md) and the checked-in `docker-compose.yml`.

Initialize a data directory, database root, and separate deployment-registry
root (mode `0600`). `--instance-key-file` defaults to
`--key-file.instance`. Keep both roots **off** the data volume.

```bash

printf 'secret\n' > /tmp/nextsql.pw

chmod 600 /tmp/nextsql.pw

nextsql init \\

  --data-dir /var/lib/nextsql \\

  --key-file /etc/nextsql/root.key \\

  --user app --password-file /tmp/nextsql.pw

```

For an existing pre-registry `DATA-DIR/nextsql.db`, stop `nextsqld` and run
`nextsql hosting adopt --data-dir DIR --key-file FILE --confirm`. Adoption
recovery-verifies and registers that exact default database without moving it
or discovering sibling files.

Start the server. Loopback may run without TLS; any non-loopback listen address requires TLS 1.3.

```bash

nextsqld \\

  --data-dir /var/lib/nextsql \\

  --key-file /etc/nextsql/root.key \\

  --listen 127.0.0.1:7210 \\

  --user app --password-file /tmp/nextsql.pw

```

Run SQL:

```bash

nextsql exec \\

  --addr 127.0.0.1:7210 \\

  --user app --password-file /tmp/nextsql.pw \\

  --insecure \\

  -c "CREATE TABLE items (id UUID PRIMARY KEY DEFAULT UUID(), name STRING NOT NULL, price DECIMAL(12,2));"

```

`--insecure` is loopback-only. Remote connections need `--tls-ca` on the client and `--tls-cert` / `--tls-key` on `nextsqld`.

---

## Architecture

```text

Native wire protocol → TLS 1.3 → authn → authz

        → SQL parser → binder / catalog

        → logical planner → cost optimizer

        → vectorized executor

              ├── relational   clustered B+Tree

              ├── JSON         binary NSJB + path indexes

              ├── vector       VECTOR<F32,N>, flat + HNSW

              ├── full-text    inverted index, BM25

              └── geo          POINT / BOX / LINESTRING / POLYGON

        → MVCC + row/range locks + UNDO

        → REDO WAL (group commit, fsync)

        → buffer manager

        → AES-256-GCM sealed pages

```

One engine, not four stores glued together. Catalog mutations, secondary indexes, HNSW graphs, and inverted postings all go through the same WAL and transaction.

Shipped CDC, automation, and bounded local partitioning surfaces, plus planned
follower-read, Studio, and Intelligence features, must preserve the same native
correctness/security model rather than bypassing the engine through hidden side
systems.

Logical page size is 16 KiB. On-disk and on-wire formats are versioned from day one. Unknown versions fail closed.

---

## SQL dialect

Unquoted identifiers fold to lowercase. Every table needs a `PRIMARY KEY`; that key is the clustered B+Tree key. Statements without `BEGIN` auto-commit.

### Types

| Type | Notes |

|---|---|

| `UUID` | `DEFAULT UUID()` |

| `STRING` / `TEXT` | UTF-8 |

| `DECIMAL(p,s)` | `1 ≤ p ≤ 38`; `DEFAULT AI()` when `s = 0` |

| `TIMESTAMPTZ` | UTC nanos, `DEFAULT NOW()` |

| `JSON` | Compact binary (`NSJB`); path extract and path indexes |

| `VECTOR<F32,N>` | Stored off-row; search metrics plus bounded norm/algebra/inspection |

| `POINT` / `LOCATION`, `BOX`, `LINESTRING`, `POLYGON` | WGS84; see [docs/geo.md](docs/geo.md) |

### Statements

`CREATE TABLE` (including `FOREIGN KEY` / `REFERENCES`; DML enforces `RESTRICT` / `NO ACTION` / `CASCADE` / `SET NULL` / `SET DEFAULT`), `CREATE INDEX` / `CREATE UNIQUE INDEX` (including `metadata.category`, `INCLUDE`, `WHERE`, and expression keys), `CREATE SPATIAL INDEX`, `CREATE FULLTEXT INDEX`, `CREATE VECTOR INDEX … USING HNSW`, `INSERT`, `SELECT` (`JOIN` / `LEFT` / `RIGHT` / `FULL OUTER` / `CROSS JOIN`, `GROUP BY`, `COUNT`/`SUM`/`AVG`/`MIN`/`MAX`), `UPDATE`, `DELETE`, `BEGIN` [`READ COMMITTED` | `SNAPSHOT` | `SERIALIZABLE`], `COMMIT`, `ROLLBACK`, `ANALYZE`, `EXPLAIN` [`ANALYZE`], `CREATE`/`DROP` `USER`/`ROLE`, `GRANT`/`REVOKE`. Shared row-tenancy statements are rejected; use hosted realm/database isolation.

```sql

-- Relational

SELECT id, name, price FROM products WHERE price BETWEEN 1000 AND 5000;

-- JSON path

SELECT * FROM products WHERE metadata.category = 'electronics';

-- Full text (AND tokens; "quoted" phrases)

SELECT * FROM products SEARCH description FOR 'wireless noise cancelling' LIMIT 20;

-- Vectors

SELECT id, name FROM products NEAREST embedding TO $query USING COSINE LIMIT 20;
SELECT VECTOR_DIM(embedding), VECTOR_NORM(embedding), VECTOR_NORMALIZE(embedding) FROM products;

-- Isolation

BEGIN SNAPSHOT;

UPDATE products SET price = price * 1.1 WHERE id = $1;

COMMIT;

```

`SERIALIZABLE` is lock-based (strict 2PL on the snapshot), not SSI. See [docs/sql.md](docs/sql.md) and [docs/mvcc.md](docs/mvcc.md).

Modern SQL examples:

```sql
SELECT DISTINCT metadata.category
FROM products
WHERE price > 0;

WITH expensive AS (
    SELECT id, name, price
    FROM products
    WHERE price >= 10000
)
SELECT name,
       ROW_NUMBER() OVER (ORDER BY price DESC) AS rn
FROM expensive;

UPSERT INTO products (id, account_id, name, price)
VALUES ($1, $2, $3, $4)
ON UNIQUE (id)
SET price = excluded.price
RETURNING id, price;
```

Schema lifecycle and maintenance:

```sql
DROP INDEX IF EXISTS ix_old;
REBUILD INDEX ix_category;        -- blocking
MAINTAIN TABLE products;
```

---

## Drivers

Official drivers speak the native NSQL protocol. **Keys and passwords never go in a URL.** TLS 1.3 is required off loopback.

| Runtime | Path | Open |

|---|---|---|

| Go | [`drivers/go`](drivers/go) | `nextsql.Open(nextsql.Config{…})` |

| Node.js 18+ | [`drivers/node`](drivers/node) | `connect({ address, user, password, tls })` |

| Bun | [`drivers/bun`](drivers/bun) | same shape as Node |

| Deno | [`drivers/deno`](drivers/deno) | `import { connect } from "./mod.ts"` |

| PHP 8.1+ | [`drivers/php`](drivers/php) | `NextSQL\Client::connect([…])` |
| Python 3.10+ | [`drivers/python`](drivers/python) | `nextsql.connect(nextsql.Config(…))` |
| Ruby 3.0+ | [`drivers/ruby`](drivers/ruby) | `NextSQL.connect(NextSQL::Config.new(…))` |

Shared TypeScript types live in [`drivers/js`](drivers/js).

The Go driver also exposes `ExecIdempotent(ctx, key, sql, params...)` for
durably retryable mutations. Repeating the same typed request returns its
committed result; using the key for another request returns `conflict`. Eligible
autocommit SELECT results use the engine's bounded WAL-invalidated result cache
automatically.

```go

conn, err := nextsql.Open(nextsql.Config{

    Address:       "127.0.0.1:7210",

    User:          "app",

    Password:      password,

    InsecureNoTLS: true, // loopback only

})

res, err := conn.Exec(ctx, `SELECT name FROM items WHERE price < $1`,

    types.MustDecimal("50.00"))

```

```js
const conn = await connect({
  address: "127.0.0.1:7210",

  user: "app",

  password: process.env.NEXTSQL_DATABASE_PASS,

  insecureNoTLS: true,
});

const res = await conn.exec("SELECT name FROM items WHERE price < $1", [
  { kind: "decimal", value: "50.00" },
]);
```

When `nextsqld` is started with `--require-client-key`, the first authenticated client supplies the 32-byte root over TLS (`key` / `KeyProvider`). The host does not keep a long-lived key file.

---

## Security

This is the contract. NextSQL is not unhackable, not “100% secure,” and does not survive a live unlocked host compromise.

| Attacker | Gets | Does not get |

|---|---|---|

| Stolen disks, snapshots, WAL, backups, vector or full-text trees | Ciphertext, wrapped DEKs, key IDs | Plaintext, unless they also have an authorized root unlock key |

| Network observer on a remote connection | TLS 1.3 records | SQL, passwords, unlock material |

| Privileged attacker on a **live unlocked** `nextsqld` | Keys, pages, and rows in RAM | Nothing the process can hide. It must decrypt to execute SQL |

Envelope hierarchy (separate DEKs, no one key for every purpose):

```text

External root unlock key     (--key-file, never in the data directory)

        → KEK → database master

              → page / WAL / UNDO / backup / vector / full-text / temp / replication DEKs

```

Also in the production surface:

- Password auth, RBAC (`GRANT` / `REVOKE`), session audit log

- Hosted realm/database registry foundation; legacy shared-tenant tables fail closed for non-`ADMIN` migration safety

- Online DEK rotation, key-version revocation (kills sessions), crypto-shred of the keystore

- Experimental `ENCRYPTED CLIENT`: randomized server-opaque `NSCE1.` fields
  with Go, Node.js/TypeScript, Bun, Deno, and PHP helpers; PITR and HA coverage
  remain open

- TLS 1.3 required for non-loopback listen addresses

Details: [docs/security.md](docs/security.md).

---

## Commands

### `nextsql` — CLI

```text

nextsql init     --data-dir DIR --key-file FILE [--instance-key-file FILE]

                 [--realm NAME --database NAME] [--user NAME --password-file FILE]

                 [--env-file PATH | --no-env]

nextsql hosting  adopt --data-dir DIR --key-file FILE [--instance-key-file FILE]

                 [--realm NAME --database NAME] --confirm

                 [--env-file PATH | --no-env]

nextsql exec     [--addr HOST:PORT] [--user NAME] [--password-file FILE]

                 [--database NAME] [--tls-ca FILE | --insecure]

                 [--env-file PATH | --no-env]

                 [-c SQL | SQL]

nextsql migrate  status|pending|version|validate|create|up|down|force|repair

                 [--dir DIR] [--addr HOST:PORT] [--user NAME]

                 [--password-file FILE] [--tls-ca FILE | --insecure]

                 [--env-file PATH | --no-env]

nextsql backup   --data-dir DIR --key-file FILE --out DIR

nextsql restore  --from DIR --data-dir DIR --key-file FILE

                 [--wal-archive DIR] [--until-lsn N | --until RFC3339]

nextsql verify   --from DIR --key-file FILE

nextsql export   --data-dir DIR --key-file FILE --out DIR

nextsql import   --from DIR --data-dir DIR --key-file FILE

nextsql diagnose --data-dir DIR

nextsql status             # server ping (mode server)

nextsql status --local --data-dir DIR --key-file FILE

nextsql cluster status --data-dir DIR

nextsql version

```

A backup is not valid until `verify` (including a restore test) succeeds. Same rule for export / import.

`nextsql migrate` is always server mode: it never reads `--data-dir` or the root unlock key. Prefer `NEXTSQL_DATABASE_PASSWORD_FILE`. Do not put the root key in the application `.env`. See [USAGE.md](USAGE.md#14-schema-migrations).

Migration parsing/validation now understands shipped `DROP INDEX` syntax. Forward-only migrations remain the recommended deployment model.

### `nextsqld` — server

```text

nextsqld --data-dir DIR --key-file FILE [--instance-key-file FILE]

         [--listen 127.0.0.1:7210] [--config FILE]

         [--env-file PATH | --no-env]

         [--tls-cert FILE --tls-key FILE [--tls-client-ca FILE [--tls-client-crl FILE]]]

         [--require-client-key]

         [--user NAME --password-file FILE]

         [--wal-archive DIR]

         [--node-id ID --raft-bind ADDR --raft-join id=addr,... [--raft-bootstrap]]

```

Optional `key=value` config file (`--config`). Unknown keys are rejected. Common keys: `data_dir`, `key_file`, `listen_addr`, `buffer_pages`, `tls_cert`, `tls_key`, `tls_client_ca`, `tls_client_crl`, `require_client_key`, `wal_archive`, `max_inflight_queries`, `max_query_queue`, `query_queue_wait_ms`, `max_result_rows`, `node_id`, `raft_bind`, `raft_join`, `raft_bootstrap`.

`nextsqld` reloads the configured server certificate/key, mTLS trust bundle,
and optional PEM CRL bundle on `SIGHUP`. Invalid reloads retain the last
known-good snapshot; successful mTLS reloads disconnect all accepted
connections, including in-progress handshakes, so clients reauthenticate
against the new trust and revocation state.

Hosting is dotenv-integrated. `NEXTSQL_DATA_DIR`, `NEXTSQL_KEY_FILE`,
`NEXTSQL_INSTANCE_KEY_FILE`, `NEXTSQL_REALM_NAME`, `NEXTSQL_DATABASE`,
`NEXTSQL_BUFFER_PAGES`, `NEXTSQL_SERVER_USER`, and
`NEXTSQL_SERVER_PASSWORD_FILE` (preferred) or `NEXTSQL_SERVER_PASS` can drive
init/adoption; `NEXTSQL_HOSTING_CONFIRM=true` enables non-interactive adoption.
`nextsqld` consumes the matching paths, buffer pages, server credentials, and
`NEXTSQL_ADDR` as its listen address. These server credentials never become a
client `NEXTSQL_DATABASE_USER` login fallback. Explicit flags win. Key values are paths,
never raw key bytes. Keep host provisioning env files mode `0600` and out of
source control.

The complete authoritative variable reference and secure hosting/client
examples are in [`ENV.md`](ENV.md).

Admission defaults: 32 in-flight queries, 128 queued, 5 s wait. Overload is rejected (`unavailable`) instead of growing without bound.

### `nextsql-bench` — official measurements

```text

nextsql-bench [--quick] [--slo]

              [--workload all|page|point|range|insert|update|delete|txn|join|agg|json|fulltext|vector|hybrid]

              [--duration 1s] [--rows 128] [--concurrency 1]

```

`--slo` is the labeled suite (hardware, filesystem, encryption, durability, cache condition printed on every row). Numbers from one host are not product guarantees. See [docs/ops.md](docs/ops.md).

---

## High availability

Optional Raft cluster (hashicorp/raft; NextSQL does not invent consensus). Minimum **3 voting nodes**.

A write is acknowledged only after the leader flushes its local WAL **and** a quorum commits the sealed replication batch. If there is no leader, writes fail closed. SQL is not re-executed on followers, so `UUID()` / `NOW()` / `AI()` stay deterministic.

Engineering targets on a healthy 3-node cluster: leader election `< 3 s`, service recovery `< 5 s`. Continuous service is a design objective (`≥ 99.999%` availability SLO), not a zero-downtime claim. See [docs/ha.md](docs/ha.md).

```bash

nextsqld --data-dir /var/lib/nextsql --key-file /etc/nextsql/root.key \\

  --tls-cert cert.pem --tls-key key.pem --listen 0.0.0.0:7210 \\

  --node-id n1 --raft-bind 10.0.0.1:7211 \\

  --raft-join n1=10.0.0.1:7211,n2=10.0.0.2:7211,n3=10.0.0.3:7211 \\

  --raft-bootstrap

```

---

## Roadmap

The intended post-P16 roadmap is:

```text
P19  WORKFLOW / TRIGGER / SCHEDULE / TASK
P20  CDC / Change Streams
P21  Native Table Partitioning
P22  Follower Reads / Read Scaling
P23  Vector Engine 2.0
P24  Full-text Search 2.0
P25  Security 2.0
P26  System Catalog / Introspection 2.0
P27  Operational Maturity / Workload Governance
P28  Professional Installer + NextSQL Manager
P29  Web-based NextSQL Studio
P30  NextSQL Intelligence + Built-in RAG
```

Roadmap rules:

- WORKFLOW is directly runnable.
- TRIGGER invokes WORKFLOW rather than creating a second procedure language.
- SCHEDULE invokes WORKFLOW.
- asynchronous/scheduled executions become durable TASK records.
- CDC emits committed changes only.
- tenant partitioning never replaces authorization.
- follower reads expose explicit consistency semantics.
- ANN performance always reports recall.
- Studio uses native NextSQL APIs and never bypasses RBAC.
- Intelligence is optional and never overrides parser, binder, optimizer, catalog, tenant policy, RBAC, or server validation.

---

## Repository

```text

cmd/nextsqld          server

cmd/nextsql           CLI

cmd/nextsql-bench     official benchmark tool

internal/             engine (storage, WAL, MVCC, SQL, crypto, HA, …)

drivers/              Go, Node, Bun, Deno, PHP + shared JS codec

tests/                integration, crash, HA

docs/                 format and operations notes

packaging/            Linux and Windows installer sources

scripts/              installer build scripts

```

| Document | Topic |

|---|---|

| [docs/web](docs/web) | Product landing and documentation site (`npm run dev` in that directory) |

| [PROJECT.md](PROJECT.md) | Intended finished NextSQL product/end-state |
| [TODO.md](TODO.md) | Current implementation status, open gates, measurements |
| [ROADMAP.md](ROADMAP.md) | Simplified, non-authoritative roadmap derived from `TODO.md` |
| [SKILLS.md](SKILLS.md) | Engineering and AI-agent operating contract |
| [AGENTS.md](AGENTS.md) | Repository-level instructions for coding agents |
| [USAGE.md](USAGE.md) | End-to-end user manual (install, SQL, migrate, drivers, backup, HA) |

| [docs/sql.md](docs/sql.md) | Dialect, types, catalog |

| [docs/optimizer.md](docs/optimizer.md) | Rewrites, costing, hybrid plans, `EXPLAIN` |

| [docs/execution.md](docs/execution.md) | Vectorized batches, budgets |

| [docs/json.md](docs/json.md) | Binary JSON and path indexes |

| [docs/fulltext.md](docs/fulltext.md) | Tokenizer, BM25, inverted index |

| [docs/vector.md](docs/vector.md) | `VECTOR<F32,N>`, HNSW, distances |

| [docs/geo.md](docs/geo.md) | WGS84 types and predicates |

| [docs/storage-format.md](docs/storage-format.md) | Pages, identity, versions |

| [docs/btree.md](docs/btree.md) | Clustered B+Tree |

| [docs/wal.md](docs/wal.md) | WAL, checkpoints |

| [docs/mvcc.md](docs/mvcc.md) | Isolation, UNDO |

| [docs/protocol.md](docs/protocol.md) | Native wire protocol |

| [docs/security.md](docs/security.md) | Keys, TLS, RBAC, tenants |

| [docs/backup.md](docs/backup.md) | Backup, restore, PITR |

| [docs/export.md](docs/export.md) | Logical export / import |

| [docs/ops.md](docs/ops.md) | Metrics, admission, SLO numbers |

| [docs/ha.md](docs/ha.md) | Raft clustering |

For implementation status, sequencing, dependencies, and phase gates, `TODO.md` is authoritative. `PROJECT.md` defines the intended end-state, `ROADMAP.md` provides a simplified roadmap, and `SKILLS.md` / `AGENTS.md` define the engineering and agent contracts.

---

## Status

NextSQL remains **0.1.0-dev**.

Current development state:

```text
P0–P15  complete
P16      open — correctness / SLO closure
P17      complete except REBUILD INDEX ... ONLINE is deferred
P18      implementable scope complete; partition-wise agg/join now unblocked by P21
P19      complete — native v1 plus clean repository-wide functional gate
P20      complete — native committed CDC/change streams
P21      complete — RANGE/HASH/LIST partitioning, local indexes, cross-partition UNIQUE/UPSERT, statistics, benchmarks, and offline legacy TENANT migration
P22–P30 planned/open
```

P17 now includes shipped schema/storage-lifecycle work such as:

- `DROP INDEX`
- blocking `REBUILD INDEX`
- page reclamation
- durable freelist reuse
- MVCC-safe garbage cleanup
- bounded maintenance
- `MAINTAIN DATABASE`
- `MAINTAIN TABLE`
- `MAINTAIN INDEX`

P18 includes the modern SQL completeness surface such as:

- `DISTINCT`
- `HAVING`
- searched/simple `CASE`
- set operations
- scalar and correlated subqueries
- CTEs and recursive CTEs
- window functions
- UPSERT
- DML `RETURNING`
- covering, partial, and expression indexes
- Top-N optimization
- improved join reordering

P16 is complete: its exit gate is green (corrected 1M-vector HNSW v10 at p95
**8.061 ms**, recall@10 **1.000**, recall@100 **0.998**; 10M DELETE published;
crash-during-merge recovers `Check()`-clean; 100M analytics `< 60 s`; 10M
INSERT/UPDATE published; security sign-off). The terminal 100M-operation B+Tree
invariant soak is a deferred standalone measurement, not a release gate — paper-
closed 2026-08-30 with the same disposition as P18. P23 Vector Engine 2.0 is
complete (production-gating sign-off 2026-08-31). P24 Full-text Search 2.0 is
complete (exit gate closed 2026-08-31). P25 Security 2.0 is complete (exit
gate closed 2026-09-02): mTLS, short-lived credentials, the external-IdP
broker, field-level client encryption (including all official drivers, PITR,
HA/failover, and durable key rotation/revocation), Argon2id password hashing,
and audit-chain hardening are all production-gated. P26 System Catalog /
Introspection 2.0 is complete (exit gate closed 2026-09-02): the virtual
`system` schema, live session/security-administration tables, `SHOW`
aliases, and an authoritative capability registry are all production-gated.
The current release gate is P27 Operational Maturity / Workload Governance.

Large sequential `DELETE` is correct after the leaf-merge fix and its 10M timing methodology is published. The tracker also records published 100M analytics results.

The P19 native v1 increment and its repository-wide functional gate are
complete:

```text
P19 WORKFLOW
→ TRIGGER
→ SCHEDULE
→ TASK runtime
```

P20 CDC is complete. P21 native table partitioning is complete: RANGE/HASH/LIST
with one-to-eight-column keys, ADD/DROP and validated ATTACH/DETACH ownership
transfer, partition-local B+Tree-family/FULLTEXT/HNSW indexes, cross-partition
secondary UNIQUE, partition-aware `UPSERT`, stable-ID statistics and costing,
bounded maintenance, backup/restore/PITR, `nextsql-bench --partition`, a
randomized pruning-soundness property test, and explicit offline migration of a
legacy `tenant_id` / `PARTITION BY TENANT` database into an isolated hosted
deployment (`nextsql hosting migrate-tenant`). Distributed sharding is a
separate future phase. The roadmap then continues through follower reads, Vector
Engine 2.0, Full-text Search 2.0, Security 2.0, system introspection, workload
governance, Installer/Manager, web-based Studio, and NextSQL Intelligence/RAG.

P22–P26 are complete; P27+ capabilities remain open until their `TODO.md` gates are green. P19
syntax and semantics are documented in `docs/workflows.md`; P20 and P21 are
documented in `docs/cdc.md` and `docs/partitioning.md`.

Run the relevant validation suites on your target hardware:

```bash
go test ./...
go test -race ./...          # needs a C compiler
go test ./tests/integration ./tests/crash ./tests/ha
```

Treat NextSQL as an engine under measurement, not a drop-in production replacement, until the relevant SLO, crash/recovery, security, and HA suites are green on your deployment environment.

---

## License

Proprietary. Driver packages are marked unlicensed / proprietary and are not published as public packages.
