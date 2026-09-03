"""Official NextSQL Python driver. Speaks the native NSQL v1 protocol.

    import nextsql

    conn = nextsql.connect(nextsql.Config(
        address="db.example.com:7210",
        database="production",
        user="app",
        password="s3cret",
        tls=nextsql.TLSConfig(),
    ))
    try:
        result = conn.exec("SELECT id, name FROM users WHERE id = $1", [1])
        for row in result.rows:
            print(row)
    finally:
        conn.close()

Not published as a package — import it from this tree directly
(``drivers/python`` on ``sys.path``), matching every other official driver.
"""

from .client import (
    Config,
    Connection,
    Result,
    Rows,
    Statement,
    TLSConfig,
    is_read_only_sql,
)
from .cluster import Cluster
from .errors import NextSQLError
from .protocol import (
    READ_BOUNDED,
    READ_STALE,
    READ_STRONG,
    Box,
    EnumValue,
    Float32,
    Float64,
    Int8,
    Int16,
    Int32,
    Int64,
    Interval,
    Line,
    NaiveTimestamp,
    NodeStatus,
    Point,
    Polygon,
    Uint8,
    Uint16,
    Uint32,
    Uint64,
    Vector,
)


def connect(cfg: Config) -> Connection:
    """Opens one connection to a single node. For an HA cluster with
    leader-failover and follower-read routing, use `connect_cluster`."""
    return Connection.connect(cfg)


def connect_cluster(cfg: Config) -> Cluster:
    """Opens a routing client over every node in `cfg.nodes` (or the single
    `cfg.address`). See `Cluster` for routing behavior."""
    return Cluster.connect(cfg)


__all__ = [
    "Box",
    "Cluster",
    "Config",
    "Connection",
    "EnumValue",
    "Float32",
    "Float64",
    "Int8",
    "Int16",
    "Int32",
    "Int64",
    "Interval",
    "Line",
    "NaiveTimestamp",
    "NextSQLError",
    "NodeStatus",
    "Point",
    "Polygon",
    "READ_BOUNDED",
    "READ_STALE",
    "READ_STRONG",
    "Result",
    "Rows",
    "Statement",
    "TLSConfig",
    "Uint8",
    "Uint16",
    "Uint32",
    "Uint64",
    "Vector",
    "connect",
    "connect_cluster",
    "is_read_only_sql",
]
