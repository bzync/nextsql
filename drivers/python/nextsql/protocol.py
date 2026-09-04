"""Wire encoding/decoding for NSQL v1 — mirrors drivers/php/src/Protocol.php
and drivers/go/nextsql.go byte-for-byte. Keep the two in sync: this is a
faithful reimplementation, not an independent design.
"""

from __future__ import annotations

import datetime
import decimal
import json as _json
import struct
import uuid as _uuid
from dataclasses import dataclass, field
from typing import Any

from .errors import NextSQLError

VERSION = 1
MAX_PACKET = 1 << 20
MAX_SQL = 1 << 20
MAX_NAME = 256
MAX_PARAMS = 256
MAX_ENUM_LABELS = 4096
MAX_ENUM_LABEL_BYTES = 255

TYPE_HELLO = 1
TYPE_HELLO_OK = 2
TYPE_AUTH = 3
TYPE_AUTH_OK = 4
TYPE_QUERY = 5
TYPE_PREPARE = 6
TYPE_PREPARE_OK = 7
TYPE_EXECUTE = 8
TYPE_CLOSE_STMT = 9
TYPE_CLOSE_OK = 10
TYPE_FLOW_ACK = 11
TYPE_CANCEL = 12
TYPE_TERMINATE = 13
TYPE_ROW_DESC = 14
TYPE_DATA_BATCH = 15
TYPE_COMMAND_COMPLETE = 16
TYPE_ERROR = 17
TYPE_READY = 18
TYPE_UNLOCK = 19
TYPE_UNLOCK_OK = 20
TYPE_IDEMPOTENT_QUERY = 21
TYPE_SET_READ_CONSISTENCY = 22
TYPE_NODE_STATUS = 23
TYPE_NODE_STATUS_RESP = 24

# Read-consistency modes. Values match the wire byte ordering.
READ_STRONG = 0
READ_BOUNDED = 1
READ_STALE = 2

AUTH_PASSWORD = 1
AUTH_PASSWORD_KEY = 2
FLAG_CANCEL = 1
FLAG_NULL = 0x01

KIND_UUID = 1
KIND_STRING = 2
KIND_TEXT = 3
KIND_DECIMAL = 4
KIND_TIMESTAMPTZ = 5
KIND_JSON = 6
KIND_VECTOR = 7
KIND_BOOL = 8
KIND_NULL = 9
KIND_POINT = 10
KIND_BOX = 11
KIND_LINE = 12
KIND_POLYGON = 13
KIND_BLOB = 14
KIND_INT8 = 15
KIND_INT16 = 16
KIND_INT32 = 17
KIND_INT64 = 18
KIND_UINT8 = 19
KIND_UINT16 = 20
KIND_UINT32 = 21
KIND_UINT64 = 22
KIND_DATE = 23
KIND_TIME = 24
KIND_CHAR = 25
KIND_VARCHAR = 26
KIND_TIMESTAMP = 27
KIND_FLOAT32 = 28
KIND_FLOAT64 = 29
KIND_ENUM = 30
KIND_INTERVAL = 31
KIND_STRUCT = 32
KIND_ARRAY = 33
KIND_MAP = 34
KIND_GEOMETRY = 35
KIND_GEOGRAPHY = 36

MAX_NEST_DEPTH = 8
MAX_STRUCT_FIELDS = 128
MAX_COLLECTION_LEN = 1 << 20

_UTC = datetime.timezone.utc
_EPOCH = datetime.datetime(1970, 1, 1, tzinfo=_UTC)


class ProtocolError(NextSQLError):
    def __init__(self, message: str) -> None:
        super().__init__("protocol", message)


@dataclass
class Column:
    name: str
    kind: int
    # ENUM's declared label list (D11, Datatype expansion track); None for
    # every other kind.
    labels: list[str] | None = None
    # Recursive descriptor for a STRUCT / ARRAY / MAP column (Collections
    # track); None for every other kind.
    coll_type: dict | None = None


@dataclass
class NodeStatus:
    role: str
    has_leader: bool
    healthy: bool
    applied_lsn: int
    last_contact_ms: int
    apply_backlog: int


@dataclass
class Vector:
    """Dense (values), reference (ref=True, dim only), or sparse (indices +
    values) VECTOR/BITVECTOR/SPARSEVECTOR payload."""

    dim: int
    values: list[float] = field(default_factory=list)
    indices: list[int] | None = None
    ref: bool = False


@dataclass
class Point:
    lon: float
    lat: float


@dataclass
class Box:
    west: float
    south: float
    east: float
    north: float


@dataclass
class Line:
    coords: list[float]


@dataclass
class Polygon:
    rings: list[list[float]]


# Int8/16/32/64 (D2, Datatype expansion track): explicit fixed-width int
# wrappers. A bare Python `int` still defaults to KIND_DECIMAL (see
# encode_param) and coerces server-side into any numeric column — these are
# only needed to pin an exact wire width (Python `int` is arbitrary
# precision, so there is no natural bare-value mapping to one width).
@dataclass
class Int8:
    value: int


@dataclass
class Int16:
    value: int


@dataclass
class Int32:
    value: int


@dataclass
class Int64:
    value: int


_INT_RANGES: dict[type, tuple[int, int, int, int]] = {
    # type -> (min, max, kind, width)
    Int8: (-0x80, 0x7F, KIND_INT8, 1),
    Int16: (-0x8000, 0x7FFF, KIND_INT16, 2),
    Int32: (-0x80000000, 0x7FFFFFFF, KIND_INT32, 4),
    Int64: (-0x8000000000000000, 0x7FFFFFFFFFFFFFFF, KIND_INT64, 8),
}


