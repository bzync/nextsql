# TLS and client-held keys

## Remote server

Any listen address that is not loopback requires TLS 1.3:

```bash
# example self-signed pair for a lab — replace with a real certificate
openssl req -x509 -newkey rsa:2048 -sha256 -days 365 -nodes \
  -keyout /etc/nextsql/server.key \
  -out    /etc/nextsql/server.crt \
  -subj "/CN=db.example.com"

./nextsqld \
  --data-dir /var/lib/nextsql \
  --key-file /etc/nextsql/root.key \
  --listen 0.0.0.0:7210 \
  --tls-cert /etc/nextsql/server.crt \
  --tls-key  /etc/nextsql/server.key \
  --user app --password-file /tmp/nextsql.pw
```

Client:

```bash
./nextsql exec \
  --addr db.example.com:7210 \
  --tls-ca /etc/nextsql/server.crt \
  --user app --password-file /tmp/nextsql.pw \
  -c "SELECT 1"
```

`--insecure` against a remote host is rejected. `--tls-ca` is a PEM CA / server certificate; SNI is taken from the host in `--addr`.

## REQUIRE CLIENT KEY

`nextsqld --require-client-key` does **not** load `--key-file`. After password auth the first client sends the 32-byte root over TLS (`TypeUnlock`). The host does not keep a long-lived key file.

```bash
./nextsqld \
  --data-dir /var/lib/nextsql \
  --require-client-key \
  --listen 127.0.0.1:7210 \
  --user app --password-file /tmp/nextsql.pw
```

The root still exists in RAM for the life of the unlocked process. That is not a zero-knowledge property.

Drivers:

- Go: `Config.KeyProvider` (a `crypto.KeyProvider` that returns the root DEK).
- Node / Bun / Deno: `key: <32-byte Buffer | Uint8Array>`.
- PHP: `'key' => $clientRoot` (32-byte string).

Field-level `ENCRYPTED CLIENT` columns are designed but **not implemented**. mTLS and external IdP are not in this version.
