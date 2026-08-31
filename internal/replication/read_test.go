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