def _encode_int(v: Int8 | Int16 | Int32 | Int64) -> bytes:
    lo, hi, kind, width = _INT_RANGES[type(v)]
    if v.value < lo or v.value > hi:
        raise NextSQLError("invalid_argument", f"{type(v).__name__} out of range")
    return bytes([kind, 0]) + _reserved5() + (v.value & ((1 << (width * 8)) - 1)).to_bytes(width, "little")


# Uint8/16/32/64 (D3, Datatype expansion track): explicit fixed-width
# unsigned int wrappers, mirroring Int8/16/32/64 above.
@dataclass
class Uint8:
    value: int


@dataclass
class Uint16:
    value: int


@dataclass
class Uint32:
    value: int


@dataclass
class Uint64:
    value: int


_UINT_RANGES: dict[type, tuple[int, int, int]] = {
    # type -> (max, kind, width)
    Uint8: (0xFF, KIND_UINT8, 1),
    Uint16: (0xFFFF, KIND_UINT16, 2),
    Uint32: (0xFFFFFFFF, KIND_UINT32, 4),
    Uint64: (0xFFFFFFFFFFFFFFFF, KIND_UINT64, 8),
}


# Float32/Float64 (D8, Datatype expansion track): explicit wrappers, since a
# bare Python float still defaults to KIND_DECIMAL (see encode_param) and
# DECIMAL requires finite — these are needed both to pin the exact wire
# width and to send NaN/+-Infinity, which FLOAT32/FLOAT64 support
# (docs/design-datatypes.md D8) but DECIMAL does not.
@dataclass
class Float32:
    value: float


@dataclass
class Float64:
    value: float


# Interval (D6, Datatype expansion track): months (int, calendar) + days
# (int, calendar) + nanos (int, time-of-day component) — Postgres-style
# 3-field storage. A plain string still works as an INTERVAL param for
# INSERT/UPDATE column assignment (server-side Coerce, same as DATE/TIME/
# TIMESTAMP text coercion) but not inside an arithmetic expression like
# `dur + $1`, which requires the actual wire Kind.
@dataclass
class Interval:
    months: int
    days: int
    nanos: int


def _encode_interval(v: Interval) -> bytes:
    return (
        bytes([KIND_INTERVAL, 0])
        + _reserved5()
        + struct.pack("<i", v.months)
        + struct.pack("<i", v.days)
        + struct.pack("<q", v.nanos)
    )


def _encode_uint(v: Uint8 | Uint16 | Uint32 | Uint64) -> bytes:
    hi, kind, width = _UINT_RANGES[type(v)]
    if v.value < 0 or v.value > hi:
        raise NextSQLError("invalid_argument", f"{type(v).__name__} out of range")
    return bytes([kind, 0]) + _reserved5() + v.value.to_bytes(width, "little")


# EnumValue (D11, Datatype expansion track): an explicit ENUM parameter wrapper.
# Ordinary INSERT/UPDATE params can just pass a plain str — the server
# coerces STRING -> ENUM against the destination column, same as a SQL
# string literal. This wrapper exists for explicit round-tripping and
# mirrors Int8/Uint8's precedent.
@dataclass
class EnumValue:
    value: str
    labels: list[str]


def append_enum_labels(labels: list[str]) -> bytes:
    out = u16le(len(labels))
    for label in labels:
        out += u16str(label, MAX_ENUM_LABEL_BYTES)
    return out


def read_enum_labels(b: bytes, off: int) -> tuple[list[str], int]:
    _need(b, off, 2, "enum label count")
    n = u16(b, off)
    if n > MAX_ENUM_LABELS:
        raise ProtocolError("enum label count exceeds limit")
    off += 2
    labels = []
    for _ in range(n):
        label, off = read_u16_string(b, off, MAX_ENUM_LABEL_BYTES)
        labels.append(label)
    return labels, off


# --- Collections (STRUCT / ARRAY / MAP), docs/design-collections.md ----------


@dataclass
class StructValue:
    """Explicit STRUCT parameter: an ordered list of (field_name, value)."""

    fields: list[tuple[str, Any]]


@dataclass
class MapValue:
    """Explicit MAP parameter: entries as a dict or a list of (key, value)."""

    entries: Any


def _read_type_full(b: bytes, off: int, depth: int) -> tuple[dict, int]:
    _need(b, off, 6, "type")
    t: dict = {
        "kind": b[off],
        "precision": u16(b, off + 1),
        "scale": u16(b, off + 3),
        "elem": b[off + 5],
    }
    nxt = _read_nested_descriptor(b, off + 6, t, depth)
    return t, nxt


def _read_nested_descriptor(b: bytes, off: int, t: dict, depth: int) -> int:
    if depth > MAX_NEST_DEPTH + 1:
        raise ProtocolError("collection type nesting too deep")
    kind = t["kind"]
    if kind == KIND_ENUM:
        labels, off = read_enum_labels(b, off)
        t["labels"] = labels
        return off
    if kind == KIND_ARRAY:
        et, off = _read_type_full(b, off, depth + 1)
        t["elemType"] = et
        return off
    if kind == KIND_MAP:
        kt, off = _read_type_full(b, off, depth + 1)
        vt, off = _read_type_full(b, off, depth + 1)
        t["keyType"] = kt
        t["elemType"] = vt
        return off
    if kind == KIND_STRUCT:
        _need(b, off, 2, "struct field count")
        n = u16(b, off)
        if n == 0 or n > MAX_STRUCT_FIELDS:
            raise ProtocolError("struct field count out of range")
        off += 2
        fields = []
        for _ in range(n):
            name, off = read_u16_string(b, off, 255)
            ft, off = _read_type_full(b, off, depth + 1)
            fields.append((name, ft))
        t["fields"] = fields
        return off
    return off


