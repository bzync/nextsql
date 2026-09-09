# Install

Install the NextSQL **binaries**, then initialize a data directory.

Published packages, checksums, and a feature comparison are on [Downloads](/download). Direct files: [GitHub release v0.0.1](https://github.com/bzync/nextsql/releases/tag/v0.0.1). For a container, see [Docker](/docs/docker). For the GUI, see [Admin](/docs/admin).

The packages copy `nextsql`, `nextsqld`, `nextsql-bench`, and `nextsql-admin` plus a default config. They do **not** create a data directory, write a root unlock key, or start the server.

## Linux x64

Verify the file against [SHA256SUMS](https://github.com/bzync/nextsql/releases/download/v0.0.1/SHA256SUMS) before you run it.

### Debian / Ubuntu (`.deb`)

```bash
curl -fsSL -O https://github.com/bzync/nextsql/releases/download/v0.0.1/nextsql_0.0.1_amd64.deb
curl -fsSL -O https://github.com/bzync/nextsql/releases/download/v0.0.1/SHA256SUMS
sha256sum -c --ignore-missing SHA256SUMS
sudo dpkg -i nextsql_0.0.1_amd64.deb
```

### Self-extracting installer (`.run`)

```bash
curl -fsSL -O https://github.com/bzync/nextsql/releases/download/v0.0.1/nextsql-0.0.1-linux-amd64.run
curl -fsSL -O https://github.com/bzync/nextsql/releases/download/v0.0.1/SHA256SUMS
sha256sum -c --ignore-missing SHA256SUMS
chmod +x nextsql-0.0.1-linux-amd64.run
sudo ./nextsql-0.0.1-linux-amd64.run
```

### Portable tarball

```bash
curl -fsSL -O https://github.com/bzync/nextsql/releases/download/v0.0.1/nextsql-0.0.1-linux-amd64.tar.gz
tar -xzf nextsql-0.0.1-linux-amd64.tar.gz
sudo ./nextsql-0.0.1-linux-amd64/install.sh
```

`install.sh` defaults to system-wide (`/usr/local`) as root, or `--user` (`~/.local`) otherwise. The systemd unit is installed but **not** enabled.

There is no Linux ARM64 package in this snapshot. Use [Docker](/docs/docker) (`linux/amd64` and `linux/arm64`) or [build from source](#build-from-source-optional).

## Windows x64

Download from [Downloads](/download) or the [GitHub release](https://github.com/bzync/nextsql/releases/tag/v0.0.1):

| Artifact | What |
|---|---|
| `nextsql-0.0.1-windows-amd64-setup.exe` | GUI installer (UAC). Silent: `setup.exe /S` |
| `nextsql-0.0.1-windows-amd64.zip` | Binaries + `install.ps1` / `uninstall.ps1` |

Default install: `%ProgramFiles%\NextSQL`. Data: `%ProgramData%\NextSQL\data`. Key: `%ProgramData%\NextSQL\keys\root.key`. The `NextSQL` service is demand-start and is not started by the installer.

Windows `setup.exe` is produced by the installer scripts; execution is **unverified** (no Windows host in this project). macOS packages are not a supported path.

## After install

```bash
printf 'secret\n' > /tmp/nextsql.pw && chmod 600 /tmp/nextsql.pw
nextsql setup --data-dir /var/lib/nextsql --key-file /etc/nextsql/root.key \
  --profile production --preset balanced --user app --password-file /tmp/nextsql.pw
sudo systemctl enable --now nextsql
```

`nextsql setup` writes a validated `nextsql.conf`, initializes the encrypted store, and verifies the result. `nextsql init` remains the lower-level equivalent if you want to write the config yourself.

`--profile` is independent of `--preset` (buffer-pool sizing):

| Profile | Default | Use |
|---|---|---|
| `developer` | CLI | local / loopback. `--skip-init` and a missing administrator are allowed. |
| `production` | Setup-mode GUI | a live deployment. Fail-closed preflight plus operational defaults. |

`production` writes `deployment_profile=production` and fills zero-valued disk-watermark, replica-lag, drain, statement/idle/lock timeout, and connection-limit fields. `nextsqld --production` forces the same profile at start even if the file still says `developer`. The preflight refuses an unlock key on the data volume, `--skip-init`, a mutating install without an administrator, and missing watermark / drain / statement / idle timeouts. tmpfs/ramfs is a warning (CI and some containers report it for `/tmp`).

Linux `.tar.gz` / `.run` / `.deb` and silent/offline/upgrade/repair paths are live-verified. `nextsql setup --recovery-key-out FILE` exports verified recovery keys for both the database and deployment-registry keystores (`FILE.instance` by default); it is rejected with `--skip-init`. Store both exports offline and separately from the root keys. Setup mode offers the same export by default and requires confirmation that both files were copied offline before completion. Windows/macOS packaged execution remains unverified.

Keep the root unlock key **off** the data volume in production. A production install with `--key-file` inside `--data-dir` fails closed. Details: [`packaging/README.md`](https://github.com/bzync/nextsql/blob/main/packaging/README.md).

## Docker

Multi-arch images (`linux/amd64`, `linux/arm64`) are on Docker Hub, published
only from a tagged release. Every tag is immutable — pin an explicit version,
there is no `latest`, `edge`, or per-commit tag:

```bash
docker pull bzynchub/nextsql:0.0.1
```

See [Docker](/docs/docker).

## Install with Go

Requires **Go 1.22+** so `go install` can fetch and compile the engine onto your `PATH`.

```bash
go install github.com/bzync/nextsql/cmd/nextsql@latest
go install github.com/bzync/nextsql/cmd/nextsqld@latest
go install github.com/bzync/nextsql/cmd/nextsql-bench@latest
go install github.com/bzync/nextsql/cmd/nextsql-auth-broker@latest
go install github.com/bzync/nextsql/cmd/nextsql-admin@latest
```

Confirm:

```bash
nextsql version
# nextsql 0.0.1
```

`nextsql` is the CLI. `nextsqld` is the server. `nextsql-bench` is optional (official measurements with encryption, WAL, and fsync on). `nextsql-auth-broker` is the optional OIDC broker. `nextsql-admin` is the loopback Admin UI.

Go puts binaries in `$(go env GOPATH)/bin` (often `~/go/bin`). Put that directory on your `PATH`.

## What you will create next

A data directory is **not** a single file. After `nextsql init` / `nextsql setup` and the first server start you typically have:

```text
DATA-DIR/
  nextsql.lock          advisory deployment/offline-migration lock
  nextsql.conf          generated by nextsql setup (optional)
  nextsql.instance      encrypted deployment registry
  nextsql.instance.keys wrapped registry keys — never the registry root
  nextsql.db            encrypted pages (16 KiB logical)
  nextsql.db.keys       wrapped DEKs only — never the root unlock key
  nextsql.db.wal/       encrypted WAL control + segments
  nextsql.db.undo/      encrypted UNDO log
  nextsql.users         versioned password hashes (Argon2id; legacy PBKDF2 readable)
  nextsql.acl           roles and grants
  nextsql.audit         JSON-lines audit log with an NSAC hash chain (mode 0600)
  raft/                 present only when Raft HA is enabled
```

The database **root unlock key** is a separate `--key-file` (`NSKY`, mode
`0600`). Initialization also creates a deployment registry root at
`--instance-key-file` (default `KEY-FILE.instance`). Keep both **off** the data
volume. A deployment serves exactly one database.

Automated installs may place the corresponding file paths and logical names in
a protected mode-`0600` host env file and run `nextsql init --env-file PATH` or
`nextsql setup`:
`NEXTSQL_DATA_DIR`, `NEXTSQL_KEY_FILE`, `NEXTSQL_INSTANCE_KEY_FILE`,
`NEXTSQL_DATABASE`, `NEXTSQL_SERVER_USER`, and
`NEXTSQL_SERVER_PASSWORD_FILE` (preferred) or `NEXTSQL_SERVER_PASS`. These are
server/bootstrap settings, not client login fallback. Key values are paths,
not raw key bytes. Keep this host provisioning file out of application
containers and source control.

## Drivers

Official drivers are also in the repo tree under `drivers/` and are versioned
independently of the engine.

| Runtime | Install |
|---|---|
| Go | `go get github.com/bzync/nextsql/drivers/go` |
| Node.js 18+ | `npm i @bzync/nextsql` |
| Bun | `drivers/bun` (repo tree) |
| PHP 8.1+ | `composer require bzync/nextsql` |
| Python 3.10+ | `pip install bzync-nextsql` |
| Ruby 3.0+ | `gem install bzync-nextsql` |

See [Drivers](/docs/drivers).

## Build from source (optional)

If you are changing the engine itself, or building packages on a host that has no published artifact:

```bash
git clone https://github.com/bzync/nextsql.git
cd nextsql
go build -o nextsql             ./cmd/nextsql
go build -o nextsqld            ./cmd/nextsqld
go build -o nextsql-bench       ./cmd/nextsql-bench
go build -o nextsql-auth-broker ./cmd/nextsql-auth-broker
go build -o nextsql-admin       ./cmd/nextsql-admin
```

OS packages (`.deb`, `.rpm`, `.tar.gz`, `.run`, Windows `.zip` / `setup.exe`):

```bash
./scripts/build-installers.sh
```

Artifacts land in `installers/`. See [`packaging/README.md`](https://github.com/bzync/nextsql/blob/main/packaging/README.md).

```bash
go test ./...
go test -race ./...          # needs a C compiler
go test ./tests/integration ./tests/crash ./tests/ha
```

## Next

[Initialize a data directory and run SQL →](/docs/quick-start)
