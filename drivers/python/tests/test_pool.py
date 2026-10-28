import threading
import time
import unittest
from unittest.mock import MagicMock, patch

from nextsql import Config, ConnectionPool, NextSQLError, create_pool
from nextsql.client import Connection, Result


class TestConnectionPool(unittest.TestCase):
    def setUp(self):
        self.cfg = Config(address="127.0.0.1:7210", user="app")

    def test_invalid_pool_parameters(self):
        with self.assertRaises(NextSQLError):
            create_pool(self.cfg, min_size=-1)
        with self.assertRaises(NextSQLError):
            create_pool(self.cfg, min_size=5, max_size=2)
        with self.assertRaises(NextSQLError):
            create_pool(self.cfg, max_size=0)
        with self.assertRaises(NextSQLError):
            create_pool(self.cfg, statement_cache_size=-1)

    @patch.object(Connection, "connect")
    def test_pool_lifecycle_and_min_size(self, mock_connect):
        c1 = MagicMock(spec=Connection)
        c2 = MagicMock(spec=Connection)
        mock_connect.side_effect = [c1, c2]

        pool = create_pool(self.cfg, min_size=2, max_size=5)
        self.assertEqual(pool.size, 2)
        self.assertEqual(pool.idle_count, 2)
        self.assertEqual(pool.in_use_count, 0)
        self.assertFalse(pool.is_closed)

        # Acquire 1 connection
        conn = pool.acquire()
        self.assertEqual(conn, c2)  # pop from idle stack
        self.assertEqual(pool.size, 2)
        self.assertEqual(pool.idle_count, 1)
        self.assertEqual(pool.in_use_count, 1)

        # Release connection
        pool.release(conn)
        self.assertEqual(pool.idle_count, 2)
        self.assertEqual(pool.in_use_count, 0)

        pool.close()
        self.assertTrue(pool.is_closed)
        c1.close.assert_called()
        c2.close.assert_called()

    @patch.object(Connection, "connect")
    def test_pool_grows_up_to_max_size(self, mock_connect):
        conns = [MagicMock(spec=Connection) for _ in range(3)]
        mock_connect.side_effect = conns

        pool = create_pool(self.cfg, min_size=0, max_size=3)
        self.assertEqual(pool.size, 0)

        # Acquire 3 connections
        a1 = pool.acquire()
        a2 = pool.acquire()
        a3 = pool.acquire()
        self.assertEqual(pool.size, 3)
        self.assertEqual(pool.in_use_count, 3)
        self.assertEqual(pool.idle_count, 0)

        # 4th acquire must timeout
        with self.assertRaises(NextSQLError) as ctx:
            pool.acquire(timeout=0.05)
        self.assertEqual(ctx.exception.error_code, "resource_exhausted")

        # Release one
        pool.release(a1)
        self.assertEqual(pool.idle_count, 1)

        # Now acquire succeeds
        a4 = pool.acquire(timeout=0.1)
        self.assertEqual(a4, a1)

        pool.close()

    @patch.object(Connection, "connect")
    def test_connection_context_manager(self, mock_connect):
        c1 = MagicMock(spec=Connection)
        mock_connect.return_value = c1

        pool = create_pool(self.cfg, min_size=1, max_size=2)
        with pool.connection() as conn:
            self.assertEqual(conn, c1)
            self.assertEqual(pool.in_use_count, 1)

        self.assertEqual(pool.in_use_count, 0)
        self.assertEqual(pool.idle_count, 1)
        pool.close()

    @patch.object(Connection, "connect")
    def test_rollback_on_release_cleans_transaction(self, mock_connect):
        c1 = MagicMock(spec=Connection)
        mock_connect.return_value = c1

        pool = create_pool(self.cfg, min_size=1, max_size=1)
        conn = pool.acquire()
        pool.release(conn)

        # Connection should have received ROLLBACK to clean state
        c1.exec.assert_called_with("ROLLBACK")
        pool.close()

    @patch.object(Connection, "connect")
    def test_direct_pool_exec_and_query(self, mock_connect):
        c1 = MagicMock(spec=Connection)
        mock_connect.return_value = c1
        expected_res = Result(affected=1, columns=["id"], rows=[[1]])
        c1.exec.return_value = expected_res

        mock_rows = MagicMock()
        mock_rows.collect.return_value = expected_res
        c1.query.return_value = mock_rows

        pool = create_pool(self.cfg, min_size=1, max_size=2)
        res1 = pool.exec("INSERT INTO t VALUES (1)")
        self.assertEqual(res1.affected, 1)

        res2 = pool.query("SELECT * FROM t")
        self.assertEqual(res2.rows, [[1]])

        # Must have returned to pool
        self.assertEqual(pool.in_use_count, 0)
        self.assertEqual(pool.idle_count, 1)
        pool.close()

    @patch.object(Connection, "connect")
    def test_concurrent_threads_competing_for_connections(self, mock_connect):
        # Pool with max_size 2, but 5 threads running queries
        conns = [MagicMock(spec=Connection) for _ in range(2)]
        mock_connect.side_effect = conns

        pool = create_pool(self.cfg, min_size=2, max_size=2, timeout=5.0)
        results = []
        errors = []

        def worker(idx):
            try:
                with pool.connection() as conn:
                    time.sleep(0.01)  # simulate brief work
                    results.append(idx)
            except Exception as e:
                errors.append(e)

        threads = [threading.Thread(target=worker, args=(i,)) for i in range(5)]
        for t in threads:
            t.start()
        for t in threads:
            t.join()

        self.assertEqual(len(errors), 0)
        self.assertEqual(len(results), 5)
        self.assertEqual(pool.idle_count, 2)
        self.assertEqual(pool.in_use_count, 0)
        pool.close()

    def test_connection_statement_lru_cache_reuse_and_eviction(self):
        closed_stmts = []

        class MockStatement:
            def __init__(self, stmt_id):
                self._id = stmt_id

            def close(self):
                closed_stmts.append(self._id)

        cfg = Config(address="127.0.0.1:7210", user="app", statement_cache_size=2)
        conn = Connection.__new__(Connection)
        conn._sock = MagicMock()
        conn._busy = False
        conn.statement_cache_size = 2
        import collections
        conn._stmt_cache = collections.OrderedDict()

        prepared_calls = []
        executed_calls = []
        next_id = [1]

        def mock_prepare(sql):
            prepared_calls.append(sql)
            sid = next_id[0]
            next_id[0] += 1
            return MockStatement(sid)

        def mock_execute_prepared(sid, params):
            executed_calls.append((sid, params))
            mock_rows = MagicMock()
            mock_rows.collect.return_value = Result(columns=["v"], rows=[[params[0]]], affected=0)
            return mock_rows

        conn.prepare = mock_prepare
        conn.execute_prepared = mock_execute_prepared

        # 1. First parameterized query prepares id=1
        conn.query("SELECT $1", [10])
        self.assertEqual(len(prepared_calls), 1)
        self.assertEqual(len(executed_calls), 1)
        self.assertEqual(executed_calls[0][0], 1)

        # 2. Repeat exact SQL reuses cached prepared statement without preparing again
        conn.query("SELECT $1", [20])
        self.assertEqual(len(prepared_calls), 1)
        self.assertEqual(len(executed_calls), 2)
        self.assertEqual(executed_calls[1][0], 1)

        # 3. Second unique query prepares id=2 (cache now full: size 2)
        conn.query("SELECT $1 + 1", [30])
        self.assertEqual(len(prepared_calls), 2)
        self.assertEqual(len(executed_calls), 3)
        self.assertEqual(executed_calls[2][0], 2)

        # 4. Third unique query evicts least recently used statement (id=1)
        conn.query("SELECT $1 + 2", [40])
        self.assertEqual(len(prepared_calls), 3)
        self.assertEqual(len(executed_calls), 4)
        self.assertEqual(executed_calls[3][0], 3)
        self.assertEqual(closed_stmts, [1])

        # 5. Closing connection closes remaining cached statements (id=2 and id=3)
        conn.close()
        self.assertEqual(sorted(closed_stmts), [1, 2, 3])


if __name__ == "__main__":
    unittest.main()
