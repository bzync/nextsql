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
```

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