def _decode_payload(b: bytes, off: int, t: dict) -> tuple[Any, int]:
    kind = t["kind"]
    if kind in (KIND_STRUCT, KIND_ARRAY, KIND_MAP):
        return _decode_collection_payload(b, off, t)
    header = bytes([kind, 0]) + _reserved5()
    if kind == KIND_ENUM:
        header += append_enum_labels(t.get("labels", []))
    synthetic = header + b[off:]
    value, nxt, _ = decode_value(synthetic, 0)
    return value, off + (nxt - len(header))


def _decode_collection_payload(b: bytes, off: int, t: dict) -> tuple[Any, int]:
    _need(b, off, 4, "collection")
    body_len = u32(b, off)
    body_end = off + 4 + body_len
    _need(b, off + 4, body_len, "collection body")
    p = off + 4
    n = u32(b, p)
    p += 4
    if n > 2 * MAX_COLLECTION_LEN + 2 or n > body_len:
        raise ProtocolError("collection member count out of range")
    nb = (n + 7) >> 3
    nulls = b[p : p + nb]
    p += nb
    kind = t["kind"]

    def member_type(i: int) -> dict:
        if kind == KIND_STRUCT:
            return t["fields"][i][1]
        if kind == KIND_ARRAY:
            return t["elemType"]
        return t["keyType"] if i % 2 == 0 else t["elemType"]

    members: list[Any] = []
    for i in range(n):
        if nulls[i >> 3] & (1 << (i & 7)):
            members.append(None)
            continue
        value, p = _decode_payload(b, p, member_type(i))
        members.append(value)
    if kind == KIND_STRUCT:
        return {name: members[i] for i, (name, _) in enumerate(t["fields"])}, body_end
    if kind == KIND_ARRAY:
        return members, body_end
    out = {}
    for i in range(0, len(members) - 1, 2):
        out[members[i]] = members[i + 1]
    return out, body_end


def _encode_type_full(t: dict) -> bytes:
    out = bytes([t["kind"]]) + _reserved5()
    kind = t["kind"]
    if kind == KIND_ENUM:
        out += append_enum_labels(t.get("labels", []))
    elif kind == KIND_ARRAY:
        out += _encode_type_full(t["elemType"])
    elif kind == KIND_MAP:
        out += _encode_type_full(t["keyType"]) + _encode_type_full(t["elemType"])
    elif kind == KIND_STRUCT:
        out += u16le(len(t["fields"]))
        for name, ft in t["fields"]:
            out += u16str(name, 255) + _encode_type_full(ft)
    return out


def _infer_value(v: Any) -> tuple[dict, bytes | None]:
    if v is None:
        return {"kind": KIND_STRING}, None
    if isinstance(v, StructValue):
        fields = []
        payloads = []
        for name, fv in v.fields:
            ft, pl = _infer_value(fv)
            fields.append((name, ft))
            payloads.append(pl)
        return {"kind": KIND_STRUCT, "fields": fields}, _collection_payload(payloads)
    if isinstance(v, MapValue):
        items = v.entries.items() if isinstance(v.entries, dict) else v.entries
        flat_types: list[dict] = []
        payloads = []
        for k, val in items:
            kt, kp = _infer_value(k)
            vt, vp = _infer_value(val)
            flat_types += [kt, vt]
            payloads += [kp, vp]
        key_type = next((flat_types[i] for i in range(0, len(flat_types), 2) if payloads[i] is not None), {"kind": KIND_STRING})
        val_type = next((flat_types[i] for i in range(1, len(flat_types), 2) if payloads[i] is not None), {"kind": KIND_STRING})
        return {"kind": KIND_MAP, "keyType": key_type, "elemType": val_type}, _collection_payload(payloads)
    if isinstance(v, (list, tuple)):
        types_payloads = [_infer_value(x) for x in v]
        elem_type = next((t for t, p in types_payloads if p is not None), {"kind": KIND_STRING})
        return {"kind": KIND_ARRAY, "elemType": elem_type}, _collection_payload([p for _, p in types_payloads])
    enc = encode_param(v)
    kind = enc[0]
    hdr = 7
    if kind == KIND_ENUM:
        lc = u16(enc, 7)
        hdr = 9
        for _ in range(lc):
            hdr += 2 + u16(enc, hdr)
    return {"kind": kind}, enc[hdr:]


def _collection_payload(payloads: list[bytes | None]) -> bytes:
    n = len(payloads)
    nb = (n + 7) >> 3
    nulls = bytearray(nb)
    chunks = b""
    for i, pl in enumerate(payloads):
        if pl is None:
            nulls[i >> 3] |= 1 << (i & 7)
        else:
            chunks += pl
    body = u32le(n) + bytes(nulls) + chunks
    return u32le(len(body)) + body


def _encode_collection_param(v: Any) -> bytes:
    t, payload = _infer_value(v)
    type_body = _encode_type_full(t)[1:]
    return bytes([t["kind"], 0]) + type_body + (payload or b"")


# --- Spatial: EWKB decode (Spatial track, docs/design-spatial.md) ----------

_EWKB_TYPES = {
    1: "Point", 2: "LineString", 3: "Polygon",
    4: "MultiPoint", 5: "MultiLineString", 6: "MultiPolygon",
    7: "GeometryCollection",
}
_EWKB_SRID_FLAG = 0x20000000


