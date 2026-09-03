# Ruby driver

Ruby 3.0+ (stdlib only — `socket`, `openssl`, `bigdecimal`, `json`; no
gem dependencies). Path: [`drivers/ruby`](https://github.com/bzync/nextsql/tree/main/drivers/ruby).
Not published as a gem — require it from this tree directly.

```ruby
$LOAD_PATH.unshift("drivers/ruby/lib")
require "nextsql"

conn = NextSQL.connect(NextSQL::Config.new(
  address: "127.0.0.1:7210",
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

## Types

`nil` ↔ SQL `NULL`; `true`/`false`/`Integer`/`Float`/`BigDecimal`/`String`
map as expected (`Integer`/`Float`/`BigDecimal` all encode as `DECIMAL`); a
formatted `String` for `UUID` columns (no dedicated UUID class in the
standard library); `Time` for `TIMESTAMPTZ`; an `Array` of numbers (or
`NextSQL::Vector` for sparse vectors) for `VECTOR`/`SPARSEVECTOR`;
`Hash`/`Array` for `JSON` (encoded as a JSON string parameter, decoded from
the server's binary JSON on the way back); `NextSQL::Point`/`Box`/`Line`/
`Polygon` for the spatial types.

## Cluster routing

`NextSQL.connect_cluster(NextSQL::Config.new(nodes: [...], read_consistency: NextSQL::READ_BOUNDED))`
returns a `Cluster` that sends eligible reads to a healthy follower and
everything else to the leader, failing over on a leader change. See
[HA / consistency](/docs/ha).
