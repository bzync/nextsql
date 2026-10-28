# Docker Compose Guide

NextSQL provides first-class support for Docker Compose to orchestrate single-node instances, application microservice stacks, and multi-node Raft High Availability (HA) clusters.

This guide covers:
- Core container architecture and security requirements
- Single-node development and production setups
- Full-stack application integration (connecting your backend services)
- Health checks and service orchestration
- Three-node Raft HA clustering (`docker-compose.ha.yml`)
- Day-2 operations (CLI execution, migrations, backups, and restores)
- Complete environment variable reference
- Troubleshooting common pitfalls

---

## 1. Core Concepts & Security Architecture

Before running NextSQL with Docker Compose, understand these fundamental architectural principles:

### Minimal Scratch Container
The official image (`bzynchub/nextsql:0.0.7`) is built `FROM scratch`. It contains only:
- Static, unprivileged Go binaries (`nextsql`, `nextsqld`)
- A lightweight PID-1 entrypoint (`nextsql-entrypoint`)
- A system root CA bundle
- No shell (`sh`, `bash`), no package manager, and no libc userland.

Administrative commands must be executed using `/usr/local/bin/nextsql` directly via `docker compose exec` (e.g. `docker compose exec nextsql nextsql exec ...`).

### Separate Volumes for Data and Keys
NextSQL is **encrypted by default** using AES-256-GCM envelope encryption. The external root unlock key (`NSKY`) must **never** be stored on the database data volume:
- Database volume: `/var/lib/nextsql` (`nextsql-data`)
- Root key volume: `/run/secrets` (`nextsql-keys`)

> [!CAUTION]
> **Data Loss Warning**: Running `docker compose down -v` deletes all named volumes, including `nextsql-keys`. If the root unlock key is destroyed, the encrypted database in `nextsql-data` cannot be decrypted or recovered. Always take a verified backup (`nextsql backup`) before removing volumes.

### Host File Permissions with Unprivileged User
`nextsqld` runs as the unprivileged `nextsql` user (**uid 10001**). Docker Compose bind-mounts secrets preserving the host file ownership and permissions:
- If a secret file on the host has mode `0600` owned by your host user (e.g. uid 1000), container uid 10001 cannot read it and the container will exit.
- Set mode `0644` on secret files in development: `chmod 644 secrets/*`. Protect secret access at the directory level on the host.

### Mandatory TLS 1.3 Off Loopback
NextSQL strictly requires **TLS 1.3** for any non-loopback network connection:
- Intra-container loopback (`127.0.0.1:7210`) permits `--insecure` for local health checks and CLI execution.
- Inter-container communication (e.g. an application service talking to `nextsql:7210`) and host connections require TLS 1.3.
- The server TLS certificate's Subject Alternative Name (SAN) must list all hostnames used to reach it (e.g. `DNS:nextsql`, `DNS:localhost`).

### No Credentials in URLs
NextSQL drivers and tools strictly reject passwords and keys in connection strings (e.g. `nextsql://user:pass@host/db` is rejected). Credentials must always be provided via separate configuration parameters or secret files.

---

## 2. Preparing Secrets and TLS Certificates

Create a local `secrets/` directory for bootstrap credentials and TLS certificates:

```bash
mkdir -p secrets
umask 077

# 1. Bootstrap password file
printf 'change-this-development-password\n' > secrets/app-password

# 2. Generate a development TLS certificate with SAN for localhost and the Compose service name
openssl req -x509 -newkey rsa:3072 -nodes -days 365 \
  -keyout secrets/server.key -out secrets/server.crt \
  -subj '/CN=nextsql' \
  -addext 'subjectAltName=DNS:nextsql,DNS:localhost,IP:127.0.0.1'

# 3. Allow unprivileged container user (uid 10001) to read secrets
chmod 644 secrets/app-password secrets/server.key secrets/server.crt
```

---

## 3. Single-Node Setup

### Project Layout

```text
my-project/
├── docker-compose.yml
└── secrets/
    ├── app-password
    ├── server.crt
    └── server.key
```

