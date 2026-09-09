# Command line

```bash
nextsql init     --data-dir DIR --key-file FILE [--instance-key-file FILE]
                 [--database NAME]
                 [--user NAME --password-file FILE] [--buffer-pages N]
                 [--env-file PATH | --no-env]
nextsql setup    --data-dir DIR --key-file FILE
                 [--profile developer|production]
                 [--preset conservative|balanced|high-performance|custom]
                 [--user NAME --password-file FILE] [--json] [--dry-run]
                 [--recovery-key-out FILE [--instance-recovery-key-out FILE]]
nextsql lifecycle detect|preflight|backup-config|upgrade|repair|uninstall
nextsql registry adopt --data-dir DIR --key-file FILE [--instance-key-file FILE]
                 [--database NAME] --confirm
                 [--env-file PATH | --no-env]
nextsql registry migrate-tenant --source-data-dir DIR --source-key-file FILE
                 --tenant VALUE --data-dir DIR --key-file FILE
                 [--instance-key-file FILE] [--database NAME]
                 [--batch-rows N] [--buffer-pages N] --confirm
nextsql registry show --data-dir DIR [--instance-key-file FILE]
nextsql key status|add-recovery|verify-recovery|remove-recovery|recover
nextsql login    --idp NAME [--addr HOST:PORT] [--idp-config FILE]
                 [--database NAME] [--no-browser]
                 [--client-credentials [--client-secret-file FILE]]
nextsql logout   (--idp NAME --addr HOST:PORT | --all)
nextsql whoami   --idp NAME [--addr HOST:PORT] [--idp-config FILE] [--json]
nextsql exec     [--addr HOST:PORT] [--user NAME] [--password-file FILE | --idp NAME]
                 [--database NAME] [--tls-ca FILE | --insecure]
                 [--env-file PATH | --no-env] [--json]
                 [-c SQL | SQL]
nextsql migrate  status|pending|version|validate|create|up|down|force|repair
                 [--dir DIR] [--addr HOST:PORT] [--user NAME]
                 [--password-file FILE] [--tls-ca FILE | --insecure]
                 [--env-file PATH | --no-env]
nextsql backup   --data-dir DIR --key-file FILE --out DIR
nextsql backup list  --base-dir DIR
nextsql backup prune --base-dir DIR (--keep-count N | --keep-days N) [--confirm]
nextsql restore  --from DIR --data-dir DIR --key-file FILE
                 [--wal-archive DIR] [--until-lsn N | --until RFC3339]
nextsql verify   --from DIR --key-file FILE
nextsql export   --data-dir DIR --key-file FILE --out DIR
nextsql import   --from DIR --data-dir DIR --key-file FILE
nextsql diagnose --data-dir DIR
nextsql status   [--addr HOST:PORT] [--user NAME] [--password-file FILE | --idp NAME]
                 [--database NAME] [--tls-ca FILE | --insecure]
                 [--env-file PATH | --no-env]
nextsql status --local [--data-dir DIR] [--key-file FILE]
nextsql cluster status --data-dir DIR [--json]
nextsql cluster transfer-leader|drain|maintenance|reconcile
nextsql token    keygen|rotate|retire|list-keys|export-public|mint|revoke|verify
nextsql audit    keygen|rotate|retire|list-keys|export-public|verify
nextsql version
nextsql help
```

`nextsql token` manages signed short-lived credentials (see [TLS](/docs/tls) and
[security](/docs/security)): `keygen` / `rotate` / `retire` / `export-public`
manage the Ed25519 signing keyset, `mint` issues a credential
(`--principal`, `--ttl`, optional `--audience` / `--database` / `--role`), `revoke` edits the revocation file (`--token-id` or `--principal`
`--before`), and `verify` inspects a credential. Servers enable verification
with `token_verify_keyset` / `token_revocations` / `token_audience`.

Interactive external identity uses a named profile in
`~/.config/nextsql/config.toml`:

```toml
[idp.corp]
issuer = "https://corp.okta.com/oauth2/abc"
client_id = "0oa..."
client_secret_file = "/run/secrets/nextsql-oidc-client" # confidential workloads only
broker_url = "https://auth.db.internal"
scopes = ["openid", "profile", "email", "groups"]
```

```bash
nextsql login --idp corp --addr db.internal:7210 --database production
nextsql whoami --idp corp --addr db.internal:7210
nextsql exec --idp corp --addr db.internal:7210 --database production \
  --tls-ca /etc/nextsql/ca.pem -c 'SELECT 1'
nextsql logout --idp corp --addr db.internal:7210
```

