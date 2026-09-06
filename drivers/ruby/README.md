# bzync-nextsql

Official [NextSQL](https://nextsql.bzync.com) driver for Ruby 3.0+. Speaks the native
NSQL v1 wire protocol over TLS 1.3. Pure standard library — no runtime gems.

Encryption keys and passwords are **never** accepted in a connection URL.

```bash
gem install bzync-nextsql
```

```ruby
require "nextsql"

conn = NextSQL.connect(NextSQL::Config.new(
  address: "db.example.com:7210",
  database: "production",
  user: "app",
  password: ENV["NEXTSQL_DATABASE_PASS"],
  tls: NextSQL::TLSConfig.new(cafile: "/etc/nextsql/ca.pem", server_name: "db.example.com"),
))

begin
  result = conn.exec("SELECT id, name FROM users WHERE id = $1", [1])
  result.rows.each { |row| puts row.inspect }
ensure
  conn.close
end
```

Plaintext connections are allowed only on loopback. For an HA cluster with
follower-read routing, use `NextSQL.connect_cluster`.

- Full driver docs: <https://nextsql.bzync.com/docs/drivers>
- Wire protocol: <https://github.com/bzync/nextsql/blob/master/docs/protocol.md>

MIT licensed.
