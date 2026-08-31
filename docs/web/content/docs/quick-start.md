# Quick start

A complete first session: install the engine, initialize, serve, create a multimodel table, write, query, index, and inspect.

Install `nextsql` and `nextsqld` first — see [Install](/docs/install). Use two terminals. Paths below are examples. In production put the key file on a different volume from `--data-dir`.

## Create a password file and initialize

```bash
printf 'secret\n' > /tmp/nextsql.pw
chmod 600 /tmp/nextsql.pw

nextsql init \
  --data-dir /var/lib/nextsql \
  --key-file /etc/nextsql/root.key \
  --user app --password-file /tmp/nextsql.pw
```

What that does:

1. Creates `/etc/nextsql/root.key` if it is missing (32-byte AES root, mode `0600`).
2. Creates a separate deployment registry root at `/etc/nextsql/root.key.instance` unless `--instance-key-file` is supplied.
3. Creates the encrypted `nextsql.instance` registry and `/var/lib/nextsql/nextsql.db` with their wrapped-key sidecars.
4. Bootstraps user `app` with `ADMIN` on `CLUSTER` and `CONNECT` on the default database.

Printed output includes the data-file path, database/file/deployment identity
UUIDs, normalized realm name/ID, and logical default database name.

`--user` requires `--password-file`. The password file may end with a newline; it is stripped.

All init values can come from a protected host dotenv file. In particular,
`NEXTSQL_DATABASE=production` automatically becomes the logical database name:

```dotenv
NEXTSQL_DATA_DIR=/var/lib/nextsql
NEXTSQL_KEY_FILE=/etc/nextsql/root.key
NEXTSQL_INSTANCE_KEY_FILE=/etc/nextsql/root.key.instance
NEXTSQL_REALM_NAME=customer-a
NEXTSQL_DATABASE=production
NEXTSQL_SERVER_USER=app
NEXTSQL_SERVER_PASSWORD_FILE=/tmp/nextsql.pw
```

Run `nextsql init --env-file /run/nextsql/hosting.env`. Explicit flags override
the file. Values are paths/names, never raw key bytes.

Existing pre-registry deployments must not be reinitialized. Stop `nextsqld`
and run `nextsql hosting adopt --data-dir /var/lib/nextsql --key-file
/etc/nextsql/root.key --confirm`. The offline command validates and
recovery-opens the existing default database, preserves its identity/files,
and does not discover sibling database files.

## Start the server (loopback)

```bash
nextsqld \
  --data-dir /var/lib/nextsql \
  --key-file /etc/nextsql/root.key \
  --listen 127.0.0.1:7210 \
  --user app --password-file /tmp/nextsql.pw
```

`--user` on `nextsqld` upserts that user if you want the server to create or refresh credentials at start. At least one user must exist or `nextsqld` refuses to start.

Loopback may run without TLS. Any bind that is not loopback requires `--tls-cert` and `--tls-key`. See [TLS and client keys](/docs/tls).

## Run SQL from the CLI

`nextsql exec` is a one-shot client. After resolve, `user`, a password, and SQL are required. SQL is `-c` or a single positional argument.

```bash
CLI=(nextsql exec --addr 127.0.0.1:7210 --user app --password-file /tmp/nextsql.pw --insecure)
```

Create the product table used throughout these docs:

```bash
"${CLI[@]}" -c "
CREATE TABLE products (
    id          UUID PRIMARY KEY DEFAULT UUID(),
    account_id   UUID NOT NULL,
    name        STRING NOT NULL,
    description TEXT,
    price       DECIMAL(12,2),
    metadata    JSON,
    embedding   VECTOR<F32,8>,
    location    POINT,
    created_at  TIMESTAMPTZ DEFAULT NOW()
)
"
```

`VECTOR<F32,1536>` is the production-shaped type. This walkthrough uses dimension **8** so you can type literals by hand. Dimension must match between the column, inserts, and `NEAREST`.

Insert two rows. JSON is a string literal that is parsed and stored as binary `NSJB`. Vectors are parenthesized floats. Points are `POINT(lon, lat)`.

