# NextSQL Environment Variables

This document is the authoritative environment-variable contract for NextSQL.
It separates deployment settings, server/bootstrap credentials, and database
client credentials so that one scope cannot silently become another.

## Naming model

```text
NEXTSQL_SERVER_*
→ initializes or updates the server/bootstrap administrator

NEXTSQL_DATABASE_*
→ selects and authenticates a database client connection

NEXTSQL_KEY_FILE / NEXTSQL_INSTANCE_KEY_FILE
→ external encryption-root file paths, never login passwords or raw key bytes
```

The ambiguous legacy names below are intentionally not accepted:

```text
NEXTSQL_USER
NEXTSQL_PASSWORD
NEXTSQL_PASSWORD_FILE
NEXTSQL_ROOT_USER
NEXTSQL_ROOT_PASSWORD
NEXTSQL_REALM
NEXTSQL_TENANT
```

## Complete core variable reference

| Variable | Scope | Meaning | Default |
|---|---|---|---|
| `NEXTSQL_DATA_DIR` | init, adoption, `nextsqld` | Encrypted deployment data directory | none |
| `NEXTSQL_KEY_FILE` | init, adoption, `nextsqld` | External default-database root key **file path** | none |
| `NEXTSQL_INSTANCE_KEY_FILE` | init, adoption, `nextsqld` | External deployment-registry root key **file path** | `NEXTSQL_KEY_FILE.instance` |
| `NEXTSQL_REALM_NAME` | init, adoption, clients | Logical subscription/account realm name; client Hello realm selection | `default` for init/adoption; server default for clients |
| `NEXTSQL_DATABASE` | init, adoption, clients | Logical database name; client Hello selection | init/adoption: `default`; client: server default |
| `NEXTSQL_BUFFER_PAGES` | init, adoption, `nextsqld` | Positive database buffer-pool page count | `1024` |
| `NEXTSQL_HOSTING_CONFIRM` | adoption | `true`/`1`/`yes` confirms non-interactive offline adoption | `false` |
| `NEXTSQL_HOSTING_MANIFEST_FILE` | init, `nextsqld` | Declarative multi-realm/database bootstrap manifest | none |
| `NEXTSQL_SERVER_USER` | init, `nextsqld` | Server/bootstrap administrator name | none |
| `NEXTSQL_SERVER_PASSWORD_FILE` | init, `nextsqld` | Preferred server/bootstrap password-file path | none |
| `NEXTSQL_SERVER_PASS` | init, `nextsqld` | Inline server/bootstrap password; automation fallback | none |
| `NEXTSQL_ADDR` | clients, `nextsqld` | Client address; server listen address when used by `nextsqld` | `127.0.0.1:7210` |
| `NEXTSQL_DATABASE_USER` | clients | Database/client authentication user | none |
| `NEXTSQL_DATABASE_PASSWORD_FILE` | clients | Preferred database/client password-file path | none |
| `NEXTSQL_DATABASE_PASS` | clients | Inline database/client password; CI fallback | none |
| `NEXTSQL_TLS_CA` | clients | PEM CA or server-certificate path | none |
| `NEXTSQL_TLS_SERVER_NAME` | clients | TLS certificate/SNI server name | host from `NEXTSQL_ADDR` |
| `NEXTSQL_TLS_CLIENT_CERT` | clients | mTLS client-certificate path | none |
| `NEXTSQL_TLS_CLIENT_KEY` | clients | mTLS client private-key path | none |
| `NEXTSQL_INSECURE` | clients | `true`/`1`/`yes` permits plaintext on loopback only | `false` |
| `NEXTSQL_MIGRATION_DIR` | migration CLI | Migration directory | `./migrations` |

Password files win when both their file and inline variables are set. Inline
password use emits a warning. Prefer mode-`0600` password files or secret
mounts.

## Hosting example

Store host provisioning configuration outside the application repository, for
example `/run/nextsql/hosting.env`:

```dotenv
NEXTSQL_DATA_DIR=/var/lib/nextsql
NEXTSQL_KEY_FILE=/etc/nextsql/database.key
NEXTSQL_INSTANCE_KEY_FILE=/etc/nextsql/instance.key

NEXTSQL_REALM_NAME=customer-a
NEXTSQL_DATABASE=production

NEXTSQL_SERVER_USER=admin
NEXTSQL_SERVER_PASSWORD_FILE=/run/secrets/nextsql-admin.pw

NEXTSQL_BUFFER_PAGES=1024
```

Protect it and initialize the deployment:

```bash
chmod 600 /run/nextsql/hosting.env
nextsql init --env-file /run/nextsql/hosting.env
nextsqld --env-file /run/nextsql/hosting.env
```

`NEXTSQL_DATABASE=production` causes `nextsql init` to create and register the
logical `production` database automatically. `NEXTSQL_REALM_NAME=customer-a`
registers it under the `customer-a` realm.

## Multiple realms and databases

`NEXTSQL_REALM_NAME` and `NEXTSQL_DATABASE` select one default pair for
single-database initialization and one realm/database in a client Hello. A
running hosted `nextsqld` can route different connections to different active,
registered pairs through the bounded database manager. Do not invent numbered
variables such as `NEXTSQL_REALM_1` or encode lists in one value.

