"""Official NextSQL Python driver. Speaks the native NSQL v1 protocol.

Encryption keys and passwords are never accepted in a URL.
"""

from __future__ import annotations

import re
import socket
import ssl
from dataclasses import dataclass, field
from typing import Any, Iterator

from . import protocol as p
from .errors import NextSQLError

_DEFAULT_TIMEOUT = 60.0
_CONNECT_TIMEOUT = 10.0

_LOOPBACK_RE = re.compile(r"^127\.\d{1,3}\.\d{1,3}\.\d{1,3}$")


@dataclass
class TLSConfig:
    """TLS options for a remote connection. `ca` is PEM bytes/str; omit both
    `ca` and `cafile` to use the system trust store. Set
    `reject_unauthorized=False` only for local testing against a
    self-signed certificate — never in production."""

    ca: bytes | str | None = None
    cafile: str | None = None
    server_name: str | None = None
    reject_unauthorized: bool = True
    client_cert: str | None = None
    client_key: str | None = None


@dataclass
class Config:
    address: str = ""
    nodes: list[str] = field(default_factory=list)
    database: str = ""
    # Selects which hosted realm this connection targets (M2-2). Optional:
    # an unset realm sends the exact pre-realm Hello and connects to the
    # server's configured default.
    realm: str = ""
    user: str = ""
    password: str = ""
    key: bytes | None = None
    key_version: int = 1
    tls: TLSConfig | None = None
    insecure_no_tls: bool = False
    read_consistency: int = p.READ_STRONG
    max_staleness_ms: int = 0
    timeout: float = _DEFAULT_TIMEOUT


def _split_host_port(addr: str, allow_bare: bool = False) -> tuple[str, int]:
    if addr.startswith("["):
        end = addr.find("]")
        if end < 0:
            raise NextSQLError("invalid_argument", "invalid address")
        host = addr[1:end]
        rest = addr[end + 1 :]
        if rest.startswith(":"):
            return host, int(rest[1:])
        if allow_bare:
            return host, 0
        raise NextSQLError("invalid_argument", "address requires a port")
    i = addr.rfind(":")
    if i < 0:
        if allow_bare:
            return addr, 0
        raise NextSQLError("invalid_argument", "address requires a port")
    return addr[:i], int(addr[i + 1 :])


def is_loopback(addr: str) -> bool:
    host, _ = _split_host_port(addr, allow_bare=True)
    host = host.strip().lower()
    if host == "localhost":
        return True
    if host in ("::1", "0:0:0:0:0:0:0:1"):
        return True
    return bool(_LOOPBACK_RE.match(host))


def _validate_config(cfg: Config) -> None:
    if not cfg.address:
        raise NextSQLError("invalid_argument", "address is required")
    addr = cfg.address.lower()
    if "://" in addr or "key=" in addr or "password=" in addr:
        raise NextSQLError("invalid_argument", "keys and credentials must not be passed in a URL")
    if cfg.tls is None and not cfg.insecure_no_tls:
        raise NextSQLError("invalid_argument", "TLS is required for remote connections")
    if cfg.insecure_no_tls and not is_loopback(cfg.address):
        raise NextSQLError("invalid_argument", "plaintext is only allowed on loopback")
    if not cfg.user:
        raise NextSQLError("invalid_argument", "user is required")


def _dial(cfg: Config) -> socket.socket:
    host, port = _split_host_port(cfg.address)
    try:
        raw = socket.create_connection((host, port), timeout=_CONNECT_TIMEOUT)
    except OSError as e:
        raise NextSQLError("io", str(e)) from e
    raw.settimeout(cfg.timeout)
    if cfg.tls is not None:
        ctx = ssl.SSLContext(ssl.PROTOCOL_TLS_CLIENT)
        ctx.minimum_version = ssl.TLSVersion.TLSv1_3
        tls = cfg.tls
        if not tls.reject_unauthorized:
            ctx.check_hostname = False
            ctx.verify_mode = ssl.CERT_NONE
        if tls.ca is not None:
            ca_text = tls.ca.decode("utf-8") if isinstance(tls.ca, bytes) else tls.ca
            ctx.load_verify_locations(cadata=ca_text)
        elif tls.cafile is not None:
            ctx.load_verify_locations(cafile=tls.cafile)
        if tls.client_cert is not None:
            ctx.load_cert_chain(tls.client_cert, tls.client_key)
        try:
            return ctx.wrap_socket(raw, server_hostname=tls.server_name or host)
        except ssl.SSLError as e:
            raw.close()
            raise NextSQLError("protocol", f"tls handshake: {e}") from e
    return raw


