package replication

import (
	"github.com/hashicorp/raft"

	"github.com/bzync/nextsql/internal/nerr"
)

// ReadConsistency selects how a read observes replicated state.
type ReadConsistency uint8

const (
	// ReadStrong observes every write acknowledged before the read begins.
	// It is served only by a leader that still holds leadership, confirmed by
	// a quorum round trip (StrongReadBarrier). This is the default.
	ReadStrong ReadConsistency = iota
	// ReadBounded serves from a member's locally applied state and rejects the
	// read when that member's replication lag exceeds the caller's bound. The
	// freshness gate is FollowerReadHealthy.
	ReadBounded
	// ReadStale serves from a member's locally applied state with no freshness
	// bound. Any member that still sees a leader can serve it.
	ReadStale
)

// String names the mode for EXPLAIN, diagnostics, and errors.
func (r ReadConsistency) String() string {
	switch r {
	case ReadStrong:
		return "strong"
	case ReadBounded:
		return "bounded"
	case ReadStale:
		return "stale"
	default:
		return "unknown"
	}
}

// StrongReadBarrier confirms this node can serve a linearizable ("STRONG")
// read right now. The node must be the Raft leader and must still hold
// leadership, verified by a round trip to a quorum (raft VerifyLeader). A read
// executed after this returns nil observes every write that was acknowledged
// before the barrier completed, so STRONG reads are read-after-write consistent
// across the whole cluster.
//
// A non-leader returns an unavailable error; the caller routes the read to the
// leader. Isolating a stale leader from the quorum also fails the barrier, so a
// partitioned former leader cannot serve a STRONG read from its own log.
func (c *Cluster) StrongReadBarrier() error {
	if c == nil || c.raft == nil {
		return nerr.New(nerr.Unavailable, "replication.StrongReadBarrier", "cluster is closed")
	}
	if c.raft.State() != raft.Leader {
		return c.notLeader("replication.StrongReadBarrier")
	}
	if err := c.raft.VerifyLeader().Error(); err != nil {
		return nerr.Wrap(nerr.Unavailable, "replication.StrongReadBarrier", "leadership not verified", err)
	}
	return nil
}
