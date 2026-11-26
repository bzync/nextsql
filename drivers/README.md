# NextSQL Official Client Drivers

This directory contains the six official, first-party client drivers for **NextSQL**, plus the shared JavaScript/TypeScript wire codec (`drivers/js`).

Every official driver speaks the native **NSQL v1** binary protocol directly over TLS 1.3 with **zero third-party runtime dependencies**. NextSQL does not use or emulate PostgreSQL (`libpq`), MySQL, or HTTP/REST query wrappers.

---

## Driver Matrix

| Language / Runtime | Directory | Package Name | Current Version | Runtime Requirement |
|---|---|---|---|---|
| **Go** | [`drivers/go`](go) | `github.com/bzync/nextsql/drivers/go` | Repository module | Go 1.24+ |
| **Node.js / TypeScript** | [`drivers/node`](node) | [`@bzync/nextsql`](https://www.npmjs.com/package/@bzync/nextsql) | `0.0.3` | Node.js 20+ (zero deps, `.d.ts` bundled) |
| **Bun** | [`drivers/bun`](bun) | `drivers/bun` | `0.0.3` | Bun 1.0+ |
| **Python** | [`drivers/python`](python) | [`bzync-nextsql`](https://pypi.org/project/bzync-nextsql/) | `0.0.3` | Python 3.10+ (stdlib only) |
| **PHP** | [`drivers/php`](php) | [`bzync/nextsql`](https://packagist.org/packages/bzync/nextsql) | `0.0.1` | PHP 8.1+ (`ext-openssl`) |
| **Ruby** | [`drivers/ruby`](ruby) | [`bzync-nextsql`](https://rubygems.org/gems/bzync-nextsql) | `0.0.3` | Ruby 3.0+ (stdlib only) |
| *Shared JS Codec* | [`drivers/js`](js) | Internal shared ESM codec | — | Shared by `drivers/node` and `drivers/bun` |

---

## Feature Parity Across All Six Drivers

| Capability | Go | Node.js / TS | Bun | Python | PHP | Ruby |
|---|:---:|:---:|:---:|:---:|:---:|:---:|
| Native NSQL v1 framing & TLS 1.3 | Yes | Yes | Yes | Yes | Yes | Yes |
| Client root key unlock (`--require-client-key`) | Yes | Yes | Yes | Yes | Yes | Yes |
| Short-lived token (`NSSC1.`) authentication | Yes | Yes | Yes | Yes | Yes | Yes |
| Connection pooling (`Pool` / `create_pool`) | `database/sql` | Yes | Yes | Yes | Yes | Yes |
| Client-side LRU prepared statement cache | Server + `sql.DB` | Yes | Yes | Yes | Yes | Yes |
| Streaming batch queries (`FlowAck` backpressure) | Yes | Yes | Yes | Yes | Yes | Yes |
| Out-of-band query cancellation (`FlagCancel`) | Yes | Yes | Yes | Yes | Yes | Yes |
| Durable idempotent mutations (`ExecIdempotent`) | Yes | Yes | Yes | Yes | Yes | Yes |
| Cluster follower-read routing (`STRONG`/`BOUNDED`/`STALE`) | Yes | Yes | Yes | Yes | Yes | Yes |
| Randomized client field encryption (`NSCE1.` AES-256-GCM) | Yes | Yes | Yes | Yes | Yes | Yes |
| Deterministic client field encryption (`NSCE2.` AES-256-SIV) | Yes | Yes | Yes | Yes | Yes | Yes |
| Durable `FileFieldKeyring` rotation & revocation | Yes | Yes | Yes | Yes | Yes | Yes |
| Stable public error codes (`ERR_*`) | Yes | Yes | Yes | Yes | Yes | Yes |

---

## Security Rules for All Drivers

1. **No secrets in URLs:** Drivers reject connection URLs containing raw encryption keys or passwords (outside Go's optional `database/sql` local DSN adapter). Always pass credentials and keys through typed `Config` structs/objects.
2. **TLS 1.3 off loopback:** Plaintext (`InsecureNoTLS` / `insecure_no_tls`) is permitted only for loopback addresses (`127.0.0.1`, `::1`, `localhost`). Remote connections require TLS 1.3.
3. **Client-encrypted fields stay on the client:** `FieldKeyProvider` (`MemoryFieldKeyring` / `FileFieldKeyring`) never transmits field encryption keys to `nextsqld`.

---

## Running Driver Test Suites

Run all six driver test suites from the repository root with:

```bash
make test-drivers
```

Or test an individual driver:

```bash
make test-driver-go
make test-driver-node
make test-driver-bun
make test-driver-python
make test-driver-php
make test-driver-ruby
```
