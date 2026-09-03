# frozen_string_literal: true

# Cluster is a routing client over every node of a NextSQL HA cluster.
#
# With +Config#read_consistency+ set to READ_BOUNDED or READ_STALE it sends
# eligible read-only statements to a healthy follower (round-robin, falling
# back to the leader) and everything else — writes, DDL, transaction
# control, and STRONG reads — to the leader. With the default STRONG
# consistency every statement goes to the leader and Cluster is just a
# leader-failover wrapper.
#
# A Cluster is safe for sequential use from one thread. Like Connection, an
# open Rows pins its connection until closed.

require_relative "client"
require_relative "protocol"

module NextSQL
  ClusterConn = Struct.new(:addr, :conn, :status, :seen, keyword_init: true) do
    def initialize(**kwargs)
      super({ status: nil, seen: 0.0 }.merge(kwargs))
    end
  end

  class Cluster
    STATUS_TTL = 0.5 # seconds

    class << self
      def connect(cfg)
        addrs = cfg.nodes && !cfg.nodes.empty? ? cfg.nodes : (cfg.address && !cfg.address.empty? ? [cfg.address] : [])
        raise Error.new("invalid_argument", "at least one node address is required") if addrs.empty?

        cl = new
        cl.instance_variable_set(:@read_consistency, cfg.read_consistency)
        conns = []
        first_err = nil
        addrs.each do |addr|
          nc = cfg.dup
          nc.address = addr
          nc.nodes = []
          begin
            conns << ClusterConn.new(addr: addr, conn: Connection.connect(nc))
          rescue StandardError => e
            first_err ||= e
          end
        end
        raise(first_err || Error.new("unavailable", "no reachable node")) if conns.empty?

        cl.instance_variable_set(:@conns, conns)
        cl
      end

      private :new
    end

    def initialize
      @conns = []
      @rr = 0
      @in_txn = false
      @read_consistency = Protocol::READ_STRONG
    end

    def close
      @conns.each { |cc| cc.conn.close }
    end

    def nodes
      refresh
      @conns.filter_map(&:status)
    end

    def exec(sql, params = [])
      query(sql, params).collect
    end

    def query(sql, params = [])
      begin_, end_ = Connection.txn_control(sql)
      routable = !@in_txn && !begin_ && !end_ &&
                 @read_consistency != Protocol::READ_STRONG &&
                 Connection.read_only_sql?(sql)

      if routable
        fc = follower_cluster_conn
        if fc
          begin
            return fc.conn.query(sql, params)
          rescue Error => e
            if transport_failure?(e)
              fc.status = nil
              fc.seen = 0.0
            elsif e.error_code != "unavailable"
              raise
            end
            # The follower lost the leader, fell outside the bound, or its
            # connection just broke; the leader can always answer, so fall
            # through.
          end
        end
      end

      leader_cc = leader_cluster_conn
      rows = begin
        leader_cc.conn.query(sql, params)
      rescue Error => e
        if transport_failure?(e)
          # The connection we cached as "the leader" just broke — most
          # commonly because that node lost leadership and was then
          # drained or restarted for planned maintenance before the
          # status cache caught up. Stop trusting that cached role (the
          # next refresh re-probes) and surface "unavailable" instead of
          # the raw transport error, so a caller already retrying on it
          # (the standard way to survive a genuine leader failover)
          # transparently survives this case too.
          leader_cc.status = nil
          leader_cc.seen = 0.0
          raise Error.new("unavailable", "leader connection failed: #{e.message}")
        end
        raise
      end
      @in_txn = begin_ if begin_ || end_
      rows
    end

    private

    def transport_failure?(err)
      # A broken connection (dial/read/write failure), not an application-
      # level rejection the server sent back deliberately — see
      # drivers/go/cluster.go isTransportFailure for the full reasoning
      # this mirrors. Server-sent errors always decode with the server's
      # own error code, never "io", so this cannot misclassify a
      # legitimate query rejection as a dead connection.
      err.error_code == "io"
    end

    def refresh
      now = Process.clock_gettime(Process::CLOCK_MONOTONIC)
      @conns.each do |cc|
        next if now - cc.seen < STATUS_TTL

        begin
          cc.status = cc.conn.node_status
          cc.seen = Process.clock_gettime(Process::CLOCK_MONOTONIC)
        rescue Error => e
          if transport_failure?(e)
            # The underlying Connection does not reconnect on its own, so
            # a transport failure here is permanent for the lifetime of
            # this Cluster: stop trusting whatever role it last reported
            # (most dangerously "leader") rather than leaving stale data
            # in place. It stays a refresh target so a future probe is
            # still attempted, at the normal TTL cadence.
            cc.status = nil
            cc.seen = Process.clock_gettime(Process::CLOCK_MONOTONIC)
          end
          # else: keep the last known status.
        end
      end
    end

    def leader_cluster_conn
      refresh
      @conns.each do |cc|
        role = cc.status&.role
        return cc if %w[leader standalone].include?(role)
      end
      raise Error.new("unavailable", "no reachable leader")
    end

    def follower_cluster_conn
      refresh
      followers = []
      others = []
      @conns.each do |cc|
        next unless cc.status&.healthy

        if cc.status.role == "follower"
          followers << cc
        elsif %w[leader standalone].include?(cc.status.role)
          others << cc
        end
      end
      pick = followers.empty? ? others : followers
      return nil if pick.empty?

      cc = pick[@rr % pick.size]
      @rr += 1
      cc
    end
  end
end
