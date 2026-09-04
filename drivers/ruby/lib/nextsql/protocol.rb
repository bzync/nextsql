# frozen_string_literal: true

require "date"
require "time"
require "json"
require "bigdecimal"

require_relative "errors"

module NextSQL
  # Wire encoding/decoding for NSQL v1 — mirrors drivers/php/src/Protocol.php
  # and drivers/go/nextsql.go byte-for-byte. Keep the three in sync: this is
  # a faithful reimplementation, not an independent design.
  module Protocol
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

    Column = Struct.new(:name, :kind, :labels, :coll_type)
    NodeStatus = Struct.new(:role, :has_leader, :healthy, :applied_lsn, :last_contact_ms, :apply_backlog)

    # Dense (values), reference (ref=true, dim only), or sparse
    # (indices + values) VECTOR/BITVECTOR/SPARSEVECTOR payload.
    Vector = Struct.new(:dim, :values, :indices, :ref) do
      def initialize(dim:, values: [], indices: nil, ref: false)
        super(dim, values, indices, ref)
      end
    end

    Point = Struct.new(:lon, :lat)
    Box = Struct.new(:west, :south, :east, :north)
    Line = Struct.new(:coords)
    Polygon = Struct.new(:rings)

    # Int8/16/32/64 (D2, Datatype expansion track): explicit fixed-width int
    # wrappers. A bare Integer still defaults to KIND_DECIMAL (see
    # encode_param) and coerces server-side into any numeric column — these
    # are only needed to pin an exact wire width (Ruby Integer is arbitrary
    # precision, so there is no natural bare-value mapping to one width).
    Int8 = Struct.new(:value)
    Int16 = Struct.new(:value)
    Int32 = Struct.new(:value)
    Int64 = Struct.new(:value)

    # Uint8/16/32/64 (D3, Datatype expansion track): explicit fixed-width
    # unsigned int wrappers, mirroring Int8/16/32/64 above.
    Uint8 = Struct.new(:value)
    Uint16 = Struct.new(:value)
    Uint32 = Struct.new(:value)
    Uint64 = Struct.new(:value)

    # EnumValue (D11, Datatype expansion track): an explicit ENUM parameter
    # wrapper (value:, labels:). Ordinary INSERT/UPDATE params can just pass
    # a plain String — the server coerces STRING -> ENUM against the
    # destination column, same as a SQL string literal. This wrapper exists
    # for explicit round-tripping and mirrors Int8/Uint8's precedent.
    EnumValue = Struct.new(:value, :labels) do
      def initialize(value:, labels:)
        super(value, labels)
      end
    end

    # NaiveTimestamp/TimeOfDay/Float32/Float64 (D7/D5/D8, Datatype expansion
    # track): explicit wrappers for types with no unambiguous native Ruby
    # mapping. A bare Time/DateTime already means TimestampTZ (see below), so
    # NaiveTimestamp is required to select the no-timezone Kind instead.
    # Ruby has no time-only stdlib class, so TimeOfDay carries nanoseconds
    # since midnight directly. Date needs no wrapper: it is unambiguous (not
    # a Time/DateTime superclass — DateTime is Date's *subclass*, checked
    # first below) and has no prior meaning to conflict with.
    NaiveTimestamp = Struct.new(:value) do
      def initialize(value:)
        super(value)
      end
    end
    TimeOfDay = Struct.new(:nanos_since_midnight) do
      def initialize(nanos_since_midnight:)
        super(nanos_since_midnight)
      end
    end
    Float32 = Struct.new(:value)
    Float64 = Struct.new(:value)

    # Interval (D6, Datatype expansion track): months (Integer, calendar) +
    # days (Integer, calendar) + nanos (Integer, time-of-day component) —
    # Postgres-style 3-field storage. A plain String still works as an
    # INTERVAL param for INSERT/UPDATE column assignment (server-side
    # Coerce) but not inside an arithmetic expression like `dur + $1`,
    # which requires the actual wire Kind.
    # StructValue / MapValue (Collections track, docs/design-collections.md):
    # explicit STRUCT / MAP parameter wrappers. StructValue.fields is an
    # ordered Array of [name, value]; MapValue.entries is a Hash or an Array
    # of [key, value].
    StructValue = Struct.new(:fields, keyword_init: true)
    MapValue = Struct.new(:entries, keyword_init: true)

    # Geometry (Spatial track, docs/design-spatial.md): a decoded GEOMETRY /
    # GEOGRAPHY value. +type+ is the OGC subtype name, +coordinates+ nested
    # per type (Point: [x, y]); a GeometryCollection uses +geometries+
    # instead. Also doubles as an explicit param wrapper: Geometry.new(type:
    # nil, srid: 4326, wkt: "POINT(1 2)").
    Geometry = Struct.new(:type, :srid, :coordinates, :geometries, :wkt, keyword_init: true) do
      def to_wkt
        return wkt if wkt

        pt = ->(xy) { "#{xy[0]} #{xy[1]}" }
        ring = ->(r) { "(#{r.map(&pt).join(', ')})" }
        case type
        when 'Point' then "POINT(#{pt.call(coordinates)})"
        when 'LineString' then "LINESTRING(#{coordinates.map(&pt).join(', ')})"
        when 'Polygon' then "POLYGON(#{coordinates.map(&ring).join(', ')})"
        when 'MultiPoint' then "MULTIPOINT(#{coordinates.map { |c| "(#{pt.call(c)})" }.join(', ')})"
        when 'MultiLineString' then "MULTILINESTRING(#{coordinates.map(&ring).join(', ')})"
        when 'MultiPolygon'
          "MULTIPOLYGON(#{coordinates.map { |poly| "(#{poly.map(&ring).join(', ')})" }.join(', ')})"
        when 'GeometryCollection'
          "GEOMETRYCOLLECTION(#{geometries.map(&:to_wkt).join(', ')})"
        else
          raise Error.new('invalid_argument', 'unsupported geometry type')
        end
      end
    end

    Interval = Struct.new(:months, :days, :nanos) do
      def initialize(months:, days:, nanos:)
        super(months, days, nanos)
      end
    end

    class ProtocolError < Error
      def initialize(message)
        super("protocol", message)
      end
    end

    module_function

    def need!(b, off, n, what)
      raise ProtocolError, "truncated #{what}" if off + n > b.bytesize
    end

    def u16(b, off)
      need!(b, off, 2, "u16")
      b.byteslice(off, 2).unpack1("v")
    end

    def u32(b, off)
      need!(b, off, 4, "u32")
      b.byteslice(off, 4).unpack1("V")
    end

    def u64(b, off)
      need!(b, off, 8, "u64")
      b.byteslice(off, 8).unpack1("Q<")
    end

    def i64(b, off)
      need!(b, off, 8, "i64")
      b.byteslice(off, 8).unpack1("q<")
    end

    def u16le(n) = [n].pack("v")
    def u32le(n) = [n].pack("V")
    def u64le(n) = [n & 0xFFFFFFFFFFFFFFFF].pack("Q<")

    def u16str(s, max = MAX_NAME)
      raw = s.to_s.b
      raise ProtocolError, "string exceeds limit" if raw.bytesize > max || raw.bytesize > 0xFFFF

      u16le(raw.bytesize) + raw
    end

    def u32bytes(raw, max)
      raw = raw.b
      raise ProtocolError, "bytes exceed limit" if raw.bytesize > max

      u32le(raw.bytesize) + raw
    end

    def read_u16_string(b, off, max)
      need!(b, off, 2, "string length")
      n = u16(b, off)
      raise ProtocolError, "truncated string" if n > max

      need!(b, off + 2, n, "string")
      [b.byteslice(off + 2, n).force_encoding("UTF-8"), off + 2 + n]
    end

    def read_u32_bytes(b, off, max)
      need!(b, off, 4, "bytes length")
      n = u32(b, off)
      raise ProtocolError, "truncated bytes" if n > max

      need!(b, off + 4, n, "bytes")
      [b.byteslice(off + 4, n), off + 4 + n]
    end

    # append_enum_labels/read_enum_labels carry an ENUM Type's declared
    # label list on the wire, matching internal/protocol/value.go's
    # appendEnumLabels/readEnumLabels exactly (docs/design-datatypes.md
    # D11): ENUM is the first D-track type whose Type needs variable-length
    # metadata beyond the fixed 5/6-byte Precision/Scale/VecElem shape every
    # other type fits into.
    def append_enum_labels(labels)
      out = +u16le(labels.size)
      labels.each { |l| out << u16str(l, MAX_ENUM_LABEL_BYTES) }
      out
    end

    def read_enum_labels(b, off)
      need!(b, off, 2, "enum label count")
      n = u16(b, off)
      raise ProtocolError, "enum label count exceeds limit" if n > MAX_ENUM_LABELS

      off += 2
      labels = []
      n.times do
        label, next_off = read_u16_string(b, off, MAX_ENUM_LABEL_BYTES)
        labels << label
        off = next_off
      end
      [labels, off]
    end

    def encode_hello(version, flags, secret, database, user, realm = "")
      sec = (secret || "").b
      sec = sec[0, 8].ljust(8, "\x00")
      out = u16le(version) + u16le(flags) + sec + u16str(database) + u16str(user)
      # Realm is an optional trailing field (M2-2): emitted only when
      # selected, so a Hello with no realm is byte-identical to the
      # pre-realm wire shape.
      out << u16str(realm) unless realm.nil? || realm.empty?
      out
    end

    def decode_hello_ok(b)
      raise ProtocolError, "bad hello-ok length" unless b.bytesize == 11

      [u16(b, 0), b.getbyte(2), b.byteslice(3, 8)]
    end

    def encode_query(sql, params)
      raise ProtocolError, "too many parameters" if params.size > MAX_PARAMS

      out = +u32bytes(sql.to_s.b, MAX_SQL)
      out << u16le(params.size)
      params.each { |v| out << u16str("") << encode_param(v) }
      out
    end

    def encode_execute(stmt_id, params)
      raise ProtocolError, "too many parameters" if params.size > MAX_PARAMS

      out = +u32le(stmt_id)
      out << u16le(params.size)
      params.each { |v| out << u16str("") << encode_param(v) }
      out
    end

    def encode_idempotent_query(key, sql, params)
      raise ProtocolError, "too many parameters" if params.size > MAX_PARAMS

      out = +u16str(key)
      out << u32bytes(sql.to_s.b, MAX_SQL)
      out << u16le(params.size)
      params.each { |v| out << u16str("") << encode_param(v) }
      out
    end

    def reserved5 = "\x00\x00\x00\x00\x00"

    def encode_int(kind, value, min, max, pack_fmt)
      raise Error.new("invalid_argument", "integer out of range") if value < min || value > max

      (kind.chr + "\x00" + reserved5).b + [value].pack(pack_fmt)
    end

    def encode_uint(kind, value, max, pack_fmt)
      raise Error.new("invalid_argument", "integer out of range") if value < 0 || value > max

      (kind.chr + "\x00" + reserved5).b + [value].pack(pack_fmt)
    end

    def encode_enum(label, labels)
      ord = labels.index(label)
      raise Error.new("invalid_argument", "value is not a member of the ENUM label set") if ord.nil?

      (KIND_ENUM.chr + "\x00" + reserved5).b + append_enum_labels(labels) + u16le(ord)
    end

    def encode_param(v)
      case v
      when nil
        (KIND_STRING.chr + FLAG_NULL.chr + reserved5).b
      when true, false
        (KIND_BOOL.chr + "\x00" + reserved5 + (v ? "\x01" : "\x00")).b
      when Integer
        (KIND_DECIMAL.chr + "\x00" + reserved5).b + encode_decimal(v.to_s)
      when BigDecimal
        (KIND_DECIMAL.chr + "\x00" + reserved5).b + encode_decimal(v.to_s("F"))
      when Float
        raise Error.new("invalid_argument", "parameter is not finite") unless v.finite?

        (KIND_DECIMAL.chr + "\x00" + reserved5).b + encode_decimal(format("%.17g", v))
      when String
        # A binary-encoded (ASCII-8BIT, e.g. via String#b) String is a BLOB;
        # any other encoding (the UTF-8 default included) stays STRING.
        kind = v.encoding == Encoding::ASCII_8BIT ? KIND_BLOB : KIND_STRING
        (kind.chr + "\x00" + reserved5).b + u32bytes(v.b, MAX_PACKET)
      when Time, DateTime
        t = v.is_a?(DateTime) ? v.to_time : v
        ns = (t.to_r * 1_000_000_000).to_i
        (KIND_TIMESTAMPTZ.chr + "\x00" + reserved5).b + [ns].pack("q<")
      when Date
        # Checked after Time/DateTime above — DateTime is Date's own
        # subclass in Ruby's stdlib, so only a bare Date reaches here.
        day_count = (v - Date.new(1970, 1, 1)).to_i
        (KIND_DATE.chr + "\x00" + reserved5).b + [day_count].pack("l<")
      when NaiveTimestamp
        # Treats v.value's own wall-clock fields as literal, ignoring
        # whatever offset it carries — constructs a UTC Time from the same
        # Y/M/D/H/M/S/usec fields rather than converting through the
        # absolute instant, matching "the civil value read literally with
        # no offset applied" (docs/design-datatypes.md D7). Converting
        # through the absolute instant (t.to_r directly, as TimestampTZ
        # does above) would be wrong here: a local-zoned Time's wall-clock
        # reading is not its UTC epoch value.
        t = v.value
        t = t.to_time if t.is_a?(DateTime)
        usec = (t.subsec * 1_000_000).to_i
        utc_civil = Time.utc(t.year, t.month, t.day, t.hour, t.min, t.sec, usec)
        ns = (utc_civil.to_r * 1_000_000_000).to_i
        (KIND_TIMESTAMP.chr + "\x00" + reserved5).b + [ns].pack("q<")
      when TimeOfDay
        (KIND_TIME.chr + "\x00" + reserved5).b + [v.nanos_since_midnight].pack("Q<")
      when Float32
        # NaN/+-Infinity are valid FLOAT32/FLOAT64 values (unlike the bare
        # Float -> Decimal path above, which requires finite) — the server
        # canonicalizes -0.0 -> +0.0 and every NaN payload to one value
        # (docs/design-datatypes.md D8).
        (KIND_FLOAT32.chr + "\x00" + reserved5).b + [v.value].pack("e")
      when Float64
        (KIND_FLOAT64.chr + "\x00" + reserved5).b + [v.value].pack("E")
      when Interval
        (KIND_INTERVAL.chr + "\x00" + reserved5).b + [v.months, v.days].pack("l<l<") + [v.nanos].pack("q<")
      when Int8
        encode_int(KIND_INT8, v.value, -0x80, 0x7f, "c")
      when Int16
        encode_int(KIND_INT16, v.value, -0x8000, 0x7fff, "s<")
      when Int32
        encode_int(KIND_INT32, v.value, -0x80000000, 0x7fffffff, "l<")
      when Int64
        encode_int(KIND_INT64, v.value, -0x8000000000000000, 0x7fffffffffffffff, "q<")
      when Uint8
        encode_uint(KIND_UINT8, v.value, 0xff, "C")
      when Uint16
        encode_uint(KIND_UINT16, v.value, 0xffff, "S<")
      when Uint32
        encode_uint(KIND_UINT32, v.value, 0xffffffff, "L<")
      when Uint64
        encode_uint(KIND_UINT64, v.value, 0xffffffffffffffff, "Q<")
      when EnumValue
        encode_enum(v.value, v.labels)
      when Point
        (KIND_POINT.chr + "\x00" + reserved5).b + [v.lon, v.lat].pack("E2")
      when Box
        (KIND_BOX.chr + "\x00" + reserved5).b + [v.west, v.south, v.east, v.north].pack("E4")
      when Vector
        encode_vector(v)
      when StructValue, MapValue
        encode_collection_param(v)
      when Geometry
        wkt = v.wkt || v.to_wkt
        wkt = "SRID=#{v.srid};#{wkt}" if v.srid && wkt !~ /\ASRID=/i
        (KIND_STRING.chr + "\x00" + reserved5).b + u32bytes(wkt.b, MAX_PACKET)
      when Array
        if !v.empty? && v.all? { |x| x.is_a?(Numeric) }
          encode_vector(Vector.new(dim: v.size, values: v.map(&:to_f)))
        else
          # A non-numeric (or empty) Array is an ARRAY collection param; the
          # server re-coerces element types against the destination column.
          encode_collection_param(v)
        end
      when Hash
        json = JSON.generate(v)
        (KIND_STRING.chr + "\x00" + reserved5).b + u32bytes(json.b, MAX_PACKET)
      else
        raise Error.new("invalid_argument", "unsupported parameter type: #{v.class}")
      end
    end

    # --- Collections (STRUCT / ARRAY / MAP), docs/design-collections.md -------

    def read_type_full(b, off, depth)
      need!(b, off, 6, "type")
      t = { kind: b.getbyte(off), precision: u16(b, off + 1), scale: u16(b, off + 3), elem: b.getbyte(off + 5) }
      nxt = read_nested_descriptor(b, off + 6, t, depth)
      [t, nxt]
    end

    def read_nested_descriptor(b, off, t, depth)
      raise ProtocolError, "collection type nesting too deep" if depth > MAX_NEST_DEPTH + 1

      case t[:kind]
      when KIND_ENUM
        labels, off = read_enum_labels(b, off)
        t[:labels] = labels
        off
      when KIND_ARRAY
        et, off = read_type_full(b, off, depth + 1)
        t[:elem_type] = et
        off
      when KIND_MAP
        kt, off = read_type_full(b, off, depth + 1)
        vt, off = read_type_full(b, off, depth + 1)
        t[:key_type] = kt
        t[:elem_type] = vt
        off
      when KIND_STRUCT
        need!(b, off, 2, "struct field count")
        n = u16(b, off)
        raise ProtocolError, "struct field count out of range" if n.zero? || n > MAX_STRUCT_FIELDS

        off += 2
        fields = []
        n.times do
          name, off = read_u16_string(b, off, 255)
          ft, off = read_type_full(b, off, depth + 1)
          fields << [name, ft]
        end
        t[:fields] = fields
        off
      else
        off
      end
    end

    def decode_payload(b, off, t)
      kind = t[:kind]
      return decode_collection_payload(b, off, t) if [KIND_STRUCT, KIND_ARRAY, KIND_MAP].include?(kind)

      header = (kind.chr + "\x00" + reserved5).b
      header << append_enum_labels(t[:labels] || []) if kind == KIND_ENUM
      synthetic = header + b.byteslice(off, b.bytesize - off)
      value, nxt, = decode_value(synthetic, 0)
      [value, off + (nxt - header.bytesize)]
    end

    def decode_collection_payload(b, off, t)
      need!(b, off, 4, "collection")
      body_len = u32(b, off)
      body_end = off + 4 + body_len
      need!(b, off + 4, body_len, "collection body")
      p = off + 4
      n = u32(b, p)
      p += 4
      raise ProtocolError, "collection member count out of range" if n > (2 * MAX_COLLECTION_LEN) + 2 || n > body_len

      nb = (n + 7) / 8
      nulls = b.byteslice(p, nb)
      p += nb
      kind = t[:kind]
      members = []
      n.times do |i|
        if (nulls.getbyte(i / 8) & (1 << (i % 8))) != 0
          members << nil
          next
        end
        mt = if kind == KIND_STRUCT
               t[:fields][i][1]
             elsif kind == KIND_ARRAY
               t[:elem_type]
             else
               i.even? ? t[:key_type] : t[:elem_type]
             end
        value, p = decode_payload(b, p, mt)
        members << value
      end
      case kind
      when KIND_STRUCT
        out = {}
        t[:fields].each_with_index { |(name, _), i| out[name] = members[i] }
        [out, body_end]
      when KIND_ARRAY
        [members, body_end]
      else
        out = {}
        (0...(members.size - 1)).step(2) { |i| out[members[i]] = members[i + 1] }
        [out, body_end]
      end
    end

    def encode_type_full(t)
      out = +(t[:kind].chr + reserved5).b
      case t[:kind]
      when KIND_ENUM
        out << append_enum_labels(t[:labels] || [])
      when KIND_ARRAY
        out << encode_type_full(t[:elem_type])
      when KIND_MAP
        out << encode_type_full(t[:key_type]) << encode_type_full(t[:elem_type])
      when KIND_STRUCT
        out << u16le(t[:fields].size)
        t[:fields].each { |name, ft| out << u16str(name, 255) << encode_type_full(ft) }
      end
      out
    end

    def infer_value(v)
      case v
      when nil
        return [{ kind: KIND_STRING }, nil]
      when StructValue
        fields = []
        payloads = []
        v.fields.each do |name, fv|
          ft, pl = infer_value(fv)
          fields << [name.to_s, ft]
          payloads << pl
        end
        return [{ kind: KIND_STRUCT, fields: fields }, collection_payload(payloads)]
      when MapValue
        items = v.entries.is_a?(Hash) ? v.entries.to_a : v.entries
        types = []
        payloads = []
        items.each do |k, val|
          kt, kp = infer_value(k)
          vt, vp = infer_value(val)
          types << kt << vt
          payloads << kp << vp
        end
        ki = (0...payloads.size).step(2).find { |i| !payloads[i].nil? }
        vi = (1...payloads.size).step(2).find { |i| !payloads[i].nil? }
        key_type = ki ? types[ki] : { kind: KIND_STRING }
        val_type = vi ? types[vi] : { kind: KIND_STRING }
        return [{ kind: KIND_MAP, key_type: key_type, elem_type: val_type }, collection_payload(payloads)]
      when Array
        types = []
        payloads = []
        v.each do |x|
          xt, xp = infer_value(x)
          types << xt
          payloads << xp
        end
        ei = payloads.index { |pl| !pl.nil? }
        elem_type = ei ? types[ei] : { kind: KIND_STRING }
        return [{ kind: KIND_ARRAY, elem_type: elem_type }, collection_payload(payloads)]
      end
      enc = encode_param(v)
      kind = enc.getbyte(0)
      hdr = 7
      if kind == KIND_ENUM
        lc = u16(enc, 7)
        hdr = 9
        lc.times { hdr += 2 + u16(enc, hdr) }
      end
      [{ kind: kind }, enc.byteslice(hdr, enc.bytesize - hdr)]
    end

    def collection_payload(payloads)
      n = payloads.size
      nb = (n + 7) / 8
      nulls = Array.new(nb, 0)
      chunks = +"".b
      payloads.each_with_index do |pl, i|
        if pl.nil?
          nulls[i / 8] |= 1 << (i % 8)
        else
          chunks << pl
        end
      end
      body = u32le(n) + nulls.pack("C*") + chunks
      u32le(body.bytesize) + body
    end

    def encode_collection_param(v)
      t, payload = infer_value(v)
      full = encode_type_full(t)
      type_body = full.byteslice(1, full.bytesize - 1)
      (t[:kind].chr + "\x00").b + type_body + (payload || "".b)
    end

    # --- Spatial: EWKB decode (Spatial track, docs/design-spatial.md) ------

    EWKB_TYPES = {
      1 => "Point", 2 => "LineString", 3 => "Polygon",
      4 => "MultiPoint", 5 => "MultiLineString", 6 => "MultiPolygon",
      7 => "GeometryCollection"
    }.freeze
    EWKB_SRID_FLAG = 0x20000000

    def decode_ewkb(b, off, depth)
      raise ProtocolError, "geometry nesting too deep" if depth > 8

      need!(b, off, 5, "geometry header")
      raise ProtocolError, "only little-endian EWKB is supported" unless b.getbyte(off) == 1

      tword = u32(b, off + 1)
      gtype = tword & ~EWKB_SRID_FLAG
      p = off + 5
      srid = 0
      if (tword & EWKB_SRID_FLAG) != 0
        srid = u32(b, p)
        p += 4
      end
      name = EWKB_TYPES[gtype]
      raise ProtocolError, "unknown geometry type" unless name

      f64 = lambda {
        v = b.byteslice(p, 8).unpack1("E")
        p += 8
        v
      }
      u32f = lambda {
        v = u32(b, p)
        p += 4
        v
      }
      pts = ->(n) { Array.new(n) { [f64.call, f64.call] } }

      case gtype
      when 1
        [Geometry.new(type: name, srid: srid, coordinates: [f64.call, f64.call]), p]
      when 2
        [Geometry.new(type: name, srid: srid, coordinates: pts.call(u32f.call)), p]
      when 3
        nr = u32f.call
        rings = Array.new(nr) { pts.call(u32f.call) }
        [Geometry.new(type: name, srid: srid, coordinates: rings), p]
      else
        np = u32f.call
        parts = []
        np.times do
          sub, p = decode_ewkb(b, p, depth + 1)
          parts << sub
        end
        if gtype == 7
          [Geometry.new(type: name, srid: srid, geometries: parts), p]
        else
          [Geometry.new(type: name, srid: srid, coordinates: parts.map(&:coordinates)), p]
        end
      end
    end

    def decode_value(b, off)
      need!(b, off, 7, "value header")
      kind = b.getbyte(off)
      flags = b.getbyte(off + 1)
      off += 7
      enum_labels = nil
      coll_type = nil
      if kind == KIND_ENUM
        enum_labels, off = read_enum_labels(b, off)
      elsif [KIND_STRUCT, KIND_ARRAY, KIND_MAP].include?(kind)
        coll_type = { kind: kind }
        off = read_nested_descriptor(b, off, coll_type, 0)
      end
      return [nil, off, kind] if flags & FLAG_NULL != 0

      if [KIND_STRUCT, KIND_ARRAY, KIND_MAP].include?(kind)
        value, nxt = decode_collection_payload(b, off, coll_type)
        return [value, nxt, kind]
      end

      case kind
      when KIND_ENUM
        need!(b, off, 2, "enum")
        ord = u16(b, off)
        raise ProtocolError, "ENUM ordinal out of range" if ord >= enum_labels.size

        [enum_labels[ord], off + 2, kind]
      when KIND_UUID
        need!(b, off, 16, "uuid")
        [format_uuid(b.byteslice(off, 16)), off + 16, kind]
      when KIND_STRING, KIND_TEXT, KIND_CHAR, KIND_VARCHAR
        raw, next_off = read_u32_bytes(b, off, MAX_PACKET)
        [raw.dup.force_encoding("UTF-8"), next_off, kind]
      when KIND_BLOB
        raw, next_off = read_u32_bytes(b, off, MAX_PACKET)
        [raw.dup.force_encoding("ASCII-8BIT"), next_off, kind]
      when KIND_JSON
        raw, next_off = read_u32_bytes(b, off, MAX_PACKET)
        [decode_nsjb(raw), next_off, kind]
      when KIND_DECIMAL
        raw, next_off = read_u32_bytes(b, off, MAX_PACKET)
        [decode_decimal(raw), next_off, kind]
      when KIND_TIMESTAMPTZ
        ns = i64(b, off)
        sec, nsec = ns.divmod(1_000_000_000)
        [Time.at(sec, nsec, :nanosecond, in: "UTC"), off + 8, kind]
      when KIND_TIMESTAMP
        # Naive/no-timezone: same wire shape as TimestampTZ. Ruby has no
        # distinct naive-time type, so this returns a UTC-tagged Time whose
        # fields are the intended civil value — same convention as
        # TimestampTZ's own decode above, just carrying no real zone
        # information (docs/design-datatypes.md D7).
        ns = i64(b, off)
        sec, nsec = ns.divmod(1_000_000_000)
        [Time.at(sec, nsec, :nanosecond, in: "UTC"), off + 8, kind]
      when KIND_DATE
        need!(b, off, 4, "date")
        day_count = b.byteslice(off, 4).unpack1("l<")
        [Date.new(1970, 1, 1) + day_count, off + 4, kind]
      when KIND_TIME
        need!(b, off, 8, "time")
        [u64(b, off), off + 8, kind]
      when KIND_FLOAT32
        need!(b, off, 4, "float32")
        [b.byteslice(off, 4).unpack1("e"), off + 4, kind]
      when KIND_FLOAT64
        need!(b, off, 8, "float64")
        [b.byteslice(off, 8).unpack1("E"), off + 8, kind]
      when KIND_INTERVAL
        need!(b, off, 16, "interval")
        months, days = b.byteslice(off, 8).unpack("l<l<")
        nanos = b.byteslice(off + 8, 8).unpack1("q<")
        [Interval.new(months: months, days: days, nanos: nanos), off + 16, kind]
      when KIND_BOOL
        need!(b, off, 1, "bool")
        [b.getbyte(off) != 0, off + 1, kind]
      when KIND_INT8
        need!(b, off, 1, "int8")
        [b.byteslice(off, 1).unpack1("c"), off + 1, kind]
      when KIND_INT16
        need!(b, off, 2, "int16")
        [b.byteslice(off, 2).unpack1("s<"), off + 2, kind]
      when KIND_INT32
        need!(b, off, 4, "int32")
        [b.byteslice(off, 4).unpack1("l<"), off + 4, kind]
      when KIND_INT64
        need!(b, off, 8, "int64")
        [b.byteslice(off, 8).unpack1("q<"), off + 8, kind]
      when KIND_UINT8
        need!(b, off, 1, "uint8")
        [b.getbyte(off), off + 1, kind]
      when KIND_UINT16
        need!(b, off, 2, "uint16")
        [b.byteslice(off, 2).unpack1("S<"), off + 2, kind]
      when KIND_UINT32
        need!(b, off, 4, "uint32")
        [b.byteslice(off, 4).unpack1("L<"), off + 4, kind]
      when KIND_UINT64
        need!(b, off, 8, "uint64")
        [b.byteslice(off, 8).unpack1("Q<"), off + 8, kind]
      when KIND_VECTOR
        value, next_off = decode_vector(b, off)
        [value, next_off, kind]
      when KIND_POINT
        need!(b, off, 16, "point")
        lon, lat = b.byteslice(off, 16).unpack("E2")
        [Point.new(lon, lat), off + 16, kind]
      when KIND_BOX
        need!(b, off, 32, "box")
        w, s, e, n = b.byteslice(off, 32).unpack("E4")
        [Box.new(w, s, e, n), off + 32, kind]
      when KIND_LINE
        n = u16(b, off)
        p = off + 2
        coords = []
        (n * 2).times do
          need!(b, p, 8, "line coord")
          coords << b.byteslice(p, 8).unpack1("E")
          p += 8
        end
        [Line.new(coords), p, kind]
      when KIND_GEOMETRY, KIND_GEOGRAPHY
        len = u32(b, off)
        g, = decode_ewkb(b, off + 4, 0)
        [g, off + 4 + len, kind]
      when KIND_POLYGON
        nr = u16(b, off)
        p = off + 2
        rings = []
        nr.times do
          npts = u16(b, p)
          p += 2
          ring = []
          (npts * 2).times do
            need!(b, p, 8, "polygon coord")
            ring << b.byteslice(p, 8).unpack1("E")
            p += 8
          end
          rings << ring
        end
        [Polygon.new(rings), p, kind]
      else
        raise ProtocolError, "unsupported type"
      end
    end

    def decode_vector(b, off)
      dim = u16(b, off)
      flag = b.getbyte(off + 2)
      if flag & 1 != 0
        return [Vector.new(dim: dim, ref: true), off + 3]
      end
      if flag & 2 != 0
        need!(b, off + 3, 4, "sparse nnz")
        nnz = u32(b, off + 3)
        indices = []
        values = []
        p = off + 7
        nnz.times do
          need!(b, p, 8, "sparse entry")
          indices << u32(b, p)
          values << b.byteslice(p + 4, 4).unpack1("e")
          p += 8
        end
        return [Vector.new(dim: dim, values: values, indices: indices), p]
      end
      p = off + 3
      values = []
      dim.times do
        need!(b, p, 4, "vector component")
        values << b.byteslice(p, 4).unpack1("e")
        p += 4
      end
      [Vector.new(dim: dim, values: values), p]
    end

    def decode_row_desc(b)
      n = u16(b, 0)
      off = 2
      cols = []
      n.times do
        name, off2 = read_u16_string(b, off, MAX_NAME)
        off = off2
        need!(b, off, 6, "column type")
        kind = b.getbyte(off)
        off += 6
        labels = nil
        coll_type = nil
        if kind == KIND_ENUM
          labels, off = read_enum_labels(b, off)
        elsif [KIND_STRUCT, KIND_ARRAY, KIND_MAP].include?(kind)
          coll_type = { kind: kind }
          off = read_nested_descriptor(b, off, coll_type, 0)
        end
        cols << Column.new(name, kind, labels, coll_type)
      end
      cols
    end

    def decode_data_batch(b)
      nrows = u32(b, 0)
      off = 4
      rows = []
      nrows.times do
        ncols = u16(b, off)
        off += 2
        row = []
        ncols.times do
          value, next_off, = decode_value(b, off)
          row << value
          off = next_off
        end
        rows << row
      end
      rows
    end

    def decode_command_complete(b)
      raise ProtocolError, "bad command-complete length" unless b.bytesize == 8

      u64(b, 0)
    end

    def decode_error(b)
      code, off = read_u16_string(b, 0, MAX_NAME)
      msg, = read_u16_string(b, off, MAX_NAME)
      Error.new(code, msg)
    end

    def encode_set_read_consistency(mode, max_staleness_ms)
      raise Error.new("invalid_argument", "unknown read consistency mode") unless [READ_STRONG, READ_BOUNDED,
                                                                                     READ_STALE].include?(mode)

      ms = max_staleness_ms.positive? ? max_staleness_ms : 0
      mode.chr + u64le(ms)
    end

    def decode_node_status(b)
      role, off = read_u16_string(b, 0, MAX_NAME)
      raise ProtocolError, "bad node-status length" unless b.bytesize - off == 25

      flags = b.getbyte(off)
      off += 1
      applied_lsn = u64(b, off)
      raw_contact = u64(b, off + 8)
      last_contact_ms = raw_contact == 0xFFFFFFFFFFFFFFFF ? -1 : raw_contact
      apply_backlog = u64(b, off + 16)
      NodeStatus.new(role, flags & 1 != 0, flags & 2 != 0, applied_lsn, last_contact_ms, apply_backlog)
    end

    DECIMAL_RE = /\A\d+(\.\d+)?\z/.freeze

    def encode_decimal(s)
      s = s.strip
      neg = false
      s = s[1..] if s.start_with?("+")
      if s.start_with?("-")
        neg = true
        s = s[1..]
      end
      raise Error.new("invalid_argument", "invalid decimal") unless DECIMAL_RE.match?(s)

      int_part, frac_part = s.split(".", 2)
      frac_part ||= ""
      scale = frac_part.length
      digits = (int_part + frac_part).sub(/\A0+(?=\d)/, "")
      coef = dec_to_bytes(digits)
      body = (neg ? "\x01" : "\x00").b + "\x00".b + u16le(scale) + coef
      u32bytes(body, MAX_PACKET)
    end

    def decode_decimal(body)
      raise Error.new("invalid_format", "truncated decimal") if body.bytesize < 4

      neg = body.getbyte(0) & 1 != 0
      scale = u16(body, 2)
      digits = bytes_to_dec(body.byteslice(4, body.bytesize - 4))
      s = if scale.positive?
            digits = digits.rjust(scale + 1, "0")
            "#{digits[0..-scale - 1]}.#{digits[-scale..]}"
          else
            digits
          end
      s = "-#{s}" if neg && !(s == "0" || s.match?(/\A0\.0+\z/))
      BigDecimal(s)
    end

    def dec_to_bytes(digits)
      n = digits.to_i
      return "".b if n.zero?

      nbytes = (n.bit_length + 7) / 8
      out = +"".b
      nbytes.times { |i| out.prepend(((n >> (8 * i)) & 0xFF).chr) }
      out
    end

    def bytes_to_dec(raw)
      n = 0
      raw.each_byte { |byte| n = (n * 256) + byte }
      n.to_s
    end

    def format_uuid(raw)
      hex = raw.unpack1("H*")
      "#{hex[0, 8]}-#{hex[8, 4]}-#{hex[12, 4]}-#{hex[16, 4]}-#{hex[20, 12]}"
    end

    def encode_vector(v)
      dim = v.dim && v.dim.positive? ? v.dim : v.values.size
      header = (KIND_VECTOR.chr + "\x00" + reserved5).b
      if v.indices
        payload = +(u16le(dim) + "\x02")
        payload << u32le(v.indices.size)
        v.indices.each_with_index do |idx, i|
          payload << u32le(idx)
          payload << [v.values[i]].pack("e")
        end
        return header + payload
      end
      payload = +(u16le(dim) + "\x00")
      v.values.each { |val| payload << [val].pack("e") }
      header + payload
    end

    # --- NSJB binary JSON (see internal/protocol/messages.go EncodeJSON) ---

    def decode_nsjb(doc)
      raise Error.new("invalid_format", "not binary JSON") if doc.bytesize < 5 || doc.byteslice(0,
                                                                                                  4) != "NSJB" || doc.getbyte(4) != 1

      value, next_off = read_nsjb(doc, 5)
      raise Error.new("invalid_format", "trailing JSON bytes") unless next_off == doc.bytesize

      value
    end

    def read_nsjb(b, off)
      raise Error.new("invalid_format", "truncated JSON") if off >= b.bytesize

      tag = b.getbyte(off)
      case tag
      when 0x00 then [nil, off + 1]
      when 0x01 then [false, off + 1]
      when 0x02 then [true, off + 1]
      when 0x03 then [i64(b, off + 1), off + 9]
      when 0x04 then read_nsjb_str(b, off, false)
      when 0x05 then read_nsjb_str(b, off, true)
      when 0x06 then read_nsjb_array(b, off)
      when 0x07 then read_nsjb_object(b, off)
      else raise Error.new("invalid_format", "unknown JSON tag")
      end
    end

    def read_nsjb_str(b, off, number)
      n = u32(b, off + 1)
      end_off = off + 5 + n
      s = b.byteslice(off + 5, n).force_encoding("UTF-8")
      if number
        if /\A-?\d+\z/.match?(s)
          return [s.to_i, end_off]
        elsif /\A-?\d+\.\d+\z/.match?(s)
          return [s.to_f, end_off]
        end
      end
      [s, end_off]
    end

    def read_nsjb_array(b, off)
      size = u32(b, off + 1)
      body = off + 5
      end_off = body + size
      count = u32(b, body)
      cur = body + 4
      out = []
      count.times do
        v, cur2 = read_nsjb(b, cur)
        cur = cur2
        out << v
      end
      [out, end_off]
    end

    def read_nsjb_object(b, off)
      size = u32(b, off + 1)
      body = off + 5
      end_off = body + size
      count = u16(b, body)
      cur = body + 2
      out = {}
      count.times do
        klen = u16(b, cur)
        cur += 2
        key = b.byteslice(cur, klen).force_encoding("UTF-8")
        cur += klen
        v, cur2 = read_nsjb(b, cur)
        cur = cur2
        out[key] = v
      end
      [out, end_off]
    end
  end
end
