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
NEXTSQL_REALM_NAME
NEXTSQL_TENANT
```

## Complete core variable reference

| Variable | Scope | Meaning | Default |
|---|---|---|---|
| `NEXTSQL_DATA_DIR` | init, adoption, `nextsqld` | Encrypted deployment data directory | none |
| `NEXTSQL_KEY_FILE` | init, adoption, `nextsqld` | External default-database root key **file path** | none |
| `NEXTSQL_INSTANCE_KEY_FILE` | init, adoption, `nextsqld` | External deployment-registry root key **file path** | `NEXTSQL_KEY_FILE.instance` |
| `NEXTSQL_DATABASE` | init, adoption, clients | Names the deployment's database — and, for init, creates it. Unset at init means no database is created | init/adoption: unset (no database); client: server default |
| `NEXTSQL_BUFFER_PAGES` | init, adoption, `nextsqld` | Positive database buffer-pool page count | `1024` |
| `NEXTSQL_REGISTRY_CONFIRM` | adoption | `true`/`1`/`yes` confirms non-interactive offline adoption | `false` |
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
deployment's database under that name. Leave it unset and `nextsql init`
provisions the deployment only — root key and administrator, no database —
which `nextsqld` refuses to serve until `nextsql init --database NAME`
completes it.

## More than one database

Run more than one deployment: multi-realm/multi-database hosting was removed,
so each database gets its own data directory, root key, registry and
`nextsqld` process — and, with them, full isolation. Do not invent numbered
variables such as `NEXTSQL_DATABASE_1` or encode lists in one value;
`NEXTSQL_HOSTING_MANIFEST_FILE` (the declarative multi-realm bootstrap) is
refused.

For an existing pre-registry default database:

```dotenv
NEXTSQL_REGISTRY_CONFIRM=true
```

```bash
nextsql registry adopt --env-file /run/nextsql/hosting.env
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

`NEXTSQL_INSTANCE_KEY_FILE` unlocks the deployment registry, nothing else. A
clearer future rename is `NEXTSQL_DEPLOYMENT_KEY_FILE`; changing that public
name requires an explicit compatibility decision.

## Docker entrypoint variables

The container PID-1 (`nextsql-entrypoint`) additionally accepts these wrapper settings:

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

## One deployment, one database

Multi-realm/multi-database hosting was removed. `NEXTSQL_DATABASE` names the
deployment's single database; there is no realm, no routing, and no
`CREATE DATABASE`. `nextsqld` refuses to start against a registry written by an
earlier release that holds more than one database, rather than leaving the
others silently unreachable — use 0.0.1 to export them into their own
deployments.

## Security checklist

- Keep hosting and client env files separate.
- Set secret-bearing env/password files to mode `0600`.
- Prefer password files over inline `*_PASS` variables.
- Never put raw root key bytes, passwords, or tokens in connection URLs.
- Never commit host provisioning, root-key paths, or password files.
- Do not expose `NEXTSQL_KEY_FILE`, `NEXTSQL_INSTANCE_KEY_FILE`, or
  `NEXTSQL_SERVER_*` to application containers or migration CI.
- Use TLS for every non-loopback connection.
