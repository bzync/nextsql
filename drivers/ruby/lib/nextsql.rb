# frozen_string_literal: true

# Official NextSQL Ruby driver. Speaks the native NSQL v1 protocol.
#
#   require "nextsql"
#
#   conn = NextSQL.connect(NextSQL::Config.new(
#     address: "db.example.com:7210",
#     database: "production",
#     user: "app",
#     password: "s3cret",
#     tls: NextSQL::TLSConfig.new,
#   ))
#   begin
#     result = conn.exec("SELECT id, name FROM users WHERE id = $1", [1])
#     result.rows.each { |row| puts row.inspect }
#   ensure
#     conn.close
#   end
#
# Not published as a gem — require it from this tree directly
# (+drivers/ruby/lib+ on +$LOAD_PATH+), matching every other official
# driver.

require_relative "nextsql/errors"
require_relative "nextsql/protocol"
require_relative "nextsql/client"
require_relative "nextsql/cluster"

module NextSQL
  READ_STRONG = Protocol::READ_STRONG
  READ_BOUNDED = Protocol::READ_BOUNDED
  READ_STALE = Protocol::READ_STALE

  Vector = Protocol::Vector
  Point = Protocol::Point
  Box = Protocol::Box
  Line = Protocol::Line
  Polygon = Protocol::Polygon
  NodeStatus = Protocol::NodeStatus
  Int8 = Protocol::Int8
  Int16 = Protocol::Int16
  Int32 = Protocol::Int32
  Int64 = Protocol::Int64
  Uint8 = Protocol::Uint8
  Uint16 = Protocol::Uint16
  Uint32 = Protocol::Uint32
  Uint64 = Protocol::Uint64
  EnumValue = Protocol::EnumValue
  NaiveTimestamp = Protocol::NaiveTimestamp
  TimeOfDay = Protocol::TimeOfDay
  Float32 = Protocol::Float32
  Float64 = Protocol::Float64
  Interval = Protocol::Interval

  # Opens one connection to a single node. For an HA cluster with
  # leader-failover and follower-read routing, use ::connect_cluster.
  def self.connect(cfg)
    Connection.connect(cfg)
  end

  # Opens a routing client over every node in +cfg.nodes+ (or the single
  # +cfg.address+). See Cluster for routing behavior.
  def self.connect_cluster(cfg)
    Cluster.connect(cfg)
  end
end
