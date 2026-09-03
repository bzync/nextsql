# Drivers

Official drivers speak the native **NSQL v1** protocol. **Do not put keys or passwords in a URL.** TLS 1.3 is required off loopback.

| Runtime | Path | Open |
|---|---|---|
| Go | [`drivers/go`](https://github.com/bzync/nextsql/tree/main/drivers/go) | `nextsql.Open(nextsql.Config{…})` |
| Node.js 18+ | [`drivers/node`](https://github.com/bzync/nextsql/tree/main/drivers/node) | `connect({ address, user, password, tls })` |
| Bun | [`drivers/bun`](https://github.com/bzync/nextsql/tree/main/drivers/bun) | same shape as Node |
| Deno | [`drivers/deno`](https://github.com/bzync/nextsql/tree/main/drivers/deno) | `import { connect } from "./mod.ts"` |
| PHP 8.1+ | [`drivers/php`](https://github.com/bzync/nextsql/tree/main/drivers/php) | `NextSQL\Client::connect([…])` |
| Python 3.10+ | [`drivers/python`](https://github.com/bzync/nextsql/tree/main/drivers/python) | `nextsql.connect(nextsql.Config(…))` |
| Ruby 3.0+ | [`drivers/ruby`](https://github.com/bzync/nextsql/tree/main/drivers/ruby) | `NextSQL.connect(NextSQL::Config.new(…))` |

Shared TypeScript types: [`drivers/js/types.d.ts`](https://github.com/bzync/nextsql/blob/main/drivers/js/types.d.ts).

## Common API

`exec` (materialize), `query` (stream rows), `prepare` / execute, `cancel`, `close`. A connection is single-flight: a second query while rows are open returns `conflict`.

Address is `host:port` only. Values containing `://`, `key=`, or `password=` are rejected.

When `nextsqld` is started with `--require-client-key`, the first authenticated client supplies the 32-byte root over TLS. See [TLS and client keys](/docs/tls).

## Language guides

- [Go](/docs/drivers-go)
- [Node, Bun, Deno](/docs/drivers-js)
- [PHP](/docs/drivers-php)
- [Python](/docs/drivers-python)
- [Ruby](/docs/drivers-ruby)

Engine note: [`docs/protocol.md`](https://github.com/bzync/nextsql/blob/main/docs/protocol.md).