### `docker-compose.yml`

A complete, production-ready single-node configuration:

```yaml
services:
  nextsql:
    image: bzynchub/nextsql:0.0.7
    # Or build locally from source:
    # build: .
    restart: unless-stopped
    ports:
      - "7210:7210"
    environment:
      NEXTSQL_SERVER_USER: app
      NEXTSQL_SERVER_PASSWORD_FILE: /run/bootstrap/app-password
      NEXTSQL_TLS_CERT: /run/tls/server.crt
      NEXTSQL_TLS_KEY: /run/tls/server.key
      NEXTSQL_PROFILE: developer    # Use 'production' for strict fail-closed preflight
      NEXTSQL_PRESET: balanced      # conservative (10% RAM), balanced (25%), high-performance (50%)
    volumes:
      - nextsql-data:/var/lib/nextsql
      - nextsql-keys:/run/secrets
    secrets:
      - source: app_password
        target: /run/bootstrap/app-password
      - source: server_crt
        target: /run/tls/server.crt
      - source: server_key
        target: /run/tls/server.key
    healthcheck:
      test: ["CMD", "/usr/local/bin/nextsql", "status", "--local", "--data-dir", "/var/lib/nextsql", "--key-file", "/run/secrets/root.key"]
      interval: 10s
      timeout: 3s
      retries: 5
      start_period: 3s

volumes:
  nextsql-data:
  nextsql-keys:

secrets:
  app_password:
    file: ./secrets/app-password
  server_crt:
    file: ./secrets/server.crt
  server_key:
    file: ./secrets/server.key
```

### Starting and Managing NextSQL

Start the container in the background:
```bash
docker compose up -d
```

View live server logs:
```bash
docker compose logs -f nextsql
```

Check service status and health:
```bash
docker compose ps
```

Stop and restart:
```bash
docker compose stop
docker compose start
```

Tear down containers (keeping persisted data and keys intact):
```bash
docker compose down
```

---

## 4. Full-Stack Application Integration

When running an application service (e.g. Node.js, Go, Python, or Ruby) alongside NextSQL in Docker Compose:
1. Connect via the Compose service name (`nextsql:7210`).
2. Provide the server certificate (`server.crt`) to the application as a trusted CA so the TLS handshake succeeds.
3. Provide application credentials securely via environment variables or secrets.
4. Use Compose `depends_on` with `condition: service_healthy` so the application waits for NextSQL to complete initialization.

### Full-Stack `docker-compose.yml` Example

```yaml
services:
  nextsql:
    image: bzynchub/nextsql:0.0.7
    restart: unless-stopped
    ports:
      - "7210:7210"
    environment:
      NEXTSQL_SERVER_USER: app
      NEXTSQL_SERVER_PASSWORD_FILE: /run/bootstrap/app-password
      NEXTSQL_TLS_CERT: /run/tls/server.crt
      NEXTSQL_TLS_KEY: /run/tls/server.key
      NEXTSQL_PROFILE: developer
    volumes:
      - nextsql-data:/var/lib/nextsql
      - nextsql-keys:/run/secrets
    secrets:
      - source: app_password
        target: /run/bootstrap/app-password
      - source: server_crt
        target: /run/tls/server.crt
      - source: server_key
        target: /run/tls/server.key
    healthcheck:
      test: ["CMD", "/usr/local/bin/nextsql", "status", "--local", "--data-dir", "/var/lib/nextsql", "--key-file", "/run/secrets/root.key"]
      interval: 5s
      timeout: 3s
      retries: 5
      start_period: 3s

  app:
    build:
      context: ./backend
      dockerfile: Dockerfile
    restart: unless-stopped
    ports:
      - "8080:8080"
    environment:
      NEXTSQL_ADDR: "nextsql:7210"
      NEXTSQL_USER: "app"
      NEXTSQL_PASSWORD_FILE: "/run/secrets/app_password"
      NEXTSQL_TLS_CA: "/run/secrets/server_crt"
      NEXTSQL_DATABASE: "app"
    secrets:
      - app_password
      - server_crt
    depends_on:
      nextsql:
        condition: service_healthy

volumes:
  nextsql-data:
  nextsql-keys:

secrets:
  app_password:
    file: ./secrets/app-password
  server_crt:
    file: ./secrets/server.crt
  server_key:
    file: ./secrets/server.key
```

