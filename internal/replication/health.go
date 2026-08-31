package replication

import (
	"time"

	"github.com/hashicorp/raft"

	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/storage/format"
)

// HealthyContactWindow bounds how long a follower may go without hearing from
// the leader before it is treated as potentially partitioned and arbitrarily
// stale. A healthy follower is contacted every DefaultHeartbeat; this window is
// generous enough to ride out a single election without flapping.
const HealthyContactWindow = 5 * DefaultHeartbeat

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
	// HealthyContactWindow.
	Healthy bool
}

// ReplicaHealth returns this node's current replication health snapshot.
func (c *Cluster) ReplicaHealth() ReplicaHealth {
	h := ReplicaHealth{Role: "shutdown"}
	if c == nil || c.raft == nil {
		return h
	}
	h.NodeID = c.cfg.NodeID
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
		h.Healthy = h.HasLeader && h.LastContact >= 0 && h.LastContact <= HealthyContactWindow
	default:
		// candidate / shutdown: no stable leader contact.
		h.LastContact = NeverContacted
		h.Healthy = false
	}
	return h
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