class Rows:
    """A streaming query result. Iterate directly, or call `.collect()` for
    a materialized `Result`."""

    def __init__(self, conn: "Connection", columns: list[p.Column]) -> None:
        self._conn = conn
        self.columns = [c.name for c in columns]
        self.affected = 0
        self._batch: list[list[Any]] = []
        self._i = -1
        self._done = not columns
        self._closed = False
        self._err: NextSQLError | None = None

    def next(self) -> bool:
        if self._closed or self._err is not None:
            return False
        if self._i + 1 < len(self._batch):
            self._i += 1
            return True
        if self._done:
            return False
        try:
            self._fill()
        except NextSQLError as e:
            self._err = e
            return False
        if self._i + 1 < len(self._batch):
            self._i += 1
            return True
        return False

    def values(self) -> list[Any] | None:
        if self._i < 0 or self._i >= len(self._batch):
            return None
        return self._batch[self._i]

    def err(self) -> NextSQLError | None:
        return self._err

    def __iter__(self) -> Iterator[list[Any]]:
        try:
            while self.next():
                row = self.values()
                if row is not None:
                    yield row
            if self._err is not None:
                raise self._err
        finally:
            if not self._closed:
                self.close()

    def close(self) -> None:
        while self.next():
            pass
        if not self._closed:
            self._finish()
        if self._err is not None:
            err, self._err = self._err, None
            raise err

    def collect(self) -> "Result":
        out: list[list[Any]] = []
        try:
            while self.next():
                row = self.values()
                if row is not None:
                    out.append(row)
            if self._err is not None:
                raise self._err
        finally:
            if not self._closed:
                self.close()
        return Result(columns=self.columns, rows=out, affected=self.affected)

    def _mark_closed(self) -> None:
        self._closed = True
        self._done = True

    def _fill(self) -> None:
        if not self._done and self._batch:
            self._conn._write_frame(p.TYPE_FLOW_ACK, b"")
        typ, payload = self._conn._read_frame()
        if typ == p.TYPE_DATA_BATCH:
            self._batch = p.decode_data_batch(payload)
            self._i = -1
            return
        if typ == p.TYPE_COMMAND_COMPLETE:
            self.affected = p.decode_command_complete(payload)
            self._done = True
            self._batch = []
            self._i = -1
            self._conn._expect_ready()
            self._finish()
            return
        raise self._conn._unexpected(typ, payload)

    def _finish(self) -> None:
        if not self._closed:
            self._conn._busy = False
        self._closed = True


@dataclass
class Result:
    columns: list[str]
    rows: list[list[Any]]
    affected: int


class Statement:
    """A prepared statement. Close it when done, or use as a context manager."""

    def __init__(self, conn: "Connection", stmt_id: int) -> None:
        self._conn = conn
        self._id = stmt_id

    def query(self, params: list[Any] | None = None) -> Rows:
        return self._conn.execute_prepared(self._id, params or [])

    def exec(self, params: list[Any] | None = None) -> Result:
        return self.query(params).collect()

    def close(self) -> None:
        if self._id == 0:
            return
        self._conn.close_statement(self._id)
        self._id = 0

    def __enter__(self) -> "Statement":
        return self

    def __exit__(self, *exc: object) -> None:
        self.close()


def _txn_control(sql: str) -> tuple[bool, bool]:
    up = sql.lstrip(" \t\r\n(").upper()
    begin = up.startswith("BEGIN") or up.startswith("START TRANSACTION")
    end = up.startswith("COMMIT") or up.startswith("ROLLBACK")
    return begin, end


def is_read_only_sql(sql: str) -> bool:
    """Conservative check: a false negative only costs a leader round trip,
    and a false positive self-corrects on the leader. EXPLAIN is excluded
    because EXPLAIN ANALYZE executes its statement."""
    s = sql.lstrip(" \t\r\n(")
    while s.startswith("--"):
        i = s.find("\n")
        if i < 0:
            return False
        s = s[i + 1 :].lstrip(" \t\r\n(")
    up = s.upper()
    if up.startswith("SELECT") or up.startswith("SHOW"):
        return True
    if up.startswith("WITH"):
        return not any(kw in up for kw in ("INSERT", "UPDATE", "DELETE", "UPSERT"))
    return False