Login uses Authorization Code + PKCE S256 and a transient loopback callback;
`--no-browser` prints the URL. The broker-minted credential and optional refresh
token are atomically stored in a mode-`0600` file under a mode-`0700` user
credentials directory. Expired credentials refresh silently when possible.
Redirect replay, oversized HTTP/file responses, symlink credential paths, and
group/other-readable credential files fail closed. `logout` removes only the
local secret; use `nextsql token revoke` for server-side revocation. The file
backend does not protect against a process already running as the same OS
account; OS-keychain integration remains a follow-on.

Confidential workloads whose IdP issues JWT access tokens can use
`nextsql login --client-credentials --client-secret-file FILE`. The secret file
must be regular, bounded, and mode `0600`; the secret is sent only to the
discovered HTTPS IdP token endpoint and is never stored with the broker-minted
credential. The broker profile must configure `access_token_audience`, and the
JWT must carry that resource audience plus an exact `client_id` or `azp`
binding. Expired workload credentials renew non-interactively from the same
secret file. Opaque-token introspection is not implemented.

`--out` for backup and export must not already exist. The tool writes a temporary directory, verifies, then publishes atomically.

`nextsql setup` is the non-interactive first-run path: it sizes a buffer pool
from a resource preset, writes a validated `nextsql.conf`, initializes the
store the same way `nextsql init` does, and verifies the result. `--profile
production` (Setup-mode GUI default) fail-closes on a key in the data
directory and on skip-init / missing administrator; the CLI default is
`developer`. See [Install](/docs/install) and [Admin](/docs/admin).

`nextsql lifecycle` covers detect / preflight / backup-config / upgrade /
repair / uninstall for packaged installs. `upgrade` refuses to run while a
server holds the data-directory lock; on a Raft member pass `--cluster-node`
so the rolling procedure (transfer leadership → drain → stop → upgrade) is
enforced.

`nextsql init` creates an encrypted/versioned deployment registry and a
separate external registry root. `--instance-key-file` defaults to
`KEY-FILE.instance`; keep both roots off the data volume. `--database` names
the deployment's one database and defaults to `default`. Each deployment
serves exactly one database.

`nextsql setup --recovery-key-out FILE` creates and verifies recovery exports
for both keystores during a first install; the registry export defaults to
`FILE.instance`. `nextsql key` reports, adds, verifies, removes, and uses
recovery keys. See [Security](/docs/security) for the recovery procedure and
offline-storage requirements.

`nextsql audit` manages the tamper-evident `NSAC` hash chain and optional
`NSAK` Ed25519 signatures on `nextsql.audit`. `verify` detects a tampered,
reordered, or deleted line.

`nextsql cluster transfer-leader`, `drain`, `maintenance enable|disable`, and
`reconcile confirm` are the operational cluster verbs; `--json` is accepted.

`nextsql registry adopt` is the explicit offline path for an existing
single-database `DATA-DIR/nextsql.db`. Stop `nextsqld` first. The command holds
the deployment lock, validates and recovery-opens the existing database,
preserves its storage identity and files, then publishes the default registry
entry through `PROVISIONING` to `ACTIVE`. Exact reruns resume safely. It never
discovers or adopts sibling files.

`nextsql registry migrate-tenant` copies one historical tenant out of a legacy
`tenant_id` / `PARTITION BY TENANT` database into a freshly provisioned isolated
deployment. Stop `nextsqld` for the source first; both deployments are
exclusively locked for the whole run. The source, destination database, and
destination registry roots must be three independent key files. The destination
stays `PROVISIONING` while every legacy-tenant table and its matching rows are
copied in bounded transactions (`--batch-rows`, 1–4096, default 256) and each
row is point-verified against the source; only a fully verified destination is
published `ACTIVE`. An exact rerun resumes safely — committed batches replay
through `UPSERT`, and an already-`ACTIVE` destination is re-verified without
touching data. The legacy tenant column is renamed to `legacy_tenant_id` and
becomes ordinary data in the isolated database. Physical TENANT partitioning,
foreign keys to unmigrated tables, and a pre-existing `legacy_tenant_id` column
each fail closed. A durable encrypted `nextsql.tenant-migration` intent binds
the destination to one source identity and tenant so a changed source, tenant,
or destination is rejected.

The deployment's data-file growth cap is `storage_cap_bytes` in
`nextsql.conf`. Once it is full, growth (`INSERT`, row-splitting `UPDATE`,
index growth) fails with `storage cap exceeded` while `DELETE` / `ROLLBACK` /
in-place `UPDATE` still work. `nextsql registry show` prints the registry,
including a cap recorded by an earlier release.

## Initialization configuration (`init`, `hosting adopt`, and `nextsqld`)

Hosting commands use the same dotenv discovery and priority as client
commands. For `nextsqld`, field priority is explicit flags > non-empty process
environment > `.env.local` > `.env` > `--config` > built-in defaults.

