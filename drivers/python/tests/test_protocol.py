"""Unit tests for NSQL v1 wire encoding — no live server required.

Run with: python3 -m unittest discover -s drivers/python/tests -t drivers/python
"""

from __future__ import annotations

import datetime
import decimal
import sys
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent.parent))

from nextsql import client as c
from nextsql import protocol as p
from nextsql.errors import NextSQLError


class TestFraming(unittest.TestCase):
    def test_hello_round_trip(self) -> None:
        payload = p.encode_hello(1, 0, b"\x00" * 8, "production", "app")
        # version, flags, secret(8), then two u16-strings
        self.assertEqual(p.u16(payload, 0), 1)
        self.assertEqual(p.u16(payload, 2), 0)
        db, off = p.read_u16_string(payload, 12, p.MAX_NAME)
        self.assertEqual(db, "production")
        user, off = p.read_u16_string(payload, off, p.MAX_NAME)
        self.assertEqual(user, "app")

    def test_hello_realm_is_opt_in_trailing_field(self) -> None:
        no_realm = p.encode_hello(1, 0, b"\x00" * 8, "production", "app")
        default_realm = p.encode_hello(1, 0, b"\x00" * 8, "production", "app", "")
        self.assertEqual(no_realm, default_realm)
        with_realm = p.encode_hello(1, 0, b"\x00" * 8, "production", "app", "tenant-a")
        self.assertEqual(len(with_realm), len(no_realm) + 2 + len(b"tenant-a"))
        self.assertEqual(with_realm[: len(no_realm)], no_realm)
        realm, off = p.read_u16_string(with_realm, len(no_realm), p.MAX_NAME)
        self.assertEqual(realm, "tenant-a")
        self.assertEqual(off, len(with_realm))

    def test_hello_ok_round_trip(self) -> None:
        raw = p.u16le(1) + bytes([p.AUTH_PASSWORD]) + (b"S" * 8)
        version, auth_method, secret = p.decode_hello_ok(raw)
        self.assertEqual(version, 1)
        self.assertEqual(auth_method, p.AUTH_PASSWORD)
        self.assertEqual(secret, b"S" * 8)

    def test_hello_ok_rejects_bad_length(self) -> None:
        with self.assertRaises(NextSQLError):
            p.decode_hello_ok(b"short")

    def test_error_round_trip(self) -> None:
        raw = p.u16str("unavailable") + p.u16str("no reachable leader")
        err = p.decode_error(raw)
        self.assertEqual(err.error_code, "unavailable")
        self.assertEqual(str(err), "no reachable leader")


class TestDecimal(unittest.TestCase):
    def roundtrip(self, s: str) -> str:
        return str(p.decode_decimal(p.encode_decimal(s)[4:]))

    def test_positive(self) -> None:
        self.assertEqual(self.roundtrip("123.45"), "123.45")

    def test_negative(self) -> None:
        self.assertEqual(self.roundtrip("-0.001"), "-0.001")

    def test_zero(self) -> None:
        self.assertEqual(self.roundtrip("0"), "0")
        self.assertEqual(self.roundtrip("0.00"), "0.00")

    def test_large_integer(self) -> None:
        big = "9" * 40
        self.assertEqual(self.roundtrip(big), big)

    def test_leading_plus_and_zeros(self) -> None:
        self.assertEqual(self.roundtrip("+007.5"), "7.5")

    def test_rejects_invalid(self) -> None:
        with self.assertRaises(NextSQLError):
            p.encode_decimal("not-a-number")

    def test_param_encoding_accepts_decimal_and_int(self) -> None:
        encoded_int = p.encode_param(42)
        encoded_dec = p.encode_param(decimal.Decimal("42"))
        self.assertEqual(encoded_int[0], p.KIND_DECIMAL)
        self.assertEqual(encoded_dec[0], p.KIND_DECIMAL)
        value, _, _ = p.decode_value(encoded_int, 0)
        self.assertEqual(str(value), "42")


