package replication

import (
	"testing"
	"time"

	"github.com/hashicorp/raft"

	"github.com/bzync/nextsql/internal/nerr"
)

// An unconfigured cluster runs the documented defaults, and a partially
// configured one keeps the defaults for everything it did not set.
func TestTimingsResolveFillsDefaults(t *testing.T) {
	got, err := (Timings{}).Resolve()
	if err != nil {
		t.Fatal(err)
	}
	if got != DefaultTimings() {
		t.Fatalf("zero Timings resolved to %+v, want %+v", got, DefaultTimings())
	}

	// Only the heartbeat is set; election must stay at its default, which is
	// still >= the new heartbeat, and the rest are untouched.
	got, err = (Timings{Heartbeat: 200 * time.Millisecond}).Resolve()
	if err != nil {
		t.Fatal(err)
	}
	if got.Heartbeat != 200*time.Millisecond {
		t.Fatalf("heartbeat = %v, want 200ms", got.Heartbeat)
	}
	if got.Election != DefaultElection || got.LeaderLease != DefaultLeaderLease || got.CommitTimeout != DefaultCommitTimeout {
		t.Fatalf("unset intervals were not defaulted: %+v", got)
	}
}

// The freshness window is five of *this cluster's* heartbeats, not five of the
// default heartbeat. This is the coupling that reverted the first attempt at
// configurable timings (TODO.md log #256): a fixed window against a raised
// heartbeat reports every follower unhealthy between contacts.
func TestHealthyContactWindowFollowsConfiguredHeartbeat(t *testing.T) {
	if got, want := DefaultTimings().HealthyContactWindow(), DefaultHealthyContactWindow; got != want {
		t.Fatalf("default window = %v, want %v", got, want)
	}
	tm, err := (Timings{Heartbeat: 2 * time.Second, Election: 4 * time.Second}).Resolve()
	if err != nil {
		t.Fatal(err)
	}
	if got, want := tm.HealthyContactWindow(), 10*time.Second; got != want {
		t.Fatalf("window at a 2s heartbeat = %v, want %v", got, want)
	}
	if tm.HealthyContactWindow() <= DefaultHealthyContactWindow {
		t.Fatal("raising the heartbeat did not widen the freshness window")
	}
}

// The two relationships are protocol requirements, and a cluster must refuse to
// start on a set that violates one rather than let hashicorp/raft reject it
// after the node is half built.
func TestTimingsResolveRejectsBadRelationships(t *testing.T) {
	cases := []struct {
		name string
		tm   Timings
	}{
		{"lease longer than heartbeat", Timings{Heartbeat: 100 * time.Millisecond, LeaderLease: 500 * time.Millisecond, Election: time.Second}},
		{"election shorter than heartbeat", Timings{Heartbeat: 800 * time.Millisecond, Election: 100 * time.Millisecond, LeaderLease: 50 * time.Millisecond}},
		{"heartbeat below the floor", Timings{Heartbeat: time.Millisecond}},
		{"heartbeat above the ceiling", Timings{Heartbeat: 10 * time.Minute, Election: 10 * time.Minute}},
		{"sub-millisecond heartbeat is refused, not rounded to the default", Timings{Heartbeat: 500 * time.Microsecond}},
		// A negative interval is not "unconfigured": left to pass, it would
		// reach raft.NewRaft and be rejected there, after the node is half
		// built and with Raft's error rather than ours.
		{"negative heartbeat", Timings{Heartbeat: -time.Second}},
		{"negative commit timeout", Timings{CommitTimeout: -time.Millisecond}},
		// Truncating a fractional millisecond to range-check it is how a set
		// this package accepts becomes one raft.ValidateConfig rejects: both
		// of these truncate to 250 and compare equal, while Raft sees an
		// election shorter than its heartbeat.
		{"fractional heartbeat and election that truncate equal", Timings{
			Heartbeat: 250*time.Millisecond + 900*time.Microsecond,
			Election:  250*time.Millisecond + 500*time.Microsecond,
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := tc.tm.Resolve(); err == nil {
				t.Fatal("accepted an invalid interval set")
			} else if !nerr.HasCode(err, nerr.InvalidArgument) {
				t.Fatalf("want invalid-argument, got %v", err)
			}
		})
	}
}

