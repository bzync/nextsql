---
name: using-nextsql
description: >-
  Connect to and query a NextSQL database — start the server, connect from the
  CLI or an official driver (Go, Node, Bun, PHP, Python, Ruby), and write NSQL
  SQL across relational, JSON, vector, full-text, and geospatial data in one
  hybrid query. Use when writing application code against NextSQL, running
  `nextsql` / `nextsqld`, debugging an NSQL query, or diagnosing a
  connection / TLS / auth error, or when the user mentions NextSQL, nextsqld,
  NSQL, or a `.nsql` file.
---

# Using NextSQL

NextSQL is an encrypted-by-default multimodel database. Relational SQL, JSON,
vector search, full-text search, and geospatial types share **one** engine, WAL,
MVCC model, optimizer, and wire protocol (**NSQL v1**).

It is **not** PostgreSQL, MySQL, MongoDB, Elasticsearch, or a vector-store
compatibility layer. Its own storage format, SQL dialect, wire protocol, and
drivers. Do not assume another engine's syntax or semantics — check the dialect.

## Non-negotiable rules

- **Keys and passwords never go in a connection URL.** Drivers reject `://`,
  `key=`, and `password=` in the address. The address is `host:port` only.
- TLS 1.3 is required for any non-loopback listen address or remote client.
  `--insecure` / `insecureNoTLS` is **loopback-only**.
- One SQL statement per `exec` / `-c` request. A trailing `;` is optional; extra
  tokens after the statement are a syntax error.
- Statements without `BEGIN` auto-commit as their own transaction.
- **Every table needs a `PRIMARY KEY`** — it is the clustered B+Tree key.
- Unquoted identifiers fold to lowercase; `"Ident"` is preserved.
- Bind parameters are `$1`, `$2`, … (1-based, max 256). The CLI `-c` flag does
  **not** bind parameters — use a driver for parameterized SQL.
- `NULL` is typed; compare with `IS NULL` / `IS NOT NULL`.
- Table names starting with `nsql_` are reserved.

## Run a local server

```bash
printf 'secret\n' > /tmp/nextsql.pw && chmod 600 /tmp/nextsql.pw

# 1. initialize a data dir (root key goes on a DIFFERENT volume in production)
nextsql init --data-dir /var/lib/nextsql --key-file /etc/nextsql/root.key \
  --user app --password-file /tmp/nextsql.pw

# 2. serve (loopback may skip TLS; any other bind needs --tls-cert/--tls-key)
nextsqld --data-dir /var/lib/nextsql --key-file /etc/nextsql/root.key \
  --listen 127.0.0.1:7210 --user app --password-file /tmp/nextsql.pw
```

At least one user must exist or `nextsqld` refuses to start. For a live install
use `nextsql setup --profile production` (fail-closed preflight + production
defaults). For a container, see `docs/docker.md`.

## Connect and run SQL from the CLI

`nextsql exec` is a one-shot client (one statement per call, no parameter binding):

```bash
nextsql exec --addr 127.0.0.1:7210 --user app --password-file /tmp/nextsql.pw \
  --insecure -c "SELECT 1"
```

Result columns are tab-separated; successful DML prints `affected N`.

## Connect from a driver

Official drivers speak NSQL v1. Same shape everywhere: an address (`host:port`),
`realm`, `database`, `user`, `password`, and either `insecureNoTLS` (loopback) or
a TLS config with a CA.

| Runtime | Install | Connect |
|---|---|---|
| Go | `go get github.com/bzync/nextsql/drivers/go` | `nextsql.Open(nextsql.Config{…})` |
| Node 18+ | `npm i @bzync/nextsql` | `connect({ address, user, password, tls })` |
| Bun | import from `drivers/bun/` in the repo tree | same shape as Node |
| PHP 8.1+ | `composer require bzync/nextsql` | `NextSQL\Client::connect([…])` |
| Python 3.10+ | `pip install bzync-nextsql` | `nextsql.connect(nextsql.Config(…))` |
| Ruby 3.0+ | `gem install bzync-nextsql` | `NextSQL.connect(NextSQL::Config.new(…))` |

Common API: `exec` (materialize rows), `query` (stream rows), `prepare` /
execute, `cancel`, `close`. A connection is **single-flight** — a second query
while rows are still open returns `conflict`.

```go
conn, err := nextsql.Open(nextsql.Config{
    Address: "127.0.0.1:7210", Realm: "default", Database: "default",
    User: "app", Password: os.Getenv("NEXTSQL_DATABASE_PASS"),
    InsecureNoTLS: true, // loopback only
})
defer conn.Close()
res, err := conn.Exec(ctx, `SELECT name FROM products WHERE price < $1`,
    types.DecimalValue(dec, types.Type{Kind: types.KindDecimal, Precision: 12, Scale: 2}))
```

```js
const { connect } = require("@bzync/nextsql");
const conn = await connect({
  address: "127.0.0.1:7210", realm: "default", database: "default",
  user: "app", password: process.env.NEXTSQL_DATABASE_PASS, insecureNoTLS: true,
});
const res = await conn.exec("SELECT name FROM products WHERE price < $1", [
  { kind: "decimal", value: "50.00" },
]);
```

Remote: pass `tls` with a CA and servername instead of `insecureNoTLS`. For a
server started with `--require-client-key`, supply the 32-byte root via the
driver's key provider — never a URL. Each driver also ships a cluster client
(`OpenCluster` / `connectCluster` / `connect_cluster` / `NextSQL\Cluster::connect`)
that routes eligible reads to a healthy follower.

See `docs/drivers-<lang>.md` for per-language type mapping and full examples.

