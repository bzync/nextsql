"""Thread-safe connection pool for NextSQL Python driver.

    import nextsql

    cfg = nextsql.Config(address="127.0.0.1:7210", user="app", password="pw")
    pool = nextsql.create_pool(cfg, min_size=2, max_size=10)

    # Context manager checkout:
    with pool.connection() as conn:
        res = conn.exec("SELECT * FROM users")

    # Direct pool queries (auto-checkout and release):
    res = pool.exec("INSERT INTO logs VALUES ($1)", ["event"])

    pool.close()
"""

from __future__ import annotations

import contextlib
import dataclasses
import threading
import time
from typing import Any, Iterator

from .client import Config, Connection, Result, _txn_control
from .errors import NextSQLError


class _PooledItem:
    def __init__(self, conn: Connection) -> None:
        self.conn = conn
        self.last_used = time.monotonic()
        self.in_txn = False


class ConnectionPool:
    """Thread-safe connection pool for multi-threaded applications."""

    def __init__(
        self,
        cfg: Config,
        min_size: int = 1,
        max_size: int = 10,
        timeout: float = 30.0,
        max_idle_time: float = 300.0,
        statement_cache_size: int = 50,
    ) -> None:
        if min_size < 0:
            raise NextSQLError("invalid_argument", "min_size must be >= 0")
        if max_size < 1:
            raise NextSQLError("invalid_argument", "max_size must be >= 1")
        if max_size < min_size:
            raise NextSQLError("invalid_argument", "max_size must be >= min_size")
        if statement_cache_size < 0:
            raise NextSQLError("invalid_argument", "statement_cache_size must be >= 0")

        self._cfg = dataclasses.replace(cfg, statement_cache_size=statement_cache_size)
        self._min_size = min_size
        self._max_size = max_size
        self._timeout = timeout
        self._max_idle_time = max_idle_time
        self._statement_cache_size = statement_cache_size

        self._lock = threading.Lock()
        self._cond = threading.Condition(self._lock)
        self._idle: list[_PooledItem] = []
        self._in_use: dict[Connection, _PooledItem] = {}
        self._closed = False

        # Pre-warm min_size connections
        if min_size > 0:
            for _ in range(min_size):
                try:
                    conn = Connection.connect(self._cfg)
                    self._idle.append(_PooledItem(conn))
                except Exception:
                    self.close()
                    raise

    @property
    def size(self) -> int:
        with self._lock:
            return len(self._idle) + len(self._in_use)

    @property
    def idle_count(self) -> int:
        with self._lock:
            return len(self._idle)

    @property
    def in_use_count(self) -> int:
        with self._lock:
            return len(self._in_use)

    @property
    def is_closed(self) -> bool:
        with self._lock:
            return self._closed

    def acquire(self, timeout: float | None = None) -> Connection:
        """Acquires a connection from the pool, creating one if within max_size,
        or waiting until an in-use connection is returned."""
        wait_time = self._timeout if timeout is None else timeout
        deadline = time.monotonic() + wait_time if wait_time > 0 else float("inf")

        with self._cond:
            while True:
                if self._closed:
                    raise NextSQLError("unavailable", "connection pool is closed")

                # Try to take from idle list
                now = time.monotonic()
                while self._idle:
                    item = self._idle.pop()
                    # Check idle expiry
                    if self._max_idle_time > 0 and (now - item.last_used) > self._max_idle_time:
                        self._safe_close(item.conn)
                        continue
                    # Check if connection is alive
                    if not self._is_alive(item.conn):
                        self._safe_close(item.conn)
                        continue
                    self._in_use[item.conn] = item
                    return item.conn

                # Can we create a new connection?
                if (len(self._idle) + len(self._in_use)) < self._max_size:
                    try:
                        conn = Connection.connect(self._cfg)
                    except Exception:
                        raise
                    item = _PooledItem(conn)
                    self._in_use[conn] = item
                    return conn

                # Pool is exhausted, wait for someone to release
                remaining = deadline - time.monotonic()
                if remaining <= 0:
                    raise NextSQLError("resource_exhausted", "connection pool exhausted, wait timeout exceeded")

                self._cond.wait(timeout=min(remaining, 0.5))

    def release(self, conn: Connection) -> None:
        """Returns a connection to the pool. If a transaction was left uncommitted,
        it is rolled back to prevent state leakage to subsequent checkouts."""
        with self._cond:
            item = self._in_use.pop(conn, None)
            if item is None:
                # Connection was not checked out from this pool or already closed
                return

            if self._closed:
                self._safe_close(conn)
                self._cond.notify()
                return

            # Reset connection state (rollback if transaction was left open)
            if not self._clean_connection(item):
                self._safe_close(conn)
                self._cond.notify()
                return

            item.last_used = time.monotonic()
            self._idle.append(item)
            self._cond.notify()

    @contextlib.contextmanager
    def connection(self, timeout: float | None = None) -> Iterator[Connection]:
        """Context manager for acquiring and safely releasing a connection."""
        conn = self.acquire(timeout=timeout)
        try:
            yield conn
        finally:
            self.release(conn)

    def exec(self, sql: str, params: list[Any] | None = None) -> Result:
        """Executes a statement using a pooled connection and returns the result."""
        with self.connection() as conn:
            return conn.exec(sql, params)

    def query(self, sql: str, params: list[Any] | None = None) -> Result:
        """Queries a statement using a pooled connection, buffering all rows into
        a Result before immediately returning the connection to the pool."""
        with self.connection() as conn:
            rows = conn.query(sql, params)
            return rows.collect()

    def close(self) -> None:
        """Closes all idle and active connections in the pool."""
        with self._cond:
            if self._closed:
                return
            self._closed = True

            for item in self._idle:
                self._safe_close(item.conn)
            self._idle.clear()

            for conn in list(self._in_use.keys()):
                self._safe_close(conn)
            self._in_use.clear()

            self._cond.notify_all()

    def __enter__(self) -> "ConnectionPool":
        return self

    def __exit__(self, *exc: object) -> None:
        self.close()

    def _safe_close(self, conn: Connection) -> None:
        try:
            conn.close()
        except Exception:
            pass

    def _is_alive(self, conn: Connection) -> bool:
        if hasattr(conn, "_sock") and getattr(conn, "_sock") is None:
            return False
        return True

    def _clean_connection(self, item: _PooledItem) -> bool:
        """Ensures the connection is in a clean state before returning to idle pool.
        Attempts ROLLBACK if transaction state was altered."""
        try:
            # We issue a ROLLBACK to clear any pending transaction or savepoints
            item.conn.exec("ROLLBACK")
            return True
        except Exception:
            # If ROLLBACK fails, the connection might be broken or disconnected
            return False


def create_pool(
    cfg: Config,
    min_size: int = 1,
    max_size: int = 10,
    timeout: float = 30.0,
    max_idle_time: float = 300.0,
    statement_cache_size: int = 50,
) -> ConnectionPool:
    """Creates a new thread-safe ConnectionPool."""
    return ConnectionPool(
        cfg=cfg,
        min_size=min_size,
        max_size=max_size,
        timeout=timeout,
        max_idle_time=max_idle_time,
        statement_cache_size=statement_cache_size,
    )
