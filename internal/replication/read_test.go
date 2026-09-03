package replication

import (
	"testing"
	"time"

	"github.com/bzync/nextsql/internal/nerr"
)

// The leader serves a STRONG read; every follower rejects it with a
// routable unavailable error rather than serving stale local state.
func TestStrongReadBarrierLeaderOnly(t *testing.T) {
	cls, _, _, _ := startRaft(t, 3)
	lead := raftLeader(t, cls)

	if err := lead.StrongReadBarrier(); err != nil {
		t.Fatalf("leader strong read barrier: %v", err)
	}
	for _, c := range cls {
		if c.IsLeader() {
			continue
		}
		err := c.StrongReadBarrier()
		if err == nil {
			t.Fatal("follower served a strong read")
		}
		if !nerr.HasCode(err, nerr.Unavailable) {
			t.Fatalf("follower barrier: want unavailable, got %v", err)
		}
	}
}

// An isolated former leader cannot pass the barrier: VerifyLeader needs a
// quorum round trip, so a partitioned node never serves a STRONG read from
// its own log. The surviving majority elects a leader that does pass.
func TestStrongReadBarrierRejectsIsolatedLeader(t *testing.T) {
	cls, trans, addrs, _ := startRaft(t, 3)
	if !cls[0].IsLeader() {
		// startRaft bootstraps node 0; if a re-election moved leadership,
		// this test's fixed isolation targets would be wrong.
		t.Skip("node 0 is not the leader")
	}

	trans[0].Disconnect(addrs[1])
	trans[0].Disconnect(addrs[2])
	trans[1].Disconnect(addrs[0])
	trans[2].Disconnect(addrs[0])

	deadline := time.Now().Add(5 * time.Second)
	var maj *Cluster
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
		t.Fatal("surviving majority elected no leader")
	}

	if err := maj.StrongReadBarrier(); err != nil {
		t.Fatalf("new majority leader barrier: %v", err)
	}

	if err := cls[0].StrongReadBarrier(); err == nil {
		t.Fatal("isolated former leader served a strong read")
	} else if !nerr.HasCode(err, nerr.Unavailable) {
		t.Fatalf("isolated leader barrier: want unavailable, got %v", err)
	}
}

// ReportReplicationOrphan blocks STRONG reads even on a node that remains
// leader and remains verified by quorum — the failure mode a leadership
// check alone cannot catch (e.g. a Replicate call that failed for a reason
// other than losing leadership). ClearReplicationSuspect (the executor-side
// effect of CLUSTER RECONCILE CONFIRM) reverses it.
func TestStrongReadBarrierBlockedByReplicationOrphanUntilReconciled(t *testing.T) {
	cls, _, _, _ := startRaft(t, 3)
	lead := raftLeader(t, cls)

	if err := lead.StrongReadBarrier(); err != nil {
		t.Fatalf("leader barrier before orphan: %v", err)
	}
	if lead.ReplicationSuspect() {
		t.Fatal("leader reports suspect before any orphan")
	}

	lead.ReportReplicationOrphan()
	if !lead.ReplicationSuspect() {
		t.Fatal("ReportReplicationOrphan did not set the suspect flag")
	}

	err := lead.StrongReadBarrier()
	if err == nil {
		t.Fatal("suspect leader served a strong read")
	}
	if !nerr.HasCode(err, nerr.Unavailable) {
		t.Fatalf("suspect leader barrier: want unavailable, got %v", err)
	}

	// A different, un-suspect node is unaffected: the flag is node-local,
	// not cluster-wide, so a clean node never wrongly refuses.
	for _, c := range cls {
		if c == lead {
			continue
		}
		if c.ReplicationSuspect() {
			t.Fatalf("node-local suspect flag leaked to another node")
		}
	}

	lead.ClearReplicationSuspect()
	if lead.ReplicationSuspect() {
		t.Fatal("ClearReplicationSuspect did not clear the flag")
	}
	if err := lead.StrongReadBarrier(); err != nil {
		t.Fatalf("leader barrier after reconcile: %v", err)
	}
}

// A nil *Cluster is a no-op for the orphan-reporting methods, matching
// every other Cluster method's nil-receiver convention (so
// storage.Engine's optional ReplicationOrphanReporter type assertion can
// never panic even against a nil Replicator value wrapped in the
// interface).
func TestReplicationOrphanMethodsNilSafe(t *testing.T) {
	var c *Cluster
	c.ReportReplicationOrphan()
	c.ClearReplicationSuspect()
	if c.ReplicationSuspect() {
		t.Fatal("nil Cluster reports suspect")
	}
}

func TestReadConsistencyString(t *testing.T) {
	cases := map[ReadConsistency]string{
		ReadStrong:            "strong",
		ReadBounded:           "bounded",
		ReadStale:             "stale",
		ReadConsistency(0xff): "unknown",
	}
	for m, want := range cases {
		if got := m.String(); got != want {
			t.Fatalf("%d.String() = %q, want %q", m, got, want)
		}
	}
}
