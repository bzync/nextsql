"""Portable randomized NSCE1 and deterministic NSCE2 client-field encryption.

Speaks the exact same binary envelope, AES-256-GCM, RFC 5297 AES-SIV, and NSFK1
durable keyring format as Go, Node.js, Bun, and PHP.
"""

from __future__ import annotations

import base64
import os
import re
import secrets
import struct
import time
from dataclasses import dataclass
from datetime import datetime, timezone
from decimal import Decimal
from typing import Any, Protocol

from .errors import NextSQLError
from .protocol import (
    KIND_BLOB,
    KIND_BOOL,
    KIND_DECIMAL,
    KIND_INT8,
    KIND_INT16,
    KIND_INT32,
    KIND_INT64,
    KIND_JSON,
    KIND_STRING,
    KIND_TEXT,
    KIND_TIMESTAMPTZ,
    KIND_UINT8,
    KIND_UINT16,
    KIND_UINT32,
    KIND_UINT64,
    KIND_UUID,
    decode_decimal,
    encode_decimal,
)

# Try importing cryptography; fallback pure-Python implementations are provided below.
try:
    from cryptography.hazmat.primitives.ciphers.aead import AESGCM, AESSIV

    _HAS_CRYPTOGRAPHY = True
except ImportError:
    _HAS_CRYPTOGRAPHY = False

PREFIX = "NSCE1."
DETERMINISTIC_PREFIX = "NSCE2."
VERSION = 1
DETERMINISTIC_VERSION = 2
SUITE_AES_256_GCM = 1
SUITE_AES_SIV = 2
KEY_SIZE = 32
MAX_KEY_ID = 64
MAX_KEYS = 64
MAX_PLAINTEXT = 1 << 20
NONCE_SIZE = 12
TAG_SIZE = 16

KEYRING_MAGIC = b"NSFK"
KEYRING_VERSION = 1
FLAG_CURRENT = 1 << 0
FLAG_REVOKED = 1 << 1

_KEY_ID_RE = re.compile(r"^[A-Za-z0-9._-]+$")


@dataclass(frozen=True)
class FieldType:
    kind: int
    precision: int = 0
    scale: int = 0
    vec_elem: int = 0

    @classmethod
    def decimal(cls, precision: int, scale: int) -> FieldType:
        if precision < 1 or precision > 38 or scale < 0 or scale > precision:
            raise NextSQLError("invalid_argument", "unsupported client-encrypted type")
        return cls(kind=KIND_DECIMAL, precision=precision, scale=scale)


FieldType.UUID = FieldType(kind=KIND_UUID)
FieldType.STRING = FieldType(kind=KIND_STRING)
FieldType.TEXT = FieldType(kind=KIND_TEXT)
FieldType.BLOB = FieldType(kind=KIND_BLOB)
FieldType.TIMESTAMPTZ = FieldType(kind=KIND_TIMESTAMPTZ)
FieldType.JSON = FieldType(kind=KIND_JSON)
FieldType.BOOL = FieldType(kind=KIND_BOOL)
FieldType.INT8 = FieldType(kind=KIND_INT8)
FieldType.INT16 = FieldType(kind=KIND_INT16)
FieldType.INT32 = FieldType(kind=KIND_INT32)
FieldType.INT64 = FieldType(kind=KIND_INT64)
FieldType.UINT8 = FieldType(kind=KIND_UINT8)
FieldType.UINT16 = FieldType(kind=KIND_UINT16)
FieldType.UINT32 = FieldType(kind=KIND_UINT32)
FieldType.UINT64 = FieldType(kind=KIND_UINT64)


@dataclass
class FieldKey:
    id: str
    material: bytes

    def __post_init__(self) -> None:
        if not valid_key_id(self.id):
            raise NextSQLError("invalid_argument", "invalid field key id")
        if len(self.material) != KEY_SIZE or all(b == 0 for b in self.material):
            raise NextSQLError("invalid_argument", "invalid field key")


@dataclass
class FieldKeyInfo:
    id: str
    created: datetime
    current: bool
    revoked: bool


class FieldKeyProvider(Protocol):
    def current_field_key(self, database: str, table: str, column: str) -> FieldKey: ...
    def field_key(self, database: str, table: str, column: str, key_id: str) -> FieldKey: ...


def generate_field_key(id: str) -> FieldKey:
    if not valid_key_id(id):
        raise NextSQLError("invalid_argument", "invalid field key id")
    return FieldKey(id=id, material=secrets.token_bytes(KEY_SIZE))


def valid_key_id(id: str) -> bool:
    if not isinstance(id, str) or len(id) < 1 or len(id) > MAX_KEY_ID:
        return False
    return bool(_KEY_ID_RE.match(id))


class MemoryFieldKeyring:
    def __init__(self, current: FieldKey, overlap: list[FieldKey] | None = None) -> None:
        self._keys: dict[str, FieldKey] = {}
        self._revoked: set[str] = set()
        all_keys = [current] + (overlap or [])
        if len(all_keys) > MAX_KEYS:
            raise NextSQLError("invalid_argument", "too many field keys")
        seen: set[str] = set()
        for k in all_keys:
            if not isinstance(k, FieldKey) or not valid_key_id(k.id):
                raise NextSQLError("invalid_argument", "invalid field key")
            if k.id in seen:
                raise NextSQLError("invalid_argument", "duplicate field key id")
            seen.add(k.id)
            self._keys[k.id] = k
        self._current_id = current.id

    def current_field_key(self, database: str, table: str, column: str) -> FieldKey:
        if not self._current_id or self._current_id not in self._keys:
            raise NextSQLError("crypto", "current field key unavailable")
        return self._keys[self._current_id]

    def field_key(self, database: str, table: str, column: str, key_id: str) -> FieldKey:
        if key_id in self._revoked or key_id not in self._keys:
            raise NextSQLError("crypto", "field key unavailable or revoked")
        return self._keys[key_id]

    def rotate(self, key: FieldKey) -> None:
        if not isinstance(key, FieldKey) or not valid_key_id(key.id):
            raise NextSQLError("invalid_argument", "invalid field key")
        if key.id in self._revoked:
            raise NextSQLError("conflict", "cannot reuse a revoked field key id")
        if key.id not in self._keys and len(self._keys) >= MAX_KEYS:
            raise NextSQLError("exhausted", "field key limit reached")
        self._keys[key.id] = key
        self._current_id = key.id

    def revoke(self, key_id: str) -> None:
        if key_id not in self._keys and key_id not in self._revoked:
            raise NextSQLError("not_found", "unknown field key id")
        if key_id == self._current_id:
            raise NextSQLError("conflict", "cannot revoke the current field key")
        if key_id in self._revoked:
            return
        self._revoked.add(key_id)
        if key_id in self._keys:
            del self._keys[key_id]