class TestValues(unittest.TestCase):
    def test_null_param(self) -> None:
        raw = p.encode_param(None)
        value, next_off, kind = p.decode_value(raw, 0)
        self.assertIsNone(value)
        self.assertEqual(next_off, 7)
        self.assertEqual(kind, p.KIND_STRING)

    def test_bool_round_trip(self) -> None:
        for b in (True, False):
            raw = p.encode_param(b)
            value, _, kind = p.decode_value(raw, 0)
            self.assertEqual(kind, p.KIND_BOOL)
            self.assertEqual(value, b)

    def test_string_round_trip(self) -> None:
        raw = p.encode_param("héllo wörld")
        value, next_off, kind = p.decode_value(raw, 0)
        self.assertEqual(value, "héllo wörld")
        self.assertEqual(next_off, len(raw))
        self.assertEqual(kind, p.KIND_STRING)

    def test_blob_round_trip(self) -> None:
        raw_bytes = bytes([0x00, 0xFF, 0xFE, 0x00, 0xDE, 0xAD, 0xBE, 0xEF])
        raw = p.encode_param(raw_bytes)
        value, next_off, kind = p.decode_value(raw, 0)
        self.assertEqual(kind, p.KIND_BLOB)
        self.assertEqual(value, raw_bytes)
        self.assertEqual(next_off, len(raw))

        # bytearray is accepted the same way as bytes.
        raw2 = p.encode_param(bytearray(raw_bytes))
        value2, _, kind2 = p.decode_value(raw2, 0)
        self.assertEqual(kind2, p.KIND_BLOB)
        self.assertEqual(value2, raw_bytes)

        empty_value, _, empty_kind = p.decode_value(p.encode_param(b""), 0)
        self.assertEqual(empty_kind, p.KIND_BLOB)
        self.assertEqual(empty_value, b"")

    def test_int_round_trip(self) -> None:
        cases = [
            (p.Int8(-128), p.KIND_INT8),
            (p.Int8(127), p.KIND_INT8),
            (p.Int16(-32768), p.KIND_INT16),
            (p.Int16(32767), p.KIND_INT16),
            (p.Int32(-2147483648), p.KIND_INT32),
            (p.Int32(2147483647), p.KIND_INT32),
            (p.Int64(-9223372036854775808), p.KIND_INT64),
            (p.Int64(9223372036854775807), p.KIND_INT64),
        ]
        for wrapped, want_kind in cases:
            raw = p.encode_param(wrapped)
            value, next_off, kind = p.decode_value(raw, 0)
            self.assertEqual(kind, want_kind)
            self.assertEqual(value, wrapped.value)
            self.assertEqual(next_off, len(raw))
        with self.assertRaises(Exception):
            p.encode_param(p.Int8(128))
        with self.assertRaises(Exception):
            p.encode_param(p.Int8(-129))
        # A bare int still defaults to KIND_DECIMAL (server coerces per column).
        _, _, bare_kind = p.decode_value(p.encode_param(42), 0)
        self.assertEqual(bare_kind, p.KIND_DECIMAL)

    def test_uint_round_trip(self) -> None:
        cases = [
            (p.Uint8(0), p.KIND_UINT8),
            (p.Uint8(255), p.KIND_UINT8),
            (p.Uint16(0), p.KIND_UINT16),
            (p.Uint16(65535), p.KIND_UINT16),
            (p.Uint32(0), p.KIND_UINT32),
            (p.Uint32(4294967295), p.KIND_UINT32),
            (p.Uint64(0), p.KIND_UINT64),
            (p.Uint64(18446744073709551615), p.KIND_UINT64),
        ]
        for wrapped, want_kind in cases:
            raw = p.encode_param(wrapped)
            value, next_off, kind = p.decode_value(raw, 0)
            self.assertEqual(kind, want_kind)
            self.assertEqual(value, wrapped.value)
            self.assertEqual(next_off, len(raw))
        with self.assertRaises(Exception):
            p.encode_param(p.Uint8(256))
        with self.assertRaises(Exception):
            p.encode_param(p.Uint8(-1))
        # A bare int still defaults to KIND_DECIMAL (server coerces per column).
        _, _, bare_kind = p.decode_value(p.encode_param(42), 0)
        self.assertEqual(bare_kind, p.KIND_DECIMAL)

    def test_enum_round_trip(self) -> None:
        labels = ["small", "medium", "large"]
        raw = p.encode_param(p.EnumValue("medium", labels))
        value, next_off, kind = p.decode_value(raw, 0)
        self.assertEqual(kind, p.KIND_ENUM)
        self.assertEqual(value, "medium")
        self.assertEqual(next_off, len(raw))
        with self.assertRaises(Exception):
            p.encode_param(p.EnumValue("huge", labels))
        # decodeRowDesc parses the same label-list framing for a column
        # header (kind byte, 5 bytes of Precision/Scale/VecElem meta, then
        # the ENUM label-count u16 + each u16-length-prefixed label).
        row_desc = (
            p.u16le(1)
            + p.u16str("sz")
            + bytes([p.KIND_ENUM])
            + b"\x00" * 5
            + p.append_enum_labels(labels)
        )
        cols = p.decode_row_desc(row_desc)
        self.assertEqual(len(cols), 1)
        self.assertEqual(cols[0].name, "sz")
        self.assertEqual(cols[0].kind, p.KIND_ENUM)
        self.assertEqual(cols[0].labels, labels)

    def test_date_round_trip(self) -> None:
        d = datetime.date(2024, 1, 15)
        raw = p.encode_param(d)
        value, next_off, kind = p.decode_value(raw, 0)
        self.assertEqual(kind, p.KIND_DATE)
        self.assertEqual(value, d)
        self.assertEqual(next_off, len(raw))
        # Pre-1970 dates round-trip too (signed day count).
        pre = datetime.date(1900, 1, 1)
        self.assertEqual(p.decode_value(p.encode_param(pre), 0)[0], pre)
        # datetime.datetime (a date subclass) must not be mistaken for a
        # bare date — it defaults to TIMESTAMPTZ (existing behavior).
        self.assertEqual(p.decode_value(p.encode_param(datetime.datetime(2024, 1, 15)), 0)[2], p.KIND_TIMESTAMPTZ)

    def test_time_round_trip(self) -> None:
        t = datetime.time(23, 59, 59, 999_000)
        raw = p.encode_param(t)
        value, next_off, kind = p.decode_value(raw, 0)
        self.assertEqual(kind, p.KIND_TIME)
        self.assertEqual(value, t)
        self.assertEqual(next_off, len(raw))

    def test_naive_timestamp_round_trip(self) -> None:
        dt = datetime.datetime(2024, 6, 15, 10, 30, 0)
        raw = p.encode_param(p.NaiveTimestamp(dt))
        value, next_off, kind = p.decode_value(raw, 0)
        self.assertEqual(kind, p.KIND_TIMESTAMP)
        self.assertEqual(value, dt)
        self.assertIsNone(value.tzinfo)
        self.assertEqual(next_off, len(raw))
        # A bare naive datetime still defaults to TIMESTAMPTZ (existing
        # behavior; NaiveTimestamp is required to select the naive Kind).
        self.assertEqual(p.decode_value(p.encode_param(dt), 0)[2], p.KIND_TIMESTAMPTZ)

    def test_float_round_trip(self) -> None:
        for wrapped, want_kind in [(p.Float32(1.5), p.KIND_FLOAT32), (p.Float64(1.5), p.KIND_FLOAT64)]:
            raw = p.encode_param(wrapped)
            value, next_off, kind = p.decode_value(raw, 0)
            self.assertEqual(kind, want_kind)
            self.assertEqual(value, 1.5)
            self.assertEqual(next_off, len(raw))
        # NaN/Infinity are valid FLOAT values (unlike the bare-float -> Decimal path).
        nan_value, _, _ = p.decode_value(p.encode_param(p.Float64(float("nan"))), 0)
        self.assertTrue(nan_value != nan_value)  # NaN != NaN
        inf_value, _, _ = p.decode_value(p.encode_param(p.Float64(float("inf"))), 0)
        self.assertEqual(inf_value, float("inf"))
        with self.assertRaises(Exception):
            p.encode_param(float("nan"))  # bare float still requires finite

    def test_interval_round_trip(self) -> None:
        iv = p.Interval(months=14, days=3, nanos=4 * 3_600_000_000_000)
        raw = p.encode_param(iv)
        value, next_off, kind = p.decode_value(raw, 0)
        self.assertEqual(kind, p.KIND_INTERVAL)
        self.assertEqual(value, iv)
        self.assertEqual(next_off, len(raw))
        # Negative nanos (e.g. "-1 hour") must round-trip exactly.
        neg = p.Interval(months=0, days=0, nanos=-3_600_000_000_000)
        neg_value, _, _ = p.decode_value(p.encode_param(neg), 0)
        self.assertEqual(neg_value.nanos, -3_600_000_000_000)

    def test_uuid_round_trip(self) -> None:
        import uuid

        u = uuid.uuid4()
        raw = p.encode_param(u)
        value, _, kind = p.decode_value(raw, 0)
        self.assertEqual(kind, p.KIND_UUID)
        self.assertEqual(value, u)

    def test_timestamptz_round_trip(self) -> None:
        dt = datetime.datetime(2026, 9, 2, 12, 34, 56, 789000, tzinfo=datetime.timezone.utc)
        raw = p.encode_param(dt)
        value, _, kind = p.decode_value(raw, 0)
        self.assertEqual(kind, p.KIND_TIMESTAMPTZ)
        self.assertEqual(value, dt)

    def test_dense_vector_round_trip(self) -> None:
        vec = [1.5, -2.25, 3.0]
        raw = p.encode_param(vec)
        value, next_off, kind = p.decode_value(raw, 0)
        self.assertEqual(kind, p.KIND_VECTOR)
        self.assertEqual(next_off, len(raw))
        self.assertIsInstance(value, p.Vector)
        self.assertEqual(value.dim, 3)
        for got, want in zip(value.values, vec):
            self.assertAlmostEqual(got, want, places=5)

    def test_sparse_vector_round_trip(self) -> None:
        vec = p.Vector(dim=100, indices=[3, 50], values=[1.0, -2.0])
        raw = p.encode_param(vec)
        value, next_off, kind = p.decode_value(raw, 0)
        self.assertEqual(kind, p.KIND_VECTOR)
        self.assertEqual(next_off, len(raw))
        self.assertEqual(value.dim, 100)
        self.assertEqual(value.indices, [3, 50])
        for got, want in zip(value.values, [1.0, -2.0]):
            self.assertAlmostEqual(got, want, places=5)

    def test_point_round_trip(self) -> None:
        pt = p.Point(lon=-122.4, lat=37.8)
        raw = p.encode_param(pt)
        value, next_off, kind = p.decode_value(raw, 0)
        self.assertEqual(kind, p.KIND_POINT)
        self.assertEqual(next_off, len(raw))
        self.assertAlmostEqual(value.lon, pt.lon)
        self.assertAlmostEqual(value.lat, pt.lat)

    def test_json_param_and_nsjb_decode(self) -> None:
        # Round trip through the server's NSJB binary form is exercised live
        # (tests/live_test.py); here we only test the decoder against a
        # hand-built document matching internal/protocol's encoder tags.
        doc = bytearray(b"NSJB\x01")
        doc += b"\x07"  # object
        body = bytearray()
        body += p.u16le(1)  # 1 key
        body += p.u16le(len(b"a")) + b"a"
        body += b"\x03" + (5).to_bytes(8, "little", signed=True)  # int64 5
        doc += p.u32le(len(body)) + body
        value = p.decode_nsjb(bytes(doc))
        self.assertEqual(value, {"a": 5})