@dataclass
class Geometry:
    """A decoded GEOMETRY / GEOGRAPHY value (Spatial track). `type` is the
    OGC subtype name, `coordinates` nested per `type` (Point: [x, y]; a
    GeometryCollection uses `geometries` instead)."""

    type: str
    srid: int
    coordinates: Any = None
    geometries: list["Geometry"] | None = None

    def to_wkt(self) -> str:
        def pt(xy: tuple[float, float]) -> str:
            return f"{xy[0]} {xy[1]}"

        def ring(r: list[tuple[float, float]]) -> str:
            return "(" + ", ".join(pt(p) for p in r) + ")"

        if self.type == "Point":
            return f"POINT({pt(self.coordinates)})"
        if self.type == "LineString":
            return "LINESTRING(" + ", ".join(pt(p) for p in self.coordinates) + ")"
        if self.type == "Polygon":
            return "POLYGON(" + ", ".join(ring(r) for r in self.coordinates) + ")"
        if self.type == "MultiPoint":
            return "MULTIPOINT(" + ", ".join(f"({pt(p)})" for p in self.coordinates) + ")"
        if self.type == "MultiLineString":
            return "MULTILINESTRING(" + ", ".join(ring(r) for r in self.coordinates) + ")"
        if self.type == "MultiPolygon":
            return "MULTIPOLYGON(" + ", ".join("(" + ", ".join(ring(r) for r in poly) + ")" for poly in self.coordinates) + ")"
        if self.type == "GeometryCollection":
            return "GEOMETRYCOLLECTION(" + ", ".join(g.to_wkt() for g in (self.geometries or [])) + ")"
        raise NextSQLError("invalid_argument", "unsupported geometry type")


def _decode_ewkb(b: bytes, off: int, depth: int) -> tuple[Geometry, int]:
    if depth > 8:
        raise ProtocolError("geometry nesting too deep")
    _need(b, off, 5, "geometry header")
    if b[off] != 1:
        raise ProtocolError("only little-endian EWKB is supported")
    tword = struct.unpack_from("<I", b, off + 1)[0]
    gtype = tword & ~_EWKB_SRID_FLAG
    p = off + 5
    srid = 0
    if tword & _EWKB_SRID_FLAG:
        srid = struct.unpack_from("<I", b, p)[0]
        p += 4
    name = _EWKB_TYPES.get(gtype)
    if name is None:
        raise ProtocolError("unknown geometry type")

    def f64() -> float:
        nonlocal p
        v = struct.unpack_from("<d", b, p)[0]
        p += 8
        return v

    def u32() -> int:
        nonlocal p
        v = struct.unpack_from("<I", b, p)[0]
        p += 4
        return v

    def pts(n: int) -> list[tuple[float, float]]:
        return [(f64(), f64()) for _ in range(n)]

    if gtype == 1:
        return Geometry(name, srid, (f64(), f64())), p
    if gtype == 2:
        return Geometry(name, srid, pts(u32())), p
    if gtype == 3:
        nr = u32()
        rings = [pts(u32()) for _ in range(nr)]
        return Geometry(name, srid, rings), p
    np_ = u32()
    parts: list[Geometry] = []
    for _ in range(np_):
        sub, p = _decode_ewkb(b, p, depth + 1)
        parts.append(sub)
    if gtype == 7:
        return Geometry(name, srid, geometries=parts), p
    return Geometry(name, srid, [part.coordinates for part in parts]), p


def _encode_enum(v: EnumValue) -> bytes:
    try:
        ord_ = v.labels.index(v.value)
    except ValueError:
        raise NextSQLError("invalid_argument", "value is not a member of the ENUM label set") from None
    return bytes([KIND_ENUM, 0]) + _reserved5() + append_enum_labels(v.labels) + u16le(ord_)


# NaiveTimestamp (D7, Datatype expansion track): an explicit wrapper for the
# no-timezone TIMESTAMP type. A bare naive datetime.datetime (tzinfo is None)
# already has an established meaning in this driver — "assume UTC", encoded
# as TIMESTAMPTZ (see encode_param) — so this wrapper is required to select
# the distinct TIMESTAMP Kind instead, rather than silently repurposing that
# existing default. datetime.date and datetime.time need no such wrapper:
# they are unambiguous native mappings for DATE/TIME with no prior meaning
# in this driver to conflict with.
@dataclass
class NaiveTimestamp:
    value: datetime.datetime


def _civil_nanos(dt: datetime.datetime) -> int:
    """Epoch nanoseconds for dt's own wall-clock fields, with no offset
    applied — converts an aware dt to UTC first (so its *fields* become the
    civil value), then treats those fields as literal (docs/design-datatypes.md D7)."""
    if dt.tzinfo is not None:
        dt = dt.astimezone(_UTC)
    dt = dt.replace(tzinfo=_UTC)
    delta = dt - _EPOCH
    return (delta.days * 86400 + delta.seconds) * 1_000_000_000 + delta.microseconds * 1000


def _encode_naive_timestamp(v: NaiveTimestamp) -> bytes:
    return bytes([KIND_TIMESTAMP, 0]) + _reserved5() + struct.pack("<q", _civil_nanos(v.value))


def _encode_date(d: datetime.date) -> bytes:
    day_count = (d - _EPOCH.date()).days
    return bytes([KIND_DATE, 0]) + _reserved5() + struct.pack("<i", day_count)


def _encode_time(t: datetime.time) -> bytes:
    ns = (t.hour * 3600 + t.minute * 60 + t.second) * 1_000_000_000 + t.microsecond * 1000
    return bytes([KIND_TIME, 0]) + _reserved5() + struct.pack("<Q", ns)


def _encode_float(kind: int, n: float) -> bytes:
    # NaN/+-Infinity are valid FLOAT32/FLOAT64 values (unlike the bare-float
    # -> Decimal default path, which requires finite) — the server
    # canonicalizes -0.0 -> +0.0 and every NaN payload to one value
    # (docs/design-datatypes.md D8).
    fmt = "<f" if kind == KIND_FLOAT32 else "<d"
    return bytes([kind, 0]) + _reserved5() + struct.pack(fmt, n)


def _need(b: bytes, off: int, n: int, what: str) -> None:
    if off + n > len(b):
        raise ProtocolError(f"truncated {what}")


def u16(b: bytes, off: int) -> int:
    _need(b, off, 2, "u16")
    return struct.unpack_from("<H", b, off)[0]