@dataclass
class _FileKeyRecord:
    id: str
    created: datetime
    current: bool
    revoked: bool
    material: bytes


class FileFieldKeyring:
    def __init__(self, path: str, records: list[_FileKeyRecord]) -> None:
        self._path = path
        self._records = records

    @property
    def path(self) -> str:
        return self._path

    @classmethod
    def create(cls, path: str, current: FieldKey) -> FileFieldKeyring:
        if os.path.exists(path):
            raise NextSQLError("already_exists", "keyring file exists")
        if not isinstance(current, FieldKey) or not valid_key_id(current.id):
            raise NextSQLError("invalid_argument", "invalid field key")
        record = _FileKeyRecord(
            id=current.id,
            created=datetime.now(timezone.utc),
            current=True,
            revoked=False,
            material=current.material,
        )
        kr = cls(path, [record])
        kr._persist()
        return kr

    @classmethod
    def open(cls, path: str) -> FileFieldKeyring:
        try:
            with open(path, "rb") as f:
                raw = f.read()
        except FileNotFoundError:
            raise NextSQLError("not_found", "keyring file not found")
        except Exception as e:
            raise NextSQLError("io", f"failed to read keyring file: {e}")
        records = _decode_keyring(raw)
        return cls(path, records)

    def current_field_key(self, database: str, table: str, column: str) -> FieldKey:
        for r in self._records:
            if r.current and not r.revoked:
                return FieldKey(id=r.id, material=r.material)
        raise NextSQLError("crypto", "current field key unavailable")

    def field_key(self, database: str, table: str, column: str, key_id: str) -> FieldKey:
        for r in self._records:
            if r.id == key_id:
                if r.revoked:
                    raise NextSQLError("crypto", "field key unavailable or revoked")
                return FieldKey(id=r.id, material=r.material)
        raise NextSQLError("crypto", "field key unavailable or revoked")

    def rotate(self, key: FieldKey) -> None:
        if not isinstance(key, FieldKey) or not valid_key_id(key.id):
            raise NextSQLError("invalid_argument", "invalid field key")
        idx = next((i for i, r in enumerate(self._records) if r.id == key.id), -1)
        if idx >= 0:
            if self._records[idx].revoked:
                raise NextSQLError("conflict", "cannot reuse a revoked field key id")
        else:
            if len(self._records) >= MAX_KEYS:
                raise NextSQLError("exhausted", "field key limit reached")
            self._records.append(
                _FileKeyRecord(
                    id=key.id,
                    created=datetime.now(timezone.utc),
                    current=False,
                    revoked=False,
                    material=key.material,
                )
            )
            idx = len(self._records) - 1

        for r in self._records:
            r.current = False
        self._records[idx].current = True
        self._records[idx].material = key.material
        self._persist()

    def revoke(self, key_id: str) -> None:
        idx = next((i for i, r in enumerate(self._records) if r.id == key_id), -1)
        if idx < 0:
            raise NextSQLError("not_found", "unknown field key id")
        if self._records[idx].current:
            raise NextSQLError("conflict", "cannot revoke the current field key")
        if self._records[idx].revoked:
            return
        self._records[idx].revoked = True
        self._records[idx].material = b"\x00" * KEY_SIZE
        self._persist()

    def reload(self) -> None:
        try:
            with open(self._path, "rb") as f:
                raw = f.read()
            records = _decode_keyring(raw)
            self._records = records
        except Exception as e:
            if isinstance(e, NextSQLError):
                raise
            raise NextSQLError("io", f"failed to reload keyring: {e}")

    def list(self) -> list[FieldKeyInfo]:
        return [
            FieldKeyInfo(id=r.id, created=r.created, current=r.current, revoked=r.revoked)
            for r in self._records
        ]

    def _persist(self) -> None:
        raw = _encode_keyring(self._records)
        tmp = f"{self._path}.tmp.{os.getpid()}.{secrets.token_hex(4)}"
        try:
            fd = os.open(tmp, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
            with os.fdopen(fd, "wb") as f:
                f.write(raw)
                f.flush()
                os.fsync(f.fileno())
            os.replace(tmp, self._path)
        except Exception as e:
            if os.path.exists(tmp):
                try:
                    os.unlink(tmp)
                except OSError:
                    pass
            raise NextSQLError("io", f"failed to persist keyring: {e}")


def _encode_keyring(records: list[_FileKeyRecord]) -> bytes:
    if len(records) > MAX_KEYS:
        raise NextSQLError("invalid_argument", "too many field keys")
    buf = bytearray()
    buf.extend(KEYRING_MAGIC)
    buf.extend(struct.pack("<H", KEYRING_VERSION))
    buf.extend(struct.pack("<H", len(records)))
    for r in records:
        id_bytes = r.id.encode("utf-8")
        if len(id_bytes) < 1 or len(id_bytes) > MAX_KEY_ID:
            raise NextSQLError("invalid_format", "invalid field key id length")
        buf.append(len(id_bytes))
        buf.extend(id_bytes)
        buf.extend(struct.pack("<Q", int(r.created.timestamp())))
        flags = 0
        if r.current:
            flags |= FLAG_CURRENT
        if r.revoked:
            flags |= FLAG_REVOKED
        buf.append(flags)
        buf.extend(r.material)
    return bytes(buf)


def _decode_keyring(raw: bytes) -> list[_FileKeyRecord]:
    if len(raw) < 8:
        raise NextSQLError("invalid_format", "truncated keyring")
    if raw[:4] != KEYRING_MAGIC:
        raise NextSQLError("invalid_format", "bad keyring magic")
    ver, count = struct.unpack_from("<HH", raw, 4)
    if ver != KEYRING_VERSION:
        raise NextSQLError("invalid_format", "unsupported keyring version")
    if count > MAX_KEYS:
        raise NextSQLError("invalid_format", "keyring key count exceeds limit")

    off = 8
    records: list[_FileKeyRecord] = []
    seen: set[str] = set()
    current_count = 0

    for _ in range(count):
        if off >= len(raw):
            raise NextSQLError("invalid_format", "truncated keyring record")
        id_len = raw[off]
        off += 1
        if id_len < 1 or id_len > MAX_KEY_ID or off + id_len + 8 + 1 + KEY_SIZE > len(raw):
            raise NextSQLError("invalid_format", "invalid field key id length or truncated record")
        id_str = raw[off : off + id_len].decode("utf-8", errors="replace")
        off += id_len
        if not valid_key_id(id_str) or id_str in seen:
            raise NextSQLError("invalid_format", "invalid or duplicate field key id")
        seen.add(id_str)

        created_sec, flags = struct.unpack_from("<QB", raw, off)
        off += 9
        material = raw[off : off + KEY_SIZE]
        off += KEY_SIZE

        is_current = bool(flags & FLAG_CURRENT)
        is_revoked = bool(flags & FLAG_REVOKED)
        if is_current and is_revoked:
            raise NextSQLError("invalid_format", "current key cannot be revoked")
        if is_revoked and any(b != 0 for b in material):
            raise NextSQLError("invalid_format", "revoked key has non-zero material")
        if not is_revoked and all(b == 0 for b in material):
            raise NextSQLError("invalid_format", "unrevoked key has all-zero material")
        if is_current:
            current_count += 1

        records.append(
            _FileKeyRecord(
                id=id_str,
                created=datetime.fromtimestamp(created_sec, tz=timezone.utc),
                current=is_current,
                revoked=is_revoked,
                material=material,
            )
        )

    if count > 0 and current_count != 1:
        raise NextSQLError("invalid_format", "keyring must contain exactly one current key")
    if off != len(raw):
        raise NextSQLError("invalid_format", "trailing bytes after keyring records")
    return records


# ---------------------------------------------------------------------------
# Encryption and Decryption
# ---------------------------------------------------------------------------

def _validate_name(name: str, what: str) -> bytes:
    if not isinstance(name, str) or not name:
        raise NextSQLError("invalid_argument", f"{what} must not be empty")
    b = name.encode("utf-8")
    if len(b) > 0xFFFF:
        raise NextSQLError("invalid_argument", f"{what} exceeds maximum length")
    return b


def _build_aad(prefix: str, database: str, table: str, column: str, public_header: bytes) -> bytes:
    db_b = _validate_name(database, "database")
    tbl_b = _validate_name(table, "table")
    col_b = _validate_name(column, "column")
    aad = bytearray(prefix.encode("utf-8"))
    aad.extend(struct.pack("<H", len(db_b)))
    aad.extend(db_b)
    aad.extend(struct.pack("<H", len(tbl_b)))
    aad.extend(tbl_b)
    aad.extend(struct.pack("<H", len(col_b)))
    aad.extend(col_b)
    aad.extend(public_header)
    return bytes(aad)


def _build_public_header(version: int, suite: int, key_id: str, field_type: FieldType) -> bytes:
    key_id_b = key_id.encode("utf-8")
    buf = bytearray()
    buf.append(version)
    buf.append(suite)
    buf.append(len(key_id_b))
    buf.extend(key_id_b)
    buf.append(field_type.kind)
    buf.extend(struct.pack("<HH", field_type.precision, field_type.scale))
    buf.append(field_type.vec_elem)
    return bytes(buf)


def _validate_field_type(ft: FieldType) -> None:
    if ft.kind == KIND_DECIMAL:
        if ft.precision < 1 or ft.precision > 38 or ft.scale < 0 or ft.scale > ft.precision:
            raise NextSQLError("invalid_argument", "unsupported client-encrypted type")
        return
    valid_kinds = {
        KIND_UUID,
        KIND_STRING,
        KIND_TEXT,
        KIND_BLOB,
        KIND_TIMESTAMPTZ,
        KIND_JSON,
        KIND_BOOL,
        KIND_INT8,
        KIND_INT16,
        KIND_INT32,
        KIND_INT64,
        KIND_UINT8,
        KIND_UINT16,
        KIND_UINT32,
        KIND_UINT64,
    }
    if ft.kind not in valid_kinds or ft.precision != 0 or ft.scale != 0 or ft.vec_elem != 0:
        raise NextSQLError("invalid_argument", "unsupported client-encrypted type")


def _encode_scalar(ft: FieldType, value: Any) -> bytes:
    if ft.kind == KIND_UUID:
        import uuid as _uuid
        if isinstance(value, _uuid.UUID):
            return value.bytes
        if isinstance(value, str):
            try:
                return _uuid.UUID(value).bytes
            except ValueError:
                pass
        if isinstance(value, (bytes, bytearray)) and len(value) == 16:
            return bytes(value)
        raise NextSQLError("invalid_argument", "invalid UUID for encrypted field")

    elif ft.kind in (KIND_STRING, KIND_TEXT):
        if not isinstance(value, str):
            raise NextSQLError("invalid_argument", "encrypted string value must be a string")
        b = value.encode("utf-8")
        return struct.pack("<I", len(b)) + b

    elif ft.kind == KIND_BLOB:
        if not isinstance(value, (bytes, bytearray)):
            raise NextSQLError("invalid_argument", "encrypted BLOB value must be bytes")
        b = bytes(value)
        return struct.pack("<I", len(b)) + b

    elif ft.kind == KIND_BOOL:
        if not isinstance(value, bool):
            raise NextSQLError("invalid_argument", "encrypted BOOL value must be boolean")
        return b"\x01" if value else b"\x00"

    elif ft.kind in (KIND_INT8, KIND_INT16, KIND_INT32, KIND_INT64):
        if not isinstance(value, int) or isinstance(value, bool):
            raise NextSQLError("invalid_argument", "encrypted int value must be an integer")
        ranges = {
            KIND_INT8: (-0x80, 0x7F, "<b"),
            KIND_INT16: (-0x8000, 0x7FFF, "<h"),
            KIND_INT32: (-0x80000000, 0x7FFFFFFF, "<i"),
            KIND_INT64: (-0x8000000000000000, 0x7FFFFFFFFFFFFFFF, "<q"),
        }
        lo, hi, fmt = ranges[ft.kind]
        if value < lo or value > hi:
            raise NextSQLError("invalid_argument", "encrypted int value out of range")
        return struct.pack(fmt, value)

    elif ft.kind in (KIND_UINT8, KIND_UINT16, KIND_UINT32, KIND_UINT64):
        if not isinstance(value, int) or isinstance(value, bool):
            raise NextSQLError("invalid_argument", "encrypted uint value must be an integer")
        ranges = {
            KIND_UINT8: (0xFF, "<B"),
            KIND_UINT16: (0xFFFF, "<H"),
            KIND_UINT32: (0xFFFFFFFF, "<I"),
            KIND_UINT64: (0xFFFFFFFFFFFFFFFF, "<Q"),
        }
        hi, fmt = ranges[ft.kind]
        if value < 0 or value > hi:
            raise NextSQLError("invalid_argument", "encrypted uint value out of range")
        return struct.pack(fmt, value)

    elif ft.kind == KIND_TIMESTAMPTZ:
        if isinstance(value, datetime):
            ns = int(value.timestamp() * 1_000_000_000)
            return struct.pack("<q", ns)
        if isinstance(value, int):
            return struct.pack("<q", value)
        raise NextSQLError("invalid_argument", "encrypted TIMESTAMPTZ value must be datetime or int nanos")

    elif ft.kind == KIND_DECIMAL:
        s = str(value)
        return encode_decimal(s)

    elif ft.kind == KIND_JSON:
        import json as _json
        if isinstance(value, str):
            payload = value.encode("utf-8")
        else:
            payload = _json.dumps(value, separators=(",", ":")).encode("utf-8")
        return struct.pack("<I", len(payload)) + payload

    raise NextSQLError("invalid_argument", "unsupported client-encrypted type")


def _decode_scalar(ft: FieldType, raw: bytes) -> Any:
    if ft.kind == KIND_UUID:
        import uuid as _uuid
        if len(raw) != 16:
            raise NextSQLError("invalid_format", "invalid encrypted UUID payload")
        return _uuid.UUID(bytes=raw)

    elif ft.kind in (KIND_STRING, KIND_TEXT):
        if len(raw) < 4:
            raise NextSQLError("invalid_format", "truncated string payload")
        length = struct.unpack_from("<I", raw, 0)[0]
        if len(raw) != 4 + length:
            raise NextSQLError("invalid_format", "string payload length mismatch")
        return raw[4:].decode("utf-8")

    elif ft.kind == KIND_BLOB:
        if len(raw) < 4:
            raise NextSQLError("invalid_format", "truncated blob payload")
        length = struct.unpack_from("<I", raw, 0)[0]
        if len(raw) != 4 + length:
            raise NextSQLError("invalid_format", "blob payload length mismatch")
        return raw[4:]

    elif ft.kind == KIND_BOOL:
        if len(raw) != 1:
            raise NextSQLError("invalid_format", "invalid encrypted bool payload")
        return raw[0] != 0

    elif ft.kind in (KIND_INT8, KIND_INT16, KIND_INT32, KIND_INT64):
        fmts = {KIND_INT8: (1, "<b"), KIND_INT16: (2, "<h"), KIND_INT32: (4, "<i"), KIND_INT64: (8, "<q")}
        w, fmt = fmts[ft.kind]
        if len(raw) != w:
            raise NextSQLError("invalid_format", "invalid encrypted int payload")
        return struct.unpack(fmt, raw)[0]

    elif ft.kind in (KIND_UINT8, KIND_UINT16, KIND_UINT32, KIND_UINT64):
        fmts = {KIND_UINT8: (1, "<B"), KIND_UINT16: (2, "<H"), KIND_UINT32: (4, "<I"), KIND_UINT64: (8, "<Q")}
        w, fmt = fmts[ft.kind]
        if len(raw) != w:
            raise NextSQLError("invalid_format", "invalid encrypted uint payload")
        return struct.unpack(fmt, raw)[0]

    elif ft.kind == KIND_TIMESTAMPTZ:
        if len(raw) != 8:
            raise NextSQLError("invalid_format", "invalid encrypted timestamptz payload")
        ns = struct.unpack("<q", raw)[0]
        return datetime.fromtimestamp(ns / 1_000_000_000, tz=timezone.utc)

    elif ft.kind == KIND_DECIMAL:
        if len(raw) < 4:
            raise NextSQLError("invalid_format", "truncated decimal payload")
        length = struct.unpack_from("<I", raw, 0)[0]
        if len(raw) != 4 + length:
            raise NextSQLError("invalid_format", "decimal payload length mismatch")
        return decode_decimal(raw[4:])

    elif ft.kind == KIND_JSON:
        import json as _json
        if len(raw) < 4:
            raise NextSQLError("invalid_format", "truncated json payload")
        length = struct.unpack_from("<I", raw, 0)[0]
        if len(raw) != 4 + length:
            raise NextSQLError("invalid_format", "json payload length mismatch")
        return _json.loads(raw[4:].decode("utf-8"))

    raise NextSQLError("invalid_format", "unsupported client-encrypted type")


# ---------------------------------------------------------------------------
# Cryptographic Primitives (AES-GCM & AES-SIV)
# ---------------------------------------------------------------------------

def _hkdf_sha256(ikm: bytes, info: bytes, length: int = 32) -> bytes:
    import hmac as _hmac
    import hashlib as _hashlib
    prk = _hmac.new(b"", ikm, _hashlib.sha256).digest()
    t = b""
    okm = b""
    i = 1
    while len(okm) < length:
        t = _hmac.new(prk, t + info + bytes([i]), _hashlib.sha256).digest()
        okm += t
        i += 1
    return okm[:length]


# Pure-Python AES / SIV / GCM implementations for zero-dependency execution
_SBOX = [
    0x63, 0x7C, 0x77, 0x7B, 0xF2, 0x6B, 0x6F, 0xC5, 0x30, 0x01, 0x67, 0x2B, 0xFE, 0xD7, 0xAB, 0x76,
    0xCA, 0x82, 0xC9, 0x7D, 0xFA, 0x59, 0x47, 0xF0, 0xAD, 0xD4, 0xA2, 0xAF, 0x9C, 0xA4, 0x72, 0xC0,
    0xB7, 0xFD, 0x93, 0x26, 0x36, 0x3F, 0xF7, 0xCC, 0x34, 0xA5, 0xE5, 0xF1, 0x71, 0xD8, 0x31, 0x15,
    0x04, 0xC7, 0x23, 0xC3, 0x18, 0x96, 0x05, 0x9A, 0x07, 0x12, 0x80, 0xE2, 0xEB, 0x27, 0xB2, 0x75,
    0x09, 0x83, 0x2C, 0x1A, 0x1B, 0x6E, 0x5A, 0xA0, 0x52, 0x3B, 0xD6, 0xB3, 0x29, 0xE3, 0x2F, 0x84,
    0x53, 0xD1, 0x00, 0xED, 0x20, 0xFC, 0xB1, 0x5B, 0x6A, 0xCB, 0xBE, 0x39, 0x4A, 0x4C, 0x58, 0xCF,
    0xD0, 0xEF, 0xAA, 0xFB, 0x43, 0x4D, 0x33, 0x85, 0x45, 0xF9, 0x02, 0x7F, 0x50, 0x3C, 0x9F, 0xA8,
    0x51, 0xA3, 0x40, 0x8F, 0x92, 0x9D, 0x38, 0xF5, 0xBC, 0xB6, 0xDA, 0x21, 0x10, 0xFF, 0xF3, 0xD2,
    0xCD, 0x0C, 0x13, 0xEC, 0x5F, 0x97, 0x44, 0x17, 0xC4, 0xA7, 0x7E, 0x3D, 0x64, 0x5D, 0x19, 0x73,
    0x60, 0x81, 0x4F, 0xDC, 0x22, 0x2A, 0x90, 0x88, 0x46, 0xEE, 0xB8, 0x14, 0xDE, 0x5E, 0x0B, 0xDB,
    0xE0, 0x32, 0x3A, 0x0A, 0x49, 0x06, 0x24, 0x5C, 0xC2, 0xD3, 0xAC, 0x62, 0x91, 0x95, 0xE4, 0x79,
    0xE7, 0xC8, 0x37, 0x6D, 0x8D, 0xD5, 0x4E, 0xA9, 0x6C, 0x56, 0xF4, 0xEA, 0x65, 0x7A, 0xAE, 0x08,
    0xBA, 0x78, 0x25, 0x2E, 0x1C, 0xA6, 0xB4, 0xC6, 0xE8, 0xDD, 0x74, 0x1F, 0x4B, 0xBD, 0x8B, 0x8A,
    0x70, 0x3E, 0xB5, 0x66, 0x48, 0x03, 0xF6, 0x0E, 0x61, 0x35, 0x57, 0xB9, 0x86, 0xC1, 0x1D, 0x9E,
    0xE1, 0xF8, 0x98, 0x11, 0x69, 0xD9, 0x8E, 0x94, 0x9B, 0x1E, 0x87, 0xE9, 0xCE, 0x55, 0x28, 0xDF,
    0x8C, 0xA1, 0x89, 0x0D, 0xBF, 0xE6, 0x42, 0x68, 0x41, 0x99, 0x2D, 0x0F, 0xB0, 0x54, 0xBB, 0x16,
]
_RCON = [0x01, 0x02, 0x04, 0x08, 0x10, 0x20, 0x40, 0x80, 0x1B, 0x36]


def _aes_key_expansion(key: bytes) -> list[int]:
    nk = len(key) // 4
    nr = nk + 6
    w = []
    for i in range(nk):
        w.append(int.from_bytes(key[4 * i : 4 * i + 4], "big"))
    for i in range(nk, 4 * (nr + 1)):
        temp = w[i - 1]
        if i % nk == 0:
            rot = ((temp << 8) & 0xFFFFFFFF) | ((temp >> 24) & 0xFF)
            sub = (
                (_SBOX[(rot >> 24) & 0xFF] << 24)
                | (_SBOX[(rot >> 16) & 0xFF] << 16)
                | (_SBOX[(rot >> 8) & 0xFF] << 8)
                | _SBOX[rot & 0xFF]
            )
            temp = sub ^ (_RCON[(i // nk) - 1] << 24)
        elif nk > 6 and i % nk == 4:
            temp = (
                (_SBOX[(temp >> 24) & 0xFF] << 24)
                | (_SBOX[(temp >> 16) & 0xFF] << 16)
                | (_SBOX[(temp >> 8) & 0xFF] << 8)
                | _SBOX[temp & 0xFF]
            )
        w.append(w[i - nk] ^ temp)
    return w


def _xt(a: int) -> int:
    return ((a << 1) ^ 0x1B) & 0xFF if a & 0x80 else (a << 1) & 0xFF


def _aes_encrypt_block(pt: bytes, w: list[int]) -> bytes:
    nr = len(w) // 4 - 1
    state = [list(pt[i * 4 : (i + 1) * 4]) for i in range(4)]
    s = [[state[j][i] for j in range(4)] for i in range(4)]
    for c in range(4):
        rk = w[c]
        s[0][c] ^= (rk >> 24) & 0xFF
        s[1][c] ^= (rk >> 16) & 0xFF
        s[2][c] ^= (rk >> 8) & 0xFF
        s[3][c] ^= rk & 0xFF
    for r_idx in range(1, nr + 1):
        for r in range(4):
            for c in range(4):
                s[r][c] = _SBOX[s[r][c]]
        s[1] = s[1][1:] + s[1][:1]
        s[2] = s[2][2:] + s[2][:2]
        s[3] = s[3][3:] + s[3][:3]
        if r_idx < nr:
            for c in range(4):
                a0, a1, a2, a3 = s[0][c], s[1][c], s[2][c], s[3][c]
                s[0][c] = _xt(a0) ^ _xt(a1) ^ a1 ^ a2 ^ a3
                s[1][c] = a0 ^ _xt(a1) ^ _xt(a2) ^ a2 ^ a3
                s[2][c] = a0 ^ a1 ^ _xt(a2) ^ _xt(a3) ^ a3
                s[3][c] = _xt(a0) ^ a0 ^ a1 ^ a2 ^ _xt(a3)
        for c in range(4):
            rk = w[r_idx * 4 + c]
            s[0][c] ^= (rk >> 24) & 0xFF
            s[1][c] ^= (rk >> 16) & 0xFF
            s[2][c] ^= (rk >> 8) & 0xFF
            s[3][c] ^= rk & 0xFF
    res = bytearray(16)
    for c in range(4):
        for r in range(4):
            res[c * 4 + r] = s[r][c]
    return bytes(res)


def _dbl(block: bytes) -> bytes:
    out = bytearray(16)
    carry = 0
    for i in range(15, -1, -1):
        b = block[i]
        out[i] = ((b << 1) & 0xFF) | carry
        carry = (b >> 7) & 1
    if carry != 0:
        out[15] ^= 0x87
    return bytes(out)


def _xor_bytes(a: bytes, b: bytes) -> bytes:
    return bytes(x ^ y for x, y in zip(a, b))


def _cmac(w: list[int], msg: bytes) -> bytes:
    k1 = _dbl(_aes_encrypt_block(b"\x00" * 16, w))
    k2 = _dbl(k1)
    n = len(msg)
    blocks = max(1, (n + 15) // 16)
    if n > 0 and n % 16 == 0:
        last = _xor_bytes(msg[n - 16 :], k1)
    else:
        rem = n % 16
        pad = msg[n - rem :] + b"\x80" + (b"\x00" * (15 - rem))
        last = _xor_bytes(pad, k2)
    y = b"\x00" * 16
    for i in range(blocks - 1):
        blk = msg[i * 16 : (i + 1) * 16]
        y = _aes_encrypt_block(_xor_bytes(y, blk), w)
    return _aes_encrypt_block(_xor_bytes(y, last), w)


def _s2v(w: list[int], vectors: list[bytes]) -> bytes:
    d = _cmac(w, b"\x00" * 16)
    for v in vectors[:-1]:
        d = _xor_bytes(_dbl(d), _cmac(w, v))
    last = vectors[-1]
    if len(last) >= 16:
        off = len(last) - 16
        t = last[:off] + _xor_bytes(last[off:], d)
    else:
        pad = last + b"\x80" + (b"\x00" * (15 - len(last)))
        t = _xor_bytes(_dbl(d), pad)
    return _cmac(w, t)


def _ctr_crypt(w: list[int], iv: bytes, data: bytes) -> bytes:
    out = bytearray(len(data))
    ctr = bytearray(iv)
    for i in range(0, len(data), 16):
        pad = _aes_encrypt_block(bytes(ctr), w)
        chunk = data[i : i + 16]
        for j in range(len(chunk)):
            out[i + j] = chunk[j] ^ pad[j]
        # Increment bottom 64 bits modulo 2^64
        for k in range(15, 7, -1):
            ctr[k] = (ctr[k] + 1) & 0xFF
            if ctr[k] != 0:
                break
    return bytes(out)


# GHASH for pure-Python fallback AES-GCM
def _gf128_mul(x: int, y: int) -> int:
    r = 0xE1000000000000000000000000000000
    z = 0
    v = x
    for i in range(128):
        if (y >> (127 - i)) & 1:
            z ^= v
        if v & 1:
            v = (v >> 1) ^ r
        else:
            v >>= 1
    return z


def _ghash(h_bytes: bytes, aad: bytes, ct: bytes) -> bytes:
    h = int.from_bytes(h_bytes, "big")
    y = 0
    # Process AAD blocks
    for i in range(0, len(aad), 16):
        block = aad[i : i + 16].ljust(16, b"\x00")
        y = _gf128_mul(y ^ int.from_bytes(block, "big"), h)
    # Process CT blocks
    for i in range(0, len(ct), 16):
        block = ct[i : i + 16].ljust(16, b"\x00")
        y = _gf128_mul(y ^ int.from_bytes(block, "big"), h)
    # Length block: len(aad) in bits (64-bit), len(ct) in bits (64-bit)
    len_block = struct.pack(">QQ", len(aad) * 8, len(ct) * 8)
    y = _gf128_mul(y ^ int.from_bytes(len_block, "big"), h)
    return y.to_bytes(16, "big")


def _aes_gcm_encrypt(key: bytes, nonce: bytes, plaintext: bytes, aad: bytes) -> bytes:
    if _HAS_CRYPTOGRAPHY:
        return AESGCM(key).encrypt(nonce, plaintext, aad)
    w = _aes_key_expansion(key)
    h = _aes_encrypt_block(b"\x00" * 16, w)
    j0 = nonce + b"\x00\x00\x00\x01"
    # Encrypt counter blocks starting at J0 + 1
    ctr = bytearray(j0)
    for k in range(15, 11, -1):
        ctr[k] = (ctr[k] + 1) & 0xFF
        if ctr[k] != 0:
            break
    ct = bytearray(len(plaintext))
    for i in range(0, len(plaintext), 16):
        pad = _aes_encrypt_block(bytes(ctr), w)
        chunk = plaintext[i : i + 16]
        for j in range(len(chunk)):
            ct[i + j] = chunk[j] ^ pad[j]
        for k in range(15, 11, -1):
            ctr[k] = (ctr[k] + 1) & 0xFF
            if ctr[k] != 0:
                break
    s = _ghash(h, aad, bytes(ct))
    tag = _xor_bytes(s, _aes_encrypt_block(j0, w))
    return bytes(ct) + tag


def _aes_gcm_decrypt(key: bytes, nonce: bytes, ct_and_tag: bytes, aad: bytes) -> bytes:
    if len(ct_and_tag) < TAG_SIZE:
        raise NextSQLError("crypto", "ciphertext authentication failed")
    if _HAS_CRYPTOGRAPHY:
        try:
            return AESGCM(key).decrypt(nonce, ct_and_tag, aad)
        except Exception:
            raise NextSQLError("crypto", "ciphertext authentication failed")
    ct = ct_and_tag[:-TAG_SIZE]
    tag = ct_and_tag[-TAG_SIZE:]
    w = _aes_key_expansion(key)
    h = _aes_encrypt_block(b"\x00" * 16, w)
    j0 = nonce + b"\x00\x00\x00\x01"
    s = _ghash(h, aad, ct)
    expected_tag = _xor_bytes(s, _aes_encrypt_block(j0, w))
    if not secrets.compare_digest(tag, expected_tag):
        raise NextSQLError("crypto", "ciphertext authentication failed")
    # Decrypt
    ctr = bytearray(j0)
    for k in range(15, 11, -1):
        ctr[k] = (ctr[k] + 1) & 0xFF
        if ctr[k] != 0:
            break
    pt = bytearray(len(ct))
    for i in range(0, len(ct), 16):
        pad = _aes_encrypt_block(bytes(ctr), w)
        chunk = ct[i : i + 16]
        for j in range(len(chunk)):
            pt[i + j] = chunk[j] ^ pad[j]
        for k in range(15, 11, -1):
            ctr[k] = (ctr[k] + 1) & 0xFF
            if ctr[k] != 0:
                break
    return bytes(pt)


def _aes_siv_encrypt(key: bytes, plaintext: bytes, aad: bytes) -> bytes:
    if _HAS_CRYPTOGRAPHY:
        return AESSIV(key).encrypt(plaintext, [aad])
    k1, k2 = key[:16], key[16:]
    w1 = _aes_key_expansion(k1)
    w2 = _aes_key_expansion(k2)
    v = _s2v(w1, [aad, plaintext])
    q = bytearray(v)
    q[8] &= 0x7F
    q[12] &= 0x7F
    c = _ctr_crypt(w2, bytes(q), plaintext)
    return v + c


def _aes_siv_decrypt(key: bytes, ciphertext: bytes, aad: bytes) -> bytes:
    if len(ciphertext) < 16:
        raise NextSQLError("crypto", "ciphertext authentication failed")
    if _HAS_CRYPTOGRAPHY:
        try:
            return AESSIV(key).decrypt(ciphertext, [aad])
        except Exception:
            raise NextSQLError("crypto", "ciphertext authentication failed")
    k1, k2 = key[:16], key[16:]
    w1 = _aes_key_expansion(k1)
    w2 = _aes_key_expansion(k2)
    v = ciphertext[:16]
    c = ciphertext[16:]
    q = bytearray(v)
    q[8] &= 0x7F
    q[12] &= 0x7F
    pt = _ctr_crypt(w2, bytes(q), c)
    expected_v = _s2v(w1, [aad, pt])
    if not secrets.compare_digest(v, expected_v):
        raise NextSQLError("crypto", "ciphertext authentication failed")
    return pt


# ---------------------------------------------------------------------------
# Public Functions
# ---------------------------------------------------------------------------

def encrypt_field(
    provider: FieldKeyProvider,
    database: str,
    table: str,
    column: str,
    field_type: FieldType,
    value: Any,
) -> str | None:
    if value is None:
        return None
    _validate_field_type(field_type)
    plain = _encode_scalar(field_type, value)
    if len(plain) > MAX_PLAINTEXT:
        raise NextSQLError("exhausted", "plaintext exceeds field limit")
    try:
        key = provider.current_field_key(database, table, column)
    except NextSQLError:
        raise
    except Exception as e:
        raise NextSQLError("crypto", f"field key unavailable: {e}")

    public_header = _build_public_header(VERSION, SUITE_AES_256_GCM, key.id, field_type)
    nonce = secrets.token_bytes(NONCE_SIZE)
    aad = _build_aad(PREFIX, database, table, column, public_header)
    sealed = _aes_gcm_encrypt(key.material, nonce, plain, aad)
    body = public_header + nonce + sealed
    return PREFIX + base64.urlsafe_b64encode(body).decode("ascii").rstrip("=")


def decrypt_field(
    provider: FieldKeyProvider,
    database: str,
    table: str,
    column: str,
    expected_type: FieldType,
    ciphertext: str | None,
) -> Any:
    if ciphertext is None:
        return None
    _validate_field_type(expected_type)
    parsed = inspect_field(ciphertext)
    if parsed["mode"] != "randomized":
        raise NextSQLError("invalid_format", "deterministic ciphertext requires decrypt_field_deterministic")
    if parsed["type"] != expected_type:
        raise NextSQLError("invalid_format", "encrypted logical type mismatch")

    try:
        key = provider.field_key(database, table, column, parsed["key_id"])
    except NextSQLError:
        raise
    except Exception as e:
        raise NextSQLError("crypto", f"field key unavailable or revoked: {e}")

    body = parsed["body"]
    header_len = parsed["header_length"]
    public_header = body[: header_len - NONCE_SIZE ]
    nonce = body[header_len - NONCE_SIZE : header_len]
    ct_and_tag = body[header_len:]
    aad = _build_aad(PREFIX, database, table, column, public_header)

    plain = _aes_gcm_decrypt(key.material, nonce, ct_and_tag, aad)
    if len(plain) > MAX_PLAINTEXT:
        raise NextSQLError("invalid_format", "plaintext exceeds field limit")
    return _decode_scalar(expected_type, plain)


def encrypt_field_deterministic(
    provider: FieldKeyProvider,
    database: str,
    table: str,
    column: str,
    field_type: FieldType,
    value: Any,
) -> str | None:
    if value is None:
        return None
    _validate_field_type(field_type)
    plain = _encode_scalar(field_type, value)
    if len(plain) > MAX_PLAINTEXT:
        raise NextSQLError("exhausted", "plaintext exceeds field limit")
    try:
        key = provider.current_field_key(database, table, column)
    except NextSQLError:
        raise
    except Exception as e:
        raise NextSQLError("crypto", f"field key unavailable: {e}")

    public_header = _build_public_header(DETERMINISTIC_VERSION, SUITE_AES_SIV, key.id, field_type)
    aad = _build_aad(DETERMINISTIC_PREFIX, database, table, column, public_header)
    derived = _hkdf_sha256(key.material, b"NextSQL NSCE2 AES-SIV v2", 32)
    sealed = _aes_siv_encrypt(derived, plain, aad)
    body = public_header + sealed
    return DETERMINISTIC_PREFIX + base64.urlsafe_b64encode(body).decode("ascii").rstrip("=")


def decrypt_field_deterministic(
    provider: FieldKeyProvider,
    database: str,
    table: str,
    column: str,
    expected_type: FieldType,
    ciphertext: str | None,
) -> Any:
    if ciphertext is None:
        return None
    _validate_field_type(expected_type)
    parsed = inspect_field(ciphertext)
    if parsed["mode"] != "deterministic":
        raise NextSQLError("invalid_format", "randomized ciphertext requires decrypt_field")
    if parsed["type"] != expected_type:
        raise NextSQLError("invalid_format", "encrypted logical type mismatch")

    try:
        key = provider.field_key(database, table, column, parsed["key_id"])
    except NextSQLError:
        raise
    except Exception as e:
        raise NextSQLError("crypto", f"field key unavailable or revoked: {e}")

    body = parsed["body"]
    header_len = parsed["header_length"]
    public_header = body[:header_len]
    sealed = body[header_len:]
    aad = _build_aad(DETERMINISTIC_PREFIX, database, table, column, public_header)
    derived = _hkdf_sha256(key.material, b"NextSQL NSCE2 AES-SIV v2", 32)
    plain = _aes_siv_decrypt(derived, sealed, aad)
    if len(plain) > MAX_PLAINTEXT:
        raise NextSQLError("invalid_format", "plaintext exceeds field limit")
    return _decode_scalar(expected_type, plain)


def inspect_field(ciphertext: str) -> dict[str, Any]:
    if not isinstance(ciphertext, str):
        raise NextSQLError("invalid_format", "invalid client ciphertext")
    if ciphertext.startswith(PREFIX):
        prefix = PREFIX
        version = VERSION
        suite = SUITE_AES_256_GCM
        nonce_size = NONCE_SIZE
        mode = "randomized"
    elif ciphertext.startswith(DETERMINISTIC_PREFIX):
        prefix = DETERMINISTIC_PREFIX
        version = DETERMINISTIC_VERSION
        suite = SUITE_AES_SIV
        nonce_size = 0
        mode = "deterministic"
    else:
        raise NextSQLError("invalid_format", "invalid client ciphertext prefix")

    encoded = ciphertext[len(prefix) :]
    max_encoded = (MAX_PLAINTEXT + 101) * 4 // 3 + 4
    if not encoded or len(encoded) > max_encoded or len(encoded) % 4 == 1 or not re.match(r"^[A-Za-z0-9_-]+$", encoded):
        raise NextSQLError("invalid_format", "client ciphertext length out of range")

    padded = encoded + "=" * ((4 - len(encoded) % 4) % 4)
    try:
        body = base64.urlsafe_b64decode(padded)
    except Exception:
        raise NextSQLError("invalid_format", "invalid client ciphertext encoding")

    min_len = 3 + 1 + 6 + nonce_size + TAG_SIZE
    if len(body) < min_len or body[0] != version or body[1] != suite:
        raise NextSQLError("invalid_format", "unsupported or truncated client ciphertext")

    key_id_len = body[2]
    if key_id_len < 1 or key_id_len > MAX_KEY_ID or len(body) < 3 + key_id_len + 6 + nonce_size + TAG_SIZE:
        raise NextSQLError("invalid_format", "invalid field key id length")

    key_id = body[3 : 3 + key_id_len].decode("utf-8", errors="replace")
    if not valid_key_id(key_id):
        raise NextSQLError("invalid_format", "invalid field key id")

    off = 3 + key_id_len
    kind = body[off]
    precision, scale = struct.unpack_from("<HH", body, off + 1)
    vec_elem = body[off + 5]
    ft = FieldType(kind=kind, precision=precision, scale=scale, vec_elem=vec_elem)
    _validate_field_type(ft)

    header_len = off + 6 + nonce_size
    return {
        "key_id": key_id,
        "type": ft,
        "body": body,
        "header_length": header_len,
        "mode": mode,
    }
