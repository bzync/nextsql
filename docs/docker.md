# Docker installation

The image runs `nextsqld` as the unprivileged `nextsql` user. Database files
are persisted in `/var/lib/nextsql`; the root unlock key is persisted in the
separate `/run/secrets` volume and is never placed in the database volume.
Pages, WAL, and UNDO remain encrypted by default.

Prepare a password file and TLS certificate/key. The certificate must include
the hostname clients use. For a local-only development certificate:

```bash
mkdir -p secrets
umask 077
printf 'change-this-development-password\n' > secrets/app-password
openssl req -x509 -newkey rsa:3072 -nodes -days 30 \
  -keyout secrets/server.key -out secrets/server.crt \
  -subj '/CN=localhost' -addext 'subjectAltName=DNS:localhost'
chmod 644 secrets/app-password secrets/server.key secrets/server.crt
```

Docker Compose (unlike Swarm) bind-mounts `secrets:` files as-is, preserving
the host file's owner and mode — it does not remap them to the container
user. `nextsqld` runs as the unprivileged `nextsql` user (uid 10001), so a
`0600` file owned by your host user is invisible to it; hence the `chmod 644`
above (world-readable is fine for locally-scoped dev secrets — restrict at
the host/filesystem layer instead, e.g. keep `secrets/` under a directory
only reachable by users who should have it).

Start the server:

```bash
docker compose up --build -d
```

The first start creates the encrypted database and root key in the named
volumes. Subsequent starts reuse them. Connect with a native driver using TLS
and the certificate in `secrets/server.crt`; remote listeners must use TLS 1.3.

Do not put the root key in a bind-mounted data directory or commit `secrets/`.
Before removing volumes, use the native `nextsql backup` flow.

```bash
docker compose logs -f nextsql
docker compose stop
docker compose start
docker compose down                 # keeps named volumes
docker compose down -v              # destroys database and key volumes
```

`docker compose down -v` is destructive and removes the persisted database and
unlock key.

## Multi-node HA cluster

`docker-compose.ha.yml` runs a 3-node Raft cluster (`internal/replication`,
`docs/ha.md`) as three containers on one Docker network — one process per
node, same as any bare-metal/VPS deployment (see `docs/ha.md` "Operations").
`nextsqld` itself enforces a 3-voter minimum
(`internal/replication.MinVotingNodes`); do not scale this file below 3
services.

Prepare secrets first — the certificate needs every node's hostname as a SAN,
since clients and inter-node Raft traffic both resolve nodes by their Compose
service name:

```bash
mkdir -p secrets
umask 077
printf 'change-this-development-password\n' > secrets/app-password
openssl req -x509 -newkey rsa:3072 -nodes -days 30 \
  -keyout secrets/server.key -out secrets/server.crt \
  -subj '/CN=node-a' \
  -addext 'subjectAltName=DNS:node-a,DNS:node-b,DNS:node-c,DNS:localhost'
```

```bash
docker compose -f docker-compose.ha.yml up --build -d
```

### How the three nodes come up with one identity