```dotenv
NEXTSQL_DATA_DIR=/var/lib/nextsql
NEXTSQL_KEY_FILE=/etc/nextsql/database.key
NEXTSQL_INSTANCE_KEY_FILE=/etc/nextsql/instance.key
NEXTSQL_DATABASE=production
NEXTSQL_BUFFER_PAGES=1024
NEXTSQL_SERVER_USER=admin
NEXTSQL_SERVER_PASSWORD_FILE=/run/secrets/nextsql-admin
```

`NEXTSQL_DATABASE` is the logical database created by `nextsql init` and
adopted by `nextsql registry adopt`. A client Hello may name it, but it selects
nothing: a deployment has one database, and a Hello naming any other is
rejected. `NEXTSQL_REGISTRY_CONFIRM=true` may supply the explicit adoption
confirmation for non-interactive provisioning.
`NEXTSQL_ADDR` supplies `nextsqld`'s listen address as well as the client address.

Server/bootstrap credentials are deliberately distinct:
`NEXTSQL_SERVER_USER` plus either `NEXTSQL_SERVER_PASSWORD_FILE` (recommended)
or `NEXTSQL_SERVER_PASS`. They are never a fallback for `nextsql exec`.

These variables contain paths and names, never raw encryption key bytes.
Protect a host provisioning env file (mode `0600`), keep it out of source
control, and do not give an application/CI env access to database or instance
root paths unless that process is authorized to operate the deployment.

Address is `host:port` only. Values containing `://`, `key=`, or `password=` are rejected.

`nextsql exec` talks to a running `nextsqld`. Mixing `--data-dir` / `--key-file` onto `exec` is an error. `NEXTSQL_KEY_FILE` in the environment or `.env` is ignored (the root key is not an exec input).

Every server-mode connect must set TLS (`--tls-ca` / `NEXTSQL_TLS_CA`) or `--insecure` / `NEXTSQL_INSECURE=true`, including `127.0.0.1`. `--insecure` is rejected unless the address is loopback.

## Client configuration (`exec` / `migrate` / server-mode `status` / OIDC)

Priority, highest wins: explicit flags (including empty strings) > non-empty process environment > `.env.local` (cwd only) > `.env` (walk from the working directory toward `/`, at most 16 levels) > defaults.

`--no-env` skips dotenv files. `--env-file PATH` loads only that file (missing path is an error). Empty environment variables do not override a file value.

| Variable | Meaning | Default |
|---|---|---|
| `NEXTSQL_ADDR` | `host:port` | `127.0.0.1:7210` |
| `NEXTSQL_DATABASE_USER` | Database/client auth user | none (required) |
| `NEXTSQL_DATABASE_PASSWORD_FILE` | Database/client password file (newline stripped) | none |
| `NEXTSQL_DATABASE_PASS` | Inline database/client password (CI convenience) | none |
| `NEXTSQL_IDP` | Named `[idp.NAME]` profile for a stored broker credential | none |
| `NEXTSQL_IDP_CONFIG` | OIDC client profile file | user config dir `nextsql/config.toml` |
| `NEXTSQL_DATABASE` | Hello database; validated against the registered default when present | empty (select default) |
| `NEXTSQL_TLS_CA` | PEM CA / server cert | none |
| `NEXTSQL_TLS_SERVER_NAME` | TLS certificate/SNI server name | host from `NEXTSQL_ADDR` |
| `NEXTSQL_TLS_CLIENT_CERT` | mTLS client certificate path | none |
| `NEXTSQL_TLS_CLIENT_KEY` | mTLS client private-key path | none |
| `NEXTSQL_INSECURE` | `true` / `1` / `yes` → plaintext, loopback only | false |
| `NEXTSQL_MIGRATION_DIR` | Migration file directory | `./migrations` |

If both a password file and an inline password are set, the file wins. Server
credentials never become a client-login fallback. Using an inline password
prints a one-line stderr warning. Do not put passwords in a committed file. Do
not put the root unlock key in the application `.env`.

The ambiguous legacy names `NEXTSQL_USER`, `NEXTSQL_PASSWORD_FILE`, and
`NEXTSQL_PASSWORD` are intentionally not accepted. Use the
`NEXTSQL_DATABASE_*` namespace for clients and `NEXTSQL_SERVER_*` for server
bootstrap.

`.env.local` is the recommended gitignored overlay. A parent directory’s `.env.local` is not loaded.

## Exit codes

| Code | When |
|---|---|
| 0 | Success |
| 1 | Usage, unknown command, invalid flags |
| 2 | Connection, authentication, or TLS |
| 3 | Dirty migration history |
| 4 | Migration checksum mismatch |
| 5 | SQL execution error |
| 6 | Migration validation error |
| 7 | Local-mode missing `--data-dir` / `--key-file` |

See [server configuration](/docs/config) for `nextsqld` and [migrations](/docs/migrate) for `nextsql migrate`.
