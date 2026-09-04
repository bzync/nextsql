# frozen_string_literal: true

# Unit tests for NSQL v1 wire encoding — no live server required.
#
# Run with: ruby drivers/ruby/test/test_protocol.rb

require "minitest/autorun"
require "bigdecimal"
require "time"
require_relative "../lib/nextsql/protocol"
require_relative "../lib/nextsql/client"

module NextSQL
  class TestFraming < Minitest::Test
    def test_hello_round_trip
      payload = Protocol.encode_hello(1, 0, "\x00" * 8, "production", "app")
      assert_equal 1, Protocol.u16(payload, 0)
      assert_equal 0, Protocol.u16(payload, 2)
      db, off = Protocol.read_u16_string(payload, 12, Protocol::MAX_NAME)
      assert_equal "production", db
      user, = Protocol.read_u16_string(payload, off, Protocol::MAX_NAME)
      assert_equal "app", user
    end

    def test_hello_realm_is_opt_in_trailing_field
      no_realm = Protocol.encode_hello(1, 0, "\x00" * 8, "production", "app")
      default_realm = Protocol.encode_hello(1, 0, "\x00" * 8, "production", "app", "")
      assert_equal no_realm, default_realm
      with_realm = Protocol.encode_hello(1, 0, "\x00" * 8, "production", "app", "tenant-a")
      assert_equal no_realm.bytesize + 2 + "tenant-a".bytesize, with_realm.bytesize
      assert_equal no_realm, with_realm.byteslice(0, no_realm.bytesize)
      realm, off = Protocol.read_u16_string(with_realm, no_realm.bytesize, Protocol::MAX_NAME)
      assert_equal "tenant-a", realm
      assert_equal with_realm.bytesize, off
    end

    def test_hello_ok_round_trip
      raw = Protocol.u16le(1) + Protocol::AUTH_PASSWORD.chr + ("S" * 8)
      version, auth_method, secret = Protocol.decode_hello_ok(raw)
      assert_equal 1, version
      assert_equal Protocol::AUTH_PASSWORD, auth_method
      assert_equal "S" * 8, secret
    end

    def test_hello_ok_rejects_bad_length
      assert_raises(Error) { Protocol.decode_hello_ok("short") }
    end

    def test_error_round_trip
      raw = Protocol.u16str("unavailable") + Protocol.u16str("no reachable leader")
      err = Protocol.decode_error(raw)
      assert_equal "unavailable", err.error_code
      assert_equal "no reachable leader", err.message
    end
  end

  class TestDecimal < Minitest::Test
    def roundtrip(s)
      Protocol.decode_decimal(Protocol.encode_decimal(s)[4..]).to_s("F")
    end

    def test_positive
      assert_equal "123.45", roundtrip("123.45")
    end

    def test_negative
      assert_equal "-0.001", roundtrip("-0.001")
    end

    def test_zero
      assert_equal "0.0", roundtrip("0")
    end

    def test_large_integer
      big = "9" * 40
      assert_equal "#{big}.0", roundtrip(big)
    end

    def test_leading_plus_and_zeros
      assert_equal "7.5", roundtrip("+007.5")
    end

    def test_rejects_invalid
      assert_raises(Error) { Protocol.encode_decimal("not-a-number") }
    end

    def test_param_encoding_accepts_integer_and_bigdecimal
      encoded_int = Protocol.encode_param(42)
      encoded_dec = Protocol.encode_param(BigDecimal("42"))
      assert_equal Protocol::KIND_DECIMAL, encoded_int.getbyte(0)
      assert_equal Protocol::KIND_DECIMAL, encoded_dec.getbyte(0)
      value, = Protocol.decode_value(encoded_int, 0)
      assert_equal "42", value.to_s("F").sub(/\.0\z/, "")
    end
  end

  class TestValues < Minitest::Test
    def test_spatial_ewkb_round_trip
      ewkb = [1].pack("C") + [1 | 0x20000000].pack("V") + [4326].pack("V") + [1.5].pack("E") + [2.5].pack("E")
      wire = [Protocol::KIND_GEOMETRY, 0, 0, 0, 0, 0, 0].pack("C7") + [ewkb.bytesize].pack("V") + ewkb
      val, next_off, kind = Protocol.decode_value(wire, 0)
      assert_equal wire.bytesize, next_off
      assert_equal Protocol::KIND_GEOMETRY, kind
      assert_equal "Point", val.type
      assert_equal 4326, val.srid
      assert_equal [1.5, 2.5], val.coordinates
      enc = Protocol.encode_param(Protocol::Geometry.new(wkt: "POINT(1 2)", srid: 4326))
      assert_equal "SRID=4326;POINT(1 2)", Protocol.decode_value(enc, 0)[0]
    end

    def test_collections_round_trip
      arr_val, arr_next, arr_kind = Protocol.decode_value(Protocol.encode_param(%w[a b c]), 0)
      assert_equal Protocol::KIND_ARRAY, arr_kind
      assert_equal %w[a b c], arr_val
      assert_equal(["a", nil, "c"], Protocol.decode_value(Protocol.encode_param(["a", nil, "c"]), 0)[0])
      m_val, _, m_kind = Protocol.decode_value(Protocol.encode_param(Protocol::MapValue.new(entries: { "x" => "hi" })), 0)
      assert_equal Protocol::KIND_MAP, m_kind
      assert_equal "hi", m_val["x"]
      s_val, _, s_kind = Protocol.decode_value(Protocol.encode_param(Protocol::StructValue.new(fields: [["street", "Main"], ["zip", "90210"]])), 0)
      assert_equal Protocol::KIND_STRUCT, s_kind
      assert_equal "Main", s_val["street"]
      nested, = Protocol.decode_value(Protocol.encode_param(Protocol::StructValue.new(fields: [["n", "bob"], ["t", %w[x y]]])), 0)
      assert_equal %w[x y], nested["t"]
    end

    def test_null_param
      raw = Protocol.encode_param(nil)
      value, next_off, kind = Protocol.decode_value(raw, 0)
      assert_nil value
      assert_equal 7, next_off
      assert_equal Protocol::KIND_STRING, kind
    end

    def test_bool_round_trip
      [true, false].each do |b|
        raw = Protocol.encode_param(b)
        value, _, kind = Protocol.decode_value(raw, 0)
        assert_equal Protocol::KIND_BOOL, kind
        assert_equal b, value
      end
    end

    def test_string_round_trip
      raw = Protocol.encode_param("héllo wörld")
      value, next_off, kind = Protocol.decode_value(raw, 0)
      assert_equal "héllo wörld", value
      assert_equal raw.bytesize, next_off
      assert_equal Protocol::KIND_STRING, kind
    end

    def test_blob_round_trip
      raw_bytes = [0x00, 0xff, 0xfe, 0x00, 0xde, 0xad, 0xbe, 0xef].pack("C*").b
      raw = Protocol.encode_param(raw_bytes)
      value, next_off, kind = Protocol.decode_value(raw, 0)
      assert_equal Protocol::KIND_BLOB, kind
      assert_equal raw_bytes, value
      assert_equal raw.bytesize, next_off

      # A default (UTF-8) String stays STRING; only a binary-encoded one is a BLOB.
      text_kind = Protocol.decode_value(Protocol.encode_param("plain text"), 0)[2]
      assert_equal Protocol::KIND_STRING, text_kind

      empty_value, _, empty_kind = Protocol.decode_value(Protocol.encode_param("".b), 0)
      assert_equal Protocol::KIND_BLOB, empty_kind
      assert_equal "", empty_value
    end

    def test_int_round_trip
      cases = [
        [Protocol::Int8.new(-128), Protocol::KIND_INT8],
        [Protocol::Int8.new(127), Protocol::KIND_INT8],
        [Protocol::Int16.new(-32_768), Protocol::KIND_INT16],
        [Protocol::Int16.new(32_767), Protocol::KIND_INT16],
        [Protocol::Int32.new(-2_147_483_648), Protocol::KIND_INT32],
        [Protocol::Int32.new(2_147_483_647), Protocol::KIND_INT32],
        [Protocol::Int64.new(-9_223_372_036_854_775_808), Protocol::KIND_INT64],
        [Protocol::Int64.new(9_223_372_036_854_775_807), Protocol::KIND_INT64],
      ]
      cases.each do |wrapped, want_kind|
        raw = Protocol.encode_param(wrapped)
        value, next_off, kind = Protocol.decode_value(raw, 0)
        assert_equal want_kind, kind
        assert_equal wrapped.value, value
        assert_equal raw.bytesize, next_off
      end
      assert_raises(Error) { Protocol.encode_param(Protocol::Int8.new(128)) }
      assert_raises(Error) { Protocol.encode_param(Protocol::Int8.new(-129)) }
      # A bare Integer still defaults to KIND_DECIMAL (server coerces per column).
      bare_kind = Protocol.decode_value(Protocol.encode_param(42), 0)[2]
      assert_equal Protocol::KIND_DECIMAL, bare_kind
    end

    def test_uint_round_trip
      cases = [
        [Protocol::Uint8.new(0), Protocol::KIND_UINT8],
        [Protocol::Uint8.new(255), Protocol::KIND_UINT8],
        [Protocol::Uint16.new(0), Protocol::KIND_UINT16],
        [Protocol::Uint16.new(65_535), Protocol::KIND_UINT16],
        [Protocol::Uint32.new(0), Protocol::KIND_UINT32],
        [Protocol::Uint32.new(4_294_967_295), Protocol::KIND_UINT32],
        [Protocol::Uint64.new(0), Protocol::KIND_UINT64],
        [Protocol::Uint64.new(18_446_744_073_709_551_615), Protocol::KIND_UINT64],
      ]
      cases.each do |wrapped, want_kind|
        raw = Protocol.encode_param(wrapped)
        value, next_off, kind = Protocol.decode_value(raw, 0)
        assert_equal want_kind, kind
        assert_equal wrapped.value, value
        assert_equal raw.bytesize, next_off
      end
      assert_raises(Error) { Protocol.encode_param(Protocol::Uint8.new(256)) }
      assert_raises(Error) { Protocol.encode_param(Protocol::Uint8.new(-1)) }
      # A bare Integer still defaults to KIND_DECIMAL (server coerces per column).
      bare_kind = Protocol.decode_value(Protocol.encode_param(42), 0)[2]
      assert_equal Protocol::KIND_DECIMAL, bare_kind
    end

    def test_enum_round_trip
      labels = %w[small medium large]
      raw = Protocol.encode_param(Protocol::EnumValue.new(value: "medium", labels: labels))
      value, next_off, kind = Protocol.decode_value(raw, 0)
      assert_equal Protocol::KIND_ENUM, kind
      assert_equal "medium", value
      assert_equal raw.bytesize, next_off
      assert_raises(Error) do
        Protocol.encode_param(Protocol::EnumValue.new(value: "huge", labels: labels))
      end
      # decode_row_desc parses the same label-list framing for a column
      # header (kind byte, 5 bytes of Precision/Scale/VecElem meta, then
      # the ENUM label-count u16 + each u16-length-prefixed label).
      row_desc = +Protocol.u16le(1)
      row_desc << Protocol.u16str("sz")
      row_desc << Protocol::KIND_ENUM.chr
      row_desc << "\x00" * 5
      row_desc << Protocol.append_enum_labels(labels)
      cols = Protocol.decode_row_desc(row_desc)
      assert_equal 1, cols.size
      assert_equal "sz", cols[0].name
      assert_equal Protocol::KIND_ENUM, cols[0].kind
      assert_equal labels, cols[0].labels
    end

    def test_timestamptz_round_trip
      t = Time.utc(2026, 9, 2, 12, 34, 56, 789_000)
      raw = Protocol.encode_param(t)
      value, _, kind = Protocol.decode_value(raw, 0)
      assert_equal Protocol::KIND_TIMESTAMPTZ, kind
      assert_in_delta t.to_r, value.to_r, 0.000001
    end

    def test_date_round_trip
      d = Date.new(2024, 1, 15)
      raw = Protocol.encode_param(d)
      value, next_off, kind = Protocol.decode_value(raw, 0)
      assert_equal Protocol::KIND_DATE, kind
      assert_equal d, value
      assert_equal raw.bytesize, next_off
      # Pre-1970 dates round-trip too (signed day count).
      pre = Date.new(1900, 1, 1)
      assert_equal pre, Protocol.decode_value(Protocol.encode_param(pre), 0)[0]
      # DateTime is Date's own subclass, but must still default to
      # TIMESTAMPTZ (existing behavior), not be mistaken for a bare Date.
      assert_equal Protocol::KIND_TIMESTAMPTZ, Protocol.decode_value(Protocol.encode_param(DateTime.new(2024, 1, 15)), 0)[2]
    end

    def test_time_of_day_round_trip
      ns = (23 * 3600 + 59 * 60 + 59) * 1_000_000_000 + 999_000_000
      raw = Protocol.encode_param(Protocol::TimeOfDay.new(nanos_since_midnight: ns))
      value, next_off, kind = Protocol.decode_value(raw, 0)
      assert_equal Protocol::KIND_TIME, kind
      assert_equal ns, value
      assert_equal raw.bytesize, next_off
    end

    def test_naive_timestamp_round_trip
      # A local-zoned Time whose wall-clock fields, not its absolute
      # instant, must be preserved (docs/design-datatypes.md D7).
      t = Time.new(2024, 6, 15, 10, 30, 0, "+08:00")
      raw = Protocol.encode_param(Protocol::NaiveTimestamp.new(value: t))
      value, next_off, kind = Protocol.decode_value(raw, 0)
      assert_equal Protocol::KIND_TIMESTAMP, kind
      assert_equal [2024, 6, 15, 10, 30, 0], [value.year, value.month, value.day, value.hour, value.min, value.sec]
      assert_equal raw.bytesize, next_off
      # A bare Time still defaults to TIMESTAMPTZ.
      assert_equal Protocol::KIND_TIMESTAMPTZ, Protocol.decode_value(Protocol.encode_param(t), 0)[2]
    end

    def test_float32_float64_round_trip
      [[Protocol::Float32.new(1.5), Protocol::KIND_FLOAT32], [Protocol::Float64.new(1.5), Protocol::KIND_FLOAT64]].each do |wrapped, want_kind|
        raw = Protocol.encode_param(wrapped)
        value, next_off, kind = Protocol.decode_value(raw, 0)
        assert_equal want_kind, kind
        assert_in_delta 1.5, value, 0.0001
        assert_equal raw.bytesize, next_off
      end
      # NaN/Infinity are valid FLOAT values (unlike the bare-Float -> Decimal path).
      nan_value, = Protocol.decode_value(Protocol.encode_param(Protocol::Float64.new(Float::NAN)), 0)
      assert nan_value.nan?
      inf_value, = Protocol.decode_value(Protocol.encode_param(Protocol::Float64.new(Float::INFINITY)), 0)
      assert_equal Float::INFINITY, inf_value
      assert_raises(Error) { Protocol.encode_param(Float::NAN) }
    end

    def test_interval_round_trip
      iv = Protocol::Interval.new(months: 14, days: 3, nanos: 4 * 3_600_000_000_000)
      raw = Protocol.encode_param(iv)
      value, next_off, kind = Protocol.decode_value(raw, 0)
      assert_equal Protocol::KIND_INTERVAL, kind
      assert_equal iv, value
      assert_equal raw.bytesize, next_off
      # Negative nanos (e.g. "-1 hour") must round-trip exactly.
      neg = Protocol::Interval.new(months: 0, days: 0, nanos: -3_600_000_000_000)
      neg_value, = Protocol.decode_value(Protocol.encode_param(neg), 0)
      assert_equal(-3_600_000_000_000, neg_value.nanos)
    end

    def test_dense_vector_round_trip
      vec = [1.5, -2.25, 3.0]
      raw = Protocol.encode_param(vec)
      value, next_off, kind = Protocol.decode_value(raw, 0)
      assert_equal Protocol::KIND_VECTOR, kind
      assert_equal raw.bytesize, next_off
      assert_instance_of Protocol::Vector, value
      assert_equal 3, value.dim
      value.values.zip(vec).each { |got, want| assert_in_delta want, got, 0.001 }
    end

    def test_sparse_vector_round_trip
      vec = Protocol::Vector.new(dim: 100, indices: [3, 50], values: [1.0, -2.0])
      raw = Protocol.encode_vector(vec)
      value, next_off, kind = Protocol.decode_value(raw, 0)
      assert_equal Protocol::KIND_VECTOR, kind
      assert_equal raw.bytesize, next_off
      assert_equal 100, value.dim
      assert_equal [3, 50], value.indices
      value.values.zip([1.0, -2.0]).each { |got, want| assert_in_delta want, got, 0.001 }
    end

    def test_point_round_trip
      pt = Protocol::Point.new(-122.4, 37.8)
      raw = Protocol.encode_param(pt)
      value, next_off, kind = Protocol.decode_value(raw, 0)
      assert_equal Protocol::KIND_POINT, kind
      assert_equal raw.bytesize, next_off
      assert_in_delta pt.lon, value.lon
      assert_in_delta pt.lat, value.lat
    end

    def test_json_param_and_nsjb_decode
      doc = +"NSJB\x01"
      doc << "\x07" # object
      body = +Protocol.u16le(1) # 1 key
      body << Protocol.u16le(1) << "a"
      body << "\x03" << [5].pack("q<")
      doc << Protocol.u32le(body.bytesize) << body
      value = Protocol.decode_nsjb(doc)
      assert_equal({ "a" => 5 }, value)
    end
  end

  class TestRowDesc < Minitest::Test
    def test_decode_row_desc
      raw = +Protocol.u16le(2)
      raw << Protocol.u16str("id") << [Protocol::KIND_UUID, 0, 0, 0, 0, 0].pack("C6")
      raw << Protocol.u16str("name") << [Protocol::KIND_STRING, 0, 0, 0, 0, 0].pack("C6")
      cols = Protocol.decode_row_desc(raw)
      assert_equal %w[id name], cols.map(&:name)
      assert_equal Protocol::KIND_UUID, cols[0].kind
      assert_equal Protocol::KIND_STRING, cols[1].kind
    end
  end

  class TestConfigValidation < Minitest::Test
    def test_requires_address
      assert_raises(Error) { Connection.validate_config!(Config.new(user: "app", insecure_no_tls: true)) }
    end

    def test_requires_tls_off_loopback
      assert_raises(Error) do
        Connection.validate_config!(Config.new(address: "db.example.com:7210", user: "app"))
      end
    end

    def test_insecure_requires_loopback
      assert_raises(Error) do
        Connection.validate_config!(Config.new(address: "db.example.com:7210", user: "app", insecure_no_tls: true))
      end
    end

    def test_loopback_insecure_ok
      Connection.validate_config!(Config.new(address: "127.0.0.1:7210", user: "app", insecure_no_tls: true))
    end

    def test_rejects_url_shaped_address
      assert_raises(Error) do
        Connection.validate_config!(Config.new(address: "tcp://127.0.0.1:7210", user: "app", insecure_no_tls: true))
      end
    end
  end

  # A minimal stand-in for a socket: readable from a preloaded buffer,
  # writes are ignored. Used to prove Connection#unexpected drains a
  # trailing Ready frame after an Error frame without a live server.
  class FakeSocket
    def initialize(data)
      @buf = data.dup
    end

    attr_reader :buf

    def read(n)
      chunk = @buf.byteslice(0, n)
      @buf = @buf.byteslice(n, @buf.bytesize - n) || "".b
      chunk
    end

    def write(_data); end
  end

  def frame(typ, payload)
    "NSQL".b + Protocol.u16le(Protocol::VERSION) + typ.chr + "\x00" + Protocol.u32le(payload.bytesize) + payload
  end
  module_function :frame

  # Regression coverage for a real bug found in this session: several
  # existing official drivers (PHP, Node, and — via shared code — Bun and
  # Deno) never drained the trailing Ready frame the server sends after an
  # Error frame outside of a couple of call sites, leaving the connection
  # permanently desynced (every subsequent call sees the stale Ready and
  # fails with a spurious "unexpected message type") the first time any
  # query/prepare/close_statement call hit a server-side error. Fixed here,
  # and centralized in Connection#unexpected so no future call site can
  # reintroduce it by forgetting to drain.
  class TestErrorReadyDraining < Minitest::Test
    def conn_on(wire)
      conn = Connection.allocate
      conn.instance_variable_set(:@sock, FakeSocket.new(wire))
      conn.instance_variable_set(:@busy, false)
      conn
    end

    def test_unexpected_drains_trailing_ready
      error_payload = Protocol.u16str("not_found") + Protocol.u16str("unknown table")
      wire = NextSQL.frame(Protocol::TYPE_READY, "") # the trailing Ready to drain
      conn = conn_on(wire)
      err = conn.unexpected(Protocol::TYPE_ERROR, error_payload)
      assert_equal "not_found", err.error_code
      assert_equal "", conn.instance_variable_get(:@sock).buf
    end

    def test_connection_usable_after_query_error
      # Simulates exactly the sequence that desynced the buggy drivers: a
      # failed query (Error+Ready) followed by a second, successful DML
      # query (CommandComplete+Ready) on the same wire. A driver that
      # forgets to drain the first Ready would misparse it as the second
      # query's own response.
      error_payload = Protocol.u16str("not_found") + Protocol.u16str("unknown table")
      second_query_wire = NextSQL.frame(Protocol::TYPE_COMMAND_COMPLETE, [3].pack("Q<")) +
                           NextSQL.frame(Protocol::TYPE_READY, "")
      wire = NextSQL.frame(Protocol::TYPE_ERROR, error_payload) + NextSQL.frame(Protocol::TYPE_READY, "") + second_query_wire
      conn = conn_on(wire)
      assert_raises(Error) { conn.send(:read_rows) }
      refute conn.busy?, "a failed query must release busy"
      rows = conn.send(:read_rows)
      assert_equal 3, rows.affected
    end
  end

  class TestHelpers < Minitest::Test
    def test_is_read_only_sql
      assert Connection.read_only_sql?("SELECT 1")
      assert Connection.read_only_sql?("  select * from t")
      assert Connection.read_only_sql?("SHOW TABLES")
      refute Connection.read_only_sql?("INSERT INTO t VALUES (1)")
      refute Connection.read_only_sql?("EXPLAIN ANALYZE SELECT 1")
      assert Connection.read_only_sql?("WITH x AS (SELECT 1) SELECT * FROM x")
      refute Connection.read_only_sql?("WITH x AS (INSERT INTO t VALUES (1) RETURNING id) SELECT * FROM x")
    end

    def test_txn_control
      assert_equal [true, false], Connection.txn_control("BEGIN")
      assert_equal [true, false], Connection.txn_control("begin snapshot")
      assert_equal [false, true], Connection.txn_control("COMMIT")
      assert_equal [false, true], Connection.txn_control("ROLLBACK")
      assert_equal [false, false], Connection.txn_control("SELECT 1")
    end

    def test_is_loopback
      assert Connection.loopback?("127.0.0.1:7210")
      assert Connection.loopback?("localhost:7210")
      assert Connection.loopback?("[::1]:7210")
      refute Connection.loopback?("db.example.com:7210")
    end
  end
end