class Connection:
    """A single connection to one NextSQL node. Not safe for concurrent use
    from multiple threads — open one Connection per thread/worker, or use
    `Cluster` which pools one connection per node."""

    def __init__(self, cfg: Config) -> None:
        _validate_config(cfg)
        self._cfg = cfg
        self._sock = _dial(cfg)
        self._secret = b""
        self._busy = False
        try:
            self._handshake()
            if cfg.read_consistency != p.READ_STRONG:
                self.set_read_consistency(cfg.read_consistency, cfg.max_staleness_ms)
        except BaseException:
            self._sock.close()
            raise

    @classmethod
    def connect(cls, cfg: Config) -> "Connection":
        return cls(cfg)

    def __enter__(self) -> "Connection":
        return self

    def __exit__(self, *exc: object) -> None:
        self.close()

    # --- control ---

    def set_read_consistency(self, mode: int, max_staleness_ms: int = 0) -> None:
        if self._busy:
            raise NextSQLError("conflict", "connection is busy")
        self._write_frame(p.TYPE_SET_READ_CONSISTENCY, p.encode_set_read_consistency(mode, max_staleness_ms))
        self._read_ack()

    def node_status(self) -> p.NodeStatus:
        if self._busy:
            raise NextSQLError("conflict", "connection is busy")
        self._write_frame(p.TYPE_NODE_STATUS, b"")
        typ, payload = self._read_frame()
        if typ != p.TYPE_NODE_STATUS_RESP:
            raise self._unexpected(typ, payload)
        st = p.decode_node_status(payload)
        self._expect_ready()
        return st

    # --- query execution ---

    def exec(self, sql: str, params: list[Any] | None = None) -> Result:
        return self.query(sql, params).collect()

    def query(self, sql: str, params: list[Any] | None = None) -> Rows:
        if self._sock is None:
            raise NextSQLError("unavailable", "connection closed")
        if self._busy:
            raise NextSQLError("conflict", "connection is busy")
        self._busy = True
        try:
            self._write_frame(p.TYPE_QUERY, p.encode_query(sql, params or []))
            return self._read_rows()
        except BaseException:
            self._busy = False
            raise

    def exec_idempotent(self, key: str, sql: str, params: list[Any] | None = None) -> Result:
        """Executes a retryable mutation under a durable idempotency key: a
        retried call with the same key replays the original result instead
        of re-executing. See docs/sql.md / docs/protocol.md."""
        return self.query_idempotent(key, sql, params).collect()

    def query_idempotent(self, key: str, sql: str, params: list[Any] | None = None) -> Rows:
        if self._busy:
            raise NextSQLError("conflict", "connection is busy")
        self._busy = True
        try:
            self._write_frame(p.TYPE_IDEMPOTENT_QUERY, p.encode_idempotent_query(key, sql, params or []))
            return self._read_rows()
        except BaseException:
            self._busy = False
            raise

    def prepare(self, sql: str) -> Statement:
        if self._busy:
            raise NextSQLError("conflict", "connection is busy")
        self._write_frame(p.TYPE_PREPARE, p.u32bytes(sql.encode("utf-8"), p.MAX_SQL))
        typ, payload = self._read_frame()
        if typ != p.TYPE_PREPARE_OK:
            raise self._unexpected(typ, payload)
        if len(payload) != 4:
            raise NextSQLError("protocol", "bad prepare-ok length")
        stmt_id = p.u32(payload, 0)
        self._expect_ready()
        return Statement(self, stmt_id)

    def execute_prepared(self, stmt_id: int, params: list[Any]) -> Rows:
        if self._busy:
            raise NextSQLError("conflict", "connection is busy")
        self._busy = True
        try:
            self._write_frame(p.TYPE_EXECUTE, p.encode_execute(stmt_id, params))
            return self._read_rows()
        except BaseException:
            self._busy = False
            raise

    def close_statement(self, stmt_id: int) -> None:
        if self._busy:
            raise NextSQLError("conflict", "connection is busy")
        self._write_frame(p.TYPE_CLOSE_STMT, p.u32le(stmt_id))
        typ, payload = self._read_frame()
        if typ != p.TYPE_CLOSE_OK:
            raise self._unexpected(typ, payload)
        self._expect_ready()

    def cancel(self) -> None:
        """Cancels the statement currently running on this connection, from
        a second, independent connection carrying this connection's secret.
        Safe to call from another thread while `query`/`exec` blocks."""
        if not self._secret:
            raise NextSQLError("unavailable", "not connected")
        side = _dial(self._cfg)
        try:
            tmp = Connection.__new__(Connection)
            tmp._sock = side
            tmp._busy = False
            tmp._write_frame(
                p.TYPE_HELLO,
                p.encode_hello(p.VERSION, p.FLAG_CANCEL, self._secret, "", ""),
            )
            typ, payload = tmp._read_frame()
            if typ != p.TYPE_READY:
                raise self._unexpected(typ, payload)
        finally:
            side.close()

    def close(self) -> None:
        if self._sock is None:
            return
        try:
            self._write_frame(p.TYPE_TERMINATE, b"")
        except NextSQLError:
            pass
        self._sock.close()
        self._sock = None

    # --- wire plumbing ---

    def _handshake(self) -> None:
        cfg = self._cfg
        self._write_frame(
            p.TYPE_HELLO,
            p.encode_hello(p.VERSION, 0, b"\x00" * 8, cfg.database, cfg.user, cfg.realm),
        )
        typ, payload = self._read_frame()
        if typ != p.TYPE_HELLO_OK:
            raise self._unexpected(typ, payload)
        _version, auth_method, secret = p.decode_hello_ok(payload)
        self._secret = secret
        self._write_frame(p.TYPE_AUTH, p.u16str(cfg.password))
        typ, payload = self._read_frame()
        if typ != p.TYPE_AUTH_OK:
            raise self._unexpected(typ, payload)
        if auth_method == p.AUTH_PASSWORD_KEY:
            if not cfg.key or len(cfg.key) != 32:
                raise NextSQLError("unauthorized", "server requires a client-held key")
            mat = p.u32le(cfg.key_version) + cfg.key
            self._write_frame(p.TYPE_UNLOCK, mat)
            typ, payload = self._read_frame()
            if typ != p.TYPE_UNLOCK_OK:
                raise self._unexpected(typ, payload)
        typ, payload = self._read_frame()
        if typ != p.TYPE_READY:
            raise self._unexpected(typ, payload)

    def _read_rows(self) -> Rows:
        typ, payload = self._read_frame()
        if typ == p.TYPE_ROW_DESC:
            return Rows(self, p.decode_row_desc(payload))
        if typ == p.TYPE_COMMAND_COMPLETE:
            rows = Rows(self, [])
            rows.affected = p.decode_command_complete(payload)
            self._expect_ready()
            self._busy = False
            rows._mark_closed()
            return rows
        self._busy = False
        raise self._unexpected(typ, payload)

    def _read_ack(self) -> None:
        typ, payload = self._read_frame()
        if typ == p.TYPE_READY:
            return
        raise self._unexpected(typ, payload)

    def _expect_ready(self) -> None:
        typ, payload = self._read_frame()
        if typ != p.TYPE_READY:
            raise self._unexpected(typ, payload)

    def _unexpected(self, typ: int, payload: bytes) -> NextSQLError:
        """Decodes an out-of-band Error frame (or reports a genuine protocol
        violation) for a call site checking "did I get what I expected?".
        The server's writeErrReady always sends Error then Ready — every
        call site funnels through here specifically so that trailing Ready
        is always drained in one place, rather than each of
        query/prepare/close_statement/etc. having to remember to do it
        individually (a per-call-site version of this is exactly the shape
        of bug this centralizes away)."""
        if typ != p.TYPE_ERROR:
            return NextSQLError("protocol", "unexpected message type")
        err = p.decode_error(payload)
        try:
            self._expect_ready()
        except NextSQLError:
            # Best-effort: surface the original application error even if
            # draining the trailing Ready itself fails (e.g. the
            # connection is now genuinely broken).
            pass
        return err

    def _read_frame(self) -> tuple[int, bytes]:
        hdr = self._read_exact(12)
        if hdr[0:4] != b"NSQL":
            raise NextSQLError("protocol", "bad magic")
        if p.u16(hdr, 4) != p.VERSION:
            raise NextSQLError("protocol", "unsupported protocol version")
        typ = hdr[6]
        if typ == 0:
            raise NextSQLError("protocol", "invalid message type")
        n = p.u32(hdr, 8)
        if n > p.MAX_PACKET:
            raise NextSQLError("protocol", "packet exceeds limit")
        payload = self._read_exact(n) if n else b""
        return typ, payload

    def _write_frame(self, typ: int, payload: bytes) -> None:
        if len(payload) > p.MAX_PACKET:
            raise NextSQLError("protocol", "payload exceeds packet limit")
        hdr = b"NSQL" + p.u16le(p.VERSION) + bytes([typ, 0]) + p.u32le(len(payload))
        self._write_all(hdr + payload)

    def _read_exact(self, n: int) -> bytes:
        if n == 0:
            return b""
        chunks = []
        remaining = n
        while remaining > 0:
            try:
                chunk = self._sock.recv(remaining)
            except OSError as e:
                raise NextSQLError("io", str(e)) from e
            if not chunk:
                raise NextSQLError("unavailable", "connection closed")
            chunks.append(chunk)
            remaining -= len(chunk)
        return b"".join(chunks)

    def _write_all(self, data: bytes) -> None:
        try:
            self._sock.sendall(data)
        except OSError as e:
            raise NextSQLError("io", str(e)) from e
