# frozen_string_literal: true

require "minitest/autorun"
require_relative "../lib/nextsql"

class TestConnectionPool < Minitest::Test
  FakeConn = Struct.new(:closed, :rollbacks, :executed) do
    def initialize
      super(false, 0, [])
    end

    def close
      self.closed = true
    end

    def exec(sql, params = nil)
      if sql == "ROLLBACK"
        self.rollbacks += 1
      else
        self.executed << [sql, params]
      end
      NextSQL::Result.new(["id"], [[1]], 1)
    end

    def query(sql, params = nil)
      rows = NextSQL::Rows.allocate
      rows.instance_variable_set(:@closed, true)
      rows.instance_variable_set(:@values, [[1]])
      rows.instance_variable_set(:@columns, [NextSQL::Protocol::Column.new("id", 1, 0, nil)])
      def rows.collect; NextSQL::Result.new(["id"], [[1]], 1); end
      rows
    end
  end

  def setup
    @cfg = NextSQL::Config.new(address: "127.0.0.1:7210", user: "app")
  end

  def test_invalid_parameters
    assert_raises(NextSQL::Error) { NextSQL.create_pool(@cfg, min_size: -1) }
    assert_raises(NextSQL::Error) { NextSQL.create_pool(@cfg, min_size: 5, max_size: 2) }
    assert_raises(NextSQL::Error) { NextSQL.create_pool(@cfg, max_size: 0) }
    assert_raises(NextSQL::Error) { NextSQL.create_pool(@cfg, statement_cache_size: -1) }
  end

  def test_connection_statement_lru_cache_reuse_and_eviction
    cfg = NextSQL::Config.new(address: "127.0.0.1:7210", user: "app", statement_cache_size: 2, insecure_no_tls: true)
    conn = NextSQL::Connection.allocate
    conn.instance_variable_set(:@cfg, cfg)
    fake_sock = Object.new
    def fake_sock.close; nil; end
    conn.instance_variable_set(:@sock, fake_sock)
    conn.instance_variable_set(:@busy, false)
    conn.instance_variable_set(:@statement_cache_size, 2)
    conn.instance_variable_set(:@stmt_cache, {})

    prepared = []
    executed = []
    closed = []
    stmt_counter = 100

    conn.define_singleton_method(:prepare_id) do |sql|
      prepared << sql
      stmt_counter += 1
      stmt_counter
    end

    conn.define_singleton_method(:execute_prepared) do |stmt_id, params|
      executed << [stmt_id, params]
      NextSQL::Result.new(["id"], [[1]], 1)
    end

    conn.define_singleton_method(:close_statement) do |stmt_id|
      closed << stmt_id
    end

    conn.define_singleton_method(:write_frame) { |_t, _p| nil }

    # Query 1 first time -> prepares
    conn.query("SELECT 1 WHERE a = $1", [1])
    assert_equal ["SELECT 1 WHERE a = $1"], prepared
    assert_equal [[101, [1]]], executed
    assert_empty closed

    # Query 1 second time -> cache hit, no prepare
    conn.query("SELECT 1 WHERE a = $1", [2])
    assert_equal ["SELECT 1 WHERE a = $1"], prepared
    assert_equal [[101, [1]], [101, [2]]], executed
    assert_empty closed

    # Query 2 -> prepares, cache now has 2 items [q1, q2]
    conn.query("SELECT 2 WHERE b = $1", [10])
    assert_equal ["SELECT 1 WHERE a = $1", "SELECT 2 WHERE b = $1"], prepared
    assert_equal 2, conn.instance_variable_get(:@stmt_cache).size
    assert_empty closed

    # Access Query 1 again to make Query 2 the LRU
    conn.query("SELECT 1 WHERE a = $1", [3])

    # Query 3 -> prepares and evicts LRU (which is Query 2, id 102)
    conn.query("SELECT 3 WHERE c = $1", [99])
    assert_equal ["SELECT 1 WHERE a = $1", "SELECT 2 WHERE b = $1", "SELECT 3 WHERE c = $1"], prepared
    assert_equal [102], closed
    assert_equal 2, conn.instance_variable_get(:@stmt_cache).size

    # Close connection -> closes remaining cached statements
    conn.close
    assert_equal [102, 101, 103].sort, closed.sort
  end

  def test_pool_lifecycle_and_min_size
    conns = [FakeConn.new, FakeConn.new]
    idx = 0
    NextSQL::Connection.stub(:connect, ->(_cfg) { c = conns[idx]; idx += 1; c }) do
      pool = NextSQL.create_pool(@cfg, min_size: 2, max_size: 5)
      assert_equal 2, pool.size
      assert_equal 2, pool.idle_count
      assert_equal 0, pool.in_use_count
      refute pool.closed?

      conn = pool.acquire
      assert_equal conns[1], conn
      assert_equal 2, pool.size
      assert_equal 1, pool.idle_count
      assert_equal 1, pool.in_use_count

      pool.release(conn)
      assert_equal 2, pool.idle_count
      assert_equal 0, pool.in_use_count

      pool.close
      assert pool.closed?
      assert conns[0].closed
      assert conns[1].closed
    end
  end

  def test_pool_exhaustion_and_timeout
    conns = [FakeConn.new, FakeConn.new]
    idx = 0
    NextSQL::Connection.stub(:connect, ->(_cfg) { c = conns[idx]; idx += 1; c }) do
      pool = NextSQL.create_pool(@cfg, min_size: 0, max_size: 2)

      a1 = pool.acquire
      a2 = pool.acquire
      assert_equal 2, pool.size
      assert_equal 2, pool.in_use_count

      # 3rd acquire must timeout
      err = assert_raises(NextSQL::Error) { pool.acquire(timeout: 0.05) }
      assert_equal "resource_exhausted", err.error_code

      # Release one and re-acquire
      pool.release(a1)
      assert_equal 1, pool.idle_count
      a3 = pool.acquire(timeout: 0.1)
      assert_equal a1, a3

      pool.close
    end
  end

  def test_with_block_guarantees_release
    conn = FakeConn.new
    NextSQL::Connection.stub(:connect, ->(_cfg) { conn }) do
      pool = NextSQL.create_pool(@cfg, min_size: 1, max_size: 2)

      pool.with do |c|
        assert_equal conn, c
        assert_equal 1, pool.in_use_count
      end

      assert_equal 0, pool.in_use_count
      assert_equal 1, pool.idle_count
      pool.close
    end
  end

  def test_rollback_on_release_cleans_transaction
    conn = FakeConn.new
    NextSQL::Connection.stub(:connect, ->(_cfg) { conn }) do
      pool = NextSQL.create_pool(@cfg, min_size: 1, max_size: 1)
      c = pool.acquire
      pool.release(c)

      assert_equal 1, conn.rollbacks
      pool.close
    end
  end

  def test_direct_exec_and_query
    conn = FakeConn.new
    NextSQL::Connection.stub(:connect, ->(_cfg) { conn }) do
      pool = NextSQL.create_pool(@cfg, min_size: 1, max_size: 2)

      res1 = pool.exec("INSERT INTO t VALUES (1)")
      assert_equal 1, res1.affected

      res2 = pool.query("SELECT * FROM t")
      assert_equal [[1]], res2.rows

      assert_equal 0, pool.in_use_count
      assert_equal 1, pool.idle_count
      pool.close
    end
  end

  def test_concurrent_threads_competing_for_connections
    conns = [FakeConn.new, FakeConn.new]
    idx = 0
    mutex = Mutex.new
    NextSQL::Connection.stub(:connect, ->(_cfg) { mutex.synchronize { c = conns[idx]; idx += 1; c } }) do
      pool = NextSQL.create_pool(@cfg, min_size: 2, max_size: 2, timeout: 5.0)
      results = []
      threads = 6.times.map do |i|
        Thread.new do
          pool.with do |_c|
            sleep 0.01
            mutex.synchronize { results << i }
          end
        end
      end
      threads.each(&:join)

      assert_equal 6, results.size
      assert_equal 2, pool.idle_count
      assert_equal 0, pool.in_use_count
      pool.close
    end
  end
end
