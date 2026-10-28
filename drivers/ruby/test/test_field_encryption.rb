# frozen_string_literal: true

require "minitest/autorun"
require "base64"
require "tmpdir"
require "fileutils"
require "bigdecimal"
require "time"
require "json"

require_relative "../lib/nextsql"

module NextSQL
  class TestFieldEncryption < Minitest::Test
    def test_generate_field_key
      k = FieldEncryption.generate_field_key("test-key.1_a")
      assert_equal "test-key.1_a", k.id
      assert_equal 32, k.material.bytesize
      refute_equal "\x00".b * 32, k.material

      assert_raises(Error) { FieldEncryption.generate_field_key("invalid key id with spaces!") }
      assert_raises(Error) { FieldEncryption.generate_field_key("") }
      assert_raises(Error) { FieldEncryption.generate_field_key("a" * 65) }
    end

    def test_memory_field_keyring
      k1 = FieldKey.new(id: "k1", material: "\x01".b * 32)
      k2 = FieldKey.new(id: "k2", material: "\x02".b * 32)
      ring = MemoryFieldKeyring.new(k1, [k2])

      cur = ring.current_field_key("db", "tbl", "col")
      assert_equal "k1", cur.id
      assert_equal "\x01".b * 32, cur.material

      resolved_k2 = ring.field_key("db", "tbl", "col", "k2")
      assert_equal "k2", resolved_k2.id

      # Rotate to k3
      k3 = FieldKey.new(id: "k3", material: "\x03".b * 32)
      ring.rotate(k3)
      assert_equal "k3", ring.current_field_key("db", "tbl", "col").id
      # k1 is still readable
      assert_equal "k1", ring.field_key("db", "tbl", "col", "k1").id

      # Revoke k1
      ring.revoke("k1")
      assert_raises(Error) { ring.field_key("db", "tbl", "col", "k1") }

      # Cannot revoke current
      assert_raises(Error) { ring.revoke("k3") }

      # Cannot reuse revoked ID
      assert_raises(Error) { ring.rotate(FieldKey.new(id: "k1", material: "\x04".b * 32)) }
    end

    def test_file_field_keyring_lifecycle
      Dir.mktmpdir do |td|
        p = File.join(td, "test.nsfk")
        k1 = FieldKey.new(id: "v1", material: "\x01".b * 32)
        kr = FileFieldKeyring.create(p, k1)

        # Re-creating same path fails
        assert_raises(Error) { FileFieldKeyring.create(p, FieldKey.new(id: "v2", material: "\x02".b * 32)) }

        # Rotate to v2
        k2 = FieldKey.new(id: "v2", material: "\x02".b * 32)
        kr.rotate(k2)
        assert_equal "v2", kr.current_field_key("db", "tbl", "col").id

        # Reopen from disk
        reopened = FileFieldKeyring.open(p)
        assert_equal "v2", reopened.current_field_key("db", "tbl", "col").id
        assert_equal "v1", reopened.field_key("db", "tbl", "col", "v1").id

        # Revoke v1
        kr.revoke("v1")
        assert_raises(Error) { kr.field_key("db", "tbl", "col", "v1") }

        # Reopened reflects revocation and zeroed material
        reopened2 = FileFieldKeyring.open(p)
        assert_raises(Error) { reopened2.field_key("db", "tbl", "col", "v1") }

        # Cannot reuse revoked ID
        assert_raises(Error) { kr.rotate(FieldKey.new(id: "v1", material: "\x05".b * 32)) }

        # Check list summaries
        lst = kr.list
        assert_equal 2, lst.length
        v1_info = lst.find { |x| x.id == "v1" }
        assert v1_info.revoked
        refute v1_info.current
        v2_info = lst.find { |x| x.id == "v2" }
        refute v2_info.revoked
        assert v2_info.current
      end
    end

    def test_file_field_keyring_go_interop_fixture
      fixture_b64 = "TlNGSwEAAgACdjEA8VNlAAAAAAIAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAJ2MmTxU2UAAAAAAQICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgIC"
      raw = Base64.decode64(fixture_b64)
      Dir.mktmpdir do |td|
        p = File.join(td, "go.nsfk")
        File.binwrite(p, raw)

        kr = FileFieldKeyring.open(p)
        current = kr.current_field_key("app", "accounts", "secret")
        assert_equal "v2", current.id
        assert_equal "\x02".b * 32, current.material

        assert_raises(Error) { kr.field_key("app", "accounts", "secret", "v1") }

        lst = kr.list
        assert_equal 2, lst.length
        assert_equal "v1", lst[0].id
        assert lst[0].revoked
        assert_equal "v2", lst[1].id
        assert lst[1].current
      end
    end

    def test_cross_driver_interop_fixtures
      v1 = FieldKey.new(id: "v1", material: "\x01".b * 32)
      ring = MemoryFieldKeyring.new(v1)

      # 1. NSCE1 fixture produced by Go driver
      go_ciphertext = "NSCE1.AQECdjEDAAAAAABEeyxf_quGP5And9z0FmNijEp3uSiDspby_y1zIxe9L1R-llGtWQxh"
      dec = FieldEncryption.decrypt(ring, "app", "accounts", "secret", FieldType::TEXT, go_ciphertext)
      assert_equal "portable", dec

      # 2. NSCE2 deterministic fixture produced by Node/Bun drivers
      node_ciphertext = "NSCE2.AgICdjEDAAAAAAB5DwopEyQ-MiLxexaaMdKrTdw36cUvjClQFjVf"
      ruby_ciphertext = FieldEncryption.encrypt_deterministic(ring, "app", "accounts", "email", FieldType::TEXT, "portable")
      assert_equal node_ciphertext, ruby_ciphertext
      dec_det = FieldEncryption.decrypt_deterministic(ring, "app", "accounts", "email", FieldType::TEXT, node_ciphertext)
      assert_equal "portable", dec_det
    end

    def test_nsce1_roundtrip_all_types
      key = FieldKey.new(id: "app-v1", material: "\x42".b * 32)
      ring = MemoryFieldKeyring.new(key)
      db = "testdb"
      tbl = "users"
      col = "secret"

      test_cases = [
        [FieldType::STRING, "Hello World!"],
        [FieldType::TEXT, "Longer text paragraph with unicode: \u2764\ufe0f"],
        [FieldType::BLOB, "\x00\xff\xfe\x01\x02\x03binary".b],
        [FieldType::BOOL, true],
        [FieldType::BOOL, false],
        [FieldType::INT8, -42],
        [FieldType::INT16, -1000],
        [FieldType::INT32, -100_000],
        [FieldType::INT64, -9_000_000_000_000],
        [FieldType::UINT8, 200],
        [FieldType::UINT16, 50_000],
        [FieldType::UINT32, 3_000_000_000],
        [FieldType::UINT64, 18_000_000_000_000_000_000],
        [FieldType::UUID, "12345678-1234-5678-1234-567812345678"],
        [FieldType::TIMESTAMPTZ, Time.utc(2026, 9, 2, 12, 0, 0)],
        [FieldType::JSON, { "key" => "val", "nested" => [1, 2, 3] }],
        [FieldType.decimal(10, 2), BigDecimal("12345.67")],
      ]

      test_cases.each do |ft, val|
        ct = FieldEncryption.encrypt(ring, db, tbl, col, ft, val)
        assert ct.start_with?("NSCE1.")

        # Nonce randomness
        ct2 = FieldEncryption.encrypt(ring, db, tbl, col, ft, val)
        refute_equal ct, ct2

        dec = FieldEncryption.decrypt(ring, db, tbl, col, ft, ct)
        if ft.kind == Protocol::KIND_JSON
          assert_equal val, dec
        elsif ft.kind == Protocol::KIND_UUID
          assert_equal val.downcase, dec.downcase
        elsif ft.kind == Protocol::KIND_TIMESTAMPTZ
          assert_equal val.to_i, dec.to_i
        elsif ft.kind == Protocol::KIND_DECIMAL
          assert_equal val, dec
        else
          assert_equal val, dec
        end

        dec2 = FieldEncryption.decrypt(ring, db, tbl, col, ft, ct2)
        if ft.kind == Protocol::KIND_TIMESTAMPTZ
          assert_equal val.to_i, dec2.to_i
        else
          assert_equal dec, dec2
        end

        # Inspect public header
        info = FieldEncryption.inspect_field(ct)
        assert_equal "app-v1", info[:key_id]
        assert_equal ft.kind, info[:type].kind
        assert_equal "randomized", info[:mode]
      end

      # Nil passthrough
      assert_nil FieldEncryption.encrypt(ring, db, tbl, col, FieldType::STRING, nil)
      assert_nil FieldEncryption.decrypt(ring, db, tbl, col, FieldType::STRING, nil)
    end

    def test_nsce1_tampering_and_context_binding
      key = FieldKey.new(id: "app-v1", material: "\x42".b * 32)
      ring = MemoryFieldKeyring.new(key)
      db = "testdb"
      tbl = "users"
      col = "secret"

      ct = FieldEncryption.encrypt(ring, db, tbl, col, FieldType::STRING, "confidential")

      # Context binding: wrong database, table, or column must fail
      assert_raises(Error) { FieldEncryption.decrypt(ring, "otherdb", tbl, col, FieldType::STRING, ct) }
      assert_raises(Error) { FieldEncryption.decrypt(ring, db, "othertbl", col, FieldType::STRING, ct) }
      assert_raises(Error) { FieldEncryption.decrypt(ring, db, tbl, "othercol", FieldType::STRING, ct) }

      # Type mismatch must fail
      assert_raises(Error) { FieldEncryption.decrypt(ring, db, tbl, col, FieldType::INT32, ct) }

      # Tampered ciphertext payload
      raw_b64 = ct[("NSCE1.".length)..]
      raw_bytes = Base64.urlsafe_decode64(raw_b64)
      raw_bytes.setbyte(-1, raw_bytes.getbyte(-1) ^ 0x01)
      tampered = "NSCE1." + Base64.urlsafe_encode64(raw_bytes, padding: false)
      assert_raises(Error) { FieldEncryption.decrypt(ring, db, tbl, col, FieldType::STRING, tampered) }

      # Wrong key
      other_key = FieldKey.new(id: "app-v1", material: "\x99".b * 32)
      other_ring = MemoryFieldKeyring.new(other_key)
      assert_raises(Error) { FieldEncryption.decrypt(other_ring, db, tbl, col, FieldType::STRING, ct) }
    end

    def test_nsce2_deterministic_equality
      key = FieldKey.new(id: "app-v1", material: "\x42".b * 32)
      ring = MemoryFieldKeyring.new(key)
      db = "testdb"
      tbl = "users"
      col = "email"

      val = "user@example.com"
      ct1 = FieldEncryption.encrypt_deterministic(ring, db, tbl, col, FieldType::TEXT, val)
      ct2 = FieldEncryption.encrypt_deterministic(ring, db, tbl, col, FieldType::TEXT, val)

      assert ct1.start_with?("NSCE2.")
      # Deterministic: identical plaintexts under same context produce identical ciphertexts
      assert_equal ct1, ct2

      dec = FieldEncryption.decrypt_deterministic(ring, db, tbl, col, FieldType::TEXT, ct1)
      assert_equal val, dec

      info = FieldEncryption.inspect_field(ct1)
      assert_equal "app-v1", info[:key_id]
      assert_equal "deterministic", info[:mode]

      # Different value produces different ciphertext
      ct_diff = FieldEncryption.encrypt_deterministic(ring, db, tbl, col, FieldType::TEXT, "other@example.com")
      refute_equal ct1, ct_diff

      # Mode separation: cannot decrypt NSCE2 with decrypt, or NSCE1 with decrypt_deterministic
      assert_raises(Error) { FieldEncryption.decrypt(ring, db, tbl, col, FieldType::TEXT, ct1) }

      ct_rand = FieldEncryption.encrypt(ring, db, tbl, col, FieldType::TEXT, val)
      assert_raises(Error) { FieldEncryption.decrypt_deterministic(ring, db, tbl, col, FieldType::TEXT, ct_rand) }
    end

    def test_connection_helper_methods
      key = FieldKey.new(id: "app-v1", material: "\x77".b * 32)
      ring = MemoryFieldKeyring.new(key)
      cfg = Config.new(
        address: "127.0.0.1:7210",
        database: "app",
        user: "app",
        password: "pwd",
        insecure_no_tls: true,
        field_keys: ring,
      )
      cfg_nokey = Config.new(
        address: "127.0.0.1:7210",
        database: "app",
        user: "app",
        password: "pwd",
        insecure_no_tls: true,
      )

      conn = Connection.allocate
      conn.instance_variable_set(:@cfg, cfg)

      ct = conn.encrypt_field("accounts", "ssn", FieldType::STRING, "123-45-6789")
      assert ct.start_with?("NSCE1.")
      dec = conn.decrypt_field("accounts", "ssn", FieldType::STRING, ct)
      assert_equal "123-45-6789", dec

      ct_det = conn.encrypt_field_deterministic("accounts", "email", FieldType::TEXT, "a@b.com")
      assert ct_det.start_with?("NSCE2.")
      dec_det = conn.decrypt_field_deterministic("accounts", "email", FieldType::TEXT, ct_det)
      assert_equal "a@b.com", dec_det

      conn_nokey = Connection.allocate
      conn_nokey.instance_variable_set(:@cfg, cfg_nokey)
      assert_raises(Error) { conn_nokey.encrypt_field("accounts", "ssn", FieldType::STRING, "123") }
    end
  end
end
