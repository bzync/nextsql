# bzync-nextsql

Official [NextSQL](https://nextsql.bzync.com) driver for Python 3.10+. Speaks the
native NSQL v1 wire protocol over TLS 1.3. Pure standard library — no
runtime dependencies.

Encryption keys and passwords are **never** accepted in a connection URL.

```bash
pip install bzync-nextsql
```

```python
import nextsql

conn = nextsql.connect(nextsql.Config(
    address="db.example.com:7210",
    database="production",
    user="app",
    password=os.environ["NEXTSQL_DATABASE_PASS"],
    tls=nextsql.TLSConfig(cafile="/etc/nextsql/ca.pem", server_name="db.example.com"),
))
try:
    result = conn.exec("SELECT id, name FROM users WHERE id = $1", [1])
    for row in result.rows:
        print(row)
finally:
    conn.close()
```

Plaintext connections are allowed only on loopback (`insecure_no_tls=True`).
For an HA cluster with follower-read routing, use `nextsql.connect_cluster`.

- Full driver docs: <https://nextsql.bzync.com/docs/drivers>
- Wire protocol: <https://github.com/bzync/nextsql/blob/master/docs/protocol.md>

MIT licensed.
