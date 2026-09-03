# frozen_string_literal: true

# Official NextSQL Ruby driver. Speaks the native NSQL v1 protocol.
#
# Encryption keys and passwords are never accepted in a URL.

require "socket"
require "openssl"

require_relative "protocol"
require_relative "errors"

module NextSQL
  # TLS options for a remote connection. +ca+ is PEM text/bytes; omit both
  # +ca+ and +cafile+ to use the system trust store. Set
  # +reject_unauthorized: false+ only for local testing against a
  # self-signed certificate — never in production.
  TLSConfig = Struct.new(:ca, :cafile, :server_name, :reject_unauthorized, :client_cert, :client_key,
                          keyword_init: true) do
    def initialize(**kwargs)
      super({ reject_unauthorized: true }.merge(kwargs))
    end
  end

  Config = Struct.new(:address, :nodes, :database, :realm, :user, :password, :key, :key_version, :tls,
                       :insecure_no_tls, :read_consistency, :max_staleness_ms, :timeout, keyword_init: true) do
    def initialize(**kwargs)
      defaults = {
        address: "", nodes: [], database: "", realm: "", user: "", password: "",
        key: nil, key_version: 1, tls: nil, insecure_no_tls: false,
        read_consistency: Protocol::READ_STRONG, max_staleness_ms: 0, timeout: 60.0
      }
      super(defaults.merge(kwargs))
    end
  end

  Result = Struct.new(:columns, :rows, :affected)

  # A single connection to one NextSQL node. Not safe for concurrent use
  # from multiple threads/fibers — open one Connection per worker, or use
  # +Cluster+ which pools one connection per node.
  class Connection
    LOOPBACK_RE = /\A127\.\d{1,3}\.\d{1,3}\.\d{1,3}\z/.freeze
    CONNECT_TIMEOUT = 10.0

    class << self
      def connect(cfg)
        new(cfg)
      end

      def split_host_port(addr, allow_bare: false)
        if addr.start_with?("[")
          e = addr.index("]")
          raise Error.new("invalid_argument", "invalid address") unless e

          host = addr[1...e]
          rest = addr[(e + 1)..]
          return [host, rest[1..].to_i] if rest.start_with?(":")
          return [host, 0] if allow_bare

          raise Error.new("invalid_argument", "address requires a port")
        end
        i = addr.rindex(":")
        if i.nil?
          return [addr, 0] if allow_bare

          raise Error.new("invalid_argument", "address requires a port")
        end
        [addr[0...i], addr[(i + 1)..].to_i]
      end

      def loopback?(addr)
        host, = split_host_port(addr, allow_bare: true)
        host = host.strip.downcase
        return true if host == "localhost"
        return true if %w[::1 0:0:0:0:0:0:0:1].include?(host)

        LOOPBACK_RE.match?(host)
      end

      def validate_config!(cfg)
        raise Error.new("invalid_argument", "address is required") if cfg.address.to_s.empty?

        addr = cfg.address.downcase
        if addr.include?("://") || addr.include?("key=") || addr.include?("password=")
          raise Error.new("invalid_argument", "keys and credentials must not be passed in a URL")
        end
        if cfg.tls.nil? && !cfg.insecure_no_tls
          raise Error.new("invalid_argument", "TLS is required for remote connections")
        end
        if cfg.insecure_no_tls && !loopback?(cfg.address)
          raise Error.new("invalid_argument", "plaintext is only allowed on loopback")
        end
        raise Error.new("invalid_argument", "user is required") if cfg.user.to_s.empty?
      end

      LEADING_WS_RE = /\A[ \t\r\n(]+/.freeze

      def strip_leading(s) = s.sub(LEADING_WS_RE, "")

      def txn_control(sql)
        up = strip_leading(sql).upcase
        begin_ = up.start_with?("BEGIN") || up.start_with?("START TRANSACTION")
        end_ = up.start_with?("COMMIT") || up.start_with?("ROLLBACK")
        [begin_, end_]
      end

      # Conservative check: a false negative only costs a leader round trip,
      # and a false positive self-corrects on the leader. EXPLAIN is
      # excluded because EXPLAIN ANALYZE executes its statement.
      def read_only_sql?(sql)
        s = strip_leading(sql)
        while s.start_with?("--")
          i = s.index("\n")
          return false unless i

          s = strip_leading(s[(i + 1)..])
        end
        up = s.upcase
        return true if up.start_with?("SELECT") || up.start_with?("SHOW")
        return %w[INSERT UPDATE DELETE UPSERT].none? { |kw| up.include?(kw) } if up.start_with?("WITH")

        false
      end

      def dial(cfg)
        host, port = split_host_port(cfg.address)
        raw = begin
          Socket.tcp(host, port, connect_timeout: CONNECT_TIMEOUT)
        rescue SocketError, SystemCallError, IOError => e
          raise Error.new("io", e.message)
        end
        raw.setsockopt(Socket::IPPROTO_TCP, Socket::TCP_NODELAY, 1)
        return raw if cfg.tls.nil?

        tls = cfg.tls
        ctx = OpenSSL::SSL::SSLContext.new
        ctx.min_version = OpenSSL::SSL::TLS1_3_VERSION
        if tls.reject_unauthorized == false
          ctx.verify_mode = OpenSSL::SSL::VERIFY_NONE
        else
          ctx.verify_mode = OpenSSL::SSL::VERIFY_PEER
          if tls.ca
            ctx.cert_store = OpenSSL::X509::Store.new
            ctx.cert_store.add_cert(OpenSSL::X509::Certificate.new(tls.ca))
          elsif tls.cafile
            ctx.ca_file = tls.cafile
          else
            ctx.cert_store = OpenSSL::X509::Store.new
            ctx.cert_store.set_default_paths
          end
        end
        if tls.client_cert
          ctx.cert = OpenSSL::X509::Certificate.new(File.read(tls.client_cert))
          ctx.key = OpenSSL::PKey.read(File.read(tls.client_key))
        end
        ssl = OpenSSL::SSL::SSLSocket.new(raw, ctx)
        ssl.hostname = tls.server_name || host
        begin
          ssl.connect
        rescue OpenSSL::SSL::SSLError => e
          raw.close
          raise Error.new("protocol", "tls handshake: #{e.message}")
        end
        ssl
      end
    end

    def initialize(cfg)
      self.class.validate_config!(cfg)
      @cfg = cfg
      @sock = self.class.dial(cfg)
      @secret = "".b
      @busy = false
      begin
        handshake
        set_read_consistency(cfg.read_consistency, cfg.max_staleness_ms) if cfg.read_consistency != Protocol::READ_STRONG
      rescue StandardError
        @sock&.close
        raise
      end
    end

    def set_read_consistency(mode, max_staleness_ms = 0)
      raise Error.new("conflict", "connection is busy") if @busy

      write_frame(Protocol::TYPE_SET_READ_CONSISTENCY, Protocol.encode_set_read_consistency(mode, max_staleness_ms))
      read_ack
    end

    def node_status
      raise Error.new("conflict", "connection is busy") if @busy

      write_frame(Protocol::TYPE_NODE_STATUS, "")
      typ, payload = read_frame
      raise unexpected(typ, payload) if typ != Protocol::TYPE_NODE_STATUS_RESP

      st = Protocol.decode_node_status(payload)
      expect_ready
      st
    end

    def exec(sql, params = [])
      query(sql, params).collect
    end

    def query(sql, params = [])
      raise Error.new("unavailable", "connection closed") if @sock.nil?
      raise Error.new("conflict", "connection is busy") if @busy

      @busy = true
      begin
        write_frame(Protocol::TYPE_QUERY, Protocol.encode_query(sql, params))
        read_rows
      rescue StandardError
        @busy = false
        raise
      end
    end

    # Executes a retryable mutation under a durable idempotency key: a
    # retried call with the same key replays the original result instead of
    # re-executing. See docs/sql.md / docs/protocol.md.
    def exec_idempotent(key, sql, params = [])
      query_idempotent(key, sql, params).collect
    end

    def query_idempotent(key, sql, params = [])
      raise Error.new("conflict", "connection is busy") if @busy

      @busy = true
      begin
        write_frame(Protocol::TYPE_IDEMPOTENT_QUERY, Protocol.encode_idempotent_query(key, sql, params))
        read_rows
      rescue StandardError
        @busy = false
        raise
      end
    end

    def prepare(sql)
      raise Error.new("conflict", "connection is busy") if @busy

      write_frame(Protocol::TYPE_PREPARE, Protocol.u32bytes(sql.b, Protocol::MAX_SQL))
      typ, payload = read_frame
      raise unexpected(typ, payload) if typ != Protocol::TYPE_PREPARE_OK
      raise Error.new("protocol", "bad prepare-ok length") unless payload.bytesize == 4

      stmt_id = Protocol.u32(payload, 0)
      expect_ready
      Statement.new(self, stmt_id)
    end

    def execute_prepared(stmt_id, params)
      raise Error.new("conflict", "connection is busy") if @busy

      @busy = true
      begin
        write_frame(Protocol::TYPE_EXECUTE, Protocol.encode_execute(stmt_id, params))
        read_rows
      rescue StandardError
        @busy = false
        raise
      end
    end

    def close_statement(stmt_id)
      raise Error.new("conflict", "connection is busy") if @busy

      write_frame(Protocol::TYPE_CLOSE_STMT, Protocol.u32le(stmt_id))
      typ, payload = read_frame
      raise unexpected(typ, payload) if typ != Protocol::TYPE_CLOSE_OK

      expect_ready
    end

    # Cancels the statement currently running on this connection, from a
    # second, independent connection carrying this connection's secret.
    # Safe to call from another thread while +query+/+exec+ blocks.
    def cancel
      raise Error.new("unavailable", "not connected") if @secret.empty?

      side = self.class.dial(@cfg)
      begin
        tmp = self.class.allocate
        tmp.instance_variable_set(:@sock, side)
        tmp.instance_variable_set(:@busy, false)
        tmp.write_frame(Protocol::TYPE_HELLO,
                         Protocol.encode_hello(Protocol::VERSION, Protocol::FLAG_CANCEL, @secret, "", ""))
        typ, payload = tmp.read_frame
        raise unexpected(typ, payload) if typ != Protocol::TYPE_READY
      ensure
        side.close
      end
    end

    def close
      return if @sock.nil?

      begin
        write_frame(Protocol::TYPE_TERMINATE, "")
      rescue Error
        nil
      end
      @sock.close
      @sock = nil
    end

    def busy? = @busy
    def release_busy! = (@busy = false)

    # --- wire plumbing shared with Rows/Statement (internal API: stable
    # within this driver, not part of the public Connection surface) ---

    # Decodes an out-of-band Error frame (or reports a genuine protocol
    # violation) for a call site checking "did I get what I expected?".
    # writeErrReady on the server always sends Error then Ready — every
    # call site funnels through here specifically so that trailing Ready
    # is always drained in one place, rather than each of query/prepare/
    # close_statement/etc. having to remember to do it individually (a
    # per-call-site version of this is exactly the shape of bug this
    # centralizes away).
    def unexpected(typ, payload)
      if typ == Protocol::TYPE_ERROR
        err = Protocol.decode_error(payload)
        begin
          expect_ready
        rescue Error
          # Best-effort: surface the original application error even if
          # draining the trailing Ready itself fails (e.g. the connection
          # is now genuinely broken).
        end
        return err
      end
      Error.new("protocol", "unexpected message type")
    end

    def expect_ready
      typ, payload = read_frame
      raise unexpected(typ, payload) if typ != Protocol::TYPE_READY
    end

    def read_frame
      hdr = read_exact(12)
      raise Error.new("protocol", "bad magic") unless hdr.byteslice(0, 4) == "NSQL"
      raise Error.new("protocol", "unsupported protocol version") unless Protocol.u16(hdr, 4) == Protocol::VERSION

      typ = hdr.getbyte(6)
      raise Error.new("protocol", "invalid message type") if typ.zero?

      n = Protocol.u32(hdr, 8)
      raise Error.new("protocol", "packet exceeds limit") if n > Protocol::MAX_PACKET

      payload = n.zero? ? "".b : read_exact(n)
      [typ, payload]
    end

    def write_frame(typ, payload)
      raise Error.new("protocol", "payload exceeds packet limit") if payload.bytesize > Protocol::MAX_PACKET

      hdr = +"NSQL".b
      hdr << Protocol.u16le(Protocol::VERSION)
      hdr << typ.chr << "\x00"
      hdr << Protocol.u32le(payload.bytesize)
      write_all(hdr + payload)
    end

    private

    def handshake
      cfg = @cfg
      write_frame(Protocol::TYPE_HELLO, Protocol.encode_hello(Protocol::VERSION, 0, "\x00" * 8, cfg.database, cfg.user, cfg.realm))
      typ, payload = read_frame
      raise unexpected(typ, payload) if typ != Protocol::TYPE_HELLO_OK

      _version, auth_method, secret = Protocol.decode_hello_ok(payload)
      @secret = secret
      write_frame(Protocol::TYPE_AUTH, Protocol.u16str(cfg.password))
      typ, payload = read_frame
      raise unexpected(typ, payload) if typ != Protocol::TYPE_AUTH_OK

      if auth_method == Protocol::AUTH_PASSWORD_KEY
        unless cfg.key && cfg.key.bytesize == 32
          raise Error.new("unauthorized", "server requires a client-held key")
        end

        mat = Protocol.u32le(cfg.key_version) + cfg.key
        write_frame(Protocol::TYPE_UNLOCK, mat)
        typ, payload = read_frame
        raise unexpected(typ, payload) if typ != Protocol::TYPE_UNLOCK_OK
      end
      typ, payload = read_frame
      raise unexpected(typ, payload) if typ != Protocol::TYPE_READY
    end

    def read_rows
      typ, payload = read_frame
      if typ == Protocol::TYPE_ROW_DESC
        return Rows.new(self, Protocol.decode_row_desc(payload))
      end
      if typ == Protocol::TYPE_COMMAND_COMPLETE
        rows = Rows.new(self, [])
        rows.affected = Protocol.decode_command_complete(payload)
        expect_ready
        @busy = false
        rows.mark_closed!
        return rows
      end
      err = unexpected(typ, payload)
      @busy = false
      raise err
    end

    def read_ack
      typ, payload = read_frame
      return if typ == Protocol::TYPE_READY

      raise unexpected(typ, payload)
    end

    def read_exact(n)
      return "".b if n.zero?

      begin
        @sock.read(n) || (raise Error.new("unavailable", "connection closed"))
      rescue IOError, SystemCallError, OpenSSL::SSL::SSLError => e
        raise Error.new("io", e.message)
      end.tap do |got|
        raise Error.new("unavailable", "connection closed") if got.bytesize != n
      end
    end

    def write_all(data)
      @sock.write(data)
    rescue IOError, SystemCallError, OpenSSL::SSL::SSLError => e
      raise Error.new("io", e.message)
    end
  end

  # A streaming query result. Iterate directly, or call +collect+ for a
  # materialized +Result+.
  class Rows
    include Enumerable

    attr_reader :columns
    attr_accessor :affected

    def initialize(conn, columns)
      @conn = conn
      @columns = columns.map(&:name)
      @affected = 0
      @batch = []
      @i = -1
      @done = columns.empty?
      @closed = false
      @err = nil
    end

    def next?
      return false if @closed || @err

      if @i + 1 < @batch.size
        @i += 1
        return true
      end
      return false if @done

      begin
        fill
      rescue Error => e
        @err = e
        return false
      end
      if @i + 1 < @batch.size
        @i += 1
        return true
      end
      false
    end

    def values
      return nil if @i.negative? || @i >= @batch.size

      @batch[@i]
    end

    def err = @err

    def each
      return enum_for(:each) unless block_given?

      begin
        while next?
          row = values
          yield row if row
        end
        raise @err if @err
      ensure
        close unless @closed
      end
    end

    def close
      nil while next?
      finish unless @closed
      if @err
        e = @err
        @err = nil
        raise e
      end
    end

    def collect
      out = []
      begin
        while next?
          row = values
          out << row if row
        end
        raise @err if @err
      ensure
        close unless @closed
      end
      Result.new(@columns, out, @affected)
    end

    def mark_closed!
      @closed = true
      @done = true
    end

    # @api private
    def fill
      @conn.write_frame(Protocol::TYPE_FLOW_ACK, "") if !@done && !@batch.empty?
      typ, payload = @conn.read_frame
      if typ == Protocol::TYPE_DATA_BATCH
        @batch = Protocol.decode_data_batch(payload)
        @i = -1
        return
      end
      if typ == Protocol::TYPE_COMMAND_COMPLETE
        @affected = Protocol.decode_command_complete(payload)
        @done = true
        @batch = []
        @i = -1
        @conn.expect_ready
        finish
        return
      end
      raise @conn.unexpected(typ, payload)
    end

    private

    def finish
      @conn.release_busy! unless @closed
      @closed = true
    end
  end

  # A prepared statement. Close it when done, or wrap it in +with_statement+
  # for automatic cleanup.
  class Statement
    def initialize(conn, stmt_id)
      @conn = conn
      @id = stmt_id
    end

    def query(params = [])
      @conn.execute_prepared(@id, params)
    end

    def exec(params = [])
      query(params).collect
    end

    def close
      return if @id.zero?

      @conn.close_statement(@id)
      @id = 0
    end
  end

  # Prepares +sql+ on +conn+, yields the Statement, and closes it
  # afterward even if the block raises.
  def self.with_statement(conn, sql)
    stmt = conn.prepare(sql)
    begin
      yield stmt
    ensure
      stmt.close
    end
  end
end
