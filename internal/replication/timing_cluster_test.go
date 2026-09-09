package replication

import (
	"testing"
	"time"

	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/wal"
)

// slowTimings are deliberately not the defaults, and deliberately slower: an
// operator on a contended node or a slow link raises these rather than watching
// a cluster re-campaign instead of converging. Every live test below runs on
// them, so the claim being made is that a cluster *works* on configured
// intervals — elects, replicates, fails over and refuses a partitioned write —
// not merely that Open accepted the numbers.
//
// The heartbeat is above DefaultHealthyContactWindow on purpose: under a fixed
// freshness window these clusters' followers would be judged unhealthy while
// perfectly current.
var slowTimings = Timings{
	Heartbeat:   600 * time.Millisecond,
	Election:    1200 * time.Millisecond,
	LeaderLease: 500 * time.Millisecond,
}

func testRecords() []wal.Record {
	return []wal.Record{
		{Type: wal.RecBegin, LSN: 1, TxnID: 1},
		{Type: wal.RecCommit, LSN: 2, TxnID: 1, PrevLSN: 1},
	}
}

// A cluster on configured intervals elects, replicates to quorum, and reports
// its followers healthy against the window its own heartbeat implies.
func TestConfiguredTimingsElectAndReplicate(t *testing.T) {
	cls, _, _, apps := startRaftTimings(t, 3, slowTimings)
	lead := raftLeader(t, cls)

	if got := lead.Timings(); got.Heartbeat != slowTimings.Heartbeat || got.Election != slowTimings.Election {
		t.Fatalf("cluster is not running the configured intervals: %+v", got)
	}
	if lead.HealthyContactWindow() != healthyContactHeartbeats*slowTimings.Heartbeat {
		t.Fatalf("window %v does not follow the configured heartbeat", lead.HealthyContactWindow())
	}

	if err := lead.Replicate(testRecords()); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(raftConverge)
	for time.Now().Before(deadline) {
		applied := true
		for i, c := range cls {
			if c.IsLeader() {
				continue
			}
			if apps[i].last() < 2 && c.AppliedLSN() < 2 {
				applied = false
			}
		}
		if applied {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	for i, c := range cls {
		if c.IsLeader() {
			continue
		}
		if apps[i].last() < 2 && c.AppliedLSN() < 2 {
			t.Fatalf("follower %d did not apply on configured intervals", i)
		}
		h := c.ReplicaHealth()
		if !h.Healthy {
			t.Fatalf("caught-up follower %d unhealthy on configured intervals: %+v", i, h)
		}
		if err := c.FollowerReadHealthy(0); err != nil {
			t.Fatalf("caught-up follower %d refused a follower read: %v", i, err)
		}
	}
}

// Killing the leader of a cluster on configured intervals elects a new one and
// writes continue. A cluster that only converges on the defaults would stall
// here.
func TestConfiguredTimingsFailover(t *testing.T) {
	cls, _, _, _ := startRaftTimings(t, 3, slowTimings)
	lead := raftLeader(t, cls)
	deadID := lead.ReplicaHealth().NodeID
	if err := lead.Shutdown(); err != nil {
		t.Fatal(err)
	}

	var next *Cluster
	deadline := time.Now().Add(raftConverge)
	for time.Now().Before(deadline) {
		for _, c := range cls {
			if c != lead && c.IsLeader() {
				next = c
			}
		}
		if next != nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if next == nil {
		t.Fatal("no leader elected after failover on configured intervals")
	}
	if next.ReplicaHealth().NodeID == deadID {
		t.Fatal("the dead leader was re-selected")
	}
	if err := next.Replicate(testRecords()); err != nil {
		t.Fatalf("new leader could not replicate: %v", err)
	}
}

// A partitioned minority on configured intervals still fails closed: the
// isolated node cannot commit, the retained majority can, and the isolated
// node stops being a follower-read target.
func TestConfiguredTimingsPartitionFailsClosed(t *testing.T) {
	cls, trans, addrs, _ := startRaftTimings(t, 3, slowTimings)
	if !cls[0].IsLeader() {
		t.Skip("node 0 is not the leader")
	}
	trans[0].Disconnect(addrs[1])
	trans[0].Disconnect(addrs[2])
	trans[1].Disconnect(addrs[0])
	trans[2].Disconnect(addrs[0])

	var maj *Cluster
	deadline := time.Now().Add(raftConverge)
	for time.Now().Before(deadline) {
		n := 0
		maj = nil
		for _, c := range []*Cluster{cls[1], cls[2]} {
			if c.IsLeader() {
				n++
				maj = c
			}
		}
		if n == 1 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if maj == nil {
		t.Fatal("majority elected no leader on configured intervals")
	}
	if err := maj.Replicate(testRecords()); err != nil {
		t.Fatalf("majority could not commit: %v", err)
	}
	if err := cls[0].Replicate(testRecords()); err == nil {
		t.Fatal("isolated node committed a write")
	}

	// The isolated node must eventually stop offering follower reads. Its
	// window is five of the configured heartbeats, so this takes longer than
	// on the defaults — which is the point: the bound is the cluster's, not a
	// constant.
	deadline = time.Now().Add(raftConverge)
	for time.Now().Before(deadline) {
		if err := cls[0].FollowerReadHealthy(0); err != nil {
			if !nerr.HasCode(err, nerr.Unavailable) {
				t.Fatalf("want unavailable, got %v", err)
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("isolated node still served follower reads: %+v", cls[0].ReplicaHealth())
}