## One table, many models

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
```

- **JSON** — insert a JSON **text literal** (`'{"category":"headphones"}'`),
  stored as binary NSJB. Read a path with dotted access: `metadata.category`.
- **Vector** — literal is parenthesized finite floats `(0.1, 0.2, …)`; the
  dimension must match the column exactly on every insert and every `NEAREST`.
- **Full-text** — needs a `CREATE FULLTEXT INDEX`; query with `SEARCH`.
- **Geo** — `POINT(lon, lat)` (longitude first), WGS84.

## Search: SEARCH and NEAREST

```sql
-- full text (BM25); orders by score then primary key unless ORDER BY is given
SELECT name FROM products
SEARCH description FOR 'wireless noise cancelling'
LIMIT 5;

-- nearest-neighbour (ANN); USING COSINE | L2 | INNER_PRODUCT | HAMMING
SELECT name FROM products
NEAREST embedding TO $query
USING COSINE
LIMIT 5;
```

`SEARCH` token modifiers: trailing `*` = prefix (`cat*`), trailing `~` = fuzzy
(`cat~`, optional `~1`/`~2`), unknown terms get typo tolerance. `HIGHLIGHT(col)` /
`SNIPPET(col)` are available in the SELECT list of a `SEARCH` query.

## Hybrid queries — one physical plan

Structured filters, JSON paths, BM25, and ANN are **one** planning problem. Run
`ANALYZE <table>` first so statistics exist; the optimizer picks filter-then-ANN
or ANN-then-filter from the cost model, then reciprocal-rank-fuses candidates.

```sql
SELECT id, name, price
FROM products
WHERE metadata.category = 'headphones' AND price <= 15000
SEARCH description FOR 'wireless noise cancelling'
NEAREST embedding TO $query
LIMIT 20;
```

`EXPLAIN` shows `Candidates` and `Rerank bm25+vector`. A second `NEAREST` fuses a
dense `VECTOR` with a `SPARSEVECTOR` column. `SEARCH` / `NEAREST` combine with
`INNER JOIN` only when the rank column is on the `FROM` table — outer join +
`SEARCH`/`NEAREST` is not supported.

## Indexes

```sql
CREATE INDEX ix_category ON products (metadata.category);         -- JSON path
CREATE UNIQUE INDEX uq_sku ON products (name);
CREATE INDEX ix_cover ON products (name) INCLUDE (price);         -- covering
CREATE INDEX ix_lower ON products (LOWER(name));                  -- expression
CREATE FULLTEXT INDEX ix_desc ON products (description)
    WITH (ANALYZER = 'english');
CREATE VECTOR INDEX ix_emb ON products (embedding) USING HNSW;    -- or IVF | IVFPQ | SPARSE
CREATE SPATIAL INDEX ix_loc ON products (location);
ANALYZE products;                                                 -- write optimizer stats
```

## Transactions

`nextsql exec` sends one statement per call, so a multi-statement transaction
needs a driver session on one connection.

```sql
BEGIN;                 -- default is SNAPSHOT
BEGIN READ COMMITTED;  -- snapshot refreshed per statement
BEGIN SNAPSHOT;        -- snapshot at BEGIN; first-committer-wins on write-write
BEGIN SERIALIZABLE;    -- snapshot + shared key/range locks (strict 2PL, not SSI)
COMMIT;
ROLLBACK;
```

Readers never see uncommitted writes. Commit is acknowledged only after
group-commit WAL + `fsync` (and, on a Raft cluster, a quorum). A deadlock aborts
the requester with error `deadlock` — that transaction must `ROLLBACK`.

## EXPLAIN

```sql
EXPLAIN SELECT name FROM products WHERE metadata.category = 'headphones';
EXPLAIN ANALYZE SELECT name FROM products SEARCH description FOR 'wireless' LIMIT 5;
```

`EXPLAIN` shows the access path (`covering` when the row is rebuilt from the
index). `EXPLAIN ANALYZE` actually runs the plan. Plain `ANALYZE <table>` only
writes statistics.

## Inspect a running instance

```sql
SELECT 1;                                          -- health check
SHOW DATABASES | TABLES | INDEXES | CONNECTIONS | QUERIES | TRANSACTIONS | LOCKS;
```

```bash
nextsql diagnose --data-dir /var/lib/nextsql          # plaintext headers, no key
nextsql status --local --data-dir … --key-file …      # opens the db, prints LSNs/counters
nextsql status --addr 127.0.0.1:7210 --user … --password-file …   # dials nextsqld
```

## When something fails

- `syntax error` after a valid-looking statement → a second statement or stray
  token in the same request, or a reserved word used as an identifier (quote it).
- Connection refused with a URL-shaped string → the driver rejected `://` /
  `key=` / `password=`; pass `host:port` plus separate fields.
- TLS handshake failure off loopback → the server needs `--tls-cert` /
  `--tls-key`; the client needs the CA. `--insecure` will not help off loopback.
- `NEAREST` dimension mismatch → the query vector length must equal the column's
  declared `<F32,N>`.
- Hybrid query with a bad plan → run `ANALYZE <table>` first.
- `foreign_key` / `deadlock` / `exhausted` / `conflict` are engine error codes —
  see `reference.md`.

## More detail

- `reference.md` (this skill) — full type table, statement list, error codes, limits.
- `docs/sql.md` — the dialect. `docs/relational.md`, `docs/json.md`,
  `docs/vectors.md`, `docs/fulltext.md`, `docs/geo.md`, `docs/hybrid.md` — per model.
- `docs/transactions.md`, `docs/drivers.md` + `docs/drivers-<lang>.md`,
  `docs/tls.md`, `docs/security.md`, `docs/ha.md`, `docs/limits.md`.