### Application Connection Examples

#### Go Driver (`github.com/bzync/nextsql/drivers/go`)
```go
package main

import (
    "context"
    "os"
    "github.com/bzync/nextsql/drivers/go"
)

func main() {
    pw, _ := os.ReadFile(os.Getenv("NEXTSQL_PASSWORD_FILE"))
    ca, _ := os.ReadFile(os.Getenv("NEXTSQL_TLS_CA"))

    db, err := nextsql.Open(nextsql.Config{
        Addr:       os.Getenv("NEXTSQL_ADDR"),
        User:       os.Getenv("NEXTSQL_USER"),
        Password:   string(pw),
        Database:   os.Getenv("NEXTSQL_DATABASE"),
        ServerName: "nextsql",
        RootCAs:    ca,
    })
    if err != nil {
        panic(err)
    }
    defer db.Close()

    res, err := db.Query(context.Background(), "SELECT 'Connected from Docker!' AS greeting")
    // ...
}
```

#### Node.js Driver (`@bzync/nextsql`)
```javascript
import fs from 'node:fs';
import { connect } from '@bzync/nextsql';

const client = await connect({
  addr: process.env.NEXTSQL_ADDR,
  user: process.env.NEXTSQL_USER,
  password: fs.readFileSync(process.env.NEXTSQL_PASSWORD_FILE, 'utf8').trim(),
  database: process.env.NEXTSQL_DATABASE,
  serverName: 'nextsql',
  tlsCA: fs.readFileSync(process.env.NEXTSQL_TLS_CA),
});

const result = await client.query("SELECT 'Connected from Docker!' AS greeting");
console.log(result.rows);
```

---

## 5. Multi-Node High Availability (HA) Raft Cluster

NextSQL has built-in Raft replication requiring a minimum of **3 voting nodes** (`internal/replication.MinVotingNodes`). The repository includes `docker-compose.ha.yml` for running a 3-node HA cluster locally.

### 1. Multi-SAN Certificate Generation
Cluster nodes verify each other's certificates during Raft peer handshakes and client queries. Generate a certificate covering all node service names:

```bash
mkdir -p secrets
umask 077
printf 'change-this-development-password\n' > secrets/app-password
openssl req -x509 -newkey rsa:3072 -nodes -days 365 \
  -keyout secrets/server.key -out secrets/server.crt \
  -subj '/CN=node-a' \
  -addext 'subjectAltName=DNS:node-a,DNS:node-b,DNS:node-c,DNS:localhost'
chmod 644 secrets/app-password secrets/server.key secrets/server.crt
```

### 2. Architecture & Startup Sequence
In `docker-compose.ha.yml`:
1. **`node-a` (Bootstrap Node)**:
   - Initializes the database via `nextsql setup`.
   - Generates the root unlock key on the shared `nextsql-keys` volume.
   - Creates an initial backup to `/seed/backup` on the shared `nextsql-seed` volume (`NEXTSQL_SEED_TO`).
   - Waits (`NEXTSQL_JOIN_WAIT: "node-b:7300,node-c:7300"`) for peers to begin listening on their Raft ports.
   - Starts `nextsqld --raft-bootstrap` to form the cluster quorum.
2. **`node-b` & `node-c` (Follower Nodes)**:
   - Do **not** run `nextsql setup`.
   - Wait for `node-a`'s seed backup to become verified (`NEXTSQL_SEED_FROM: /seed/backup`).
   - Restore `node-a`'s seed backup so all cluster nodes share the exact same database identity and encryption keys.
   - Join the Raft cluster on port `7300`.