class TestRowDesc(unittest.TestCase):
    def test_decode_row_desc(self) -> None:
        raw = bytearray(p.u16le(2))
        raw += p.u16str("id") + bytes([p.KIND_UUID, 0, 0, 0, 0, 0])
        raw += p.u16str("name") + bytes([p.KIND_STRING, 0, 0, 0, 0, 0])
        cols = p.decode_row_desc(bytes(raw))
        self.assertEqual([c.name for c in cols], ["id", "name"])
        self.assertEqual(cols[0].kind, p.KIND_UUID)
        self.assertEqual(cols[1].kind, p.KIND_STRING)


class TestConfigValidation(unittest.TestCase):
    def test_requires_address(self) -> None:
        with self.assertRaises(NextSQLError):
            c._validate_config(c.Config(user="app", insecure_no_tls=True))

    def test_requires_tls_off_loopback(self) -> None:
        with self.assertRaises(NextSQLError):
            c._validate_config(c.Config(address="db.example.com:7210", user="app"))

    def test_insecure_requires_loopback(self) -> None:
        with self.assertRaises(NextSQLError):
            c._validate_config(c.Config(address="db.example.com:7210", user="app", insecure_no_tls=True))

    def test_loopback_insecure_ok(self) -> None:
        c._validate_config(c.Config(address="127.0.0.1:7210", user="app", insecure_no_tls=True))

    def test_rejects_url_shaped_address(self) -> None:
        with self.assertRaises(NextSQLError):
            c._validate_config(c.Config(address="tcp://127.0.0.1:7210", user="app", insecure_no_tls=True))

    def test_rejects_credentials_in_address(self) -> None:
        with self.assertRaises(NextSQLError):
            c._validate_config(c.Config(address="127.0.0.1:7210?password=x", user="app", insecure_no_tls=True))


