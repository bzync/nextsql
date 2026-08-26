# Command line

```text
nextsql init     --data-dir DIR --key-file FILE [--user NAME --password-file FILE]
                 [--buffer-pages N]
nextsql exec     [--addr HOST:PORT] [--user NAME] [--password-file FILE]
                 [--database NAME] [--tls-ca FILE | --insecure]
                 [--env-file PATH | --no-env] [--tenant VALUE]
                 [-c SQL | SQL]
nextsql migrate  status|pending|version|validate|create|up|down|force|repair
                 [--dir DIR] [--addr HOST:PORT] [--user NAME]
                 [--password-file FILE] [--tls-ca FILE | --insecure]
                 [--env-file PATH | --no-env] [--tenant VALUE]
nextsql backup   --data-dir DIR --key-file FILE --out DIR
nextsql restore  --from DIR --data-dir DIR --key-file FILE
                 [--wal-archive DIR] [--until-lsn N | --until RFC3339]
nextsql verify   --from DIR --key-file FILE
nextsql export   --data-dir DIR --key-file FILE --out DIR
nextsql import   --from DIR --data-dir DIR --key-file FILE
nextsql diagnose --data-dir DIR
nextsql status   [--addr HOST:PORT] [--user NAME] [--password-file FILE]
                 [--database NAME] [--tls-ca FILE | --insecure]
                 [--env-file PATH | --no-env]
nextsql status --local [--data-dir DIR] [--key-file FILE]
nextsql cluster status --data-dir DIR
nextsql version
nextsql help
```

`--out` for backup and export must not already exist. The tool writes a temporary directory, verifies, then publishes atomically.

Address is `host:port` only. Values containing `://`, `key=`, or `password=` are rejected.

`nextsql exec` talks to a running `nextsqld`. Mixing `--data-dir` / `--key-file` onto `exec` is an error. `NEXTSQL_KEY_FILE` in the environment or `.env` is ignored (the root key is not an exec input).

Every server-mode connect must set TLS (`--tls-ca` / `NEXTSQL_TLS_CA`) or `--insecure` / `NEXTSQL_INSECURE=true`, including `127.0.0.1`. `--insecure` is rejected unless the address is loopback.

## Client configuration (`exec` / `migrate` / server-mode `status`)

Priority, highest wins: explicit flags (including empty strings) > non-empty process environment > `.env.local` (cwd only) > `.env` (walk from the working directory toward `/`, at most 16 levels) > defaults.

`--no-env` skips dotenv files. `--env-file PATH` loads only that file (missing path is an error). Empty environment variables do not override a file value.

| Variable | Meaning | Default |
|---|---|---|
| `NEXTSQL_ADDR` | `host:port` | `127.0.0.1:7210` |
| `NEXTSQL_USER` | Auth user | none (required) |
| `NEXTSQL_PASSWORD_FILE` | Password file (newline stripped) | none |
| `NEXTSQL_PASSWORD` | Inline password (CI convenience) | none |
| `NEXTSQL_DATABASE` | Hello `database` field | empty (optional) |
| `NEXTSQL_TLS_CA` | PEM CA / server cert | none |
| `NEXTSQL_INSECURE` | `true` / `1` / `yes` → plaintext, loopback only | false |
| `NEXTSQL_TENANT` | Optional `SET TENANT` after connect | unset |
| `NEXTSQL_MIGRATIONS_DIR` | Migration file directory | `./migrations` |

If both a password file and `NEXTSQL_PASSWORD` are set, the file wins. Using the inline password prints a one-line stderr warning. Do not put `NEXTSQL_PASSWORD` in a committed file. Do not put the root unlock key in the application `.env`.

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