### 3. Running the HA Cluster

Start all three nodes:
```bash
docker compose -f docker-compose.ha.yml up -d
```

Verify cluster status:
```bash
docker compose -f docker-compose.ha.yml exec node-a nextsql cluster status --data-dir /var/lib/nextsql
```
Expected output:
```text
cluster status: healthy
voters: 3
has_leader: true
leader_id: node-a
```

Inspect replication status over SQL:
```bash
docker compose -f docker-compose.ha.yml exec node-a nextsql exec \
  --addr 127.0.0.1:7210 --user app --password-file /run/bootstrap/app-password --insecure \
  -c "SHOW CLUSTER;"
```

### 4. Testing Automatic Failover

Stop the active leader:
```bash
docker compose -f docker-compose.ha.yml stop node-a
```

Check cluster status from one of the remaining nodes:
```bash
docker compose -f docker-compose.ha.yml exec node-b nextsql cluster status --data-dir /var/lib/nextsql
```
Raft elects a new leader between `node-b` and `node-c`, maintaining quorum and write availability. Restart `node-a` to automatically rejoin:
```bash
docker compose -f docker-compose.ha.yml start node-a
```

---

## 6. Day-2 Operations

All administrative tasks can be performed directly inside the running container using `docker compose exec`.

### Executing SQL Queries
Run one-off queries using loopback `--insecure` inside the container:
```bash
docker compose exec nextsql nextsql exec \
  --addr 127.0.0.1:7210 \
  --user app \
  --password-file /run/bootstrap/app-password \
  --insecure \
  -c "CREATE TABLE users (id UUID PRIMARY KEY DEFAULT UUID(), email STRING NOT NULL);"
```

### Applying Schema Migrations
If you mount a migrations directory into the container:
```bash
docker compose exec nextsql nextsql migrate up \
  --dir /migrations \
  --addr 127.0.0.1:7210 \
  --user app \
  --password-file /run/bootstrap/app-password \
  --insecure
```

### Physical Backups & Verification
Mount a backup volume or host directory (e.g. `- ./backups:/backups`) and trigger an encrypted physical backup:

```bash
# 1. Take encrypted backup
docker compose exec nextsql nextsql backup \
  --data-dir /var/lib/nextsql \
  --key-file /run/secrets/root.key \
  --out /backups/backup-$(date +%Y%m%d%H%M%S)

# 2. Verify backup integrity (verifies checksums and performs test open)
docker compose exec nextsql nextsql verify \
  --from /backups/backup-$(date +%Y%m%d%H%M%S) \
  --key-file /run/secrets/root.key
```

### Checking Local Engine Status
Inspect internal database state, durable LSNs, checkpoint status, and admission queue counters:
```bash
docker compose exec nextsql nextsql status --local \
  --data-dir /var/lib/nextsql \
  --key-file /run/secrets/root.key
```

---

## 7. Container Environment Variables Reference

The container PID-1 entrypoint (`internal/dockerentry`) configures and boots NextSQL based on the following variables:

