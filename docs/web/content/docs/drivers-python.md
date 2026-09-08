# Python driver

Python 3.10+ (stdlib only — `socket`, `ssl`, `decimal`, `json`; no external
dependencies). MIT, versioned independently of the engine (package `0.1.0`).

```bash
pip install bzync-nextsql
```

The distribution is `bzync-nextsql`; the import name is `nextsql`. Source:
[`drivers/python`](https://github.com/bzync/nextsql/tree/master/drivers/python) —
you can also vendor it straight from the tree (`sys.path.insert(0, "drivers/python")`).

```python
import decimal
import nextsql

conn = nextsql.connect(nextsql.Config(
    address="127.0.0.1:7210",
    realm="default",
    database="default",
    user="app",
    password="s3cret",
    insecure_no_tls=True,
))
res = conn.exec("SELECT name FROM items WHERE price < $1", [decimal.Decimal("50.00")])
conn.close()
```

Remote TLS:

```python
conn = nextsql.connect(nextsql.Config(
    address="db.example.com:7210",
    user="app",
    password="s3cret",
    tls=nextsql.TLSConfig(cafile="/etc/nextsql/ca.pem", server_name="db.example.com"),
))
```

For `--require-client-key`, pass `key=client_root_32_bytes` (a `bytes` object).
Never put keys or passwords in a URL.

## Types

`None` ↔ SQL `NULL`; `bool`/`int`/`float`/`decimal.Decimal`/`str` map as
expected (`int`/`float`/`Decimal` all encode as `DECIMAL`); `uuid.UUID` for
`UUID` columns; `datetime.datetime` for `TIMESTAMPTZ` (naive datetimes are
treated as UTC); a `list[float]` (or `nextsql.Vector` for sparse vectors) for
`VECTOR`/`SPARSEVECTOR`; `dict`/`list` for `JSON` (encoded as a JSON string
parameter, decoded from the server's binary JSON on the way back);
`nextsql.Point`/`Box`/`Line`/`Polygon` for the WGS84 shapes; `nextsql.Geometry`
for `GEOMETRY`/`GEOGRAPHY`; `datetime.date` / `time` plus `nextsql.Interval`
for temporal values; `nextsql.EnumValue` / `StructValue` / `MapValue` for
`ENUM` and collections. Field-encryption helpers are not in this driver.

## Cluster routing

`nextsql.connect_cluster(nextsql.Config(nodes=[...], read_consistency=nextsql.READ_BOUNDED))`
returns a `Cluster` that sends eligible reads to a healthy follower and
everything else to the leader, failing over on a leader change. See
[HA / consistency](/docs/ha).
