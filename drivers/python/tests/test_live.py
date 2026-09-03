"""Live integration test against a running nextsqld. Mirrors
drivers/php/tests/live.php's coverage. Requires NEXTSQL_ADDR + NEXTSQL_CA
(TLS PEM file path); skipped otherwise.

Run e.g.:
    NEXTSQL_ADDR=127.0.0.1:7210 NEXTSQL_CA=/path/to/ca.pem \
        python3 drivers/python/tests/test_live.py
"""

from __future__ import annotations

import os
import sys
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent.parent))

import nextsql

_ADDR = os.environ.get("NEXTSQL_ADDR", "")
_CA = os.environ.get("NEXTSQL_CA", "")


@unittest.skipUnless(_ADDR and _CA, "NEXTSQL_ADDR and NEXTSQL_CA are required")
class TestLive(unittest.TestCase):
    def _cfg(self) -> nextsql.Config:
        return nextsql.Config(
            address=_ADDR,
            database="production",
            user=os.environ.get("NEXTSQL_DATABASE_USER", "app"),
            password=os.environ.get("NEXTSQL_DATABASE_PASS", "s3cret"),
            tls=nextsql.TLSConfig(cafile=_CA, server_name="localhost"),
        )

    def test_end_to_end(self) -> None:
        conn = nextsql.connect(self._cfg())
        try:
            conn.exec(
                """CREATE TABLE items (
                    id UUID PRIMARY KEY DEFAULT UUID(),
                    sku STRING NOT NULL,
                    qty DECIMAL(10,0)
                )"""
            )
            ins = conn.exec("INSERT INTO items (sku, qty) VALUES ('A-1', 3), ('B-2', 9)")
            self.assertEqual(ins.affected, 2)

            sel = conn.exec("SELECT sku, qty FROM items WHERE sku = $1", ["B-2"])
            self.assertEqual(len(sel.rows), 1)
            self.assertEqual(sel.rows[0][0], "B-2")

            with conn.prepare("SELECT sku FROM items WHERE sku = $1") as stmt:
                pres = stmt.exec(["A-1"])
                self.assertEqual(pres.rows, [["A-1"]])

            seen = [row[0] for row in conn.query("SELECT sku FROM items")]
            self.assertEqual(sorted(seen), ["A-1", "B-2"])

            status = conn.node_status()
            self.assertEqual(status.role, "standalone")
            self.assertTrue(status.healthy)

            conn.set_read_consistency(nextsql.READ_BOUNDED, 5000)
            bounded = conn.exec("SELECT sku FROM items WHERE sku = $1", ["A-1"])
            self.assertEqual(len(bounded.rows), 1)
            conn.set_read_consistency(nextsql.READ_STRONG)
        finally:
            conn.close()

    def test_cluster_routes_to_standalone(self) -> None:
        cl = nextsql.connect_cluster(self._cfg())
        try:
            # Every SELECT requires an explicit FROM in this dialect (no
            # implicit dual-table literal select); system.tables always
            # exists, so this needs no prior schema setup.
            res = cl.exec("SELECT COUNT(*) FROM system.tables")
            self.assertEqual(len(res.rows), 1)
            nodes = cl.nodes()
            self.assertEqual(nodes[0].role, "standalone")
        finally:
            cl.close()


if __name__ == "__main__":
    unittest.main()
