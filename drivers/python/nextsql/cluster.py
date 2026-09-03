"""Cluster is a routing client over every node of a NextSQL HA cluster.

With `Config.read_consistency` set to READ_BOUNDED or READ_STALE it sends
eligible read-only statements to a healthy follower (round-robin, falling
back to the leader) and everything else — writes, DDL, transaction control,
and STRONG reads — to the leader. With the default STRONG consistency every
statement goes to the leader and Cluster is just a leader-failover wrapper.

A Cluster is safe for sequential use from one thread. Like Connection, an
open Rows pins its connection until closed.
"""

from __future__ import annotations

import copy
import time
from dataclasses import dataclass, field
from typing import Any

from . import protocol as p
from .client import Config, Connection, Result, Rows, _txn_control, is_read_only_sql
from .errors import NextSQLError

_STATUS_TTL = 0.5  # seconds


@dataclass
class _ClusterConn:
    addr: str
    conn: Connection
    status: p.NodeStatus | None = None
    seen: float = 0.0


def _is_transport_failure(err: NextSQLError) -> bool:
    """A broken connection (dial/read/write failure), not an application-
    level rejection the server sent back deliberately — see
    drivers/go/cluster.go isTransportFailure for the full reasoning this
    mirrors. Server-sent errors always decode with the server's own error
    code (via Connection._unexpected), never "io", so this cannot
    misclassify a legitimate query rejection as a dead connection."""
    return err.error_code == "io"


class Cluster:
    def __init__(self) -> None:
        self._conns: list[_ClusterConn] = []
        self._rr = 0
        self._in_txn = False
        self._read_consistency = p.READ_STRONG

    @classmethod
    def connect(cls, cfg: Config) -> "Cluster":
        addrs = list(cfg.nodes) if cfg.nodes else ([cfg.address] if cfg.address else [])
        if not addrs:
            raise NextSQLError("invalid_argument", "at least one node address is required")

        self = cls()
        self._read_consistency = cfg.read_consistency
        first_err: Exception | None = None
        for addr in addrs:
            nc = copy.copy(cfg)
            nc.address = addr
            nc.nodes = []
            try:
                self._conns.append(_ClusterConn(addr=addr, conn=Connection.connect(nc)))
            except Exception as e:  # noqa: BLE001 - collect and re-raise if none succeed
                if first_err is None:
                    first_err = e
        if not self._conns:
            raise first_err or NextSQLError("unavailable", "no reachable node")
        return self

    def close(self) -> None:
        for cc in self._conns:
            cc.conn.close()

    def nodes(self) -> list[p.NodeStatus]:
        self._refresh()
        return [cc.status for cc in self._conns if cc.status is not None]

    def exec(self, sql: str, params: list[Any] | None = None) -> Result:
        return self.query(sql, params).collect()

    def query(self, sql: str, params: list[Any] | None = None) -> Rows:
        begin, end = _txn_control(sql)
        routable = (
            not self._in_txn
            and not begin
            and not end
            and self._read_consistency != p.READ_STRONG
            and is_read_only_sql(sql)
        )

        if routable:
            fc = self._follower_cluster_conn()
            if fc is not None:
                try:
                    return fc.conn.query(sql, params)
                except NextSQLError as e:
                    if _is_transport_failure(e):
                        fc.status = None
                        fc.seen = 0.0
                    elif e.error_code != "unavailable":
                        raise
                    # The follower lost the leader, fell outside the bound, or
                    # its connection just broke; the leader can always
                    # answer, so fall through.

        leader_cc = self._leader_cluster_conn()
        try:
            rows = leader_cc.conn.query(sql, params)
        except NextSQLError as e:
            if _is_transport_failure(e):
                # The connection we cached as "the leader" just broke —
                # most commonly because that node lost leadership and was
                # then drained or restarted for planned maintenance before
                # the status cache caught up. Stop trusting that cached
                # role (next _refresh re-probes) and surface "unavailable"
                # instead of the raw transport error, so a caller already
                # retrying on it (the standard way to survive a genuine
                # leader failover) transparently survives this case too.
                leader_cc.status = None
                leader_cc.seen = 0.0
                raise NextSQLError("unavailable", f"leader connection failed: {e}") from e
            raise
        if begin or end:
            self._in_txn = begin
        return rows

    def _refresh(self) -> None:
        now = time.monotonic()
        for cc in self._conns:
            if now - cc.seen < _STATUS_TTL:
                continue
            try:
                cc.status = cc.conn.node_status()
                cc.seen = time.monotonic()
            except NextSQLError as e:
                if _is_transport_failure(e):
                    # The underlying Connection does not reconnect on its
                    # own, so a transport failure here is permanent for the
                    # lifetime of this Cluster: stop trusting whatever role
                    # it last reported (most dangerously "leader", which
                    # would otherwise keep winning _leader_conn's selection
                    # forever and starve every other node) rather than
                    # leaving stale data in place. It stays a refresh
                    # target so a future probe still gets attempted, at the
                    # normal TTL cadence.
                    cc.status = None
                    cc.seen = time.monotonic()
                # else: keep the last known status (e.g. a timeout worth
                # retrying sooner than the next natural TTL elapse).

    def _leader_cluster_conn(self) -> _ClusterConn:
        self._refresh()
        for cc in self._conns:
            role = cc.status.role if cc.status else None
            if role in ("leader", "standalone"):
                return cc
        raise NextSQLError("unavailable", "no reachable leader")

    def _follower_cluster_conn(self) -> _ClusterConn | None:
        self._refresh()
        followers: list[_ClusterConn] = []
        others: list[_ClusterConn] = []
        for cc in self._conns:
            if cc.status is None or not cc.status.healthy:
                continue
            if cc.status.role == "follower":
                followers.append(cc)
            elif cc.status.role in ("leader", "standalone"):
                others.append(cc)
        pick = followers or others
        if not pick:
            return None
        cc = pick[self._rr % len(pick)]
        self._rr += 1
        return cc