Batch bootstrap uses a declarative file selected by
`NEXTSQL_HOSTING_MANIFEST_FILE`; the file can provision multiple realms and
databases, including a fully managed deployment with no legacy default. See
[`docs/design-multidatabase-dbaas.md`](docs/design-multidatabase-dbaas.md).

For an existing pre-registry default database:

```dotenv
NEXTSQL_HOSTING_CONFIRM=true
```

```bash
nextsql hosting adopt --env-file /run/nextsql/hosting.env
```

Adoption is offline, preserves the existing database identity/files, and never
auto-discovers sibling database files.

## Database client example

Use a separate application environment that does not contain database root or
server-bootstrap settings:

```dotenv
NEXTSQL_ADDR=127.0.0.1:7210
NEXTSQL_DATABASE=production
NEXTSQL_DATABASE_USER=app
NEXTSQL_DATABASE_PASSWORD_FILE=/run/secrets/nextsql-app.pw
NEXTSQL_INSECURE=true
```

```bash
nextsql exec --env-file /run/nextsql/client.env -c "SELECT 1"
nextsql migrate up --env-file /run/nextsql/client.env
```

For non-loopback connections, configure `NEXTSQL_TLS_CA`; plaintext
`NEXTSQL_INSECURE=true` is rejected outside loopback.

## Precedence and dotenv discovery

For CLI commands, highest priority wins:

```text
explicit flags, including explicit empty values
→ non-empty process environment
→ .env.local in the current directory
→ nearest .env found while walking upward, at most 16 levels
→ built-in defaults
```

For common `nextsqld` hosting fields, dotenv/environment values override the
server `--config` file:

```text
explicit flags
→ process environment
→ .env.local
→ .env
→ --config key=value fields
→ built-in defaults
```

`--env-file PATH` loads only that dotenv file. `--no-env` disables dotenv-file
loading but does not erase explicitly supplied process environment variables.
Empty process/dotenv values do not override lower-priority non-empty values.

## Encryption-key boundaries

Environment variables contain key **paths**, not keys:

```text
NEXTSQL_KEY_FILE
→ root that unlocks the default database envelope

NEXTSQL_INSTANCE_KEY_FILE
→ separate root that unlocks the encrypted deployment registry
```

Keep both paths outside `NEXTSQL_DATA_DIR`. The data directory contains only
encrypted data and wrapped-key sidecars.

`NEXTSQL_INSTANCE_KEY_FILE` is not a realm key. A future
`NEXTSQL_REALM_KEY_FILE` name is reserved for the per-realm key hierarchy and
is not accepted by the current M1 runtime. A clearer future rename for the
current registry root is `NEXTSQL_DEPLOYMENT_KEY_FILE`; changing that public
name requires an explicit compatibility decision.

## Docker entrypoint variables

The checked-in Docker entrypoint additionally accepts these wrapper settings:

| Variable | Meaning |
|---|---|
| `NEXTSQL_LISTEN` | Server listen address; default `0.0.0.0:7210` |
| `NEXTSQL_AUTH_FILE` | Optional explicit authentication-store path |
| `NEXTSQL_TLS_CERT` | Server TLS certificate path |
| `NEXTSQL_TLS_KEY` | Server TLS private-key path |
| `NEXTSQL_TLS_CLIENT_CA` | Optional CA bundle that requires mTLS client certificates |
| `NEXTSQL_TLS_CLIENT_CRL` | Optional PEM CRL bundle for fail-closed mTLS revocation checks |

`NEXTSQL_TLS_CERT` and `NEXTSQL_TLS_KEY` must be set together.
`NEXTSQL_TLS_CLIENT_CA` requires both and enables service-certificate mTLS.
`NEXTSQL_TLS_CLIENT_CRL` requires `NEXTSQL_TLS_CLIENT_CA`. Replace mounted TLS
files atomically and send `SIGHUP` to reload them.
Docker bootstrap
uses `NEXTSQL_SERVER_USER` with `NEXTSQL_SERVER_PASSWORD_FILE` or
`NEXTSQL_SERVER_PASS`.

## Current hosting limitation

Selectable bounded multi-realm/multi-database routing (M2) is implemented.
`NEXTSQL_REALM_NAME`/`NEXTSQL_DATABASE` bind a client to a registered active
database; realm-scoped auth, idle eviction, open-failure quarantine, the shared
buffer budget, and centralized task workers/scheduling apply. Managed
secondaries remain single-node and process-level TLS/config/metrics/log/backup
sources are not yet attached to every database opened lazily by the manager.
Database-addressed backup/restore/PITR/import/export, independent key lifecycle,
registry DR/Raft replication, and hosted HA remain M3+ work.

## Security checklist

- Keep hosting and client env files separate.
- Set secret-bearing env/password files to mode `0600`.
- Prefer password files over inline `*_PASS` variables.
- Never put raw root key bytes, passwords, or tokens in connection URLs.
- Never commit host provisioning, root-key paths, or password files.
- Do not expose `NEXTSQL_KEY_FILE`, `NEXTSQL_INSTANCE_KEY_FILE`, or
  `NEXTSQL_SERVER_*` to application containers or migration CI.
- Use TLS for every non-loopback connection.