class _FakeSocket:
    """A minimal stand-in for a socket: readable from a preloaded buffer,
    writes are ignored. Used to prove Connection._unexpected drains a
    trailing Ready frame after an Error frame without a live server."""

    def __init__(self, data: bytes) -> None:
        self._buf = data

    def recv(self, n: int) -> bytes:
        chunk, self._buf = self._buf[:n], self._buf[n:]
        return chunk

    def sendall(self, data: bytes) -> None:
        pass


def _frame(typ: int, payload: bytes) -> bytes:
    return b"NSQL" + p.u16le(p.VERSION) + bytes([typ, 0]) + p.u32le(len(payload)) + payload


class TestErrorReadyDraining(unittest.TestCase):
    """Regression coverage for a real bug found in this session: several
    existing official drivers (PHP, Node, and — via shared code — Bun and
    Deno) never drained the trailing Ready frame the server sends after an
    Error frame outside of a couple of call sites, leaving the connection
    permanently desynced (every subsequent call sees the stale Ready and
    fails with a spurious "unexpected message type") the first time any
    query/prepare/close_statement call hit a server-side error. Fixed here,
    and centralized in Connection._unexpected so no future call site can
    reintroduce it by forgetting to drain."""

    def _conn_on(self, wire: bytes) -> c.Connection:
        conn = c.Connection.__new__(c.Connection)
        conn._sock = _FakeSocket(wire)
        conn._busy = False
        return conn

    def test_unexpected_drains_trailing_ready(self) -> None:
        error_payload = p.u16str("not_found") + p.u16str("unknown table")
        wire = _frame(p.TYPE_READY, b"")  # the trailing Ready to drain
        conn = self._conn_on(wire)
        err = conn._unexpected(p.TYPE_ERROR, error_payload)
        self.assertEqual(err.error_code, "not_found")
        self.assertEqual(conn._sock._buf, b"", "the trailing Ready must be fully consumed")

    def test_connection_usable_after_query_error(self) -> None:
        # Simulates exactly the sequence that desynced the buggy drivers: a
        # failed query (Error+Ready) followed by a second, successful DML
        # query (CommandComplete+Ready) on the same wire. A driver that
        # forgets to drain the first Ready would misparse it as the second
        # query's own response.
        error_payload = p.u16str("not_found") + p.u16str("unknown table")
        second_query_wire = _frame(p.TYPE_COMMAND_COMPLETE, (3).to_bytes(8, "little")) + _frame(p.TYPE_READY, b"")
        wire = _frame(p.TYPE_ERROR, error_payload) + _frame(p.TYPE_READY, b"") + second_query_wire
        conn = self._conn_on(wire)
        with self.assertRaises(NextSQLError):
            conn._read_rows()
        self.assertFalse(conn._busy, "a failed query must release busy")
        rows = conn._read_rows()
        self.assertEqual(rows.affected, 3)