def u32(b: bytes, off: int) -> int:
    _need(b, off, 4, "u32")
    return struct.unpack_from("<I", b, off)[0]


def u64(b: bytes, off: int) -> int:
    _need(b, off, 8, "u64")
    return struct.unpack_from("<Q", b, off)[0]


def i64(b: bytes, off: int) -> int:
    _need(b, off, 8, "i64")
    return struct.unpack_from("<q", b, off)[0]


def u16le(n: int) -> bytes:
    return struct.pack("<H", n)


def u32le(n: int) -> bytes:
    return struct.pack("<I", n)


def u64le(n: int) -> bytes:
    return struct.pack("<Q", n & 0xFFFFFFFFFFFFFFFF)


def u16str(s: str, max_len: int = MAX_NAME) -> bytes:
    raw = s.encode("utf-8")
    if len(raw) > max_len or len(raw) > 0xFFFF:
        raise ProtocolError("string exceeds limit")
    return u16le(len(raw)) + raw


def u32bytes(raw: bytes, max_len: int) -> bytes:
    if len(raw) > max_len:
        raise ProtocolError("bytes exceed limit")
    return u32le(len(raw)) + raw


def read_u16_string(b: bytes, off: int, max_len: int) -> tuple[str, int]:
    _need(b, off, 2, "string length")
    n = u16(b, off)
    if n > max_len:
        raise ProtocolError("truncated string")
    _need(b, off + 2, n, "string")
    return b[off + 2 : off + 2 + n].decode("utf-8"), off + 2 + n


def read_u32_bytes(b: bytes, off: int, max_len: int) -> tuple[bytes, int]:
    _need(b, off, 4, "bytes length")
    n = u32(b, off)
    if n > max_len:
        raise ProtocolError("truncated bytes")
    _need(b, off + 4, n, "bytes")
    return b[off + 4 : off + 4 + n], off + 4 + n


def encode_hello(version: int, flags: int, secret: bytes, database: str, user: str, realm: str = "") -> bytes:
    sec = (secret or b"")[:8].ljust(8, b"\x00")
    out = u16le(version) + u16le(flags) + sec + u16str(database) + u16str(user)
    # Realm is an optional trailing field (M2-2): emitted only when
    # selected, so a Hello with no realm is byte-identical to the
    # pre-realm wire shape.
    if realm:
        out += u16str(realm)
    return out


def decode_hello_ok(b: bytes) -> tuple[int, int, bytes]:
    if len(b) != 11:
        raise ProtocolError("bad hello-ok length")
    version = u16(b, 0)
    auth_method = b[2]
    secret = b[3:11]
    return version, auth_method, secret


def encode_query(sql: str, params: list[Any]) -> bytes:
    if len(params) > MAX_PARAMS:
        raise ProtocolError("too many parameters")
    out = bytearray(u32bytes(sql.encode("utf-8"), MAX_SQL))
    out += u16le(len(params))
    for p in params:
        out += u16str("")
        out += encode_param(p)
    return bytes(out)


def encode_execute(stmt_id: int, params: list[Any]) -> bytes:
    if len(params) > MAX_PARAMS:
        raise ProtocolError("too many parameters")
    out = bytearray(u32le(stmt_id))
    out += u16le(len(params))
    for p in params:
        out += u16str("")
        out += encode_param(p)
    return bytes(out)


def encode_idempotent_query(key: str, sql: str, params: list[Any]) -> bytes:
    if len(params) > MAX_PARAMS:
        raise ProtocolError("too many parameters")
    out = bytearray(u16str(key))
    out += u32bytes(sql.encode("utf-8"), MAX_SQL)
    out += u16le(len(params))
    for p in params:
        out += u16str("")
        out += encode_param(p)
    return bytes(out)


def _reserved5() -> bytes:
    return b"\x00" * 5


def encode_param(v: Any) -> bytes:
    if v is None:
        return bytes([KIND_STRING, FLAG_NULL]) + _reserved5()
    if isinstance(v, bool):
        return bytes([KIND_BOOL, 0]) + _reserved5() + (b"\x01" if v else b"\x00")
    if isinstance(v, _uuid.UUID):
        return bytes([KIND_UUID, 0]) + _reserved5() + v.bytes
    if isinstance(v, (int, decimal.Decimal)):
        return bytes([KIND_DECIMAL, 0]) + _reserved5() + encode_decimal(str(v))
    if isinstance(v, float):
        if v != v or v in (float("inf"), float("-inf")):  # NaN/Inf check without importing math
            raise NextSQLError("invalid_argument", "parameter is not finite")
        return bytes([KIND_DECIMAL, 0]) + _reserved5() + encode_decimal(format(v, "f"))
    if isinstance(v, (bytes, bytearray)):
        return bytes([KIND_BLOB, 0]) + _reserved5() + u32bytes(bytes(v), MAX_PACKET)
    if isinstance(v, (Int8, Int16, Int32, Int64)):
        return _encode_int(v)
    if isinstance(v, (Uint8, Uint16, Uint32, Uint64)):
        return _encode_uint(v)
    if isinstance(v, Float32):
        return _encode_float(KIND_FLOAT32, v.value)
    if isinstance(v, Float64):
        return _encode_float(KIND_FLOAT64, v.value)
    if isinstance(v, EnumValue):
        return _encode_enum(v)
    if isinstance(v, NaiveTimestamp):
        return _encode_naive_timestamp(v)
    if isinstance(v, Interval):
        return _encode_interval(v)
    if isinstance(v, str):
        return bytes([KIND_STRING, 0]) + _reserved5() + u32bytes(v.encode("utf-8"), MAX_PACKET)
    if isinstance(v, datetime.datetime):
        dt = v if v.tzinfo is not None else v.replace(tzinfo=_UTC)
        delta = dt.astimezone(_UTC) - _EPOCH
        ns = (delta.days * 86400 + delta.seconds) * 1_000_000_000 + delta.microseconds * 1000
        return bytes([KIND_TIMESTAMPTZ, 0]) + _reserved5() + struct.pack("<q", ns)
    if isinstance(v, datetime.date):
        # Checked after datetime.datetime (a subclass of date) above, so
        # only a bare date reaches here.
        return _encode_date(v)
    if isinstance(v, datetime.time):
        return _encode_time(v)
    if isinstance(v, Point):
        return bytes([KIND_POINT, 0]) + _reserved5() + struct.pack("<dd", v.lon, v.lat)
    if isinstance(v, Box):
        return bytes([KIND_BOX, 0]) + _reserved5() + struct.pack("<dddd", v.west, v.south, v.east, v.north)
    if isinstance(v, Vector):
        return _encode_vector(v)
    if isinstance(v, (list, tuple)) and len(v) > 0 and all(isinstance(x, (int, float)) for x in v):
        return _encode_vector(Vector(dim=len(v), values=[float(x) for x in v]))
    if isinstance(v, (StructValue, MapValue)):
        return _encode_collection_param(v)
    if isinstance(v, Geometry):
        wkt = v.to_wkt()
        if v.srid:
            wkt = f"SRID={v.srid};{wkt}"
        return bytes([KIND_STRING, 0]) + _reserved5() + u32bytes(wkt.encode("utf-8"), MAX_PACKET)
    if isinstance(v, (list, tuple)):
        # A non-numeric (or empty) list/tuple is an ARRAY collection param;
        # the server re-coerces element types against the destination column.
        return _encode_collection_param(v)
    if isinstance(v, dict):
        payload = _json.dumps(v, ensure_ascii=False).encode("utf-8")
        return bytes([KIND_STRING, 0]) + _reserved5() + u32bytes(payload, MAX_PACKET)
    raise NextSQLError("invalid_argument", f"unsupported parameter type: {type(v)!r}")


