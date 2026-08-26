# Schema migrations

Keep schema in Git. `nextsql migrate` applies timestamped SQL files to a running `nextsqld` over NSQL. It is always **server mode**: it never opens `--data-dir` and never reads the root unlock key. A laptop `nextsqld` and a remote VPS are the same session. Only TLS and latency differ.

Prefer a password file. Never put the root unlock key in the application `.env`. Default directory is `./migrations` (`--dir` / `NEXTSQL_MIGRATIONS_DIR`). Connection flags and dotenv match [exec](/docs/cli).

## Commands

```text
nextsql migrate validate
nextsql migrate create add_orders
nextsql migrate status
nextsql migrate pending
nextsql migrate version
nextsql migrate up   [--count N] [--to VERSION] [--dry-run]
nextsql migrate down [--count N] [--to VERSION] [--dry-run]
nextsql migrate force VERSION --confirm
nextsql migrate repair --confirm
```

| Command | Connects? | Notes |
|---|---|---|
| `validate` | no | filenames, pairing, parse; no server |
| `create NAME` | no | writes empty `.up.sql` / `.down.sql` |
| `status` | yes | version, dirty, applied/pending, checksum mismatches |
| `pending` | yes | unapplied versions |
| `version` | yes | one line: version or `none` |
| `up` | yes | apply pending in order |
| `down` | yes | newest-first; legal compensating SQL only |
| `force VERSION --confirm` | yes | rewrite history; does **not** run SQL |
| `repair --confirm` | yes | refresh stored checksums of already-applied files |

`status` / `up` / `down` / `force` / `repair` create `nsql_schema_migrations` if it is missing. The CLI never sends `GRANT` SQL: creating that table grants `SELECT`/`INSERT`/`UPDATE`/`DELETE` on it to the handshake user.

Each up file is one transaction: `BEGIN`, dirty history insert, each statement, finalize (`dirty=0`), `COMMIT`. On error the file is rolled back. Files must not contain `BEGIN`/`COMMIT`/`ROLLBACK`, `SET TENANT`, or `GRANT`/`REVOKE`/`CREATE`/`DROP` `USER`/`ROLE`. Pass `--tenant` on the CLI instead.

`--dry-run` connects, lists the files that would run, checksums them, and parses every statement. It does not `BEGIN` and does not execute user SQL.

## Local development (`.env`)

`.env` is safe to commit if it contains no secrets. `.env.local` is gitignored and is the place for the password-file path.

```bash
# .env  — safe to commit if it contains no secrets
NEXTSQL_ADDR=127.0.0.1:7210
NEXTSQL_USER=app
NEXTSQL_INSECURE=true
NEXTSQL_MIGRATIONS_DIR=./migrations
```

```bash
# .env.local  — gitignored
NEXTSQL_PASSWORD_FILE=/home/dev/secrets/nextsql.pw
```

`NEXTSQL_INSECURE=true` is loopback-only. A laptop that omits both `--insecure` and `--tls-ca` fails at resolve, including `127.0.0.1`.

## Remote VPS (`.env.production`)

Load this on the migrate runner, not on the database host. The VPS `nextsqld` already has the root key; the migrator must not.

```bash
# .env.production
NEXTSQL_ADDR=db.example.com:7210
NEXTSQL_USER=migrator
NEXTSQL_PASSWORD_FILE=/run/secrets/nextsql-migrator.pw
NEXTSQL_TLS_CA=/etc/nextsql/ca.pem
NEXTSQL_MIGRATIONS_DIR=./migrations
```

```bash
nextsql migrate up --env-file .env.production
```

On Raft, connect to the **leader**. History inserts and `CREATE TABLE` replicate as WAL records; followers do not re-run the migrator.

## File names

```text
migrations/
  20260818120000_create_customers.up.sql
  20260818120000_create_customers.down.sql
  20260818120100_create_orders.up.sql
  20260818120100_create_orders.down.sql
```

Pattern: `YYYYMMDDHHMMSS_slug.up.sql` (optional matching `.down.sql`). Version is a 14-digit UTC timestamp. Integer prefixes such as `0001_name.up.sql` are not accepted.

Preferred style: one statement per file. Multi-statement files are split on `;` (not inside strings or comments), up to 32 statements per file. Checksum: SHA-256 of the file after CR LF → LF and stripping a single UTF-8 BOM. Comment edits change the digest; `repair --confirm` updates stored checksums of already-applied files.

## Example

Recommended pattern: composite `PRIMARY KEY (tenant_id, id)` so an FK can include `tenant_id` on both sides. Sample files live in [`docs/examples/migrations/`](https://github.com/bzync/nextsql/tree/main/docs/examples/migrations).

```sql
CREATE TABLE customers (
    tenant_id  UUID NOT NULL,
    id         UUID NOT NULL DEFAULT UUID(),
    email      STRING NOT NULL,
    name       STRING NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (tenant_id, id)
);

CREATE UNIQUE INDEX ux_customers_tenant_email ON customers (tenant_id, email);
```

```sql
CREATE TABLE orders (
    tenant_id   UUID NOT NULL,
    id          UUID NOT NULL DEFAULT UUID(),
    customer_id UUID NOT NULL,
    total       DECIMAL(12,2) NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (tenant_id, id),
    CONSTRAINT fk_orders_customer
        FOREIGN KEY (tenant_id, customer_id)
        REFERENCES customers (tenant_id, id)
        ON DELETE RESTRICT
        ON UPDATE RESTRICT
);
```

## Apply

```bash
nextsql migrate validate
nextsql migrate up --dry-run
nextsql migrate up
nextsql migrate status
```

The recommended v1 workflow is **forward-only** (`up`) when you want expand/contract deploys. `DROP TABLE`, `ALTER TABLE`, and `DROP INDEX` are legal in migration files. `REBUILD INDEX ... ONLINE` remains unsupported; the shipped rebuild is blocking.

A dirty history row or a checksum mismatch stops `up` (exit 3 / 4). Run **one migrator per database**: the history primary key is the lock.

The migrate user needs `CONNECT` + `CREATE` on the database, table DML on `nsql_schema_migrations`, and whatever the files themselves require. Cluster `ADMIN` is sufficient. `--tenant` issues `SET TENANT` after connect. History has no `tenant_id`; it is cluster-global.
