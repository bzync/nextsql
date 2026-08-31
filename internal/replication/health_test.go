package replication

import (
	"testing"
	"time"

	"github.com/bzync/nextsql/internal/nerr"
)

// A healthy 3-node cluster: the leader reports role "leader" with zero
// staleness, every follower reports role "follower", sees the leader, and is
// within the healthy-contact window.
func TestReplicaHealthSteadyState(t *testing.T) {
	cls, _, _, _ := startRaft(t, 3)
	lead := raftLeader(t, cls)
	waitFollowersHealthy(t, cls)

	lh := lead.ReplicaHealth()
	if lh.Role != "leader" || !lh.Healthy || lh.LastContact != 0 {
		t.Fatalf("leader health: %+v", lh)
	}
	if err := lead.FollowerReadHealthy(0); err != nil {
		t.Fatalf("leader follower-read gate: %v", err)
	}
	if err := lead.FollowerReadHealthy(time.Millisecond); err != nil {
		t.Fatalf("leader ignores staleness bound: %v", err)
	}

	for _, c := range cls {
		if c.IsLeader() {
			continue
		}
		h := c.ReplicaHealth()
		if h.Role != "follower" || !h.HasLeader {
			t.Fatalf("follower health: %+v", h)
		}
		if h.LastContact < 0 || h.LastContact > HealthyContactWindow {
			t.Fatalf("follower contact age %v out of window", h.LastContact)
		}
		if !h.Healthy {
			t.Fatalf("follower reported unhealthy: %+v", h)
		}
		if err := c.FollowerReadHealthy(0); err != nil {
			t.Fatalf("healthy follower rejected unbounded read: %v", err)
		}
		if err := c.FollowerReadHealthy(time.Nanosecond); err == nil {
			t.Fatal("follower served a read tighter than its contact age")
		} else if !nerr.HasCode(err, nerr.Unavailable) {
			t.Fatalf("want unavailable, got %v", err)
		}
	}
}

// A partitioned follower loses leader contact: its contact age grows past the
// healthy window, ReplicaHealth flips to unhealthy, and FollowerReadHealthy
// rejects even an unbounded read because the replica may be arbitrarily stale.
func TestReplicaHealthPartitionedFollower(t *testing.T) {
	cls, trans, addrs, _ := startRaft(t, 3)
	if !cls[0].IsLeader() {
		t.Skip("node 0 is not the leader")
	}
	// Isolate follower 2 from both other nodes.
	for _, i := range []int{0, 1} {
		trans[2].Disconnect(addrs[i])
		trans[i].Disconnect(addrs[2])
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		h := cls[2].ReplicaHealth()
		if !h.Healthy && (h.LastContact > HealthyContactWindow || h.LastContact == NeverContacted || h.Role != "follower") {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	h := cls[2].ReplicaHealth()
	if h.Healthy {
		t.Fatalf("partitioned follower still healthy: %+v", h)
	}
	if err := cls[2].FollowerReadHealthy(0); err == nil {
		t.Fatal("partitioned follower served an unbounded follower read")
	} else if !nerr.HasCode(err, nerr.Unavailable) {
		t.Fatalf("want unavailable, got %v", err)
	}

	// The retained majority leader still serves.
	maj := cls[0]
	if !cls[0].IsLeader() {
		maj = cls[1]
	}
	if err := maj.FollowerReadHealthy(0); err != nil {
		t.Fatalf("majority leader rejected: %v", err)
	}
}

func TestReplicaHealthClosedCluster(t *testing.T) {
	var c *Cluster
	h := c.ReplicaHealth()
	if h.Role != "shutdown" || h.Healthy {
		t.Fatalf("nil cluster health: %+v", h)
	}
	if err := c.FollowerReadHealthy(0); err == nil || !nerr.HasCode(err, nerr.Unavailable) {
		t.Fatalf("nil cluster gate: %v", err)
	}
}

// Status carries the health fields for the plaintext status file.
func TestStatusCarriesHealth(t *testing.T) {
	cls, _, _, _ := startRaft(t, 3)
	lead := raftLeader(t, cls)
	waitFollowersHealthy(t, cls)
	st := lead.Status()
	if !st.Healthy || st.LastContactMS != 0 {
		t.Fatalf("leader status health: %+v", st)
	}
	for _, c := range cls {
		if c.IsLeader() {
			continue
		}
		st := c.Status()
		if st.LastContactMS < 0 {
			t.Fatalf("follower never contacted: %+v", st)
		}
	}
}

// waitFollowersHealthy blocks until every non-leader node has heard from the
// leader at least once, i.e. its health snapshot is Healthy.
func waitFollowersHealthy(t *testing.T, cls []*Cluster) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		ok := true
		for _, c := range cls {
			if c.IsLeader() {
				continue
			}
			if !c.ReplicaHealth().Healthy {
				ok = false
				break
			}
		}
		if ok {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("followers did not become healthy")
}
