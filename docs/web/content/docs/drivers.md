# Drivers

Official drivers speak the native **NSQL v1** protocol. **Do not put keys or passwords in a URL.** TLS 1.3 is required off loopback.

| Runtime | Install | Open |
|---|---|---|
| Go | `go get github.com/bzync/nextsql/drivers/go` | `nextsql.Open(nextsql.Config{…})` |
| Node.js 18+ | `npm i @bzync/nextsql` | `connect({ address, user, password, tls })` |
| Bun | [`drivers/bun`](https://github.com/bzync/nextsql/tree/master/drivers/bun) (repo tree) | same shape as Node |
| PHP 8.1+ | `composer require bzync/nextsql` | `NextSQL\Client::connect([…])` |
| Python 3.10+ | `pip install bzync-nextsql` | `nextsql.connect(nextsql.Config(…))` |
| Ruby 3.0+ | `gem install bzync-nextsql` | `NextSQL.connect(NextSQL::Config.new(…))` |

Shared TypeScript types: [`drivers/js/types.d.ts`](https://github.com/bzync/nextsql/blob/master/drivers/js/types.d.ts) (bundled into `@bzync/nextsql`).

## Common API

`exec` (materialize), `query` (stream rows), `prepare` / execute, `cancel`, `close`. A connection is single-flight: a second query while rows are open returns `conflict`.

Address is `host:port` only. Values containing `://`, `key=`, or `password=` are rejected.

When `nextsqld` is started with `--require-client-key`, the first authenticated client supplies the 32-byte root over TLS. See [TLS and client keys](/docs/tls).

Every official driver also ships a cluster client (`OpenCluster` / `connectCluster` / `connect_cluster` / `NextSQL\Cluster::connect`) that sends eligible reads to a healthy follower under `STRONG` / `BOUNDED` / `STALE`. See [High availability](/docs/ha).

`database` on the connect config names the deployment's database on Hello; an empty name accepts whatever the deployment serves. `realm` is reserved and must stay empty — multi-realm hosting was removed.

All six drivers encode the scalar type expansion (`BLOB`, integers, floats, `DATE`/`TIME`/`TIMESTAMP`/`INTERVAL`, `CHAR`/`VARCHAR`, `ENUM`) and collections (`STRUCT`/`ARRAY`/`MAP`). Field-encryption helpers exist in Go/Node/Bun/PHP only.

## Language guides

- [Go](/docs/drivers-go)
- [Node, Bun](/docs/drivers-js)
- [PHP](/docs/drivers-php)
- [Python](/docs/drivers-python)
- [Ruby](/docs/drivers-ruby)

Engine note: [`docs/protocol.md`](https://github.com/bzync/nextsql/blob/main/docs/protocol.md).
