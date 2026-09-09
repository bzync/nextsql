package replication

import (
	"time"

	"github.com/hashicorp/raft"

	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/storage/format"
)

// DefaultHealthyContactWindow is the healthy-contact window of a cluster
// running the default heartbeat. It is the fallback for callers that have no
// cluster to ask — a single-node deployment, or a read gate with no
// replication attached.
//
// A configured cluster does not use it: the window is five of *its* configured
// heartbeats (Timings.HealthyContactWindow), because an operator who raised
// raft_heartbeat_ms for a slow link has said contact is expected less often. A
// fixed window would then report every follower unhealthy between heartbeats,
// which is exactly why the first attempt at configurable timings was reverted
// (TODO.md log #256).
const DefaultHealthyContactWindow = healthyContactHeartbeats * DefaultHeartbeat

// NeverContacted is the LastContact value for a follower that has not yet heard
// from any leader since start.
const NeverContacted = time.Duration(-1)

// ReplicaHealth is a key-free snapshot of one node's replication position and
// freshness. It is safe to expose over SQL and in the plaintext status file.
type ReplicaHealth struct {
	// NodeID is this node's Raft server id.
	NodeID string
	// Role is the lowercase Raft role: "leader", "follower", "candidate", or
	// "shutdown".
	Role string
	// HasLeader is true when this node currently sees a cluster leader.
	HasLeader bool
	// AppliedLSN is the WAL LSN of the last batch this node's FSM installed.
	AppliedLSN format.LSN
	// CommitIndex is the highest Raft log index this node knows is committed.
	CommitIndex uint64
	// AppliedIndex is the highest Raft log index handed to this node's FSM.
	AppliedIndex uint64
	// ApplyBacklog is CommitIndex-AppliedIndex: entries known committed but not
	// yet applied locally. Zero on a caught-up node and on the leader.
	ApplyBacklog uint64
	// LastContact is the age of the last leader contact. Zero on the leader,
	// NeverContacted (-1) on a follower that has never heard from a leader,
	// otherwise time.Since(lastContact).
	LastContact time.Duration
	// Healthy reports whether this node is a safe follower-read target right
	// now: leader, or a follower that sees a leader and was contacted within
	// this cluster's HealthyContactWindow.
	Healthy bool
	// ReplicationSuspect reports whether this node has an unreconciled
	// replication orphan (see Cluster.ReportReplicationOrphan) and is
	// therefore refusing STRONG reads until an operator runs CLUSTER
	// RECONCILE CONFIRM.
	ReplicationSuspect bool
}

// ReplicaHealth returns this node's current replication health snapshot.
func (c *Cluster) ReplicaHealth() ReplicaHealth {
	h := ReplicaHealth{Role: "shutdown"}
	if c == nil || c.raft == nil {
		return h
	}
	h.NodeID = c.cfg.NodeID
	h.ReplicationSuspect = c.replSuspect.Load()
	state := c.raft.State()
	h.Role = raftRoleName(state)
	_, id := c.raft.LeaderWithID()
	h.HasLeader = id != ""
	h.AppliedLSN = c.AppliedLSN()
	h.CommitIndex = c.raft.CommitIndex()
	h.AppliedIndex = c.raft.AppliedIndex()
	if h.CommitIndex > h.AppliedIndex {
		h.ApplyBacklog = h.CommitIndex - h.AppliedIndex
	}
	switch state {
	case raft.Leader:
		h.LastContact = 0
		h.Healthy = true
	case raft.Follower:
		last := c.raft.LastContact()
		if last.IsZero() {
			h.LastContact = NeverContacted
		} else {
			h.LastContact = time.Since(last)
			if h.LastContact < 0 {
				h.LastContact = 0
			}
		}
		h.Healthy = healthyContact(h.HasLeader, h.LastContact, c.HealthyContactWindow())
	default:
		// candidate / shutdown: no stable leader contact.
		h.LastContact = NeverContacted
		h.Healthy = false
	}
	return h
}

// healthyContact decides whether a follower is a safe follower-read target from
// its leader visibility and contact age alone. It is the whole freshness rule,
// kept separate from the Raft plumbing so the boundary can be checked directly:
// the window is a cluster's own (Timings.HealthyContactWindow), so a raised
// heartbeat must widen it rather than leave healthy followers outside a fixed
// one.
//
// NeverContacted (-1) fails the lower bound: a follower that has never heard
// from a leader is arbitrarily stale, not maximally fresh.
func healthyContact(hasLeader bool, lastContact, window time.Duration) bool {
	return hasLeader && lastContact >= 0 && lastContact <= window
}

// Timings reports the Raft intervals this cluster resolved at Open. A nil or
// unopened cluster reports the defaults.
func (c *Cluster) Timings() Timings {
	if c == nil {
		return DefaultTimings()
	}
	t := c.cfg.Timings
	if t.Heartbeat <= 0 {
		return DefaultTimings()
	}
	return t
}

// HealthyContactWindow is how long this cluster's followers may go without
// leader contact before they stop being safe follower-read targets. It is five
// of this cluster's configured heartbeats — see Timings.HealthyContactWindow.
func (c *Cluster) HealthyContactWindow() time.Duration {
	return c.Timings().HealthyContactWindow()
}

// DefaultMaxStaleness is the freshness bound a BOUNDED read takes on this
// cluster when the session sets no explicit MAX STALENESS: the healthy-contact
// window, so the default bound and the health model agree by construction
// rather than by two constants that must be kept equal.
func (c *Cluster) DefaultMaxStaleness() time.Duration {
	return c.HealthyContactWindow()
}

// FollowerReadHealthy reports whether this node may serve a follower read now.
//
// The leader always may (its state is current by definition). A follower may
// when it still sees a leader and, if maxStaleness > 0, was last contacted by
// that leader within maxStaleness. maxStaleness <= 0 means the caller accepts
// unbounded staleness and only a total loss of leader contact is rejected.
//
// A rejected node returns an unavailable error so the caller can route the read
// elsewhere. This is the shared gate for bounded-staleness reads and for
// follower-read routing.
func (c *Cluster) FollowerReadHealthy(maxStaleness time.Duration) error {
	const op = "replication.FollowerReadHealthy"
	if c == nil || c.raft == nil {
		return nerr.New(nerr.Unavailable, op, "cluster is closed")
	}
	h := c.ReplicaHealth()
	if h.Role == "leader" {
		return nil
	}
	if h.Role != "follower" {
		return nerr.New(nerr.Unavailable, op, "node has no stable leader contact")
	}
	if !h.HasLeader {
		return nerr.New(nerr.Unavailable, op, "no leader visible; replica may be arbitrarily stale")
	}
	if h.LastContact == NeverContacted {
		return nerr.New(nerr.Unavailable, op, "replica has never heard from a leader")
	}
	if maxStaleness > 0 && h.LastContact > maxStaleness {
		return nerr.New(nerr.Unavailable, op, "replica staleness exceeds the requested bound")
	}
	return nil
}

func raftRoleName(s raft.RaftState) string {
	switch s {
	case raft.Leader:
		return "leader"
	case raft.Follower:
		return "follower"
	case raft.Candidate:
		return "candidate"
	default:
		return "shutdown"
	}
}
