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

To require service certificates, add a client CA on the server:

```bash
./nextsqld \
  --data-dir /var/lib/nextsql \
  --key-file /etc/nextsql/root.key \
  --listen 0.0.0.0:7210 \
  --tls-cert /etc/nextsql/server.crt \
  --tls-key /etc/nextsql/server.key \
  --tls-client-ca /etc/nextsql/client-ca.pem \
  --tls-client-crl /etc/nextsql/client-crl.pem
```

The client certificate needs one URI SAN
`nextsql://service/<principal>` matching `--user`. The CLI loads it with
`--tls-client-cert FILE --tls-client-key FILE`. Native password authentication
and RBAC still apply.

Atomically replace the server certificate/key, client CA bundle, and optional
PEM CRL bundle, then send `SIGHUP` to `nextsqld`. Invalid reloads retain the
last known-good snapshot. CRLs must be current, signed by a configured client
authority, and cover every non-root certificate in the verified chain; missing
coverage and revoked serials fail closed. Successful mTLS reloads disconnect
all accepted connections, including in-progress handshakes, so they reconnect
under the new snapshot. Use an old+new CA overlap bundle while rotating trust.
OCSP is not implemented.

`--insecure` against a remote host is rejected. `--tls-ca` is a PEM CA / server certificate; SNI defaults to the host in `--addr` and can be overridden by `--tls-server-name` / `NEXTSQL_TLS_SERVER_NAME`.

## Short-lived credentials

Set `token_verify_keyset=FILE` (optionally `token_revocations=FILE`,
`token_audience=STRING`) and a client can authenticate with a signed
short-lived credential (`NSSC1.`…) sent **in place of the password** — no driver
change. The server checks the Ed25519 signature, the validity window (60 s
skew, max lifetime 24 h), the audience, the served-database scope, and
revocation; it also requires the credential's principal to match `--user` and
to be a known native user, narrows the session to the credential's role scope,
and closes the session when the credential expires. `SIGHUP` reloads the keyset
and revocation list. Issue and manage credentials with `nextsql token`
(`keygen`, `export-public`, `mint`, `revoke`, `rotate`, `retire`). See
[security](/docs/security).

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

Field-level `ENCRYPTED CLIENT` columns are designed but **not implemented**.
mTLS service certificates, live rotation, X.509 CRL revocation, and signed
short-lived credentials are implemented as described above; OCSP and external
IdP integration are not.