def decode_value(b: bytes, off: int) -> tuple[Any, int, int]:
    _need(b, off, 7, "value header")
    kind = b[off]
    flags = b[off + 1]
    off += 7
    enum_labels: list[str] | None = None
    coll_type: dict | None = None
    if kind == KIND_ENUM:
        enum_labels, off = read_enum_labels(b, off)
    elif kind in (KIND_STRUCT, KIND_ARRAY, KIND_MAP):
        coll_type = {"kind": kind}
        off = _read_nested_descriptor(b, off, coll_type, 0)
    if flags & FLAG_NULL:
        return None, off, kind
    if kind in (KIND_STRUCT, KIND_ARRAY, KIND_MAP):
        value, nxt = _decode_collection_payload(b, off, coll_type)
        return value, nxt, kind
    if kind == KIND_ENUM:
        _need(b, off, 2, "enum")
        ord_ = u16(b, off)
        if ord_ >= len(enum_labels):
            raise ProtocolError("ENUM ordinal out of range")
        return enum_labels[ord_], off + 2, kind
    if kind == KIND_UUID:
        _need(b, off, 16, "uuid")
        return _uuid.UUID(bytes=b[off : off + 16]), off + 16, kind
    if kind in (KIND_STRING, KIND_TEXT, KIND_CHAR, KIND_VARCHAR):
        raw, next_off = read_u32_bytes(b, off, MAX_PACKET)
        return raw.decode("utf-8"), next_off, kind
    if kind == KIND_BLOB:
        raw, next_off = read_u32_bytes(b, off, MAX_PACKET)
        return raw, next_off, kind
    if kind == KIND_JSON:
        raw, next_off = read_u32_bytes(b, off, MAX_PACKET)
        return decode_nsjb(raw), next_off, kind
    if kind == KIND_DECIMAL:
        raw, next_off = read_u32_bytes(b, off, MAX_PACKET)
        return decode_decimal(raw), next_off, kind
    if kind == KIND_TIMESTAMPTZ:
        ns = i64(b, off)
        sec, nsec = divmod(ns, 1_000_000_000)
        dt = _EPOCH + datetime.timedelta(seconds=sec, microseconds=nsec // 1000)
        return dt, off + 8, kind
    if kind == KIND_TIMESTAMP:
        # Naive/no-timezone: same wire shape as TimestampTZ, decoded to a
        # naive datetime.datetime (tzinfo=None) — the civil value read
        # literally, no offset (docs/design-datatypes.md D7).
        ns = i64(b, off)
        sec, nsec = divmod(ns, 1_000_000_000)
        dt = (_EPOCH + datetime.timedelta(seconds=sec, microseconds=nsec // 1000)).replace(tzinfo=None)
        return dt, off + 8, kind
    if kind == KIND_DATE:
        _need(b, off, 4, "date")
        day_count = struct.unpack_from("<i", b, off)[0]
        return _EPOCH.date() + datetime.timedelta(days=day_count), off + 4, kind
    if kind == KIND_TIME:
        # Nanoseconds since midnight; datetime.time only holds microsecond
        # precision, so sub-microsecond nanoseconds are truncated (the same
        # precision ceiling every timestamp-family decode already has here).
        _need(b, off, 8, "time")
        ns = u64(b, off)
        sec, nsec = divmod(ns, 1_000_000_000)
        h, rem = divmod(sec, 3600)
        m, s = divmod(rem, 60)
        return datetime.time(h, m, s, nsec // 1000), off + 8, kind
    if kind in (KIND_FLOAT32, KIND_FLOAT64):
        width = 4 if kind == KIND_FLOAT32 else 8
        _need(b, off, width, "float")
        fmt = "<f" if kind == KIND_FLOAT32 else "<d"
        return struct.unpack_from(fmt, b, off)[0], off + width, kind
    if kind == KIND_INTERVAL:
        _need(b, off, 16, "interval")
        months = struct.unpack_from("<i", b, off)[0]
        days = struct.unpack_from("<i", b, off + 4)[0]
        nanos = struct.unpack_from("<q", b, off + 8)[0]
        return Interval(months, days, nanos), off + 16, kind
    if kind == KIND_BOOL:
        _need(b, off, 1, "bool")
        return b[off] != 0, off + 1, kind
    if kind in (KIND_INT8, KIND_INT16, KIND_INT32, KIND_INT64):
        width = {KIND_INT8: 1, KIND_INT16: 2, KIND_INT32: 4, KIND_INT64: 8}[kind]
        _need(b, off, width, "int")
        # Python int is arbitrary precision, so every width decodes to a
        # plain native int (no BigInt-style split needed, unlike JS).
        return int.from_bytes(b[off : off + width], "little", signed=True), off + width, kind
    if kind in (KIND_UINT8, KIND_UINT16, KIND_UINT32, KIND_UINT64):
        width = {KIND_UINT8: 1, KIND_UINT16: 2, KIND_UINT32: 4, KIND_UINT64: 8}[kind]
        _need(b, off, width, "uint")
        return int.from_bytes(b[off : off + width], "little", signed=False), off + width, kind
    if kind == KIND_VECTOR:
        value, next_off = _decode_vector(b, off)
        return value, next_off, kind
    if kind == KIND_POINT:
        _need(b, off, 16, "point")
        lon, lat = struct.unpack_from("<dd", b, off)
        return Point(lon, lat), off + 16, kind
    if kind == KIND_BOX:
        _need(b, off, 32, "box")
        west, south, east, north = struct.unpack_from("<dddd", b, off)
        return Box(west, south, east, north), off + 32, kind
    if kind == KIND_LINE:
        n = u16(b, off)
        p = off + 2
        coords = []
        for _ in range(n * 2):
            _need(b, p, 8, "line coord")
            coords.append(struct.unpack_from("<d", b, p)[0])
            p += 8
        return Line(coords), p, kind
    if kind in (KIND_GEOMETRY, KIND_GEOGRAPHY):
        length = u32(b, off)
        g, _ = _decode_ewkb(b, off + 4, 0)
        return g, off + 4 + length, kind
    if kind == KIND_POLYGON:
        nr = u16(b, off)
        p = off + 2
        rings = []
        for _ in range(nr):
            npts = u16(b, p)
            p += 2
            ring = []
            for _ in range(npts * 2):
                _need(b, p, 8, "polygon coord")
                ring.append(struct.unpack_from("<d", b, p)[0])
                p += 8
            rings.append(ring)
        return Polygon(rings), p, kind
    raise ProtocolError("unsupported type")


def _decode_vector(b: bytes, off: int) -> tuple[Vector, int]:
    dim = u16(b, off)
    flag = b[off + 2]
    if flag & 1:
        return Vector(dim=dim, ref=True), off + 3
    if flag & 2:
        _need(b, off + 3, 4, "sparse nnz")
        nnz = u32(b, off + 3)
        indices: list[int] = []
        values: list[float] = []
        p = off + 7
        for _ in range(nnz):
            _need(b, p, 8, "sparse entry")
            indices.append(u32(b, p))
            values.append(struct.unpack_from("<f", b, p + 4)[0])
            p += 8
        return Vector(dim=dim, values=values, indices=indices), p
    p = off + 3
    values = []
    for _ in range(dim):
        _need(b, p, 4, "vector component")
        values.append(struct.unpack_from("<f", b, p)[0])
        p += 4
    return Vector(dim=dim, values=values), p


def decode_row_desc(b: bytes) -> list[Column]:
    n = u16(b, 0)
    off = 2
    cols = []
    for _ in range(n):
        name, off = read_u16_string(b, off, MAX_NAME)
        _need(b, off, 6, "column type")
        kind = b[off]
        off += 6
        labels: list[str] | None = None
        coll_type: dict | None = None
        if kind == KIND_ENUM:
            labels, off = read_enum_labels(b, off)
        elif kind in (KIND_STRUCT, KIND_ARRAY, KIND_MAP):
            coll_type = {"kind": kind}
            off = _read_nested_descriptor(b, off, coll_type, 0)
        cols.append(Column(name=name, kind=kind, labels=labels, coll_type=coll_type))
    return cols


def decode_data_batch(b: bytes) -> list[list[Any]]:
    nrows = u32(b, 0)
    off = 4
    rows: list[list[Any]] = []
    for _ in range(nrows):
        ncols = u16(b, off)
        off += 2
        row = []
        for _ in range(ncols):
            value, next_off, _kind = decode_value(b, off)
            row.append(value)
            off = next_off
        rows.append(row)
    return rows


def decode_command_complete(b: bytes) -> int:
    if len(b) != 8:
        raise ProtocolError("bad command-complete length")
    return u64(b, 0)


def decode_error(b: bytes) -> NextSQLError:
    code, off = read_u16_string(b, 0, MAX_NAME)
    msg, _ = read_u16_string(b, off, MAX_NAME)
    return NextSQLError(code, msg)


def encode_set_read_consistency(mode: int, max_staleness_ms: int) -> bytes:
    if mode not in (READ_STRONG, READ_BOUNDED, READ_STALE):
        raise NextSQLError("invalid_argument", "unknown read consistency mode")
    ms = max_staleness_ms if max_staleness_ms > 0 else 0
    return bytes([mode]) + u64le(ms)


def decode_node_status(b: bytes) -> NodeStatus:
    role, off = read_u16_string(b, 0, MAX_NAME)
    if len(b) - off != 25:
        raise ProtocolError("bad node-status length")
    flags = b[off]
    off += 1
    applied_lsn = u64(b, off)
    raw_contact = u64(b, off + 8)
    last_contact_ms = -1 if raw_contact == 0xFFFFFFFFFFFFFFFF else raw_contact
    apply_backlog = u64(b, off + 16)
    return NodeStatus(
        role=role,
        has_leader=bool(flags & 1),
        healthy=bool(flags & 2),
        applied_lsn=applied_lsn,
        last_contact_ms=last_contact_ms,
        apply_backlog=apply_backlog,
    )


_DEC_RE = None


def encode_decimal(s: str) -> bytes:
    s = s.strip()
    neg = False
    if s.startswith("+"):
        s = s[1:]
    if s.startswith("-"):
        neg = True
        s = s[1:]
    global _DEC_RE
    if _DEC_RE is None:
        import re

        _DEC_RE = re.compile(r"^\d+(\.\d+)?$")
    if not _DEC_RE.match(s):
        raise NextSQLError("invalid_argument", "invalid decimal")
    if "." in s:
        int_part, frac_part = s.split(".", 1)
    else:
        int_part, frac_part = s, ""
    scale = len(frac_part)
    digits = (int_part + frac_part).lstrip("0")
    if digits == "":
        digits = "0"
    coef = _dec_to_bytes(digits)
    body = (b"\x01" if neg else b"\x00") + b"\x00" + u16le(scale) + coef
    return u32bytes(body, MAX_PACKET)


def decode_decimal(body: bytes) -> decimal.Decimal:
    if len(body) < 4:
        raise NextSQLError("invalid_format", "truncated decimal")
    neg = (body[0] & 1) != 0
    scale = u16(body, 2)
    digits = _bytes_to_dec(body[4:])
    if scale > 0:
        digits = digits.rjust(scale + 1, "0")
        s = digits[:-scale] + "." + digits[-scale:]
    else:
        s = digits
    if neg and not (s == "0" or set(s) <= {"0", "."}):
        s = "-" + s
    return decimal.Decimal(s)


def _dec_to_bytes(digits: str) -> bytes:
    n = int(digits)
    if n == 0:
        return b""
    nbytes = (n.bit_length() + 7) // 8
    return n.to_bytes(nbytes, "big")


def _bytes_to_dec(raw: bytes) -> str:
    return str(int.from_bytes(raw, "big"))


def format_uuid(raw: bytes) -> str:
    return str(_uuid.UUID(bytes=raw))


def _encode_vector(v: Vector) -> bytes:
    # Generic 7-byte value header (kind, flags=0, 5 reserved metadata bytes
    # — left zero; the server derives dimension/element-kind from the
    # self-contained payload below when the metadata is zero, see
    # internal/sql/types/row.go decodeScalar's KindVector branch). The
    # payload then repeats dim + a flag byte before the actual data — this
    # duplication matches the wire format every other driver produces, not
    # a Python-specific choice.
    dim = v.dim if v.dim else len(v.values)
    header = bytes([KIND_VECTOR, 0]) + _reserved5()
    if v.indices is not None:
        payload = bytearray(u16le(dim) + b"\x02")
        payload += u32le(len(v.indices))
        for idx, val in zip(v.indices, v.values):
            payload += u32le(idx)
            payload += struct.pack("<f", val)
        return header + bytes(payload)
    payload = bytearray(u16le(dim) + b"\x00")
    for val in v.values:
        payload += struct.pack("<f", val)
    return header + bytes(payload)


# --- NSJB binary JSON (see internal/protocol/messages.go EncodeJSON) ---


def decode_nsjb(doc: bytes) -> Any:
    if len(doc) < 5 or doc[0:4] != b"NSJB" or doc[4] != 1:
        raise NextSQLError("invalid_format", "not binary JSON")
    value, next_off = _read_nsjb(doc, 5)
    if next_off != len(doc):
        raise NextSQLError("invalid_format", "trailing JSON bytes")
    return value


def _read_nsjb(b: bytes, off: int) -> tuple[Any, int]:
    if off >= len(b):
        raise NextSQLError("invalid_format", "truncated JSON")
    tag = b[off]
    if tag == 0x00:
        return None, off + 1
    if tag == 0x01:
        return False, off + 1
    if tag == 0x02:
        return True, off + 1
    if tag == 0x03:
        return i64(b, off + 1), off + 9
    if tag == 0x04:
        return _read_nsjb_str(b, off, number=False)
    if tag == 0x05:
        return _read_nsjb_str(b, off, number=True)
    if tag == 0x06:
        return _read_nsjb_array(b, off)
    if tag == 0x07:
        return _read_nsjb_object(b, off)
    raise NextSQLError("invalid_format", "unknown JSON tag")


def _read_nsjb_str(b: bytes, off: int, number: bool) -> tuple[Any, int]:
    n = u32(b, off + 1)
    end = off + 5 + n
    s = b[off + 5 : end].decode("utf-8")
    if number:
        try:
            return (float(s) if "." in s or "e" in s or "E" in s else int(s)), end
        except ValueError:
            pass
    return s, end


def _read_nsjb_array(b: bytes, off: int) -> tuple[list[Any], int]:
    size = u32(b, off + 1)
    body = off + 5
    end = body + size
    count = u32(b, body)
    cur = body + 4
    out = []
    for _ in range(count):
        v, cur = _read_nsjb(b, cur)
        out.append(v)
    return out, end


def _read_nsjb_object(b: bytes, off: int) -> tuple[dict[str, Any], int]:
    size = u32(b, off + 1)
    body = off + 5
    end = body + size
    count = u16(b, body)
    cur = body + 2
    out: dict[str, Any] = {}
    for _ in range(count):
        klen = u16(b, cur)
        cur += 2
        key = b[cur : cur + klen].decode("utf-8")
        cur += klen
        v, cur = _read_nsjb(b, cur)
        out[key] = v
    return out, end
