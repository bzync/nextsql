# Introduction

NextSQL is a high-performance, encrypted-by-default multimodel database. Relational SQL, native JSON, vector search, full-text search, and geospatial types share **one ACID engine**, one WAL, and one query optimizer.

It is a new database. It is not PostgreSQL, MySQL, MongoDB, Elasticsearch, or a vector-store compatibility layer. It has its own storage format, SQL dialect, wire protocol (**NSQL v1**), and official drivers.

Install the `nextsql` and `nextsqld` binaries, initialize a data directory, and start serving NSQL. Storage, WAL, MVCC, SQL, the optimizer, JSON, full-text, vectors, hybrid plans, security, backup/PITR/export, and Raft HA are in the engine.

Hard limits and unimplemented SQL are listed under [Limits](/docs/limits).

## What you can do

A single table can hold structured columns, JSON, a vector, full-text, and a point. A single query can filter, search, and rank nearest neighbors under one physical plan:

```sql
SELECT id, name, price
FROM products
WHERE metadata.category = 'headphones'
  AND price <= 15000
SEARCH description FOR 'wireless noise cancelling'
NEAREST embedding TO $query
LIMIT 20;
```

The write path for that row is the same WAL, MVCC, encryption, and crash recovery as a `DECIMAL` update.

## Binaries

| Binary | Role |
|---|---|
| `nextsql` | CLI: init, exec, migrate, backup, restore, verify, export, import, diagnose, status, cluster |
| `nextsqld` | Server. Speaks NSQL v1 on `--listen` (default `127.0.0.1:7210`) |
| `nextsql-bench` | Official measurements. Encryption, WAL, and fsync stay on |

## Non-negotiable rules

- Keys and passwords never go in a connection URL. Drivers reject `://`, `key=`, and `password=` in the address.
- TLS 1.3 is required for any non-loopback listen address or remote client.
- `--insecure` / `insecureNoTLS` is loopback-only.
- A backup or export is not valid until `verify` (including a restore/import test) succeeds.
- Statements without `BEGIN` auto-commit. One SQL statement per `exec` / `-c`.
- Every table needs a `PRIMARY KEY`. That key is the clustered B+Tree key.
- Unquoted identifiers fold to lowercase.

## Priority order

Correctness, durability, security, integrity, availability, predictable latency, throughput, efficiency, developer experience, then extra features. Official benches keep encryption, WAL, fsync, checksums, MVCC, and authentication on.

## How to read these docs

1. [Install](/docs/install) and the [quick start](/docs/quick-start) get a local instance running.
2. The SQL chapters cover the dialect, each data model, and transactions.
3. Operate covers users, CLI, migrations, TLS, backup, export, HA, and benches.
4. Drivers speak NSQL v1 from Go, Node, Bun, Deno, and PHP.
5. Internals document architecture, the wire protocol, and current limits.
