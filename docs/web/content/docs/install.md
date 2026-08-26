# Install

Install the NextSQL **binaries**, then initialize a data directory.

Published versioned builds, checksums, and a feature comparison are on [Downloads](/download).

## Packages

Build Linux (`.deb`, `.tar.gz`, `.run`) and Windows (`.zip`, `setup.exe`) installers from a checkout:

```bash
./scripts/build-installers.sh
```

| Platform | Artifact | Install |
|---|---|---|
| Linux | `dist/nextsql_*_amd64.deb` | `sudo dpkg -i dist/nextsql_*_amd64.deb` |
| Linux | `dist/nextsql-*-linux-amd64.run` | `sudo ./dist/nextsql-*-linux-amd64.run` |
| Windows | `dist/nextsql-*-windows-amd64-setup.exe` | Run as Administrator (`/S` for silent) |

The packages copy `nextsql`, `nextsqld`, and `nextsql-bench` plus a default config. They do **not** create a data directory, write a root unlock key, or start the server. After install:

```bash
printf 'secret\n' > /tmp/nextsql.pw && chmod 600 /tmp/nextsql.pw
nextsql init --data-dir /var/lib/nextsql --key-file /etc/nextsql/root.key \
  --user app --password-file /tmp/nextsql.pw
sudo systemctl enable --now nextsql
```

Windows defaults: binaries in `%ProgramFiles%\NextSQL`, data in `%ProgramData%\NextSQL\data`, key in `%ProgramData%\NextSQL\keys\root.key`. Start with `Start-Service NextSQL` after `nextsql init`.

Keep the root unlock key **off** the data volume in production. Details: [`packaging/README.md`](https://github.com/bzync/nextsql/blob/main/packaging/README.md).

## Install with Go

Requires **Go 1.22+** so `go install` can fetch and compile the engine onto your `PATH`.

```bash
go install github.com/bzync/nextsql/cmd/nextsql@latest
go install github.com/bzync/nextsql/cmd/nextsqld@latest
go install github.com/bzync/nextsql/cmd/nextsql-bench@latest
```

Confirm:

```bash
nextsql version
# nextsql 0.1.0-dev (phase 15)
```

`nextsql` is the CLI. `nextsqld` is the server. `nextsql-bench` is optional (official measurements with encryption, WAL, and fsync on).

Go puts binaries in `$(go env GOPATH)/bin` (often `~/go/bin`). Put that directory on your `PATH`.

## What you will create next

A data directory is **not** a single file. After `nextsql init` and the first server start you typically have:

```text
DATA-DIR/
  nextsql.db            encrypted pages (16 KiB logical)
  nextsql.db.keys       wrapped DEKs only — never the root unlock key
  nextsql.db.wal/       encrypted WAL control + segments
  nextsql.db.undo/      encrypted UNDO log
  nextsql.users         password hashes (PBKDF2-HMAC-SHA256)
  nextsql.acl           roles and grants
  nextsql.audit         JSON-lines audit log (mode 0600)
  raft/                 present only when Raft HA is enabled
```

The **root unlock key** is a separate `--key-file` (`NSKY`, mode `0600`). Keep it **off** the data volume. Stolen disks, snapshots, WAL, backups, vector trees, and full-text trees stay ciphertext without that file.

## Drivers

Official drivers ship with the engine. They are not a separate download.

| Runtime | Import |
|---|---|
| Go | `github.com/bzync/nextsql/drivers/go` |
| Node.js 18+ | `drivers/node` in the NextSQL tree |
| Bun | `drivers/bun` |
| Deno | `drivers/deno` |
| PHP 8.1+ | `drivers/php` |

See [Drivers](/docs/drivers).

## Build from source (optional)

If you are changing the engine itself:

```bash
git clone https://github.com/bzync/nextsql.git
cd nextsql
go build -o nextsql       ./cmd/nextsql
go build -o nextsqld      ./cmd/nextsqld
go build -o nextsql-bench ./cmd/nextsql-bench
```

```bash
go test ./...
go test -race ./...          # needs a C compiler
go test ./tests/integration ./tests/crash ./tests/ha
```

## Next

[Initialize a data directory and run SQL →](/docs/quick-start)
