# frozen_string_literal: true

require "monitor"

module NextSQL
  # Thread-safe connection pool for NextSQL Ruby driver.
  #
  #   pool = NextSQL.create_pool(cfg, min_size: 2, max_size: 10)
  #   pool.with do |conn|
  #     res = conn.exec("SELECT * FROM users")
  #   end
  #
  #   # Or directly on the pool:
  #   pool.exec("INSERT INTO logs VALUES ($1)", ["event"])
  #   pool.close
  class ConnectionPool
    class PooledItem
      attr_reader :conn
      attr_accessor :last_used

      def initialize(conn)
        @conn = conn
        @last_used = Process.clock_gettime(Process::CLOCK_MONOTONIC)
      end
    end

    def initialize(config, min_size: 1, max_size: 10, timeout: 30.0, max_idle: 300.0, statement_cache_size: 50)
      raise Error.new("invalid_argument", "min_size must be >= 0") if min_size < 0
      raise Error.new("invalid_argument", "max_size must be >= 1") if max_size < 1
      raise Error.new("invalid_argument", "max_size must be >= min_size") if max_size < min_size
      raise Error.new("invalid_argument", "statement_cache_size must be >= 0") if statement_cache_size < 0

      @config = config.dup
      @config.statement_cache_size = statement_cache_size if @config.respond_to?(:statement_cache_size=)
      @min_size = min_size
      @max_size = max_size
      @timeout = timeout
      @max_idle = max_idle

      @monitor = Monitor.new
      @cond = @monitor.new_cond
      @idle = []
      @in_use = {}.compare_by_identity
      @closed = false

      if min_size > 0
        min_size.times do
          begin
            conn = Connection.connect(@config)
            @idle << PooledItem.new(conn)
          rescue StandardError
            close
            raise
          end
        end
      end
    end

    def size
      @monitor.synchronize { @idle.size + @in_use.size }
    end

    def idle_count
      @monitor.synchronize { @idle.size }
    end

    def in_use_count
      @monitor.synchronize { @in_use.size }
    end

    def closed?
      @monitor.synchronize { @closed }
    end

    # Acquires a connection from the pool, waiting up to +timeout+ seconds if
    # all connections are currently in use.
    def acquire(timeout: nil)
      wait_time = timeout || @timeout
      deadline = wait_time > 0 ? Process.clock_gettime(Process::CLOCK_MONOTONIC) + wait_time : Float::INFINITY

      @monitor.synchronize do
        loop do
          raise Error.new("unavailable", "connection pool is closed") if @closed

          now = Process.clock_gettime(Process::CLOCK_MONOTONIC)
          while (item = @idle.pop)
            if @max_idle > 0 && (now - item.last_used) > @max_idle
              safe_close(item.conn)
              next
            end
            if !alive?(item.conn)
              safe_close(item.conn)
              next
            end
            @in_use[item.conn] = item
            return item.conn
          end

          if (@idle.size + @in_use.size) < @max_size
            conn = Connection.connect(@config)
            item = PooledItem.new(conn)
            @in_use[conn] = item
            return conn
          end

          remaining = deadline - Process.clock_gettime(Process::CLOCK_MONOTONIC)
          if remaining <= 0
            raise Error.new("resource_exhausted", "connection pool exhausted, wait timeout exceeded")
          end

          @cond.wait([remaining, 0.5].min)
        end
      end
    end

    # Returns a connection to the pool.
    def release(conn)
      @monitor.synchronize do
        item = @in_use.delete(conn)
        return if item.nil?

        if @closed
          safe_close(conn)
          @cond.signal
          return
        end

        unless clean_connection(item)
          safe_close(conn)
          @cond.signal
          return
        end

        item.last_used = Process.clock_gettime(Process::CLOCK_MONOTONIC)
        @idle << item
        @cond.signal
      end
    end

    # Checks out a connection for the duration of the block and guarantees its
    # release even if the block raises.
    def with(timeout: nil)
      conn = acquire(timeout: timeout)
      begin
        yield conn
      ensure
        release(conn)
      end
    end

    # Executes a statement on a pooled connection and returns the result.
    def exec(sql, params = nil)
      with { |conn| conn.exec(sql, params) }
    end

    # Queries a statement on a pooled connection, buffering all rows into
    # a Result before releasing the connection.
    def query(sql, params = nil)
      with do |conn|
        rows = conn.query(sql, params)
        rows.collect
      end
    end

    # Closes all idle and active connections in the pool.
    def close
      @monitor.synchronize do
        return if @closed

        @closed = true
        @idle.each { |item| safe_close(item.conn) }
        @idle.clear

        @in_use.each_key { |conn| safe_close(conn) }
        @in_use.clear

        @cond.broadcast
      end
    end

    private

    def safe_close(conn)
      conn.close
    rescue StandardError
      nil
    end

    def alive?(conn)
      if conn.instance_variable_defined?(:@sock) && conn.instance_variable_get(:@sock).nil?
        return false
      end
      true
    rescue StandardError
      true
    end

    def clean_connection(item)
      item.conn.exec("ROLLBACK")
      true
    rescue StandardError
      false
    end
  end

  def self.create_pool(config, min_size: 1, max_size: 10, timeout: 30.0, max_idle: 300.0)
    ConnectionPool.new(
      config,
      min_size: min_size,
      max_size: max_size,
      timeout: timeout,
      max_idle: max_idle,
    )
  end
end