Every replica of one database must share the same identity and root unlock
key (`docs/ha.md` "All replicas of one database share the keystore / root
unlock key") — there is no CLI flag to give `nextsql init` an existing
identity, so only one node may ever run `init`:

1. **`node-a`** (the Raft bootstrap node) runs `nextsql init` into its own
   volume on first start, then `nextsql backup`s that freshly-initialized
   (still pre-Raft, so trivially small) database into the shared
   `nextsql-seed` volume.
2. **`node-b`/`node-c`** never run `init`. Each waits for `node-a`'s backup
   to reach the `verified` state (`docs/backup.md` "On-disk layout" — the
   marker file written last, after hash checks and a restore-test open),
   then runs `nextsql restore` from it. This is exactly the same
   `backup`/`restore` path `docs/ha.md` "Replica repair" uses for a wiped
   replica — a fresh join and a repair are the same operation.
3. The root unlock key lives on the shared `nextsql-keys` volume mounted
   into all three containers: `node-a` creates it, `node-b`/`node-c` only
   ever read it back. It is never generated independently per node.
4. Only once `node-a` sees `node-b` and `node-c` already listening on their
   Raft ports does it start `nextsqld --raft-bootstrap`. This ordering is
   required, not cosmetic: `--raft-bootstrap` triggers one internal
   `AddVoter` attempt per peer with about a second of total retry budget
   (`internal/replication.Cluster.JoinPeers`) and does not retry again on
   failure, so every peer's Raft transport must already be reachable when
   that attempt fires.

All of this is driven by `docker/entrypoint.sh` from environment variables —
`NEXTSQL_SEED_TO` / `NEXTSQL_SEED_FROM` (the seed handoff),
`NEXTSQL_NODE_ID` / `NEXTSQL_RAFT_BIND` / `NEXTSQL_RAFT_JOIN` /
`NEXTSQL_RAFT_BOOTSTRAP` (passed straight through to the matching `nextsqld`
flags in `docs/ha.md` "Operations"), and `NEXTSQL_JOIN_WAIT` (the bootstrap
node's pre-flight peer check, step 4 above). None of this fires unless those
variables are set, so the plain single-node `docker-compose.yml` is
unaffected.

### Verifying the cluster

```bash
docker compose -f docker-compose.ha.yml exec node-a nextsql cluster status --data-dir /var/lib/nextsql
```

`voters` should read `3` and `has_leader` should read `true`. From any node,
`SHOW CLUSTER` (`internal/sql/parser` alias for `SELECT * FROM
system.replication`) reports the same state over SQL. Writes issued against
any node are only ever accepted by the leader; kill its container and a new
leader is elected from the remaining two, matching the failover behavior
described in `docs/ha.md`.

### Operating it

Scaling to more than 3 nodes, adding a replaced node, and rolling
maintenance all follow the same `AddVoter` / backup-restore path described in
`docs/ha.md` "Replica repair and rolling maintenance" — there is no separate
Docker-specific procedure. `docker compose -f docker-compose.ha.yml down -v`
destroys all three databases, the shared root key, and the seed volume; take
a `nextsql backup` first if that data matters.

## Podman

The same image runs with Podman, including rootless Podman. Build it with:

```bash
podman build -t nextsql:local .
```

Create Podman-managed secrets and persistent volumes:

```bash
podman secret create nextsql-app-password secrets/app-password
podman secret create nextsql-server-crt secrets/server.crt
podman secret create nextsql-server-key secrets/server.key
podman volume create nextsql-data
podman volume create nextsql-keys
```

Run the server as the unprivileged image user:

```bash
podman run -d --name nextsql \
  --restart=unless-stopped -p 7210:7210 \
  -v nextsql-data:/var/lib/nextsql \
  -v nextsql-keys:/run/secrets \
  --secret nextsql-app-password,type=mount,target=/run/bootstrap/app-password \
  --secret nextsql-server-crt,type=mount,target=/run/tls/server.crt \
  --secret nextsql-server-key,type=mount,target=/run/tls/server.key \
  -e NEXTSQL_SERVER_USER=app \
  -e NEXTSQL_SERVER_PASSWORD_FILE=/run/bootstrap/app-password \
  -e NEXTSQL_TLS_CERT=/run/tls/server.crt \
  -e NEXTSQL_TLS_KEY=/run/tls/server.key \
  nextsql:local
```

To require service-certificate mTLS, mount the client CA bundle and set
`NEXTSQL_TLS_CLIENT_CA` to its in-container path. Each client certificate must
carry `nextsql://service/<principal>` matching its database user. Optionally
mount a PEM CRL bundle and set `NEXTSQL_TLS_CLIENT_CRL`; it requires the client
CA setting and fails closed on missing/stale chain coverage. After atomically
replacing mounted TLS files, send `SIGHUP` to the container. Invalid reloads
retain the last known-good snapshot; successful mTLS reloads disconnect all
accepted connections, including in-progress handshakes. OCSP is not
implemented.

Use `podman logs -f nextsql`, `podman stop nextsql`, and `podman start nextsql`
to operate the container. Remove the container with `podman rm nextsql`; only
remove the named volumes after taking a backup. Podman Compose can also consume
the checked-in `docker-compose.yml` when a Compose provider is installed:
`podman compose up --build -d`.
