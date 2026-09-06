# Docker

The image runs `nextsqld` as the unprivileged `nextsql` user (uid 10001). Database files persist in `/var/lib/nextsql`. The root unlock key lives on a **separate** `/run/secrets` volume and is never placed in the database volume. Pages, WAL, and UNDO stay encrypted by default. The image is a static Go runtime (`FROM scratch`) with no shell; `docker exec` can still run `/usr/local/bin/nextsql`. Compose is the supported container path; a first-class OCI non-interactive installer path is not implemented.

## Single node

Prepare a password file and a TLS certificate. The certificate must include the hostname clients use. For a local development certificate:

```bash
mkdir -p secrets
umask 077
printf 'change-this-development-password\n' > secrets/app-password
openssl req -x509 -newkey rsa:3072 -nodes -days 30 \
  -keyout secrets/server.key -out secrets/server.crt \
  -subj '/CN=localhost' -addext 'subjectAltName=DNS:localhost'
chmod 644 secrets/app-password secrets/server.key secrets/server.crt
```

Docker Compose bind-mounts `secrets:` files as-is. `nextsqld` runs as uid 10001, so a host-owned `0600` file is invisible to it; the `chmod 644` above is for locally-scoped development secrets. Restrict the `secrets/` directory at the host layer.

```bash
docker compose up --build -d
```

The first start creates the encrypted database and root key in the named volumes. Subsequent starts reuse them. Connect with a native driver using TLS and `secrets/server.crt`. Remote listeners must use TLS 1.3.

Do not put the root key in a bind-mounted data directory or commit `secrets/`. Before removing volumes, use the native `nextsql backup` flow.

```bash
docker compose logs -f nextsql
docker compose stop
docker compose start
docker compose down                 # keeps named volumes
docker compose down -v              # destroys database and key volumes
```

`docker compose down -v` is destructive.

## Three-node HA cluster

`docker-compose.ha.yml` runs a 3-node Raft cluster as three containers on one Docker network. `nextsqld` enforces a 3-voter minimum; do not scale this file below 3 services.

The certificate needs every node's hostname as a SAN:

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
docker compose -f docker-compose.ha.yml exec node-a \
  nextsql cluster status --data-dir /var/lib/nextsql
```

`voters` should read `3` and `has_leader` should read `true`. Only **one** node ever runs `nextsql init`; the other two restore from that node's backup so every replica shares identity and the root unlock key. See [High availability](/docs/ha).

`docker compose -f docker-compose.ha.yml down -v` destroys all three databases, the shared root key, and the seed volume.

## Podman

The same image runs with Podman, including rootless Podman:

```bash
podman build -t nextsql:local .
podman secret create nextsql-app-password secrets/app-password
podman secret create nextsql-server-crt secrets/server.crt
podman secret create nextsql-server-key secrets/server.key
podman volume create nextsql-data
podman volume create nextsql-keys
```

Engine note: [`docs/docker.md`](https://github.com/bzync/nextsql/blob/main/docs/docker.md).
