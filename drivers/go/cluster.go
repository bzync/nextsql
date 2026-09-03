package nextsql

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/sql/types"
)

// statusTTL bounds how long a cached per-node NodeStatus is trusted before the
// Cluster re-probes it for a routing decision.
const statusTTL = 500 * time.Millisecond

// Cluster is a routing client over every node of a NextSQL HA cluster.
//
// With Config.ReadConsistency set to Bounded or Stale it sends eligible
// read-only statements to a healthy follower and everything else — writes, DDL,
// transaction control, and Strong reads — to the leader. With the default
// Strong consistency every statement goes to the leader and Cluster is just a
// leader-failover wrapper.
//
// A Cluster is safe for sequential use from one goroutine. Like Conn, an open
// *Rows pins its connection until closed.
type Cluster struct {
	cfg   Config
	mu    sync.Mutex
	conns []*clusterConn
	rr    int  // follower round-robin cursor
	inTxn bool // an explicit transaction is open on the leader
}

type clusterConn struct {
	addr   string
	conn   *Conn
	status NodeStatus
	seen   time.Time
}

// OpenCluster dials every address in cfg.Nodes (or cfg.Address when Nodes is
// empty) and returns a routing client. It fails only when no node could be
// reached; a partially reachable cluster is usable.
func OpenCluster(ctx context.Context, cfg Config) (*Cluster, error) {
	addrs := cfg.Nodes
	if len(addrs) == 0 && cfg.Address != "" {
		addrs = []string{cfg.Address}
	}
	if len(addrs) == 0 {
		return nil, nerr.New(nerr.InvalidArgument, "nextsql.OpenCluster", "at least one node address is required")
	}
	cl := &Cluster{cfg: cfg}
	var firstErr error
	for _, a := range addrs {
		nc := cfg
		nc.Address = a
		nc.Nodes = nil
		conn, err := OpenContext(ctx, nc)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		cl.conns = append(cl.conns, &clusterConn{addr: a, conn: conn})
	}
	if len(cl.conns) == 0 {
		return nil, firstErr
	}
	return cl, nil
}

// Close closes every underlying connection.
func (cl *Cluster) Close() error {
	cl.mu.Lock()
	defer cl.mu.Unlock()
	var err error
	for _, cc := range cl.conns {
		if e := cc.conn.Close(); e != nil && err == nil {
			err = e
		}
	}
	return err
}

// Nodes returns the last observed status of every reachable node.
func (cl *Cluster) Nodes(ctx context.Context) []NodeStatus {
	cl.refresh(ctx)
	cl.mu.Lock()
	defer cl.mu.Unlock()
	out := make([]NodeStatus, 0, len(cl.conns))
	for _, cc := range cl.conns {
		out = append(out, cc.status)
	}
	return out
}

