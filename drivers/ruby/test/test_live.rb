# frozen_string_literal: true

# Live integration test against a running nextsqld. Mirrors
# drivers/php/tests/live.php's coverage. Requires NEXTSQL_ADDR + NEXTSQL_CA
# (TLS PEM file path); skipped otherwise.
#
# Run e.g.:
#   NEXTSQL_ADDR=127.0.0.1:7210 NEXTSQL_CA=/path/to/ca.pem \
#       ruby drivers/ruby/test/test_live.rb

require "minitest/autorun"
require_relative "../lib/nextsql"

ADDR = ENV.fetch("NEXTSQL_ADDR", "")
CA = ENV.fetch("NEXTSQL_CA", "")

if ADDR.empty? || CA.empty?
  warn "skipping test_live.rb: NEXTSQL_ADDR and NEXTSQL_CA are required"
else
  class TestLive < Minitest::Test
    def cfg
      NextSQL::Config.new(
        address: ADDR,
        database: "production",
        user: ENV.fetch("NEXTSQL_DATABASE_USER", "app"),
        password: ENV.fetch("NEXTSQL_DATABASE_PASS", "s3cret"),
        tls: NextSQL::TLSConfig.new(cafile: CA, server_name: "localhost")
      )
    end

    def test_end_to_end
      conn = NextSQL.connect(cfg)
      begin
        conn.exec(<<~SQL)
          CREATE TABLE items (
            id UUID PRIMARY KEY DEFAULT UUID(),
            sku STRING NOT NULL,
            qty DECIMAL(10,0)
          )
        SQL
        ins = conn.exec("INSERT INTO items (sku, qty) VALUES ('A-1', 3), ('B-2', 9)")
        assert_equal 2, ins.affected

        sel = conn.exec("SELECT sku, qty FROM items WHERE sku = $1", ["B-2"])
        assert_equal 1, sel.rows.size
        assert_equal "B-2", sel.rows[0][0]

        stmt = conn.prepare("SELECT sku FROM items WHERE sku = $1")
        pres = stmt.exec(["A-1"])
        assert_equal [["A-1"]], pres.rows
        stmt.close

        seen = conn.query("SELECT sku FROM items").map { |row| row[0] }
        assert_equal %w[A-1 B-2], seen.sort

        status = conn.node_status
        assert_equal "standalone", status.role
        assert status.healthy

        conn.set_read_consistency(NextSQL::READ_BOUNDED, 5000)
        bounded = conn.exec("SELECT sku FROM items WHERE sku = $1", ["A-1"])
        assert_equal 1, bounded.rows.size
        conn.set_read_consistency(NextSQL::READ_STRONG)

        # Regression check for the Error/Ready-draining fix found this
        # session: a failed query must not desync the connection.
        begin
          conn.exec("SELECT * FROM totally_bogus_table_xyz")
        rescue NextSQL::Error => e
          assert_equal "not_found", e.error_code
        end
        still_usable = conn.exec("SELECT sku FROM items WHERE sku = $1", ["A-1"])
        assert_equal 1, still_usable.rows.size
      ensure
        conn.close
      end
    end

    def test_cluster_routes_to_standalone
      cl = NextSQL.connect_cluster(cfg)
      begin
        res = cl.exec("SELECT COUNT(*) FROM system.tables")
        assert_equal 1, res.rows.size
        nodes = cl.nodes
        assert_equal "standalone", nodes[0].role
      ensure
        cl.close
      end
    end
  end
end
