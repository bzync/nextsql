# Ruby driver

Ruby 3.0+ (stdlib only — `socket`, `openssl`, `bigdecimal`, `json`; no
gem dependencies). MIT, versioned independently of the engine (gem `0.0.1`).

```bash
gem install bzync-nextsql
```

The gem is `bzync-nextsql`; you load it with `require "nextsql"`. Source:
[`drivers/ruby`](https://github.com/bzync/nextsql/tree/master/drivers/ruby) — you
can also vendor it straight from the tree (`$LOAD_PATH.unshift("drivers/ruby/lib")`).

```ruby
require "nextsql"

conn = NextSQL.connect(NextSQL::Config.new(
  address: "127.0.0.1:7210",
  database: "default",
  user: "app",
  password: "s3cret",
  insecure_no_tls: true,
))
res = conn.exec("SELECT name FROM items WHERE price < $1", [BigDecimal("50.00")])
conn.close
```

Remote TLS:

```ruby
conn = NextSQL.connect(NextSQL::Config.new(
  address: "db.example.com:7210",
  user: "app",
  password: "s3cret",
  tls: NextSQL::TLSConfig.new(cafile: "/etc/nextsql/ca.pem", server_name: "db.example.com"),
))
```

For `--require-client-key`, pass `key: client_root_32_bytes` (a binary
`String`). Never put keys or passwords in a URL.

## Native wire protocol

The Ruby driver speaks NSQL v1 directly over a blocking `TCPSocket` wrapped by
TLS 1.3 off loopback. It sends length-bounded Hello/Auth/Unlock frames, typed
Query/Prepare/Execute parameters, streaming RowDesc/DataBatch results with
FlowAck backpressure, Cancel on a separate authenticated connection,
IdempotentQuery, SetReadConsistency, and NodeStatus. Server Error is always
followed by Ready; the driver drains that Ready before raising
`NextSQL::Error`, so a handled statement error does not desynchronize the next
request. Frames, SQL text, parameters, prepared statements, and batches obey
the limits documented in [Wire protocol](/docs/protocol).

The driver does not emulate PostgreSQL or MySQL and exposes no compatibility
protocol. `database` names the deployment database; `realm` is reserved and
must remain empty. Field-encryption helpers are not yet present in Ruby, so an
application must not bind plaintext to an `ENCRYPTED CLIENT` column.

## Types

`nil` ↔ SQL `NULL`; `true`/`false`/`Integer`/`Float`/`BigDecimal`/`String`
map as expected (`Integer`/`Float`/`BigDecimal` all encode as `DECIMAL`); a
formatted `String` for `UUID` columns (no dedicated UUID class in the
standard library); `Time` for `TIMESTAMPTZ`; an `Array` of numbers (or
`NextSQL::Vector` for sparse vectors) for `VECTOR`/`SPARSEVECTOR`;
`Hash`/`Array` for `JSON` (encoded as a JSON string parameter, decoded from
the server's binary JSON on the way back); `NextSQL::Point`/`Box`/`Line`/
`Polygon` for the WGS84 shapes; `NextSQL::Interval` / `EnumValue` for
temporal and `ENUM` values; `NextSQL::Protocol::Geometry` /
`StructValue` / `MapValue` for `GEOMETRY`/`GEOGRAPHY` and collections.
Field-encryption helpers are not in this driver.

## Cluster routing

`NextSQL.connect_cluster(NextSQL::Config.new(nodes: [...], read_consistency: NextSQL::READ_BOUNDED))`
returns a `Cluster` that sends eligible reads to a healthy follower and
everything else to the leader, failing over on a leader change. See
[HA / consistency](/docs/ha).
