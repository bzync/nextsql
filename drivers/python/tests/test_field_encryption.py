import base64
import os
import sys
import tempfile
import unittest
import uuid
from datetime import datetime, timezone
from decimal import Decimal
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent.parent))

import nextsql
from nextsql import (
    FieldKey,
    FieldType,
    FileFieldKeyring,
    MemoryFieldKeyring,
    NextSQLError,
    decrypt_field,
    decrypt_field_deterministic,
    encrypt_field,
    encrypt_field_deterministic,
    generate_field_key,
    inspect_field,
)


class TestFieldEncryption(unittest.TestCase):
    def test_generate_field_key(self) -> None:
        k = generate_field_key("test-key.1_a")
        self.assertEqual(k.id, "test-key.1_a")
        self.assertEqual(len(k.material), 32)
        self.assertTrue(any(b != 0 for b in k.material))

        with self.assertRaises(NextSQLError):
            generate_field_key("invalid key id with spaces!")
        with self.assertRaises(NextSQLError):
            generate_field_key("")
        with self.assertRaises(NextSQLError):
            generate_field_key("a" * 65)

    def test_memory_field_keyring(self) -> None:
        k1 = FieldKey("k1", b"\x01" * 32)
        k2 = FieldKey("k2", b"\x02" * 32)
        ring = MemoryFieldKeyring(k1, [k2])

        cur = ring.current_field_key("db", "tbl", "col")
        self.assertEqual(cur.id, "k1")
        self.assertEqual(cur.material, b"\x01" * 32)

        resolved_k2 = ring.field_key("db", "tbl", "col", "k2")
        self.assertEqual(resolved_k2.id, "k2")

        # Rotate to k2
        k3 = FieldKey("k3", b"\x03" * 32)
        ring.rotate(k3)
        self.assertEqual(ring.current_field_key("db", "tbl", "col").id, "k3")
        # k1 is still readable
        self.assertEqual(ring.field_key("db", "tbl", "col", "k1").id, "k1")

        # Revoke k1
        ring.revoke("k1")
        with self.assertRaises(NextSQLError):
            ring.field_key("db", "tbl", "col", "k1")

        # Cannot revoke current
        with self.assertRaises(NextSQLError):
            ring.revoke("k3")

        # Cannot reuse revoked ID
        with self.assertRaises(NextSQLError):
            ring.rotate(FieldKey("k1", b"\x04" * 32))

    def test_file_field_keyring_lifecycle(self) -> None:
        with tempfile.TemporaryDirectory() as td:
            p = os.path.join(td, "test.nsfk")
            k1 = FieldKey("v1", b"\x01" * 32)
            kr = FileFieldKeyring.create(p, k1)

            # Re-creating same path fails
            with self.assertRaises(NextSQLError):
                FileFieldKeyring.create(p, FieldKey("v2", b"\x02" * 32))

            # Rotate to v2
            k2 = FieldKey("v2", b"\x02" * 32)
            kr.rotate(k2)
            self.assertEqual(kr.current_field_key("db", "tbl", "col").id, "v2")

            # Reopen from disk
            reopened = FileFieldKeyring.open(p)
            self.assertEqual(reopened.current_field_key("db", "tbl", "col").id, "v2")
            self.assertEqual(reopened.field_key("db", "tbl", "col", "v1").id, "v1")

            # Revoke v1
            kr.revoke("v1")
            with self.assertRaises(NextSQLError):
                kr.field_key("db", "tbl", "col", "v1")

            # Reopened reflects revocation and zeroed material
            reopened2 = FileFieldKeyring.open(p)
            with self.assertRaises(NextSQLError):
                reopened2.field_key("db", "tbl", "col", "v1")

            # Cannot reuse revoked ID
            with self.assertRaises(NextSQLError):
                kr.rotate(FieldKey("v1", b"\x05" * 32))

            # Check list summaries
            lst = kr.list()
            self.assertEqual(len(lst), 2)
            v1_info = next(x for x in lst if x.id == "v1")
            self.assertTrue(v1_info.revoked)
            self.assertFalse(v1_info.current)
            v2_info = next(x for x in lst if x.id == "v2")
            self.assertFalse(v2_info.revoked)
            self.assertTrue(v2_info.current)

    def test_file_field_keyring_go_interop_fixture(self) -> None:
        # Cross-driver fixture from Go/Node.js:
        # One revoked key "v1" (zeroed material) and one current key "v2" (32 x 0x02)
        fixture_b64 = (
            "TlNGSwEAAgACdjEA8VNlAAAAAAIAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAJ2MmTxU2UAAAAAAQICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgIC"
        )
        raw = base64.b64decode(fixture_b64)
        with tempfile.TemporaryDirectory() as td:
            p = os.path.join(td, "go.nsfk")
            with open(p, "wb") as f:
                f.write(raw)

            kr = FileFieldKeyring.open(p)
            current = kr.current_field_key("app", "accounts", "secret")
            self.assertEqual(current.id, "v2")
            self.assertEqual(current.material, b"\x02" * 32)

            with self.assertRaises(NextSQLError):
                kr.field_key("app", "accounts", "secret", "v1")

            lst = kr.list()
            self.assertEqual(len(lst), 2)
            self.assertEqual(lst[0].id, "v1")
            self.assertTrue(lst[0].revoked)
            self.assertEqual(lst[1].id, "v2")
            self.assertTrue(lst[1].current)

    def test_cross_driver_interop_fixtures(self) -> None:
        v1 = FieldKey("v1", b"\x01" * 32)
        ring = MemoryFieldKeyring(v1)

        # 1. NSCE1 fixture produced by Go driver
        go_ciphertext = "NSCE1.AQECdjEDAAAAAABEeyxf_quGP5And9z0FmNijEp3uSiDspby_y1zIxe9L1R-llGtWQxh"
        dec = decrypt_field(ring, "app", "accounts", "secret", FieldType.TEXT, go_ciphertext)
        self.assertEqual(dec, "portable")

        # 2. NSCE2 deterministic fixture produced by Node/Bun drivers
        node_ciphertext = "NSCE2.AgICdjEDAAAAAAB5DwopEyQ-MiLxexaaMdKrTdw36cUvjClQFjVf"
        py_ciphertext = encrypt_field_deterministic(ring, "app", "accounts", "email", FieldType.TEXT, "portable")
        self.assertEqual(py_ciphertext, node_ciphertext)
        dec_det = decrypt_field_deterministic(ring, "app", "accounts", "email", FieldType.TEXT, node_ciphertext)
        self.assertEqual(dec_det, "portable")

    def test_nsce1_roundtrip_all_types(self) -> None:
        key = FieldKey("app-v1", b"\x42" * 32)
        ring = MemoryFieldKeyring(key)
        db, tbl, col = "testdb", "users", "secret"

        test_cases = [
            (FieldType.STRING, "Hello World!"),
            (FieldType.TEXT, "Longer text paragraph with unicode: \u2764\ufe0f"),
            (FieldType.BLOB, b"\x00\xff\xfe\x01\x02\x03binary"),
            (FieldType.BOOL, True),
            (FieldType.BOOL, False),
            (FieldType.INT8, -42),
            (FieldType.INT16, -1000),
            (FieldType.INT32, -100000),
            (FieldType.INT64, -9000000000000),
            (FieldType.UINT8, 200),
            (FieldType.UINT16, 50000),
            (FieldType.UINT32, 3000000000),
            (FieldType.UINT64, 18000000000000000000),
            (FieldType.UUID, uuid.UUID("12345678-1234-5678-1234-567812345678")),
            (FieldType.TIMESTAMPTZ, datetime(2026, 9, 2, 12, 0, 0, tzinfo=timezone.utc)),
            (FieldType.JSON, {"key": "val", "nested": [1, 2, 3]}),
            (FieldType.decimal(10, 2), Decimal("12345.67")),
        ]

        for ft, val in test_cases:
            ct = encrypt_field(ring, db, tbl, col, ft, val)
            self.assertTrue(ct.startswith("NSCE1."))
            # Nonce randomness
            ct2 = encrypt_field(ring, db, tbl, col, ft, val)
            self.assertNotEqual(ct, ct2)

            dec = decrypt_field(ring, db, tbl, col, ft, ct)
            self.assertEqual(dec, val)
            dec2 = decrypt_field(ring, db, tbl, col, ft, ct2)
            self.assertEqual(dec2, val)

            # Inspect public header
            info = inspect_field(ct)
            self.assertEqual(info["key_id"], "app-v1")
            self.assertEqual(info["type"], ft)
            self.assertEqual(info["mode"], "randomized")

        # None passthrough
        self.assertIsNone(encrypt_field(ring, db, tbl, col, FieldType.STRING, None))
        self.assertIsNone(decrypt_field(ring, db, tbl, col, FieldType.STRING, None))

    def test_nsce1_tampering_and_context_binding(self) -> None:
        key = FieldKey("app-v1", b"\x42" * 32)
        ring = MemoryFieldKeyring(key)
        db, tbl, col = "testdb", "users", "secret"

        ct = encrypt_field(ring, db, tbl, col, FieldType.STRING, "confidential")

        # Context binding: wrong database, table, or column must fail
        with self.assertRaises(NextSQLError):
            decrypt_field(ring, "otherdb", tbl, col, FieldType.STRING, ct)
        with self.assertRaises(NextSQLError):
            decrypt_field(ring, db, "othertbl", col, FieldType.STRING, ct)
        with self.assertRaises(NextSQLError):
            decrypt_field(ring, db, tbl, "othercol", FieldType.STRING, ct)

        # Type mismatch must fail
        with self.assertRaises(NextSQLError):
            decrypt_field(ring, db, tbl, col, FieldType.INT32, ct)

        # Tampered ciphertext payload
        raw_b64 = ct[len("NSCE1.") :]
        raw_bytes = bytearray(base64.urlsafe_b64decode(raw_b64 + "=="))
        raw_bytes[-1] ^= 0x01
        tampered = "NSCE1." + base64.urlsafe_b64encode(raw_bytes).decode("ascii").rstrip("=")
        with self.assertRaises(NextSQLError):
            decrypt_field(ring, db, tbl, col, FieldType.STRING, tampered)

        # Wrong key
        other_key = FieldKey("app-v1", b"\x99" * 32)
        other_ring = MemoryFieldKeyring(other_key)
        with self.assertRaises(NextSQLError):
            decrypt_field(other_ring, db, tbl, col, FieldType.STRING, ct)

    def test_nsce2_deterministic_equality(self) -> None:
        key = FieldKey("app-v1", b"\x42" * 32)
        ring = MemoryFieldKeyring(key)
        db, tbl, col = "testdb", "users", "email"

        val = "user@example.com"
        ct1 = encrypt_field_deterministic(ring, db, tbl, col, FieldType.TEXT, val)
        ct2 = encrypt_field_deterministic(ring, db, tbl, col, FieldType.TEXT, val)

        self.assertTrue(ct1.startswith("NSCE2."))
        # Deterministic: identical plaintexts under same context produce identical ciphertexts
        self.assertEqual(ct1, ct2)

        dec = decrypt_field_deterministic(ring, db, tbl, col, FieldType.TEXT, ct1)
        self.assertEqual(dec, val)

        info = inspect_field(ct1)
        self.assertEqual(info["key_id"], "app-v1")
        self.assertEqual(info["mode"], "deterministic")

        # Different value produces different ciphertext
        ct_diff = encrypt_field_deterministic(ring, db, tbl, col, FieldType.TEXT, "other@example.com")
        self.assertNotEqual(ct1, ct_diff)

        # Mode separation: cannot decrypt NSCE2 with decrypt_field, or NSCE1 with decrypt_field_deterministic
        with self.assertRaises(NextSQLError):
            decrypt_field(ring, db, tbl, col, FieldType.TEXT, ct1)

        ct_rand = encrypt_field(ring, db, tbl, col, FieldType.TEXT, val)
        with self.assertRaises(NextSQLError):
            decrypt_field_deterministic(ring, db, tbl, col, FieldType.TEXT, ct_rand)

    def test_connection_helper_methods(self) -> None:
        key = FieldKey("app-v1", b"\x77" * 32)
        ring = MemoryFieldKeyring(key)
        cfg = nextsql.Config(
            address="127.0.0.1:7210",
            database="app",
            user="app",
            password="pwd",
            insecure_no_tls=True,
            field_keys=ring,
        )
        # Verify Connection exposes helpers and rejects without field_keys
        cfg_nokey = nextsql.Config(
            address="127.0.0.1:7210",
            database="app",
            user="app",
            password="pwd",
            insecure_no_tls=True,
        )
        conn = nextsql.Connection.__new__(nextsql.Connection)
        conn._cfg = cfg
        conn._database = "app"

        ct = conn.encrypt_field("accounts", "ssn", FieldType.STRING, "123-45-6789")
        self.assertTrue(ct.startswith("NSCE1."))
        dec = conn.decrypt_field("accounts", "ssn", FieldType.STRING, ct)
        self.assertEqual(dec, "123-45-6789")

        ct_det = conn.encrypt_field_deterministic("accounts", "email", FieldType.TEXT, "a@b.com")
        self.assertTrue(ct_det.startswith("NSCE2."))
        dec_det = conn.decrypt_field_deterministic("accounts", "email", FieldType.TEXT, ct_det)
        self.assertEqual(dec_det, "a@b.com")

        # No key config
        conn_nokey = nextsql.Connection.__new__(nextsql.Connection)
        conn_nokey._cfg = cfg_nokey
        with self.assertRaises(NextSQLError):
            conn_nokey.encrypt_field("accounts", "ssn", FieldType.STRING, "123")


if __name__ == "__main__":
    unittest.main()