// Open applies the resolved intervals and refuses an invalid set outright,
// rather than starting a node that cannot hold an election.
func TestOpenAppliesAndValidatesTimings(t *testing.T) {
	keys := testKeys(t)
	_, tr := raft.NewInmemTransport("")
	_, err := Open(Config{
		NodeID: "n1", Keys: keys, AllowMinority: true, Inmem: true, Transport: tr,
		Timings: Timings{Heartbeat: 100 * time.Millisecond, LeaderLease: time.Second},
	}, &recordApplier{})
	if err == nil {
		t.Fatal("Open accepted a lease longer than the heartbeat")
	} else if !nerr.HasCode(err, nerr.InvalidArgument) {
		t.Fatalf("want invalid-argument, got %v", err)
	}

	_, tr2 := raft.NewInmemTransport("")
	cl, err := Open(Config{
		NodeID: "n1", Keys: keys, AllowMinority: true, Inmem: true, Transport: tr2,
		Timings: Timings{Heartbeat: 400 * time.Millisecond, Election: 900 * time.Millisecond},
	}, &recordApplier{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cl.Shutdown() })
	got := cl.Timings()
	if got.Heartbeat != 400*time.Millisecond || got.Election != 900*time.Millisecond {
		t.Fatalf("configured intervals not applied: %+v", got)
	}
	if got.LeaderLease != DefaultLeaderLease || got.CommitTimeout != DefaultCommitTimeout {
		t.Fatalf("unset intervals not defaulted: %+v", got)
	}
	if want := 2 * time.Second; cl.HealthyContactWindow() != want {
		t.Fatalf("cluster window = %v, want %v", cl.HealthyContactWindow(), want)
	}
	if cl.DefaultMaxStaleness() != cl.HealthyContactWindow() {
		t.Fatal("default bounded-read staleness diverged from the health window")
	}
}

// The four intervals must land on the four Raft fields they name, and the
// result must be a configuration Raft itself accepts. Resolve enforces the same
// two relationships hashicorp/raft does, so anything Resolve passes must also
// pass raft.ValidateConfig — if that ever stops being true, a cluster fails at
// Open with a Raft error instead of at configuration time with ours.
func TestTimingsApplyToRaftConfig(t *testing.T) {
	tm, err := (Timings{
		Heartbeat:     700 * time.Millisecond,
		Election:      1500 * time.Millisecond,
		LeaderLease:   300 * time.Millisecond,
		CommitTimeout: 40 * time.Millisecond,
	}).Resolve()
	if err != nil {
		t.Fatal(err)
	}
	rc := raft.DefaultConfig()
	rc.LocalID = "n1"
	tm.applyTo(rc)
	if rc.HeartbeatTimeout != tm.Heartbeat {
		t.Fatalf("HeartbeatTimeout = %v, want %v", rc.HeartbeatTimeout, tm.Heartbeat)
	}
	if rc.ElectionTimeout != tm.Election {
		t.Fatalf("ElectionTimeout = %v, want %v", rc.ElectionTimeout, tm.Election)
	}
	if rc.LeaderLeaseTimeout != tm.LeaderLease {
		t.Fatalf("LeaderLeaseTimeout = %v, want %v", rc.LeaderLeaseTimeout, tm.LeaderLease)
	}
	if rc.CommitTimeout != tm.CommitTimeout {
		t.Fatalf("CommitTimeout = %v, want %v", rc.CommitTimeout, tm.CommitTimeout)
	}
	if err := raft.ValidateConfig(rc); err != nil {
		t.Fatalf("Resolve accepted intervals raft rejects: %v", err)
	}
}

// Every interval set Resolve accepts across the whole configurable range must
// also be one Raft accepts. This is the contract that lets an operator learn
// about a bad combination at configuration time instead of at the next restart.
func TestResolvedTimingsAlwaysSatisfyRaft(t *testing.T) {
	ms := []time.Duration{10, 11, 50, 200, 250, 999, 1000, 30000, 60000}
	rc := raft.DefaultConfig()
	rc.LocalID = "n1"
	accepted := 0
	for _, hb := range ms {
		for _, el := range ms {
			for _, ll := range ms {
				tm, err := (Timings{
					Heartbeat:   hb * time.Millisecond,
					Election:    el * time.Millisecond,
					LeaderLease: ll * time.Millisecond,
				}).Resolve()
				if err != nil {
					continue
				}
				accepted++
				tm.applyTo(rc)
				if err := raft.ValidateConfig(rc); err != nil {
					t.Fatalf("Resolve accepted hb=%v el=%v ll=%v but raft rejects it: %v", hb, el, ll, err)
				}
			}
		}
	}
	if accepted == 0 {
		t.Fatal("no interval set was accepted; the sweep proves nothing")
	}
}

// A nil cluster answers with the defaults rather than a zero window, which
// would make every follower read look infinitely stale.
func TestNilClusterTimings(t *testing.T) {
	var c *Cluster
	if c.Timings() != DefaultTimings() {
		t.Fatalf("nil cluster timings = %+v", c.Timings())
	}
	if c.HealthyContactWindow() != DefaultHealthyContactWindow {
		t.Fatalf("nil cluster window = %v", c.HealthyContactWindow())
	}
}
