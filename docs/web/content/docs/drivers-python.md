# Python driver

Python 3.10+ (stdlib only — `socket`, `ssl`, `decimal`, `json`; zero external runtime dependencies). MIT. Package version **0.0.3**. Driver package versions are independent of the engine version.

```bash
pip install bzync-nextsql==0.0.3
```

The distribution is `bzync-nextsql`; the import name is `nextsql`. Source: [`drivers/python`](https://github.com/bzync/nextsql/tree/master/drivers/python) — you can also vendor it directly from the repository (`sys.path.insert(0, "drivers/python")`).

---

## Quick start

```python
import decimal
import os
import nextsql

conn = nextsql.connect(nextsql.Config(
    address="127.0.0.1:7210",
    database="default",
    user="app",
    password=os.environ["NEXTSQL_DATABASE_PASS"],
    insecure_no_tls=True,  # loopback only
))
try:
    res = conn.exec(
        "SELECT name FROM items WHERE price < $1",
        [decimal.Decimal("50.00")],
    )
    for row in res.rows:
        print(row)
finally:
    conn.close()
```

### Remote TLS & client unlock key

```python
conn = nextsql.connect(nextsql.Config(
    address="db.example.com:7210",
    database="production",
    user="app",
    password=os.environ["NEXTSQL_DATABASE_PASS"],
    tls=nextsql.TLSConfig(
        cafile="/etc/nextsql/ca.pem",
        server_name="db.example.com",
    ),
))
```

For `--require-client-key`, pass `key=client_root_32_bytes` (a 32-byte `bytes` object). Never put keys or passwords in a connection URL.

---

## Connection pooling & prepared statement LRU cache

Both `nextsql.Connection` and `nextsql.ConnectionPool` (`nextsql.create_pool`) maintain a client-side **LRU prepared statement cache** (`statement_cache_size=50` by default; `0` disables caching). Parameterized queries (`conn.query(sql, params)` or `pool.query(sql, params)`) automatically reuse server-side prepared statement handles and close evicted handles on the server (`TYPE_CLOSE_STMT`).

```python
pool = nextsql.create_pool(
    cfg,
    min_size=2,
    max_size=10,
    statement_cache_size=50,
)
try:
    # Shorthand query (auto-acquires and releases a pooled connection):
    rows = pool.query("SELECT id, name FROM users WHERE status = $1", ["active"])

    # Context-managed connection checkout:
    with pool.connection() as conn:
        conn.exec("INSERT INTO audit_log (event) VALUES ($1)", ["login"])
finally:
    pool.close()
```

---

## Streaming queries & idempotent mutations

* **Streaming batches (`conn.query`):** Returns a `Rows` iterator that streams `DataBatch` frames with `FlowAck` backpressure.
* **Explicit prepared statements (`conn.prepare`):** Returns a `Statement` with `.exec(params)`, `.query(params)`, and `.close()`.
* **Durable idempotent mutations (`conn.exec_idempotent`):**
  ```python
  res = conn.exec_idempotent(
      "order-20260826-42",
      "INSERT INTO orders (id, status) VALUES ($1, $2)",
      ["42", "created"],
  )
  ```

---

## Type mapping

| Python Type | NextSQL Type | Notes |
|---|---|---|
| `None` | `NULL` | |
| `bool` | `BOOL` | |
| `int` / `float` / `decimal.Decimal` | `DECIMAL` | Use `nextsql.Int8`/`Int16`/`Int32`/`Int64`, `Uint8`..`Uint64`, `Float32`/`Float64` for exact fixed-width numeric types |
| `str` | `STRING` / `TEXT` | |
| `bytes` | `BYTES` | |
| `uuid.UUID` | `UUID` | |
| `datetime.datetime` / `date` / `time` | `TIMESTAMPTZ` / `DATE` / `TIME` | Naive datetimes are treated as UTC; use `nextsql.NaiveTimestamp` for `TIMESTAMP` and `nextsql.Interval` for `INTERVAL` |
| `list[float]` / `nextsql.Vector` | `VECTOR` / `SPARSEVECTOR` | |
| `dict` / `list` | `JSON` | Decoded from server binary `NSJB` on read |
| `nextsql.Point` / `Box` / `Line` / `Polygon` | WGS84 Geo shapes | `nextsql.Geometry` for `GEOMETRY` / `GEOGRAPHY` |
| `nextsql.EnumValue` / `StructValue` / `MapValue` | `ENUM` / `STRUCT` / `MAP` | |

---

## Client-side field encryption

The Python driver ships full client-side encryption helpers (`NSCE1.` randomized AES-256-GCM and `NSCE2.` deterministic AES-256-SIV) and keyring providers (`MemoryFieldKeyring` and durable `FileFieldKeyring`):

```python
keyring = nextsql.FileFieldKeyring("/etc/nextsql/field-keys.json")
cfg = nextsql.Config(
    address="127.0.0.1:7210",
    database="default",
    user="app",
    password=os.environ["NEXTSQL_DATABASE_PASS"],
    insecure_no_tls=True,
    field_keys=keyring,
)
conn = nextsql.connect(cfg)

# Randomized encryption (NSCE1):
ct = conn.encrypt_field("patients", "ssn", "123-45-6789")
conn.exec("INSERT INTO patients (id, ssn) VALUES ($1, $2)", [1, ct])

row = conn.exec("SELECT ssn FROM patients WHERE id = $1", [1]).rows[0]
ssn = conn.decrypt_field("patients", "ssn", nextsql.FieldType.STRING, row[0])

# Deterministic encryption for equality lookups (NSCE2):
email_ct = conn.encrypt_field_deterministic("patients", "email", "alice@example.com")
rows = conn.exec("SELECT id FROM patients WHERE email = $1", [email_ct]).rows
```

See [Client-side encryption](/docs/client-encryption) for key rotation and security properties.

---

## Cluster routing & follower reads

`nextsql.connect_cluster` routes eligible read-only queries to healthy followers under `READ_STRONG`, `READ_BOUNDED`, or `READ_STALE` consistency and sends writes to the Raft leader:

```python
cluster = nextsql.connect_cluster(nextsql.Config(
    nodes=["10.0.0.1:7210", "10.0.0.2:7210", "10.0.0.3:7210"],
    database="production",
    user="app",
    password=os.environ["NEXTSQL_DATABASE_PASS"],
    tls=nextsql.TLSConfig(cafile="/etc/nextsql/ca.pem"),
    read_consistency=nextsql.READ_BOUNDED,
))
```

See [High availability](/docs/ha) and [Error codes & taxonomy](/docs/error-codes).