```bash
"${CLI[@]}" -c "
INSERT INTO products (account_id, name, description, price, metadata, embedding, location)
VALUES
  ('11111111-1111-1111-1111-111111111111',
   'Aero 2',
   'wireless noise cancelling headphones',
   12900.00,
   '{\"category\":\"headphones\",\"color\":\"black\"}',
   (0.1, 0.2, 0.3, 0.4, 0.5, 0.6, 0.7, 0.8),
   POINT(-73.9857, 40.7484)),
  ('11111111-1111-1111-1111-111111111111',
   'Desk Lamp',
   'adjustable LED desk lamp',
   4500.00,
   '{\"category\":\"lighting\",\"color\":\"white\"}',
   (0.9, 0.1, 0.0, 0.0, 0.0, 0.0, 0.0, 0.0),
   POINT(-73.9780, 40.7580))
"
```

Successful DML prints `affected N`.

## Query

```bash
"${CLI[@]}" -c "SELECT name, price FROM products WHERE price BETWEEN 4000 AND 15000"
"${CLI[@]}" -c "SELECT name FROM products WHERE metadata.category = 'headphones'"
```

Result columns are tab-separated.

## Indexes

```bash
"${CLI[@]}" -c "CREATE INDEX ix_category ON products (metadata.category)"
"${CLI[@]}" -c "CREATE FULLTEXT INDEX ix_desc ON products (description)"
"${CLI[@]}" -c "CREATE VECTOR INDEX ix_emb ON products (embedding) USING HNSW"
"${CLI[@]}" -c "CREATE SPATIAL INDEX ix_loc ON products (location)"
"${CLI[@]}" -c "ANALYZE products"
```

`ANALYZE` writes statistics the optimizer uses. `EXPLAIN` shows the chosen access path:

```bash
"${CLI[@]}" -c "EXPLAIN SELECT name FROM products WHERE metadata.category = 'headphones'"
"${CLI[@]}" -c "EXPLAIN SELECT name FROM products SEARCH description FOR 'wireless noise cancelling' LIMIT 5"
"${CLI[@]}" -c "EXPLAIN SELECT name FROM products NEAREST embedding TO (0.1, 0.2, 0.3, 0.4, 0.5, 0.6, 0.7, 0.8) LIMIT 5"
```

## Full text, vectors, hybrid, geo

```bash
"${CLI[@]}" -c "
SELECT name FROM products
SEARCH description FOR 'wireless noise cancelling'
LIMIT 5
"

"${CLI[@]}" -c "
SELECT name FROM products
NEAREST embedding TO (0.1, 0.2, 0.3, 0.4, 0.5, 0.6, 0.7, 0.8)
USING COSINE
LIMIT 5
"

"${CLI[@]}" -c "
SELECT name, price FROM products
WHERE metadata.category = 'headphones' AND price <= 15000
SEARCH description FOR 'wireless noise cancelling'
NEAREST embedding TO (0.1, 0.2, 0.3, 0.4, 0.5, 0.6, 0.7, 0.8)
LIMIT 5
"

"${CLI[@]}" -c "
SELECT name FROM products
WHERE DWITHIN(location, POINT(-73.9857, 40.7484), 2000)
"
```

That hybrid statement is one physical plan: structured filters, BM25, and ANN share the same cost model and the same WAL / MVCC / encryption path.

## Multi-statement transaction

`nextsql exec` sends one statement per invocation, so a `BEGIN` … `COMMIT` session needs a [driver](/docs/drivers). From a Go session:

```go
conn.Exec(ctx, `BEGIN SNAPSHOT`)
conn.Exec(ctx, `UPDATE products SET price = price * 1.1 WHERE name = $1`, types.StringValue("Aero 2"))
conn.Exec(ctx, `COMMIT`)
```

Without `BEGIN`, each statement is its own committed transaction.

## Inspect the instance

```bash
nextsql diagnose --data-dir /var/lib/nextsql
nextsql status --local --data-dir /var/lib/nextsql --key-file /etc/nextsql/root.key
```

`diagnose` reads plaintext headers only (no key). `status --local` also opens the database and prints table count, LSNs, isolated pages, query counters, and admission stats. Default `status` (no `--local`) dials `nextsqld` and prints `mode server`.