// Exec runs a statement through the router and materializes its rows.
func (cl *Cluster) Exec(ctx context.Context, sql string, params ...types.Value) (*Result, error) {
	rows, err := cl.Query(ctx, sql, params...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := &Result{Columns: rows.Columns()}
	for rows.Next() {
		out.Rows = append(out.Rows, append([]types.Value(nil), rows.Values()...))
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out.Affected = rows.Affected()
	return out, nil
}

// Query routes one statement and returns its streaming result.
func (cl *Cluster) Query(ctx context.Context, sql string, params ...types.Value) (*Rows, error) {
	begin, end := txnControl(sql)

	cl.mu.Lock()
	routable := !cl.inTxn && !begin && !end && cl.cfg.ReadConsistency != Strong && isReadOnlySQL(sql)
	cl.mu.Unlock()

	if routable {
		if fc, ok := cl.followerConn(ctx); ok {
			rows, err := fc.conn.Query(ctx, sql, params...)
			if err == nil {
				return rows, nil
			}
			if isTransportFailure(err) {
				cl.invalidate(fc)
			} else if !nerr.HasCode(err, nerr.Unavailable) {
				return nil, err
			}
			// The follower lost the leader, fell outside the bound, or its
			// connection just broke (e.g. a graceful drain closing it out
			// from under an in-flight request); the leader can always
			// answer, so fall through.
		}
	}

	lc, err := cl.leaderConn(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := lc.conn.Query(ctx, sql, params...)
	if err != nil {
		if isTransportFailure(err) {
			// The connection we cached as "the leader" just broke — most
			// commonly because that node lost leadership and was then
			// drained or restarted for planned maintenance before our
			// statusTTL-bounded cache caught up. Stop trusting that cached
			// role so the next call re-probes for the real leader, and
			// surface Unavailable (not the raw transport error) so a
			// caller's standard retry-on-Unavailable convention — the same
			// one already used for a genuine leader failover — catches
			// this and retries instead of failing the statement outright.
			cl.invalidate(lc)
			return nil, nerr.Wrap(nerr.Unavailable, "nextsql.Cluster", "leader connection failed", err)
		}
		return nil, err
	}
	if begin || end {
		cl.mu.Lock()
		cl.inTxn = begin
		cl.mu.Unlock()
	}
	return rows, nil
}

func (cl *Cluster) leaderConn(ctx context.Context) (*clusterConn, error) {
	cl.refresh(ctx)
	cl.mu.Lock()
	defer cl.mu.Unlock()
	for _, cc := range cl.conns {
		if cc.status.Role == "leader" || cc.status.Role == "standalone" {
			return cc, nil
		}
	}
	return nil, nerr.New(nerr.Unavailable, "nextsql.Cluster", "no reachable leader")
}

func (cl *Cluster) followerConn(ctx context.Context) (*clusterConn, bool) {
	cl.refresh(ctx)
	cl.mu.Lock()
	defer cl.mu.Unlock()
	var followers, others []*clusterConn
	for _, cc := range cl.conns {
		if !cc.status.Healthy {
			continue
		}
		switch cc.status.Role {
		case "follower":
			followers = append(followers, cc)
		case "leader", "standalone":
			others = append(others, cc)
		}
	}
	pick := followers
	if len(pick) == 0 {
		pick = others
	}
	if len(pick) == 0 {
		return nil, false
	}
	cc := pick[cl.rr%len(pick)]
	cl.rr++
	return cc, true
}

// isTransportFailure reports whether err represents a broken connection
// (dial/read/write failure) rather than an application-level rejection the
// server sent back deliberately. Server-sent errors always decode with the
// server's own nerr code (see unexpected/DecodeError), never nerr.IO, so
// this check cannot misclassify a legitimate query error as a dead
// connection.
func isTransportFailure(err error) bool {
	return nerr.HasCode(err, nerr.IO)
}

// invalidate forces cc's cached NodeStatus stale so the next refresh
// re-probes it instead of continuing to trust a routing decision that just
// proved wrong (its connection broke).
func (cl *Cluster) invalidate(cc *clusterConn) {
	cl.mu.Lock()
	cc.seen = time.Time{}
	cl.mu.Unlock()
}

// refresh re-probes every node whose cached status is older than statusTTL and
// is not currently pinned by an open *Rows. A probe that fails on a genuine
// transport error clears the cached status (see the loop body) so a
// permanently broken connection stops being selected as though it were still
// the last role it reported; any other probe failure (e.g. context
// cancellation) keeps the last known status, unchanged from before.
func (cl *Cluster) refresh(ctx context.Context) {
	cl.mu.Lock()
	targets := make([]*clusterConn, 0, len(cl.conns))
	for _, cc := range cl.conns {
		if time.Since(cc.seen) >= statusTTL {
			targets = append(targets, cc)
		}
	}
	cl.mu.Unlock()
	for _, cc := range targets {
		st, err := cc.conn.NodeStatus(ctx)
		cl.mu.Lock()
		switch {
		case err == nil:
			cc.status = st
			cc.seen = time.Now()
		case isTransportFailure(err):
			// The underlying *Conn does not reconnect on its own, so a
			// transport failure here is permanent for the lifetime of this
			// Cluster: stop trusting whatever role it last reported (most
			// dangerously "leader", which would otherwise keep winning
			// leaderConn's selection forever and starve every other node)
			// rather than leaving stale data in place. It stays a refresh
			// target so a future probe still gets attempted, at the normal
			// statusTTL cadence, in case that ever changes.
			cc.status = NodeStatus{}
			cc.seen = time.Now()
		}
		cl.mu.Unlock()
	}
}

func txnControl(sql string) (begin, end bool) {
	up := strings.ToUpper(strings.TrimLeft(sql, " \t\r\n("))
	begin = strings.HasPrefix(up, "BEGIN") || strings.HasPrefix(up, "START TRANSACTION")
	end = strings.HasPrefix(up, "COMMIT") || strings.HasPrefix(up, "ROLLBACK")
	return begin, end
}

// isReadOnlySQL is a conservative check: a false negative only costs a leader
// round trip, and a false positive on a write self-corrects (the follower
// rejects it as not-leader and the caller retries on the leader). EXPLAIN is
// excluded because EXPLAIN ANALYZE executes its statement.
func isReadOnlySQL(sql string) bool {
	s := strings.TrimLeft(sql, " \t\r\n(")
	for strings.HasPrefix(s, "--") {
		i := strings.IndexByte(s, '\n')
		if i < 0 {
			return false
		}
		s = strings.TrimLeft(s[i+1:], " \t\r\n(")
	}
	up := strings.ToUpper(s)
	switch {
	case strings.HasPrefix(up, "SELECT"), strings.HasPrefix(up, "SHOW"):
		return true
	case strings.HasPrefix(up, "WITH"):
		return !strings.Contains(up, "INSERT") && !strings.Contains(up, "UPDATE") &&
			!strings.Contains(up, "DELETE") && !strings.Contains(up, "UPSERT")
	default:
		return false
	}
}