| Variable | Default | Description |
|---|---|---|
| `NEXTSQL_SERVER_USER` | *(none)* | Bootstrap administrator username created on first start (required on fresh initialization). |
| `NEXTSQL_SERVER_PASSWORD_FILE` | `/run/bootstrap/password` | Path to file containing the administrator password. |
| `NEXTSQL_SERVER_PASS` | *(none)* | Direct password string in env. If set, written to a private temporary file for setup and wiped immediately. |
| `NEXTSQL_PROFILE` | `developer` | `developer` or `production`. `production` enforces strict fail-closed preflight (admin required, key off data volume, TLS 1.3 off loopback) and writes operational defaults. |
| `NEXTSQL_PRESET` | `balanced` | Buffer-pool sizing preset: `conservative` (10% RAM), `balanced` (25%), `high-performance` (50%), or `custom`. |
| `NEXTSQL_BUFFER_PAGES` | *(none)* | Explicit number of 16 KiB buffer pages. Overrides `NEXTSQL_PRESET`. |
| `NEXTSQL_DATA_DIR` | `/var/lib/nextsql` | Database storage directory for encrypted pages, WAL, undo logs, and catalog files. |
| `NEXTSQL_KEY_FILE` | `/run/secrets/root.key` | Location of the external root unlock key. Must reside on a separate volume from `NEXTSQL_DATA_DIR`. |
| `NEXTSQL_LISTEN` | `0.0.0.0:7210` | Server listen host and port for the native NSQL v1 protocol. |
| `NEXTSQL_TLS_CERT` | *(none)* | In-container path to the server TLS certificate (PEM). Required with `NEXTSQL_TLS_KEY`. |
| `NEXTSQL_TLS_KEY` | *(none)* | In-container path to the server TLS private key (PEM). Required with `NEXTSQL_TLS_CERT`. |
| `NEXTSQL_TLS_CLIENT_CA` | *(none)* | Optional PEM bundle of client CA certificates for mTLS service authentication. |
| `NEXTSQL_TLS_CLIENT_CRL` | *(none)* | Optional PEM certificate revocation list bundle for mTLS. Requires `NEXTSQL_TLS_CLIENT_CA`. |
| `NEXTSQL_CONFIG_FILE` | `$NEXTSQL_DATA_DIR/nextsql.conf` | Path where generated configuration is written during setup and loaded on subsequent restarts. |
| `NEXTSQL_NODE_ID` | *(none)* | Cluster node identifier for Raft replication (e.g. `node-a`). |
| `NEXTSQL_RAFT_BIND` | *(none)* | Internal host:port for inter-node Raft consensus traffic (e.g. `node-a:7300`). |
| `NEXTSQL_RAFT_JOIN` | *(none)* | Comma-delimited list of all cluster peer addresses (e.g. `node-a=node-a:7300,node-b=node-b:7300,node-c=node-c:7300`). |
| `NEXTSQL_RAFT_BOOTSTRAP` | *(none)* | Set to `"1"` on the single designated bootstrap node to initialize the Raft cluster. |
| `NEXTSQL_JOIN_WAIT` | *(none)* | Comma-delimited peer addresses to probe before the bootstrap node begins `AddVoter` handshake. |
| `NEXTSQL_SEED_TO` | *(none)* | Path on a shared volume where the bootstrap node writes its initial backup for joining replicas. |
| `NEXTSQL_SEED_FROM` | *(none)* | Path on a shared volume from which follower replicas restore initial cluster state. |

---

## 8. Troubleshooting & Common Pitfalls

### 1. `permission denied` reading password file or TLS key
**Cause**: `nextsqld` runs as uid 10001 (`nextsql`). If host secrets have permissions `0600` owned by your personal host user, the unprivileged container user cannot read them.  
**Fix**: Run `chmod 644 secrets/*` on the host. Restrict directory permissions on the `secrets/` folder itself.

### 2. `x509: certificate is valid for ..., not nextsql`
**Cause**: The application is connecting to hostname `nextsql:7210`, but the server's TLS certificate SAN only includes `localhost`.  
**Fix**: Re-generate the development certificate with `subjectAltName=DNS:nextsql,DNS:localhost,IP:127.0.0.1`.

### 3. Fail-Closed Exit in Production Profile
**Cause**: `NEXTSQL_PROFILE=production` failed the startup preflight check.  
**Common reasons**:
- `NEXTSQL_SERVER_USER` or password file is missing.
- Root key file (`NEXTSQL_KEY_FILE`) was placed inside the data directory volume.
- Non-loopback listen address configured without TLS certificates.  
**Fix**: Ensure all three production requirements are satisfied before starting.

### 4. Data Loss After `docker compose down -v`
**Cause**: Running `docker compose down -v` removes the `nextsql-keys` volume containing the root unlock key.  
**Fix**: Before tearing down volumes, run `docker compose exec nextsql nextsql backup ...` to an external directory, or avoid using the `-v` flag unless you deliberately intend to wipe all data.
