# frozen_string_literal: true

require "base64"
require "openssl"
require "securerandom"
require "bigdecimal"
require "json"

require_relative "protocol"
require_relative "errors"

module NextSQL
  FieldType = Struct.new(:kind, :precision, :scale, :vec_elem, keyword_init: true) do
    def self.decimal(precision, scale)
      if precision < 1 || precision > 38 || scale.negative? || scale > precision
        raise Error.new("invalid_argument", "unsupported client-encrypted type")
      end

      new(kind: Protocol::KIND_DECIMAL, precision: precision, scale: scale, vec_elem: 0)
    end
  end

  FieldType::UUID = FieldType.new(kind: Protocol::KIND_UUID, precision: 0, scale: 0, vec_elem: 0).freeze
  FieldType::STRING = FieldType.new(kind: Protocol::KIND_STRING, precision: 0, scale: 0, vec_elem: 0).freeze
  FieldType::TEXT = FieldType.new(kind: Protocol::KIND_TEXT, precision: 0, scale: 0, vec_elem: 0).freeze
  FieldType::BLOB = FieldType.new(kind: Protocol::KIND_BLOB, precision: 0, scale: 0, vec_elem: 0).freeze
  FieldType::TIMESTAMPTZ = FieldType.new(kind: Protocol::KIND_TIMESTAMPTZ, precision: 0, scale: 0, vec_elem: 0).freeze
  FieldType::JSON = FieldType.new(kind: Protocol::KIND_JSON, precision: 0, scale: 0, vec_elem: 0).freeze
  FieldType::BOOL = FieldType.new(kind: Protocol::KIND_BOOL, precision: 0, scale: 0, vec_elem: 0).freeze
  FieldType::INT8 = FieldType.new(kind: Protocol::KIND_INT8, precision: 0, scale: 0, vec_elem: 0).freeze
  FieldType::INT16 = FieldType.new(kind: Protocol::KIND_INT16, precision: 0, scale: 0, vec_elem: 0).freeze
  FieldType::INT32 = FieldType.new(kind: Protocol::KIND_INT32, precision: 0, scale: 0, vec_elem: 0).freeze
  FieldType::INT64 = FieldType.new(kind: Protocol::KIND_INT64, precision: 0, scale: 0, vec_elem: 0).freeze
  FieldType::UINT8 = FieldType.new(kind: Protocol::KIND_UINT8, precision: 0, scale: 0, vec_elem: 0).freeze
  FieldType::UINT16 = FieldType.new(kind: Protocol::KIND_UINT16, precision: 0, scale: 0, vec_elem: 0).freeze
  FieldType::UINT32 = FieldType.new(kind: Protocol::KIND_UINT32, precision: 0, scale: 0, vec_elem: 0).freeze
  FieldType::UINT64 = FieldType.new(kind: Protocol::KIND_UINT64, precision: 0, scale: 0, vec_elem: 0).freeze

  FieldKey = Struct.new(:id, :material, keyword_init: true) do
    def initialize(id:, material:)
      FieldEncryption.validate_key_id!(id)
      material_bytes = material.b
      if material_bytes.bytesize != FieldEncryption::KEY_SIZE || material_bytes == ("\x00".b * FieldEncryption::KEY_SIZE)
        raise Error.new("invalid_argument", "invalid field key")
      end

      super(id: id, material: material_bytes)
    end
  end

  FieldKeyInfo = Struct.new(:id, :created, :current, :revoked, keyword_init: true)

  class MemoryFieldKeyring
    def initialize(current, overlap = [])
      @keys = {}
      @revoked = {}
      all_keys = [current] + (overlap || [])
      if all_keys.length > FieldEncryption::MAX_KEYS
        raise Error.new("invalid_argument", "too many field keys")
      end

      seen = {}
      all_keys.each do |k|
        raise Error.new("invalid_argument", "invalid field key") unless k.is_a?(FieldKey)
        raise Error.new("invalid_argument", "duplicate field key id") if seen[k.id]

        seen[k.id] = true
        @keys[k.id] = k
      end
      @current_id = current.id
    end

    def current_field_key(_database, _table, _column)
      k = @keys[@current_id]
      raise Error.new("crypto", "current field key unavailable") unless k

      k
    end

    def field_key(_database, _table, _column, key_id)
      if @revoked[key_id] || !@keys.key?(key_id)
        raise Error.new("crypto", "field key unavailable or revoked")
      end

      @keys[key_id]
    end

    def rotate(key)
      raise Error.new("invalid_argument", "invalid field key") unless key.is_a?(FieldKey)
      raise Error.new("conflict", "cannot reuse a revoked field key id") if @revoked[key.id]

      if !@keys.key?(key.id) && @keys.length >= FieldEncryption::MAX_KEYS
        raise Error.new("exhausted", "field key limit reached")
      end

      @keys[key.id] = key
      @current_id = key.id
    end

    def revoke(key_id)
      if !@keys.key?(key_id) && !@revoked.key?(key_id)
        raise Error.new("not_found", "unknown field key id")
      end
      if key_id == @current_id
        raise Error.new("conflict", "cannot revoke the current field key")
      end
      return if @revoked[key_id]

      @revoked[key_id] = true
      @keys.delete(key_id)
    end
  end

  class FileFieldKeyring
    FileRecord = Struct.new(:id, :created, :current, :revoked, :material, keyword_init: true)

    attr_reader :path

    def initialize(path, records)
      @path = path
      @records = records
    end

    class << self
      def create(path, current)
        raise Error.new("already_exists", "keyring file exists") if File.exist?(path)
        raise Error.new("invalid_argument", "invalid field key") unless current.is_a?(FieldKey)

        rec = FileRecord.new(
          id: current.id,
          created: Time.now.utc,
          current: true,
          revoked: false,
          material: current.material
        )
        kr = new(path, [rec])
        kr.send(:persist!)
        kr
      end

      def open(path)
        raise Error.new("not_found", "keyring file not found") unless File.exist?(path)

        raw = File.binread(path)
        records = decode_keyring(raw)
        new(path, records)
      rescue Error
        raise
      rescue StandardError => e
        raise Error.new("io", "failed to read keyring: #{e.message}")
      end

      def decode_keyring(raw)
        raise Error.new("invalid_format", "truncated keyring") if raw.bytesize < 8
        raise Error.new("invalid_format", "bad keyring magic") if raw[0, 4] != FieldEncryption::KEYRING_MAGIC

        ver = raw[4, 2].unpack1("v")
        count = raw[6, 2].unpack1("v")
        raise Error.new("invalid_format", "unsupported keyring version") if ver != FieldEncryption::KEYRING_VERSION
        raise Error.new("invalid_format", "keyring key count exceeds limit") if count > FieldEncryption::MAX_KEYS

        off = 8
        records = []
        seen = {}
        current_count = 0

        count.times do
          raise Error.new("invalid_format", "truncated keyring record") if off >= raw.bytesize

          id_len = raw.getbyte(off)
          off += 1
          if id_len < 1 || id_len > FieldEncryption::MAX_KEY_ID || off + id_len + 8 + 1 + FieldEncryption::KEY_SIZE > raw.bytesize
            raise Error.new("invalid_format", "invalid field key id length or truncated record")
          end

          id_str = raw[off, id_len].force_encoding("utf-8")
          off += id_len
          unless FieldEncryption.valid_key_id?(id_str) && !seen[id_str]
            raise Error.new("invalid_format", "invalid or duplicate field key id")
          end

          seen[id_str] = true

          created_sec = raw[off, 8].unpack1("Q<")
          flags = raw.getbyte(off + 8)
          off += 9
          material = raw[off, FieldEncryption::KEY_SIZE]
          off += FieldEncryption::KEY_SIZE

          is_current = (flags & FieldEncryption::FLAG_CURRENT) != 0
          is_revoked = (flags & FieldEncryption::FLAG_REVOKED) != 0

          raise Error.new("invalid_format", "current key cannot be revoked") if is_current && is_revoked

          if is_revoked && material != ("\x00".b * FieldEncryption::KEY_SIZE)
            raise Error.new("invalid_format", "revoked key has non-zero material")
          end
          if !is_revoked && material == ("\x00".b * FieldEncryption::KEY_SIZE)
            raise Error.new("invalid_format", "unrevoked key has all-zero material")
          end

          current_count += 1 if is_current

          records << FileRecord.new(
            id: id_str,
            created: Time.at(created_sec).utc,
            current: is_current,
            revoked: is_revoked,
            material: material
          )
        end

        if count.positive? && current_count != 1
          raise Error.new("invalid_format", "keyring must contain exactly one current key")
        end
        if off != raw.bytesize
          raise Error.new("invalid_format", "trailing bytes after keyring records")
        end

        records
      end
    end

    def current_field_key(_database, _table, _column)
      rec = @records.find { |r| r.current && !r.revoked }
      raise Error.new("crypto", "current field key unavailable") unless rec

      FieldKey.new(id: rec.id, material: rec.material)
    end

    def field_key(_database, _table, _column, key_id)
      rec = @records.find { |r| r.id == key_id }
      if !rec || rec.revoked
        raise Error.new("crypto", "field key unavailable or revoked")
      end

      FieldKey.new(id: rec.id, material: rec.material)
    end

    def rotate(key)
      raise Error.new("invalid_argument", "invalid field key") unless key.is_a?(FieldKey)

      idx = @records.find_index { |r| r.id == key.id }
      if idx
        raise Error.new("conflict", "cannot reuse a revoked field key id") if @records[idx].revoked
      else
        if @records.length >= FieldEncryption::MAX_KEYS
          raise Error.new("exhausted", "field key limit reached")
        end

        @records << FileRecord.new(
          id: key.id,
          created: Time.now.utc,
          current: false,
          revoked: false,
          material: key.material
        )
        idx = @records.length - 1
      end

      @records.each { |r| r.current = false }
      @records[idx].current = true
      @records[idx].material = key.material
      persist!
    end

    def revoke(key_id)
      idx = @records.find_index { |r| r.id == key_id }
      raise Error.new("not_found", "unknown field key id") unless idx
      if @records[idx].current
        raise Error.new("conflict", "cannot revoke the current field key")
      end
      return if @records[idx].revoked

      @records[idx].revoked = true
      @records[idx].material = "\x00".b * FieldEncryption::KEY_SIZE
      persist!
    end

    def reload
      raw = File.binread(@path)
      @records = self.class.decode_keyring(raw)
    rescue Error
      raise
    rescue StandardError => e
      raise Error.new("io", "failed to reload keyring: #{e.message}")
    end

    def list
      @records.map do |r|
        FieldKeyInfo.new(id: r.id, created: r.created, current: r.current, revoked: r.revoked)
      end
    end

    private

    def persist!
      raw = encode_keyring
      tmp = "#{@path}.tmp.#{Process.pid}.#{SecureRandom.hex(4)}"
      File.open(tmp, File::WRONLY | File::CREAT | File::EXCL, 0o600) do |f|
        f.binmode
        f.write(raw)
        f.flush
        f.fsync
      end
      File.rename(tmp, @path)
    rescue StandardError => e
      File.unlink(tmp) if File.exist?(tmp)
      raise Error.new("io", "failed to persist keyring: #{e.message}")
    end

    def encode_keyring
      buf = +"".b
      buf << FieldEncryption::KEYRING_MAGIC
      buf << [FieldEncryption::KEYRING_VERSION].pack("v")
      buf << [@records.length].pack("v")

      @records.each do |r|
        id_b = r.id.encode("utf-8").b
        buf << [id_b.bytesize].pack("C")
        buf << id_b
        buf << [r.created.to_i].pack("Q<")
        flags = 0
        flags |= FieldEncryption::FLAG_CURRENT if r.current
        flags |= FieldEncryption::FLAG_REVOKED if r.revoked
        buf << [flags].pack("C")
        buf << r.material
      end

      buf
    end
  end

  module FieldEncryption
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

    KEYRING_MAGIC = "NSFK".b
    KEYRING_VERSION = 1
    FLAG_CURRENT = 1 << 0
    FLAG_REVOKED = 1 << 1

    KEY_ID_RE = /\A[A-Za-z0-9._-]+\z/

    class << self
      def generate_field_key(id)
        validate_key_id!(id)
        FieldKey.new(id: id, material: SecureRandom.random_bytes(KEY_SIZE))
      end

      def valid_key_id?(id)
        id.is_a?(String) && id.length >= 1 && id.length <= MAX_KEY_ID && KEY_ID_RE.match?(id)
      end

      def validate_key_id!(id)
        raise Error.new("invalid_argument", "invalid field key id") unless valid_key_id?(id)
      end

      def encrypt(provider, database, table, column, field_type, value)
        return nil if value.nil?

        validate_field_type!(field_type)
        plain = encode_scalar(field_type, value)
        if plain.bytesize > MAX_PLAINTEXT
          raise Error.new("exhausted", "plaintext exceeds field limit")
        end

        key = provider.current_field_key(database, table, column)
        public_header = build_public_header(VERSION, SUITE_AES_256_GCM, key.id, field_type)
        nonce = SecureRandom.random_bytes(NONCE_SIZE)
        aad = build_aad(PREFIX, database, table, column, public_header)

        cipher = OpenSSL::Cipher.new("aes-256-gcm")
        cipher.encrypt
        cipher.key = key.material
        cipher.iv = nonce
        cipher.auth_data = aad
        ct = cipher.update(plain) + cipher.final
        tag = cipher.auth_tag

        body = public_header + nonce + ct + tag
        PREFIX + Base64.urlsafe_encode64(body, padding: false)
      end

      def decrypt(provider, database, table, column, expected_type, ciphertext)
        return nil if ciphertext.nil?

        validate_field_type!(expected_type)
        parsed = inspect_field(ciphertext)
        if parsed[:mode] != "randomized"
          raise Error.new("invalid_format", "deterministic ciphertext requires decrypt_field_deterministic")
        end
        if parsed[:type] != expected_type
          raise Error.new("invalid_format", "encrypted logical type mismatch")
        end

        key = provider.field_key(database, table, column, parsed[:key_id])
        body = parsed[:body]
        header_len = parsed[:header_length]
        public_header = body[0...(header_len - NONCE_SIZE)]
        nonce = body[(header_len - NONCE_SIZE)...header_len]
        ct_and_tag = body[header_len..]
        tag = ct_and_tag[-TAG_SIZE..]
        ct = ct_and_tag[0...-TAG_SIZE]
        aad = build_aad(PREFIX, database, table, column, public_header)

        cipher = OpenSSL::Cipher.new("aes-256-gcm")
        cipher.decrypt
        cipher.key = key.material
        cipher.iv = nonce
        cipher.auth_tag = tag
        cipher.auth_data = aad
        begin
          plain = cipher.update(ct) + cipher.final
        rescue OpenSSL::Cipher::CipherError
          raise Error.new("crypto", "ciphertext authentication failed")
        end

        if plain.bytesize > MAX_PLAINTEXT
          raise Error.new("invalid_format", "plaintext exceeds field limit")
        end

        decode_scalar(expected_type, plain)
      end

      def encrypt_deterministic(provider, database, table, column, field_type, value)
        return nil if value.nil?

        validate_field_type!(field_type)
        plain = encode_scalar(field_type, value)
        if plain.bytesize > MAX_PLAINTEXT
          raise Error.new("exhausted", "plaintext exceeds field limit")
        end

        key = provider.current_field_key(database, table, column)
        public_header = build_public_header(DETERMINISTIC_VERSION, SUITE_AES_SIV, key.id, field_type)
        aad = build_aad(DETERMINISTIC_PREFIX, database, table, column, public_header)

        derived = hkdf_sha256(key.material, "NextSQL NSCE2 AES-SIV v2".b, 32)
        k1 = derived[0, 16]
        k2 = derived[16, 16]

        tag = s2v(k1, aad, plain)
        cipher_bytes = ctr_crypt(k2, tag, plain)

        body = public_header + tag + cipher_bytes
        DETERMINISTIC_PREFIX + Base64.urlsafe_encode64(body, padding: false)
      end

      def decrypt_deterministic(provider, database, table, column, expected_type, ciphertext)
        return nil if ciphertext.nil?

        validate_field_type!(expected_type)
        parsed = inspect_field(ciphertext)
        if parsed[:mode] != "deterministic"
          raise Error.new("invalid_format", "randomized ciphertext requires decrypt_field")
        end
        if parsed[:type] != expected_type
          raise Error.new("invalid_format", "encrypted logical type mismatch")
        end

        key = provider.field_key(database, table, column, parsed[:key_id])
        body = parsed[:body]
        header_len = parsed[:header_length]
        public_header = body[0...header_len]
        tag = body[header_len, 16]
        ct = body[(header_len + 16)..]

        aad = build_aad(DETERMINISTIC_PREFIX, database, table, column, public_header)
        derived = hkdf_sha256(key.material, "NextSQL NSCE2 AES-SIV v2".b, 32)
        k1 = derived[0, 16]
        k2 = derived[16, 16]

        plain = ctr_crypt(k2, tag, ct)
        expected_tag = s2v(k1, aad, plain)
        unless OpenSSL.fixed_length_secure_compare(tag, expected_tag)
          raise Error.new("crypto", "ciphertext authentication failed")
        end

        if plain.bytesize > MAX_PLAINTEXT
          raise Error.new("invalid_format", "plaintext exceeds field limit")
        end

        decode_scalar(expected_type, plain)
      end

      def inspect_field(ciphertext)
        raise Error.new("invalid_format", "invalid client ciphertext") unless ciphertext.is_a?(String)

        if ciphertext.start_with?(PREFIX)
          prefix = PREFIX
          version = VERSION
          suite = SUITE_AES_256_GCM
          nonce_size = NONCE_SIZE
          mode = "randomized"
        elsif ciphertext.start_with?(DETERMINISTIC_PREFIX)
          prefix = DETERMINISTIC_PREFIX
          version = DETERMINISTIC_VERSION
          suite = SUITE_AES_SIV
          nonce_size = 0
          mode = "deterministic"
        else
          raise Error.new("invalid_format", "invalid client ciphertext prefix")
        end

        encoded = ciphertext[prefix.length..]
        max_encoded = ((MAX_PLAINTEXT + 101) * 4 / 3) + 4
        if encoded.nil? || encoded.empty? || encoded.length > max_encoded || encoded.length % 4 == 1 || !encoded.match?(/\A[A-Za-z0-9_-]+\z/)
          raise Error.new("invalid_format", "client ciphertext length out of range")
        end

        padded = encoded + ("=" * ((4 - (encoded.length % 4)) % 4))
        begin
          body = Base64.urlsafe_decode64(padded)
        rescue StandardError
          raise Error.new("invalid_format", "invalid client ciphertext encoding")
        end

        min_len = 3 + 1 + 6 + nonce_size + TAG_SIZE
        if body.bytesize < min_len || body.getbyte(0) != version || body.getbyte(1) != suite
          raise Error.new("invalid_format", "unsupported or truncated client ciphertext")
        end

        key_id_len = body.getbyte(2)
        if key_id_len < 1 || key_id_len > MAX_KEY_ID || body.bytesize < 3 + key_id_len + 6 + nonce_size + TAG_SIZE
          raise Error.new("invalid_format", "invalid field key id length")
        end

        key_id = body[3, key_id_len].force_encoding("utf-8")
        raise Error.new("invalid_format", "invalid field key id") unless valid_key_id?(key_id)

        off = 3 + key_id_len
        kind = body.getbyte(off)
        precision = body[off + 1, 2].unpack1("v")
        scale = body[off + 3, 2].unpack1("v")
        vec_elem = body.getbyte(off + 5)
        ft = FieldType.new(kind: kind, precision: precision, scale: scale, vec_elem: vec_elem)
        validate_field_type!(ft)

        header_len = off + 6 + nonce_size
        {
          key_id: key_id,
          type: ft,
          body: body,
          header_length: header_len,
          mode: mode,
        }
      end

      # --- internal helpers ---

      def validate_field_type!(ft)
        if ft.kind == Protocol::KIND_DECIMAL
          if ft.precision < 1 || ft.precision > 38 || ft.scale.negative? || ft.scale > ft.precision
            raise Error.new("invalid_argument", "unsupported client-encrypted type")
          end
          return
        end

        valid = [
          Protocol::KIND_UUID, Protocol::KIND_STRING, Protocol::KIND_TEXT, Protocol::KIND_BLOB,
          Protocol::KIND_TIMESTAMPTZ, Protocol::KIND_JSON, Protocol::KIND_BOOL,
          Protocol::KIND_INT8, Protocol::KIND_INT16, Protocol::KIND_INT32, Protocol::KIND_INT64,
          Protocol::KIND_UINT8, Protocol::KIND_UINT16, Protocol::KIND_UINT32, Protocol::KIND_UINT64,
        ]
        if !valid.include?(ft.kind) || ft.precision != 0 || ft.scale != 0 || ft.vec_elem != 0
          raise Error.new("invalid_argument", "unsupported client-encrypted type")
        end
      end

      def build_public_header(version, suite, key_id, field_type)
        key_id_b = key_id.encode("utf-8").b
        [version, suite, key_id_b.bytesize].pack("CCC") +
          key_id_b +
          [field_type.kind, field_type.precision, field_type.scale, field_type.vec_elem].pack("CvvC")
      end

      def build_aad(prefix, database, table, column, public_header)
        db_b = validate_name!(database, "database")
        tbl_b = validate_name!(table, "table")
        col_b = validate_name!(column, "column")

        out = prefix.encode("utf-8").b
        out << [db_b.bytesize].pack("v") << db_b
        out << [tbl_b.bytesize].pack("v") << tbl_b
        out << [col_b.bytesize].pack("v") << col_b
        out << public_header
        out
      end

      def validate_name!(val, what)
        raise Error.new("invalid_argument", "#{what} must not be empty") unless val.is_a?(String) && !val.empty?
        b = val.encode("utf-8").b
        raise Error.new("invalid_argument", "#{what} exceeds maximum length") if b.bytesize > 0xFFFF
        b
      end

      def encode_scalar(ft, value)
        case ft.kind
        when Protocol::KIND_UUID
          if value.is_a?(String)
            clean = value.delete("-")
            raise Error.new("invalid_argument", "invalid UUID") unless clean.match?(/\A[0-9a-fA-F]{32}\z/)
            [clean].pack("H*")
          elsif value.is_a?(String) && value.bytesize == 16
            value.b
          else
            raise Error.new("invalid_argument", "invalid UUID for encrypted field")
          end
        when Protocol::KIND_STRING, Protocol::KIND_TEXT
          raise Error.new("invalid_argument", "encrypted string value must be a string") unless value.is_a?(String)
          b = value.encode("utf-8").b
          [b.bytesize].pack("V") + b
        when Protocol::KIND_BLOB
          raise Error.new("invalid_argument", "encrypted BLOB value must be bytes") unless value.is_a?(String)
          b = value.b
          [b.bytesize].pack("V") + b
        when Protocol::KIND_BOOL
          raise Error.new("invalid_argument", "encrypted BOOL value must be boolean") unless value == true || value == false
          value ? "\x01".b : "\x00".b
        when Protocol::KIND_INT8
          raise Error.new("invalid_argument", "encrypted int value out of range") if value < -0x80 || value > 0x7F
          [value].pack("c")
        when Protocol::KIND_INT16
          raise Error.new("invalid_argument", "encrypted int value out of range") if value < -0x8000 || value > 0x7FFF
          [value].pack("s<")
        when Protocol::KIND_INT32
          raise Error.new("invalid_argument", "encrypted int value out of range") if value < -0x80000000 || value > 0x7FFFFFFF
          [value].pack("l<")
        when Protocol::KIND_INT64
          raise Error.new("invalid_argument", "encrypted int value out of range") if value < -0x8000000000000000 || value > 0x7FFFFFFFFFFFFFFF
          [value].pack("q<")
        when Protocol::KIND_UINT8
          raise Error.new("invalid_argument", "encrypted uint value out of range") if value.negative? || value > 0xFF
          [value].pack("C")
        when Protocol::KIND_UINT16
          raise Error.new("invalid_argument", "encrypted uint value out of range") if value.negative? || value > 0xFFFF
          [value].pack("S<")
        when Protocol::KIND_UINT32
          raise Error.new("invalid_argument", "encrypted uint value out of range") if value.negative? || value > 0xFFFFFFFF
          [value].pack("L<")
        when Protocol::KIND_UINT64
          raise Error.new("invalid_argument", "encrypted uint value out of range") if value.negative? || value > 0xFFFFFFFFFFFFFFFF
          [value].pack("Q<")
        when Protocol::KIND_TIMESTAMPTZ
          if value.is_a?(Time)
            ns = (value.to_r * 1_000_000_000).to_i
            [ns].pack("q<")
          elsif value.is_a?(Integer)
            [value].pack("q<")
          else
            raise Error.new("invalid_argument", "encrypted TIMESTAMPTZ value must be Time or unix nanoseconds")
          end
        when Protocol::KIND_DECIMAL
          dec_str = value.is_a?(BigDecimal) ? value.to_s("F") : value.to_s
          Protocol.encode_decimal(dec_str)
        when Protocol::KIND_JSON
          json_str = value.is_a?(String) ? value : JSON.generate(value)
          b = json_str.encode("utf-8").b
          [b.bytesize].pack("V") + b
        else
          raise Error.new("invalid_argument", "unsupported client-encrypted type")
        end
      end

      def decode_scalar(ft, raw)
        case ft.kind
        when Protocol::KIND_UUID
          raise Error.new("invalid_format", "invalid encrypted UUID payload") if raw.bytesize != 16
          Protocol.format_uuid(raw)
        when Protocol::KIND_STRING, Protocol::KIND_TEXT
          raise Error.new("invalid_format", "truncated string payload") if raw.bytesize < 4
          len = raw[0, 4].unpack1("V")
          raise Error.new("invalid_format", "string payload length mismatch") if raw.bytesize != 4 + len
          raw[4..].force_encoding("utf-8")
        when Protocol::KIND_BLOB
          raise Error.new("invalid_format", "truncated blob payload") if raw.bytesize < 4
          len = raw[0, 4].unpack1("V")
          raise Error.new("invalid_format", "blob payload length mismatch") if raw.bytesize != 4 + len
          raw[4..]
        when Protocol::KIND_BOOL
          raise Error.new("invalid_format", "invalid encrypted bool payload") if raw.bytesize != 1
          raw.getbyte(0) != 0
        when Protocol::KIND_INT8
          raise Error.new("invalid_format", "invalid encrypted int payload") if raw.bytesize != 1
          raw.unpack1("c")
        when Protocol::KIND_INT16
          raise Error.new("invalid_format", "invalid encrypted int payload") if raw.bytesize != 2
          raw.unpack1("s<")
        when Protocol::KIND_INT32
          raise Error.new("invalid_format", "invalid encrypted int payload") if raw.bytesize != 4
          raw.unpack1("l<")
        when Protocol::KIND_INT64
          raise Error.new("invalid_format", "invalid encrypted int payload") if raw.bytesize != 8
          raw.unpack1("q<")
        when Protocol::KIND_UINT8
          raise Error.new("invalid_format", "invalid encrypted uint payload") if raw.bytesize != 1
          raw.unpack1("C")
        when Protocol::KIND_UINT16
          raise Error.new("invalid_format", "invalid encrypted uint payload") if raw.bytesize != 2
          raw.unpack1("S<")
        when Protocol::KIND_UINT32
          raise Error.new("invalid_format", "invalid encrypted uint payload") if raw.bytesize != 4
          raw.unpack1("L<")
        when Protocol::KIND_UINT64
          raise Error.new("invalid_format", "invalid encrypted uint payload") if raw.bytesize != 8
          raw.unpack1("Q<")
        when Protocol::KIND_TIMESTAMPTZ
          raise Error.new("invalid_format", "invalid encrypted timestamptz payload") if raw.bytesize != 8
          ns = raw.unpack1("q<")
          Time.at(Rational(ns, 1_000_000_000)).utc
        when Protocol::KIND_DECIMAL
          raise Error.new("invalid_format", "truncated decimal payload") if raw.bytesize < 4
          len = raw[0, 4].unpack1("V")
          raise Error.new("invalid_format", "decimal payload length mismatch") if raw.bytesize != 4 + len
          Protocol.decode_decimal(raw[4..])
        when Protocol::KIND_JSON
          raise Error.new("invalid_format", "truncated json payload") if raw.bytesize < 4
          len = raw[0, 4].unpack1("V")
          raise Error.new("invalid_format", "json payload length mismatch") if raw.bytesize != 4 + len
          JSON.parse(raw[4..].force_encoding("utf-8"))
        else
          raise Error.new("invalid_format", "unsupported client-encrypted type")
        end
      end

      # --- RFC 5297 AES-SIV implementation ---

      def hkdf_sha256(ikm, info, length = 32)
        prk = OpenSSL::HMAC.digest("SHA256", "".b, ikm)
        t = "".b
        okm = +"".b
        i = 1
        while okm.bytesize < length
          t = OpenSSL::HMAC.digest("SHA256", prk, t + info + [i].pack("C"))
          okm << t
          i += 1
        end
        okm[0, length]
      end

      def aes_block(key, block)
        c = OpenSSL::Cipher.new("aes-128-cbc")
        c.encrypt
        c.key = key
        c.iv = "\x00".b * 16
        c.padding = 0
        c.update(block) + c.final
      end

      def dbl(block)
        out = +"\x00".b * 16
        carry = 0
        15.downto(0) do |i|
          b = block.getbyte(i)
          out.setbyte(i, ((b << 1) & 0xFF) | carry)
          carry = (b >> 7) & 1
        end
        out.setbyte(15, out.getbyte(15) ^ 0x87) if carry != 0
        out
      end

      def xor_16(a, b)
        res = +"\x00".b * 16
        16.times { |i| res.setbyte(i, a.getbyte(i) ^ b.getbyte(i)) }
        res
      end

      def cmac(key, message)
        k1 = dbl(aes_block(key, "\x00".b * 16))
        k2 = dbl(k1)
        n = message.bytesize
        blocks = [1, (n + 15) / 16].max
        last = if n.positive? && (n % 16).zero?
                 xor_16(message[(n - 16)..], k1)
               else
                 rem = n % 16
                 pad = message[(n - rem)..] + "\x80".b + ("\x00".b * (15 - rem))
                 xor_16(pad, k2)
               end
        y = "\x00".b * 16
        (blocks - 1).times do |i|
          blk = message[(i * 16), 16]
          y = aes_block(key, xor_16(y, blk))
        end
        aes_block(key, xor_16(y, last))
      end

      def s2v(key, ad, plain)
        d = dbl(cmac(key, "\x00".b * 16))
        d = xor_16(d, cmac(key, ad))
        t = if plain.bytesize >= 16
              off = plain.bytesize - 16
              plain[0...off] + xor_16(plain[off..], d)
            else
              pad = plain + "\x80".b + ("\x00".b * (15 - plain.bytesize))
              xor_16(dbl(d), pad)
            end
        cmac(key, t)
      end

      def ctr_crypt(key, tag, input)
        q = tag.dup
        q.setbyte(8, q.getbyte(8) & 0x7F)
        q.setbyte(12, q.getbyte(12) & 0x7F)
        c = OpenSSL::Cipher.new("aes-128-ctr")
        c.encrypt
        c.key = key
        c.iv = q
        c.update(input) + c.final
      end
    end
  end
end
