# Ruby driver

Ruby 3.0+ (stdlib only — `socket`, `openssl`, `bigdecimal`, `json`; zero runtime gem dependencies). MIT. Gem version **0.0.3**. Driver package versions are independent of the engine version.

```bash
gem install bzync-nextsql -v 0.0.3
```

The gem is `bzync-nextsql`; load it with `require "nextsql"`. Source: [`drivers/ruby`](https://github.com/bzync/nextsql/tree/master/drivers/ruby) — you can also vendor it directly from the repository (`$LOAD_PATH.unshift("drivers/ruby/lib")`).

---

## Quick start

```ruby
require "bigdecimal"
require "nextsql"

conn = NextSQL.connect(NextSQL::Config.new(
  address: "127.0.0.1:7210",
  database: "default",
  user: "app",
  password: ENV.fetch("NEXTSQL_DATABASE_PASS"),
  insecure_no_tls: true, # loopback only
))

begin
  res = conn.exec("SELECT name FROM items WHERE price < $1", [BigDecimal("50.00")])
  res.rows.each { |row| p row }
ensure
  conn.close
end
```

### Remote TLS & client unlock key

```ruby
conn = NextSQL.connect(NextSQL::Config.new(
  address: "db.example.com:7210",
  database: "production",
  user: "app",
  password: ENV.fetch("NEXTSQL_DATABASE_PASS"),
  tls: NextSQL::TLSConfig.new(
    cafile: "/etc/nextsql/ca.pem",
    server_name: "db.example.com",
  ),
))
```

For `--require-client-key`, pass `key: client_root_32_bytes` (a 32-byte binary `String`). Never put keys or passwords in a connection URL.

---

## Connection pooling & prepared statement LRU cache

Both `NextSQL::Connection` and `NextSQL::Pool` (`NextSQL.create_pool`) include a client-side **LRU prepared statement cache** (`statement_cache_size: 50` by default; `0` disables caching). Parameterized queries automatically reuse server-side prepared statements and close evicted handles on the server (`Protocol::TYPE_CLOSE_STMT`).

```ruby
pool = NextSQL.create_pool(cfg, min_size: 2, max_size: 10, statement_cache_size: 50)
begin
  # Block checkout (always returns connection to pool):
  pool.with do |conn|
    res = conn.exec("SELECT id, name FROM users WHERE status = $1", ["active"])
    p res.rows
  end

  # Or direct shorthand execution:
  rows = pool.query("SELECT id, name FROM users WHERE status = $1", ["active"])
ensure
  pool.close
end
```

---

## Native wire protocol

The Ruby driver speaks NSQL v1 directly over a blocking `TCPSocket` wrapped by TLS 1.3 off loopback. It sends length-bounded `Hello`/`Auth`/`Unlock` frames, typed `Query`/`Prepare`/`Execute` parameters, streaming `RowDesc`/`DataBatch` results with `FlowAck` backpressure, `Cancel` on a separate authenticated connection, `IdempotentQuery`, `SetReadConsistency`, and `NodeStatus`. Server `Error` is always followed by `Ready`; the driver drains that `Ready` before raising `NextSQL::Error`, so a handled statement error never desynchronizes the connection.

---

## Types

* `nil` ↔ SQL `NULL`
* `true` / `false` ↔ `BOOL`
* `Integer` / `Float` / `BigDecimal` ↔ `DECIMAL` (or explicit `NextSQL::Int8`..`Int64`, `Uint8`..`Uint64`, `Float32`/`Float64`)
* `String` ↔ `STRING` / `TEXT` / formatted `UUID`
* `Time` ↔ `TIMESTAMPTZ` (`NextSQL::Interval` for `INTERVAL`)
* `Array` of numbers or `NextSQL::Vector` ↔ `VECTOR` / `SPARSEVECTOR`
* `Hash` / `Array` ↔ `JSON` (decoded from binary `NSJB` on read)
* `NextSQL::Point` / `Box` / `Line` / `Polygon` and `NextSQL::Protocol::Geometry` ↔ WGS84 shapes and `GEOMETRY` / `GEOGRAPHY`
* `NextSQL::EnumValue` / `StructValue` / `MapValue` ↔ `ENUM` / `STRUCT` / `MAP`

---

## Client-side field encryption

The Ruby driver provides full client-side field encryption (`NSCE1.` randomized AES-256-GCM and `NSCE2.` deterministic AES-256-SIV) and keyring management (`MemoryFieldKeyring` and durable `FileFieldKeyring`):

```ruby
keyring = NextSQL::FileFieldKeyring.new("/etc/nextsql/field-keys.json")
cfg = NextSQL::Config.new(
  address: "127.0.0.1:7210",
  database: "default",
  user: "app",
  password: ENV.fetch("NEXTSQL_DATABASE_PASS"),
  insecure_no_tls: true,
  field_keys: keyring,
)
conn = NextSQL.connect(cfg)

# Randomized encryption (NSCE1):
ct = conn.encrypt_field("patients", "ssn", "123-45-6789")
conn.exec("INSERT INTO patients (id, ssn) VALUES ($1, $2)", [1, ct])

row = conn.exec("SELECT ssn FROM patients WHERE id = $1", [1]).rows.first
ssn = conn.decrypt_field("patients", "ssn", :string, row[0])

# Deterministic encryption for equality predicates (NSCE2):
email_ct = conn.encrypt_field_deterministic("patients", "email", "alice@example.com")
rows = conn.exec("SELECT id FROM patients WHERE email = $1", [email_ct]).rows
```

See [Client-side encryption](/docs/client-encryption) for key rotation and security properties.

---

## Cluster routing & follower reads

`NextSQL.connect_cluster` routes eligible read-only queries to healthy followers under `NextSQL::READ_STRONG`, `NextSQL::READ_BOUNDED`, or `NextSQL::READ_STALE` consistency and directs writes to the leader:

```ruby
cluster = NextSQL.connect_cluster(NextSQL::Config.new(
  nodes: ["10.0.0.1:7210", "10.0.0.2:7210", "10.0.0.3:7210"],
  database: "production",
  user: "app",
  password: ENV.fetch("NEXTSQL_DATABASE_PASS"),
  tls: NextSQL::TLSConfig.new(cafile: "/etc/nextsql/ca.pem"),
  read_consistency: NextSQL::READ_BOUNDED,
))
```

See [High availability](/docs/ha) and [Error codes & taxonomy](/docs/error-codes).