class TestHelpers(unittest.TestCase):
    def test_is_read_only_sql(self) -> None:
        self.assertTrue(c.is_read_only_sql("SELECT 1"))
        self.assertTrue(c.is_read_only_sql("  select * from t"))
        self.assertTrue(c.is_read_only_sql("SHOW TABLES"))
        self.assertFalse(c.is_read_only_sql("INSERT INTO t VALUES (1)"))
        self.assertFalse(c.is_read_only_sql("EXPLAIN ANALYZE SELECT 1"))
        self.assertTrue(c.is_read_only_sql("WITH x AS (SELECT 1) SELECT * FROM x"))
        self.assertFalse(c.is_read_only_sql("WITH x AS (INSERT INTO t VALUES (1) RETURNING id) SELECT * FROM x"))

    def test_txn_control(self) -> None:
        self.assertEqual(c._txn_control("BEGIN"), (True, False))
        self.assertEqual(c._txn_control("begin snapshot"), (True, False))
        self.assertEqual(c._txn_control("COMMIT"), (False, True))
        self.assertEqual(c._txn_control("ROLLBACK"), (False, True))
        self.assertEqual(c._txn_control("SELECT 1"), (False, False))

    def test_is_loopback(self) -> None:
        self.assertTrue(c.is_loopback("127.0.0.1:7210"))
        self.assertTrue(c.is_loopback("localhost:7210"))
        self.assertTrue(c.is_loopback("[::1]:7210"))
        self.assertFalse(c.is_loopback("db.example.com:7210"))


if __name__ == "__main__":
    unittest.main()
